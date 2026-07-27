package corebackup

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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	formatVersion       = 1
	archiveRoot         = "telebox_backup"
	manifestName        = archiveRoot + "/manifest.json"
	maxArchiveBytes     = int64(512 << 20)
	maxExtractedBytes   = int64(2 << 30)
	maxArchiveFileCount = 50000
)

type Storage interface {
	Backup(context.Context, string) error
}

type Paths struct {
	Config       string
	Storage      string
	Assets       string
	LegacyAssets string
	Plugins      string
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Full      bool      `json:"full"`
	Files     []File    `json:"files"`
}

type ApplyResult struct {
	Applied     bool
	Full        bool
	RollbackDir string
}

// Create writes a portable TeleBox-Go backup. Telegram sessions, logs and the
// executable are intentionally excluded; they are host-specific or sensitive.
func Create(
	ctx context.Context,
	store Storage,
	paths Paths,
	full bool,
	destination string,
) (Manifest, error) {
	if store == nil {
		return Manifest{}, errors.New("storage is required")
	}
	if strings.TrimSpace(destination) == "" {
		return Manifest{}, errors.New("backup destination is required")
	}
	tempDir, err := os.MkdirTemp("", "telebox-backup-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("create backup workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	databaseSnapshot := filepath.Join(tempDir, "telebox.db")
	if err := store.Backup(ctx, databaseSnapshot); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create archive directory: %w", err)
	}
	output, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("create backup archive: %w", err)
	}
	keepArchive := false
	defer func() {
		_ = output.Close()
		if !keepArchive {
			_ = os.Remove(destination)
		}
	}()
	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestSpeed)
	if err != nil {
		return Manifest{}, fmt.Errorf("create gzip writer: %w", err)
	}
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := Manifest{
		Format:    "telebox-go-backup",
		Version:   formatVersion,
		CreatedAt: time.Now().UTC(),
		Full:      full,
	}
	sources := []archiveSource{
		{Source: databaseSnapshot, Archive: archiveRoot + "/data/telebox.db"},
		{Source: paths.Assets, Archive: archiveRoot + "/data/assets"},
		{Source: paths.LegacyAssets, Archive: archiveRoot + "/data/legacy-assets"},
		{Source: paths.Plugins, Archive: archiveRoot + "/data/plugins"},
	}
	if full {
		sources = append(sources, archiveSource{
			Source:  paths.Config,
			Archive: archiveRoot + "/config/config.json",
		})
	}
	for _, source := range sources {
		if strings.TrimSpace(source.Source) == "" {
			continue
		}
		if err := addSource(tarWriter, source, &manifest); err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(manifest.Files, func(i, j int) bool {
		return manifest.Files[i].Path < manifest.Files[j].Path
	})
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("encode backup manifest: %w", err)
	}
	if err := writeBytes(tarWriter, manifestName, manifestData, 0o600); err != nil {
		return Manifest{}, err
	}
	if err := tarWriter.Close(); err != nil {
		return Manifest{}, fmt.Errorf("finish tar archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return Manifest{}, fmt.Errorf("finish gzip archive: %w", err)
	}
	if err := output.Close(); err != nil {
		return Manifest{}, fmt.Errorf("finish backup archive: %w", err)
	}
	keepArchive = true
	return manifest, nil
}

type archiveSource struct {
	Source  string
	Archive string
}

func addSource(writer *tar.Writer, source archiveSource, manifest *Manifest) error {
	info, err := os.Lstat(source.Source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect backup source %q: %w", source.Source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup source %q is a symbolic link", source.Source)
	}
	if info.Mode().IsRegular() {
		return addFile(writer, source.Source, source.Archive, info, manifest)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup source %q is not a file or directory", source.Source)
	}
	return filepath.WalkDir(source.Source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(source.Source, path)
		if err != nil {
			return err
		}
		name := source.Archive + "/" + filepath.ToSlash(relative)
		return addFile(writer, path, name, info, manifest)
	})
}

func addFile(
	writer *tar.Writer,
	path string,
	name string,
	info fs.FileInfo,
	manifest *Manifest,
) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup file %q: %w", path, err)
	}
	defer input.Close()
	header := &tar.Header{
		Name:    name,
		Mode:    int64(info.Mode().Perm() & 0o700),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if header.Mode == 0 {
		header.Mode = 0o600
	}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write backup header %q: %w", name, err)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(writer, hash), input)
	if err != nil {
		return fmt.Errorf("archive backup file %q: %w", path, err)
	}
	if written != info.Size() {
		return fmt.Errorf("backup file %q changed while being read", path)
	}
	manifest.Files = append(manifest.Files, File{
		Path:   name,
		Size:   written,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	})
	return nil
}

func writeBytes(writer *tar.Writer, name string, data []byte, mode int64) error {
	if err := writer.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("write %q header: %w", name, err)
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	return nil
}

