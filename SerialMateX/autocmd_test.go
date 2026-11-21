package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		{Type: "TX", Number: 0, Text: "AT+RST", Enter: true},
		{Type: "RX", Number: 5, Text: "ready", Enter: true},
		{Type: "TX", Number: 12, Text: "AT+BAUD=115200", Enter: true},
		{Type: "RX", Number: 99, Text: "OK", Enter: true},
	}
	if err := app.SaveAutoCmds(want); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("读回不一致: %#v", got)
	}

	// 换行会被压平，空行被丢弃
	if err := app.SaveAutoCmds([]AutoCmd{
		{Type: "TX", Number: 3, Text: "a", Enter: true},
		{Type: "RX", Number: 0, Text: "b\nc", Enter: true},
		{Type: "TX", Number: 0, Text: ""},
		{Type: "RX", Number: 0, Text: "   "},
	}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, []AutoCmd{
		{Type: "TX", Number: 3, Text: "a", Enter: true},
		{Type: "RX", Number: 0, Text: "b c", Enter: true},
	}) {
		t.Fatalf("换行/空行处理错误: %#v", got)
	}

	// 非法 Type 写盘时回落为 TX；负数夹紧为 0；超过两位夹紧为 99
	if err := app.SaveAutoCmds([]AutoCmd{{Type: "XX", Number: -5, Text: "ping", Enter: true}}); err != nil {
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
		{Type: "TX", Number: 0, Text: "AT", Enter: true}, // 没有前缀的老格式按 TX 处理
		{Type: "RX", Number: 0, Text: "hello world", Enter: true},
		{Type: "TX", Number: 0, Text: "AT+TEST", Enter: true},
		{Type: "TX", Number: 42, Text: "AT+DELAY", Enter: true}, // 数字在行尾
		{Type: "TX", Number: 7, Text: "AT+LEGACY", Enter: true}, // 旧格式：数字紧跟类型
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
	want := []AutoCmd{{Type: "TX", Number: 0, Text: "123", Enter: true}}
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
		{Type: "TX", Number: 3, Text: "AT+BAUD=115200", Enter: true},
		{Type: "RX", Number: 8, Text: "ADC=12 7", Enter: true},
	}
	if got := app.GetAutoCmds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("命令内数字被误拆: %#v", got)
	}
}

// parseAutoCmdLine 要按行尾的 hex= / enter= 关键字填充两个开关，缺省为 false / true。
func TestAutoCmdKeywordsParse(t *testing.T) {
	cases := []struct {
		name string
		line string
		want AutoCmd
	}{
		{
			name: "老行：默认不 HEX、自动回车",
			line: "TX AT+RST 00",
			want: AutoCmd{Type: "TX", Number: 0, Text: "AT+RST", Hex: false, Enter: true},
		},
		{
			name: "hex=1 写在延时数字之后",
			line: "TX 01 02 03 00 hex=1",
			want: AutoCmd{Type: "TX", Number: 0, Text: "01 02 03", Hex: true, Enter: true},
		},
		{
			name: "enter=0 写在延时数字之后",
			line: "TX hello 05 enter=0",
			want: AutoCmd{Type: "TX", Number: 5, Text: "hello", Hex: false, Enter: false},
		},
		{
			name: "文本中间的 AT+HEX=1 不是关键字",
			line: "TX AT+HEX=1 00",
			want: AutoCmd{Type: "TX", Number: 0, Text: "AT+HEX=1", Hex: false, Enter: true},
		},
	}
	for _, c := range cases {
		if got := parseAutoCmdLine(c.line); got != c.want {
			t.Errorf("%s: parseAutoCmdLine(%q) = %#v, 期望 %#v", c.name, c.line, got, c.want)
		}
	}
}

// RX 行是文本包含匹配：行尾的 hex= / enter= 要被吞掉（不能留在匹配文本里，也不能破坏行尾延时数字），
// 但开关必须钉回默认值（Hex=false、Enter=true）。
func TestAutoCmdRxIgnoresKeywords(t *testing.T) {
	got := parseAutoCmdLine("RX ready 05 hex=1 enter=0")
	want := AutoCmd{Type: "RX", Number: 5, Text: "ready", Hex: false, Enter: true}
	if got != want {
		t.Fatalf("RX 行应吞掉关键字并保持默认开关：parseAutoCmdLine 得到 %#v, 期望 %#v", got, want)
	}
}

