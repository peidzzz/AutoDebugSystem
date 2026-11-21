package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in      string
		r, g, b uint8
		ok      bool
	}{
		{"#F7F5EF", 247, 245, 239, true},
		{"f7f5ef", 247, 245, 239, true},
		{"  #f7f5ef  ", 247, 245, 239, true},
		{"#FFF", 255, 255, 255, true},
		{"123", 17, 34, 51, true},
		{"#000000", 0, 0, 0, true},
		{"", 0, 0, 0, false},
		{"#12345", 0, 0, 0, false},
		{"#1234567", 0, 0, 0, false},
		{"#GGGGGG", 0, 0, 0, false},
		{"#12 3456", 0, 0, 0, false},
	}
	for _, c := range cases {
		r, g, b, ok := parseHexColor(c.in)
		if ok != c.ok || (ok && (r != c.r || g != c.g || b != c.b)) {
			t.Fatalf("parseHexColor(%q) = (%d,%d,%d,%v)，期望 (%d,%d,%d,%v)", c.in, r, g, b, ok, c.r, c.g, c.b, c.ok)
		}
	}
}

func TestLoadStartupBackground(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_UICFG_DIR", dir)

	// 文件不存在时按“没有配置”处理，调用方沿用默认底色。
	if _, _, _, ok := loadStartupBackground(); ok {
		t.Fatal("ui_config.ini 不存在时不该返回 OK")
	}

	path := filepath.Join(dir, uiCfgFileName)
	content := "; SerialMateX 界面配置\r\n[UI]\r\nBG-COLOR=#1E1E1E\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, g, b, ok := loadStartupBackground()
	if !ok || r != 30 || g != 30 || b != 30 {
		t.Fatalf("读到 (%d,%d,%d,%v)，期望 (30,30,30,true)", r, g, b, ok)
	}

	// 值写坏时不能把窗口涂成不可预知的颜色。
	if err := os.WriteFile(path, []byte("[UI]\nBG-COLOR=oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := loadStartupBackground(); ok {
		t.Fatal("非法颜色值不该返回 OK")
	}
}

func TestSaveUIBackground(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SERIALMATEX_UICFG_DIR", dir)

	if err := saveUIBackground("#123456"); err != "" {
		t.Fatalf("保存失败: %s", err)
	}
	r, g, b, ok := loadStartupBackground()
	if !ok || r != 0x12 || g != 0x34 || b != 0x56 {
		t.Fatalf("回读得到 (%d,%d,%d,%v)", r, g, b, ok)
	}

	// 覆盖保存第二次要替换而不是追加。
	if err := saveUIBackground("#0A0B0C"); err != "" {
		t.Fatalf("二次保存失败: %s", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, uiCfgFileName))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "BG-COLOR="); n != 1 {
		t.Fatalf("BG-COLOR 应只有一行，实际 %d 行:\n%s", n, raw)
	}
	if r, g, b, ok := loadStartupBackground(); !ok || r != 0x0A || g != 0x0B || b != 0x0C {
		t.Fatalf("二次回读得到 (%d,%d,%d,%v)", r, g, b, ok)
	}

	// 非法颜色拒绝写入，且不能破坏已有文件。
	if err := saveUIBackground("nope"); err == "" {
		t.Fatal("非法颜色应返回错误")
	}
	if r, g, b, ok := loadStartupBackground(); !ok || r != 0x0A || g != 0x0B || b != 0x0C {
		t.Fatalf("非法保存不应改动文件，回读 (%d,%d,%d,%v)", r, g, b, ok)
	}
}