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

	"github.com/Acacia415/TeleBox-Go/internal/buildinfo"
	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmarket"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

type PackageManager interface {
	Installed() ([]pluginmarket.Installed, error)
	Search(context.Context, string) ([]pluginapi.CatalogPlugin, error)
	Install(context.Context, string, string) (pluginmarket.InstallResult, error)
	Update(context.Context, string) (pluginmarket.InstallResult, error)
	UpdateAll(context.Context) ([]pluginmarket.InstallResult, error)
	Remove(context.Context, string) error
	Enable(context.Context, string) error
	Disable(context.Context, string) error
}

type Plugin struct {
	services service.Container
	router   *command.Router
	registry *plugin.Registry
	packages PackageManager
	started  time.Time
}

func New(
	services service.Container,
	router *command.Router,
	registry *plugin.Registry,
	packages PackageManager,
) *Plugin {
	return &Plugin{
		services: services,
		router:   router,
		registry: registry,
		packages: packages,
		started:  time.Now(),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "core",
		Version:     buildinfo.Version,
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
			Name:        "tpm",
			Aliases:     []string{"p", "t", "plugins", "plugin"},
			Description: "安装、更新和管理插件",
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
	if len(request.Args) == 0 ||
		(len(request.Args) == 1 &&
			(request.Args[0] == "show" || request.Args[0] == "ls")) {
		return p.respond(ctx, request,
			"⌨️ 命令前缀\n\n"+
				"• 当前："+strings.Join(p.router.Prefixes(), "  ")+
				"\n\n"+
				request.Prefix+"prefix set <前缀>\n"+
				request.Prefix+"prefix add <前缀>\n"+
				request.Prefix+"prefix remove <前缀>",
		)
	}

	previous := p.router.Prefixes()
	next, err := updatePrefixes(previous, request.Args)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	if err := p.router.SetPrefixes(next); err != nil {
		return p.respond(ctx, request, "❌ 前缀无效："+err.Error())
	}
	next = p.router.Prefixes()
	encoded, err := json.Marshal(next)
	if err == nil {
		err = p.services.Storage.Put(ctx, "core", "command_prefixes", encoded)
	}
	if err != nil {
		rollbackErr := p.router.SetPrefixes(previous)
		return errors.Join(err, rollbackErr)
	}
	return p.respond(ctx, request,
		"✅ 命令前缀已更新\n\n"+
			"• 当前："+strings.Join(next, "  ")+
			"\n• 示例："+next[0]+"ping",
	)
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
	if len(request.Args) == 0 {
		return p.listPlugins(ctx, request)
	}

	action := strings.ToLower(strings.TrimSpace(request.Args[0]))
	args := request.Args[1:]
	switch action {
	case "ls", "list":
		return p.listPlugins(ctx, request)
	case "i", "install", "add":
		return p.installPlugin(ctx, request, args)
	case "u", "up", "update", "upgrade":
		return p.updatePlugins(ctx, request, args)
	case "rm", "remove", "del", "uninstall":
		return p.removePlugin(ctx, request, args)
	case "s", "search", "find":
		return p.searchPlugins(ctx, request, args)
	case "on", "enable", "off", "disable":
		return p.changePluginState(ctx, request, action, args)
	case "info":
		return p.pluginInfo(ctx, request, args)
	case "doctor", "check":
		return p.pluginDoctor(ctx, request)
	case "h", "help":
		return p.pluginHelp(ctx, request)
	default:
		return p.respond(ctx, request,
			"❌ 未知操作："+action+"\n\n发送 "+
				request.Prefix+"p help 查看用法",
		)
	}
}

func (p *Plugin) listPlugins(ctx context.Context, request command.Request) error {
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
		"<b>🧩 插件列表</b>",
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
			html.EscapeString(strings.TrimPrefix(status.Metadata.Version, "v")),
		))
	}
	lines = append(lines, "", "管理插件：<code>"+
		html.EscapeString(request.Prefix+"p help")+"</code>")
	return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) installPlugin(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) < 1 || len(args) > 2 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p i <插件名>[@版本]",
		)
	}
	name, version := splitPluginReference(args[0])
	if len(args) == 2 {
		version = args[1]
	}
	if name == "all" {
		return p.installAllPlugins(ctx, request, version)
	}
	result, err := p.packages.Install(ctx, name, version)
	if err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	title := "✅ 插件已安装"
	if result.Previous != "" {
		title = "✅ 插件已更新"
	}
	return p.respondHTML(ctx, request, fmt.Sprintf(
		"<b>%s</b>\n\n• <code>%s</code>\n• 版本：<code>%s</code>",
		title,
		html.EscapeString(result.Installed.Manifest.Name),
		html.EscapeString(result.Installed.Manifest.Version),
	))
}

