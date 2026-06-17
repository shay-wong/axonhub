package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/looplj/axonhub/internal/build"
	"github.com/looplj/axonhub/internal/ent"
)

// Version retrieves the system version from system settings.
// Returns empty string if not set.
func (s *SystemService) Version(ctx context.Context) (string, error) {
	value, err := s.getSystemValue(ctx, SystemKeyVersion)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", nil
		}

		return "", fmt.Errorf("failed to get system version: %w", err)
	}

	return value, nil
}

// SetVersion sets the system version.
func (s *SystemService) SetVersion(ctx context.Context, version string) error {
	return s.setSystemValue(ctx, SystemKeyVersion, version)
}

// VersionCheckResult contains the result of a version check.
type VersionCheckResult struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	ReleaseURL     string `json:"release_url"`
}

// CheckForUpdate checks if there is a newer version available on GitHub.
func (s *SystemService) CheckForUpdate(ctx context.Context) (*VersionCheckResult, error) {
	currentVersion := build.Version
	repository := updateRepository()

	latestVersion, err := s.fetchLatestGitHubRelease(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}

	hasUpdate := s.isNewerVersion(currentVersion, latestVersion)
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", repository, latestVersion)

	return &VersionCheckResult{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      hasUpdate,
		ReleaseURL:     releaseURL,
	}, nil
}

// fetchLatestGitHubRelease fetches the latest stable release tag from GitHub.
// It skips beta and rc versions.
func (s *SystemService) fetchLatestGitHubRelease(ctx context.Context) (string, error) {
	return FetchLatestGitHubRelease(ctx)
}

// isNewerVersion compares two semantic versions and returns true if latest is newer than current.
func (s *SystemService) isNewerVersion(current, latest string) bool {
	return IsNewerVersion(current, latest)
}

// GitHubRelease represents a GitHub release.
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
}

type GitHubTag struct {
	Name string `json:"name"`
}

// releaseCooldownDuration is the time to wait after a release is published before considering it available.
// This accounts for build and upload time.
const releaseCooldownDuration = 30 * time.Minute

const defaultUpdateRepository = "shay-wong/axonhub"

// FetchLatestGitHubRelease fetches the latest stable version tag from the configured GitHub repository.
// It checks releases first, falls back to tags, skips beta/rc prerelease versions, and waits for a cooldown period after release.
// In monorepo mode, it only considers tags matching "vX.Y.Z" or "vX.Y.Z-fork.N" (no service prefix).
func FetchLatestGitHubRelease(ctx context.Context) (string, error) {
	repository := updateRepository()
	candidateTags := make([]string, 0, 2)

	releaseTag, releaseErr := fetchLatestGitHubReleaseFromRepository(ctx, repository)
	if releaseErr == nil {
		candidateTags = append(candidateTags, releaseTag)
	}

	tag, tagErr := fetchLatestGitHubTagFromRepository(ctx, repository)
	if tagErr == nil {
		candidateTags = append(candidateTags, tag)
	}

	latestTag, latestErr := selectLatestUpdateTag(candidateTags)
	if latestErr == nil {
		return latestTag, nil
	}

	return "", fmt.Errorf("failed to fetch latest release: %w; failed to fetch latest tag: %w", releaseErr, tagErr)
}

func updateRepository() string {
	if repository := strings.TrimSpace(os.Getenv("AXONHUB_UPDATE_REPOSITORY")); repository != "" {
		return normalizeGitHubRepository(repository)
	}

	if repository := strings.TrimSpace(build.Repository); repository != "" {
		return normalizeGitHubRepository(repository)
	}

	return defaultUpdateRepository
}

func normalizeGitHubRepository(repository string) string {
	repository = strings.TrimSpace(repository)
	repository = strings.TrimPrefix(repository, "https://github.com/")
	repository = strings.TrimPrefix(repository, "git@github.com:")
	repository = strings.TrimSuffix(repository, ".git")
	repository = strings.Trim(repository, "/")
	if repository == "" || strings.Count(repository, "/") != 1 {
		return defaultUpdateRepository
	}

	return repository
}

func fetchLatestGitHubReleaseFromRepository(ctx context.Context, repository string) (string, error) {
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/releases", repository)

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	q.Set("per_page", "10")
	q.Set("page", "1")
	u.RawQuery = q.Encode()
	apiURL := u.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "AxonHub-Version-Checker")

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch releases: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("failed to decode releases: %w", err)
	}

	now := time.Now().UTC()
	candidateTags := make([]string, 0, len(releases))

	// Find the latest stable release (not prerelease, not draft, not beta/rc, and past cooldown)
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}

		// Only consider axonhub tags (vX.Y.Z format, skip service-prefixed tags like "axonclaw/v1.0.0")
		if !isAxonHubTag(release.TagName) {
			continue
		}

		if isPreReleaseTag(release.TagName) {
			continue
		}

		// Check if the release has passed the cooldown period
		if now.Sub(release.PublishedAt) < releaseCooldownDuration {
			continue
		}

		candidateTags = append(candidateTags, release.TagName)
	}

	latestTag, err := selectLatestUpdateTag(candidateTags)
	if err != nil {
		return "", fmt.Errorf("no stable release found")
	}

	return latestTag, nil
}

