package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/herozmy/herobox/internal/config"
)

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

func resolveDomesticFakeDnsAddress(store *config.Store) string {
	return resolveSetting(store, "domesticFakeDnsAddress", defaultDomesticFakeDns)
}

func resolveListenAddress7777(store *config.Store) string {
	return resolveSetting(store, "listenAddress7777", defaultListenAddress7777)
}

func resolveListenAddress8888(store *config.Store) string {
	return resolveSetting(store, "listenAddress8888", defaultListenAddress8888)
}

func resolveAliyunDohEcsIP(store *config.Store) string {
	return resolveSetting(store, "aliyunDohEcsIp", "")
}

func resolveAliyunDohID(store *config.Store) string {
	return resolveSetting(store, "aliyunDohId", "")
}

func resolveAliyunDohKeyID(store *config.Store) string {
	return resolveSetting(store, "aliyunDohKeyId", "")
}

func resolveAliyunDohKeySecret(store *config.Store) string {
	return resolveSetting(store, "aliyunDohKeySecret", "")
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
