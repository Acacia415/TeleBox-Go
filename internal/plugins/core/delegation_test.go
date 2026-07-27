package core

import "testing"

func TestMatchSureMessage(t *testing.T) {
	t.Parallel()

	records := []allowedMessage{
		{ID: 1, Message: "hello", Redirect: "world"},
		{ID: 2, Message: "_command:/sb", Redirect: "-ban"},
	}
	if got, ok := matchSureMessage(records, "hello"); !ok || got != "world" {
		t.Fatalf("exact match = %q, %v", got, ok)
	}
	if got, ok := matchSureMessage(records, "/sb 123"); !ok || got != "-ban 123" {
		t.Fatalf("command match = %q, %v", got, ok)
	}
	if _, ok := matchSureMessage(records, "/sbot"); ok {
		t.Fatal("partial command unexpectedly matched")
	}
}

func TestRawAfterWordsPreservesMessageSpacing(t *testing.T) {
	t.Parallel()

	if got := rawAfterWords("msg add hello   world", 2); got != "hello   world" {
		t.Fatalf("rawAfterWords() = %q", got)
	}
}
