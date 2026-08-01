package pluginrelease

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

func TestParsePlatforms(t *testing.T) {
	t.Parallel()
	got, err := ParsePlatforms("linux/amd64, linux/arm64,linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	want := []Platform{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("platforms = %#v, want %#v", got, want)
	}
}

func TestParsePluginNames(t *testing.T) {
	t.Parallel()
	got := ParsePluginNames(" speedlink,yt-dlp,SPEEDLINK, ")
	want := []string{"speedlink", "yt-dlp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin names = %#v, want %#v", got, want)
	}
}

func TestSelectedSpecificationsRejectsUnknownPlugin(t *testing.T) {
	t.Parallel()
	if _, err := selectedSpecifications([]string{"missing"}); err == nil {
		t.Fatal("selectedSpecifications() error = nil")
	}
}

func TestMergeCatalogPluginReplacesVersionAndKeepsHistory(t *testing.T) {
	t.Parallel()
	catalog := pluginapi.Catalog{
		SchemaVersion: pluginapi.CatalogSchemaVersion,
		Plugins: []pluginapi.CatalogPlugin{{
			Name:        "speedlink",
			Description: "old",
			Releases: []pluginapi.PluginRelease{
				{Version: "0.4.1"},
				{Version: "0.4.0"},
			},
		}},
	}
	updated := pluginapi.CatalogPlugin{
		Name:        "speedlink",
		Description: "new",
		Releases: []pluginapi.PluginRelease{{
			Version: "0.4.2",
		}},
	}
	mergeCatalogPlugin(&catalog, updated, 2)
	if got := catalog.Plugins[0].Description; got != "new" {
		t.Fatalf("description = %q, want new", got)
	}
	got := catalog.Plugins[0].Releases
	want := []pluginapi.PluginRelease{
		{Version: "0.4.2"},
		{Version: "0.4.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("releases = %#v, want %#v", got, want)
	}
}

func TestMergeCatalogPluginReplacesSameVersion(t *testing.T) {
	t.Parallel()
	catalog := pluginapi.Catalog{
		SchemaVersion: pluginapi.CatalogSchemaVersion,
		Plugins: []pluginapi.CatalogPlugin{{
			Name:        "speedlink",
			Description: "old",
			Releases: []pluginapi.PluginRelease{{
				Version: "v0.4.1",
				MinHost: "0.7.1",
			}},
		}},
	}
	updated := pluginapi.CatalogPlugin{
		Name:        "speedlink",
		Description: "new",
		Releases: []pluginapi.PluginRelease{{
			Version: "0.4.1",
			MinHost: "0.7.2",
		}},
	}
	mergeCatalogPlugin(&catalog, updated, 3)
	if got := catalog.Plugins[0].Releases; len(got) != 1 ||
		got[0].MinHost != "0.7.2" {
		t.Fatalf("releases = %#v", got)
	}
}

func TestLoadBaseCatalog(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "catalog.json")
	data := `{"schema_version":1,"plugins":[]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := loadBaseCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.SchemaVersion != pluginapi.CatalogSchemaVersion ||
		len(catalog.Plugins) != 0 {
		t.Fatalf("catalog = %#v", catalog)
	}
}

func TestParsePlatformsRejectsInvalidValue(t *testing.T) {
	t.Parallel()
	if _, err := ParsePlatforms("linux"); err == nil {
		t.Fatal("ParsePlatforms() error = nil")
	}
}
