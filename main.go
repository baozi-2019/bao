// bao 是 Albert 风格的 GTK4 桌面启动器入口：
// 组装配置、应用扫描与主窗口，运行 GTK 应用主循环。
package main

import (
	"fmt"
	"os"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"bao/internal/apps"
	"bao/internal/config"
	"bao/internal/sni"
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

	// 解析自有参数：--hidden 只驻留不弹窗（开机自启用），
	// 从参数列表剔除后交给 GApplication，避免其因未知选项报错。
	hidden := false
	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args {
		if a == "--hidden" {
			hidden = true
			continue
		}
		args = append(args, a)
	}

	app := gtk.NewApplication("dev.bao.launcher", gio.ApplicationFlagsNone)

	var win *ui.Window
	app.ConnectActivate(func() {
		if win == nil {
			win = ui.NewWindow(app, cfg, appList)

			// 系统托盘：协议失败仅打印不致命，bao 照常运行。
			// sni 回调跑在 godbus 的 goroutine 上，GTK 操作必须经
			// glib.IdleAdd 调度回 GTK 主线程。
			tray, err := sni.New("bao 启动器", "bao")
			if err != nil {
				fmt.Fprintln(os.Stderr, "系统托盘初始化失败（不影响使用）:", err)
			} else {
				tray.OnActivate(func() { glib.IdleAdd(func() { win.Toggle() }) })
				tray.OnToggle(func() { glib.IdleAdd(func() { win.Toggle() }) })
				tray.OnSettings(func() { glib.IdleAdd(func() { win.OpenSettings() }) })
				tray.OnQuit(func() { glib.IdleAdd(func() { win.Close() }) })
			}
		}
		if !hidden {
			win.Present()
		}
	})

	if code := app.Run(args); code > 0 {
		os.Exit(code)
	}
}
