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
