package ui

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"bao/internal/config"
)

// SettingsWindow 是排除目录设置对话框：
// 列表展示当前生效的排除目录，可逐条移除；底部输入框可新增；
// 每次增删立即写回配置文件并通过 onSaved 通知主窗口。
type SettingsWindow struct {
	win     *gtk.Window
	cfg     *config.Config
	list    *gtk.ListBox
	entry   *gtk.Entry
	onSaved func(*config.Config)
}

// NewSettingsWindow 创建并显示模态设置窗口。
// parent 为主窗口（用于 transient-for），cfg 为当前生效配置的副本，
// 对话框在其上就地修改并保存，保存后经 onSaved 回传，不影响主窗口旧配置。
func NewSettingsWindow(parent *gtk.Window, cfg *config.Config, onSaved func(*config.Config)) {
	s := &SettingsWindow{cfg: cfg, onSaved: onSaved}

	win := gtk.NewWindow()
	win.SetTitle("排除目录设置")
	win.SetTransientFor(parent)
	win.SetModal(true)
	win.SetDefaultSize(520, 400)
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
	scrolled.SetMinContentHeight(220)
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

	closeBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	closeBtn := gtk.NewButton()
	closeBtn.SetLabel("关闭")
	closeBtn.SetHAlign(gtk.AlignEnd)
	closeBtn.ConnectClicked(func() { win.Close() })
	closeBox.Append(closeBtn)
	root.Append(closeBox)

	s.refresh()
	win.Present()
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
