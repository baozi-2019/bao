// Package sni 实现 freedesktop StatusNotifierItem 系统托盘协议
// （org.kde.StatusNotifierItem + org.freedesktop.DBus.Properties +
// com.canonical.dbusmenu），供 bao 启动器驻留桌面托盘。
// 本包是纯 D-Bus 协议实现，不依赖 GTK；方法回调运行在 godbus 的
// 连接 goroutine 上，调用方负责把 GTK 操作调度回 GTK 主线程
// （如 gotk4 的 glib.IdleAdd）。
package sni

import (
	"bytes"
	"embed"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// iconFS 内嵌全部托盘图标（16..256 八个尺寸），由
// internal/sni/assets/gen 按 packaging/assets/bao.svg 造型 Go 原生重绘：
// SDF 矢量（圆角矩形/椭圆/胶囊线段的解析距离场）+ 8 倍画布 8x8 超采样
// 盒式降采样，圆角透明、边缘平滑。
// 重新生成：go run ./internal/sni/assets/gen
//
//go:embed assets/icon_16.png assets/icon_22.png assets/icon_24.png assets/icon_32.png assets/icon_48.png assets/icon_64.png assets/icon_128.png assets/icon_256.png
var iconFS embed.FS

// iconFiles 是内嵌图标的文件名，按尺寸从大到小排列
// （IconPixmap 全尺寸提供，宿主按需要的尺寸挑选）。
var iconFiles = []string{
	"assets/icon_256.png",
	"assets/icon_128.png",
	"assets/icon_64.png",
	"assets/icon_48.png",
	"assets/icon_32.png",
	"assets/icon_24.png",
	"assets/icon_22.png",
	"assets/icon_16.png",
}

const (
	// itemPath 是 SNI 项导出的 D-Bus 对象路径，取协议惯例默认路径
	// （ubuntu-appindicators 的 DEFAULT_ITEM_OBJECT_PATH）：以总线名向 watcher
	// 注册时宿主到该路径读取属性与信号，扩展的 busAnalyzer 兜底扫描也依赖
	// 默认路径才能正确识别项，故不再导出额外自定义路径。
	itemPath = dbus.ObjectPath("/StatusNotifierItem")
	// menuPath 是右键菜单（dbusmenu）导出的对象路径。
	menuPath = dbus.ObjectPath("/org/bao/Menu")

	// watcherDest / watcherPath 是状态通知器观察者的地址。
	watcherDest  = "org.kde.StatusNotifierWatcher"
	watcherPath  = dbus.ObjectPath("/StatusNotifierWatcher")
	watcherIface = "org.kde.StatusNotifierWatcher"

	itemIface       = "org.kde.StatusNotifierItem"
	menuIface       = "com.canonical.dbusmenu"
	propsIface      = "org.freedesktop.DBus.Properties"
	introspectIface = "org.freedesktop.DBus.Introspectable"
)

// itemIntrospectXML 是 SNI 对象的内省数据：
// ubuntu-appindicators 会通过 Introspect 探测 Activate 方法是否存在，
// 缺失会导致左键激活能力被关闭，因此必须提供。
const itemIntrospectXML = `<node>
  <interface name="org.kde.StatusNotifierItem">
    <method name="Activate"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="SecondaryActivate"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="Scroll"><arg type="i" direction="in"/><arg type="s" direction="in"/></method>
    <signal name="NewIcon"/>
    <signal name="NewAttentionIcon"/>
    <signal name="NewToolTip"/>
    <signal name="NewStatus"><arg type="s"/></signal>
  </interface>
</node>`

// pixmap 对应 SNI 的 IconPixmap 元素 (iiay)：
// 宽、高与按网络字节序打包的 ARGB32 像素数据。
type pixmap struct {
	Width  int32
	Height int32
	Bytes  []byte
}

// tooltip 对应 SNI 的 ToolTip 属性 (sa(iiay)ss)。
type tooltip struct {
	IconName   string
	IconPixmap []pixmap
	Title      string
	Text       string
}

// menuLayout 对应 dbusmenu 的布局节点 (ia{sv}av)：
// Children 必须是 Variant 数组，每个元素内嵌一个完整布局节点。
type menuLayout struct {
	Id         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

// groupProps 对应 dbusmenu GetGroupProperties 的返回元素 (ia{sv})。
type groupProps struct {
	Id         int32
	Properties map[string]dbus.Variant
}

// Tray 是一个系统托盘项：左键触发 onActivate，右键弹出
// 「显示/隐藏」「设置」「关于」「退出」菜单。零值不可用，必须用 New 创建。
type Tray struct {
	conn     *dbus.Conn
	busName  string // 本进程持有的总线名（注册时告知 watcher）
	title    string
	iconName string
	pixmaps  []pixmap // 全部内嵌尺寸的 ARGB 像素，属性查询时直接引用

	mu         sync.RWMutex
	onActivate func()
	onToggle   func()
	onSettings func()
	onAbout    func()
	onQuit     func()
	closed     bool
	registered bool          // 与 watcher 的注册是否成功
	retrying   bool          // 退避重试 goroutine 是否在跑
	stopCh     chan struct{} // Close 时关闭，用于退出重试与监听 goroutine
}

// New 创建托盘项：连接 session bus、申请总线名、导出 SNI 对象与
// 菜单对象，并向 org.kde.StatusNotifierWatcher 注册。
// title 为悬停提示文字，iconName 为主题图标名（IconName 属性，
// 供已安装 bao.svg 图标主题的环境使用）；图标数据用内嵌的
// iconFS（全部尺寸，IconPixmap 属性按尺寸全量提供，无主题图标时兜底）。
// watcher 不存在或注册失败不视为致命错误：后台退避重试，且监听 watcher
// 总线名的 NameOwnerChanged，在其重新出现（扩展重载/shell 重启）后自动重注册。
func New(title, iconName string) (*Tray, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 session bus: %w", err)
	}

	name := "org.bao.Tray"
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("申请总线名 %s: %w", name, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner && reply != dbus.RequestNameReplyAlreadyOwner {
		// 同名已被占用（残留的 bao 实例等）：退回 PID 后缀名再试一次。
		name = fmt.Sprintf("org.bao.Tray-%d", os.Getpid())
		reply, err = conn.RequestName(name, dbus.NameFlagDoNotQueue)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("申请总线名 %s: %w", name, err)
		}
		if reply != dbus.RequestNameReplyPrimaryOwner && reply != dbus.RequestNameReplyAlreadyOwner {
			conn.Close()
			return nil, fmt.Errorf("总线名 %s 已被占用", name)
		}
	}

	pixmaps, err := pngsToPixmaps(iconFS, iconFiles)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("解析托盘图标 PNG: %w", err)
	}

	t := &Tray{
		conn:     conn,
		busName:  name,
		title:    title,
		iconName: iconName,
		pixmaps:  pixmaps,
		stopCh:   make(chan struct{}),
	}

	// SNI 项对象与属性接口：同时导出到自定义路径与协议默认路径
	// （按总线名注册时宿主访问的是默认路径）。
	itemMethods := map[string]any{
		"Activate":          t.Activate,
		"SecondaryActivate": t.Activate,
		"Scroll":            t.Scroll,
	}
	propsMethods := map[string]any{
		"Get":    t.GetProperty,
		"GetAll": t.GetAllProperties,
		"Set":    t.SetProperty,
	}
	introspectMethods := map[string]any{
		"Introspect": t.Introspect,
	}
	if err := conn.ExportMethodTable(itemMethods, itemPath, itemIface); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.ExportMethodTable(propsMethods, itemPath, propsIface); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.ExportMethodTable(introspectMethods, itemPath, introspectIface); err != nil {
		conn.Close()
		return nil, err
	}
	// 菜单（dbusmenu）对象。
	if err := conn.ExportMethodTable(map[string]any{
		"GetLayout":           t.GetLayout,
		"GetGroupProperties":  t.GetGroupProperties,
		"GetProperty":         t.GetMenuProperty,
		"Event":               t.Event,
		"AboutToShow":         t.AboutToShow,
		"AboutToShowGroup":    t.AboutToShowGroup,
		"EventGroup":          t.EventGroup,
		"GetSerializedLayout": t.GetSerializedLayout,
	}, menuPath, menuIface); err != nil {
		conn.Close()
		return nil, err
	}
	// 属性接口在菜单对象上也导出一份（菜单属性集为空，避免宿主探测报错）。
	if err := conn.ExportMethodTable(propsMethods, menuPath, propsIface); err != nil {
		conn.Close()
		return nil, err
	}

	// 监听 watcher 总线名的归属变化：AppIndicator 扩展禁用/重载会销毁全部托盘项
	// 并依赖应用自行重注册（wechat/flameshot 等 libappindicator 客户端均如此），
	// 不监听则一次 shell/扩展重载就会让图标永久丢失。
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath("/org/freedesktop/DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, watcherDest),
	); err != nil {
		conn.Close()
		return nil, fmt.Errorf("订阅 watcher 名称变化: %w", err)
	}
	sigCh := make(chan *dbus.Signal, 16)
	conn.Signal(sigCh)
	go t.watchWatcher(sigCh)

	t.register()
	return t, nil
}

