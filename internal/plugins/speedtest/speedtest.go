package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

type messageType string

const (
	typePhoto   messageType = "photo"
	typeSticker messageType = "sticker"
	typeFile    messageType = "file"
	typeText    messageType = "txt"
)

type result struct {
	ISP    string `json:"isp"`
	Server struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Location string `json:"location"`
		Country  string `json:"country"`
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
	PacketLoss float64 `json:"packetLoss"`
	Timestamp  string  `json:"timestamp"`
	Result     struct {
		URL string `json:"url"`
	} `json:"result"`
}

type server struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Location string  `json:"location"`
	Country  string  `json:"country"`
	Distance float64 `json:"distance"`
}

type Plugin struct {
	services service.Container
	workDir  string
	mu       sync.Mutex
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-speedtest"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "speedtest",
		Version:     "0.1.0",
		Description: "通过官方 Ookla Speedtest CLI 测量网络性能",
	}
}

func (p *Plugin) Commands() []command.Definition {
	definition := func(name string) command.Definition {
		return command.Definition{
			Name:        name,
			Description: "运行 Ookla Speedtest",
			OwnerOnly:   true,
			Handler:     p.handle,
		}
	}
	return []command.Definition{definition("speedtest"), definition("st")}
}

func (p *Plugin) Start(context.Context) error {
	return os.MkdirAll(p.workDir, 0o700)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "help", "h":
			return p.respond(ctx, request, helpText(request.Prefix))
		case "list":
			return p.listServers(ctx, request, false)
		case "best":
			return p.listServers(ctx, request, true)
		case "test":
			return p.testServer(ctx, request)
		case "set":
			return p.setServer(ctx, request)
		case "clear":
			return p.clearServer(ctx, request)
		case "type":
			return p.setType(ctx, request)
		case "config":
			return p.showConfig(ctx, request)
		case "check":
			return p.check(ctx, request)
		case "diagnose":
			return p.diagnose(ctx, request)
		case "binary":
			return p.configureBinary(ctx, request)
		case "update", "fix":
			return p.respond(ctx, request,
				"ℹ️ Go 版不下载或覆盖测速二进制。\n请用 Ookla 官方软件源更新 Speedtest CLI，"+
					"然后用 "+request.Prefix+"speedtest diagnose 验证。")
		}
	}
	var serverID int
	if len(request.Args) > 0 {
		value, err := strconv.Atoi(request.Args[0])
		if err != nil || value <= 0 {
			return p.respond(ctx, request, helpText(request.Prefix))
		}
		serverID = value
	} else if value, _ := p.read(ctx, "default_server"); value != "" {
		serverID, _ = strconv.Atoi(value)
	}
	return p.run(ctx, request, serverID)
}

func (p *Plugin) run(
	ctx context.Context,
	request command.Request,
	serverID int,
) error {
	executable, err := p.findExecutable(ctx)
	if err != nil {
		return p.respond(ctx, request, installHint(request.Prefix))
	}
	if err := p.respond(ctx, request, "⏳ 网络测速…"); err != nil {
		return err
	}
	args := []string{"--accept-license", "--accept-gdpr", "-f", "json"}
	if serverID > 0 {
		args = append(args, "-s", strconv.Itoa(serverID))
	}
	commandResult, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable, Args: args, Timeout: 5 * time.Minute, MaxOutput: 512 << 10,
	})
	if err != nil {
		return p.respond(ctx, request,
			"❌ Speedtest 失败："+shortToolError(commandResult.Stderr, err))
	}
	parsed, err := parseResult(commandResult.Stdout)
	if err != nil {
		return p.respond(ctx, request, "❌ 解析 Speedtest 结果失败："+err.Error())
	}
	text := formatResult(parsed)
	preferred := typePhoto
	if value, _ := p.read(ctx, "message_type"); value != "" {
		preferred = messageType(value)
	}
	if preferred == typeText || parsed.Result.URL == "" {
		return p.respond(ctx, request, text)
	}
	if err := p.sendImageResult(ctx, request, parsed.Result.URL, text, preferred); err != nil {
		p.services.Logger.Warn("send speedtest image; falling back to text",
			"type", preferred, "error", err)
		return p.respond(ctx, request, text+"\n\n⚠️ 结果图片发送失败："+err.Error())
	}
	if request.Message.Outgoing {
		return p.services.Telegram.DeleteMessages(
			ctx, request.Message.ChatID, []int{request.Message.ID},
		)
	}
	return nil
}

