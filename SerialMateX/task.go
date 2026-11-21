package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 任务执行引擎：按 AutoCmd 列表驱动一轮轮“上电 → 跑命令 → 运行时间 → 掉电 → 间隔”，
// 同时把被测设备的 COM 输出写进日志、把电流采样写进 CSV，并把状态实时推给界面。
//
// 设计要点：
//   - 单 goroutine 顺序执行，用 deadline 驱动倒计时，不额外开定时器（CSV 采样除外）；
//   - 日志 / CSV 每次写入都是「打开 → 追加 → 关闭」，不持有文件句柄，
//     其它程序可以随时打开查看，满足“记录过程中不占用该日志文件”；
//   - 状态放在 taskEngine.rt 里，由 GetTaskRuntime() 供界面轮询。

// TaskRunOptions 是界面点“启动”时传进来的参数。
type TaskRunOptions struct {
	DeviceName string `json:"deviceName"` // 电源设备名（pwr_dev.ini 里的 [Name]）
	EOL        string `json:"eol"`        // TX 命令追加的换行："" / "\n" / "\r" / "\r\n"
}

// TaskRuntime 是任务运行的实时状态快照（TASK STATUS 面板用）。
type TaskRuntime struct {
	Running      bool   `json:"running"`      // 是否正在跑
	Paused       bool   `json:"paused"`       // 是否因异常暂停
	PausedReason string `json:"pausedReason"` // 暂停原因："abnormal" 表示有命令没跑完；空串表示没暂停
	Round        int    `json:"round"`        // 当前轮次（从 1 开始，未运行 0）
	RunCount     int    `json:"runCount"`     // 总轮次
	NormalCount  int    `json:"normalCount"`  // 正常完成轮次
	PowerOn      bool   `json:"powerOn"`      // 电源状态
	Current      string `json:"current"`      // 当前电流（已格式化，"" 表示 NULL）
	Phase        string `json:"phase"`        // idling / running / commands / interval / paused / done
	Countdown    int    `json:"countdown"`    // 阶段倒计时剩余秒
	CountdownTo  string `json:"countdownTo"`  // powerOn / powerOff / ""
	CmdIndex     int    `json:"cmdIndex"`     // 当前命令下标，-1 表示不在命令阶段
	CmdPhase     string `json:"cmdPhase"`     // exec / wait / ""
	CmdCountdown int    `json:"cmdCountdown"` // 当前命令数字的倒计时剩余秒
	LogPath      string `json:"logPath"`      // 本次日志文件路径（未开日志为空）
	CsvPath      string `json:"csvPath"`      // 本次功耗文件路径（未开功耗为空）
	Message      string `json:"message"`      // 结束原因等提示
}

// taskEngine 持有运行状态与串口送来的数据。
type taskEngine struct {
	mu       sync.Mutex
	logMu    sync.Mutex // 串行化任务日志的写入（任务 goroutine 与读串口 goroutine 并发写）
	rt       TaskRuntime
	stopCh   chan struct{}
	decision chan string     // 异常暂停时的决定："end" 结束任务 / "skip" 跳过本轮
	rxBuf    strings.Builder // 最近收到的被测设备数据（用于 RX 匹配）
	curVal   float64
	hasCurr  bool
	csvCnt   int
	logPath  string
	csvPath  string
}

// --- 提供给前端的接口 ---

