# PING 检测 / 任务总结 / 窗口底色 跟随皮肤 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让任务在每轮命令跑完后可选地 ping 一次目标 IPv4（丢包 ≤50% 通过），失败时按「异常」处理；任务结束时在日志里写一段带失败轮次明细的总结；窗口底色跟随上次选择的皮肤。

**Architecture:** 全部沿用现有单 goroutine 任务引擎（`SerialMateX/task.go` 的 `runTask`），在「掉电倒计时结束」与「异常暂停判定」之间插入 PING 阶段；PING 用系统 `ping.exe` 子进程，用 `context` 使其可被「停止」立即打断。总结与计数只在任务 goroutine 内累加，结束时拼成一段文本追加到日志。前端只多一行设置控件与两个 wails 绑定。

**Tech Stack:** Go 1.23（`os/exec`、`net`、`context`，不新增第三方依赖）、Wails v2（`go build -tags "desktop,production"`）、Vue 3 + TypeScript + Tailwind 4。

**Spec:** `docs/superpowers/specs/2026-11-20-ping-check-and-task-summary-design.md`

## Global Constraints

- 模块 `SerialMateX/go.mod` 声明 `go 1.23`；**不新增任何第三方依赖**（`os/exec`、`net`、`context` 都在标准库）。
- 配置文件一律 ini + `writeFileAtomic`（`SerialMateX/pwrdev.go:273`），不长期占用句柄。
- 三份配置的目录都可由环境变量覆盖，测试必须用它们改道：`SERIALMATEX_TASKCFG_DIR`、`SERIALMATEX_LOGDIR`、新增 `SERIALMATEX_UICFG_DIR`。
- 中文文件**只能用 Edit / Write 工具修改**；用 PowerShell 的 `Get-Content`/`Set-Content` 会把中文双重编码并加 BOM（已发生过一次 833 行假 diff）。
- Go 测试命令固定：`powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 [-Filter TestXxx] [-Run all]`。裸 `go test ./...` 在本机会因 Defender 拦截 `%TEMP%` 下的测试 exe 而失败，不要用。
- 前端构建固定：`$env:TEMP=$env:TMP="<ws>\.tools\tmp"; npm run build`（否则 esbuild 报 `Access is denied`）。
- 桌面端由 `wails build` 生成资源；本机 `wails` CLI 在 Go 1.27 下不可用，构建用 `go build -tags "desktop,production" -ldflags "-w -s -H windowsgui" -o build\bin\SerialMateX.exe .`，图标/版本来自已入库的 `SerialMateX/SerialMateX-res.syso`。
- 界面基线 1280×820，左/右栏固定 288px；新增控件必须在 288px 内不换行。
- 产品语义：TX 绿 = `#3f7d5b` 色族，错误 = `var(--col-error)`；不引入新色。

## Review Focus

1. **机器上没有 `ping.exe` 或被安全软件拦截**（`exec.ErrNotFound` / 权限拒绝）：任务不能崩、不能卡住，要按「PING 未通过」走完这一轮并在日志里写明原因。—— 测试落在 Task 4（`TestTaskPingToolMissingDoesNotCrash`）。
2. **手改 ini 造出「开着 PING + 非法地址」**：用户仍必须能把开关关掉、再改地址，不能锁死在任何设置都存不下去的状态。—— 测试落在 Task 2（`TestTaskCfgSaveAllowsDisableWithBadPingIP`）。
3. **在 PING 期间点「停止」**：必须 1 秒内停下，不残留 `ping.exe` 子进程，也不进异常暂停。—— 测试落在 Task 4（`TestTaskStopDuringPingEndsQuickly`）。
4. **`RunCount=100000` 且每轮都失败**：失败明细不能把日志撑爆。—— 测试落在 Task 5（`TestTaskSummaryCapsFailureDetail`）。
5. **配置目录只读**（装在 Program Files / 只读 U 盘）：三份配置写入失败要静默降级，程序照常运行。—— 测试落在 Task 6（`TestSaveUIBackgroundReadOnlyDirDoesNotPanic`）。

---

## File Structure

| 文件 | 职责 |
| --- | --- |
| `SerialMateX/pingcheck.go`（新） | IPv4 校验、`ping.exe` 调用、回包计数与判定、PING 日志行文本 |
| `SerialMateX/uiconfig.go`（新） | `ui_config.ini` 的读写、颜色解析、启动底色读取 |
| `SerialMateX/taskconfig.go` | `TaskConfig` 增加 `PingEnable` / `PingIP`，解析、校验、落盘 |
| `SerialMateX/task.go` | `runCommands` 返回原因、`[CMD]`/`[PING]` 日志行、PING 阶段、统计与总结块 |
| `SerialMateX/main.go` | 启动底色覆盖默认值 |
| `SerialMateX/frontend/src/App.vue` | PING 设置行、`pingState` 展示、底色落盘 |
| `SerialMateX/frontend/wailsjs/go/main/App.{js,d.ts}` | 手写 `SaveUIBackground` 绑定（生成物已入库，`wails generate` 不可用） |

---

### Task 1: PING 探针与 IPv4 校验（`pingcheck.go`）

**Files:**
- Create: `SerialMateX/pingcheck.go`
- Create: `SerialMateX/pingcheck_test.go`

**Interfaces:**
- Produces（Task 2、Task 4、Task 5 会用到）:
  - `const pingPacketCount = 4`、`const pingTimeout = 8 * time.Second`
  - `func validIPv4(s string) bool`
  - `func pingLossRate(recv int) int` — 丢包百分比，`recv=1` → `75`
  - `func pingPassed(recv int) bool` — `recv >= 2`
  - `func pingCountTTL(out string) int` — 数输出里 `TTL=` 的行数（大小写不敏感）
  - `var pingRunHook func(ctx context.Context, ip string) (int, error)` — 仅供测试替换
  - `func pingProbe(ctx context.Context, ip string) (int, error)`
  - `func pingLogLine(at, ip string, recv int, passed bool) string` — 以 `\n` 结尾
  - `func pingFailDetail(ip string, recv int) string` — 不含时间戳、不带换行

- [ ] **Step 1: 写失败的测试**

`SerialMateX/pingcheck_test.go`：

