package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/buildinfo"
	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmarket"
	"github.com/Acacia415/TeleBox-Go/internal/selfupdate"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

type PackageManager interface {
	Installed() ([]pluginmarket.Installed, error)
	Search(context.Context, string) ([]pluginapi.CatalogPlugin, error)
	Install(context.Context, string, string) (pluginmarket.InstallResult, error)
	InstallArchive(context.Context, []byte, string) (pluginmarket.InstallResult, error)
	Update(context.Context, string) (pluginmarket.InstallResult, error)
	UpdateAll(context.Context) ([]pluginmarket.InstallResult, error)
	Remove(context.Context, string) error
	Export(string, string) (pluginmarket.Installed, error)
	Enable(context.Context, string) error
	Disable(context.Context, string) error
}

type FrameworkUpdater interface {
	Check(context.Context) (selfupdate.Status, error)
	Update(context.Context, bool) (selfupdate.Result, error)
}

type Plugin struct {
	services   service.Container
	router     *command.Router
	registry   *plugin.Registry
	packages   PackageManager
	updater    FrameworkUpdater
	started    time.Time
	updateMu   sync.Mutex
	backupMu   sync.Mutex
	restarting atomic.Bool

	accessMu   sync.RWMutex
	sudoAccess accessState
	sureAccess accessState
}

func New(
	services service.Container,
	router *command.Router,
	registry *plugin.Registry,
	packages PackageManager,
	updater FrameworkUpdater,
) *Plugin {
	return &Plugin{
		services: services,
		router:   router,
		registry: registry,
		packages: packages,
		updater:  updater,
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
			Description: "测试 Telegram、主机和数据中心网络延迟",
			Usage: []string{
				"ping",
				"ping <IP|域名|dc1-dc5>",
				"ping all",
			},
			Handler: p.ping,
		},
		{
			Name:        "help",
			Description: "列出当前可用命令",
			Usage:       []string{"help", "help <命令>"},
			Handler:     p.help,
		},
		{
			Name:        "status",
			Description: "显示 TeleBox-Go 运行状态",
			Usage:       []string{"status"},
			Handler:     p.status,
		},
		{
			Name:        "sysinfo",
			Description: "显示主机系统、资源、磁盘和网络信息",
			Usage:       []string{"sysinfo"},
			Handler:     p.sysinfo,
		},
		{
			Name:        "update",
			Aliases:     []string{"upgrade"},
			Description: "检查并更新 TeleBox-Go 框架",
			Usage:       []string{"update", "update check", "update force|-f|--force"},
			OwnerOnly:   true,
			Handler:     p.updateFramework,
		},
		{
			Name:        "prefix",
			Description: "查看或修改命令前缀",
			Usage: []string{
				"prefix show",
				"prefix set <前缀...>",
				"prefix add <前缀>",
				"prefix remove <前缀>",
			},
			OwnerOnly: true,
			Handler:   p.prefix,
		},
		{
			Name:        "alias",
			Description: "查看和管理用户自定义命令别名",
			Usage: []string{
				"alias ls",
				"alias set <别名...> <原命令...>",
				"alias del <别名...>",
			},
			OwnerOnly: true,
			Handler:   p.alias,
		},
		{
			Name:        "exec",
			Description: "在主机上运行 Shell 命令",
			Usage:       []string{"exec <Shell命令>"},
			OwnerOnly:   true,
			Handler:     p.exec,
		},
		{
			Name:        "loglevel",
			Description: "查看或动态调整日志等级",
			Usage:       []string{"loglevel", "loglevel <debug|info|warning|error|silent>"},
			OwnerOnly:   true,
			Handler:     p.logLevel,
		},
		{
			Name:        "sendlog",
			Aliases:     []string{"logs", "log"},
			Description: "发送或清理 TeleBox-Go 日志文件",
			Usage: []string{
				"sendlog",
				"sendlog set <对话ID|@用户名|me>",
				"sendlog clean",
			},
			OwnerOnly: true,
			Handler:   p.sendLog,
		},
		{
			Name:        "bf",
			Description: "备份 TeleBox-Go 插件、配置与运行数据",
			Usage: []string{
				"bf",
				"bf all",
				"bf set <对话ID...>",
				"bf to <对话ID...>",
				"bf del <对话ID|all>",
			},
			OwnerOnly: true,
			Handler:   p.backup,
		},
		{
			Name:        "hf",
			Description: "从回复的 TeleBox-Go 备份文件安全恢复",
			Usage:       []string{"hf（回复 .tar.gz 备份文件）"},
			OwnerOnly:   true,
			Handler:     p.restore,
		},
		{
			Name:        "sudo",
			Description: "授权指定用户在允许的对话中使用 TeleBox 命令",
			Usage: []string{
				"sudo add|del <用户ID|@用户名>（也可回复用户）",
				"sudo ls",
				"sudo chat add|del [对话ID|@用户名]",
				"sudo chat ls",
			},
			OwnerOnly: true,
			Handler:   p.sudo,
		},
		{
			Name:        "sure",
			Description: "授权指定用户发送白名单消息或受控命令",
			Usage: []string{
				"sure add|del <用户ID|@用户名>（也可回复用户）",
				"sure ls",
				"sure chat add|del [对话ID|@用户名]",
				"sure chat ls",
				"sure msg add <消息>",
				"sure msg redirect <ID> [新消息]",
				"sure msg del <ID>",
				"sure msg ls",
			},
			OwnerOnly: true,
			Handler:   p.sure,
		},
		{
			Name:        "reload",
			Description: "重新启动所有已启用业务插件",
			Usage:       []string{"reload"},
			OwnerOnly:   true,
			Handler:     p.reloadPlugins,
		},
		{
			Name:        "restart",
			Aliases:     []string{"exit", "pmr"},
			Description: "重新启动 TeleBox-Go 进程",
			Usage:       []string{"restart", "exit", "pmr"},
			OwnerOnly:   true,
			Handler:     p.restartProcess,
		},
		{
			Name:        "tpm",
			Aliases:     []string{"p", "t", "plugins", "plugin"},
			Description: "安装、更新和管理插件",
			Usage: []string{
				"p ls",
				"p s [关键词]",
				"p i <插件[@版本]|all>",
				"p i（回复已编译的 ZIP/TAR.GZ 插件包）",
				"p u [插件]",
				"p rm <插件>",
				"p upload <插件>",
				"p on|off <插件>",
				"p info <插件>",
				"p doctor",
			},
			OwnerOnly: true,
			Handler:   p.plugins,
		},
	}
}

