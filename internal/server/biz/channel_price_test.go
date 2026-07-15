package biz

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/objects"
)

func TestChannelService_SaveChannelModelPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// Create a test channel
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Test Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key1"}).
		SetSupportedModels([]string{"gpt-4", "gpt-3.5-turbo"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	price1 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: loToDecimalPtr("0.01"),
				},
			},
		},
	}

	price2 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{
				ItemCode: objects.PriceItemCodeCompletion,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: loToDecimalPtr("0.02"),
				},
			},
		},
	}

	t.Run("batch create", func(t *testing.T) {
		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-4",
				Price:   price1,
			},
			{
				ModelID: "gpt-3.5-turbo",
				Price:   price2,
			},
		}

		results, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs)
		require.NoError(t, err)
		require.Len(t, results, 2)

		for _, res := range results {
			// Check if version is created
			version, err := client.ChannelModelPriceVersion.Query().
				Where(channelmodelpriceversion.ChannelModelPriceID(res.ID)).
				Only(ctx)
			require.NoError(t, err)
			require.Equal(t, channelmodelpriceversion.StatusActive, version.Status)
			require.Equal(t, res.ReferenceID, version.ReferenceID)
			require.Len(t, res.ReferenceID, 8)
		}
	})

	t.Run("batch update and archive old version", func(t *testing.T) {
		// First update gpt-4
		newPrice1 := price1
		newPrice1.Items[0].Pricing.UsagePerUnit = loToDecimalPtr("0.015")

		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-4",
				Price:   newPrice1,
			},
		}

		// Store old ref id
		oldPrice, err := client.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID("gpt-4"),
			).Only(ctx)
		require.NoError(t, err)

		oldRefID := oldPrice.ReferenceID

		// Wait a bit to ensure time difference
		time.Sleep(10 * time.Millisecond)

		results, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs)
		require.NoError(t, err)
		require.Len(t, results, 1)

		updatedPrice := results[0]
		require.NotEqual(t, oldRefID, updatedPrice.ReferenceID)
		require.Equal(t, newPrice1, updatedPrice.Price)

		// Check versions
		versions, err := client.ChannelModelPriceVersion.Query().
			Where(channelmodelpriceversion.ChannelModelPriceID(updatedPrice.ID)).
			Order(ent.Asc(channelmodelpriceversion.FieldEffectiveStartAt)).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, versions, 2)

		// Old version should be archived
		require.Equal(t, channelmodelpriceversion.StatusArchived, versions[0].Status)
		require.NotNil(t, versions[0].EffectiveEndAt)
		require.Equal(t, oldRefID, versions[0].ReferenceID)

		// New version should be active
		require.Equal(t, channelmodelpriceversion.StatusActive, versions[1].Status)
		require.Nil(t, versions[1].EffectiveEndAt)
		require.Equal(t, updatedPrice.ReferenceID, versions[1].ReferenceID)
	})

	t.Run("delete missing models", func(t *testing.T) {
		// Only send gpt-3.5-turbo, gpt-4 should be deleted
		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-3.5-turbo",
				Price:   price2,
			},
		}

		// Verify gpt-4 exists before delete
		exists, err := client.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID("gpt-4"),
			).Exist(ctx)
		require.NoError(t, err)
		require.True(t, exists)

		results, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs)
		require.NoError(t, err)
		require.Len(t, results, 1) // Only gpt-3.5-turbo remains (as skip/update)

		// Verify gpt-4 is deleted
		exists, err = client.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID("gpt-4"),
			).Exist(ctx)
		require.NoError(t, err)
		require.False(t, exists)

		// Verify gpt-4 versions are archived
		versions, err := client.ChannelModelPriceVersion.Query().
			Where(
				channelmodelpriceversion.ChannelID(ch.ID),
				channelmodelpriceversion.ModelID("gpt-4"),
			).All(ctx)
		require.NoError(t, err)

		for _, v := range versions {
			require.Equal(t, channelmodelpriceversion.StatusArchived, v.Status)
			require.NotNil(t, v.EffectiveEndAt)
		}
	})

	t.Run("duplicate model id should error", func(t *testing.T) {
		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-4",
				Price:   price1,
			},
			{
				ModelID: "gpt-4",
				Price:   price2,
			},
		}

		_, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs)
		require.Error(t, err)
		require.Contains(t, err.Error(), "duplicate model price input")
		require.Contains(t, err.Error(), "model_id=gpt-4")
	})
}

