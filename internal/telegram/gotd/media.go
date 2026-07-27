package gotd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	gotdmessage "github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) SendFile(
	ctx context.Context,
	chatID int64,
	upload teleboxtelegram.Upload,
) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}
	if upload.Path == "" {
		return teleboxtelegram.SentMessage{}, errors.New("upload path is required")
	}
	info, err := os.Stat(upload.Path)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("inspect upload: %w", err)
	}
	if !info.Mode().IsRegular() {
		return teleboxtelegram.SentMessage{}, errors.New("upload path must point to a regular file")
	}

	kind := upload.Kind
	if kind == "" {
		kind = teleboxtelegram.MediaDocument
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	builder := sender.To(peer).CloneBuilder()
	if upload.ReplyToID > 0 {
		builder = builder.Reply(upload.ReplyToID)
	}
	input, err := builder.Upload(gotdmessage.FromPath(upload.Path)).AsInputFile(ctx)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("upload file: %w", err)
	}

	var caption []gotdmessage.StyledTextOption
	if upload.Caption != "" {
		caption = append(caption, styling.Plain(upload.Caption))
	}
	name := filepath.Base(upload.FileName)
	if name == "." || name == "" {
		name = filepath.Base(upload.Path)
	}

	var media gotdmessage.MediaOption
	switch kind {
	case teleboxtelegram.MediaPhoto:
		media = gotdmessage.UploadedPhoto(input, caption...).Spoiler(upload.Spoiler)
	case teleboxtelegram.MediaDocument:
		media = configureDocument(gotdmessage.UploadedDocument(input, caption...), name, upload.MIMEType).
			Spoiler(upload.Spoiler).
			ForceFile(true)
	case teleboxtelegram.MediaAudio:
		audio := configureDocument(gotdmessage.UploadedDocument(input, caption...), name, upload.MIMEType).
			Spoiler(upload.Spoiler).
			Audio()
		if upload.Duration > 0 {
			audio.Duration(upload.Duration)
		}
		if upload.AudioTitle != "" {
			audio.Title(upload.AudioTitle)
		}
		if upload.Performer != "" {
			audio.Performer(upload.Performer)
		}
		media = audio
	case teleboxtelegram.MediaVoice:
		voice := configureDocument(gotdmessage.UploadedDocument(input, caption...), name, upload.MIMEType).
			Spoiler(upload.Spoiler).
			Voice()
		if upload.Duration > 0 {
			voice.Duration(upload.Duration)
		}
		media = voice
	case teleboxtelegram.MediaVideo, teleboxtelegram.MediaVideoNote:
		videoDocument := configureDocument(
			gotdmessage.UploadedDocument(input, caption...),
			name,
			upload.MIMEType,
		).Spoiler(upload.Spoiler)
		video := videoDocument.Video()
		if kind == teleboxtelegram.MediaVideoNote {
			video.Round()
		} else {
			video.SupportsStreaming()
		}
		if upload.Width > 0 && upload.Height > 0 {
			video.Resolution(upload.Width, upload.Height)
		}
		if upload.Duration > 0 {
			video.Duration(upload.Duration)
		}
		media = video
	case teleboxtelegram.MediaAnimation:
		media = configureDocument(
			gotdmessage.UploadedDocument(input, caption...),
			name,
			upload.MIMEType,
		).Spoiler(upload.Spoiler).GIF()
	case teleboxtelegram.MediaSticker:
		sticker := configureDocument(gotdmessage.UploadedDocument(input, caption...), name, upload.MIMEType).
			Spoiler(upload.Spoiler).
			UploadedSticker()
		if upload.StickerEmoji != "" {
			sticker.Alt(upload.StickerEmoji)
		}
		media = sticker
	default:
		return teleboxtelegram.SentMessage{}, fmt.Errorf("unsupported upload media kind %q", kind)
	}

	updates, err := builder.Media(ctx, media)
	messageID, err := unpack.MessageID(updates, err)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("send file: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func configureDocument(
	document *gotdmessage.UploadedDocumentBuilder,
	name string,
	mimeType string,
) *gotdmessage.UploadedDocumentBuilder {
	if name != "" {
		document.Filename(name)
	}
	if mimeType != "" {
		document.MIME(mimeType)
	}
	return document
}

func (c *Client) GetMessages(ctx context.Context, chatID int64, ids []int) ([]teleboxtelegram.Message, error) {
	rawMessages, err := c.getRawMessages(ctx, chatID, ids)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	selfID := c.selfID
	c.mu.RUnlock()

	messages := make([]teleboxtelegram.Message, 0, len(rawMessages))
	for _, class := range rawMessages {
		raw, ok := class.(*tg.Message)
		if !ok {
			continue
		}
		messages = append(messages, stableMessage(raw, chatID, selfID))
	}
	if len(messages) == 0 {
		return nil, teleboxtelegram.ErrMessageNotFound
	}
	return messages, nil
}

