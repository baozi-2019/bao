// Package ui 实现 bao 启动器的 GTK4 界面：
// macOS Spotlight 风格的无边框搜索面板（防抖触发应用/文件/计算三路搜索）
// + 结果列表 + 键盘导航。
package ui

import (
	"context"
	"log"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"bao/internal/apps"
	"bao/internal/build"
	"bao/internal/calc"
	"bao/internal/config"
	"bao/internal/search"
)

const (
	// debounceDelay 是输入防抖时长：文件索引让单次过滤纯内存化（毫秒级），
	// 可以承受更密的触发，打字跟手感更好。
	debounceDelay = 80 * time.Millisecond
	// maxApps 是应用结果上限。
	maxApps = 20
	// maxFiles 是文件结果上限。
	maxFiles = 100
	// maxRecent 是「最近使用文件」置顶条数。
	maxRecent = 5
	// cacheMaxAge 是文件索引的保鲜时长，超时后窗口唤起时后台重建。
	cacheMaxAge = 2 * time.Minute
)

// itemKind 标识结果列表中一条结果的类型。
type itemKind int

const (
	kindCalc itemKind = iota // 计算结果，回车复制
	kindApp                  // 桌面应用，回车启动
	kindFile                 // 文件/目录，回车用 gio open 打开
)

// 结果分组标识：计算结果单独置顶（无小标题），其后依次是应用、文件分组。
const (
	sectionCalc = iota // 计算结果：最顶，无分组小标题
	sectionApp         // 应用分组，小标题「应用程序」
	sectionFile        // 文件分组，小标题「文件」
)

// sectionOfItem 返回一条结果所属的分组。
func sectionOfItem(it item) int {
	switch it.kind {
	case kindApp:
		return sectionApp
	case kindFile:
		return sectionFile
	default:
		return sectionCalc
	}
}

// sectionTitle 返回分组的小标题文字；计算结果无小标题，返回空串。
func sectionTitle(it item) string {
	switch sectionOfItem(it) {
	case sectionApp:
		return "应用程序"
	case sectionFile:
		return "文件"
	default:
		return ""
	}
}

// filterMode 是过滤模式：apps 只搜应用、files 只搜文件，filterAll 为全量三路搜索。
type filterMode int

const (
	filterAll filterMode = iota
	filterApps
	filterFiles
)

// filterKeywords 是过滤关键字候选表（顺序即下拉列表顺序），匹配大小写不敏感。
var filterKeywords = []struct {
	word string
	mode filterMode
	desc string
}{
	{"apps", filterApps, "仅搜索应用程序"},
	{"files", filterFiles, "仅搜索文件"},
}

// resolveFilter 解析当前输入文本的过滤模式，返回：
//   - mode：本次搜索应使用的模式（committed 生效，或文本自带关键字时提交的新模式）；
//   - query：有效查询词（调用方自行 TrimSpace；模式内空文本即空查询）；
//   - partial：是否应弹出关键字候选下拉（仅全量态、文本为关键字严格非空前缀时为 true）；
//   - rewrite / doRewrite：文本自带关键字（如 "apps term"、"apps "）时为 true，rewrite
//     为重写后的查询词——调用方先把 committed 置为 mode 再 SetText(rewrite)，SetText 会
//     再次触发解析，此时文本已稳定、不再重写（重写每次去掉首个词，必然收敛）。
//
// 状态机：committed 是 Window 承载的已提交模式，不再从文本推导模式；文本自带关键字
// 优先于 committed（粘贴 "files x" 也会提交 files）；committed 非全量时文本就是查询词
// 本身，输入框不再保留关键字。
func resolveFilter(committed filterMode, text string) (mode filterMode, query string, partial bool, rewrite string, doRewrite bool) {
	lower := strings.ToLower(text)
	for i, r := range text {
		if unicode.IsSpace(r) {
			// 第一个词是关键字即提交：查询词可为空（"apps " → 空串）。
			for _, kw := range filterKeywords {
				if lower[:i] == kw.word {
					q := strings.TrimLeft(text[i:], " \t")
					return kw.mode, q, false, q, true
				}
			}
			break // 有空白但第一个词不是关键字：按 committed/全量语义处理
		}
	}
	if committed != filterAll {
		return committed, text, false, "", false
	}
	// 无空白：是否为某关键字的严格非空前缀。
	if lower != "" {
		for _, kw := range filterKeywords {
			if len(lower) < len(kw.word) && strings.HasPrefix(kw.word, lower) {
				return filterAll, text, true, "", false
			}
		}
	}
	return filterAll, text, false, "", false
}

// escAction 是 Esc 分层退出语义里的一次动作。
type escAction int

const (
	escClosePopup escAction = iota // 关候选下拉（文本不动）
	escClearText                   // 清文本，停留当前模式（药丸仍在）
	escExitFilter                  // 退出过滤模式回全量（药丸消失）
	escHideWindow                  // 隐藏窗口回托盘，进程驻留（退出程序走托盘菜单或 Ctrl+Q）
)

// escActionFor 按当前状态决定 Esc 应执行哪一层动作。
func escActionFor(popShown bool, text string, committed filterMode) escAction {
	switch {
	case popShown:
		return escClosePopup
	case text != "":
		return escClearText
	case committed != filterAll:
		return escExitFilter
	default:
		return escHideWindow
	}
}

