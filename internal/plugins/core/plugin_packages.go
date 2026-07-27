package core

import (
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxLocalPluginArchive = int64(128 << 20)

func (p *Plugin) installRepliedPlugin(
	ctx context.Context,
	request command.Request,
) error {
	if request.Message.ReplyToID <= 0 {
		return p.respondHTML(
			ctx,
			request,
			"❌ 请指定插件名，或回复 TeleBox-Go 已编译的 <code>.zip</code> / <code>.tar.gz</code> 插件包",
		)
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 || messages[0].Media == nil {
		return p.respond(ctx, request, "❌ 无法读取回复的插件包")
	}
	media := messages[0].Media
	format := localPluginFormat(media.FileName)
	if format == "" {
		return p.respond(ctx, request, "❌ 本地插件包仅支持 .zip 或 .tar.gz")
	}
	if media.Size > maxLocalPluginArchive {
		return p.respond(ctx, request, "❌ 插件包超过 128 MiB 限制")
	}
	if err := p.respond(ctx, request, "📥 正在校验并安装本地插件包…"); err != nil {
		return err
	}
	var output bytes.Buffer
	limited := &limitedBuffer{buffer: &output, remaining: maxLocalPluginArchive + 1}
	_, err = p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		limited,
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 下载插件包失败："+err.Error())
	}
	if int64(output.Len()) > maxLocalPluginArchive {
		return p.respond(ctx, request, "❌ 插件包超过 128 MiB 限制")
	}
	result, err := p.packages.InstallArchive(ctx, output.Bytes(), format)
	if err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	return p.respondHTML(ctx, request,
		"<b>✅ 本地插件已安装并启用</b>\n\n• <code>"+
			html.EscapeString(result.Installed.Manifest.Name)+"</code>\n"+
			"• 版本：<code>"+html.EscapeString(result.Installed.Manifest.Version)+"</code>\n\n"+
			"本地包由所有者直接信任，不经过官方目录校验。",
	)
}

func (p *Plugin) uploadPlugin(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 1 {
		return p.respond(ctx, request, "❌ 用法："+request.Prefix+"p upload <插件名>")
	}
	name, _ := splitPluginReference(args[0])
	temp, err := os.CreateTemp("", "telebox-plugin-*.zip")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建插件导出文件失败："+err.Error())
	}
	path := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(path)
		return p.respond(ctx, request, "❌ 创建插件导出文件失败："+err.Error())
	}
	defer os.Remove(path)
	installed, err := p.packages.Export(name, path)
	if err != nil {
		return p.respondPackageError(ctx, request, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取插件导出文件失败："+err.Error())
	}
	if _, err := p.services.Telegram.SendFile(
		ctx,
		request.Message.ChatID,
		telegram.Upload{
			Path: path,
			FileName: "telebox-plugin-" + installed.Manifest.Name + "_" +
				strings.TrimPrefix(installed.Manifest.Version, "v") + ".zip",
			MIMEType: "application/zip",
			Caption: "📦 " + installed.Manifest.Name + " " +
				installed.Manifest.Version + "\n" +
				"当前平台编译包 · " + formatBytes(uint64(info.Size())),
			ReplyToID: request.Message.ID,
			Kind:      telegram.MediaDocument,
		},
	); err != nil {
		return p.respond(ctx, request, "❌ 发送插件包失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 插件包已导出")
}

func localPluginFormat(name string) string {
	name = strings.ToLower(strings.TrimSpace(filepath.Base(name)))
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(name, ".zip"):
		return "zip"
	default:
		return ""
	}
}

type limitedBuffer struct {
	buffer    *bytes.Buffer
	remaining int64
}

func (w *limitedBuffer) Write(data []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("data exceeds configured limit")
	}
	if int64(len(data)) > w.remaining {
		data = data[:w.remaining]
	}
	written, err := w.buffer.Write(data)
	w.remaining -= int64(written)
	if err == nil && w.remaining == 0 {
		err = io.ErrShortWrite
	}
	return written, err
}
