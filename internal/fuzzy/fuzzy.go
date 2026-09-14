// Package fuzzy 提供大小写不敏感的子序列模糊匹配与打分。
package fuzzy

import "unicode"

// Match 报告 query 是否为 target 的子序列（大小写不敏感），并给出打分，越高越优。
// 空 query 视为匹配，得分为 0。
func Match(query, target string) (score int, ok bool) {
	if query == "" {
		return 0, true
	}

	q := []rune(query)
	t := []rune(target)

	qi := 0
	last := -2 // 上一个命中位置（-2 表示尚未命中）
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		if unicode.ToLower(q[qi]) != unicode.ToLower(t[ti]) {
			continue
		}
		score += 3
		switch {
		case ti == last+1:
			score += 5 // 连续命中
		case last >= -1:
			if gap := ti - last - 1; gap > 0 {
				score -= min(gap, 8) // 跳跃惩罚，封顶避免长名被过度惩罚
			}
		}
		if ti == 0 || isBoundary(t, ti) {
			score += 7 // 词首命中
		}
		last = ti
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	return score, true
}

// isBoundary 报告 target 位置 i 是否处于词首（前一个字符为分隔符，或驼峰大写转换处）。
func isBoundary(t []rune, i int) bool {
	prev := t[i-1]
	cur := t[i]
	if prev == '/' || prev == '-' || prev == '_' || prev == ' ' || prev == '.' {
		return true
	}
	return unicode.IsLower(prev) && unicode.IsUpper(cur)
}
