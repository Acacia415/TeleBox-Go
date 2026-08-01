package pmcaptcha

import (
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestSafeRuleEvaluation(t *testing.T) {
	t.Parallel()
	data := ruleContext{
		Text: "订单 123",
		User: telegram.User{
			ID:       42,
			Username: "Alice",
			Premium:  true,
		},
		Message: telegram.Message{Media: &telegram.Media{}},
	}
	tests := []struct {
		expression string
		want       bool
	}{
		{`contains(text, "订单") && user.premium`, true},
		{`contains(text, '订单') and user.id == 42`, true},
		{`equals_fold(user.username, "alice")`, true},
		{`matches(text, "^订单") && message.has_media`, true},
		{`user.contact || user.id > 100`, false},
		{`not user.premium`, false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.expression, func(t *testing.T) {
			t.Parallel()
			got, err := evaluateRule(test.expression, data)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("evaluateRule() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDecodeLegacyExportedConfig(t *testing.T) {
	t.Parallel()
	config, warnings, err := decodeImportedConfig([]byte(`{
		"version":"2.34",
		"whitelist":["订单"],
		"timeout":45,
		"disable":true,
		"report":false,
		"flood_limit":8,
		"flood_act":"captcha",
		"type":"sticker",
		"collect_logs":true,
		"custom_rule":"await bot.delete_messages(1, 1)"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.MathTimeout != 45 || !config.DisablePM ||
		config.Report || config.FloodLimit != 8 ||
		config.FloodAction != "captcha" ||
		config.ChallengeType != "sticker" ||
		len(config.WhitelistWords) != 1 {
		t.Fatalf("legacy config = %+v", config)
	}
	if config.CustomRule != "" || config.DetailedLogs {
		t.Fatalf("unsafe settings were enabled: %+v", config)
	}
	if len(warnings) != 2 ||
		!strings.Contains(strings.Join(warnings, " "), "第三方日志") ||
		!strings.Contains(strings.Join(warnings, " "), "自定义规则") {
		t.Fatalf("warnings = %+v", warnings)
	}
}

func TestSafeRuleRejectsCodeExecution(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{
		`exec("danger")`,
		`user.DeleteAll()`,
		`func() bool { return true }()`,
		`false && unknown`,
		`text + "x" == "x"`,
	} {
		if err := validateRule(expression); err == nil {
			t.Fatalf("validateRule(%q) accepted unsafe expression", expression)
		}
	}
}

func TestNormalizeStateRepairsInvalidValues(t *testing.T) {
	t.Parallel()
	state := State{Config: Config{
		MathTimeout:     -1,
		StickerTimeout:  -1,
		ImageTimeout:    -1,
		Action:          "bad",
		Premium:         "bad",
		FloodLimit:      0,
		FloodAction:     "bad",
		ChallengeType:   "bad",
		ImageType:       "bad",
		ImageMaxRetries: 0,
	}}
	normalizeState(&state)
	defaults := defaultState().Config
	if state.Config.MathTimeout != defaults.MathTimeout ||
		state.Config.Action != defaults.Action ||
		state.Config.FloodLimit != defaults.FloodLimit ||
		state.Config.ChallengeType != defaults.ChallengeType ||
		state.Config.ImageMaxRetries != defaults.ImageMaxRetries {
		t.Fatalf("normalizeState() = %+v", state.Config)
	}
	if state.Verified == nil || state.Challenges == nil ||
		state.Flood.Users == nil || state.Flood.Recent == nil {
		t.Fatalf("normalizeState() left nil maps: %+v", state)
	}
}