// StartTask 启动任务；返回空串表示成功，否则是给界面显示的失败原因。
func (a *App) StartTask(opts TaskRunOptions) string {
	t := &a.task
	t.mu.Lock()
	if t.rt.Running {
		t.mu.Unlock()
		return "任务已在运行中"
	}
	t.mu.Unlock()

	if !a.isConnected || a.connType != TypeSerial {
		return "请先在 SERIAL 页连接 COM 口"
	}

	cmds := a.GetAutoCmds()
	// 丢掉空命令：否则会多发一个空回车，看起来像“多跑了一条命令”
	filtered := make([]AutoCmd, 0, len(cmds))
	for _, c := range cmds {
		if strings.TrimSpace(c.Text) != "" {
			filtered = append(filtered, c)
		}
	}
	cmds = filtered
	if len(cmds) == 0 {
		return "AUTO CMD 里还没有命令"
	}

	cfg := a.GetTaskConfig()
	device, ok := a.findPwrDevice(opts.DeviceName)
	if !ok {
		return "没有找到电源设备：" + opts.DeviceName
	}

	// 电源指令走设备自己的 COM 口，所以必须配端口，而且不能和 SERIAL 口是同一个。
	if strings.TrimSpace(device.Port) == "" {
		return "设备 " + device.Name + " 没有配置 COM 端口"
	}
	if strings.EqualFold(device.Port, a.serialPortName) {
		return "电源端口 " + device.Port + " 不能与 SERIAL 端口相同"
	}

	// 上电 / 掉电先各编码一次：非法内容在启动时就报出来，而不是跑到一半才失败。
	if _, err := encodeCmdPayload(device.PowerOn, device.PowerOnHex, device.PowerOnEnter); err != nil {
		return "PWR-ON 编码失败：" + err.Error()
	}
	if _, err := encodeCmdPayload(device.PowerOff, device.PowerOffHex, device.PowerOffEnter); err != nil {
		return "PWR-OFF 编码失败：" + err.Error()
	}

	// 打开电源口；失败时立刻返回，不留下半启动的任务。
	if err := a.openPwrDevice(device); err != nil {
		return "打开电源端口 " + device.Port + " 失败：" + err.Error()
	}

	t.reset(cfg)
	go a.runTask(cfg, cmds, device, opts)
	return ""
}

// StopTask 请求停止。异常暂停时任务 goroutine 正卡在等待决定上，所以走与 EndTask
// 相同的收尾路径（投 "end"），否则它会一直等下去、界面也会留下一个永远跑不完的任务。
func (a *App) StopTask() string {
	t := &a.task
	t.mu.Lock()
	running := t.rt.Running
	paused := t.rt.Paused
	decision := t.decision
	t.mu.Unlock()
	if !running {
		return "任务未在运行"
	}
	if paused && decision != nil {
		select {
		case decision <- "end":
			// 投递成功也顺手关掉 stopCh：万一 runTask 刚好在这几微秒里结束了暂停，
			// 这次“停止”依然会生效。
			t.closeStop()
			return ""
		default:
		}
		// 通道里已经有一个决定（连点了两下），或者暂停刚好结束：
		// 退回普通停止路径，关掉 stopCh 两边都能兜住，这次点击不会丢。
	}
	t.closeStop()
	return ""
}

// EndTask 结束整个任务：异常暂停中先掉电，然后不再跑下一轮。
func (a *App) EndTask() string {
	if !a.postPauseDecision("end") {
		return "当前不在异常暂停状态"
	}
	// 投递成功也关掉 stopCh：暂停恰好结束的极小窗口里，这次“结束”同样要生效。
	a.task.closeStop()
	return ""
}

// SkipRound 跳过本轮：掉电、照常走间隔倒计时，然后继续下一轮，但这一轮不计正常次数。
// 这里不能像 EndTask 那样顺手关 stopCh：runTask 在间隔前会检查 stopped()，
// 关掉就会把“跳过”退化成“结束任务”。
func (a *App) SkipRound() string {
	if !a.postPauseDecision("skip") {
		return "当前不在异常暂停状态"
	}
	return ""
}

// ResumeTask 是 SkipRound 的兼容别名：旧界面的「继续」按钮还在调它。
func (a *App) ResumeTask() string {
	if !a.postPauseDecision("skip") {
		return "当前没有暂停"
	}
	return ""
}

