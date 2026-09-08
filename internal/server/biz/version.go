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
	return compareUpdateVersions(latest, current) > 0
}

func compareUpdateVersions(a, b updateVersion) int {
	if result := compareVersions(a.base, b.base); result != 0 {
		return result
	}

	switch {
	case a.hasFork && !b.hasFork:
		return 1
	case !a.hasFork && b.hasFork:
		return -1
	case a.hasFork && b.hasFork:
		return compareInt(a.fork, b.fork)
	default:
		return 0
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
	result, err := CompareVersions(latest, current)
	if err != nil {
		// Handle error, maybe log it and return false
		return false
	}

	return result > 0
}

// CompareVersions compares two version strings and reports whether a is less than,
// equal to, or greater than b, returning -1, 0, or 1 respectively.
// Versions are expected to be in format "vX.Y.Z" or "X.Y.Z", optionally with a
// prerelease or fork suffix. Fork revisions follow their upstream base version.
// An error is returned when either version cannot be parsed.
func CompareVersions(a, b string) (int, error) {
	vA, err := parseUpdateVersion(a)
	if err != nil {
		return 0, err
	}

	vB, err := parseUpdateVersion(b)
	if err != nil {
		return 0, err
	}

	return compareUpdateVersions(vA, vB), nil
}

// ParseVersion parses a version string in the format "vX.Y.Z" or "X.Y.Z".
func ParseVersion(version string) (*semver.Version, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("failed to parse version %q: %w", version, err)
	}

	return v, nil
}

// compareVersions compares the release numbers first and falls back to
// comparePrerelease, which understands prerelease identifiers such as "beta10"
// that glue a name to a counter without a dot separator.
func compareVersions(a, b *semver.Version) int {
	if result := compareUint64(a.Major(), b.Major()); result != 0 {
		return result
	}

	if result := compareUint64(a.Minor(), b.Minor()); result != 0 {
		return result
	}

	if result := compareUint64(a.Patch(), b.Patch()); result != 0 {
		return result
	}

	return comparePrerelease(a.Prerelease(), b.Prerelease())
}

// comparePrerelease compares two prerelease strings following the SemVer
// precedence rules, except that each identifier is further split into digit and
// non-digit runs. That makes the counter in tags like "beta9" and "beta10"
// compare numerically, and treats "beta10" as equal to "beta.10".
// A release without a prerelease outranks any prerelease of the same version.
func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}

	if a == "" {
		return 1
	}

	if b == "" {
		return -1
	}

	tokensA := prereleaseTokens(a)
	tokensB := prereleaseTokens(b)

	for i := 0; i < len(tokensA) && i < len(tokensB); i++ {
		if result := comparePrereleaseToken(tokensA[i], tokensB[i]); result != 0 {
			return result
		}
	}

	// A larger set of prerelease tokens takes precedence when every shared token is equal.
	return compareInt(len(tokensA), len(tokensB))
}

type updateVersion struct {
	base    *semver.Version
	hasFork bool
	fork    int
}

func parseUpdateVersion(version string) (updateVersion, error) {
	baseVersion, hasFork, forkNumber := splitForkVersion(version)
	parsedBase, err := ParseVersion(baseVersion)
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

// prereleaseTokens splits a prerelease string on dots and then into digit and
// non-digit runs, e.g. "beta10" and "beta.10" both become []string{"beta", "10"}.
func prereleaseTokens(s string) []string {
	var tokens []string

	for identifier := range strings.SplitSeq(s, ".") {
		tokens = append(tokens, splitDigitRuns(identifier)...)
	}

	return tokens
}

// comparePrereleaseToken compares a single flattened prerelease token.
func comparePrereleaseToken(a, b string) int {
	numericA, numericB := isDigitRun(a), isDigitRun(b)

	switch {
	case numericA && numericB:
		return compareNumericRuns(a, b)
	case numericA != numericB:
		// Numeric identifiers always have lower precedence than alphanumeric ones.
		if numericA {
			return -1
		}

		return 1
	default:
		return strings.Compare(a, b)
	}
}

// splitDigitRuns splits s into consecutive runs of digits and non-digits,
// e.g. "beta10" becomes []string{"beta", "10"}.
func splitDigitRuns(s string) []string {
	if s == "" {
		return nil
	}

	var runs []string

	start := 0
	digits := isASCIIDigit(s[0])

	for i := 1; i < len(s); i++ {
		if isASCIIDigit(s[i]) == digits {
			continue
		}

		runs = append(runs, s[start:i])
		start = i
		digits = isASCIIDigit(s[i])
	}

	return append(runs, s[start:])
}

// compareNumericRuns compares two digit runs by value without parsing them,
// so arbitrarily long counters cannot overflow.
func compareNumericRuns(a, b string) int {
	trimmedA := strings.TrimLeft(a, "0")
	trimmedB := strings.TrimLeft(b, "0")

	if result := compareInt(len(trimmedA), len(trimmedB)); result != 0 {
		return result
	}

	return strings.Compare(trimmedA, trimmedB)
}

func isDigitRun(s string) bool {
	return s != "" && isASCIIDigit(s[0])
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
