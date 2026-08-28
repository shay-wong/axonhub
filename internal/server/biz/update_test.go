package biz

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpdateArchiveName(t *testing.T) {
	require.Equal(t, "axonhub_1.0.0-beta7-fork.7_linux_amd64.zip", updateArchiveName("v1.0.0-beta7-fork.7", "linux", "amd64"))
}

func TestUpdateChecksumAndExtraction(t *testing.T) {
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "axonhub.zip")
	archiveFile, err := os.Create(archivePath)
	require.NoError(t, err)
	archive := zip.NewWriter(archiveFile)
	binary, err := archive.Create("axonhub")
	require.NoError(t, err)
	_, err = binary.Write([]byte("new-binary"))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, archiveFile.Close())

	archiveData, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	hash := sha256.Sum256(archiveData)
	checksums := []byte(hex.EncodeToString(hash[:]) + "  axonhub.zip\n")
	require.NoError(t, verifyUpdateChecksum(archivePath, "axonhub.zip", checksums))
	require.ErrorContains(t, verifyUpdateChecksum(archivePath, "axonhub.zip", []byte("bad  axonhub.zip\n")), "checksum mismatch")

	destination := filepath.Join(directory, "axonhub.new")
	require.NoError(t, extractUpdateBinary(archivePath, destination, "linux"))
	extracted, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, []byte("new-binary"), extracted)
}

func TestUpdateReplaceExecutableKeepsBackup(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "axonhub")
	newBinary := filepath.Join(directory, "axonhub.new")
	require.NoError(t, os.WriteFile(executable, []byte("old"), 0o755))
	require.NoError(t, os.WriteFile(newBinary, []byte("new"), 0o755))

	require.NoError(t, replaceExecutable(executable, newBinary))
	current, err := os.ReadFile(executable)
	require.NoError(t, err)
	backup, err := os.ReadFile(executable + ".backup")
	require.NoError(t, err)
	require.Equal(t, []byte("new"), current)
	require.Equal(t, []byte("old"), backup)
}

func TestUpdateTrustedGitHubDownloadURL(t *testing.T) {
	for _, rawURL := range []string{
		"https://github.com/shay-wong/axonhub/releases/download/v1/axonhub.zip",
		"https://objects.githubusercontent.com/example",
		"https://release-assets.githubusercontent.com/example",
	} {
		parsed, err := url.Parse(rawURL)
		require.NoError(t, err)
		require.True(t, isTrustedGitHubDownloadURL(parsed), rawURL)
	}

	for _, rawURL := range []string{
		"http://github.com/example",
		"https://github.com.example.com/example",
		"https://example.com/example",
	} {
		parsed, err := url.Parse(rawURL)
		require.NoError(t, err)
		require.False(t, isTrustedGitHubDownloadURL(parsed), rawURL)
	}
}

func TestUpdateExtractBinaryRejectsArchiveWithoutBinary(t *testing.T) {
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "axonhub.zip")
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	file, err := archive.Create("README.md")
	require.NoError(t, err)
	_, err = file.Write([]byte("missing"))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, os.WriteFile(archivePath, data.Bytes(), 0o600))

	err = extractUpdateBinary(archivePath, filepath.Join(directory, "axonhub.new"), "linux")
	require.ErrorContains(t, err, "axonhub not found")
}

func TestUpdateInstallVersionRejectsDevelopmentBuild(t *testing.T) {
	service := NewUpdateService()
	service.supportedBuild = false

	require.False(t, service.Supported())
	err := service.InstallVersion(context.Background(), "v1.0.0-beta7-fork.7")
	require.ErrorIs(t, err, ErrUpdateUnsupported)
}

func TestUpdateRollbackVersionsFilterSortAndLimit(t *testing.T) {
	now := time.Now().UTC()
	releases := []GitHubRelease{
		{TagName: "v1.0.0-beta6-fork.9", PublishedAt: now.Add(-6 * time.Hour), HTMLURL: "https://example.com/6"},
		{TagName: "v1.0.0-beta7-fork.5", PublishedAt: now.Add(-2 * time.Hour), HTMLURL: "https://example.com/5"},
		{TagName: "v1.0.0-beta7", PublishedAt: now.Add(-time.Hour)},
		{TagName: "v1.0.0-beta7-fork.7", PublishedAt: now},
		{TagName: "v1.0.0-beta8-fork.1", PublishedAt: now.Add(time.Hour)},
		{TagName: "v1.0.0-beta7-fork.6", PublishedAt: now.Add(-time.Hour), HTMLURL: "https://example.com/6-fork"},
		{TagName: "v1.0.0-beta7-fork.4", Draft: true},
		{TagName: "v1.0.0-fork.8", Prerelease: true},
		{TagName: "v1.0.0-beta7-fork.6", PublishedAt: now.Add(-time.Hour)},
		{TagName: "v1.0.0-fork.9", PublishedAt: now.Add(-time.Hour)},
	}

	versions := selectRollbackVersions(releases, "v1.0.0-beta7-fork.7", 3)
	require.Equal(t, []RollbackVersion{
		{Version: "v1.0.0-beta7-fork.6", PublishedAt: now.Add(-time.Hour), ReleaseURL: "https://example.com/6-fork"},
		{Version: "v1.0.0-beta7-fork.5", PublishedAt: now.Add(-2 * time.Hour), ReleaseURL: "https://example.com/5"},
		{Version: "v1.0.0-beta6-fork.9", PublishedAt: now.Add(-6 * time.Hour), ReleaseURL: "https://example.com/6"},
	}, versions)
}

func TestUpdateRollbackRejectsVersionOutsideAllowedHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		require.Equal(t, "15", request.URL.Query().Get("per_page"))
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[{"tag_name":"v1.0.0-beta7-fork.6","published_at":"2026-08-01T00:00:00Z"}]`))
	}))
	defer server.Close()

	service := NewUpdateService()
	service.supportedBuild = true
	service.currentVersion = "v1.0.0-beta7-fork.7"
	service.releaseAPIURL = server.URL
	service.httpClient = server.Client()

	err := service.RollbackToVersion(context.Background(), "v1.0.0-beta7-fork.5")
	require.ErrorIs(t, err, ErrRollbackVersionNotAllowed)
}

func TestUpdateRestartAsyncExitsAfterResponseDelay(t *testing.T) {
	exited := make(chan int, 1)
	service := NewUpdateService()
	service.exit = func(code int) { exited <- code }

	require.NoError(t, service.RestartAsync())
	require.ErrorIs(t, service.InstallVersion(context.Background(), "v1.0.0-beta7-fork.7"), ErrUpdateInProgress)

	select {
	case code := <-exited:
		require.Zero(t, code)
	case <-time.After(time.Second):
		t.Fatal("restart did not exit")
	}
}
