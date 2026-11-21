# 任务电源串口、日志与暂停决策 —— 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 让 TASK 的电源指令走设备自己的 COM 口并带串口参数，让日志与 RX MONITOR 能区分「任务发的」和「板子回的」，把异常暂停改成「结束任务 / 跳过本轮」两个明确出口，同时置灰 Plot、给串口与 AUTO CMD 补齐 HEX/回车开关。

**Architecture:** Go 侧新增一条只写不读的电源串口（`App.pwrPort`），复用抽出来的 `buildSerialMode`；发送载荷统一收口到 `encodeCmdPayload`，`buildTxPayload` 变成它的一层薄包装；任务引擎把裸 `<-t.resume` 换成 `select` + `decision`，并新增 `EndTask` / `SkipRound` 两个出口；前端把 RX 面板从单个只读 textarea 换成按行渲染的 transcript（`showHex` 时退回今天的字节 dump），其余三处是弹窗与按钮的增量改造。

**Tech Stack:** Go（`go.bug.st/serial`）、Wails v2 绑定、Vue 3 + TypeScript + Vite、Tailwind v4（本项目实际用 scoped CSS）。

**Spec:** `docs/superpowers/specs/2026-11-16-task-power-serial-design.md`（已批准）

## Global Constraints

- `SerialMateX/` 是 Go module 根，测试命令 `cd SerialMateX && go test ./...`；前端命令 `cd SerialMateX/frontend && npm run build`。
- **不引入新依赖**：不用第三方 ini 解析库，不用前端 UI 库。
- **老配置文件必须继续能读**：`pwr_dev.ini` 缺串口键 → `9600/8/1/None`；缺 `PWR-*-HEX` → `true`；缺 `PWR-*-ENTER` → `false`；`auto_cmd.txt` 缺关键字 → `hex=0 enter=1`。这些必须由解析层补齐，而不是由调用方补。
- **不改 SERIAL 的连接语义**：`isConnected` / `connType` / `connType==TypeSerial` 的判定一处都不动；电源口完全平行。
- **不动 TASK 之外的布局与皮肤体系**（Bench token、6 套 skin、`--card/--field/--line` 等）。
- 前端中文文案与现有文件一致；`App.vue` 用 `edit` 工具做局部修改，不要整文件重写（3622 行，重写必丢东西）。
- 每个 Task 结束提交一次，消息格式 `feat(task): …` / `fix(task): …` / `test(task): …`，中文正文。
- 构建前端前必须 `$env:TEMP=$env:TMP="<ws>\.tools\tmp"`，否则 esbuild 报 `remove ... Access is denied`。
- 验收截图用既有流水线：`node .tools\serve-dist.js <ws>\SerialMateX\frontend\dist 5199` + `.tools\cdp-shot.js`（Wails 桩已注入），`CDP_EVAL` 可切 TASK / 开弹窗。

## Review Focus

1. **暂停中的停止**：`select { case action = <-t.decision: case <-t.stopCh: action = "end" }` —— 任何情况下都不能出现永久阻塞的 goroutine 接收。
2. **缺省值只写在解析层**：`withDefaults` / `splitAutoCmdKeywords` 是唯一入口，别在 `StartTask` 或 Vue 里再补一遍默认值，否则三处会漂移。
3. **日志换行夹三明治**：写 TX 行前后各保证一个 `\n`，不允许出现 `ab[TX 10:00:01] AT+RSTcd`。
4. **HEX 编码失败必须报错**：`encodeCmdPayload(text, true, …)` 对非法内容返回 error，不允许静默发空字节。
5. **RX 行不写也不认 `hex=/enter=`**：文件里 RX 行带关键字时解析要忽略，保存时不要写。
6. **停止位归一化**：`1` / `1.5` / `15` / `2` 四种写法都要落到内部值 `1 / 15 / 2`，写回输出 `1.5`。
7. **电源口 == SERIAL 口要拒绝**，并且错误信息里带两个端口名。

## Task Graph

```
T1 pwrdev 模型 ─┐
T2 autocmd 载荷 ─┼→ T3 电源口 ─→ T4 暂停决策/日志 ─┐
                │                                  ├→ T10 验收
T5 RX 行视图 ───┴──────────────────────────────────┤
T6 设备弹窗 → T7 AUTO CMD 弹窗 → T8 暂停按钮 → T9 Plot/波特率 ┘
```