// postPauseDecision 在“异常暂停”中投递一次决定。通道容量只有 1：
// 连点两次时以最后一次为准（先丢掉还没被取走的旧决定），任务 goroutine 不会卡住。
func (a *App) postPauseDecision(action string) bool {
	t := &a.task
	t.mu.Lock()
	paused := t.rt.Running && t.rt.Paused
	decision := t.decision
	t.mu.Unlock()
	if !paused || decision == nil {
		return false
	}
	select {
	case decision <- action:
		return true
	default:
	}
	select {
	case <-decision:
	default:
	}
	select {
	case decision <- action:
		return true
	default:
		return false
	}
}

// closeStop 关闭 stopCh（已关闭则跳过）。暂停决定投递成功后也调用它，
// 保证“结束 / 停止”在暂停刚好结束的极小窗口里同样生效。
func (t *taskEngine) closeStop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopCh == nil {
		return
	}
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
}

// GetTaskRuntime 返回状态快照（界面每 200ms 轮询一次）。
func (a *App) GetTaskRuntime() TaskRuntime {
	t := &a.task
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rt
}

// --- 内部实现 ---

func (t *taskEngine) reset(cfg TaskConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rt = TaskRuntime{
		Running:     true,
		RunCount:    cfg.RunCount,
		CmdIndex:    -1,
		Phase:       "running",
		LogPath:     t.logPath,
		CsvPath:     t.csvPath,
		CountdownTo: "powerOff",
		Current:     "",
	}
	t.rxBuf.Reset()
	t.curVal = 0
	t.hasCurr = false
	t.csvCnt = 0
	t.stopCh = make(chan struct{})
	t.decision = make(chan string, 1)
}

func (t *taskEngine) set(fn func(rt *TaskRuntime)) {
	t.mu.Lock()
	fn(&t.rt)
	t.mu.Unlock()
}

func (t *taskEngine) stopped() bool {
	select {
	case <-t.stopCh:
		return true
	default:
		return false
	}
}

// sleep 等待 d，期间可被停止打断；返回 true 表示正常等完。
func (t *taskEngine) sleep(d time.Duration) bool {
	if d <= 0 {
		return !t.stopped()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.stopCh:
		return false
	case <-timer.C:
		return true
	}
}

// clearRx 清空接收缓冲：RX 只认“开始等待之后”到达的数据。
func (t *taskEngine) clearRx() {
	t.mu.Lock()
	t.rxBuf.Reset()
	t.mu.Unlock()
}

// startDeadlineTicker 在运行窗口内持续刷新“N 秒后掉电”，
// 这样命令还在执行时 TASK STATUS 也能看到掉电倒计时（返回的函数用于停止刷新）。
func (t *taskEngine) startDeadlineTicker(deadline time.Time) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.stopCh:
				return
			case <-ticker.C:
				remaining := int(time.Until(deadline).Seconds() + 0.999)
				if remaining < 0 {
					remaining = 0
				}
				t.set(func(rt *TaskRuntime) {
					if rt.Phase == "paused" {
						return
					}
					rt.Countdown = remaining
					rt.CountdownTo = "powerOff"
				})
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// countdown 以 1 秒粒度刷新阶段倒计时，直到到点或停止。
func (t *taskEngine) countdown(until time.Time, phase, to string) bool {
	last := -1
	for {
		remaining := int(time.Until(until).Seconds() + 0.999)
		if remaining < 0 {
			remaining = 0
		}
		if remaining != last {
			last = remaining
			t.set(func(rt *TaskRuntime) {
				rt.Phase = phase
				rt.Countdown = remaining
				rt.CountdownTo = to
			})
		}
		if remaining == 0 {
			return true
		}
		if !t.sleep(100 * time.Millisecond) {
			return false
		}
	}
}

// waitForMatch 在 until 之前等待 RX 数据里出现 needle（needle 为空视为立刻匹配）。
func (t *taskEngine) waitForMatch(needle string, until time.Time) bool {
	for {
		t.mu.Lock()
		buf := t.rxBuf.String()
		t.mu.Unlock()
		if needle == "" || strings.Contains(buf, needle) {
			return true
		}
		if !time.Now().Before(until) {
			return false
		}
		if !t.sleep(50 * time.Millisecond) {
			return false
		}
	}
}

