package biz

import (
	"testing"

	"github.com/looplj/axonhub/internal/build"
	"github.com/stretchr/testify/require"
)

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{
			name:    "latest version is newer - major version",
			current: "v1.0.0",
			latest:  "v2.0.0",
			want:    true,
		},
		{
			name:    "latest version is newer - minor version",
			current: "v1.0.0",
			latest:  "v1.1.0",
			want:    true,
		},
		{
			name:    "latest version is newer - patch version",
			current: "v1.0.0",
			latest:  "v1.0.1",
			want:    true,
		},
		{
			name:    "latest version is same",
			current: "v1.0.0",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "latest version is older",
			current: "v2.0.0",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "versions without v prefix - latest newer",
			current: "1.0.0",
			latest:  "1.1.0",
			want:    true,
		},
		{
			name:    "mixed v prefix - current has v, latest doesn't",
			current: "v1.0.0",
			latest:  "1.1.0",
			want:    true,
		},
		{
			name:    "mixed v prefix - latest has v, current doesn't",
			current: "1.0.0",
			latest:  "v1.1.0",
			want:    true,
		},
		{
			name:    "complex version comparison",
			current: "v1.2.3",
			latest:  "v2.0.0",
			want:    true,
		},
		{
			name:    "same major, higher minor",
			current: "v1.5.0",
			latest:  "v1.6.0",
			want:    true,
		},
		{
			name:    "same major and minor, higher patch",
			current: "v1.5.2",
			latest:  "v1.5.3",
			want:    true,
		},
		{
			name:    "invalid current version",
			current: "invalid",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "invalid latest version",
			current: "v1.0.0",
			latest:  "invalid",
			want:    false,
		},
		{
			name:    "both invalid versions",
			current: "invalid",
			latest:  "invalid",
			want:    false,
		},
		{
			name:    "empty current version",
			current: "",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "empty latest version",
			current: "v1.0.0",
			latest:  "",
			want:    false,
		},
		{
			name:    "both empty versions",
			current: "",
			latest:  "",
			want:    false,
		},
		{
			name:    "prerelease versions - current is prerelease",
			current: "v1.0.0-beta",
			latest:  "v1.0.0",
			want:    true,
		},
		{
			name:    "prerelease versions - latest is prerelease",
			current: "v1.0.0",
			latest:  "v1.0.1-beta",
			want:    true,
		},
		{
			name:    "upstream beta without dot is newer",
			current: "v1.0.0-beta3",
			latest:  "v1.0.0-beta4",
			want:    true,
		},
		{
			name:    "upstream beta numeric suffix sorts numerically",
			current: "v1.0.0-beta4",
			latest:  "v1.0.0-beta10",
			want:    true,
		},
		{
			name:    "build metadata",
			current: "v1.0.0+build.1",
			latest:  "v1.0.0+build.2",
			want:    false, // build metadata doesn't affect version comparison
		},
		{
			name:    "version with many digits",
			current: "v1.2.3",
			latest:  "v1.2.3.4",
			want:    false, // semver only supports 3-part versions
		},
		{
			name:    "fork build is not older than same upstream version",
			current: "v0.9.43-fork.1",
			latest:  "v0.9.43",
			want:    false,
		},
		{
			name:    "same fork version",
			current: "v0.9.43-fork.1",
			latest:  "v0.9.43-fork.1",
			want:    false,
		},
		{
			name:    "newer fork version",
			current: "v0.9.43-fork.1",
			latest:  "v0.9.43-fork.2",
			want:    true,
		},
		{
			name:    "newer upstream base version",
			current: "v0.9.43-fork.1",
			latest:  "v0.9.44",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNewerVersion(tt.current, tt.latest)
			require.Equal(t, tt.want, got, "IsNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		})
	}
}

