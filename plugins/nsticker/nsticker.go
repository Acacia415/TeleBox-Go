package nsticker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxPackSize = 120

var packNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "nsticker",
		Version:     "0.3.1",
		Description: "将回复的静态、动态或视频贴纸收藏到自己的贴纸包",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "sticker",
		Aliases:     []string{"s"},
		Description: "收藏回复的静态、动态或视频贴纸",
		Usage: []string{
			"s（回复贴纸）",
			"s <已有包名>",
			"s to <包名>（回复贴纸）",
			"s cancel",
		},
		HelpHTML:  stickerGuideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	var replied telegram.Message
	if request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err == nil && len(messages) > 0 {
			replied = messages[0]
		}
	}
	if replied.Sticker == nil {
		return p.configure(ctx, request)
	}
	return p.save(ctx, request, *replied.Sticker)
}

func (p *Plugin) configure(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		defaultPack, _ := p.readDefault(ctx)
		if defaultPack == "" {
			me, err := p.services.Telegram.ResolveUser(ctx, "me")
			if err == nil && me.Username != "" {
				return p.respond(ctx, request,
					"⚙️ 尚未设置默认贴纸包，将自动使用 "+
						autoPackName(me.Username, telegram.Sticker{}, 1)+" 系列包。\n\n"+
						helpText(request.Prefix))
			}
			return p.respond(ctx, request,
				"⚙️ 尚未设置默认贴纸包\n\n"+helpText(request.Prefix))
		}
		return p.respond(ctx, request,
			"⚙️ 当前默认贴纸包：https://t.me/addstickers/"+defaultPack)
	}
	switch strings.ToLower(request.Args[0]) {
	case "help", "h":
		return p.respond(ctx, request, helpText(request.Prefix))
	case "cancel":
		if err := p.services.Storage.Delete(ctx, "nsticker", "default_pack"); err != nil {
			return p.respond(ctx, request, "❌ 清除默认贴纸包失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已取消默认贴纸包")
	}
	if len(request.Args) != 1 {
		return p.respond(ctx, request, "❌ 参数错误\n\n"+helpText(request.Prefix))
	}
	packName := request.Args[0]
	if !validPackName(packName) {
		return p.respond(ctx, request, "❌ 贴纸包名称格式无效")
	}
	if _, err := p.services.Telegram.GetStickerSet(ctx, packName); err != nil {
		return p.respond(ctx, request,
			"❌ 无法访问该贴纸包；设置默认包前请先确保它存在："+friendlyError(err))
	}
	if err := p.services.Storage.Put(
		ctx, "nsticker", "default_pack", []byte(packName),
	); err != nil {
		return p.respond(ctx, request, "❌ 保存默认贴纸包失败："+err.Error())
	}
	return p.respond(ctx, request,
		"✅ 默认贴纸包已设置：https://t.me/addstickers/"+packName)
}

func (p *Plugin) save(
	ctx context.Context,
	request command.Request,
	sticker telegram.Sticker,
) error {
	if sticker.DocumentID == 0 || sticker.AccessHash == 0 ||
		len(sticker.FileReference) == 0 {
		return p.respond(ctx, request, "❌ 贴纸引用不完整，无法收藏")
	}
	if err := p.respond(ctx, request, "⏳ 收藏贴纸…"); err != nil {
		return err
	}
	me, err := p.services.Telegram.ResolveUser(ctx, "me")
	if err != nil {
		return p.respond(ctx, request, "❌ 无法获取当前账号："+err.Error())
	}
	target := ""
	if len(request.Args) == 2 && strings.EqualFold(request.Args[0], "to") {
		target = request.Args[1]
		if !validPackName(target) {
			return p.respond(ctx, request, "❌ 临时贴纸包名称格式无效")
		}
	} else if len(request.Args) > 0 {
		return p.respond(ctx, request, "❌ 参数错误\n\n"+helpText(request.Prefix))
	} else {
		target, _ = p.readDefault(ctx)
	}
	pack, create, err := p.findPack(ctx, target, me.Username, sticker)
	if err != nil {
		return p.respond(ctx, request, "❌ 查找贴纸包失败："+friendlyError(err))
	}
	if create {
		title := stickerTitle(me.Username, sticker)
		if err := p.services.Telegram.CreateStickerSet(
			ctx, me.ID, title, pack, sticker,
		); err != nil {
			return p.respond(ctx, request, "❌ 创建贴纸包失败："+friendlyError(err))
		}
	} else if err := p.services.Telegram.AddStickerToSet(ctx, pack, sticker); err != nil {
		return p.respond(ctx, request, "❌ 添加贴纸失败："+friendlyError(err))
	}
	return p.respond(ctx, request,
		"✅ 收藏成功：https://t.me/addstickers/"+pack)
}

