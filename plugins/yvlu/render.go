package yvlu

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stdDraw "image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const canvasSize = 512

type quoteItem struct {
	Sender string
	Text   string
	Reply  string
	Avatar image.Image
	Media  image.Image
}

type renderer struct {
	font *opentype.Font
}

func newRenderer(assetsDir string) (*renderer, error) {
	candidates := []string{
		filepath.Join(assetsDir, "fonts", "NotoSansCJK-Regular.ttc"),
		filepath.Join(assetsDir, "fonts", "NotoSansCJK-Regular.otf"),
		filepath.Join(assetsDir, "fonts", "NotoSansSC-Regular.otf"),
	}
	if runtime.GOOS == "windows" {
		windows := os.Getenv("WINDIR")
		if windows == "" {
			windows = `C:\Windows`
		}
		candidates = append(candidates,
			filepath.Join(windows, "Fonts", "msyh.ttc"),
			filepath.Join(windows, "Fonts", "msyh.ttf"),
			filepath.Join(windows, "Fonts", "simhei.ttf"),
		)
	} else {
		candidates = append(candidates,
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		)
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		parsed, err := parseOpenType(data, filepath.Ext(candidate))
		if err == nil {
			return &renderer{font: parsed}, nil
		}
	}
	fallback, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	return &renderer{font: fallback}, nil
}

func parseOpenType(data []byte, extension string) (*opentype.Font, error) {
	if strings.EqualFold(extension, ".ttc") {
		collection, err := opentype.ParseCollection(data)
		if err != nil {
			return nil, err
		}
		return collection.Font(0)
	}
	return opentype.Parse(data)
}

func (r *renderer) render(items []quoteItem) ([]byte, error) {
	if len(items) == 0 || len(items) > 5 {
		return nil, errors.New("quote item count must be between 1 and 5")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, canvasSize, canvasSize))
	drawGradient(canvas)
	margin := 10
	gap := 8
	cardHeight := (canvasSize - margin*2 - gap*(len(items)-1)) / len(items)
	for index, item := range items {
		y := margin + index*(cardHeight+gap)
		if err := r.drawItem(
			canvas,
			image.Rect(margin, y, canvasSize-margin, y+cardHeight),
			item,
			len(items),
		); err != nil {
			return nil, err
		}
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (r *renderer) drawItem(
	canvas *image.RGBA,
	bounds image.Rectangle,
	item quoteItem,
	itemCount int,
) error {
	drawRoundedRect(canvas, bounds, 20, color.RGBA{35, 28, 49, 245})
	padding := 12
	avatarSize := 46
	if bounds.Dy() < 95 {
		avatarSize = 34
	}
	avatarX := bounds.Min.X + padding
	avatarY := bounds.Min.Y + padding
	if item.Avatar != nil {
		drawCircleImage(canvas, item.Avatar, avatarX, avatarY, avatarSize)
	} else {
		drawAvatarPlaceholder(canvas, avatarX, avatarY, avatarSize, item.Sender)
	}

	senderFace, err := opentype.NewFace(r.font, &opentype.FaceOptions{
		Size:    15,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return err
	}
	textSize := 20.0
	if itemCount >= 3 {
		textSize = 17
	}
	if itemCount == 5 {
		textSize = 15
	}
	textFace, err := opentype.NewFace(r.font, &opentype.FaceOptions{
		Size:    textSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return err
	}
	replyFace, err := opentype.NewFace(r.font, &opentype.FaceOptions{
		Size:    12,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return err
	}

	textX := avatarX + avatarSize + 12
	right := bounds.Max.X - padding
	if item.Media != nil && bounds.Dy() >= 105 {
		thumbnailSize := minInt(96, bounds.Dy()-padding*2)
		drawRoundedImage(
			canvas,
			item.Media,
			image.Rect(
				right-thumbnailSize,
				bounds.Min.Y+padding,
				right,
				bounds.Min.Y+padding+thumbnailSize,
			),
			12,
		)
		right -= thumbnailSize + 10
	}
	drawString(
		canvas,
		senderFace,
		color.RGBA{203, 169, 255, 255},
		textX,
		bounds.Min.Y+padding+16,
		truncateText(senderFace, firstNonEmpty(item.Sender, "Unknown"), right-textX),
	)
	currentY := bounds.Min.Y + padding + 38
	if strings.TrimSpace(item.Reply) != "" && bounds.Dy() >= 90 {
		reply := "↪ " + strings.ReplaceAll(strings.TrimSpace(item.Reply), "\n", " ")
		drawString(
			canvas,
			replyFace,
			color.RGBA{168, 157, 184, 255},
			textX,
			currentY,
			truncateText(replyFace, reply, right-textX),
		)
		currentY += 18
	}
	lineHeight := int(textSize) + 5
	maxLines := (bounds.Max.Y - padding - currentY) / lineHeight
	if maxLines < 1 {
		maxLines = 1
	}
	lines := wrapText(textFace, firstNonEmpty(item.Text, mediaLabel(item)), right-textX, maxLines)
	for _, line := range lines {
		drawString(
			canvas,
			textFace,
			color.RGBA{246, 242, 250, 255},
			textX,
			currentY,
			line,
		)
		currentY += lineHeight
	}
	return nil
}

func drawGradient(destination *image.RGBA) {
	for y := 0; y < destination.Bounds().Dy(); y++ {
		ratio := float64(y) / float64(destination.Bounds().Dy())
		red := uint8(25 + 13*ratio)
		green := uint8(17 + 10*ratio)
		blue := uint8(39 + 23*ratio)
		stdDraw.Draw(
			destination,
			image.Rect(0, y, canvasSize, y+1),
			&image.Uniform{C: color.RGBA{red, green, blue, 255}},
			image.Point{},
			stdDraw.Src,
		)
	}
}

func drawRoundedRect(
	destination *image.RGBA,
	bounds image.Rectangle,
	radius int,
	fill color.RGBA,
) {
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if insideRounded(bounds, radius, x, y) {
				destination.SetRGBA(x, y, fill)
			}
		}
	}
}

func insideRounded(bounds image.Rectangle, radius, x, y int) bool {
	if x >= bounds.Min.X+radius && x < bounds.Max.X-radius {
		return true
	}
	if y >= bounds.Min.Y+radius && y < bounds.Max.Y-radius {
		return true
	}
	centerX := bounds.Min.X + radius
	if x >= bounds.Max.X-radius {
		centerX = bounds.Max.X - radius - 1
	}
	centerY := bounds.Min.Y + radius
	if y >= bounds.Max.Y-radius {
		centerY = bounds.Max.Y - radius - 1
	}
	dx := x - centerX
	dy := y - centerY
	return dx*dx+dy*dy <= radius*radius
}

func drawCircleImage(
	destination *image.RGBA,
	source image.Image,
	x, y, size int,
) {
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(
		scaled,
		scaled.Bounds(),
		source,
		source.Bounds(),
		stdDraw.Over,
		nil,
	)
	radius := size / 2
	for targetY := 0; targetY < size; targetY++ {
		for targetX := 0; targetX < size; targetX++ {
			dx := targetX - radius
			dy := targetY - radius
			if dx*dx+dy*dy <= radius*radius {
				destination.Set(x+targetX, y+targetY, scaled.At(targetX, targetY))
			}
		}
	}
}

func drawAvatarPlaceholder(
	destination *image.RGBA,
	x, y, size int,
	name string,
) {
	fill := color.RGBA{110, 73, 156, 255}
	radius := size / 2
	for targetY := 0; targetY < size; targetY++ {
		for targetX := 0; targetX < size; targetX++ {
			dx := targetX - radius
			dy := targetY - radius
			if dx*dx+dy*dy <= radius*radius {
				destination.SetRGBA(x+targetX, y+targetY, fill)
			}
		}
	}
	_ = name
}

func drawRoundedImage(
	destination *image.RGBA,
	source image.Image,
	bounds image.Rectangle,
	radius int,
) {
	scaled := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.CatmullRom.Scale(
		scaled,
		scaled.Bounds(),
		source,
		source.Bounds(),
		stdDraw.Over,
		nil,
	)
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			if insideRounded(
				image.Rect(0, 0, bounds.Dx(), bounds.Dy()),
				radius,
				x,
				y,
			) {
				destination.Set(bounds.Min.X+x, bounds.Min.Y+y, scaled.At(x, y))
			}
		}
	}
}

