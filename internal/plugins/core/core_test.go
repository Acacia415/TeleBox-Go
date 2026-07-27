package core

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
)

func TestUpdatePrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current []string
		args    []string
		want    []string
		err     string
	}{
		{name: "set", current: []string{"."}, args: []string{"set", "-"}, want: []string{"-"}},
		{name: "set multiple", current: []string{"."}, args: []string{"set", "-", "."}, want: []string{"-", "."}},
		{name: "compatibility", current: []string{"."}, args: []string{"-"}, want: []string{"-"}},
		{name: "add", current: []string{"-"}, args: []string{"add", "."}, want: []string{"-", "."}},
		{name: "remove", current: []string{"-", "."}, args: []string{"remove", "."}, want: []string{"-"}},
		{name: "remove last", current: []string{"-"}, args: []string{"remove", "-"}, err: "至少保留"},
		{name: "duplicate", current: []string{"-"}, args: []string{"add", "-"}, err: "已存在"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := updatePrefixes(test.current, test.args)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("error = %v, want containing %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("prefixes = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPluginManagerAliases(t *testing.T) {
	t.Parallel()

	router, err := command.NewRouter([]string{"-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate := New(
		service.Container{},
		router,
		plugin.NewRegistry(router),
		nil,
	)
	for _, definition := range candidate.Commands() {
		if definition.Name != "tpm" {
			continue
		}
		want := []string{"p", "t", "plugins", "plugin"}
		if !reflect.DeepEqual(definition.Aliases, want) {
			t.Fatalf("aliases = %#v, want %#v", definition.Aliases, want)
		}
		return
	}
	t.Fatal("tpm command was not registered")
}

func TestSplitPluginReference(t *testing.T) {
	t.Parallel()
	name, version := splitPluginReference("BIN@v1.2.3")
	if name != "bin" || version != "v1.2.3" {
		t.Fatalf("split = %q, %q", name, version)
	}
}
