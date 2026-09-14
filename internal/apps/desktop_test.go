package apps

import (
	"os"
	"path/filepath"
	"testing"
)

// xdgEnv 在临时目录构造 XDG 环境变量，返回 user/sys1 两个 applications 目录。
func xdgEnv(t *testing.T) (user, sys string) {
	t.Helper()
	root := t.TempDir()
	user = filepath.Join(root, "user", "applications")
	sys = filepath.Join(root, "sys1", "applications")
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "user"))
	t.Setenv("XDG_DATA_DIRS", filepath.Join(root, "sys1")+":"+filepath.Join(root, "sys2"))
	t.Setenv("LC_ALL", "zh_CN.UTF-8")
	return user, sys
}

func writeDesktop(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanSkipsHiddenAndLocalizedName(t *testing.T) {
	user, _ := xdgEnv(t)
	writeDesktop(t, filepath.Join(user, "foo.desktop"), `
[Desktop Entry]
Type=Application
Name=Foo Editor
Name[zh_CN]= foo 编辑器
Comment=Edit things
Exec=foo-editor --new
Icon=foo
`)
	writeDesktop(t, filepath.Join(user, "hidden.desktop"), `
[Desktop Entry]
Name=Hidden App
Hidden=true
`)
	writeDesktop(t, filepath.Join(user, "nodisplay.desktop"), `
[Desktop Entry]
Name=Secret Tool
NoDisplay=true
`)
	writeDesktop(t, filepath.Join(user, "link.desktop"), `
[Desktop Entry]
Name=Some Link
Type=Link
URL=https://example.com
`)

	apps := Scan()
	if len(apps) != 2 {
		t.Fatalf("期望 2 个应用，得到 %d: %+v", len(apps), apps)
	}
	var foo, secret *App
	for i := range apps {
		switch apps[i].ID {
		case filepath.Join(user, "foo.desktop"):
			foo = &apps[i]
		case filepath.Join(user, "nodisplay.desktop"):
			secret = &apps[i]
		}
	}
	if foo == nil {
		t.Fatal("未扫描到 foo.desktop")
	}
	if foo.Name != "foo 编辑器" {
		t.Fatalf("本地化 Name 未生效: %q", foo.Name)
	}
	if foo.Exec != "foo-editor --new" || foo.Icon != "foo" || foo.Comment != "Edit things" {
		t.Fatalf("字段解析错误: %+v", *foo)
	}
	if secret == nil || !secret.NoDisplay {
		t.Fatalf("NoDisplay 应用解析错误: %+v", apps)
	}
}

func TestScanUserDirShadowsSystem(t *testing.T) {
	user, sys := xdgEnv(t)
	writeDesktop(t, filepath.Join(user, "dup.desktop"), `
[Desktop Entry]
Name=User Version
`)
	writeDesktop(t, filepath.Join(sys, "dup.desktop"), `
[Desktop Entry]
Name=System Version
`)
	writeDesktop(t, filepath.Join(sys, "sub", "only.desktop"), `
[Desktop Entry]
Name=Only System
`)

	apps := Scan()
	names := map[string]string{}
	for _, a := range apps {
		names[filepath.Base(a.ID)] = a.Name
	}
	if names["dup.desktop"] != "User Version" {
		t.Fatalf("用户目录未覆盖系统目录: %+v", names)
	}
	if names["only.desktop"] != "Only System" {
		t.Fatalf("子目录中的系统应用缺失: %+v", names)
	}
	if len(apps) != 2 {
		t.Fatalf("同名系统应用应被去重: %+v", apps)
	}
}

func TestSearchFallbackAndLimit(t *testing.T) {
	list := []App{
		{Name: "Terminal", Comment: "命令行终端"},
		{Name: "Text Editor", Comment: "编辑文本"},
		{Name: "secret-helper", NoDisplay: true},
	}
	got := Search(list, "te", 10)
	if len(got) == 0 {
		t.Fatal("普通匹配失败")
	}
	for _, a := range got {
		if a.NoDisplay {
			t.Fatalf("有普通结果时不应返回 NoDisplay: %+v", got)
		}
	}
	got = Search(list, "secret", 10)
	if len(got) != 1 || got[0].Name != "secret-helper" {
		t.Fatalf("NoDisplay 兜底失败: %+v", got)
	}
	if got := Search(list, "", 1); len(got) != 1 {
		t.Fatalf("limit 未生效: %+v", got)
	}
	if got := Search(list, "zzz", 10); len(got) != 0 {
		t.Fatalf("无匹配时应为空: %+v", got)
	}
	if got := Search(list, "te", 0); got != nil {
		t.Fatalf("limit<=0 应返回 nil: %+v", got)
	}
}
