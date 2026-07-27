package telegram

import "testing"

func TestPlainTextFromHTML(t *testing.T) {
	t.Parallel()

	got := plainTextFromHTML(
		"<b>📊 状态</b>\n• 目标：<code>&lt;example.com&gt;</code>\n<i>完成</i>",
	)
	want := "📊 状态\n• 目标：<example.com>\n完成"
	if got != want {
		t.Fatalf("plainTextFromHTML() = %q, want %q", got, want)
	}
}
