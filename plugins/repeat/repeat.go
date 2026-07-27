package repeat

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
)

const (
	maxMessageCount = 100
	maxRepeatCount  = 10
)

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "re",
		Version:     "0.1.0",
		Description: "复读回复的消息",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "re",
		Description: "复读回复消息，可指定消息数和次数",
		OwnerOnly:   true,
		Handler:     p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if request.Message.ReplyToID <= 0 {
		return p.respond(
			ctx,
			request,
			fmt.Sprintf("你必须回复一条消息才能复读\n用法：%sre [消息数] [复读次数]", request.Prefix),
		)
	}
	messageCount, repeatCount, err := parseArgs(request.Args)
	if err != nil {
		return p.respond(ctx, request, "参数错误："+err.Error())
	}
	messageIDs := messageRange(request.Message.ReplyToID, messageCount)
	if len(messageIDs) == 0 {
		return p.respond(ctx, request, "没有可复读的消息")
	}

	if err := p.services.Telegram.DeleteMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ID},
	); err != nil {
		return fmt.Errorf("delete repeat command: %w", err)
	}
	for index := 0; index < repeatCount; index++ {
		err = p.services.Telegram.ForwardMessages(
			ctx,
			request.Message.ChatID,
			request.Message.ChatID,
			messageIDs,
		)
		if err == nil {
			continue
		}
		p.services.Logger.Warn(
			"forward repeat failed; copying without author",
			"error", err,
			"message_count", len(messageIDs),
		)
		if copyErr := p.services.Telegram.CopyMessages(
			ctx,
			request.Message.ChatID,
			request.Message.ChatID,
			messageIDs,
		); copyErr != nil {
			_, sendErr := p.services.Telegram.SendText(
				ctx,
				request.Message.ChatID,
				"复读失败："+copyErr.Error(),
			)
			return errors.Join(err, copyErr, sendErr)
		}
	}
	return nil
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

func parseArgs(args []string) (int, int, error) {
	messageCount := 1
	repeatCount := 1
	var err error
	if len(args) > 0 {
		messageCount, err = strconv.Atoi(args[0])
		if err != nil || messageCount < 1 || messageCount > maxMessageCount {
			return 0, 0, fmt.Errorf("消息数必须为 1–%d", maxMessageCount)
		}
	}
	if len(args) > 1 {
		repeatCount, err = strconv.Atoi(args[1])
		if err != nil || repeatCount < 1 || repeatCount > maxRepeatCount {
			return 0, 0, fmt.Errorf("复读次数必须为 1–%d", maxRepeatCount)
		}
	}
	if len(args) > 2 {
		return 0, 0, errors.New("参数过多")
	}
	return messageCount, repeatCount, nil
}

func messageRange(replyToID, count int) []int {
	start := replyToID - count + 1
	if start < 1 {
		start = 1
	}
	result := make([]int, 0, replyToID-start+1)
	for id := start; id <= replyToID; id++ {
		result = append(result, id)
	}
	return result
}
