package pmcaptcha

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (p *Plugin) handleCommand(
	ctx context.Context,
	request command.Request,
) error {
	if len(request.Args) == 0 {
		return p.commandCurrent(ctx, request)
	}
	subcommand := strings.ToLower(request.Args[0])
	args := request.Args[1:]
	switch subcommand {
	case "help", "h", "search":
		return p.commandHelp(ctx, request, subcommand, args)
	case "status":
		return p.respond(ctx, request, p.statusText())
	case "version", "v", "ver":
		return p.respond(ctx, request, "PMCaptcha-Go v0.1.2")
	case "check":
		return p.commandCheck(ctx, request, firstArg(args))
	case "add":
		return p.commandAdd(ctx, request, firstArg(args))
	case "delete", "del":
		return p.commandDelete(ctx, request, firstArg(args))
	case "unstuck":
		return p.commandUnstuck(ctx, request, firstArg(args))
	case "welcome", "wel":
		return p.commandWelcome(ctx, request, args)
	case "whitelist", "wl", "whl":
		return p.commandWords(ctx, request, "whitelist", args)
	case "blacklist", "bl":
		return p.commandWords(ctx, request, "blacklist", args)
	case "timeout", "wait":
		return p.commandTimeout(ctx, request, args)
	case "disable_pm", "disablepm", "disable":
		return p.commandToggle(ctx, request, "disable_pm", args)
	case "stats":
		return p.commandStats(ctx, request, args)
	case "action", "act":
		return p.commandChoice(
			ctx,
			request,
			"action",
			args,
			[]string{"ban", "delete", "none"},
		)
	case "report":
		return p.commandToggle(ctx, request, "report", args)
	case "premium", "vip", "prem":
		return p.commandChoice(
			ctx,
			request,
			"premium",
			args,
			[]string{"allow", "ban", "only", "none"},
		)
	case "groups_in_common", "group", "groups", "common":
		return p.commandOptionalCount(ctx, request, "groups", args)
	case "chat_history", "his", "history":
		return p.commandOptionalCount(ctx, request, "history", args)
	case "initiative":
		return p.commandToggle(ctx, request, "initiative", args)
	case "silent", "quiet":
		return p.commandToggle(ctx, request, "silent", args)
	case "flood", "boom":
		return p.commandFlood(ctx, request, args)
	case "flood_username", "boom_username":
		return p.commandFloodUsername(ctx, request, args)
	case "flood_act", "boom_act":
		return p.commandChoice(
			ctx,
			request,
			"flood_action",
			args,
			[]string{"asis", "delete", "captcha", "none"},
		)
	case "custom_rule":
		return p.commandCustomRule(ctx, request, args)
	case "collect_logs", "collect", "log":
		return p.commandToggle(ctx, request, "detailed_logs", args)
	case "change_type", "type", "typ":
		return p.commandChoice(
			ctx,
			request,
			"challenge_type",
			args,
			[]string{"math", "sticker", "img"},
		)
	case "show_settings", "settings", "setting":
		return p.respond(ctx, request, p.settingsText())
	case "export_settings", "export", "export_setting":
		return p.commandExport(ctx, request)
	case "import_settings", "import_setting", "import":
		return p.commandImport(ctx, request)
	case "change_img_type", "img_type", "img_typ":
		return p.commandChoice(
			ctx,
			request,
			"image_type",
			args,
			[]string{"func", "github", "rec"},
		)
	case "img_retry_chance", "img_re":
		return p.commandImageRetries(ctx, request, args)
	case "web_configure", "web":
		return p.commandConfigCode(ctx, request, args)
	default:
		return p.respond(
			ctx,
			request,
			"未找到子命令："+subcommand+"\n使用 "+
				request.Prefix+"pmcaptcha help 查看完整说明。",
		)
	}
}

func (p *Plugin) commandCurrent(
	ctx context.Context,
	request command.Request,
) error {
	userID, err := p.resolveTargetUser(ctx, request, "")
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	p.mu.Lock()
	_, verified := p.state.Verified[verifiedKey(userID)]
	_, challenging := p.state.Challenges[verifiedKey(userID)]
	p.mu.Unlock()
	status := "未验证"
	if verified {
		status = "已验证"
	} else if challenging {
		status = "验证中"
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("🛡️ 用户 %d：%s", userID, status),
	)
}

