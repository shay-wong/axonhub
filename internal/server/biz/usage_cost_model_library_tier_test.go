package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func TestUsageCost_ModelLibraryFallbackServiceTier(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	const modelID = "gpt-6-astra"
	createModelLibraryPriceEntry(t, ctx, client, modelID, model.StatusEnabled, objects.ModelCardCost{
		Input: 1, Output: 2,
	})
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenaiFake).
		SetName("model-library-tier").
		SetSupportedModels([]string{modelID}).
		SetDefaultTestModel(modelID).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{}).
		Save(ctx)
	require.NoError(t, err)

	channelService := NewChannelServiceForTest(client)
	built, err := channelService.GetChannel(ctx, ch.ID)
	require.NoError(t, err)
	channelService.SetEnabledChannelsForTest([]*Channel{built})
	service := NewUsageLogService(client, NewSystemService(SystemServiceParams{Ent: client}), channelService)

	for _, tt := range []struct {
		name, requested, applied, override, effective string
		wantCost                                      float64
	}{
		{"standard", "default", "default", "", "default", 0.0005},
		{"fast alias", "fast", "", "", "priority", 0.001},
		{"codex fast override", "priority", "default", "priority", "priority", 0.001},
		{"codex ultrafast override", "ultrafast", "default", "ultrafast", "ultrafast", 0.003},
		{"provider priority wins", "ultrafast", "priority", "ultrafast", "priority", 0.001},
		{"provider flex wins", "ultrafast", "flex", "ultrafast", "flex", 0.0005},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requestCtx := WithModelLibraryPriceMemo(ctx)
			usage := &llm.Usage{PromptTokens: 100, CompletionTokens: 200, TotalTokens: 300}
			service.InjectUsageCost(requestCtx, ch.ID, modelID, usage, tt.requested, tt.applied,
				tt.override, RequestPricingOverrideWhenAppliedDefault)
			log, err := service.CreateUsageLog(requestCtx, CreateUsageLogParams{
				RequestID: 1, ProjectID: 1, ChannelID: ch.ID, ActualModelID: modelID,
				Usage: usage, Source: "api", Format: "openai/responses",
				RequestedServiceTier: tt.requested, AppliedServiceTier: tt.applied,
				RequestPricingOverride: tt.override, RequestPricingOverridePolicy: RequestPricingOverrideWhenAppliedDefault,
			})
			require.NoError(t, err)
			require.NotNil(t, log.TotalCost)
			require.NotNil(t, usage.Cost)
			require.Equal(t, *log.TotalCost, *usage.Cost)
			require.Equal(t, tt.effective, log.ServiceTier)
			require.Equal(t, modelLibraryPriceReferenceID(modelID), log.CostPriceReferenceID)
			require.InDelta(t, tt.wantCost, *log.TotalCost, 1e-12)
		})
	}
}
