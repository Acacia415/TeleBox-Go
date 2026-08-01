package speedlink

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/managedtool"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
	"golang.org/x/crypto/ssh"
	_ "modernc.org/sqlite"
)

const (
	backupVersion    = 1
	speedtestVersion = "1.2.0"
)

type remoteServer struct {
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	AuthMethod    string `json:"auth_method"`
	Credential    string `json:"credential"`
	HostKeySHA256 string `json:"host_key_sha256"`
}

type backupDocument struct {
	Version   int            `json:"version"`
	CreatedAt string         `json:"created_at"`
	Servers   []remoteServer `json:"servers"`
}

type speedResult struct {
	ISP    string `json:"isp"`
	Server struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Location string `json:"location"`
	} `json:"server"`
	Interface struct {
		ExternalIP string `json:"externalIp"`
		Name       string `json:"name"`
	} `json:"interface"`
	Ping struct {
		Latency float64 `json:"latency"`
		Jitter  float64 `json:"jitter"`
	} `json:"ping"`
	Download struct {
		Bandwidth float64 `json:"bandwidth"`
		Bytes     float64 `json:"bytes"`
	} `json:"download"`
	Upload struct {
		Bandwidth float64 `json:"bandwidth"`
		Bytes     float64 `json:"bytes"`
	} `json:"upload"`
	Timestamp string `json:"timestamp"`
	Result    struct {
		URL string `json:"url"`
	} `json:"result"`
}

type Plugin struct {
	services  service.Container
	assetDir  string
	legacyDir string
	workDir   string

	mu        sync.Mutex
	masterKey []byte
	servers   []remoteServer
	timeout   time.Duration
}

func New(services service.Container) *Plugin {
	assetDir := filepath.Join(services.AssetsDir, "speedlink")
	if services.AssetsDir == "" {
		assetDir = filepath.Join(os.TempDir(), "telebox-go-speedlink-assets")
	}
	return &Plugin{
		services:  services,
		assetDir:  assetDir,
		legacyDir: legacySpeedlinkAssetDir(services.LegacyAssetsDir),
		workDir:   filepath.Join(os.TempDir(), "telebox-go-speedlink"),
		timeout:   5 * time.Minute,
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "speedlink",
		Version:     "0.4.0",
		Description: "通过 SSH 在本机或多台远程服务器运行 Ookla Speedtest",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "speedlink",
		Aliases:     []string{"sl"},
		Description: "在本机或多台远程服务器运行 Ookla Speedtest",
		Usage: []string{
			"sl",
			"sl <序号...>|all|all no <序号...>",
			"sl add <别名> <user@host:port> password <密码>",
			"sl add <别名> <user@host:port> key <私钥绝对路径>",
			"sl list",
			"sl del <序号>",
			"sl rename <序号> <新名称>",
			"sl timeout [10-600]",
			"sl backup",
			"sl restore confirm（回复备份文件）",
		},
		HelpHTML:  speedlinkGuideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(ctx context.Context) error {
	if err := os.MkdirAll(p.assetDir, 0o700); err != nil {
		return fmt.Errorf("create speedlink asset directory: %w", err)
	}
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return fmt.Errorf("create speedlink work directory: %w", err)
	}
	key, err := loadOrCreateKey(filepath.Join(p.assetDir, "go_secret.key"))
	if err != nil {
		return err
	}
	p.masterKey = key
	if data, getErr := p.services.Storage.Get(ctx, "speedlink", "timeout_seconds"); getErr == nil {
		seconds, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && seconds >= 10 && seconds <= 600 {
			p.timeout = time.Duration(seconds) * time.Second
		}
	}
	if err := p.loadServers(ctx); err != nil {
		return err
	}
	if len(p.servers) == 0 {
		if migrated, migrateErr := p.migrateLegacy(); migrateErr != nil {
			p.services.Logger.Warn("migrate legacy speedlink servers", "error", migrateErr)
		} else if len(migrated) > 0 {
			p.servers = migrated
			if err := p.saveServers(ctx); err != nil {
				return fmt.Errorf("save migrated speedlink servers: %w", err)
			}
			p.services.Logger.Info("migrated legacy speedlink servers", "count", len(migrated))
		}
	}
	return nil
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(request.Args) == 0 {
		return p.runLocal(ctx, request)
	}
	switch strings.ToLower(request.Args[0]) {
	case "help", "h":
		return p.respond(ctx, request, helpText(request.Prefix))
	case "add":
		return p.add(ctx, request)
	case "list":
		return p.list(ctx, request)
	case "del", "delete":
		return p.remove(ctx, request)
	case "rename":
		return p.rename(ctx, request)
	case "timeout":
		return p.configureTimeout(ctx, request)
	case "backup":
		return p.backup(ctx, request)
	case "restore":
		return p.restore(ctx, request)
	case "all":
		if len(p.servers) == 0 {
			return p.respond(ctx, request, "❌ 尚未配置远程服务器")
		}
		excluded := make(map[int]struct{})
		if len(request.Args) >= 2 && strings.EqualFold(request.Args[1], "no") {
			if len(request.Args) == 2 {
				return p.respond(ctx, request, "❌ 请在 no 后填写要排除的服务器序号")
			}
			for _, value := range request.Args[2:] {
				displayIndex, err := strconv.Atoi(value)
				if err != nil || displayIndex <= 0 || displayIndex > len(p.servers) {
					return p.respond(ctx, request, "❌ 无效的服务器序号："+value)
				}
				excluded[displayIndex-1] = struct{}{}
			}
		} else if len(request.Args) > 1 {
			return p.respond(ctx, request, "❌ 用法："+request.Prefix+"sl all no <序号...>")
		}
		indexes := make([]int, 0, len(p.servers))
		for index := range p.servers {
			if _, skip := excluded[index]; !skip {
				indexes = append(indexes, index)
			}
		}
		return p.runRemoteMany(ctx, request, indexes)
	}
	indexes := make([]int, 0, len(request.Args))
	for _, value := range request.Args {
		displayIndex, err := strconv.Atoi(value)
		if err != nil || displayIndex <= 0 || displayIndex > len(p.servers) {
			return p.respond(ctx, request, "❌ 无效的服务器序号："+value)
		}
		indexes = append(indexes, displayIndex-1)
	}
	return p.runRemoteMany(ctx, request, indexes)
}

func (p *Plugin) add(ctx context.Context, request command.Request) error {
	if len(request.Args) < 5 {
		return p.respond(ctx, request,
			"用法："+request.Prefix+
				"sl add <别名> <user@host:port> <password|key> <密码或私钥路径>")
	}
	name := strings.TrimSpace(request.Args[1])
	username, host, port, err := parseConnection(request.Args[2])
	if err != nil {
		return p.respond(ctx, request, "❌ 连接地址无效："+err.Error())
	}
	authMethod := strings.ToLower(request.Args[3])
	credential := strings.TrimSpace(strings.Join(request.Args[4:], " "))
	if name == "" || credential == "" ||
		(authMethod != "password" && authMethod != "key") {
		return p.respond(ctx, request, "❌ 名称、认证方式或凭据无效")
	}
	if authMethod == "key" {
		absolute, err := filepath.Abs(credential)
		if err != nil {
			return p.respond(ctx, request, "❌ 私钥路径无效："+err.Error())
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			return p.respond(ctx, request, "❌ 私钥文件不存在")
		}
		credential = absolute
	}
	if err := p.respond(ctx, request, "⏳ 验证 SSH 登录并记录主机指纹…"); err != nil {
		return err
	}
	auth, err := sshAuth(authMethod, credential)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取 SSH 凭据失败："+err.Error())
	}
	fingerprint, err := verifyFirstConnection(
		ctx,
		net.JoinHostPort(host, strconv.Itoa(port)),
		username,
		auth,
	)
	if err != nil {
		return p.respond(ctx, request, "❌ SSH 验证失败："+sanitizeError(err))
	}
	storedCredential := credential
	if authMethod == "password" {
		storedCredential, err = encryptCredential(p.masterKey, credential)
		if err != nil {
			return p.respond(ctx, request, "❌ 加密密码失败："+err.Error())
		}
	}
	p.servers = append(p.servers, remoteServer{
		Name:          name,
		Host:          host,
		Port:          port,
		Username:      username,
		AuthMethod:    authMethod,
		Credential:    storedCredential,
		HostKeySHA256: fingerprint,
	})
	if err := p.saveServers(ctx); err != nil {
		p.servers = p.servers[:len(p.servers)-1]
		return p.respond(ctx, request, "❌ 保存服务器失败："+err.Error())
	}
	return p.respond(ctx, request,
		"✅ 服务器 "+name+" 已添加并固定 SSH 主机指纹\n"+fingerprint)
}

