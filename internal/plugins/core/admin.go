package core

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

func (p *Plugin) exec(ctx context.Context, request command.Request) error {
	shellCommand := strings.TrimSpace(request.RawArgs)
	if shellCommand == "" {
		return p.respond(
			ctx,
			request,
			"❌ 用法："+request.Prefix+"exec <Shell命令>",
		)
	}
	if p.services.Tools == nil {
		return p.respond(ctx, request, "❌ 当前运行环境未配置命令执行器")
	}
	if err := p.respond(ctx, request, "⏳ 正在运行 Shell 命令…"); err != nil {
		return err
	}
	name := "/bin/sh"
	args := []string{"-c", shellCommand}
	if runtime.GOOS == "windows" {
		name = "powershell.exe"
		args = []string{
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			shellCommand,
		}
	}
	result, runErr := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:      name,
		Args:      args,
		Timeout:   60 * time.Second,
		MaxOutput: 512 << 10,
	})
	output := formatShellResult(result, runErr)
	if len([]rune(output)) <= 3500 {
		return p.respond(ctx, request, output)
	}
	return p.sendCommandOutputFile(ctx, request, output)
}

func formatShellResult(result toolrunner.Result, runErr error) string {
	var sections []string
	if strings.TrimSpace(result.Stdout) != "" {
		sections = append(sections, "Shell 输出：\n"+strings.TrimSpace(result.Stdout))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		sections = append(sections, "Shell 错误：\n"+strings.TrimSpace(result.Stderr))
	}
	if runErr != nil {
		sections = append(sections, "运行错误：\n"+runErr.Error())
	}
	if len(sections) == 0 {
		sections = append(sections, "命令执行完成，没有输出。")
	}
	if result.ExitCode >= 0 {
		sections = append(
			sections,
			fmt.Sprintf(
				"退出码：%d · 耗时：%s",
				result.ExitCode,
				result.Duration.Round(time.Millisecond),
			),
		)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		sections = append(sections, "⚠️ 输出超过 512 KiB，已截断。")
	}
	return strings.Join(sections, "\n\n")
}

func (p *Plugin) sendCommandOutputFile(
	ctx context.Context,
	request command.Request,
	output string,
) error {
	directory := filepath.Join(p.services.AssetsDir, "core", "temp")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return p.respond(ctx, request, "❌ 保存命令输出失败："+err.Error())
	}
	file, err := os.CreateTemp(directory, "exec-*.txt")
	if err != nil {
		return p.respond(ctx, request, "❌ 保存命令输出失败："+err.Error())
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.WriteString(output)
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return p.respond(ctx, request, "❌ 保存命令输出失败")
	}
	if _, err := p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:      path,
		FileName:  "shell-output.txt",
		MIMEType:  "text/plain",
		Caption:   "Shell 输出",
		ReplyToID: request.Message.ID,
		Kind:      telegram.MediaDocument,
	}); err != nil {
		return p.respond(ctx, request, "❌ 发送命令输出失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 命令执行完成，完整输出已作为文件发送。")
}

func (p *Plugin) logLevel(ctx context.Context, request command.Request) error {
	if p.services.LogLevel == nil {
		return p.respond(ctx, request, "❌ 当前构建不支持动态日志等级")
	}
	if len(request.Args) == 0 {
		return p.respondHTML(ctx, request,
			"<b>📋 当前日志等级：</b> <code>"+
				html.EscapeString(logLevelName(p.services.LogLevel.Level()))+
				"</code>",
		)
	}
	if len(request.Args) != 1 {
		return p.logLevelHelp(ctx, request)
	}
	level, name, ok := coreLogLevel(request.Args[0])
	if !ok {
		return p.logLevelHelp(ctx, request)
	}
	previous := p.services.LogLevel.Level()
	p.services.LogLevel.Set(level)
	if err := p.services.Storage.Put(
		ctx,
		"core",
		"log_level",
		[]byte(name),
	); err != nil {
		p.services.LogLevel.Set(previous)
		return p.respond(ctx, request, "❌ 保存日志等级失败："+err.Error())
	}
	return p.respondHTML(ctx, request,
		"<b>✅ 日志等级已设为：</b> <code>"+
			html.EscapeString(name)+"</code>",
	)
}

func (p *Plugin) logLevelHelp(
	ctx context.Context,
	request command.Request,
) error {
	return p.respondHTML(ctx, request,
		"<b>📝 日志等级</b>\n\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"loglevel debug</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"loglevel info</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"loglevel warning</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"loglevel error</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"loglevel silent</code>",
	)
}

func coreLogLevel(value string) (slog.Level, string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, "debug", true
	case "info":
		return slog.LevelInfo, "info", true
	case "warning", "warn":
		return slog.LevelWarn, "warning", true
	case "error", "err":
		return slog.LevelError, "error", true
	case "silent", "off":
		return slog.Level(100), "silent", true
	default:
		return 0, "", false
	}
}

func logLevelName(level slog.Level) string {
	switch {
	case level >= slog.Level(100):
		return "silent"
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warning"
	case level <= slog.LevelDebug:
		return "debug"
	default:
		return "info"
	}
}

func (p *Plugin) reloadPlugins(
	ctx context.Context,
	request command.Request,
) error {
	if err := p.respond(ctx, request, "🔄 正在重新加载业务插件…"); err != nil {
		return err
	}
	statuses := p.registry.List()
	var enabled []string
	for _, status := range statuses {
		if status.Enabled && status.Metadata.Name != "core" {
			enabled = append(enabled, status.Metadata.Name)
		}
	}
	sort.Strings(enabled)
	started := time.Now()
	var failures []string
	for _, name := range enabled {
		if err := p.registry.Disable(ctx, name); err != nil {
			failures = append(failures, name+"（停止失败）")
			continue
		}
		if err := p.registry.Enable(ctx, name); err != nil {
			failures = append(failures, name+"（启动失败）")
		}
	}
	if len(failures) > 0 {
		return p.respond(
			ctx,
			request,
			"⚠️ 插件重载完成，但以下插件失败：\n"+
				strings.Join(failures, "\n"),
		)
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf(
			"✅ 已重新加载 %d 个业务插件，耗时 %s",
			len(enabled),
			time.Since(started).Round(time.Millisecond),
		),
	)
}

func (p *Plugin) restartProcess(
	ctx context.Context,
	request command.Request,
) error {
	if p.services.Restart == nil {
		return p.respond(ctx, request, "❌ 当前运行方式不支持自动重启")
	}
	if err := p.respond(
		ctx,
		request,
		"🔄 TeleBox-Go 正在重新启动…",
	); err != nil {
		return err
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		p.services.Restart()
	}()
	return nil
}
