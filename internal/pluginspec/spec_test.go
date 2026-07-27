package pluginspec

import "testing"

func TestCatalog(t *testing.T) {
	t.Parallel()
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(All()); got != 25 {
		t.Fatalf("plugin specifications = %d, want 25", got)
	}
	for _, name := range []string{"bin", "eatgif", "telegram-backup", "yt-dlp"} {
		if _, exists := Find(name); !exists {
			t.Fatalf("Find(%q) = false", name)
		}
	}
	if _, exists := Find("unsupported"); exists {
		t.Fatal(`Find("unsupported") = true`)
	}
}
