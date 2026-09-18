package search

import (
	"cmp"
	"container/heap"
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"bao/internal/fuzzy"
)

// Entry 是文件索引中的一条记录：Name/Path 对外展示，lower/bound/chars 为构建期
// 预计算字段，查询期只读，使每次击键的过滤成为零分配、无大小写转换的纯扫描。
type Entry struct {
	Name  string // 原始基名
	Path  string // 完整路径
	lower string // 小写基名
	bound uint64 // 词首边界位图（fuzzy.BoundaryBitmap）
	chars uint64 // 字符存在位图（fuzzy.CharMask(lower)）
}

// Cache 是 $HOME 可见条目的全量内存索引：窗口唤起时后台构建，排除配置变更或
// 缓存超时后由调用方触发重建。全量索引使任意查询（含删改中间字符等非单调编辑）
// 都能在内存中直接过滤，不再受遍历顺序影响。
type Cache struct {
	entries  []Entry
	excluded []string // 构建时的排除配置快照（AllExcluded 的副本）
	builtAt  time.Time
}

// NewCache 由构建好的条目与排除配置快照组装 Cache。
func NewCache(entries []Entry, excluded []string) *Cache {
	return &Cache{entries: entries, excluded: excluded, builtAt: time.Now()}
}

// BuiltAt 返回构建完成时刻；Age 返回距今时长，供超时判定。
func (c *Cache) BuiltAt() time.Time { return c.builtAt }
func (c *Cache) Age() time.Duration { return time.Since(c.builtAt) }

// MatchesConfig 报告缓存的排除快照与当前配置（AllExcluded 列表）是否一致；
// 两侧均为「默认在前、新增在后」的确定性顺序，逐项比较即可。
func (c *Cache) MatchesConfig(excluded []string) bool {
	if len(c.excluded) != len(excluded) {
		return false
	}
	for i := range c.excluded {
		if c.excluded[i] != excluded[i] {
			return false
		}
	}
	return true
}

// Build 以与 Files 相同的规则遍历 root（root 自身不参与；跳过隐藏项与 exclude
// 命中的目录；文件与目录都收录），并为每个条目预计算小写名、边界位图与字符位图。
// 遍历任一错误（如无权限）跳过不中断；ctx 取消时返回已收集部分。
func Build(ctx context.Context, root string, exclude func(name, fullPath string) bool) []Entry {
	var entries []Entry
	isRoot := true
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if isRoot {
			isRoot = false
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
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
		lower := strings.ToLower(name)
		entries = append(entries, Entry{
			Name:  name,
			Path:  path,
			lower: lower,
			bound: fuzzy.BoundaryBitmap(name),
			chars: fuzzy.CharMask(lower),
		})
		return nil
	})
	return entries
}

// Filter 在索引上做预筛（长度 + 字符存在位图）与模糊匹配，命中项按
// Score/Name/Path 排序后截取至多 limit 个。与 Files 的「遍历序前 limit 个」不同，
// 这里是全局最优的前 limit 个：命中数超过 limit 后用容量为 limit 的小顶堆保留
// 最优者，避免对全部命中做 O(M log M) 排序，总代价 O(M log limit)。
// query 为空或 limit <= 0 时返回 nil。
func (c *Cache) Filter(query string, limit int) []Result {
	if limit <= 0 || query == "" {
		return nil
	}
	m := fuzzy.NewMatcher(query)
	qlen := m.RuneLen()
	qmask := m.Mask()
	h := make(resultHeap, 0, limit)
	for i := range c.entries {
		e := &c.entries[i]
		if len(e.lower) < qlen || qmask&^e.chars != 0 {
			continue
		}
		if score, ok := m.MatchLower(e.lower, e.bound); ok {
			r := Result{Path: e.Path, Name: e.Name, Score: score}
			switch {
			case len(h) < limit:
				heap.Push(&h, r)
			case compareResult(r, h[0]) < 0: // 优于当前堆顶（最差保留者）才替换
				h[0] = r
				heap.Fix(&h, 0)
			}
		}
	}
	results := []Result(h)
	slices.SortFunc(results, compareResult)
	return results
}

// sortResults 按得分降序、名字升序、路径升序排序（与 Files 的排序规则一致）。
func sortResults(results []Result) {
	slices.SortFunc(results, compareResult)
}

// compareResult 实现结果排序规则：得分降序、名字升序、路径升序。
// 路径唯一，该比较构成全序，因此堆选择的 top-limit 集合与全量排序后截断一致。
func compareResult(a, b Result) int {
	if c := cmp.Compare(b.Score, a.Score); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	return cmp.Compare(a.Path, b.Path)
}

// resultHeap 是容量上限为 limit 的小顶堆：堆顶为堆内按 compareResult 最差的一条，
// 新命中优于堆顶时替换之，使 Filter 只需保留而非排序全部命中。
type resultHeap []Result

func (h resultHeap) Len() int           { return len(h) }
func (h resultHeap) Less(i, j int) bool { return compareResult(h[i], h[j]) > 0 }
func (h resultHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *resultHeap) Push(x any)        { *h = append(*h, x.(Result)) }
func (h *resultHeap) Pop() any {
	old := *h
	n := len(old)
	r := old[n-1]
	*h = old[:n-1]
	return r
}
