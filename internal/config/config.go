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

// DefaultExcluded 返回内置默认排除目录的副本，调用方可安全修改。
// 回收站按当前用户主目录展开为绝对路径，以便按完整路径前缀匹配。
func DefaultExcluded() []string {
	dirs := []string{"node_modules", ".git", ".cache", ".npm", ".cargo"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "Trash"))
	}
	return dirs
}

// Load 读取配置文件并返回配置。
// 配置文件不存在（或内容为空）时返回默认配置且不报错；
// JSON 损坏时返回错误。
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return &Config{ExcludedDirs: DefaultExcluded()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	var user Config
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &user); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s: %w", Path(), err)
		}
	}
	return &Config{ExcludedDirs: mergeExcluded(DefaultExcluded(), user.ExcludedDirs)}, nil
}

// Save 将配置写回 Path()，文件权限收敛到 0600，父目录不存在时自动创建。
func (c *Config) Save() error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
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
