package ytdlp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/legacyconfig"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

const (
	ytDLPReleaseBase = "https://github.com/yt-dlp/yt-dlp/releases/latest/download"
	youtubeUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

func (p *Plugin) setup(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.respond(ctx, request, "⏳ 正在安装最新 yt-dlp…"); err != nil {
		return err
	}
	executable, err := p.ensureYTDLP(ctx, true)
	if err != nil {
		return p.respond(ctx, request, "❌ 安装失败："+shortError(err))
	}
	version := p.toolVersion(ctx, executable)
	deno := p.denoExecutable(ctx)
	text := "✅ yt-dlp 已准备完成\n\n• 路径：" + executable
	if version != "" {
		text += "\n• 版本：" + version
	}
	if deno == "" {
		text += "\n\n⚠️ 未找到 Deno。YouTube 现在需要 JS 运行时，" +
			"请安装 Deno 后运行 " + request.Prefix + "yt doctor。"
	} else {
		text += "\n• Deno：" + deno
	}
	return p.respond(ctx, request, text)
}

func (p *Plugin) doctor(ctx context.Context, request command.Request) error {
	var lines []string
	lines = append(lines, "🩺 yt 环境检查", "")
	if executable, err := p.findExecutable(ctx); err != nil {
		lines = append(lines, "❌ yt-dlp：未安装",
			"  运行 "+request.Prefix+"yt setup 自动安装")
	} else {
		version := p.toolVersion(ctx, executable)
		lines = append(lines, "✅ yt-dlp："+fallback(version, executable))
	}
	if executable, err := p.services.Tools.LookPath("ffmpeg"); err != nil {
		lines = append(lines, "❌ FFmpeg：未找到")
	} else {
		lines = append(lines, "✅ FFmpeg："+executable)
	}
	if executable := p.denoExecutable(ctx); executable == "" {
		lines = append(lines, "⚠️ Deno：未找到（YouTube JS 挑战需要）")
	} else {
		lines = append(lines, "✅ Deno："+executable)
	}
	if cookies, _ := p.read(ctx, "cookies"); cookies == "" {
		lines = append(lines, "⚠️ Cookies：未配置（服务器 IP 被风控时需要）")
	} else if info, err := os.Stat(cookies); err != nil || !info.Mode().IsRegular() {
		lines = append(lines, "❌ Cookies：文件不存在")
	} else {
		lines = append(lines, "✅ Cookies："+cookies)
	}
	if proxy, _ := p.read(ctx, "proxy"); proxy == "" {
		lines = append(lines, "ℹ️ 代理：未配置")
	} else {
		lines = append(lines, "✅ 代理："+redactProxy(proxy))
	}
	lines = append(lines, "",
		"如果出现 “Sign in to confirm you’re not a bot”，请配置 Cookies；"+
			"代理只能改善网络出口，不能替代账号 Cookies。")
	return p.respond(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) ensureYTDLP(ctx context.Context, force bool) (string, error) {
	if !force {
		if executable, err := p.findExecutable(ctx); err == nil {
			return executable, nil
		}
	}
	assetName := "yt-dlp"
	targetName := "yt-dlp"
	if runtime.GOOS == "windows" {
		assetName = "yt-dlp.exe"
		targetName = "yt-dlp.exe"
	}
	sums, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: ytDLPReleaseBase + "/SHA2-256SUMS",
	})
	if err != nil {
		return "", fmt.Errorf("download yt-dlp checksums: %w", err)
	}
	if sums.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download yt-dlp checksums: HTTP %d", sums.StatusCode)
	}
	expected := checksumFor(sums.Body, assetName)
	if expected == "" {
		return "", errors.New("yt-dlp checksum list does not contain " + assetName)
	}
	binary, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: ytDLPReleaseBase + "/" + assetName,
	})
	if err != nil {
		return "", fmt.Errorf(
			"download %s: %w (Windows 可执行文件较大时请调高 http.max_response_bytes)",
			assetName,
			err,
		)
	}
	if binary.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", assetName, binary.StatusCode)
	}
	digest := sha256.Sum256(binary.Body)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), expected) {
		return "", errors.New("yt-dlp SHA-256 verification failed")
	}
	if err := os.MkdirAll(p.assetDir, 0o700); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(p.assetDir, ".yt-dlp-*.tmp")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o700); err != nil {
		_ = temp.Close()
		return "", err
	}
	if _, err := temp.Write(binary.Body); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	target := filepath.Join(p.assetDir, targetName)
	if force {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := os.Rename(tempPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func checksumFor(document []byte, name string) string {
	for _, line := range strings.Split(string(document), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			value := strings.TrimSpace(fields[0])
			if len(value) == sha256.Size*2 {
				return value
			}
		}
	}
	return ""
}

func (p *Plugin) configureProxy(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		value, _ := p.read(ctx, "proxy")
		if value == "" {
			return p.respond(ctx, request, "ℹ️ 当前未配置 YouTube 下载代理")
		}
		return p.respond(ctx, request, "🌐 当前代理："+redactProxy(value))
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "clear") {
		return p.deleteSetting(ctx, request, "proxy", "YouTube 下载代理")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return p.respond(ctx, request, "❌ 代理地址无效")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks4", "socks5", "socks5h":
	default:
		return p.respond(ctx, request, "❌ 代理仅支持 HTTP(S)、SOCKS4 或 SOCKS5")
	}
	if err := p.write(ctx, "proxy", value); err != nil {
		return p.respond(ctx, request, "❌ 保存代理失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ YouTube 下载代理已保存："+redactProxy(value))
}

func (p *Plugin) configureCookies(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		value, _ := p.read(ctx, "cookies")
		if value == "" {
			return p.respond(ctx, request, "ℹ️ 当前未配置 Cookies 文件")
		}
		return p.respond(ctx, request, "🍪 当前 Cookies 文件："+value)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "clear") {
		return p.deleteSetting(ctx, request, "cookies", "Cookies 文件")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return p.respond(ctx, request, "❌ Cookies 路径无效："+err.Error())
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return p.respond(ctx, request, "❌ Cookies 文件不存在")
	}
	if err := p.write(ctx, "cookies", absolute); err != nil {
		return p.respond(ctx, request, "❌ 保存 Cookies 路径失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Cookies 路径已保存")
}

func (p *Plugin) configureRuntime(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		value, _ := p.read(ctx, "runtime")
		if value == "" {
			value = "auto"
		}
		return p.respond(ctx, request, "🧩 当前 JS 运行时："+value)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "auto") {
		return p.deleteSetting(ctx, request, "runtime", "JS 运行时（自动检测）")
	}
	if strings.EqualFold(value, "none") {
		if err := p.write(ctx, "runtime", "none"); err != nil {
			return p.respond(ctx, request, "❌ 保存 JS 运行时设置失败："+err.Error())
		}
		return p.respond(ctx, request, "⚠️ 已禁用显式 JS 运行时配置")
	}
	executable := value
	if !filepath.IsAbs(value) {
		resolved, err := p.services.Tools.LookPath(value)
		if err != nil {
			return p.respond(ctx, request, "❌ 找不到 Deno 可执行文件")
		}
		executable = resolved
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return p.respond(ctx, request, "❌ Deno 可执行文件不存在")
	}
	if err := p.write(ctx, "runtime", executable); err != nil {
		return p.respond(ctx, request, "❌ 保存 Deno 路径失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Deno 路径已保存")
}

func (p *Plugin) deleteSetting(
	ctx context.Context,
	request command.Request,
	key string,
	label string,
) error {
	if err := p.services.Storage.Delete(ctx, "yt-dlp", key); err != nil &&
		!errors.Is(err, storage.ErrNotFound) {
		return p.respond(ctx, request, "❌ 清除"+label+"失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 已清除"+label)
}

func (p *Plugin) ytDLPOptions(ctx context.Context) []string {
	options := []string{
		"--user-agent", youtubeUserAgent,
		"--extractor-args", "youtube:player_client=android,web",
		"--remote-components", "ejs:github",
	}
	if proxy, _ := p.read(ctx, "proxy"); proxy != "" {
		options = append(options, "--proxy", proxy)
	}
	if cookies, _ := p.read(ctx, "cookies"); cookies != "" {
		options = append(options, "--cookies", cookies)
	}
	if runtimePath, _ := p.read(ctx, "runtime"); runtimePath != "" &&
		!strings.EqualFold(runtimePath, "none") {
		options = append(options, "--js-runtimes", "deno:"+runtimePath)
	}
	return options
}

func (p *Plugin) denoExecutable(ctx context.Context) string {
	configured, _ := p.read(ctx, "runtime")
	if strings.EqualFold(configured, "none") {
		return ""
	}
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && info.Mode().IsRegular() {
			return configured
		}
		return ""
	}
	executable, err := p.services.Tools.LookPath("deno")
	if err != nil {
		return ""
	}
	return executable
}

func (p *Plugin) toolVersion(ctx context.Context, executable string) string {
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable, Args: []string{"--version"}, Timeout: 30 * time.Second,
	})
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(result.Stdout), "\n")
	return strings.TrimSpace(line)
}

func redactProxy(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "已配置"
	}
	return parsed.Redacted()
}

func (p *Plugin) migrateLegacyConfig(ctx context.Context) error {
	if p.services.AssetsDir == "" {
		return nil
	}
	values, err := legacyconfig.ReadSQLiteConfig(
		filepath.Join(p.services.AssetsDir, "ytdlp_gemini_config.db"),
	)
	if err != nil {
		return err
	}
	mapping := map[string]string{
		"ytdlp_gemini_api_key":     "api_key",
		"ytdlp_gemini_base_url":    "base_url",
		"ytdlp_gemini_model":       "model",
		"ytdlp_gemini_temperature": "temperature",
		"ytdlp_gemini_top_p":       "top_p",
		"ytdlp_gemini_top_k":       "top_k",
	}
	imported := 0
	for oldKey, newKey := range mapping {
		value := strings.TrimSpace(values[oldKey])
		if value == "" {
			continue
		}
		if _, err := p.services.Storage.Get(ctx, "yt-dlp", newKey); err == nil {
			continue
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if err := p.write(ctx, newKey, value); err != nil {
			return err
		}
		imported++
	}
	if imported > 0 {
		p.services.Logger.Info("migrated legacy yt-dlp config", "keys", imported)
	}
	return nil
}
