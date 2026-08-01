package pluginbuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/pluginspec"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

type Options struct {
	Repository string
	Plugin     string
	GOOS       string
	GOARCH     string
	OutputDir  string
	GoBinary   string
}

type Result struct {
	ManifestPath string
	BinaryPath   string
	Manifest     pluginapi.Manifest
}

type inspection struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Description  string              `json:"description"`
	Commands     []pluginapi.Command `json:"commands"`
	Listens      bool                `json:"listens"`
	ListensEdits bool                `json:"listens_edits"`
}

func Build(ctx context.Context, options Options) (Result, error) {
	specification, exists := pluginspec.Find(options.Plugin)
	if !exists {
		return Result{}, fmt.Errorf("unknown plugin %q", options.Plugin)
	}
	repository, err := resolveRepository(options.Repository)
	if err != nil {
		return Result{}, err
	}
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(options.GOARCH)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if strings.TrimSpace(options.OutputDir) == "" {
		return Result{}, errors.New("plugin output directory is required")
	}
	outputDir, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve output directory: %w", err)
	}
	goBinary := options.GoBinary
	if goBinary == "" {
		goBinary = defaultGoBinary()
	}

	buildRoot := filepath.Join(
		repository,
		".build",
		"plugin-sdk",
		safeFilename(specification.Name),
	)
	if err := os.MkdirAll(buildRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create plugin build directory: %w", err)
	}
	inspectSource, err := renderInspectionSource(specification)
	if err != nil {
		return Result{}, err
	}
	inspectDir := filepath.Join(buildRoot, "inspect")
	if err := os.MkdirAll(inspectDir, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(
		filepath.Join(inspectDir, "main.go"),
		inspectSource,
		0o600,
	); err != nil {
		return Result{}, fmt.Errorf("write plugin inspector: %w", err)
	}
	metadata, err := inspectPlugin(ctx, goBinary, repository, inspectDir)
	if err != nil {
		return Result{}, err
	}
	if metadata.Name != specification.Name {
		return Result{}, fmt.Errorf(
			"plugin specification %q provides %q",
			specification.Name,
			metadata.Name,
		)
	}
	permissions, err := AnalyzePermissions(
		filepath.Join(repository, filepath.FromSlash(specification.SourceDir)),
	)
	if err != nil {
		return Result{}, err
	}

	executableName := "telebox-plugin-" + safeFilename(specification.Name)
	if goos == "windows" {
		executableName += ".exe"
	}
	manifest := pluginapi.Manifest{
		SchemaVersion: pluginapi.ManifestSchemaVersion,
		APIVersion:    pluginapi.HostAPIVersion,
		Name:          metadata.Name,
		Version:       metadata.Version,
		Description:   metadata.Description,
		Executable:    executableName,
		Commands:      metadata.Commands,
		Listens:       metadata.Listens,
		ListensEdits:  metadata.ListensEdits,
		Permissions:   permissions,
		Homepage:      "https://github.com/Acacia415/TeleBox-Go",
	}
	if err := manifest.Validate(); err != nil {
		return Result{}, fmt.Errorf("generated manifest is invalid: %w", err)
	}

	mainSource, err := renderRuntimeSource(specification)
	if err != nil {
		return Result{}, err
	}
	runtimeDir := filepath.Join(buildRoot, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(
		filepath.Join(runtimeDir, "main.go"),
		mainSource,
		0o600,
	); err != nil {
		return Result{}, fmt.Errorf("write plugin entrypoint: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create plugin output directory: %w", err)
	}
	binaryPath := filepath.Join(outputDir, executableName)
	command := exec.CommandContext(
		ctx,
		goBinary,
		"build",
		"-trimpath",
		"-ldflags=-s -w",
		"-o",
		binaryPath,
		runtimeDir,
	)
	command.Dir = repository
	command.Env = buildEnvironment(goos, goarch)
	output, err := command.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf(
			"build plugin %q for %s/%s: %w\n%s",
			specification.Name,
			goos,
			goarch,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	manifestPath := filepath.Join(outputDir, "plugin.json")
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return Result{}, fmt.Errorf("write plugin manifest: %w", err)
	}
	return Result{
		ManifestPath: manifestPath,
		BinaryPath:   binaryPath,
		Manifest:     manifest,
	}, nil
}

func AnalyzePermissions(sourceDir string) (pluginapi.Permissions, error) {
	telegramMethods := make(map[string]struct{})
	var permissions pluginapi.Permissions
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(sourceDir, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(source, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if parent, ok := selector.X.(*ast.SelectorExpr); ok &&
				parent.Sel.Name == "Telegram" &&
				containsServices(parent.X) {
				telegramMethods[camelToSnake(selector.Sel.Name)] = struct{}{}
			}
			if identifier, ok := selector.X.(*ast.Ident); ok &&
				identifier.Name == "telegram" {
				switch selector.Sel.Name {
				case "SendHTML", "ReplyHTML", "EditHTML":
					telegramMethods[camelToSnake(selector.Sel.Name)] = struct{}{}
				}
			}
			if !containsServices(selector.X) {
				return true
			}
			switch selector.Sel.Name {
			case "Storage":
				permissions.Storage = true
			case "HTTP":
				permissions.Network = true
			case "Tools":
				permissions.Tools = []string{"*"}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return pluginapi.Permissions{}, fmt.Errorf(
			"analyze plugin permissions: %w",
			err,
		)
	}
	permissions.Telegram = make([]string, 0, len(telegramMethods))
	for method := range telegramMethods {
		permissions.Telegram = append(permissions.Telegram, method)
	}
	sort.Strings(permissions.Telegram)
	return permissions, nil
}

func inspectPlugin(
	ctx context.Context,
	goBinary string,
	repository string,
	sourceDir string,
) (inspection, error) {
	command := exec.CommandContext(ctx, goBinary, "run", sourceDir)
	command.Dir = repository
	command.Env = buildEnvironment(runtime.GOOS, runtime.GOARCH)
	output, err := command.CombinedOutput()
	if err != nil {
		return inspection{}, fmt.Errorf(
			"inspect plugin metadata: %w\n%s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	var result inspection
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return inspection{}, fmt.Errorf("decode plugin metadata: %w", err)
	}
	return result, nil
}

func renderInspectionSource(specification pluginspec.Spec) ([]byte, error) {
	source := fmt.Sprintf(`package main

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
	selected %q
)

func main() {
	candidate := selected.%s(service.Container{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	metadata := candidate.Metadata()
	definitions := candidate.Commands()
	commands := make([]pluginapi.Command, 0, len(definitions))
	for _, definition := range definitions {
		commands = append(commands, pluginapi.Command{
			Name:        definition.Name,
			Aliases:     definition.Aliases,
			Description: definition.Description,
			Usage:       definition.Usage,
			HelpHTML:    definition.HelpHTML,
			OwnerOnly:   definition.OwnerOnly,
		})
	}
	listens := false
	if listener, ok := any(candidate).(plugin.MessageListener); ok {
		listens = listener != nil
		if conditional, ok := any(candidate).(plugin.ConditionalMessageListener); ok {
			listens = conditional.ListensToMessages()
		}
	}
	listensEdits := false
	if listener, ok := any(candidate).(plugin.EditedMessageListener); ok {
		listensEdits = listener != nil
		if conditional, ok := any(candidate).(plugin.ConditionalEditedMessageListener); ok {
			listensEdits = conditional.ListensToEditedMessages()
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Name string `+"`json:\"name\"`"+`
		Version string `+"`json:\"version\"`"+`
		Description string `+"`json:\"description\"`"+`
		Commands []pluginapi.Command `+"`json:\"commands\"`"+`
		Listens bool `+"`json:\"listens\"`"+`
		ListensEdits bool `+"`json:\"listens_edits\"`"+`
	}{
		Name: metadata.Name,
		Version: metadata.Version,
		Description: metadata.Description,
		Commands: commands,
		Listens: listens,
		ListensEdits: listensEdits,
	})
}
`, specification.Package, specification.Constructor)
	return format.Source([]byte(source))
}

func renderRuntimeSource(specification pluginspec.Spec) ([]byte, error) {
	source := fmt.Sprintf(`package main

import (
	"fmt"
	"os"

	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginruntime"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	selected %q
)

func main() {
	err := pluginruntime.Run(func(services service.Container) plugin.Plugin {
		return selected.%s(services)
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`, specification.Package, specification.Constructor)
	return format.Source([]byte(source))
}

func resolveRepository(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		root, err := filepath.Abs(value)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			return "", fmt.Errorf("repository does not contain go.mod: %w", err)
		}
		return root, nil
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("could not locate repository root")
		}
		current = parent
	}
}

func defaultGoBinary() string {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(runtime.GOROOT(), "bin", name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return name
}

func buildEnvironment(goos, goarch string) []string {
	result := append([]string(nil), os.Environ()...)
	result = append(result,
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED=0",
	)
	return result
}

func safeFilename(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func containsServices(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name == "services"
	case *ast.SelectorExpr:
		return value.Sel.Name == "services" || containsServices(value.X)
	case *ast.IndexExpr:
		return containsServices(value.X)
	case *ast.ParenExpr:
		return containsServices(value.X)
	default:
		return false
	}
}

func camelToSnake(value string) string {
	characters := []rune(value)
	var result strings.Builder
	for index, character := range characters {
		if unicode.IsUpper(character) {
			previousIsLower := index > 0 &&
				(unicode.IsLower(characters[index-1]) ||
					unicode.IsDigit(characters[index-1]))
			nextIsLower := index+1 < len(characters) &&
				unicode.IsLower(characters[index+1])
			if index > 0 && (previousIsLower || nextIsLower) {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(character))
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}
