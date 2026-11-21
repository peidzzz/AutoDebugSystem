# PING 检测、任务总结与窗口底色跟随皮肤 — 设计文档

日期：2026-11-20
作者：lead（与用户 m02954–m02997 讨论后定稿）
状态：待用户批准

## 1. 意图与起因

用户在做长时间、无人值守的串口自动化测试。两件事促使这份设计：

1. **「正常次数」会比「总轮次」少 1，而且没有任何提示。** 用户以为是计数漏加。
2. **测试是否"真的正常"目前只看串口命令有没有跑完。** 用户在每轮结束后想再确认一次被测设备在网络侧还活着，并按结果决定这一轮算不算正常、要不要暂停。

查证结果（探针实验，临时测试已删）：计数没有漏。`SerialMateX/task.go:643` 只在 `complete && offErr == nil` 时 `NormalCount++`，而 `complete` 来自 `runCommands`，三种情况会返回 false：

- `SerialMateX/task.go:724` RX 命令在运行时间内没等到匹配数据；
- `SerialMateX/task.go:710`／`:769` 命令的延时把运行窗口吃满，后续命令没轮到；
- `SerialMateX/task.go:731`／`:736` 编码失败或发送失败。

`AbnormalPause` 关闭时，这三种情况既不暂停也不提示，直接进下一轮。探针实测：3 轮 RX 永不回包 → `Round=3, NormalCount=0, Message="任务完成"`；2 轮延时吃满窗口 → `Round=2, NormalCount=0, Message="任务完成"`。

所以这次不是"修计数"，而是**补可见性**：把"这轮为什么没算正常"写进日志，把 ping 的结果摆在 IP 栏上。

## 2. 目标

1. 每轮命令全部执行完后，若 PING 检测已开启且地址合法，用电脑网口对目标 IPv4 发 4 个 ICMP 包；丢包率 ≤ 50% 视为通过。
2. PING 不通过时：开了「异常暂停」→ 进暂停（沿用现有「结束任务 / 跳过本轮」）；没开 → 记为不正常、不增加正常次数、继续下一轮。
3. PING 期间 IP 栏高亮；开了异常暂停且 PING 失败时 IP 栏转红底，保持到下一次 PING 开始或任务结束。
4. IP 栏为空或格式不对时，点开关直接报错且打不开；任务运行中 IP 栏与开关都不可编辑。
5. 任务结束后在日志里追加一段总结：总测试轮数、命令执行、PING 检测各自的正常/异常/总数，加上「失败轮次明细」（第几轮、哪条命令失败、或哪一轮 PING 的丢包率多少），以及结束原因与结束时间。
6. 顺带：窗口底色跟随上次选择的皮肤，消除深色皮肤启动时反向闪浅底。

## 3. 非目标

- 不做 IPv6、不做域名/DNS 解析（只接受 IPv4 字面量）。
- 不做自定义包数、超时、成功率阈值（固定 4 包、单包 1 秒、丢包 ≤50% 通过）。
- 不把 PING 结果写进 CSV，不做历史统计、不做跨任务累计。
- 不新增「异常次数」计数器（用户在方案甲/乙/丙中选择了甲）。
- 不改 SEPARATE 的 SERIAL 连接语义：PING 走操作系统网络栈，与串口无关。

## 4. 反目标（即使实现了也不算成功）

- 每轮弹 UAC 或要求管理员权限。
- PING 期间点「停止」要等 ping 跑完才生效。
- 保存日志关闭时，总结与逐轮失败行根本不会产生，用户却以为记下了（继承现状；在本文档与交付说明里写明，界面只保留失败瞬间那 2 秒描边——用户选择甲）。
- IP 栏在任务运行中仍可编辑，或非法 IP 能打开开关并让任务带着坏配置启动。

## 5. 约束（来自现有代码）

- 配置读写都在 `SerialMateX/taskconfig.go`（`GetTaskConfig` / `SaveTaskConfig` / `taskCfgTemplate`，键名归一化后匹配，`=` 分隔，`[Task]` 段）。
- 任务主循环在 `SerialMateX/task.go:553-673`（`runTask`），每轮以下电、掉电、间隔的固定顺序走；暂停靠 `t.decision` channel（`task.go:611`），停止靠 `t.stopCh`。
- 日志写入必须走 `t.appendTaskLog` / `t.appendTxLog`（`task.go:403`、`:416`，`logMu` 保护、读末字节补齐换行）。新增行不能绕开这两个入口。
- `TaskRuntime` 是前后端唯一的状态契约（`task.go:29-46`，`GetTaskRuntime` 返回快照，前端 200ms 轮询）。
- 前端设置行结构：`.panel-sep` > `.setting-row` > `.setting-label` + 控件；开关已有 `.switch`（`App.vue:2244`），数值用 `.chip chip--num`。
- 技能/项目约束：中文文件只能用 Edit/Write 工具改，禁止 PowerShell 文本改写（会把中文双重编码并加 BOM）。
- Go 测试入口只能是 `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1`（裸 `go test` 会被 Defender 拦杀）。