// Stage validates a downloaded backup and stores it for application before the
// next storage connection is opened.
func Stage(archive string, paths Paths) (Manifest, string, error) {
	manifest, err := Validate(archive)
	if err != nil {
		return Manifest{}, "", err
	}
	pending := PendingPath(paths)
	if err := os.MkdirAll(filepath.Dir(pending), 0o700); err != nil {
		return Manifest{}, "", fmt.Errorf("create restore directory: %w", err)
	}
	temp := pending + ".new"
	if err := copyFile(archive, temp, 0o600); err != nil {
		return Manifest{}, "", err
	}
	if err := os.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temp)
		return Manifest{}, "", fmt.Errorf("replace pending restore: %w", err)
	}
	if err := os.Rename(temp, pending); err != nil {
		_ = os.Remove(temp)
		return Manifest{}, "", fmt.Errorf("stage restore archive: %w", err)
	}
	return manifest, pending, nil
}

func PendingPath(paths Paths) string {
	return filepath.Join(filepath.Dir(paths.Storage), ".telebox-restore-pending.tar.gz")
}

func Validate(archive string) (Manifest, error) {
	tempDir, err := os.MkdirTemp("", "telebox-validate-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(tempDir)
	return extractAndValidate(archive, tempDir)
}

// ApplyPending restores a previously validated archive. Existing files are
// moved to a timestamped rollback directory before replacements are activated.
func ApplyPending(paths Paths) (ApplyResult, error) {
	pending := PendingPath(paths)
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		return ApplyResult{}, nil
	} else if err != nil {
		return ApplyResult{}, err
	}
	parent := filepath.Dir(paths.Storage)
	stage, err := os.MkdirTemp(parent, ".telebox-restore-work-*")
	if err != nil {
		return ApplyResult{}, fmt.Errorf("create restore workspace: %w", err)
	}
	defer os.RemoveAll(stage)
	manifest, err := extractAndValidate(pending, stage)
	if err != nil {
		quarantine := pending + ".invalid-" + time.Now().Format("20060102-150405")
		_ = os.Rename(pending, quarantine)
		return ApplyResult{}, fmt.Errorf("validate pending restore (moved to %s): %w", quarantine, err)
	}
	rollback := filepath.Join(
		parent,
		"telebox-restore-backup-"+time.Now().Format("20060102-150405"),
	)
	if err := os.MkdirAll(rollback, 0o700); err != nil {
		return ApplyResult{}, fmt.Errorf("create rollback directory: %w", err)
	}
	replacements := []replacement{
		{
			Source: filepath.Join(stage, filepath.FromSlash(archiveRoot+"/data/telebox.db")),
			Target: paths.Storage,
			Backup: filepath.Join(rollback, "telebox.db"),
		},
		{
			Source: filepath.Join(stage, filepath.FromSlash(archiveRoot+"/data/assets")),
			Target: paths.Assets,
			Backup: filepath.Join(rollback, "assets"),
		},
		{
			Source: filepath.Join(stage, filepath.FromSlash(archiveRoot+"/data/legacy-assets")),
			Target: paths.LegacyAssets,
			Backup: filepath.Join(rollback, "legacy-assets"),
		},
		{
			Source: filepath.Join(stage, filepath.FromSlash(archiveRoot+"/data/plugins")),
			Target: paths.Plugins,
			Backup: filepath.Join(rollback, "plugins"),
		},
	}
	if manifest.Full {
		replacements = append(replacements, replacement{
			Source: filepath.Join(stage, filepath.FromSlash(archiveRoot+"/config/config.json")),
			Target: paths.Config,
			Backup: filepath.Join(rollback, "config.json"),
		})
	}
	for _, item := range replacements {
		if _, err := os.Stat(item.Source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return ApplyResult{}, err
		}
		if strings.TrimSpace(item.Target) == "" {
			return ApplyResult{}, fmt.Errorf("restore target is required for %q", item.Source)
		}
		if err := ensureRestoreTarget(item.Target); err != nil {
			return ApplyResult{}, err
		}
	}
	var applied []replacement
	for _, item := range replacements {
		if _, err := os.Stat(item.Source); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(item.Target), 0o700); err != nil {
			rollbackApplied(applied)
			return ApplyResult{}, err
		}
		if _, err := os.Stat(item.Target); err == nil {
			if err := os.MkdirAll(filepath.Dir(item.Backup), 0o700); err != nil {
				rollbackApplied(applied)
				return ApplyResult{}, err
			}
			if err := os.Rename(item.Target, item.Backup); err != nil {
				rollbackApplied(applied)
				return ApplyResult{}, fmt.Errorf("preserve %q: %w", item.Target, err)
			}
			item.HadOriginal = true
		} else if !errors.Is(err, os.ErrNotExist) {
			rollbackApplied(applied)
			return ApplyResult{}, err
		}
		if err := os.Rename(item.Source, item.Target); err != nil {
			if item.HadOriginal {
				_ = os.Rename(item.Backup, item.Target)
			}
			rollbackApplied(applied)
			return ApplyResult{}, fmt.Errorf("activate restored %q: %w", item.Target, err)
		}
		applied = append(applied, item)
	}
	if err := os.Remove(pending); err != nil {
		return ApplyResult{}, fmt.Errorf("remove applied restore marker: %w", err)
	}
	return ApplyResult{Applied: true, Full: manifest.Full, RollbackDir: rollback}, nil
}

