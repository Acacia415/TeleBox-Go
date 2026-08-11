package inkstone

import (
	"errors"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestSupportedMediaKind(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		message telegram.Message
		want    string
	}{
		{message: telegram.Message{Media: &telegram.Media{Kind: telegram.MediaPhoto}}, want: "image"},
		{message: telegram.Message{Media: &telegram.Media{Kind: telegram.MediaVideo}}, want: "video"},
		{message: telegram.Message{Media: &telegram.Media{Kind: telegram.MediaDocument, MIMEType: "image/png"}}, want: "image"},
	} {
		got, err := supportedMediaKind(test.message)
		if err != nil || got != test.want {
			t.Fatalf("supportedMediaKind = %q, %v; want %q", got, err, test.want)
		}
	}
	for _, message := range []telegram.Message{
		{Media: &telegram.Media{Kind: telegram.MediaAnimation}},
		{Media: &telegram.Media{Kind: telegram.MediaDocument, MIMEType: "image/gif"}},
		{Media: &telegram.Media{Kind: telegram.MediaSticker}},
		{Sticker: &telegram.Sticker{}, Media: &telegram.Media{Kind: telegram.MediaPhoto}},
	} {
		if _, err := supportedMediaKind(message); err == nil {
			t.Fatalf("unsupported media was accepted: %+v", message)
		}
	}
}

func TestNormalizeMediaMIME(t *testing.T) {
	t.Parallel()

	jpeg := []byte{0xff, 0xd8, 0xff, 0xdb}
	mimeType, extension, err := normalizeMediaMIME("image", "", "", jpeg)
	if err != nil || mimeType != "image/jpeg" || extension != ".jpg" {
		t.Fatalf("JPEG normalization = %q, %q, %v", mimeType, extension, err)
	}
	mp4 := append([]byte{0, 0, 0, 24}, []byte("ftypisom")...)
	mimeType, extension, err = normalizeMediaMIME("video", "", "clip.mp4", mp4)
	if err != nil || mimeType != "video/mp4" || extension != ".mp4" {
		t.Fatalf("MP4 normalization = %q, %q, %v", mimeType, extension, err)
	}
	if _, _, err := normalizeMediaMIME("image", "image/gif", "a.gif", []byte("GIF89a")); err == nil {
		t.Fatal("GIF was accepted")
	}
}

func TestCappedBuffer(t *testing.T) {
	t.Parallel()

	buffer := &cappedBuffer{limit: 4}
	if _, err := buffer.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("5")); !errors.Is(err, errAttachmentTooLarge) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestAttachmentMarkdown(t *testing.T) {
	t.Parallel()

	image := attachmentMarkdown(uploadedAttachment{
		FileName: "截图.png",
		URL:      "/api/files/01k00000000000000000000000",
	}, "image")
	if !strings.HasPrefix(image, "![截图]") {
		t.Fatalf("image markdown = %q", image)
	}
	video := attachmentMarkdown(uploadedAttachment{
		URL: "/api/files/01k00000000000000000000000",
	}, "video")
	if !strings.Contains(video, "<video controls") || !strings.Contains(video, "preload=\"metadata\"") {
		t.Fatalf("video markdown = %q", video)
	}
}