// feed 由读串口线程调用，喂入被测设备数据（同时用于 RX 匹配、日志、电流解析）。
func (t *taskEngine) feed(data []byte) {
	t.mu.Lock()
	if !t.rt.Running {
		t.mu.Unlock()
		return
	}
	t.rxBuf.Write(data)
	if t.rxBuf.Len() > 16384 {
		s := t.rxBuf.String()
		t.rxBuf.Reset()
		t.rxBuf.WriteString(s[len(s)-8192:])
	}
	t.rt.Current, t.curVal, t.hasCurr = parseCurrentChunk(string(data), t.curVal, t.hasCurr)
	logPath := t.logPath
	t.mu.Unlock()

	if logPath != "" {
		t.appendTaskLog(logPath, string(data))
	}
}

// appendFile 打开 → 追加 → 关闭，不长期占用文件。
func appendFile(path, text string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(text)
	_ = f.Close()
}

// appendTaskLog 是任务日志的统一写入口：日志按“打开 → 追加 → 关闭”写，
// 任务 goroutine（横幅 / TX 行）和读串口 goroutine（RX 数据）会并发写同一个文件，
// 必须用 logMu 串行化，否则两段内容会交错。
func (t *taskEngine) appendTaskLog(logPath, text string) {
	if logPath == "" {
		return
	}
	t.logMu.Lock()
	defer t.logMu.Unlock()
	appendFile(logPath, text)
}

// appendMarkedLog 写一行带标记的日志（line 含末尾换行）：日志里的 RX 数据可能不带换行
// （甚至什么都还没写），不加前置换行就会把这一行粘在上一段 RX 字节后面。
// “看最后一个字节 + 追加”必须在同一把锁里完成，否则 RX 数据可能插在中间，
// 前置换行就白算了（会得到 ...\nOK[TX ...] 这种粘在一起的日志）。
func (t *taskEngine) appendMarkedLog(logPath, line string) {
	if logPath == "" {
		return
	}
	t.logMu.Lock()
	defer t.logMu.Unlock()
	prefix := ""
	if last, ok := lastFileByte(logPath); ok && last != '\n' {
		prefix = "\n"
	}
	appendFile(logPath, prefix+line)
}

// appendTxLog 把任务发出的一条 TX 命令记进日志：[TX 15:04:05] 正文。
// at 由调用方统一取一次时间，保证日志行和 task-tx 事件的时间一致。
func (t *taskEngine) appendTxLog(logPath, at, text string) {
	t.appendMarkedLog(logPath, "[TX "+at+"] "+text+"\n")
}

// cmdLogLine 是「这一轮命令没跑完」的日志行，写清第几轮、失败在哪一步。
func cmdLogLine(at string, round int, reason string) string {
	return "[CMD " + at + "] 第 " + strconv.Itoa(round) + " 次测试未完成：" + reason + "\n"
}

// lastFileByte 读文件的最后一个字节，用来判断日志是否已经以换行结尾。
func lastFileByte(path string) (byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if _, err := f.Seek(-1, io.SeekEnd); err != nil {
		return 0, false
	}
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil {
		return 0, false
	}
	return b[0], true
}

// csvAppend 每 0.5 秒写一个采样点：英文逗号分隔，每 50 个换行。
func (t *taskEngine) csvAppend(path, value string) {
	t.csvCnt++
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	idx := t.csvCnt - 1
	switch {
	case idx == 0:
		_, _ = f.WriteString(value)
	case idx%50 == 0:
		_, _ = f.WriteString("\n" + value)
	default:
		_, _ = f.WriteString("," + value)
	}
	_ = f.Close()
}