type replacement struct {
	Source      string
	Target      string
	Backup      string
	HadOriginal bool
}

func rollbackApplied(items []replacement) {
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		_ = os.RemoveAll(item.Target)
		if item.HadOriginal {
			_ = os.Rename(item.Backup, item.Target)
		}
	}
}

func ensureRestoreTarget(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	root := string(filepath.Separator)
	if volume != "" {
		root = volume + string(filepath.Separator)
	}
	if filepath.Clean(absolute) == filepath.Clean(root) ||
		filepath.Dir(absolute) == absolute {
		return fmt.Errorf("refusing broad restore target %q", absolute)
	}
	return nil
}

func extractAndValidate(archive, destination string) (Manifest, error) {
	info, err := os.Stat(archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect restore archive: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxArchiveBytes {
		return Manifest{}, fmt.Errorf("backup archive must be a regular file no larger than %d MiB", maxArchiveBytes>>20)
	}
	input, err := os.Open(archive)
	if err != nil {
		return Manifest{}, err
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return Manifest{}, fmt.Errorf("open gzip backup: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	hashes := make(map[string]File)
	var (
		manifestData []byte
		total        int64
		count        int
	)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read backup archive: %w", err)
		}
		count++
		if count > maxArchiveFileCount {
			return Manifest{}, errors.New("backup contains too many entries")
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return Manifest{}, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return Manifest{}, fmt.Errorf("backup entry %q has unsupported type", name)
		}
		if header.Size < 0 || total > maxExtractedBytes-header.Size {
			return Manifest{}, errors.New("expanded backup is too large")
		}
		total += header.Size
		if name == manifestName {
			if header.Size > 8<<20 {
				return Manifest{}, errors.New("backup manifest is too large")
			}
			manifestData, err = io.ReadAll(io.LimitReader(tarReader, header.Size))
			if err != nil {
				return Manifest{}, err
			}
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return Manifest{}, err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return Manifest{}, fmt.Errorf("extract %q: %w", name, err)
		}
		hash := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(output, hash), tarReader, header.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return Manifest{}, fmt.Errorf("extract %q: %w", name, errors.Join(copyErr, closeErr))
		}
		hashes[name] = File{
			Path:   name,
			Size:   written,
			SHA256: hex.EncodeToString(hash.Sum(nil)),
		}
	}
	if len(manifestData) == 0 {
		return Manifest{}, errors.New("backup manifest is missing")
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.Format != "telebox-go-backup" || manifest.Version != formatVersion {
		return Manifest{}, errors.New("unsupported TeleBox-Go backup format")
	}
	if len(manifest.Files) != len(hashes) {
		return Manifest{}, errors.New("backup file list does not match manifest")
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, expected := range manifest.Files {
		if _, duplicate := seen[expected.Path]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate manifest entry %q", expected.Path)
		}
		seen[expected.Path] = struct{}{}
		actual, ok := hashes[expected.Path]
		if !ok || actual.Size != expected.Size ||
			!strings.EqualFold(actual.SHA256, expected.SHA256) {
			return Manifest{}, fmt.Errorf("backup checksum mismatch for %q", expected.Path)
		}
	}
	if _, ok := hashes[archiveRoot+"/data/telebox.db"]; !ok {
		return Manifest{}, errors.New("backup does not contain TeleBox storage")
	}
	if manifest.Full {
		if _, ok := hashes[archiveRoot+"/config/config.json"]; !ok {
			return Manifest{}, errors.New("full backup does not contain configuration")
		}
	}
	return manifest, nil
}

func safeArchiveName(name string) (string, error) {
	name = filepath.ToSlash(strings.TrimSpace(name))
	clean := filepath.ToSlash(filepath.Clean(name))
	if name == "" || clean == "." || clean != name ||
		strings.HasPrefix(clean, "/") ||
		strings.HasPrefix(clean, "../") ||
		strings.Contains(clean, ":") {
		return "", fmt.Errorf("unsafe backup path %q", name)
	}
	if clean != manifestName &&
		!strings.HasPrefix(clean, archiveRoot+"/data/") &&
		!strings.HasPrefix(clean, archiveRoot+"/config/") {
		return "", fmt.Errorf("unexpected backup path %q", clean)
	}
	return clean, nil
}

func copyFile(source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}
