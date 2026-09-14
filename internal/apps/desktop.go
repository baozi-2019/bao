// Package apps 提供已安装桌面应用（.desktop）的扫描、模糊搜索与启动。
package apps

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
func Search(list []App, query string, limit int) []App {
	if limit <= 0 {
		return nil
	}
	var normal, hidden []scoredApp
	for _, a := range list {
		score, ok := fuzzy.Match(query, a.Name)
		if cs, ok2 := fuzzy.Match(query, a.Comment); ok2 && cs > score {
			score, ok = cs, true
		}
		if !ok {
			continue
		}
		if a.NoDisplay {
			hidden = append(hidden, scoredApp{a, score})
		} else {
			normal = append(normal, scoredApp{a, score})
		}
	}
	sortByScore(normal)
	if len(normal) > 0 {
		return take(normal, limit)
	}
	sortByScore(hidden)
	return take(hidden, limit)
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

// sortByScore 按得分降序稳定排序。
func sortByScore(list []scoredApp) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
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
	return app, true
}

// unescapeExec 还原 .desktop Exec 值中的转义序列（\s、\n、\t、\\）。
func unescapeExec(s string) string {
	return strings.NewReplacer(`\s`, " ", `\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(s)
}