// register 向 watcher 注册本托盘项：失败时启动退避重试
// （200ms 起翻倍、封顶 10s），watcher 消失又出现时由 watchWatcher 再次触发。
func (t *Tray) register() {
	t.mu.Lock()
	if t.closed || t.registered || t.retrying {
		t.mu.Unlock()
		return
	}
	t.retrying = true
	t.mu.Unlock()
	go t.registerLoop()
}

// registerLoop 持续重试注册直到成功或 Close。
func (t *Tray) registerLoop() {
	delay := 200 * time.Millisecond
	for {
		if t.tryRegister() {
			t.mu.Lock()
			t.registered = true
			t.retrying = false
			t.mu.Unlock()
			return
		}
		select {
		case <-t.stopCh:
			t.mu.Lock()
			t.retrying = false
			t.mu.Unlock()
			return
		case <-time.After(delay):
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

// tryRegister 向 watcher 发一次注册请求；成功后广播图标与提示刷新信号
// （部分宿主依赖首次信号拉取图标）。
func (t *Tray) tryRegister() bool {
	call := t.conn.Object(watcherDest, watcherPath).
		Call(watcherIface+".RegisterStatusNotifierItem", 0, t.busName)
	if call.Err != nil {
		return false
	}
	// 广播失败说明连接已异常，视为注册失败交给重试循环接管。
	if err := t.conn.Emit(itemPath, itemIface+".NewIcon"); err != nil {
		return false
	}
	if err := t.conn.Emit(itemPath, itemIface+".NewToolTip"); err != nil {
		return false
	}
	return t.conn.Emit(menuPath, menuIface+".LayoutUpdated", uint32(1), int32(0)) == nil
}

// watchWatcher 监听 watcher 总线名的 NameOwnerChanged：名称失去所有者
// （扩展禁用/shell 重启）时标记未注册，重新获得所有者时触发重注册。
func (t *Tray) watchWatcher(ch chan *dbus.Signal) {
	for {
		select {
		case <-t.stopCh:
			return
		case sig, ok := <-ch:
			if !ok {
				return
			}
			if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) != 3 {
				continue
			}
			name, _ := sig.Body[0].(string)
			if name != watcherDest {
				continue
			}
			owner, _ := sig.Body[2].(string)
			if owner == "" {
				t.mu.Lock()
				t.registered = false
				t.mu.Unlock()
				continue
			}
			t.register()
		}
	}
}

// OnActivate 设置左键点击托盘图标时的回调。
func (t *Tray) OnActivate(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onActivate = fn
}

// OnToggle 设置右键菜单「显示/隐藏」被点击时的回调。
func (t *Tray) OnToggle(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onToggle = fn
}

// OnSettings 设置右键菜单「设置」被点击时的回调。
func (t *Tray) OnSettings(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onSettings = fn
}

// OnAbout 设置右键菜单「关于」被点击时的回调。
func (t *Tray) OnAbout(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onAbout = fn
}

// OnQuit 设置右键菜单「退出」被点击时的回调。
func (t *Tray) OnQuit(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onQuit = fn
}

// Close 关闭 D-Bus 连接并注销托盘项（幂等）。
func (t *Tray) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	close(t.stopCh)
	t.mu.Unlock()
	return t.conn.Close()
}

// Activate 处理 SNI 的 Activate(ii)（左键单击，SecondaryActivate 复用）。
func (t *Tray) Activate(x, y int32) *dbus.Error {
	t.mu.RLock()
	fn := t.onActivate
	t.mu.RUnlock()
	if fn != nil {
		fn()
	}
	return nil
}

// Scroll 处理滚轮事件：bao 托盘不响应滚动，直接忽略。
func (t *Tray) Scroll(delta int32, orientation string) *dbus.Error {
	return nil
}

// Introspect 实现 org.freedesktop.DBus.Introspectable.Introspect：
// ubuntu-appindicators 用它探测 Activate 方法以决定是否启用左键激活。
func (t *Tray) Introspect() (string, *dbus.Error) {
	return itemIntrospectXML, nil
}

// itemProps 返回 SNI 项的完整属性表（每次新建，属性值均为快照）。
func (t *Tray) itemProps() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"Category":            dbus.MakeVariant("ApplicationStatus"),
		"Id":                  dbus.MakeVariant("bao"),
		"Title":               dbus.MakeVariant(t.title),
		"Status":              dbus.MakeVariant("Active"),
		"WindowId":            dbus.MakeVariant(int32(0)),
		"IconName":            dbus.MakeVariant(t.iconName),
		"IconPixmap":          dbus.MakeVariant(t.pixmaps),
		"OverlayIconName":     dbus.MakeVariant(""),
		"OverlayIconPixmap":   dbus.MakeVariant([]pixmap{}),
		"AttentionIconName":   dbus.MakeVariant(""),
		"AttentionIconPixmap": dbus.MakeVariant([]pixmap{}),
		"AttentionMovieName":  dbus.MakeVariant(""),
		"ToolTip":             dbus.MakeVariant(tooltip{t.iconName, t.pixmaps, t.title, t.title}),
		"ItemIsMenu":          dbus.MakeVariant(false),
		"Menu":                dbus.MakeVariant(menuPath),
	}
}

