package pmcaptcha

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (p *Plugin) applyRules(
	ctx context.Context,
	user telegram.User,
	message telegram.Message,
) error {
	p.mu.Lock()
	config := cloneConfig(p.state.Config)
	p.mu.Unlock()

	if config.CustomRule != "" {
		matched, err := evaluateRule(config.CustomRule, ruleContext{
			Text:    message.Text,
			User:    user,
			Message: message,
		})
		if err != nil {
			p.services.Logger.Warn(
				"evaluate PMCaptcha custom rule",
				"error", err,
			)
		} else if matched {
			p.logRule("custom_rule", user.ID)
			return nil
		}
	}
	if config.DisablePM {
		p.logRule("disable_pm", user.ID)
		return p.punishDirect(ctx, user.ID, config, false)
	}
	if config.HistoryCount != nil && *config.HistoryCount > 0 {
		history, err := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
			ChatID: user.ID,
			Limit:  *config.HistoryCount + 1,
		})
		if err == nil {
			count := 0
			for _, item := range history {
				if item.ID != message.ID {
					count++
				}
			}
			if count >= *config.HistoryCount {
				p.logRule("chat_history", user.ID)
				_, err = p.setVerified(ctx, user.ID, true)
				return err
			}
		}
	}
	if config.CommonGroups != nil && user.CommonChats >= *config.CommonGroups {
		p.logRule("common_groups", user.ID)
		_, err := p.setVerified(ctx, user.ID, true)
		return err
	}
	switch config.Premium {
	case "allow":
		if user.Premium {
			p.logRule("premium_allow", user.ID)
			_, err := p.setVerified(ctx, user.ID, true)
			return err
		}
	case "ban":
		if user.Premium {
			p.logRule("premium_ban", user.ID)
			return p.punishDirect(ctx, user.ID, config, false)
		}
	case "only":
		if user.Premium {
			p.logRule("premium_only_allow", user.ID)
			_, err := p.setVerified(ctx, user.ID, true)
			return err
		}
		p.logRule("premium_only_deny", user.ID)
		return p.punishDirect(ctx, user.ID, config, false)
	}
	for _, word := range config.WhitelistWords {
		if strings.Contains(message.Text, word) {
			p.logRule("word_whitelist", user.ID)
			_, err := p.setVerified(ctx, user.ID, true)
			return err
		}
	}
	for _, word := range config.BlacklistWords {
		if strings.Contains(message.Text, word) {
			p.logRule("word_blacklist", user.ID)
			return p.punishDirect(ctx, user.ID, config, false)
		}
	}
	flooded, err := p.observeFlood(ctx, user.ID)
	if err != nil {
		p.services.Logger.Warn("PMCaptcha flood watcher", "error", err)
	}
	if flooded {
		p.logRule("flood", user.ID)
		return nil
	}
	canReport := true
	if settings, err := p.services.Telegram.GetPrivateChatSettings(
		ctx,
		user.ID,
	); err == nil {
		canReport = settings.CanReportSpam
	} else {
		p.services.Logger.Debug(
			"read private chat report capability",
			"user_id", user.ID,
			"error", err,
		)
	}
	p.logRule("captcha", user.ID)
	return p.startChallenge(ctx, user.ID, config.ChallengeType, canReport)
}

