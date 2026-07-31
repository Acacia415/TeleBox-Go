package migration

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/config"
)

const maxSessionSize = 16 << 20

type ImportOptions struct {
	SourceRoot       string
	ConfigPath       string
	SessionPath      string
	AssetsPath       string
	LegacyAssetsPath string
	SkipSession      bool
}

type ImportStats struct {
	CopiedFiles  int   `json:"copied_files"`
	SkippedFiles int   `json:"skipped_files"`
	CopiedBytes  int64 `json:"copied_bytes"`
}

type ImportResult struct {
	Config       ImportStats `json:"config"`
	Session      ImportStats `json:"session"`
	Assets       ImportStats `json:"assets"`
	LegacyAssets ImportStats `json:"legacy_assets"`
}

type convertedLayout struct {
	root         string
	config       string
	session      string
	assets       string
	legacyAssets string
	sourceSHA256 string
}

// ImportConverted copies a validated telebox-migrate conversion into an
// installation without replacing files that already exist. It is safe to run
// repeatedly and keeps unsupported plugin data inert in LegacyAssetsPath.
func ImportConverted(options ImportOptions) (ImportResult, error) {
	layout, err := validateConvertedRoot(options.SourceRoot)
	if err != nil {
		return ImportResult{}, err
	}
	for name, value := range map[string]string{
		"config destination":       options.ConfigPath,
		"session destination":      options.SessionPath,
		"asset destination":        options.AssetsPath,
		"legacy asset destination": options.LegacyAssetsPath,
	} {
		if strings.TrimSpace(value) == "" {
			return ImportResult{}, fmt.Errorf("%s is required", name)
		}
	}
	targets := []struct {
		name string
		path string
	}{
		{name: "config", path: options.ConfigPath},
		{name: "session", path: options.SessionPath},
		{name: "assets", path: options.AssetsPath},
		{name: "legacy assets", path: options.LegacyAssetsPath},
	}
	for index, target := range targets {
		for otherIndex := index + 1; otherIndex < len(targets); otherIndex++ {
			other := targets[otherIndex]
			if outputPathsOverlap(target.path, other.path) {
				return ImportResult{}, fmt.Errorf(
					"%s and %s destinations must not overlap",
					target.name,
					other.name,
				)
			}
		}
		if outputPathsOverlap(layout.root, target.path) {
			return ImportResult{}, fmt.Errorf(
				"%s destination must be outside the converted source",
				target.name,
			)
		}
	}

	var result ImportResult
	result.Config, err = importConfig(layout.config, options)
	if err != nil {
		return ImportResult{}, fmt.Errorf("import config: %w", err)
	}
	if !options.SkipSession {
		result.Session, err = importFile(
			layout.session,
			options.SessionPath,
			maxSessionSize,
		)
		if err != nil {
			return ImportResult{}, fmt.Errorf("import session: %w", err)
		}
	}
	result.Assets, err = importTree(layout.assets, options.AssetsPath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("import assets: %w", err)
	}
	result.LegacyAssets, err = importTree(
		layout.legacyAssets,
		options.LegacyAssetsPath,
	)
	if err != nil {
		return ImportResult{}, fmt.Errorf("import legacy assets: %w", err)
	}
	if err := writeImportReceipt(
		options.LegacyAssetsPath,
		layout.sourceSHA256,
	); err != nil {
		return ImportResult{}, fmt.Errorf("record migration import: %w", err)
	}
	return result, nil
}

func importConfig(source string, options ImportOptions) (ImportStats, error) {
	if existing, err := existingRegularFile(options.ConfigPath); err != nil {
		return ImportStats{}, err
	} else if existing {
		return ImportStats{SkippedFiles: 1}, nil
	}
	data, err := readLimitedFile(source, maxConfigSize)
	if err != nil {
		return ImportStats{}, err
	}
	var converted config.Config
	if err := json.Unmarshal(data, &converted); err != nil {
		return ImportStats{}, err
	}
	configRoot := filepath.Dir(options.ConfigPath)
	dataRoot := filepath.Dir(options.SessionPath)
	converted.Telegram.SessionFile = relativeOrAbsolute(
		configRoot,
		options.SessionPath,
	)
	converted.Storage.Path = relativeOrAbsolute(
		configRoot,
		filepath.Join(dataRoot, "telebox.db"),
	)
	converted.Storage.AssetsPath = relativeOrAbsolute(
		configRoot,
		options.AssetsPath,
	)
	converted.Storage.LegacyAssetsPath = relativeOrAbsolute(
		configRoot,
		options.LegacyAssetsPath,
	)
	converted.Plugins.Directory = relativeOrAbsolute(
		configRoot,
		filepath.Join(dataRoot, "plugins"),
	)
	converted.Logging.Path = relativeOrAbsolute(
		configRoot,
		filepath.Join(dataRoot, "logs", "telebox.log"),
	)
	encoded, err := json.MarshalIndent(converted, "", "  ")
	if err != nil {
		return ImportStats{}, err
	}
	return importBytes(options.ConfigPath, append(encoded, '\n'))
}

