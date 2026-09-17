// Package apps 提供已安装桌面应用（.desktop）的扫描、模糊搜索与启动。
package apps

import (
	"cmp"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"bao/internal/fuzzy"
)

// App 描述一个已安装的桌面应用。
type App struct {
	Name      string
	Comment   string
	Exec      string
	Icon      string
	ID        string // .desktop 文件完整路径
	NoDisplay bool

	// 搜索预计算形态（parseDesktop 填充，语义同 search.Entry）：查询期配合
	// fuzzy.Matcher 做零分配匹配。lowerName 为零值表示手工构造的未预计算
	// 实例，Search 对此退化为 fuzzy.Match。
	lowerName    string // Name 的小写形态
	boundName    uint64 // Name 的词首边界位图（fuzzy.BoundaryBitmap）
	charsName    uint64 // Name 的字符存在位图（fuzzy.CharMask(lowerName)）
	lowerComment string
	boundComment uint64
	charsComment uint64
}

// Scan 解析 XDG 应用目录中的全部 .desktop 文件。
// 目录优先级：$XDG_DATA_HOME/applications 在前，$XDG_DATA_DIRS 各项依次在后；
// 同名文件按优先级去重（用户目录覆盖系统目录），Hidden=true 的条目被忽略。
func Scan() []App {
	locs := locales()
	seen := map[string]bool{}
	var apps []App
	for _, dir := range dataDirs() {
		root := filepath.Join(dir, "applications")
		// 遍历任一错误（如无权限）跳过不中断
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(name, ".desktop") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = name
			}
			if seen[rel] {
				return nil
			}
			app, ok := parseDesktop(path, locs)
			if !ok {
				return nil
			}
			seen[rel] = true
			apps = append(apps, app)
			return nil
		})
	}
	return apps
}

// Search 在 list 中按 Name/Comment 做模糊匹配，返回至多 limit 个按得分降序的应用。
// NoDisplay 应用仅在没有任何普通结果时作为兜底返回。
// Scan 解析出的应用带预计算字段，单次查询只构建一次 Matcher 并走零分配快速路径；
// 手工构造（预计算字段为零值）的实例逐条退化为 fuzzy.Match，行为与旧实现一致。
func Search(list []App, query string, limit int) []App {
	if limit <= 0 {
		return nil
	}
	m := fuzzy.NewMatcher(query)
	qlen := m.RuneLen()
	qmask := m.Mask()
	var normal, hidden []scoredApp
	for _, a := range list {
		score, ok := a.match(m, qlen, qmask, query)
		if !ok {
			continue
		}
		if a.NoDisplay {
			hidden = append(hidden, scoredApp{a, score})
		} else {
			normal = append(normal, scoredApp{a, score})
		}
	}
	byScore := func(x, y scoredApp) int { return cmp.Compare(y.score, x.score) }
	slices.SortStableFunc(normal, byScore)
	if len(normal) > 0 {
		return take(normal, limit)
	}
	slices.SortStableFunc(hidden, byScore)
	return take(hidden, limit)
}

// match 返回 Name/Comment 模糊匹配的较高得分。预计算字段就绪时走长度 + 字符位图
// 预筛与 MatchLower 打分（与 search.Cache 同一套语义，parity 由
// fuzzy.TestMatcherParityWithMatch 保证）；lowerName 为零值（手工构造）时
// 退化为 fuzzy.Match。
func (a App) match(m fuzzy.Matcher, qlen int, qmask uint64, query string) (int, bool) {
	if a.lowerName == "" {
		ns, nok := fuzzy.Match(query, a.Name)
		if cs, cok := fuzzy.Match(query, a.Comment); cok && cs > ns {
			return cs, true
		}
		return ns, nok
	}
	ns, nok := matchPrepared(m, qlen, qmask, a.lowerName, a.boundName, a.charsName)
	if cs, cok := matchPrepared(m, qlen, qmask, a.lowerComment, a.boundComment, a.charsComment); cok && cs > ns {
		return cs, true
	}
	return ns, nok
}