func (p *Plugin) startChallenge(
	ctx context.Context,
	userID int64,
	challengeType string,
	canReport bool,
) error {
	if !oneOf(challengeType, "math", "sticker", "img") {
		challengeType = "math"
	}
	now := time.Now()
	challenge := Challenge{
		UserID:       userID,
		Type:         challengeType,
		StartedAt:    now,
		LastActiveAt: now,
		CanReport:    canReport,
	}
	key := verifiedKey(userID)
	p.mu.Lock()
	if _, verified := p.state.Verified[key]; verified {
		p.mu.Unlock()
		return nil
	}
	if _, exists := p.state.Challenges[key]; exists {
		p.mu.Unlock()
		return nil
	}
	p.state.Challenges[key] = challenge
	if err := p.persistLocked(ctx); err != nil {
		delete(p.state.Challenges, key)
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()

	if err := p.services.Telegram.SetPrivateChatQuarantined(
		ctx,
		userID,
		true,
	); err != nil {
		p.logActionResult("initial_archive", userID, err)
	} else {
		p.logActionResult("initial_archive", userID, nil)
	}
	switch challengeType {
	case "sticker":
		return p.startStickerChallenge(ctx, challenge)
	case "img":
		return p.startImageChallenge(ctx, challenge)
	default:
		return p.startMathChallenge(ctx, challenge)
	}
}

func (p *Plugin) startMathChallenge(
	ctx context.Context,
	challenge Challenge,
) error {
	left, right, operator, answer, err := randomExpression()
	if err != nil {
		return p.removeChallenge(ctx, challenge.UserID)
	}
	timeout := p.timeoutFor("math")
	text := "🛡️ 私聊验证\n\n" +
		fmt.Sprintf("请计算：%d %s %d = ?", left, operator, right) +
		timeoutHint(timeout) +
		"\n\n直接发送答案即可。"
	sent, err := p.services.Telegram.SendText(ctx, challenge.UserID, text)
	if err != nil {
		_ = p.removeChallenge(ctx, challenge.UserID)
		return fmt.Errorf("发送数学验证失败: %w", err)
	}
	challenge.Type = "math"
	challenge.Answer = answer
	challenge.MessageIDs = []int{sent.MessageID}
	if err := p.saveChallenge(ctx, challenge); err != nil {
		return err
	}
	p.scheduleChallengeTimer(challenge)
	return nil
}

func (p *Plugin) startStickerChallenge(
	ctx context.Context,
	challenge Challenge,
) error {
	timeout := p.timeoutFor("sticker")
	text := "🛡️ 私聊验证\n\n请发送任意一张贴纸完成验证。" +
		timeoutHint(timeout)
	sent, err := p.services.Telegram.SendText(ctx, challenge.UserID, text)
	if err != nil {
		_ = p.removeChallenge(ctx, challenge.UserID)
		return fmt.Errorf("发送贴纸验证失败: %w", err)
	}
	challenge.Type = "sticker"
	challenge.MessageIDs = []int{sent.MessageID}
	if err := p.saveChallenge(ctx, challenge); err != nil {
		return err
	}
	p.scheduleChallengeTimer(challenge)
	return nil
}

func (p *Plugin) startImageChallenge(
	ctx context.Context,
	challenge Challenge,
) error {
	p.mu.Lock()
	botID := p.imageBotID
	imageType := p.state.Config.ImageType
	p.mu.Unlock()
	if botID == 0 {
		return p.fallbackToMath(ctx, challenge.UserID)
	}
	timeout := p.timeoutFor("img")
	intro, err := p.services.Telegram.SendText(
		ctx,
		challenge.UserID,
		"🛡️ 私聊验证\n\n请按图片中的提示完成操作。"+
			timeoutHint(timeout),
	)
	if err != nil {
		return p.fallbackToMath(ctx, challenge.UserID)
	}
	inline, err := p.services.Telegram.SendInlineBotResult(
		ctx,
		telegram.InlineBotRequest{
			Bot:    imageCaptchaBot,
			Query:  imageType,
			ChatID: challenge.UserID,
		},
	)
	if err != nil {
		_ = p.services.Telegram.DeleteMessages(
			ctx,
			challenge.UserID,
			[]int{intro.MessageID},
		)
		p.services.Logger.Warn(
			"image captcha unavailable; falling back to math",
			"user_id", challenge.UserID,
			"error", err,
		)
		return p.fallbackToMath(ctx, challenge.UserID)
	}
	challenge.Type = "img"
	challenge.MessageIDs = []int{intro.MessageID, inline.MessageID}
	challenge.LastActiveAt = time.Now()
	if err := p.saveChallenge(ctx, challenge); err != nil {
		return err
	}
	if err := p.services.Telegram.BlockUser(ctx, challenge.UserID); err != nil {
		p.services.Logger.Warn(
			"temporarily block image captcha user",
			"user_id", challenge.UserID,
			"error", err,
		)
	}
	p.scheduleChallengeTimer(challenge)
	return nil
}

func (p *Plugin) fallbackToMath(ctx context.Context, userID int64) error {
	p.mu.Lock()
	challenge, exists := p.state.Challenges[verifiedKey(userID)]
	p.mu.Unlock()
	if !exists {
		return nil
	}
	challenge.Type = "math"
	challenge.StartedAt = time.Now()
	challenge.LastActiveAt = challenge.StartedAt
	challenge.MessageIDs = nil
	challenge.TryCount = 0
	return p.startMathChallenge(ctx, challenge)
}

func (p *Plugin) verifyChallenge(
	ctx context.Context,
	message telegram.Message,
	challenge Challenge,
) error {
	switch challenge.Type {
	case "math":
		answer, err := parseMathAnswer(message.Text)
		if err != nil {
			return p.failChallenge(ctx, challenge.UserID, challenge.StartedAt)
		}
		if answer != challenge.Answer {
			return p.failChallenge(ctx, challenge.UserID, challenge.StartedAt)
		}
	case "sticker":
		if message.Sticker == nil {
			return p.failChallenge(ctx, challenge.UserID, challenge.StartedAt)
		}
	case "img":
		return nil
	default:
		return p.failChallenge(ctx, challenge.UserID, challenge.StartedAt)
	}
	_ = p.services.Telegram.DeleteMessages(
		ctx,
		message.ChatID,
		[]int{message.ID},
	)
	return p.completeChallenge(ctx, challenge.UserID)
}

func parseMathAnswer(value string) (int, error) {
	var digits strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			digits.WriteRune(character)
		}
	}
	if digits.Len() == 0 {
		return 0, strconv.ErrSyntax
	}
	answer, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0, err
	}
	if strings.Contains(value, "-") {
		answer = -answer
	}
	return answer, nil
}