func (c *Client) DownloadMedia(
	ctx context.Context,
	chatID int64,
	messageID int,
	output io.Writer,
) (teleboxtelegram.Media, error) {
	if output == nil {
		return teleboxtelegram.Media{}, errors.New("download output is required")
	}
	rawMessages, err := c.getRawMessages(ctx, chatID, []int{messageID})
	if err != nil {
		return teleboxtelegram.Media{}, err
	}
	if len(rawMessages) == 0 {
		return teleboxtelegram.Media{}, teleboxtelegram.ErrMessageNotFound
	}
	raw, ok := rawMessages[0].(*tg.Message)
	if !ok {
		return teleboxtelegram.Media{}, teleboxtelegram.ErrMessageNotFound
	}
	mediaClass, ok := raw.GetMedia()
	if !ok {
		return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
	}
	metadata := mediaMetadata(mediaClass)
	if metadata == nil {
		return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
	}

	var location tg.InputFileLocationClass
	switch media := mediaClass.(type) {
	case *tg.MessageMediaDocument:
		class, exists := media.GetDocument()
		if !exists {
			return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
		}
		document, exists := class.AsNotEmpty()
		if !exists {
			return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
		}
		location = document.AsInputDocumentFileLocation("")
	case *tg.MessageMediaPhoto:
		class, exists := media.GetPhoto()
		if !exists {
			return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
		}
		photo, exists := class.AsNotEmpty()
		if !exists {
			return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
		}
		thumbType, _, _, _ := largestPhotoSize(photo.Sizes)
		if thumbType == "" {
			return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
		}
		location = photo.AsInputPhotoFileLocation(thumbType)
	default:
		return teleboxtelegram.Media{}, teleboxtelegram.ErrMediaNotFound
	}

	if _, err := c.raw.Download(location).Stream(ctx, output); err != nil {
		return teleboxtelegram.Media{}, fmt.Errorf("download media: %w", err)
	}
	return *metadata, nil
}

func (c *Client) DownloadProfilePhoto(
	ctx context.Context,
	userID int64,
	output io.Writer,
) error {
	if userID <= 0 {
		return errors.New("profile photo user ID must be greater than zero")
	}
	if output == nil {
		return errors.New("profile photo output is required")
	}
	c.mu.RLock()
	ready := c.sender != nil
	c.mu.RUnlock()
	if !ready {
		return teleboxtelegram.ErrTransportUnavailable
	}
	user, err := c.peers.ResolveUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve profile photo user: %w", err)
	}
	photo, ok, err := user.Photo(ctx)
	if err != nil {
		return fmt.Errorf("get profile photo: %w", err)
	}
	if !ok {
		return teleboxtelegram.ErrMediaNotFound
	}
	thumbType, _, _, _ := largestPhotoSize(photo.Sizes)
	if thumbType == "" {
		return teleboxtelegram.ErrMediaNotFound
	}
	if _, err := c.raw.Download(photo.AsInputPhotoFileLocation(thumbType)).
		Stream(ctx, output); err != nil {
		return fmt.Errorf("download profile photo: %w", err)
	}
	return nil
}

func (c *Client) getRawMessages(ctx context.Context, chatID int64, ids []int) ([]tg.MessageClass, error) {
	c.mu.RLock()
	ready := c.sender != nil
	c.mu.RUnlock()
	if !ready {
		return nil, teleboxtelegram.ErrTransportUnavailable
	}
	if len(ids) == 0 {
		return nil, errors.New("at least one message ID is required")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, errors.New("message IDs must be greater than zero")
		}
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	messages, err := query.Messages(c.raw.API()).GetMessages(ctx, peer, ids...)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	return messages, nil
}

func stableMessage(raw *tg.Message, chatID, selfID int64) teleboxtelegram.Message {
	senderID := int64(0)
	if from, exists := raw.GetFromID(); exists {
		senderID, _ = peerID(from)
	} else if raw.Out {
		senderID = selfID
	} else if _, isUser := raw.PeerID.(*tg.PeerUser); isUser {
		senderID = chatID
	}

	replyToID := 0
	if reply, exists := raw.GetReplyTo(); exists {
		if header, ok := reply.(*tg.MessageReplyHeader); ok {
			replyToID, _ = header.GetReplyToMsgID()
		}
	}
	groupedID, _ := raw.GetGroupedID()
	customEmojiIDs := make([]int64, 0)
	for _, entity := range raw.Entities {
		if custom, ok := entity.(*tg.MessageEntityCustomEmoji); ok {
			customEmojiIDs = append(customEmojiIDs, custom.DocumentID)
		}
	}
	outgoing := raw.Out || selfID != 0 && senderID == selfID
	return teleboxtelegram.Message{
		ID:             raw.ID,
		ChatID:         chatID,
		SenderID:       senderID,
		ReplyToID:      replyToID,
		Text:           raw.Message,
		Outgoing:       outgoing,
		Date:           time.Unix(int64(raw.Date), 0),
		GroupedID:      groupedID,
		Media:          mediaMetadataFromMessage(raw),
		Sticker:        stickerReference(raw),
		CustomEmojiIDs: customEmojiIDs,
	}
}

