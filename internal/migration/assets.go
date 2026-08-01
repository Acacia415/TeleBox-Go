package migration

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
	legacyAssetsPrefix      = "telebox/assets/"
	legacyAssetManifestName = "_legacy_manifest.json"
	maxAssetFileSize        = 512 << 20
	maxAssetTotalSize       = 4 << 30
	maxAssetFileCount       = 100000
)

type AssetExtraction struct {
	Files            int   `json:"files"`
	Bytes            int64 `json:"bytes"`
	QuarantinedFiles int   `json:"quarantined_files,omitempty"`
	QuarantinedBytes int64 `json:"quarantined_bytes,omitempty"`
}

type assetManifest struct {
	Version         int      `json:"version"`
	SourceSHA256    string   `json:"source_sha256"`
	Plugins         []string `json:"plugins"`
	ExtractedFile   int      `json:"extracted_files"`
	ExtractedByte   int64    `json:"extracted_bytes"`
	QuarantinedFile int      `json:"quarantined_files,omitempty"`
	QuarantinedByte int64    `json:"quarantined_bytes,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

type preservedAssetFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type preservedAssetManifest struct {
	Format        string               `json:"format"`
	Version       int                  `json:"version"`
	SourceSHA256  string               `json:"source_sha256"`
	Files         []preservedAssetFile `json:"files"`
	ExtractedFile int                  `json:"extracted_files"`
	ExtractedByte int64                `json:"extracted_bytes"`
	CreatedAt     string               `json:"created_at"`
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
	seen := make(map[string]struct{})
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
		if _, duplicate := seen[relative]; duplicate {
			return AssetExtraction{}, fmt.Errorf("duplicate archive asset %q", name)
		}
		seen[relative] = struct{}{}
		if header.Size < 0 || header.Size > maxAssetFileSize ||
			result.Bytes+header.Size > maxAssetTotalSize {
			return AssetExtraction{}, fmt.Errorf("archive asset %q exceeds migration size limit", name)
		}
		prefix, err := readAssetPrefix(reader, header.Size)
		if err != nil {
			return AssetExtraction{}, fmt.Errorf("inspect asset %q: %w", name, err)
		}
		if unsafeActiveAsset(relative, header.Mode, prefix) {
			result.QuarantinedFiles++
			result.QuarantinedBytes += header.Size
			continue
		}
		result.Files++
		if result.Files > maxAssetFileCount {
			return AssetExtraction{}, errors.New("backup contains too many legacy asset files")
		}
		result.Bytes += header.Size
	}
}

func inspectAllLegacyAssets(archivePath string) (AssetExtraction, error) {
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

	reader := tar.NewReader(compressed)
	var result AssetExtraction
	seen := make(map[string]struct{})
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
		if relative == "" || header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return AssetExtraction{}, fmt.Errorf("unsupported archive asset type for %q", name)
		}
		if _, duplicate := seen[relative]; duplicate {
			return AssetExtraction{}, fmt.Errorf("duplicate archive asset %q", name)
		}
		seen[relative] = struct{}{}
		if header.Size < 0 || header.Size > maxAssetFileSize ||
			result.Bytes+header.Size > maxAssetTotalSize {
			return AssetExtraction{}, fmt.Errorf("archive asset %q exceeds migration size limit", name)
		}
		result.Files++
		if result.Files > maxAssetFileCount {
			return AssetExtraction{}, errors.New("backup contains too many legacy asset files")
		}
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
		Version:         1,
		SourceSHA256:    sourceSHA256,
		Plugins:         append([]string(nil), plugins...),
		ExtractedFile:   extraction.Files,
		ExtractedByte:   extraction.Bytes,
		QuarantinedFile: extraction.QuarantinedFiles,
		QuarantinedByte: extraction.QuarantinedBytes,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
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

// PreserveLegacyAssets copies every file under telebox/assets into an inert,
// private directory. Files are never executable and a per-file SHA-256
// manifest allows future Go plugins to import their original data safely.
func PreserveLegacyAssets(
	archivePath string,
	destination string,
	sourceSHA256 string,
) (AssetExtraction, error) {
	if destination == "" {
		return AssetExtraction{}, errors.New("legacy asset destination is required")
	}
	if _, err := os.Lstat(destination); err == nil {
		return AssetExtraction{}, fmt.Errorf("refusing to overwrite existing legacy asset path %q", destination)
	} else if !os.IsNotExist(err) {
		return AssetExtraction{}, fmt.Errorf("inspect legacy asset output path: %w", err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return AssetExtraction{}, fmt.Errorf("create legacy asset parent directory: %w", err)
	}
	temp, err := os.MkdirTemp(parent, ".telebox-legacy-assets-*.tmp")
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("create legacy asset temp directory: %w", err)
	}
	defer os.RemoveAll(temp)

	extraction, files, err := extractAllLegacyAssets(archivePath, temp)
	if err != nil {
		return AssetExtraction{}, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	manifest := preservedAssetManifest{
		Format:        "telebox-go-legacy-assets",
		Version:       1,
		SourceSHA256:  sourceSHA256,
		Files:         files,
		ExtractedFile: extraction.Files,
		ExtractedByte: extraction.Bytes,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("encode legacy asset manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	manifestPath, err := availableLegacyManifestPath(temp)
	if err != nil {
		return AssetExtraction{}, err
	}
	manifestFile, err := os.OpenFile(
		manifestPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return AssetExtraction{}, fmt.Errorf("create legacy asset manifest: %w", err)
	}
	_, writeErr := manifestFile.Write(encoded)
	closeErr := manifestFile.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return AssetExtraction{}, fmt.Errorf("write legacy asset manifest: %w", err)
	}
	if err := os.Rename(temp, destination); err != nil {
		return AssetExtraction{}, fmt.Errorf("install preserved legacy assets: %w", err)
	}
	return extraction, nil
}

func availableLegacyManifestPath(root string) (string, error) {
	for index := 0; index <= maxAssetFileCount; index++ {
		name := legacyAssetManifestName
		if index > 0 {
			name = fmt.Sprintf("_legacy_manifest.%d.json", index)
		}
		candidate := filepath.Join(root, name)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect legacy asset manifest path: %w", err)
		}
	}
	return "", errors.New("no available legacy asset manifest filename")
}

func extractAllLegacyAssets(
	archivePath string,
	destination string,
) (AssetExtraction, []preservedAssetFile, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return AssetExtraction{}, nil, fmt.Errorf("open backup assets: %w", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return AssetExtraction{}, nil, fmt.Errorf("open backup asset gzip stream: %w", err)
	}
	defer compressed.Close()

	var result AssetExtraction
	var files []preservedAssetFile
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return result, files, nil
		}
		if err != nil {
			return AssetExtraction{}, nil, fmt.Errorf("read backup assets: %w", err)
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return AssetExtraction{}, nil, err
		}
		if !strings.HasPrefix(name, legacyAssetsPrefix) {
			continue
		}
		relative := strings.TrimPrefix(name, legacyAssetsPrefix)
		if relative == "" || header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return AssetExtraction{}, nil, fmt.Errorf("unsupported archive asset type for %q", name)
		}
		if header.Size < 0 || header.Size > maxAssetFileSize ||
			result.Bytes+header.Size > maxAssetTotalSize {
			return AssetExtraction{}, nil, fmt.Errorf("archive asset %q exceeds migration size limit", name)
		}
		target, err := safeDestinationPath(destination, relative)
		if err != nil {
			return AssetExtraction{}, nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return AssetExtraction{}, nil, fmt.Errorf("create legacy asset directory: %w", err)
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return AssetExtraction{}, nil, fmt.Errorf("create preserved legacy asset %q: %w", relative, err)
		}
		hash := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(file, hash), reader, header.Size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return AssetExtraction{}, nil, fmt.Errorf(
				"preserve legacy asset %q: %w",
				relative,
				errors.Join(copyErr, closeErr),
			)
		}
		result.Files++
		if result.Files > maxAssetFileCount {
			return AssetExtraction{}, nil, errors.New("backup contains too many legacy asset files")
		}
		result.Bytes += written
		files = append(files, preservedAssetFile{
			Path:   filepath.ToSlash(relative),
			Size:   written,
			SHA256: hex.EncodeToString(hash.Sum(nil)),
		})
	}
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
		prefix, err := readAssetPrefix(reader, header.Size)
		if err != nil {
			return AssetExtraction{}, fmt.Errorf("inspect asset %q: %w", relative, err)
		}
		if unsafeActiveAsset(relative, header.Mode, prefix) {
			result.QuarantinedFiles++
			result.QuarantinedBytes += header.Size
			continue
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return AssetExtraction{}, fmt.Errorf("create migrated asset %q: %w", relative, err)
		}
		prefixWritten, prefixErr := file.Write(prefix)
		remaining := header.Size - int64(len(prefix))
		written, copyErr := io.CopyN(file, reader, remaining)
		written += int64(prefixWritten)
		closeErr := file.Close()
		if prefixErr != nil || prefixWritten != len(prefix) || copyErr != nil {
			writeErr := errors.Join(prefixErr, copyErr)
			if prefixWritten != len(prefix) {
				writeErr = errors.Join(writeErr, io.ErrShortWrite)
			}
			return AssetExtraction{}, fmt.Errorf(
				"extract asset %q: %w",
				relative,
				writeErr,
			)
		}
		if closeErr != nil {
			return AssetExtraction{}, fmt.Errorf("close migrated asset %q: %w", relative, closeErr)
		}
		result.Files++
		result.Bytes += written
	}
	return result, nil
}

func readAssetPrefix(reader io.Reader, size int64) ([]byte, error) {
	length := min(size, int64(4096))
	if length <= 0 {
		return nil, nil
	}
	prefix := make([]byte, int(length))
	_, err := io.ReadFull(reader, prefix)
	return prefix, err
}

func unsafeActiveAsset(relative string, mode int64, prefix []byte) bool {
	if mode&0o111 != 0 {
		return true
	}
	name := strings.ToLower(path.Base(strings.ReplaceAll(relative, "\\", "/")))
	for _, managed := range []string{
		"speedtest", "speedtest.exe", "yt-dlp", "yt-dlp.exe",
		"deno", "deno.exe", "ffmpeg", "ffmpeg.exe", "ffprobe", "ffprobe.exe",
	} {
		if name == managed {
			return true
		}
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".exe", ".com", ".bat", ".cmd", ".ps1", ".sh", ".bash", ".zsh",
		".fish", ".py", ".pyc", ".js", ".mjs", ".cjs", ".ts", ".so",
		".dll", ".dylib", ".appimage", ".jar", ".class", ".wasm", ".node":
		return true
	}
	if len(prefix) >= 2 && prefix[0] == 'M' && prefix[1] == 'Z' {
		return true
	}
	if len(prefix) >= 4 {
		magic := string(prefix[:4])
		switch magic {
		case "\x7fELF", "\xfe\xed\xfa\xce", "\xce\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xcf\xfa\xed\xfe":
			return true
		}
	}
	trimmed := prefix
	if len(trimmed) >= 3 && trimmed[0] == 0xef && trimmed[1] == 0xbb && trimmed[2] == 0xbf {
		trimmed = trimmed[3:]
	}
	return len(trimmed) >= 2 && trimmed[0] == '#' && trimmed[1] == '!'
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
