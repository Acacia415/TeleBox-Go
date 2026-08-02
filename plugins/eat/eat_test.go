package eat

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"
)

func TestEatAndEat2UsePlainSharedHelp(t *testing.T) {
	t.Parallel()
	definitions := New(service.Container{}).Commands()
	if len(definitions) != 2 {
		t.Fatalf("command count = %d", len(definitions))
	}
	forbidden := []string{"AI", "智能", "精确", "精准"}
	for _, definition := range definitions {
		if definition.Name != "eat" && definition.Name != "eat2" {
			t.Fatalf("unexpected command %q", definition.Name)
		}
		for _, phrase := range forbidden {
			if strings.Contains(definition.Description, phrase) ||
				strings.Contains(definition.HelpHTML, phrase) {
				t.Fatalf("%s help contains %q", definition.Name, phrase)
			}
		}
		if definition.HelpHTML != eatGuideHTML {
			t.Fatalf("%s does not use shared help", definition.Name)
		}
	}
}

func TestValidateEntriesAcceptsStampMode(t *testing.T) {
	t.Parallel()
	var document configDocument
	err := json.Unmarshal([]byte(`{
		"resources": {
			"tuzai": {
				"name": "屠宰",
				"url": "eat/eattuzai.png",
				"stamp": {
					"size": 512,
					"scale": 0.9,
					"rotate": -12,
					"opacity": 0.6
				}
			}
		}
	}`), &document)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateEntries(document.Resources); err != nil {
		t.Fatalf("validateEntries() error = %v", err)
	}
	settings := document.Resources["tuzai"].Stamp.settings()
	if settings.Size != 512 || settings.Scale != 0.9 ||
		settings.Rotate != -12 || settings.Opacity != 0.6 {
		t.Fatalf("stamp settings = %+v", settings)
	}
}

func TestValidateEntriesRejectsInvalidStamp(t *testing.T) {
	t.Parallel()
	invalidOpacity := 2.0
	err := validateEntries(map[string]entry{
		"stamp": {
			Name: "印章",
			URL:  "stamp.png",
			Stamp: &stamp{
				Opacity: &invalidOpacity,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "印章参数无效") {
		t.Fatalf("validateEntries() error = %v", err)
	}
}

func TestComposeStampCentersOverlay(t *testing.T) {
	t.Parallel()
	avatar := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			avatar.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	overlay := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			overlay.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	result := composeStamp(avatar, overlay, stampSettings{
		Size:    4,
		Scale:   0.5,
		Rotate:  0,
		Opacity: 0.5,
	})
	if result.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("result bounds = %v", result.Bounds())
	}
	if top := result.NRGBAAt(1, 0); top.B != 255 || top.R != 0 {
		t.Fatalf("top pixel = %+v", top)
	}
	if middle := result.NRGBAAt(1, 1); middle.R == 0 || middle.B == 0 {
		t.Fatalf("middle pixel = %+v", middle)
	}
}

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

func TestTrimTransparentStickerCanvas(t *testing.T) {
	t.Parallel()
	source := image.NewNRGBA(image.Rect(0, 0, 6, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 6; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, A: stickerTrimAlpha - 1})
		}
	}
	for y := 1; y < 4; y++ {
		for x := 2; x < 5; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 200, G: 100, A: 255})
		}
	}
	got := trimTransparent(source)
	if got.Bounds().Dx() != 3 || got.Bounds().Dy() != 3 {
		t.Fatalf("trimmed bounds = %v", got.Bounds())
	}
	if pixel := color.NRGBAModel.Convert(got.At(0, 0)).(color.NRGBA); pixel.R != 200 || pixel.G != 100 || pixel.A != 255 {
		t.Fatalf("trimmed first pixel = %+v", pixel)
	}
}

func TestSignificantAlphaBoundsReportsThresholds(t *testing.T) {
	t.Parallel()
	source := image.NewNRGBA(image.Rect(0, 0, 5, 4))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 4})
	source.SetNRGBA(2, 1, color.NRGBA{G: 255, A: 32})
	source.SetNRGBA(3, 2, color.NRGBA{B: 255, A: 255})

	low, lowPixels := significantAlphaBounds(source, 1)
	if low != image.Rect(0, 0, 4, 3) || lowPixels != 3 {
		t.Fatalf("low alpha bounds = %v, pixels = %d", low, lowPixels)
	}
	high, highPixels := significantAlphaBounds(source, 64)
	if high != image.Rect(3, 2, 4, 3) || highPixels != 1 {
		t.Fatalf("high alpha bounds = %v, pixels = %d", high, highPixels)
	}
}

