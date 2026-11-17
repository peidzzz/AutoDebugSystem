package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// auto_cmd.txt：右侧栏 "AUTO CMD" 的命令列表，一行一条命令。
// 用纯文本而不是 INI，因为命令里常常带有 '=' / ':' / 空格，任何分隔符都会打架。
//
// 每行格式为 “TYPE 命令文本 数字”，TYPE 取 TX 或 RX，数字放在行尾（两位，00 ~ 99）：
//
//	TX AT+RST 00
//	RX ready 05
//
// 为了兼容手写的旧文件：没有 TX/RX 前缀的行按 TX 处理；行尾没有数字时数字按 0 处理；
// 第 34 轮那种 “数字在类型后面” 的格式（“TX 0 AT+RST”）也能读，写回时会自动改成新格式。
const autoCmdFileName = "auto_cmd.txt"

// maxAutoCmdNumber 是数字框允许的最大值：界面上只保留两位。
const maxAutoCmdNumber = 99

// 导入 / 导出时用到的错误。
var (
	errNoAutoCmdInFile = errors.New("文件里没有可用的命令")
	errBadExportDir    = errors.New("导出目录无效")
)

const autoCmdTemplate = `# SerialMateX AUTO CMD 命令列表
# 一行一条命令，格式：TX <命令文本> <数字> / RX <命令文本> <数字>
# 数字在行尾，两位（00 ~ 99），默认 00
# 行尾可加 hex=1（按十六进制字节发送）/ enter=0（不自动补回车），只对 TX 行有效
# TX 用绿色显示，RX 用红色显示；以 # 或 ; 开头的行是注释，不会被载入
# 示例：
# TX AT+RST 00
# TX 01 02 03 00 hex=1
# RX ready 05

`

// AutoCmd 是一条自动命令：Type 为 "TX" 或 "RX"，Number 为行尾数字（00 ~ 99，默认 0），Text 为命令内容。
// Hex 表示 TX 内容按十六进制字节解析，Enter 表示 TX 末尾自动补 \r；两者只对 TX 有意义，
// 缺省为 Hex=false、Enter=true，写盘时只在非默认时写 hex=1 / enter=0。
type AutoCmd struct {
	Type   string `json:"type"`
	Number int    `json:"number"`
	Text   string `json:"text"`
	Hex    bool   `json:"hex"`
	Enter  bool   `json:"enter"`
}

// AutoCmdPath 返回 auto_cmd.txt 的路径：优先环境变量，否则与可执行文件同目录。
func AutoCmdPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("SERIALMATEX_AUTOCMD_DIR")); dir != "" {
		return filepath.Join(dir, autoCmdFileName), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), autoCmdFileName), nil
}

// ensureAutoCmdFile 在文件不存在时写入模板，保证用户能找到并手工编辑它。
func (a *App) ensureAutoCmdFile() error {
	path, err := AutoCmdPath()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return nil
	}
	return writeFileAtomic(path, autoCmdTemplate)
}

// GetAutoCmds 读取命令列表；注释行与空行会被跳过。
func (a *App) GetAutoCmds() []AutoCmd {
	path, err := AutoCmdPath()
	if err != nil {
		return []AutoCmd{}
	}
	_ = a.ensureAutoCmdFile()

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return []AutoCmd{}
	}
	return parseAutoCmdsText(string(data))
}

// parseAutoCmdLine 解析一行 “TX AT+RST 00” / “RX ready 05”；没有前缀时按 TX 处理。
// 行尾的 hex=1 / enter=0 关键字会先被削掉（RX 行同样吞掉、但不生效），再按行尾数字取延时；
// 数字拆不出来时数字为 0，关键字缺省为 Hex=false、Enter=true。
// 另外兼容第 34 轮 “TX 0 AT+RST”（数字紧跟在类型后面）的写法。
func parseAutoCmdLine(line string) AutoCmd {
	// 支持 “TX xxx”、“TX:xxx”、“TX=xxx”
	for _, sep := range []string{" ", "\t", ":", "="} {
		if idx := strings.Index(line, sep); idx > 0 {
			tag := strings.ToUpper(strings.TrimSpace(line[:idx]))
			if tag == "TX" || tag == "RX" {
				return parseAutoCmdBody(tag, strings.TrimSpace(line[idx+len(sep):]))
			}
		}
	}
	return parseAutoCmdBody("TX", line)
}

