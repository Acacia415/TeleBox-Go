package ids

import (
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestFormatUser(t *testing.T) {
	got := formatUser(telegram.User{
		ID:          42,
		FirstName:   "Tele",
		LastName:    "Box",
		Username:    "telebox",
		Bio:         "test bio",
		CommonChats: 3,
		PhotoDC:     5,
		Premium:     true,
	})
	for _, fragment := range []string{
		"Tele Box",
		"@telebox",
		"DC5",
		"3 个",
		"Premium",
		"tg://user?id=42",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("formatted user missing %q:\n%s", fragment, got)
		}
	}
}

func TestHasHelp(t *testing.T) {
	if !hasHelp([]string{"@name", "HELP"}) {
		t.Fatal("HELP was not detected")
	}
	if hasHelp([]string{"@name"}) {
		t.Fatal("normal target detected as help")
	}
}
