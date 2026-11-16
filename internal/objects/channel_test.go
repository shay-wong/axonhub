package objects

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelSettingsIgnoresRemovedCodexStylePreset(t *testing.T) {
	var settings ChannelSettings
	require.NoError(t, json.Unmarshal([]byte(`{"transformOptions":{"codexStyleResponses":true,"forceArrayStop":true,"forceArrayInputs":true}}`), &settings))
	require.True(t, settings.TransformOptions.ForceArrayStop)
	require.True(t, settings.TransformOptions.ForceArrayInputs)
	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "codexStyleResponses")
}

func TestChannelCredentialsVertexKeysPreserveStructuredIdentity(t *testing.T) {
	credentials := ChannelCredentials{
		APIKeys:       []string{"stale-legacy"},
		APIKeyConfigs: []ChannelAPIKeyConfig{{Key: " primary ", Name: " Primary ", Weight: 250}},
		VertexAPIKeys: []VertexAPIKey{
			{APIKey: " primary ", ProjectID: "project-a", Region: "global"},
			{APIKey: "backup", ProjectID: "project-b", Region: "us-central1"},
		},
	}
	require.Equal(t, []ChannelAPIKeyConfig{
		{Key: "primary", Name: "Primary", Weight: 250},
		{Key: "backup", Weight: 100},
	}, credentials.GetAllAPIKeyConfigs())
	require.Equal(t, []string{"backup"}, credentials.GetEnabledAPIKeysAt([]DisabledAPIKey{{Key: "primary"}}, time.Now()))
	binding, ok := credentials.VertexAPIKey("primary")
	require.True(t, ok)
	require.Equal(t, "project-a", binding.ProjectID)
}

func TestChannelCredentialsGetAllAPIKeyConfigsPreservesNames(t *testing.T) {
	credentials := ChannelCredentials{
		APIKeyConfigs: []ChannelAPIKeyConfig{
			{Key: " key-primary ", Name: " Primary Account ", Weight: 200},
			{Key: "key-backup", Name: "Backup Key"},
		},
	}

	configs := credentials.GetAllAPIKeyConfigs()

	require.Equal(t, []ChannelAPIKeyConfig{
		{Key: "key-primary", Name: "Primary Account", Weight: 200},
		{Key: "key-backup", Name: "Backup Key", Weight: 100},
	}, configs)
}

func TestChannelCredentialsNormalizedDisableIdentity(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Minute)
	credentials := ChannelCredentials{
		APIKeyConfigs: []ChannelAPIKeyConfig{
			{Key: " primary ", Name: "Primary", Weight: 200},
			{Key: "backup", Name: "Backup", Weight: 100},
		},
	}
	disabled := []DisabledAPIKey{
		{Key: "  primary  "},
		{Key: " backup ", ExpiresAt: &expired},
	}

	require.Equal(t, []string{"backup"}, credentials.GetEnabledAPIKeysAt(disabled, now))
	require.Equal(t, []string{"backup"}, credentials.GetEnabledCredentialRefsAt(disabled, now))
	require.Equal(t, []ChannelAPIKeyConfig{{Key: "backup", Name: "Backup", Weight: 100}},
		credentials.GetEnabledAPIKeyConfigsAt(disabled, now))
	require.Equal(t, "  primary  ", disabled[0].Key, "matching must not rewrite stored records")
}
