package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

const (
	// 配置下载地址
	//defaultConfigArchive = "https://raw.githubusercontent.com/yyysuo/firetv/refs/heads/master/mosdns.zip"
	defaultConfigArchive = "https://raw.githubusercontent.com/yyysuo/firetv/refs/heads/master/mosdnsconfigupdate/mosdns1204all.zip"
	placeholderMosdnsDir = "/cus/mosdns"
	defaultFakeIPRange   = "f2b0::/18"
	defaultDomesticDNS   = "114.114.114.114"
	defaultFakeIPNeedle  = "fc00::/18"
	defaultDnsNeedle     = "202.102.128.68"
	// defaultForwardEcsAddress 是实际使用的 ECS 转发地址（用户可自定义）。
	defaultForwardEcsAddress   = "2408:8214:213::1"
	defaultSocks5Address       = "127.0.0.1:7891"
	defaultProxyInboundAddress = "127.0.0.1:7874"
	defaultDomesticFakeDns     = "udp://127.0.0.1:7874"
	defaultListenAddress7777   = ":7777"
	defaultListenAddress8888   = ":8888"
	// defaultOverridesECS 是 config_overrides.json 顶层 ecs 字段的默认值，
	// 对应 upstream 模板中的 “ecs 2408:8888::8”，该值应保持不变。
	defaultOverridesECS     = "2408:8888::8"
	configOverridesFilename = "config_overrides.json"
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
	mosdnsBinaryPaths = binaryCandidates("MOSDNS_BIN", "/usr/local/bin/mosdns")
	if configStore.MosdnsVersion() == "" {
		refreshMosdnsVersion(configStore, mosdnsBinaryPaths)
	}
	if err := syncConfigOverrides(configStore); err != nil {
		log.Printf("初始化 config_overrides.json 失败: %v", err)
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
	configArchiveURL := getenv("MOSDNS_CONFIG_ARCHIVE", defaultConfigArchive)

	mux := http.NewServeMux()
	pluginClient := &http.Client{Timeout: 15 * time.Second}
	proxyMosdnsPlugin := func(r *http.Request, path, method, contentType string, body []byte) ([]byte, int, error) {
		baseURL := resolveMosdnsPluginBaseURL(configStore)
		url := baseURL + path
		var reader io.Reader
		if len(body) > 0 {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, url, reader)
		if err != nil {
			return nil, 0, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := pluginClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, err
		}
		if resp.StatusCode >= http.StatusBadRequest {
			msg := strings.TrimSpace(string(data))
			if msg == "" {
				msg = resp.Status
			}
			return nil, resp.StatusCode, errors.New(msg)
		}
		return data, resp.StatusCode, nil
	}

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

	mux.HandleFunc("/api/mosdns/config/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		targetDir := resolveConfigDir(configStore.GetConfigPath())
		if err := downloadAndExtractConfig(ctx, configArchiveURL, targetDir); err != nil {
			respondErr(w, err)
			return
		}
		placeholderCount, err := rewriteConfigValue(targetDir, placeholderMosdnsDir, targetDir)
		if err != nil {
			respondErr(w, err)
			return
		}
		guideSteps := []map[string]any{
			buildGuideStep("步骤1：同步配置目录", placeholderCount, placeholderMosdnsDir, targetDir),
			{
				"title":   "步骤2：自定义设置已迁移到 config_overrides.json",
				"detail":  "后续 FakeIP、DNS、SOCKS5 等自定义设置将仅通过 overrides 生效，不再直接修改 mosdns 配置文件。",
				"success": false,
			},
		}
		status := buildConfigStatus(configStore.GetConfigPath())
		status["placeholder"] = placeholderMosdnsDir
		status["replacement"] = targetDir
		status["rewritten"] = placeholderCount
		status["guideSteps"] = guideSteps
		// 下载配置仅同步基础目录等信息，自定义设置依赖 config_overrides.json，由 syncConfigOverrides 负责写入。
		if err := syncConfigOverrides(configStore); err != nil {
			log.Printf("sync config overrides failed: %v", err)
		}
		respondJSON(w, status)
	})

	mux.HandleFunc("/api/mosdns/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		logFile := resolveMosdnsLogFile(configStore)
		entries := readMosdnsLogEntries(logFile, 400)
		respondJSON(w, map[string]any{
			"entries": entries,
			"file":    logFile,
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

	mux.HandleFunc("/api/mosdns/config/file", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			file := strings.TrimSpace(r.URL.Query().Get("file"))
			if file == "" {
				respondErr(w, errors.New("缺少 file 参数"))
				return
			}
			var payload struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondErr(w, fmt.Errorf("无效的请求体: %w", err))
				return
			}
			target := resolveConfigDir(configStore.GetConfigPath())
			joined, err := safeJoin(target, file)
			if err != nil {
				respondErr(w, err)
				return
			}
			if err := os.MkdirAll(filepath.Dir(joined), 0o755); err != nil {
				respondErr(w, err)
				return
			}
			if err := os.WriteFile(joined, []byte(payload.Content), 0o644); err != nil {
				respondErr(w, err)
				return
			}
			respondJSON(w, map[string]any{"path": joined, "saved": true})
		default:
			methodNotAllowed(w)
		}
	})

	mux.HandleFunc("/api/mosdns/greylist", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			data, _, err := proxyMosdnsPlugin(r, "/plugins/greylist/show", http.MethodGet, "", nil)
			if err != nil {
				respondErr(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(data)
		case http.MethodPost:
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				respondErr(w, fmt.Errorf("读取请求失败: %w", err))
				return
			}
			data, _, err := proxyMosdnsPlugin(r, "/plugins/greylist/post", http.MethodPost, r.Header.Get("Content-Type"), payload)
			if err != nil {
				respondErr(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(data)
		default:
			methodNotAllowed(w)
		}
	})

	mux.HandleFunc("/api/mosdns/greylist/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		data, _, err := proxyMosdnsPlugin(r, "/plugins/greylist/save", http.MethodGet, "", nil)
		if err != nil {
			respondErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	})

	allowedListTags := map[string]string{
		"whitelist": "whitelist",
		"blocklist": "blocklist",
		"greylist":  "greylist",
		"ddnslist":  "ddnslist",
		"client_ip": "client_ip",
	}
	allowedSwitchTags := map[string]string{
		"switch1": "switch1",
		"switch2": "switch2",
		"switch3": "switch3",
		"switch4": "switch4",
		"switch5": "switch5",
		"switch6": "switch6",
		"switch7": "switch7",
		"switch8": "switch8",
		"switch9": "switch9",
	}

	mux.HandleFunc("/api/mosdns/lists/", func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/api/mosdns/lists/")
		if trimmed == r.URL.Path || trimmed == "" {
			http.NotFound(w, r)
			return
		}
		tag := strings.Trim(trimmed, "/")
		pluginTag, ok := allowedListTags[tag]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			data, _, err := proxyMosdnsPlugin(r, "/plugins/"+pluginTag+"/show", http.MethodGet, "", nil)
			if err != nil {
				respondErr(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(data)
		case http.MethodPost:
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				respondErr(w, fmt.Errorf("读取请求失败: %w", err))
				return
			}
			if len(payload) == 0 {
				respondErr(w, errors.New("请求体不能为空"))
				return
			}
			if _, _, err := proxyMosdnsPlugin(r, "/plugins/"+pluginTag+"/post", http.MethodPost, r.Header.Get("Content-Type"), payload); err != nil {
				respondErr(w, err)
				return
			}
			if _, _, err := proxyMosdnsPlugin(r, "/plugins/"+pluginTag+"/save", http.MethodGet, "", nil); err != nil {
				respondErr(w, err)
				return
			}
			respondJSON(w, map[string]any{"saved": true})
		default:
			methodNotAllowed(w)
		}
	})

	mux.HandleFunc("/api/mosdns/switches/", func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/api/mosdns/switches/")
		if trimmed == r.URL.Path || trimmed == "" {
			http.NotFound(w, r)
			return
		}
		name := strings.Trim(trimmed, "/")
		tag, ok := allowedSwitchTags[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switchURL := "/plugins/" + tag
		switchValuePath := switchURL + "/show"
		switchUpdatePath := switchURL + "/post"
		if r.Method == http.MethodGet {
			data, _, err := proxyMosdnsPlugin(r, switchValuePath, http.MethodGet, "", nil)
			if err != nil {
				respondErr(w, err)
				return
			}
			respondJSON(w, map[string]any{"value": strings.TrimSpace(string(data))})
			return
		}
		if r.Method == http.MethodPost {
			var payload struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondErr(w, fmt.Errorf("无效的请求体: %w", err))
				return
			}
			value := strings.TrimSpace(payload.Value)
			if value == "" {
				respondErr(w, errors.New("缺少 value"))
				return
			}
			body := []byte(fmt.Sprintf(`{"value":"%s"}`, value))
			if _, _, err := proxyMosdnsPlugin(r, switchUpdatePath, http.MethodPost, "application/json", body); err != nil {
				respondErr(w, err)
				return
			}
			if _, _, err := proxyMosdnsPlugin(r, switchURL+"/save", http.MethodGet, "", nil); err != nil {
				respondErr(w, err)
				return
			}
			respondJSON(w, map[string]any{"value": value})
			return
		}
		methodNotAllowed(w)
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
			// 根据当前 SOCKS5 设置，在 mosdns 配置文件中注释或恢复 SOCKS5 相关行。
			cfgDir := resolveConfigDir(configStore.GetConfigPath())
			if count, err := toggleSocks5References(cfgDir, resolveSocks5Enabled(configStore)); err != nil {
				log.Printf("toggle socks5 references failed (updated %d entries): %v", count, err)
			}
			if err := syncConfigOverrides(configStore); err != nil {
				log.Printf("sync overrides failed: %v", err)
			}
			respondJSON(w, map[string]any{"settings": configStore.Settings()})
		default:
			methodNotAllowed(w)
		}
	})

	staticDir := resolveStaticDir()
	log.Printf("静态资源目录: %s", staticDir)
	mux.Handle("/", spaFileServer(staticDir))

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

