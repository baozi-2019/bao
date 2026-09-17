// Package search 提供基于模糊匹配的文件系统遍历搜索。
package search

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"bao/internal/fuzzy"
)

// Result 表示一次文件搜索的命中项。
type Result struct {
	Path  string // 完整路径
	Name  string // 基名
	Score int    // 模糊匹配得分，越高越优
}

// Files 从 root 遍历，对文件与目录名做模糊匹配；跳过隐藏项（以 . 开头）与
// exclude 为 true 的目录；收集至多 limit 个按 Score 降序的结果。
// ctx 取消时尽快返回已收集结果；遍历任一错误（如无权限）跳过不中断。
// root 本身只作为遍历起点，不参与匹配与过滤；limit <= 0 时返回空结果。
func Files(ctx context.Context, root, query string, exclude func(name, fullPath string) bool, limit int) []Result {
	if limit <= 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	results := make([]Result, 0, limit)
	isRoot := true

	// WalkDir 返回的错误（含 ctx 取消触发的中止）一律忽略，返回已收集结果。
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限等错误：跳过该项，不中断遍历
		}
		if isRoot {
			isRoot = false
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && exclude(name, path) {
			return filepath.SkipDir
		}

		if score, ok := fuzzy.Match(query, name); ok {
			results = append(results, Result{Path: path, Name: name, Score: score})
			if len(results) >= limit {
				return filepath.SkipAll
			}
		}
		return nil
	})

	sortResults(results)
	return results
}
