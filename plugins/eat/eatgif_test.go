package eat

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

type eatGIFTestTelegram struct {
	telegram.Client
	avatar  []byte
	notices []string
	uploads []telegram.Upload
}

func (c *eatGIFTestTelegram) ReplyText(
	_ context.Context, _ int64, _ int, text string,
) (telegram.SentMessage, error) {
	c.notices = append(c.notices, text)
	return telegram.SentMessage{}, nil
}

func (c *eatGIFTestTelegram) GetMessages(
	context.Context, int64, []int,
) ([]telegram.Message, error) {
	return []telegram.Message{{SenderID: 2}}, nil
}

func (c *eatGIFTestTelegram) DownloadProfilePhoto(
	_ context.Context, _ int64, destination io.Writer,
) error {
	_, err := destination.Write(c.avatar)
	return err
}

func (c *eatGIFTestTelegram) SendFile(
	_ context.Context, _ int64, upload telegram.Upload,
) (telegram.SentMessage, error) {
	if _, err := os.ReadFile(upload.Path); err != nil {
		return telegram.SentMessage{}, err
	}
	c.uploads = append(c.uploads, upload)
	return telegram.SentMessage{}, nil
}

type eatGIFTestTools struct {
	service.ToolRunner
	t *testing.T
}

func (r eatGIFTestTools) LookPath(string) (string, error) { return "ffmpeg", nil }

func (r eatGIFTestTools) Run(
	_ context.Context, request toolrunner.Command,
) (toolrunner.Result, error) {
	r.t.Helper()
	if _, err := os.ReadFile(filepath.Join(request.Directory, "frames.txt")); err != nil {
		r.t.Fatal(err)
	}
	frame, err := os.Open(filepath.Join(request.Directory, "frame-000.png"))
	if err != nil {
		r.t.Fatal(err)
	}
	_, decodeErr := png.Decode(frame)
	closeErr := frame.Close()
	if decodeErr != nil || closeErr != nil {
		r.t.Fatalf("generated frame: decode = %v, close = %v", decodeErr, closeErr)
	}
	err = os.WriteFile(request.Args[len(request.Args)-1], []byte("encoded sticker"), 0o600)
	return toolrunner.Result{}, err
}

func TestEatGIFGeneratesAfterWorkDirectoryIsRemoved(t *testing.T) {
	for _, state := range []string{"existing", "removed", "replaced by file"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			pixel := image.NewNRGBA(image.Rect(0, 0, 2, 2))
			pixel.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
			var data bytes.Buffer
			if err := png.Encode(&data, pixel); err != nil {
				t.Fatal(err)
			}
			client := &eatGIFTestTelegram{avatar: data.Bytes()}
			root := t.TempDir()
			p := NewGIF(service.Container{
				AssetsDir: filepath.Join(root, "assets"),
				Telegram:  client,
				Tools:     eatGIFTestTools{t: t},
			})
			p.workDir = filepath.Join(root, "tmp", "eatgif")
			if err := p.Start(ctx); err != nil {
				t.Fatal(err)
			}
			for name, contents := range map[string][]byte{
				"detail.json": []byte(`{"width":2,"height":2,"res":[{"url":"frame.png","delay":100,"you":{"mask":"mask.png"}}]}`),
				"frame.png":   data.Bytes(),
				"mask.png":    data.Bytes(),
			} {
				if err := os.WriteFile(filepath.Join(p.cacheDir, name), contents, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if state != "existing" {
				if err := os.Remove(p.workDir); err != nil {
					t.Fatal(err)
				}
				if state == "removed" {
					if err := os.Remove(filepath.Dir(p.workDir)); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(p.workDir, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p.list = map[string]gifListEntry{"test": {URL: "detail.json", Desc: "test"}}
			if err := p.handle(ctx, command.Request{
				Args:    []string{"test"},
				Message: telegram.Message{ID: 3, ChatID: 4, SenderID: 1, ReplyToID: 5},
			}); err != nil {
				t.Fatal(err)
			}
			if state == "replaced by file" {
				if len(client.uploads) != 0 || len(client.notices) != 2 ||
					!strings.HasPrefix(client.notices[1], "❌ 创建逐帧目录失败：") {
					t.Fatalf("uploads = %v, notices = %v", client.uploads, client.notices)
				}
				if contents, err := os.ReadFile(p.workDir); err != nil || string(contents) != "keep" {
					t.Fatalf("existing file changed: contents = %q, error = %v", contents, err)
				}
				return
			}
			if len(client.uploads) != 1 || client.uploads[0].Kind != telegram.MediaSticker {
				t.Fatalf("uploads = %v, notices = %v", client.uploads, client.notices)
			}
			entries, err := os.ReadDir(p.workDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("work directory after cleanup: entries = %v, error = %v", entries, err)
			}
		})
	}
}

func TestEatGIFUsesOfficialTemplateSource(t *testing.T) {
	const want = "https://raw.githubusercontent.com/TeleBoxOrg/TeleBox-Plugins/refs/heads/main/eatgif/"
	if eatGIFBaseURL != want {
		t.Fatalf("eatGIFBaseURL = %q, want %q", eatGIFBaseURL, want)
	}
}

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
