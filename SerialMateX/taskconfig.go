package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TaskConfig 是 task_config.ini 中的任务参数（TASK 页下半部分）。
type TaskConfig struct {
	SaveLog       bool   `json:"saveLog"`       // 保存日志
	SavePower     bool   `json:"savePower"`     // 保存功耗
	InitialState  bool   `json:"initialState"`  // 初始状态（开 / 关）
	AbnormalPause bool   `json:"abnormalPause"` // 异常暂停（开 / 关）
	WaitSeconds   int    `json:"waitSeconds"`   // 运行时间（秒）
	DelaySeconds  int    `json:"delaySeconds"`  // 间隔时间（秒）
	RunCount      int    `json:"runCount"`      // 运行次数（次）
	PingEnable    bool   `json:"pingEnable"`    // PING 检测（每轮命令跑完后 ping 目标地址）
	PingIP        string `json:"pingIP"`        // PING 目标地址（IPv4）
}

const taskCfgFileName = "task_config.ini"

const (
	defaultWaitSeconds  = 5
	defaultDelaySeconds = 1
	defaultRunCount     = 1

	maxTaskSeconds = 86400
	maxRunCount    = 100000
)

// taskCfgTemplate 是 task_config.ini 不存在时自动生成的模板。
const taskCfgTemplate = `; SerialMateX 任务参数配置（TASK 页）
; SaveLog     = 保存日志 0/1
; SavePower   = 保存功耗 0/1
; InitialState = 初始状态 0/1
; AbnormalPause = 异常暂停 0/1
; WaitSeconds  = 运行时间（秒）
; DelaySeconds = 间隔时间（秒）
; RunCount     = 运行次数（次）
; PingEnable   = PING 检测 0/1
; PingIP       = PING 目标地址（IPv4）

[Task]
SaveLog=0
SavePower=0
InitialState=0
AbnormalPause=0
WaitSeconds=5
DelaySeconds=1
RunCount=1
PingEnable=0
PingIP=
`

// TaskCfgPath 返回 task_config.ini 的绝对路径（与可执行文件同目录）；
// 设置环境变量 SERIALMATEX_TASKCFG_DIR 可以把它指到别的目录（便于测试 / 便携部署）。
func TaskCfgPath() (string, error) {
	if dir := os.Getenv("SERIALMATEX_TASKCFG_DIR"); dir != "" {
		return filepath.Join(dir, taskCfgFileName), nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exePath), taskCfgFileName), nil
}

// ensureTaskCfgFile 在 task_config.ini 不存在时写出模板；已存在则原样保留。
func (a *App) ensureTaskCfgFile() {
	path, err := TaskCfgPath()
	if err != nil {
		return
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		_ = os.WriteFile(path, []byte(taskCfgTemplate), 0o644)
	}
}

// GetTaskConfig 读取 task_config.ini；文件不存在时按模板创建并返回默认值。
func (a *App) GetTaskConfig() TaskConfig {
	path, err := TaskCfgPath()
	if err != nil {
		return defaultTaskConfig()
	}
	a.ensureTaskCfgFile()

	cfg := defaultTaskConfig()
	file, err := os.Open(path)
	if err != nil {
		return cfg
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToUpper(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(key)))
		value = strings.TrimSpace(value)
		// 行内注释：PingIP=192.168.1.10 ; 目标地址 这种人写的写法不能整串当成值，
		// 否则 StartTask 会以「PING 目标地址无效」拒绝启动。
		if i := strings.IndexAny(value, ";#"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		switch key {
		case "SAVELOG":
			cfg.SaveLog = parseBoolValue(value)
		case "SAVEPOWER":
			cfg.SavePower = parseBoolValue(value)
		case "INITIALSTATE", "INITIAL":
			cfg.InitialState = parseBoolValue(value)
		case "ABNORMALPAUSE", "ABNORMAL", "PAUSE":
			cfg.AbnormalPause = parseBoolValue(value)
		case "WAITSECONDS", "WAIT":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.WaitSeconds = clampInt(n, 0, maxTaskSeconds)
			}
		case "DELAYSECONDS", "DELAY":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.DelaySeconds = clampInt(n, 0, maxTaskSeconds)
			}
		case "RUNCOUNT", "RUNS":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.RunCount = clampInt(n, 1, maxRunCount)
			}
		case "PINGENABLE", "PING":
			cfg.PingEnable = parseBoolValue(value)
		case "PINGIP", "PINGADDR", "PINGADDRESS", "PINGTARGET":
			cfg.PingIP = value
		}
	}
	// 当前版本“保存功耗”固定为关（界面已置灰），旧配置里的 1 也按 0 处理
	cfg.SavePower = false
	return cfg
}