T1/T2/T5 可并行；T3 依赖 T1+T2；T4 依赖 T3；前端 T6–T9 互不依赖但都改 `App.vue`，串行做以免冲突。

---

### Task 1: pwrdev 串口参数与开关（Go）

**Files:**
- Modify: `SerialMateX/pwrdev.go`
- Test: `SerialMateX/pwrdev_test.go`

**Interfaces:**
```go
const (
    defaultPwrBaud     = 9600
    defaultPwrDataBits = 8
    defaultPwrStopBits = 1      // 内部值：1 / 15 / 2
    defaultPwrParity   = "None"
)

type PwrDevice struct {
    Name         string `json:"name"`
    Port         string `json:"port"`
    Baud         int    `json:"baud"`
    DataBits     int    `json:"dataBits"`
    StopBits     int    `json:"stopBits"`   // 1 / 15 / 2
    Parity       string `json:"parity"`
    PowerOn      string `json:"powerOn"`
    PowerOff     string `json:"powerOff"`
    PowerOnHex   bool   `json:"powerOnHex"`
    PowerOnEnter bool   `json:"powerOnEnter"`
    PowerOffHex  bool   `json:"powerOffHex"`
    PowerOffEnter bool  `json:"powerOffEnter"`
}

func normalizeStopBits(v int) int          // 1|15|2 原样，其余 → 1
func parseStopBitsText(s string) int       // "1"/"1.5"/"15"/"2"/"" → 1|15|2|1|1
func formatStopBitsText(v int) string      // 1→"1"，15→"1.5"，2→"2"
func parseBoolText(s string) (bool, bool)  // 1/0 true/false on/off 是/否，第二返回 ok
func (d PwrDevice) withDefaults() PwrDevice
```

- [ ] 在 `pwrdev_test.go` 写失败测试：
  - 老段（只有 `Name/Port/PWR-ON/PWR-OFF`）解析后 `Baud=9600, DataBits=8, StopBits=1, Parity="None", PowerOnHex=true, PowerOnEnter=false, PowerOffHex=true, PowerOffEnter=false`。
  - 新段（含 `BAUD=115200 DATA=7 STOP=1.5 PARITY=Odd PWR-ON-HEX=0 PWR-ON-ENTER=1 PWR-OFF-HEX=1 PWR-OFF-ENTER=0`）解析后 `StopBits=15`。
  - `STOP=15` 与 `STOP=1.5` 得到相同内部值。
  - 保存再解析，两个开关与四个数值原样回来，且文件里停止位写成 `1.5`。
  - `parseBoolText` 覆盖 `1 0 true false on off 是 否` 与非法输入。
- [ ] 跑 `go test ./... -run TestPwrDev`，确认新测试失败（字段不存在 / 值不对）。
- [ ] 实现：`PwrDevice` 扩字段；`parsePwrDevFile` 增加键归一化匹配 `BAUD|波特率`、`DATA|DATABITS|数据位`、`STOP|STOPBITS|停止位`、`PARITY|校验位`、`PWRONHEX|PWRONHEXBIT|上电HEX`、`PWRONENTER|上电回车`、`PWROFFHEX`、`PWROFFENTER`；所有设备在 `parsePwrDevFile` 返回前过一遍 `withDefaults()`。
- [ ] `SavePwrDevices` 每段固定写 `Name/Port/BAUD/DATA/STOP/PARITY/PWR-ON/PWR-ON-HEX/PWR-ON-ENTER/PWR-OFF/PWR-OFF-HEX/PWR-OFF-ENTER`，`STOP` 用 `formatStopBitsText`，布尔写 `1`/`0`。
- [ ] `pwrDevTemplate` 同步更新成带全键的示例（注释说明缺键默认）。
- [ ] `go test ./...` 全绿；`gofmt -l .` 无输出。
- [ ] Commit `feat(task): 电源设备支持独立串口参数与 HEX/回车开关`。

---

### Task 2: AUTO CMD 的 HEX/回车与统一载荷编码（Go）

**Files:**
- Modify: `SerialMateX/autocmd.go`, `SerialMateX/task.go`（`buildTxPayload` 调用点）
- Test: `SerialMateX/autocmd_test.go`

