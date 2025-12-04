package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/herozmy/herobox/internal/config"
	"github.com/herozmy/herobox/internal/logs"
	"gopkg.in/yaml.v3"
)

func resolveMosdnsLogFile(store *config.Store) string {
	if env := os.Getenv("MOSDNS_LOG_FILE"); env != "" {
		return env
	}
	if store != nil {
		if file := extractLogFilePath(store.GetConfigPath()); file != "" {
			return file
		}
	}
	return "/tmp/mosdns.log"
}

func extractLogFilePath(configPath string) string {
	if configPath == "" {
		return ""
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var cfg struct {
		Log struct {
			File string `yaml:"file"`
		} `yaml:"log"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Log.File)
}

func readMosdnsLogEntries(logFile string, limit int) []logs.Entry {
	if limit <= 0 {
		limit = 400
	}
	f, err := os.Open(logFile)
	if err != nil {
		return []logs.Entry{{
			Timestamp: time.Now(),
			Level:     "error",
			Message:   fmt.Sprintf("无法读取 mosdns 日志 (%s): %v", logFile, err),
		}}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := make([]string, 0, limit)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > limit {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return []logs.Entry{{
			Timestamp: time.Now(),
			Level:     "error",
			Message:   fmt.Sprintf("读取 mosdns 日志失败: %v", err),
		}}
	}
	entries := make([]logs.Entry, 0, len(lines))
	for _, line := range lines {
		if entry, ok := parseMosdnsLogLine(line); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func parseMosdnsLogLine(line string) (logs.Entry, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return logs.Entry{}, false
	}
	entry := logs.Entry{Timestamp: time.Now(), Level: "info", Message: trimmed}
	if ts, rest, ok := extractTimestamp(trimmed); ok {
		entry.Timestamp = ts
		entry.Message = strings.TrimSpace(rest)
	}
	return entry, true
}

func extractTimestamp(line string) (time.Time, string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return time.Time{}, "", false
	}
	if ts, err := time.Parse("2006-01-02T15:04:05.000Z0700", fields[0]); err == nil {
		rest := strings.TrimPrefix(line, fields[0])
		return ts, rest, true
	}
	if len(fields) >= 2 {
		candidate := fields[0] + " " + fields[1]
		if ts, err := time.Parse("2006/01/02 15:04:05", candidate); err == nil {
			rest := strings.TrimPrefix(line, candidate)
			return ts, rest, true
		}
	}
	return time.Time{}, "", false
}
