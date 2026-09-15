package ui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
)

// 本文件实现「全局快捷键」设置：通过 gsettings 把启动 bao 的快捷键
// 注册到 GNOME 的自定义快捷键（org.gnome.settings-daemon.plugins.media-keys
// 的 custom-keybindings），逻辑移植自 packaging/assets/bao-keybind。
// 非 GNOME 环境（无 gsettings 或 schema 缺失）由 KeybindSupported 判定后禁用界面。

const (
	// keybindSchema 是 GNOME 媒体快捷键 schema。
	keybindSchema = "org.gnome.settings-daemon.plugins.media-keys"
	// keybindBase 是 bao 在 custom-keybindings 里的注册路径。
	keybindBase = "/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/bao/"
	// keybindDefault 是默认绑定（对应「恢复默认」按钮）。
	keybindDefault = "<Super>space"
)

// accelFromKey 把 GDK 按键事件转换成 gsettings accelerator 字符串
// （如 "<Super>space"、 "<Control><Alt>t"、 "<Shift>F4"）。
// 修饰键顺序固定为 Control/Alt/Shift/Super；字母键名转小写
// （GTK accelerator 惯例：大小写字母本身隐含 Shift）。
// 纯修饰键（只按了 Shift/Ctrl/Alt/Super 等）返回 ok=false，调用方应忽略。
func accelFromKey(keyval uint, state gdk.ModifierType) (accel string, ok bool) {
	// 纯修饰键与锁定键不参与绑定。
	switch keyval {
	case gdk.KEY_Shift_L, gdk.KEY_Shift_R,
		gdk.KEY_Control_L, gdk.KEY_Control_R,
		gdk.KEY_Alt_L, gdk.KEY_Alt_R,
		gdk.KEY_Super_L, gdk.KEY_Super_R,
		gdk.KEY_Meta_L, gdk.KEY_Meta_R,
		gdk.KEY_Hyper_L, gdk.KEY_Hyper_R,
		gdk.KEY_ISO_Level3_Shift,
		gdk.KEY_Caps_Lock, gdk.KEY_Num_Lock, gdk.KEY_Scroll_Lock:
		return "", false
	}

	name := gdk.KeyvalName(keyval)
	if name == "" {
		return "", false
	}
	// 单字符键名（字母/数字/符号）统一小写，与 GTK accelerator 格式一致。
	if len(name) == 1 {
		name = strings.ToLower(name)
	}

	var mods strings.Builder
	if state&gdk.ControlMask != 0 {
		mods.WriteString("<Control>")
	}
	if state&gdk.AltMask != 0 {
		mods.WriteString("<Alt>")
	}
	if state&gdk.ShiftMask != 0 {
		mods.WriteString("<Shift>")
	}
	if state&gdk.SuperMask != 0 {
		mods.WriteString("<Super>")
	}
	if state&gdk.MetaMask != 0 {
		mods.WriteString("<Meta>")
	}
	if state&gdk.HyperMask != 0 {
		mods.WriteString("<Hyper>")
	}
	return mods.String() + name, true
}

// parseVariantStringArray 解析 gsettings 输出的 GVariant 字符串数组
// （如 "['/a/', '/b/']"，空数组可能输出 "@as []"），返回去引号后的元素。
// 解析失败时返回 nil（调用方按不支持处理）。
func parseVariantStringArray(out string) []string {
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "@as") { // 空数组的 GVariant 文本表示
		out = strings.TrimSpace(strings.TrimPrefix(out, "@as"))
	}
	if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
		return nil
	}
	inner := strings.TrimSpace(out[1 : len(out)-1])
	if inner == "" {
		return []string{}
	}
	parts := strings.Split(inner, ",")
	items := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// 元素必须是单引号字符串，否则整体视为非字符串数组（解析失败）。
		if len(p) < 2 || p[0] != '\'' || p[len(p)-1] != '\'' {
			return nil
		}
		items = append(items, unquoteGVariant(p))
	}
	return items
}

// unquoteGVariant 去掉 GVariant 字符串文本的单引号并反转义（\' 与 \\）。
func unquoteGVariant(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		s = s[1 : len(s)-1]
	}
	s = strings.ReplaceAll(s, `\'`, `'`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

// formatVariantStringArray 把路径列表格式化为 gsettings set 可接受的
// GVariant 字符串数组文本。
func formatVariantStringArray(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "'" + strings.ReplaceAll(strings.ReplaceAll(it, `\`, `\\`), `'`, `\'`) + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// gsettings 执行一条 gsettings 命令并返回合并输出。
func gsettings(args ...string) (string, error) {
	out, err := exec.Command("gsettings", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("gsettings %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// keybindSchemaPath 返回带 relocatable 路径的 schema id，
// 用于读写 bao 这一条 custom-keybinding 的 name/command/binding。
func keybindSchemaPath() string {
	return keybindSchema + ".custom-keybinding:" + keybindBase
}

// KeybindSupported 报告当前环境是否支持自动绑定：
// gsettings 存在且 media-keys schema 可读取（GNOME）。
func KeybindSupported() bool {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return false
	}
	_, err := gsettings("get", keybindSchema, "custom-keybindings")
	return err == nil
}

// keybindRegistered 报告 bao 是否已注册进 custom-keybindings 列表。
func keybindRegistered() bool {
	out, err := gsettings("get", keybindSchema, "custom-keybindings")
	if err != nil {
		return false
	}
	for _, item := range parseVariantStringArray(out) {
		if item == keybindBase {
			return true
		}
	}
	return false
}

// KeybindGet 返回当前绑定的 accelerator 字符串；未绑定或读取失败返回 ""。
func KeybindGet() string {
	if !keybindRegistered() {
		return ""
	}
	out, err := gsettings("get", keybindSchemaPath(), "binding")
	if err != nil {
		return ""
	}
	return unquoteGVariant(strings.TrimSpace(out))
}

// KeybindSet 把 accel 绑定为启动 bao 的全局快捷键：
// 先幂等注册 bao 到 custom-keybindings 列表，再写 name/command/binding。
func KeybindSet(accel string) error {
	if !keybindRegistered() {
		out, err := gsettings("get", keybindSchema, "custom-keybindings")
		if err != nil {
			return err
		}
		list := parseVariantStringArray(out)
		if list == nil {
			return fmt.Errorf("解析 custom-keybindings 失败: %s", strings.TrimSpace(out))
		}
		list = append(list, keybindBase)
		if _, err := gsettings("set", keybindSchema, "custom-keybindings", formatVariantStringArray(list)); err != nil {
			return err
		}
	}
	if _, err := gsettings("set", keybindSchemaPath(), "name", "'bao 启动器'"); err != nil {
		return err
	}
	if _, err := gsettings("set", keybindSchemaPath(), "command", "'bao'"); err != nil {
		return err
	}
	_, err := gsettings("set", keybindSchemaPath(), "binding", "'"+strings.ReplaceAll(accel, `'`, `\'`)+"'")
	return err
}

// KeybindResetDefault 恢复默认绑定 Super+空格。
func KeybindResetDefault() error {
	return KeybindSet(keybindDefault)
}

// KeybindClear 清除绑定（binding 置空，注册项保留；GNOME 中空绑定即禁用）。
func KeybindClear() error {
	if !keybindRegistered() {
		return nil
	}
	_, err := gsettings("set", keybindSchemaPath(), "binding", "''")
	return err
}