func (p *Plugin) list(ctx context.Context, request command.Request) error {
	if len(p.servers) == 0 {
		return p.respond(ctx, request, "ℹ️ 未配置任何远程服务器")
	}
	var text strings.Builder
	text.WriteString("⚡️ SpeedLink 服务器\n\n")
	for index, item := range p.servers {
		fmt.Fprintf(
			&text,
			"%d. %s — %s@%s:%d（%s）\n",
			index+1,
			item.Name,
			item.Username,
			redactHost(item.Host),
			item.Port,
			item.AuthMethod,
		)
	}
	return p.respond(ctx, request, strings.TrimSpace(text.String()))
}

func (p *Plugin) remove(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request, "用法："+request.Prefix+"sl del <序号>")
	}
	index, err := strconv.Atoi(request.Args[1])
	if err != nil || index <= 0 || index > len(p.servers) {
		return p.respond(ctx, request, "❌ 服务器序号无效")
	}
	removed := p.servers[index-1]
	p.servers = append(p.servers[:index-1], p.servers[index:]...)
	if err := p.saveServers(ctx); err != nil {
		return p.respond(ctx, request, "❌ 删除服务器失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 已删除服务器 "+removed.Name)
}

func (p *Plugin) rename(ctx context.Context, request command.Request) error {
	if len(request.Args) < 3 {
		return p.respond(ctx, request,
			"用法："+request.Prefix+"sl rename <序号> <新名称>")
	}
	index, err := strconv.Atoi(request.Args[1])
	name := strings.TrimSpace(strings.Join(request.Args[2:], " "))
	if err != nil || index <= 0 || index > len(p.servers) || name == "" {
		return p.respond(ctx, request, "❌ 服务器序号或新名称无效")
	}
	for position, server := range p.servers {
		if position != index-1 && strings.EqualFold(server.Name, name) {
			return p.respond(ctx, request, "❌ 已存在同名服务器")
		}
	}
	oldName := p.servers[index-1].Name
	p.servers[index-1].Name = name
	if err := p.saveServers(ctx); err != nil {
		p.servers[index-1].Name = oldName
		return p.respond(ctx, request, "❌ 重命名失败："+err.Error())
	}
	return p.respond(ctx, request,
		"✅ 已将服务器 "+oldName+" 重命名为 "+name)
}

