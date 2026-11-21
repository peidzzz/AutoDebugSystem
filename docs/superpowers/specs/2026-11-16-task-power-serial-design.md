# 任务电源串口、日志与暂停决策 —— 设计文档

- 日期：2026-11-16
- 状态：已批准（用户 m01252 确认，并把「结束本次」改名为「结束任务」）
- 范围：SerialMateX（Wails + Go + Vue3）

## 1. 要解决的问题

1. **设备参数**：`pwr_dev.ini` 里的设备只有名称、端口、上电指令、掉电指令四项；电源实际走的是 SERIAL 那条连接，设备的端口从来没被打开过。需要每台设备自己的端口参数（波特率/数据位/停止位/校验位），以及上电/掉电各自的「是否 HEX」「是否回车」。
2. **日志**：勾了保存日志后，日志里只有被测板发回来的字节，任务发出去的命令一条都没有；RX MONITOR 也没法区分「任务发的」和「板子回的」。
3. **异常暂停**：选了这个开关，本轮命令没在倒计时内跑完时，现在的行为是无限期暂停，且暂停中点停止会卡死。
4. **Plot**：还没实现，先置灰不可选。
5. **AUTO CMD 编辑框**：缺「是否 HEX」「是否回车」；行尾数字的文案要改成「延时」。

## 2. 现状（读代码得到的事实）

- 电源命令发在 SERIAL 上：`task.go:437`（上电）与 `task.go:477`（掉电）调 `sendBytes()` → `taskSend` → `a.SendData()`（`app.go:500`）。`device.Port` 只读写，从不打开。
- 日志只写接收原文：`task.feed`（`task.go:304`）里 `appendFile(logPath, string(data))`，发出去的命令不记。
- RX 面板是只读 `textarea`（`App.vue:2014`），整块一个字符串，无法按行高亮。
- 暂停等待是裸 `<-t.resume`（`task.go:472`），暂停时点停止，goroutine 永久卡在接收上。
- `buildTxPayload`（`autocmd.go:259`）固定「末尾没有 CR/LF 就补 `\r`」，没法关，也没法按 HEX 发。
- `TaskConfig{SaveLog, SavePower, InitialState, AbnormalPause, WaitSeconds, DelaySeconds, RunCount}` 已够用，不改。

## 3. 设计

### 3.1 双串口

- 新增 `App.pwrPort *serial.Port`（不参与 `isConnected` / `connType`，与 SERIAL 完全独立）。
- `StartTask` 阶段校验并打开：设备 `Port` 为空 → 拒绝；与当前 SERIAL 口相同 → 拒绝；打开失败 → 拒绝启动并返回底层错误。**打不开就不让任务跑**：一台没断电的板子继续「测试」比拒绝启动更危险。
- 电源口只写不读（不启读循环），任务结束（正常完成 / 手动停止 / 出错）统一 `defer` 关闭。
- 抽一个 `buildSerialMode(baud, dataBits, stopBits int, parity string) *serial.Mode`，给 `OpenSerial` 与电源口共用，避免两份映射表漂移。

### 3.2 数据模型

```go
type PwrDevice struct {
    Name, Port string
    Baud, DataBits, StopBits int   // 默认 9600 / 8 / 1
    Parity string                  // 默认 "None"
    PowerOn, PowerOff string
    PowerOnHex, PowerOnEnter, PowerOffHex, PowerOffEnter bool
}
```

```go
type AutoCmd struct {
    Type string   // TX / RX
    Number int    // 字段名不变，界面文案改为「延时」
    Text string
    Hex bool      // 默认 false；只对 TX 有意义
    Enter bool    // 默认 true（等于今天「自动补 \r」的行为）；只对 TX 有意义
}
```

`Hex` / `Enter` **只作用于 TX 行**。RX 行是文本包含匹配（`strings.Contains`），没有「编码」可谈；编辑器里 RX 类型下这两个开关置灰，写文件时 RX 行不写这两个关键字（解析时遇到也忽略）。

缺省值在**解析时**填好，这样老文件、前端新建对象、测试三处行为一致：

- `pwr_dev.ini` 缺 `BAUD/DATA/STOP/PARITY` → `9600/8/1/None`；缺 `PWR-ON-HEX` / `PWR-OFF-HEX` → **true**（今天电源命令恒按十六进制解析）；缺 `PWR-ON-ENTER` / `PWR-OFF-ENTER` → **false**（今天不追加回车）。
- `auto_cmd.txt` 行缺关键字 → `hex=0 enter=1`（等于今天的 `buildTxPayload`）。

布尔文本接受 `1/0`、`true/false`、`on/off`、`是/否`，大小写不敏感。

### 3.3 文件格式（保持人可读、老文件可用）

`pwr_dev.ini` 每段追加四个串口键与四个开关：