func TestNormalizeGitHubRepository(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		want       string
	}{
		{
			name:       "owner repo",
			repository: "shay-wong/axonhub",
			want:       "shay-wong/axonhub",
		},
		{
			name:       "https url",
			repository: "https://github.com/shay-wong/axonhub",
			want:       "shay-wong/axonhub",
		},
		{
			name:       "ssh url",
			repository: "git@github.com:shay-wong/axonhub.git",
			want:       "shay-wong/axonhub",
		},
		{
			name:       "invalid repo falls back",
			repository: "axonhub",
			want:       "shay-wong/axonhub",
		},
		{
			name:       "empty repo falls back",
			repository: "",
			want:       "shay-wong/axonhub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeGitHubRepository(tt.repository)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestUpdateRepositoryUsesEnvironment(t *testing.T) {
	t.Setenv("AXONHUB_UPDATE_REPOSITORY", "https://github.com/shay-wong/axonhub")

	require.Equal(t, "shay-wong/axonhub", updateRepository())
}

func TestUpdateRepositoryUsesBuildRepository(t *testing.T) {
	t.Setenv("AXONHUB_UPDATE_REPOSITORY", "")

	previousRepository := build.Repository
	build.Repository = "git@github.com:shay-wong/axonhub.git"
	t.Cleanup(func() {
		build.Repository = previousRepository
	})

	require.Equal(t, "shay-wong/axonhub", updateRepository())
}

func TestUpdateChannel(t *testing.T) {
	t.Run("default stable", func(t *testing.T) {
		t.Setenv("AXONHUB_UPDATE_CHANNEL", "")

		previousChannel := build.UpdateChannel
		build.UpdateChannel = ""
		t.Cleanup(func() {
			build.UpdateChannel = previousChannel
		})

		require.Equal(t, updateChannelStable, updateChannel())
	})

	t.Run("environment beta", func(t *testing.T) {
		t.Setenv("AXONHUB_UPDATE_CHANNEL", "beta")

		require.Equal(t, updateChannelBeta, updateChannel())
	})

	t.Run("build channel fallback", func(t *testing.T) {
		t.Setenv("AXONHUB_UPDATE_CHANNEL", "")

		previousChannel := build.UpdateChannel
		build.UpdateChannel = "beta"
		t.Cleanup(func() {
			build.UpdateChannel = previousChannel
		})

		require.Equal(t, updateChannelBeta, updateChannel())
	})

	t.Run("invalid channel falls back to stable", func(t *testing.T) {
		t.Setenv("AXONHUB_UPDATE_CHANNEL", "nightly")

		require.Equal(t, updateChannelStable, updateChannel())
	})
}

func TestSelectLatestUpdateTag(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{
			name: "fork tag wins over same upstream base",
			tags: []string{
				"v0.9.43",
				"v0.9.43-fork.1",
			},
			want: "v0.9.43-fork.1",
		},
		{
			name: "newer fork tag wins",
			tags: []string{
				"v0.9.43-fork.1",
				"v0.9.43-fork.2",
			},
			want: "v0.9.43-fork.2",
		},
		{
			name: "newer base wins over older fork base",
			tags: []string{
				"v0.9.43-fork.2",
				"v0.9.44",
			},
			want: "v0.9.44",
		},
		{
			name: "skip prerelease and service tags",
			tags: []string{
				"axonclaw/v9.9.9",
				"v0.9.44-beta",
				"v0.9.43-fork.1",
			},
			want: "v0.9.43-fork.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectLatestUpdateTag(tt.tags)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSelectLatestUpdateTagForChannel(t *testing.T) {
	tags := []string{
		"v0.9.44",
		"v1.0.0-beta1",
		"v1.0.0-beta4",
		"v1.0.0-rc.1",
		"axonclaw/v9.9.9",
	}

	stableTag, err := selectLatestUpdateTagForChannel(tags, updateChannelStable)
	require.NoError(t, err)
	require.Equal(t, "v0.9.44", stableTag)

	betaTag, err := selectLatestUpdateTagForChannel(tags, updateChannelBeta)
	require.NoError(t, err)
	require.Equal(t, "v1.0.0-rc.1", betaTag)
}

func TestSelectLatestUpdateTagForChannel_UpstreamBetaTags(t *testing.T) {
	tags := []string{
		"v0.9.43",
		"v0.9.42-beta1",
		"v1.0.0-alpha2",
		"v1.0.0-beta1",
		"v1.0.0-beta4",
		"v1.0.0-beta10",
		"axonclaw-v0.0.1-beta10",
	}

	betaTag, err := selectLatestUpdateTagForChannel(tags, updateChannelBeta)
	require.NoError(t, err)
	require.Equal(t, "v1.0.0-beta10", betaTag)
}

func TestIsAxonHubTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{
			name: "standard axonhub tag",
			tag:  "v1.0.0",
			want: true,
		},
		{
			name: "axonhub prerelease tag",
			tag:  "v1.0.0-beta",
			want: true,
		},
		{
			name: "fork release tag",
			tag:  "v0.9.43-fork.1",
			want: true,
		},
		{
			name: "axonclaw prefixed tag",
			tag:  "axonclaw/v1.0.0",
			want: false,
		},
		{
			name: "other service prefixed tag",
			tag:  "other-service/v2.0.0",
			want: false,
		},
		{
			name: "empty tag",
			tag:  "",
			want: false,
		},
		{
			name: "non-version tag",
			tag:  "vrelease-2024",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAxonHubTag(tt.tag)
			require.Equal(t, tt.want, got, "isAxonHubTag(%q) = %v, want %v", tt.tag, got, tt.want)
		})
	}
}

func TestIsPreReleaseTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{
			name: "beta tag",
			tag:  "v1.0.0-beta",
			want: true,
		},
		{
			name: "rc tag",
			tag:  "v1.0.0-rc",
			want: true,
		},
		{
			name: "alpha tag",
			tag:  "v1.0.0-alpha",
			want: true,
		},
		{
			name: "dev tag",
			tag:  "v1.0.0-dev",
			want: true,
		},
		{
			name: "preview tag",
			tag:  "v1.0.0-preview",
			want: true,
		},
		{
			name: "snapshot tag",
			tag:  "v1.0.0-snapshot",
			want: true,
		},
		{
			name: "stable tag",
			tag:  "v1.0.0",
			want: false,
		},
		{
			name: "uppercase beta",
			tag:  "v1.0.0-BETA",
			want: true,
		},
		{
			name: "mixed case",
			tag:  "v1.0.0-Beta",
			want: true,
		},
		{
			name: "beta with number",
			tag:  "v1.0.0-beta.1",
			want: true,
		},
		{
			name: "upstream beta with number",
			tag:  "v1.0.0-beta4",
			want: true,
		},
		{
			name: "rc with number",
			tag:  "v1.0.0-rc.1",
			want: true,
		},
		{
			name: "empty tag",
			tag:  "",
			want: false,
		},
		{
			name: "tag without prerelease",
			tag:  "release",
			want: false,
		},
		{
			name: "tag containing beta but not as prerelease",
			tag:  "betatest",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPreReleaseTag(tt.tag)
			require.Equal(t, tt.want, got, "isPreReleaseTag(%q) = %v, want %v", tt.tag, got, tt.want)
		})
	}
}

func TestIsBetaReleaseTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{
			name: "beta tag",
			tag:  "v1.0.0-beta1",
			want: true,
		},
		{
			name: "rc tag",
			tag:  "v1.0.0-rc.1",
			want: true,
		},
		{
			name: "stable tag",
			tag:  "v1.0.0",
			want: false,
		},
		{
			name: "dev prerelease is not beta channel",
			tag:  "v1.0.0-dev.1",
			want: false,
		},
		{
			name: "preview prerelease is not beta channel",
			tag:  "v1.0.0-preview.1",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBetaReleaseTag(tt.tag)
			require.Equal(t, tt.want, got, "isBetaReleaseTag(%q) = %v, want %v", tt.tag, got, tt.want)
		})
	}
}
