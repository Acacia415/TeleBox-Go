package usererror

import (
	"strings"
	"testing"
)

func TestSanitizeText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		want       string
		forbidden  []string
		wantChange bool
	}{
		{
			name: "telegram administrator",
			input: "❌ 踢出失败：remote_error: edit banned rights: " +
				"rpcDoRequest: rpc error code 400: USER_ADMIN_INVALID",
			want:       "❌ 踢出失败：无法执行管理员操作",
			forbidden:  []string{"remote_error", "rpcDoRequest"},
			wantChange: true,
		},
		{
			name: "network timeout hides URL",
			input: `❌ 下载失败：Get "https://private.example/token": ` +
				"context deadline exceeded",
			want:       "❌ 下载失败：请求超时，请稍后重试",
			forbidden:  []string{"private.example", "context deadline"},
			wantChange: true,
		},
		{
			name:       "unknown rpc code",
			input:      "❌ 操作失败：rpc error: SOME_INTERNAL_FAILURE",
			want:       "❌ 操作失败：操作未完成（错误代码：SOME_INTERNAL_FAILURE）",
			forbidden:  []string{"rpc error"},
			wantChange: true,
		},
		{
			name:       "external tool",
			input:      "❌ 本机测速失败：terminate called after throwing std::logic_error",
			want:       "❌ 本机测速失败：外部程序执行失败",
			forbidden:  []string{"std::", "terminate called"},
			wantChange: true,
		},
		{
			name:       "html error",
			input:      "<b>❌ 更新失败</b>\n\nremote_error: FLOOD_WAIT_30",
			want:       "<b>❌ 更新失败</b>\n\n操作过于频繁，请等待 30 秒后重试",
			forbidden:  []string{"remote_error"},
			wantChange: true,
		},
		{
			name:       "already Chinese",
			input:      "❌ 参数错误：请输入有效的用户 ID",
			want:       "❌ 参数错误：请输入有效的用户 ID",
			wantChange: false,
		},
		{
			name:       "success label containing failure",
			input:      "✅ 验证失败处理：none",
			want:       "✅ 验证失败处理：none",
			wantChange: false,
		},
		{
			name: "settings labels containing failure",
			input: "⚙️ PMCaptcha 设置\n\n" +
				"失败处理：none\n失败后举报：关闭",
			want:       "失败处理：none",
			wantChange: false,
		},
		{
			name:       "unknown English library error",
			input:      "❌ 处理失败：provider returned an opaque failure",
			want:       "❌ 处理失败：操作未完成，请查看服务日志",
			forbidden:  []string{"provider returned"},
			wantChange: true,
		},
		{
			name:       "ASCII delimiter preserves plugin context",
			input:      "语录生成失败: provider returned an opaque failure",
			want:       "语录生成失败:操作未完成，请查看服务日志",
			forbidden:  []string{"provider returned"},
			wantChange: true,
		},
		{
			name:       "emoji-only raw error",
			input:      "❌ provider returned an opaque failure",
			want:       "❌ 操作未完成，请查看服务日志",
			forbidden:  []string{"provider returned"},
			wantChange: true,
		},
		{
			name:       "ordinary content",
			input:      "ERROR: 这是用户要求保留的普通文本",
			want:       "ERROR: 这是用户要求保留的普通文本",
			wantChange: false,
		},
		{
			name: "help text containing troubleshooting phrases",
			input: "<b>命令帮助</b>\n<code>-yt</code>\n\n" +
				"Gemini 官方 API 无法直连时，可部署反代。\n" +
				"示例：https://example.workers.dev\n\n" +
				"出现 Sign in to confirm you're not a bot 时需要 Cookies。",
			want:       "Gemini 官方 API 无法直连时，可部署反代。",
			wantChange: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, changed := SanitizeText(test.input)
			if changed != test.wantChange {
				t.Fatalf("changed = %v, want %v; text = %q", changed, test.wantChange, got)
			}
			if !strings.Contains(got, test.want) {
				t.Fatalf("text = %q, want substring %q", got, test.want)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("text = %q contains forbidden %q", got, forbidden)
				}
			}
		})
	}
}

func TestLocalizeCommonErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"CHAT_ADMIN_REQUIRED":               "不是管理员",
		"FLOOD_WAIT_12":                     "等待 12 秒",
		"SLOWMODE_WAIT_8":                   "等待 8 秒",
		"PEER_FLOOD":                        "限制了当前账号",
		"USER_PRIVACY_RESTRICTED":           "隐私设置",
		"MESSAGE_EDIT_TIME_EXPIRED":         "超过允许编辑的时间",
		"CHAT_SEND_STICKERS_FORBIDDEN":      "发送此类媒体",
		"INVITE_HASH_EXPIRED":               "邀请链接已经过期",
		"PHONE_CODE_INVALID":                "登录验证码无效",
		"permission denied":                 "系统权限不足",
		"exec format error":                 "系统架构不兼容",
		"database is locked":                "数据库正忙",
		"ssh: handshake failed: no auth":    "SSH 验证失败",
		"status code 429 from remote API":   "请求过于频繁",
		"requested format is not available": "没有提供可下载的媒体格式",
		"unexpected EOF":                    "连接提前中断",
		"executable file not found in PATH": "未找到所需的外部程序",
	}
	for input, want := range tests {
		if got := Localize(input); !strings.Contains(got, want) {
			t.Fatalf("Localize(%q) = %q, want substring %q", input, got, want)
		}
	}
}
