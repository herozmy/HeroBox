package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/herozmy/herobox/internal/config"
	"github.com/herozmy/herobox/internal/logs"
	"github.com/herozmy/herobox/internal/mosdns"
	"github.com/herozmy/herobox/internal/service"
)

func main() {
	addr := getenv("HEROBOX_ADDR", ":8080")
	configStore, err := config.NewStore(
		getenv("MOSDNS_CONFIG_PATH", "/etc/herobox/mosdns/config.yaml"),
		getenv("HEROBOX_CONFIG_FILE", defaultConfigFile()),
	)
	if err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}
	if err := configStore.SetHeroboxPort(addr); err != nil {
		log.Printf("记录端口失败: %v", err)
	}
	logBuffer := logs.NewBuffer(500)
	logs.SetBuffer(logBuffer)

	mosdnsHooks := newMosdnsHooks(configStore)

	svcManager := service.NewManager([]service.ServiceSpec{
		{
			Name:        "mosdns",
			Unit:        getenv("MOSDNS_UNIT", "mosdns.service"),
			BinaryPaths: binaryCandidates("MOSDNS_BIN", "/usr/local/bin/mosdns"),
			Hooks:       mosdnsHooks,
		},
		{
			Name:        "sing-box",
			Unit:        getenv("SING_BOX_UNIT", "sing-box.service"),
			BinaryPaths: binaryCandidates("SING_BOX_BIN", "/usr/local/bin/sing-box"),
		},
		{
			Name:        "mihomo",
			Unit:        getenv("MIHOMO_UNIT", "mihomo.service"),
			BinaryPaths: binaryCandidates("MIHOMO_BIN", "/usr/local/bin/mihomo"),
		},
	})
	updater := mosdns.DefaultUpdater()
	if updater.InstallDir == "" {
		updater.InstallDir = filepath.Join(".", "bin")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/services", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		snaps, err := svcManager.List(ctx)
		if err != nil {
			respondErr(w, err)
			return
		}
		updateMosdnsState(configStore, snaps...)
		respondJSON(w, snaps)
	})
	mux.Handle("/api/services/", http.StripPrefix("/api/services", serviceHandler(svcManager, configStore)))

	mux.HandleFunc("/api/mosdns/kernel/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		rel, err := updater.Client.LatestRelease(ctx)
		if err != nil {
			respondErr(w, err)
			return
		}
		respondJSON(w, rel)
	})

	mux.HandleFunc("/api/mosdns/kernel/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		rel, path, err := updater.UpdateLatest(ctx)
		if err != nil {
			respondErr(w, err)
			return
		}
		respondJSON(w, map[string]any{
			"release": rel,
			"binary":  path,
		})
	})

	mux.HandleFunc("/api/mosdns/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			status := buildConfigStatus(configStore.GetConfigPath())
			respondJSON(w, status)
		case http.MethodPut:
			var payload struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondErr(w, fmt.Errorf("无效的请求体: %w", err))
				return
			}
			path := strings.TrimSpace(payload.Path)
			if err := configStore.SetConfigPath(path); err != nil {
				respondErr(w, err)
				return
			}
			respondJSON(w, buildConfigStatus(configStore.GetConfigPath()))
		default:
			methodNotAllowed(w)
		}
	})

	mux.HandleFunc("/api/mosdns/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		respondJSON(w, map[string]any{
			"entries": logs.BufferEntries(),
		})
	})

	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			respondJSON(w, map[string]any{"settings": configStore.Settings()})
		case http.MethodPut:
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondErr(w, fmt.Errorf("无效的请求体: %w", err))
				return
			}
			if err := configStore.UpdateSettings(payload); err != nil {
				respondErr(w, err)
				return
			}
			respondJSON(w, map[string]any{"settings": configStore.Settings()})
		default:
			methodNotAllowed(w)
		}
	})

	staticDir := resolveStaticDir()
	log.Printf("静态资源目录: %s", staticDir)
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	srv := &http.Server{
		Addr:    addr,
		Handler: cors(mux),
	}

	go func() {
		log.Printf("herobox 后端启动，监听 %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func serviceHandler(mgr *service.Manager, store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		if path == "" {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(path, "/")
		name := parts[0]
		switch r.Method {
		case http.MethodGet:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			snap, err := mgr.Status(ctx, name)
			if err != nil {
				respondErr(w, err)
				return
			}
			updateMosdnsState(store, snap)
			respondJSON(w, snap)
		case http.MethodPost:
			if len(parts) < 2 {
				respondErr(w, errors.New("缺少操作动作，如 start/stop"))
				return
			}
			action := parts[1]
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			var err error
			switch action {
			case "start":
				err = mgr.Start(ctx, name)
			case "stop":
				err = mgr.Stop(ctx, name)
			case "restart":
				err = mgr.Restart(ctx, name)
			default:
				respondErr(w, fmt.Errorf("不支持的操作 %s", action))
				return
			}
			if err != nil {
				respondErr(w, err)
				return
			}
			snap, err := mgr.Status(ctx, name)
			if err != nil {
				respondErr(w, err)
				return
			}
			updateMosdnsState(store, snap)
			respondJSON(w, snap)
		default:
			methodNotAllowed(w)
		}
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func respondJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json failed: %v", err)
	}
}

