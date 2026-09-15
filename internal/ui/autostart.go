package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 本文件实现「开机自启」设置：通过 XDG autostart 目录的 bao.desktop
// 控制登录后是否以 --hidden 模式驻留。
//
// 状态语义（与 freedesktop 自动启动规范一致）：
//   - 用户文件 ~/.config/autostart/bao.desktop 存在且含 Hidden=true → 关（用户级屏蔽）
//   - 用户文件存在但无 Hidden=true → 开
//   - 用户文件不存在但 /etc/xdg/autostart/bao.desktop 存在 → 开（系统默认）
//   - 两者都不存在 → 关
//
// 关闭自启时不动系统文件，只写带 Hidden=true 的用户文件（规范规定的屏蔽方式）。

// defaultDesktopEntry 是无系统自启文件时使用的简化版桌面项内容。
const defaultDesktopEntry = `[Desktop Entry]
Type=Application
Name=bao
Comment=bao 启动器
Exec=bao --hidden
Icon=bao
Terminal=false
`

// autostartUserPath 返回用户级自启文件路径：$XDG_CONFIG_HOME/autostart/bao.desktop
// （XDG_CONFIG_HOME 未设置或非绝对路径时回退 ~/.config/autostart/）。
func autostartUserPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "autostart", "bao.desktop")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "autostart", "bao.desktop")
}

// autostartSystemPath 返回系统级自启文件路径（XDG 默认搜索路径）。
func autostartSystemPath() string {
	return "/etc/xdg/autostart/bao.desktop"
}

// autostartEnabled 是纯状态推导：用户文件是否存在、是否含 Hidden=true、
// 系统文件是否存在 → 当前自启是否生效。
func autostartEnabled(userExists, userHidden, sysExists bool) bool {
	if userExists {
		return !userHidden
	}
	return sysExists
}

// parseDesktopHidden 判断桌面项内容是否含 Hidden=true（大小写不敏感、忽略空白）。
// Hidden 是布尔键，只认 true；缺失或其他值均视为未屏蔽。
func parseDesktopHidden(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "hidden") {
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return false
}

// desktopEntrySetHidden 返回把 Hidden=true 写入或移出 [Desktop Entry] 组后的内容：
// hidden=true 时在 [Desktop Entry] 行后插入一行（无该组则追加到文件尾），
// hidden=false 时删除所有 Hidden= 行。其他内容原样保留。
func desktopEntrySetHidden(content string, hidden bool) string {
	lines := strings.Split(content, "\n")
	// 结尾空行（Split 产物）先摘掉，处理完再补回一个换行。
	trailing := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailing {
		lines = lines[:len(lines)-1]
	}

	out := make([]string, 0, len(lines)+1)
	inserted := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHiddenLine := false
		if key, _, ok := strings.Cut(trimmed, "="); ok {
			if strings.EqualFold(strings.TrimSpace(key), "hidden") {
				isHiddenLine = true
			}
		}
		if isHiddenLine {
			continue // 移除旧 Hidden 行
		}
		out = append(out, line)
		if hidden && !inserted && strings.HasPrefix(trimmed, "[") && strings.HasPrefix(trimmed, "[Desktop Entry]") {
			out = append(out, "Hidden=true")
			inserted = true
		}
	}
	if hidden && !inserted {
		// 内容里没有 [Desktop Entry] 组：补一个组再写 Hidden。
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "[Desktop Entry]", "Hidden=true")
	}
	result := strings.Join(out, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

// AutostartEnabled 读取用户文件与系统文件，返回当前自启是否生效。
func AutostartEnabled() (bool, error) {
	content, err := os.ReadFile(autostartUserPath())
	switch {
	case err == nil:
		return autostartEnabled(true, parseDesktopHidden(string(content)), false), nil
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("读取用户自启文件: %w", err)
	}

	_, serr := os.Stat(autostartSystemPath())
	switch {
	case serr == nil:
		return true, nil // 系统默认自启，用户未屏蔽
	case errors.Is(serr, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("检查系统自启文件: %w", serr)
	}
}

// SetAutostart 开启或关闭开机自启（写用户级文件，绝不改动系统文件）：
//   - enable=true：用户文件已存在则移除其中的 Hidden；否则复制系统文件内容
//     （剥离其中的 Hidden 行保证语义），无系统文件则写简化版；最终均不含 Hidden=true。
//   - enable=false：基于现有用户文件（或简化版）写入 Hidden=true。
func SetAutostart(enable bool) error {
	user := autostartUserPath()
	content, err := os.ReadFile(user)
	userExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取用户自启文件: %w", err)
	}

	var base string
	switch {
	case userExists:
		base = string(content)
	case enable:
		if sys, err := os.ReadFile(autostartSystemPath()); err == nil {
			base = desktopEntrySetHidden(string(sys), false) // 复制系统内容并剥离屏蔽
		} else {
			base = defaultDesktopEntry
		}
	default:
		base = defaultDesktopEntry
	}

	return writeAutostart(user, desktopEntrySetHidden(base, !enable))
}

// writeAutostart 把内容写入用户自启文件（父目录自动创建，权限 0644）。
func writeAutostart(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 autostart 目录: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入自启文件 %s: %w", path, err)
	}
	return nil
}