// GetProperty 实现 org.freedesktop.DBus.Properties.Get。
func (t *Tray) GetProperty(iface, prop string) (dbus.Variant, *dbus.Error) {
	if iface != itemIface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs",
			[]any{fmt.Sprintf("未知属性接口 %s", iface)})
	}
	v, ok := t.itemProps()[prop]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs",
			[]any{fmt.Sprintf("未知属性 %s", prop)})
	}
	return v, nil
}

// GetAllProperties 实现 org.freedesktop.DBus.Properties.GetAll。
func (t *Tray) GetAllProperties(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != itemIface {
		return map[string]dbus.Variant{}, nil
	}
	return t.itemProps(), nil
}

// SetProperty 实现 org.freedesktop.DBus.Properties.Set：托盘属性只读。
func (t *Tray) SetProperty(iface, prop string, v dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly",
		[]any{"StatusNotifierItem 属性只读"})
}

// menuItemProps 返回一个菜单项的属性表。
func menuItemProps(label string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(label),
		"enabled": dbus.MakeVariant(true),
		"visible": dbus.MakeVariant(true),
	}
}

// rootMenuProps 返回菜单根节点属性。
func rootMenuProps() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"type":             dbus.MakeVariant("standard"),
		"children-display": dbus.MakeVariant("submenu"),
	}
}