func (p *Plugin) updateFramework(ctx context.Context, request command.Request) error {
	if p.updater == nil {
		return p.respond(ctx, request, "❌ 当前构建未配置框架更新器")
	}
	if p.restarting.Load() {
		return p.respond(ctx, request, "⏳ 新版本已安装，服务正在重启")
	}
	if !p.updateMu.TryLock() {
		return p.respond(ctx, request, "⏳ 已有更新任务正在进行")
	}
	defer p.updateMu.Unlock()

	action := ""
	if len(request.Args) > 0 {
		action = strings.ToLower(strings.TrimSpace(request.Args[0]))
	}
	switch action {
	case "help", "h":
		return p.respondHTML(ctx, request, strings.Join([]string{
			"<b>⬆️ TeleBox-Go 框架更新</b>",
			"",
			"• <code>" + html.EscapeString(request.Prefix+"update") + "</code>  更新到最新正式版",
			"• <code>" + html.EscapeString(request.Prefix+"update check") + "</code>  只检查版本",
			"• <code>" + html.EscapeString(request.Prefix+"update force") + "</code>  重新安装最新正式版",
			"",
			"插件更新：<code>" + html.EscapeString(request.Prefix+"p u") + "</code>",
		}, "\n"))
	case "check":
		if len(request.Args) != 1 {
			return p.respondUpdateUsage(ctx, request)
		}
		return p.checkFrameworkUpdate(ctx, request)
	case "", "force", "-f", "--force":
		if len(request.Args) > 1 {
			return p.respondUpdateUsage(ctx, request)
		}
	default:
		return p.respondUpdateUsage(ctx, request)
	}
	if p.services.Restart == nil {
		return p.respond(ctx, request, "❌ 当前运行方式不支持自动重启")
	}

	if err := p.respond(ctx, request, "🔎 正在检查 TeleBox-Go 更新…"); err != nil {
		return err
	}
	force := action == "force" || action == "-f" || action == "--force"
	result, err := p.updater.Update(ctx, force)
	if err != nil {
		return p.respondHTML(ctx, request,
			"<b>❌ 更新失败</b>\n\n"+html.EscapeString(err.Error()))
	}
	if !result.Updated {
		return p.respondHTML(ctx, request, fmt.Sprintf(
			"<b>✅ 已是最新版本</b>\n\n当前版本：<code>%s</code>",
			html.EscapeString(result.CurrentVersion),
		))
	}

	p.restarting.Store(true)
	responseErr := p.respondHTML(ctx, request, fmt.Sprintf(
		"<b>✅ TeleBox-Go 更新完成</b>\n\n"+
			"• <code>%s</code> → <code>%s</code>\n"+
			"• SHA-256 校验通过\n"+
			"• 旧版本已保存为 <code>telebox.previous</code>\n\n"+
			"服务将在数秒后自动重启。",
		html.EscapeString(result.CurrentVersion),
		html.EscapeString(result.LatestVersion),
	))
	go func() {
		time.Sleep(2 * time.Second)
		p.services.Restart()
	}()
	return responseErr
}

