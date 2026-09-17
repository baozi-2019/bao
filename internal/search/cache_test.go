package search

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newTestTree 建一棵临时目录树，返回根路径。
// 结构：可见/隐藏/被排除目录三种情况都覆盖。
func newTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"visible.txt",
		"fire-note.md",
		".hidden.txt",
		".hiddendir/x.txt",
		"proj/src/main.go",
		"proj/src/util.go",
		"proj/target/artifact.bin",
		"proj/__pycache__/m.pyc",
	}
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func testExclude(name, _ string) bool {
	return name == "target" || name == "__pycache__"
}

func TestBuildSkipsHiddenAndExcluded(t *testing.T) {
	root := newTestTree(t)
	entries := Build(context.Background(), root, testExclude)
	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Name == "" || e.Path == "" || e.lower == "" {
			t.Fatalf("条目字段不完整: %+v", e)
		}
		got[e.Name] = true
	}
	for _, want := range []string{"visible.txt", "fire-note.md", "proj", "src", "main.go", "util.go"} {
		if !got[want] {
			t.Errorf("索引缺少 %q", want)
		}
	}
	for _, unwanted := range []string{".hidden.txt", "x.txt", "artifact.bin", "m.pyc"} {
		if got[unwanted] {
			t.Errorf("索引不应包含 %q", unwanted)
		}
	}
}

func TestFilterParityWithFiles(t *testing.T) {
	root := newTestTree(t)
	entries := Build(context.Background(), root, testExclude)
	c := NewCache(entries, []string{"target", "__pycache__"})
	for _, q := range []string{"f", "fire", "main", "util", "note", "go", "proj"} {
		want := Files(context.Background(), root, q, testExclude, 100)
		got := c.Filter(q, 100)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Filter(%q) 与 Files 不一致:\n got %+v\nwant %+v", q, got, want)
		}
	}
}

func TestFilterLimitAndEmpty(t *testing.T) {
	root := newTestTree(t)
	c := NewCache(Build(context.Background(), root, testExclude), nil)
	if got := c.Filter("", 10); got != nil {
		t.Errorf("空 query 应返回 nil，got %v", got)
	}
	if got := c.Filter("e", 0); got != nil {
		t.Errorf("limit<=0 应返回 nil，got %v", got)
	}
	got := c.Filter("e", 1)
	if len(got) != 1 {
		t.Fatalf("limit=1 应只返回 1 条，got %v", got)
	}
}

func TestCacheMatchesConfig(t *testing.T) {
	c := NewCache(nil, []string{"a", "b"})
	if !c.MatchesConfig([]string{"a", "b"}) {
		t.Error("相同快照应匹配")
	}
	if c.MatchesConfig([]string{"b", "a"}) || c.MatchesConfig([]string{"a"}) || c.MatchesConfig([]string{"a", "b", "c"}) {
		t.Error("不同快照不应匹配")
	}
	if c.Age() < 0 {
		t.Error("Age 不应为负")
	}
	if c.BuiltAt().IsZero() {
		t.Error("BuiltAt 应已设置")
	}
}
