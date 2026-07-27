package pluginexternal

import (
	"strings"
	"testing"
)

func TestPluginEnvironmentFiltersHostSecrets(t *testing.T) {
	t.Setenv("TELEBOX_API_HASH", "secret")
	t.Setenv("TELEBOX_API_ID", "123")

	environment := pluginEnvironment(
		"example",
		"/tmp/telebox/example",
		"/tmp/telebox/assets",
		"/tmp/telebox/legacy-assets",
	)
	values := make(map[string]string)
	for _, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[strings.ToUpper(key)] = value
		}
	}
	if _, exists := values["TELEBOX_API_HASH"]; exists {
		t.Fatal("TELEBOX_API_HASH leaked into plugin process")
	}
	if _, exists := values["TELEBOX_API_ID"]; exists {
		t.Fatal("TELEBOX_API_ID leaked into plugin process")
	}
	if values["TELEBOX_PLUGIN_NAME"] != "example" {
		t.Fatalf("plugin name = %q", values["TELEBOX_PLUGIN_NAME"])
	}
	if values["TELEBOX_PLUGIN_ASSETS_DIR"] != "/tmp/telebox/assets" {
		t.Fatalf("assets directory = %q", values["TELEBOX_PLUGIN_ASSETS_DIR"])
	}
	if values["TELEBOX_PLUGIN_LEGACY_ASSETS_DIR"] != "/tmp/telebox/legacy-assets" {
		t.Fatalf("legacy assets directory = %q", values["TELEBOX_PLUGIN_LEGACY_ASSETS_DIR"])
	}
}

func TestChannelClosed(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	if channelClosed(done) {
		t.Fatal("open channel reported as closed")
	}
	close(done)
	if !channelClosed(done) {
		t.Fatal("closed channel reported as open")
	}
}
