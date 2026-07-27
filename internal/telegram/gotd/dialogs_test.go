package gotd

import "testing"

func TestInviteHash(t *testing.T) {
	for input, expected := range map[string]string{
		"https://t.me/+abc123":         "abc123",
		"https://t.me/joinchat/abc123": "abc123",
		"+abc123":                      "abc123",
		"https://t.me/public_channel":  "",
	} {
		if got := inviteHash(input); got != expected {
			t.Fatalf("inviteHash(%q) = %q", input, got)
		}
	}
}

func TestNormalizePublicTarget(t *testing.T) {
	if got := normalizePublicTarget("https://t.me/example"); got != "@example" {
		t.Fatalf("normalized target = %q", got)
	}
	if got := normalizePublicTarget("example"); got != "@example" {
		t.Fatalf("normalized username = %q", got)
	}
}
