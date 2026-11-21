// gensyso 生成 wails 项目本应由 `wails build` 生成的 Windows 资源对象文件（*.syso）。
//
// 为什么需要它：本机 Go 1.27 下 `wails build` 报
// `internal error: package "bytes" without types was imported`（旧 go/packages 读不了新导出数据），
// 只能直接用 `go build` 编译；而图标 / 清单 / 版本信息本来是 `wails build`
// 用 winres 写进 `<Name>-res.syso`、再由链接器合并进 exe 的，`go build` 不会自己加。
// 这个程序复刻 wails `pkg/commands/build/packager.go:compileResources()` 的行为。
//
// 用法（在本目录下）：
//
//	go run .            # 写入 ../../SerialMateX-res.syso
//	go run . -root ../.. -out /tmp/x.syso
//
// 换图标只需替换 build/windows/icon.ico 后重跑一次。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func main() {
	root := flag.String("root", filepath.Join("..", ".."), "SerialMateX 项目目录（含 build/windows）")
	out := flag.String("out", "", "输出文件；默认 <root>/SerialMateX-res.syso")
	flag.Parse()

	win := filepath.Join(*root, "build", "windows")
	icoPath := filepath.Join(win, "icon.ico")
	manifestPath := filepath.Join(win, "wails.exe.manifest")
	target := *out
	if target == "" {
		target = filepath.Join(*root, "SerialMateX-res.syso")
	}

	if err := run(icoPath, manifestPath, target); err != nil {
		fmt.Fprintln(os.Stderr, "gensyso:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", target)
}

func run(icoPath, manifestPath, target string) error {
	iconFile, err := os.Open(icoPath)
	if err != nil {
		return err
	}
	defer iconFile.Close()

	ico, err := winres.LoadICO(iconFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", icoPath, err)
	}

	var rs winres.ResourceSet
	if err := rs.SetIcon(winres.RT_ICON, ico); err != nil {
		return fmt.Errorf("set icon: %w", err)
	}

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	manifest, err := winres.AppManifestFromXML(manifestData)
	if err != nil {
		return fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	rs.SetManifest(manifest)

	// wails 的 info.json 是 go-winres 模板（值全是 {{.Info.X}}），而 wails.json 没有 info 段，
	// 直接套模板只会得到空字符串，所以这里把展示用的字段写死。
	vi := version.Info{}
	vi.FileVersion = [4]uint16{0, 0, 2, 0}
	vi.ProductVersion = [4]uint16{0, 0, 2, 0}
	fields := []struct{ key, value string }{
		{version.ProductName, "SerialMateX"},
		{version.CompanyName, "TheWInds071"},
		{version.FileDescription, "SerialMateX 串口调试与自动化测试工具"},
		{version.LegalCopyright, "Copyright (c) 2026 TheWInds071"},
		{version.OriginalFilename, "SerialMateX.exe"},
		// 字符串版本号：资源管理器的「详细信息」里显示的就是这两项
		{version.FileVersion, "0.0.2.0"},
		{version.ProductVersion, "0.0.2.0"},
	}
	for _, f := range fields {
		if err := vi.Set(version.LangDefault, f.key, f.value); err != nil {
			return fmt.Errorf("version info %s: %w", f.key, err)
		}
	}
	rs.SetVersionInfo(vi)

	fout, err := os.Create(target)
	if err != nil {
		return err
	}
	defer fout.Close()

	if err := rs.WriteObject(fout, winres.ArchAMD64); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}