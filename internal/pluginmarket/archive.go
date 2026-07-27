package pluginmarket

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxArchiveEntries = 1024

func extractArchive(
	reader io.ReaderAt,
	size int64,
	format string,
	destination string,
	maxExtracted int64,
) error {
	switch format {
	case "zip":
		return extractZIP(reader, size, destination, maxExtracted)
	case "tar.gz":
		stream := io.NewSectionReader(reader, 0, size)
		compressed, err := gzip.NewReader(stream)
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		defer compressed.Close()
		return extractTAR(tar.NewReader(compressed), destination, maxExtracted)
	default:
		return fmt.Errorf("unsupported plugin archive format %q", format)
	}
}

func extractZIP(
	reader io.ReaderAt,
	size int64,
	destination string,
	maxExtracted int64,
) error {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return fmt.Errorf("open ZIP archive: %w", err)
	}
	if len(archive.File) > maxArchiveEntries {
		return errors.New("plugin archive contains too many files")
	}
	var extracted int64
	for _, item := range archive.File {
		clean, err := safeArchivePath(item.Name)
		if err != nil {
			return err
		}
		mode := item.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsDir() && !mode.IsRegular() {
			return fmt.Errorf("plugin archive contains unsupported entry %q", item.Name)
		}
		if mode.IsDir() {
			if err := os.MkdirAll(filepath.Join(destination, clean), 0o700); err != nil {
				return err
			}
			continue
		}
		extracted += int64(item.UncompressedSize64)
		if extracted > maxExtracted {
			return errors.New("plugin archive expands beyond the configured limit")
		}
		input, err := item.Open()
		if err != nil {
			return err
		}
		err = writeArchiveFile(
			input,
			filepath.Join(destination, clean),
			mode,
			maxExtracted,
		)
		_ = input.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTAR(
	archive *tar.Reader,
	destination string,
	maxExtracted int64,
) error {
	var (
		entries   int
		extracted int64
	)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read TAR archive: %w", err)
		}
		entries++
		if entries > maxArchiveEntries {
			return errors.New("plugin archive contains too many files")
		}
		clean, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(destination, clean), 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return errors.New("plugin archive contains a negative file size")
			}
			extracted += header.Size
			if extracted > maxExtracted {
				return errors.New("plugin archive expands beyond the configured limit")
			}
			if err := writeArchiveFile(
				io.LimitReader(archive, header.Size),
				filepath.Join(destination, clean),
				fs.FileMode(header.Mode),
				header.Size,
			); err != nil {
				return err
			}
		default:
			return fmt.Errorf(
				"plugin archive contains unsupported entry %q",
				header.Name,
			)
		}
	}
}

func safeArchivePath(value string) (string, error) {
	normalized := strings.ReplaceAll(value, `\`, "/")
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || path.IsAbs(clean) ||
		strings.HasPrefix(clean, "../") ||
		strings.Contains(strings.SplitN(clean, "/", 2)[0], ":") {
		return "", fmt.Errorf("unsafe plugin archive path %q", value)
	}
	return filepath.FromSlash(clean), nil
}

func writeArchiveFile(
	reader io.Reader,
	target string,
	mode fs.FileMode,
	limit int64,
) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create plugin archive directory: %w", err)
	}
	permissions := mode.Perm() & 0o700
	if permissions == 0 {
		permissions = 0o600
	}
	output, err := os.OpenFile(
		target,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		permissions,
	)
	if err != nil {
		return fmt.Errorf("create plugin archive file: %w", err)
	}
	written, copyErr := io.Copy(output, io.LimitReader(reader, limit+1))
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("write plugin archive file: %w", copyErr)
	}
	if written > limit {
		return errors.New("plugin archive file exceeds the configured limit")
	}
	if closeErr != nil {
		return fmt.Errorf("close plugin archive file: %w", closeErr)
	}
	return nil
}
