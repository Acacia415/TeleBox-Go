package pluginbuilder

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnalyzePermissions(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source := []byte(`package example
func run(p *Plugin) {
	p.services.Telegram.SendText()
	p.services.Telegram.ResolveUser()
	p.services.Storage.Get()
	p.services.HTTP.Do()
	p.services.Tools.Run()
	telegram.ReplyHTML()
}`)
	if err := os.WriteFile(filepath.Join(directory, "plugin.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	permissions, err := AnalyzePermissions(directory)
	if err != nil {
		t.Fatal(err)
	}
	wantTelegram := []string{"reply_html", "resolve_user", "send_text"}
	if !reflect.DeepEqual(permissions.Telegram, wantTelegram) {
		t.Fatalf("Telegram = %#v, want %#v", permissions.Telegram, wantTelegram)
	}
	if !permissions.Storage || !permissions.Network {
		t.Fatalf("permissions = %+v", permissions)
	}
	if !reflect.DeepEqual(permissions.Tools, []string{"*"}) {
		t.Fatalf("Tools = %#v", permissions.Tools)
	}
}

func TestCamelToSnake(t *testing.T) {
	t.Parallel()
	if got := camelToSnake("GetMyPermissions"); got != "get_my_permissions" {
		t.Fatalf("camelToSnake() = %q", got)
	}
}