func (p *Plugin) installAllPlugins(
	ctx context.Context,
	request command.Request,
	version string,
) error {
	items, err := p.packages.Search(ctx, "")
	if err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	installed := 0
	var failures []string
	for _, item := range items {
		if _, err := p.packages.Install(ctx, item.Name, version); err != nil {
			failures = append(failures, item.Name)
			p.services.Logger.Error(
				"install plugin package failed",
				"plugin", item.Name,
				"error", err,
			)
			continue
		}
		installed++
	}
	if len(failures) > 0 {
		return p.respond(ctx, request, fmt.Sprintf(
			"⚠️ 已安装 %d 个，失败 %d 个：%s",
			installed,
			len(failures),
			strings.Join(failures, "、"),
		))
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 已安装并启用全部 %d 个插件",
		installed,
	))
}

func (p *Plugin) updatePlugins(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) > 1 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p u [插件名]",
		)
	}
	if len(args) == 1 {
		result, err := p.packages.Update(ctx, args[0])
		if err != nil {
			return p.respondPackageError(ctx, request, err)
		}
		if !result.Updated {
			return p.respond(ctx, request,
				"✅ "+result.Installed.Manifest.Name+" 已是最新版本 "+
					result.Installed.Manifest.Version,
			)
		}
		return p.respond(ctx, request, fmt.Sprintf(
			"✅ %s 已从 %s 更新到 %s",
			result.Installed.Manifest.Name,
			result.Previous,
			result.Installed.Manifest.Version,
		))
	}

	results, err := p.packages.UpdateAll(ctx)
	if err != nil {
		p.services.Logger.Error("update plugin packages failed", "error", err)
		return p.respond(ctx, request, fmt.Sprintf(
			"⚠️ 已完成 %d 个插件，部分更新失败，请查看日志",
			len(results),
		))
	}
	updated := 0
	for _, result := range results {
		if result.Updated {
			updated++
		}
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 检查完成：%d 个插件，%d 个已更新",
		len(results),
		updated,
	))
}

func (p *Plugin) removePlugin(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 1 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p rm <插件名>",
		)
	}
	name, _ := splitPluginReference(args[0])
	if err := p.packages.Remove(ctx, name); err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	return p.respond(ctx, request, "✅ 插件 "+name+" 已卸载")
}

func (p *Plugin) searchPlugins(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	items, err := p.packages.Search(ctx, strings.Join(args, " "))
	if err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	if len(items) == 0 {
		return p.respond(ctx, request, "🔎 没有找到匹配的插件")
	}
	lines := []string{"<b>🔎 插件仓库</b>", ""}
	for _, item := range items {
		version := "N/A"
		if latest, exists := item.Latest(); exists {
			version = latest.Version
		}
		lines = append(lines, fmt.Sprintf(
			"• <code>%s</code>  ·  %s\n  %s",
			html.EscapeString(item.Name),
			html.EscapeString(version),
			html.EscapeString(item.Description),
		))
	}
	return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) changePluginState(
	ctx context.Context,
	request command.Request,
	action string,
	args []string,
) error {
	if len(args) != 1 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p <on|off> <插件名>",
		)
	}
	name, _ := splitPluginReference(args[0])
	var err error
	enabled := action == "enable" || action == "on"
	if enabled {
		err = p.packages.Enable(ctx, name)
	} else {
		err = p.packages.Disable(ctx, name)
	}
	if err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	state := "启用"
	if !enabled {
		state = "停用"
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 插件 %s 已%s", name, state))
}