func (p *Plugin) handleImageCaptchaResult(
	ctx context.Context,
	message telegram.Message,
) error {
	key := verifiedKey(message.ChatID)
	p.mu.Lock()
	challenge, exists := p.state.Challenges[key]
	if !exists || challenge.Type != "img" {
		p.mu.Unlock()
		return nil
	}
	challenge.LastActiveAt = time.Now()
	p.state.Challenges[key] = challenge
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	p.scheduleChallengeTimer(challenge)

	text := strings.ToUpper(message.Text)
	switch {
	case strings.Contains(text, "CAPTCHA_SOLVED"):
		_ = p.services.Telegram.DeleteMessages(
			ctx,
			message.ChatID,
			[]int{message.ID},
		)
		return p.completeChallenge(ctx, message.ChatID)
	case strings.Contains(text, "CAPTCHA_FALLBACK"):
		_ = p.services.Telegram.UnblockUser(ctx, message.ChatID)
		p.deleteChallengeMessages(
			ctx,
			message.ChatID,
			challenge.MessageIDs,
		)
		return p.fallbackToMath(ctx, message.ChatID)
	case strings.Contains(text, "CAPTCHA_FAILED"):
		if strings.Contains(strings.ToLower(message.Text), "forced") {
			return p.failChallenge(
				ctx,
				message.ChatID,
				challenge.StartedAt,
			)
		}
		p.mu.Lock()
		current, ok := p.state.Challenges[key]
		if ok && current.StartedAt.Equal(challenge.StartedAt) {
			current.TryCount++
			current.LastActiveAt = time.Now()
			p.state.Challenges[key] = current
			challenge = current
			err = p.persistLocked(ctx)
		}
		maxRetries := p.state.Config.ImageMaxRetries
		p.mu.Unlock()
		if err != nil {
			return err
		}
		if challenge.TryCount >= maxRetries {
			return p.failChallenge(
				ctx,
				message.ChatID,
				challenge.StartedAt,
			)
		}
	}
	return nil
}

func (p *Plugin) completeChallenge(ctx context.Context, userID int64) error {
	key := verifiedKey(userID)
	p.mu.Lock()
	challenge, exists := p.state.Challenges[key]
	if !exists {
		p.mu.Unlock()
		return nil
	}
	delete(p.state.Challenges, key)
	p.state.Verified[key] = time.Now()
	p.state.Stats.Passed++
	config := cloneConfig(p.state.Config)
	p.cancelChallengeTimerLocked(userID)
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	var operationErrors []error
	if challenge.Type == "img" {
		if err := p.services.Telegram.UnblockUser(ctx, userID); err != nil {
			operationErrors = append(operationErrors, err)
		}
	}
	if err := p.services.Telegram.SetPrivateChatQuarantined(
		ctx,
		userID,
		false,
	); err != nil {
		operationErrors = append(operationErrors, err)
	}
	p.deleteChallengeMessages(ctx, userID, challenge.MessageIDs)
	if !config.Silent {
		text := strings.TrimSpace(config.Welcome)
		if text == "" {
			text = "✅ 验证通过"
		}
		sent, sendErr := p.services.Telegram.SendText(ctx, userID, text)
		if sendErr != nil {
			operationErrors = append(operationErrors, sendErr)
		} else {
			delay := 3 * time.Second
			if config.Welcome != "" {
				delay = 5 * time.Second
			}
			p.deleteLater(userID, sent.MessageID, delay)
		}
	}
	return errors.Join(operationErrors...)
}