func (p *Plugin) configureTimeout(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		return p.respond(ctx, request, fmt.Sprintf(
			"ℹ️ 当前测速超时：%d 秒\n使用 %ssl timeout <秒数> 修改",
			int(p.timeout/time.Second), request.Prefix,
		))
	}
	if len(request.Args) != 2 {
		return p.respond(ctx, request,
			"用法："+request.Prefix+"sl timeout <10-600>")
	}
	seconds, err := strconv.Atoi(request.Args[1])
	if err != nil || seconds < 10 || seconds > 600 {
		return p.respond(ctx, request, "❌ 超时时间必须为 10 到 600 秒")
	}
	if err := p.services.Storage.Put(
		ctx, "speedlink", "timeout_seconds", []byte(strconv.Itoa(seconds)),
	); err != nil {
		return p.respond(ctx, request, "❌ 保存超时设置失败："+err.Error())
	}
	p.timeout = time.Duration(seconds) * time.Second
	return p.respond(ctx, request,
		fmt.Sprintf("✅ 测速超时已设置为 %d 秒", seconds))
}

func (p *Plugin) runLocal(ctx context.Context, request command.Request) error {
	executable, err := p.findSpeedtest()
	if err != nil {
		if err := p.respond(ctx, request, "⏳ 本机 Speedtest CLI 不存在，正在自动安装…"); err != nil {
			return err
		}
		executable, err = p.installSpeedtest(ctx)
		if err != nil {
			return p.respond(ctx, request,
				"❌ 自动安装 Ookla Speedtest CLI 失败："+sanitizeError(err))
		}
	}
	status, err := p.statusHTML(ctx, request, "⚡️ 正在进行<b>本机</b>速度测试...")
	if err != nil {
		return err
	}
	started := time.Now()
	output, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:    executable,
		Args:    []string{"--accept-license", "--accept-gdpr", "-f", "json"},
		Env:     p.speedtestEnvironment(),
		Timeout: p.timeout, MaxOutput: 512 << 10,
	})
	duration := time.Since(started)
	if err != nil {
		return p.editStatusHTML(ctx, status,
			"❌ <b>速度测试失败</b>\n\n<code>"+
				html.EscapeString(shortDetail(output.Stderr, err))+"</code>")
	}
	result, err := parseSpeedResult(output.Stdout)
	if err != nil {
		return p.editStatusHTML(ctx, status,
			"❌ <b>速度测试失败</b>\n\n<code>无法解析测速结果</code>")
	}
	if err := p.sendSpeedResult(ctx, request.Message.ChatID, "", true, result, duration); err != nil {
		return p.editStatusHTML(ctx, status,
			"❌ <b>发送测速结果失败</b>\n\n<code>"+
				html.EscapeString(sanitizeError(err))+"</code>")
	}
	return p.deleteStatus(ctx, status)
}

func (p *Plugin) speedtestEnvironment() []string {
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = p.assetDir
	}
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + configHome,
		"XDG_DATA_HOME=" + dataHome,
	}
}

func (p *Plugin) runRemoteMany(
	ctx context.Context,
	request command.Request,
	indexes []int,
) error {
	if len(indexes) == 0 {
		return p.respond(ctx, request, "❌ 没有有效的测速服务器")
	}
	if request.Message.Outgoing {
		if err := p.services.Telegram.DeleteMessages(
			ctx, request.Message.ChatID, []int{request.Message.ID},
		); err != nil {
			return err
		}
	}
	for position, index := range indexes {
		server := p.servers[index]
		statusText := fmt.Sprintf(
			"⚡️ [%d/%d] 正在为 <b>%s</b> 进行远程测速...",
			position+1, len(indexes), html.EscapeString(server.Name),
		)
		status, err := telegram.SendHTML(
			ctx, p.services.Telegram, request.Message.ChatID, statusText,
		)
		if err != nil {
			return err
		}
		started := time.Now()
		result, err := p.runRemote(ctx, server)
		duration := time.Since(started)
		if err != nil {
			_ = p.editStatusHTML(ctx, status,
				"❌ <b>"+html.EscapeString(server.Name)+
					"</b> 测速失败\n\n<code>"+
					html.EscapeString(sanitizeError(err))+"</code>")
			continue
		}
		if err := p.sendSpeedResult(
			ctx, request.Message.ChatID, server.Name, false, result, duration,
		); err != nil {
			_ = p.editStatusHTML(ctx, status,
				"❌ <b>"+html.EscapeString(server.Name)+
					"</b> 结果发送失败\n\n<code>"+
					html.EscapeString(sanitizeError(err))+"</code>")
			continue
		}
		_ = p.deleteStatus(ctx, status)
	}
	return nil
}

