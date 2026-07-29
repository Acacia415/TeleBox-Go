package pluginmanager

import (
	"reflect"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmarket"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

func TestActivationAfterInstall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		forceEnable   bool
		wasRegistered bool
		wasEnabled    bool
		want          bool
	}{
		{name: "new install", forceEnable: true, want: true},
		{
			name:          "update running plugin",
			wasRegistered: true,
			wasEnabled:    true,
			want:          true,
		},
		{
			name:          "update stopped plugin",
			wasRegistered: true,
			wasEnabled:    false,
			want:          false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := activationAfterInstall(
				test.forceEnable,
				plugin.Status{Enabled: test.wasEnabled},
				test.wasRegistered,
			)
			if got != test.want {
				t.Fatalf("activation = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCatalogInstalledPluginsSkipsPackagesRemovedFromCatalog(t *testing.T) {
	t.Parallel()
	installed := []pluginmarket.Installed{
		{Manifest: pluginapi.Manifest{Name: "music_bot"}},
		{Manifest: pluginapi.Manifest{Name: "speedtest"}},
		{Manifest: pluginapi.Manifest{Name: "yt-dlp"}},
	}
	catalog := pluginapi.Catalog{Plugins: []pluginapi.CatalogPlugin{
		{Name: "yt-dlp"},
	}}

	updatable, skipped := catalogInstalledPlugins(installed, catalog)
	var names []string
	for _, item := range updatable {
		names = append(names, item.Manifest.Name)
	}
	if !reflect.DeepEqual(names, []string{"yt-dlp"}) {
		t.Fatalf("updatable = %v", names)
	}
	if !reflect.DeepEqual(skipped, []string{"music_bot", "speedtest"}) {
		t.Fatalf("skipped = %v", skipped)
	}
}
