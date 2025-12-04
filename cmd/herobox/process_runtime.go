package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/herozmy/herobox/internal/config"
	"github.com/herozmy/herobox/internal/service"
)

func newMosdnsHooks(store *config.Store) service.ServiceHooks {
	defaultDataDir := getenv("MOSDNS_DATA_DIR", "")
	return service.ServiceHooks{
		Start: func(ctx context.Context, spec service.ServiceSpec) error {
			binary, err := firstExistingBinary(spec.BinaryPaths)
			if err != nil {
				return err
			}
			cfg := store.GetConfigPath()
			dataDir := resolveMosdnsDataDir(defaultDataDir, cfg)
			pid, err := runCommandDetached(binary, "start", "-c", cfg, "-d", dataDir)
			if err != nil {
				return err
			}
			return store.SetMosdnsPID(pid)
		},
		Stop: func(ctx context.Context, spec service.ServiceSpec) error {
			pid := store.MosdnsPID()
			if pid <= 0 {
				return errors.New("未找到 mosdns 进程 PID")
			}
			if err := terminateProcess(pid); err != nil {
				return err
			}
			return store.SetMosdnsPID(0)
		},
		Restart: func(ctx context.Context, spec service.ServiceSpec) error {
			if pid := store.MosdnsPID(); pid > 0 {
				_ = terminateProcess(pid)
			}
			binary, err := firstExistingBinary(spec.BinaryPaths)
			if err != nil {
				return err
			}
			cfg := store.GetConfigPath()
			dataDir := resolveMosdnsDataDir(defaultDataDir, cfg)
			pid, err := runCommandDetached(binary, "start", "-c", cfg, "-d", dataDir)
			if err != nil {
				return err
			}
			return store.SetMosdnsPID(pid)
		},
		Status: func(ctx context.Context, spec service.ServiceSpec) (service.Status, error) {
			if pid := store.MosdnsPID(); pid > 0 {
				if processAlive(pid) {
					return service.StatusRunning, nil
				}
				_ = store.SetMosdnsPID(0)
			}
			ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			host := resolveMosdnsPluginHost(store)
			port := resolveMosdnsPluginPort(store)
			if isMosdnsAPIAlive(ctx, host, port) {
				return service.StatusRunning, nil
			}
			return service.StatusStopped, nil
		},
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func isMosdnsAPIAlive(ctx context.Context, host, port string) bool {
	addr := net.JoinHostPort(host, port)
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func resolveMosdnsDataDir(envDir, configPath string) string {
	if envDir != "" {
		return envDir
	}
	if configPath == "" {
		return "/etc/herobox/mosdns"
	}
	dir := filepath.Dir(configPath)
	if dir == "." {
		return "/etc/herobox/mosdns"
	}
	return dir
}

func firstExistingBinary(paths []string) (string, error) {
	for _, candidate := range paths {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到可用的 mosdns 二进制路径")
}

func runCommand(ctx context.Context, binary string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandDetached(binary string, args ...string) (int, error) {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

func terminateProcess(pid int) error {
	if pid <= 0 {
		return errors.New("无效的 PID")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}
