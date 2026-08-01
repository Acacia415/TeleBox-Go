package pmcaptcha

import (
	"strings"
	"testing"
)

func TestHelpTextUsesConfiguredPrefixAndShowsCommonTasks(t *testing.T) {
	t.Parallel()

	got := helpText("!")
	for _, want := range []string{
		"!pmcaptcha type math  设置数学验证",
		"!pmcaptcha report on  失败后举报",
		"!pmcaptcha add [用户]  手动放行",
		"!pmcaptcha help <子命令>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("helpText() missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "规则顺序：") {
		t.Fatalf("helpText() still exposes implementation-oriented rule order\n%s", got)
	}
}

func TestCommandHelpExplainsPotentiallyDestructiveSettings(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"action":         "delete 拉黑并删除私聊",
		"disable_pm":     "不会出现验证码",
		"flood_username": "此操作会修改账号用户名",
		"report":         "与 action 分开控制",
	} {
		if !strings.Contains(commandHelp[name], want) {
			t.Fatalf("commandHelp[%q] missing %q: %s", name, want, commandHelp[name])
		}
	}
}

func TestGuideHTMLKeepsHelpDiscoverable(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"{{prefix}}pmcaptcha settings",
		"{{prefix}}pmcaptcha help &lt;子命令&gt;",
		"{{prefix}}pmcaptcha search &lt;关键词&gt;",
	} {
		if !strings.Contains(guideHTML, want) {
			t.Fatalf("guideHTML missing %q", want)
		}
	}
}