func (p *Plugin) runRemote(
	ctx context.Context,
	server remoteServer,
) (speedResult, error) {
	credential := server.Credential
	if server.AuthMethod == "password" {
		var err error
		credential, err = decryptCredential(p.masterKey, credential)
		if err != nil {
			return speedResult{}, errors.New("保存的密码无法解密")
		}
	}
	auth, err := sshAuth(server.AuthMethod, credential)
	if err != nil {
		return speedResult{}, err
	}
	client, err := dialSSH(
		ctx,
		net.JoinHostPort(server.Host, strconv.Itoa(server.Port)),
		server.Username,
		auth,
		server.HostKeySHA256,
	)
	if err != nil {
		return speedResult{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return speedResult{}, err
	}
	defer session.Close()
	type outputResult struct {
		data []byte
		err  error
	}
	done := make(chan outputResult, 1)
	go func() {
		data, runErr := session.Output(
			"speedtest --accept-license --accept-gdpr -f json",
		)
		done <- outputResult{data: data, err: runErr}
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		return speedResult{}, ctx.Err()
	case output := <-done:
		if output.err != nil {
			return speedResult{}, output.err
		}
		return parseSpeedResult(string(output.data))
	case <-time.After(p.timeout):
		_ = client.Close()
		return speedResult{}, errors.New("远程测速超时")
	}
}

func (p *Plugin) backup(ctx context.Context, request command.Request) error {
	document := backupDocument{
		Version:   backupVersion,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Servers:   append([]remoteServer(nil), p.servers...),
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建备份失败："+err.Error())
	}
	jobDir, err := os.MkdirTemp(p.workDir, "backup-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建备份目录失败："+err.Error())
	}
	defer os.RemoveAll(jobDir)
	path := filepath.Join(jobDir, "speedlink-backup.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return p.respond(ctx, request, "❌ 写入备份失败："+err.Error())
	}
	me, err := p.services.Telegram.ResolveUser(ctx, "me")
	if err != nil {
		return p.respond(ctx, request, "❌ 无法打开收藏夹："+err.Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, me.ID, telegram.Upload{
		Path:     path,
		FileName: "speedlink-backup.json",
		MIMEType: "application/json",
		Caption:  "SpeedLink 加密服务器配置备份",
		Kind:     telegram.MediaDocument,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 发送备份失败："+err.Error())
	}
	return p.respond(ctx, request,
		"✅ 备份已发送至收藏夹\n密码仍为本机密钥加密，只能在保留 go_secret.key 的安装中恢复。")
}

func (p *Plugin) restore(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 || !strings.EqualFold(request.Args[1], "confirm") {
		return p.respond(ctx, request,
			"⚠️ 恢复会覆盖现有服务器配置。\n回复备份文件并发送 "+
				request.Prefix+"sl restore confirm")
	}
	if request.Message.ReplyToID == 0 {
		return p.respond(ctx, request, "❌ 请回复 SpeedLink JSON 备份文件")
	}
	var data bytes.Buffer
	if _, err := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		&boundedWriter{Writer: &data, Remaining: 1 << 20},
	); err != nil {
		return p.respond(ctx, request, "❌ 下载备份失败："+err.Error())
	}
	var document backupDocument
	decoder := json.NewDecoder(bytes.NewReader(data.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return p.respond(ctx, request, "❌ 备份 JSON 无效："+err.Error())
	}
	if document.Version != backupVersion || len(document.Servers) > 100 {
		return p.respond(ctx, request, "❌ 备份版本或服务器数量无效")
	}
	for index, server := range document.Servers {
		if err := validateServer(server); err != nil {
			return p.respond(ctx, request, fmt.Sprintf(
				"❌ 第 %d 台服务器无效：%s", index+1, err,
			))
		}
		if server.AuthMethod == "password" {
			if _, err := decryptCredential(p.masterKey, server.Credential); err != nil {
				return p.respond(ctx, request,
					"❌ 备份密码无法用当前 go_secret.key 解密，未更改现有配置")
			}
		}
	}
	previous := p.servers
	p.servers = append([]remoteServer(nil), document.Servers...)
	if err := p.saveServers(ctx); err != nil {
		p.servers = previous
		return p.respond(ctx, request, "❌ 保存恢复配置失败："+err.Error())
	}
	return p.respond(ctx, request,
		fmt.Sprintf("✅ 已恢复 %d 台服务器", len(p.servers)))
}

func (p *Plugin) loadServers(ctx context.Context) error {
	data, err := p.services.Storage.Get(ctx, "speedlink", "servers")
	if err != nil {
		p.servers = nil
		return nil
	}
	if err := json.Unmarshal(data, &p.servers); err != nil {
		return fmt.Errorf("decode speedlink servers: %w", err)
	}
	for _, server := range p.servers {
		if err := validateServer(server); err != nil {
			return fmt.Errorf("invalid stored speedlink server: %w", err)
		}
	}
	return nil
}

func (p *Plugin) saveServers(ctx context.Context) error {
	data, err := json.Marshal(p.servers)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, "speedlink", "servers", data)
}

func (p *Plugin) migrateLegacy() ([]remoteServer, error) {
	for _, directory := range uniquePaths(p.assetDir, p.legacyDir) {
		result, found, err := p.migrateLegacyDirectory(directory)
		if err != nil {
			return nil, err
		}
		if found {
			return result, nil
		}
	}
	return nil, nil
}

func (p *Plugin) migrateLegacyDirectory(directory string) ([]remoteServer, bool, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, false, nil
	}
	keyData, err := os.ReadFile(filepath.Join(directory, "secret.key"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	legacyKey := bytes.TrimSpace(keyData)
	if len(legacyKey) != 32 {
		return nil, false, errors.New("legacy speedlink key length is invalid")
	}
	var databasePath string
	for _, name := range []string{"servers.db", "servers.db.bak"} {
		candidate := filepath.Join(directory, name)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			databasePath = candidate
			break
		}
	}
	if databasePath == "" {
		return nil, false, nil
	}
	database, err := sql.Open(
		"sqlite",
		"file:"+filepath.ToSlash(databasePath)+"?mode=ro",
	)
	if err != nil {
		return nil, true, err
	}
	defer database.Close()
	rows, err := database.Query(`
		SELECT name, host, port, username, auth_method, credentials
		FROM servers ORDER BY id
	`)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	var result []remoteServer
	for rows.Next() {
		var server remoteServer
		if err := rows.Scan(
			&server.Name,
			&server.Host,
			&server.Port,
			&server.Username,
			&server.AuthMethod,
			&server.Credential,
		); err != nil {
			return nil, true, err
		}
		if server.AuthMethod == "password" {
			plain, err := decryptLegacyCredential(legacyKey, server.Credential)
			if err != nil {
				return nil, true, err
			}
			server.Credential, err = encryptCredential(p.masterKey, plain)
			if err != nil {
				return nil, true, err
			}
		}
		// Legacy code disabled host-key checking. An empty fingerprint forces
		// users to re-add the server before remote execution.
		result = append(result, server)
	}
	if err := rows.Err(); err != nil {
		return nil, true, err
	}
	return result, true, nil
}

func legacySpeedlinkAssetDir(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(root, "speedlink")
}

func uniquePaths(values ...string) []string {
	var result []string
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		cleaned := filepath.Clean(value)
		duplicate := false
		for _, existing := range result {
			if filepath.Clean(existing) == cleaned {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, value)
		}
	}
	return result
}

