package fuzzy

import (
	"strings"
	"testing"
)

// TestMatcherParityWithMatch 验证 ASCII 目标 + 预计算小写/边界位图时，
// Matcher.MatchLower 与 Match 的判定和打分完全一致。
func TestMatcherParityWithMatch(t *testing.T) {
	cases := []struct{ q, target string }{
		{"", "anything"},
		{"f", "firefox"},
		{"fb", "firefox browser"},
		{"FB", "FireFox"},
		{"abc", "a-b-c"},
		{"report", "Q3-Report_final.DOCX"},
		{"zzz", "no match here"},
		{"baozi", "bao-zi"},
		{"123", "abc123"},
		{"长长", "长长的名字"},
		{"x", ""},
	}
	for _, c := range cases {
		wantScore, wantOK := Match(c.q, c.target)
		gotScore, gotOK := NewMatcher(c.q).MatchLower(strings.ToLower(c.target), BoundaryBitmap(c.target))
		if gotOK != wantOK || gotScore != wantScore {
			t.Errorf("MatchLower(%q, %q) = (%d,%v)，Match = (%d,%v)",
				c.q, c.target, gotScore, gotOK, wantScore, wantOK)
		}
	}
}

func TestMatcherMaskPrefilterNoFalseNegative(t *testing.T) {
	// 对一批含多字节字符的名字：凡 Match 判定匹配的，预筛（长度 + 字符位图）必放行。
	targets := []string{"报告.pdf", "会议纪要-0906.md", "照片 001.jpg", "plain-ascii.txt", "日本語ファイル"}
	for _, target := range targets {
		m := NewMatcher(target) // 用全名做查询，必匹配
		if m.Mask()&^CharMask(strings.ToLower(target)) != 0 {
			t.Errorf("预筛误拒: query=%q target=%q", target, target)
		}
		for _, q := range []string{"报告", "pdf", "会议", "001", "ascii", "日", "語"} {
			mm := NewMatcher(q)
			if _, ok := mm.MatchLower(strings.ToLower(target), BoundaryBitmap(target)); ok {
				if mm.Mask()&^CharMask(strings.ToLower(target)) != 0 {
					t.Errorf("预筛误拒: query=%q target=%q", q, target)
				}
			}
		}
	}
}

func TestBoundaryBitmap(t *testing.T) {
	bm := BoundaryBitmap("FireFox")
	if bm&(1<<0) == 0 || bm&(1<<4) == 0 {
		t.Errorf("FireFox 应在位置 0 与 4（F 驼峰）有边界位: %b", bm)
	}
	if bm&(1<<1) != 0 {
		t.Errorf("FireFox 位置 1 不应是边界: %b", bm)
	}
	bm = BoundaryBitmap("a-b")
	if bm&(1<<0) == 0 || bm&(1<<2) == 0 || bm&(1<<1) != 0 {
		t.Errorf("a-b 边界位错误: %b", bm)
	}
	// 超长名字不 panic，越界位恒 0。
	if BoundaryBitmap(strings.Repeat("a", 200)) != 1 {
		t.Error("超长名字应仅有位置 0 边界位")
	}
}

func TestCharMask(t *testing.T) {
	if CharMask("a")&CharMask("a") == 0 {
		t.Error("CharMask(a) 应包含 a 的位")
	}
	// 位图作用在「已小写」的串上：'A' 经 strings.ToLower 归一为 'a'（97%64=33），
	// 'b'（98%64=34）与 'a' 不同位。这里验证小写归一后的一致性。
	if CharMask(strings.ToLower("A")) != CharMask("a") {
		t.Error("小写归一后 A 与 a 应映射到同一位")
	}
	if CharMask("a") == CharMask("b") {
		t.Error("a 与 b 不应同位")
	}
}
