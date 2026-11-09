package eat

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"golang.org/x/image/webp"
)

type builtinTestTelegram struct {
	telegram.Client
	avatars map[int64][]byte
	media   []byte
	photos  []int64
	notices []string
	uploads []telegram.Upload
	images  []image.Image
	deleted []int
}

func (c *builtinTestTelegram) ReplyText(
	_ context.Context, _ int64, _ int, text string,
) (telegram.SentMessage, error) {
	c.notices = append(c.notices, text)
	return telegram.SentMessage{}, nil
}

func (c *builtinTestTelegram) EditText(
	ctx context.Context, chatID int64, messageID int, text string,
) (telegram.SentMessage, error) {
	return c.ReplyText(ctx, chatID, messageID, text)
}

func (c *builtinTestTelegram) GetMessages(
	context.Context, int64, []int,
) ([]telegram.Message, error) {
	return []telegram.Message{{
		ID: 5, SenderID: 2,
		Media: &telegram.Media{Kind: telegram.MediaPhoto, MIMEType: "image/png"},
	}}, nil
}

func (c *builtinTestTelegram) DownloadProfilePhoto(
	_ context.Context, userID int64, destination io.Writer,
) error {
	c.photos = append(c.photos, userID)
	_, err := destination.Write(c.avatars[userID])
	return err
}

func (c *builtinTestTelegram) DownloadMedia(
	_ context.Context, _ int64, _ int, destination io.Writer,
) (telegram.Media, error) {
	_, err := destination.Write(c.media)
	return telegram.Media{Kind: telegram.MediaPhoto, MIMEType: "image/png"}, err
}

func (c *builtinTestTelegram) SendFile(
	_ context.Context, _ int64, upload telegram.Upload,
) (telegram.SentMessage, error) {
	data, err := os.ReadFile(upload.Path)
	if err != nil {
		return telegram.SentMessage{}, err
	}
	decoded, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return telegram.SentMessage{}, err
	}
	c.uploads = append(c.uploads, upload)
	c.images = append(c.images, decoded)
	return telegram.SentMessage{}, nil
}

func (c *builtinTestTelegram) DeleteMessages(_ context.Context, _ int64, ids []int) error {
	c.deleted = append(c.deleted, ids...)
	return nil
}

func solidAvatar(t *testing.T, value color.NRGBA) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(value), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestBuiltinKJGeneratesWithoutRemoteConfig(t *testing.T) {
	for _, useMedia := range []bool{false, true} {
		t.Run(map[bool]string{false: "eat", true: "eat2"}[useMedia], func(t *testing.T) {
			self := color.NRGBA{R: 255, A: 255}
			target := color.NRGBA{B: 255, A: 255}
			media := color.NRGBA{G: 255, A: 255}
			client := &builtinTestTelegram{
				avatars: map[int64][]byte{1: solidAvatar(t, self), 2: solidAvatar(t, target)},
				media:   solidAvatar(t, media),
			}
			root := t.TempDir()
			// No HTTP or storage service: kj must not try to load a remote catalog.
			p := New(service.Container{AssetsDir: root, Telegram: client})
			p.workDir = filepath.Join(root, "work")
			ctx := context.Background()
			if err := p.Start(ctx); err != nil {
				t.Fatal(err)
			}
			request := command.Request{
				Args: []string{"KJ"}, Prefix: "-",
				Message: telegram.Message{ID: 7, ChatID: 4, SenderID: 1, ReplyToID: 5, Outgoing: true},
			}
			if err := p.handle(ctx, request, useMedia); err != nil {
				t.Fatal(err)
			}
			if len(client.uploads) != 1 {
				t.Fatalf("uploads = %v, notices = %v", client.uploads, client.notices)
			}
			upload := client.uploads[0]
			if upload.Kind != telegram.MediaSticker || upload.MIMEType != "image/webp" ||
				upload.ReplyToID != 5 || upload.Width != 512 || upload.Height != 512 {
				t.Fatalf("unexpected sticker metadata: %+v", upload)
			}
			if useMedia {
				target = media
			}
			result := client.images[0]
			for point, want := range map[image.Point]color.NRGBA{
				{X: 126, Y: 235}: self,
				{X: 462, Y: 290}: target,
				{X: 433, Y: 280}: target,
				{X: 450, Y: 275}: target,
			} {
				if got := color.NRGBAModel.Convert(result.At(point.X, point.Y)); got != want {
					t.Fatalf("pixel %v = %v, want %v", point, got, want)
				}
			}
			originalBytes, err := builtinAssets.ReadFile("builtin/kanjian.png")
			if err != nil {
				t.Fatal(err)
			}
			original, err := png.Decode(bytes.NewReader(originalBytes))
			if err != nil {
				t.Fatal(err)
			}
			for _, point := range []image.Point{{X: 120, Y: 285}, {X: 474, Y: 318}, {X: 420, Y: 230}} {
				got := color.NRGBAModel.Convert(result.At(point.X, point.Y))
				want := color.NRGBAModel.Convert(original.At(point.X, point.Y))
				if got != want {
					t.Fatalf("foreground pixel %v changed: %v != %v", point, got, want)
				}
			}
			if !slices.Equal(client.deleted, []int{7}) {
				t.Fatalf("deleted messages = %v", client.deleted)
			}
			if !slices.Contains(client.photos, 1) || (!useMedia && !slices.Contains(client.photos, 2)) {
				t.Fatalf("avatar users = %v", client.photos)
			}
		})
	}
}