func (p *Plugin) sendImageResult(
	ctx context.Context,
	request command.Request,
	resultURL string,
	caption string,
	preferred messageType,
) error {
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: strings.TrimRight(resultURL, "/") + ".png",
	})
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("测速图片 HTTP %d", response.StatusCode)
	}
	jobDir, err := os.MkdirTemp(p.workDir, "result-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(jobDir)
	path := filepath.Join(jobDir, "speedtest.png")
	if err := os.WriteFile(path, response.Body, 0o600); err != nil {
		return err
	}
	upload := telegram.Upload{
		Path:      path,
		FileName:  "speedtest.png",
		MIMEType:  "image/png",
		Caption:   caption,
		ReplyToID: request.Message.ID,
	}
	switch preferred {
	case typePhoto:
		upload.Kind = telegram.MediaPhoto
	case typeSticker:
		upload.Kind = telegram.MediaSticker
		upload.Caption = ""
		upload.StickerEmoji = "⚡️"
	case typeFile:
		upload.Kind = telegram.MediaDocument
	default:
		return errors.New("不支持的结果类型")
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, upload)
	if err == nil && preferred == typeSticker {
		_, err = p.services.Telegram.ReplyText(
			ctx, request.Message.ChatID, request.Message.ID, caption,
		)
	}
	return err
}

func (p *Plugin) listServers(
	ctx context.Context,
	request command.Request,
	bestOnly bool,
) error {
	executable, err := p.findExecutable(ctx)
	if err != nil {
		return p.respond(ctx, request, installHint(request.Prefix))
	}
	if err := p.respond(ctx, request, "⏳ 读取附近服务器…"); err != nil {
		return err
	}
	servers, err := p.servers(ctx, executable)
	if err != nil {
		return p.respond(ctx, request, "❌ 获取服务器失败："+err.Error())
	}
	limit := min(20, len(servers))
	header := "⚡️ 附近 Speedtest 服务器"
	if bestOnly {
		limit = min(3, len(servers))
		header = "🎯 推荐服务器（按距离排序）"
	}
	var text strings.Builder
	text.WriteString(header + "\n\n")
	for _, item := range servers[:limit] {
		fmt.Fprintf(&text, "%d - %s - %s", item.ID, item.Name, item.Location)
		if item.Distance > 0 {
			fmt.Fprintf(&text, "（%.1f km）", item.Distance)
		}
		text.WriteByte('\n')
	}
	return p.respond(ctx, request, strings.TrimSpace(text.String()))
}

func (p *Plugin) servers(ctx context.Context, executable string) ([]server, error) {
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:    executable,
		Args:    []string{"--accept-license", "--accept-gdpr", "-f", "json", "-L"},
		Timeout: 45 * time.Second, MaxOutput: 512 << 10,
	})
	if err != nil {
		return nil, errors.New(shortToolError(result.Stderr, err))
	}
	var payload struct {
		Servers []server `json:"servers"`
	}
	if err := json.Unmarshal([]byte(extractJSON(result.Stdout)), &payload); err != nil {
		return nil, err
	}
	if len(payload.Servers) == 0 {
		return nil, errors.New("Speedtest 未返回服务器")
	}
	return payload.Servers, nil
}

func (p *Plugin) testServer(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request,
			"用法："+request.Prefix+"speedtest test <服务器 ID>")
	}
	serverID, err := strconv.Atoi(request.Args[1])
	if err != nil || serverID <= 0 {
		return p.respond(ctx, request, "❌ 服务器 ID 无效")
	}
	executable, err := p.findExecutable(ctx)
	if err != nil {
		return p.respond(ctx, request, installHint(request.Prefix))
	}
	servers, err := p.servers(ctx, executable)
	if err != nil {
		return p.respond(ctx, request, "❌ 获取服务器列表失败："+err.Error())
	}
	for _, item := range servers {
		if item.ID == serverID {
			return p.respond(ctx, request, fmt.Sprintf(
				"✅ 服务器 %d 可用\n%s - %s",
				item.ID, item.Name, item.Location,
			))
		}
	}
	return p.respond(ctx, request, "❌ 该服务器不在当前可用列表中")
}