// Hex / Enter 四种组合经 formatAutoCmds 写盘再读回必须不变；
// 关键字只在非默认时出现（Hex 为真写 hex=1，Enter 为假写 enter=0），RX 行一个都不写。
func TestAutoCmdHexEnterFormatRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		cmd  AutoCmd
	}{
		{"默认（不写关键字）", AutoCmd{Type: "TX", Number: 0, Text: "AT+RST", Hex: false, Enter: true}},
		{"只有 hex", AutoCmd{Type: "TX", Number: 0, Text: "01 02 03", Hex: true, Enter: true}},
		{"只有 enter=0", AutoCmd{Type: "TX", Number: 5, Text: "AT+RST", Hex: false, Enter: false}},
		{"hex 与 enter=0 都写", AutoCmd{Type: "TX", Number: 7, Text: "AA BB", Hex: true, Enter: false}},
	}
	for _, c := range cases {
		formatted := formatAutoCmds([]AutoCmd{c.cmd})
		var line string
		for _, l := range strings.Split(formatted, "\n") {
			if strings.HasPrefix(l, "TX ") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%s: 没有写出 TX 行，内容 %q", c.name, formatted)
		}
		if gotHexKw, wantHexKw := strings.Contains(line, "hex="), c.cmd.Hex; gotHexKw != wantHexKw {
			t.Errorf("%s: 行 %q 的 hex 关键字出现=%v, 期望 %v", c.name, line, gotHexKw, wantHexKw)
		}
		if gotEnterKw, wantEnterKw := strings.Contains(line, "enter="), !c.cmd.Enter; gotEnterKw != wantEnterKw {
			t.Errorf("%s: 行 %q 的 enter 关键字出现=%v, 期望 %v", c.name, line, gotEnterKw, wantEnterKw)
		}

		got := parseAutoCmdsText(formatted)
		if len(got) != 1 {
			t.Fatalf("%s: 读回条数 %d, 期望 1（内容 %q）", c.name, len(got), formatted)
		}
		if got[0] != c.cmd {
			t.Errorf("%s: 读回 %#v, 期望 %#v", c.name, got[0], c.cmd)
		}
	}

	// RX 行永远不写关键字，读回时两个开关取默认值
	rx := AutoCmd{Type: "RX", Number: 5, Text: "ready", Enter: true}
	formatted := formatAutoCmds([]AutoCmd{rx})
	var rxLine string
	for _, l := range strings.Split(formatted, "\n") {
		if strings.HasPrefix(l, "RX ") {
			rxLine = l
			break
		}
	}
	if rxLine == "" {
		t.Fatalf("没有写出 RX 行，内容 %q", formatted)
	}
	if strings.Contains(rxLine, "hex=") || strings.Contains(rxLine, "enter=") {
		t.Fatalf("RX 行不应写 hex=/enter= 关键字，行内容 %q", rxLine)
	}
	if got := parseAutoCmdsText(formatted); len(got) != 1 || got[0] != rx {
		t.Fatalf("RX 行读回 %#v, 期望 %#v", got, rx)
	}
}

// encodeCmdPayload 是唯一的载荷编码入口：HEX 走 parseHexBytes，文本走 decodeCmdText，最后按需补回车。
func TestAutoCmdEncodePayload(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		asHex     bool
		withEnter bool
		want      string
		wantErr   bool
	}{
		{"HEX 字节", "AA BB", true, false, "\xaa\xbb", false},
		{"HEX 也按 Enter 补回车", "AA BB", true, true, "\xaa\xbb\r", false},
		{"HEX 内容非法", "ZZ", true, true, "", true},
		{"文本补回车", "AT", false, true, "AT\r", false},
		{"已有 LF 不再补回车", "AT\n", false, true, "AT\n", false},
		{"不要回车", "AT", false, false, "AT", false},
		{"展开功能键后裁掉尾部空白", "AT+{ENTER}   ", false, true, "AT+\r", false},
	}
	for _, c := range cases {
		got, err := encodeCmdPayload(c.text, c.asHex, c.withEnter)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: encodeCmdPayload(%q, %v, %v) 应当报错，得到 % x", c.name, c.text, c.asHex, c.withEnter, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 意外错误: %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s: encodeCmdPayload(%q, %v, %v) = %q, 期望 %q", c.name, c.text, c.asHex, c.withEnter, string(got), c.want)
		}
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

// formatCmdLogText 是日志与界面里显示 TX 命令用的形态：功能键标记原样保留，
// 裸控制字符写成 {0xNN}，HEX 命令按字节列表显示。
func TestFormatCmdLogText(t *testing.T) {
	cases := []struct {
		text  string
		asHex bool
		want  string
	}{
		{"AT+RST{UP}{ENTER}", false, "AT+RST{UP}{ENTER}"},
		{"AT+RST   ", false, "AT+RST"},
		{"a\rb\x1b", false, "a{0x0D}b{0x1B}"},
		{"A{FOO}B", false, "A{FOO}B"},
		{"A{", false, "A{"},
		{"0x01 0xaa", true, "0x01 0xaa"},
		{"0x01   0xaa  ", true, "0x01 0xaa"},
	}
	for _, c := range cases {
		if got := formatCmdLogText(c.text, c.asHex); got != c.want {
			t.Fatalf("formatCmdLogText(%q, %v) = %q, 期望 %q", c.text, c.asHex, got, c.want)
		}
	}
}

func TestExportImportAutoCmds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_AUTOCMD_DIR", dir)
	app := &App{}

	want := []AutoCmd{
		{Type: "TX", Number: 1, Text: "AT+RST{ENTER}", Enter: true},
		{Type: "RX", Number: 7, Text: "ready", Enter: true},
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
