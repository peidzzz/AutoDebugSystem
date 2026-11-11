package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSavePwrDevicesRoundTrip 验证保存后再读回，4 项信息（含空格分隔的多个十六进制数）保持不变。
func TestSavePwrDevicesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_PWRDEV_DIR", dir)

	app := &App{}
	devices := []PwrDevice{
		{Name: "BoardA", Port: "COM7", PowerOn: "0x01 0x02 AA", PowerOff: "0x00 0x00"},
		{Name: "BoardB", Port: "COM11", PowerOn: "AA 01", PowerOff: "AA 00"},
	}
	if err := app.SavePwrDevices(devices); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := app.GetPwrDevices()
	if len(got) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(got), got)
	}
	if got[0] != devices[0] {
		t.Errorf("device0 mismatch:\n got %+v\nwant %+v", got[0], devices[0])
	}
	if got[1] != devices[1] {
		t.Errorf("device1 mismatch:\n got %+v\nwant %+v", got[1], devices[1])
	}

	// 文件里应当有可读的 PWR-ON 行
	raw, err := os.ReadFile(filepath.Join(dir, "pwr_dev.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(string(raw), "[BoardA]", "Port=COM7", "PWR-ON=0x01 0x02 AA", "PWR-OFF=0x00 0x00") {
		t.Errorf("unexpected ini content:\n%s", raw)
	}
}

// TestSavePwrDevicesRejectsDuplicates 验证重名会被拒绝。
func TestSavePwrDevicesRejectsDuplicates(t *testing.T) {
	t.Setenv("SERIALMATEX_PWRDEV_DIR", t.TempDir())
	app := &App{}
	err := app.SavePwrDevices([]PwrDevice{
		{Name: "Same", Port: "COM1"},
		{Name: "Same", Port: "COM2"},
	})
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

// TestTaskConfigDefaultsAndRoundTrip 验证默认值、保存与再次读取。
func TestTaskConfigDefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)

	app := &App{}
	cfg := app.GetTaskConfig()
	if cfg.WaitSeconds != defaultWaitSeconds || cfg.DelaySeconds != defaultDelaySeconds || cfg.RunCount != defaultRunCount {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, taskCfgFileName)); err != nil {
		t.Fatalf("task_config.ini not created: %v", err)
	}

	want := TaskConfig{SaveLog: true, SavePower: false, WaitSeconds: 12, DelaySeconds: 3, RunCount: 7}
	if err := app.SaveTaskConfig(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := app.GetTaskConfig()
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestTaskConfigClamps 验证越界值被收敛到合法区间。
func TestTaskConfigClamps(t *testing.T) {
	t.Setenv("SERIALMATEX_TASKCFG_DIR", t.TempDir())
	app := &App{}
	if err := app.SaveTaskConfig(TaskConfig{WaitSeconds: -5, DelaySeconds: 999999, RunCount: 0}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := app.GetTaskConfig()
	if got.WaitSeconds != 0 || got.DelaySeconds != maxTaskSeconds || got.RunCount != 1 {
		t.Fatalf("clamp mismatch: %+v", got)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for i := 0; i+len(part) <= len(text); i++ {
			if text[i:i+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