## 6. 方案

### 6.1 PING 怎么发

| 方案 | 做法 | 结论 |
| --- | --- | --- |
| **A（选定）** | 调系统 `ping.exe -4 -n 4 -w 1000 <ip>`，按输出里 `TTL=` 的行数算回包数 | 零依赖、不需要管理员、语义就是"用电脑网口 ping"、超时与重传由系统处理 |
| B | Go 自己发 ICMP（`golang.org/x/net/icmp`） | Windows 上 raw socket 需要管理员权限，`x/net/icmp` 的免特权模式只在 Linux 有效；每轮弹 UAC 不可接受 |
| C | TCP 端口探测替代 | 语义不是 ping，目标设备未必开端口，达不到"确认设备还活着"的目的 |

`TTL=` 只出现在 ICMP 回包行里；`Request timed out`/`无法访问目标主机` 这类输出没有 `TTL=`，正好计 0 回包。目标不可达时 ping.exe 也会在几秒内返回，不会挂死。

### 6.2 「不正常的一轮」怎么让人看见

| 方案 | 做法 | 结论 |
| --- | --- | --- |
| **甲（用户选定）** | 只写日志：每轮失败一行，任务结束一段总结；界面只在 PING 场景用 IP 栏高亮/红底表达 | 改动最小、不动 288px 栏的排版 |
| 乙 | TASK STATUS 增加「异常次数」+ 提示行写原因 | 不用翻日志，但要重排状态栏 |
| 丙 | 「正常次数」改成「成功/总数」两个数 | 信息最全，与用户现有叫法冲突 |

甲 + 用户追加的总结统计，已经能回答"少的那一轮去哪了"。注意：甲 的可见性依赖「保存日志」开启（见 7.5）。

### 6.3 窗口底色跟随皮肤

| 方案 | 做法 | 结论 |
| --- | --- | --- |
| **A（选定）** | 前端换肤时把 `theme.bgMain`（`#RRGGBB`）写进 `ui_config.ini`；`main.go` 启动时读回来当 `BackgroundColour` | 不复制皮肤色表，新增皮肤零改动；坏值/缺文件回退默认色 |
| B | Go 里再抄一份 6 套皮肤的 bgMain 映射，按 skin id 查表 | 真相分裂，加皮肤时容易漏改，且启动时还得知道 skin id |

## 7. 具体设计（已定部分）

### 7.1 配置项（`task_config.ini`）

新增两键，`[Task]` 段内：

```ini
PingEnable=0
PingIP=
```

- 键名归一化后匹配 `PINGENABLE` / `PING`，以及 `PINGIP` / `PINGADDR` / `PINGADDRESS` / `PINGTARGET`。
- `TaskConfig` 增加 `PingEnable bool`（json `pingEnable`）、`PingIP string`（json `pingIP`）。
- 模板与 `SaveTaskConfig` 的注释块同步加两行说明。
- `SaveTaskConfig` 校验：`PingEnable` 为真且 `PingIP` 不是合法 IPv4 → 返回错误 `PING 目标地址无效：<值>`，不落盘。
- `GetTaskConfig` **不**因非法 IP 报错（旧文件/手改文件要能读出来），只原样返回，由启动任务时拦截。
- 校验只拦「开着 PING 又填坏地址」这一种保存；**关掉开关的保存（`PingEnable=0`）一律放行**，所以手改 ini 弄出坏地址之后，仍然可以在界面上把 PING 关掉、再改地址，不会锁死。

### 7.2 IP 合法性与文案

- 后端：`net.ParseIP(s)` 且 `To4() != nil`；同时拒绝 `0.0.0.0`（广播/无效目标）。
- 前端：同样的规则（四段十进制、每段 0–255、无前导零），点开关时校验。
- 非法时开关保持关闭，弹现有 `showModal('无法开启 PING 检测', msg, 'error')`，文案：「请先填写合法的 IPv4 地址，例如 192.168.1.10」。

### 7.3 PING 探针（新文件 `SerialMateX/pingcheck.go`）

