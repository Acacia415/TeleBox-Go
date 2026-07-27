package speedlink

import (
	"bytes"
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
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
	"golang.org/x/crypto/ssh"
	_ "modernc.org/sqlite"
)

const backupVersion = 1

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
}

type Plugin struct {
	services service.Container
	assetDir string
	workDir  string

	mu        sync.Mutex
	masterKey []byte
	servers   []remoteServer
}

func New(services service.Container) *Plugin {
	assetDir := filepath.Join(services.AssetsDir, "speedlink")
	if services.AssetsDir == "" {
		assetDir = filepath.Join(os.TempDir(), "telebox-go-speedlink-assets")
	}
	return &Plugin{
		services: services,
		assetDir: assetDir,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-speedlink"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "speedlink",
		Version:     "0.1.0",
		Description: "通过 SSH 在本机或多台远程服务器运行 Ookla Speedtest",
	}
}

func (p *Plugin) Commands() []command.Definition {
	definition := func(name string) command.Definition {
		return command.Definition{
			Name:        name,
			Description: "多服务器 SSH 网络测速",
			OwnerOnly:   true,
			Handler:     p.handle,
		}
	}
	return []command.Definition{definition("speedlink"), definition("sl")}
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
	case "backup":
		return p.backup(ctx, request)
	case "restore":
		return p.restore(ctx, request)
	case "all":
		if len(p.servers) == 0 {
			return p.respond(ctx, request, "❌ 尚未配置远程服务器")
		}
		indexes := make([]int, len(p.servers))
		for index := range p.servers {
			indexes[index] = index
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

func (p *Plugin) runLocal(ctx context.Context, request command.Request) error {
	executable, err := p.findSpeedtest()
	if err != nil {
		return p.respond(ctx, request,
			"❌ 未找到官方 Ookla Speedtest CLI；请安装到 PATH 或迁移 speedlink 资产")
	}
	if err := p.respond(ctx, request, "⏳ 本机测速…"); err != nil {
		return err
	}
	output, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:    executable,
		Args:    []string{"--accept-license", "--accept-gdpr", "-f", "json"},
		Timeout: 5 * time.Minute, MaxOutput: 512 << 10,
	})
	if err != nil {
		return p.respond(ctx, request,
			"❌ 本机测速失败："+shortDetail(output.Stderr, err))
	}
	result, err := parseSpeedResult(output.Stdout)
	if err != nil {
		return p.respond(ctx, request, "❌ 解析本机测速结果失败："+err.Error())
	}
	return p.respond(ctx, request, formatSpeedResult("本机", result))
}

func (p *Plugin) runRemoteMany(
	ctx context.Context,
	request command.Request,
	indexes []int,
) error {
	if len(indexes) == 0 {
		return p.respond(ctx, request, "❌ 没有有效的测速服务器")
	}
	if err := p.respond(ctx, request, fmt.Sprintf(
		"⚡️ 准备测试 %d 台远程服务器…", len(indexes),
	)); err != nil {
		return err
	}
	var results []string
	for position, index := range indexes {
		server := p.servers[index]
		if err := p.respond(ctx, request, fmt.Sprintf(
			"⏳ [%d/%d] 测试 %s…",
			position+1, len(indexes), server.Name,
		)); err != nil {
			return err
		}
		result, err := p.runRemote(ctx, server)
		if err != nil {
			results = append(results,
				"❌ "+server.Name+"："+
					sanitizeError(err))
			continue
		}
		results = append(results, formatSpeedResult(server.Name, result))
	}
	return p.respond(ctx, request, strings.Join(results, "\n\n"))
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
	case <-time.After(5 * time.Minute):
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
	keyData, err := os.ReadFile(filepath.Join(p.assetDir, "secret.key"))
	if err != nil {
		return nil, nil
	}
	legacyKey := bytes.TrimSpace(keyData)
	if len(legacyKey) != 32 {
		return nil, errors.New("legacy speedlink key length is invalid")
	}
	var databasePath string
	for _, name := range []string{"servers.db", "servers.db.bak"} {
		candidate := filepath.Join(p.assetDir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			databasePath = candidate
			break
		}
	}
	if databasePath == "" {
		return nil, nil
	}
	database, err := sql.Open(
		"sqlite",
		"file:"+filepath.ToSlash(databasePath)+"?mode=ro",
	)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.Query(`
		SELECT name, host, port, username, auth_method, credentials
		FROM servers ORDER BY id
	`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		if server.AuthMethod == "password" {
			plain, err := decryptLegacyCredential(legacyKey, server.Credential)
			if err != nil {
				return nil, err
			}
			server.Credential, err = encryptCredential(p.masterKey, plain)
			if err != nil {
				return nil, err
			}
		}
		// Legacy code disabled host-key checking. An empty fingerprint forces
		// users to re-add the server before remote execution.
		result = append(result, server)
	}
	return result, rows.Err()
}

func (p *Plugin) findSpeedtest() (string, error) {
	names := []string{"speedtest"}
	if runtime.GOOS == "windows" {
		names = []string{"speedtest.exe", "speedtest"}
	}
	for _, name := range names {
		candidate := filepath.Join(p.assetDir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		if path, err := p.services.Tools.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", toolrunner.ErrExecutableNotFound
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

func formatSpeedResult(label string, value speedResult) string {
	connection := "IPv4"
	if strings.Contains(value.Interface.ExternalIP, ":") {
		connection = "IPv6"
	}
	return fmt.Sprintf(
		"⚡️ %s\n"+
			"运营商：%s\n"+
			"节点：%d - %s - %s\n"+
			"连接：%s - %s\n"+
			"延迟：⇔ %.3f ms  ± %.3f ms\n"+
			"速率：↓ %s  ↑ %s\n"+
			"流量：↓ %s  ↑ %s\n"+
			"时间：%s",
		label,
		fallback(value.ISP, "未知"),
		value.Server.ID,
		fallback(value.Server.Name, "未知"),
		fallback(value.Server.Location, "未知"),
		connection,
		fallback(value.Interface.Name, "未知"),
		value.Ping.Latency,
		value.Ping.Jitter,
		formatBandwidth(value.Download.Bandwidth),
		formatBandwidth(value.Upload.Bandwidth),
		formatBytes(value.Download.Bytes),
		formatBytes(value.Upload.Bytes),
		strings.Replace(strings.TrimSuffix(value.Timestamp, "Z"), "T", " ", 1),
	)
}

func formatBandwidth(bytesPerSecond float64) string {
	return formatUnit(bytesPerSecond*8, []string{"bps", "Kbps", "Mbps", "Gbps"})
}

func formatBytes(value float64) string {
	return formatUnit(value, []string{"B", "KB", "MB", "GB", "TB"})
}

func formatUnit(value float64, units []string) string {
	index := 0
	for value >= 1000 && index < len(units)-1 {
		value /= 1000
		index++
	}
	return strconv.FormatFloat(value, 'f', 2, 64) + " " + units[index]
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
		prefix + "sl  本机测速\n" +
		prefix + "sl <序号...> / all  远程测速\n" +
		prefix + "sl add <别名> <user@host:port> password <密码>\n" +
		prefix + "sl add <别名> <user@host:port> key <私钥绝对路径>\n" +
		prefix + "sl list / del <序号>\n" +
		prefix + "sl backup\n" +
		prefix + "sl restore confirm（回复备份）\n\n" +
		"首次添加会验证登录并固定 SSH 主机指纹；密码使用 AES-256-GCM 本地加密。"
}
