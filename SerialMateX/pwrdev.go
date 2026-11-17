package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PwrDevice 是 pwr_dev.ini 中定义的一台受控设备。
// 每台设备包含自己的串口参数（波特率/数据位/停止位/校验位）、上电与断电指令，
// 以及两条指令各自的 HEX / 回车开关。
// StopBits 用内部值表示：1 = 1 位、15 = 1.5 位、2 = 2 位（与 app.go 的串口映射一致）。
type PwrDevice struct {
	Name          string `json:"name"`
	Port          string `json:"port"`
	Baud          int    `json:"baud"`
	DataBits      int    `json:"dataBits"`
	StopBits      int    `json:"stopBits"`
	Parity        string `json:"parity"`
	PowerOn       string `json:"powerOn"`
	PowerOff      string `json:"powerOff"`
	PowerOnHex    bool   `json:"powerOnHex"`
	PowerOnEnter  bool   `json:"powerOnEnter"`
	PowerOffHex   bool   `json:"powerOffHex"`
	PowerOffEnter bool   `json:"powerOffEnter"`
}

const (
	pwrDevFileName = "pwr_dev.ini"

	defaultPwrBaud     = 9600
	defaultPwrDataBits = 8
	defaultPwrStopBits = 1 // 内部值：1 / 15 / 2
	defaultPwrParity   = "None"
)

// pwrDevTemplate 是 pwr_dev.ini 不存在时自动生成的模板。
const pwrDevTemplate = `; SerialMateX 设备电源控制配置
; 每个 [段] 代表一台设备，包含以下信息：
;   Name    = 设备名称（显示在 TASK -> 设备选择 菜单中）
;   Port    = 电源串口的 COM 端口
;   BAUD / DATA / STOP / PARITY = 电源串口参数，缺省 9600 / 8 / 1 / None
;                                 （STOP 可以写 1、1.5、2）
;   PWR-ON  = 给设备上电时发送的内容（HEX=1 时写十六进制，如 0x01 或 01 02 03；HEX=0 时写普通字符串）
;   PWR-OFF = 给设备断电时发送的内容
;   PWR-ON-HEX / PWR-OFF-HEX     = 是否按十六进制发送，缺省 1
;   PWR-ON-ENTER / PWR-OFF-ENTER = 发送后是否追加回车，缺省 0
;
; 删掉或注释掉某个键就使用它的缺省值。修改后重新打开软件即可生效。

[DemoBoard]
Name=DemoBoard
Port=COM7
BAUD=9600
DATA=8
STOP=1
PARITY=None
PWR-ON=0x01
PWR-ON-HEX=1
PWR-ON-ENTER=0
PWR-OFF=0x00
PWR-OFF-HEX=1
PWR-OFF-ENTER=0
`

// normalizeStopBits 把停止位收敛到内部值 1 / 15 / 2，其余值按 1（缺省）处理。
func normalizeStopBits(v int) int {
	switch v {
	case 1, 15, 2:
		return v
	default:
		return defaultPwrStopBits
	}
}

// parseStopBitsText 解析配置文件里的停止位写法："1" / "1.5" / "15" / "2"，
// 空值与非法值按 1（缺省）处理。
func parseStopBitsText(s string) int {
	switch strings.TrimSpace(s) {
	case "1.5", "15":
		return 15
	case "2":
		return 2
	default:
		return defaultPwrStopBits
	}
}

// formatStopBitsText 把停止位内部值写回人读法：1 → "1"，15 → "1.5"，2 → "2"。
func formatStopBitsText(v int) string {
	switch normalizeStopBits(v) {
	case 15:
		return "1.5"
	case 2:
		return "2"
	default:
		return "1"
	}
}

// parseBoolText 解析布尔文本：1/0、true/false、on/off、是/否，大小写不敏感。
// 第二个返回值表示输入是否被识别。
func parseBoolText(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "on", "是":
		return true, true
	case "0", "false", "off", "否":
		return false, true
	default:
		return false, false
	}
}

