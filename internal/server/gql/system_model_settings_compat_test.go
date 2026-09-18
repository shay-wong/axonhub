package gql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func setupTestSystemModelSettingsResolver(t *testing.T) (*mutationResolver, context.Context, *ent.Client) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	systemService := biz.NewSystemService(biz.SystemServiceParams{Ent: client})

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	resolver := &Resolver{
		client:        client,
		systemService: systemService,
	}

	return &mutationResolver{resolver}, ctx, client
}

func TestUpdateSystemModelSettings_PreservesDeveloperSettingsWhenOmitted(t *testing.T) {
	mutationResolver, ctx, client := setupTestSystemModelSettingsResolver(t)
	defer client.Close()

	err := mutationResolver.systemService.SetModelSettings(ctx, biz.SystemModelSettings{
		CodexImageMainModel:               "gpt-custom",
		FallbackToChannelsOnModelNotFound: true,
		QueryAllChannelModels:             true,
		DeveloperSettings: []*biz.DeveloperModelSettings{
			{
				Developer: "openai",
				Associations: []*objects.ModelAssociation{
					{
						Type:         "channel_model",
						ChannelModel: &objects.ChannelModelAssociation{ChannelID: 10},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	ok, err := mutationResolver.UpdateSystemModelSettings(ctx, biz.SystemModelSettings{
		FallbackToChannelsOnModelNotFound: false,
		QueryAllChannelModels:             false,
	})
	require.NoError(t, err)
	require.True(t, ok)

	settings, err := mutationResolver.systemService.ModelSettings(ctx)
	require.NoError(t, err)
	require.Len(t, settings.DeveloperSettings, 1)
	require.Equal(t, "gpt-custom", settings.CodexImageMainModel)
	require.Equal(t, "openai", settings.DeveloperSettings[0].Developer)
	require.False(t, settings.FallbackToChannelsOnModelNotFound)
	require.False(t, settings.QueryAllChannelModels)
}

func TestUpdateSystemModelSettings_AllowsExplicitDeveloperSettingsClear(t *testing.T) {
	mutationResolver, ctx, client := setupTestSystemModelSettingsResolver(t)
	defer client.Close()

	err := mutationResolver.systemService.SetModelSettings(ctx, biz.SystemModelSettings{
		DeveloperSettings: []*biz.DeveloperModelSettings{
			{
				Developer: "openai",
				Associations: []*objects.ModelAssociation{
					{
						Type:         "channel_model",
						ChannelModel: &objects.ChannelModelAssociation{ChannelID: 10},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	emptyDeveloperSettings := []*biz.DeveloperModelSettings{}
	ok, err := mutationResolver.UpdateSystemModelSettings(ctx, biz.SystemModelSettings{
		DeveloperSettings: emptyDeveloperSettings,
	})
	require.NoError(t, err)
	require.True(t, ok)

	settings, err := mutationResolver.systemService.ModelSettings(ctx)
	require.NoError(t, err)
	require.Empty(t, settings.DeveloperSettings)
}

func TestUpdateSystemModelSettings_ImageMainModelRoundTrip(t *testing.T) {
	resolver, ctx, client := setupTestSystemModelSettingsResolver(t)
	defer client.Close()
	settings, err := resolver.systemService.ModelSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", settings.CodexImageMainModel)

	input, err := (&executionContext{}).unmarshalInputUpdateSystemModelSettingsInput(ctx, map[string]any{
		"codexImageMainModel": " gpt-custom ",
	})
	require.NoError(t, err)
	ok, err := resolver.UpdateSystemModelSettings(ctx, input)
	require.NoError(t, err)
	require.True(t, ok)
	settings, err = (&queryResolver{resolver.Resolver}).SystemModelSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-custom", settings.CodexImageMainModel)
}
