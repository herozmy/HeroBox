package mosdns

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/herozmy/herobox/internal/logs"
)

const defaultRepo = "yyysuo/mosdns"

// Release 描述 GitHub 发布。
type Release struct {
	TagName     string    `json:"tag_name"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset 对应发布附件。
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

// Client 封装 GitHub 请求。
type Client struct {
	owner string
	repo  string
	http  *http.Client
	token string
}

// NewClient 创建 mosdns GitHub 客户端。
func NewClient(repo string) *Client {
	owner := "yyysuo"
	name := "mosdns"
	if repo != "" {
		parts := strings.Split(repo, "/")
		if len(parts) == 2 {
			owner = parts[0]
			name = parts[1]
		}
	}
	return &Client{
		owner: owner,
		repo:  name,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
		token: os.Getenv("GITHUB_TOKEN"),
	}
}

// LatestRelease 查询最新发行版。
func (c *Client) LatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", c.owner, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github api %s", resp.Status)
	}
	logs.Infof("[mosdns] GitHub %s/%s 状态 %s", c.owner, c.repo, resp.Status)
	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// Updater 负责下载/解压 mosdns 内核。
type Updater struct {
	Client     *Client
	InstallDir string
	AssetHint  string
}

// DefaultUpdater 简化创建。
func DefaultUpdater() *Updater {
	repo := os.Getenv("MOSDNS_REPO")
	if repo == "" {
		repo = defaultRepo
	}
	installDir := os.Getenv("MOSDNS_INSTALL_DIR")
	if installDir == "" {
		installDir = "/usr/local/bin"
	}
	return &Updater{
		Client:     NewClient(repo),
		InstallDir: installDir,
		AssetHint:  os.Getenv("MOSDNS_ASSET_KEYWORD"),
	}
}

// UpdateLatest 下载最新发行版，并尝试将核心二进制写入 InstallDir。
func (u *Updater) UpdateLatest(ctx context.Context) (*Release, string, error) {
	if u.Client == nil {
		u.Client = NewClient(defaultRepo)
	}
	if u.InstallDir == "" {
		u.InstallDir = "/usr/local/bin"
	}
	if err := os.MkdirAll(u.InstallDir, 0o755); err != nil {
		return nil, "", err
	}

	logs.Infof("[mosdns] 正在检测仓库 %s/%s 最新发行版", u.Client.owner, u.Client.repo)
	rel, err := u.Client.LatestRelease(ctx)
	if err != nil {
		logs.Errorf("[mosdns] 获取最新发行版失败: %v", err)
		return nil, "", err
	}

	asset, err := selectAsset(rel.Assets, u.AssetHint)
	if err != nil {
		logs.Errorf("[mosdns] 选择配置失败: %v", err)
		return nil, "", err
	}

	target := filepath.Join(u.InstallDir, "mosdns")
	logs.Infof("[mosdns] 下载配置 %s -> %s", asset.Name, target)
	if err := downloadAndExtract(ctx, asset.BrowserDownloadURL, target); err != nil {
		logs.Errorf("[mosdns] 下载或解压失败: %v", err)
		return nil, "", err
	}
	logs.Infof("[mosdns] mosdns 内核更新完成 -> %s", target)
	return rel, target, nil
}

func selectAsset(assets []Asset, hint string) (Asset, error) {
	if len(assets) == 0 {
		return Asset{}, errors.New("未找到可下载的资产")
	}

	filters := []string{runtime.GOOS, runtime.GOARCH}
	if hint != "" {
		filters = append(filters, hint)
	}

	for _, asset := range assets {
		if matchAsset(asset.Name, filters) {
			return asset, nil
		}
	}
	// fallback: 第一个
	return assets[0], nil
}

func matchAsset(name string, filters []string) bool {
	lower := strings.ToLower(name)
	for _, f := range filters {
		if f == "" {
			continue
		}
		if !strings.Contains(lower, strings.ToLower(f)) {
			return false
		}
	}
	return true
}

func downloadAndExtract(ctx context.Context, url, target string) error {
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
		return fmt.Errorf("下载失败：%s", resp.Status)
	}

	tempFile, err := os.CreateTemp("", "mosdns-asset-*.bin")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		tempFile.Close()
		return err
	}
	tempFile.Close()

	// 根据扩展名决定如何处理
	switch {
	case strings.HasSuffix(url, ".zip"):
		return extractZip(tempFile.Name(), target)
	case strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz"):
		return extractTarGz(tempFile.Name(), target)
	default:
		return moveBinary(tempFile.Name(), target)
	}
}

func extractZip(src, target string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.Contains(strings.ToLower(f.Name), "mosdns") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeBinary(rc, target, f.Mode())
	}
	return errors.New("zip 未找到 mosdns 可执行文件")
}

func extractTarGz(src, target string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		if !strings.Contains(strings.ToLower(hdr.Name), "mosdns") {
			continue
		}
		return writeBinary(tr, target, hdr.FileInfo().Mode())
	}
	return errors.New("tar.gz 未找到 mosdns 可执行文件")
}

func moveBinary(src, target string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeBinary(in, target, 0o755)
}

func writeBinary(r io.Reader, target string, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(target), "mosdns-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())

	if _, err := io.Copy(temp, r); err != nil {
		temp.Close()
		return err
	}
	temp.Close()

	if mode == 0 {
		mode = 0o755
	}
	if err := os.Chmod(temp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(temp.Name(), target)
}