func (p *Plugin) setServer(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request,
			"用法："+request.Prefix+"speedtest set <服务器 ID>")
	}
	serverID, err := strconv.Atoi(request.Args[1])
	if err != nil || serverID <= 0 {
		return p.respond(ctx, request, "❌ 服务器 ID 无效")
	}
	if err := p.write(ctx, "default_server", strconv.Itoa(serverID)); err != nil {
		return p.respond(ctx, request, "❌ 保存默认服务器失败："+err.Error())
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 默认服务器已设为 %d", serverID))
}

func (p *Plugin) clearServer(ctx context.Context, request command.Request) error {
	if err := p.services.Storage.Delete(ctx, "speedtest", "default_server"); err != nil {
		return p.respond(ctx, request, "❌ 清除默认服务器失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 默认服务器已清除")
}

func (p *Plugin) setType(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request,
			"用法："+request.Prefix+"speedtest type photo/sticker/file/txt")
	}
	value := messageType(strings.ToLower(request.Args[1]))
	if !slices.Contains([]messageType{typePhoto, typeSticker, typeFile, typeText}, value) {
		return p.respond(ctx, request, "❌ 结果类型必须是 photo/sticker/file/txt")
	}
	if err := p.write(ctx, "message_type", string(value)); err != nil {
		return p.respond(ctx, request, "❌ 保存结果类型失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 结果类型已设为 "+string(value))
}

func (p *Plugin) showConfig(ctx context.Context, request command.Request) error {
	serverID, _ := p.read(ctx, "default_server")
	outputType, _ := p.read(ctx, "message_type")
	executable, err := p.findExecutable(ctx)
	if err != nil {
		executable = "未找到"
	}
	return p.respond(ctx, request,
		"⚡️ Speedtest 配置\n\n默认服务器："+fallback(serverID, "自动")+
			"\n结果类型："+fallback(outputType, string(typePhoto))+
			"\n可执行文件："+executable)
}

func (p *Plugin) check(ctx context.Context, request command.Request) error {
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: "https://www.speedtest.net/",
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 网络检查失败："+err.Error())
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return p.respond(ctx, request, fmt.Sprintf(
			"❌ Speedtest.net 返回 HTTP %d", response.StatusCode,
		))
	}
	return p.respond(ctx, request, "✅ Speedtest.net 网络连接正常")
}

func (p *Plugin) diagnose(ctx context.Context, request command.Request) error {
	executable, err := p.findExecutable(ctx)
	if err != nil {
		return p.respond(ctx, request, installHint(request.Prefix))
	}
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable, Args: []string{"--version"}, Timeout: 20 * time.Second,
	})
	if err != nil {
		return p.respond(ctx, request,
			"❌ Speedtest CLI 无法运行："+shortToolError(result.Stderr, err))
	}
	version := strings.TrimSpace(result.Stdout)
	if !strings.Contains(strings.ToLower(version), "ookla") {
		return p.respond(ctx, request,
			"⚠️ 当前 speedtest 可能不是官方 Ookla CLI：\n"+version)
	}
	return p.respond(ctx, request,
		"✅ 官方 Ookla Speedtest CLI 可用\n路径："+executable+"\n\n"+version)
}

func (p *Plugin) configureBinary(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		executable, err := p.findExecutable(ctx)
		if err != nil {
			return p.respond(ctx, request, installHint(request.Prefix))
		}
		return p.respond(ctx, request, "Speedtest CLI："+executable)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "auto") || strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "speedtest", "binary"); err != nil {
			return p.respond(ctx, request, "❌ 清除路径失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已恢复自动查找")
	}
	info, err := os.Stat(value)
	if err != nil || info.IsDir() {
		return p.respond(ctx, request, "❌ 可执行文件不存在")
	}
	if err := p.write(ctx, "binary", value); err != nil {
		return p.respond(ctx, request, "❌ 保存路径失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Speedtest CLI 路径已保存")
}

