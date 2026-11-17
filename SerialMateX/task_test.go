package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"
)

func TestParseHexBytes(t *testing.T) {
	got, err := parseHexBytes("0x01 aa 0X0F")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 3 || got[0] != 0x01 || got[1] != 0xAA || got[2] != 0x0F {
		t.Fatalf("解析结果不对: %v", got)
	}
	for _, bad := range []string{"", "   ", "1234", "zz", "0x"} {
		if _, err := parseHexBytes(bad); err == nil {
			t.Fatalf("%q 应该报错", bad)
		}
	}
}

func TestFormatCurrent(t *testing.T) {
	cases := []struct {
		value float64
		has   bool
		want  string
	}{
		{0, false, ""},
		{0, true, "00.0"},
		{1.25, true, "01.3"},
		{12.34, true, "12.3"},
		{123.9, true, "123.9"},
	}
	for _, c := range cases {
		if got := formatCurrent(c.value, c.has); got != c.want {
			t.Fatalf("formatCurrent(%v,%v)=%q, 期望 %q", c.value, c.has, got, c.want)
		}
	}
}

func TestParseCurrentChunk(t *testing.T) {
	got, val, has := parseCurrentChunk("ADC=900 12.3 A\r\n", 0, false)
	if !has || got != "12.3" || val != 12.3 {
		t.Fatalf("应识别 12.3 A，实际 %q %v %v", got, val, has)
	}
	// 没有数字时保持上一次的值
	got, val, has = parseCurrentChunk("AT+RST", val, has)
	if !has || got != "12.3" {
		t.Fatalf("无数字时应保持原值，实际 %q %v %v", got, val, has)
	}
}

func TestTaskLogBanner(t *testing.T) {
	banner := taskLogBanner("第 1 次上电", "DemoBoard")
	if !strings.Contains(banner, "第 1 次上电") || !strings.Contains(banner, "DemoBoard") {
		t.Fatalf("标识内容缺失: %q", banner)
	}
	if !strings.Contains(banner, strings.Repeat("*", 60)) {
		t.Fatalf("标识没有被 * 号框住: %q", banner)
	}
}

// TestTaskEngineRounds 用假串口跑完整的轮次流程：
// 2 轮、每轮 1 秒、命令 TX + RX(ready)、开日志与功耗。
func TestTaskEngineRounds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)
	t.Setenv("SERIALMATEX_PWRDEV_DIR", dir)
	t.Setenv("SERIALMATEX_LOGDIR", dir)

	if err := os.WriteFile(filepath.Join(dir, "task_config.ini"), []byte(""+
		"[Task]\nSaveLog=1\nSavePower=1\nInitialState=0\nAbnormalPause=0\n"+
		"WaitSeconds=1\nDelaySeconds=0\nRunCount=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auto_cmd.txt"), []byte("TX AT+RST 00\nRX ready 00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pwr_dev.ini"), []byte(""+
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	installFakePwrPort(t)
	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard", EOL: "\r\n"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}

	// 模拟被测设备在每一轮都重新回 “ready”：RX 只认开始等待之后的数据，
	// 所以这里按轮次反复喂（而不是只喂一次）。
	stopFeed := make(chan struct{})
	defer close(stopFeed)
	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopFeed:
				return
			case <-ticker.C:
				app.task.feed([]byte("ready\r\n"))
			}
		}
	}()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rt := app.GetTaskRuntime()
		if !rt.Running {
			if rt.Round != 2 || rt.NormalCount != 2 {
				t.Fatalf("轮次/正常次数不对: round=%d normal=%d msg=%s", rt.Round, rt.NormalCount, rt.Message)
			}
			logs, _ := filepath.Glob(filepath.Join(dir, "DEBUGLOG_*.log"))
			csvs, _ := filepath.Glob(filepath.Join(dir, "CURRENT_*.csv"))
			// 保存功耗在当前版本固定为关：只应有日志，没有 CSV
			if len(logs) != 1 || len(csvs) != 0 {
				t.Fatalf("日志文件数量不对（不应生成 CSV）: %v %v", logs, csvs)
			}
			content, _ := os.ReadFile(logs[0])
			text := string(content)
			for _, want := range []string{"第 1 次上电", "第 2 次上电", "第 1 次测试停止", "第 2 次测试停止"} {
				if !strings.Contains(text, want) {
					t.Fatalf("日志里缺少 %q:\n%s", want, text)
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("任务没有在 20 秒内结束: %+v", app.GetTaskRuntime())
}

// TestTaskEngineRejectsWhenNotConnected 未连接串口时必须拒绝启动。
func TestTaskEngineRejectsWhenNotConnected(t *testing.T) {
	app := &App{}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "x"}); msg == "" {
		t.Fatal("未连接串口时不应允许启动")
	}
}

