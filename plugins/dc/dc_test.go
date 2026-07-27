package dc

import "testing"

func TestFormatChatDC(t *testing.T) {
	if got := formatChatDC("测试群", 4); got != "📍 数据中心\n\n• 对象：测试群\n• DC：DC4" {
		t.Fatalf("formatChatDC = %q", got)
	}
	if got := formatChatDC("测试群", 0); got != "❌ 测试群 没有头像，无法获取 DC 信息" {
		t.Fatalf("formatChatDC without photo = %q", got)
	}
}
