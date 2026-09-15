package ui

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"bao/internal/config"
)

// SettingsWindow 是设置对话框：
// 上部为排除目录列表（可增删，立即写回配置）；
// 中部为「开机自启」开关与「全局快捷键」绑定（分别落到 autostart 文件与 gsettings）；
// 底部为关闭按钮。功能变化不经由 onSaved（配置 JSON 只承载排除目录）。
type SettingsWindow struct {
	win     *gtk.Window
	cfg     *config.Config
	list    *gtk.ListBox
	entry   *gtk.Entry
	onSaved func(*config.Config)

	updating     bool       // 程序化切换 CheckButton 时抑制回调
	status       *gtk.Label // 底部操作结果/错误提示
	bindingLabel *gtk.Label // 当前全局快捷键绑定
}

// NewSettingsWindow 创建并显示模态设置窗口，返回窗口句柄
// （parent 销毁等场景可由调用方感知；正常关闭由窗口自身处理）。
// parent 为主窗口（用于 transient-for），cfg 为当前生效配置的副本，
// 对话框在其上就地修改并保存，保存后经 onSaved 回传，不影响主窗口旧配置。
func NewSettingsWindow(parent *gtk.Window, cfg *config.Config, onSaved func(*config.Config)) *SettingsWindow {
	s := &SettingsWindow{cfg: cfg, onSaved: onSaved}

	win := gtk.NewWindow()
	win.SetTitle("设置")
	win.SetTransientFor(parent)
	win.SetModal(true)
	win.SetDefaultSize(520, 640)
	win.SetResizable(false)
	s.win = win

	root := gtk.NewBox(gtk.OrientationVertical, 12)
	setMargins(&root.Widget, 12)
	win.SetChild(root)

	hint := gtk.NewLabel("以下目录会在文件搜索中被跳过（基名或完整路径前缀匹配）：")
	hint.SetXAlign(0)
	hint.SetWrap(true)
	root.Append(hint)

	scrolled := gtk.NewScrolledWindow()
	scrolled.SetMinContentHeight(180)
	scrolled.SetHasFrame(true)
	scrolled.SetVExpand(true)
	root.Append(scrolled)

	s.list = gtk.NewListBox()
	s.list.SetSelectionMode(gtk.SelectionNone)
	scrolled.SetChild(s.list)

	addBox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	s.entry = gtk.NewEntry()
	s.entry.SetPlaceholderText("新增排除目录，如 ~/Downloads 或 node_modules")
	s.entry.SetHExpand(true)
	s.entry.ConnectActivate(func() { s.addEntry() })
	addBtn := gtk.NewButton()
	addBtn.SetLabel("添加")
	addBtn.ConnectClicked(func() { s.addEntry() })
	addBox.Append(s.entry)
	addBox.Append(addBtn)
	root.Append(addBox)

	root.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	s.buildAutostartRow(root)
	root.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	s.buildKeybindRow(root)

	s.status = gtk.NewLabel("")
	s.status.SetXAlign(0)
	s.status.SetWrap(true)
	s.status.AddCSSClass("spot-subtitle")
	root.Append(s.status)

	closeBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	closeBtn := gtk.NewButton()
	closeBtn.SetLabel("关闭")
	closeBtn.SetHAlign(gtk.AlignEnd)
	closeBtn.ConnectClicked(func() { win.Close() })
	closeBox.Append(closeBtn)
	root.Append(closeBox)

	s.refresh()
	win.Present()
	return s
}

// buildAutostartRow 构造「开机自启」勾选行：状态来自 autostart 文件，
// 勾选写用户 autostart 文件，取消勾选写 Hidden=true（见 autostart.go）。
func (s *SettingsWindow) buildAutostartRow(root *gtk.Box) {
	box := gtk.NewBox(gtk.OrientationVertical, 4)

	cb := gtk.NewCheckButtonWithLabel("开机自启（登录后驻留系统托盘）")
	cb.SetTooltipText("在 ~/.config/autostart/ 写入或屏蔽 bao.desktop")
	enabled, err := AutostartEnabled()
	if err != nil {
		s.showStatus("读取开机自启状态失败: " + err.Error())
	}
	cb.SetActive(enabled)
	cb.ConnectToggled(func() {
		if s.updating {
			return
		}
		want := cb.Active()
		if err := SetAutostart(want); err != nil {
			// 写盘失败：回滚勾选状态并提示。
			s.updating = true
			cb.SetActive(!want)
			s.updating = false
			s.showStatus("设置开机自启失败: " + err.Error())
			return
		}
		if want {
			s.showStatus("已开启开机自启")
		} else {
			s.showStatus("已关闭开机自启（用户级屏蔽，不影响系统配置）")
		}
	})
	box.Append(cb)
	root.Append(box)
}