func validateConvertedRoot(root string) (convertedLayout, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return convertedLayout{}, errors.New("converted source directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return convertedLayout{}, fmt.Errorf("resolve converted source: %w", err)
	}
	if err := requireDirectory(absolute); err != nil {
		return convertedLayout{}, fmt.Errorf("inspect converted source: %w", err)
	}
	layout := convertedLayout{
		root:         absolute,
		config:       filepath.Join(absolute, "config.json"),
		session:      filepath.Join(absolute, "data", "session.json"),
		assets:       filepath.Join(absolute, "data", "assets"),
		legacyAssets: filepath.Join(absolute, "data", "legacy-assets"),
	}
	if err := validateConvertedConfig(layout.config); err != nil {
		return convertedLayout{}, err
	}
	if err := requireRegularFile(layout.session, maxSessionSize); err != nil {
		return convertedLayout{}, fmt.Errorf("inspect converted session: %w", err)
	}
	if err := requireDirectory(layout.assets); err != nil {
		return convertedLayout{}, fmt.Errorf("inspect converted assets: %w", err)
	}
	if err := requireDirectory(layout.legacyAssets); err != nil {
		return convertedLayout{}, fmt.Errorf(
			"inspect preserved legacy assets: %w",
			err,
		)
	}

	activeManifestPath := filepath.Join(layout.assets, "_migration.json")
	data, err := readLimitedFile(activeManifestPath, maxConfigSize)
	if err != nil {
		return convertedLayout{}, fmt.Errorf("read asset migration manifest: %w", err)
	}
	var activeManifest assetManifest
	if err := json.Unmarshal(data, &activeManifest); err != nil {
		return convertedLayout{}, fmt.Errorf("decode asset migration manifest: %w", err)
	}
	if activeManifest.Version != 1 || !validSHA256(activeManifest.SourceSHA256) {
		return convertedLayout{}, errors.New("asset migration manifest is invalid")
	}
	if err := validateLegacyManifest(
		layout.legacyAssets,
		activeManifest.SourceSHA256,
	); err != nil {
		return convertedLayout{}, err
	}
	layout.sourceSHA256 = strings.ToLower(activeManifest.SourceSHA256)
	return layout, nil
}