func (p *Plugin) findSpeedtest() (string, error) {
	names := []string{"speedtest"}
	if runtime.GOOS == "windows" {
		names = []string{"speedtest.exe", "speedtest"}
	}
	for _, name := range names {
		candidate := filepath.Join(p.assetDir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if managedtool.Verify(candidate, p.speedtestReceiptPath()) == nil {
				return candidate, nil
			}
			quarantined, quarantineErr := managedtool.Quarantine(
				candidate, filepath.Join(p.assetDir, "quarantine"),
			)
			if quarantineErr != nil {
				return "", fmt.Errorf("隔离旧 Speedtest 失败: %w", quarantineErr)
			}
			_ = os.Remove(p.speedtestReceiptPath())
			if quarantined != "" && p.services.Logger != nil {
				p.services.Logger.Info(
					"quarantined unmanaged speedtest",
					"path", quarantined,
				)
			}
		}
		if path, err := p.services.Tools.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", toolrunner.ErrExecutableNotFound
}

func (p *Plugin) installSpeedtest(ctx context.Context) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("不支持在 %s 上自动安装，请将官方 speedtest 加入 PATH", runtime.GOOS)
	}
	architecture := map[string]string{
		"amd64": "x86_64",
		"arm64": "aarch64",
		"arm":   "armhf",
	}[runtime.GOARCH]
	if architecture == "" {
		return "", fmt.Errorf("不支持的 Linux 架构 %s", runtime.GOARCH)
	}
	filename := fmt.Sprintf(
		"ookla-speedtest-%s-linux-%s.tgz",
		speedtestVersion,
		architecture,
	)
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: "https://install.speedtest.net/app/cli/" + filename,
	})
	if err != nil {
		return "", fmt.Errorf("下载 %s: %w", filename, err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 %s: HTTP %d", filename, response.StatusCode)
	}
	target := filepath.Join(p.assetDir, "speedtest")
	if err := extractSpeedtestArchive(response.Body, target); err != nil {
		return "", err
	}
	digest, err := managedtool.FileSHA256(target)
	if err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if err := managedtool.WriteReceipt(
		p.speedtestReceiptPath(),
		"https://install.speedtest.net/app/cli/"+filename,
		speedtestVersion,
		digest,
	); err != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("保存 Speedtest 安装凭据: %w", err)
	}
	return target, nil
}

func (p *Plugin) speedtestReceiptPath() string {
	return filepath.Join(p.assetDir, ".speedtest-managed.json")
}

func extractSpeedtestArchive(document []byte, target string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(document))
	if err != nil {
		return fmt.Errorf("打开 Speedtest 压缩包: %w", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("读取 Speedtest 压缩包: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "speedtest" {
			continue
		}
		if header.Size <= 0 || header.Size > 32<<20 {
			return errors.New("Speedtest 可执行文件大小无效")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(target), ".speedtest-*.tmp")
		if err != nil {
			return err
		}
		tempPath := temp.Name()
		defer os.Remove(tempPath)
		if err := temp.Chmod(0o700); err != nil {
			_ = temp.Close()
			return err
		}
		if _, err := io.CopyN(temp, archive, header.Size); err != nil {
			_ = temp.Close()
			return fmt.Errorf("解压 Speedtest: %w", err)
		}
		if err := temp.Sync(); err != nil {
			_ = temp.Close()
			return err
		}
		if err := temp.Close(); err != nil {
			return err
		}
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(tempPath, target); err != nil {
			return err
		}
		return nil
	}
	return errors.New("Speedtest 压缩包中没有可执行文件")
}

func parseConnection(value string) (username, host string, port int, err error) {
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return "", "", 0, errors.New("格式应为 user@host:port")
	}
	username = value[:at]
	hostPort := value[at+1:]
	host, portText, splitErr := net.SplitHostPort(hostPort)
	if splitErr != nil {
		lastColon := strings.LastIndex(hostPort, ":")
		if lastColon <= 0 || lastColon == len(hostPort)-1 {
			return "", "", 0, errors.New("缺少端口；IPv6 请使用 [地址]:端口")
		}
		host, portText = hostPort[:lastColon], hostPort[lastColon+1:]
	}
	host = strings.Trim(host, "[]")
	port, err = strconv.Atoi(portText)
	if username == "" || net.ParseIP(host) == nil && !validHostname(host) ||
		err != nil || port <= 0 || port > 65535 {
		return "", "", 0, errors.New("用户名、主机或端口无效")
	}
	return username, host, port, nil
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, " \t\r\n/\\") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 ||
			strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func sshAuth(method, credential string) (ssh.AuthMethod, error) {
	switch method {
	case "password":
		return ssh.Password(credential), nil
	case "key":
		keyData, err := os.ReadFile(credential)
		if err != nil {
			return nil, err
		}
		signer, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return nil, err
		}
		return ssh.PublicKeys(signer), nil
	default:
		return nil, errors.New("unsupported SSH authentication method")
	}
}