**Interfaces:**
```go
type AutoCmd struct {
    Type   string `json:"type"`
    Number int    `json:"number"`
    Text   string `json:"text"`
    Hex    bool   `json:"hex"`
    Enter  bool   `json:"enter"`
}

// 从行尾削掉 hex=/enter=，返回清理后的文本与两个已带缺省的值
func splitAutoCmdKeywords(rest string) (body string, asHex bool, withEnter bool)

// 唯一的载荷编码入口
func encodeCmdPayload(text string, asHex, withEnter bool) ([]byte, error)

// 兼容包装：老调用与老测试不动
func buildTxPayload(text string) string   // string(encodeCmdPayload(text,false,true)) 且 err == nil
```

**规则（必须按此顺序实现）：**
1. 先按空白切词，**从行尾往前**吞掉匹配 `^(hex|enter)=(0|1|true|false|on|off|是|否)$` 的词（大小写不敏感），记录下来；遇到第一个不匹配的词就停。
2. 剩下的部分交给今天的 `mkat` 逻辑取「类型 + 文本 + 行尾延时数字」。
3. 缺省：`asHex=false`、`withEnter=true`；只写了 `hex=1` 时 `withEnter` 仍是 `true`。
4. `formatAutoCmds` 只在非默认时写关键字：`Hex` 为真写 ` hex=1`，`Enter` 为假写 ` enter=0`；RX 行一个都不写。
5. `encodeCmdPayload`：`asHex` → `parseHexBytes(text)`（错误原样返回）；否则 `decodeCmdText(text)` 再去尾部空格/制表；`withEnter` 且末字节不是 `\r`/`\n` → 追加 `\r`。

- [ ] 写失败测试：
  - `parseAutoCmdsText("TX AT+RST 00\n")` → `Hex=false, Enter=true, Text="AT+RST", Number=0`。
  - `TX 01 02 03 00 hex=1` → `Hex=true, Enter=true, Text="01 02 03"`, `Number=0`。
  - `TX hello 05 enter=0` → `Enter=false, Number=5, Text="hello"`。
  - `TX AT+HEX=1 00`（文本里含 `=` 但不是关键字）→ `Text="AT+HEX=1"`, `Hex=false`。
  - `RX ready 05 hex=1 enter=0` → `Hex=false, Enter=true`（RX 忽略关键字）。
  - 格式化往返：`Hex/Enter` 四种组合经 `formatAutoCmds` → `parseAutoCmdsText` 后值不变，且只有非默认才出现关键字。
  - `encodeCmdPayload("AA BB", true, false)` → `[]byte{0xAA, 0xBB}`；`encodeCmdPayload("ZZ", true, true)` → error；`encodeCmdPayload("AT", false, true)` → `"AT\r"`；`encodeCmdPayload("AT\n", false, true)` 不再加 `\r`。
- [ ] 跑 `go test ./... -run TestAutoCmd` 确认失败。
- [ ] 实现上述接口，`runCommands` 的 TX 分支改为 `payload, err := encodeCmdPayload(cmd.Text, cmd.Hex, cmd.Enter)`，err 非空时按失败处理（`Message` 带命令序号）。
- [ ] `go test ./...` 全绿。
- [ ] Commit `feat(task): AUTO CMD 支持 HEX/回车与统一的载荷编码`。

---

### Task 3: 电源串口（Go）

**Files:**
- Modify: `SerialMateX/app.go`（抽 `buildSerialMode`、加 `pwrPort` 字段）
- Create: `SerialMateX/pwrport.go`
- Modify: `SerialMateX/task.go`（`StartTask` 校验与打开、`runTask` defer 关闭、`sendBytes` 改走电源口）
- Test: `SerialMateX/task_test.go`

**Interfaces:**
```go
// app.go
func buildSerialMode(baudRate, dataBits, stopBits int, parityName string) *serial.Mode
// OpenSerial 内部改为 buildSerialMode(...)，行为与今天完全一致

// pwrport.go
var pwrOpenHook func(PwrDevice) (io.WriteCloser, error)   // 测试注入
var pwrWriteHook func([]byte) error                       // 测试注入
func (a *App) openPwrDevice(dev PwrDevice) error
func (a *App) closePwrDevice()
func (a *App) writePwrPort(b []byte) error                // 未打开返回错误，不 panic
```