func (p *Plugin) checkFrameworkUpdate(
	ctx context.Context,
	request command.Request,
) error {
	if err := p.respond(ctx, request, "🔎 正在检查 TeleBox-Go 更新…"); err != nil {
		return err
	}
	status, err := p.updater.Check(ctx)
	if err != nil {
		return p.respondHTML(ctx, request,
			"<b>❌ 检查更新失败</b>\n\n"+html.EscapeString(err.Error()))
	}
	title := "✅ 当前已是最新版本"
	if status.UpdateAvailable {
		title = "🆕 发现新版本"
	}
	text := fmt.Sprintf(
		"<b>%s</b>\n\n• 当前：<code>%s</code>\n• 最新：<code>%s</code>",
		title,
		html.EscapeString(status.CurrentVersion),
		html.EscapeString(status.LatestVersion),
	)
	if status.UpdateAvailable {
		text += "\n\n发送 <code>" +
			html.EscapeString(request.Prefix+"update") +
			"</code> 开始更新"
	}
	return p.respondHTML(ctx, request, text)
}

func (p *Plugin) respondUpdateUsage(
	ctx context.Context,
	request command.Request,
) error {
	return p.respond(ctx, request,
		"❌ 用法："+request.Prefix+"update [check|force|help]")
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

func (p *Plugin) Start(ctx context.Context) error {
	p.started = time.Now()
	p.loadAccessSettings(ctx)
	return nil
}

func (p *Plugin) Stop(context.Context) error {
	return nil
}

func (p *Plugin) help(ctx context.Context, request command.Request) error {
	routes := visibleHelpRoutes(p.router.List(), p.router.IsOwner(request.Message))
	if len(request.Args) > 0 {
		target := strings.TrimSpace(request.Args[0])
		target = strings.TrimPrefix(target, request.Prefix)
		if resolved, ok := p.router.ResolveUserAlias(target); ok {
			target = strings.Fields(resolved)[0]
		}
		if route, ok := findHelpRoute(routes, target); ok {
			return p.respondHTML(
				ctx,
				request,
				formatCommandHelp(
					request.Prefix,
					route,
					p.router.UserAliases(),
				),
			)
		}
		return p.respondHTML(ctx, request, fmt.Sprintf(
			"<b>❌ 未找到命令</b>\n\n<code>%s</code>\n\n发送 <code>%s</code> 查看命令列表",
			html.EscapeString(target),
			html.EscapeString(request.Prefix+"help"),
		))
	}
	return p.respondHTML(ctx, request, formatCommandList(request.Prefix, routes))
}

func visibleHelpRoutes(routes []command.RouteInfo, isOwner bool) []command.RouteInfo {
	result := make([]command.RouteInfo, 0, len(routes))
	for _, route := range routes {
		if route.OwnerOnly && !isOwner {
			continue
		}
		result = append(result, route)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func findHelpRoute(routes []command.RouteInfo, target string) (command.RouteInfo, bool) {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, route := range routes {
		if route.Name == target {
			return route, true
		}
		for _, alias := range route.Aliases {
			if alias == target {
				return route, true
			}
		}
	}
	return command.RouteInfo{}, false
}

func formatCommandList(prefix string, routes []command.RouteInfo) string {
	names := make([]string, 0, len(routes))
	for _, route := range routes {
		names = append(names, "<code>"+html.EscapeString(route.Name)+"</code>")
	}
	return "<b>命令列表：</b>\n" +
		strings.Join(names, ", ") +
		"\n\n发送 <code>" +
		html.EscapeString(prefix+"help <命令>") +
		"</code> 查看特定命令的帮助"
}

func formatCommandHelp(
	prefix string,
	route command.RouteInfo,
	userAliases ...map[string]string,
) string {
	lines := []string{
		"<b>命令帮助</b>\n<code>" +
			html.EscapeString(prefix+route.Name) +
			"</code>",
	}
	if route.Description != "" {
		lines = append(lines, "", html.EscapeString(route.Description))
	}
	if len(route.Usage) > 0 {
		lines = append(lines, "", "<b>用法：</b>")
		for _, usage := range route.Usage {
			lines = append(lines, "• <code>"+
				html.EscapeString(prefix+usage)+
				"</code>")
		}
	}
	if strings.TrimSpace(route.HelpHTML) != "" {
		guide := strings.ReplaceAll(
			route.HelpHTML,
			"{{prefix}}",
			html.EscapeString(prefix),
		)
		lines = append(lines, "", guide)
	}
	if len(route.Aliases) > 0 {
		aliases := make([]string, 0, len(route.Aliases))
		for _, alias := range route.Aliases {
			aliases = append(aliases, "<code>"+html.EscapeString(prefix+alias)+"</code>")
		}
		lines = append(lines, "", "别名："+strings.Join(aliases, "、"))
	}
	if len(userAliases) > 0 {
		custom := formatUserAliases(prefix, route, userAliases[0])
		if len(custom) > 0 {
			lines = append(lines, "", "自定义别名：")
			lines = append(lines, custom...)
		}
	}
	if route.OwnerOnly {
		lines = append(lines, "权限：仅所有者")
	}
	return strings.Join(lines, "\n")
}

func formatUserAliases(
	prefix string,
	route command.RouteInfo,
	aliases map[string]string,
) []string {
	routeNames := map[string]struct{}{route.Name: {}}
	for _, alias := range route.Aliases {
		routeNames[alias] = struct{}{}
	}
	names := make([]string, 0)
	for alias, target := range aliases {
		fields := strings.Fields(target)
		if len(fields) == 0 {
			continue
		}
		if _, ok := routeNames[strings.ToLower(fields[0])]; !ok {
			continue
		}
		names = append(names, alias)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, alias := range names {
		result = append(
			result,
			"• <code>"+html.EscapeString(prefix+alias)+"</code> → <code>"+
				html.EscapeString(prefix+aliases[alias])+"</code>",
		)
	}
	return result
}

func (p *Plugin) alias(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.aliasHelp(ctx, request)
	}
	switch strings.ToLower(request.Args[0]) {
	case "ls", "list":
		if len(request.Args) != 1 {
			return p.aliasHelp(ctx, request)
		}
		return p.listAliases(ctx, request)
	case "set", "add":
		alias, target, err := splitAliasSet(request.Args[1:], p.router)
		if err != nil {
			return p.respond(ctx, request, "❌ "+err.Error())
		}
		targetCommand := strings.ToLower(strings.Fields(target)[0])
		if !p.router.HasRoute(targetCommand) {
			return p.respond(
				ctx,
				request,
				"❌ 未找到原始命令："+targetCommand,
			)
		}
		next := p.router.UserAliases()
		for existing, final := range next {
			if strings.EqualFold(
				strings.Join(strings.Fields(final), " "),
				strings.Join(strings.Fields(target), " "),
			) {
				delete(next, existing)
			}
		}
		next[alias] = target
		if err := p.saveAliases(ctx, next); err != nil {
			return p.respond(ctx, request, "❌ 保存别名失败："+err.Error())
		}
		return p.respond(
			ctx,
			request,
			"✅ 命令别名已保存\n\n"+alias+" → "+target,
		)
	case "del", "delete", "rm", "remove":
		alias := strings.ToLower(
			strings.Join(strings.Fields(strings.Join(request.Args[1:], " ")), " "),
		)
		if alias == "" {
			return p.respond(ctx, request, "❌ 用法："+request.Prefix+"alias del <别名...>")
		}
		next := p.router.UserAliases()
		if _, exists := next[alias]; !exists {
			return p.respond(ctx, request, "❌ 未找到别名："+alias)
		}
		delete(next, alias)
		if err := p.saveAliases(ctx, next); err != nil {
			return p.respond(ctx, request, "❌ 删除别名失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已删除命令别名："+alias)
	default:
		return p.aliasHelp(ctx, request)
	}
}

func splitAliasSet(args []string, router *command.Router) (string, string, error) {
	if len(args) < 2 {
		return "", "", errors.New("用法：alias set <别名...> <原命令...>")
	}
	split := -1
	for index := 1; index < len(args); index++ {
		if router.HasRoute(args[index]) {
			split = index
			break
		}
	}
	if split < 1 {
		split = 1
	}
	alias := strings.ToLower(strings.Join(args[:split], " "))
	target := strings.Join(args[split:], " ")
	alias = strings.Join(strings.Fields(alias), " ")
	target = strings.Join(strings.Fields(target), " ")
	if alias == "" || target == "" {
		return "", "", errors.New("用法：alias set <别名...> <原命令...>")
	}
	return alias, target, nil
}

func (p *Plugin) saveAliases(
	ctx context.Context,
	aliases map[string]string,
) error {
	previous := p.router.UserAliases()
	if err := p.router.SetUserAliases(aliases); err != nil {
		return err
	}
	encoded, err := json.Marshal(p.router.UserAliases())
	if err != nil {
		_ = p.router.SetUserAliases(previous)
		return err
	}
	if err := p.services.Storage.Put(
		ctx,
		"core",
		"command_aliases",
		encoded,
	); err != nil {
		_ = p.router.SetUserAliases(previous)
		return err
	}
	return nil
}

func (p *Plugin) listAliases(
	ctx context.Context,
	request command.Request,
) error {
	aliases := p.router.UserAliases()
	if len(aliases) == 0 {
		return p.respond(ctx, request, "当前没有命令别名")
	}
	names := make([]string, 0, len(aliases))
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	lines := []string{"<b>🏷️ 命令别名</b>", ""}
	for _, alias := range names {
		lines = append(
			lines,
			"• <code>"+html.EscapeString(alias)+"</code> → <code>"+
				html.EscapeString(aliases[alias])+"</code>",
		)
	}
	return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) aliasHelp(
	ctx context.Context,
	request command.Request,
) error {
	prefix := html.EscapeString(request.Prefix)
	return p.respondHTML(ctx, request, strings.Join([]string{
		"<b>🏷️ 命令别名</b>",
		"",
		"• <code>" + prefix + "alias set &lt;别名...&gt; &lt;原命令...&gt;</code>",
		"• <code>" + prefix + "alias del &lt;别名...&gt;</code>",
		"• <code>" + prefix + "alias ls</code>",
		"",
		"支持多词别名和固定参数；执行时会把额外参数接在目标命令之后。",
	}, "\n"))
}

func (p *Plugin) plugins(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.listPlugins(ctx, request, false)
	}

	action := strings.ToLower(strings.TrimSpace(request.Args[0]))
	args := request.Args[1:]
	switch action {
	case "ls", "list":
		verbose := len(args) > 0 &&
			(strings.EqualFold(args[0], "-v") || strings.EqualFold(args[0], "--verbose"))
		return p.listPlugins(ctx, request, verbose)
	case "lv":
		return p.listPlugins(ctx, request, true)
	case "i", "install", "add":
		return p.installPlugin(ctx, request, args)
	case "u", "up", "update", "upgrade", "updateall", "ua":
		return p.updatePlugins(ctx, request, args)
	case "rm", "remove", "del", "uninstall", "un":
		return p.removePlugin(ctx, request, args)
	case "upload", "ul":
		return p.uploadPlugin(ctx, request, args)
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

func (p *Plugin) listPlugins(
	ctx context.Context,
	request command.Request,
	verbose bool,
) error {
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
		line := fmt.Sprintf(
			"%s <code>%s</code>  ·  v%s",
			state,
			html.EscapeString(status.Metadata.Name),
			html.EscapeString(strings.TrimPrefix(status.Metadata.Version, "v")),
		)
		if verbose && strings.TrimSpace(status.Metadata.Description) != "" {
			line += "\n  " + html.EscapeString(status.Metadata.Description)
		}
		lines = append(lines, line)
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
	if len(args) == 0 {
		return p.installRepliedPlugin(ctx, request)
	}
	if len(args) < 1 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p i <插件名[@版本]...>",
		)
	}
	name, version := splitPluginReference(args[0])
	if name == "all" {
		if len(args) != 1 {
			return p.respond(ctx, request, "❌ all 不能与其他插件名同时使用")
		}
		return p.installAllPlugins(ctx, request, version)
	}
	var (
		installed []string
		failures  []string
	)
	for _, reference := range args {
		name, version = splitPluginReference(reference)
		result, err := p.packages.Install(ctx, name, version)
		if err != nil {
			failures = append(failures, name)
			p.services.Logger.Warn("install plugin failed", "plugin", name, "error", err)
			continue
		}
		installed = append(
			installed,
			result.Installed.Manifest.Name+"@"+result.Installed.Manifest.Version,
		)
	}
	if len(installed) == 0 {
		return p.respond(ctx, request, "❌ 插件安装失败："+strings.Join(failures, "、"))
	}
	text := "✅ 已安装并启用：\n• " + strings.Join(installed, "\n• ")
	if len(failures) > 0 {
		text += "\n\n⚠️ 安装失败：" + strings.Join(failures, "、")
	}
	return p.respond(ctx, request, text)
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
	if len(args) == 0 {
		return p.respond(ctx, request,
			"❌ 用法："+request.Prefix+"p rm <插件名...|all>",
		)
	}
	if len(args) == 1 && strings.EqualFold(args[0], "all") {
		installed, err := p.packages.Installed()
		if err != nil && len(installed) == 0 {
			return p.respondPackageError(ctx, request, err)
		}
		args = args[:0]
		for _, item := range installed {
			args = append(args, item.Manifest.Name)
		}
	}
	var removed, failures []string
	for _, reference := range args {
		name, _ := splitPluginReference(reference)
		if err := p.packages.Remove(ctx, name); err != nil {
			failures = append(failures, name)
			p.services.Logger.Warn("remove plugin failed", "plugin", name, "error", err)
			continue
		}
		removed = append(removed, name)
	}
	if len(removed) == 0 {
		if len(failures) == 0 {
			return p.respond(ctx, request, "✅ 当前没有可卸载的业务插件")
		}
		return p.respond(ctx, request, "❌ 插件卸载失败："+strings.Join(failures, "、"))
	}
	text := "✅ 已卸载：" + strings.Join(removed, "、")
	if len(failures) > 0 {
		text += "\n⚠️ 卸载失败：" + strings.Join(failures, "、")
	}
	return p.respond(ctx, request, text)
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
		"• <code>" + html.EscapeString(prefix+"ls -v") + "</code>  已安装插件详情",
		"• <code>" + html.EscapeString(prefix+"s [关键词]") + "</code>  搜索插件",
		"• <code>" + html.EscapeString(prefix+"i 插件名[@版本]...") + "</code>  安装一个或多个插件",
		"• <code>" + html.EscapeString(prefix+"i") + "</code>  回复已编译插件包安装",
		"• <code>" + html.EscapeString(prefix+"i all") + "</code>  安装全部官方插件",
		"• <code>" + html.EscapeString(prefix+"u [插件名]") + "</code>  更新插件",
		"• <code>" + html.EscapeString(prefix+"rm 插件名...|all") + "</code>  卸载插件",
		"• <code>" + html.EscapeString(prefix+"upload 插件名") + "</code>  导出当前平台插件包",
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