```go
// pingCount 统计 ping 输出里 TTL= 的行数（= 收到的回包数）。
func pingCountTTL(out string) int

// pingRunHook 只给测试用：替换真正的 ping 调用。
var pingRunHook func(ctx context.Context, ip string) (int, error)

// pingProbe 对 ip 发 4 包 ICMP，返回收到几包；ctx 取消时立刻杀掉子进程。
func pingProbe(ctx context.Context, ip string) (int, error)

// pingPassed 判定通过条件：4 包里至少回 2 包（丢包率 ≤ 50%）。
func pingPassed(recv int) bool
```

实现要点：

- `exec.CommandContext(ctx, "ping", "-4", "-n", "4", "-w", "1000", ip)`，捕获 stdout；`syscall.SysProcAttr{HideWindow: true}` 避免控制台闪窗。
- 子进程超时由调用方 ctx 控制（8 秒上限），ctx 取消 → `CommandContext` 杀进程。
- `ping` 可执行文件找不到（`exec.ErrNotFound`）→ 返回错误，按"PING 未通过"处理并在日志里写明原因，不崩任务。
- 非零退出码不算错误（有丢包时 ping.exe 返回 1），只以 `TTL=` 计数为准。

### 7.4 每轮流程（`runTask`）

每轮顺序改为：

```
上电 → 命令（runCommands）→ 掉电倒计时结束
      → 【若 complete && PingEnable】PING 检测（Phase="ping"）
      → 异常暂停判定（命令未完成 或 PING 未通过）
      → 掉电 → 计数 → 间隔 → 下一轮
```

- PING 放在掉电**之前**：板子还在上电，测的是设备还活着。
- `runCommands` 改为返回 `(complete bool, reason string)`，`reason` 直接进日志，例如 `命令 3（RX "OK"）在运行时间内没收到匹配数据`。
- PING 期间：`Phase="ping"`、`PingState="running"`、`Message="正在 PING 192.168.1.10…"`；结束后 `PingState="ok"|"fail"`。
- 停止：`pingProbe` 的 ctx 由 `stopCh` 触发取消，点「停止」立即生效，不等 8 秒。
- PING 不通过时的暂停复用现有分支：`PausedReason="ping"`、`Message="PING 192.168.1.10 未通过（回包 1/4），已暂停（电源保持）"`；按钮仍是「结束任务 / 跳过本轮」。

计数规则：

- 命令正常 = `complete`；命令异常 = `!complete`（原因按 `reason` 记日志）。
- PING 只在命令正常时执行，所以 PING 总数 ≤ 命令正常数。
- 整轮正常 = 命令正常 且（PING 未开启 或 PING 通过）；否则整轮异常。
- 分母 = **实际开始的轮数**（每进入一轮 `总测试轮数.总数 +1`），不是配置里的「运行次数」。
- 上电失败/掉电失败提前中止的轮次：该轮没做命令或 PING 判定，不计入对应项的分子分母，但算「总测试轮数」里的一个异常轮。
- 用户点「停止」打断的轮次：同样算一个异常轮；命令已跑完则命令记正常，若正卡在 PING 被取消则 PING 记异常。

失败轮次明细（总结里要能看出「第几轮、哪条命令 / 哪次 PING、丢包率多少」）：

- 引擎里加 `failures []roundFailure`，`roundFailure{Round int, Kind string, Detail string}`，`Kind ∈ {"cmd","ping","power"}`。
- 命令失败：`Round=当前轮，Kind="cmd"，Detail="命令 3（RX \"OK\"）在运行时间内没收到匹配数据"`（即 `runCommands` 返回的 `reason`）。
- PING 失败：`Round=当前轮，Kind="ping"，Detail="PING 192.168.1.10 回包 1/4，丢包 75% → 不通过"`——丢包率必须出现在这里。
- 上电/掉电失败：`Kind="power"`，`Detail` 用现成的错误文案。
- **条数上限**：最多记 200 条（`maxRecordedFailures = 200`），超出后只累加 `failureOverflow int`，总结里补一行 `…（另有 N 轮失败未逐条列出）`。`maxRunCount` 是 100000，不设上限会把日志撑爆。
- 明细只写日志、不进 `TaskRuntime`（界面不加常驻提示，见 7.5 已知限制）。

### 7.5 日志内容

每条都用现有 `appendTxLog` 风格的"保证换行 + 时间戳前缀"写入（`logPath` 为空时什么都不写，与现有横幅一致）：