**`StartTask` 新增校验（顺序固定）：**
1. 现有判定（running / 连接 / 命令非空 / 找设备）。
2. `dev.Port` 为空 → 返回 `"设备 <name> 没有配置 COM 端口"`。
3. `strings.EqualFold(dev.Port, a.currentPort)` → 返回 `"电源端口 <port> 不能与 SERIAL 端口相同"`（`a.currentPort` 若不存在，就在 `OpenSerial` 成功时记下端口名，新增 `App.serialPortName string`）。
4. `encodeCmdPayload(dev.PowerOn, dev.PowerOnHex, dev.PowerOnEnter)` 与 `PowerOff` 各试编码一次，失败即返回错误。
5. `a.openPwrDevice(dev)` 失败 → 返回 `"打开电源端口 <port> 失败：<err>"`。

**`runTask` 生命周期：** `defer a.closePwrDevice()` 放在函数最外层；任务开始前打开、结束时无论什么路径都关闭。

**`sendBytes` 改成：** `payload, err := encodeCmdPayload(text, dev.PowerOnHex/PowerOffHex, dev.PowerOnEnter/PowerOffEnter)`；`a.writePwrPort(payload)` 失败 → 任务中止（`Message="电源口写入失败：…"`）。

- [ ] 写失败测试（`pwrOpenHook` 返回 `io.NopCloser` 包装的 `bytes.Buffer`，`pwrWriteHook` 记录字节）：
  - 电源口为空 → `StartTask` 返回非空消息且不启动。
  - 电源口与 SERIAL 口相同 → 返回消息里同时含两个端口名。
  - `openPwrHook` 返回 error → 返回消息含 `打开电源端口`。
  - 正常一轮：`pwrWriteHook` 依次收到上电字节与掉电字节（用 `PWR-ON=0x01 PWR-OFF=0x00` 与 `PWR-ON-ENTER=1` 各测一次）。
  - 任务结束后 `closePwrDevice` 被调用过（hook 里记 `closed bool`）。
- [ ] 跑测试确认失败。
- [ ] 实现；`go test ./...` 全绿。
- [ ] Commit `feat(task): 电源指令改走设备自己的串口`。

---

### Task 4: 暂停决策、TX 日志与 task-tx 事件（Go）

**Files:**
- Modify: `SerialMateX/task.go`, `SerialMateX/app.go`
- Test: `SerialMateX/task_test.go`

**Interfaces:**
```go
// TaskRuntime 增字段
PausedReason string `json:"pausedReason"`   // "abnormal" 或 ""

// taskEngine 增字段
decision chan string   // 取代 resume；容量 1

// 新导出方法（Wails 绑定）
func (a *App) EndTask() string     // 暂停中：掉电 + 结束整个任务
func (a *App) SkipRound() string   // 暂停中：不计正常次数 + 掉电 + 进下一轮
func (a *App) StopTask() string    // 语义不变；暂停态下等价 EndTask

// 内部
func (t *taskEngine) appendTxLog(logPath, text string)  // [TX 15:04:05] <text>，前后换行夹住
func (a *App) emitTaskTx(text string)                   // runtime.EventsEmit(a.ctx, "task-tx", map[string]string{"text": text, "time": "15:04:05"})
```

**暂停段实现（照抄这段结构，别自己发明）：**
```go
if !complete && cfg.AbnormalPause {
    t.set(func(rt *TaskRuntime) { rt.Paused = true; rt.Phase = "paused"; rt.PausedReason = "abnormal" })
    var action string
    select {
    case action = <-t.decision:
    case <-t.stopCh:
        action = "end"
    }
    t.set(func(rt *TaskRuntime) { rt.Paused = false; rt.PausedReason = "" })
    if action == "end" {
        // 掉电 → finishRound → return（不写「第 N 次测试停止」横幅以外的任何东西）
    }
    // action == "skip" 落到原有掉电/间隔流程，但跳过 NormalCount++
}
```

`StopTask` 在暂停态下要走同一条收尾路径：向 `decision` 投递 `"end"`（非阻塞，`select default`），不要只关 `stopCh` 就返回。

- [ ] 写失败测试：
  - `TestTaskPauseEndTask`：`AbnormalPause=1` 且命令含一条永不匹配的 RX → 进入 paused；调 `EndTask()` 后 `waitTaskDone` 返回 `NormalCount==0`、`Message` 含「已手动结束」、`pwrWriteHook` 收到掉电字节。
  - `TestTaskPauseSkipRound`：`RunCount=2`，第一轮暂停后 `SkipRound()` → 最终 `Round==2`、`NormalCount==1`（第二轮正常），掉电/上电字节顺序正确。
  - `TestTaskPauseStopDoesNotHang`：暂停中 `StopTask()`，10 秒内任务结束（今天会卡死）。
  - `TestTaskTxLogLine`：`SaveLog=1`，日志内容包含 `[TX ` 且 TX 行前后各有一个 `\n`（构造 RX 尾部无换行的场景验证）。