func (p *Plugin) pluginInfo(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 1 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p info <插件名>",
		)
	}
	name, _ := splitPluginReference(args[0])
	status, exists := p.registry.Status(name)
	if !exists {
		return p.respond(ctx, request, "❌ 插件未安装："+name)
	}
	state := "已停用"
	if status.Enabled {
		state = "运行中"
	}
	return p.respondHTML(ctx, request, fmt.Sprintf(
		"<b>🧩 %s</b>\n\n"+
			"• 版本：<code>%s</code>\n"+
			"• 状态：%s\n"+
			"• 说明：%s",
		html.EscapeString(status.Metadata.Name),
		html.EscapeString(status.Metadata.Version),
		state,
		html.EscapeString(status.Metadata.Description),
	))
}

func (p *Plugin) pluginDoctor(
	ctx context.Context,
	request command.Request,
) error {
	installed, err := p.packages.Installed()
	missing := make([]string, 0)
	for _, item := range installed {
		if _, exists := p.registry.Status(item.Manifest.Name); !exists {
			missing = append(missing, item.Manifest.Name)
		}
	}
	if len(missing) > 0 {
		return p.respond(ctx, request,
			"⚠️ 以下插件已安装但未加载："+strings.Join(missing, "、"),
		)
	}
	if err != nil {
		p.services.Logger.Error("inspect installed plugins failed", "error", err)
		return p.respond(ctx, request, fmt.Sprintf(
			"⚠️ 已加载 %d 个插件，插件目录中存在损坏项，请查看日志",
			len(installed),
		))
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 插件检查通过\n\n• 平台：%s/%s\n• 已安装：%d",
		runtime.GOOS,
		runtime.GOARCH,
		len(installed),
	))
}

func (p *Plugin) pluginHelp(
	ctx context.Context,
	request command.Request,
) error {
	prefix := request.Prefix + "p "
	return p.respondHTML(ctx, request, strings.Join([]string{
		"<b>🧩 插件管理</b>",
		"",
		"• <code>" + html.EscapeString(prefix+"ls") + "</code>  已安装插件",
		"• <code>" + html.EscapeString(prefix+"s [关键词]") + "</code>  搜索插件",
		"• <code>" + html.EscapeString(prefix+"i 插件名") + "</code>  安装插件",
		"• <code>" + html.EscapeString(prefix+"i all") + "</code>  安装全部官方插件",
		"• <code>" + html.EscapeString(prefix+"u [插件名]") + "</code>  更新插件",
		"• <code>" + html.EscapeString(prefix+"rm 插件名") + "</code>  卸载插件",
		"• <code>" + html.EscapeString(prefix+"on 插件名") + "</code>  启用插件",
		"• <code>" + html.EscapeString(prefix+"off 插件名") + "</code>  停用插件",
		"• <code>" + html.EscapeString(prefix+"doctor") + "</code>  检查插件",
	}, "\n"))
}

func splitPluginReference(value string) (string, string) {
	value = strings.ToLower(strings.TrimSpace(value))
	name, version, found := strings.Cut(value, "@")
	if !found {
		return name, ""
	}
	return name, strings.TrimSpace(version)
}

func (p *Plugin) respondPackageError(
	ctx context.Context,
	request command.Request,
	err error,
) error {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "插件操作失败"
	}
	return p.respond(ctx, request, "❌ "+message)
}

func updatePrefixes(current, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("缺少前缀操作")
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	switch action {
	case "set":
		if len(args) < 2 {
			return nil, errors.New("用法：prefix set <前缀>")
		}
		return append([]string(nil), args[1:]...), nil
	case "add":
		if len(args) != 2 {
			return nil, errors.New("用法：prefix add <前缀>")
		}
		for _, prefix := range current {
			if prefix == args[1] {
				return nil, fmt.Errorf("前缀 %q 已存在", args[1])
			}
		}
		return append(append([]string(nil), current...), args[1]), nil
	case "remove", "rm", "del":
		if len(args) != 2 {
			return nil, errors.New("用法：prefix remove <前缀>")
		}
		next := make([]string, 0, len(current))
		found := false
		for _, prefix := range current {
			if prefix == args[1] {
				found = true
				continue
			}
			next = append(next, prefix)
		}
		if !found {
			return nil, fmt.Errorf("未配置前缀 %q", args[1])
		}
		if len(next) == 0 {
			return nil, errors.New("至少保留一个命令前缀")
		}
		return next, nil
	default:
		// Keep the original `prefix <value>` syntax as a compatibility alias.
		if len(args) == 1 {
			return []string{args[0]}, nil
		}
		return nil, errors.New("支持 show、set、add、remove")
	}
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