// parseAutoCmdBody 按类型解析 “命令文本 [数字]”：TX / RX 都先削掉行尾的 hex= / enter= 关键字。
// RX 是文本包含匹配：关键字要被吞掉（否则会留在匹配文本里、还会拆丢行尾延时数字），
// 但不许改变行为，两个开关一律钉回缺省值 Hex=false、Enter=true。
func parseAutoCmdBody(tag, rest string) AutoCmd {
	body, asHex, withEnter := splitAutoCmdKeywords(rest)
	cmd := mkat(tag, body)
	if tag == "RX" {
		cmd.Hex, cmd.Enter = false, true
		return cmd
	}
	cmd.Hex, cmd.Enter = asHex, withEnter
	return cmd
}

// splitAutoCmdKeywords 从行尾往前削掉 hex= / enter= 关键字，返回清理后的命令文本与两个已带缺省的值。
// 只认整词（大小写不敏感，值接受 1/0、true/false、on/off、是/否），并且必须从行尾连续匹配：
// 遇到第一个不是关键字的词就停，所以文本中间的 “AT+HEX=1” 不会被误吞。
func splitAutoCmdKeywords(rest string) (body string, asHex bool, withEnter bool) {
	body = strings.TrimSpace(rest)
	asHex, withEnter = false, true
	for {
		i := strings.LastIndexAny(body, " \t")
		token, head := body, ""
		if i >= 0 {
			token, head = body[i+1:], strings.TrimSpace(body[:i])
		}

		name, value, ok := strings.Cut(strings.ToLower(token), "=")
		if !ok {
			break
		}
		flag, ok := parseCmdKeywordBool(value)
		if !ok {
			break
		}
		switch name {
		case "hex":
			asHex = flag
		case "enter":
			withEnter = flag
		default:
			return strings.TrimSpace(body), asHex, withEnter
		}
		body = head
	}
	return strings.TrimSpace(body), asHex, withEnter
}

// parseCmdKeywordBool 解析 hex= / enter= 的取值：1/0、true/false、on/off、是/否，大小写不敏感。
func parseCmdKeywordBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on", "是":
		return true, true
	case "0", "false", "off", "否":
		return false, true
	}
	return false, false
}

// mkat 把 “命令文本 [数字]”（数字在行尾）拆成 AutoCmd；拆不出来时整段当文本、数字为 0。
func mkat(tag, rest string) AutoCmd {
	text := strings.TrimSpace(rest)
	if text == "" {
		return AutoCmd{Type: tag, Number: 0, Text: ""}
	}

	// 新格式：数字在行尾
	if i := strings.LastIndexAny(text, " \t"); i > 0 {
		head, last := strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:])
		if head != "" && isAllDigits(last) {
			if n, err := strconv.Atoi(last); err == nil {
				return AutoCmd{Type: tag, Number: clampInt(n, 0, maxAutoCmdNumber), Text: head}
			}
		}
	}

	// 兼容旧格式：数字紧跟在类型后面（“TX 0 AT+RST”）
	if i := strings.IndexAny(text, " \t"); i > 0 {
		first, tail := text[:i], strings.TrimSpace(text[i+1:])
		if tail != "" && isAllDigits(first) {
			if n, err := strconv.Atoi(first); err == nil {
				return AutoCmd{Type: tag, Number: clampInt(n, 0, maxAutoCmdNumber), Text: tail}
			}
		}
	}

	return AutoCmd{Type: tag, Number: 0, Text: text}
}

// isAllDigits 判断是否只由 0-9 组成（空串返回 false）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SaveAutoCmds 原子地写回命令列表（先把内容写进临时文件再改名）。
func (a *App) SaveAutoCmds(cmds []AutoCmd) error {
	path, err := AutoCmdPath()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, formatAutoCmds(cmds))
}

// GetAutoCmdPath 返回配置文件路径，供界面显示。
func (a *App) GetAutoCmdPath() string {
	path, err := AutoCmdPath()
	if err != nil {
		return autoCmdFileName
	}
	return path
}