// item 是结果列表中的一条展示项。
type item struct {
	kind     itemKind
	title    string // 主标题
	subtitle string // 副标题
	icon     string // 图标名
	value    string // 计算结果的文本 / 文件完整路径
	app      apps.App
}

// spotlightCSS 是 macOS Spotlight 风格的全部样式。
// 颜色集中在 @define-color 变量区（GTK CSS 变量机制，用 @name 引用），
// 字号集中在 --spot-fs-* 变量；每个样式块注释说明对应 Spotlight 的哪部分。
const spotlightCSS = `
/* ---------- 变量区：Spotlight 配色（GTK CSS 用 @name 引用） ---------- */
@define-color spot_bg rgba(30, 30, 34, 0.92);         /* 面板背景：近黑半透明 */
@define-color spot_fg rgb(235, 235, 240);             /* 主文字 */
@define-color spot_fg_dim rgb(152, 152, 160);         /* 副标题灰 */
@define-color spot_accent #0A84FF;                    /* macOS 强调蓝：选中行 */
@define-color spot_hover rgba(255, 255, 255, 0.07);   /* 行悬停高亮 */
@define-color spot_divider rgba(255, 255, 255, 0.09); /* 搜索区/结果区分隔线 */

/* ---------- 窗口外壳：整体透明，圆角与半透明交给内层 spot-frame ---------- */
.spot-window {
  background-color: transparent;
}

/* ---------- 面板衬底：Spotlight 深色半透明面板 + 圆角 + 多层柔和阴影 ---------- */
/* margin 留出投影出界空间（阴影会被窗口表面边缘裁剪）；
   三层外阴影由远及近：大范围低浓度（漂浮感）→ 中距离过渡 → 贴边落地，
   再加 1px 细腻内描边与顶部内高光，模仿 macOS 的柔和漂浮质感。 */
.spot-frame {
  --spot-fs-entry: 20px;
  --spot-fs-title: 15px;
  --spot-fs-subtitle: 12px;
  --spot-fs-placeholder: 13px;
  --spot-fs-section: 12px;
  background-color: @spot_bg;
  border-radius: 14px;
  border: 1px solid rgba(255, 255, 255, 0.09);
  box-shadow:
    0 20px 48px rgba(0, 0, 0, 0.36),
    0 8px 20px rgba(0, 0, 0, 0.30),
    0 1px 3px rgba(0, 0, 0, 0.28),
    inset 0 1px 0 rgba(255, 255, 255, 0.07);
  margin: 14px;
}

/* ---------- 搜索行：上下留白，左侧放大镜、右侧设置入口 ---------- */
.spot-search-row {
  padding: 14px 14px 10px 14px;
}
.spot-search-icon {
  color: @spot_fg_dim;
  margin: 0 6px 0 2px;
}

/* ---------- 搜索框：扁平无框、大字号、placeholder「搜索」 ---------- */
.spot-entry {
  background-color: transparent;
  box-shadow: none;
  color: @spot_fg;
  font-size: var(--spot-fs-entry);
  padding: 2px 4px;
}
/* 屏蔽主题（Yaru 等）在聚焦时画的焦点边框环，保持 Spotlight 的扁平观感。
   Yaru 用 outline 属性画焦点环（rgba(239,134,97,0.7)），这里一并清零。 */
.spot-entry:focus-within {
  box-shadow: none;
  border-color: transparent;
  outline-width: 0;
}
.spot-entry placeholder {
  color: @spot_fg_dim;
}

/* ---------- 设置齿轮：小、低对比，悬停时显形（Spotlight 无齿轮，弱化处理） ---------- */
.spot-settings-btn {
  background-color: transparent;
  box-shadow: none;
  padding: 3px;
  margin: 0 2px 0 6px;
  opacity: 0.38;
}
.spot-settings-btn:hover {
  opacity: 0.85;
}

/* ---------- 结果区：透明底 + 分隔线；隐藏滚动条但保留滚动能力 ---------- */
.spot-scrolled {
  background-color: transparent;
  border-top: 1px solid @spot_divider;
}
.spot-scrolled scrollbar {
  opacity: 0;
  min-width: 0;
  min-height: 0;
}

/* ---------- 结果列表：行间距 + 行圆角；选中行为 macOS 强调蓝 ---------- */
.spot-list {
  background-color: transparent;
}
.spot-list row {
  margin: 2px 10px;
  padding: 6px 8px;
  border-radius: 8px;
  background-color: transparent;
  color: @spot_fg;
}
.spot-list row:hover {
  background-color: @spot_hover;
}
.spot-list row:selected {
  background-color: @spot_accent;
  color: white;
}

/* ---------- 行内文字：主标题 + 灰色副标题；选中行反白 ---------- */
.spot-title {
  color: @spot_fg;
  font-size: var(--spot-fs-title);
}
.spot-subtitle {
  color: @spot_fg_dim;
  font-size: var(--spot-fs-subtitle);
}
.spot-list row:selected .spot-title,
.spot-list row:selected .spot-subtitle {
  color: white;
}

/* ---------- 分组小标题：「应用程序」「文件」（非交互，12px 灰字） ---------- */
.spot-section-header {
  color: @spot_fg_dim;
  font-size: var(--spot-fs-section);
  padding: 8px 12px 2px 18px;
}

/* ---------- 过滤关键字下拉提醒（Popover 浮层，深色圆角小面板） ---------- */
.spot-filter-pop {
  background-color: rgba(28, 28, 32, 0.97);
  border-radius: 10px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}
.spot-filter-pop > contents {
  background-color: rgba(28, 28, 32, 0.97);
  border-radius: 10px;
}
/* 候选行文字：files — 仅搜索文件 */
.spot-filter-item {
  color: @spot_fg;
  font-size: var(--spot-fs-subtitle);
  padding: 2px 6px;
}

/* ---------- 过滤模式提示：搜索框左侧的蓝色药丸标签 ---------- */
.spot-mode-chip {
  color: white;
  background-color: @spot_accent;
  border-radius: 10px;
  font-size: var(--spot-fs-subtitle);
  padding: 2px 10px;
  margin: 0 8px 0 0;
}

/* ---------- 空态提示 ---------- */
.spot-placeholder {
  color: @spot_fg_dim;
  font-size: var(--spot-fs-placeholder);
}
`

