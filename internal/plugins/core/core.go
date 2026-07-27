package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type Plugin struct {
	services service.Container
	router   *command.Router
	registry *plugin.Registry
	started  time.Time
}

func New(services service.Container, router *command.Router, registry *plugin.Registry) *Plugin {
	return &Plugin{
		services: services,
		router:   router,
		registry: registry,
		started:  time.Now(),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "core",
		Version:     "0.1.0",
		Description: "TeleBox core administration commands",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{
		{
			Name:        "ping",
			Description: "检查 TeleBox 是否正在运行",
			Handler:     p.ping,
		},
		{
			Name:        "help",
			Description: "列出当前可用命令",
			Handler:     p.help,
		},
		{
			Name:        "status",
			Description: "显示 TeleBox-Go 运行状态",
			Handler:     p.status,
		},
		{
			Name:        "prefix",
			Description: "查看或修改命令前缀",
			OwnerOnly:   true,
			Handler:     p.prefix,
		},
		{
			Name:        "plugins",
			Aliases:     []string{"plugin"},
			Description: "列出插件，或使用 enable/disable 修改状态",
			OwnerOnly:   true,
			Handler:     p.plugins,
		},
	}
}

func (p *Plugin) status(ctx context.Context, request command.Request) error {
	scanStarted := time.Now()
	statuses := p.registry.List()
	enabled := 0
	for _, status := range statuses {
		if status.Enabled {
			enabled++
		}
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "N/A"
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	text := fmt.Sprintf(
		"<b>📊 TeleBox-Go 运行状态</b>\n\n"+
			"<b>🏠 主机信息</b>\n"+
			"• 主机名：<code>%s</code>\n"+
			"• 平台：<code>%s %s</code>\n"+
			"• CPU 核心：<code>%d</code>\n\n"+
			"<b>📦 版本信息</b>\n"+
			"• Go：<code>%s</code>\n"+
			"• TeleBox-Go：<code>%s</code>\n\n"+
			"<b>📈 运行资源</b>\n"+
			"• 进程内存：<code>%s</code>\n"+
			"• Goroutine：<code>%d</code>\n"+
			"• 插件：<code>%d/%d</code> 已启用\n\n"+
			"<b>⏱️ 运行状态</b>\n"+
			"• 运行时间：<code>%s</code>\n"+
			"• 扫描耗时：<code>%dms</code>",
		html.EscapeString(hostname),
		html.EscapeString(runtime.GOOS),
		html.EscapeString(runtime.GOARCH),
		runtime.NumCPU(),
		html.EscapeString(runtime.Version()),
		html.EscapeString(p.Metadata().Version),
		html.EscapeString(formatBytes(memory.Alloc)),
		runtime.NumGoroutine(),
		enabled,
		len(statuses),
		html.EscapeString(formatDuration(time.Since(p.started))),
		time.Since(scanStarted).Milliseconds(),
	)
	return p.respondHTML(ctx, request, text)
}

func (p *Plugin) prefix(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.respond(ctx, request,
			"⌨️ 当前命令前缀："+strings.Join(p.router.Prefixes(), ", "),
		)
	}
	if len(request.Args) != 1 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"prefix <新前缀>",
		)
	}

	previous := p.router.Prefixes()
	next := []string{request.Args[0]}
	if err := p.router.SetPrefixes(next); err != nil {
		return err
	}
	encoded, err := json.Marshal(next)
	if err == nil {
		err = p.services.Storage.Put(ctx, "core", "command_prefixes", encoded)
	}
	if err != nil {
		rollbackErr := p.router.SetPrefixes(previous)
		return errors.Join(err, rollbackErr)
	}
	return p.respond(ctx, request, "✅ 命令前缀已修改为 "+next[0])
}

func (p *Plugin) Start(context.Context) error {
	p.started = time.Now()
	return nil
}

func (p *Plugin) Stop(context.Context) error {
	return nil
}

func (p *Plugin) ping(ctx context.Context, request command.Request) error {
	apiStarted := time.Now()
	if _, err := p.services.Telegram.ResolveUser(ctx, "me"); err != nil {
		p.services.Logger.Warn("Telegram latency probe failed", "error", err)
		return p.respond(ctx, request, "❌ Telegram 连接不可用")
	}
	apiLatency := time.Since(apiStarted).Milliseconds()

	messageLatency := int64(0)
	if request.Message.Outgoing {
		messageStarted := time.Now()
		if _, err := p.services.Telegram.EditText(
			ctx,
			request.Message.ChatID,
			request.Message.ID,
			"🏓 Pong!",
		); err != nil {
			return err
		}
		messageLatency = time.Since(messageStarted).Milliseconds()
	}

	text := fmt.Sprintf(
		"<b>🏓 Pong!</b>\n\n"+
			"📡 API 延迟：<code>%dms</code>\n"+
			"✏️ 消息延迟：<code>%dms</code>\n\n"+
			"⏰ <i>%s</i>",
		apiLatency,
		messageLatency,
		time.Now().Format("2006/01/02 15:04:05"),
	)
	return p.respondHTML(ctx, request, text)
}