// startCsvSampler 在“已上电”期间每 0.5 秒记录一次电流；返回停止函数。
func (t *taskEngine) startCsvSampler(path string) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				t.mu.Lock()
				powered := t.rt.PowerOn
				value := t.rt.Current
				t.mu.Unlock()
				if !powered {
					continue
				}
				if value == "" {
					value = "NULL"
				}
				t.csvAppend(path, value)
			}
		}
	}()
	return func() { close(done) }
}

// parseCurrentChunk 从一段数据里挑出电流值（形如 "12.3" / "12.3A" / "12.3 A"），没有则保持原值。
func parseCurrentChunk(chunk string, last float64, has bool) (string, float64, bool) {
	fields := strings.Fields(chunk)
	for i := len(fields) - 1; i >= 0; i-- {
		field := strings.TrimSuffix(strings.TrimSuffix(fields[i], "A"), "a")
		if field == "" {
			continue
		}
		if v, err := strconv.ParseFloat(field, 64); err == nil {
			return formatCurrent(v, true), v, true
		}
	}
	return formatCurrent(last, has), last, has
}

// formatCurrent 按“两位整数 + 一位小数”格式化；无数据返回空串（界面显示 NULL）。
func formatCurrent(v float64, has bool) string {
	if !has {
		return ""
	}
	tenths := int(v*10 + 0.5)
	return fmt.Sprintf("%02d.%d", tenths/10, tenths%10)
}

