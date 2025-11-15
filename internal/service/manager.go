package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/herozmy/herobox/internal/logs"
)

// Status 表示 systemd 报告的状态结果。
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusUnknown Status = "unknown"
	StatusMissing Status = "missing"
)

// ServiceSpec 定义一个受控服务。
type ServiceSpec struct {
	Name        string   // 业务名称，例如 mosdns
	Unit        string   // systemd unit 名称，例如 mosdns.service
	BinaryPaths []string // 可选：对应核心二进制路径（可多备选）
	Hooks       ServiceHooks
}

// Snapshot 描述一个服务的即时状态。
type Snapshot struct {
	Name        string    `json:"name"`
	Unit        string    `json:"unit"`
	Status      Status    `json:"status"`
	LastUpdated time.Time `json:"lastUpdated"`
	Version     string    `json:"version,omitempty"`
}

// ServiceHooks 允许为特定服务注入自定义驱动逻辑（例如直接执行二进制）。
type ServiceHooks struct {
	Start   func(ctx context.Context, spec ServiceSpec) error
	Stop    func(ctx context.Context, spec ServiceSpec) error
	Restart func(ctx context.Context, spec ServiceSpec) error
}

// Manager 负责通过 systemctl 控制服务，若系统不支持则自动切换为内存模拟模式。
type Manager struct {
	specs  map[string]ServiceSpec
	states map[string]Snapshot
	mu     sync.RWMutex
	useCtl bool
	dryRun bool
}

// NewManager 创建 Manager。若未找到 systemctl 或显式设置 HEROBOX_DRY_RUN=true，则进入 dry-run。
func NewManager(specs []ServiceSpec) *Manager {
	specMap := make(map[string]ServiceSpec, len(specs))
	for _, spec := range specs {
		specMap[strings.ToLower(spec.Name)] = spec
	}

	_, err := exec.LookPath("systemctl")
	dryRun := os.Getenv("HEROBOX_DRY_RUN") == "true"
	useCtl := err == nil && !dryRun

	return &Manager{
		specs:  specMap,
		states: make(map[string]Snapshot, len(specMap)),
		useCtl: useCtl,
		dryRun: !useCtl,
	}
}

// ensureSpec 返回服务定义。
func (m *Manager) ensureSpec(name string) (ServiceSpec, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	spec, ok := m.specs[strings.ToLower(name)]
	if !ok {
		return ServiceSpec{}, fmt.Errorf("service %s 未注册", name)
	}
	return spec, nil
}

// Start 启动服务。
func (m *Manager) Start(ctx context.Context, name string) error {
	spec, err := m.ensureSpec(name)
	if err != nil {
		return err
	}
	if !m.binaryReady(spec) {
		m.recordState(spec.Name, StatusMissing)
		err = fmt.Errorf("%s 未安装", spec.Name)
		logService(spec, "error", "%s start 失败：%v", spec.Name, err)
		return err
	}
	if spec.Hooks.Start != nil {
		if err := spec.Hooks.Start(ctx, spec); err != nil {
			logService(spec, "error", "自定义 start 失败：%v", err)
			return err
		}
	} else if m.useCtl {
		if err := m.execSystemctl(ctx, "start", spec.Unit); err != nil {
			logService(spec, "error", "systemctl start %s(%s) 失败：%v", spec.Name, spec.Unit, err)
			return err
		}
	}
	m.recordState(spec.Name, StatusRunning)
	logService(spec, "info", "%s 已启动", spec.Name)
	return nil
}

// Stop 停止服务。
func (m *Manager) Stop(ctx context.Context, name string) error {
	spec, err := m.ensureSpec(name)
	if err != nil {
		return err
	}
	if !m.binaryReady(spec) {
		m.recordState(spec.Name, StatusMissing)
		err = fmt.Errorf("%s 未安装", spec.Name)
		logService(spec, "error", "%s stop 失败：%v", spec.Name, err)
		return err
	}
	if spec.Hooks.Stop != nil {
		if err := spec.Hooks.Stop(ctx, spec); err != nil {
			logService(spec, "error", "自定义 stop 失败：%v", err)
			return err
		}
	} else if m.useCtl {
		if err := m.execSystemctl(ctx, "stop", spec.Unit); err != nil {
			logService(spec, "error", "systemctl stop %s(%s) 失败：%v", spec.Name, spec.Unit, err)
			return err
		}
	}
	m.recordState(spec.Name, StatusStopped)
	logService(spec, "info", "%s 已停止", spec.Name)
	return nil
}

