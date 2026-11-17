package main

import (
	"fmt"
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
	mu      sync.Mutex
	rt      TaskRuntime
	stopCh  chan struct{}
	resume  chan struct{}
	rxBuf   strings.Builder // 最近收到的被测设备数据（用于 RX 匹配）
	curVal  float64
	hasCurr bool
	csvCnt  int
	logPath string
	csvPath string
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

// StopTask 请求停止。
func (a *App) StopTask() string {
	t := &a.task
	t.mu.Lock()
	running := t.rt.Running
	if running && t.stopCh != nil {
		select {
		case <-t.stopCh:
		default:
			close(t.stopCh)
		}
	}
	t.mu.Unlock()
	if !running {
		return "任务未在运行"
	}
	return ""
}

// ResumeTask 解除“异常暂停”。
func (a *App) ResumeTask() string {
	t := &a.task
	t.mu.Lock()
	paused := t.rt.Paused && t.resume != nil
	resume := t.resume
	t.mu.Unlock()
	if !paused {
		return "当前没有暂停"
	}
	select {
	case resume <- struct{}{}:
	default:
	}
	return ""
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
	t.resume = make(chan struct{}, 1)
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
		appendFile(logPath, string(data))
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
			if logPath != "" {
				appendFile(logPath, taskLogBanner("第 "+strconv.Itoa(round)+" 次上电", device.Name))
			}
		} else if err := a.sendBytes(device.PowerOn, device.PowerOnHex, device.PowerOnEnter); err != nil {
			failMsg = "上电失败：" + err.Error()
			t.set(func(rt *TaskRuntime) { rt.Message = failMsg })
			aborted = true
			break
		} else {
			t.set(func(rt *TaskRuntime) { rt.PowerOn = true })
			if logPath != "" {
				appendFile(logPath, taskLogBanner("第 "+strconv.Itoa(round)+" 次上电", device.Name))
			}
		}

		if csvPath != "" {
			stopCsv = t.startCsvSampler(csvPath)
		}

		// 运行时间窗口：命令跑完也继续等满，时间到才掉电。
		deadline := time.Now().Add(time.Duration(cfg.WaitSeconds) * time.Second)
		// 命令执行期间也要实时显示“N 秒后掉电”，所以起一个独立的倒计时刷新
		stopTick := t.startDeadlineTicker(deadline)
		complete := a.runCommands(cmds, deadline)
		stopTick()

		if !t.countdown(deadline, "running", "powerOff") {
			t.set(func(rt *TaskRuntime) { rt.PowerOn = false })
			_ = a.sendBytes(device.PowerOff, device.PowerOffHex, device.PowerOffEnter)
			aborted = true
			break
		}

		if !complete && cfg.AbnormalPause {
			t.set(func(rt *TaskRuntime) {
				rt.Paused = true
				rt.Phase = "paused"
				rt.Message = "有命令未执行完，已暂停（电源保持）"
			})
			<-t.resume
			t.set(func(rt *TaskRuntime) { rt.Paused = false; rt.Message = "" })
		}

		// 掉电。写不出去说明电源口已经不可用，再跑下一轮没有意义，直接中止。
		offErr := a.sendBytes(device.PowerOff, device.PowerOffHex, device.PowerOffEnter)
		t.set(func(rt *TaskRuntime) { rt.PowerOn = false })
		if offErr != nil {
			failMsg = "掉电失败：" + offErr.Error()
			t.set(func(rt *TaskRuntime) { rt.Message = failMsg })
		}
		if logPath != "" {
			appendFile(logPath, taskLogBanner("第 "+strconv.Itoa(round)+" 次测试停止", device.Name))
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
		if failMsg != "" {
			rt.Message = failMsg
		} else {
			rt.Message = msg
		}
		rt.Running = false
		rt.Paused = false
		rt.Phase = "done"
		rt.PowerOn = false
		rt.Countdown = 0
		rt.CountdownTo = ""
		rt.CmdIndex = -1
		rt.CmdPhase = ""
		rt.CmdCountdown = 0
	})
}

// runCommands 在 deadline 之前依次执行命令；返回 true 表示全部执行完。
func (a *App) runCommands(cmds []AutoCmd, deadline time.Time) bool {
	t := &a.task
	for i, cmd := range cmds {
		if t.stopped() || !time.Now().Before(deadline) {
			return false
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
				return false
			}
		} else {
			// TX：统一走 encodeCmdPayload —— HEX 按字节解析，文本展开 {ENTER} / {UP} 之类功能键标记，
			// 再按 Enter 决定是否补一个 \r（命令后面常带空格，直接拼接会识别不到）。
			payload, encErr := encodeCmdPayload(cmd.Text, cmd.Hex, cmd.Enter)
			if encErr != nil {
				t.set(func(rt *TaskRuntime) { rt.Message = fmt.Sprintf("第 %d 条命令编码失败：%v", i+1, encErr) })
				return false
			}
			if res := a.taskSend(string(payload)); strings.HasPrefix(res, "Error") || strings.HasPrefix(res, "Send") {
				t.set(func(rt *TaskRuntime) { rt.Message = "发送失败：" + res })
				return false
			}
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
					return false
				}
			}
			if !time.Now().Before(deadline) {
				return false
			}
		}
	}

	t.set(func(rt *TaskRuntime) { rt.CmdIndex = -1; rt.CmdPhase = ""; rt.CmdCountdown = 0 })
	return true
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
