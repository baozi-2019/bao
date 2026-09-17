package search

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestHome 写一个 recently-used.xbel 固定件，返回临时主目录。
func newTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	xbel := `<?xml version="1.0" encoding="UTF-8"?>
<xbel version="1.0" xmlns:bookmark="http://www.freedesktop.org/standards/desktop-bookmarks">
  <bookmark href="file:///home/u/%E6%8A%A5%E5%91%8A%2024.pdf" added="2026-09-10T01:00:00Z" modified="2026-09-10T01:00:00Z" visited="2026-09-10T01:00:00Z"/>
  <bookmark href="file:///home/u/fire-note.md" added="2026-09-15T02:00:00Z" modified="2026-09-15T02:00:00Z" visited="2026-09-15T02:00:00Z"/>
  <bookmark href="file:///home/u/older.txt" added="2026-09-01T03:00:00Z" modified="2026-09-01T03:00:00Z" visited="2026-09-01T03:00:00Z"/>
  <bookmark href="file:///home/u/newer.txt" added="2026-09-14T03:00:00Z" modified="2026-09-14T03:00:00Z" visited="2026-09-14T03:00:00Z"/>
  <bookmark href="https://example.com/page" modified="2026-09-16T00:00:00Z"/>
</xbel>
`
	if err := os.WriteFile(filepath.Join(dir, "recently-used.xbel"), []byte(xbel), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestRecentMatchAndOrder(t *testing.T) {
	home := newTestHome(t)
	got := Recent(home, "txt", 10)
	if len(got) != 2 {
		t.Fatalf("应命中 2 个 .txt，got %v", got)
	}
	// modified 新→旧：newer(09-14) 先于 older(09-01)。
	if got[0].Name != "newer.txt" || got[1].Name != "older.txt" {
		t.Errorf("排序错误: %v", got)
	}
}

func TestRecentURLEscapeAndCJK(t *testing.T) {
	home := newTestHome(t)
	got := Recent(home, "报告", 10)
	if len(got) != 1 {
		t.Fatalf("CJK 查询应命中 1 条，got %v", got)
	}
	if got[0].Path != "/home/u/报告 24.pdf" {
		t.Errorf("URL 转义错误: %q", got[0].Path)
	}
}

func TestRecentLimitAndMiss(t *testing.T) {
	home := newTestHome(t)
	if got := Recent(home, "txt", 1); len(got) != 1 || got[0].Name != "newer.txt" {
		t.Errorf("limit=1 应只取最新一条: %v", got)
	}
	if got := Recent(home, "zzz", 10); got != nil {
		t.Errorf("无命中应返回 nil: %v", got)
	}
	if got := Recent(home, "", 10); got != nil {
		t.Errorf("空 query 应返回 nil: %v", got)
	}
	// http 链接不应出现在结果里（前面用例已隐式覆盖，这里显式断言）。
	if got := Recent(home, "page", 10); got != nil {
		t.Errorf("非 file:// 条目应被跳过: %v", got)
	}
}

func TestRecentBrokenFile(t *testing.T) {
	home := t.TempDir()
	if got := Recent(home, "x", 10); got != nil {
		t.Errorf("文件缺失应返回 nil: %v", got)
	}
	dir := filepath.Join(home, ".local", "share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recently-used.xbel"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Recent(home, "x", 10); got != nil {
		t.Errorf("解析失败应返回 nil: %v", got)
	}
}
