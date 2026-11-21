package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ui_config.ini 只存界面侧的「上次用的东西」，目前就是窗口底色。
// 它和 pwr_dev.ini / task_config.ini 一样放在 exe 同目录，便于便携部署。
const (
	uiCfgFileName = "ui_config.ini"
	uiCfgKeyBG    = "BG-COLOR"
	// uiCfgKeyBGNorm 是 uiCfgKeyBG 归一化后的写法（去 -、_、空格并转大写），
	// 比对时两边都归一化，这样 bg-color / BG_COLOR / BG COLOR 都认。
	uiCfgKeyBGNorm = "BGCOLOR"
)

// uiCfgTemplate 是 ui_config.ini 的初始内容；注释行解释这文件是程序自己写的。
const uiCfgTemplate = "; SerialMateX 界面配置（由程序自动写入，可手工修改）\n" +
	"; BG-COLOR = 窗口底色，取当前皮肤的 --bg-main，避免启动时闪一下默认色\n" +
	"[UI]\n" +
	uiCfgKeyBG + "=%s\n"

// defaultUICfgContent 生成首次创建的 ui_config.ini 内容（填进当前底色）。
func defaultUICfgContent(hex string) string {
	return strings.Replace(uiCfgTemplate, "%s", hex, 1)
}

// UICfgPath 返回 ui_config.ini 的绝对路径（与可执行文件同目录）；
// 设置环境变量 SERIALMATEX_UICFG_DIR 可以把它指到别的目录（便于测试）。
func UICfgPath() (string, error) {
	if dir := os.Getenv("SERIALMATEX_UICFG_DIR"); dir != "" {
		return filepath.Join(dir, uiCfgFileName), nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exePath), uiCfgFileName), nil
}

// parseHexColor 解析 #RRGGBB / #RGB（# 可省、大小写不敏感）；
// 认不出就返回 ok=false，调用方沿用默认颜色，绝不把窗口涂成不可预知的颜色。
func parseHexColor(s string) (r, g, b uint8, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 && len(s) != 3 {
		return 0, 0, 0, false
	}
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(v >> 16 & 0xFF), uint8(v >> 8 & 0xFF), uint8(v & 0xFF), true
}

// loadStartupBackground 读 ui_config.ini 里的 BG-COLOR。文件缺失、读不动、
// 值非法都返回 ok=false（窗口用默认底色），不报错——这一步在 wails.Run 之前，
// 没有任何界面能显示错误，静默回退比弹窗卡住启动更合理。
func loadStartupBackground() (r, g, b uint8, ok bool) {
	path, err := UICfgPath()
	if err != nil {
		return 0, 0, 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if normalizeUICfgKey(key) != uiCfgKeyBGNorm {
			continue
		}
		return parseHexColor(value)
	}
	return 0, 0, 0, false
}

// normalizeUICfgKey 让 "bg-color" / "BG_COLOR" / "BG COLOR" 都认作同一个键。
func normalizeUICfgKey(key string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(key)))
}

// SaveUIBackground 把界面底色写进 ui_config.ini（前端换肤时调用）。
// 成功返回空串，失败返回给用户看的错误信息。
func SaveUIBackground(hex string) string {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return "颜色值无效：" + hex
	}
	path, err := UICfgPath()
	if err != nil {
		return "无法定位 ui_config.ini：" + err.Error()
	}
	content := defaultUICfgContent(formatHexColor(r, g, b))
	// 已有文件时保留它的注释与其它键，只替换底色那一行。
	if raw, readErr := os.ReadFile(path); readErr == nil {
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		replaced := false
		out := make([]string, 0, len(lines)+1)
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if key, _, found := strings.Cut(trimmed, "="); found && normalizeUICfgKey(key) == uiCfgKeyBGNorm {
				if !replaced {
					out = append(out, uiCfgKeyBG+"="+formatHexColor(r, g, b))
					replaced = true
				}
				continue
			}
			out = append(out, line)
		}
		if replaced {
			content = strings.Join(out, "\n")
		} else if strings.Contains(strings.ToUpper(string(raw)), "[UI]") {
			content = strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") +
				"\n" + uiCfgKeyBG + "=" + formatHexColor(r, g, b) + "\n"
		} else {
			content = strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") +
				"\n[UI]\n" + uiCfgKeyBG + "=" + formatHexColor(r, g, b) + "\n"
		}
	}
	if err := writeFileAtomic(path, content); err != nil {
		return "写入 ui_config.ini 失败：" + err.Error()
	}
	return ""
}

// formatHexColor 输出 #RRGGBB（大写），与前端 CSS 变量写法一致。
func formatHexColor(r, g, b uint8) string {
	const hexDigits = "0123456789ABCDEF"
	return string([]byte{'#',
		hexDigits[r>>4], hexDigits[r&0x0F],
		hexDigits[g>>4], hexDigits[g&0x0F],
		hexDigits[b>>4], hexDigits[b&0x0F]})
}