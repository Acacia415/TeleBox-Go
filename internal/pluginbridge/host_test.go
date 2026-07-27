package pluginbridge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/pluginrpc"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

func TestHostRejectsUndeclaredPermission(t *testing.T) {
	t.Parallel()
	host, err := NewHost(service.Container{}, testManifest(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(HTTPRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Handle(context.Background(), MethodHTTPDo, raw); err == nil {
		t.Fatal("Handle() error = nil")
	} else {
		var remote *pluginrpc.RemoteError
		if !errors.As(err, &remote) || remote.Code != "permission_denied" {
			t.Fatalf("Handle() error = %v", err)
		}
	}
}

func TestHostIsolatesStorageNamespaceAndWorkDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manifest := testManifest()
	manifest.Permissions.Storage = true
	host, err := NewHost(service.Container{}, manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(StorageRequest{
		Plugin: "other",
		Key:    "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Handle(context.Background(), MethodStorageGet, raw); err == nil {
		t.Fatal("cross-plugin storage access was accepted")
	}
	if _, err := host.safePath(filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal("path outside the work directory was accepted")
	}
}

func testManifest() pluginapi.Manifest {
	return pluginapi.Manifest{
		SchemaVersion: pluginapi.ManifestSchemaVersion,
		APIVersion:    pluginapi.HostAPIVersion,
		Name:          "example",
		Version:       "1.0.0",
		Description:   "Example plugin",
		Executable:    "plugin",
		Commands:      []pluginapi.Command{{Name: "example"}},
	}
}
