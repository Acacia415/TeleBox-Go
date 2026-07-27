package jointime

import (
	"strings"
	"testing"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestFormatResult(t *testing.T) {
	joinedAt := time.Date(2026, 7, 26, 12, 34, 56, 0, time.Local)
	got := formatResult(telegram.User{
		ID:        42,
		FirstName: "Tele",
		Username:  "telebox",
	}, joinedAt, telegram.MemberRoleAdmin)
	for _, fragment := range []string{"Tele (@telebox)", "🛡️", "42", "2026-07-26 12:34:56"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("result missing %q:\n%s", fragment, got)
		}
	}
}

func TestFormatCount(t *testing.T) {
	if got := formatCount(100000); got != "100,000" {
		t.Fatalf("formatCount = %q", got)
	}
}