// TestTaskCsvFormatting 校验 50 个一行、逗号分隔。
func TestTaskCsvFormatting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.csv")
	_ = os.WriteFile(path, nil, 0o644)
	engine := &taskEngine{}
	for i := 0; i < 51; i++ {
		engine.csvAppend(path, "01.0")
	}
	content, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("应为 2 行（50 + 1），实际 %d 行", len(lines))
	}
	if len(strings.Split(lines[0], ",")) != 50 {
		t.Fatalf("第一行应有 50 个数据，实际 %d", len(strings.Split(lines[0], ",")))
	}
	if lines[1] != "01.0" {
		t.Fatalf("第二行内容不对: %q", lines[1])
	}
}

// fakePwrPort 是测试用的电源口：记录写进来的字节，并记住是否打开过 / 关闭过。
// 真实的串口驱动不能出现在测试里，pwrOpenHook / pwrWriteHook 是唯一的注入点。
type fakePwrPort struct {
	mu     sync.Mutex
	writes [][]byte
	opened bool
	closed bool
}

func (p *fakePwrPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes = append(p.writes, append([]byte(nil), b...))
	return len(b), nil
}

func (p *fakePwrPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *fakePwrPort) written() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.writes))
	copy(out, p.writes)
	return out
}

func (p *fakePwrPort) isOpened() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opened
}

func (p *fakePwrPort) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// installFakePwrPort 把打开电源口的实现换成假口，免得测试去碰真实 COM 口。
func installFakePwrPort(t *testing.T) *fakePwrPort {
	t.Helper()
	port := &fakePwrPort{}
	pwrOpenHook = func(PwrDevice) (io.WriteCloser, error) {
		port.mu.Lock()
		port.opened = true
		port.mu.Unlock()
		return port, nil
	}
	pwrWriteHook = nil
	t.Cleanup(func() {
		pwrOpenHook = nil
		pwrWriteHook = nil
	})
	return port
}

// waitPwrClosed 等关闭真正发生：Running 先置 false、defer 里的 closePwrDevice 在其后执行。
func waitPwrClosed(t *testing.T, port *fakePwrPort) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port.isClosed() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务结束后电源口没有被关闭")
}

// writeTaskFixtures 写一套临时配置，供下面几个引擎测试复用。
// 返回假的电源口：StartTask 现在会打开电源口，所有用到它的测试都得先装好 hook。
func writeTaskFixtures(t *testing.T, cfg, cmds, pwr string) *fakePwrPort {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)
	t.Setenv("SERIALMATEX_PWRDEV_DIR", dir)
	t.Setenv("SERIALMATEX_LOGDIR", dir)
	for name, content := range map[string]string{
		"task_config.ini": cfg,
		"auto_cmd.txt":    cmds,
		"pwr_dev.ini":     pwr,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return installFakePwrPort(t)
}

