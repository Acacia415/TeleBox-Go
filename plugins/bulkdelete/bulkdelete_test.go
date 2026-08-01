package bulkdelete

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type transientNoticeTelegram struct {
	telegram.Client
	deleted chan telegram.SentMessage
}

func (c *transientNoticeTelegram) SendText(
	context.Context,
	int64,
	string,
) (telegram.SentMessage, error) {
	return telegram.SentMessage{ChatID: 91, MessageID: 27}, nil
}

func (c *transientNoticeTelegram) DeleteMessages(
	_ context.Context,
	chatID int64,
	messageIDs []int,
) error {
	c.deleted <- telegram.SentMessage{ChatID: chatID, MessageID: messageIDs[0]}
	return nil
}

func TestHelpTextMode(t *testing.T) {
	if got := helpText(".", true); !strings.Contains(got, "当前删除他人权限：开启") {
		t.Fatalf("enabled help = %q", got)
	}
	if got := helpText("!", false); !strings.Contains(got, "!bd") ||
		!strings.Contains(got, "当前删除他人权限：关闭") {
		t.Fatalf("disabled help = %q", got)
	}
}

func TestCompletionNoticeDeletesItself(t *testing.T) {
	client := &transientNoticeTelegram{deleted: make(chan telegram.SentMessage, 1)}
	plugin := New(service.Container{Telegram: client})
	if err := plugin.sendTransientNotice(
		context.Background(), 10, "done", 5*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case deleted := <-client.deleted:
		if deleted.ChatID != 91 || deleted.MessageID != 27 {
			t.Fatalf("deleted message = %+v", deleted)
		}
	case <-time.After(time.Second):
		t.Fatal("completion notice was not deleted")
	}
}

func TestCompletionNoticeLifetime(t *testing.T) {
	if completionNoticeLifetime != 3*time.Second {
		t.Fatalf("completion notice lifetime = %v", completionNoticeLifetime)
	}
}