func (p *Plugin) findExecutable(ctx context.Context) (string, error) {
	if configured, _ := p.read(ctx, "binary"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
	}
	names := []string{"speedtest"}
	if runtime.GOOS == "windows" {
		names = []string{"speedtest.exe", "speedtest"}
	}
	for _, name := range names {
		if p.services.AssetsDir != "" {
			for _, candidate := range []string{
				filepath.Join(p.services.AssetsDir, "speedtest", name),
				filepath.Join(p.services.AssetsDir, "speedlink", name),
			} {
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					return candidate, nil
				}
			}
		}
		if executable, err := p.services.Tools.LookPath(name); err == nil {
			return executable, nil
		}
	}
	return "", toolrunner.ErrExecutableNotFound
}

func parseResult(output string) (result, error) {
	var parsed result
	if err := json.Unmarshal([]byte(extractJSON(output)), &parsed); err != nil {
		return result{}, err
	}
	if parsed.Server.ID <= 0 || parsed.Download.Bandwidth < 0 ||
		parsed.Upload.Bandwidth < 0 || math.IsNaN(parsed.Ping.Latency) {
		return result{}, errors.New("结果字段不完整或无效")
	}
	return parsed, nil
}

func extractJSON(output string) string {
	output = strings.TrimSpace(output)
	index := strings.IndexByte(output, '{')
	if index < 0 {
		return output
	}
	return output[index:]
}

func formatResult(value result) string {
	connection := "IPv4"
	if strings.Contains(value.Interface.ExternalIP, ":") {
		connection = "IPv6"
	}
	packetLoss := ""
	if value.PacketLoss > 0 {
		packetLoss = fmt.Sprintf("\n丢包：%.2f%%", value.PacketLoss)
	}
	return fmt.Sprintf(
		"⚡️ SPEEDTEST by OOKLA\n\n"+
			"运营商：%s\n"+
			"节点：%d - %s - %s\n"+
			"连接：%s - %s - %s\n"+
			"延迟：⇔ %.3f ms  ± %.3f ms\n"+
			"速率：↓ %s  ↑ %s\n"+
			"流量：↓ %s  ↑ %s%s\n"+
			"时间：%s",
		fallback(value.ISP, "未知"),
		value.Server.ID,
		fallback(value.Server.Name, "未知"),
		fallback(value.Server.Location, value.Server.Country),
		connection,
		fallback(value.Interface.ExternalIP, "未知"),
		fallback(value.Interface.Name, "未知"),
		value.Ping.Latency,
		value.Ping.Jitter,
		formatBandwidth(value.Download.Bandwidth),
		formatBandwidth(value.Upload.Bandwidth),
		formatBytes(value.Download.Bytes),
		formatBytes(value.Upload.Bytes),
		packetLoss,
		strings.Replace(strings.TrimSuffix(value.Timestamp, "Z"), "T", " ", 1),
	)
}

func formatBandwidth(bytesPerSecond float64) string {
	return formatUnit(bytesPerSecond*8, []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"})
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

func (p *Plugin) read(ctx context.Context, key string) (string, error) {
	value, err := p.services.Storage.Get(ctx, "speedtest", key)
	return string(value), err
}

func (p *Plugin) write(ctx context.Context, key, value string) error {
	return p.services.Storage.Put(ctx, "speedtest", key, []byte(value))
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

func shortToolError(stderr string, err error) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	runes := []rune(detail)
	if len(runes) > 500 {
		return string(runes[:500]) + "…"
	}
	return detail
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

func installHint(prefix string) string {
	return "❌ 未找到官方 Ookla Speedtest CLI\n\n" +
		"请通过 https://www.speedtest.net/apps/cli 安装，或使用 " +
		prefix + "speedtest binary <路径> 指定可执行文件。"
}

func helpText(prefix string) string {
	return "⚡️ Ookla 网络测速\n\n" +
		prefix + "speedtest [服务器 ID]\n" +
		prefix + "speedtest list / best\n" +
		prefix + "speedtest test <服务器 ID>\n" +
		prefix + "speedtest set <服务器 ID> / clear\n" +
		prefix + "speedtest type photo/sticker/file/txt\n" +
		prefix + "speedtest config / check / diagnose\n" +
		prefix + "speedtest binary <路径|auto>\n\n" +
		"Go 版仅调用官方 CLI，不静默下载或替换系统二进制。"
}