func (p *Plugin) commandHelp(
	ctx context.Context,
	request command.Request,
	subcommand string,
	args []string,
) error {
	if subcommand != "search" && len(args) > 0 &&
		strings.EqualFold(args[0], "search") {
		return p.commandHelp(ctx, request, "search", args[1:])
	}
	if subcommand == "search" {
		term := strings.ToLower(strings.Join(args, " "))
		if term == "" {
			return p.respond(ctx, request, helpText(request.Prefix))
		}
		var found []string
		for name, text := range commandHelp {
			if strings.Contains(strings.ToLower(name+" "+text), term) {
				found = append(found, name+" — "+firstLine(text))
			}
		}
		if len(found) == 0 {
			return p.respond(ctx, request, "没有找到与“"+term+"”相关的说明。")
		}
		sort.Strings(found)
		return p.respond(ctx, request, "🔎 搜索结果\n\n"+strings.Join(found, "\n"))
	}
	if len(args) == 0 {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	name := canonicalHelpName(strings.ToLower(args[0]))
	text, exists := commandHelp[name]
	if !exists {
		return p.respond(ctx, request, "没有找到该子命令的说明。")
	}
	return p.respond(
		ctx,
		request,
		"📖 "+name+"\n\n"+strings.ReplaceAll(text, "{{prefix}}", request.Prefix),
	)
}

func (p *Plugin) commandCheck(
	ctx context.Context,
	request command.Request,
	arg string,
) error {
	userID, err := p.resolveTargetUser(ctx, request, arg)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	p.mu.Lock()
	_, verified := p.state.Verified[verifiedKey(userID)]
	p.mu.Unlock()
	status := "未通过验证"
	if verified {
		status = "已通过验证"
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("用户 %d %s。", userID, status),
	)
}

func (p *Plugin) commandAdd(
	ctx context.Context,
	request command.Request,
	arg string,
) error {
	userID, err := p.resolveTargetUser(ctx, request, arg)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	p.mu.Lock()
	challenge, challenging := p.state.Challenges[verifiedKey(userID)]
	p.mu.Unlock()
	if challenging {
		if err := p.completeChallenge(ctx, userID); err != nil {
			return p.respond(ctx, request, "❌ 放行失败："+err.Error())
		}
		p.deleteChallengeMessages(ctx, userID, challenge.MessageIDs)
		return p.respond(ctx, request, fmt.Sprintf("✅ 已放行用户 %d", userID))
	}
	changed, err := p.setVerified(ctx, userID, true)
	if err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	_ = p.services.Telegram.SetPrivateChatQuarantined(ctx, userID, false)
	if !changed {
		return p.respond(ctx, request, fmt.Sprintf("用户 %d 已在验证名单中。", userID))
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 已将用户 %d 标记为已验证", userID))
}

func (p *Plugin) commandDelete(
	ctx context.Context,
	request command.Request,
	arg string,
) error {
	userID, err := p.resolveTargetUser(ctx, request, arg)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	changed, err := p.setVerified(ctx, userID, false)
	if err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	if !changed {
		return p.respond(ctx, request, fmt.Sprintf("用户 %d 没有验证记录。", userID))
	}
	return p.respond(ctx, request, fmt.Sprintf("🗑️ 已移除用户 %d 的验证记录", userID))
}

func (p *Plugin) commandUnstuck(
	ctx context.Context,
	request command.Request,
	arg string,
) error {
	userID, err := p.resolveTargetUser(ctx, request, arg)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	p.mu.Lock()
	challenge, exists := p.state.Challenges[verifiedKey(userID)]
	if exists {
		delete(p.state.Challenges, verifiedKey(userID))
		p.cancelChallengeTimerLocked(userID)
		err = p.persistLocked(ctx)
	}
	p.mu.Unlock()
	if err != nil {
		return p.respond(ctx, request, "❌ 解除失败："+err.Error())
	}
	if !exists {
		return p.respond(ctx, request, fmt.Sprintf("用户 %d 当前没有卡住的验证。", userID))
	}
	_ = p.services.Telegram.UnblockUser(ctx, userID)
	_ = p.services.Telegram.SetPrivateChatQuarantined(ctx, userID, false)
	p.deleteChallengeMessages(ctx, userID, challenge.MessageIDs)
	return p.respond(ctx, request, fmt.Sprintf("✅ 已解除用户 %d 的验证状态", userID))
}

func (p *Plugin) commandWelcome(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	p.mu.Lock()
	current := p.state.Config.Welcome
	p.mu.Unlock()
	if len(args) == 0 {
		if current == "" {
			current = "默认：✅ 验证通过"
		}
		return p.respond(ctx, request, "当前欢迎消息：\n"+current)
	}
	value := strings.Join(args, " ")
	if value == "-c" || value == "clear" {
		value = ""
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		config.Welcome = value
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	if value == "" {
		return p.respond(ctx, request, "✅ 已恢复默认欢迎消息")
	}
	return p.respond(ctx, request, "✅ 欢迎消息已保存")
}

func (p *Plugin) commandWords(
	ctx context.Context,
	request command.Request,
	kind string,
	args []string,
) error {
	p.mu.Lock()
	var current []string
	if kind == "whitelist" {
		current = append(current, p.state.Config.WhitelistWords...)
	} else {
		current = append(current, p.state.Config.BlacklistWords...)
	}
	p.mu.Unlock()
	if len(args) == 0 {
		if len(current) == 0 {
			return p.respond(ctx, request, "当前列表为空。")
		}
		return p.respond(ctx, request, "当前关键词：\n"+strings.Join(current, ", "))
	}
	value := strings.Join(args, " ")
	words := splitWords(value)
	if value == "-c" || value == "clear" {
		words = nil
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		if kind == "whitelist" {
			config.WhitelistWords = words
		} else {
			config.BlacklistWords = words
		}
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 已保存 %d 个关键词", len(words)))
}

func (p *Plugin) commandTimeout(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	p.mu.Lock()
	currentType := p.state.Config.ChallengeType
	p.mu.Unlock()
	challengeType := currentType
	if len(args) >= 2 {
		challengeType = strings.ToLower(args[1])
	}
	if !oneOf(challengeType, "math", "sticker", "img") {
		return p.respond(ctx, request, "验证类型只能是 math、sticker 或 img。")
	}
	if len(args) == 0 {
		return p.respond(
			ctx,
			request,
			fmt.Sprintf("%s 验证超时：%d 秒", challengeType, p.timeoutFor(challengeType)),
		)
	}
	seconds := 0
	if !oneOf(strings.ToLower(args[0]), "off", "none") {
		value, err := strconv.Atoi(args[0])
		if err != nil || value < 0 {
			return p.respond(ctx, request, "超时时间必须是非负整数，或 off。")
		}
		seconds = value
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		switch challengeType {
		case "sticker":
			config.StickerTimeout = seconds
		case "img":
			config.ImageTimeout = seconds
		default:
			config.MathTimeout = seconds
		}
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	if seconds == 0 {
		return p.respond(ctx, request, "✅ 已关闭 "+challengeType+" 验证超时")
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ %s 验证超时已设为 %d 秒", challengeType, seconds),
	)
}

func (p *Plugin) commandToggle(
	ctx context.Context,
	request command.Request,
	name string,
	args []string,
) error {
	current := p.toggleValue(name)
	if len(args) == 0 {
		return p.respond(ctx, request, settingLabel(name)+"："+onOff(current))
	}
	value, ok := parseToggle(args[0])
	if !ok {
		return p.respond(ctx, request, "请使用 on/off、y/n 或 true/false。")
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		switch name {
		case "disable_pm":
			config.DisablePM = value
		case "report":
			config.Report = value
		case "initiative":
			config.Initiative = value
		case "silent":
			config.Silent = value
		case "detailed_logs":
			config.DetailedLogs = value
		}
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ "+settingLabel(name)+"："+onOff(value))
}

func (p *Plugin) commandStats(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) > 0 && oneOf(strings.ToLower(args[0]), "-c", "reset", "clear") {
		p.mu.Lock()
		p.state.Stats = Stats{}
		err := p.persistLocked(ctx)
		p.mu.Unlock()
		if err != nil {
			return p.respond(ctx, request, "❌ 重置失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 验证统计已重置")
	}
	p.mu.Lock()
	stats := p.state.Stats
	pending := len(p.state.Challenges)
	flooding := p.state.Flood.Active
	floodUsers := len(p.state.Flood.Users)
	p.mu.Unlock()
	text := fmt.Sprintf(
		"📊 PMCaptcha 统计\n\n✅ 已通过：%d\n⛔ 已处理：%d\n⏳ 验证中：%d\n🌊 洪水触发：%d",
		stats.Passed,
		stats.Banned,
		pending,
		stats.Flooded,
	)
	if flooding {
		text += fmt.Sprintf("\n\n当前正在处理私聊洪水：%d 个用户", floodUsers)
	}
	return p.respond(ctx, request, text)
}

func (p *Plugin) commandChoice(
	ctx context.Context,
	request command.Request,
	name string,
	args []string,
	allowed []string,
) error {
	current := p.choiceValue(name)
	if len(args) == 0 {
		return p.respond(ctx, request, settingLabel(name)+"："+current)
	}
	value := strings.ToLower(args[0])
	if !oneOf(value, allowed...) {
		return p.respond(
			ctx,
			request,
			"可用选项："+strings.Join(allowed, "、"),
		)
	}
	if name == "challenge_type" && value == "img" {
		p.mu.Lock()
		imageBotID := p.imageBotID
		p.mu.Unlock()
		if imageBotID == 0 {
			return p.respond(
				ctx,
				request,
				"❌ 当前无法连接图片验证机器人，请稍后重试或使用 math/sticker。",
			)
		}
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		switch name {
		case "action":
			config.Action = value
		case "premium":
			config.Premium = value
		case "flood_action":
			config.FloodAction = value
		case "challenge_type":
			config.ChallengeType = value
		case "image_type":
			config.ImageType = value
		}
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	text := "✅ " + settingLabel(name) + "：" + value
	if name == "challenge_type" && value == "img" {
		text += "\n\n图片验证由 @" + imageCaptchaBot +
			" 提供，验证消息会发送给该机器人；不需要时请改回 math 或 sticker。"
	}
	return p.respond(ctx, request, text)
}

func (p *Plugin) commandOptionalCount(
	ctx context.Context,
	request command.Request,
	name string,
	args []string,
) error {
	p.mu.Lock()
	var current *int
	if name == "groups" {
		current = p.state.Config.CommonGroups
	} else {
		current = p.state.Config.HistoryCount
	}
	p.mu.Unlock()
	if len(args) == 0 {
		if current == nil {
			return p.respond(ctx, request, settingLabel(name)+"：关闭")
		}
		return p.respond(
			ctx,
			request,
			fmt.Sprintf("%s：%d", settingLabel(name), *current),
		)
	}
	value, err := strconv.Atoi(args[0])
	if err != nil {
		return p.respond(ctx, request, "请输入整数；-1 表示关闭。")
	}
	var next *int
	if value >= 0 {
		next = &value
	} else if value != -1 {
		return p.respond(ctx, request, "请输入非负整数；-1 表示关闭。")
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		if name == "groups" {
			config.CommonGroups = next
		} else {
			config.HistoryCount = next
		}
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	if next == nil {
		return p.respond(ctx, request, "✅ "+settingLabel(name)+"已关闭")
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ %s：%d", settingLabel(name), value),
	)
}

func (p *Plugin) commandFlood(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	p.mu.Lock()
	current := p.state.Config.FloodLimit
	p.mu.Unlock()
	if len(args) == 0 {
		return p.respond(
			ctx,
			request,
			fmt.Sprintf("私聊洪水阈值：1 分钟内 %d 个陌生用户", current),
		)
	}
	value, err := strconv.Atoi(args[0])
	if err != nil || value <= 0 {
		return p.respond(ctx, request, "洪水阈值必须是大于 0 的整数。")
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		config.FloodLimit = value
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ 私聊洪水阈值已设为 %d", value),
	)
}

func (p *Plugin) commandFloodUsername(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	p.mu.Lock()
	current := p.state.Config.FloodUsername
	confirmation := p.usernameConfirm
	p.mu.Unlock()
	if len(args) == 0 {
		return p.respond(ctx, request, "洪水期间保护用户名："+onOff(current))
	}
	value, ok := parseToggle(args[0])
	if !ok {
		return p.respond(ctx, request, "请使用 on 或 off。")
	}
	if value && !current {
		confirmed := len(args) > 1 && strings.EqualFold(args[1], "confirm")
		if !confirmed && time.Since(confirmation) > 2*time.Minute {
			p.mu.Lock()
			p.usernameConfirm = time.Now()
			p.mu.Unlock()
			return p.respond(
				ctx,
				request,
				"⚠️ 开启后，私聊洪水触发时会暂时把你的公开用户名转移到临时频道，结束后再恢复。\n\n"+
					"确认开启请再次发送：\n"+
					request.Prefix+"pmcaptcha flood_username on confirm",
			)
		}
		if !confirmed {
			return p.respond(
				ctx,
				request,
				"请在命令末尾加入 confirm 以确认开启。",
			)
		}
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		config.FloodUsername = value
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	p.mu.Lock()
	p.usernameConfirm = time.Time{}
	p.mu.Unlock()
	return p.respond(ctx, request, "✅ 洪水期间保护用户名："+onOff(value))
}

func (p *Plugin) commandCustomRule(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	p.mu.Lock()
	current := p.state.Config.CustomRule
	p.mu.Unlock()
	if len(args) == 0 {
		if current == "" {
			return p.respond(ctx, request, "当前未设置自定义规则。")
		}
		return p.respond(ctx, request, "当前规则：\n"+current)
	}
	value := strings.Join(args, " ")
	if value == "-c" || value == "clear" {
		value = ""
	} else if err := validateRule(value); err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		config.CustomRule = value
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	if value == "" {
		return p.respond(ctx, request, "✅ 自定义规则已清除")
	}
	return p.respond(ctx, request, "✅ 自定义规则已保存")
}

func (p *Plugin) commandImageRetries(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	p.mu.Lock()
	current := p.state.Config.ImageMaxRetries
	p.mu.Unlock()
	if len(args) == 0 {
		return p.respond(
			ctx,
			request,
			fmt.Sprintf("图片验证最多重试：%d 次", current),
		)
	}
	value, err := strconv.Atoi(args[0])
	if err != nil || value <= 0 || value > 20 {
		return p.respond(ctx, request, "重试次数必须在 1 到 20 之间。")
	}
	if err := p.updateConfig(ctx, func(config *Config) error {
		config.ImageMaxRetries = value
		return nil
	}); err != nil {
		return p.respond(ctx, request, "❌ 保存失败："+err.Error())
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ 图片验证最多重试 %d 次", value),
	)
}

func (p *Plugin) commandExport(
	ctx context.Context,
	request command.Request,
) error {
	p.mu.Lock()
	config := cloneConfig(p.state.Config)
	p.mu.Unlock()
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	file, err := os.CreateTemp("", "pmcaptcha-settings-*.json")
	if err != nil {
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	if err := file.Close(); err != nil {
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	p.mu.Lock()
	selfID := p.selfID
	p.mu.Unlock()
	_, err = p.services.Telegram.SendFile(
		ctx,
		selfID,
		telegram.Upload{
			Path:     path,
			FileName: "pmcaptcha-settings.json",
			MIMEType: "application/json",
			Caption:  "PMCaptcha 设置备份",
		},
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 设置文件已发送到“收藏夹”")
}

func (p *Plugin) commandImport(
	ctx context.Context,
	request command.Request,
) error {
	if request.Message.ReplyToID <= 0 {
		return p.respond(
			ctx,
			request,
			"请回复 pmcaptcha-settings.json 文件后再执行此命令。",
		)
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 || messages[0].Media == nil {
		return p.respond(ctx, request, "❌ 回复的消息没有可读取的设置文件")
	}
	if messages[0].Media.Size > 1<<20 {
		return p.respond(ctx, request, "❌ 设置文件不能超过 1 MiB")
	}
	file, err := os.CreateTemp("", "pmcaptcha-import-*.json")
	if err != nil {
		return p.respond(ctx, request, "❌ 导入失败："+err.Error())
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		file,
	); err != nil {
		_ = file.Close()
		return p.respond(ctx, request, "❌ 读取设置文件失败："+err.Error())
	}
	if err := file.Close(); err != nil {
		return p.respond(ctx, request, "❌ 导入失败："+err.Error())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p.respond(ctx, request, "❌ 导入失败："+err.Error())
	}
	config, warnings, err := decodeImportedConfig(raw)
	if err != nil {
		return p.respond(ctx, request, "❌ 设置文件格式无效："+err.Error())
	}
	temp := State{Config: config}
	normalizeState(&temp)
	if err := p.replaceConfig(ctx, temp.Config); err != nil {
		return p.respond(ctx, request, "❌ 导入失败："+err.Error())
	}
	text := "✅ PMCaptcha 设置已导入"
	if len(warnings) > 0 {
		text += "\n\n注意：\n• " + strings.Join(warnings, "\n• ")
	}
	return p.respond(ctx, request, text)
}

func (p *Plugin) commandConfigCode(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) == 0 {
		p.mu.Lock()
		config := cloneConfig(p.state.Config)
		p.mu.Unlock()
		raw, err := json.Marshal(config)
		if err != nil {
			return p.respond(ctx, request, "❌ 生成配置码失败："+err.Error())
		}
		code := base64.RawURLEncoding.EncodeToString(raw)
		return p.respond(
			ctx,
			request,
			"🔐 本地配置码\n\n"+code+
				"\n\n配置码包含插件设置，请勿公开转发。它不会上传到外部网站。",
		)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(args[0]))
	if err != nil {
		return p.respond(ctx, request, "❌ 配置码无效")
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return p.respond(ctx, request, "❌ 配置码内容无效")
	}
	if config.CustomRule != "" {
		if err := validateRule(config.CustomRule); err != nil {
			return p.respond(ctx, request, "❌ "+err.Error())
		}
	}
	temp := State{Config: config}
	normalizeState(&temp)
	if err := p.replaceConfig(ctx, temp.Config); err != nil {
		return p.respond(ctx, request, "❌ 导入失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 已从配置码恢复设置")
}

func (p *Plugin) updateConfig(
	ctx context.Context,
	update func(*Config) error,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	config := cloneConfig(p.state.Config)
	if err := update(&config); err != nil {
		return err
	}
	p.state.Config = config
	return p.persistLocked(ctx)
}

func decodeImportedConfig(raw []byte) (Config, []string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, nil, err
	}
	_, hasLegacyVersion := fields["version"]
	_, hasLegacyDisable := fields["disable"]
	_, hasLegacyTimeout := fields["timeout"]
	if !hasLegacyVersion && !hasLegacyDisable && !hasLegacyTimeout {
		var config Config
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return Config{}, nil, err
		}
		if config.CustomRule != "" {
			if err := validateRule(config.CustomRule); err != nil {
				return Config{}, nil, err
			}
		}
		return config, nil, nil
	}

	config := defaultState().Config
	var warnings []string
	read := func(key string, target any) {
		value, exists := fields[key]
		if !exists {
			return
		}
		if err := json.Unmarshal(value, target); err != nil {
			warnings = append(warnings, key+" 的值无效，已使用默认值")
		}
	}
	read("welcome", &config.Welcome)
	read("whitelist", &config.WhitelistWords)
	read("blacklist", &config.BlacklistWords)
	read("timeout", &config.MathTimeout)
	read("sticker_timeout", &config.StickerTimeout)
	read("img_timeout", &config.ImageTimeout)
	read("disable", &config.DisablePM)
	read("action", &config.Action)
	read("report", &config.Report)
	read("premium", &config.Premium)
	read("groups_in_common", &config.CommonGroups)
	read("history_count", &config.HistoryCount)
	read("initiative", &config.Initiative)
	read("silent", &config.Silent)
	read("flood_limit", &config.FloodLimit)
	read("flood_username", &config.FloodUsername)
	read("flood_act", &config.FloodAction)
	read("type", &config.ChallengeType)
	read("img_type", &config.ImageType)
	read("img_max_retry", &config.ImageMaxRetries)
	read("custom_rule", &config.CustomRule)
	var collectedLogs bool
	read("collect_logs", &collectedLogs)
	if collectedLogs {
		warnings = append(
			warnings,
			"原版第三方日志上报未启用；Go 版详细本地日志仍保持关闭",
		)
	}
	if config.CustomRule != "" {
		if err := validateRule(config.CustomRule); err != nil {
			config.CustomRule = ""
			warnings = append(
				warnings,
				"原版自定义规则包含不可安全执行的语法，已保留为空",
			)
		}
	}
	return config, warnings, nil
}

func (p *Plugin) replaceConfig(ctx context.Context, config Config) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Config = cloneConfig(config)
	return p.persistLocked(ctx)
}

func (p *Plugin) respond(
	ctx context.Context,
	request command.Request,
	text string,
) error {
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

func (p *Plugin) statusText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf(
		"🛡️ PMCaptcha 运行状态\n\n"+
			"验证方式：%s\n"+
			"已验证用户：%d\n"+
			"验证中：%d\n"+
			"陌生人私聊：%s\n"+
			"举报失败用户：%s\n"+
			"反洪水：%s（阈值 %d）",
		p.state.Config.ChallengeType,
		len(p.state.Verified),
		len(p.state.Challenges),
		map[bool]string{true: "禁止", false: "允许"}[p.state.Config.DisablePM],
		onOff(p.state.Config.Report),
		map[bool]string{true: "处理中", false: "待命"}[p.state.Flood.Active],
		p.state.Config.FloodLimit,
	)
}

func (p *Plugin) settingsText() string {
	p.mu.Lock()
	config := cloneConfig(p.state.Config)
	p.mu.Unlock()
	common := "关闭"
	if config.CommonGroups != nil {
		common = strconv.Itoa(*config.CommonGroups)
	}
	history := "关闭"
	if config.HistoryCount != nil {
		history = strconv.Itoa(*config.HistoryCount)
	}
	custom := "未设置"
	if config.CustomRule != "" {
		custom = config.CustomRule
	}
	return fmt.Sprintf(
		"⚙️ PMCaptcha 设置\n\n"+
			"验证方式：%s\n"+
			"超时：math %ds / sticker %ds / img %ds\n"+
			"失败处理：%s\n"+
			"失败后举报：%s\n"+
			"禁止陌生人私聊：%s\n"+
			"Premium 规则：%s\n"+
			"共同群阈值：%s\n"+
			"历史消息阈值：%s\n"+
			"主动私聊自动放行：%s\n"+
			"安静模式：%s\n"+
			"洪水阈值：%d\n"+
			"洪水结束处理：%s\n"+
			"洪水用户名保护：%s\n"+
			"图片类型：%s（最多重试 %d 次）\n"+
			"详细本地日志：%s\n"+
			"自定义规则：%s",
		config.ChallengeType,
		config.MathTimeout,
		config.StickerTimeout,
		config.ImageTimeout,
		config.Action,
		onOff(config.Report),
		onOff(config.DisablePM),
		config.Premium,
		common,
		history,
		onOff(config.Initiative),
		onOff(config.Silent),
		config.FloodLimit,
		config.FloodAction,
		onOff(config.FloodUsername),
		config.ImageType,
		config.ImageMaxRetries,
		onOff(config.DetailedLogs),
		custom,
	)
}

func (p *Plugin) toggleValue(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch name {
	case "disable_pm":
		return p.state.Config.DisablePM
	case "report":
		return p.state.Config.Report
	case "initiative":
		return p.state.Config.Initiative
	case "silent":
		return p.state.Config.Silent
	case "detailed_logs":
		return p.state.Config.DetailedLogs
	default:
		return false
	}
}

func (p *Plugin) choiceValue(name string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch name {
	case "action":
		return p.state.Config.Action
	case "premium":
		return p.state.Config.Premium
	case "flood_action":
		return p.state.Config.FloodAction
	case "challenge_type":
		return p.state.Config.ChallengeType
	case "image_type":
		return p.state.Config.ImageType
	default:
		return ""
	}
}

func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstLine(value string) string {
	if before, _, found := strings.Cut(value, "\n"); found {
		return before
	}
	return value
}

func parseToggle(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "y", "yes", "true", "1", "开", "开启":
		return true, true
	case "off", "n", "no", "false", "0", "关", "关闭":
		return false, true
	default:
		return false, false
	}
}

func onOff(value bool) string {
	if value {
		return "开启"
	}
	return "关闭"
}

func settingLabel(name string) string {
	labels := map[string]string{
		"disable_pm":     "禁止陌生人私聊",
		"report":         "失败后举报",
		"initiative":     "主动私聊自动放行",
		"silent":         "安静模式",
		"detailed_logs":  "详细本地日志",
		"action":         "验证失败处理",
		"premium":        "Premium 规则",
		"flood_action":   "洪水结束处理",
		"challenge_type": "验证方式",
		"image_type":     "图片验证类型",
		"groups":         "共同群阈值",
		"history":        "历史消息阈值",
	}
	if label := labels[name]; label != "" {
		return label
	}
	return name
}

func canonicalHelpName(name string) string {
	aliases := map[string]string{
		"h":              "help",
		"del":            "delete",
		"wel":            "welcome",
		"wl":             "whitelist",
		"whl":            "whitelist",
		"bl":             "blacklist",
		"wait":           "timeout",
		"disablepm":      "disable_pm",
		"disable":        "disable_pm",
		"act":            "action",
		"vip":            "premium",
		"prem":           "premium",
		"group":          "groups_in_common",
		"groups":         "groups_in_common",
		"common":         "groups_in_common",
		"his":            "chat_history",
		"history":        "chat_history",
		"quiet":          "silent",
		"boom":           "flood",
		"boom_username":  "flood_username",
		"boom_act":       "flood_act",
		"collect":        "collect_logs",
		"log":            "collect_logs",
		"type":           "change_type",
		"typ":            "change_type",
		"settings":       "show_settings",
		"setting":        "show_settings",
		"export":         "export_settings",
		"export_setting": "export_settings",
		"import":         "import_settings",
		"import_setting": "import_settings",
		"img_type":       "change_img_type",
		"img_typ":        "change_img_type",
		"img_re":         "img_retry_chance",
		"web":            "web_configure",
	}
	if canonical := aliases[name]; canonical != "" {
		return canonical
	}
	return name
}

var commandHelp = map[string]string{
	"help": "查看命令总览，或查询某个子命令。\n" +
		"用法：{{prefix}}pmcaptcha help [子命令]\n" +
		"示例：{{prefix}}pmcaptcha help report",
	"search": "按关键词搜索设置项。\n" +
		"用法：{{prefix}}pmcaptcha search <关键词>\n" +
		"示例：{{prefix}}pmcaptcha search 举报",
	"status":  "查看插件是否启用、当前验证方式和等待验证人数。\n用法：{{prefix}}pmcaptcha status",
	"version": "查看插件版本。\n用法：{{prefix}}pmcaptcha version",
	"check": "查看某个用户是否已经通过验证。可以回复对方的消息、填写用户 ID，或在对方私聊中直接使用。\n" +
		"用法：{{prefix}}pmcaptcha check [用户 ID]",
	"add": "手动放行用户，并记为已验证。若对方正在验证，会立即结束验证。\n" +
		"用法：{{prefix}}pmcaptcha add [用户 ID]",
	"delete": "删除用户的已验证记录。对方下次私聊时会重新接受检查。\n" +
		"用法：{{prefix}}pmcaptcha delete [用户 ID]",
	"unstuck": "解除异常中断或一直未结束的验证，同时撤销临时封锁、归档和静音。\n" +
		"用法：{{prefix}}pmcaptcha unstuck [用户 ID]",
	"welcome": "设置验证通过后发给对方的消息。不带内容可查看当前设置，-c 恢复默认。\n" +
		"用法：{{prefix}}pmcaptcha welcome <消息|-c>",
	"whitelist": "首条消息包含白名单词时直接放行。多个词用英文逗号分隔，-c 清空。\n" +
		"用法：{{prefix}}pmcaptcha whitelist <词1,词2|-c>",
	"blacklist": "首条消息包含黑名单词时直接按失败设置处理。多个词用英文逗号分隔，-c 清空。\n" +
		"用法：{{prefix}}pmcaptcha blacklist <词1,词2|-c>",
	"timeout": "设置验证限时，也可以分别设置数学、贴纸和图片验证。off 表示不限时。\n" +
		"用法：{{prefix}}pmcaptcha timeout <秒|off> [math|sticker|img]\n" +
		"示例：{{prefix}}pmcaptcha timeout 120 math",
	"disable_pm": "开启后，陌生人的私聊不会出现验证码，而是直接按失败设置处理。联系人和已验证用户不受影响。\n" +
		"用法：{{prefix}}pmcaptcha disable_pm <on|off>",
	"stats": "查看通过、失败和洪水拦截数量；加 reset 可清零统计。\n" +
		"用法：{{prefix}}pmcaptcha stats [reset]",
	"action": "设置验证失败后的处理方式：ban 仅拉黑，delete 拉黑并删除私聊，none 不处理。\n" +
		"用法：{{prefix}}pmcaptcha action <ban|delete|none>",
	"report": "设置验证失败后是否向 Telegram 举报垃圾消息。此项与 action 分开控制。\n" +
		"用法：{{prefix}}pmcaptcha report <on|off>",
	"premium": "设置 Telegram Premium 用户的处理方式：allow 直接放行，ban 直接拦截，only 只允许 Premium 用户，none 不特殊处理。\n" +
		"用法：{{prefix}}pmcaptcha premium <allow|ban|only|none>",
	"groups_in_common": "与对方的共同群达到指定数量时直接放行，-1 关闭这项规则。\n" +
		"用法：{{prefix}}pmcaptcha groups <数量|-1>",
	"chat_history": "与对方已有的历史消息达到指定数量时直接放行，-1 关闭这项规则。\n" +
		"用法：{{prefix}}pmcaptcha history <数量|-1>",
	"initiative": "开启后，你主动给对方发私聊时会自动放行对方。\n" +
		"用法：{{prefix}}pmcaptcha initiative <on|off>",
	"silent": "开启后，不再向对方发送验证成功或失败的结果消息。验证码仍会正常发送。\n" +
		"用法：{{prefix}}pmcaptcha silent <on|off>",
	"flood": "设置反洪水阈值。一分钟内出现达到该数量的陌生私聊时，插件会进入洪水保护。\n" +
		"用法：{{prefix}}pmcaptcha flood <数量>",
	"flood_username": "洪水期间临时移走你的公开用户名，洪水结束后恢复。此操作会修改账号用户名，开启时必须加 confirm。\n" +
		"用法：{{prefix}}pmcaptcha flood_username on confirm\n" +
		"关闭：{{prefix}}pmcaptcha flood_username off",
	"flood_act": "设置洪水结束后的处理：asis 按失败设置处理，delete 删除并举报，captcha 逐个验证，none 不处理。\n" +
		"用法：{{prefix}}pmcaptcha flood_act <asis|delete|captcha|none>",
	"custom_rule": "设置自定义放行条件。条件命中后不再执行后续检查，-c 清除。\n" +
		"字段：text、user.id、user.username、user.premium、user.verified、user.contact、user.mutual_contact、message.has_media、message.has_sticker\n" +
		"函数：contains、has_prefix、has_suffix、equals_fold、matches\n" +
		"示例：{{prefix}}pmcaptcha custom_rule contains(text, \"订单\") && user.premium",
	"collect_logs": "开启详细日志，排查私聊没有触发验证等问题。日志只保存在本机。\n" +
		"用法：{{prefix}}pmcaptcha collect_logs <on|off>",
	"change_type": "设置验证码类型：math 数学题，sticker 发送贴纸，img 图片验证。图片验证需要使用 @PagerMaid_Sam_Bot。\n" +
		"用法：{{prefix}}pmcaptcha type <math|sticker|img>",
	"show_settings":   "查看当前全部设置。\n用法：{{prefix}}pmcaptcha settings",
	"export_settings": "将当前设置导出为 JSON 文件并发送到收藏夹。\n用法：{{prefix}}pmcaptcha export_settings",
	"import_settings": "回复设置文件后执行该命令即可导入，也支持原版 .pmc-settings.json。\n用法：{{prefix}}pmcaptcha import_settings",
	"change_img_type": "设置图片验证模式：func、github 或 rec。只在 type 设为 img 时生效。\n" +
		"用法：{{prefix}}pmcaptcha img_type <func|github|rec>",
	"img_retry_chance": "设置图片验证失败后的最大重试次数，可填 1 至 20。\n" +
		"用法：{{prefix}}pmcaptcha img_retry_chance <1-20>",
	"web_configure": "将设置生成配置码，或从配置码恢复。配置码在本机处理，不会上传到网站。\n" +
		"导出：{{prefix}}pmcaptcha web\n" +
		"导入：{{prefix}}pmcaptcha web <配置码>",
}

func helpText(prefix string) string {
	return "🛡️ PMCaptcha 私聊验证\n\n" +
		"陌生人发来私聊时，先完成验证才能继续联系你。联系人、已放行用户和符合放行规则的用户不会收到验证码。\n\n" +
		"快速设置\n" +
		prefix + "pmcaptcha type math  设置数学验证\n" +
		prefix + "pmcaptcha timeout 120 math  限时 120 秒\n" +
		prefix + "pmcaptcha action ban  失败后拉黑\n" +
		prefix + "pmcaptcha report on  失败后举报\n" +
		prefix + "pmcaptcha settings  查看当前设置\n\n" +
		"用户管理\n" +
		prefix + "pmcaptcha check [用户]  查看验证状态\n" +
		prefix + "pmcaptcha add [用户]  手动放行\n" +
		prefix + "pmcaptcha delete [用户]  取消放行\n" +
		prefix + "pmcaptcha unstuck [用户]  解除卡住的验证\n" +
		"以上命令可回复对方消息使用，也可填写用户 ID。\n\n" +
		"常用规则\n" +
		prefix + "pmcaptcha initiative on  主动私聊时自动放行\n" +
		prefix + "pmcaptcha whitelist <词1,词2>  命中后放行\n" +
		prefix + "pmcaptcha blacklist <词1,词2>  命中后拦截\n" +
		prefix + "pmcaptcha groups <数量|-1>  按共同群放行\n" +
		prefix + "pmcaptcha history <数量|-1>  按历史消息放行\n\n" +
		"查询某项设置：" + prefix + "pmcaptcha help <子命令>\n" +
		"例如：" + prefix + "pmcaptcha help report\n" +
		"搜索设置：" + prefix + "pmcaptcha search <关键词>"
}

const guideHTML = `<b>🛡️ PMCaptcha 私聊验证</b>

陌生人发来私聊时，先完成验证才能继续联系你。联系人、已放行用户和符合放行规则的用户不会收到验证码。

<b>快速设置</b>
<code>{{prefix}}pmcaptcha type math</code> 使用数学验证
<code>{{prefix}}pmcaptcha timeout 120 math</code> 限时 120 秒
<code>{{prefix}}pmcaptcha action ban</code> 失败后拉黑
<code>{{prefix}}pmcaptcha report on</code> 失败后举报
<code>{{prefix}}pmcaptcha settings</code> 查看当前设置

<b>用户管理</b>
<code>{{prefix}}pmcaptcha check [用户]</code> 查看验证状态
<code>{{prefix}}pmcaptcha add [用户]</code> 手动放行
<code>{{prefix}}pmcaptcha delete [用户]</code> 取消放行
<code>{{prefix}}pmcaptcha unstuck [用户]</code> 解除卡住的验证
这些命令可以回复对方消息使用，也可以填写用户 ID。

<b>常用规则</b>
<code>{{prefix}}pmcaptcha initiative on</code> 主动私聊时自动放行
<code>{{prefix}}pmcaptcha whitelist &lt;词1,词2&gt;</code> 命中后放行
<code>{{prefix}}pmcaptcha blacklist &lt;词1,词2&gt;</code> 命中后拦截
<code>{{prefix}}pmcaptcha groups &lt;数量|-1&gt;</code> 按共同群放行
<code>{{prefix}}pmcaptcha history &lt;数量|-1&gt;</code> 按历史消息放行

<b>更多设置</b>
<code>{{prefix}}pmcaptcha help</code> 查看插件说明
<code>{{prefix}}pmcaptcha help &lt;子命令&gt;</code> 查询某项设置
<code>{{prefix}}pmcaptcha search &lt;关键词&gt;</code> 搜索设置

例如：<code>{{prefix}}pmcaptcha help report</code>`
