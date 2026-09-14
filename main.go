// bao 是 Albert 风格的 GTK4 桌面启动器入口：
// 组装配置、应用扫描与主窗口，运行 GTK 应用主循环。
package main

import (
	"fmt"
	"os"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"bao/internal/apps"
	"bao/internal/config"
	"bao/internal/ui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置加载失败，回退到默认配置:", err)
		cfg = &config.Config{ExcludedDirs: config.DefaultExcluded()}
	}

	// 启动时一次性扫描应用目录载入内存（契约：启动时载入）。
	appList := apps.Scan()

	app := gtk.NewApplication("dev.bao.launcher", gio.ApplicationFlagsNone)

	var win *ui.Window
	app.ConnectActivate(func() {
		if win == nil {
			win = ui.NewWindow(app, cfg, appList)
		}
		win.Present()
	})

	if code := app.Run(os.Args); code > 0 {
		os.Exit(code)
	}
}