// applySpotlightStyle 把 spotlightCSS 注入当前 display 的样式上下文，
// 优先级用 APPLICATION 级（高于主题、低于用户配置）。
func applySpotlightStyle() {
	provider := gtk.NewCSSProvider()
	provider.LoadFromString(spotlightCSS)
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// Window 是 bao 主窗口：搜索 Entry + 结果 ListBox + 键盘导航。
// 所有 GTK 控件操作都在 GTK 主线程完成；耗时搜索在后台 goroutine 中执行，
// 结果通过 glib.IdleAdd 回灌主线程；generation 保证过期结果不覆盖新输入。
type Window struct {
	win   *gtk.ApplicationWindow
	entry *gtk.Entry
	list  *gtk.ListBox

	// 过滤模式 UI：关键字候选下拉（Popover）与模式提示药丸（Label）。
	// committed 是已提交的过滤模式（初值 filterAll）：提交后输入框只保留
	// 查询词，模式由该字段承载，不再从文本推导。
	committed      filterMode        // 已提交的过滤模式
	modeChip       *gtk.Label        // 搜索行上的模式提示标签，仅过滤模式可见
	filterPop      *gtk.Popover      // 关键字候选下拉浮层
	filterList     *gtk.ListBox      // 下拉候选列表
	filterRows     []*gtk.ListBoxRow // 候选行，顺序与 filterKeywords 对齐
	filterPopShown bool              // 下拉弹出状态由代码自行维护（SetAutohide(false)）

	mu       sync.RWMutex // 保护 cfg 指针（设置保存后整体替换）
	cfg      *config.Config
	listApps []apps.App // 启动时扫描的应用列表（只读）

	settingsDlg *SettingsWindow // 当前设置对话框（非 nil 时置顶而不是新开）
	aboutDlg    *gtk.Window     // 当前关于对话框（非 nil 时置顶而不是新开）

	items      []item
	debounce   *time.Timer
	generation uint64
	cancel     context.CancelFunc

	cacheMu    sync.Mutex    // 保护下面的缓存字段；持锁期间只允许再取 w.mu（顺序 cacheMu→w.mu）
	cache      *search.Cache // 文件索引；nil = 未就绪或已失效
	cacheBuild bool          // 索引构建进行中（防止重复触发）
}

// NewWindow 创建主窗口并装配控件。
// cfg 为当前生效配置（不可为 nil），appList 为 apps.Scan() 的结果。
func NewWindow(app *gtk.Application, cfg *config.Config, appList []apps.App) *Window {
	w := &Window{cfg: cfg, listApps: appList}

	window := gtk.NewApplicationWindow(app)
	window.SetTitle("bao")
	window.SetIconName("bao")      // 包子图标（packaging/assets/bao.svg 安装到 hicolor）
	window.SetDecorated(false)     // Spotlight：无边框窗口
	window.SetResizable(false)     // Spotlight：面板不可缩放
	window.SetDefaultSize(704, -1) // 含衬底 14px 透明环；面板视觉宽度约 680px，高度取自然高度
	// 窗口位置说明：GTK4 起移除了 gtk_window_set_position；已 grep gotk4
	// v0.4.1 模块缓存确认 gdk/v4、gdkwayland、gdkx11 中均不存在任何
	// Toplevel/Surface 级别的移动 API（无 MoveSize/MoveToRect 等函数）。
	// Wayland 下顶层窗口位置完全由合成器决定（GNOME/mutter 会把小窗口
	// 置于屏幕中央，接近「屏幕中央偏上」的目标），故代码不设位置。
	window.AddCSSClass("spot-window")
	w.win = window

	applySpotlightStyle()

	// 设置入口收到搜索行尾部：小尺寸、低对比、无框（Spotlight 没有齿轮按钮，弱化处理）。
	settingsBtn := gtk.NewButton()
	settingsBtn.SetTooltipText("设置")
	settingsBtn.SetChild(gtk.NewImageFromIconName("emblem-system-symbolic"))
	settingsBtn.SetHasFrame(false)
	settingsBtn.SetVAlign(gtk.AlignCenter)
	settingsBtn.AddCSSClass("spot-settings-btn")
	settingsBtn.ConnectClicked(func() { w.OpenSettings() })

	// 内层衬底：圆角半透明面板；窗口节点整体透明，
	// 圆角外侧像素直接透出合成器，形成圆角窗口观感。
	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.AddCSSClass("spot-frame")
	window.SetChild(root)

	// 搜索行：放大镜图标 + 大字号扁平输入框 + 行尾设置入口。
	searchRow := gtk.NewBox(gtk.OrientationHorizontal, 0)
	searchRow.AddCSSClass("spot-search-row")
	root.Append(searchRow)

	searchIcon := gtk.NewImageFromIconName("system-search-symbolic")
	searchIcon.SetPixelSize(18)
	searchIcon.SetVAlign(gtk.AlignCenter)
	searchIcon.AddCSSClass("spot-search-icon")
	searchRow.Append(searchIcon)

	w.entry = gtk.NewEntry()
	w.entry.SetPlaceholderText("搜索")
	w.entry.SetHasFrame(false)
	w.entry.SetHExpand(true)
	w.entry.SetVAlign(gtk.AlignCenter)
	w.entry.AddCSSClass("spot-entry")
	w.entry.ConnectChanged(func() { w.onQueryChanged() })
	// 回车执行选中项：Entry 会把 Return 消费为 activate 信号、不再冒泡到
	// 窗口级 KeyController，因此回车不能只依赖 onKeyPressed，这里必须接一份。
	w.entry.ConnectActivate(func() { w.activateSelectedOrFirst() })

	// 过滤模式提示药丸：放在搜索框左侧，仅过滤模式可见。
	w.modeChip = gtk.NewLabel("")
	w.modeChip.SetVisible(false)
	w.modeChip.SetVAlign(gtk.AlignCenter)
	w.modeChip.AddCSSClass("spot-mode-chip")

	searchRow.Append(w.modeChip)
	searchRow.Append(w.entry)

	searchRow.Append(settingsBtn)

	// 过滤关键字下拉提醒：文本是关键字的严格非空前缀时在搜索框下方弹出候选，
	// Tab/点击候选提交过滤模式并清空输入框。Popover 是非模态浮层，与
	// 结果列表相互独立；弹出状态由代码自行维护（SetAutohide(false)）。
	w.filterPop = gtk.NewPopover()
	w.filterPop.SetParent(w.entry)
	w.filterPop.SetAutohide(false)
	w.filterPop.SetHasArrow(false)
	w.filterPop.AddCSSClass("spot-filter-pop")
	w.filterList = gtk.NewListBox()
	w.filterList.SetSelectionMode(gtk.SelectionSingle)
	w.filterList.SetActivateOnSingleClick(true)
	w.filterList.AddCSSClass("spot-list") // 复用行样式：圆角行 + 强调蓝高亮
	w.filterList.ConnectRowActivated(func(row *gtk.ListBoxRow) { w.acceptFilterRow(row) })
	for _, kw := range filterKeywords {
		row := gtk.NewListBoxRow()
		label := gtk.NewLabel(kw.word + " — " + kw.desc)
		label.SetXAlign(0)
		label.AddCSSClass("spot-filter-item")
		row.SetChild(label)
		w.filterRows = append(w.filterRows, row)
		w.filterList.Append(row)
	}
	w.filterPop.SetChild(w.filterList)

	// Tab 拦截必须赶在 entry 默认的 Tab 焦点切换之前：
	// 用 capture 阶段的 KeyController，命中候选时消费按键。
	tabCtrl := gtk.NewEventControllerKey()
	tabCtrl.SetPropagationPhase(gtk.PhaseCapture)
	tabCtrl.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if keyval == gdk.KEY_Tab && w.filterPopShown {
			w.acceptFilterRow(w.filterList.SelectedRow())
			return true
		}
		return false
	})
	w.entry.AddController(tabCtrl)

	scrolled := gtk.NewScrolledWindow()
	scrolled.SetMinContentHeight(360)
	scrolled.SetHasFrame(false)
	scrolled.SetOverlayScrolling(true)
	scrolled.SetVExpand(true)
	scrolled.AddCSSClass("spot-scrolled")
	root.Append(scrolled)

	w.list = gtk.NewListBox()
	w.list.SetSelectionMode(gtk.SelectionSingle)
	w.list.SetActivateOnSingleClick(false)
	w.list.AddCSSClass("spot-list")
	w.list.ConnectRowActivated(func(row *gtk.ListBoxRow) { w.activateIndex(row.Index()) })
	// 分组小标题：同一分组的首行显示「应用程序」「文件」标题。
	// header 依附在 row 上但不是 ListBoxRow，天然不参与选中与键盘导航。
	w.list.SetHeaderFunc(w.updateRowHeader)
	placeholder := gtk.NewLabel("输入关键字搜索应用与文件\n输入数学表达式（如 2^10+1）直接计算")
	placeholder.SetSensitive(false)
	placeholder.AddCSSClass("spot-placeholder")
	w.list.SetPlaceholder(placeholder)
	scrolled.SetChild(w.list)

	keyCtrl := gtk.NewEventControllerKey()
	keyCtrl.ConnectKeyPressed(w.onKeyPressed)
	window.AddController(keyCtrl)

	return w
}

