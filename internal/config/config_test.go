package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// useTempConfig 把 XDG_CONFIG_HOME 指到临时目录，避免测试读写真实用户配置。
func useTempConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func setOf(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, e := range list {
		m[e] = true
	}
	return m
}

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	useTempConfig(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg.ExcludedDirs, DefaultExcluded()) {
		t.Fatalf("缺省加载应等于内置默认，got %v", cfg.ExcludedDirs)
	}
}

func TestDefaultExcludedAllVisible(t *testing.T) {
	for _, d := range DefaultExcluded() {
		if d == "" || strings.HasPrefix(d, ".") || filepath.IsAbs(d) {
			t.Fatalf("默认排除项应为非空可见基名，got %q", d)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	useTempConfig(t)
	want := mergeExcluded(DefaultExcluded(), []string{"~/bao-test-add"})
	if err := (&Config{ExcludedDirs: want}).Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg.ExcludedDirs, want) {
		t.Fatalf("往返不一致，want %v got %v", want, cfg.ExcludedDirs)
	}
}

func TestRemovedDefaultPersists(t *testing.T) {
	useTempConfig(t)
	want := setDiff(DefaultExcluded(), []string{"target"})
	if err := (&Config{ExcludedDirs: want}).Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 落盘文件中应记录停用差量。
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var persisted struct {
		Added   []string `json:"excluded_dirs"`
		Removed []string `json:"removed_defaults"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("落盘文件不是合法 JSON: %v", err)
	}
	if !reflect.DeepEqual(persisted.Removed, []string{"target"}) || len(persisted.Added) != 0 {
		t.Fatalf("差量未按预期写入: %s", raw)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg.ExcludedDirs, want) {
		t.Fatalf("停用默认项后往返不一致，want %v got %v", want, cfg.ExcludedDirs)
	}
	if cfg.IsExcluded("target", "/any/where/target") {
		t.Fatal("target 已停用，IsExcluded 不应命中")
	}
}

func TestLegacyFullListTreatedAsAdditions(t *testing.T) {
	useTempConfig(t)
	legacy := `{"excluded_dirs": ["node_modules", ".git", "/tmp/bao-legacy"]}`
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := setOf(cfg.ExcludedDirs)
	for _, want := range []string{"node_modules", ".git", "/tmp/bao-legacy", "__pycache__", "dist"} {
		if !got[want] {
			t.Fatalf("有效列表缺少 %q，got %v", want, cfg.ExcludedDirs)
		}
	}
}

func TestLoadCorrupt(t *testing.T) {
	useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("损坏的 JSON 应返回错误")
	}
}

func TestSetDiff(t *testing.T) {
	got := setDiff([]string{"a/", "b", "a", "", "c"}, []string{"a", "x"})
	if !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("setDiff 应规范化去重并剔除 b 侧元素，got %v", got)
	}
}