// cmdKeyToken 是命令里可以使用的功能键标记：发送时会被替换成对应的实际字节。
// 标记不区分大小写，写在花括号里，例如 “AT+RST{ENTER}{UP}”。
type cmdKeyToken struct {
	name string
	seq  string
}

// cmdKeyTokens 支持的功能键（顺序无关，扫描时按花括号匹配查表）。
var cmdKeyTokens = []cmdKeyToken{
	{"ENTER", "\r"},
	{"CR", "\r"},
	{"LF", "\n"},
	{"TAB", "\t"},
	{"ESC", "\x1b"},
	{"UP", "\x1b[A"},
	{"DOWN", "\x1b[B"},
	{"RIGHT", "\x1b[C"},
	{"LEFT", "\x1b[D"},
	{"HOME", "\x1b[H"},
	{"END", "\x1b[F"},
	{"PGUP", "\x1b[5~"},
	{"PGDN", "\x1b[6~"},
	{"DEL", "\x1b[3~"},
	{"INS", "\x1b[2~"},
	{"BACKSPACE", "\x7f"},
	{"BS", "\x7f"},
	{"SPACE", " "},
}

// decodeCmdText 把命令文本里的 {ENTER} / {UP} / {0x1B} 之类标记换成实际字节。
// 认不出的花括号内容原样保留，方便用户写普通文本。
func decodeCmdText(text string) string {
	table := make(map[string]string, len(cmdKeyTokens))
	for _, tk := range cmdKeyTokens {
		table[tk.name] = tk.seq
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			b.WriteByte(text[i])
			continue
		}
		end := strings.IndexByte(text[i:], '}')
		if end < 0 {
			b.WriteString(text[i:])
			break
		}
		inner := text[i+1 : i+end]
		if seq, ok := table[strings.ToUpper(strings.TrimSpace(inner))]; ok {
			b.WriteString(seq)
			i += end
			continue
		}
		// {0x0A} / {0X1B} 形式：任意单字节
		if seq, ok := decodeHexToken(inner); ok {
			b.WriteString(seq)
			i += end
			continue
		}
		b.WriteString(text[i : i+end+1])
		i += end
	}
	return b.String()
}

// decodeHexToken 解析 “0x0A” 这样的单字节标记。
func decodeHexToken(inner string) (string, bool) {
	s := strings.TrimSpace(inner)
	if len(s) != 4 && len(s) != 3 {
		return "", false
	}
	if len(s) == 4 && !strings.EqualFold(s[:2], "0x") {
		return "", false
	}
	hexPart := s
	if len(s) == 4 {
		hexPart = s[2:]
	}
	v, err := strconv.ParseUint(hexPart, 16, 8)
	if err != nil {
		return "", false
	}
	return string([]byte{byte(v)}), true
}

// encodeCmdPayload 是 TX 载荷唯一的编码入口：
//   - asHex 时按空格分隔的十六进制字节解析，非法内容原样返回错误；
//   - 否则展开 {ENTER} / {UP} 之类功能键标记，并裁掉尾部空格 / 制表符；
//   - withEnter 且末字节不是回车（CR / LF）时补一个 CR，保证下位机能识别到一条完整命令。
func encodeCmdPayload(text string, asHex, withEnter bool) ([]byte, error) {
	var payload []byte
	if asHex {
		b, err := parseHexBytes(text)
		if err != nil {
			return nil, err
		}
		payload = b
	} else {
		// 只裁尾部的空格 / 制表符：显式写在末尾的 \r / \n 要保留，交给下面的 withEnter 判断。
		payload = []byte(strings.TrimRight(decodeCmdText(text), " \t"))
	}
	if withEnter && !strings.HasSuffix(string(payload), "\r") && !strings.HasSuffix(string(payload), "\n") {
		payload = append(payload, '\r')
	}
	return payload, nil
}

// buildTxPayload 生成 TX 命令实际要发送的字节，是 encodeCmdPayload 的兼容包装：
// 等价于 encodeCmdPayload(text, false, true)，老调用与老测试不用改。
func buildTxPayload(text string) string {
	payload, _ := encodeCmdPayload(text, false, true)
	return string(payload)
}