func verifyFirstConnection(
	ctx context.Context,
	address string,
	username string,
	auth ssh.AuthMethod,
) (string, error) {
	var fingerprint string
	client, err := dialSSHWithCallback(ctx, address, username, auth,
		func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fingerprint = ssh.FingerprintSHA256(key)
			return nil
		})
	if err != nil {
		return "", err
	}
	client.Close()
	if fingerprint == "" {
		return "", errors.New("server did not present an SSH host key")
	}
	return fingerprint, nil
}

func dialSSH(
	ctx context.Context,
	address string,
	username string,
	auth ssh.AuthMethod,
	expectedFingerprint string,
) (*ssh.Client, error) {
	if expectedFingerprint == "" {
		return nil, errors.New("旧配置没有主机指纹，请删除后重新添加该服务器")
	}
	return dialSSHWithCallback(ctx, address, username, auth,
		func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if actual != expectedFingerprint {
				return fmt.Errorf("SSH 主机指纹已变化：%s", actual)
			}
			return nil
		})
}

func dialSSHWithCallback(
	ctx context.Context,
	address string,
	username string,
	auth ssh.AuthMethod,
	callback ssh.HostKeyCallback,
) (*ssh.Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return nil, err
	}
	_ = connection.SetDeadline(time.Now().Add(25 * time.Second))
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: callback,
		Timeout:         20 * time.Second,
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(
		connection, address, config,
	)
	if err != nil {
		connection.Close()
		return nil, err
	}
	_ = connection.SetDeadline(time.Time{})
	return ssh.NewClient(clientConnection, channels, requests), nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		decoded, decodeErr := base64.RawStdEncoding.DecodeString(
			strings.TrimSpace(string(data)),
		)
		if decodeErr != nil || len(decoded) != 32 {
			return nil, errors.New("speedlink master key is invalid")
		}
		return decoded, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	encoded := base64.RawStdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptCredential(key []byte, value string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), []byte("speedlink/v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func decryptCredential(key []byte, value string) (string, error) {
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", errors.New("encrypted credential is truncated")
	}
	plain, err := aead.Open(
		nil,
		data[:aead.NonceSize()],
		data[aead.NonceSize():],
		[]byte("speedlink/v1"),
	)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func decryptLegacyCredential(key []byte, value string) (string, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", errors.New("legacy credential format is invalid")
	}
	iv, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	encrypted, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(iv) != block.BlockSize() || len(encrypted) == 0 ||
		len(encrypted)%block.BlockSize() != 0 {
		return "", errors.New("legacy credential ciphertext is invalid")
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, encrypted)
	padding := int(plain[len(plain)-1])
	if padding <= 0 || padding > block.BlockSize() || padding > len(plain) {
		return "", errors.New("legacy credential padding is invalid")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return "", errors.New("legacy credential padding is invalid")
		}
	}
	return string(plain[:len(plain)-padding]), nil
}

func parseSpeedResult(output string) (speedResult, error) {
	start := strings.IndexByte(output, '{')
	if start < 0 {
		return speedResult{}, errors.New("speedtest did not return JSON")
	}
	var result speedResult
	if err := json.Unmarshal([]byte(output[start:]), &result); err != nil {
		return speedResult{}, err
	}
	if result.Server.ID <= 0 {
		return speedResult{}, errors.New("speedtest result has no server")
	}
	return result, nil
}

func (p *Plugin) sendSpeedResult(
	ctx context.Context,
	chatID int64,
	label string,
	local bool,
	value speedResult,
	duration time.Duration,
) error {
	asInfo, countryFlag := p.lookupIPDetails(ctx, value.Interface.ExternalIP)
	caption := formatSpeedResultHTML(
		label, local, value, asInfo, countryFlag, duration,
	)
	imagePath, cleanup := p.saveSpeedtestImage(ctx, value.Result.URL)
	if cleanup != nil {
		defer cleanup()
	}
	if imagePath != "" {
		_, err := p.services.Telegram.SendFile(ctx, chatID, telegram.Upload{
			Path:        imagePath,
			FileName:    "speedtest.png",
			MIMEType:    "image/png",
			Caption:     caption,
			CaptionHTML: true,
			Kind:        telegram.MediaPhoto,
		})
		return err
	}
	_, err := telegram.SendHTML(ctx, p.services.Telegram, chatID, caption)
	return err
}

func formatSpeedResultHTML(
	label string,
	local bool,
	value speedResult,
	asInfo string,
	countryFlag string,
	duration time.Duration,
) string {
	heading := "<b>" + html.EscapeString(label) + "</b>"
	if local {
		heading = "<b>⚡️SPEEDTEST by OOKLA</b>"
	}
	connection := "4"
	if strings.Contains(value.Interface.ExternalIP, ":") {
		connection = "6"
	}
	name := html.EscapeString(value.ISP + " " + asInfo)
	beijingTime := value.Timestamp
	if parsed, err := time.Parse(time.RFC3339Nano, value.Timestamp); err == nil {
		beijingTime = parsed.In(time.FixedZone("UTC+8", 8*60*60)).
			Format("2006-01-02 15:04:05")
	}
	return strings.Join([]string{
		heading + " " + countryFlag,
		"<code>Name</code>  <code>" + name + "</code>",
		fmt.Sprintf(
			"<code>Node</code>  <code>%d - %s - %s</code>",
			value.Server.ID,
			html.EscapeString(value.Server.Name),
			html.EscapeString(value.Server.Location),
		),
		"<code>Conn</code>  <code>Multi - IPv" + connection + " - " +
			html.EscapeString(value.Interface.Name) + "</code>",
		fmt.Sprintf(
			"<code>Ping</code>  <code>⇔%.3fms ±%.3fms</code>",
			value.Ping.Latency, value.Ping.Jitter,
		),
		"<code>Rate</code>  <code>↓" + unitConvert(value.Download.Bandwidth, false) +
			" ↑" + unitConvert(value.Upload.Bandwidth, false) + "</code>",
		"<code>Data</code>  <code>↓" + unitConvert(value.Download.Bytes, true) +
			" ↑" + unitConvert(value.Upload.Bytes, true) + "</code>",
		"<code>Time</code>  <code>" + html.EscapeString(beijingTime) + " (UTC+8)</code>",
		fmt.Sprintf("<code>Used</code>  <code>%.2fs</code>", duration.Seconds()),
		"<code>Link</code>  " + html.EscapeString(value.Result.URL),
	}, "\n")
}