// SaveTaskConfig 把任务参数写回 task_config.ini（先写临时文件再替换）。
func (a *App) SaveTaskConfig(cfg TaskConfig) error {
	path, err := TaskCfgPath()
	if err != nil {
		return err
	}

	wait := clampInt(cfg.WaitSeconds, 0, maxTaskSeconds)
	delay := clampInt(cfg.DelaySeconds, 0, maxTaskSeconds)
	runs := clampInt(cfg.RunCount, 1, maxRunCount)
	pingIP := strings.TrimSpace(cfg.PingIP)

	// PING 地址只在开关打开时校验：手改 ini 写了坏地址的用户必须还能把开关关掉。
	if cfg.PingEnable && !validIPv4(pingIP) {
		return fmt.Errorf("PING 目标地址无效：%s", pingIP)
	}

	var sb strings.Builder
	sb.WriteString("; SerialMateX 任务参数配置（TASK 页，由界面保存时自动生成）\n")
	sb.WriteString("; SaveLog     = 保存日志 0/1\n")
	sb.WriteString("; SavePower   = 保存功耗 0/1\n")
	sb.WriteString("; InitialState = 初始状态 0/1\n")
	sb.WriteString("; AbnormalPause = 异常暂停 0/1\n")
	sb.WriteString("; WaitSeconds  = 运行时间（秒）\n")
	sb.WriteString("; DelaySeconds = 间隔时间（秒）\n")
	sb.WriteString("; RunCount     = 运行次数（次）\n")
	sb.WriteString("; PingEnable   = PING 检测 0/1\n")
	sb.WriteString("; PingIP       = PING 目标地址（IPv4）\n\n")
	sb.WriteString("[Task]\n")
	sb.WriteString("SaveLog=" + boolText(cfg.SaveLog) + "\n")
	// 当前版本“保存功耗”固定为关（界面已置灰）
	sb.WriteString("SavePower=" + boolText(false) + "\n")
	sb.WriteString("InitialState=" + boolText(cfg.InitialState) + "\n")
	sb.WriteString("AbnormalPause=" + boolText(cfg.AbnormalPause) + "\n")
	sb.WriteString("WaitSeconds=" + strconv.Itoa(wait) + "\n")
	sb.WriteString("DelaySeconds=" + strconv.Itoa(delay) + "\n")
	sb.WriteString("RunCount=" + strconv.Itoa(runs) + "\n")
	sb.WriteString("PingEnable=" + boolText(cfg.PingEnable) + "\n")
	sb.WriteString("PingIP=" + pingIP + "\n")

	return writeFileAtomic(path, sb.String())
}

// GetTaskCfgPath 返回配置文件路径，方便界面提示用户去哪里编辑。
func (a *App) GetTaskCfgPath() string {
	path, err := TaskCfgPath()
	if err != nil {
		return taskCfgFileName
	}
	return path
}

func defaultTaskConfig() TaskConfig {
	return TaskConfig{
		SaveLog:       false,
		SavePower:     false,
		InitialState:  false,
		AbnormalPause: false,
		WaitSeconds:   defaultWaitSeconds,
		DelaySeconds:  defaultDelaySeconds,
		RunCount:      defaultRunCount,
	}
}

func parseBoolValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "是", "开":
		return true
	default:
		return false
	}
}

func boolText(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
