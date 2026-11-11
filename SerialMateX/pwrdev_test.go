package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParsePwrDevFile 验证 pwr_dev.ini 的解析：段名兜底、键名大小写/写法兼容。
func TestParsePwrDevFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwr_dev.ini")
	content := `; 注释行
[BoardA]
Name=板卡 A
Port=COM7
PWR-ON=0x01
PWR-OFF=0x00

[BoardB]
port=COM11
pwr_on=AA 01
poweroff=AA 00

[BoardC]
设备名称=测试设备
COM端口=COM12
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	devices, err := parsePwrDevFile(path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(devices) != 3 {
		t.Fatalf("want 3 devices, got %d: %+v", len(devices), devices)
	}

	a := devices[0]
	if a.Name != "板卡 A" || a.Port != "COM7" || a.PowerOn != "0x01" || a.PowerOff != "0x00" {
		t.Errorf("BoardA mismatch: %+v", a)
	}

	// 段名兜底为设备名，键名大小写不敏感，下划线/短横线等价
	b := devices[1]
	if b.Name != "BoardB" || b.Port != "COM11" || b.PowerOn != "AA 01" || b.PowerOff != "AA 00" {
		t.Errorf("BoardB mismatch: %+v", b)
	}

	// 中文键名
	c := devices[2]
	if c.Name != "测试设备" || c.Port != "COM12" {
		t.Errorf("BoardC mismatch: %+v", c)
	}
}

// TestPwrDevTemplateIsParsable 确认自动生成的模板能被解析出至少一台设备。
func TestPwrDevTemplateIsParsable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwr_dev.ini")
	if err := os.WriteFile(path, []byte(pwrDevTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	devices, err := parsePwrDevFile(path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "DemoBoard" || devices[0].Port != "COM7" {
		t.Fatalf("template parse mismatch: %+v", devices)
	}
}

// TestGetPwrDevicesCreatesIni 验证：文件不存在时 GetPwrDevices 会自动创建模板并读出一台设备，
// 且第二次调用不会覆盖用户改过的内容。
func TestGetPwrDevicesCreatesIni(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_PWRDEV_DIR", dir)

	app := &App{}
	devices := app.GetPwrDevices()
	if len(devices) != 1 || devices[0].Name != "DemoBoard" {
		t.Fatalf("auto-create mismatch: %+v", devices)
	}

	iniPath := filepath.Join(dir, "pwr_dev.ini")
	if _, err := os.Stat(iniPath); err != nil {
		t.Fatalf("pwr_dev.ini not created: %v", err)
	}

	custom := "[MyBoard]\nName=我的板卡\nPort=COM5\nPWR-ON=0x11\nPWR-OFF=0x22\n"
	if err := os.WriteFile(iniPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	devices = app.GetPwrDevices()
	if len(devices) != 1 || devices[0].Name != "我的板卡" || devices[0].Port != "COM5" || devices[0].PowerOn != "0x11" {
		t.Fatalf("custom file mismatch: %+v", devices)
	}
}