// Present 显示窗口、聚焦搜索框并选中已有输入；顺带按需预热文件索引
// （无缓存、超时或排除配置变更时后台重建，重复调用为空操作）。
func (w *Window) Present() {
	w.maybeWarmCache()
	w.win.Present()
	w.entry.GrabFocus()
	if w.entry.Text() != "" {
		w.entry.SelectRegion(0, -1)
	}
}

// Toggle 切换主窗口可见性：可见则隐藏，隐藏则显示并聚焦
// （系统托盘左键 Activate 与右键菜单「显示/隐藏」用）。
func (w *Window) Toggle() {
	if w.win.IsVisible() {
		w.win.SetVisible(false)
		return
	}
	w.Present()
}

// Close 退出程序（托盘「退出」菜单与 Ctrl+Q 共用；最后一个窗口关闭后应用退出）。
func (w *Window) Close() {
	// 关于弹窗与隐藏态下打开的设置窗都是独立窗口（非 transient），
	// 不随主窗口关闭，需一并销毁，否则残留窗口会阻止进程退出。
	if w.aboutDlg != nil {
		w.aboutDlg.Destroy()
	}
	if w.settingsDlg != nil {
		w.settingsDlg.win.Destroy()
	}
	// 隐藏态主窗口未映射，gtk_window_close 对未映射窗口是空操作，
	// 托盘「退出」会静默失败（进程驻留）；Destroy 才能可靠释放
	// ApplicationWindow 对 GApplication 的持有并退出进程。
	w.win.Destroy()
}

