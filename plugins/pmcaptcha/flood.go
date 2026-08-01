package pmcaptcha

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const floodQuietPeriod = 5 * time.Minute

func (p *Plugin) observeFlood(
	ctx context.Context,
	userID int64,
) (bool, error) {
	now := time.Now()
	key := verifiedKey(userID)
	p.mu.Lock()
	if p.state.Flood.Active {
		p.state.Flood.Users[key] = true
		p.state.Flood.LastMessageAt = now
		err := p.persistLocked(ctx)
		p.mu.Unlock()
		if err != nil {
			return true, err
		}
		_ = p.services.Telegram.SetPrivateChatQuarantined(ctx, userID, true)
		p.scheduleFloodEnd(floodQuietPeriod)
		return true, nil
	}
	cutoff := now.Add(-time.Minute).Unix()
	for item, timestamp := range p.state.Flood.Recent {
		if timestamp < cutoff {
			delete(p.state.Flood.Recent, item)
		}
	}
	p.state.Flood.Recent[key] = now.Unix()
	limit := p.state.Config.FloodLimit
	trigger := len(p.state.Flood.Recent) >= limit
	if !trigger {
		err := p.persistLocked(ctx)
		p.mu.Unlock()
		return false, err
	}
	users := make([]int64, 0, len(p.state.Flood.Recent))
	p.state.Flood.Active = true
	p.state.Flood.LastMessageAt = now
	p.state.Flood.Users = make(map[string]bool, len(p.state.Flood.Recent))
	for item := range p.state.Flood.Recent {
		p.state.Flood.Users[item] = true
		if id, err := strconv.ParseInt(item, 10, 64); err == nil {
			users = append(users, id)
		}
	}
	challengeMessages := make(map[int64][]int)
	for _, id := range users {
		if challenge, exists := p.state.Challenges[verifiedKey(id)]; exists {
			challengeMessages[id] = append(
				[]int(nil),
				challenge.MessageIDs...,
			)
			delete(p.state.Challenges, verifiedKey(id))
			p.cancelChallengeTimerLocked(id)
		}
	}
	usernameProtection := p.state.Config.FloodUsername
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return true, err
	}

	previous, privacyErr := p.services.Telegram.GetGlobalAutoArchive(ctx)
	if privacyErr == nil {
		p.mu.Lock()
		p.state.Flood.PreviousAutoArchive = previous
		err = p.persistLocked(ctx)
		p.mu.Unlock()
	}
	if setErr := p.services.Telegram.SetGlobalAutoArchive(ctx, true); setErr != nil {
		privacyErr = errors.Join(privacyErr, setErr)
	}
	for _, id := range users {
		_ = p.services.Telegram.SetPrivateChatQuarantined(ctx, id, true)
		p.deleteChallengeMessages(ctx, id, challengeMessages[id])
	}
	if usernameProtection {
		if transferErr := p.transferFloodUsername(ctx); transferErr != nil {
			p.services.Logger.Warn(
				"transfer username during private-message flood",
				"error", transferErr,
			)
		}
	}
	p.scheduleFloodEnd(floodQuietPeriod)
	if privacyErr != nil {
		p.services.Logger.Warn(
			"enable Telegram global auto archive during flood",
			"error", privacyErr,
		)
	}
	return true, nil
}

func (p *Plugin) scheduleFloodEnd(delay time.Duration) {
	p.mu.Lock()
	if p.floodTimer != nil {
		p.floodTimer()
		p.floodTimer = nil
	}
	if p.runCtx == nil {
		p.mu.Unlock()
		return
	}
	timerCtx, cancel := context.WithCancel(p.runCtx)
	p.floodTimer = cancel
	p.mu.Unlock()
	if delay < 0 {
		delay = 0
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timerCtx.Done():
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(
				context.Background(),
				5*time.Minute,
			)
			defer cancel()
			if err := p.endFlood(ctx); err != nil {
				p.services.Logger.Warn(
					"finish PMCaptcha flood protection",
					"error", err,
				)
			}
		}
	}()
}

