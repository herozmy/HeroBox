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

var mosdnsBinaryPaths []string

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
	mosdnsBinaryPaths = binaryCandidates("MOSDNS_BIN", "/usr/local/bin/mosdns")
	if configStore.MosdnsVersion() == "" {
		refreshMosdnsVersion(configStore, mosdnsBinaryPaths)
	}

	svcManager := service.NewManager([]service.ServiceSpec{
		{
			Name:        "mosdns",
			Unit:        getenv("MOSDNS_UNIT", "mosdns.service"),
			BinaryPaths: mosdnsBinaryPaths,
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
		updateMosdnsState(configStore, mosdnsBinaryPaths, snaps...)
		for i := range snaps {
			applyMosdnsVersion(configStore, &snaps[i])
		}
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
		refreshMosdnsVersion(configStore, mosdnsBinaryPaths)
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

	mux.HandleFunc("/api/mosdns/config/content", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		path := configStore.GetConfigPath()
		files, dir, err := collectMosdnsFiles(path)
		if err != nil {
			respondErr(w, err)
			return
		}
		respondJSON(w, map[string]any{
			"path": path,
			"dir":  dir,
			"tree": files,
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
			updateMosdnsState(store, mosdnsBinaryPaths, snap)
			applyMosdnsVersion(store, &snap)
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
			updateMosdnsState(store, mosdnsBinaryPaths, snap)
			applyMosdnsVersion(store, &snap)
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

func updateMosdnsState(store *config.Store, binPaths []string, snaps ...service.Snapshot) {
	if store == nil {
		return
	}
	for _, snap := range snaps {
		if !strings.EqualFold(snap.Name, "mosdns") {
			continue
		}
		_ = store.SetMosdnsStatus(string(snap.Status))
		if snap.Status == service.StatusMissing {
			continue
		}
		refreshMosdnsVersion(store, binPaths)
	}
}

func applyMosdnsVersion(store *config.Store, snap *service.Snapshot) {
	if store == nil || snap == nil {
		return
	}
	if !strings.EqualFold(snap.Name, "mosdns") {
		return
	}
	snap.Version = store.MosdnsVersion()
}

func refreshMosdnsVersion(store *config.Store, binPaths []string) {
	if store == nil || len(binPaths) == 0 {
		return
	}
	version, err := detectMosdnsVersion(binPaths)
	if err != nil {
		log.Printf("检测 mosdns 版本失败: %v", err)
		return
	}
	if version == "" {
		return
	}
	if err := store.SetMosdnsVersion(version); err != nil {
		log.Printf("记录 mosdns 版本失败: %v", err)
	}
}

func detectMosdnsVersion(binPaths []string) (string, error) {
	if len(binPaths) == 0 {
		return "", fmt.Errorf("未配置 mosdns binary 路径")
	}
	binary, err := firstExistingBinary(binPaths)
	if err != nil {
		return "", err
	}
	version, runErr := runMosdnsVersionCommand(binary, "version")
	if runErr != nil {
		version, runErr = runMosdnsVersionCommand(binary, "--version")
	}
	if runErr != nil {
		return "", runErr
	}
	return version, nil
}

func runMosdnsVersionCommand(binary, arg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, arg)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	version := normalizeMosdnsVersion(string(output))
	if version == "" {
		return "", fmt.Errorf("mosdns %s 输出为空", arg)
	}
	return version, nil
}

func normalizeMosdnsVersion(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	line := trimmed
	if idx := strings.IndexRune(line, '\n'); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	for i := len(fields) - 1; i >= 0; i-- {
		token := strings.Trim(fields[i], ":")
		if token == "" {
			continue
		}
		if strings.ContainsAny(token, "0123456789") {
			return token
		}
	}
	if line != "" {
		return line
	}
	return trimmed
}

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
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".txt")
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