func unitConvert(value float64, bytes bool) string {
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	if bytes {
		units = []string{"B", "KB", "MB", "GB", "TB"}
	} else {
		value *= 8
	}
	index := 0
	for value >= 1000 && index < len(units)-1 {
		value /= 1000
		index++
	}
	value = math.Round(value*100) / 100
	return strconv.FormatFloat(value, 'f', -1, 64) + units[index]
}

func (p *Plugin) lookupIPDetails(ctx context.Context, ip string) (string, string) {
	if net.ParseIP(strings.TrimSpace(ip)) == nil {
		return "", ""
	}
	var response struct {
		AS          string `json:"as"`
		CountryCode string `json:"countryCode"`
	}
	result, err := p.services.HTTP.JSON(ctx, httpclient.Request{
		URL:     "http://ip-api.com/json/" + ip + "?fields=as,countryCode",
		Timeout: 10 * time.Second,
	}, &response)
	if err != nil || result.StatusCode != http.StatusOK {
		return "", ""
	}
	asInfo := ""
	if fields := strings.Fields(response.AS); len(fields) > 0 {
		asInfo = fields[0]
	}
	code := strings.ToUpper(strings.TrimSpace(response.CountryCode))
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return asInfo, ""
	}
	return asInfo, string([]rune{
		rune(127397) + rune(code[0]),
		rune(127397) + rune(code[1]),
	})
}

func (p *Plugin) saveSpeedtestImage(
	ctx context.Context,
	resultURL string,
) (string, func()) {
	if strings.TrimSpace(resultURL) == "" {
		return "", nil
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL:     strings.TrimSpace(resultURL) + ".png",
		Timeout: 30 * time.Second,
	})
	if err != nil || response.StatusCode != http.StatusOK || len(response.Body) == 0 {
		return "", nil
	}
	directory, err := os.MkdirTemp(p.workDir, "result-*")
	if err != nil {
		return "", nil
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	rawPath := filepath.Join(directory, "speedtest.png")
	if err := os.WriteFile(rawPath, response.Body, 0o600); err != nil {
		cleanup()
		return "", nil
	}
	filledPath := filepath.Join(directory, "speedtest_filled.png")
	if err := fillSpeedtestBorder(rawPath, filledPath, 14); err != nil {
		return rawPath, cleanup
	}
	return filledPath, cleanup
}

func fillSpeedtestBorder(inputPath, outputPath string, border int) error {
	file, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	source, _, decodeErr := image.Decode(file)
	closeErr := file.Close()
	if err := errors.Join(decodeErr, closeErr); err != nil {
		return err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return errors.New("测速图片尺寸无效")
	}
	maxInset := (min(width, height) - 1) / 2
	inset := max(0, min(border, maxInset))
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(
		canvas,
		canvas.Bounds(),
		&image.Uniform{C: color.RGBA{R: 0x21, G: 0x23, B: 0x38, A: 0xff}},
		image.Point{},
		draw.Src,
	)
	draw.Draw(
		canvas,
		image.Rect(inset, inset, width-inset, height-inset),
		source,
		image.Pt(bounds.Min.X+inset, bounds.Min.Y+inset),
		draw.Src,
	)
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	encodeErr := encoder.Encode(output, canvas)
	return errors.Join(encodeErr, output.Close())
}

func (p *Plugin) statusHTML(
	ctx context.Context,
	request command.Request,
	text string,
) (telegram.SentMessage, error) {
	if request.Message.Outgoing {
		return telegram.EditHTML(
			ctx, p.services.Telegram, request.Message.ChatID, request.Message.ID, text,
		)
	}
	return telegram.ReplyHTML(
		ctx, p.services.Telegram, request.Message.ChatID, request.Message.ID, text,
	)
}

func (p *Plugin) editStatusHTML(
	ctx context.Context,
	status telegram.SentMessage,
	text string,
) error {
	_, err := telegram.EditHTML(
		ctx, p.services.Telegram, status.ChatID, status.MessageID, text,
	)
	return err
}

func (p *Plugin) deleteStatus(ctx context.Context, status telegram.SentMessage) error {
	if status.MessageID <= 0 {
		return nil
	}
	return p.services.Telegram.DeleteMessages(ctx, status.ChatID, []int{status.MessageID})
}

func validateServer(server remoteServer) error {
	if server.Name == "" || server.Username == "" || server.Host == "" ||
		server.Port <= 0 || server.Port > 65535 ||
		(server.AuthMethod != "password" && server.AuthMethod != "key") ||
		server.Credential == "" {
		return errors.New("server fields are incomplete")
	}
	return nil
}