// matchPrepared 先做长度与字符存在位图预筛，再执行 MatchLower；预筛无假阴性，
// 打分语义与 fuzzy.Match 一致。
func matchPrepared(m fuzzy.Matcher, qlen int, qmask uint64, lower string, bound, chars uint64) (int, bool) {
	if len(lower) < qlen || qmask&^chars != 0 {
		return 0, false
	}
	return m.MatchLower(lower, bound)
}

// Launch 通过 `gio launch <ID>` 启动应用。
func Launch(a App) error {
	return exec.Command("gio", "launch", a.ID).Run()
}

// scoredApp 携带匹配得分用于排序。
type scoredApp struct {
	app   App
	score int
}

// take 取前 limit 个应用。
func take(list []scoredApp, limit int) []App {
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]App, len(list))
	for i, s := range list {
		out[i] = s.app
	}
	return out
}

// dataDirs 返回按优先级排列的 XDG 数据目录。
func dataDirs() []string {
	var dirs []string
	home := os.Getenv("XDG_DATA_HOME")
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".local", "share")
		}
	}
	if home != "" {
		dirs = append(dirs, home)
	}
	sys := os.Getenv("XDG_DATA_DIRS")
	if sys == "" {
		sys = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(sys, ":") {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// locales 返回当前语言环境候选列表（如 zh_CN.UTF-8 → ["zh_CN", "zh"]）。
func locales() []string {
	lang := os.Getenv("LC_ALL")
	if lang == "" {
		lang = os.Getenv("LC_MESSAGES")
	}
	if lang == "" {
		lang = os.Getenv("LANG")
	}
	lang = strings.Split(lang, ".")[0]
	if lang == "" || lang == "C" || lang == "POSIX" {
		return nil
	}
	out := []string{lang}
	if i := strings.Index(lang, "_"); i >= 0 {
		out = append(out, lang[:i])
	}
	return out
}

// pickLocalized 按 locale 优先级从本地化表取值；无命中返回空串。
func pickLocalized(m map[string]string, locs []string) string {
	for _, l := range locs {
		if v, ok := m[l]; ok {
			return v
		}
	}
	return ""
}

// parseDesktop 解析单个 .desktop 文件；Hidden=true、类型非 Application 或缺少 Name 时返回 ok=false。
func parseDesktop(path string, locs []string) (App, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return App{}, false
	}
	var app App
	app.ID = path
	typ := ""
	hidden := false
	locName := map[string]string{}
	locComment := map[string]string{}
	inEntry := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inEntry = strings.TrimSpace(line[1:len(line)-1]) == "Desktop Entry"
			continue
		}
		if !inEntry {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		base, loc, _ := strings.Cut(key, "[")
		loc = strings.TrimSuffix(loc, "]")
		switch base {
		case "Name":
			if loc == "" {
				app.Name = value
			} else {
				locName[loc] = value
			}
		case "Comment":
			if loc == "" {
				app.Comment = value
			} else {
				locComment[loc] = value
			}
		case "Exec":
			app.Exec = unescapeExec(value)
		case "Icon":
			app.Icon = value
		case "Type":
			typ = value
		case "Hidden":
			hidden = strings.EqualFold(value, "true")
		case "NoDisplay":
			app.NoDisplay = strings.EqualFold(value, "true")
		}
	}
	if hidden || app.Name == "" {
		return App{}, false
	}
	if typ != "" && typ != "Application" {
		return App{}, false
	}
	if v := pickLocalized(locName, locs); v != "" {
		app.Name = v
	}
	if v := pickLocalized(locComment, locs); v != "" {
		app.Comment = v
	}
	// 填充搜索预计算形态（须在本地化覆盖 Name/Comment 之后）。
	app.lowerName = strings.ToLower(app.Name)
	app.boundName = fuzzy.BoundaryBitmap(app.Name)
	app.charsName = fuzzy.CharMask(app.lowerName)
	app.lowerComment = strings.ToLower(app.Comment)
	app.boundComment = fuzzy.BoundaryBitmap(app.Comment)
	app.charsComment = fuzzy.CharMask(app.lowerComment)
	return app, true
}

// unescapeExec 还原 .desktop Exec 值中的转义序列（\s、\n、\t、\\）。
func unescapeExec(s string) string {
	return strings.NewReplacer(`\s`, " ", `\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(s)
}
