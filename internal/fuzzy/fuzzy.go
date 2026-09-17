// Package fuzzy 提供大小写不敏感的子序列模糊匹配与打分。
package fuzzy

import (
	"strings"
	"unicode"
)

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

// Matcher 持有查询的预计算形态（小写 rune 序列 + 字符存在位图 + 纯 ASCII 标记），
// 配合预先小写且带词首边界位图的目标（见 BoundaryBitmap/CharMask），匹配期
// 零分配、无大小写转换；查询含非 ASCII 时自动退化为 rune 级内循环。
type Matcher struct {
	q     []rune
	mask  uint64
	ascii bool
}

// NewMatcher 预计算查询形态；空查询匹配一切（得 0 分）。
func NewMatcher(query string) Matcher {
	m := Matcher{q: []rune(strings.ToLower(query)), ascii: true}
	for _, r := range m.q {
		m.mask |= 1 << (uint(r) & 63)
		if r > unicode.MaxASCII {
			m.ascii = false
		}
	}
	return m
}

// RuneLen 返回查询的 rune 数；目标字节数小于它时绝不可能匹配。
func (m Matcher) RuneLen() int { return len(m.q) }

// Mask 返回查询字符的存在位图，供调用方与目标的 CharMask 做 O(1) 预筛：
// q.Mask() &^ t.Mask != 0 即查询必有字符在目标中缺失，可直接拒绝（无假阴性）。
func (m Matcher) Mask() uint64 { return m.mask }

// MatchLower 在已小写的目标上执行匹配，bound 为目标的词首边界位图。
// 判定与打分语义和 Match 一致（连续命中、跳跃封顶惩罚、词首加分）。
func (m Matcher) MatchLower(lower string, bound uint64) (score int, ok bool) {
	if len(m.q) == 0 {
		return 0, true
	}
	if m.ascii {
		return m.matchASCII(lower, bound)
	}
	return m.matchRunes(lower)
}

// matchASCII 字节级内循环：查询纯 ASCII 时按字节比较与按 rune 比较等价
// （ASCII 字节不会作为 UTF-8 续字节出现）；连续/跳跃/词首都按字节位计。
func (m Matcher) matchASCII(lower string, bound uint64) (score int, ok bool) {
	qi, last := 0, -2
	for ti := 0; ti < len(lower) && qi < len(m.q); ti++ {
		if lower[ti] != byte(m.q[qi]) {
			continue
		}
		score += 3
		switch {
		case ti == last+1:
			score += 5
		case last >= -1:
			score -= min(ti-last-1, 8)
		}
		if ti == 0 || bound&(1<<uint(ti)) != 0 {
			score += 7
		}
		last = ti
		qi++
	}
	if qi < len(m.q) {
		return 0, false
	}
	return score, true
}

// matchRunes rune 级内循环：小写目标丢失了原大小写，驼峰边界不可得，
// 仅分隔符边界与首位给予词首加分（非 ASCII 查询罕见，此退化可接受）。
func (m Matcher) matchRunes(lower string) (score int, ok bool) {
	t := []rune(lower)
	qi, last := 0, -2
	for ti := 0; ti < len(t) && qi < len(m.q); ti++ {
		if t[ti] != m.q[qi] {
			continue
		}
		score += 3
		switch {
		case ti == last+1:
			score += 5
		case last >= -1:
			score -= min(ti-last-1, 8)
		}
		if ti == 0 || isSep(t[ti-1]) {
			score += 7
		}
		last = ti
		qi++
	}
	if qi < len(m.q) {
		return 0, false
	}
	return score, true
}

// isSep 报告 r 是否为词首分隔符（与 isBoundary 的分隔符集合一致）。
func isSep(r rune) bool {
	return r == '/' || r == '-' || r == '_' || r == ' ' || r == '.'
}

// BoundaryBitmap 返回 t 的词首边界位图：bit i 为 1 表示字节 i 处于词首
// （位置 0，或前一字节为分隔符，或为小写转大写的驼峰边界）。
// 超过 64 字节的名字该位恒为 0，仅损失词首加分，不影响判定正确性。
func BoundaryBitmap(t string) uint64 {
	var bm uint64
	for i := 0; i < len(t) && i < 64; i++ {
		if i == 0 || isSepByte(t[i-1]) || (isLowerByte(t[i-1]) && isUpperByte(t[i])) {
			bm |= 1 << uint(i)
		}
	}
	return bm
}

func isSepByte(b byte) bool {
	switch b {
	case '/', '-', '_', ' ', '.':
		return true
	}
	return false
}

func isLowerByte(b byte) bool { return 'a' <= b && b <= 'z' }
func isUpperByte(b byte) bool { return 'A' <= b && b <= 'Z' }

// CharMask 返回 t 中出现过字符的位图（字节值 mod 64）：无假阴性、允许假阳性，
// 适合做「查询字符是否全部可能存在」的 O(1) 预筛。对多字节 UTF-8 依然成立：
// 任一变体的末字节低 6 位恰为该字符码位低 6 位，故查询字符的（码位 mod 64）
// 必被其 UTF-8 末字节的（字节 mod 64）覆盖。
func CharMask(t string) uint64 {
	var m uint64
	for i := 0; i < len(t); i++ {
		m |= 1 << (uint(t[i]) & 63)
	}
	return m
}