- 每轮命令失败：`[CMD 21:10:32] 第 2 次测试未完成：命令 3（RX "OK"）在运行时间内没收到匹配数据`
- 每次 PING：`[PING 21:10:32] 192.168.1.10 回包 1/4（丢包 75%）→ 不通过`
- 任务结束前追加总结块：

```
=============== 任务总结 ===============
* 总测试轮数：正常 3 / 异常 2 / 总数 5
* 命令执行：  正常 5 / 异常 0 / 总数 5
* PING 检测： 正常 3 / 异常 2 / 总数 5
----------------------------------------
* 失败轮次：2 轮
  - 第 2 轮：命令 3（RX "OK"）在运行时间内没收到匹配数据
  - 第 4 轮：PING 192.168.1.10 回包 1/4，丢包 75% → 不通过
* 结束原因：任务完成
* 结束时间：2026-11-20 21:30:12
========================================
```

- 全部正常时省掉「失败轮次」小节与分隔线。
- 超过 200 条时末尾补 `- …（另有 N 轮失败未逐条列出）`（见 7.4 的条数上限）。

- PING 未开启时该行写 `* PING 检测： 未开启`。
- 「结束原因」直接用运行时最终 `Message`（任务完成 / 任务已停止 / 已手动结束 / 已跳过最后一轮，任务结束 / 上电失败：… / 掉电失败：…）。
- **已知限制（继承现状）**：`logPath` 只在「保存日志」开启时才有值，所以总结与逐轮失败行都只在开启保存日志时产生。界面上不加常驻提示（用户选择甲）；唯一例外是 PING 失败瞬间 IP 栏那 2 秒错误色描边（见 7.6），转瞬即逝、不留痕。

### 7.6 运行时状态与界面

`TaskRuntime` 新增（json 小驼峰）：`pingState string`（`""`/`"running"`/`"ok"`/`"fail"`）；`PausedReason` 已有，新增取值 `"ping"`。

右栏「AUTO CMD」命令列表下方、启动按钮上方新增一行（2026-11-21 按用户要求从 TASK SETTINGS 移过来，标题由「PING 检测」改为「PING」）：

```
┌─────────────────────────────────────┐
│ PING   [192.168.1.10]        (开)   │   ← 与命令列表同宽，开关在右
└─────────────────────────────────────┘
```

- 结构：`.rail--r` > `.ping-bar`（`display:flex; gap:7px; padding:7px 12px; border-top:1px solid var(--line)`）> `.ping-label`「PING」+ `input.field.field--ip` + `button.switch`。
- `.field--ip`：等宽 11px、左对齐、占满剩余宽度；`:disabled` 时保持可读灰。
- 高亮（PING 进行中）：`.field--ip.is-ping { border-color: var(--col-primary); box-shadow: 0 0 0 3px var(--accent-ring) }`。
- 红底（`pingState === 'fail' && taskConfig.abnormalPause && taskRt.running`）：`.field--ip.is-ping-fail { background: color-mix(in srgb, var(--col-error) 16%, var(--field)); border-color: var(--col-error); color: var(--col-error) }`。
- 失败瞬间的短提示（**无论是否开异常暂停、是否开保存日志**）：`pingState` 由 `running` 变 `fail` 时给 IP 栏加 `.is-ping-flash`（`border-color: var(--col-error)` + `box-shadow: 0 0 0 3px color-mix(in srgb, var(--col-error) 30%, transparent)`），2 秒后自动移除；不常驻、不改底色，所以不会和上面的红底打架。
- 任务运行中（`taskRt.running`）输入框与开关都 `disabled`。
- 「下一动作」在 `Phase === 'ping'` 时显示「PING 检测中」。
- 开关点击流程：校验 IP → 合法则取反并 `SaveTaskConfig` → 失败弹错误、状态回滚；非法则弹错误、保持关闭。
- 落盘时机：输入框用 `v-model.lazy`（失焦或回车才提交），提交时校验——合法就写盘，非法就弹错并还原成上次保存的值。这样打字过程中不会每敲一个字符就写一次 ini，也不会出现「输入了新 IP、但启动时用的是旧 IP」。

### 7.7 启动任务时的校验

`StartTask` 在现有校验之后追加：`cfg.PingEnable && !validIPv4(cfg.PingIP)` → 返回 `PING 目标地址无效：<值>`，任务不启动。

### 7.8 窗口底色跟随皮肤（`SerialMateX/uiconfig.go` + `main.go` + 前端）