// buildKeybindRow 构造「全局快捷键」区：显示当前绑定，提供
// 录制 / 恢复默认 / 清除三个按钮；非 GNOME 环境整体禁用并提示。
func (s *SettingsWindow) buildKeybindRow(root *gtk.Box) {
	box := gtk.NewBox(gtk.OrientationVertical, 6)

	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	title := gtk.NewLabel("全局快捷键：")
	title.SetXAlign(0)
	row.Append(title)
	s.bindingLabel = gtk.NewLabel("")
	s.bindingLabel.SetXAlign(0)
	s.bindingLabel.SetEllipsize(pango.EllipsizeEnd)
	s.bindingLabel.SetHExpand(true)
	row.Append(s.bindingLabel)
	box.Append(row)

	btns := gtk.NewBox(gtk.OrientationHorizontal, 8)
	recordBtn := gtk.NewButton()
	recordBtn.SetLabel("录制…")
	recordBtn.ConnectClicked(func() { s.recordKeybind() })
	defaultBtn := gtk.NewButton()
	defaultBtn.SetLabel("恢复默认(Super+空格)")
	defaultBtn.ConnectClicked(func() {
		if err := KeybindResetDefault(); err != nil {
			s.showStatus("恢复默认快捷键失败: " + err.Error())
			return
		}
		s.showStatus("已恢复默认绑定 Super+空格")
		s.refreshBinding()
	})
	clearBtn := gtk.NewButton()
	clearBtn.SetLabel("清除")
	clearBtn.ConnectClicked(func() {
		if err := KeybindClear(); err != nil {
			s.showStatus("清除快捷键失败: " + err.Error())
			return
		}
		s.showStatus("已清除全局快捷键")
		s.refreshBinding()
	})
	btns.Append(recordBtn)
	btns.Append(defaultBtn)
	btns.Append(clearBtn)
	box.Append(btns)

	if !KeybindSupported() {
		box.SetSensitive(false)
		note := gtk.NewLabel("当前桌面环境不支持自动绑定（仅 GNOME）")
		note.SetXAlign(0)
		note.AddCSSClass("spot-subtitle")
		box.Append(note)
	}

	root.Append(box)
	s.refreshBinding()
}

// refreshBinding 刷新当前绑定显示；未绑定或读取失败显示「未绑定」。
func (s *SettingsWindow) refreshBinding() {
	if s.bindingLabel == nil {
		return
	}
	binding := KeybindGet()
	if binding == "" {
		binding = "未绑定"
	}
	s.bindingLabel.SetText("当前绑定：" + binding)
}

// recordKeybind 弹出模态录制窗口：捕获下一次完整按键组合
// （忽略纯修饰键，Esc 取消），确认后写入 gsettings。
func (s *SettingsWindow) recordKeybind() {
	win := gtk.NewWindow()
	win.SetTitle("录制快捷键")
	win.SetTransientFor(s.win)
	win.SetModal(true)
	win.SetDefaultSize(380, 120)
	win.SetResizable(false)

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	setMargins(&box.Widget, 16)
	win.SetChild(box)

	label := gtk.NewLabel("请按下新的快捷键组合…\n（Esc 取消）")
	label.SetWrap(true)
	box.Append(label)

	ctrl := gtk.NewEventControllerKey()
	ctrl.SetPropagationPhase(gtk.PhaseCapture)
	ctrl.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if keyval == gdk.KEY_Escape {
			win.Close()
			return true
		}
		accel, ok := accelFromKey(keyval, state)
		if !ok {
			return false // 纯修饰键：继续等待
		}
		win.Close()
		if err := KeybindSet(accel); err != nil {
			s.showStatus("绑定快捷键失败: " + err.Error())
			return true
		}
		s.showStatus("已绑定 " + accel)
		s.refreshBinding()
		return true
	})
	win.AddController(ctrl)

	win.Present()
}

// showStatus 在底部状态行显示一条提示。
func (s *SettingsWindow) showStatus(msg string) {
	if s.status != nil {
		s.status.SetText(msg)
	}
}

// refresh 重建排除目录列表。
func (s *SettingsWindow) refresh() {
	s.list.RemoveAll()
	for _, dir := range s.cfg.AllExcluded() {
		d := dir
		row := gtk.NewListBoxRow()

		box := gtk.NewBox(gtk.OrientationHorizontal, 8)
		setMargins(&box.Widget, 6)
		box.SetMarginStart(12)
		box.SetMarginEnd(12)

		label := gtk.NewLabel(d)
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetEllipsize(pango.EllipsizeEnd)
		box.Append(label)

		removeBtn := gtk.NewButton()
		removeBtn.SetLabel("移除")
		removeBtn.ConnectClicked(func() { s.removeEntry(d) })
		box.Append(removeBtn)

		row.SetChild(box)
		s.list.Append(row)
	}
}

// addEntry 新增一条排除目录：去空白、去重后立即保存。
func (s *SettingsWindow) addEntry() {
	dir := strings.TrimSpace(s.entry.Text())
	s.entry.SetText("")
	if dir == "" {
		return
	}
	for _, e := range s.cfg.AllExcluded() {
		if e == dir {
			return
		}
	}
	s.cfg.ExcludedDirs = append(s.cfg.ExcludedDirs, dir)
	s.save()
}

// removeEntry 移除一条排除目录并立即保存。
func (s *SettingsWindow) removeEntry(dir string) {
	kept := make([]string, 0, len(s.cfg.ExcludedDirs))
	for _, e := range s.cfg.ExcludedDirs {
		if e != dir {
			kept = append(kept, e)
		}
	}
	s.cfg.ExcludedDirs = kept
	s.save()
}

// save 写回配置文件并通知主窗口；写盘失败时仅刷新界面，不传播错误。
func (s *SettingsWindow) save() {
	if err := s.cfg.Save(); err == nil && s.onSaved != nil {
		s.onSaved(s.cfg)
	}
	s.refresh()
}
