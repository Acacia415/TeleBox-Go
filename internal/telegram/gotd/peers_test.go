package gotd

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestPortableUser(t *testing.T) {
	t.Parallel()

	raw := &tg.User{
		ID:        42,
		FirstName: "Tele",
		LastName:  "Box",
		Username:  "telebox",
		Deleted:   true,
		Premium:   true,
		Photo:     &tg.UserProfilePhoto{DCID: 5},
	}
	raw.SetStatus(&tg.UserStatusOffline{WasOnline: 100})
	got := portableUser(raw)
	if got.ID != 42 || got.Username != "telebox" || got.PhotoDC != 5 ||
		got.Presence != teleboxtelegram.PresenceOffline ||
		!got.LastSeen.Equal(time.Unix(100, 0)) {
		t.Fatalf("portableUser() = %+v", got)
	}
}

func TestExportedInviteLink(t *testing.T) {
	t.Parallel()

	if got := exportedInviteLink(&tg.ChatInviteExported{
		Link: "https://t.me/+secret",
	}); got != "https://t.me/+secret" {
		t.Fatalf("invite link = %q", got)
	}
	if got := exportedInviteLink(&tg.ChatInviteExported{
		Link:    "https://t.me/+revoked",
		Revoked: true,
	}); got != "" {
		t.Fatalf("revoked invite leaked: %q", got)
	}
}