func (p *Plugin) failChallenge(
	ctx context.Context,
	userID int64,
	startedAt time.Time,
) error {
	key := verifiedKey(userID)
	p.mu.Lock()
	challenge, exists := p.state.Challenges[key]
	if !exists || !challenge.StartedAt.Equal(startedAt) {
		p.mu.Unlock()
		return nil
	}
	delete(p.state.Challenges, key)
	p.state.Stats.Banned++
	config := cloneConfig(p.state.Config)
	p.cancelChallengeTimerLocked(userID)
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	p.deleteChallengeMessages(ctx, userID, challenge.MessageIDs)
	if challenge.Type == "img" {
		_ = p.services.Telegram.UnblockUser(ctx, userID)
	}
	return p.punish(ctx, userID, config, challenge.CanReport)
}

func (p *Plugin) punish(
	ctx context.Context,
	userID int64,
	config Config,
	canReport bool,
) error {
	var operationErrors []error
	if !config.Silent {
		sent, err := p.services.Telegram.SendText(
			ctx,
			userID,
			"❌ 验证未通过",
		)
		p.collectActionResult(
			"send_failure_notice",
			userID,
			err,
			&operationErrors,
		)
		if err == nil {
			p.deleteLater(userID, sent.MessageID, 3*time.Second)
		}
	}
	switch config.Action {
	case "none":
		err := p.services.Telegram.SetPrivateChatQuarantined(
			ctx,
			userID,
			false,
		)
		p.collectActionResult(
			"release_archive",
			userID,
			err,
			&operationErrors,
		)
		return errors.Join(operationErrors...)
	case "delete":
		if config.Report && canReport {
			p.collectActionResult(
				"report_spam",
				userID,
				p.services.Telegram.ReportSpam(ctx, userID),
				&operationErrors,
			)
		} else if config.Report {
			p.logActionSkipped(
				"report_spam",
				userID,
				"Telegram 当前未提供举报入口",
			)
		}
		p.collectActionResult(
			"block_user",
			userID,
			p.services.Telegram.BlockUser(ctx, userID),
			&operationErrors,
		)
		p.collectActionResult(
			"delete_history",
			userID,
			p.services.Telegram.DeletePrivateHistory(ctx, userID),
			&operationErrors,
		)
	default:
		if config.Report && canReport {
			p.collectActionResult(
				"report_spam",
				userID,
				p.services.Telegram.ReportSpam(ctx, userID),
				&operationErrors,
			)
		} else if config.Report {
			p.logActionSkipped(
				"report_spam",
				userID,
				"Telegram 当前未提供举报入口",
			)
		}
		p.collectActionResult(
			"block_user",
			userID,
			p.services.Telegram.BlockUser(ctx, userID),
			&operationErrors,
		)
		// Sending the failure notice may move an archived conversation back to
		// the main list. Reapply the archive and mute state after all outgoing
		// messages and moderation calls so action=ban has a stable final state.
		p.collectActionResult(
			"final_archive",
			userID,
			p.services.Telegram.SetPrivateChatQuarantined(ctx, userID, true),
			&operationErrors,
		)
	}
	if joined := errors.Join(operationErrors...); joined != nil {
		p.services.Logger.Warn(
			"PMCaptcha punishment was only partially applied",
			"user_id", userID,
			"error", joined,
		)
	}
	return nil
}

func (p *Plugin) collectActionResult(
	action string,
	userID int64,
	err error,
	operationErrors *[]error,
) {
	p.logActionResult(action, userID, err)
	if err != nil {
		*operationErrors = append(
			*operationErrors,
			fmt.Errorf("%s: %w", action, err),
		)
	}
}

func (p *Plugin) logActionResult(action string, userID int64, err error) {
	if p.services.Logger == nil {
		return
	}
	if err != nil {
		p.services.Logger.Warn(
			"PMCaptcha 操作失败",
			"operation", action,
			"user_id", userID,
			"error", err,
		)
		return
	}
	p.services.Logger.Info(
		"PMCaptcha 操作成功",
		"operation", action,
		"user_id", userID,
	)
}

