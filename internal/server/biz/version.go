package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
func (s *SystemService) CheckForUpdate(ctx context.Context, includeBeta bool) (*VersionCheckResult, error) {
	currentVersion := build.Version
	repository := updateRepository()

	latestVersion, err := s.fetchLatestGitHubRelease(ctx, includeBeta)
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
func (s *SystemService) fetchLatestGitHubRelease(ctx context.Context, includeBeta bool) (string, error) {
	return FetchLatestGitHubRelease(ctx, includeBeta)
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
	HTMLURL     string    `json:"html_url"`
}

type GitHubTag struct {
	Name string `json:"name"`
}

// releaseCooldownDuration is the time to wait after a release is published before considering it available.
// This accounts for build and upload time.
const releaseCooldownDuration = 30 * time.Minute

const defaultUpdateRepository = "shay-wong/axonhub"

const (
	updateChannelStable = "stable"
	updateChannelBeta   = "beta"
)

var compactPrereleasePattern = regexp.MustCompile(`(?i)-(alpha|beta|rc)([0-9]+)($|[+-].*)`)

// FetchLatestGitHubRelease fetches the latest version tag from the configured GitHub repository.
// It checks releases first, falls back to tags, follows AXONHUB_UPDATE_CHANNEL, and waits for a cooldown period after release.
// In monorepo mode, it only considers unprefixed axonhub tags (no service prefix).
func FetchLatestGitHubRelease(ctx context.Context, includeBeta bool) (string, error) {
	repository := updateRepository()
	channels := updateChannels(includeBeta)
	candidateTags := make([]string, 0, 2)

	releaseTag, releaseErr := fetchLatestGitHubReleaseFromRepository(ctx, repository, channels)
	if releaseErr == nil {
		candidateTags = append(candidateTags, releaseTag)
	}

	tag, tagErr := fetchLatestGitHubTagFromRepository(ctx, repository, channels)
	if tagErr == nil {
		candidateTags = append(candidateTags, tag)
	}

	latestTag, latestErr := selectLatestUpdateTagForChannels(candidateTags, channels...)
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

func updateChannel() string {
	if channel := normalizeUpdateChannel(os.Getenv("AXONHUB_UPDATE_CHANNEL")); channel != "" {
		return channel
	}

	if channel := normalizeUpdateChannel(build.UpdateChannel); channel != "" {
		return channel
	}

	return updateChannelStable
}

func updateChannels(includeBeta bool) []string {
	channel := updateChannel()
	if includeBeta && channel == updateChannelStable {
		return []string{updateChannelStable, updateChannelBeta}
	}

	return []string{channel}
}

func normalizeUpdateChannel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case updateChannelStable:
		return updateChannelStable
	case updateChannelBeta:
		return updateChannelBeta
	default:
		return ""
	}
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

func fetchLatestGitHubReleaseFromRepository(ctx context.Context, repository string, channels []string) (string, error) {
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/releases", repository)

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

	return selectLatestGitHubRelease(releases, channels, time.Now().UTC())
}

func selectLatestGitHubRelease(releases []GitHubRelease, channels []string, now time.Time) (string, error) {
	candidateTags := make([]string, 0, len(releases))
	for _, release := range releases {
		if release.Draft ||
			(release.Prerelease && !isBetaReleaseTag(release.TagName)) ||
			!isAxonHubTag(release.TagName) ||
			!isUpdateChannelTagForAny(release.TagName, channels) {
			continue
		}
		if now.Sub(release.PublishedAt) < releaseCooldownDuration {
			continue
		}

		candidateTags = append(candidateTags, release.TagName)
	}

	latestTag, err := selectLatestUpdateTagForChannels(candidateTags, channels...)
	if err != nil {
		return "", fmt.Errorf("no eligible release found")
	}

	return latestTag, nil
}

func fetchLatestGitHubTagFromRepository(ctx context.Context, repository string, channels []string) (string, error) {
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

	latestTag, err := selectLatestUpdateTagForChannels(candidateTags, channels...)
	if err != nil {
		return "", fmt.Errorf("no eligible tag found")
	}

	return latestTag, nil
}

func selectLatestUpdateTag(tags []string) (string, error) {
	return selectLatestUpdateTagForChannel(tags, updateChannelStable)
}

func selectLatestUpdateTagForChannel(tags []string, channel string) (string, error) {
	return selectLatestUpdateTagForChannels(tags, channel)
}

func selectLatestUpdateTagForChannels(tags []string, channels ...string) (string, error) {
	normalizedChannels := make([]string, 0, len(channels))
	for _, channel := range channels {
		if normalized := normalizeUpdateChannel(channel); normalized != "" {
			normalizedChannels = append(normalizedChannels, normalized)
		}
	}
	if len(normalizedChannels) == 0 {
		normalizedChannels = append(normalizedChannels, updateChannelStable)
	}

	var latest *updateVersion
	latestTag := ""
	for _, tag := range tags {
		if !isAxonHubTag(tag) || !isUpdateChannelTagForAny(tag, normalizedChannels) {
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
		return "", fmt.Errorf("no eligible version tag found")
	}

	return latestTag, nil
}

func isUpdateChannelTag(tag, channel string) bool {
	isPrerelease := isPreReleaseTag(tag)
	switch normalizeUpdateChannel(channel) {
	case updateChannelBeta:
		return isBetaReleaseTag(tag)
	case updateChannelStable:
		return !isPrerelease
	default:
		return !isPrerelease
	}
}

func isUpdateChannelTagForAny(tag string, channels []string) bool {
	for _, channel := range channels {
		if isUpdateChannelTag(tag, channel) {
			return true
		}
	}

	return false
}

func isBetaReleaseTag(tag string) bool {
	lowerTag := strings.ToLower(tag)
	return strings.Contains(lowerTag, "-beta") || strings.Contains(lowerTag, "-rc")
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

// isAxonHubTag returns true if the tag is an axonhub semantic version tag.
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
	baseVersion = normalizeCompactPrereleaseVersion(baseVersion)
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

func normalizeCompactPrereleaseVersion(version string) string {
	return compactPrereleasePattern.ReplaceAllString(version, "-$1.$2$3")
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