// runTask 是一轮轮测试的主循环。
func (a *App) runTask(cfg TaskConfig, cmds []AutoCmd, device PwrDevice, opts TaskRunOptions) {
	// 电源口在 StartTask 里打开，这里保证任务无论从哪条路径结束都会关闭它。
	defer a.closePwrDevice()

	t := &a.task
	dir := taskLogDir()

	logPath := ""
	if cfg.SaveLog {
		logPath = filepath.Join(dir, "DEBUGLOG_"+time.Now().Format("2006-01-02_15-04-05")+".log")
		_ = os.MkdirAll(dir, 0o755)
		appendFile(logPath, "")
	}
	csvPath := ""
	// 当前版本“保存功耗”恒为关（界面已置灰），这里再兜一层，避免旧配置把 CSV 打开
	if cfg.SavePower && savePowerEnabled {
		csvPath = filepath.Join(dir, "CURRENT_"+time.Now().Format("2006-01-02_15-04-05")+".csv")
		_ = os.MkdirAll(dir, 0o755)
		appendFile(csvPath, "")
	}

	t.mu.Lock()
	t.logPath, t.csvPath = logPath, csvPath
	t.rt.LogPath, t.rt.CsvPath = logPath, csvPath
	t.mu.Unlock()

	aborted := false
	// failMsg 记下真正的失败原因（上电/掉电/电源口写入），结束时优先显示它。
	failMsg := ""
	// endMsg 记下用户主动“结束任务”，排在 failMsg 之后、完成语之前显示。
	endMsg := ""
	var stopCsv func()

	for round := 1; round <= cfg.RunCount; round++ {
		t.set(func(rt *TaskRuntime) {
			rt.Round = round
			rt.CmdIndex = -1
			rt.CmdPhase = ""
			rt.CmdCountdown = 0
			rt.Phase = "running"
			rt.Message = ""
			rt.Countdown = cfg.WaitSeconds
			rt.CountdownTo = "powerOff"
		})
		// 每轮开始丢掉上一轮残留的接收数据，避免用旧数据判断 RX
		t.clearRx()

		// 上电：第一轮若“初始状态”为开，则不发 ON 命令，直接进入运行。
		if round == 1 && cfg.InitialState {
			t.set(func(rt *TaskRuntime) { rt.PowerOn = true })
			t.appendTaskLog(logPath, taskLogBanner("第 "+strconv.Itoa(round)+" 次上电", device.Name))
		} else if err := a.sendBytes(device.PowerOn, device.PowerOnHex, device.PowerOnEnter); err != nil {
			failMsg = "上电失败：" + err.Error()
			t.set(func(rt *TaskRuntime) { rt.Message = failMsg })
			aborted = true
			break
		} else {
			t.set(func(rt *TaskRuntime) { rt.PowerOn = true })
			t.appendTaskLog(logPath, taskLogBanner("第 "+strconv.Itoa(round)+" 次上电", device.Name))
		}

		if csvPath != "" {
			stopCsv = t.startCsvSampler(csvPath)
		}

		// 运行时间窗口：命令跑完也继续等满，时间到才掉电。
		deadline := time.Now().Add(time.Duration(cfg.WaitSeconds) * time.Second)
		// 命令执行期间也要实时显示“N 秒后掉电”，所以起一个独立的倒计时刷新
		stopTick := t.startDeadlineTicker(deadline)
		complete, failReason := a.runCommands(cmds, deadline)
		stopTick()

		// 命令没跑完时先留一行 [CMD]，写清第几轮、卡在第几条命令；
		// 否则「正常次数比总轮次少 1」在没开异常暂停时完全没有痕迹。
		if !complete && logPath != "" {
			t.appendMarkedLog(logPath, cmdLogLine(time.Now().Format("15:04:05"), round, failReason))
		}

		if !t.countdown(deadline, "running", "powerOff") {
			t.set(func(rt *TaskRuntime) { rt.PowerOn = false })
			_ = a.sendBytes(device.PowerOff, device.PowerOffHex, device.PowerOffEnter)
			aborted = true
			break
		}

		// 异常暂停：等界面给出决定。停止（stopCh）也按“结束任务”处理，
		// 所以无论用户点结束、跳过还是停止，这个 goroutine 都不会被永久挂住。
		endRequested := false
		skipRound := false
		if !complete && cfg.AbnormalPause {
			t.set(func(rt *TaskRuntime) {
				rt.Paused = true
				rt.Phase = "paused"
				rt.PausedReason = "abnormal"
				rt.Message = "有命令未执行完，已暂停（电源保持）"
			})
			var action string
			select {
			case action = <-t.decision:
			case <-t.stopCh:
				action = "end"
			}
			t.set(func(rt *TaskRuntime) {
				rt.Paused = false
				rt.PausedReason = ""
				rt.Message = ""
			})
			if action == "end" {
				// 掉电、写停止横幅，然后结束整个任务（不再跑下一轮）。
				endRequested = true
			}
			// action == "skip" 落到下面的掉电 / 间隔流程，只是跳过 NormalCount++。
			skipRound = action == "skip"
		}

		// 掉电。写不出去说明电源口已经不可用，再跑下一轮没有意义，直接中止。
		offErr := a.sendBytes(device.PowerOff, device.PowerOffHex, device.PowerOffEnter)
		t.set(func(rt *TaskRuntime) { rt.PowerOn = false })
		if offErr != nil {
			failMsg = "掉电失败：" + offErr.Error()
			t.set(func(rt *TaskRuntime) { rt.Message = failMsg })
		}
		if logPath != "" {
			t.appendTaskLog(logPath, taskLogBanner("第 "+strconv.Itoa(round)+" 次测试停止", device.Name))
		}
		if stopCsv != nil {
			stopCsv()
			stopCsv = nil
		}
		if complete && offErr == nil {
			t.set(func(rt *TaskRuntime) { rt.NormalCount++ })
		}
		if offErr != nil {
			aborted = true
			break
		}
		if endRequested {
			aborted = true
			endMsg = "已手动结束"
			break
		}
		// 最后一轮被跳过：没有下一轮可跑，明确告诉用户任务是因为“跳过”而结束的。
		if skipRound && round == cfg.RunCount {
			endMsg = "已跳过最后一轮，任务结束"
			break
		}

		if round == cfg.RunCount || t.stopped() {
			if t.stopped() {
				aborted = true
			}
			break
		}

		// 间隔时间：TASK STATUS 显示“N 秒后上电”。
		if !t.countdown(time.Now().Add(time.Duration(cfg.DelaySeconds)*time.Second), "interval", "powerOn") {
			aborted = true
			break
		}
	}

	if stopCsv != nil {
		stopCsv()
	}

	msg := "任务完成"
	if aborted {
		msg = "任务已停止"
	}
	t.set(func(rt *TaskRuntime) {
		// 中途失败时保留具体原因（上电失败 / 掉电失败 / 电源口写入失败），不要被完成语覆盖掉。
		switch {
		case failMsg != "":
			rt.Message = failMsg
		case endMsg != "":
			rt.Message = endMsg
		default:
			rt.Message = msg
		}
		rt.Running = false
		rt.Paused = false
		rt.PausedReason = ""
		rt.Phase = "done"
		rt.PowerOn = false
		rt.Countdown = 0
		rt.CountdownTo = ""
		rt.CmdIndex = -1
		rt.CmdPhase = ""
		rt.CmdCountdown = 0
	})
}

