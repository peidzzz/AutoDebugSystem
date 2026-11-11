package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAutoCmdRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)

	app := &App{}
	// 文件不存在时自动创建模板
	if got := app.GetAutoCmds(); len(got) != 0 {
		t.Fatalf("模板里不应有命令，得到 %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, autoCmdFileName)); err != nil {
		t.Fatalf("配置文件未自动创建: %v", err)
	}

	want := []AutoCmd{
		{Type: "TX", Number: 0, Text: "AT+RST"},
		{Type: "RX", Number: 5, Text: "ready"},
		{Type: "TX", Number: 12, Text: "AT+BAUD=115200"},
		{Type: "RX", Number: 99, Text: "OK"},
	}
	if err := app.SaveAutoCmds(want); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("读回不一致: %#v", got)
	}

	// 换行会被压平，空行被丢弃
	if err := app.SaveAutoCmds([]AutoCmd{
		{Type: "TX", Number: 3, Text: "a"},
		{Type: "RX", Number: 0, Text: "b\nc"},
		{Type: "TX", Number: 0, Text: ""},
		{Type: "RX", Number: 0, Text: "   "},
	}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, []AutoCmd{
		{Type: "TX", Number: 3, Text: "a"},
		{Type: "RX", Number: 0, Text: "b c"},
	}) {
		t.Fatalf("换行/空行处理错误: %#v", got)
	}

	// 非法 Type 写盘时回落为 TX；负数夹紧为 0；超过两位夹紧为 99
	if err := app.SaveAutoCmds([]AutoCmd{{Type: "XX", Number: -5, Text: "ping"}}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, autoCmdFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(raw), "TX ping 0") {
		t.Fatalf("非法 Type / 负数未回落，文件内容: %q", string(raw))
	}

	if err := app.SaveAutoCmds([]AutoCmd{{Type: "RX", Number: 500, Text: "big"}}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, autoCmdFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(raw), "RX big 99") {
		t.Fatalf("超两位的数字没有夹紧为 99，文件内容: %q", string(raw))
	}
}

func TestAutoCmdsSkipComments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)

	body := "# 注释\n; 也是注释\nAT\r\n\r\nRX hello world\nTX:AT+TEST\nTX AT+DELAY 42\nTX 7 AT+LEGACY\n"
	if err := os.WriteFile(filepath.Join(dir, autoCmdFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	want := []AutoCmd{
		{Type: "TX", Number: 0, Text: "AT"}, // 没有前缀的老格式按 TX 处理
		{Type: "RX", Number: 0, Text: "hello world"},
		{Type: "TX", Number: 0, Text: "AT+TEST"},
		{Type: "TX", Number: 42, Text: "AT+DELAY"}, // 数字在行尾
		{Type: "TX", Number: 7, Text: "AT+LEGACY"}, // 旧格式：数字紧跟类型
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("注释/前缀/数字解析错误: %#v", got)
	}
}

// 只有数字、没有命令文本的行，按“命令就是那个数字”处理（数字仍为 0）
func TestAutoCmdNumericText(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, autoCmdFileName), []byte("TX 123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	want := []AutoCmd{{Type: "TX", Number: 0, Text: "123"}}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("纯数字命令解析错误: %#v", got)
	}
}

// 命令文本本身带数字（末尾不是数字、或末尾数字属于命令的一部分）时不应被吃掉
func TestAutoCmdTextKeepsInnerNumbers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)

	body := "TX AT+BAUD=115200 03\nRX ADC=12 7 08\n"
	if err := os.WriteFile(filepath.Join(dir, autoCmdFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	want := []AutoCmd{
		{Type: "TX", Number: 3, Text: "AT+BAUD=115200"},
		{Type: "RX", Number: 8, Text: "ADC=12 7"},
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("命令内数字被误拆: %#v", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestDecodeCmdText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AT+RST{ENTER}", "AT+RST\r"},
		{"{enter}", "\r"},
		{"{UP}{DOWN}{LEFT}{RIGHT}", "\x1b[A\x1b[B\x1b[D\x1b[C"},
		{"{TAB}x{ESC}{BACKSPACE}", "\tx\x1b\x7f"},
		{"{0x1B}y{0x0A}", "\x1by\n"},
		{"A{FOO}B", "A{FOO}B"},
		{"A{", "A{"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := decodeCmdText(c.in); got != c.want {
			t.Fatalf("decodeCmdText(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestBuildTxPayload(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AT+RST", "AT+RST\r"},
		{"AT+RST   ", "AT+RST\r"},
		{"AT+RST{ENTER}", "AT+RST\r"},
		{"AT+RST{LF}", "AT+RST\n"},
		{"AT+RST{ENTER}{UP}", "AT+RST\r\x1b[A\r"},
	}
	for _, c := range cases {
		if got := buildTxPayload(c.in); got != c.want {
			t.Fatalf("buildTxPayload(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestExportImportAutoCmds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)
	app := &App{}

	want := []AutoCmd{
		{Type: "TX", Number: 1, Text: "AT+RST{ENTER}"},
		{Type: "RX", Number: 7, Text: "ready"},
	}
	if err := app.SaveAutoCmds(want); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	exportDir := t.TempDir()
	out, err := app.ExportAutoCmdsTo(exportDir, time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if filepath.Base(out) != "auto_cmd_export_20261110_120000.txt" {
		t.Fatalf("导出文件名不对: %s", filepath.Base(out))
	}
	if _, statErr := os.Stat(out); statErr != nil {
		t.Fatalf("导出文件不存在: %v", statErr)
	}

	// 清空后再导入，应当恢复成导出时的内容
	if err := app.SaveAutoCmds(nil); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	n, impErr := app.ImportAutoCmdsFrom(out)
	if impErr != nil {
		t.Fatalf("导入失败: %v", impErr)
	}
	if n != len(want) {
		t.Fatalf("导入条数 %d, 期望 %d", n, len(want))
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("导入后内容不一致: %#v", got)
	}

	// 空文件应报错
	emptyFile := filepath.Join(exportDir, "empty.txt")
	if err := os.WriteFile(emptyFile, []byte("# nothing here\n"), 0o644); err != nil {
		t.Fatalf("写空文件失败: %v", err)
	}
	if _, err := app.ImportAutoCmdsFrom(emptyFile); err == nil {
		t.Fatal("空文件导入应当报错")
	}
}