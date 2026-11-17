package main

import (
	"os"
	"path/filepath"
	"strings"
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

// TestPwrDevDefaultsForOldSection 验证只有 4 个老键的段在解析后拿到今天的缺省行为：
// 9600/8/1/None，上电与掉电都按十六进制解析、都不追加回车。
func TestPwrDevDefaultsForOldSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwr_dev.ini")
	content := `[OldBoard]
Name=OldBoard
Port=COM3
PWR-ON=0x01
PWR-OFF=0x00
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	devices, err := parsePwrDevFile(path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("want 1 device, got %d: %+v", len(devices), devices)
	}

	d := devices[0]
	if d.Baud != 9600 || d.DataBits != 8 || d.StopBits != 1 || d.Parity != "None" {
		t.Errorf("serial defaults mismatch: %+v", d)
	}
	if !d.PowerOnHex || d.PowerOnEnter || !d.PowerOffHex || d.PowerOffEnter {
		t.Errorf("switch defaults mismatch: %+v", d)
	}
}

// TestPwrDevParsesNewKeys 验证新增的串口参数键与 HEX/回车开关能被解析，停止位 1.5 归一为 15。
func TestPwrDevParsesNewKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwr_dev.ini")
	content := `[BoardNew]
Name=BoardNew
Port=COM9
BAUD=115200
DATA=7
STOP=1.5
PARITY=Odd
PWR-ON=0x02
PWR-ON-HEX=0
PWR-ON-ENTER=1
PWR-OFF=0x03
PWR-OFF-HEX=1
PWR-OFF-ENTER=0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	devices, err := parsePwrDevFile(path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("want 1 device, got %d: %+v", len(devices), devices)
	}

	d := devices[0]
	if d.Baud != 115200 || d.DataBits != 7 || d.StopBits != 15 || d.Parity != "Odd" {
		t.Errorf("serial params mismatch: %+v", d)
	}
	if d.PowerOnHex || !d.PowerOnEnter || !d.PowerOffHex || d.PowerOffEnter {
		t.Errorf("switch values mismatch: %+v", d)
	}
}

// TestPwrDevKeyAliases 验证中文/下划线写法的键与短横线写法等价。
func TestPwrDevKeyAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwr_dev.ini")
	content := `[AliasBoard]
波特率=57600
数据位=8
停止位=2
校验位=Even
上电HEX=0
上电回车=1
PWR_OFF_HEX=0
PWR_OFF_ENTER=1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	devices, err := parsePwrDevFile(path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("want 1 device, got %d: %+v", len(devices), devices)
	}

	d := devices[0]
	if d.Baud != 57600 || d.DataBits != 8 || d.StopBits != 2 || d.Parity != "Even" {
		t.Errorf("alias serial params mismatch: %+v", d)
	}
	if d.PowerOnHex || !d.PowerOnEnter || d.PowerOffHex || !d.PowerOffEnter {
		t.Errorf("alias switch values mismatch: %+v", d)
	}
}

// TestPwrDevStopBitsEquivalent 验证 STOP=15（内部值）与 STOP=1.5 解析成同一个内部值。
func TestPwrDevStopBitsEquivalent(t *testing.T) {
	for _, raw := range []string{"1.5", "15"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "pwr_dev.ini")
		content := "[Board]\nPort=COM2\nSTOP=" + raw + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		devices, err := parsePwrDevFile(path)
		if err != nil {
			t.Fatalf("STOP=%s parse error: %v", raw, err)
		}
		if len(devices) != 1 || devices[0].StopBits != 15 {
			t.Errorf("STOP=%s: want stopBits 15, got %+v", raw, devices)
		}
	}
}

// TestPwrDevSaveParseRoundTrip 验证保存再解析时 12 个键原样回来，
// 包括两个 HEX 开关、两个回车开关，且文件里停止位写成 1.5。
func TestPwrDevSaveParseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_PWRDEV_DIR", dir)
	app := &App{}

	want := PwrDevice{
		Name:          "BoardX",
		Port:          "COM9",
		Baud:          57600,
		DataBits:      7,
		StopBits:      15,
		Parity:        "Even",
		PowerOn:       "0x02 0x03",
		PowerOff:      "AA 00",
		PowerOnHex:    false,
		PowerOnEnter:  true,
		PowerOffHex:   true,
		PowerOffEnter: true,
	}
	if err := app.SavePwrDevices([]PwrDevice{want}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := app.GetPwrDevices()
	if len(got) != 1 {
		t.Fatalf("want 1 device, got %d: %+v", len(got), got)
	}
	if got[0] != want {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got[0], want)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "pwr_dev.ini"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"Name=BoardX", "Port=COM9", "BAUD=57600", "DATA=7", "STOP=1.5",
		"PARITY=Even", "PWR-ON=0x02 0x03", "PWR-ON-HEX=0", "PWR-ON-ENTER=1",
		"PWR-OFF=AA 00", "PWR-OFF-HEX=1", "PWR-OFF-ENTER=1",
	} {
		if !strings.Contains(string(raw), line) {
			t.Errorf("ini missing %q:\n%s", line, raw)
		}
	}
}

// TestPwrDevParseBoolText 验证布尔文本的 Accepted 写法与非法输入。
func TestPwrDevParseBoolText(t *testing.T) {
	cases := []struct {
		in   string
		want bool
		ok   bool
	}{
		{"1", true, true},
		{"0", false, true},
		{"true", true, true},
		{"false", false, true},
		{"on", true, true},
		{"off", false, true},
		{"是", true, true},
		{"否", false, true},
		{" TRUE ", true, true},
		{"", false, false},
		{"yes", false, false},
		{"2", false, false},
	}
	for _, c := range cases {
		got, ok := parseBoolText(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseBoolText(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