func (p *Plugin) endFlood(ctx context.Context) error {
	p.mu.Lock()
	if !p.state.Flood.Active {
		p.mu.Unlock()
		return nil
	}
	if remaining := floodQuietPeriod -
		time.Since(p.state.Flood.LastMessageAt); remaining > 0 {
		p.mu.Unlock()
		p.scheduleFloodEnd(remaining)
		return nil
	}
	users := make([]int64, 0, len(p.state.Flood.Users))
	for key := range p.state.Flood.Users {
		if userID, err := strconv.ParseInt(key, 10, 64); err == nil {
			users = append(users, userID)
		}
	}
	previousAutoArchive := p.state.Flood.PreviousAutoArchive
	username := p.state.Flood.Username
	channelID := p.state.Flood.TemporaryChannelID
	action := p.state.Config.FloodAction
	config := cloneConfig(p.state.Config)
	p.state.Flood = FloodState{
		Users:  make(map[string]bool),
		Recent: make(map[string]int64),
	}
	p.state.Stats.Flooded++
	p.state.Stats.Banned += len(users)
	p.floodTimer = nil
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return err
	}

	var operationErrors []error
	if err := p.services.Telegram.SetGlobalAutoArchive(
		ctx,
		previousAutoArchive,
	); err != nil {
		operationErrors = append(operationErrors, err)
	}
	if username != "" || channelID != 0 {
		if err := p.restoreFloodUsername(ctx, username, channelID); err != nil {
			operationErrors = append(operationErrors, err)
		}
	}
	switch action {
	case "asis":
		for _, userID := range users {
			if err := p.applyFloodAsIs(ctx, userID, config.Action); err != nil {
				operationErrors = append(operationErrors, err)
			}
		}
	case "captcha":
		for _, userID := range users {
			canReport := true
			if settings, err := p.services.Telegram.GetPrivateChatSettings(
				ctx,
				userID,
			); err == nil {
				canReport = settings.CanReportSpam
			}
			if err := p.startChallenge(
				ctx,
				userID,
				config.ChallengeType,
				canReport,
			); err != nil {
				operationErrors = append(operationErrors, err)
			}
		}
	case "delete":
		for _, userID := range users {
			if err := p.services.Telegram.ReportSpam(ctx, userID); err != nil {
				operationErrors = append(operationErrors, err)
			}
			if err := p.services.Telegram.DeletePrivateHistory(
				ctx,
				userID,
			); err != nil {
				operationErrors = append(operationErrors, err)
			}
		}
	case "none":
		// Keep the chats archived and muted; no further action is requested.
	}
	return errors.Join(operationErrors...)
}

func (p *Plugin) applyFloodAsIs(
	ctx context.Context,
	userID int64,
	action string,
) error {
	switch action {
	case "delete":
		return errors.Join(
			p.services.Telegram.BlockUser(ctx, userID),
			p.services.Telegram.DeletePrivateHistory(ctx, userID),
		)
	case "ban":
		return p.services.Telegram.BlockUser(ctx, userID)
	default:
		return nil
	}
}

func (p *Plugin) transferFloodUsername(ctx context.Context) error {
	self, err := p.services.Telegram.ResolveUser(ctx, "me")
	if err != nil || self.Username == "" {
		return err
	}
	channel, err := p.services.Telegram.CreateChannel(
		ctx,
		"PMCaptcha 临时保护",
		"私聊洪水期间临时保存用户名；洪水结束后会自动删除。",
	)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.state.Flood.Username = self.Username
	p.state.Flood.TemporaryChannelID = channel.ID
	err = p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		_ = p.services.Telegram.DeleteChannel(ctx, channel.ID)
		return err
	}
	if err := p.services.Telegram.UpdateAccountUsername(ctx, ""); err != nil {
		_ = p.services.Telegram.DeleteChannel(ctx, channel.ID)
		p.clearFloodUsernameState(ctx)
		return err
	}
	if err := p.services.Telegram.UpdateChatUsername(
		ctx,
		channel.ID,
		self.Username,
	); err != nil {
		_ = p.services.Telegram.UpdateAccountUsername(ctx, self.Username)
		_ = p.services.Telegram.DeleteChannel(ctx, channel.ID)
		p.clearFloodUsernameState(ctx)
		return err
	}
	return nil
}

func (p *Plugin) clearFloodUsernameState(ctx context.Context) {
	p.mu.Lock()
	p.state.Flood.Username = ""
	p.state.Flood.TemporaryChannelID = 0
	if err := p.persistLocked(ctx); err != nil {
		p.services.Logger.Warn(
			"clear temporary flood username state",
			"error", err,
		)
	}
	p.mu.Unlock()
}

func (p *Plugin) restoreFloodUsername(
	ctx context.Context,
	username string,
	channelID int64,
) error {
	var operationErrors []error
	if channelID != 0 {
		if err := p.services.Telegram.UpdateChatUsername(
			ctx,
			channelID,
			"",
		); err != nil {
			operationErrors = append(operationErrors, err)
		}
	}
	if username != "" {
		if err := p.services.Telegram.UpdateAccountUsername(
			ctx,
			username,
		); err != nil {
			operationErrors = append(operationErrors, err)
		}
	}
	if channelID != 0 {
		if err := p.services.Telegram.DeleteChannel(
			ctx,
			channelID,
		); err != nil {
			operationErrors = append(operationErrors, err)
		}
	}
	return errors.Join(operationErrors...)
}