// filterProps 按 propertyNames 过滤属性；空表表示不过滤。
func filterProps(props map[string]dbus.Variant, names []string) map[string]dbus.Variant {
	if len(names) == 0 {
		return props
	}
	out := make(map[string]dbus.Variant, len(names))
	for _, n := range names {
		if v, ok := props[n]; ok {
			out[n] = v
		}
	}
	return out
}

// layoutTree 构造完整菜单布局：根节点下挂「显示/隐藏」「设置」「关于」「退出」四个扁平项。
func layoutTree(names []string) menuLayout {
	toggle := menuLayout{Id: 1, Properties: filterProps(menuItemProps("显示/隐藏"), names), Children: []dbus.Variant{}}
	settings := menuLayout{Id: 2, Properties: filterProps(menuItemProps("设置"), names), Children: []dbus.Variant{}}
	about := menuLayout{Id: 3, Properties: filterProps(menuItemProps("关于"), names), Children: []dbus.Variant{}}
	quit := menuLayout{Id: 4, Properties: filterProps(menuItemProps("退出"), names), Children: []dbus.Variant{}}
	return menuLayout{
		Id:         0,
		Properties: filterProps(rootMenuProps(), names),
		Children: []dbus.Variant{
			dbus.MakeVariant(toggle),
			dbus.MakeVariant(settings),
			dbus.MakeVariant(about),
			dbus.MakeVariant(quit),
		},
	}
}