```go
func TestValidIPv4(t *testing.T) {
	cases := map[string]bool{
		"192.168.1.10": true, " 10.0.0.5 ": true, "255.255.255.255": true, "8.8.8.8": true,
		"": false, "0.0.0.0": false, "192.168.1": false, "192.168.1.256": false,
		"1.2.3.4.5": false, "192.168.01.1": false, "abc": false, "::1": false,
	}
	for in, want := range cases {
		if got := validIPv4(in); got != want {
			t.Errorf("validIPv4(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPingLossRateAndPassed(t *testing.T) {
	// recv → 丢包百分比 / 是否通过（通过 = 丢包 ≤ 50%，即至少回 2 包）
	cases := []struct{ recv, loss int; pass bool }{
		{4, 0, true}, {3, 25, true}, {2, 50, true}, {1, 75, false}, {0, 100, false},
	}
	for _, c := range cases {
		if got := pingLossRate(c.recv); got != c.loss {
			t.Errorf("pingLossRate(%d) = %d, want %d", c.recv, got, c.loss)
		}
		if got := pingPassed(c.recv); got != c.pass {
			t.Errorf("pingPassed(%d) = %v, want %v", c.recv, got, c.pass)
		}
	}
}

func TestPingCountTTL(t *testing.T) {
	en := "Reply from 192.168.1.1: bytes=32 time<1ms TTL=64\n" +
		"Reply from 192.168.1.1: bytes=32 time<1ms TTL=64\n" +
		"请求超时。\n"
	zh := "来自 192.168.1.1 的回复: 字节=32 时间<1ms TTL=64\r\n" +
		"来自 192.168.1.1 的回复: 字节=32 时间=2ms ttl=64\r\n"
	for name, out := range map[string]struct{ out string; want int }{
		"en": {en, 2}, "zh": {zh, 2},
	} {
		if got := pingCountTTL(out.out); got != out.want {
			t.Errorf("pingCountTTL(%s) = %d, want %d", name, got, out.want)
		}
	}
	if got := pingCountTTL("无法访问目标主机。\n"); got != 0 {
		t.Errorf("全丢包应数出 0，得到 %d", got)
	}
}

func TestPingLogLineAndFailDetail(t *testing.T) {
	line := pingLogLine("21:10:32", "192.168.1.10", 1, false)
	want := "[PING 21:10:32] 192.168.1.10 回包 1/4（丢包 75%）→ 不通过\n"
	if line != want {
		t.Fatalf("pingLogLine = %q, want %q", line, want)
	}
	if ok := pingLogLine("21:10:32", "192.168.1.10", 4, true); !strings.HasSuffix(ok, "回包 4/4（丢包 0%）→ 通过\n") {
		t.Fatalf("通过时日志行不对: %q", ok)
	}
	detail := pingFailDetail("192.168.1.10", 1)
	if detail != "PING 192.168.1.10 回包 1/4，丢包 75% → 不通过" {
		t.Fatalf("pingFailDetail = %q", detail)
	}
}

func TestPingProbeUsesHook(t *testing.T) {
	defer func() { pingRunHook = nil }()
	pingRunHook = func(ctx context.Context, ip string) (int, error) {
		if ip != "10.0.0.1" {
			t.Errorf("hook 收到的 ip = %q", ip)
		}
		return 3, nil
	}
	recv, err := pingProbe(context.Background(), "10.0.0.1")
	if err != nil || recv != 3 {
		t.Fatalf("pingProbe = (%d, %v), want (3, nil)", recv, err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestPing -Run tests`
Expected: FAIL / 编译错误（`undefined: validIPv4`、`undefined: pingProbe` 等）

- [ ] **Step 3: 实现 `SerialMateX/pingcheck.go`**

签名与取值已定，实现要点：

- `validIPv4`：`ip := net.ParseIP(strings.TrimSpace(s))`；`ip == nil → false`；`to4 := ip.To4(); to4 == nil → false`；`to4.Equal(net.IPv4zero) → false`；否则 `true`。（Go 1.17+ 的 `ParseIP` 已拒绝前导零，`192.168.01.1` 自然为假。）
- `pingLossRate`：`return (pingPacketCount - recv) * 100 / pingPacketCount`。
- `pingCountTTL`：按行扫描，`strings.Contains(strings.ToUpper(line), "TTL=")` 计一行。
- `pingLogLine`：`fmt.Sprintf("[PING %s] %s 回包 %d/%d（丢包 %d%%）→ %s\n", at, ip, recv, pingPacketCount, pingLossRate(recv), verdict)`，`verdict` 为 `通过` / `不通过`。
- `pingFailDetail`：同样内容但不带时间戳、不带换行，`回包` 与 `丢包` 之间用 `，`。
- `pingProbe`：
  - `if pingRunHook != nil { return pingRunHook(ctx, ip) }`
  - `cmd := exec.CommandContext(ctx, "ping", "-4", "-n", strconv.Itoa(pingPacketCount), "-w", "1000", ip)`
  - `cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}`（`pingcheck.go` 需要 `//go:build windows` 之外的普通构建；本仓库只在 Windows 上构建，直接 import `syscall` 即可）
  - `out, err := cmd.Output()`；若 `err` 是 `*exec.ExitError`（有丢包时 ping.exe 返回 1）→ 取 `ee.Stdout` 继续，不当作错误；其它 `err`（含 `exec.ErrNotFound`）→ `return 0, err`。
  - 返回 `pingCountTTL(string(out)), nil`。

