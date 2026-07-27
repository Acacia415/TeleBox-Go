package eat

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestOfficialResourceBase(t *testing.T) {
	got, err := resourceBase(defaultConfigURL)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://raw.githubusercontent.com/TeleBoxOrg/TeleBox_Plugins/refs/heads/main/"
	if got != want {
		t.Fatalf("resource base = %q, want %q", got, want)
	}
	resolved, err := resolveAssetURL(got, "eat/eatat.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resolved, "/main/eat/eatat.png") {
		t.Fatalf("resolved URL = %q", resolved)
	}
}

func TestMaskImageUsesMaskAlpha(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	mask := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	mask.SetNRGBA(0, 0, color.NRGBA{A: 255})
	mask.SetNRGBA(1, 0, color.NRGBA{A: 0})
	got := maskImage(source, mask)
	if got.NRGBAAt(0, 0).A != 255 || got.NRGBAAt(1, 0).A != 0 {
		t.Fatalf("masked alpha = %d, %d", got.NRGBAAt(0, 0).A, got.NRGBAAt(1, 0).A)
	}
}

func TestFitWithin(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1024, 512))
	got := fitWithin(source, 512, 512)
	if got.Bounds().Dx() != 512 || got.Bounds().Dy() != 256 {
		t.Fatalf("fitted bounds = %v", got.Bounds())
	}
}

func TestRotate(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	got := rotate(source, 90)
	if got.Bounds().Dx() != 3 || got.Bounds().Dy() != 4 {
		t.Fatalf("rotated bounds = %v", got.Bounds())
	}
}