- [ ] 跑测试确认失败（尤其 `StopDoesNotHang` 超时）。
- [ ] 实现；`go test ./...` 全绿；`go vet ./...` 干净。
- [ ] Commit `feat(task): 异常暂停支持结束任务与跳过本轮，日志记录 TX 行`。

---

### Task 5: RX MONITOR 行视图（前端）

**Files:**
- Modify: `SerialMateX/frontend/src/App.vue`（script / template / style 三段）
- 参考现有：`receivedData`(610)、`applyTerminalChars`(618)、接收拼装(1192–1200)、`EventsOn("serial-data")`(1234)、`onRxKeydown`(1430)、`onRxPaste`(1492)、`clearReceive`(1544)、RX 面板(1990–2018)

**Interfaces（script 内）：**
```ts
interface LogLine { dir: 'rx' | 'tx'; text: string; time: string }
const transcript = ref<LogLine[]>([]);          // 行视图数据
const MAX_LOG_LINES = 2000;
const logWindowRef = ref<HTMLDivElement | null>(null);
const pushRxChunk = (chunk: string) => void;    // 按 \n 切行累加；末行不完整时留在最后一行
const pushTxLine = (text: string) => void;      // 另起一行，dir='tx'，time=当前 HH:MM:SS
const scrollLogToEnd = () => void;
```

**模板：**
```html
<div v-if="!showHex" class="rx-log custom-scrollbar" ref="logWindowRef" tabindex="0"
     @keydown="onRxKeydown" @paste="onRxPaste">
  <div v-for="(line, i) in transcript" :key="i" class="rx-line" :class="{ 'is-tx': line.dir === 'tx' }">
    <span v-if="line.dir === 'tx'" class="rx-tag">{{ line.time }}</span>{{ line.text }}
  </div>
</div>
<textarea v-else class="rx-text custom-scrollbar" readonly :value="receivedData"></textarea>
```

**规则：**
- **不要动现有 `receivedData` 管道**：1183–1195 的 `rawDataBuffer` 累积、`showHex` 分支、1195 的 `applyTerminalChars`、1571–1572 的 `watch(showHex)` 全部原样保留；行视图的 `transcript` 与它**并行**维护，互不替代。
- `showHex` 打开时整块换成今天的字节 dump（`receivedData` 在 1571 的 watch 里已经变成 `formatData(raw, true)`），关掉回行视图。
- `task-tx` 事件（Wails `EventsOn`）→ `pushTxLine`；`serial-data` → `pushRxChunk`；`clearReceive` 同时清空 `transcript`。
- `onRxKeydown` / `onRxPaste` 语义一条都不能丢：Enter 单回车、↑/↓ 历史、Backspace `0x7F`、Ctrl+C 有选中则复制（用 `window.getSelection()?.toString()` 判断）、可打印字符直发。
- 自动滚屏：`autoScroll` 为真时 `scrollLogToEnd()`（`nextTick` 后 `scrollTop = scrollHeight`）。
- 行数超 `MAX_LOG_LINES` 从头部裁剪。
- 样式：`.rx-log` 等宽 12px / 行高 1.72 / `padding: 12px 16px` / `overflow:auto`；`.rx-line.is-tx { color: var(--col-primary); font-weight: 600 }`；`.rx-tag { opacity:.55; margin-right:8px; font-size:10.5px }`；`.rx-line { white-space: pre-wrap; word-break: break-all }`。

- [ ] 改 script（数据结构 + 两条事件 + 三个函数，保留旧 `receivedData` 供 Hex 视图）。
- [ ] 改 template（行视图 + 条件渲染）。
- [ ] 改 style（`.rx-log` / `.rx-line` / `.rx-tag`）。
- [ ] `npm run build` 通过（记得 TEMP 环境变量）。
- [ ] 用 CDP 截图核对：注入 `serial-data` 与 `task-tx` 各若干条，确认 TX 行绿色带时间、RX 行连续、清空生效、自动滚屏到底。
- [ ] Commit `feat(rx): 接收区改为行视图并高亮任务发出的命令`。

