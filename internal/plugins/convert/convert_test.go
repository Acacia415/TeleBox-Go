package convert

import "testing"

func TestParseSongInfo(t *testing.T) {
	got, err := parseSongInfo(`{"title":"稻香","artist":"周杰伦","album":"魔杰座"}`, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "稻香" || got.Artist != "周杰伦" || got.Album != "魔杰座" {
		t.Fatalf("song info = %+v", got)
	}
}

func TestParseSongInfoLegacyText(t *testing.T) {
	got, err := parseSongInfo("歌曲名：晴天\n歌手：周杰伦\n专辑：叶惠美", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "晴天" || got.Artist != "周杰伦" || got.Album != "叶惠美" {
		t.Fatalf("song info = %+v", got)
	}
}

func TestSafeFilename(t *testing.T) {
	if got := safeFilename(` 周杰伦: "稻香"?.mp3 `); got != "周杰伦 稻香.mp3" {
		t.Fatalf("safe filename = %q", got)
	}
	if got := safeExtension("video.MP4"); got != ".mp4" {
		t.Fatalf("safe extension = %q", got)
	}
}