func validateConvertedConfig(path string) error {
	data, err := readLimitedFile(path, maxConfigSize)
	if err != nil {
		return fmt.Errorf("read converted config: %w", err)
	}
	var converted struct {
		Telegram struct {
			APIID       int    `json:"api_id"`
			APIHash     string `json:"api_hash"`
			SessionFile string `json:"session_file"`
		} `json:"telegram"`
		Storage struct {
			AssetsPath       string `json:"assets_path"`
			LegacyAssetsPath string `json:"legacy_assets_path"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(data, &converted); err != nil {
		return fmt.Errorf("decode converted config: %w", err)
	}
	if converted.Telegram.APIID <= 0 ||
		strings.TrimSpace(converted.Telegram.APIHash) == "" ||
		strings.TrimSpace(converted.Telegram.SessionFile) == "" ||
		strings.TrimSpace(converted.Storage.AssetsPath) == "" ||
		strings.TrimSpace(converted.Storage.LegacyAssetsPath) == "" {
		return errors.New("converted config is incomplete")
	}
	return nil
}

func validateLegacyManifest(root, sourceSHA256 string) error {
	matches, err := filepath.Glob(filepath.Join(root, "_legacy_manifest*.json"))
	if err != nil {
		return fmt.Errorf("find preserved asset manifest: %w", err)
	}
	for _, candidate := range matches {
		data, readErr := readLimitedFile(candidate, maxConfigSize)
		if readErr != nil {
			continue
		}
		var manifest preservedAssetManifest
		if json.Unmarshal(data, &manifest) == nil &&
			manifest.Format == "telebox-go-legacy-assets" &&
			manifest.Version == 1 &&
			strings.EqualFold(manifest.SourceSHA256, sourceSHA256) {
			return nil
		}
	}
	return errors.New("matching preserved asset manifest was not found")
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == 32
}

func importTree(source, destination string) (ImportStats, error) {
	if err := requireDirectory(source); err != nil {
		return ImportStats{}, err
	}
	if err := ensureDirectory(destination); err != nil {
		return ImportStats{}, err
	}
	var result ImportStats
	var sourceFiles int
	var sourceBytes int64
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source contains symbolic link %q", relative)
		}
		if entry.IsDir() {
			return ensureDirectory(target)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source contains unsupported file %q", relative)
		}
		sourceFiles++
		sourceBytes += info.Size()
		if sourceFiles > maxAssetFileCount ||
			info.Size() < 0 ||
			info.Size() > maxAssetFileSize ||
			sourceBytes > maxAssetTotalSize {
			return fmt.Errorf("source asset limits exceeded at %q", relative)
		}
		fileResult, err := importFile(path, target, maxAssetFileSize)
		if err != nil {
			return fmt.Errorf("copy %q: %w", relative, err)
		}
		result.CopiedFiles += fileResult.CopiedFiles
		result.SkippedFiles += fileResult.SkippedFiles
		result.CopiedBytes += fileResult.CopiedBytes
		return nil
	})
	return result, err
}

func importFile(source, destination string, maxSize int64) (ImportStats, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return ImportStats{}, err
	}
	if !info.Mode().IsRegular() {
		return ImportStats{}, fmt.Errorf("source %q is not a regular file", source)
	}
	if info.Size() < 0 || info.Size() > maxSize {
		return ImportStats{}, fmt.Errorf("source %q exceeds size limit", source)
	}
	if err := ensureDirectory(filepath.Dir(destination)); err != nil {
		return ImportStats{}, err
	}
	if existing, err := existingRegularFile(destination); err != nil {
		return ImportStats{}, err
	} else if existing {
		return ImportStats{SkippedFiles: 1}, nil
	}

	input, err := os.Open(source)
	if err != nil {
		return ImportStats{}, err
	}
	defer input.Close()
	output, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ImportStats{SkippedFiles: 1}, nil
		}
		return ImportStats{}, err
	}
	copied, copyErr := io.Copy(output, io.LimitReader(input, maxSize+1))
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(destination)
		return ImportStats{}, err
	}
	if copied != info.Size() {
		_ = os.Remove(destination)
		return ImportStats{}, fmt.Errorf(
			"copied %d bytes, expected %d",
			copied,
			info.Size(),
		)
	}
	return ImportStats{CopiedFiles: 1, CopiedBytes: copied}, nil
}

func importBytes(destination string, data []byte) (ImportStats, error) {
	if len(data) == 0 {
		return ImportStats{}, errors.New("refusing to import an empty file")
	}
	if err := ensureDirectory(filepath.Dir(destination)); err != nil {
		return ImportStats{}, err
	}
	if existing, err := existingRegularFile(destination); err != nil {
		return ImportStats{}, err
	} else if existing {
		return ImportStats{SkippedFiles: 1}, nil
	}
	file, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ImportStats{SkippedFiles: 1}, nil
		}
		return ImportStats{}, err
	}
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(destination)
		return ImportStats{}, err
	}
	return ImportStats{
		CopiedFiles: 1,
		CopiedBytes: int64(len(data)),
	}, nil
}

func existingRegularFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("destination %q is a symbolic link", path)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("destination %q is not a regular file", path)
	}
	return true, nil
}

func writeImportReceipt(root, sourceSHA256 string) error {
	directory := filepath.Join(root, "_imports")
	if err := ensureDirectory(directory); err != nil {
		return err
	}
	path := filepath.Join(directory, strings.ToLower(sourceSHA256)+".json")
	encoded, err := json.MarshalIndent(struct {
		Format       string `json:"format"`
		Version      int    `json:"version"`
		SourceSHA256 string `json:"source_sha256"`
	}{
		Format:       "telebox-go-migration-import",
		Version:      1,
		SourceSHA256: strings.ToLower(sourceSHA256),
	}, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = importBytes(path, encoded)
	return err
}

func readLimitedFile(path string, maxSize int64) ([]byte, error) {
	if err := requireRegularFile(path, maxSize); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func requireRegularFile(path string, maxSize int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", path)
	}
	if info.Size() <= 0 || info.Size() > maxSize {
		return fmt.Errorf("%q has invalid size %d", path, info.Size())
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

func ensureDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%q is not a directory", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, 0o700)
}
