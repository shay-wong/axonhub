package biz

import (
	"archive/zip"
	"bufio"
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
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/build"
	"github.com/looplj/axonhub/internal/log"
)

var (
	ErrNoUpdateAvailable         = errors.New("no update available")
	ErrRollbackVersionNotAllowed = errors.New("version is not available for rollback")
	ErrUpdateInProgress          = errors.New("an update is already in progress")
	ErrUpdateUnsupported         = errors.New("in-app updates require a supported release build")
)

const (
	maxUpdateArchiveSize  = 500 << 20
	maxUpdateChecksumSize = 1 << 20
	maxRollbackVersions   = 3
	rollbackFetchPageSize = 15
	updateRequestTimeout  = 10 * time.Minute
	restartDelay          = 300 * time.Millisecond
)

type RollbackVersion struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt"`
	ReleaseURL  string    `json:"releaseUrl"`
}

// UpdateService installs a checksum-verified release over the currently running binary.
type UpdateService struct {
	mu             sync.Mutex
	httpClient     *http.Client
	executablePath func() (string, error)
	exit           func(int)
	currentVersion string
	releaseAPIURL  string
	releaseBaseURL string
	goos           string
	goarch         string
	supportedBuild bool
}

func NewUpdateService() *UpdateService {
	client := &http.Client{Timeout: updateRequestTimeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many update download redirects")
		}
		if !isTrustedGitHubDownloadURL(req.URL) {
			return fmt.Errorf("update download redirected to untrusted host %q", req.URL.Hostname())
		}
		return nil
	}

	return &UpdateService{
		httpClient:     client,
		executablePath: os.Executable,
		exit:           os.Exit,
		currentVersion: build.Version,
		releaseAPIURL:  "https://api.github.com",
		releaseBaseURL: "https://github.com",
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		supportedBuild: strings.TrimSpace(build.BuildTime) != "" && runtime.GOOS != "windows",
	}
}

func (s *UpdateService) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	if !s.supportedBuild {
		return nil, ErrUpdateUnsupported
	}

	releases, err := s.fetchRecentReleases(ctx)
	if err != nil {
		return nil, err
	}

	return selectRollbackVersions(releases, s.currentVersion, maxRollbackVersions), nil
}

func (s *UpdateService) RollbackToVersion(ctx context.Context, version string) error {
	target := strings.TrimSpace(version)
	if !isAxonHubTag(target) {
		return ErrRollbackVersionNotAllowed
	}

	versions, err := s.ListRollbackVersions(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range versions {
		if candidate.Version == target {
			return s.InstallVersion(ctx, target)
		}
	}

	return ErrRollbackVersionNotAllowed
}

func (s *UpdateService) InstallVersion(ctx context.Context, version string) error {
	if !s.mu.TryLock() {
		return ErrUpdateInProgress
	}
	defer s.mu.Unlock()
	if !s.supportedBuild {
		return ErrUpdateUnsupported
	}

	if !isAxonHubTag(version) {
		return fmt.Errorf("invalid update version %q", version)
	}

	executablePath, err := s.executablePath()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	executablePath, err = filepath.EvalSymlinks(executablePath)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	archiveName := updateArchiveName(version, s.goos, s.goarch)
	releaseURL := fmt.Sprintf(
		"%s/%s/releases/download/%s",
		strings.TrimRight(s.releaseBaseURL, "/"),
		updateRepository(),
		url.PathEscape(version),
	)

	tempDir, err := os.MkdirTemp(filepath.Dir(executablePath), ".axonhub-update-*")
	if err != nil {
		return fmt.Errorf("create update directory beside executable: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	archivePath := filepath.Join(tempDir, archiveName)
	if err := s.downloadFile(ctx, releaseURL+"/"+url.PathEscape(archiveName), archivePath, maxUpdateArchiveSize); err != nil {
		return fmt.Errorf("download update archive: %w", err)
	}

	checksums, err := s.downloadBytes(ctx, releaseURL+"/checksums.txt", maxUpdateChecksumSize)
	if err != nil {
		return fmt.Errorf("download update checksums: %w", err)
	}
	if err := verifyUpdateChecksum(archivePath, archiveName, checksums); err != nil {
		return fmt.Errorf("verify update archive: %w", err)
	}

	newBinaryPath := filepath.Join(tempDir, "axonhub.new")
	if err := extractUpdateBinary(archivePath, newBinaryPath, s.goos); err != nil {
		return fmt.Errorf("extract update binary: %w", err)
	}

	info, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	if err := os.Chmod(newBinaryPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set update executable permissions: %w", err)
	}

	if err := replaceExecutable(executablePath, newBinaryPath); err != nil {
		return err
	}

	return nil
}

func (s *UpdateService) Supported() bool {
	return s.supportedBuild
}

func (s *UpdateService) RestartAsync() error {
	if !s.mu.TryLock() {
		return ErrUpdateInProgress
	}
	time.AfterFunc(restartDelay, func() {
		defer s.mu.Unlock()
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error(context.Background(), "panic while restarting AxonHub", log.Any("panic", recovered))
			}
		}()
		s.exit(0)
	})
	return nil
}

func (s *UpdateService) fetchRecentReleases(ctx context.Context) ([]GitHubRelease, error) {
	endpoint, err := url.Parse(fmt.Sprintf(
		"%s/repos/%s/releases",
		strings.TrimRight(s.releaseAPIURL, "/"),
		updateRepository(),
	))
	if err != nil {
		return nil, fmt.Errorf("parse release API URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("per_page", fmt.Sprint(rollbackFetchPageSize))
	query.Set("page", "1")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create release history request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "AxonHub-Updater")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch release history: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release history returned HTTP %d", response.StatusCode)
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, maxUpdateChecksumSize)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode release history: %w", err)
	}
	return releases, nil
}

