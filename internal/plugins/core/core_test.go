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

func TestFrameworkUpdateCommand(t *testing.T) {
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
		nil,
	)
	for _, definition := range candidate.Commands() {
		if definition.Name != "update" {
			continue
		}
		if !definition.OwnerOnly {
			t.Fatal("update command must be owner-only")
		}
		if !reflect.DeepEqual(definition.Aliases, []string{"upgrade"}) {
			t.Fatalf("aliases = %#v", definition.Aliases)
		}
		return
	}
	t.Fatal("update command was not registered")
}

func TestSplitPluginReference(t *testing.T) {
	t.Parallel()
	name, version := splitPluginReference("BIN@v1.2.3")
	if name != "bin" || version != "v1.2.3" {
		t.Fatalf("split = %q, %q", name, version)
	}
}

func TestFormatCommandListIsCompact(t *testing.T) {
	t.Parallel()

	routes := visibleHelpRoutes([]command.RouteInfo{
		{Name: "status", Description: "显示运行状态"},
		{Name: "ping", Description: "检查连接"},
		{Name: "prefix", Description: "修改前缀", OwnerOnly: true},
	}, false)
	got := formatCommandList("-", routes)

	if strings.Contains(got, "显示运行状态") || strings.Contains(got, "修改前缀") {
		t.Fatalf("compact help contains descriptions: %q", got)
	}
	if strings.Contains(got, "prefix") {
		t.Fatalf("non-owner help contains owner command: %q", got)
	}
	if !strings.Contains(got, "<code>ping</code>, <code>status</code>") {
		t.Fatalf("commands are missing or not sorted: %q", got)
	}
	if !strings.Contains(got, "-help &lt;命令&gt;") {
		t.Fatalf("command help hint is missing: %q", got)
	}
}

func TestFormatCommandHelpFindsAliases(t *testing.T) {
	t.Parallel()

	routes := visibleHelpRoutes([]command.RouteInfo{
		{
			Name:        "tpm",
			Aliases:     []string{"p", "t"},
			Description: "安装、更新和管理插件",
			OwnerOnly:   true,
		},
	}, true)
	route, ok := findHelpRoute(routes, "P")
	if !ok {
		t.Fatal("alias was not found")
	}
	got := formatCommandHelp("-", route)
	for _, want := range []string{
		"-tpm",
		"安装、更新和管理插件",
		"-p",
		"-t",
		"仅所有者",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command help %q does not contain %q", got, want)
		}
	}
}