- 新文件 `SerialMateX/uiconfig.go`：
  - `UICfgPath()`：默认与 exe 同目录的 `ui_config.ini`，支持 `SERIALMATEX_UICFG_DIR` 覆盖（测试用）。
  - `loadStartupBackground() (r, g, b int, ok bool)`：读 `[UI]` 段的 `BgMain=#RRGGBB`；缺失/非法 → `ok=false`。
  - `(a *App) SaveUIBackground(hex string) string`：校验 `#RRGGBB`（允许 `#RGB`），`writeFileAtomic` 落盘（先写临时文件再替换），成功返回 `""`，失败返回错误文案。
- `main.go`：`r, g, b, ok := loadStartupBackground()`；`ok` 时才覆盖默认的 `R:247 G:245 B:239`（Bench 的 `--bg-main`）。
- 前端：`applySkin` 里 `SaveUIBackground(theme.bgMain)`（fire-and-forget，忽略错误）；`onMounted` 再补一次，保证老用户升级后第一次启动就有文件。
- 手写 `frontend/wailsjs/go/main/App.{js,d.ts}` 的 `SaveUIBackground`（生成物已入库，与 `EndTask`/`SkipRound` 同样处理）。
- 已知限制：文件与 `pwr_dev.ini` / `task_config.ini` 同目录（默认 exe 同目录）。装在 Program Files 之类只读目录时写失败 → 静默沿用默认色（与其它两个配置文件的既有行为一致）。换皮肤后要**下一次**启动才看到新底色，本次启动的底色已经定了。

## 8. 测试计划（TDD，先红后绿）

- `pingcheck_test.go`：`pingCountTTL` 对中英文输出、全丢包、单包回显的计数；`pingPassed` 边界（2/4 通过、1/4 不通过、0/4 不通过）；`pingRunHook` 注入错误 → 走未通过分支。
- `taskconfig_test.go`：新键解析（`PingEnable=1`、`PingIP=10.0.0.5`、别名、大小写）；`SaveTaskConfig` 往返；`PingEnable=1` + 非法/空 IP → 返回错误且不落盘。
- `uiconfig_test.go`：写→读往返；`#FFF` 三位简写；坏值/缺文件 → `ok=false`；写入非法色 → 返回错误。
- `task_test.go`：
  - PING 通过 → `NormalCount` 增加、PING 正常数 1；
  - PING 失败 + `AbnormalPause=1` → 进暂停、`PausedReason=="ping"`、日志有 `[PING` 行；
  - PING 失败 + `AbnormalPause=0` → `NormalCount` 不增、继续下一轮、日志有 `[PING` 行与总结；
  - 命令失败 + `AbnormalPause=0` → 日志有 `[CMD` 行（覆盖这次的 bug）；
  - 总结块内容与计数（含 PING 未开启时写「未开启」）；
  - 总结里的失败轮次明细：命令失败轮写「第 N 轮：命令 …」、PING 失败轮写「第 N 轮：PING … 丢包 X%」、全正常时没有该小节；
  - 失败明细超过 200 条时补「另有 N 轮失败未逐条列出」；
  - PING 期间点停止 → 任务在 1 秒内结束（不挂在 ping 上）。
- 前端：`npm run build`；CDP 断言（IP 行存在、非法 IP 点开关弹错且保持关、任务运行中 disabled、`pingState=fail` + 异常暂停时红底类名生效、Phase=ping 时下一动作文案）。

## 9. 留给实现者的决定

- `runCommands` 返回值改签名后，现有 4 处调用点与测试的改法。
- `[CMD]` / `[PING]` 行的确切措辞（只要保住"时间戳 + 轮次 + 原因"）。
- 总结块分隔线的长度（建议与 `taskLogBanner` 的 60 个 `*` 视觉协调，不必一致）。
- `pingcheck.go` 里 `HideWindow` 用 `syscall.SysProcAttr` 还是别的写法（只要能不闪控制台）。
- `.field--ip` 的确切宽度与字号（288px 栏内不换行即可）。
- `ui_config.ini` 的键名（`BgMain` 还是 `Background`）。

## 10. 风险

- **系统 `ping.exe` 被安全软件拦截**：按未通过处理，日志写明"无法执行 ping"，任务继续（开异常暂停则暂停）。用户可据此判断是环境问题。
- **`ping -4` 在极旧系统上不支持**：Windows 7+ 均支持；若失败，退化为不带 `-4`（IPv4 目标下结果一致）。
- **PING 每轮多花 1–4 秒**：用户可见（Phase=ping + IP 栏高亮），不隐藏。
- **总结只在开启保存日志时产生**：用户已知（选择甲），文档与交付说明里必须再提一次。
- **窗口底色滞后一次启动**：换皮肤后需重启才不闪，属可接受限制。