func respondErr(w http.ResponseWriter, err error) {
	log.Printf("api error: %v", err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func resolveBinDir() string {
	if env := os.Getenv("HEROBOX_BIN_DIR"); env != "" {
		if info, err := os.Stat(env); err == nil && info.IsDir() {
			return env
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return filepath.Join(".", "bin")
}

func binaryCandidates(envKey string, fallbacks ...string) []string {
	var candidates []string
	if val := os.Getenv(envKey); val != "" {
		candidates = append(candidates, val)
	}
	candidates = append(candidates, fallbacks...)
	uniq := make([]string, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		uniq = append(uniq, c)
	}
	return uniq
}

func resolveStaticDir() string {
	var candidates []string
	if env := os.Getenv("HEROBOX_STATIC_DIR"); env != "" {
		candidates = append(candidates, env)
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "dist"))
	}

	candidates = append(candidates,
		filepath.Join(".", "bin", "dist"),
		filepath.Join(".", "frontend"),
	)

	seen := make(map[string]struct{})
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return "."
}

func buildConfigStatus(path string) map[string]any {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{
				"path":   path,
				"exists": false,
			}
		}
		return map[string]any{
			"path":   path,
			"exists": false,
			"error":  err.Error(),
		}
	}
	return map[string]any{
		"path":    path,
		"exists":  true,
		"size":    info.Size(),
		"modTime": info.ModTime(),
	}
}

func defaultConfigFile() string {
	if env := os.Getenv("HEROBOX_CONFIG_FILE"); env != "" {
		return env
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, "herobox.yaml")
	}
	return "herobox.yaml"
}

func updateMosdnsState(store *config.Store, snaps ...service.Snapshot) {
	if store == nil {
		return
	}
	for _, snap := range snaps {
		if !strings.EqualFold(snap.Name, "mosdns") {
			continue
		}
		_ = store.SetMosdnsStatus(string(snap.Status))
	}
}


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
			return runCommandDetached(binary, "start", "-c", cfg, "-d", dataDir)
		},
		Stop: func(ctx context.Context, spec service.ServiceSpec) error {
			binary, err := firstExistingBinary(spec.BinaryPaths)
			if err != nil {
				return err
			}
			return runCommand(ctx, binary, "stop")
		},
		Restart: func(ctx context.Context, spec service.ServiceSpec) error {
			binary, err := firstExistingBinary(spec.BinaryPaths)
			if err != nil {
				return err
			}
			cfg := store.GetConfigPath()
			dataDir := resolveMosdnsDataDir(defaultDataDir, cfg)
			return runCommandDetached(binary, "restart", "-c", cfg, "-d", dataDir)
		},
	}
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

func runCommandDetached(binary string, args ...string) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
