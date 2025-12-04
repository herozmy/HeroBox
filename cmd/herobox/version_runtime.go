package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/herozmy/herobox/internal/config"
	"github.com/herozmy/herobox/internal/service"
)

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
