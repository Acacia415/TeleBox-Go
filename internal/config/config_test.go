package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaultsAndResolvesPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{
		"telegram": {"api_id": 123, "api_hash": "secret", "session_file": "state/session.json"},
		"commands": {"prefixes": ["."], "owner_ids": [42]},
		"storage": {"path": "state/telebox.db"},
		"plugins": {"enabled": [], "disabled": []},
		"logging": {"level": "info", "format": "text"}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := filepath.Join(dir, "state", "session.json"); cfg.Telegram.SessionFile != want {
		t.Fatalf("SessionFile = %q, want %q", cfg.Telegram.SessionFile, want)
	}
	if want := filepath.Join(dir, "state", "telebox.db"); cfg.Storage.Path != want {
		t.Fatalf("Storage.Path = %q, want %q", cfg.Storage.Path, want)
	}
	if want := filepath.Join(dir, "data", "legacy-assets"); cfg.Storage.LegacyAssetsPath != want {
		t.Fatalf("Storage.LegacyAssetsPath = %q, want %q", cfg.Storage.LegacyAssetsPath, want)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"telegram":{"api_id":1,"api_hash":"x","session_file":"s"},"commands":{"prefixes":["."],"owner_ids":[]},"storage":{"path":"d"},"plugins":{"enabled":[],"disabled":[]},"logging":{"level":"info","format":"text"},"unexpected":true}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestLoadRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"telegram":{"api_id":1,"api_hash":"x","session_file":"s"},"commands":{"prefixes":["."],"owner_ids":[]},"storage":{"path":"d"},"plugins":{"enabled":[],"disabled":[]},"logging":{"level":"info","format":"text"}} {"another":true}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Load() error = %v, want trailing JSON error", err)
	}
}

func TestEnvironmentOverridesSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{
		"telegram": {"api_id": 1, "api_hash": "old", "session_file": "session.json"},
		"commands": {"prefixes": ["."], "owner_ids": []},
		"storage": {"path": "telebox.db"},
		"plugins": {"enabled": [], "disabled": []},
		"logging": {"level": "info", "format": "text"}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEBOX_API_ID", "999")
	t.Setenv("TELEBOX_API_HASH", "new-secret")
	t.Setenv("TELEBOX_LEGACY_ASSETS_PATH", "preserved")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Telegram.APIID != 999 || cfg.Telegram.APIHash != "new-secret" {
		t.Fatalf("environment overrides were not applied")
	}
	if want := filepath.Join(dir, "preserved"); cfg.Storage.LegacyAssetsPath != want {
		t.Fatalf("legacy asset environment override = %q, want %q", cfg.Storage.LegacyAssetsPath, want)
	}
}

func TestValidateAcceptsPhoneLogin(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Telegram.APIID = 123
	cfg.Telegram.APIHash = "hash"
	cfg.Telegram.SessionFile = "session.json"
	cfg.Telegram.LoginMode = "phone"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsOverlappingAssetPaths(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Telegram.APIID = 123
	cfg.Telegram.APIHash = "hash"
	cfg.Telegram.SessionFile = "session.json"
	cfg.Storage.LegacyAssetsPath = filepath.Join(cfg.Storage.AssetsPath, "legacy")
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "must not overlap") {
		t.Fatalf("Validate() error = %v, want overlapping path rejection", err)
	}
}