---

### Task 6: 设备弹窗 8 项（前端）

**Files:**
- Modify: `SerialMateX/frontend/src/App.vue`（`interface PwrDevice` 97–102、`deviceDraft` 字面量 134/144/153、`saveDeviceEditor` ~165、设备弹窗 2167–2215、样式）

**Interfaces：**
```ts
interface PwrDevice {
  name: string; port: string;
  baud: number; dataBits: number; stopBits: number; parity: string;
  powerOn: string; powerOff: string;
  powerOnHex: boolean; powerOnEnter: boolean;
  powerOffHex: boolean; powerOffEnter: boolean;
}
const emptyDeviceDraft = (): PwrDevice => ({
  name: '', port: '', baud: 9600, dataBits: 8, stopBits: 1, parity: 'None',
  powerOn: '', powerOff: '',
  powerOnHex: true, powerOnEnter: false,
  powerOffHex: true, powerOffEnter: false
});
```
- 三处字面量（`openDeviceEditor` 空列表、`addDeviceDraft`、`removeDeviceDraft` 兜底）统一换成 `emptyDeviceDraft()`。
- 弹窗布局：`名称|端口`、`波特率|数据位`、`停止位|校验位`、`上电（HEX/回车两个 `.switch`）`、`掉电（同）`。
- 校验：`saveDeviceEditor` 里 `isHexTokenList` 只在对应 `*Hex` 为真时生效；`port` 不能与 SERIAL 口相同可留到启动时判定（前端不重复拦）。
- `.modal--devices` 加 `max-height: calc(100vh - 96px)` + `.modal-body { overflow: auto }`。

- [ ] 接口与三处字面量替换。
- [ ] 模板加 4 个字段 + 4 个开关（复用 `.switch`），HEX 钩上时输入框旁显示「空格分隔的十六进制」提示。
- [ ] 保存逻辑按开关决定是否校验十六进制。
- [ ] `npm run build` 通过。
- [ ] CDP 截图：TASK → 编辑设备参数，确认 8 项排布、开关状态、弹窗不溢出。
- [ ] Commit `feat(task): 设备参数支持串口参数与 HEX/回车开关`。

---

### Task 7: AUTO CMD 弹窗开关与「延时」（前端）

**Files:**
- Modify: `App.vue`：`AutoCmdModel`(375)、`autoCmdDialog`(376)、`loadAutoCmds`(385)、`persistAutoCmds`(401)、`openAutoCmdDialog`(409)、`confirmAutoCmdDialog`(~470)、弹窗模板 2238–2299、样式

**Interfaces：**
```ts
// 类型别名仍叫 AutoCmdType（'TX' | 'RX'），不要另起一个联合类型
interface AutoCmdModel { type: AutoCmdType; number: number; text: string; hex: boolean; enter: boolean }
const autoCmdDialog = reactive({
  show: false, index: -1, text: '', type: 'TX' as AutoCmdType,
  numberText: '00', numberError: '', hex: false, enter: true
});
const hexPreview = computed(() => { /* 文本 → "AA BB" 或 ''；非法时返回错误标记 */ });
```
- 读入时 `hex: !!c.hex`、`enter: c.enter !== false`（缺省 true）。
- 保存时 RX 类型强制 `hex/enter` 回落默认（`false/true`）。
- 模板：`.num-label` 文案「数字」→「延时」、`title` 同步改；`HEX`/`ENTER` 两个 `.switch` 放在类型切换旁边；类型为 RX 时 `disabled` 且样式置灰。
- HEX 勾选时显示 `.hex-preview`（等宽、`AA BB CC`）；内容非法时显示 `.err-line`「不是合法的十六进制」并禁用保存。
- 两段 `hint-line` 文案更新（新增 HEX/回车说明，仍保留功能键说明）。

- [ ] 模型与读写改造。
- [ ] 模板：两个开关 + 延时文案 + 预览。
- [ ] 保存校验（HEX 非法不许保存）。
- [ ] `npm run build` 通过。
- [ ] CDP 截图：命令弹窗 TX/RX 两态、HEX 预览、置灰效果。
- [ ] Commit `feat(task): AUTO CMD 编辑框支持 HEX 与回车，行尾数字改称延时`。

---

### Task 8: 暂停态的两个按钮（前端）

