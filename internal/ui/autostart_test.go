package ui

import (
	"strings"
	"testing"
)

func TestAutostartEnabled(t *testing.T) {
	cases := []struct {
		name                              string
		userExists, userHidden, sysExists bool
		want                              bool
	}{
		{"用户文件无Hidden → 开", true, false, false, true},
		{"用户文件Hidden=true → 关", true, true, false, false},
		{"用户文件Hidden=true 优先于系统文件", true, true, true, false},
		{"无用户文件有系统文件 → 开（系统默认）", false, false, true, true},
		{"两者都无 → 关", false, false, false, false},
	}
	for _, c := range cases {
		if got := autostartEnabled(c.userExists, c.userHidden, c.sysExists); got != c.want {
			t.Errorf("%s: autostartEnabled(%v,%v,%v) = %v，期望 %v",
				c.name, c.userExists, c.userHidden, c.sysExists, got, c.want)
		}
	}
}

func TestParseDesktopHidden(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"[Desktop Entry]\nExec=bao --hidden\n", false},
		{"[Desktop Entry]\nHidden=true\nExec=bao\n", true},
		{"[Desktop Entry]\nhidden = True\n", true},  // 大小写与空白不敏感
		{"[Desktop Entry]\nHidden=false\n", false},  // false 不屏蔽
		{"[Desktop Entry]\nX-Hidden=true\n", false}, // 其他键不误判
		{"#Hidden=true\n", false},                   // 注释不误判
		{"", false},
	}
	for _, c := range cases {
		if got := parseDesktopHidden(c.content); got != c.want {
			t.Errorf("parseDesktopHidden(%q) = %v，期望 %v", c.content, got, c.want)
		}
	}
}

func TestDesktopEntrySetHidden(t *testing.T) {
	entry := "[Desktop Entry]\nName=bao\nExec=bao --hidden\n"

	// 写入 Hidden=true：插在 [Desktop Entry] 之后
	got := desktopEntrySetHidden(entry, true)
	if !parseDesktopHidden(got) {
		t.Errorf("写入后未检出 Hidden=true:\n%s", got)
	}
	if !strings.Contains(got, "Exec=bao --hidden") {
		t.Errorf("原有内容丢失:\n%s", got)
	}
	if idx := strings.Index(got, "Hidden=true"); idx < 0 ||
		idx > strings.Index(got, "Name=bao") {
		t.Errorf("Hidden=true 应插入 [Desktop Entry] 之后:\n%s", got)
	}

	// 移除 Hidden=true（幂等往返）
	roundTrip := desktopEntrySetHidden(got, false)
	if parseDesktopHidden(roundTrip) {
		t.Errorf("移除后仍检出 Hidden=true:\n%s", roundTrip)
	}
	if strings.Contains(roundTrip, "Hidden") {
		t.Errorf("移除后仍残留 Hidden 行:\n%s", roundTrip)
	}

	// 重复写入不叠加
	twice := desktopEntrySetHidden(got, true)
	if strings.Count(twice, "Hidden=true") != 1 {
		t.Errorf("重复写入叠加了 Hidden 行:\n%s", twice)
	}

	// 无 [Desktop Entry] 组时也能写入
	noGroup := desktopEntrySetHidden("Exec=bao\n", true)
	if !parseDesktopHidden(noGroup) {
		t.Errorf("无组内容写入失败:\n%s", noGroup)
	}
}

func TestDesktopEntrySetHiddenPreservesOtherGroups(t *testing.T) {
	content := "[Desktop Entry]\nName=bao\n\n[Desktop Action new]\nName=新建\n"
	got := desktopEntrySetHidden(content, true)
	if !strings.Contains(got, "[Desktop Action new]") || !strings.Contains(got, "Name=新建") {
		t.Errorf("其他组内容被损坏:\n%s", got)
	}
}
