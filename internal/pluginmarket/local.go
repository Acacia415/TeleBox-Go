package pluginmarket

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

// InspectArchive validates an owner-provided package and returns its manifest
// without installing it. Local packages use the same format as release assets.
func (m *Manager) InspectArchive(
	archive []byte,
	format string,
) (pluginapi.Manifest, error) {
	stage, err := m.extractLocalArchive(archive, format)
	if err != nil {
		return pluginapi.Manifest{}, err
	}
	defer os.RemoveAll(stage)
	return readManifest(filepath.Join(stage, "plugin.json"))
}

// InstallArchive installs an owner-provided compiled package. Unlike catalog
// installs it cannot compare a remote checksum, so this operation must remain
// owner-only at the command layer.
func (m *Manager) InstallArchive(
	_ context.Context,
	archive []byte,
	format string,
) (InstallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stage, err := m.extractLocalArchive(archive, format)
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(stage)
	manifest, err := readManifest(filepath.Join(stage, "plugin.json"))
	if err != nil {
		return InstallResult{}, err
	}
	name := manifest.Name
	if !validPluginName(name) || name == "core" {
		return InstallResult{}, fmt.Errorf("invalid local plugin name %q", name)
	}
	executable, err := resolveExecutable(stage, manifest.Executable)
	if err != nil {
		return InstallResult{}, err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, 0o700); err != nil {
			return InstallResult{}, fmt.Errorf("make plugin executable: %w", err)
		}
	}
	target := filepath.Join(m.directory, name)
	previous := ""
	if installed, readErr := m.readInstalled(name); readErr == nil {
		previous = installed.Manifest.Version
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return InstallResult{}, readErr
	}
	backup := ""
	if _, statErr := os.Stat(target); statErr == nil {
		backup = filepath.Join(
			m.directory,
			fmt.Sprintf(".backup-%s-%d", name, time.Now().UnixNano()),
		)
		if err := os.Rename(target, backup); err != nil {
			return InstallResult{}, fmt.Errorf("stage previous plugin: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return InstallResult{}, statErr
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return InstallResult{}, fmt.Errorf("activate local plugin %q: %w", name, err)
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return InstallResult{}, fmt.Errorf("remove previous plugin backup: %w", err)
		}
	}
	installed, err := m.readInstalled(name)
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{
		Installed: installed,
		Previous:  previous,
		Updated:   previous != "" && previous != manifest.Version,
	}, nil
}

func (m *Manager) extractLocalArchive(
	archive []byte,
	format string,
) (string, error) {
	if len(archive) == 0 {
		return "", errors.New("local plugin archive is empty")
	}
	if int64(len(archive)) > m.maxArchiveBytes {
		return "", errors.New("local plugin archive exceeds the configured limit")
	}
	if format != "zip" && format != "tar.gz" {
		return "", fmt.Errorf("unsupported local plugin format %q", format)
	}
	if err := os.MkdirAll(m.directory, 0o700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(m.directory, ".inspect-local-")
	if err != nil {
		return "", err
	}
	if err := extractArchive(
		bytes.NewReader(archive),
		int64(len(archive)),
		format,
		stage,
		m.maxArchiveBytes*4,
	); err != nil {
		_ = os.RemoveAll(stage)
		return "", fmt.Errorf("extract local plugin: %w", err)
	}
	return stage, nil
}

// Export writes an installed package as a ZIP that can be restored with
// "p i" on the same operating system and architecture.
func (m *Manager) Export(name, destination string) (Installed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.ToLower(strings.TrimSpace(name))
	installed, err := m.readInstalled(name)
	if err != nil {
		return Installed{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Installed{}, err
	}
	output, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return Installed{}, err
	}
	success := false
	defer func() {
		_ = output.Close()
		if !success {
			_ = os.Remove(destination)
		}
	}()
	writer := zip.NewWriter(output)
	var paths []string
	if err := filepath.WalkDir(
		installed.Directory,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == installed.Directory {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("installed plugin contains symbolic link %q", path)
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("installed plugin contains unsupported file %q", path)
			}
			paths = append(paths, path)
			return nil
		},
	); err != nil {
		_ = writer.Close()
		return Installed{}, err
	}
	sort.Strings(paths)
	if len(paths) == 0 || len(paths) > maxArchiveEntries {
		_ = writer.Close()
		return Installed{}, errors.New("installed plugin file count is invalid")
	}
	for _, path := range paths {
		if err := addExportFile(writer, installed.Directory, path); err != nil {
			_ = writer.Close()
			return Installed{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return Installed{}, err
	}
	if err := output.Close(); err != nil {
		return Installed{}, err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return Installed{}, err
	}
	if info.Size() > m.maxArchiveBytes {
		return Installed{}, errors.New("exported plugin archive exceeds the configured limit")
	}
	success = true
	return installed, nil
}

func addExportFile(writer *zip.Writer, root, source string) error {
	relative, err := filepath.Rel(root, source)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	header := &zip.FileHeader{
		Name:   filepath.ToSlash(relative),
		Method: zip.Deflate,
	}
	header.SetMode(info.Mode().Perm() & 0o700)
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