func fetchLatestGitHubTagFromRepository(ctx context.Context, repository string) (string, error) {
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/tags", repository)

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	q.Set("per_page", "100")
	q.Set("page", "1")
	u.RawQuery = q.Encode()
	apiURL := u.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "AxonHub-Version-Checker")

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch tags: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var tags []GitHubTag
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("failed to decode tags: %w", err)
	}

	candidateTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		candidateTags = append(candidateTags, tag.Name)
	}

	latestTag, err := selectLatestUpdateTag(candidateTags)
	if err != nil {
		return "", fmt.Errorf("no stable tag found")
	}

	return latestTag, nil
}

func selectLatestUpdateTag(tags []string) (string, error) {
	var latest *updateVersion
	latestTag := ""
	for _, tag := range tags {
		if !isAxonHubTag(tag) || isPreReleaseTag(tag) {
			continue
		}

		version, err := parseUpdateVersion(tag)
		if err != nil {
			continue
		}

		if latest == nil || isParsedUpdateVersionNewer(version, *latest) {
			latest = &version
			latestTag = tag
		}
	}

	if latestTag == "" {
		return "", fmt.Errorf("no stable version tag found")
	}

	return latestTag, nil
}

func isParsedUpdateVersionNewer(latest, current updateVersion) bool {
	switch {
	case latest.base.GreaterThan(current.base):
		return true
	case current.base.GreaterThan(latest.base):
		return false
	case latest.hasFork && !current.hasFork:
		return true
	case latest.hasFork && current.hasFork:
		return latest.fork > current.fork
	default:
		return false
	}
}

// isAxonHubTag returns true if the tag is an axonhub version tag (vX.Y.Z or vX.Y.Z-fork.N format).
// Tags with a service prefix (e.g., "axonclaw/v1.0.0") are not axonhub tags.
func isAxonHubTag(tag string) bool {
	if !strings.HasPrefix(tag, "v") {
		return false
	}

	_, err := parseUpdateVersion(tag)
	return err == nil
}

// isPreReleaseTag checks if a version tag contains beta, rc, alpha, or similar prerelease indicators.
func isPreReleaseTag(tag string) bool {
	lowerTag := strings.ToLower(tag)
	preReleasePatterns := []string{"-beta", "-rc", "-alpha", "-dev", "-preview", "-snapshot"}

	for _, pattern := range preReleasePatterns {
		if strings.Contains(lowerTag, pattern) {
			return true
		}
	}

	return false
}

// IsNewerVersion compares two semantic versions and returns true if latest is newer than current.
// Versions are expected to be in format "vX.Y.Z", "X.Y.Z", or "vX.Y.Z-fork.N".
func IsNewerVersion(current, latest string) bool {
	currentVersion, err := parseUpdateVersion(current)
	if err != nil {
		// Handle error, maybe log it and return false
		return false
	}

	latestVersion, err := parseUpdateVersion(latest)
	if err != nil {
		// Handle error, maybe log it and return false
		return false
	}

	return isParsedUpdateVersionNewer(latestVersion, currentVersion)
}

type updateVersion struct {
	base    *semver.Version
	hasFork bool
	fork    int
}

func parseUpdateVersion(version string) (updateVersion, error) {
	baseVersion, hasFork, forkNumber := splitForkVersion(version)
	parsedBase, err := semver.NewVersion(baseVersion)
	if err != nil {
		return updateVersion{}, err
	}

	return updateVersion{
		base:    parsedBase,
		hasFork: hasFork,
		fork:    forkNumber,
	}, nil
}

func splitForkVersion(version string) (baseVersion string, hasFork bool, forkNumber int) {
	version = strings.TrimSpace(version)
	lowerVersion := strings.ToLower(version)
	forkIndex := strings.LastIndex(lowerVersion, "-fork.")
	if forkIndex == -1 {
		return version, false, 0
	}

	forkNumberText := version[forkIndex+len("-fork."):]
	if forkNumberText == "" {
		return version, false, 0
	}

	forkNumber, err := strconv.Atoi(forkNumberText)
	if err != nil || forkNumber < 0 {
		return version, false, 0
	}

	return version[:forkIndex], true, forkNumber
}