// waitTaskDone 等到任务停止；返回最后一次运行时快照。
func waitTaskDone(t *testing.T, app *App, timeout time.Duration) TaskRuntime {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rt := app.GetTaskRuntime()
		if !rt.Running {
			return rt
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("任务没有在 %v 内结束", timeout)
	return TaskRuntime{}
}

// TestTaskEngineCommandOrder 断言命令严格按列表顺序执行、每轮各一次，
// 并且 TX 发出的内容以单个 \r 结尾（多余的空格会被裁掉）。
func TestTaskEngineCommandOrder(t *testing.T) {
	writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=1\nAbnormalPause=0\nWaitSeconds=3\nDelaySeconds=0\nRunCount=2\n",
		"TX first  00\nRX ok 00\nTX second 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	var mu sync.Mutex
	var sent []string
	taskSendHook = func(s string) string {
		mu.Lock()
		sent = append(sent, s)
		mu.Unlock()
		return "Sent"
	}
	defer func() { taskSendHook = nil }()

	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}

	stopFeed := make(chan struct{})
	defer close(stopFeed)
	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopFeed:
				return
			case <-ticker.C:
				app.task.feed([]byte("ok\r\n"))
			}
		}
	}()

	rt := waitTaskDone(t, app, 25*time.Second)
	if rt.NormalCount != 2 {
		t.Fatalf("两轮都应算正常: normal=%d msg=%s", rt.NormalCount, rt.Message)
	}

	mu.Lock()
	got := append([]string(nil), sent...)
	mu.Unlock()

	var tx []string
	for _, s := range got {
		if s == "first\r" || s == "second\r" {
			tx = append(tx, s)
		}
	}
	want := []string{"first\r", "second\r", "first\r", "second\r"}
	if len(tx) != len(want) {
		t.Fatalf("TX 次数/内容不对: got=%v (all=%v)", tx, got)
	}
	for i := range want {
		if tx[i] != want[i] {
			t.Fatalf("第 %d 条 TX 不对: got=%q want=%q (all=%v)", i+1, tx[i], want[i], got)
		}
	}
}

// TestTaskEngineIgnoresStaleRx 第二轮不再有新数据时，不能再用第一轮的数据匹配 RX。
func TestTaskEngineIgnoresStaleRx(t *testing.T) {
	writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=1\nAbnormalPause=0\nWaitSeconds=2\nDelaySeconds=0\nRunCount=2\n",
		"RX ready 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}

	// 只在第一轮（前 1.6 秒）喂数据，第二轮故意不喂
	stopFeed := make(chan struct{})
	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(1600 * time.Millisecond)
		for {
			select {
			case <-stopFeed:
				return
			case <-timeout:
				return
			case <-ticker.C:
				app.task.feed([]byte("ready\r\n"))
			}
		}
	}()
	defer close(stopFeed)

	rt := waitTaskDone(t, app, 25*time.Second)
	if rt.NormalCount != 1 {
		t.Fatalf("第二轮没有新数据，正常次数应为 1，实际 %d（msg=%s）", rt.NormalCount, rt.Message)
	}
}

// TestTaskRuntimeCountdownPhases 运行窗口内显示“N 秒后掉电”，间隔阶段显示“N 秒后上电”。
func TestTaskRuntimeCountdownPhases(t *testing.T) {
	writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=1\nAbnormalPause=0\nWaitSeconds=2\nDelaySeconds=2\nRunCount=2\n",
		"TX ping 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	taskSendHook = func(string) string { return "Sent" }
	defer func() { taskSendHook = nil }()

	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}

	seen := map[string]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rt := app.GetTaskRuntime()
		if rt.Running && rt.Countdown > 0 && rt.CountdownTo != "" {
			seen[rt.CountdownTo] = true
		}
		if !rt.Running {
			break
		}
		if seen["powerOff"] && seen["powerOn"] {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}

	if !seen["powerOff"] {
		t.Fatalf("运行窗口内没有显示“N 秒后掉电”")
	}
	if !seen["powerOn"] {
		t.Fatalf("间隔阶段没有显示“N 秒后上电”")
	}
}

// TestTaskBuildSerialMode 抽出 buildSerialMode 后行为必须与原来的 OpenSerial 完全一致。
func TestTaskBuildSerialMode(t *testing.T) {
	mode := buildSerialMode(115200, 8, 2, "Even")
	if mode.BaudRate != 115200 || mode.DataBits != 8 || mode.Parity != serial.EvenParity || mode.StopBits != serial.TwoStopBits {
		t.Fatalf("串口参数映射不对: %+v", mode)
	}

	mode = buildSerialMode(9600, 8, 15, "None")
	if mode.Parity != serial.NoParity || mode.StopBits != serial.OnePointFiveStopBits {
		t.Fatalf("1.5 停止位映射不对: %+v", mode)
	}

	// 非法值按今天的行为回落到 无校验 / 1 位停止位
	mode = buildSerialMode(9600, 8, 7, "nope")
	if mode.Parity != serial.NoParity || mode.StopBits != serial.OneStopBit {
		t.Fatalf("非法值没有回落: %+v", mode)
	}
}

