package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type configFile struct {
	Name     string       `json:"name"`
	Path     string       `json:"path"`
	IsDir    bool         `json:"isDir"`
	Content  string       `json:"content,omitempty"`
	Children []configFile `json:"children,omitempty"`
}

type configNode struct {
	Entry    configFile
	Children []*configNode
}

func collectMosdnsFiles(path string) ([]configFile, string, error) {
	if path == "" {
		return nil, "", fmt.Errorf("配置路径为空")
	}
	baseDir := resolveConfigDir(path)
	nodes := map[string]*configNode{}
	root := &configNode{Entry: configFile{Name: filepath.Base(baseDir), Path: "", IsDir: true}}
	nodes[""] = root

	var addDir func(string) *configNode
	addDir = func(rel string) *configNode {
		if n, ok := nodes[rel]; ok {
			return n
		}
		if rel == "" {
			return root
		}
		parentPath := filepath.Dir(rel)
		if parentPath == "." {
			parentPath = ""
		}
		parent := addDir(parentPath)
		entry := &configNode{Entry: configFile{Name: filepath.Base(rel), Path: rel, IsDir: true}}
		parent.Children = append(parent.Children, entry)
		nodes[rel] = entry
		return entry
	}

	err := filepath.WalkDir(baseDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			addDir(rel)
			return nil
		}
		if !isAllowedConfigFile(d.Name()) {
			return nil
		}
		parentPath := filepath.Dir(rel)
		if parentPath == "." {
			parentPath = ""
		}
		parent := addDir(parentPath)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		entry := &configNode{Entry: configFile{Name: d.Name(), Path: rel, Content: string(data)}}
		parent.Children = append(parent.Children, entry)
		return nil
	})
	if err != nil {
		return nil, baseDir, fmt.Errorf("读取配置目录失败: %w", err)
	}
	return flattenConfigTree(root.Children), baseDir, nil
}

func flattenConfigTree(children []*configNode) []configFile {
	result := make([]configFile, len(children))
	for i, child := range children {
		entry := child.Entry
		if len(child.Children) > 0 {
			entry.Children = flattenConfigTree(child.Children)
		}
		result[i] = entry
	}
	return result
}

func resolveConfigDir(path string) string {
	if path == "" {
		return "."
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return path
	}
	dir := filepath.Dir(path)
	if dir == "" {
		return "."
	}
	return dir
}

func safeJoin(base, rel string) (string, error) {
	if base == "" {
		base = "."
	}
	if rel == "" {
		return "", fmt.Errorf("未提供文件名")
	}
	full := filepath.Join(base, rel)
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absFull, absBase) {
		return "", fmt.Errorf("非法文件路径")
	}
	return full, nil
}

func isAllowedConfigFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") ||
		strings.HasSuffix(lower, ".txt") ||
		strings.HasSuffix(lower, ".conf") ||
		strings.HasSuffix(lower, ".cfg") ||
		strings.HasSuffix(lower, ".json")
}
