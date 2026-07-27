package pluginspec

import "testing"

func TestCatalog(t *testing.T) {
	t.Parallel()
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(All()); got != 27 {
		t.Fatalf("plugin specifications = %d, want 27", got)
	}
	for _, name := range []string{"bin", "eatgif", "telegram-backup", "yt-dlp"} {
		if _, exists := Find(name); !exists {
			t.Fatalf("Find(%q) = false", name)
		}
	}
}
