package objects

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
