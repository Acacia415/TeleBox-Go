package inkstone

import (
	"strings"
	"testing"
)

func TestStableOperationID(t *testing.T) {
	t.Parallel()

	first := stableOperationID(-100123, 42, "01k00000000000000000000000")
	second := stableOperationID(-100123, 42, "01k00000000000000000000000")
	other := stableOperationID(-100123, 43, "01k00000000000000000000000")
	if first != second || first == other {
		t.Fatalf("operation IDs = %q, %q, %q", first, second, other)
	}
	if len(first) < 8 || len(first) > 128 || !strings.HasPrefix(first, "tg_") {
		t.Fatalf("operation ID format = %q", first)
	}
}

func TestDetailedHelpCoversSetupAndUsage(t *testing.T) {
	t.Parallel()

	help := helpHTML("-")
	for _, want := range []string{
		"设置 → MCP",
		"INKSTONE_API_KEY",
		"systemctl restart telebox.service",
		"-ink find 浩希",
		"-ink bind hx 浩希",
		"-ink bind hx #2",
		"-ink new hx 浩希",
		"25 MiB",
		"GIF",
		"视频",
		"-ink hx -force",
		"普通文字",
		"不发送笔记地址",
		"/n/",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not contain %q", want)
		}
	}
	if strings.Contains(help, "{{prefix}}") {
		t.Fatal("help still contains prefix placeholder")
	}
	if !strings.Contains(help, "https://inkstone.example.com/mcp") {
		t.Fatal("help must use a generic Inkstone endpoint example")
	}
}

func TestParseCreateRequest(t *testing.T) {
	t.Parallel()

	alias, title, err := parseCreateRequest("new HX 项目 备忘")
	if err != nil || alias != "hx" || title != "项目 备忘" {
		t.Fatalf("parseCreateRequest = %q, %q, %v", alias, title, err)
	}
	if _, _, err := parseCreateRequest("new hx"); err == nil {
		t.Fatal("create request without title was accepted")
	}
	if err := validateAlias("new"); err == nil {
		t.Fatal("new command was accepted as an alias")
	}
}

func TestStableCreateOperationID(t *testing.T) {
	t.Parallel()

	first := stableCreateOperationID(-100123, 42, "HX", "项目 备忘")
	second := stableCreateOperationID(-100123, 42, "hx", "项目 备忘")
	other := stableCreateOperationID(-100123, 43, "hx", "项目 备忘")
	if first != second || first == other || !strings.HasPrefix(first, "tg_create_") {
		t.Fatalf("create operation IDs = %q, %q, %q", first, second, other)
	}
}

func TestParseBindRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw    string
		alias  string
		target string
	}{
		{"bind hx 浩希", "hx", "浩希"},
		{"bind HX #2 append", "hx", "#2"},
		{"bind hx title 月度笔记", "hx", "月度笔记"},
	}
	for _, test := range tests {
		alias, target, err := parseBindRequest(test.raw)
		if err != nil || alias != test.alias || target != test.target {
			t.Errorf(
				"parseBindRequest(%q) = %q, %q, %v",
				test.raw, alias, target, err,
			)
		}
	}
	if _, _, err := parseBindRequest("bind hx"); err == nil {
		t.Fatal("bind request without target was accepted")
	}
	if _, _, err := parseBindRequest("bind hx 浩希 month"); err == nil {
		t.Fatal("month mode was accepted")
	}
}

func TestParseWriteInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		raw     string
		content string
		force   bool
	}{
		{"hx 火锅店 200", "火锅店 200", false},
		{"hx -force", "", true},
		{"hx -force 火锅店 200", "火锅店 200", true},
		{"hx 内容包含 --force", "内容包含 --force", false},
	} {
		content, force := parseWriteInput(test.raw)
		if content != test.content || force != test.force {
			t.Errorf(
				"parseWriteInput(%q) = %q, %t; want %q, %t",
				test.raw,
				content,
				force,
				test.content,
				test.force,
			)
		}
	}
}

func TestExtractNoteID(t *testing.T) {
	t.Parallel()

	id := "01k00000000000000000000000"
	for _, input := range []string{
		id,
		"https://inkstone.example.com/n/" + id,
		"https://inkstone.example.com/n/" + id + "?pane=note",
	} {
		if got := extractNoteID(input); got != id {
			t.Errorf("extractNoteID(%q) = %q", input, got)
		}
	}
	if got := extractNoteID("浩希"); got != "" {
		t.Fatalf("title was treated as a note ID: %q", got)
	}
}

func TestFormatSearchResults(t *testing.T) {
	t.Parallel()

	result := formatSearchResults("-", "hx", "浩希", []noteSearchResult{
		{ID: "01k00000000000000000000000", Title: "浩希"},
		{ID: "01k00000000000000000000001", Title: "浩希备忘"},
	})
	for _, want := range []string{"1. 浩希", "2. 浩希备忘", "-ink bind hx #序号"} {
		if !strings.Contains(result, want) {
			t.Errorf("formatted results do not contain %q: %s", want, result)
		}
	}
}
