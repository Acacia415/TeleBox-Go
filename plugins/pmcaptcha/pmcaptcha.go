package pmcaptcha

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const imageCaptchaBot = "PagerMaid_Sam_Bot"

type Plugin struct {
	services service.Container

	identityMu      sync.Mutex
	mu              sync.Mutex
	state           State
	runCtx          context.Context
	cancel          context.CancelFunc
	challengeTimers map[int64]context.CancelFunc
	floodTimer      context.CancelFunc
	selfID          int64
	imageBotID      int64
	imageBotAttempt time.Time
	usernameConfirm time.Time
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services:        services,
		state:           defaultState(),
		challengeTimers: make(map[int64]context.CancelFunc),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "pmcaptcha",
		Version:     "0.1.0",
		Description: "陌生人私聊验证、过滤与反洪水保护",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "pmcaptcha",
		Aliases:     []string{"pmc"},
		Description: "管理陌生人私聊验证",
		Usage: []string{
			"pmcaptcha",
			"pmcaptcha help [子命令]",
			"pmcaptcha status",
			"pmcaptcha settings",
			"pmcaptcha add|delete|check [用户 ID]",
		},
		HelpHTML:  guideHTML,
		OwnerOnly: true,
		Handler:   p.handleCommand,
	}}
}

func (p *Plugin) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return nil
	}
	p.state = defaultState()
	if err := p.loadState(ctx); err != nil {
		p.mu.Unlock()
		return err
	}
	p.runCtx, p.cancel = context.WithCancel(context.Background())
	p.challengeTimers = make(map[int64]context.CancelFunc)
	p.selfID = 0
	p.imageBotID = 0
	p.imageBotAttempt = time.Time{}
	p.mu.Unlock()
	p.resumeRuntime()
	return nil
}

func (p *Plugin) Stop(ctx context.Context) error {
	p.stopRuntime()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persistLocked(ctx)
}

func (p *Plugin) stopRuntime() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	for _, cancel := range p.challengeTimers {
		cancel()
	}
	p.challengeTimers = make(map[int64]context.CancelFunc)
	if p.floodTimer != nil {
		p.floodTimer()
		p.floodTimer = nil
	}
	p.cancel = nil
	p.runCtx = nil
}

func (p *Plugin) resumeRuntime() {
	p.mu.Lock()
	challenges := make([]Challenge, 0, len(p.state.Challenges))
	for _, challenge := range p.state.Challenges {
		challenges = append(challenges, challenge)
	}
	floodActive := p.state.Flood.Active
	lastFlood := p.state.Flood.LastMessageAt
	p.mu.Unlock()
	for _, challenge := range challenges {
		p.scheduleChallengeTimer(challenge)
	}
	if floodActive {
		remaining := 5*time.Minute - time.Since(lastFlood)
		if remaining < 0 {
			remaining = 0
		}
		p.scheduleFloodEnd(remaining)
	}
}

func (p *Plugin) OnMessage(ctx context.Context, message telegram.Message) error {
	if message.Edited || message.ChatID <= 0 || message.SenderID == 0 {
		return nil
	}
	if err := p.ensureTelegramIdentities(ctx); err != nil {
		p.services.Logger.Debug("PMCaptcha 读取当前账号失败", "error", err)
	}
	if p.detailedLogs() {
		p.services.Logger.Info(
			"PMCaptcha 收到私聊消息",
			"chat_id", message.ChatID,
			"sender_id", message.SenderID,
			"outgoing", message.Outgoing,
			"via_bot_id", message.ViaBotID,
		)
	}
	if p.isImageCaptchaMessage(message) {
		return p.handleImageCaptchaResult(ctx, message)
	}
	if message.Outgoing {
		return p.handleOutgoing(ctx, message)
	}
	return p.handleIncoming(ctx, message)
}

func (p *Plugin) OnEditedMessage(
	ctx context.Context,
	message telegram.Message,
) error {
	if message.ChatID <= 0 {
		return nil
	}
	if err := p.ensureTelegramIdentities(ctx); err != nil {
		p.services.Logger.Debug("PMCaptcha 读取当前账号失败", "error", err)
	}
	if !p.isImageCaptchaMessage(message) {
		return nil
	}
	return p.handleImageCaptchaResult(ctx, message)
}