// TestTaskRejectsEmptyPwrPort 设备没配置电源口时必须在启动阶段拒绝，且不打开端口。
func TestTaskRejectsEmptyPwrPort(t *testing.T) {
	port := writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
		"TX ping 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	app := &App{isConnected: true, connType: TypeSerial}
	msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"})
	if msg == "" {
		t.Fatal("电源口为空时不应允许启动")
	}
	if !strings.Contains(msg, "没有配置 COM 端口") || !strings.Contains(msg, "DemoBoard") {
		t.Fatalf("错误信息不对: %q", msg)
	}
	if app.GetTaskRuntime().Running {
		t.Fatal("校验失败时任务不应启动")
	}
	if port.isOpened() {
		t.Fatal("校验失败时不应打开电源口")
	}
}

// TestTaskRejectsSamePwrPortAsSerial 电源口与 SERIAL 口相同时必须拒绝（大小写不敏感）。
func TestTaskRejectsSamePwrPortAsSerial(t *testing.T) {
	for _, serialPort := range []string{"COM7", "com7"} {
		t.Run(serialPort, func(t *testing.T) {
			port := writeTaskFixtures(t,
				"[Task]\nSaveLog=0\nSavePower=0\nInitialState=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
				"TX ping 00\n",
				"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
			)

			app := &App{isConnected: true, connType: TypeSerial, serialPortName: serialPort}
			msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"})
			if msg == "" {
				t.Fatal("电源口与 SERIAL 口相同时不应允许启动")
			}
			// 两个端口名都要出现在提示里（大小写不同也指向同一个口，比较时忽略大小写）
			if !strings.Contains(msg, "COM7") || !strings.Contains(strings.ToUpper(msg), strings.ToUpper(serialPort)) {
				t.Fatalf("错误信息里应含两个端口名: %q", msg)
			}
			if !strings.Contains(msg, "不能与 SERIAL 端口") {
				t.Fatalf("错误信息不对: %q", msg)
			}
			if app.GetTaskRuntime().Running || port.isOpened() {
				t.Fatal("校验失败时任务不应启动，也不应打开电源口")
			}
		})
	}
}

// TestTaskReportsPwrOpenError 打开电源口失败时把端口名与底层错误带回界面。
func TestTaskReportsPwrOpenError(t *testing.T) {
	port := writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
		"TX ping 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)
	_ = port

	pwrOpenHook = func(PwrDevice) (io.WriteCloser, error) {
		return nil, errors.New("端口被占用")
	}

	app := &App{isConnected: true, connType: TypeSerial}
	msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"})
	if msg == "" {
		t.Fatal("打开电源口失败时不应允许启动")
	}
	for _, want := range []string{"打开电源端口", "COM7", "端口被占用"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息里缺少 %q: %q", want, msg)
		}
	}
	if app.GetTaskRuntime().Running {
		t.Fatal("打开电源口失败时任务不应启动")
	}
}

// TestTaskSendsPowerOnOffBytes 一轮任务里上电 / 掉电字节要按设备的 HEX 与回车开关编码，
// 并且走电源口（不再走 SERIAL）。
func TestTaskSendsPowerOnOffBytes(t *testing.T) {
	cases := []struct {
		name     string
		extraPwr string
		wantOn   []byte
		wantOff  []byte
	}{
		{name: "默认 HEX 不加回车", extraPwr: "", wantOn: []byte{0x01}, wantOff: []byte{0x00}},
		{name: "上电追加回车", extraPwr: "PWR-ON-ENTER=1\n", wantOn: []byte{0x01, '\r'}, wantOff: []byte{0x00}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port := writeTaskFixtures(t,
				"[Task]\nSaveLog=0\nSavePower=0\nInitialState=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
				"TX ping 00\n",
				"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n"+c.extraPwr,
			)

			// 被测板卡的 TX 仍然走 SERIAL，这里用 hook 拦下，避免依赖真实串口。
			taskSendHook = func(string) string { return "Sent" }
			defer func() { taskSendHook = nil }()

			app := &App{isConnected: true, connType: TypeSerial}
			if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
				t.Fatalf("启动失败: %s", msg)
			}
			waitTaskDone(t, app, 20*time.Second)
			waitPwrClosed(t, port)

			writes := port.written()
			if len(writes) != 2 {
				t.Fatalf("一轮任务应只有上电 / 掉电两次写入，实际 %d 次: %q", len(writes), writes)
			}
			if !bytes.Equal(writes[0], c.wantOn) {
				t.Fatalf("上电字节不对: got=%v want=%v", writes[0], c.wantOn)
			}
			if !bytes.Equal(writes[1], c.wantOff) {
				t.Fatalf("掉电字节不对: got=%v want=%v", writes[1], c.wantOff)
			}
		})
	}
}

