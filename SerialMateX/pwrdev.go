package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PwrDevice 是 pwr_dev.ini 中定义的一台受控设备。
// 每台设备包含 4 项信息：设备名称、COM 端口、PWR-ON、PWR-OFF。
type PwrDevice struct {
	Name     string `json:"name"`
	Port     string `json:"port"`
	PowerOn  string `json:"powerOn"`
	PowerOff string `json:"powerOff"`
}

const pwrDevFileName = "pwr_dev.ini"

// pwrDevTemplate 是 pwr_dev.ini 不存在时自动生成的模板。
const pwrDevTemplate = `; SerialMateX 设备电源控制配置
; 每个 [段] 代表一台设备，包含 4 项信息：
;   Name    = 设备名称（显示在 TASK -> 设备选择 菜单中）
;   Port    = COM 端口
;   PWR-ON  = 给设备上电时发送的内容（十六进制如 0x01 或 01 02 03，也可以写普通字符串）
;   PWR-OFF = 给设备断电时发送的内容
;
; 修改后重新打开软件即可生效。

[DemoBoard]
Name=DemoBoard
Port=COM7
PWR-ON=0x01
PWR-OFF=0x00
`

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
	sb.WriteString("; 每个 [段] 代表一台设备，包含 4 项信息：\n")
	sb.WriteString(";   Name    = 设备名称（显示在 TASK -> 设备选择 菜单中）\n")
	sb.WriteString(";   Port    = COM 端口\n")
	sb.WriteString(";   PWR-ON  = 上电时发送的内容，可以是多个十六进制数，用空格分隔，例如 0x01 0x02 AA\n")
	sb.WriteString(";   PWR-OFF = 断电时发送的内容，格式同上\n\n")

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
		fmt.Fprintf(&sb, "PWR-ON=%s\n", strings.TrimSpace(device.PowerOn))
		fmt.Fprintf(&sb, "PWR-OFF=%s\n\n", strings.TrimSpace(device.PowerOff))
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
// 同时兼容 Name/Device/设备名称、Port/COM/COM端口、PWR-ON/PWR_ON/PowerOn 等写法。
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
				devices = append(devices, PwrDevice{Name: section})
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
		case "PWRON", "POWERON", "上电", "上电指令":
			devices[pos].PowerOn = value
		case "PWROFF", "POWEROFF", "断电", "断电指令":
			devices[pos].PowerOff = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}