type builtinTestHTTP struct{ service.HTTPClient }

func (builtinTestHTTP) Do(context.Context, httpclient.Request) (httpclient.Response, error) {
	return httpclient.Response{
		StatusCode: http.StatusOK,
		Body: []byte(`{"resources":{
			"remote":{"name":"远程模板","url":"remote.png","you":{"mask":"mask.png"}},
			"kj":{"name":"同名远程模板","url":"remote-kj.png","you":{"mask":"mask.png"}}
		}}`),
	}, nil
}

type builtinTestStorage struct{ service.Storage }

func (builtinTestStorage) Put(context.Context, string, string, []byte) error { return nil }

func TestBuiltinKJSurvivesConfigRefreshAndClear(t *testing.T) {
	client := &builtinTestTelegram{
		avatars: map[int64][]byte{
			1: solidAvatar(t, color.NRGBA{R: 255, A: 255}),
			2: solidAvatar(t, color.NRGBA{B: 255, A: 255}),
		},
	}
	root := t.TempDir()
	p := New(service.Container{
		AssetsDir: root, Telegram: client, HTTP: builtinTestHTTP{}, Storage: builtinTestStorage{},
	})
	p.workDir = filepath.Join(root, "work")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	request := command.Request{Prefix: "-", Message: telegram.Message{ID: 7, ChatID: 4, SenderID: 1}}
	request.Args = []string{"set", "https://example.com/templates/config.json"}
	if err := p.handle(ctx, request, false); err != nil {
		t.Fatal(err)
	}
	if len(p.cfg.Entries) != 2 || p.cfg.Entries["remote"].Name != "远程模板" ||
		p.cfg.Entries["kj"].URL != "builtin/kanjian.png" {
		t.Fatalf("refreshed catalog = %+v", p.cfg.Entries)
	}
	if !strings.Contains(p.listText("-"), "kj - 看剑！偷袭") {
		t.Fatal("kj missing from refreshed template list")
	}
	request.Args = []string{"clear"}
	if err := p.handle(ctx, request, false); err != nil {
		t.Fatal(err)
	}
	p.services.HTTP, p.services.Storage = nil, nil
	request.Args, request.Message.ReplyToID = []string{"kj"}, 5
	if err := p.handle(ctx, request, false); err != nil {
		t.Fatal(err)
	}
	if len(client.uploads) != 1 {
		t.Fatalf("kj unavailable after clear: %v", client.notices)
	}
}
