package migration

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	legacyAssetsPrefix = "telebox/assets/"
	maxAssetFileSize   = 512 << 20
	maxAssetTotalSize  = 4 << 30
)

type AssetExtraction struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

type assetManifest struct {
	Version       int      `json:"version"`
	SourceSHA256  string   `json:"source_sha256"`
	Plugins       []string `json:"plugins"`
	ExtractedFile int      `json:"extracted_files"`
	ExtractedByte int64    `json:"extracted_bytes"`
	CreatedAt     string   `json:"created_at"`
}

func inspectLegacyAssets(archivePath string, plugins []string) (AssetExtraction, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("open backup assets: %w", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("open backup asset gzip stream: %w", err)
	}
	defer compressed.Close()

	selectors := assetSelectors(plugins)
	reader := tar.NewReader(compressed)
	var result AssetExtraction
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return AssetExtraction{}, fmt.Errorf("read backup assets: %w", err)
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return AssetExtraction{}, err
		}
		if !strings.HasPrefix(name, legacyAssetsPrefix) {
			continue
		}
		relative := strings.TrimPrefix(name, legacyAssetsPrefix)
		if relative == "" || !matchesAssetSelector(relative, selectors) || header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return AssetExtraction{}, fmt.Errorf("unsupported archive asset type for %q", name)
		}
		if header.Size < 0 || header.Size > maxAssetFileSize ||
			result.Bytes+header.Size > maxAssetTotalSize {
			return AssetExtraction{}, fmt.Errorf("archive asset %q exceeds migration size limit", name)
		}
		result.Files++
		result.Bytes += header.Size
	}
}

// ExtractLegacyAssets copies only assets belonging to the plugins present in
// the full backup. It rejects links, traversal, duplicate files and oversized
// entries, and installs the result with one directory rename.
func ExtractLegacyAssets(
	archivePath string,
	destination string,
	plugins []string,
	sourceSHA256 string,
) (AssetExtraction, error) {
	if destination == "" {
		return AssetExtraction{}, errors.New("asset destination is required")
	}
	if _, err := os.Lstat(destination); err == nil {
		return AssetExtraction{}, fmt.Errorf("refusing to overwrite existing asset path %q", destination)
	} else if !os.IsNotExist(err) {
		return AssetExtraction{}, fmt.Errorf("inspect asset output path: %w", err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return AssetExtraction{}, fmt.Errorf("create asset parent directory: %w", err)
	}
	temp, err := os.MkdirTemp(parent, ".telebox-assets-*.tmp")
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("create asset temp directory: %w", err)
	}
	defer os.RemoveAll(temp)

	selectors := assetSelectors(plugins)
	extraction, err := extractSelectedAssets(archivePath, temp, selectors)
	if err != nil {
		return AssetExtraction{}, err
	}
	manifest := assetManifest{
		Version:       1,
		SourceSHA256:  sourceSHA256,
		Plugins:       append([]string(nil), plugins...),
		ExtractedFile: extraction.Files,
		ExtractedByte: extraction.Bytes,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	sort.Strings(manifest.Plugins)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("encode asset migration manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(temp, "_migration.json"), encoded, 0o600); err != nil {
		return AssetExtraction{}, fmt.Errorf("write asset migration manifest: %w", err)
	}
	if err := os.Rename(temp, destination); err != nil {
		return AssetExtraction{}, fmt.Errorf("install migrated assets: %w", err)
	}
	return extraction, nil
}

func extractSelectedAssets(
	archivePath string,
	destination string,
	selectors []string,
) (AssetExtraction, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("open backup assets: %w", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("open backup asset gzip stream: %w", err)
	}
	defer compressed.Close()

	var result AssetExtraction
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return AssetExtraction{}, fmt.Errorf("read backup assets: %w", err)
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return AssetExtraction{}, err
		}
		if !strings.HasPrefix(name, legacyAssetsPrefix) {
			continue
		}
		relative := strings.TrimPrefix(name, legacyAssetsPrefix)
		if relative == "" || !matchesAssetSelector(relative, selectors) {
			continue
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return AssetExtraction{}, fmt.Errorf("unsupported archive asset type for %q", name)
		}
		if header.Size < 0 || header.Size > maxAssetFileSize ||
			result.Bytes+header.Size > maxAssetTotalSize {
			return AssetExtraction{}, fmt.Errorf("archive asset %q exceeds migration size limit", name)
		}

		target, err := safeDestinationPath(destination, relative)
		if err != nil {
			return AssetExtraction{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return AssetExtraction{}, fmt.Errorf("create asset directory: %w", err)
		}
		mode := os.FileMode(0o600)
		if header.Mode&0o111 != 0 {
			mode = 0o700
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return AssetExtraction{}, fmt.Errorf("create migrated asset %q: %w", relative, err)
		}
		written, copyErr := io.CopyN(file, reader, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return AssetExtraction{}, fmt.Errorf("extract asset %q: %w", relative, copyErr)
		}
		if closeErr != nil {
			return AssetExtraction{}, fmt.Errorf("close migrated asset %q: %w", relative, closeErr)
		}
		result.Files++
		result.Bytes += written
	}
	return result, nil
}

func safeDestinationPath(root, relative string) (string, error) {
	relative = strings.ReplaceAll(relative, "\\", "/")
	cleaned := path.Clean(relative)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("unsafe asset path %q", relative)
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("asset path escapes destination %q", relative)
	}
	return resolvedTarget, nil
}

func assetSelectors(plugins []string) []string {
	selected := map[string]struct{}{
		// Alias is an original core feature rather than a business plugin, so
		// it does not appear in the installed plugin list in config.json.
		"alias/": {},
		"sudo/":  {},
		"sure/":  {},
	}
	for _, plugin := range plugins {
		plugin = strings.ToLower(strings.TrimSpace(plugin))
		if plugin == "" {
			continue
		}
		selected[plugin+"/"] = struct{}{}
		for _, selector := range pluginAssetAliases[plugin] {
			selected[selector] = struct{}{}
		}
	}
	return sortedKeys(selected)
}

var pluginAssetAliases = map[string][]string{
	"ai":              {"ai_config.db"},
	"bulk_delete":     {"bd/"},
	"cezi":            {"cezi_config.db"},
	"gif":             {"gif_converter/"},
	"telegram-backup": {"telegram-backup/"},
	"yt-dlp":          {"ytdlp/", "ytdlp_gemini_config.db"},
}

func matchesAssetSelector(relative string, selectors []string) bool {
	for _, selector := range selectors {
		if strings.HasSuffix(selector, "/") {
			if strings.HasPrefix(relative, selector) {
				return true
			}
		} else if relative == selector {
			return true
		}
	}
	return false
}
