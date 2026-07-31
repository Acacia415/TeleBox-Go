package usererror

import (
	"context"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type captureClient struct {
	telegram.Client
	text   string
	html   string
	upload telegram.Upload
}

func (c *captureClient) SendText(
	context.Context,
	int64,
	string,
) (telegram.SentMessage, error) {
	panic("unexpected SendText call")
}

func (c *captureClient) ReplyText(
	_ context.Context,
	_ int64,
	_ int,
	text string,
) (telegram.SentMessage, error) {
	c.text = text
	return telegram.SentMessage{}, nil
}

func (c *captureClient) EditText(
	_ context.Context,
	_ int64,
	_ int,
	text string,
) (telegram.SentMessage, error) {
	c.text = text
	return telegram.SentMessage{}, nil
}

func (c *captureClient) SendFile(
	_ context.Context,
	_ int64,
	upload telegram.Upload,
) (telegram.SentMessage, error) {
	c.upload = upload
	return telegram.SentMessage{}, nil
}

func (c *captureClient) SendHTML(
	_ context.Context,
	_ int64,
	text string,
) (telegram.SentMessage, error) {
	c.html = text
	return telegram.SentMessage{}, nil
}

func (c *captureClient) ReplyHTML(
	_ context.Context,
	_ int64,
	_ int,
	text string,
) (telegram.SentMessage, error) {
	c.html = text
	return telegram.SentMessage{}, nil
}

func (c *captureClient) EditHTML(
	_ context.Context,
	_ int64,
	_ int,
	text string,
) (telegram.SentMessage, error) {
	c.html = text
	return telegram.SentMessage{}, nil
}

func TestClientSanitizesTextAndCaption(t *testing.T) {
	t.Parallel()

	underlying := &captureClient{}
	wrapped := Wrap(underlying, nil)
	raw := "❌ 踢出失败：remote_error: USER_ADMIN_INVALID"

	if _, err := wrapped.ReplyText(context.Background(), 1, 2, raw); err != nil {
		t.Fatal(err)
	}
	if underlying.text !=
		"❌ 踢出失败：无法执行管理员操作。若目标是管理员，请先取消其管理员权限；群主无法被踢出" {
		t.Fatalf("reply text = %q", underlying.text)
	}

	if _, err := wrapped.SendFile(context.Background(), 1, telegram.Upload{
		Caption: "❌ 导出失败：open /private/path: permission denied",
	}); err != nil {
		t.Fatal(err)
	}
	if underlying.upload.Caption !=
		"❌ 导出失败：系统权限不足，请检查文件权限或服务运行账号" {
		t.Fatalf("upload caption = %q", underlying.upload.Caption)
	}
}

func TestClientSanitizesRichTextHelper(t *testing.T) {
	t.Parallel()

	underlying := &captureClient{}
	wrapped := Wrap(underlying, nil)

	if _, err := telegram.ReplyHTML(
		context.Background(),
		wrapped,
		1,
		2,
		"<b>❌ 更新失败</b>\n\nremote_error: FLOOD_WAIT_30",
	); err != nil {
		t.Fatal(err)
	}
	if underlying.html !=
		"<b>❌ 更新失败</b>\n\n操作过于频繁，请等待 30 秒后重试" {
		t.Fatalf("HTML reply = %q", underlying.html)
	}
}