// TestTaskClosesPwrPortAfterRun 任务正常跑完必须关掉电源口。
func TestTaskClosesPwrPortAfterRun(t *testing.T) {
	port := writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
		"TX ping 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	taskSendHook = func(string) string { return "Sent" }
	defer func() { taskSendHook = nil }()

	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}
	rt := waitTaskDone(t, app, 20*time.Second)
	if rt.NormalCount != 1 {
		t.Fatalf("一轮应算正常: normal=%d msg=%s", rt.NormalCount, rt.Message)
	}
	waitPwrClosed(t, port)

	// 初始状态为开：这一轮只有掉电一次写入
	writes := port.written()
	if len(writes) != 1 || !bytes.Equal(writes[0], []byte{0x00}) {
		t.Fatalf("初始状态为开时不该再发上电: %q", writes)
	}
}

// TestTaskClosesPwrPortWhenStopped 中途停止（不同路径）同样要关掉电源口。
func TestTaskClosesPwrPortWhenStopped(t *testing.T) {
	port := writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=1\nAbnormalPause=0\nWaitSeconds=5\nDelaySeconds=0\nRunCount=1\n",
		"TX ping 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	taskSendHook = func(string) string { return "Sent" }
	defer func() { taskSendHook = nil }()

	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}
	if msg := app.StopTask(); msg != "" {
		t.Fatalf("停止失败: %s", msg)
	}
	waitTaskDone(t, app, 10*time.Second)
	waitPwrClosed(t, port)
}

// TestTaskAbortsWhenPwrWriteFails 电源口写入失败必须中止任务，并把原因留在 Message 里。
func TestTaskAbortsWhenPwrWriteFails(t *testing.T) {
	port := writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nSavePower=0\nInitialState=0\nAbnormalPause=0\nWaitSeconds=5\nDelaySeconds=0\nRunCount=1\n",
		"TX ping 00\n",
		"[DemoBoard]\nName=DemoBoard\nPort=COM7\nPWR-ON=0x01\nPWR-OFF=0x00\n",
	)

	pwrWriteHook = func([]byte) error { return errors.New("串口已拔出") }

	app := &App{isConnected: true, connType: TypeSerial}
	if msg := app.StartTask(TaskRunOptions{DeviceName: "DemoBoard"}); msg != "" {
		t.Fatalf("启动失败: %s", msg)
	}
	rt := waitTaskDone(t, app, 10*time.Second)
	if rt.NormalCount != 0 {
		t.Fatalf("写失败的一轮不能算正常: normal=%d", rt.NormalCount)
	}
	if !strings.Contains(rt.Message, "电源口写入失败") || !strings.Contains(rt.Message, "串口已拔出") {
		t.Fatalf("中止原因不对: %q", rt.Message)
	}
	waitPwrClosed(t, port)
}

// TestTaskWritePwrPortWithoutOpen 未打开时返回错误而不 panic；重复关闭也安全。
func TestTaskWritePwrPortWithoutOpen(t *testing.T) {
	pwrOpenHook = nil
	pwrWriteHook = nil
	defer func() { pwrOpenHook = nil; pwrWriteHook = nil }()

	app := &App{}
	if err := app.writePwrPort([]byte{0x01}); err == nil {
		t.Fatal("电源口未打开时应返回错误")
	}
	// closePwrDevice 必须幂等：defer 与错误路径都可能调用它
	app.closePwrDevice()
	app.closePwrDevice()
}