// GetLayout 实现 dbusmenu 的 GetLayout(iias) → (u(ia{sv}av))。
func (t *Tray) GetLayout(parentID, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	// 菜单是扁平两级结构：任何 parentID 都返回整棵树（调用方只取所需子树）。
	return 1, layoutTree(propertyNames), nil
}

// GetGroupProperties 实现 dbusmenu 的 GetGroupProperties(aias) → a(ia{sv})。
func (t *Tray) GetGroupProperties(ids []int32, propertyNames []string) ([]groupProps, *dbus.Error) {
	all := map[int32]map[string]dbus.Variant{
		0: rootMenuProps(),
		1: menuItemProps("显示/隐藏"),
		2: menuItemProps("设置"),
		3: menuItemProps("关于"),
		4: menuItemProps("退出"),
	}
	out := make([]groupProps, 0, len(ids))
	for _, id := range ids {
		props, ok := all[id]
		if !ok {
			return nil, dbus.NewError(menuIface+".InvalidId", []any{fmt.Sprintf("未知菜单项 %d", id)})
		}
		out = append(out, groupProps{Id: id, Properties: filterProps(props, propertyNames)})
	}
	return out, nil
}

// GetMenuProperty 实现 dbusmenu 的 GetProperty(is) → v。
func (t *Tray) GetMenuProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	groups, err := t.GetGroupProperties([]int32{id}, nil)
	if err != nil {
		return dbus.Variant{}, err
	}
	v, ok := groups[0].Properties[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs",
			[]any{fmt.Sprintf("菜单项 %d 无属性 %s", id, name)})
	}
	return v, nil
}

// Event 实现 dbusmenu 的 Event(isuv)：处理菜单项点击。
func (t *Tray) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	switch id {
	case 1:
		if t.onToggle != nil {
			t.onToggle()
		}
	case 2:
		if t.onSettings != nil {
			t.onSettings()
		}
	case 3:
		if t.onAbout != nil {
			t.onAbout()
		}
	case 4:
		if t.onQuit != nil {
			t.onQuit()
		}
	}
	return nil
}

// EventGroup 实现 dbusmenu 的 EventGroup(a(isuv)) → ai：逐个分发。
func (t *Tray) EventGroup(events []struct {
	Id        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}) ([]int32, *dbus.Error) {
	for _, e := range events {
		if err := t.Event(e.Id, e.EventID, e.Data, e.Timestamp); err != nil {
			return nil, err
		}
	}
	return []int32{}, nil
}

// AboutToShow 实现 dbusmenu 的 AboutToShow(i) → b：菜单静态，无需刷新。
func (t *Tray) AboutToShow(id int32) (bool, *dbus.Error) {
	return false, nil
}

// AboutToShowGroup 实现 dbusmenu 的 AboutToShowGroup(ai) → (ai, a(iis))。
func (t *Tray) AboutToShowGroup(ids []int32) ([]int32, []struct {
	Id    int32
	Error int32
	Event string
}, *dbus.Error) {
	return []int32{}, nil, nil
}

// GetSerializedLayout 实现 dbusmenu 扩展方法（返回与 GetLayout 相同的数据）。
func (t *Tray) GetSerializedLayout(parentID, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	return t.GetLayout(parentID, recursionDepth, propertyNames)
}

// pngsToPixmaps 把内嵌 PNG 逐张解码并转成 SNI IconPixmap 所需的
// ARGB32 网络字节序数据。
func pngsToPixmaps(fsys embed.FS, names []string) ([]pixmap, error) {
	out := make([]pixmap, 0, len(names))
	for _, name := range names {
		data, err := fsys.ReadFile(name)
		if err != nil {
			return nil, err
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		buf := make([]byte, 0, w*h*4)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				// NRGBAModel 统一转成非预乘 8bit，逐像素按 A,R,G,B 大端打包。
				c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				buf = append(buf, c.A, c.R, c.G, c.B)
			}
		}
		out = append(out, pixmap{Width: int32(w), Height: int32(h), Bytes: buf})
	}
	return out, nil
}
