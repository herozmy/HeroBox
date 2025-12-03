package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/herozmy/herobox/internal/config"
	"github.com/herozmy/herobox/internal/logs"
	"github.com/herozmy/herobox/internal/mosdns"
	"github.com/herozmy/herobox/internal/service"
	"gopkg.in/yaml.v3"
)

var mosdnsBinaryPaths []string

const (
	defaultConfigArchive       = "https://github.com/herozmy/StoreHouse/raw/refs/heads/latest/config/mosdns/jph/mosdns.zip"
	placeholderMosdnsDir       = "/cus/mosdns"
	defaultFakeIPRange         = "f2b0::/18"
	defaultDomesticDNS         = "114.114.114.114"
	defaultFakeIPNeedle        = "fc00::/18"
	defaultDnsNeedle           = "202.102.128.68"
	defaultForwardEcsAddress   = "2408:8214:213::1"
	defaultSocks5Address       = "127.0.0.1:7891"
	defaultProxyInboundAddress = "127.0.0.1:7874"
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
		fakeIPRange := resolveFakeIPRange(configStore)
		domesticDNS := resolveDomesticDNS(configStore)
		forwardEcsAddress := resolveForwardEcsAddress(configStore)
		proxyInboundAddress := resolveProxyInboundAddress(configStore)
		socks5Enabled := resolveSocks5Enabled(configStore)
		socks5CustomAddr := strings.TrimSpace(resolveSocks5Address(configStore))
		socks5EffectiveAddr := socks5CustomAddr
		if socks5EffectiveAddr == "" {
			socks5EffectiveAddr = defaultSocks5Address
		}
		fakeIPNeedle := resolveSetting(configStore, "fakeIpRangeCurrent", defaultFakeIPNeedle)
		dnsNeedle := resolveSetting(configStore, "domesticDnsCurrent", defaultDnsNeedle)
		forwardEcsNeedle := resolveSetting(configStore, "forwardEcsAddressCurrent", defaultForwardEcsAddress)
		proxyAddrNeedle := resolveSetting(configStore, "proxyInboundAddressCurrent", defaultProxyInboundAddress)
		socksAddrNeedle := resolveSetting(configStore, "socks5AddressCurrent", defaultSocks5Address)
		fakeIPCount, err := rewriteWithFallback(targetDir, fakeIPNeedle, defaultFakeIPNeedle, fakeIPRange)
		if err != nil {
			respondErr(w, err)
			return
		}
		dnsCount, err := rewriteWithFallback(targetDir, dnsNeedle, defaultDnsNeedle, domesticDNS)
		if err != nil {
			respondErr(w, err)
			return
		}
		forwardEcsCount, err := rewriteWithFallback(targetDir, forwardEcsNeedle, defaultForwardEcsAddress, forwardEcsAddress)
		if err != nil {
			respondErr(w, err)
			return
		}
		proxyAddrCount, err := rewriteWithFallback(targetDir, proxyAddrNeedle, defaultProxyInboundAddress, proxyInboundAddress)
		if err != nil {
			respondErr(w, err)
			return
		}
		socksAddrCount, err := rewriteWithFallback(targetDir, socksAddrNeedle, defaultSocks5Address, socks5EffectiveAddr)
		if err != nil {
			respondErr(w, err)
			return
		}
		socks5Count, err := toggleSocks5References(targetDir, socks5Enabled)
		if err != nil {
			respondErr(w, err)
			return
		}
		guideSteps := []map[string]any{
			buildGuideStep("步骤1：同步配置目录", placeholderCount, placeholderMosdnsDir, targetDir),
			buildGuideStep("步骤2：更新 FakeIP IPv6 段", fakeIPCount, fakeIPNeedle, fakeIPRange),
			buildGuideStep("步骤3：更新国内 DNS", dnsCount, dnsNeedle, domesticDNS),
			buildGuideStep("步骤4：更新 forward_nocn_ecs 地址", forwardEcsCount, forwardEcsNeedle, forwardEcsAddress),
			buildGuideStep("步骤5：更新 Proxy 入站地址", proxyAddrCount, proxyAddrNeedle, proxyInboundAddress),
			buildGuideStep("步骤6：更新 SOCKS5 地址", socksAddrCount, socksAddrNeedle, socks5EffectiveAddr),
			buildSocks5GuideStep(socks5Count, socks5Enabled),
			{
				"title":   "步骤8：高级向导",
				"detail":  "功能开发中，敬请期待",
				"success": false,
			},
		}
		status := buildConfigStatus(configStore.GetConfigPath())
		status["placeholder"] = placeholderMosdnsDir
		status["replacement"] = targetDir
		status["rewritten"] = placeholderCount
		status["fakeIpRange"] = fakeIPRange
		status["domesticDns"] = domesticDNS
		status["forwardEcsAddress"] = forwardEcsAddress
		status["proxyInboundAddress"] = proxyInboundAddress
		status["socks5Enabled"] = socks5Enabled
		status["socks5Address"] = socks5CustomAddr
		status["guideSteps"] = guideSteps
		if err := configStore.UpdateSettings(map[string]string{
			"fakeIpRangeCurrent":         fakeIPRange,
			"domesticDnsCurrent":         domesticDNS,
			"forwardEcsAddressCurrent":   forwardEcsAddress,
			"proxyInboundAddressCurrent": proxyInboundAddress,
			"socks5AddressCurrent":       socks5EffectiveAddr,
		}); err != nil {
			log.Printf("update settings failed: %v", err)
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
			if err := applyPreferenceSettings(configStore); err != nil {
				log.Printf("apply settings failed: %v", err)
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

func buildGuideStep(title string, count int, from, to string) map[string]any {
	success := count > 0
	detail := fmt.Sprintf("未在配置中检测到 %s", from)
	if success {
		detail = fmt.Sprintf("已在 %d 个文件中将 %s 替换为 %s", count, from, to)
	}
	return map[string]any{
		"title":   title,
		"detail":  detail,
		"success": success,
	}
}

func buildSocks5GuideStep(count int, enabled bool) map[string]any {
	var detail string
	success := true
	if enabled {
		if count > 0 {
			detail = fmt.Sprintf("已启用 %d 处 SOCKS5 配置", count)
		} else {
			detail = "未找到可启用的 SOCKS5 配置"
			success = false
		}
	} else {
		if count > 0 {
			detail = fmt.Sprintf("已注释 %d 处 SOCKS5 配置", count)
		} else {
			detail = "未检测到需要注释的 SOCKS5 配置"
		}
	}
	return map[string]any{
		"title":   "步骤4：SOCKS5 代理",
		"detail":  detail,
		"success": success,
	}
}

func resolveFakeIPRange(store *config.Store) string {
	return resolveSetting(store, "fakeIpRange", defaultFakeIPRange)
}

func resolveDomesticDNS(store *config.Store) string {
	return resolveSetting(store, "domesticDns", defaultDomesticDNS)
}

func resolveForwardEcsAddress(store *config.Store) string {
	return resolveSetting(store, "forwardEcsAddress", defaultForwardEcsAddress)
}

func resolveProxyInboundAddress(store *config.Store) string {
	return resolveSetting(store, "proxyInboundAddress", defaultProxyInboundAddress)
}

func resolveSocks5Enabled(store *config.Store) bool {
	addr := strings.TrimSpace(resolveSocks5Address(store))
	return addr != ""
}

func resolveSocks5Address(store *config.Store) string {
	if store == nil {
		return defaultSocks5Address
	}
	settings := store.Settings()
	if val, ok := settings["socks5Address"]; ok {
		return strings.TrimSpace(val)
	}
	return defaultSocks5Address
}

func applyPreferenceSettings(store *config.Store) error {
	if store == nil {
		return nil
	}
	dir := resolveConfigDir(store.GetConfigPath())
	if dir == "" {
		return nil
	}
	fakeNeedle := resolveSetting(store, "fakeIpRangeCurrent", defaultFakeIPNeedle)
	fakeTarget := resolveFakeIPRange(store)
	if _, err := rewriteWithFallback(dir, fakeNeedle, defaultFakeIPNeedle, fakeTarget); err != nil {
		return err
	}
	dnsNeedle := resolveSetting(store, "domesticDnsCurrent", defaultDnsNeedle)
	dnsTarget := resolveDomesticDNS(store)
	if _, err := rewriteWithFallback(dir, dnsNeedle, defaultDnsNeedle, dnsTarget); err != nil {
		return err
	}
	forwardNeedle := resolveSetting(store, "forwardEcsAddressCurrent", defaultForwardEcsAddress)
	forwardTarget := resolveForwardEcsAddress(store)
	if _, err := rewriteWithFallback(dir, forwardNeedle, defaultForwardEcsAddress, forwardTarget); err != nil {
		return err
	}
	proxyNeedle := resolveSetting(store, "proxyInboundAddressCurrent", defaultProxyInboundAddress)
	proxyTarget := resolveProxyInboundAddress(store)
	if _, err := rewriteWithFallback(dir, proxyNeedle, defaultProxyInboundAddress, proxyTarget); err != nil {
		return err
	}
	socksNeedle := resolveSetting(store, "socks5AddressCurrent", defaultSocks5Address)
	socksCustom := strings.TrimSpace(resolveSocks5Address(store))
	socksEffective := socksCustom
	if socksEffective == "" {
		socksEffective = defaultSocks5Address
	}
	if _, err := rewriteWithFallback(dir, socksNeedle, defaultSocks5Address, socksEffective); err != nil {
		return err
	}
	if _, err := toggleSocks5References(dir, resolveSocks5Enabled(store)); err != nil {
		return err
	}
	_ = store.UpdateSettings(map[string]string{
		"fakeIpRangeCurrent":         fakeTarget,
		"domesticDnsCurrent":         dnsTarget,
		"forwardEcsAddressCurrent":   forwardTarget,
		"proxyInboundAddressCurrent": proxyTarget,
		"socks5AddressCurrent":       socksEffective,
	})
	return nil
}

func rewriteWithFallback(dir, primaryNeedle, fallbackNeedle, target string) (int, error) {
	count, err := rewriteConfigValue(dir, primaryNeedle, target)
	if err != nil {
		return 0, err
	}
	if count == 0 && fallbackNeedle != "" && fallbackNeedle != primaryNeedle {
		return rewriteConfigValue(dir, fallbackNeedle, target)
	}
	return count, nil
}

func resolveSetting(store *config.Store, key, fallback string) string {
	if store == nil {
		return fallback
	}
	settings := store.Settings()
	if val, ok := settings[key]; ok {
		if trimmed := strings.TrimSpace(val); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func resolveMosdnsPluginBaseURL(store *config.Store) string {
	base := effectiveMosdnsPluginBase(store)
	if base == "" {
		host := resolveMosdnsPluginHost(store)
		if host == "" {
			host = "127.0.0.1"
		}
		port := resolveMosdnsPluginPort(store)
		if port == "" {
			port = "9099"
		}
		base = "http://" + net.JoinHostPort(host, port)
	}
	return strings.TrimRight(base, "/")
}

func effectiveMosdnsPluginBase(store *config.Store) string {
	if base := normalizePluginBaseURL(getenv("MOSDNS_PLUGIN_BASE", "")); base != "" {
		return base
	}
	if store != nil {
		if base := normalizePluginBaseURL(resolveSetting(store, "mosdnsPluginBase", "")); base != "" {
			return base
		}
	}
	return ""
}

func resolveMosdnsPluginPort(store *config.Store) string {
	if port := strings.TrimSpace(getenv("MOSDNS_PLUGIN_PORT", "")); port != "" {
		return port
	}
	if base := getenv("MOSDNS_PLUGIN_BASE", ""); base != "" {
		if _, p := pluginBaseHostPort(base); p != "" {
			return p
		}
	}
	if store != nil {
		if base := resolveSetting(store, "mosdnsPluginBase", ""); base != "" {
			if _, p := pluginBaseHostPort(base); p != "" {
				return p
			}
		}
		if port := strings.TrimSpace(resolveSetting(store, "mosdnsPluginPort", "")); port != "" {
			return port
		}
	}
	return "9099"
}

func resolveMosdnsPluginHost(store *config.Store) string {
	if host := strings.TrimSpace(getenv("MOSDNS_STATUS_HOST", "")); host != "" {
		return host
	}
	if base := getenv("MOSDNS_PLUGIN_BASE", ""); base != "" {
		if h, _ := pluginBaseHostPort(base); h != "" {
			return h
		}
	}
	if store != nil {
		if base := resolveSetting(store, "mosdnsPluginBase", ""); base != "" {
			if h, _ := pluginBaseHostPort(base); h != "" {
				return h
			}
		}
		if host := strings.TrimSpace(resolveSetting(store, "mosdnsPluginHost", "")); host != "" {
			return host
		}
	}
	return "127.0.0.1"
}

func normalizePluginBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}

func pluginBaseHostPort(raw string) (string, string) {
	normalized := normalizePluginBaseURL(raw)
	if normalized == "" {
		return "", ""
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", ""
	}
	return u.Hostname(), u.Port()
}

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

func resolveBoolSetting(store *config.Store, key string, fallback bool) bool {
	if store == nil {
		return fallback
	}
	settings := store.Settings()
	val, ok := settings[key]
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func toggleSocks5References(baseDir string, enable bool) (int, error) {
	if baseDir == "" {
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
		if !isAllowedConfigFile(d.Name()) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i, line := range lines {
			updated, toggled := adjustSocks5Line(line, enable)
			if toggled {
				lines[i] = updated
				count++
				changed = true
			}
		}
		if changed {
			mode := os.FileMode(0o644)
			if info, err := d.Info(); err == nil {
				mode = info.Mode()
			}
			content := strings.Join(lines, "\n")
			if len(lines) > 0 && strings.HasSuffix(string(data), "\n") {
				content += "\n"
			}
			if err := os.WriteFile(path, []byte(content), mode); err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}

func adjustSocks5Line(line string, enable bool) (string, bool) {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "socks5") {
		return line, false
	}
	idx := firstNonSpaceIndex(line)
	if idx == -1 {
		return line, false
	}
	prefix := line[:idx]
	body := line[idx:]
	trimmed := strings.TrimLeft(body, " \t")
	if trimmed == "" {
		return line, false
	}
	trimmedLower := strings.ToLower(trimmed)
	if !strings.Contains(trimmedLower, "socks5") {
		return line, false
	}
	if enable {
		if strings.HasPrefix(trimmed, "#") {
			un := strings.TrimLeft(trimmed[1:], " \t")
			return prefix + un, true
		}
		return line, false
	}
	if strings.HasPrefix(trimmed, "#") {
		return line, false
	}
	return prefix + "# " + body, true
}

func firstNonSpaceIndex(s string) int {
	for i, r := range s {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return -1
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
	return strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") ||
		strings.HasSuffix(lower, ".txt") ||
		strings.HasSuffix(lower, ".conf") ||
		strings.HasSuffix(lower, ".cfg") ||
		strings.HasSuffix(lower, ".json")
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