// runCommands 在 deadline 之前依次执行命令；complete 为 false 时 reason 说明卡在哪一步
// （写给日志的 [CMD] 行，用户靠它知道是第几条命令出的问题）。
func (a *App) runCommands(cmds []AutoCmd, deadline time.Time) (bool, string) {
	t := &a.task
	for i, cmd := range cmds {
		if t.stopped() {
			return false, fmt.Sprintf("命令 %d 未执行：任务已停止", i+1)
		}
		if !time.Now().Before(deadline) {
			return false, fmt.Sprintf("运行时间内没轮到命令 %d（%s %q）", i+1, cmd.Type, cmd.Text)
		}

		t.set(func(rt *TaskRuntime) {
			rt.Phase = "commands"
			rt.CmdIndex = i
			rt.CmdPhase = "exec"
			rt.CmdCountdown = 0
		})

		if strings.EqualFold(cmd.Type, "RX") {
			// 只认“开始等待之后”收到的数据：先清空缓冲，避免上一轮/上一条的旧数据立刻命中
			t.clearRx()
			if !t.waitForMatch(cmd.Text, deadline) {
				if t.stopped() {
					return false, fmt.Sprintf("命令 %d 未执行完：任务已停止", i+1)
				}
				return false, fmt.Sprintf("命令 %d（%s %q）在运行时间内没收到匹配数据", i+1, cmd.Type, cmd.Text)
			}
		} else {
			// TX：统一走 encodeCmdPayload —— HEX 按字节解析，文本展开 {ENTER} / {UP} 之类功能键标记，
			// 再按 Enter 决定是否补一个 \r（命令后面常带空格，直接拼接会识别不到）。
			payload, encErr := encodeCmdPayload(cmd.Text, cmd.Hex, cmd.Enter)
			if encErr != nil {
				t.set(func(rt *TaskRuntime) { rt.Message = fmt.Sprintf("第 %d 条命令编码失败：%v", i+1, encErr) })
				return false, fmt.Sprintf("命令 %d（%s %q）编码失败：%v", i+1, cmd.Type, cmd.Text, encErr)
			}
			text := string(payload)
			if res := a.taskSend(text); strings.HasPrefix(res, "Error") || strings.HasPrefix(res, "Send") {
				t.set(func(rt *TaskRuntime) { rt.Message = "发送失败：" + res })
				return false, fmt.Sprintf("命令 %d（%s %q）发送失败：%s", i+1, cmd.Type, cmd.Text, res)
			}
			// 发送成功才记录：日志里留一行 [TX 时:分:秒] 正文，同时推给界面高亮显示。
			// 日志行与事件用同一个字符串：显示的是命令原文里的功能键标记（{UP} / {ENTER}），
			// 而不是发出去的 ESC 序列 —— 后者在日志里就是乱码。时间也只取一次。
			logText := formatCmdLogText(cmd.Text, cmd.Hex)
			at := time.Now().Format("15:04:05")
			t.mu.Lock()
			logPath := t.logPath
			t.mu.Unlock()
			t.appendTxLog(logPath, at, logText)
			a.emitTaskTx(at, logText)
		}

		// 执行完成后高亮数字并倒计时。
		if cmd.Number > 0 {
			until := time.Now().Add(time.Duration(cmd.Number) * time.Second)
			t.set(func(rt *TaskRuntime) { rt.CmdPhase = "wait"; rt.CmdCountdown = cmd.Number })
			for {
				remaining := int(time.Until(until).Seconds() + 0.999)
				if remaining < 0 {
					remaining = 0
				}
				t.set(func(rt *TaskRuntime) { rt.CmdCountdown = remaining })
				if remaining == 0 || !time.Now().Before(deadline) {
					break
				}
				if !t.sleep(100 * time.Millisecond) {
					return false, fmt.Sprintf("命令 %d 未执行完：任务已停止", i+1)
				}
			}
			if !time.Now().Before(deadline) {
				return false, fmt.Sprintf("运行时间在命令 %d 之后用完，剩余命令未执行", i+1)
			}
		}
	}

	t.set(func(rt *TaskRuntime) { rt.CmdIndex = -1; rt.CmdPhase = ""; rt.CmdCountdown = 0 })
	return true, ""
}

