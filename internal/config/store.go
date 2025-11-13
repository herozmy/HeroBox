package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store 持久化保存可在前端调整的配置信息，例如 mosdns 配置路径。
type Store struct {
	mu         sync.RWMutex
	configPath string
	filePath   string
	settings   map[string]string
}

type fileState struct {
	MosdnsConfigPath string            `json:"mosdnsConfigPath"`
	UISettings       map[string]string `json:"uiSettings,omitempty"`
}

func NewStore(defaultPath, filePath string) (*Store, error) {
	if defaultPath == "" {
		defaultPath = "/etc/herobox/mosdns/config.yaml"
	}
	store := &Store{configPath: defaultPath, filePath: filePath, settings: map[string]string{}}
	if err := store.load(); err != nil {
		return nil, err
	}
	if err := store.persist(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) GetConfigPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configPath
}

func (s *Store) SetConfigPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("配置路径不能为空")
	}
	s.mu.Lock()
	s.configPath = path
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) Settings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyMap := make(map[string]string, len(s.settings))
	for k, v := range s.settings {
		copyMap[k] = v
	}
	return copyMap
}

func (s *Store) UpdateSettings(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.settings == nil {
		s.settings = make(map[string]string)
	}
	for k, v := range values {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		s.settings[key] = v
	}
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) load() error {
	if s.filePath == "" {
		return nil
	}
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.MosdnsConfigPath != "" {
		s.configPath = state.MosdnsConfigPath
	}
	if len(state.UISettings) > 0 {
		s.settings = make(map[string]string, len(state.UISettings))
		for k, v := range state.UISettings {
			s.settings[k] = v
		}
	}
	return nil
}

func (s *Store) persist() error {
	if s.filePath == "" {
		return nil
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.mu.RLock()
	state := fileState{
		MosdnsConfigPath: s.configPath,
		UISettings:       make(map[string]string, len(s.settings)),
	}
	for k, v := range s.settings {
		state.UISettings[k] = v
	}
	s.mu.RUnlock()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0o644)
}
