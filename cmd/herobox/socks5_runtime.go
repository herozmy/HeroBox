package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/herozmy/herobox/internal/config"
)

func resolveBoolSetting(store *config.Store, key string, fallback bool) bool {
	if store == nil {
		return fallback
	}
	settings := store.Settings()
	val, ok := settings[key]
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func toggleSocks5References(baseDir string, enable bool) (int, error) {
	if baseDir == "" {
		return 0, nil
	}
	count := 0
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// 不在 JSON overrides 文件里注释/取消注释 SOCKS5，避免破坏 JSON。
		if strings.EqualFold(d.Name(), configOverridesFilename) {
			return nil
		}
		if !isAllowedConfigFile(d.Name()) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i, line := range lines {
			updated, toggled := adjustSocks5Line(line, enable)
			if toggled {
				lines[i] = updated
				count++
				changed = true
			}
		}
		if changed {
			mode := os.FileMode(0o644)
			if info, err := d.Info(); err == nil {
				mode = info.Mode()
			}
			content := strings.Join(lines, "\n")
			if len(lines) > 0 && strings.HasSuffix(string(data), "\n") {
				content += "\n"
			}
			if err := os.WriteFile(path, []byte(content), mode); err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}

func adjustSocks5Line(line string, enable bool) (string, bool) {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "socks5") {
		return line, false
	}
	idx := firstNonSpaceIndex(line)
	if idx == -1 {
		return line, false
	}
	prefix := line[:idx]
	body := line[idx:]
	trimmed := strings.TrimLeft(body, " \t")
	if trimmed == "" {
		return line, false
	}
	trimmedLower := strings.ToLower(trimmed)
	if !strings.Contains(trimmedLower, "socks5") {
		return line, false
	}
	if enable {
		if strings.HasPrefix(trimmed, "#") {
			un := strings.TrimLeft(trimmed[1:], " \t")
			return prefix + un, true
		}
		return line, false
	}
	if strings.HasPrefix(trimmed, "#") {
		return line, false
	}
	return prefix + "# " + body, true
}

func firstNonSpaceIndex(s string) int {
	for i, r := range s {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return -1
}
