package config

import "sync"

// Store 保存可动态调整的配置，例如 mosdns 配置路径。
type Store struct {
	mu         sync.RWMutex
	configPath string
}

func NewStore(defaultPath string) *Store {
	if defaultPath == "" {
		defaultPath = "/etc/herobox/mosdns/config.yaml"
	}
	return &Store{configPath: defaultPath}
}

func (s *Store) GetConfigPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configPath
}

func (s *Store) SetConfigPath(path string) {
	if path == "" {
		return
	}
	s.mu.Lock()
	s.configPath = path
	s.mu.Unlock()
}