func (p *Plugin) ensureTelegramIdentities(ctx context.Context) error {
	p.identityMu.Lock()
	defer p.identityMu.Unlock()

	p.mu.Lock()
	selfID := p.selfID
	imageBotID := p.imageBotID
	imageBotAttempt := p.imageBotAttempt
	p.mu.Unlock()
	if selfID == 0 {
		self, err := p.services.Telegram.ResolveUser(ctx, "me")
		if err != nil {
			return err
		}
		p.mu.Lock()
		p.selfID = self.ID
		p.mu.Unlock()
	}
	if imageBotID != 0 || time.Since(imageBotAttempt) < time.Minute {
		return nil
	}
	p.mu.Lock()
	p.imageBotAttempt = time.Now()
	p.mu.Unlock()
	imageBot, err := p.services.Telegram.ResolveUser(ctx, imageCaptchaBot)
	if err != nil {
		p.services.Logger.Debug(
			"PMCaptcha 图片验证机器人暂不可用",
			"error", err,
		)
		return nil
	}
	p.mu.Lock()
	p.imageBotID = imageBot.ID
	p.mu.Unlock()
	return nil
}

func (p *Plugin) isImageCaptchaMessage(message telegram.Message) bool {
	p.mu.Lock()
	botID := p.imageBotID
	p.mu.Unlock()
	return message.Outgoing && botID > 0 && message.ViaBotID == botID
}

func (p *Plugin) handleOutgoing(
	ctx context.Context,
	message telegram.Message,
) error {
	p.mu.Lock()
	initiative := p.state.Config.Initiative
	key := verifiedKey(message.ChatID)
	_, verified := p.state.Verified[key]
	if initiative && !verified {
		p.state.Verified[key] = time.Now()
		err := p.persistLocked(ctx)
		p.mu.Unlock()
		if err != nil {
			return err
		}
		if p.detailedLogs() {
			p.services.Logger.Info(
				"private chat user trusted after outgoing message",
				"user_id", message.ChatID,
			)
		}
		return nil
	}
	p.mu.Unlock()
	return nil
}

func (p *Plugin) handleIncoming(
	ctx context.Context,
	message telegram.Message,
) error {
	userID := message.SenderID
	p.mu.Lock()
	if _, ok := p.state.Verified[verifiedKey(userID)]; ok {
		p.mu.Unlock()
		return nil
	}
	challenge, pending := p.state.Challenges[verifiedKey(userID)]
	p.mu.Unlock()
	if pending {
		return p.verifyChallenge(ctx, message, challenge)
	}

	user, err := p.services.Telegram.ResolveUser(
		ctx,
		strconv.FormatInt(userID, 10),
	)
	if err != nil {
		return fmt.Errorf("读取私聊用户资料失败: %w", err)
	}
	skipReason := ""
	switch {
	case user.ID == p.selfID:
		skipReason = "当前登录账号"
	case user.Contact:
		skipReason = "Telegram 联系人"
	case user.MutualContact:
		skipReason = "Telegram 双向联系人"
	case user.Verified:
		skipReason = "Telegram 官方认证账号"
	case user.Bot:
		skipReason = "Telegram 机器人"
	case user.Deleted:
		skipReason = "已删除账号"
	}
	if skipReason != "" {
		if p.detailedLogs() {
			p.services.Logger.Info(
				"PMCaptcha 跳过免验证用户",
				"user_id", user.ID,
				"reason", skipReason,
				"contact", user.Contact,
				"mutual_contact", user.MutualContact,
				"telegram_verified", user.Verified,
				"bot", user.Bot,
				"deleted", user.Deleted,
			)
		}
		return nil
	}
	return p.applyRules(ctx, user, message)
}

func (p *Plugin) detailedLogs() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state.Config.DetailedLogs
}

func (p *Plugin) logRule(name string, userID int64) {
	if !p.detailedLogs() {
		return
	}
	p.services.Logger.Info(
		"PMCaptcha rule matched",
		"rule", name,
		"user_id", userID,
	)
}

func (p *Plugin) setVerified(
	ctx context.Context,
	userID int64,
	verified bool,
) (bool, error) {
	key := verifiedKey(userID)
	p.mu.Lock()
	_, existed := p.state.Verified[key]
	if verified {
		p.state.Verified[key] = time.Now()
	} else {
		delete(p.state.Verified, key)
	}
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	return existed != verified, err
}

func (p *Plugin) resolveTargetUser(
	ctx context.Context,
	request command.Request,
	arg string,
) (int64, error) {
	arg = strings.TrimSpace(arg)
	if arg != "" {
		user, err := p.services.Telegram.ResolveUser(ctx, arg)
		if err != nil {
			return 0, err
		}
		return user.ID, nil
	}
	if request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err == nil && len(messages) > 0 && messages[0].SenderID > 0 {
			return messages[0].SenderID, nil
		}
	}
	if request.Message.ChatID > 0 {
		return request.Message.ChatID, nil
	}
	return 0, errors.New("请回复目标用户的消息，或提供用户 ID")
}
