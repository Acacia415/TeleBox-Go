package migration

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

	"github.com/gotd/td/session"

	"github.com/Acacia415/TeleBox-Go/internal/config"
	"github.com/Acacia415/TeleBox-Go/internal/pluginspec"
)

const (
	legacyConfigPath = "telebox/config.json"
	maxConfigSize    = 1 << 20
)

type LegacyConfig struct {
	APIID   int    `json:"api_id"`
	APIHash string `json:"api_hash"`
	Session string `json:"session"`
}

type BackupInventory struct {
	SHA256              string              `json:"sha256"`
	ArchiveBytes        int64               `json:"archive_bytes"`
	Plugins             []string            `json:"plugins"`
	PluginCount         int                 `json:"plugin_count"`
	AssetRoots          []string            `json:"asset_roots"`
	AssetFiles          int                 `json:"migratable_asset_files"`
	AssetBytes          int64               `json:"migratable_asset_bytes"`
	QuarantinedFiles    int                 `json:"quarantined_asset_files"`
	QuarantinedBytes    int64               `json:"quarantined_asset_bytes"`
	PreservedAssetFiles int                 `json:"preserved_asset_files"`
	PreservedAssetBytes int64               `json:"preserved_asset_bytes"`
	SessionFormat       StringSessionFormat `json:"session_format"`
	SessionDC           int                 `json:"session_dc"`
}

type ConvertOptions struct {
	ConfigPath       string
	SessionPath      string
	AssetsPath       string
	LegacyAssetsPath string
}

type ConversionResult struct {
	Inventory    BackupInventory `json:"inventory"`
	Assets       AssetExtraction `json:"assets"`
	LegacyAssets AssetExtraction `json:"legacy_assets"`
}

func InspectBackup(archivePath string) (BackupInventory, error) {
	checksum, size, err := fileChecksum(archivePath)
	if err != nil {
		return BackupInventory{}, err
	}

	legacy, entries, err := scanBackup(archivePath)
	if err != nil {
		return BackupInventory{}, err
	}
	sessionData, format, err := ParseStringSession(legacy.Session)
	if err != nil {
		return BackupInventory{}, fmt.Errorf("parse legacy session: %w", err)
	}

	plugins := make(map[string]struct{})
	assetRoots := make(map[string]struct{})
	for _, name := range entries {
		if strings.HasPrefix(name, "telebox/plugins/") &&
			strings.Count(name, "/") == 2 &&
			strings.HasSuffix(name, ".ts") {
			pluginName := strings.TrimSuffix(path.Base(name), ".ts")
			if _, supported := pluginspec.Find(pluginName); supported {
				plugins[pluginName] = struct{}{}
			}
		}
		if strings.HasPrefix(name, "telebox/assets/") {
			remaining := strings.TrimPrefix(name, "telebox/assets/")
			if slash := strings.IndexByte(remaining, '/'); slash > 0 {
				assetRoots[remaining[:slash]] = struct{}{}
			}
		}
	}

	inventory := BackupInventory{
		SHA256:        checksum,
		ArchiveBytes:  size,
		Plugins:       sortedKeys(plugins),
		AssetRoots:    sortedKeys(assetRoots),
		SessionFormat: format,
		SessionDC:     sessionData.DC,
	}
	inventory.PluginCount = len(inventory.Plugins)
	assets, err := inspectLegacyAssets(archivePath, inventory.Plugins)
	if err != nil {
		return BackupInventory{}, err
	}
	inventory.AssetFiles = assets.Files
	inventory.AssetBytes = assets.Bytes
	inventory.QuarantinedFiles = assets.QuarantinedFiles
	inventory.QuarantinedBytes = assets.QuarantinedBytes
	preservedAssets, err := inspectAllLegacyAssets(archivePath)
	if err != nil {
		return BackupInventory{}, err
	}
	inventory.PreservedAssetFiles = preservedAssets.Files
	inventory.PreservedAssetBytes = preservedAssets.Bytes
	return inventory, nil
}

