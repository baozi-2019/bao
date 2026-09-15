package ui

import (
	"reflect"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
)

func TestAccelFromKey(t *testing.T) {
	cases := []struct {
		name   string
		keyval uint
		state  gdk.ModifierType
		want   string
		wantOK bool
	}{
		{"Super+空格", gdk.KEY_space, gdk.SuperMask, "<Super>space", true},
		{"Ctrl+Alt+t", gdk.KEY_t, gdk.ControlMask | gdk.AltMask, "<Control><Alt>t", true},
		{"Shift+F4", gdk.KEY_F4, gdk.ShiftMask, "<Shift>F4", true},
		{"纯字母", gdk.KEY_a, gdk.NoModifierMask, "a", true},
		{"Shift+字母键值大写", gdk.KEY_T, gdk.ShiftMask, "<Shift>t", true},
		{"四修饰键全按", gdk.KEY_x, gdk.ControlMask | gdk.AltMask | gdk.ShiftMask | gdk.SuperMask, "<Control><Alt><Shift><Super>x", true},
		{"Escape 可绑定", gdk.KEY_Escape, gdk.NoModifierMask, "Escape", true},
		{"单按左Ctrl拒绝", gdk.KEY_Control_L, gdk.ControlMask, "", false},
		{"单按右Shift拒绝", gdk.KEY_Shift_R, gdk.ShiftMask, "", false},
		{"单按Super拒绝", gdk.KEY_Super_L, gdk.SuperMask, "", false},
		{"单按CapsLock拒绝", gdk.KEY_Caps_Lock, gdk.LockMask, "", false},
	}
	for _, c := range cases {
		got, ok := accelFromKey(c.keyval, c.state)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("%s: accelFromKey(%d, %v) = %q,%v，期望 %q,%v",
				c.name, c.keyval, c.state, got, ok, c.want, c.wantOK)
		}
	}
}

func TestParseVariantStringArray(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"['/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/bao/']\n",
			[]string{"/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/bao/"}},
		{"['/a/', '/b/']", []string{"/a/", "/b/"}},
		{"@as []\n", []string{}},
		{"[]", []string{}},
		{"[1, 2]", nil}, // 非字符串数组：解析失败
		{"垃圾输出", nil},
	}
	for _, c := range cases {
		got := parseVariantStringArray(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseVariantStringArray(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

func TestUnquoteGVariant(t *testing.T) {
	cases := map[string]string{
		"'<Super>space'": "<Super>space",
		"''":             "",
		"'a\\'b'":        "a'b",
		"'a\\\\b'":       "a\\b",
	}
	for in, want := range cases {
		if got := unquoteGVariant(in); got != want {
			t.Errorf("unquoteGVariant(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestFormatVariantStringArray(t *testing.T) {
	got := formatVariantStringArray([]string{"/a/", "/gnome/x/"})
	want := "['/a/', '/gnome/x/']"
	if got != want {
		t.Errorf("formatVariantStringArray = %q，期望 %q", got, want)
	}
	// 往返一致性
	parsed := parseVariantStringArray(got)
	if !reflect.DeepEqual(parsed, []string{"/a/", "/gnome/x/"}) {
		t.Errorf("往返解析失败: %v", parsed)
	}
}
