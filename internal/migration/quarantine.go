package migration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/managedtool"
)

// QuarantineUnsafeActiveAssets repairs conversions made by older migrators.
// Plugin runtime executables are framework-managed and excluded; managed tools
// are retained only when their installation receipt matches their checksum.
func QuarantineUnsafeActiveAssets(
	assetsRoot string,
	legacyRoot string,
) (AssetExtraction, error) {
	if strings.TrimSpace(assetsRoot) == "" || strings.TrimSpace(legacyRoot) == "" {
		return AssetExtraction{}, errors.New("asset and legacy asset roots are required")
	}
	if err := os.MkdirAll(assetsRoot, 0o700); err != nil {
		return AssetExtraction{}, err
	}
	quarantineRoot := filepath.Join(legacyRoot, "_quarantine", "active-assets")
	var result AssetExtraction
	err := filepath.WalkDir(assetsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == assetsRoot {
			return nil
		}
		relative, err := filepath.Rel(assetsRoot, path)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if entry.IsDir() {
			if slashed == "plugin-runtime" || strings.HasPrefix(slashed, "plugin-runtime/") ||
				slashed == "quarantine" || strings.HasPrefix(slashed, "quarantine/") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("active asset %q is not a regular file", relative)
		}
		if trustedManagedActiveAsset(assetsRoot, slashed, path) {
			return nil
		}
		prefix, err := readFilePrefix(path, info.Size())
		if err != nil {
			return err
		}
		if !unsafeActiveAsset(slashed, int64(info.Mode().Perm()), prefix) {
			return nil
		}
		target, err := availableQuarantinePath(quarantineRoot, relative)
		if err != nil {
			return err
		}
		if err := movePrivateFile(path, target); err != nil {
			return fmt.Errorf("quarantine active asset %q: %w", relative, err)
		}
		result.QuarantinedFiles++
		result.QuarantinedBytes += info.Size()
		return nil
	})
	return result, err
}

func trustedManagedActiveAsset(root, relative, filePath string) bool {
	switch relative {
	case "speedlink/speedtest", "speedlink/speedtest.exe":
		return managedtool.Verify(
			filePath,
			filepath.Join(root, "speedlink", ".speedtest-managed.json"),
		) == nil
	case "ytdlp/yt-dlp", "ytdlp/yt-dlp.exe":
		return managedtool.Verify(
			filePath,
			filepath.Join(root, "ytdlp", ".yt-dlp-managed.json"),
		) == nil
	default:
		return false
	}
}

func availableQuarantinePath(root, relative string) (string, error) {
	base := filepath.Join(root, relative)
	for index := 0; index <= maxAssetFileCount; index++ {
		candidate := base
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", base, index)
		}
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("no available quarantine filename")
}

func movePrivateFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := os.Rename(source, target); err == nil {
		return os.Chmod(target, 0o600)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(target)
		return err
	}
	return os.Remove(source)
}
