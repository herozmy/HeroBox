package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/herozmy/herobox/internal/config"
)

var defaultOverrideReplacements = []config.OverrideReplacement{
	{Original: "udp://127.0.0.1:7874", Comment: "sing-box fakedns"},
	{Original: "udp://127.0.0.1:1053", Comment: "mihomo fakedns"},
	{Original: "127.0.0.1:7891", Comment: "sing-box socks5"},
	{Original: "114.114.114.114", Comment: "运营商dns"},
	{Original: "ecs 2408:8888::8", Comment: "谷歌dns ecs ip"},
	{Original: "123.123.110.123", Comment: "阿里私有doh ecs ip"},
	{Original: "888888", Comment: "阿里私有doh id"},
	{Original: "888888_88888", Comment: "阿里私有doh key id"},
	{Original: "999999999", Comment: "阿里私有doh key secret"},
	{Original: "127.0.0.1:7777", Comment: "取消仅限制监听 7777"},
	{Original: "127.0.0.1:8888", Comment: "取消仅限制监听 8888"},
}

func defaultConfigOverridesDocument() config.Overrides {
	doc := config.Overrides{
		// 顶层 socks5、ecs 字段保持默认模板值，不随 UI 设置改变。
		Socks5: defaultSocks5Address,
		ECS:    defaultOverridesECS,
	}
	if len(defaultOverrideReplacements) > 0 {
		doc.Replacements = make([]config.OverrideReplacement, len(defaultOverrideReplacements))
		copy(doc.Replacements, defaultOverrideReplacements)
	}
	return doc
}

func syncConfigOverrides(store *config.Store) error {
	if store == nil {
		return nil
	}
	dir := resolveConfigDir(store.GetConfigPath())
	if dir == "" {
		dir = "."
	}
	if info, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%s 不是有效配置目录", dir)
	}
	path := filepath.Join(dir, configOverridesFilename)
	doc := defaultConfigOverridesDocument()
	if loaded, err := loadConfigOverrides(path); err == nil {
		doc = loaded
	} else if !os.IsNotExist(err) {
		return err
	}
	ensureOverrideDefaults(&doc)

	// 计算当前 forward ECS 地址。默认使用 Google ECS (2408:8888::8)，
	// 并让顶层 ecs 字段与替换规则中的 new 保持一致。
	forwardAddr := strings.TrimSpace(resolveForwardEcsAddress(store))
	if forwardAddr == "" {
		forwardAddr = defaultOverridesECS
	}
	doc.ECS = forwardAddr

	socksAddr := strings.TrimSpace(resolveSocks5Address(store))
	if !resolveSocks5Enabled(store) {
		socksAddr = ""
	}
	// 顶层 socks5 字段用于 mosdns 识别原始 SOCKS5 地址，这里仍然反映当前设置。
	doc.Socks5 = socksAddr

	proxyAddr := prefixIfMissing(strings.TrimSpace(resolveProxyInboundAddress(store)), "udp://")
	domesticDNS := resolveDomesticDNS(store)
	domesticFakeDns := resolveDomesticFakeDnsAddress(store)
	listen7777 := resolveListenAddress7777(store)
	listen8888 := resolveListenAddress8888(store)
	aliyunEcs := resolveAliyunDohEcsIP(store)
	aliyunId := resolveAliyunDohID(store)
	aliyunKeyId := resolveAliyunDohKeyID(store)
	aliyunKeySecret := resolveAliyunDohKeySecret(store)
	var ecsReplacement string
	if forwardAddr != "" {
		ecsReplacement = fmt.Sprintf("ecs %s", forwardAddr)
	}
	setOverrideReplacement(&doc, "udp://127.0.0.1:7874", preferOverrideValue(proxyAddr, doc, "udp://127.0.0.1:7874"))
	setOverrideReplacement(&doc, "udp://127.0.0.1:1053", preferOverrideValue(domesticFakeDns, doc, "udp://127.0.0.1:1053"))
	setOverrideReplacement(&doc, "127.0.0.1:7891", preferOverrideValue(socksAddr, doc, "127.0.0.1:7891"))
	setOverrideReplacement(&doc, "114.114.114.114", preferOverrideValue(domesticDNS, doc, "114.114.114.114"))
	setOverrideReplacement(&doc, "ecs 2408:8888::8", preferOverrideValue(ecsReplacement, doc, "ecs 2408:8888::8"))
	setOverrideReplacement(&doc, "123.123.110.123", preferOverrideValue(aliyunEcs, doc, "123.123.110.123"))
	setOverrideReplacement(&doc, "888888", preferOverrideValue(aliyunId, doc, "888888"))
	setOverrideReplacement(&doc, "888888_88888", preferOverrideValue(aliyunKeyId, doc, "888888_88888"))
	setOverrideReplacement(&doc, "999999999", preferOverrideValue(aliyunKeySecret, doc, "999999999"))
	setOverrideReplacement(&doc, "127.0.0.1:7777", preferOverrideValue(listen7777, doc, "127.0.0.1:7777"))
	setOverrideReplacement(&doc, "127.0.0.1:8888", preferOverrideValue(listen8888, doc, "127.0.0.1:8888"))
	if err := writeConfigOverrides(path, doc); err != nil {
		return err
	}
	if err := store.SetConfigOverrides(doc); err != nil {
		log.Printf("记录 config_overrides 到 herobox.yaml 失败: %v", err)
	}
	return nil
}

