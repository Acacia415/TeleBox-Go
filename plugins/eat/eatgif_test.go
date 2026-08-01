package eat

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestEatGIFFilterDoesNotUpscaleFrames(t *testing.T) {
	for _, duration := range []time.Duration{2 * time.Second, 5 * time.Second} {
		filter := eatGIFFilter(duration)
		if strings.Contains(filter, "scale") {
			t.Fatalf("filter %q unexpectedly scales frames", filter)
		}
	}
	if got := eatGIFFilter(2 * time.Second); got != "null" {
		t.Fatalf("short filter = %q", got)
	}
	if got := eatGIFFilter(5 * time.Second); !strings.HasPrefix(got, "setpts=") {
		t.Fatalf("long filter = %q", got)
	}
}

func TestEatGIFEncodingStartsAtHighQuality(t *testing.T) {
	if len(eatGIFCRFLevels) == 0 || eatGIFCRFLevels[0] != 20 {
		t.Fatalf("CRF levels = %v", eatGIFCRFLevels)
	}
	for index := 1; index < len(eatGIFCRFLevels); index++ {
		if eatGIFCRFLevels[index] <= eatGIFCRFLevels[index-1] {
			t.Fatalf("CRF levels are not increasing: %v", eatGIFCRFLevels)
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