// formatAutoCmds 生成 auto_cmd.txt 的内容（保存与导出共用）。
func formatAutoCmds(cmds []AutoCmd) string {
	var b strings.Builder
	b.WriteString("# SerialMateX AUTO CMD 命令列表\n")
	b.WriteString("# 一行一条命令，格式：TX <命令文本> <数字> / RX <命令文本> <数字>（数字在行尾，0 ~ 99）\n")
	b.WriteString("# 行尾可加 hex=1 / enter=0（只写在 TX 行，且只在非默认时写）\n")
	b.WriteString("# 命令里可用功能键标记：{ENTER} {TAB} {ESC} {UP} {DOWN} {LEFT} {RIGHT} {HOME} {END} {PGUP} {PGDN} {DEL} {INS} {BACKSPACE} {SPACE} {0x1B}\n")
	for _, cmd := range cmds {
		// 一条命令只能占一行：换行符会被压成空格，避免破坏文件结构
		text := strings.ReplaceAll(strings.ReplaceAll(cmd.Text, "\r", " "), "\n", " ")
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		tag := strings.ToUpper(strings.TrimSpace(cmd.Type))
		if tag != "RX" {
			tag = "TX"
		}
		b.WriteString(tag)
		b.WriteString(" ")
		b.WriteString(text)
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(clampInt(cmd.Number, 0, maxAutoCmdNumber)))
		// 只写非默认值：Hex 为真写 hex=1，Enter 为假写 enter=0；RX 行一个都不写。
		if tag == "TX" {
			if cmd.Hex {
				b.WriteString(" hex=1")
			}
			if !cmd.Enter {
				b.WriteString(" enter=0")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseAutoCmdsText 解析整份命令文件内容（GetAutoCmds 与导入共用）。
func parseAutoCmdsText(content string) []AutoCmd {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	cmds := make([]AutoCmd, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		cmd := parseAutoCmdLine(text)
		if cmd.Text == "" {
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// ImportAutoCmdsFrom 从指定文件读取命令并覆盖当前列表，返回导入条数。
func (a *App) ImportAutoCmdsFrom(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	cmds := parseAutoCmdsText(string(data))
	if len(cmds) == 0 {
		return 0, errNoAutoCmdInFile
	}
	if err := a.SaveAutoCmds(cmds); err != nil {
		return 0, err
	}
	return len(cmds), nil
}

// ExportAutoCmdsTo 把当前列表导出到 dir 下的 auto_cmd_export_<时间戳>.txt，返回完整路径。
func (a *App) ExportAutoCmdsTo(dir string, stamp time.Time) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errBadExportDir
	}
	name := "auto_cmd_export_" + stamp.Format("20060102_150405") + ".txt"
	out := filepath.Join(dir, name)
	if err := writeFileAtomic(out, formatAutoCmds(a.GetAutoCmds())); err != nil {
		return "", err
	}
	return out, nil
}

// ExportAutoCmds 把当前命令列表导出到 exe 同目录（右上角“导出”按钮）。
func (a *App) ExportAutoCmds() (string, error) {
	path, err := AutoCmdPath()
	if err != nil {
		return "", err
	}
	return a.ExportAutoCmdsTo(filepath.Dir(path), time.Now())
}

// ImportAutoCmds 弹出文件选择框，选中后导入并覆盖当前列表；返回给界面显示的说明。
func (a *App) ImportAutoCmds() (string, error) {
	cfgPath, err := AutoCmdPath()
	if err != nil {
		return "", err
	}
	picked, dlgErr := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "导入 AUTO CMD 命令",
		DefaultDirectory: filepath.Dir(cfgPath),
		Filters: []runtime.FileFilter{
			{DisplayName: "文本文件 (*.txt)", Pattern: "*.txt"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
	if dlgErr != nil {
		return "", dlgErr
	}
	if picked == "" {
		return "", nil // 用户取消
	}
	n, impErr := a.ImportAutoCmdsFrom(picked)
	if impErr != nil {
		return "", impErr
	}
	return strconv.Itoa(n), nil
}