func loToDecimalPtr(s string) *decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return &d
}

func TestChannelService_DuplicateChannelCopiesModelPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	source, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Source Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key1"}).
		SetSupportedModels([]string{"gpt-4", "gpt-4o"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	price := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: loToDecimalPtr("0.01"),
				},
			},
		},
	}

	sourcePrices, err := svc.SaveChannelModelPrices(ctx, source.ID, []SaveChannelModelPriceInput{
		{ModelID: "gpt-4", Price: price},
	})
	require.NoError(t, err)
	require.Len(t, sourcePrices, 1)

	legacyPrice := price
	legacyPrice.ServiceTierPrices = []objects.ServiceTierPrice{
		{ServiceTier: "PRIORITY", Items: price.Items},
	}
	_, err = client.ChannelModelPrice.UpdateOne(sourcePrices[0]).
		SetPrice(legacyPrice).
		Save(ctx)
	require.NoError(t, err)
	expectedPrice := legacyPrice.CanonicalizedServiceTiers()

	duplicated, err := svc.DuplicateChannel(ctx, source.ID, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Name:             "Source Channel (1)",
		Credentials:      objects.ChannelCredentials{APIKey: "key2"},
		SupportedModels:  []string{"gpt-4", "gpt-4o"},
		DefaultTestModel: "gpt-4",
	})
	require.NoError(t, err)

	copiedPrices, err := client.ChannelModelPrice.Query().
		Where(channelmodelprice.ChannelID(duplicated.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, copiedPrices, 1)

	copiedPrice := copiedPrices[0]
	require.Equal(t, "gpt-4", copiedPrice.ModelID)
	require.Equal(t, expectedPrice, copiedPrice.Price)
	require.NotEqual(t, sourcePrices[0].ReferenceID, copiedPrice.ReferenceID)
	require.Len(t, copiedPrice.ReferenceID, 8)

	copiedVersion, err := client.ChannelModelPriceVersion.Query().
		Where(channelmodelpriceversion.ChannelModelPriceID(copiedPrice.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, duplicated.ID, copiedVersion.ChannelID)
	require.Equal(t, "gpt-4", copiedVersion.ModelID)
	require.Equal(t, expectedPrice, copiedVersion.Price)
	require.Equal(t, channelmodelpriceversion.StatusActive, copiedVersion.Status)
	require.Nil(t, copiedVersion.EffectiveEndAt)
	require.Equal(t, copiedPrice.ReferenceID, copiedVersion.ReferenceID)
}

func TestChannelService_DuplicateChannelPreservesOpenCodeGoQuotaAuthCookie(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	source, err := client.Channel.Create().
		SetType(channel.TypeOpencodeGo).
		SetName("OpenCode Go Source").
		SetBaseURL("https://opencode.ai/zen/go/v1").
		SetCredentials(objects.ChannelCredentials{}).
		SetSupportedModels([]string{"openai/gpt-5"}).
		SetDefaultTestModel("openai/gpt-5").
		SetSettings(&objects.ChannelSettings{
			ProviderQuota: &objects.ChannelProviderQuotaSettings{
				OpencodeGo: &objects.OpenCodeGoQuotaSettings{
					WorkspaceID: "wk_source",
					AuthCookie:  "source-cookie",
				},
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	duplicated, err := svc.DuplicateChannel(ctx, source.ID, ent.CreateChannelInput{
		Type:             channel.TypeOpencodeGo,
		BaseURL:          lo.ToPtr("https://opencode.ai/zen/go/v1"),
		Name:             "OpenCode Go Copy",
		Credentials:      objects.ChannelCredentials{},
		SupportedModels:  []string{"openai/gpt-5"},
		DefaultTestModel: "openai/gpt-5",
		Settings: &objects.ChannelSettings{
			ProviderQuota: &objects.ChannelProviderQuotaSettings{
				OpencodeGo: &objects.OpenCodeGoQuotaSettings{
					WorkspaceID: "wk_copy",
					AuthCookie:  "",
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, duplicated.Settings)
	require.NotNil(t, duplicated.Settings.ProviderQuota)
	require.NotNil(t, duplicated.Settings.ProviderQuota.OpencodeGo)
	require.Equal(t, "wk_copy", duplicated.Settings.ProviderQuota.OpencodeGo.WorkspaceID)
	require.Equal(t, "source-cookie", duplicated.Settings.ProviderQuota.OpencodeGo.AuthCookie)
	require.False(t, duplicated.Settings.ProviderQuota.OpencodeGo.ClearAuthCookie)
}

func TestChannelService_DuplicateChannelClearsOpenCodeGoQuotaAuthCookie(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	source, err := client.Channel.Create().
		SetType(channel.TypeOpencodeGo).
		SetName("OpenCode Go Source Clear").
		SetBaseURL("https://opencode.ai/zen/go/v1").
		SetCredentials(objects.ChannelCredentials{}).
		SetSupportedModels([]string{"openai/gpt-5"}).
		SetDefaultTestModel("openai/gpt-5").
		SetSettings(&objects.ChannelSettings{
			ProviderQuota: &objects.ChannelProviderQuotaSettings{
				OpencodeGo: &objects.OpenCodeGoQuotaSettings{
					WorkspaceID: "wk_source",
					AuthCookie:  "source-cookie",
				},
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	duplicated, err := svc.DuplicateChannel(ctx, source.ID, ent.CreateChannelInput{
		Type:             channel.TypeOpencodeGo,
		BaseURL:          lo.ToPtr("https://opencode.ai/zen/go/v1"),
		Name:             "OpenCode Go Copy Clear",
		Credentials:      objects.ChannelCredentials{},
		SupportedModels:  []string{"openai/gpt-5"},
		DefaultTestModel: "openai/gpt-5",
		Settings: &objects.ChannelSettings{
			ProviderQuota: &objects.ChannelProviderQuotaSettings{
				OpencodeGo: &objects.OpenCodeGoQuotaSettings{
					WorkspaceID:     "wk_copy",
					ClearAuthCookie: true,
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, duplicated.Settings)
	require.NotNil(t, duplicated.Settings.ProviderQuota)
	require.NotNil(t, duplicated.Settings.ProviderQuota.OpencodeGo)
	require.Equal(t, "wk_copy", duplicated.Settings.ProviderQuota.OpencodeGo.WorkspaceID)
	require.Empty(t, duplicated.Settings.ProviderQuota.OpencodeGo.AuthCookie)
	require.False(t, duplicated.Settings.ProviderQuota.OpencodeGo.ClearAuthCookie)
}

func TestCalculatePriceChanges(t *testing.T) {
	price1 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeUsage},
		},
	}
	price2 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeCompletion},
		},
	}

	existingPrices := []*ent.ChannelModelPrice{
		{
			ID:      1,
			ModelID: "gpt-4",
			Price:   price1,
		},
	}

	tests := []struct {
		name           string
		existingPrices []*ent.ChannelModelPrice
		inputs         []SaveChannelModelPriceInput
		want           []PriceChangeAction
	}{
		{
			name:           "create and update",
			existingPrices: existingPrices,
			inputs: []SaveChannelModelPriceInput{
				{
					ModelID: "gpt-4",
					Price:   price1,
				},
				{
					ModelID: "gpt-3.5-turbo",
					Price:   price2,
				},
			},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeSkip,
					ModelID:       "gpt-4",
					Price:         price1,
					ExistingPrice: existingPrices[0],
				},
				{
					Type:          ActionTypeCreate,
					ModelID:       "gpt-3.5-turbo",
					Price:         price2,
					ExistingPrice: nil,
				},
			},
		},
		{
			name:           "all create",
			existingPrices: []*ent.ChannelModelPrice{},
			inputs: []SaveChannelModelPriceInput{
				{
					ModelID: "gpt-4",
					Price:   price1,
				},
			},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeCreate,
					ModelID:       "gpt-4",
					Price:         price1,
					ExistingPrice: nil,
				},
			},
		},
		{
			name:           "all update",
			existingPrices: existingPrices,
			inputs: []SaveChannelModelPriceInput{
				{
					ModelID: "gpt-4",
					Price:   price2,
				},
			},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeUpdate,
					ModelID:       "gpt-4",
					Price:         price2,
					ExistingPrice: existingPrices[0],
				},
			},
		},
		{
			name:           "delete missing",
			existingPrices: existingPrices,
			inputs:         []SaveChannelModelPriceInput{},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeDelete,
					ModelID:       "gpt-4",
					ExistingPrice: existingPrices[0],
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculatePriceChanges(tt.existingPrices, tt.inputs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChannelService_SaveChannelModelPrices_CanonicalizesServiceTierPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Canonical tier channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-5-codex"}).
		SetDefaultTestModel("gpt-5-codex").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	price := objects.ModelPrice{
		Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("1")},
		}},
		ServiceTierPrices: []objects.ServiceTierPrice{{
			ServiceTier: " PRIORITY ",
			Items: []objects.ModelPriceItem{{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("2")},
			}},
		}},
	}

	results, err := svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{
		ModelID: "gpt-5-codex",
		Price:   price,
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "priority", results[0].Price.ServiceTierPrices[0].ServiceTier)
	require.Equal(t, " PRIORITY ", price.ServiceTierPrices[0].ServiceTier)

	version, err := client.ChannelModelPriceVersion.Query().
		Where(channelmodelpriceversion.ChannelModelPriceID(results[0].ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "priority", version.Price.ServiceTierPrices[0].ServiceTier)
}

func TestChannelService_SaveChannelModelPrices_PreservesOmittedServiceTierPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Service tier compatibility channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-5-codex"}).
		SetDefaultTestModel("gpt-5-codex").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	basePrice := objects.ModelPrice{
		Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("1")},
		}},
		ServiceTierPrices: []objects.ServiceTierPrice{{
			ServiceTier: "priority",
			Items: []objects.ModelPriceItem{{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("2")},
			}},
		}},
	}

	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{
		ModelID: "gpt-5-codex",
		Price:   basePrice,
	}})
	require.NoError(t, err)

	// Older GraphQL clients omit serviceTierPrices. Updating their base price must
	// preserve the tier-specific price that they cannot represent yet.
	omittedTierPrice := objects.ModelPrice{
		Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("3")},
		}},
	}
	results, err := svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{
		ModelID: "gpt-5-codex",
		Price:   omittedTierPrice,
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Price.ServiceTierPrices, 1)
	require.Equal(t, "priority", results[0].Price.ServiceTierPrices[0].ServiceTier)
	require.Equal(t, "2", results[0].Price.ServiceTierPrices[0].Items[0].Pricing.UsagePerUnit.String())

	// A non-nil empty slice remains an explicit clear from a current client.
	explicitClear := omittedTierPrice
	explicitClear.ServiceTierPrices = []objects.ServiceTierPrice{}
	results, err = svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{
		ModelID: "gpt-5-codex",
		Price:   explicitClear,
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, results[0].Price.ServiceTierPrices)
}