func redactHost(host string) string {
	if net.ParseIP(host) != nil {
		return "[IP 已隐藏]"
	}
	parts := strings.Split(host, ".")
	if len(parts) > 2 {
		return "***." + strings.Join(parts[len(parts)-2:], ".")
	}
	return "***"
}

var (
	ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Pattern = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]*\b`)
)

func sanitizeError(err error) string {
	text := ipv4Pattern.ReplaceAllString(err.Error(), "[IP 已隐藏]")
	text = ipv6Pattern.ReplaceAllString(text, "[IPv6 已隐藏]")
	runes := []rune(text)
	if len(runes) > 500 {
		return string(runes[:500]) + "…"
	}
	return text
}

func shortDetail(stderr string, err error) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	return sanitizeError(errors.New(detail))
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := p.services.Telegram.EditText(
			ctx, request.Message.ChatID, request.Message.ID, text,
		)
		return err
	}
	_, err := p.services.Telegram.ReplyText(
		ctx, request.Message.ChatID, request.Message.ID, text,
	)
	return err
}

type boundedWriter struct {
	io.Writer
	Remaining int64
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.Remaining {
		return 0, errors.New("backup exceeds 1 MiB")
	}
	count, err := w.Writer.Write(data)
	w.Remaining -= int64(count)
	return count, err
}

func helpText(prefix string) string {
	return "⚡️ SpeedLink 多服务器测速\n\n" +
		"本插件可以对本机或多台远程服务器进行网络速度测试，并保存、管理服务器配置。\n\n" +
		"⚠️ 远程服务器要求\n" +
		"远程服务器必须先安装 Ookla Speedtest CLI。\n\n" +
		"Debian/Ubuntu：\n" +
		"curl -sL https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh | sudo bash\n" +
		"sudo apt-get install speedtest\n\n" +
		"CentOS/RHEL：\n" +
		"curl -sL https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh | sudo bash\n" +
		"sudo yum install speedtest\n\n" +
		"服务器管理：\n" +
		prefix + "sl add <别名> <user@host:port> password <密码>\n" +
		prefix + "sl add <别名> <user@host:port> key <私钥绝对路径>\n" +
		prefix + "sl list\n" +
		prefix + "sl del <序号>\n" +
		prefix + "sl rename <序号> <新名称>\n\n" +
		"执行测速：\n" +
		prefix + "sl（本机）\n" +
		prefix + "sl <序号>（单台远程）\n" +
		prefix + "sl 1 3 5（多台远程）\n" +
		prefix + "sl all（全部）\n" +
		prefix + "sl all no 2 4（全部但排除 2、4）\n\n" +
		"测速设置：\n" +
		prefix + "sl timeout（查看当前超时）\n" +
		prefix + "sl timeout <10-600>（修改秒数）\n\n" +
		"备份与恢复：\n" +
		prefix + "sl backup（备份到收藏夹）\n" +
		prefix + "sl restore confirm（回复备份文件，会覆盖现有数据）\n\n" +
		"首次添加会验证登录并固定 SSH 主机指纹；密码使用 AES-256-GCM 本地加密。"
}

const speedlinkGuideHTML = `<b>完整教程</b>

本插件可以对本机或多台远程服务器进行网络速度测试，并保存、管理服务器配置。

<b>⚠️ 远程服务器要求</b>
远程服务器必须先安装 <b>Ookla Speedtest CLI</b>。

<b>Debian/Ubuntu：</b>
<pre>curl -sL https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh | sudo bash
sudo apt-get install speedtest</pre>

<b>CentOS/RHEL：</b>
<pre>curl -sL https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh | sudo bash
sudo yum install speedtest</pre>

<b>服务器管理</b>
• 密码认证：
  <code>{{prefix}}sl add &lt;别名&gt; &lt;user@host:port&gt; password &lt;密码&gt;</code>
  示例：<code>{{prefix}}sl add 东京-甲骨文 root@1.2.3.4:22 password MyPassword123</code>
• 密钥认证：
  <code>{{prefix}}sl add &lt;别名&gt; &lt;user@host:port&gt; key &lt;私钥路径&gt;</code>
  私钥路径必须是运行 TeleBox 的服务器上的绝对路径。
  示例：<code>{{prefix}}sl add 法兰克福-谷歌 ubuntu@5.6.7.8:22 key /root/.ssh/id_rsa</code>
• 查看：<code>{{prefix}}sl list</code>
• 删除：<code>{{prefix}}sl del &lt;序号&gt;</code>
• 重命名：<code>{{prefix}}sl rename &lt;序号&gt; &lt;新名称&gt;</code>

<b>执行测速</b>
• 本机：<code>{{prefix}}sl</code>
• 单台远程：<code>{{prefix}}sl &lt;序号&gt;</code>
• 多台远程：<code>{{prefix}}sl 1 3 5</code>
• 全部：<code>{{prefix}}sl all</code>
• 排除部分：<code>{{prefix}}sl all no 2 4</code>

<b>测速设置</b>
• 查看超时：<code>{{prefix}}sl timeout</code>
• 修改超时：<code>{{prefix}}sl timeout &lt;10-600 秒&gt;</code>

<b>备份与恢复</b>
• 备份：<code>{{prefix}}sl backup</code>（发送到收藏夹）
• 恢复：回复备份文件后发送 <code>{{prefix}}sl restore confirm</code>
  恢复会覆盖现有服务器数据，请谨慎操作。

首次添加服务器时会验证登录并固定 SSH 主机指纹；密码使用 AES-256-GCM 在本地加密保存。`
