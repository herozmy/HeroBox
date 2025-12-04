package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Store 持久化保存可在前端调整的配置信息，例如 mosdns 配置路径与 UI 设置。
type Store struct {
	mu              sync.RWMutex
	configPath      string
	heroboxPort     string
	mosdnsState     string
	mosdnsPID       int
	mosdnsVersion   string
	uiSettings      map[string]string
	configOverrides Overrides
	filePath        string
}

type fileState struct {
	HeroboxPort     string            `yaml:"heroboxPort"`
	UISettings      map[string]string `yaml:"uiSettings,omitempty"`
	ConfigOverrides Overrides         `yaml:"configOverrides,omitempty"`
	Mosdns          struct {
		ConfigPath string `yaml:"configPath"`
		Status     string `yaml:"status"`
		PID        int    `yaml:"pid"`
		Version    string `yaml:"version"`
	} `yaml:"mosdns"`
}

func NewStore(defaultConfigPath, filePath string) (*Store, error) {
	if defaultConfigPath == "" {
		defaultConfigPath = "/etc/herobox/mosdns/config.yaml"
	}
	store := &Store{
		configPath: defaultConfigPath,
		uiSettings: make(map[string]string),
		filePath:   filePath,
	}
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

func (s *Store) SetMosdnsStatus(status string) error {
	status = strings.TrimSpace(status)
	s.mu.Lock()
	s.mosdnsState = status
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) SetMosdnsPID(pid int) error {
	s.mu.Lock()
	s.mosdnsPID = pid
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) MosdnsPID() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mosdnsPID
}

func (s *Store) SetMosdnsVersion(version string) error {
	version = strings.TrimSpace(version)
	s.mu.Lock()
	s.mosdnsVersion = version
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) MosdnsVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mosdnsVersion
}

func (s *Store) SetHeroboxPort(port string) error {
	if port == "" {
		return nil
	}
	s.mu.Lock()
	s.heroboxPort = port
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) Settings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]string, len(s.uiSettings))
	for k, v := range s.uiSettings {
		result[k] = v
	}
	return result
}

func (s *Store) ConfigOverrides() Overrides {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configOverrides.Clone()
}

func (s *Store) UpdateSettings(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.uiSettings == nil {
		s.uiSettings = make(map[string]string)
	}
	for k, v := range values {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		s.uiSettings[key] = v
	}
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) SetConfigOverrides(ov Overrides) error {
	s.mu.Lock()
	s.configOverrides = ov.Clone()
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
	if err := yaml.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Mosdns.ConfigPath != "" {
		s.configPath = state.Mosdns.ConfigPath
	}
	s.mosdnsState = state.Mosdns.Status
	s.mosdnsPID = state.Mosdns.PID
	s.mosdnsVersion = state.Mosdns.Version
	if len(state.UISettings) > 0 {
		if s.uiSettings == nil {
			s.uiSettings = make(map[string]string)
		}
		for k, v := range state.UISettings {
			s.uiSettings[k] = v
		}
	}
	if state.HeroboxPort != "" {
		s.heroboxPort = state.HeroboxPort
	}
	s.configOverrides = state.ConfigOverrides.Clone()
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
		HeroboxPort: s.heroboxPort,
		UISettings:  make(map[string]string, len(s.uiSettings)),
	}
	for k, v := range s.uiSettings {
		state.UISettings[k] = v
	}
	state.Mosdns.ConfigPath = s.configPath
	state.Mosdns.Status = s.mosdnsState
	state.Mosdns.PID = s.mosdnsPID
	state.Mosdns.Version = s.mosdnsVersion
	state.ConfigOverrides = s.configOverrides.Clone()
	s.mu.RUnlock()

	data, err := yaml.Marshal(&state)
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0o644)
}