// pwrBoolText 把布尔开关写成 ini 里的 1 / 0。
func pwrBoolText(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// withDefaults 补齐缺失的串口参数，让老文件与缺键的新配置都得到同样的行为。
// 两个 HEX 开关的缺省值（true）在 newPwrDevice 里给出：布尔零值无法区分
// “键不存在”和“显式写 0”，所以必须在创建段时预置。
func (d PwrDevice) withDefaults() PwrDevice {
	if d.Baud <= 0 {
		d.Baud = defaultPwrBaud
	}
	if d.DataBits <= 0 {
		d.DataBits = defaultPwrDataBits
	}
	d.StopBits = normalizeStopBits(d.StopBits)
	d.Parity = strings.TrimSpace(d.Parity)
	if d.Parity == "" {
		d.Parity = defaultPwrParity
	}
	return d
}

// newPwrDevice 创建一台带缺省值的设备；HEX 开关默认打开，保持老文件的发送行为。
func newPwrDevice(name string) PwrDevice {
	return PwrDevice{Name: name, PowerOnHex: true, PowerOffHex: true}.withDefaults()
}

// PwrDevPath 返回 pwr_dev.ini 的绝对路径（与可执行文件同目录）；
// 设置环境变量 SERIALMATEX_PWRDEV_DIR 可以把它指到别的目录（便于测试 / 便携部署）。
func PwrDevPath() (string, error) {
	if dir := os.Getenv("SERIALMATEX_PWRDEV_DIR"); dir != "" {
		return filepath.Join(dir, pwrDevFileName), nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exePath), pwrDevFileName), nil
}

// ensurePwrDevFile 在 pwr_dev.ini 不存在时写出模板；已存在则原样保留。
func (a *App) ensurePwrDevFile() {
	path, err := PwrDevPath()
	if err != nil {
		return
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		_ = os.WriteFile(path, []byte(pwrDevTemplate), 0o644)
	}
}

// GetPwrDevices 读取可执行文件同目录下的 pwr_dev.ini；
// 文件不存在时按模板自动创建，保证 TASK 页始终有设备可选。
func (a *App) GetPwrDevices() []PwrDevice {
	path, err := PwrDevPath()
	if err != nil {
		return []PwrDevice{}
	}
	a.ensurePwrDevFile()
	devices, err := parsePwrDevFile(path)
	if err != nil {
		return []PwrDevice{}
	}
	return devices
}

// GetPwrDevPath 返回配置文件路径，方便界面提示用户去哪里编辑。
func (a *App) GetPwrDevPath() string {
	path, err := PwrDevPath()
	if err != nil {
		return pwrDevFileName
	}
	return path
}

// SavePwrDevices 把界面上的设备列表整体写回 pwr_dev.ini
// （先写临时文件再替换，避免写到一半断电导致配置损坏）。
func (a *App) SavePwrDevices(devices []PwrDevice) error {
	path, err := PwrDevPath()
	if err != nil {
		return err
	}

	seen := make(map[string]bool, len(devices))
	var sb strings.Builder
	sb.WriteString("; SerialMateX 设备电源控制配置（由界面保存时自动生成，也可以手工编辑）\n")
	sb.WriteString("; 每个 [段] 代表一台设备，包含以下信息：\n")
	sb.WriteString(";   Name    = 设备名称（显示在 TASK -> 设备选择 菜单中）\n")
	sb.WriteString(";   Port    = 电源串口的 COM 端口\n")
	sb.WriteString(";   BAUD / DATA / STOP / PARITY = 电源串口参数（STOP 可写 1、1.5、2）\n")
	sb.WriteString(";   PWR-ON  = 上电时发送的内容，HEX=1 时是多个十六进制数，用空格分隔，例如 0x01 0x02 AA\n")
	sb.WriteString(";   PWR-OFF = 断电时发送的内容，格式同上\n")
	sb.WriteString(";   PWR-ON-HEX / PWR-OFF-HEX     = 是否按十六进制发送（1/0）\n")
	sb.WriteString(";   PWR-ON-ENTER / PWR-OFF-ENTER = 发送后是否追加回车（1/0）\n\n")

	for _, device := range devices {
		name := strings.TrimSpace(device.Name)
		if name == "" {
			return fmt.Errorf("设备名称不能为空")
		}
		if strings.ContainsAny(name, "[]\r\n") {
			return fmt.Errorf("设备名称不能包含方括号或换行: %s", name)
		}
		if seen[name] {
			return fmt.Errorf("设备名称重复: %s", name)
		}
		seen[name] = true

		fmt.Fprintf(&sb, "[%s]\n", name)
		fmt.Fprintf(&sb, "Name=%s\n", name)
		fmt.Fprintf(&sb, "Port=%s\n", strings.TrimSpace(device.Port))
		fmt.Fprintf(&sb, "BAUD=%d\n", device.Baud)
		fmt.Fprintf(&sb, "DATA=%d\n", device.DataBits)
		fmt.Fprintf(&sb, "STOP=%s\n", formatStopBitsText(device.StopBits))
		fmt.Fprintf(&sb, "PARITY=%s\n", strings.TrimSpace(device.Parity))
		fmt.Fprintf(&sb, "PWR-ON=%s\n", strings.TrimSpace(device.PowerOn))
		fmt.Fprintf(&sb, "PWR-ON-HEX=%s\n", pwrBoolText(device.PowerOnHex))
		fmt.Fprintf(&sb, "PWR-ON-ENTER=%s\n", pwrBoolText(device.PowerOnEnter))
		fmt.Fprintf(&sb, "PWR-OFF=%s\n", strings.TrimSpace(device.PowerOff))
		fmt.Fprintf(&sb, "PWR-OFF-HEX=%s\n", pwrBoolText(device.PowerOffHex))
		fmt.Fprintf(&sb, "PWR-OFF-ENTER=%s\n\n", pwrBoolText(device.PowerOffEnter))
	}

	return writeFileAtomic(path, sb.String())
}