func TestTrimStickerContentSelectsDominantComponent(t *testing.T) {
	t.Parallel()
	source := image.NewNRGBA(image.Rect(0, 0, 12, 6))
	for y := 1; y < 3; y++ {
		for x := 0; x < 2; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	for y := 0; y < 5; y++ {
		for x := 7; x < 12; x++ {
			source.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	got := trimStickerContent(source)
	if got.Bounds() != image.Rect(0, 0, 5, 5) {
		t.Fatalf("dominant crop bounds = %v", got.Bounds())
	}
	if pixel := color.NRGBAModel.Convert(got.At(0, 0)).(color.NRGBA); pixel.B != 255 {
		t.Fatalf("dominant crop first pixel = %+v", pixel)
	}
}

func TestTrimStickerContentKeepsBalancedComponents(t *testing.T) {
	t.Parallel()
	source := image.NewNRGBA(image.Rect(0, 0, 8, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
		for x := 5; x < 8; x++ {
			source.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
		}
	}
	got := trimStickerContent(source)
	if got.Bounds() != image.Rect(0, 0, 8, 3) {
		t.Fatalf("balanced crop bounds = %v", got.Bounds())
	}
}

func TestWriteDiagnosticArchive(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/eat2-diagnostic.zip"
	err := writeDiagnosticArchive(path, map[string][]byte{
		"00-report.txt":    []byte("report"),
		"full-decoded.png": []byte("png"),
	})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 2 || archive.File[0].Name != "00-report.txt" ||
		archive.File[1].Name != "full-decoded.png" {
		t.Fatalf("archive entries = %#v", archive.File)
	}
}

func TestResizeUsesSmoothInterpolation(t *testing.T) {
	t.Parallel()
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	got := resize(source, 3, 1)
	middle := got.NRGBAAt(1, 0)
	if middle.R == 0 || middle.R == 255 {
		t.Fatalf("middle pixel was not interpolated: %+v", middle)
	}
}

func TestStickerOutputIsWebP(t *testing.T) {
	t.Parallel()
	if stickerFileName != "output.webp" || stickerMIMEType != "image/webp" {
		t.Fatalf("sticker output = %q, %q", stickerFileName, stickerMIMEType)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, A: uint8(64 + x*32)})
		}
	}
	var encoded bytes.Buffer
	if err := nativewebp.Encode(&encoded, source, nil); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("output is not WebP: %x", data[:min(len(data), 12)])
	}
	decoded, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v", decoded.Bounds())
	}
}

func TestDynamicStickerFrameStrategy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		media      telegram.Media
		preview    bool
		firstFrame bool
	}{
		{
			name:    "tgs MIME",
			media:   telegram.Media{MIMEType: "application/x-tgsticker"},
			preview: true,
		},
		{
			name:    "tgs filename",
			media:   telegram.Media{FileName: "sticker.TGS"},
			preview: true,
		},
		{
			name:       "webm MIME",
			media:      telegram.Media{MIMEType: "video/webm"},
			firstFrame: true,
		},
		{
			name:       "webm filename",
			media:      telegram.Media{FileName: "sticker.WEBM"},
			firstFrame: true,
		},
		{
			name:  "static webp",
			media: telegram.Media{MIMEType: "image/webp"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := usesTelegramStickerPreview(test.media); got != test.preview {
				t.Fatalf("usesTelegramStickerPreview() = %t, want %t", got, test.preview)
			}
			if got := usesFFmpegFirstFrame(test.media); got != test.firstFrame {
				t.Fatalf("usesFFmpegFirstFrame() = %t, want %t", got, test.firstFrame)
			}
		})
	}
}

func TestRotate(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	got := rotate(source, 90)
	if got.Bounds().Dx() != 3 || got.Bounds().Dy() != 4 {
		t.Fatalf("rotated bounds = %v", got.Bounds())
	}
}
