package pluginrelease

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/pluginbuilder"
	"github.com/Acacia415/TeleBox-Go/internal/pluginspec"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

type Platform struct {
	OS   string
	Arch string
}

type Options struct {
	Repository    string
	OutputDir     string
	Tag           string
	RepositoryURL string
	Platforms     []Platform
	GoBinary      string
	MinimumHost   string
}

type Result struct {
	CatalogPath string
	Artifacts   []string
}

func Generate(ctx context.Context, options Options) (Result, error) {
	if strings.TrimSpace(options.OutputDir) == "" {
		return Result{}, errors.New("release output directory is required")
	}
	if strings.TrimSpace(options.Tag) == "" {
		return Result{}, errors.New("release tag is required")
	}
	if len(options.Platforms) == 0 {
		return Result{}, errors.New("at least one plugin platform is required")
	}
	repositoryURL := strings.TrimSuffix(
		strings.TrimSpace(options.RepositoryURL),
		"/",
	)
	if repositoryURL == "" {
		repositoryURL = "https://github.com/Acacia415/TeleBox-Go"
	}
	outputDir, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return Result{}, err
	}
	staging := filepath.Join(outputDir, ".staging")
	if err := os.RemoveAll(staging); err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)

	catalog := pluginapi.Catalog{
		SchemaVersion: pluginapi.CatalogSchemaVersion,
	}
	var artifactPaths []string
	for _, specification := range pluginspec.All() {
		catalogItem := pluginapi.CatalogPlugin{
			Name:     specification.Name,
			Homepage: repositoryURL,
		}
		release := pluginapi.PluginRelease{
			MinHost: strings.TrimSpace(options.MinimumHost),
		}
		for _, platform := range options.Platforms {
			buildDir := filepath.Join(
				staging,
				specification.Name,
				platform.OS+"-"+platform.Arch,
			)
			built, err := pluginbuilder.Build(ctx, pluginbuilder.Options{
				Repository: options.Repository,
				Plugin:     specification.Name,
				GOOS:       platform.OS,
				GOARCH:     platform.Arch,
				OutputDir:  buildDir,
				GoBinary:   options.GoBinary,
			})
			if err != nil {
				return Result{}, err
			}
			if release.Version == "" {
				release.Version = built.Manifest.Version
				catalogItem.Description = built.Manifest.Description
			} else if release.Version != built.Manifest.Version {
				return Result{}, fmt.Errorf(
					"plugin %q version changed between platform builds",
					specification.Name,
				)
			}
			archiveName := fmt.Sprintf(
				"telebox-plugin-%s_%s_%s_%s.zip",
				releaseFilename(specification.Name),
				strings.TrimPrefix(built.Manifest.Version, "v"),
				platform.OS,
				platform.Arch,
			)
			archivePath := filepath.Join(outputDir, archiveName)
			if err := createArchive(
				archivePath,
				built.ManifestPath,
				built.BinaryPath,
			); err != nil {
				return Result{}, err
			}
			checksum, size, err := fileDigest(archivePath)
			if err != nil {
				return Result{}, err
			}
			release.Artifacts = append(release.Artifacts, pluginapi.Artifact{
				OS:   platform.OS,
				Arch: platform.Arch,
				URL: repositoryURL + "/releases/download/" +
					options.Tag + "/" + archiveName,
				SHA256: checksum,
				Size:   size,
				Format: "zip",
			})
			artifactPaths = append(artifactPaths, archivePath)
		}
		sort.Slice(release.Artifacts, func(i, j int) bool {
			if release.Artifacts[i].OS == release.Artifacts[j].OS {
				return release.Artifacts[i].Arch < release.Artifacts[j].Arch
			}
			return release.Artifacts[i].OS < release.Artifacts[j].OS
		})
		catalogItem.Releases = []pluginapi.PluginRelease{release}
		catalog.Plugins = append(catalog.Plugins, catalogItem)
	}
	if err := catalog.Validate(); err != nil {
		return Result{}, fmt.Errorf("generated catalog is invalid: %w", err)
	}
	encoded, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return Result{}, err
	}
	encoded = append(encoded, '\n')
	catalogPath := filepath.Join(outputDir, "catalog.json")
	if err := os.WriteFile(catalogPath, encoded, 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		CatalogPath: catalogPath,
		Artifacts:   artifactPaths,
	}, nil
}

func ParsePlatforms(value string) ([]Platform, error) {
	var result []Platform
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		goos, goarch, found := strings.Cut(item, "/")
		goos = strings.TrimSpace(goos)
		goarch = strings.TrimSpace(goarch)
		if !found || goos == "" || goarch == "" {
			return nil, fmt.Errorf("invalid platform %q; expected os/arch", item)
		}
		key := goos + "/" + goarch
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, Platform{OS: goos, Arch: goarch})
	}
	if len(result) == 0 {
		return nil, errors.New("no plugin platforms were provided")
	}
	return result, nil
}

func createArchive(target, manifestPath, binaryPath string) (returnErr error) {
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, output.Close())
	}()
	writer := zip.NewWriter(output)
	defer func() {
		returnErr = errors.Join(returnErr, writer.Close())
	}()
	if err := addArchiveFile(writer, manifestPath, "plugin.json", 0o644); err != nil {
		return err
	}
	return addArchiveFile(
		writer,
		binaryPath,
		filepath.Base(binaryPath),
		0o755,
	)
}

func addArchiveFile(
	writer *zip.Writer,
	source string,
	name string,
	mode os.FileMode,
) error {
	header := &zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	}
	header.SetMode(mode)
	header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	_, err = io.Copy(entry, input)
	return err
}

func fileDigest(path string) (string, int64, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer input.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, input)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func releaseFilename(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.ReplaceAll(value, "_", "-")
}