func drawString(
	destination stdDraw.Image,
	face font.Face,
	fill color.Color,
	x, baseline int,
	text string,
) {
	drawer := font.Drawer{
		Dst:  destination,
		Src:  image.NewUniform(fill),
		Face: face,
		Dot:  fixed.P(x, baseline),
	}
	drawer.DrawString(text)
}

func wrapText(
	face font.Face,
	text string,
	maxWidth int,
	maxLines int,
) []string {
	text = strings.TrimSpace(text)
	if text == "" || maxWidth <= 0 || maxLines <= 0 {
		return nil
	}
	var result []string
	var line []rune
	for _, character := range []rune(text) {
		if character == '\n' {
			result = append(result, string(line))
			line = nil
			if len(result) >= maxLines {
				break
			}
			continue
		}
		candidate := append(append([]rune(nil), line...), character)
		if font.MeasureString(face, string(candidate)).Ceil() > maxWidth && len(line) > 0 {
			result = append(result, string(line))
			line = []rune{character}
			if len(result) >= maxLines {
				break
			}
		} else {
			line = candidate
		}
	}
	if len(result) < maxLines && len(line) > 0 {
		result = append(result, string(line))
	}
	consumed := strings.Join(result, "")
	normalized := strings.ReplaceAll(text, "\n", "")
	if len([]rune(consumed)) < len([]rune(normalized)) && len(result) > 0 {
		result[len(result)-1] = truncateText(
			face,
			strings.TrimSuffix(result[len(result)-1], "…")+"…",
			maxWidth,
		)
	}
	return result
}

func truncateText(face font.Face, text string, maxWidth int) string {
	if font.MeasureString(face, text).Ceil() <= maxWidth {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if font.MeasureString(face, candidate).Ceil() <= maxWidth {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func mediaLabel(item quoteItem) string {
	if item.Media != nil {
		return "[媒体]"
	}
	return ""
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
