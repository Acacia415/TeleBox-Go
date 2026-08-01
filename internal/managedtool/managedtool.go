package managedtool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Receipt struct {
	Version     int    `json:"version"`
	Source      string `json:"source"`
	ToolVersion string `json:"tool_version,omitempty"`
	SHA256      string `json:"sha256"`
	InstalledAt string `json:"installed_at"`
}

func Executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

func Verify(path, receiptPath string) error {
	if !Executable(path) {
		return errors.New("managed tool is not executable")
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return fmt.Errorf("read managed tool receipt: %w", err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return fmt.Errorf("decode managed tool receipt: %w", err)
	}
	if receipt.Version != 1 || len(receipt.SHA256) != sha256.Size*2 {
		return errors.New("managed tool receipt is invalid")
	}
	digest, err := FileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(digest, receipt.SHA256) {
		return errors.New("managed tool checksum does not match its receipt")
	}
	return nil
}

func WriteReceipt(path, source, toolVersion, digest string) error {
	if len(digest) != sha256.Size*2 {
		return errors.New("managed tool checksum is invalid")
	}
	receipt := Receipt{
		Version:     1,
		Source:      source,
		ToolVersion: toolVersion,
		SHA256:      strings.ToLower(digest),
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".managed-tool-receipt-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tempPath, path)
}

func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Quarantine moves an untrusted managed executable out of the runnable path
// and removes every executable bit. It never touches files outside the caller's
// explicitly supplied quarantine directory.
func Quarantine(path, directory string) (string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("managed tool path is not a regular file")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf(
		"%s.%s.quarantine",
		filepath.Base(path),
		time.Now().UTC().Format("20060102T150405.000000000Z"),
	)
	target := filepath.Join(directory, name)
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", err
	}
	return target, nil
}
