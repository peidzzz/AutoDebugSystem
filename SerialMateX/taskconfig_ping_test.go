package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskCfgPingKeys(t *testing.T) {
	// 键名归一化后 PINGENABLE / PING 都认；PINGIP / PINGADDR / PINGTARGET 都认；大小写不敏感
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	body := "[Task]\nping=1\npingaddr = 10.0.0.5\nRunCount=1\n"
	if err := os.WriteFile(filepath.Join(dir, "task_config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := (&App{}).GetTaskConfig()
	if !cfg.PingEnable || cfg.PingIP != "10.0.0.5" {
		t.Fatalf("PING 键没解析出来: %+v", cfg)
	}
}

func TestTaskCfgSavePingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	app := &App{}
	in := TaskConfig{PingEnable: true, PingIP: " 192.168.1.10 ", WaitSeconds: 5, DelaySeconds: 1, RunCount: 1}
	if err := app.SaveTaskConfig(in); err != nil {
		t.Fatal(err)
	}
	got := app.GetTaskConfig()
	if !got.PingEnable || got.PingIP != "192.168.1.10" {
		t.Fatalf("往返丢了 PING 值: %+v", got)
	}
}

func TestTaskCfgSaveRejectsBadPingIP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	app := &App{}
	err := app.SaveTaskConfig(TaskConfig{PingEnable: true, PingIP: "999.1.1.1", WaitSeconds: 5, DelaySeconds: 1, RunCount: 1})
	if err == nil || !strings.Contains(err.Error(), "PING 目标地址无效：999.1.1.1") {
		t.Fatalf("期望 PING 地址错误，得到 %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "task_config.ini")); !os.IsNotExist(statErr) {
		t.Fatalf("校验失败时不应落盘")
	}
}

func TestTaskCfgPingIPIgnoresInlineComment(t *testing.T) {
	// 手改 ini 常写成「PingIP=192.168.1.10 ; 目标」，原来整串进了 PingIP，
	// StartTask 直接拒绝启动，而其它键遇到同样写法只是回落默认值。
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	body := "[Task]\nPingEnable=1\nPingIP=192.168.1.10 ; 目标地址\nRunCount=1\n"
	if err := os.WriteFile(filepath.Join(dir, "task_config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := (&App{}).GetTaskConfig()
	if cfg.PingIP != "192.168.1.10" {
		t.Fatalf("行内注释没被剥掉: %q", cfg.PingIP)
	}
	if !cfg.PingEnable {
		t.Fatalf("开关没解析出来: %+v", cfg)
	}
}

func TestTaskCfgSaveAllowsDisableWithBadPingIP(t *testing.T) {
	// 手改 ini 弄出坏地址后，用户必须还能把开关关掉
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_TASKCFG_DIR", dir)
	app := &App{}
	if err := app.SaveTaskConfig(TaskConfig{PingEnable: false, PingIP: "bad", WaitSeconds: 5, DelaySeconds: 1, RunCount: 1}); err != nil {
		t.Fatalf("关闭 PING 时不该拦: %v", err)
	}
}
