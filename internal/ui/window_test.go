package ui

import "testing"

// TestResolveFilter 覆盖过滤状态机解析：两入口提交、空查询停留、模式内
// partial 不弹、关键字后多空格/tab、大小写、Esc 前置条件见 TestEscActionFor。
func TestResolveFilter(t *testing.T) {
	cases := []struct {
		name      string
		committed filterMode
		text      string
		mode      filterMode
		query     string
		partial   bool
		rewrite   string
		doRewrite bool
	}{
		// —— 全量态：关键字候选前缀（partial）——
		{"全量空串", filterAll, "", filterAll, "", false, "", false},
		{"全量前缀 f", filterAll, "f", filterAll, "f", true, "", false},
		{"全量前缀 fi", filterAll, "fi", filterAll, "fi", true, "", false},
		{"全量前缀 fil", filterAll, "fil", filterAll, "fil", true, "", false},
		{"全量 file 是 files 严格前缀", filterAll, "file", filterAll, "file", true, "", false},
		{"全量完整词 files 不弹", filterAll, "files", filterAll, "files", false, "", false},
		{"全量前缀 a", filterAll, "a", filterAll, "a", true, "", false},
		{"全量前缀 ap", filterAll, "ap", filterAll, "ap", true, "", false},
		{"全量完整词 apps 不弹", filterAll, "apps", filterAll, "apps", false, "", false},
		{"全量大小写前缀 Fi", filterAll, "Fi", filterAll, "Fi", true, "", false},
		{"全量非法词 apx", filterAll, "apx", filterAll, "apx", false, "", false},
		{"全量非法词 application", filterAll, "application", filterAll, "application", false, "", false},
		{"全量普通含空白查询", filterAll, "foo bar", filterAll, "foo bar", false, "", false},
		{"全量前导空白", filterAll, " apps x", filterAll, " apps x", false, "", false},

		// —— 入口 a：文本自带关键字 → 提交并重写 ——
		{"入口a apps term", filterAll, "apps term", filterApps, "term", false, "term", true},
		{"入口a 大小写 APPS Terminal", filterAll, "APPS Terminal", filterApps, "Terminal", false, "Terminal", true},
		{"入口a files 多空格", filterAll, "files  goalsteer", filterFiles, "goalsteer", false, "goalsteer", true},
		{"入口a 大小写混合 Files x", filterAll, "Files x", filterFiles, "x", false, "x", true},
		{"入口a apps 仅空白", filterAll, "apps ", filterApps, "", false, "", true},
		{"入口a files 多空格仅空白", filterAll, "files   ", filterFiles, "", false, "", true},
		{"入口a tab 空白", filterAll, "apps\tterm", filterApps, "term", false, "term", true},
		{"入口a 模式内粘贴新关键字切换", filterApps, "files x", filterFiles, "x", false, "x", true},
		{"入口a 重写结果仍带关键字自然收敛", filterAll, "apps apps x", filterApps, "apps x", false, "apps x", true},

		// —— committed 模式内：文本就是查询词 ——
		{"apps 空查询停留", filterApps, "", filterApps, "", false, "", false},
		{"apps 模式内查询", filterApps, "term", filterApps, "term", false, "", false},
		{"apps 模式内多词查询", filterApps, "foo bar", filterApps, "foo bar", false, "", false},
		{"apps 模式内 f 不弹下拉", filterApps, "f", filterApps, "f", false, "", false},
		{"apps 模式内 fi 不弹下拉", filterApps, "fi", filterApps, "fi", false, "", false},
		{"apps 模式内完整词 files 不弹", filterApps, "files", filterApps, "files", false, "", false},
		{"files 空查询停留", filterFiles, "", filterFiles, "", false, "", false},
		{"files 模式内 goal", filterFiles, "goal", filterFiles, "goal", false, "", false},
		{"files 模式内 fi 不弹下拉", filterFiles, "fi", filterFiles, "fi", false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mode, query, partial, rewrite, doRewrite := resolveFilter(c.committed, c.text)
			if mode != c.mode || query != c.query || partial != c.partial ||
				rewrite != c.rewrite || doRewrite != c.doRewrite {
				t.Fatalf("resolveFilter(%v, %q) = (mode=%d, query=%q, partial=%v, rewrite=%q, doRewrite=%v)，"+
					"期望 (mode=%d, query=%q, partial=%v, rewrite=%q, doRewrite=%v)",
					c.committed, c.text,
					mode, query, partial, rewrite, doRewrite,
					c.mode, c.query, c.partial, c.rewrite, c.doRewrite)
			}
		})
	}
}

// TestEscActionFor 覆盖 Esc 分层退出的前置条件：下拉 → 清文本 → 退出模式 → 隐藏窗口回托盘。
func TestEscActionFor(t *testing.T) {
	cases := []struct {
		name      string
		popShown  bool
		text      string
		committed filterMode
		want      escAction
	}{
		{"下拉可见优先关下拉", true, "fi", filterAll, escClosePopup},
		{"下拉可见时模式内也先关下拉", true, "term", filterApps, escClosePopup},
		{"全量有文本清文本", false, "terminal", filterAll, escClearText},
		{"模式内有文本清文本停留模式", false, "term", filterApps, escClearText},
		{"模式内空白文本清文本", false, "  ", filterFiles, escClearText},
		{"模式空文本退到全量", false, "", filterApps, escExitFilter},
		{"全量空文本隐藏窗口驻留托盘", false, "", filterAll, escHideWindow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := escActionFor(c.popShown, c.text, c.committed); got != c.want {
				t.Fatalf("escActionFor(%v, %q, %v) = %d，期望 %d",
					c.popShown, c.text, c.committed, got, c.want)
			}
		})
	}
}
