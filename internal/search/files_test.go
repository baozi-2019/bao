package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// buildTree 在 t.TempDir() 下构造固定的目录结构用于搜索测试。
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{
		"report.txt",
		"notes.txt",
		"photos/beach.jpg",
		"photos/mountain.jpg",
		"work/reports/annual.txt",
		"node_modules/pkg/index.js",
		".hidden/secret.txt",
		".secret.txt",
	} {
		full := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFilesMatchAndSort(t *testing.T) {
	root := buildTree(t)
	got := Files(context.Background(), root, "report", excludeNothing, 10)
	if len(got) != 2 {
		t.Fatalf("应命中 2 项，得到 %d：%v", len(got), got)
	}
	// 两个命中：report.txt（词首命中）与 work/reports/annual.txt 之上的 reports 目录。
	// reports 作为目录名命中，得分应高于子串命中 report.txt 内的片段。
	if got[0].Score < got[1].Score {
		t.Fatalf("结果应按 Score 降序：%v", got)
	}
	for _, r := range got {
		if !filepath.IsAbs(r.Path) || filepath.Base(r.Path) != r.Name {
			t.Fatalf("Path/Name 不一致：%+v", r)
		}
	}
}

func TestFilesLimit(t *testing.T) {
	root := buildTree(t)
	got := Files(context.Background(), root, "jpg", excludeNothing, 1)
	if len(got) != 1 {
		t.Fatalf("limit=1 应只返回 1 项，得到 %d", len(got))
	}
	if got[0].Name != "beach.jpg" && got[0].Name != "mountain.jpg" {
		t.Fatalf("命中内容错误：%v", got)
	}
	if got := Files(context.Background(), root, "jpg", excludeNothing, 0); len(got) != 0 {
		t.Fatalf("limit=0 应返回空，得到 %v", got)
	}
}

func TestFilesSkipsHiddenAndExcluded(t *testing.T) {
	root := buildTree(t)
	got := Files(context.Background(), root, "secret", excludeNothing, 10)
	if len(got) != 0 {
		t.Fatalf("隐藏项不应被命中：%v", got)
	}
	got = Files(context.Background(), root, "index", excludeNothing, 10)
	if len(got) != 1 {
		t.Fatalf("不排除 node_modules 时应命中 index.js，得到 %v", got)
	}
	got = Files(context.Background(), root, "index", excludeNodeModules, 10)
	if len(got) != 0 {
		t.Fatalf("exclude 为 true 的目录应被跳过：%v", got)
	}
}

func TestFilesCancelledContext(t *testing.T) {
	root := buildTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Files(ctx, root, "jpg", excludeNothing, 10); len(got) != 0 {
		t.Fatalf("已取消的 ctx 应返回空结果，得到 %v", got)
	}
}

func TestFilesWalkErrorSkipped(t *testing.T) {
	root := buildTree(t)
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0o755) // 便于 t.TempDir() 清理
	if _, err := os.Stat(filepath.Join(blocked, "inner.txt")); err == nil {
		t.Skip("当前环境可穿透 0o000 目录，无法模拟无权限")
	}
	if err := os.WriteFile(filepath.Join(blocked, "inner.txt"), []byte("x"), 0o644); err == nil {
		t.Skip("当前环境可写入 0o000 目录，无法模拟无权限")
	}
	got := Files(context.Background(), root, "txt", excludeNothing, 10)
	if len(got) == 0 {
		t.Fatal("无权限目录被跳过，其余结果仍应收集")
	}
}

func excludeNothing(_, _ string) bool { return false }

func excludeNodeModules(name, _ string) bool { return name == "node_modules" }
