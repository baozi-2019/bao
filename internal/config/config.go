// Package config 负责 bao 启动器配置的加载与保存。
// 配置文件位于 $XDG_CONFIG_HOME/bao/config.json（缺省 ~/.config/bao/config.json），
// 内置默认排除目录与用户配置合并去重后对外生效。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// appDirName 是 $XDG_CONFIG_HOME 下的应用配置目录名。
const appDirName = "bao"

// Config 是 bao 的持久化配置。
type Config struct {
	// ExcludedDirs 是有效排除目录列表（内置默认与用户配置合并去重后）。
	ExcludedDirs []string `json:"excluded_dirs"`
}

// Path 返回配置文件路径：$XDG_CONFIG_HOME/bao/config.json。
// XDG_CONFIG_HOME 未设置或不是绝对路径时，回退到 ~/.config/bao/config.json。
func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, appDirName, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", appDirName, "config.json")
}

// DefaultExcluded 返回内置默认排除目录的副本（按目录基名匹配，命中即整目录跳过）。
// 只收录可见的构建产物类重目录；隐藏目录（.git/.cache 等）由文件遍历的隐藏项
// 规则统一跳过，回收站位于 ~/.local 下同样不可达，二者均无须在此列出。
func DefaultExcluded() []string {
	return []string{"node_modules", "__pycache__", "target", "venv", "build", "dist"}
}

// Load 读取配置文件并返回配置。
// 配置文件不存在（或内容为空）时返回默认配置且不报错；
// JSON 损坏时返回错误。
// 配置文件只存差量：excluded_dirs 为用户新增项，removed_defaults 为用户停用的
// 内置默认项；加载时按（内置默认 - 停用项）∪ 新增项 还原有效列表。
// 旧版配置曾把合并后的完整列表写入 excluded_dirs，按新增项处理结果一致。
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return &Config{ExcludedDirs: DefaultExcluded()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	var raw struct {
		Added   []string `json:"excluded_dirs"`
		Removed []string `json:"removed_defaults"`
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s: %w", Path(), err)
		}
	}
	removed := make(map[string]bool, len(raw.Removed))
	for _, entry := range raw.Removed {
		removed[normalizeEntry(entry)] = true
	}
	active := make([]string, 0, len(DefaultExcluded()))
	for _, entry := range DefaultExcluded() {
		if !removed[entry] {
			active = append(active, entry)
		}
	}
	return &Config{ExcludedDirs: mergeExcluded(active, raw.Added)}, nil
}

// Save 将配置写回 Path()，文件权限收敛到 0600，父目录不存在时自动创建。
// 写入的是差量表示：有效列表中属于内置默认的项不重复记录，缺失的默认项记入
// removed_defaults，使"停用某个内置默认"可以被持久化。
func (c *Config) Save() error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	defaults := DefaultExcluded()
	payload := struct {
		Added   []string `json:"excluded_dirs"`
		Removed []string `json:"removed_defaults"`
	}{
		Added:   setDiff(c.ExcludedDirs, defaults),
		Removed: setDiff(defaults, c.ExcludedDirs),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入配置文件: %w", err)
	}
	// WriteFile 只在新建文件时应用权限，已存在文件需显式收敛。
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置配置文件权限: %w", err)
	}
	return nil
}

// setDiff 返回 a - b：按 a 的顺序输出规范化去重后的元素，规范化后等于 b 中
// 某项的一律剔除。
func setDiff(a, b []string) []string {
	banned := make(map[string]bool, len(b))
	for _, entry := range b {
		banned[normalizeEntry(entry)] = true
	}
	seen := make(map[string]bool, len(a))
	out := make([]string, 0, len(a))
	for _, entry := range a {
		entry = normalizeEntry(entry)
		if entry == "" || banned[entry] || seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out
}

// IsExcluded 报告 name（条目基名）或 fullPath（完整路径）是否命中排除列表：
// 任一条目等于 name（基名匹配），或是 fullPath 的前缀（路径匹配）时返回 true。
func (c *Config) IsExcluded(name, fullPath string) bool {
	for _, entry := range c.ExcludedDirs {
		if entry == "" {
			continue
		}
		if entry == name || strings.HasPrefix(fullPath, entry) {
			return true
		}
	}
	return false
}

// AllExcluded 返回 ExcludedDirs 的去重副本，调用方可安全修改。
func (c *Config) AllExcluded() []string {
	seen := make(map[string]bool, len(c.ExcludedDirs))
	out := make([]string, 0, len(c.ExcludedDirs))
	for _, entry := range c.ExcludedDirs {
		if entry != "" && !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out
}

// mergeExcluded 将用户配置条目并入默认列表：先规范化（去空白、展开 ~ 前缀、
// 去结尾分隔符），跳过空条目并去重，保持默认在前、用户新增在后的顺序。
func mergeExcluded(defaults, user []string) []string {
	seen := make(map[string]bool, len(defaults)+len(user))
	out := make([]string, 0, len(defaults)+len(user))
	appendOne := func(entry string) {
		entry = normalizeEntry(entry)
		if entry == "" || seen[entry] {
			return
		}
		seen[entry] = true
		out = append(out, entry)
	}
	for _, entry := range defaults {
		appendOne(entry)
	}
	for _, entry := range user {
		appendOne(entry)
	}
	return out
}

// normalizeEntry 清理单条排除配置：去除首尾空白、展开开头的 ~ 为用户主目录、
// 去除结尾的路径分隔符（根目录 "/" 除外）。
func normalizeEntry(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return entry
	}
	if strings.HasPrefix(entry, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			entry = filepath.Join(home, entry[2:])
		}
	}
	for len(entry) > 1 && os.IsPathSeparator(entry[len(entry)-1]) {
		entry = entry[:len(entry)-1]
	}
	return entry
}