// OpenSettings 打开设置对话框（搜索行齿轮与托盘菜单「设置」共用）：
// 已存在则置顶，不重复开窗。传配置副本给对话框：对话框就地修改并保存
// 副本，保存成功后经 onConfigSaved 原子换入，避免后台文件搜索遍历配置时
// 与界面线程并发读写同一份切片。
func (w *Window) OpenSettings() {
	if w.settingsDlg != nil {
		w.settingsDlg.win.Present()
		return
	}
	cfg := w.currentConfig()
	w.settingsDlg = NewSettingsWindow(&w.win.Window, &config.Config{ExcludedDirs: cfg.AllExcluded()}, w.onConfigSaved)
	w.settingsDlg.win.ConnectDestroy(func() { w.settingsDlg = nil })
}

// OpenAbout 打开关于对话框（托盘菜单「关于」入口）：展示图标、版本号与
// 软件简介；已存在则置顶，不重复开窗。版本号取 build.Version
// （打包时由 git tag 经 ldflags 注入，本地构建为 dev）。
func (w *Window) OpenAbout() {
	if w.aboutDlg != nil {
		w.aboutDlg.Present()
		return
	}
	dlg := gtk.NewWindow()
	dlg.SetTitle("关于 bao")
	// 不做 transient/modal：托盘唤起时主窗口常处于隐藏态，Wayland 下
	// 未映射父窗口的子窗口拿不到合理定位（会贴到屏幕边缘）；
	// 独立窗口由合成器居中，关于弹窗也不需要模态约束。
	dlg.SetResizable(false)

	box := gtk.NewBox(gtk.OrientationVertical, 10)
	box.SetMarginTop(28)
	box.SetMarginBottom(20)
	box.SetMarginStart(32)
	box.SetMarginEnd(32)
	dlg.SetChild(box)

	icon := gtk.NewImageFromIconName("bao")
	icon.SetPixelSize(72)
	box.Append(icon)

	name := gtk.NewLabel("bao 启动器")
	name.AddCSSClass("title-2")
	box.Append(name)

	ver := build.Version
	if ver == "dev" {
		ver = "dev（本地未打包构建）"
	}
	version := gtk.NewLabel("版本 " + ver)
	version.AddCSSClass("dim-label")
	box.Append(version)

	intro := gtk.NewLabel("Albert 风格的 GTK4 桌面启动器：\n应用与文件模糊搜索 + 表达式计算，\n全局快捷键唤起、系统托盘驻留。")
	intro.SetJustify(gtk.JustifyCenter)
	intro.AddCSSClass("dim-label")
	box.Append(intro)

	closeBtn := gtk.NewButtonWithLabel("关闭")
	closeBtn.SetHAlign(gtk.AlignCenter)
	closeBtn.ConnectClicked(func() { dlg.Destroy() })
	box.Append(closeBtn)

	w.aboutDlg = dlg
	dlg.ConnectDestroy(func() { w.aboutDlg = nil })
	dlg.Present()
}

// currentConfig 返回当前配置指针。
func (w *Window) currentConfig() *config.Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cfg
}