func ReadLegacyConfig(archivePath string) (LegacyConfig, error) {
	legacy, _, err := scanBackup(archivePath)
	return legacy, err
}

func ConvertBackup(ctx context.Context, archivePath, configPath, sessionPath string) (BackupInventory, error) {
	result, err := ConvertBackupWithOptions(ctx, archivePath, ConvertOptions{
		ConfigPath:  configPath,
		SessionPath: sessionPath,
	})
	return result.Inventory, err
}

func ConvertBackupWithOptions(
	ctx context.Context,
	archivePath string,
	options ConvertOptions,
) (ConversionResult, error) {
	if options.ConfigPath == "" || options.SessionPath == "" {
		return ConversionResult{}, errors.New("config and session output paths are required")
	}
	if options.AssetsPath != "" && options.LegacyAssetsPath != "" &&
		outputPathsOverlap(options.AssetsPath, options.LegacyAssetsPath) {
		return ConversionResult{}, errors.New("asset and legacy asset output paths must not overlap")
	}
	targets := []string{options.ConfigPath, options.SessionPath}
	if options.AssetsPath != "" {
		targets = append(targets, options.AssetsPath)
	}
	if options.LegacyAssetsPath != "" {
		targets = append(targets, options.LegacyAssetsPath)
	}
	for _, target := range targets {
		if _, err := os.Lstat(target); err == nil {
			return ConversionResult{}, fmt.Errorf("refusing to overwrite existing path %q", target)
		} else if !os.IsNotExist(err) {
			return ConversionResult{}, fmt.Errorf("inspect output path %q: %w", target, err)
		}
	}

	inventory, err := InspectBackup(archivePath)
	if err != nil {
		return ConversionResult{}, err
	}
	legacy, err := ReadLegacyConfig(archivePath)
	if err != nil {
		return ConversionResult{}, err
	}
	sessionData, _, err := ParseStringSession(legacy.Session)
	if err != nil {
		return ConversionResult{}, err
	}

	sessionTemp, err := createSessionTemp(ctx, options.SessionPath, sessionData)
	if err != nil {
		return ConversionResult{}, err
	}
	defer os.Remove(sessionTemp)

	cfg := config.Default()
	cfg.Telegram.APIID = legacy.APIID
	cfg.Telegram.APIHash = legacy.APIHash
	cfg.Telegram.SessionFile = relativeOrAbsolute(filepath.Dir(options.ConfigPath), options.SessionPath)
	cfg.Telegram.LoginMode = "existing"
	cfg.Plugins.Enabled = append([]string(nil), inventory.Plugins...)
	if options.AssetsPath != "" {
		cfg.Storage.AssetsPath = relativeOrAbsolute(filepath.Dir(options.ConfigPath), options.AssetsPath)
	}
	if options.LegacyAssetsPath != "" {
		cfg.Storage.LegacyAssetsPath = relativeOrAbsolute(
			filepath.Dir(options.ConfigPath),
			options.LegacyAssetsPath,
		)
	}

	configTemp, err := createConfigTemp(options.ConfigPath, cfg)
	if err != nil {
		return ConversionResult{}, err
	}
	defer os.Remove(configTemp)

	var extraction AssetExtraction
	var legacyExtraction AssetExtraction
	if options.AssetsPath != "" {
		extraction, err = ExtractLegacyAssets(
			archivePath,
			options.AssetsPath,
			inventory.Plugins,
			inventory.SHA256,
		)
		if err != nil {
			return ConversionResult{}, err
		}
	}
	if options.LegacyAssetsPath != "" {
		legacyExtraction, err = PreserveLegacyAssets(
			archivePath,
			options.LegacyAssetsPath,
			inventory.SHA256,
		)
		if err != nil {
			if options.AssetsPath != "" {
				_ = os.RemoveAll(options.AssetsPath)
			}
			return ConversionResult{}, err
		}
	}
	rollbackAssets := func() {
		if options.AssetsPath != "" {
			_ = os.RemoveAll(options.AssetsPath)
		}
		if options.LegacyAssetsPath != "" {
			_ = os.RemoveAll(options.LegacyAssetsPath)
		}
	}

	if err := os.Rename(sessionTemp, options.SessionPath); err != nil {
		rollbackAssets()
		return ConversionResult{}, fmt.Errorf("install converted session: %w", err)
	}
	if err := os.Rename(configTemp, options.ConfigPath); err != nil {
		_ = os.Remove(options.SessionPath)
		rollbackAssets()
		return ConversionResult{}, fmt.Errorf("install converted config: %w", err)
	}
	return ConversionResult{
		Inventory:    inventory,
		Assets:       extraction,
		LegacyAssets: legacyExtraction,
	}, nil
}

