package biz

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func TestUsageCost_UltrafastRequiresKnownOrExplicitPrice(t *testing.T) {
	for _, tt := range []struct {
		name          string
		modelID       string
		cached        bool
		fastPriceOnly bool
		explicitPrice *float64
		priced        bool
		wantCost      float64
	}{
		{name: "unknown cached model", modelID: "custom-model", cached: true},
		{name: "unknown library model", modelID: "custom-model"},
		{name: "fast price does not imply ultrafast", modelID: "custom-model", cached: true, fastPriceOnly: true},
		{name: "known fast model without ultrafast rates", modelID: "gpt-5.6-sol", cached: true},
		{name: "future model has no inferred rates", modelID: "gpt-6-future", cached: true},
		{name: "explicit zero price", modelID: "custom-model", cached: true, explicitPrice: lo.ToPtr(0.0), priced: true},
		{name: "explicit custom price", modelID: "custom-model", cached: true, explicitPrice: lo.ToPtr(3.0), priced: true, wantCost: 3},
		{name: "known cached ultrafast model", modelID: "gpt-6-astra", cached: true, priced: true, wantCost: 6},
		{name: "known library ultrafast model", modelID: "gpt-6.1-sol", priced: true, wantCost: 6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			defer client.Close()
			ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			ch, err := client.Channel.Create().
				SetType(channel.TypeOpenaiFake).
				SetName("ultrafast-pricing").
				SetSupportedModels([]string{tt.modelID}).
				SetDefaultTestModel(tt.modelID).
				SetStatus(channel.StatusEnabled).
				SetCredentials(objects.ChannelCredentials{}).
				Save(ctx)
			require.NoError(t, err)
			if tt.cached {
				price := objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeUsagePerUnit, 1)}}
				if tt.explicitPrice != nil {
					price.ServiceTierPrices = []objects.ServiceTierPrice{{ServiceTier: "ultrafast", Items: []objects.ModelPriceItem{
						createPriceItem(objects.PricingModeUsagePerUnit, *tt.explicitPrice),
					}}}
				} else if tt.fastPriceOnly {
					price.ServiceTierPrices = []objects.ServiceTierPrice{{ServiceTier: "priority", Items: []objects.ModelPriceItem{
						createPriceItem(objects.PricingModeUsagePerUnit, 2),
					}}}
				}
				_, err = client.ChannelModelPrice.Create().SetChannelID(ch.ID).SetModelID(tt.modelID).
					SetPrice(price).SetReferenceID("explicit-channel-price").Save(ctx)
				require.NoError(t, err)
			} else {
				createModelLibraryPriceEntry(t, ctx, client, tt.modelID, model.StatusEnabled, objects.ModelCardCost{Input: 1})
			}

			channelService := NewChannelServiceForTest(client)
			built, err := channelService.GetChannel(ctx, ch.ID)
			require.NoError(t, err)
			channelService.preloadModelPrices(ctx, built)
			channelService.SetEnabledChannelsForTest([]*Channel{built})
			service := NewUsageLogService(client, NewSystemService(SystemServiceParams{Ent: client}), channelService)
			usage := &llm.Usage{PromptTokens: 1_000_000, TotalTokens: 1_000_000, Cost: lo.ToPtr(999.0)}
			service.InjectUsageCost(ctx, ch.ID, tt.modelID, usage, "ultrafast", "default", "ultrafast", RequestPricingOverrideWhenAppliedDefault)
			usageLog, err := service.CreateUsageLog(ctx, CreateUsageLogParams{
				RequestID: 1, ProjectID: 1, ChannelID: ch.ID, ActualModelID: tt.modelID, Usage: usage,
				Source: "api", Format: "openai/responses", RequestedServiceTier: "ultrafast", AppliedServiceTier: "default",
				RequestPricingOverride: "ultrafast", RequestPricingOverridePolicy: RequestPricingOverrideWhenAppliedDefault,
			})
			require.NoError(t, err)
			require.Equal(t, usage.TotalTokens, usageLog.TotalTokens)
			require.Equal(t, "ultrafast", usageLog.ServiceTier)
			if tt.priced {
				require.NotNil(t, usage.Cost)
				require.NotNil(t, usageLog.TotalCost)
				require.InDelta(t, tt.wantCost, *usage.Cost, 1e-12)
				require.Equal(t, *usage.Cost, *usageLog.TotalCost)
				require.NotEmpty(t, usageLog.CostItems)
				require.NotEmpty(t, usageLog.CostPriceReferenceID)
			} else {
				require.Nil(t, usage.Cost)
				require.Nil(t, usageLog.TotalCost)
				require.Empty(t, usageLog.CostItems)
				require.Empty(t, usageLog.CostPriceReferenceID)
				body, err := json.Marshal(usage)
				require.NoError(t, err)
				require.NotContains(t, string(body), `"cost":`)
			}
		})
	}
}