// maybeWarmCache 按需触发文件索引后台构建：缓存缺失、超过保鲜时长或排除配置
// 快照不一致时重建，构建期间重复调用为空操作。窗口每次唤起（Present）时调用。
func (w *Window) maybeWarmCache() {
	w.cacheMu.Lock()
	if w.cacheBuild {
		w.cacheMu.Unlock()
		return
	}
	c := w.cache
	if c != nil && c.Age() < cacheMaxAge && c.MatchesConfig(w.currentConfig().AllExcluded()) {
		w.cacheMu.Unlock()
		return
	}
	w.cacheBuild = true
	w.cacheMu.Unlock()

	go func() {
		cfg := w.currentConfig()
		home, err := os.UserHomeDir()
		if err != nil {
			w.finishCacheBuild(nil)
			return
		}
		entries := search.Build(context.Background(), home, cfg.IsExcluded)
		w.finishCacheBuild(search.NewCache(entries, cfg.AllExcluded()))
	}()
}

// finishCacheBuild 在索引构建完成后换入缓存；配置在构建期间被修改的快照直接作废。
func (w *Window) finishCacheBuild(c *search.Cache) {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	w.cacheBuild = false
	if c != nil && c.MatchesConfig(w.currentConfig().AllExcluded()) {
		w.cache = c
	}
}

// currentFileCache 返回可直接服务的索引缓存；未就绪、超时或与当前排除配置
// 不一致时返回 nil（调用方回退到实时遍历，maybeWarmCache 会择机重建）。
func (w *Window) currentFileCache() *search.Cache {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	c := w.cache
	if c == nil {
		return nil
	}
	if c.Age() >= cacheMaxAge || !c.MatchesConfig(w.currentConfig().AllExcluded()) {
		return nil
	}
	return c
}