- [ ] **Step 4: 跑测试确认通过**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestPing -Run tests`
Expected: `PASS`（`TestValidIPv4`、`TestPingLossRateAndPassed`、`TestPingCountTTL`、`TestPingLogLineAndFailDetail`、`TestPingProbeUsesHook`）

- [ ] **Step 5: Commit**

```bash
git add SerialMateX/pingcheck.go SerialMateX/pingcheck_test.go
git commit -m "feat(task): 新增 PING 探针与 IPv4 校验"
```

---

### Task 2: `task_config.ini` 增加 PING 配置

**Files:**
- Modify: `SerialMateX/taskconfig.go:12-20`（`TaskConfig` 结构）、`:34-51`（模板）、`:104-126`（解析）、`:133-163`（保存）
- Test: `SerialMateX/taskconfig_test.go`

**Interfaces:**
- Consumes: `validIPv4`（Task 1）
- Produces:
  - `TaskConfig.PingEnable bool`（json `pingEnable`）、`TaskConfig.PingIP string`（json `pingIP`）
  - `SaveTaskConfig` 在 `PingEnable && !validIPv4(PingIP)` 时返回 `fmt.Errorf("PING 目标地址无效：%s", cfg.PingIP)` 且不落盘

- [ ] **Step 1: 写失败的测试**

在 `SerialMateX/taskconfig_test.go` 追加：

```go
func TestTaskCfgPingKeys(t *testing.T) {
	// 键名归一化后 PINGENABLE / PING 都认；PINGIP / PINGADDR / PINGTARGET 都认；大小写不敏感
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	body := "[Task]\nping=1\npingaddr = 10.0.0.5\nRunCount=1\n"
	if err := os.WriteFile(filepath.Join(dir, "task_config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := (&App{}).GetTaskConfig()
	if !cfg.PingEnable || cfg.PingIP != "10.0.0.5" {
		t.Fatalf("PING 键没解析出来: %+v", cfg)
	}
}

func TestTaskCfgSavePingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	app := &App{}
	in := TaskConfig{PingEnable: true, PingIP: " 192.168.1.10 ", WaitSeconds: 5, DelaySeconds: 1, RunCount: 1}
	if err := app.SaveTaskConfig(in); err != nil {
		t.Fatal(err)
	}
	got := app.GetTaskConfig()
	if !got.PingEnable || got.PingIP != "192.168.1.10" {
		t.Fatalf("往返丢了 PING 值: %+v", got)
	}
}

func TestTaskCfgSaveRejectsBadPingIP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	app := &App{}
	err := app.SaveTaskConfig(TaskConfig{PingEnable: true, PingIP: "999.1.1.1", WaitSeconds: 5, DelaySeconds: 1, RunCount: 1})
	if err == nil || !strings.Contains(err.Error(), "PING 目标地址无效：999.1.1.1") {
		t.Fatalf("期望 PING 地址错误，得到 %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "task_config.ini")); !os.IsNotExist(statErr) {
		t.Fatalf("校验失败时不应落盘")
	}
}

func TestTaskCfgSaveAllowsDisableWithBadPingIP(t *testing.T) {
	// 手改 ini 弄出坏地址后，用户必须还能把开关关掉
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	app := &App{}
	if err := app.SaveTaskConfig(TaskConfig{PingEnable: false, PingIP: "bad", WaitSeconds: 5, DelaySeconds: 1, RunCount: 1}); err != nil {
		t.Fatalf("关闭 PING 时不该拦: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestTaskCfgSave -Run tests`
Expected: FAIL（`cfg.PingEnable` 未定义 / 保存后读不到）

- [ ] **Step 3: 实现**

- `TaskConfig` 末尾加两个字段（json 小驼峰），注释写「PING 检测」与「PING 目标 IPv4」。
- `taskCfgTemplate` 与 `SaveTaskConfig` 的注释块各加两行：`; PingEnable   = PING 检测 0/1`、`; PingIP       = PING 目标地址（IPv4）`。
- 解析 switch 增加：
  - `case "PINGENABLE", "PING":` → `cfg.PingEnable = parseBoolValue(value)`
  - `case "PINGIP", "PINGADDR", "PINGADDRESS", "PINGTARGET":` → `cfg.PingIP = value`
- `SaveTaskConfig`：在 clamp 之后、拼字符串之前：

```go
if cfg.PingEnable && !validIPv4(cfg.PingIP) {
    return fmt.Errorf("PING 目标地址无效：%s", cfg.PingIP)
}
```

  并在 `[Task]` 段落末写 `PingEnable=` + `boolText(cfg.PingEnable)`、`PingIP=` + `strings.TrimSpace(cfg.PingIP)`。
- `taskconfig.go` 的 import 需要加 `fmt`。

- [ ] **Step 4: 跑测试确认通过**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Run all`
Expected: 三个包全 `PASS`（`PASSED: serial-assistant, serial-assistant/pkg/jlink, serial-assistant/pkg/updater`）

- [ ] **Step 5: Commit**

```bash
git add SerialMateX/taskconfig.go SerialMateX/taskconfig_test.go
git commit -m "feat(task): task_config.ini 支持 PING 检测开关与目标地址"
```

---

### Task 3: 命令未完成时返回原因并写 `[CMD]` 日志行

**Files:**
- Modify: `SerialMateX/task.go:403-429`（日志写入口）、`:589`（调用点）、`:707-777`（`runCommands`）
- Test: `SerialMateX/task_test.go`

**Interfaces:**
- Consumes: 现有 `appendTxLog` / `lastFileByte`
- Produces:
  - `func (a *App) runCommands(cmds []AutoCmd, deadline time.Time) (complete bool, reason string)` — Task 4、Task 5 依赖
  - `func (t *taskEngine) appendMarkedLog(logPath, line string)` — 保证前面恰好一个换行的单行日志入口；`appendTxLog` 改为调用它
  - `func cmdLogLine(at string, round int, reason string) string` — `[CMD 21:10:32] 第 2 次测试未完成：<reason>\n`

- [ ] **Step 1: 写失败的测试**

在 `SerialMateX/task_test.go` 追加（复用 `writeTaskFixtures` / `waitTaskDone` / `readTaskLog`）：

```go
func TestTaskLogCmdLineOnIncompleteRound(t *testing.T) {
	// 一条永不匹配的 RX：这一轮命令没跑完，必须留下 [CMD] 行，写清第几轮、哪条命令。
	// 本用例没有 TX 命令，所以不需要 taskSendHook。
	writeTaskFixtures(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
		"RX NEVER_MATCH 0\n",
		"[Dev]\nName=Dev\nPort=COM9\nPWR-ON=0x01\nPWR-OFF=0x00\n")

	app := &App{isConnected: true, connType: TypeSerial}
	if res := app.StartTask(TaskRunOptions{DeviceName: "Dev"}); res != "" {
		t.Fatalf("启动失败: %s", res)
	}
	waitTaskDone(t, app, 8*time.Second)

	log := readTaskLog(t)
	if !strings.Contains(log, "[CMD ") {
		t.Fatalf("日志里没有 [CMD] 行:\n%s", log)
	}
	if !strings.Contains(log, "第 1 次测试未完成") || !strings.Contains(log, `命令 1（RX "NEVER_MATCH"）`) {
		t.Fatalf("[CMD] 行缺少轮次或命令描述:\n%s", log)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestTaskLogCmdLine -Run tests`
Expected: FAIL（日志里没有 `[CMD`）

- [ ] **Step 3: 实现**

- 把 `appendTxLog` 的加锁 + 补换行逻辑抽成 `appendMarkedLog(logPath, line string)`（参数是**整行，含末尾 `\n`**），`appendTxLog` 改为 `t.appendMarkedLog(logPath, "[TX "+at+"] "+text+"\n")`。行为必须与现在完全一致。
- `runCommands` 改成返回 `(complete bool, reason string)`；每个 `return false` 换成带原因的返回，文案按此表（`i` 为 0 基下标，展示用 `i+1`）：

| 位置 | reason |
| --- | --- |
| 循环开头 `t.stopped() \|\| !before(deadline)` | `t.stopped()` 时 `fmt.Sprintf("命令 %d 未执行：任务已停止", i+1)`，否则 `fmt.Sprintf("运行时间内没轮到命令 %d（%s %q）", i+1, cmd.Type, cmd.Text)` |
| RX 等待超时（`!t.waitForMatch`） | `fmt.Sprintf("命令 %d（%s %q）在运行时间内没收到匹配数据", i+1, cmd.Type, cmd.Text)` |
| 编码失败 | `fmt.Sprintf("命令 %d（%s %q）编码失败：%v", i+1, cmd.Type, cmd.Text, encErr)` |
| 发送失败 | `fmt.Sprintf("命令 %d（%s %q）发送失败：%s", i+1, cmd.Type, cmd.Text, res)` |
| 延时吃完运行时间 | `fmt.Sprintf("运行时间在命令 %d 之后用完，剩余命令未执行", i+1)` |

  成功路径 `return true, ""`；`stopped()` 且出现在延时等待里时也返回上面「任务已停止」那类原因。
- `runTask` 调用点：`complete, failReason := a.runCommands(cmds, deadline)`；`if !complete && logPath != ""` 时写 `t.appendMarkedLog(logPath, cmdLogLine(time.Now().Format("15:04:05"), round, failReason))`。`[CMD]` 行写在 **`[TX]`/RX 之后、掉电横幅之前**。

- [ ] **Step 4: 跑测试确认通过**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Run all`
Expected: 全绿；尤其 `TestTaskTxLogLine`、`TestTaskTxLogLineStripsEOL`、`TestTaskTxLogUsesKeyTokens` 不回归（证明 `appendMarkedLog` 抽取没改变 TX 行行为）

- [ ] **Step 5: Commit**

```bash
git add SerialMateX/task.go SerialMateX/task_test.go
git commit -m "feat(task): 命令未完成时说明是哪一条，并写入 [CMD] 日志行"
```

---

### Task 4: 每轮插入 PING 阶段

**Files:**
- Modify: `SerialMateX/task.go:30-48`（`TaskRuntime` 增加 `PingState`）、`:553-673`（`runTask` 每轮）、`:68-124`（`StartTask` 追加校验）
- Test: `SerialMateX/task_test.go`

**Interfaces:**
- Consumes: `validIPv4`、`pingProbe`、`pingPassed`、`pingLogLine`、`pingFailDetail`（Task 1）；`appendMarkedLog`（Task 3）
- Produces:
  - `TaskRuntime.PingState string`（json `pingState`，取值 `""` / `"running"` / `"ok"` / `"fail"`）
  - `func (a *App) runPingCheck(ip string) (recv int, err error)` — 置 `Phase="ping"`、可被停止打断
  - `PausedReason` 新增取值 `"ping"`

- [ ] **Step 1: 写失败的测试**

```go
// startPingTask 是本任务四个用例的公共前置：假电源口 + TX 恒成功 + 注入 pingRunHook。
// 注意 writeTaskFixtures 内部已经装了假电源口，不要在外面再调 installFakePwrPort。
func startPingTask(t *testing.T, cfg, cmds string, hook func(ctx context.Context, ip string) (int, error)) *App {
	t.Helper()
	writeTaskFixtures(t, cfg, cmds, "[Dev]\nName=Dev\nPort=COM9\nPWR-ON=0x01\nPWR-OFF=0x00\n")
	taskSendHook = func(string) string { return "Sent" }
	pingRunHook = hook
	t.Cleanup(func() { taskSendHook = nil; pingRunHook = nil })

	app := &App{isConnected: true, connType: TypeSerial}
	if res := app.StartTask(TaskRunOptions{DeviceName: "Dev"}); res != "" {
		t.Fatalf("启动失败: %s", res)
	}
	return app
}

func TestTaskPingPassKeepsNormalCount(t *testing.T) {
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 4, nil })

	rt := waitTaskDone(t, app, 8*time.Second)
	if rt.NormalCount != 1 || rt.PingState != "ok" {
		t.Fatalf("PING 通过应计正常: %+v", rt)
	}
	if log := readTaskLog(t); !strings.Contains(log, "[PING ") || !strings.Contains(log, "→ 通过") {
		t.Fatalf("日志缺 PING 行:\n%s", log)
	}
}

func TestTaskPingFailPauses(t *testing.T) {
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=1\nWaitSeconds=1\nDelaySeconds=0\nRunCount=3\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 1, nil })

	rt := waitTaskPaused(t, app, 8*time.Second)
	if rt.PausedReason != "ping" {
		t.Fatalf("PausedReason = %q, want ping", rt.PausedReason)
	}
	if !strings.Contains(rt.Message, "PING 192.168.1.10 未通过（回包 1/4）") {
		t.Fatalf("暂停文案不对: %q", rt.Message)
	}
	if res := app.EndTask(); res != "" {
		t.Fatalf("EndTask: %s", res)
	}
	waitTaskDone(t, app, 5*time.Second)
}

func TestTaskPingToolMissingDoesNotCrash(t *testing.T) {
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 0, exec.ErrNotFound })

	rt := waitTaskDone(t, app, 8*time.Second)
	if rt.NormalCount != 0 {
		t.Fatalf("PING 工具不可用应按未通过: %+v", rt)
	}
	if log := readTaskLog(t); !strings.Contains(log, "→ 不通过") {
		t.Fatalf("日志应记未通过:\n%s", log)
	}
}

func TestTaskPingRunsWithoutLogOrPause(t *testing.T) {
	// 用户明确要求：PING 是否执行只取决于 PingEnable，与 SaveLog / AbnormalPause 无关。
	// （没开日志、没开异常暂停时，界面上的正常次数就是 PING 的通过次数。）
	pings := 0
	app := startPingTask(t,
		"[Task]\nSaveLog=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=2\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			pings++
			if pings == 1 {
				return 4, nil
			}
			return 0, nil
		})

	rt := waitTaskDone(t, app, 10*time.Second)
	if pings != 2 {
		t.Fatalf("两轮都应执行 PING，实际 %d 次", pings)
	}
	if rt.NormalCount != 1 {
		t.Fatalf("PING 失败的那一轮不应计正常: %+v", rt)
	}
}

func TestTaskPingOnlyTaskWithoutCommands(t *testing.T) {
	// 命令列表为空 + PING 开启：应当能启动，整轮只有 PING 一步（正常次数 = PING 通过次数）
	writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=2\nPingEnable=1\nPingIP=192.168.1.10\n",
		"",
		"[Dev]\nName=Dev\nPort=COM9\nPWR-ON=0x01\nPWR-OFF=0x00\n")
	pingRunHook = func(ctx context.Context, ip string) (int, error) { return 4, nil }
	t.Cleanup(func() { pingRunHook = nil })

	app := &App{isConnected: true, connType: TypeSerial}
	if res := app.StartTask(TaskRunOptions{DeviceName: "Dev"}); res != "" {
		t.Fatalf("PING 开启时应允许空命令列表: %s", res)
	}
	rt := waitTaskDone(t, app, 10*time.Second)
	if rt.NormalCount != 2 || rt.RunCount != 2 {
		t.Fatalf("空命令列表应只靠 PING 计正常: %+v", rt)
	}
}

func TestTaskStopDuringPingEndsQuickly(t *testing.T) {
	entered := make(chan struct{})
	app := startPingTask(t,
		"[Task]\nSaveLog=0\nAbnormalPause=1\nWaitSeconds=1\nDelaySeconds=0\nRunCount=5\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done() // 只有 ctx 被取消才返回，模拟一个挂住的 ping
			return 0, ctx.Err()
		})

	<-entered
	if res := app.StopTask(); res != "" {
		t.Fatalf("StopTask: %s", res)
	}
	waitTaskDone(t, app, 2*time.Second) // 必须远小于 pingTimeout(8s)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestTaskPing -Run tests`
Expected: FAIL（`rt.PingState` 未定义 / PING 从不执行）

- [ ] **Step 3: 实现**

- `TaskRuntime` 增加 `PingState string` + 注释（`""`/`running`/`ok`/`fail`）。
- `StartTask`：在现有 `encodeCmdPayload` 校验之前加

```go
if cfg.PingEnable && !validIPv4(cfg.PingIP) {
    return "PING 目标地址无效：" + cfg.PingIP
}
```

- `StartTask` 的「命令列表为空」校验改为**只在 PING 关闭时拦截**：`if len(cmds) == 0 && !cfg.PingEnable { return "命令列表为空，无法启动任务" }`。理由：PING 开启时空命令列表是合法的「只做连通性检查」任务，用户明确要求这种场景下正常/异常次数就等于 PING 的通过/失败次数。
- `runPingCheck` 只在 `cfg.PingEnable` 为真时执行，**与 `SaveLog` / `AbnormalPause` 无关**（用户 m03169 明确要求：日志没开也要 PING，否则失败时完全没有反馈）。

- 新增 `runPingCheck`：置 `Phase="ping"`、`PingState="running"`、`Message="正在 PING "+ip+"…"`；用 `context.WithTimeout(context.Background(), pingTimeout)` + 一个 goroutine 监听 `t.stopCh` 触发 `cancel()`；返回 `pingProbe(ctx, ip)`；结束时把 `Message` 清空。
- `runTask` 每轮：在 `complete, failReason := a.runCommands(...)` 之后、`t.countdown(deadline, "running", "powerOff")` 之后插入 PING 阶段：

```go
pingFailed := false
pingRecv := 0
if complete && cfg.PingEnable {
    var pingErr error
    pingRecv, pingErr = a.runPingCheck(cfg.PingIP)
    passed := pingErr == nil && pingPassed(pingRecv)
    if logPath != "" {
        t.appendMarkedLog(logPath, pingLogLine(time.Now().Format("15:04:05"), cfg.PingIP, pingRecv, passed))
    }
    t.set(func(rt *TaskRuntime) {
        if passed { rt.PingState = "ok" } else { rt.PingState = "fail" }
    })
    pingFailed = !passed
}
```

- 暂停判定改为 `if (!complete || pingFailed) && cfg.AbnormalPause && !t.stopped()`；块内按 `complete` 选择原因：

```go
if !complete {
    reason, msg = "abnormal", "有命令未执行完，已暂停（电源保持）"
} else {
    reason = "ping"
    msg = fmt.Sprintf("PING %s 未通过（回包 %d/%d），已暂停（电源保持）", cfg.PingIP, pingRecv, pingPacketCount)
}
```

  并沿用原有的 `select`（`decision` / `stopCh`）与「结束任务 / 跳过本轮」语义，不要改动这部分逻辑。
- 计数（`Stats` 结构在 Task 5 才建）本任务只保证：PING 失败时 `NormalCount` 不增加。实现方式：把现有 `if complete && offErr == nil { NormalCount++ }` 改为 `if complete && !pingFailed && offErr == nil { NormalCount++ }`。

- [ ] **Step 4: 跑测试确认通过**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Run all`
Expected: 全绿；`TestTaskPauseEndTask` / `TestTaskPauseSkipRound` / `TestTaskPauseStopDoesNotHang` 不回归

- [ ] **Step 5: Commit**

```bash
git add SerialMateX/task.go SerialMateX/task_test.go
git commit -m "feat(task): 每轮命令跑完后按配置 PING 目标地址并在失败时暂停"
```

---

### Task 5: 统计与任务总结块

**Files:**
- Modify: `SerialMateX/task.go`（新增类型、`runTask` 计数与收尾）
- Test: `SerialMateX/task_test.go`

**Interfaces:**
- Consumes: `cmdLogLine`（Task 3）、`pingFailDetail`（Task 1）
- Produces:
  - `type roundFailure struct { Round int; Kind string; Detail string }`（`Kind ∈ {"cmd","ping","power"}`）
  - `type taskStats struct { ... }` + `func (s *taskStats) addFailure(round int, kind, detail string)`
  - `const maxRecordedFailures = 200`
  - `func formatTaskSummary(s *taskStats, endReason string, endedAt time.Time) string` — 返回以 `\n` 开头、以 `\n` 结尾的整段文本

- [ ] **Step 1: 写失败的测试**

```go
func TestFormatTaskSummary(t *testing.T) {
	s := &taskStats{pingEnabled: true}
	s.roundsTotal, s.roundsNormal = 5, 3
	s.cmdsTotal, s.cmdsNormal = 5, 5
	s.pingsTotal, s.pingsNormal = 5, 3
	s.addFailure(2, "cmd", `命令 3（RX "OK"）在运行时间内没收到匹配数据`)
	s.addFailure(4, "ping", "PING 192.168.1.10 回包 1/4，丢包 75% → 不通过")

	got := formatTaskSummary(s, "任务完成", time.Date(2026, 11, 20, 21, 30, 12, 0, time.Local))
	want := "\n" +
		"=============== 任务总结 ===============\n" +
		"* 总测试轮数：正常 3 / 异常 2 / 总数 5\n" +
		"* 命令执行：  正常 5 / 异常 0 / 总数 5\n" +
		"* PING 检测： 正常 3 / 异常 2 / 总数 5\n" +
		"----------------------------------------\n" +
		"* 失败轮次：2 轮\n" +
		"  - 第 2 轮：命令 3（RX \"OK\"）在运行时间内没收到匹配数据\n" +
		"  - 第 4 轮：PING 192.168.1.10 回包 1/4，丢包 75% → 不通过\n" +
		"* 结束原因：任务完成\n" +
		"* 结束时间：2026-11-20 21:30:12\n" +
		"========================================\n"
	if got != want {
		t.Fatalf("总结不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatTaskSummaryAllNormalAndPingOff(t *testing.T) {
	s := &taskStats{}
	s.roundsTotal, s.roundsNormal = 2, 2
	s.cmdsTotal, s.cmdsNormal = 2, 2
	got := formatTaskSummary(s, "任务完成", time.Date(2026, 11, 20, 21, 30, 12, 0, time.Local))
	if strings.Contains(got, "失败轮次") || strings.Contains(got, "-----") {
		t.Fatalf("全正常时不该有失败小节:\n%s", got)
	}
	if !strings.Contains(got, "* PING 检测： 未开启\n") {
		t.Fatalf("PING 未开启时应写「未开启」:\n%s", got)
	}

	// 空命令列表的 PING-only 任务：命令那行写「未配置命令」，且不能出现「正常 0 / 异常 0 / 总数 0」
	only := &taskStats{pingEnabled: true}
	only.roundsTotal, only.roundsNormal = 3, 3
	only.pingsTotal, only.pingsNormal = 3, 3
	got = formatTaskSummary(only, "任务完成", time.Date(2026, 11, 20, 21, 30, 12, 0, time.Local))
	if !strings.Contains(got, "* 命令执行：  未配置命令\n") {
		t.Fatalf("空命令列表应写「未配置命令」:\n%s", got)
	}
}

func TestTaskSummaryCapsFailureDetail(t *testing.T) {
	s := &taskStats{}
	for i := 1; i <= 205; i++ {
		s.addFailure(i, "cmd", "x")
	}
	if len(s.failures) != maxRecordedFailures || s.overflow != 5 {
		t.Fatalf("上限没生效: len=%d overflow=%d", len(s.failures), s.overflow)
	}
	got := formatTaskSummary(s, "任务完成", time.Now())
	if !strings.Contains(got, "* 失败轮次：205 轮") || !strings.Contains(got, "另有 5 轮失败未逐条列出") {
		t.Fatalf("溢出提示不对:\n%s", got)
	}
}

func TestTaskSummaryAppendedToLog(t *testing.T) {
	// 第一轮 PING 通过、第二轮失败：验证 PING 的正常/异常计数与失败明细。
	// pingRunHook 只在任务 goroutine 里被调用，计数器不需要加锁。
	calls := 0
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=2\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			calls++
			if calls == 1 {
				return 4, nil
			}
			return 1, nil
		})

	waitTaskDone(t, app, 10*time.Second)

	log := readTaskLog(t)
	for _, want := range []string{
		"=============== 任务总结 ===============",
		"* 总测试轮数：正常 1 / 异常 1 / 总数 2",
		"* PING 检测： 正常 1 / 异常 1 / 总数 2",
		"* 失败轮次：1 轮",
		"第 2 轮：PING 192.168.1.10 回包 1/4，丢包 75% → 不通过",
		"* 结束原因：任务完成",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志缺少 %q，实际:\n%s", want, log)
		}
	}
}
```

（`startPingTask` 来自 Task 4 的测试辅助函数，同一个 `task_test.go`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestFormatTaskSummary -Run tests`
Expected: FAIL（`taskStats` / `formatTaskSummary` 未定义）

- [ ] **Step 3: 实现**

- `roundFailure` / `taskStats` / `maxRecordedFailures` 定义在 `task.go` 顶部常量区附近。`taskStats` 字段：`roundsTotal, roundsNormal, cmdsTotal, cmdsNormal, pingsTotal, pingsNormal int`、`pingEnabled bool`、`failures []roundFailure`、`overflow int`。注释写明「只有任务 goroutine 读写，不需要加锁」。
- `addFailure`：`if len(s.failures) >= maxRecordedFailures { s.overflow++; return }`，否则 append。
- `formatTaskSummary`：按 Step 1 期望的**逐行**拼装；异常数一律用 `总数 - 正常` 计算；`pingEnabled` 为假时该行写 `* PING 检测： 未开启`；`len(failures)+overflow == 0` 时跳过「失败轮次」小节与它上面的分隔线；溢出时在明细末尾加 `…（另有 N 轮失败未逐条列出）`（用 `  - ` 前缀那行的缩进）。
- `runTask` 计数点：
  - 进轮开始（`rt.Round = round` 那次 `t.set` 之后）：`stats.roundsTotal++`。
  - `runCommands` 之后：`if len(cmds) > 0 { stats.cmdsTotal++; complete 则 stats.cmdsNormal++，否则 stats.addFailure(round, "cmd", failReason) }`（空命令列表的 PING-only 任务不产生命令统计，`formatTaskSummary` 里 `cmdsTotal == 0` 时整行写 `* 命令执行：  未配置命令`）。
  - PING 分支：`stats.pingsTotal++`；通过 `stats.pingsNormal++`，否则 `stats.addFailure(round, "ping", pingFailDetail(cfg.PingIP, pingRecv))`。
  - 上电失败 / 掉电失败：`stats.addFailure(round, "power", failMsg)`（`failMsg` 已赋值处各加一次；同一轮只记一条）。
  - 正常轮：与 `NormalCount++` 同一处 `stats.roundsNormal++`（即 `complete && !pingFailed && offErr == nil`）。
  - 初始化：`stats := &taskStats{pingEnabled: cfg.PingEnable}` 放在 `runTask` 开头（`logPath` 之后）。
- 收尾：把现有 `msg / failMsg / endMsg` 的 `switch` 提到 `t.set` 之前算出 `finalMsg`，`t.set` 里用 `finalMsg`；随后 `if logPath != "" { t.appendTaskLog(logPath, formatTaskSummary(stats, finalMsg, time.Now())) }`（`appendTaskLog` 本身不补换行，所以 `formatTaskSummary` 自带首尾换行）。

- [ ] **Step 4: 跑测试确认通过**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Run all`
Expected: 全绿

- [ ] **Step 5: Commit**

```bash
git add SerialMateX/task.go SerialMateX/task_test.go
git commit -m "feat(task): 任务结束写入统计总结与失败轮次明细"
```

---

### Task 6: 窗口底色跟随皮肤（`uiconfig.go` + `main.go`）

**Files:**
- Create: `SerialMateX/uiconfig.go`
- Create: `SerialMateX/uiconfig_test.go`
- Modify: `SerialMateX/main.go:33-35`

**Interfaces:**
- Produces:
  - `const uiCfgFileName = "ui_config.ini"`
  - `func UICfgPath() (string, error)` — `SERIALMATEX_UICFG_DIR` 覆盖
  - `func parseHexColor(s string) (r, g, b uint8, ok bool)` — 接受 `#RRGGBB`、`#RGB`（可省 `#`）
  - `func loadStartupBackground() (r, g, b uint8, ok bool)`
  - `func (a *App) SaveUIBackground(hex string) string` — 成功 `""`，失败返回错误文案

- [ ] **Step 1: 写失败的测试**

```go
func TestParseHexColor(t *testing.T) {
	cases := []struct{ in string; r, g, b uint8; ok bool }{
		{"#F7F5EF", 247, 245, 239, true},
		{"F7F5EF", 247, 245, 239, true},
		{"#FFF", 255, 255, 255, true},
		{"  #0a0B0c ", 10, 11, 12, true},
		{"#F7F5", 0, 0, 0, false},
		{"#GGGGGG", 0, 0, 0, false},
		{"", 0, 0, 0, false},
	}
	for _, c := range cases {
		r, g, b, ok := parseHexColor(c.in)
		if ok != c.ok || (ok && (r != c.r || g != c.g || b != c.b)) {
			t.Errorf("parseHexColor(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)", c.in, r, g, b, ok, c.r, c.g, c.b, c.ok)
		}
	}
}

func TestUIBackgroundRoundTrip(t *testing.T) {
	t.Setenv("SERIALMATEX_UICFG_DIR", t.TempDir())
	app := &App{}
	if res := app.SaveUIBackground("#101820"); res != "" {
		t.Fatalf("保存失败: %s", res)
	}
	if r, g, b, ok := loadStartupBackground(); !ok || r != 16 || g != 24 || b != 32 {
		t.Fatalf("读回 = (%d,%d,%d,%v)", r, g, b, ok)
	}
}

func TestLoadStartupBackgroundMissingFile(t *testing.T) {
	t.Setenv("SERIALMATEX_UICFG_DIR", t.TempDir())
	if _, _, _, ok := loadStartupBackground(); ok {
		t.Fatal("文件不存在时应返回 ok=false")
	}
}

func TestSaveUIBackgroundRejectsBadColor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_UICFG_DIR", dir)
	app := &App{}
	if res := app.SaveUIBackground("nope"); res == "" {
		t.Fatal("非法颜色应返回错误文案")
	}
	if _, err := os.Stat(filepath.Join(dir, uiCfgFileName)); !os.IsNotExist(err) {
		t.Fatal("非法颜色不应落盘")
	}
}

func TestSaveUIBackgroundReadOnlyDirDoesNotPanic(t *testing.T) {
	// 目录不可写时只返回错误文案，绝不 panic（Program Files 安装场景）
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_UICFG_DIR", filepath.Join(dir, "missing", "deeper"))
	app := &App{}
	_ = app.SaveUIBackground("#101820") // 不 panic 即通过
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Filter TestUIBackground -Run tests`
Expected: FAIL / 编译错误

- [ ] **Step 3: 实现**

- `uiconfig.go`：模板照 `taskconfig.go` 的风格写 `; SerialMateX 界面配置` + `[UI]` + `BgMain=#F7F5EF`；`loadStartupBackground` 读文件、找 `BGMAIN` 键（键名归一化同 `GetTaskConfig`）、`parseHexColor` 成功则返回；任何失败返回 `ok=false`。
- `SaveUIBackground`：`parseHexColor` 失败 → 返回 `"颜色格式无效：" + hex`；成功则 `os.MkdirAll(dir)`（忽略错误）+ `writeFileAtomic(path, content)`，错误转成字符串返回。
- `main.go`：

```go
bg := &options.RGBA{R: 247, G: 245, B: 239, A: 1}
if r, g, b, ok := loadStartupBackground(); ok {
    bg = &options.RGBA{R: r, G: g, B: b, A: 1}
}
// 在 wails.Run(&options.App{ ... BackgroundColour: bg, ... }) 里使用
```

  注释说明「底色来自上次选择的皮肤；文件缺失/非法时退回 Bench 的 --bg-main」。

- [ ] **Step 4: 跑测试确认通过**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Run all`
Expected: 全绿（`main.go` 也要能编译：`go build ./...` 由脚本的 `-Run all` 覆盖）

- [ ] **Step 5: Commit**

```bash
git add SerialMateX/uiconfig.go SerialMateX/uiconfig_test.go SerialMateX/main.go
git commit -m "feat(ui): 窗口底色从 ui_config.ini 读取，跟随上次选择的皮肤"
```

---

### Task 7: TASK SETTINGS 的 PING 行与运行时反馈

**Files:**
- Modify: `SerialMateX/frontend/src/App.vue:292-301`（配置模型）、`:306-345`（运行时模型）、`:357-376`（读配置）、`:2273-2279`（设置行）、`:2305-2314`（下一动作）、scoped style 末尾
- Modify: `SerialMateX/frontend/wailsjs/go/main/App.js`、`App.d.ts`（若要跑 Task 8，两处一起改也可以；本任务不改绑定）

**Interfaces:**
- Consumes: `task_config.ini` 的 `pingEnable` / `pingIP`（Task 2）、`GetTaskRuntime().pingState` 与 `phase === 'ping'`（Task 4）
- Produces（供后续任务/验收引用）: `const isValidIPv4 = (s: string): boolean`、`const togglePing = async () => void`、`const commitPingIP = async () => void`、`const pingFlash = ref(false)`

- [ ] **Step 1: 改模型与读取**

- `TaskConfigModel` 增加 `pingEnable: boolean; pingIP: string;`，`taskConfig` 初值加 `pingEnable: false, pingIP: ''`。
- `loadTaskConfig` 的赋值对象加 `pingEnable: !!(cfg as any).pingEnable, pingIP: String((cfg as any).pingIP ?? '')`，并在赋值后 `lastSavedPingIP.value = taskConfig.value.pingIP`。
- `TaskRuntimeModel` 增加 `pingState: string;`，`emptyRuntime()` 加 `pingState: ''`。

- [ ] **Step 2: 加校验与交互函数（放在 `persistTaskConfig` 之后）**

- `const lastSavedPingIP = ref('')`
- `isValidIPv4(s)`：`trim()` → 按 `.` 切四段、每段 `/^\d{1,3}$/`、无前导零（`'0'` 除外）、`0–255`、且不全为 `0.0.0.0`。
- `togglePing()`：`if (taskRt.value.running) return;` → 打开时校验 `taskConfig.value.pingIP`，非法 → `showModal('无法开启 PING 检测', '请先填写合法的 IPv4 地址，例如 192.168.1.10', 'error')` 并 `return`；合法 → 取反 → `await persistTaskConfig()`；`SaveTaskConfig` 抛错时把取反撤回来（`persistTaskConfig` 目前吞错误，所以这里改成 try/catch 调 `SaveTaskConfig` 自己处理，或让 `persistTaskConfig` 返回 boolean）。
- `commitPingIP()`：合法 → `persistTaskConfig()` 成功后 `lastSavedPingIP.value = taskConfig.value.pingIP`；非法 → 弹同一条错误并 `taskConfig.value.pingIP = lastSavedPingIP.value`。
- `watch(() => taskRt.value.pingState, (now, before) => { if (before === 'running' && now === 'fail') { pingFlash.value = true; window.setTimeout(() => { pingFlash.value = false; }, 2000); } })`。

- [ ] **Step 3: 加标记（「运行次数」那一行之后，`App.vue:2278` 附近）**

```html
<div class="setting-row">
  <span class="setting-label">PING</span>
  <div class="ping-ctl">
    <input
      v-model.lazy="taskConfig.pingIP"
      @change="commitPingIP"
      :disabled="taskRt.running"
      class="field field--ip"
      :class="{ 'is-ping': taskRt.pingState === 'running', 'is-ping-fail': taskRt.pingState === 'fail' && taskConfig.abnormalPause && taskRt.running, 'is-ping-flash': pingFlash }"
      placeholder="192.168.1.10"
      title="每轮命令跑完后 ping 这个地址（4 包，丢包 ≤50% 通过）"
    />
    <button @click="togglePing" :disabled="taskRt.running" class="switch" :class="{ 'is-on': taskConfig.pingEnable }" :title="taskConfig.pingEnable ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ taskConfig.pingEnable ? '开' : '关' }}</span></button>
  </div>
</div>
```

- 「下一动作」加一个分支（在 `paused` 之前）：`<template v-else-if="taskRt.running && taskRt.phase === 'ping'">PING 检测中</template>`。

- [ ] **Step 4: 加样式（scoped style 里 `.chip--num` 附近）**

- `.ping-ctl { display: flex; align-items: center; gap: 6px; }`
- `.field--ip { width: 100px; text-align: center; font-size: 11px; font-family: inherit; }`，等宽数字 `font-variant-numeric: tabular-nums`
- `.field--ip.is-ping { border-color: var(--col-primary); box-shadow: 0 0 0 3px var(--accent-ring); }`
- `.field--ip.is-ping-fail { background: color-mix(in srgb, var(--col-error) 16%, var(--field)); border-color: var(--col-error); color: var(--col-error); }`
- `.field--ip.is-ping-flash { border-color: var(--col-error); box-shadow: 0 0 0 3px color-mix(in srgb, var(--col-error) 30%, transparent); }`
- `.field--ip:disabled { opacity: 1; color: color-mix(in srgb, var(--text-main) 55%, transparent); cursor: not-allowed; }`

- [ ] **Step 5: 构建并做 DOM 断言**

Run: `$env:TEMP=$env:TMP="D:\User\document\A_Myworkfiles\STM32\MyProject\AutoDebugTool\.tools\tmp"; npm run build`
Expected: `✓ built in …`，无 TypeScript/Vue 报错

然后用 preview 服务 + CDP 断言（沿用 `.tools/serve-dist.js` 与 `.tools/cdp-shot.js` 的 STUB）：
- 存在 `.field--ip`，默认空值、开关 `关`；
- 输入 `192.168.1` 后失焦 → 弹窗出现、输入框还原成空；
- 输入 `192.168.1.10` 后点开关 → 开关变 `开`，`window.go.main.App.SaveTaskConfig` 收到 `pingEnable: true, pingIP: "192.168.1.10"`；
- 覆盖 `GetTaskRuntime` 返回 `phase: 'ping', pingState: 'running'` → 「下一动作」显示 `PING 检测中`，输入框有 `is-ping`；
- 返回 `pingState: 'fail'` 且 `abnormalPause: true`、`running: true` → 同时有 `is-ping-fail` 与（2 秒内）`is-ping-flash`。

- [ ] **Step 6: Commit**

```bash
git add SerialMateX/frontend/src/App.vue
git commit -m "feat(ui): TASK SETTINGS 增加 PING 目标与开关，运行中高亮失败反馈"
```

---

### Task 8: 换肤时把窗口底色落盘

**Files:**
- Modify: `SerialMateX/frontend/src/App.vue:31`（import）、`:1339-1344`（`applySkin`）、`:1515-1530`（`onMounted`）
- Modify: `SerialMateX/frontend/wailsjs/go/main/App.js`、`SerialMateX/frontend/wailsjs/go/main/App.d.ts`

**Interfaces:**
- Consumes: `SaveUIBackground(hex string) string`（Task 6）
- Produces: 无（叶子任务）

- [ ] **Step 1: 手写绑定**

`App.js`（按文件里现有函数同样的两行格式，放在 `SaveTaskConfig` 之后）：

```js
export function SaveUIBackground(arg1) {
  return window['go']['main']['App']['SaveUIBackground'](arg1);
}
```

`App.d.ts`：

```ts
export function SaveUIBackground(arg1:string):Promise<string>;
```

- [ ] **Step 2: 调用**

- `App.vue:31` 的 import 列表加 `SaveUIBackground`。
- `applySkin` 末尾（localStorage 之后、`showSkinPanel.value = false` 之前）：

```ts
// 窗口底色只在 WebView2 画出第一帧之前露一下，所以要提前落盘、下次启动生效
void SaveUIBackground(theme.bgMain).catch(() => { /* 只读目录下忽略 */ });
```

- `onMounted` 里（`loadTaskConfig()` 附近）补一次 `void SaveUIBackground(theme.bgMain).catch(() => {});`，保证老用户升级后第一次启动就写出文件。

- [ ] **Step 3: 构建并断言**

Run: `$env:TEMP=$env:TMP="D:\User\document\A_Myworkfiles\STM32\MyProject\AutoDebugTool\.tools\tmp"; npm run build`
Expected: `✓ built in …`

CDP 断言（STUB 里把 `SaveUIBackground` 记到 `window.__uiBg`）：
- 启动后 `window.__uiBg` 至少有一次等于 Bench 的 `#F7F5EF`；
- 点皮肤面板里 Cyberpunk 那一项后，`window.__uiBg` 追加了该皮肤的 `--bg-main`，且 `localStorage['serialmatex.skin'] === 'cyberpunk'`。

- [ ] **Step 4: Commit**

```bash
git add SerialMateX/frontend/src/App.vue SerialMateX/frontend/wailsjs/go/main/App.js SerialMateX/frontend/wailsjs/go/main/App.d.ts
git commit -m "feat(ui): 换肤时把窗口底色写入 ui_config.ini"
```

---

### Task 9: 全量验收与桌面端重建

**Files:**
- 无源码改动；若验收发现问题，回到对应任务修。

**Interfaces:**
- Consumes: 全部

- [ ] **Step 1: 全量 Go 测试**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File .tools\gotest.ps1 -Run all`
Expected: `PASSED: serial-assistant, serial-assistant/pkg/jlink, serial-assistant/pkg/updater`

- [ ] **Step 2: 前端构建 + exe 构建**

Run:
```
$env:TEMP=$env:TMP="<ws>\.tools\tmp"; npm run build
cd SerialMateX; $env:GOFLAGS='-mod=mod'; $env:GOPROXY='https://goproxy.cn,direct'
go build -tags "desktop,production" -ldflags "-w -s -H windowsgui" -o build\bin\SerialMateX.exe .
```
Expected: exe 生成，体积 ~12.2 MB

- [ ] **Step 3: 冒烟**

- 复制到 `D:\smt-test\SerialMateX.exe`（工作区内有火绒的文件夹策略，非管理员启动会失败）。
- 非管理员启动：进程存活、标题 `SerialMateX`。
- `(Get-Item ...).VersionInfo` → `FileVersion 1.0.0.0`、`ProductName SerialMateX`。
- `ExtractAssociatedIcon` 位图哈希不是系统默认值（`418eaf0e…`）；测完用 `Stop-Process` 关掉。
- 若手动验证 PING：`task_config.ini` 写 `PingEnable=1` + 一个能 ping 通的地址，跑 1 轮，日志里应出现 `[PING …]` 与任务总结块。

- [ ] **Step 4: Commit（若有修复）**

```bash
git add -A
git commit -m "fix(task): 验收中发现的 PING/总结问题"
```

- [ ] **Step 5: 账本**

把本计划的完成情况追加到 `.superpowers/sdd/2026-11-16-task-power-serial/progress.md`（用 `[System.IO.File]::ReadAllText/WriteAllText` + `UTF8Encoding($false)`，不要用 `Set-Content`）。

---

## Self-Review

- **Spec coverage**：7.1 Task 2；7.2 Task 1+2+7；7.3 Task 1；7.4 Task 3+4+5；7.5 Task 3+4+5；7.6 Task 4+7；7.7 Task 4；7.8 Task 6+8；§8 测试计划逐条落到各任务；§9 的「留给实现者的决定」在对应步骤里带默认建议。
- **类型一致性**：`runCommands` 新签名 `(bool, string)` 只在 Task 3 定义、Task 4/5 使用；`pingProbe` / `pingPassed` / `pingLogLine` / `pingFailDetail`（**统一用 `pingFailDetail`，文档里没有 `pingFailureDetail` 这个别名**）名字在 Task 1 定义、Task 4/5 引用一致；`taskStats` 字段名在 Task 5 内自洽。
- **Review Focus 各有测试**：1→Task 4 `TestTaskPingToolMissingDoesNotCrash`；2→Task 2 `TestTaskCfgSaveAllowsDisableWithBadPingIP`；3→Task 4 `TestTaskStopDuringPingEndsQuickly`；4→Task 5 `TestTaskSummaryCapsFailureDetail`；5→Task 6 `TestSaveUIBackgroundReadOnlyDirDoesNotPanic`。