func selectRollbackVersions(releases []GitHubRelease, currentVersion string, limit int) []RollbackVersion {
	current, err := parseUpdateVersion(currentVersion)
	if err != nil || limit <= 0 {
		return []RollbackVersion{}
	}
	channel := updateChannelStable
	if isBetaReleaseTag(currentVersion) {
		channel = updateChannelBeta
	}

	seen := make(map[string]struct{}, len(releases))
	versions := make([]RollbackVersion, 0, limit)
	for _, release := range releases {
		candidate, parseErr := parseUpdateVersion(release.TagName)
		if release.Draft || (release.Prerelease && !isBetaReleaseTag(release.TagName)) ||
			parseErr != nil || candidate.hasFork != current.hasFork ||
			!isAxonHubTag(release.TagName) || !isUpdateChannelTag(release.TagName, channel) ||
			!isParsedUpdateVersionNewer(current, candidate) {
			continue
		}
		if _, ok := seen[release.TagName]; ok {
			continue
		}
		seen[release.TagName] = struct{}{}
		versions = append(versions, RollbackVersion{
			Version:     release.TagName,
			PublishedAt: release.PublishedAt,
			ReleaseURL:  release.HTMLURL,
		})
	}

	sort.SliceStable(versions, func(i, j int) bool {
		left, _ := parseUpdateVersion(versions[i].Version)
		right, _ := parseUpdateVersion(versions[j].Version)
		return isParsedUpdateVersionNewer(left, right)
	})
	if len(versions) > limit {
		versions = versions[:limit]
	}
	return versions
}

func (s *UpdateService) downloadFile(ctx context.Context, rawURL, destination string, maxSize int64) error {
	response, err := s.doDownload(ctx, rawURL)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.ContentLength > maxSize {
		return fmt.Errorf("download is too large: %d bytes", response.ContentLength)
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	if written > maxSize {
		_ = os.Remove(destination)
		return fmt.Errorf("download exceeded %d bytes", maxSize)
	}
	return nil
}

func (s *UpdateService) downloadBytes(ctx context.Context, rawURL string, maxSize int64) ([]byte, error) {
	response, err := s.doDownload(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.ContentLength > maxSize {
		return nil, fmt.Errorf("download is too large: %d bytes", response.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("download exceeded %d bytes", maxSize)
	}
	return data, nil
}

func (s *UpdateService) doDownload(ctx context.Context, rawURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "AxonHub-Updater")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func updateArchiveName(version, goos, goarch string) string {
	return fmt.Sprintf("axonhub_%s_%s_%s.zip", strings.TrimPrefix(version, "v"), goos, goarch)
}

func verifyUpdateChecksum(archivePath, archiveName string, checksums []byte) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))

	scanner := bufio.NewScanner(strings.NewReader(string(checksums)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != archiveName {
			continue
		}
		if !strings.EqualFold(fields[0], actual) {
			return fmt.Errorf("checksum mismatch for %s", archiveName)
		}
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("checksum not found for %s", archiveName)
}

func extractUpdateBinary(archivePath, destination, goos string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = archive.Close() }()

	binaryName := "axonhub"
	if goos == "windows" {
		binaryName += ".exe"
	}

	for _, entry := range archive.File {
		if !entry.Mode().IsRegular() || filepath.Base(entry.Name) != binaryName {
			continue
		}
		if entry.UncompressedSize64 > maxUpdateArchiveSize {
			return fmt.Errorf("binary exceeds %d bytes", maxUpdateArchiveSize)
		}

		source, err := entry.Open()
		if err != nil {
			return err
		}
		destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = source.Close()
			return err
		}
		written, copyErr := io.Copy(destinationFile, io.LimitReader(source, maxUpdateArchiveSize+1))
		closeDestinationErr := destinationFile.Close()
		closeSourceErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeDestinationErr != nil {
			return closeDestinationErr
		}
		if closeSourceErr != nil {
			return closeSourceErr
		}
		if written > maxUpdateArchiveSize {
			return fmt.Errorf("binary exceeds %d bytes", maxUpdateArchiveSize)
		}
		return nil
	}

	return fmt.Errorf("%s not found in update archive", binaryName)
}

func replaceExecutable(executablePath, newBinaryPath string) error {
	backupPath := executablePath + ".backup"
	if err := os.Remove(backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove previous update backup: %w", err)
	}
	if err := os.Rename(executablePath, backupPath); err != nil {
		return fmt.Errorf("backup current executable: %w", err)
	}
	if err := os.Rename(newBinaryPath, executablePath); err != nil {
		if restoreErr := os.Rename(backupPath, executablePath); restoreErr != nil {
			return fmt.Errorf("replace executable: %w; restore backup: %v", err, restoreErr)
		}
		return fmt.Errorf("replace executable (backup restored): %w", err)
	}
	return nil
}

func isTrustedGitHubDownloadURL(downloadURL *url.URL) bool {
	if downloadURL == nil || downloadURL.Scheme != "https" {
		return false
	}
	host := strings.ToLower(downloadURL.Hostname())
	return host == "github.com" ||
		host == "objects.githubusercontent.com" ||
		strings.HasSuffix(host, ".githubusercontent.com")
}