func scanBackup(archivePath string) (LegacyConfig, []string, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return LegacyConfig{}, nil, fmt.Errorf("open backup: %w", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return LegacyConfig{}, nil, fmt.Errorf("open backup gzip stream: %w", err)
	}
	defer compressed.Close()

	reader := tar.NewReader(compressed)
	var (
		legacy      LegacyConfig
		configFound bool
		entries     []string
	)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return LegacyConfig{}, nil, fmt.Errorf("read backup tar: %w", err)
		}

		name, err := safeArchivePath(header.Name)
		if err != nil {
			return LegacyConfig{}, nil, err
		}
		entries = append(entries, name)
		if name != legacyConfigPath || header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size < 0 || header.Size > maxConfigSize {
			return LegacyConfig{}, nil, fmt.Errorf("legacy config has invalid size %d", header.Size)
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxConfigSize+1))
		if err != nil {
			return LegacyConfig{}, nil, fmt.Errorf("read legacy config: %w", err)
		}
		if len(data) > maxConfigSize {
			return LegacyConfig{}, nil, errors.New("legacy config exceeds size limit")
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return LegacyConfig{}, nil, fmt.Errorf("decode legacy config: %w", err)
		}
		configFound = true
	}
	if !configFound {
		return LegacyConfig{}, nil, errors.New("backup does not contain telebox/config.json")
	}
	if legacy.APIID <= 0 || strings.TrimSpace(legacy.APIHash) == "" || strings.TrimSpace(legacy.Session) == "" {
		return LegacyConfig{}, nil, errors.New("legacy config is missing API credentials or session")
	}
	return legacy, entries, nil
}

func safeArchivePath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", fmt.Errorf("unsafe archive path %q", value)
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe archive path %q", value)
	}
	return cleaned, nil
}

func fileChecksum(filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open backup for checksum: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash backup: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func createSessionTemp(ctx context.Context, target string, data *session.Data) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create session output directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".telebox-session-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create session temp file: %w", err)
	}
	tempPath := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("secure session temp file: %w", err)
	}

	loader := session.Loader{Storage: &session.FileStorage{Path: tempPath}}
	if err := loader.Save(ctx, data); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("encode gotd session: %w", err)
	}
	return tempPath, nil
}

func createConfigTemp(target string, cfg config.Config) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create config output directory: %w", err)
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode converted config: %w", err)
	}
	encoded = append(encoded, '\n')

	file, err := os.CreateTemp(filepath.Dir(target), ".telebox-config-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create config temp file: %w", err)
	}
	tempPath := file.Name()
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("secure config temp file: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("write config temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("sync config temp file: %w", err)
	}
	return tempPath, nil
}

func relativeOrAbsolute(baseDir, target string) string {
	relative, err := filepath.Rel(baseDir, target)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return relative
	}
	absolute, err := filepath.Abs(target)
	if err == nil {
		return absolute
	}
	return target
}

func outputPathsOverlap(first, second string) bool {
	return outputPathContains(first, second) || outputPathContains(second, first)
}

func outputPathContains(parent, child string) bool {
	parent, parentErr := filepath.Abs(parent)
	child, childErr := filepath.Abs(child)
	if parentErr != nil || childErr != nil {
		return strings.EqualFold(filepath.Clean(parent), filepath.Clean(child))
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil &&
		(relative == "." ||
			(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