func spaFileServer(root string) http.Handler {
	fs := http.Dir(root)
	fileServer := http.FileServer(fs)
	indexPath := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		if fileExists(fs, r.URL.Path) {
			fileServer.ServeHTTP(w, r)
			return
		}

		if shouldFallbackToSPA(r.URL.Path) {
			http.ServeFile(w, r, indexPath)
			return
		}

		http.NotFound(w, r)
	})
}

func fileExists(fs http.FileSystem, path string) bool {
	f, err := fs.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Stat()
	return err == nil
}

func shouldFallbackToSPA(p string) bool {
	if p == "" || p == "/" {
		return true
	}
	base := filepath.Base(p)
	if base == "." || base == "" {
		return true
	}
	return !strings.Contains(base, ".")
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

func downloadAndExtractConfig(ctx context.Context, url, targetDir string) error {
	if url == "" {
		return fmt.Errorf("未配置 mosdns 配置下载地址")
	}
	if targetDir == "" {
		targetDir = "."
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("配置下载失败：%s", resp.Status)
	}
	tempFile, err := os.CreateTemp("", "mosdns-config-*.zip")
	if err != nil {
		return err
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()
	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return extractConfigZip(tempFile.Name(), targetDir)
}

func extractConfigZip(src, targetDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		rel := filepath.Clean(name)
		if rel == "." {
			continue
		}
		destination, err := safeJoin(targetDir, rel)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		srcFile, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			srcFile.Close()
			return err
		}
		if _, err := io.Copy(out, srcFile); err != nil {
			out.Close()
			srcFile.Close()
			return err
		}
		out.Close()
		srcFile.Close()
	}
	return nil
}

func rewriteConfigValue(baseDir, needle, replacement string) (int, error) {
	if baseDir == "" || needle == "" || replacement == "" {
		return 0, nil
	}
	needle = strings.TrimSpace(needle)
	replacement = strings.TrimSpace(replacement)
	if needle == replacement {
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
		// 避免直接改写 mosdns 的 overrides JSON，由 syncConfigOverrides 统一管理。
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
		content := string(data)
		if !strings.Contains(content, needle) {
			return nil
		}
		updated := strings.ReplaceAll(content, needle, replacement)
		if updated == content {
			return nil
		}
		mode := os.FileMode(0o644)
		if info, err := d.Info(); err == nil {
			mode = info.Mode()
		}
		if err := os.WriteFile(path, []byte(updated), mode); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}
