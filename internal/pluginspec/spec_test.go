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
	for _, name := range []string{"bin", "eatgif", "inkstone", "telegram-backup", "yt-dlp"} {
		if _, exists := Find(name); !exists {
			t.Fatalf("Find(%q) = false", name)
		}
	}
	pmcaptcha, ok := Find("pmcaptcha")
	if !ok || pmcaptcha.MinHost != "0.7.0" {
		t.Fatalf("pmcaptcha specification = %+v, found=%t", pmcaptcha, ok)
	}
	speedlink, ok := Find("speedlink")
	if !ok || speedlink.MinHost != "0.7.2" {
		t.Fatalf("speedlink specification = %+v, found=%t", speedlink, ok)
	}
	search, ok := Find("search")
	if !ok || search.MinHost != "0.8.2" {
		t.Fatalf("search specification = %+v, found=%t", search, ok)
	}
	inkstone, ok := Find("inkstone")
	if !ok || inkstone.MinHost != "0.8.3" {
		t.Fatalf("inkstone specification = %+v, found=%t", inkstone, ok)
	}
	if _, exists := Find("unsupported"); exists {
		t.Fatal(`Find("unsupported") = true`)
	}
}