func loadConfigOverrides(path string) (config.Overrides, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.Overrides{}, err
	}
	data = stripJSONComments(data)
	var doc config.Overrides
	if err := json.Unmarshal(data, &doc); err != nil {
		return config.Overrides{}, err
	}
	return doc, nil
}

func writeConfigOverrides(path string, doc config.Overrides) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func setOverrideReplacement(doc *config.Overrides, original, value string) {
	if doc == nil || original == "" {
		return
	}
	for i, rep := range doc.Replacements {
		if strings.TrimSpace(rep.Original) == original {
			doc.Replacements[i].New = strings.TrimSpace(value)
			return
		}
	}
	doc.Replacements = append(doc.Replacements, config.OverrideReplacement{
		Original: original,
		New:      strings.TrimSpace(value),
	})
}

func lookupOverrideValue(source config.Overrides, original string) string {
	key := strings.TrimSpace(original)
	if key == "" {
		return ""
	}
	for _, rep := range source.Replacements {
		if strings.TrimSpace(rep.Original) == key {
			return strings.TrimSpace(rep.New)
		}
	}
	return ""
}

func preferOverrideValue(primary string, source config.Overrides, original string) string {
	if trimmed := strings.TrimSpace(primary); trimmed != "" {
		return trimmed
	}
	// 若 primary 为空，则优先使用已有 overrides 中的值；
	// 如果也没有，则保持为空字符串，让 mosdns 使用 original 作为默认值。
	if existing := strings.TrimSpace(lookupOverrideValue(source, original)); existing != "" {
		return existing
	}
	return ""
}

func stripJSONComments(data []byte) []byte {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var buf bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		// 跳过以 # 开头的注释行和空行，其余保持原样写回。
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return data
	}
	return buf.Bytes()
}

func ensureOverrideDefaults(doc *config.Overrides) {
	if doc == nil {
		return
	}
	existing := make(map[string]struct{}, len(doc.Replacements))
	for _, rep := range doc.Replacements {
		key := strings.TrimSpace(rep.Original)
		if key == "" {
			continue
		}
		existing[key] = struct{}{}
	}
	for _, rep := range defaultOverrideReplacements {
		key := strings.TrimSpace(rep.Original)
		if key == "" {
			continue
		}
		if _, ok := existing[key]; ok {
			continue
		}
		doc.Replacements = append(doc.Replacements, rep)
	}
}

func prefixIfMissing(value, prefix string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, prefix) {
		return trimmed
	}
	return prefix + trimmed
}
