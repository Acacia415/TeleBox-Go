package isalive

import (
	"strings"
	"testing"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestFormatStatusOffline(t *testing.T) {
	t.Parallel()

	user := telegram.User{
		ID:        42,
		FirstName: "Tele",
		LastName:  "Box",
		Username:  "telebox",
		Presence:  telegram.PresenceOffline,
		LastSeen:  time.Unix(100, 0),
	}
	text := formatStatus(user, time.Unix(100, 0).Add(49*time.Hour))
	for _, expected := range []string{
		"• 用户：Tele Box @telebox",
		"• ID：42",
		"1970-01-01 08:01:40",
		"离线天数：2",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatStatus() = %q, missing %q", text, expected)
		}
	}
}

func TestPresenceTextApproximateStatuses(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		presence telegram.Presence
		text     string
		days     string
	}{
		{telegram.PresenceOnline, "在线", "0"},
		{telegram.PresenceRecently, "最近上线", "0"},
		{telegram.PresenceLastWeek, "一周内", "7"},
		{telegram.PresenceLastMonth, "一个月内", "30"},
	} {
		text, days := presenceText(telegram.User{Presence: test.presence}, time.Now())
		if text != test.text || days != test.days {
			t.Fatalf("presenceText(%q) = %q, %q", test.presence, text, days)
		}
	}
}
