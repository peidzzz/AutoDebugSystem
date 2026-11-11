package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// writeTaskFixtures 写一套临时配置，供下面几个引擎测试复用。
func writeTaskFixtures(t *testing.T, cfg, cmds, pwr string) {
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
