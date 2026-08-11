package inkstone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"path/filepath"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

var errAttachmentTooLarge = errors.New("图片或视频不能超过 25 MiB")

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 || len(value) > remaining {
		return 0, errAttachmentTooLarge
	}
	return b.buffer.Write(value)
}

func (b *cappedBuffer) Bytes() []byte { return b.buffer.Bytes() }

type preparedMedia struct {
	Kind     string
	FileName string
	MIMEType string
	Data     []byte
}

func (p *Plugin) downloadSupportedMedia(
	ctx context.Context,
	message telegram.Message,
) (preparedMedia, error) {
	kind, err := supportedMediaKind(message)
	if err != nil {
		return preparedMedia{}, err
	}
	if message.Media.Size > maxAttachmentBytes {
		return preparedMedia{}, errAttachmentTooLarge
	}
	buffer := &cappedBuffer{limit: maxAttachmentBytes}
	downloaded, err := p.services.Telegram.DownloadMedia(
		ctx,
		message.ChatID,
		message.ID,
		buffer,
	)
	if err != nil {
		if errors.Is(err, errAttachmentTooLarge) {
			return preparedMedia{}, errAttachmentTooLarge
		}
		return preparedMedia{}, fmt.Errorf("下载 Telegram 媒体失败：%w", err)
	}
	if downloaded.Size > maxAttachmentBytes || len(buffer.Bytes()) > maxAttachmentBytes {
		return preparedMedia{}, errAttachmentTooLarge
	}
	if downloaded.Kind != "" {
		probe := message
		probe.Media = &downloaded
		downloadedKind, kindErr := supportedMediaKind(probe)
		if kindErr != nil {
			return preparedMedia{}, kindErr
		}
		kind = downloadedKind
	}
	fileName := strings.TrimSpace(downloaded.FileName)
	if fileName == "" {
		fileName = strings.TrimSpace(message.Media.FileName)
	}
	mimeType := strings.TrimSpace(downloaded.MIMEType)
	if mimeType == "" {
		mimeType = strings.TrimSpace(message.Media.MIMEType)
	}
	mimeType, extension, err := normalizeMediaMIME(kind, mimeType, fileName, buffer.Bytes())
	if err != nil {
		return preparedMedia{}, err
	}
	if fileName == "" {
		fileName = "telegram-" + kind + extension
	}
	return preparedMedia{
		Kind:     kind,
		FileName: safeUploadFileName(fileName),
		MIMEType: mimeType,
		Data:     append([]byte(nil), buffer.Bytes()...),
	}, nil
}

func supportedMediaKind(message telegram.Message) (string, error) {
	if message.Sticker != nil {
		return "", errors.New("不支持写入 Telegram 贴纸")
	}
	if message.Media == nil {
		return "", errors.New("回复消息没有可写入的图片或视频")
	}
	mimeType := strings.ToLower(strings.TrimSpace(message.Media.MIMEType))
	if message.Media.Kind == telegram.MediaAnimation || mimeType == "image/gif" {
		return "", errors.New("不支持写入 GIF 或 Telegram 动图")
	}
	if message.Media.Kind == telegram.MediaSticker {
		return "", errors.New("不支持写入 Telegram 贴纸")
	}
	switch message.Media.Kind {
	case telegram.MediaPhoto:
		return "image", nil
	case telegram.MediaVideo:
		return "video", nil
	case telegram.MediaDocument:
		switch {
		case strings.HasPrefix(mimeType, "image/"):
			return "image", nil
		case strings.HasPrefix(mimeType, "video/"):
			return "video", nil
		}
	}
	return "", errors.New("当前只支持图片和普通视频，不支持贴纸、GIF 或其他文件")
}

func normalizeMediaMIME(
	kind string,
	mimeType string,
	fileName string,
	data []byte,
) (string, string, error) {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	extension := strings.ToLower(filepath.Ext(fileName))
	if kind == "image" {
		if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
			return "", "", errors.New("不支持写入 GIF 或 Telegram 动图")
		}
		if mimeType == "" || mimeType == "application/octet-stream" {
			switch {
			case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
				mimeType = "image/jpeg"
			case len(data) >= 8 && string(data[1:4]) == "PNG":
				mimeType = "image/png"
			case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
				mimeType = "image/webp"
			}
		}
		switch mimeType {
		case "image/jpeg":
			return mimeType, ".jpg", nil
		case "image/png":
			return mimeType, ".png", nil
		case "image/webp":
			return mimeType, ".webp", nil
		case "image/avif":
			return mimeType, ".avif", nil
		default:
			return "", "", errors.New("该图片格式无法在 Inkstone 中安全预览")
		}
	}

	if mimeType == "" || mimeType == "application/octet-stream" {
		switch {
		case len(data) >= 4 && data[0] == 0x1a && data[1] == 0x45 && data[2] == 0xdf && data[3] == 0xa3:
			mimeType = "video/webm"
		case len(data) >= 12 && string(data[4:8]) == "ftyp":
			if extension == ".mov" {
				mimeType = "video/quicktime"
			} else {
				mimeType = "video/mp4"
			}
		}
	}
	switch mimeType {
	case "video/mp4":
		return mimeType, ".mp4", nil
	case "video/quicktime":
		return mimeType, ".mov", nil
	case "video/webm":
		return mimeType, ".webm", nil
	default:
		return "", "", errors.New("该视频格式无法在 Inkstone 中安全播放")
	}
}

func attachmentMarkdown(attachment uploadedAttachment, kind string) string {
	if kind == "video" {
		return `<video controls preload="metadata" src="` +
			html.EscapeString(attachment.URL) + `"></video>`
	}
	label := strings.TrimSuffix(attachment.FileName, filepath.Ext(attachment.FileName))
	if strings.TrimSpace(label) == "" {
		label = "Telegram 图片"
	}
	label = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(label)
	return "![" + label + "](<" + encodeMarkdownDestination(attachment.URL) + ">)"
}