func (p *Plugin) help(ctx context.Context, request command.Request) error {
	routes := p.router.List()
	lines := make([]string, 0, len(routes)+1)
	lines = append(lines, "<b>🧭 TeleBox-Go 命令列表</b>", "")
	isOwner := p.router.IsOwner(request.Message)
	for _, route := range routes {
		if route.OwnerOnly && !isOwner {
			continue
		}
		line := "• <code>" + html.EscapeString(request.Prefix+route.Name) + "</code>"
		if route.Description != "" {
			line += "  " + html.EscapeString(route.Description)
		}
		lines = append(lines, line)
	}
	return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) plugins(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 {
		return p.changePluginState(ctx, request)
	}

	statuses := p.registry.List()
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Metadata.Name < statuses[j].Metadata.Name
	})
	enabled := 0
	for _, status := range statuses {
		if status.Enabled {
			enabled++
		}
	}
	lines := make([]string, 0, len(statuses)+3)
	lines = append(lines,
		"<b>🧩 TeleBox-Go 插件</b>",
		fmt.Sprintf("📊 已启用 <code>%d/%d</code>", enabled, len(statuses)),
		"",
	)
	for _, status := range statuses {
		state := "⏸️"
		if status.Enabled {
			state = "✅"
		}
		lines = append(lines, fmt.Sprintf(
			"%s <code>%s</code>  ·  v%s",
			state,
			html.EscapeString(status.Metadata.Name),
			html.EscapeString(status.Metadata.Version),
		))
	}
	return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) changePluginState(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"plugins <enable|disable> <插件名>",
		)
	}

	action := strings.ToLower(request.Args[0])
	name := strings.ToLower(request.Args[1])
	if name == "core" {
		return p.respond(ctx, request, "⚠️ 核心插件不能被禁用")
	}
	previous, exists := p.registry.Status(name)
	if !exists {
		return p.respond(ctx, request, "❌ 未编译该插件："+name)
	}

	var err error
	switch action {
	case "enable":
		err = p.registry.Enable(ctx, name)
	case "disable":
		err = p.registry.Disable(ctx, name)
	default:
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"plugins <enable|disable> <插件名>",
		)
	}
	if err != nil {
		return err
	}

	enabled := action == "enable"
	if err := p.services.Storage.SetPluginState(ctx, storage.PluginState{
		Name:    name,
		Enabled: enabled,
	}); err != nil {
		// Keep persistent and in-memory state aligned if the database write
		// fails. The rollback error is joined so diagnostics retain both.
		var rollbackErr error
		if previous.Enabled {
			rollbackErr = p.registry.Enable(ctx, name)
		} else {
			rollbackErr = p.registry.Disable(ctx, name)
		}
		return errors.Join(err, rollbackErr)
	}

	state := "启用"
	if action == "disable" {
		state = "禁用"
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 插件 %s 已%s", name, state))
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := p.services.Telegram.EditText(
			ctx,
			request.Message.ChatID,
			request.Message.ID,
			text,
		)
		return err
	}
	_, err := p.services.Telegram.ReplyText(
		ctx,
		request.Message.ChatID,
		request.Message.ID,
		text,
	)
	return err
}

func (p *Plugin) respondHTML(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := telegram.EditHTML(
			ctx,
			p.services.Telegram,
			request.Message.ChatID,
			request.Message.ID,
			text,
		)
		return err
	}
	_, err := telegram.ReplyHTML(
		ctx,
		p.services.Telegram,
		request.Message.ChatID,
		request.Message.ID,
		text,
	)
	return err
}

func formatBytes(value uint64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case value >= gib:
		return fmt.Sprintf("%.2f GiB", float64(value)/gib)
	case value >= mib:
		return fmt.Sprintf("%.2f MiB", float64(value)/mib)
	case value >= kib:
		return fmt.Sprintf("%.2f KiB", float64(value)/kib)
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func formatDuration(value time.Duration) string {
	value = value.Round(time.Second)
	days := value / (24 * time.Hour)
	value %= 24 * time.Hour
	hours := value / time.Hour
	value %= time.Hour
	minutes := value / time.Minute
	seconds := (value % time.Minute) / time.Second
	if days > 0 {
		return fmt.Sprintf("%d天 %d小时 %d分 %d秒", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时 %d分 %d秒", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d分 %d秒", minutes, seconds)
}
