package eat

import (
	"path/filepath"
	"testing"
)

func TestSafeRelativePath(t *testing.T) {
	got, err := safeRelativePath("dr/dr1.png")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("dr", "dr1.png") {
		t.Fatalf("safe path = %q", got)
	}
	for _, value := range []string{
		"../secret",
		"/absolute",
		`..\secret`,
		`C:\secret`,
		`\\server\share`,
		".",
	} {
		if _, err := safeRelativePath(value); err == nil {
			t.Fatalf("unsafe path %q was accepted", value)
		}
	}
}

func TestPathInside(t *testing.T) {
	root := filepath.Join("C:", "data", "assets")
	if !isPathInside(root, filepath.Join(root, "eatgif")) {
		t.Fatal("child cache rejected")
	}
	if isPathInside(root, filepath.Join(root, "..", "other")) {
		t.Fatal("outside cache accepted")
	}
}
