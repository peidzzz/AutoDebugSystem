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
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
