package legacyconfig

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestCandidatePathsUsesActiveThenPreservedAssets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	active := filepath.Join(root, "assets")
	preserved := filepath.Join(root, "legacy-assets")
	got := CandidatePaths(
		active,
		preserved,
		"plugin/config.db",
		"root.db",
	)
	want := []string{
		filepath.Join(active, "plugin", "config.db"),
		filepath.Join(active, "root.db"),
		filepath.Join(preserved, "plugin", "config.db"),
		filepath.Join(preserved, "root.db"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CandidatePaths() = %#v, want %#v", got, want)
	}
}

func TestCandidatePathsRemovesDuplicateRootsAndUnsafeAbsolutePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got := CandidatePaths(root, root, "config.db", filepath.Join(root, "absolute.db"))
	want := []string{filepath.Join(root, "config.db")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CandidatePaths() = %#v, want %#v", got, want)
	}
}