**Files:**
- Modify: `App.vue`：`taskRt` 类型（含 `pausedReason`）、`resumeTask`(602) → `endTask`/`skipRound`、TASK STATUS 面板 1940–1976、`run-foot` 2133–2164、样式

**Interfaces：**
```ts
const endTask = async () => { await EndTask(); };      // 调 wails 绑定，随后继续轮询
const skipRound = async () => { await SkipRound(); };
```
- 模板：`v-if="taskRt.paused"` 时 `run-foot` 内两个按钮 —— `结束任务`（`btn btn-warn`）+ `跳过本轮`（`btn btn-primary`）；否则沿用单按钮（`停止任务` / `启动任务`）。
- 删掉 TASK STATUS 里 `继续（解除异常暂停）`，替换成一行 `已暂停 · 有命令未执行完`（`.status-pill` 加 `.is-warn`）。
- 暂停时 `.run-ring--paused` 停止呼吸动画（静态描边）。
- 轮询继续跑（200ms），按钮随 `taskRt.paused` 切换。

- [ ] script：两个方法 + `wailsjs/go/main/App` 类型声明补充。
- [ ] template：两按钮 + 删除旧「继续」+ 暂停说明行。
- [ ] style：`.run-foot--paused`、`.status-pill.is-warn`。
- [ ] `npm run build` 通过。
- [ ] CDP 截图：注入 `paused` 快照确认两按钮与说明行。
- [ ] Commit `feat(task): 异常暂停时提供结束任务与跳过本轮`。

---

### Task 9: Plot 置灰与 SERIAL 波特率下拉（前端）

**Files:**
- Modify: `App.vue`：`baudOptions`(89)、波特率控件(1763)、RX 面板分段(1998–2002)

- [ ] `baudOptions` 改为 `[4800, 9600, 14400, 19200, 38400, 57600, 115200, 128000, 230400, 256000, 460800, 921600]`；当前值不在列表时补一项。
- [ ] 波特率 `<input list>` 换成既有 `.field-wrap` + `.field--btn` + `.menu` 下拉（与数据位/停止位同款），保留 `baudRate` 的 number 语义。
- [ ] Plot 按钮：`disabled`、`title="暂未开放"`、`@click` 移除（`showChart` 保留但不再被切换）；样式 `.seg button:disabled { opacity:.45; cursor:not-allowed }`。
- [ ] `npm run build` 通过。
- [ ] CDP 截图：SERIAL 面板波特率下拉展开、Plot 置灰。
- [ ] Commit `feat(ui): 波特率改为典型值下拉，Plot 暂未开放置灰`。

---

### Task 10: 全量验收

- [ ] `cd SerialMateX && go test ./...` 全绿；`go vet ./...` 无输出；`gofmt -l .` 无输出。
- [ ] `cd SerialMateX/frontend && $env:TEMP=$env:TMP="<ws>\.tools\tmp"; npm run build` 通过。
- [ ] 起 5199 静态服务，用 CDP 逐项截图核对：
  - 设备弹窗 8 项（3 台设备不溢出窗口）
  - AUTO CMD 弹窗 TX（HEX 开/关、延时标签）/ RX（开关置灰）
  - TASK 运行中暂停 → 两个按钮 → 「跳过本轮」后轮次推进、正常次数不增
  - RX 行视图：RX 行黑、TX 行绿带时间戳、清空、自动滚屏
  - Plot 置灰、波特率下拉
- [ ] 手上若有真机：实际跑一轮，确认电源口动作、日志里 `[TX …]` 行与 `* 第 N 次上电 *` 横幅、掉电正常。
- [ ] `wails build` 出桌面端（提醒用户：前端走 `go:embed`，不重新 build 看不到新界面）。
- [ ] 汇总一句给用户：改了哪些文件、测试结果、需要他手动确认的点。

---

## Execution

两种执行方式：

1. **Subagent-driven（推荐）** —— 每个 Task 派一个全新 subagent 实现，我在两轮之间做 code review，任务之间上下文干净，适合这种 10 个任务、跨 Go/前端两条线的计划。
2. **Native** —— 我在本会话里按 Task 顺序逐个做，上下文连贯、沟通成本低，但会占用较多上下文，且中途打断后恢复成本高。

我推荐 1：T1/T2 可并行，T3/T4 依赖链清晰，前端 T5–T9 串行但每个都很小，subagent 之间用 commit 作为交接面最稳。
