package ai

import (
	"strings"
	"testing"
)

func TestCombineQuestion(t *testing.T) {
	display, prompt := combineQuestion("为什么", "天空是蓝色的")
	if display != "为什么" ||
		prompt != "原消息内容：天空是蓝色的\n\n问题：为什么" {
		t.Fatalf("combined question = %q / %q", display, prompt)
	}
	display, prompt = combineQuestion("", "只使用回复")
	if display != "只使用回复" || prompt != display {
		t.Fatalf("reply question = %q / %q", display, prompt)
	}
}

func TestProviderCapabilities(t *testing.T) {
	if supports(providerClaude, featureImage, providerClaude) {
		t.Fatal("Claude unexpectedly supports image generation")
	}
	if !supports(providerThirdParty, featureTTS, providerOpenAI) {
		t.Fatal("OpenAI-compatible third party did not support TTS")
	}
	if supports(providerThirdParty, featureTTS, providerDeepSeek) {
		t.Fatal("DeepSeek-compatible third party unexpectedly supports TTS")
	}
}

func TestAutoAssignModels(t *testing.T) {
	got := autoAssignModels([]string{
		"text-embedding-3-small",
		"gpt-4.1-mini",
		"gpt-image-1",
		"gpt-4o-mini-tts",
	})
	if got[featureChat] != "gpt-4.1-mini" ||
		got[featureSearch] != "gpt-4.1-mini" ||
		got[featureImage] != "gpt-image-1" ||
		got[featureTTS] != "gpt-4o-mini-tts" {
		t.Fatalf("assignments = %#v", got)
	}
}

func TestSplitText(t *testing.T) {
	parts := splitText(strings.Repeat("甲", 12), 5)
	if len(parts) != 3 || len([]rune(parts[0])) > 5 {
		t.Fatalf("split = %#v", parts)
	}
}

func TestPCMToWAV(t *testing.T) {
	wav := pcmToWAV([]byte{1, 2, 3, 4}, "audio/L16;rate=16000")
	if len(wav) != 48 || string(wav[:4]) != "RIFF" ||
		string(wav[8:12]) != "WAVE" {
		t.Fatalf("invalid WAV header: %x", wav[:12])
	}
}

func TestValidateBaseURL(t *testing.T) {
	if !validateBaseURL("https://api.example.com/v1") {
		t.Fatal("valid URL rejected")
	}
	for _, value := range []string{
		"file:///tmp/api",
		"https://user:pass@example.com",
		"not-a-url",
	} {
		if validateBaseURL(value) {
			t.Fatalf("invalid URL accepted: %q", value)
		}
	}
}

func TestConvertLegacyHistory(t *testing.T) {
	history := convertLegacyHistory(
		`[{"role":"user","parts":[{"text":"你好"}]},` +
			`{"role":"model","parts":[{"text":"你好！"}]}]`,
	)
	if len(history) != 2 ||
		history[0].Role != "user" ||
		history[1].Role != "assistant" ||
		history[1].Text != "你好！" {
		t.Fatalf("history = %#v", history)
	}
}

func TestTelegraphNodesPreserveParagraphsAndLineBreaks(t *testing.T) {
	nodes := telegraphNodes("第一行\n第二行\n\n下一段")
	if len(nodes) != 2 {
		t.Fatalf("nodes = %#v", nodes)
	}
	children, ok := nodes[0]["children"].([]any)
	if !ok || len(children) != 3 {
		t.Fatalf("first paragraph = %#v", nodes[0])
	}
}

func TestVoiceCompatibility(t *testing.T) {
	if !voiceCompatible("Kore", providerGemini) ||
		voiceCompatible("alloy", providerGemini) ||
		!voiceCompatible("alloy", providerOpenAI) {
		t.Fatal("voice compatibility matrix is incorrect")
	}
}
