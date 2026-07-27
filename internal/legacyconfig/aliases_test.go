package legacyconfig

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadAliases(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "alias.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE aliases (
			original TEXT PRIMARY KEY,
			final TEXT NOT NULL
		);
		INSERT INTO aliases(original, final) VALUES
			('de', 'bd'),
			('qa', 'yvlu');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ReadAliases(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"de": "bd", "qa": "yvlu"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadAliases() = %#v, want %#v", got, want)
	}
}

func TestReadAliasesMissingDatabase(t *testing.T) {
	t.Parallel()

	got, err := ReadAliases(filepath.Join(t.TempDir(), "missing.db"))
	if err != nil || got != nil {
		t.Fatalf("ReadAliases() = %#v, %v", got, err)
	}
}