// Restart 重启服务。
func (m *Manager) Restart(ctx context.Context, name string) error {
	spec, err := m.ensureSpec(name)
	if err != nil {
		return err
	}
	if !m.binaryReady(spec) {
		m.recordState(spec.Name, StatusMissing)
		err = fmt.Errorf("%s 未安装", spec.Name)
		logService(spec, "error", "%s restart 失败：%v", spec.Name, err)
		return err
	}
	if spec.Hooks.Restart != nil {
		if err := spec.Hooks.Restart(ctx, spec); err != nil {
			logService(spec, "error", "自定义 restart 失败：%v", err)
			return err
		}
	} else if m.useCtl {
		if err := m.execSystemctl(ctx, "restart", spec.Unit); err != nil {
			logService(spec, "error", "systemctl restart %s(%s) 失败：%v", spec.Name, spec.Unit, err)
			return err
		}
	} else {
		// dry run: 直接标记为 running
		m.recordState(spec.Name, StatusRunning)
		logService(spec, "info", "%s restart (dry-run)", spec.Name)
		return nil
	}
	m.recordState(spec.Name, StatusRunning)
	logService(spec, "info", "%s 已重启", spec.Name)
	return nil
}

// Status 获取服务状态。
func (m *Manager) Status(ctx context.Context, name string) (Snapshot, error) {
	spec, err := m.ensureSpec(name)
	if err != nil {
		return Snapshot{}, err
	}
	if !m.binaryReady(spec) {
		m.recordState(spec.Name, StatusMissing)
		logService(spec, "error", "%s 状态：missing（binary 未找到）", spec.Name)
		return m.snapshot(spec.Name), nil
	}
	if m.useCtl {
		if err := m.execSystemctl(ctx, "is-active", spec.Unit); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				m.recordState(spec.Name, StatusStopped)
			} else {
				return Snapshot{}, err
			}
		} else {
			m.recordState(spec.Name, StatusRunning)
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	snap, ok := m.states[strings.ToLower(spec.Name)]
	if !ok {
		snap = Snapshot{Name: spec.Name, Unit: spec.Unit, Status: StatusUnknown}
	}
	return snap, nil
}

// List 返回所有服务状态（必要时刷新）。
func (m *Manager) List(ctx context.Context) ([]Snapshot, error) {
	m.mu.RLock()
	specs := make([]ServiceSpec, 0, len(m.specs))
	for _, spec := range m.specs {
		specs = append(specs, spec)
	}
	m.mu.RUnlock()

	snaps := make([]Snapshot, 0, len(specs))
	for _, spec := range specs {
		snap, err := m.Status(ctx, spec.Name)
		if err != nil {
			return nil, err
		}
		snaps = append(snaps, snap)
	}
	return snaps, nil
}

func (m *Manager) execSystemctl(ctx context.Context, action string, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", action, unit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (m *Manager) recordState(name string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(name)
	spec := m.specs[key]
	m.states[key] = Snapshot{
		Name:        spec.Name,
		Unit:        spec.Unit,
		Status:      status,
		LastUpdated: time.Now(),
	}
}

func (m *Manager) snapshot(name string) Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := strings.ToLower(name)
	if snap, ok := m.states[key]; ok {
		return snap
	}
	if spec, ok := m.specs[key]; ok {
		return Snapshot{Name: spec.Name, Unit: spec.Unit, Status: StatusUnknown}
	}
	return Snapshot{Name: name, Status: StatusUnknown}
}

func (m *Manager) binaryReady(spec ServiceSpec) bool {
	if len(spec.BinaryPaths) == 0 {
		return true
	}
	for _, candidate := range spec.BinaryPaths {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

func logService(spec ServiceSpec, level, format string, args ...any) {
	prefix := "[service]"
	if strings.EqualFold(spec.Name, "mosdns") {
		prefix = "[mosdns]"
	}
	msg := fmt.Sprintf("%s %s", prefix, fmt.Sprintf(format, args...))
	switch level {
	case "error":
		logs.Errorf(msg)
	default:
		logs.Infof(msg)
	}
}
