package gql

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestChannelImageMainModelSettingsRoundTrip(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	settings, err := (&executionContext{}).unmarshalInputChannelSettingsInput(ctx, map[string]any{
		"codexImageMainModel": "gpt-5.6-sol",
	})
	require.NoError(t, err)
	resolver := &mutationResolver{&Resolver{channelService: biz.NewChannelServiceForTest(client)}}
	created, err := resolver.CreateChannel(ctx, ent.CreateChannelInput{
		Type: channel.TypeCodex, Name: "image model", BaseURL: lo.ToPtr("https://relay.example/v1"),
		Credentials:     objects.ChannelCredentials{APIKey: "test-key"},
		SupportedModels: []string{"gpt-image-2"}, DefaultTestModel: "gpt-image-2", Settings: &settings,
	})
	require.NoError(t, err)
	read, err := client.Channel.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", read.Settings.CodexImageMainModel)
	_, err = resolver.UpdateChannel(ctx, objects.GUID{Type: "Channel", ID: created.ID}, ent.UpdateChannelInput{
		Settings: &objects.ChannelSettings{CodexImageMainModel: "gpt-6-astra"},
	})
	require.NoError(t, err)
	read, err = client.Channel.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", read.Settings.CodexImageMainModel)
}