```ini
[DemoBoard]
Name=DemoBoard
Port=COM7
BAUD=9600
DATA=8
STOP=1
PARITY=None
PWR-ON=0x01
PWR-ON-HEX=1
PWR-ON-ENTER=0
PWR-OFF=0x00
PWR-OFF-HEX=1
PWR-OFF-ENTER=0
```

`auto_cmd.txt` 行尾追加可选关键字，只写在 TX 行，并且**只在非默认时写**（`hex=1`、`enter=0`），这样普通命令文件仍然干净：

```txt
TX AT+RST 00
TX 01 02 03 00 hex=1
RX ready 05
```

解析顺序：先削掉行尾所有 `key=value`（只有 `hex` / `enter` 认），再按今天的规则取「类型 + 文本 + 延时」。这样 `TX AT+RST 00` 这种老行、以及文本里本身带 `=` 的命令都不受影响。

停止位在 ini 里按人读法写 `1` / `1.5` / `2`，解析时兼容 `15`（内部值）并统一归一到 `1 / 15 / 2`（与 `app.go` 的 `case 15 → OnePointFiveStopBits` 对齐）。写回文件时 `15` 输出成 `1.5`。

### 3.4 发送载荷

`buildTxPayload` 拆成两层：

- `encodeCmdPayload(text string, asHex, withEnter bool) ([]byte, error)`：
  - `asHex` → `parseHexBytes(text)`，非法时返回错误（命令编辑弹窗里当场提示）；
  - 否则 → `decodeCmdText(text)` 处理功能键标记；
  - `withEnter` 且末尾不是 `\r`/`\n` → 补 `\r`。
- 旧的 `buildTxPayload(text)` 保留为 `encodeCmdPayload(text, false, true)`，老测试不动。

电源命令走同一个编码函数（按设备自己的两个开关），写 `pwrPort`。

### 3.5 任务引擎与暂停决策

`TaskRuntime` 增加 `PausedReason string`（`"abnormal"`，供界面显示），暂停时**电源保持 ON**（今天已是如此，明确写进契约）。

暂停时右下角的运行按钮一分为二，两个前端方法替代单一的 `ResumeTask`：

- `EndTask() string` —— 界面文案「结束任务」：**掉电并结束整个任务**（`NormalCount` 不增加，`Message="已手动结束本次测试"`，状态回 IDLE）。它就是暂停态下「停止任务」的替身，所以暂停时不再需要单独的停止按钮。
- `SkipRound() string` —— 界面文案「跳过本轮」：**不计正常次数**，掉电 → 间隔倒计时 → 下一轮；若已经是最后一轮，则按任务完成收尾。

实现要点：

- `taskEngine.decision chan string`（容量 1）取代 `resume`。
- 暂停等待不能是裸的 `<-t.decision`，否则暂停中点停止会永久卡死 goroutine。改成：

  ```go
  var action string
  select {
  case action = <-t.decision:
  case <-t.stopCh:
      action = "end"      // 暂停中被停止：等同于结束任务
  }
  ```

- `StopTask` 在暂停态下与 `EndTask` 走同一条收尾路径（掉电、关闭电源口、写日志、回 IDLE），两条路径共用一个 `finishRound(powerOff bool)` 收尾函数，避免两套状态机。
- `SkipRound` 之后本轮的 `NormalCount++` 不执行，其余循环逻辑（掉电、间隔、轮次推进）不变。

### 3.6 日志

- 每条 TX 命令发出前写一行：`[TX 15:04:05] AT+RST`（写 `cmd.Text`，不写展开后的控制字节）。
- RX 是任意字节流，可能停在一行中间。所以 TX 行**前后各保证一个换行**：如果当前日志结尾不是 `\n`，先补一个 `\n`，写完 TX 行再补一个 `\n`。这样日志永远是「RX 原文 + 独占一行的 TX 记录」，不会出现 `ab[TX 10:00:01] AT+RSTcd`。
- 电源命令不写 TX 行，沿用已有的 `* 第 N 次上电 *` 横幅。
- 写日志仍然是「打开 → 追加 → 关闭」，不持有句柄（满足「记录过程中不占用该文件」）。

### 3.7 RX MONITOR 改成行视图

只有换成 DOM 行视图才能「高亮」。**这是本设计里唯一会改动现有交互实现的点**：

