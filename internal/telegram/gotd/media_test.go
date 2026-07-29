package gotd

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestDocumentMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		attributes []tg.DocumentAttributeClass
		wantKind   teleboxtelegram.MediaKind
	}{
		{
			name:       "ordinary document",
			attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "report.pdf"}},
			wantKind:   teleboxtelegram.MediaDocument,
		},
		{
			name: "video",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "clip.mp4"},
				&tg.DocumentAttributeVideo{W: 1920, H: 1080, Duration: 3.5},
			},
			wantKind: teleboxtelegram.MediaVideo,
		},
		{
			name: "round video",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeVideo{RoundMessage: true, W: 512, H: 512, Duration: 5},
			},
			wantKind: teleboxtelegram.MediaVideoNote,
		},
		{
			name: "voice",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAudio{Voice: true, Duration: 7},
			},
			wantKind: teleboxtelegram.MediaVoice,
		},
		{
			name: "animation takes precedence over video",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAnimated{},
				&tg.DocumentAttributeVideo{W: 640, H: 360, Duration: 2},
			},
			wantKind: teleboxtelegram.MediaAnimation,
		},
		{
			name: "sticker takes precedence",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAnimated{},
				&tg.DocumentAttributeSticker{},
			},
			wantKind: teleboxtelegram.MediaSticker,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := documentMetadata(&tg.Document{
				ID:         10,
				MimeType:   "application/octet-stream",
				Size:       1234,
				Attributes: test.attributes,
			})
			if got.Kind != test.wantKind {
				t.Fatalf("documentMetadata().Kind = %q, want %q", got.Kind, test.wantKind)
			}
			if got.Size != 1234 {
				t.Fatalf("documentMetadata().Size = %d, want 1234", got.Size)
			}
		})
	}
}

func TestStableMessageIncludesPortableMediaAndReply(t *testing.T) {
	t.Parallel()

	raw := &tg.Message{
		ID:      11,
		Out:     true,
		PeerID:  &tg.PeerChannel{ChannelID: 5},
		Message: "caption",
		Date:    100,
	}
	reply := &tg.MessageReplyHeader{}
	reply.SetReplyToMsgID(9)
	reply.SetQuoteText("quoted")
	reply.SetQuoteEntities([]tg.MessageEntityClass{
		&tg.MessageEntityItalic{Offset: 0, Length: 6},
	})
	raw.SetReplyTo(reply)
	raw.SetGroupedID(99)
	raw.Entities = []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 7},
		&tg.MessageEntityTextURL{
			Offset: 0,
			Length: 7,
			URL:    "https://example.com",
		},
		&tg.MessageEntityCustomEmoji{Offset: 7, Length: 2, DocumentID: 123},
	}
	documentMedia := &tg.MessageMediaDocument{}
	documentMedia.SetDocument(&tg.Document{
		ID:       12,
		MimeType: "audio/ogg",
		Size:     42,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAudio{Voice: true, Duration: 3},
		},
	})
	raw.SetMedia(documentMedia)

	got := stableMessage(raw, -1000000000005, 77)
	if got.SenderID != 77 || got.ReplyToID != 9 || got.GroupedID != 99 {
		t.Fatalf("stableMessage() = %+v", got)
	}
	if got.Media == nil || got.Media.Kind != teleboxtelegram.MediaVoice ||
		got.Media.Duration != 3*time.Second {
		t.Fatalf("stableMessage().Media = %+v", got.Media)
	}
	if got.ReplyQuote != "quoted" || len(got.ReplyEntities) != 1 ||
		got.ReplyEntities[0].Type != "italic" {
		t.Fatalf("stableMessage().ReplyEntities = %+v", got.ReplyEntities)
	}
	if len(got.Entities) != 3 ||
		got.Entities[0].Type != "bold" ||
		got.Entities[1].Type != "text_link" ||
		got.Entities[1].URL != "https://example.com" ||
		got.Entities[2].DocumentID != 123 {
		t.Fatalf("stableMessage().Entities = %+v", got.Entities)
	}
}

func TestStableMessageTreatsSavedMessagesFromSelfAsOutgoing(t *testing.T) {
	t.Parallel()

	raw := &tg.Message{
		ID:      7,
		PeerID:  &tg.PeerUser{UserID: 77},
		FromID:  &tg.PeerUser{UserID: 77},
		Message: ".plugins",
		Date:    100,
	}

	got := stableMessage(raw, 77, 77)
	if !got.Outgoing {
		t.Fatal("stableMessage().Outgoing = false for a Saved Messages post authored by self")
	}
	if got.SenderID != 77 {
		t.Fatalf("stableMessage().SenderID = %d, want 77", got.SenderID)
	}
}

func TestLargestPhotoSize(t *testing.T) {
	t.Parallel()

	kind, width, height, size := largestPhotoSize([]tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 1000},
		&tg.PhotoSizeProgressive{Type: "x", W: 1280, H: 720, Sizes: []int{2000, 8000}},
	})
	if kind != "x" || width != 1280 || height != 720 || size != 8000 {
		t.Fatalf("largestPhotoSize() = %q, %d, %d, %d", kind, width, height, size)
	}
}
