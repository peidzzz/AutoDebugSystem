package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// Version is the current application version
const Version = "v1.3.7"

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// 窗口底色 = 上次用的皮肤背景（前端换肤时写进 ui_config.ini 的 BG-COLOR）。
	// 这个颜色只在 WebView2 还没画出第一帧时露一下；读不到配置就沿用默认皮肤
	// Bench 的 --bg-main（#F7F5EF），不会因为配置缺失或写坏而闪黑底。
	background := options.RGBA{R: 247, G: 245, B: 239, A: 1}
	if r, g, b, ok := loadStartupBackground(); ok {
		background = options.RGBA{R: r, G: g, B: b, A: 1}
	}

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "SerialMateX",
		Width:  1280,
		Height: 820,
		// 窗口最小尺寸：左右栏各 288px + 中间栏最小 576px（左侧栏的 2 倍）；
		// 高度以 TASK 页能完整显示 TASK STATUS 为准（客户区 768 + 标题栏余量）
		MinWidth:  1200,
		MinHeight: 820,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &background,
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