// writeFileAtomic 先写同目录临时文件再 rename，保证配置文件不会只写一半。
func writeFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()

	if _, err = temp.WriteString(content); err != nil {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err = temp.Close(); err != nil {
		os.Remove(tempName)
		return err
	}
	if err = os.Rename(tempName, path); err != nil {
		os.Remove(tempName)
		return err
	}
	return nil
}

// parsePwrDevFile 解析 pwr_dev.ini：段名即设备标识，键名不区分大小写，
// 同时兼容 Name/Device/设备名称、Port/COM/COM端口、PWR-ON/PWR_ON/PowerOn 等写法；
// 串口参数与 HEX/回车开关同样兼容中英文键名，缺键时用缺省值补齐。
func parsePwrDevFile(path string) ([]PwrDevice, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	devices := make([]PwrDevice, 0, 8)
	index := make(map[string]int) // 段名 -> devices 下标
	section := ""

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}

		// 段名：[DemoBoard]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section == "" {
				continue
			}
			if _, ok := index[section]; !ok {
				index[section] = len(devices)
				devices = append(devices, newPwrDevice(section))
			}
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			continue
		}
		pos, ok := index[section]
		if !ok {
			continue
		}

		value = strings.TrimSpace(value)
		normalized := strings.ToUpper(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(key)))
		switch normalized {
		case "NAME", "DEVICENAME", "设备名称":
			if value != "" {
				devices[pos].Name = value
			}
		case "PORT", "COM", "COMPORT", "COM端口":
			devices[pos].Port = value
		case "BAUD", "波特率":
			if baud, err := strconv.Atoi(value); err == nil && baud > 0 {
				devices[pos].Baud = baud
			}
		case "DATA", "DATABITS", "数据位":
			if dataBits, err := strconv.Atoi(value); err == nil && dataBits > 0 {
				devices[pos].DataBits = dataBits
			}
		case "STOP", "STOPBITS", "停止位":
			devices[pos].StopBits = parseStopBitsText(value)
		case "PARITY", "校验位":
			if value != "" {
				devices[pos].Parity = value
			}
		case "PWRON", "POWERON", "上电", "上电指令":
			devices[pos].PowerOn = value
		case "PWROFF", "POWEROFF", "断电", "断电指令":
			devices[pos].PowerOff = value
		case "PWRONHEX", "PWRONHEXBIT", "上电HEX":
			if on, ok := parseBoolText(value); ok {
				devices[pos].PowerOnHex = on
			}
		case "PWRONENTER", "上电回车":
			if on, ok := parseBoolText(value); ok {
				devices[pos].PowerOnEnter = on
			}
		case "PWROFFHEX":
			if on, ok := parseBoolText(value); ok {
				devices[pos].PowerOffHex = on
			}
		case "PWROFFENTER":
			if on, ok := parseBoolText(value); ok {
				devices[pos].PowerOffEnter = on
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	for i := range devices {
		devices[i] = devices[i].withDefaults()
	}
	return devices, nil
}