func stickerReference(raw *tg.Message) *teleboxtelegram.Sticker {
	mediaClass, ok := raw.GetMedia()
	if !ok {
		return nil
	}
	media, ok := mediaClass.(*tg.MessageMediaDocument)
	if !ok {
		return nil
	}
	class, ok := media.GetDocument()
	if !ok {
		return nil
	}
	document, ok := class.AsNotEmpty()
	if !ok {
		return nil
	}
	result := &teleboxtelegram.Sticker{
		DocumentID:    document.ID,
		AccessHash:    document.AccessHash,
		FileReference: append([]byte(nil), document.FileReference...),
		Animated:      document.MimeType == "application/x-tgsticker",
		Video:         document.MimeType == "video/webm",
	}
	for _, attribute := range document.Attributes {
		if sticker, ok := attribute.(*tg.DocumentAttributeSticker); ok {
			result.Emoji = sticker.Alt
			return result
		}
	}
	return nil
}

func mediaMetadataFromMessage(raw *tg.Message) *teleboxtelegram.Media {
	media, ok := raw.GetMedia()
	if !ok {
		return nil
	}
	return mediaMetadata(media)
}

func mediaMetadata(media tg.MessageMediaClass) *teleboxtelegram.Media {
	switch value := media.(type) {
	case *tg.MessageMediaPhoto:
		class, ok := value.GetPhoto()
		if !ok {
			return nil
		}
		photo, ok := class.AsNotEmpty()
		if !ok {
			return nil
		}
		_, width, height, size := largestPhotoSize(photo.Sizes)
		return &teleboxtelegram.Media{
			Kind:     teleboxtelegram.MediaPhoto,
			FileName: fmt.Sprintf("photo_%d.jpg", photo.ID),
			MIMEType: "image/jpeg",
			Size:     size,
			Width:    width,
			Height:   height,
		}
	case *tg.MessageMediaDocument:
		class, ok := value.GetDocument()
		if !ok {
			return nil
		}
		document, ok := class.AsNotEmpty()
		if !ok {
			return nil
		}
		return documentMetadata(document)
	default:
		return nil
	}
}

func documentMetadata(document *tg.Document) *teleboxtelegram.Media {
	result := &teleboxtelegram.Media{
		Kind:     teleboxtelegram.MediaDocument,
		FileName: fmt.Sprintf("document_%d", document.ID),
		MIMEType: document.MimeType,
		Size:     document.Size,
	}
	var (
		animated bool
		sticker  bool
		video    bool
		round    bool
		audio    bool
		voice    bool
	)
	for _, attribute := range document.Attributes {
		switch value := attribute.(type) {
		case *tg.DocumentAttributeFilename:
			result.FileName = value.FileName
		case *tg.DocumentAttributeImageSize:
			result.Width = value.W
			result.Height = value.H
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeSticker, *tg.DocumentAttributeCustomEmoji:
			sticker = true
		case *tg.DocumentAttributeVideo:
			video = true
			round = value.RoundMessage
			result.Width = value.W
			result.Height = value.H
			result.Duration = time.Duration(value.Duration * float64(time.Second))
		case *tg.DocumentAttributeAudio:
			audio = true
			voice = value.Voice
			result.Duration = time.Duration(value.Duration) * time.Second
		}
	}
	switch {
	case sticker:
		result.Kind = teleboxtelegram.MediaSticker
	case voice:
		result.Kind = teleboxtelegram.MediaVoice
	case audio:
		result.Kind = teleboxtelegram.MediaAudio
	case round:
		result.Kind = teleboxtelegram.MediaVideoNote
	case animated:
		result.Kind = teleboxtelegram.MediaAnimation
	case video:
		result.Kind = teleboxtelegram.MediaVideo
	}
	return result
}

func largestPhotoSize(sizes []tg.PhotoSizeClass) (kind string, width, height int, bytes int64) {
	var score int64
	for _, class := range sizes {
		var candidateType string
		var candidateWidth, candidateHeight int
		var candidateBytes int64
		switch value := class.(type) {
		case *tg.PhotoSize:
			candidateType = value.Type
			candidateWidth = value.W
			candidateHeight = value.H
			candidateBytes = int64(value.Size)
		case *tg.PhotoSizeProgressive:
			candidateType = value.Type
			candidateWidth = value.W
			candidateHeight = value.H
			for _, size := range value.Sizes {
				if int64(size) > candidateBytes {
					candidateBytes = int64(size)
				}
			}
		case *tg.PhotoCachedSize:
			candidateType = value.Type
			candidateWidth = value.W
			candidateHeight = value.H
			candidateBytes = int64(len(value.Bytes))
		default:
			continue
		}
		candidateScore := int64(candidateWidth) * int64(candidateHeight)
		if candidateScore > score || (candidateScore == score && candidateBytes > bytes) {
			kind = candidateType
			width = candidateWidth
			height = candidateHeight
			bytes = candidateBytes
			score = candidateScore
		}
	}
	return kind, width, height, bytes
}
