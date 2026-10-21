package objects

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