func (p *Plugin) findPack(
	ctx context.Context,
	explicit string,
	username string,
	sticker telegram.Sticker,
) (name string, create bool, err error) {
	if explicit != "" {
		info, err := p.services.Telegram.GetStickerSet(ctx, explicit)
		if errors.Is(err, telegram.ErrStickerSetNotFound) {
			return explicit, true, nil
		}
		if err != nil {
			return "", false, err
		}
		if info.Count >= maxPackSize {
			return "", false, fmt.Errorf("%s 已满（%d/%d）",
				explicit, info.Count, maxPackSize)
		}
		return explicit, false, nil
	}
	if username == "" {
		return "", false, errors.New(
			"当前账号没有用户名，请先用 s <包名> 设置一个已有的默认包",
		)
	}
	for index := 1; index <= 50; index++ {
		candidate := autoPackName(username, sticker, index)
		info, err := p.services.Telegram.GetStickerSet(ctx, candidate)
		if errors.Is(err, telegram.ErrStickerSetNotFound) {
			return candidate, true, nil
		}
		if err != nil {
			return "", false, err
		}
		if info.Count < maxPackSize {
			return candidate, false, nil
		}
	}
	return "", false, errors.New("已检查 50 个自动贴纸包，均已满")
}

func (p *Plugin) readDefault(ctx context.Context) (string, error) {
	value, err := p.services.Storage.Get(ctx, "nsticker", "default_pack")
	return string(value), err
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

func validPackName(value string) bool {
	return packNamePattern.MatchString(value) && !strings.Contains(value, "__")
}

func autoPackName(username string, sticker telegram.Sticker, index int) string {
	username = strings.TrimPrefix(username, "@")
	suffix := ""
	if sticker.Animated {
		suffix = "_animated"
	} else if sticker.Video {
		suffix = "_video"
	}
	name := fmt.Sprintf("%s%s_%d", username, suffix, index)
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func stickerTitle(username string, sticker telegram.Sticker) string {
	title := "TeleBox 收藏"
	if username != "" {
		title = "@" + strings.TrimPrefix(username, "@") + " 的收藏"
	}
	if sticker.Animated {
		title += "（动态）"
	} else if sticker.Video {
		title += "（视频）"
	}
	return title
}

func friendlyError(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "STICKER_VIDEO_LONG"):
		return "视频贴纸时长不能超过 3 秒"
	case strings.Contains(text, "STICKER_PNG_DIMENSIONS"):
		return "静态贴纸必须有一边为 512px"
	case strings.Contains(text, "STICKERSET_INVALID"):
		return "贴纸包不存在、名称无效或不属于当前账号"
	case strings.Contains(text, "PEER_ID_INVALID"):
		return "Telegram 拒绝了账号目标，请先与 @Stickers 私聊一次"
	default:
		return text
	}
}

func helpText(prefix string) string {
	return "⭐ 贴纸收藏\n\n" +
		"回复贴纸后使用 " + prefix + "s\n" +
		prefix + "s <已有包名>  设置永久默认包\n" +
		"回复贴纸后使用 " + prefix + "s to <包名>  临时指定（不存在则创建）\n" +
		prefix + "s cancel\n\n" +
		"支持静态、TGS 动态与 WebM 视频贴纸；自动包满后会创建下一包。"
}

const stickerGuideHTML = `<b>⭐ 贴纸收藏</b>

<b>使用方法</b>
回复贴纸后发送 <code>{{prefix}}s</code>，保存到默认包；未设置默认包时使用当前账号用户名创建贴纸包
<code>{{prefix}}s &lt;已有包名&gt;</code> 设置永久默认包
回复贴纸后发送 <code>{{prefix}}s to &lt;包名&gt;</code>，本次临时保存到指定包；不存在时创建
<code>{{prefix}}s cancel</code> 取消默认包
不回复贴纸时发送 <code>{{prefix}}s</code> 查看当前设置

<b>支持类型</b>
静态贴纸、TGS 动态贴纸和 WebM 视频贴纸。贴纸包达到上限后会创建下一个分包。

<b>注意</b>
• 首次使用前需先私聊 Telegram 官方 <code>@Stickers</code> 机器人
• 贴纸包短名称只能包含字母、数字和下划线，并以字母开头
• 自动创建贴纸包需要当前账号设置 Telegram 用户名`
