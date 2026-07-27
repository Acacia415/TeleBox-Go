package legacyconfig

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestReadSQLiteConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		"CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		"INSERT INTO config(key, value) VALUES (?, ?)", "api_key", "secret",
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	values, err := ReadSQLiteConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["api_key"]; got != "secret" {
		t.Fatalf("api_key = %q, want secret", got)
	}
}

func TestReadSQLiteConfigMissing(t *testing.T) {
	t.Parallel()
	values, err := ReadSQLiteConfig(filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	if values != nil {
		t.Fatalf("values = %#v, want nil", values)
	}
}
