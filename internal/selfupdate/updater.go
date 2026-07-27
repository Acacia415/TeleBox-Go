package selfupdate

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
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	defaultRepository   = "Acacia415/TeleBox-Go"
	maxReleaseJSONBytes = 1 << 20
	maxChecksumBytes    = 1 << 20
	maxArchiveBytes     = 64 << 20
	maxBinaryBytes      = 64 << 20
)

var (
	ErrUnsupportedPlatform = errors.New("self-update supports Linux amd64 and arm64 only")
	ErrUnversionedBuild    = errors.New("development build cannot be compared with a release")
)

type Status struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
}

type Result struct {
	Status
	Updated    bool
	AssetName  string
	BackupPath string
}

type Updater struct {
	currentVersion string
	releaseAPIURL  string
	executablePath string
	goos           string
	goarch         string
	client         *http.Client
	allowHTTP      bool
	replace        func(string, string) error
}

type releaseMetadata struct {
	TagName string         `json:"tag_name"`
	Draft   bool           `json:"draft"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func New(currentVersion string) *Updater {
	repository := defaultRepository
	return &Updater{
		currentVersion: strings.TrimSpace(currentVersion),
		releaseAPIURL:  "https://api.github.com/repos/" + repository + "/releases/latest",
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		client: &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many update download redirects")
				}
				return validateDownloadURL(request.URL, false)
			},
		},
	}
}

func (u *Updater) Check(ctx context.Context) (Status, error) {
	if !supportedPlatform(u.goos, u.goarch) {
		return Status{}, ErrUnsupportedPlatform
	}
	release, err := u.latestRelease(ctx)
	if err != nil {
		return Status{}, err
	}
	available, err := updateAvailable(u.currentVersion, release.TagName)
	if err != nil {
		return Status{}, err
	}
	return Status{
		CurrentVersion:  normalizeVersion(u.currentVersion),
		LatestVersion:   normalizeVersion(release.TagName),
		UpdateAvailable: available,
	}, nil
}

func (u *Updater) Update(ctx context.Context, force bool) (Result, error) {
	if !supportedPlatform(u.goos, u.goarch) {
		return Result{}, ErrUnsupportedPlatform
	}

	release, err := u.latestRelease(ctx)
	if err != nil {
		return Result{}, err
	}
	available, err := updateAvailable(u.currentVersion, release.TagName)
	if err != nil && !force {
		return Result{}, err
	}
	status := Status{
		CurrentVersion:  normalizeVersion(u.currentVersion),
		LatestVersion:   normalizeVersion(release.TagName),
		UpdateAvailable: available,
	}
	if !available && !force {
		return Result{Status: status}, nil
	}

	assetName := fmt.Sprintf(
		"telebox-go_%s_linux_%s.tar.gz",
		normalizeVersion(release.TagName),
		u.goarch,
	)
	archiveAsset, ok := findAsset(release.Assets, assetName)
	if !ok {
		return Result{}, fmt.Errorf("release %s does not contain %s", release.TagName, assetName)
	}
	checksumAsset, ok := findAsset(release.Assets, "SHA256SUMS.txt")
	if !ok {
		return Result{}, fmt.Errorf("release %s does not contain SHA256SUMS.txt", release.TagName)
	}

	checksums, err := u.downloadBytes(ctx, checksumAsset, maxChecksumBytes)
	if err != nil {
		return Result{}, fmt.Errorf("download release checksums: %w", err)
	}
	expected, err := checksumFor(checksums, assetName)
	if err != nil {
		return Result{}, err
	}

	executable, err := u.resolveExecutable()
	if err != nil {
		return Result{}, err
	}
	archive, err := os.CreateTemp(filepath.Dir(executable), ".telebox-update-*.tar.gz")
	if err != nil {
		return Result{}, fmt.Errorf("create update download: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	actual, err := u.downloadFile(ctx, archiveAsset, archive, maxArchiveBytes)
	closeErr := archive.Close()
	if err != nil {
		return Result{}, fmt.Errorf("download update package: %w", err)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close update package: %w", closeErr)
	}
	if !strings.EqualFold(actual, expected) {
		return Result{}, fmt.Errorf(
			"update package SHA-256 mismatch: expected %s, got %s",
			expected,
			actual,
		)
	}

	staged, err := extractExecutable(archivePath, filepath.Dir(executable))
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(staged)

	info, err := os.Stat(executable)
	if err != nil {
		return Result{}, fmt.Errorf("inspect current executable: %w", err)
	}
	if err := os.Chmod(staged, info.Mode().Perm()); err != nil {
		return Result{}, fmt.Errorf("set update executable permissions: %w", err)
	}

	backupPath := executable + ".previous"
	if err := copyExecutable(executable, backupPath, info.Mode().Perm()); err != nil {
		return Result{}, fmt.Errorf("back up current executable: %w", err)
	}
	replace := u.replace
	if replace == nil {
		replace = os.Rename
	}
	if err := replace(staged, executable); err != nil {
		return Result{}, fmt.Errorf("replace current executable: %w", err)
	}

	return Result{
		Status:     status,
		Updated:    true,
		AssetName:  assetName,
		BackupPath: backupPath,
	}, nil
}

func supportedPlatform(goos, goarch string) bool {
	return goos == "linux" && (goarch == "amd64" || goarch == "arm64")
}

func (u *Updater) latestRelease(ctx context.Context) (releaseMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.releaseAPIURL, nil)
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("create latest release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "TeleBox-Go/"+normalizeVersion(u.currentVersion))

	response, err := u.client.Do(request)
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("request latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return releaseMetadata{}, fmt.Errorf("latest release returned HTTP %d", response.StatusCode)
	}
	data, err := readLimited(response.Body, maxReleaseJSONBytes)
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("read latest release: %w", err)
	}
	var release releaseMetadata
	if err := json.Unmarshal(data, &release); err != nil {
		return releaseMetadata{}, fmt.Errorf("decode latest release: %w", err)
	}
	if release.Draft {
		return releaseMetadata{}, errors.New("latest release is a draft")
	}
	if semver.Canonical(normalizeVersion(release.TagName)) == "" {
		return releaseMetadata{}, fmt.Errorf("latest release has invalid version %q", release.TagName)
	}
	return release, nil
}

func (u *Updater) downloadBytes(
	ctx context.Context,
	asset releaseAsset,
	limit int64,
) ([]byte, error) {
	response, err := u.requestAsset(ctx, asset, limit)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return readLimited(response.Body, limit)
}

func (u *Updater) downloadFile(
	ctx context.Context,
	asset releaseAsset,
	target *os.File,
	limit int64,
) (string, error) {
	response, err := u.requestAsset(ctx, asset, limit)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", err
	}
	if written > limit {
		return "", fmt.Errorf("asset exceeds %d bytes", limit)
	}
	if asset.Size > 0 && written != asset.Size {
		return "", fmt.Errorf("asset size is %d bytes, expected %d", written, asset.Size)
	}
	if err := target.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (u *Updater) requestAsset(
	ctx context.Context,
	asset releaseAsset,
	limit int64,
) (*http.Response, error) {
	if asset.Size < 0 || asset.Size > limit {
		return nil, fmt.Errorf("asset %s exceeds the size limit", asset.Name)
	}
	parsed, err := url.Parse(asset.URL)
	if err != nil {
		return nil, fmt.Errorf("parse asset URL: %w", err)
	}
	if err := validateDownloadURL(parsed, u.allowHTTP); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "TeleBox-Go/"+normalizeVersion(u.currentVersion))
	response, err := u.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("asset %s returned HTTP %d", asset.Name, response.StatusCode)
	}
	if response.ContentLength > limit {
		response.Body.Close()
		return nil, fmt.Errorf("asset %s exceeds the size limit", asset.Name)
	}
	return response, nil
}

func (u *Updater) resolveExecutable() (string, error) {
	executable := strings.TrimSpace(u.executablePath)
	var err error
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate current executable: %w", err)
		}
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}
	return resolved, nil
}

func updateAvailable(current, latest string) (bool, error) {
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)
	if semver.Canonical(current) == "" {
		return false, ErrUnversionedBuild
	}
	if semver.Canonical(latest) == "" {
		return false, fmt.Errorf("invalid release version %q", latest)
	}
	return semver.Compare(latest, current) > 0, nil
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if !strings.HasPrefix(value, "v") {
		return "v" + value
	}
	return value
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func checksumFor(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 for %s", assetName)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid SHA-256 for %s", assetName)
		}
		return sum, nil
	}
	return "", fmt.Errorf("SHA256SUMS.txt does not contain %s", assetName)
}

func extractExecutable(archivePath, targetDirectory string) (string, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open update package: %w", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("open compressed update package: %w", err)
	}
	defer compressed.Close()

	reader := tar.NewReader(compressed)
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", fmt.Errorf("read update package: %w", nextErr)
		}
		name := path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("update package contains unsafe path %q", header.Name)
		}
		if path.Base(name) != "telebox" {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxBinaryBytes {
			return "", errors.New("update package contains an invalid telebox executable")
		}
		target, err := os.CreateTemp(targetDirectory, ".telebox-new-*")
		if err != nil {
			return "", fmt.Errorf("create staged executable: %w", err)
		}
		targetPath := target.Name()
		written, copyErr := io.Copy(target, io.LimitReader(reader, maxBinaryBytes+1))
		syncErr := target.Sync()
		closeErr := target.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size {
			os.Remove(targetPath)
			extractErr := errors.Join(copyErr, syncErr, closeErr)
			if written != header.Size {
				extractErr = errors.Join(extractErr, fmt.Errorf(
					"extract executable: wrote %d bytes, expected %d",
					written,
					header.Size,
				))
			}
			return "", extractErr
		}
		return targetPath, nil
	}
	return "", errors.New("update package does not contain the telebox executable")
}

func copyExecutable(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	temp, err := os.CreateTemp(filepath.Dir(destination), ".telebox-backup-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, input); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, destination)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func validateDownloadURL(target *url.URL, allowHTTP bool) error {
	if target == nil || target.User != nil || target.Hostname() == "" {
		return errors.New("update download URL is invalid")
	}
	if target.Scheme == "http" && allowHTTP {
		return nil
	}
	if target.Scheme != "https" {
		return errors.New("update download URL must use HTTPS")
	}
	host := strings.ToLower(target.Hostname())
	if host == "github.com" ||
		host == "api.github.com" ||
		strings.HasSuffix(host, ".githubusercontent.com") {
		return nil
	}
	return fmt.Errorf("update download host %q is not allowed", host)
}