func (p *Plugin) logActionSkipped(action string, userID int64, reason string) {
	if p.services.Logger == nil {
		return
	}
	p.services.Logger.Info(
		"PMCaptcha 操作跳过",
		"operation", action,
		"user_id", userID,
		"reason", reason,
	)
}

func (p *Plugin) punishDirect(
	ctx context.Context,
	userID int64,
	config Config,
	canReport bool,
) error {
	p.mu.Lock()
	p.state.Stats.Banned++
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	return p.punish(ctx, userID, config, canReport)
}

func (p *Plugin) timeoutFor(challengeType string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch challengeType {
	case "sticker":
		return p.state.Config.StickerTimeout
	case "img":
		return p.state.Config.ImageTimeout
	default:
		return p.state.Config.MathTimeout
	}
}

func (p *Plugin) saveChallenge(
	ctx context.Context,
	challenge Challenge,
) error {
	p.mu.Lock()
	p.state.Challenges[verifiedKey(challenge.UserID)] = challenge
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	return err
}

func (p *Plugin) removeChallenge(ctx context.Context, userID int64) error {
	p.mu.Lock()
	delete(p.state.Challenges, verifiedKey(userID))
	p.cancelChallengeTimerLocked(userID)
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	return err
}

func (p *Plugin) scheduleChallengeTimer(challenge Challenge) {
	timeout := p.timeoutFor(challenge.Type)
	p.mu.Lock()
	p.cancelChallengeTimerLocked(challenge.UserID)
	if timeout <= 0 || p.runCtx == nil {
		p.mu.Unlock()
		return
	}
	base := challenge.StartedAt
	if challenge.Type == "img" {
		base = challenge.LastActiveAt
	}
	remaining := time.Duration(timeout)*time.Second - time.Since(base)
	timerCtx, cancel := context.WithCancel(p.runCtx)
	p.challengeTimers[challenge.UserID] = cancel
	p.mu.Unlock()
	if remaining < 0 {
		remaining = 0
	}
	go func() {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timerCtx.Done():
			return
		case <-timer.C:
			runCtx, cancel := context.WithTimeout(
				context.Background(),
				45*time.Second,
			)
			defer cancel()
			if err := p.failChallenge(
				runCtx,
				challenge.UserID,
				challenge.StartedAt,
			); err != nil {
				p.services.Logger.Warn(
					"expire PMCaptcha challenge",
					"user_id", challenge.UserID,
					"error", err,
				)
			}
		}
	}()
}

func (p *Plugin) cancelChallengeTimerLocked(userID int64) {
	if cancel := p.challengeTimers[userID]; cancel != nil {
		cancel()
		delete(p.challengeTimers, userID)
	}
}

func (p *Plugin) deleteChallengeMessages(
	ctx context.Context,
	userID int64,
	messageIDs []int,
) {
	if len(messageIDs) == 0 {
		return
	}
	if err := p.services.Telegram.DeleteMessages(
		ctx,
		userID,
		messageIDs,
	); err != nil {
		p.services.Logger.Debug(
			"delete PMCaptcha challenge messages",
			"user_id", userID,
			"error", err,
		)
	}
}

func (p *Plugin) deleteLater(userID int64, messageID int, delay time.Duration) {
	p.mu.Lock()
	runCtx := p.runCtx
	p.mu.Unlock()
	if runCtx == nil {
		return
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-runCtx.Done():
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = p.services.Telegram.DeleteMessages(
				ctx,
				userID,
				[]int{messageID},
			)
		}
	}()
}

func timeoutHint(seconds int) string {
	if seconds <= 0 {
		return "\n\n本次验证不设时间限制。"
	}
	return fmt.Sprintf("\n\n请在 %d 秒内完成。", seconds)
}

func randomExpression() (int, int, string, int, error) {
	left, err := randomInt(2, 20)
	if err != nil {
		return 0, 0, "", 0, err
	}
	right, err := randomInt(1, 12)
	if err != nil {
		return 0, 0, "", 0, err
	}
	operatorIndex, err := randomInt(0, 2)
	if err != nil {
		return 0, 0, "", 0, err
	}
	switch operatorIndex {
	case 0:
		return left, right, "+", left + right, nil
	case 1:
		return left, right, "-", left - right, nil
	default:
		return left, right, "×", left * right, nil
	}
}

func randomInt(minimum, maximum int) (int, error) {
	if maximum < minimum {
		return 0, errors.New("invalid random range")
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(maximum-minimum+1)))
	if err != nil {
		return 0, err
	}
	return minimum + int(value.Int64()), nil
}