// onConfigSaved 在设置对话框写回配置后调用：替换配置、作废旧文件索引并立即重搜当前输入。
func (w *Window) onConfigSaved(cfg *config.Config) {
	w.mu.Lock()
	w.cfg = cfg
	w.mu.Unlock()

	// 排除配置已变更：旧索引快照作废，Present 时按需重建。
	w.cacheMu.Lock()
	w.cache = nil
	w.cacheMu.Unlock()
	w.maybeWarmCache()

	mode, query, _, _, doRewrite := resolveFilter(w.committed, w.entry.Text())
	if doRewrite {
		w.committed = mode
	}
	w.generation++
	gen := w.generation
	if w.cancel != nil {
		w.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go w.runSearch(ctx, strings.TrimSpace(query), gen, mode)
}

// onQueryChanged 在输入变化时（主线程）解析过滤模式：文本自带关键字则提交
// committed 并重写文本为查询词（SetText 会再次触发本函数，第二次文本已稳定、
// 不再重写）；否则按 committed 全量/模式语义更新药丸、候选下拉，并重启防抖搜索。
func (w *Window) onQueryChanged() {
	raw := w.entry.Text()
	mode, query, partial, rewrite, doRewrite := resolveFilter(w.committed, raw)
	if doRewrite {
		w.committed = mode
		w.entry.SetText(rewrite)
		return
	}
	w.updateModeChip(mode)
	w.updateFilterDropdown(raw, partial)

	query = strings.TrimSpace(query)
	w.generation++
	gen := w.generation
	if w.cancel != nil {
		w.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	if w.debounce != nil {
		w.debounce.Stop()
	}
	w.debounce = time.AfterFunc(debounceDelay, func() {
		w.runSearch(ctx, query, gen, mode)
	})
}

// updateModeChip 按当前过滤模式更新搜索行左侧的蓝色药丸提示标签。
func (w *Window) updateModeChip(mode filterMode) {
	switch mode {
	case filterApps:
		w.modeChip.SetText("应用程序")
		w.modeChip.SetVisible(true)
	case filterFiles:
		w.modeChip.SetText("文件")
		w.modeChip.SetVisible(true)
	default:
		w.modeChip.SetVisible(false)
	}
}

// updateFilterDropdown 按当前前缀更新候选下拉：列出匹配的关键字，
// 首个候选高亮；不在前缀状态或无匹配候选时收起。
func (w *Window) updateFilterDropdown(raw string, partial bool) {
	if !partial {
		w.hideFilterPopup()
		return
	}
	lower := strings.ToLower(raw)
	first := -1
	for i, kw := range filterKeywords {
		match := strings.HasPrefix(kw.word, lower)
		w.filterRows[i].SetVisible(match)
		if match && first < 0 {
			first = i
		}
	}
	if first < 0 {
		w.hideFilterPopup()
		return
	}
	w.filterList.SelectRow(w.filterRows[first])
	if !w.filterPopShown {
		w.filterPop.Popup()
		w.filterPopShown = true
	}
}

// hideFilterPopup 收起候选下拉（幂等）。
func (w *Window) hideFilterPopup() {
	if w.filterPopShown {
		w.filterPop.Popdown()
		w.filterPopShown = false
	}
}

// acceptFilterRow 接受一个候选（Tab 或鼠标点击，入口 b）：提交 committed
// 模式并清空输入框（关键字不留在输入框，由药丸提示），光标置末尾；
// SetText 触发的 onQueryChanged 按 committed 空查询处理（结果清空，
// 用户继续输入即在该模式内搜索）。
func (w *Window) acceptFilterRow(row *gtk.ListBoxRow) {
	w.hideFilterPopup()
	if row == nil || row.Index() < 0 || row.Index() >= len(filterKeywords) {
		return
	}
	kw := filterKeywords[row.Index()]
	w.committed = kw.mode
	w.entry.SetText("")
	w.entry.SetPosition(-1)
	w.entry.GrabFocus()
}

// runSearch 在后台 goroutine 中按过滤模式执行搜索：
// 计算结果置顶（仅全量模式），其后是应用（全量/apps 模式）；
// 文件一路优先走内存索引（最近使用文件置顶、按路径去重），索引未就绪时
// 回退实时遍历，结果稍后追加（全量/files 模式）。
// 任何一路的产出都通过 glib.IdleAdd 回灌主线程。
func (w *Window) runSearch(ctx context.Context, query string, gen uint64, mode filterMode) {
	items := make([]item, 0, maxApps+1)
	if query != "" {
		if calc.LooksLikeMath(query) && mode == filterAll {
			if v, ok := calc.Eval(query); ok {
				text := formatNumber(v)
				items = append(items, item{
					kind:     kindCalc,
					title:    text,
					subtitle: query + " 的计算结果，回车复制",
					icon:     "accessories-calculator",
					value:    text,
				})
			}
		}
		if mode == filterAll || mode == filterApps {
			for _, a := range apps.Search(w.listApps, query, maxApps) {
				items = append(items, newAppItem(a))
			}
		}
	}
	glib.IdleAdd(func() { w.applyItems(gen, items) })

	if query == "" || mode == filterApps {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	cfg := w.currentConfig()

	// 文件一路：索引就绪走纯内存过滤（全局最优前 maxFiles 个），否则回退实时遍历。
	var files []search.Result
	if c := w.currentFileCache(); c != nil {
		files = c.Filter(query, maxFiles)
	} else {
		files = search.Files(ctx, home, query, cfg.IsExcluded, maxFiles)
	}
	// 最近使用文件置顶：解析 xbel 仅几毫秒，两条路都适用；按路径去重。
	if recent := search.Recent(home, query, maxRecent); len(recent) > 0 {
		seen := make(map[string]bool, len(recent))
		for _, r := range recent {
			seen[r.Path] = true
		}
		kept := files[:0]
		for _, f := range files {
			if !seen[f.Path] {
				kept = append(kept, f)
			}
		}
		files = append(recent, kept...)
		if len(files) > maxFiles {
			files = files[:maxFiles]
		}
	}
	glib.IdleAdd(func() { w.appendItems(gen, files) })
}

// applyItems 用新结果整体重建结果列表（主线程）。
func (w *Window) applyItems(gen uint64, items []item) {
	if gen != w.generation {
		return
	}
	w.items = items
	w.list.RemoveAll()
	for _, it := range items {
		w.list.Append(w.newRow(it))
	}
	if len(items) > 0 {
		w.list.SelectRow(w.list.RowAtIndex(0))
	}
}

// appendItems 把文件搜索结果追加到列表末尾（主线程）。
func (w *Window) appendItems(gen uint64, files []search.Result) {
	if gen != w.generation || len(files) == 0 {
		return
	}
	for _, f := range files {
		it := item{
			kind:     kindFile,
			title:    f.Name,
			subtitle: f.Path,
			icon:     "text-x-generic-symbolic",
			value:    f.Path,
		}
		w.items = append(w.items, it)
		w.list.Append(w.newRow(it))
	}
}

// newRow 构造一行结果：图标 + 主标题 + 副标题。
func (w *Window) newRow(it item) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetActivatable(true)

	box := gtk.NewBox(gtk.OrientationHorizontal, 12)

	// Icon 字段可能是图标名，也可能是绝对路径（如微信、腾讯会议的 .desktop），
	// 绝对路径且文件存在时按文件加载，否则按图标名解析（含缺省占位）。
	var icon *gtk.Image
	if strings.HasPrefix(it.icon, "/") {
		if _, err := os.Stat(it.icon); err == nil {
			icon = gtk.NewImageFromFile(it.icon)
		}
	}
	if icon == nil {
		icon = gtk.NewImageFromIconName(it.icon)
	}
	icon.SetIconSize(gtk.IconSizeLarge)
	icon.SetPixelSize(32)
	icon.SetVAlign(gtk.AlignCenter)
	box.Append(icon)

	labels := gtk.NewBox(gtk.OrientationVertical, 0)
	labels.SetHExpand(true)
	labels.SetVAlign(gtk.AlignCenter)

	title := gtk.NewLabel(it.title)
	title.SetXAlign(0)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.SetMaxWidthChars(64)
	title.AddCSSClass("spot-title")
	labels.Append(title)

	if it.subtitle != "" {
		subtitle := gtk.NewLabel(it.subtitle)
		subtitle.SetXAlign(0)
		subtitle.SetEllipsize(pango.EllipsizeEnd)
		subtitle.SetMaxWidthChars(64)
		subtitle.AddCSSClass("spot-subtitle")
		labels.Append(subtitle)
	}

	box.Append(labels)
	row.SetChild(box)
	return row
}

// updateRowHeader 是结果列表的 header func：分组首行显示「应用程序」「文件」
// 小标题，其余行隐藏；空分组没有行也就不会出现标题。GTK 会对同一 row 反复
// 调用此回调，因此必须复用 row 上已有的 header widget（row.Header()），
// 只切换 Visible 与文字，不重复创建。header 依附在 row 上但不是 ListBoxRow，
// 不参与选中、键盘导航与激活，行号索引与 w.items 一一对应。
func (w *Window) updateRowHeader(row, before *gtk.ListBoxRow) {
	hide := func() {
		if h := row.Header(); h != nil {
			if hv, ok := h.(interface{ SetVisible(bool) }); ok {
				hv.SetVisible(false)
			}
		}
	}
	// 防御：列表重建期间 w.items 已替换而旧行尚未删完，索引可能越界。
	idx := row.Index()
	if idx < 0 || idx >= len(w.items) {
		hide()
		return
	}
	title := sectionTitle(w.items[idx])
	if title == "" {
		hide() // 计算结果行：无小标题
		return
	}
	show := true
	if before != nil {
		// 与前一行同组则不显示标题；before 索引越界按不同组处理。
		bi := before.Index()
		show = bi < 0 || bi >= len(w.items) ||
			sectionOfItem(w.items[bi]) != sectionOfItem(w.items[idx])
	}
	if !show {
		hide()
		return
	}
	header, ok := row.Header().(*gtk.Label)
	if !ok {
		header = gtk.NewLabel("")
		header.SetXAlign(0)
		header.SetSensitive(false) // 非交互：不可点、不进入选中
		header.AddCSSClass("spot-section-header")
		row.SetHeader(header)
	}
	header.SetText(title)
	header.SetVisible(true)
}

// onKeyPressed 处理全局按键：回车执行、Esc 分层隐藏、Ctrl+Q 退出、上下移动选中。
func (w *Window) onKeyPressed(keyval, keycode uint, state gdk.ModifierType) bool {
	switch keyval {
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		w.activateSelectedOrFirst()
		return true
	case gdk.KEY_Escape:
		// 分层退出：关候选下拉 → 清文本（停留当前模式）→ 退出过滤模式回全量 →
		// 隐藏窗口回托盘（进程驻留；退出程序只能走托盘菜单或 Ctrl+Q）。
		switch escActionFor(w.filterPopShown, w.entry.Text(), w.committed) {
		case escClosePopup:
			w.hideFilterPopup()
		case escClearText:
			w.entry.SetText("")
		case escExitFilter:
			w.committed = filterAll
			w.updateModeChip(filterAll)
		case escHideWindow:
			w.win.SetVisible(false)
		}
		return true
	case gdk.KEY_q:
		if state&gdk.ControlMask != 0 {
			w.Close()
			return true
		}
		return false
	case gdk.KEY_Up:
		w.moveSelection(-1)
		return true
	case gdk.KEY_Down:
		w.moveSelection(1)
		return true
	}
	return false
}

// moveSelection 上下移动选中行（钳位在首尾之间）。
func (w *Window) moveSelection(delta int) {
	n := len(w.items)
	if n == 0 {
		return
	}
	idx := 0
	if row := w.list.SelectedRow(); row != nil {
		idx = row.Index() + delta
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	w.list.SelectRow(w.list.RowAtIndex(idx))
}

// activateSelectedOrFirst 激活当前选中行；无选中时激活第一条。
func (w *Window) activateSelectedOrFirst() {
	if len(w.items) == 0 {
		return
	}
	idx := 0
	if row := w.list.SelectedRow(); row != nil {
		idx = row.Index()
	}
	w.activateIndex(idx)
}

// activateIndex 执行一条结果：计算结果复制到剪贴板；应用启动；文件用 gio open 打开。
func (w *Window) activateIndex(idx int) {
	if idx < 0 || idx >= len(w.items) {
		return
	}
	it := w.items[idx]
	switch it.kind {
	case kindCalc:
		w.entry.Display().Clipboard().SetText(it.value)
	case kindApp:
		app := it.app
		go func() {
			if err := apps.Launch(app); err != nil {
				log.Println("启动应用失败:", app.Name, err)
			}
		}()
		w.entry.SetText("")
		w.win.SetVisible(false)
	case kindFile:
		path := it.value
		go func() {
			if err := exec.Command("gio", "open", path).Run(); err != nil {
				log.Println("打开文件失败:", path, err)
			}
		}()
		w.entry.SetText("")
		w.win.SetVisible(false)
	}
}

// newAppItem 把应用转换为展示项。
func newAppItem(a apps.App) item {
	icon := a.Icon
	if icon == "" {
		icon = "application-x-executable"
	}
	subtitle := a.Comment
	if subtitle == "" {
		subtitle = a.Exec
	}
	return item{kind: kindApp, title: a.Name, subtitle: subtitle, icon: icon, app: a}
}

// formatNumber 把计算结果格式化为简洁文本：
// 绝对值小于 1e15 的整数不带小数点，其余用 'g' 最短表示。
func formatNumber(v float64) string {
	if math.Trunc(v) == v && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// setMargins 统一设置控件四边外边距。
func setMargins(widget *gtk.Widget, margin int) {
	widget.SetMarginTop(margin)
	widget.SetMarginBottom(margin)
	widget.SetMarginStart(margin)
	widget.SetMarginEnd(margin)
}
