package search

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bao/internal/fuzzy"
)

// xbelBookmark 只取 recently-used.xbel 中过滤所需的两项属性。
type xbelBookmark struct {
	Href     string
	Modified string // ISO 8601 UTC，字典序即时序
}

// Recent 解析 GTK 最近使用列表（$HOME/.local/share/recently-used.xbel），
// 返回基名命中 query 的条目，按修改时间新→旧排序、至多 limit 个。
// 读取/解析失败或无命中均返回 nil；路径存在性不校验（结果仅供置顶提示）。
func Recent(home, query string, limit int) []Result {
	if limit <= 0 || query == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".local", "share", "recently-used.xbel"))
	if err != nil {
		return nil
	}
	m := fuzzy.NewMatcher(query)
	qlen := m.RuneLen()
	type hit struct {
		Result
		stamp string
	}
	var hits []hit
	for _, b := range scanBookmarks(string(data)) {
		p, err := url.PathUnescape(strings.TrimPrefix(b.Href, "file://"))
		if err != nil || p == "" {
			continue
		}
		name := filepath.Base(p)
		lower := strings.ToLower(name)
		if len(lower) < qlen || m.Mask()&^fuzzy.CharMask(lower) != 0 {
			continue
		}
		if score, ok := m.MatchLower(lower, fuzzy.BoundaryBitmap(name)); ok {
			r := Result{Path: p, Name: name, Score: score}
			hits = append(hits, hit{Result: r, stamp: b.Modified})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	// ISO 8601 UTC 时间串字典序即时序，倒序即新→旧。
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].stamp > hits[j].stamp })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Result, len(hits))
	for i, h := range hits {
		out[i] = h.Result
	}
	return out
}

// scanBookmarks 轻量扫描 xbel 中的 <bookmark ...> 标签，抽出 href 与 modified
// 属性。手写扫描取代 xml.Unmarshal：通用解码器在该热路径上单次耗时数十毫秒
// 量级，扫描只取两个属性，亚毫秒完成。
// 局限：属性值内不能含与定界符相同的引号（XML 规范允许但文件名带引号极罕见）。
func scanBookmarks(data string) []xbelBookmark {
	var out []xbelBookmark
	rest := data
	for {
		i := strings.Index(rest, "<bookmark ")
		if i < 0 {
			break
		}
		tag := rest[i:]
		end := strings.IndexByte(tag, '>')
		if end < 0 {
			break
		}
		tag = tag[:end]
		if href, ok := attr(tag, "href"); ok && strings.HasPrefix(href, "file://") {
			modified, _ := attr(tag, "modified")
			out = append(out, xbelBookmark{Href: xmlUnescape(href), Modified: modified})
		}
		rest = rest[i+end:]
	}
	return out
}

// attr 抽取 tag 内 name="value" 或 name='value' 形式的属性值。
func attr(tag, name string) (string, bool) {
	i := strings.Index(tag, name+"=")
	if i < 0 {
		return "", false
	}
	v := tag[i+len(name)+1:]
	if len(v) == 0 || (v[0] != '"' && v[0] != '\'') {
		return "", false
	}
	j := strings.IndexByte(v[1:], v[0])
	if j < 0 {
		return "", false
	}
	return v[1 : 1+j], true
}

// xmlUnescape 还原属性值中的 5 个预定义实体（无 DTD 的 xbel 只有这些）。
// Replacer 单次扫描不二次展开，&amp;amp; 之类序列也安全。
func xmlUnescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&")
	return r.Replace(s)
}
