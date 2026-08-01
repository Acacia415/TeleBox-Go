package pluginapi

import (
	"strings"
	"testing"
)

func TestManifestValidation(t *testing.T) {
	t.Parallel()

	valid := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		APIVersion:    HostAPIVersion,
		Name:          "example",
		Version:       "v1.2.3",
		Description:   "Example plugin",
		Executable:    "telebox-plugin-example",
		Commands: []Command{{
			Name:    "example",
			Aliases: []string{"ex"},
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	invalid := valid
	invalid.Executable = `..\outside`
	if err := invalid.Validate(); err == nil ||
		!strings.Contains(err.Error(), "escapes") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCatalogSelectsPlatformArtifact(t *testing.T) {
	t.Parallel()

	release := PluginRelease{
		Version: "v1.0.0",
		Artifacts: []Artifact{
			{OS: "linux", Arch: "amd64", Format: "tar.gz"},
			{OS: "linux", Arch: "arm64", Format: "tar.gz"},
		},
	}
	artifact, ok := release.ArtifactFor("linux", "arm64")
	if !ok || artifact.Arch != "arm64" {
		t.Fatalf("ArtifactFor() = %+v, %t", artifact, ok)
	}
}

func TestCatalogRejectsInvalidMinimumHost(t *testing.T) {
	t.Parallel()

	catalog := Catalog{
		SchemaVersion: CatalogSchemaVersion,
		Plugins: []CatalogPlugin{{
			Name:        "example",
			Description: "Example",
			Releases: []PluginRelease{{
				Version: "1.0.0",
				MinHost: "not-a-version",
				Artifacts: []Artifact{{
					OS:     "linux",
					Arch:   "amd64",
					URL:    "https://example.com/plugin.zip",
					SHA256: strings.Repeat("a", 64),
					Format: "zip",
				}},
			}},
		}},
	}
	if err := catalog.Validate(); err == nil ||
		!strings.Contains(err.Error(), "minimum host") {
		t.Fatalf("Catalog.Validate() error = %v", err)
	}
}
