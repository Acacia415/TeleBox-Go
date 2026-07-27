package cezi

import "testing"

func TestFirstChinese(t *testing.T) {
	if got := firstChinese("abc缘分"); got != "缘" {
		t.Fatalf("firstChinese = %q", got)
	}
}

func TestRandomMeaningfulChinese(t *testing.T) {
	got, err := randomMeaningfulChinese("你的缘")
	if err != nil {
		t.Fatal(err)
	}
	if got != "缘" {
		t.Fatalf("random character = %q", got)
	}
	if _, err := randomMeaningfulChinese("你我的"); err == nil {
		t.Fatal("excluded-only message succeeded")
	}
}

func TestCleanText(t *testing.T) {
	if got := cleanText("\uFEFF【起卦】\x00妙哉\n"); got != "【起卦】妙哉" {
		t.Fatalf("cleanText = %q", got)
	}
}