// taskLogBanner 生成带 * 框的起始 / 停止标识，便于在日志里一眼定位。
func taskLogBanner(title, device string) string {
	star := strings.Repeat("*", 60)
	return "\n" + star + "\n" +
		"* " + title + "\n" +
		"* 电源设备：" + device + "\n" +
		"* 时间：" + time.Now().Format("2006-01-02 15:04:05") + "\n" +
		star + "\n"
}

// taskLogDir 返回日志 / CSV 目录（默认 exe 同目录，可用 SERIALMATEX_LOGDIR 覆盖）。
func taskLogDir() string {
	if dir := os.Getenv("SERIALMATEX_LOGDIR"); dir != "" {
		return dir
	}
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exePath)
}

// findPwrDevice 在 pwr_dev.ini 里按名字找设备。
func (a *App) findPwrDevice(name string) (PwrDevice, bool) {
	for _, dev := range a.GetPwrDevices() {
		if dev.Name == name {
			return dev, true
		}
	}
	return PwrDevice{}, false
}

// sendBytes 把电源指令按设备配置（HEX / 回车）编码后写到电源口。
func (a *App) sendBytes(text string, asHex, withEnter bool) error {
	payload, err := encodeCmdPayload(text, asHex, withEnter)
	if err != nil {
		return err
	}
	return a.writePwrPort(payload)
}

// taskSendHook 只给测试用：替换真正的发送实现，便于断言发出的字节。
var taskSendHook func(string) string

// savePowerEnabled 控制“保存功耗”是否可用。
// 当前版本按需求把它固定在“关”：界面置灰，CSV 采样也不会启动。
const savePowerEnabled = false

// taskSend 发一段原始数据（测试时可被 taskSendHook 拦下）。
func (a *App) taskSend(data string) string {
	if taskSendHook != nil {
		return taskSendHook(data)
	}
	return a.SendData(data)
}

// parseHexBytes 解析空格分隔的十六进制字节串。
func parseHexBytes(text string) ([]byte, error) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return nil, fmt.Errorf("内容为空")
	}
	out := make([]byte, 0, len(fields))
	for _, f := range fields {
		clean := strings.TrimPrefix(strings.TrimPrefix(f, "0x"), "0X")
		if clean == "" || len(clean) > 2 {
			return nil, fmt.Errorf("非法字节 %q", f)
		}
		v, err := strconv.ParseUint(clean, 16, 8)
		if err != nil {
			return nil, fmt.Errorf("非法字节 %q", f)
		}
		out = append(out, byte(v))
	}
	return out, nil
}