- 数据结构：`transcript: { dir: 'rx' | 'tx'; text: string; time?: string }[]`。RX 数据按 `\n` 累积进最后一行（`applyTerminalChars` 逻辑保留），TX 事件插入前先另起一行。
- 渲染：`<div class="rx-log" tabindex="0" ref="logWindowRef">` + `v-for` 行；`.rx-line.is-tx` 绿色、行首 `▸`、带时间；RX 行用默认正文色。
- **保留的交互**（重要，不能退化）：点击接收区直接键入发送、Enter 单回车、↑/↓ 历史、Backspace 发 `0x7F`、Ctrl+C 中断、粘贴直接下发、自动滚屏、清空按钮、Hex 显示、缓冲区上限 `MAX_BUFFER_SIZE`。
  - `keydown` / `paste` 从 textarea 移到这个 div 上（`tabindex="0"`）。
  - Ctrl+C 的「有选中就复制」判定从 `el.selectionStart` 改为 `window.getSelection()`。
  - **Hex 显示**：文本转不回原始字节（非法 UTF-8、退格已被 `applyTerminalChars` 吃掉），所以 `showHex` 打开时**整块切换成今天的字节 dump 视图**（`formatData(rawDataBuffer, true)`，不分行、不掺 TX 行）；关掉再回到行视图。这是唯一「退化」的地方，但保住了 Hex 的正确性。
  - 复制：选中即复制（浏览器原生），不额外拦截。
- 裁剪：`transcript` 超过 `MAX_LINES`（2000 行）时从头部丢弃；`rawDataBuffer` 的 `MAX_BUFFER_SIZE` 语义不变。
- 新增 Wails 事件 `task-tx`，载荷 `{ text: string, time: string }`，只在任务发 AUTO CMD 时发。手动发送（TX EDITOR / 接收区键入）不产生 TX 行，RX MONITOR 只标「任务发出的」。

### 3.8 界面

- **设备弹窗**：每台设备从 2×2 到 8 项 —— `名称/端口`、`波特率/数据位`、`停止位/校验位`、`上电(+HEX+回车)`、`掉电(+HEX+回车)`；`HEX` 勾上时输入框旁给出「空格分隔的十六进制」提示，失焦校验非法字节。弹窗加 `max-height` 内部滚动，避免 3 台设备时超出 820 高的窗口。
- **AUTO CMD 编辑弹窗**：加 `HEX` `回车` 两个开关（复用 `.switch`），「数字」标签改「延时」，列出 HEX 勾选后的实时预览字节；类型切到 RX 时两个开关置灰。
- **暂停态按钮**：占的是右下角 `run-foot` 的位置（原本是「启动/停止」），一分为二：`结束任务`（次级样式）与 `跳过本轮`（主色）。暂停结束、任务回到运行态后恢复成单个「停止任务」。左侧 TASK STATUS 里原来的「继续（解除异常暂停）」按钮删掉，只保留一行「已暂停 · 有命令未执行完」的说明。
- **Plot**：按钮 `disabled` + `title="暂未开放"`，去掉 `showChart` 的切换（图表代码先留着）。
- **SERIAL 波特率**：从 `<input list>` 改成下拉，典型值 `4800, 9600, 14400, 19200, 38400, 57600, 115200, 128000, 230400, 256000, 460800, 921600`；当前值不在列表里时（老配置）额外补一个选项显示它。

## 4. 错误处理

| 情况 | 行为 |
| --- | --- |
| 电源口为空 / 打不开 | 拒绝启动，`StartTask` 返回原因，界面弹出提示 |
| 电源口 == SERIAL 口 | 拒绝启动，提示两者不能相同 |
| 电源命令 HEX 勾选但内容非法 | 保存前校验并拦住；启动时再校验一次 |
| 任务中途写电源口失败 | 任务中止，`Message="电源口写入失败：…"`，状态回 IDLE |
| HEX 命令内容非法（AUTO CMD） | 编辑弹窗当场提示，不允许保存 |
| 暂停中点「停止任务」 | 与「结束任务」同一收尾路径，不会卡死 goroutine |
| 停止位写成 `1.5` 或 `15` | 都归一化成内部值 15，写回时输出 `1.5` |

## 5. 测试

- 单元（Go）：`pwrdev_test.go` 补新键解析/保存往返与缺省值（含 `STOP=1.5` 归一化）；`autocmd_test.go` 补 `hex=/enter=` 解析与格式化往返、老行兼容、RX 行不写关键字；`task_test.go` 用 `taskSendHook` + 假电源口验证：暂停 → `EndTask`（结束整个任务）/ `SkipRound`（不计正常数、进下一轮）的掉电与计数、暂停中 `StopTask` 能收尾、TX 日志行文本与前后换行、HEX/回车载荷。
- 手动验收（`npm run build` 后用 `.tools` 的 dist + CDP 截图流水线）：设备弹窗 8 项、AUTO CMD 开关、暂停两按钮、RX 行视图的 TX 绿行、Plot 置灰、波特率下拉。

## 6. 不做

- 不读电源口回执；不支持多台电源设备同时使用；Plot 功能不实现。
- 不改 SERIAL 的连接语义，不动 TASK 之外的三栏布局与皮肤体系。
