package pluginrelease

import (
	"reflect"
	"testing"
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

func TestParsePlatformsRejectsInvalidValue(t *testing.T) {
	t.Parallel()
	if _, err := ParsePlatforms("linux"); err == nil {
		t.Fatal("ParsePlatforms() error = nil")
	}
}
