package biz

import (
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func astraContextPrice(multiplier int64) objects.ModelPrice {
	var items []objects.ModelPriceItem
	for _, rate := range []struct {
		code        objects.PriceItemCode
		short, long string
	}{
		{objects.PriceItemCodeUsage, "10", "20"},
		{objects.PriceItemCodeCompletion, "50", "75"},
		{objects.PriceItemCodePromptCachedToken, "1", "2"},
		{objects.PriceItemCodeWriteCachedTokens, "12.5", "25"},
	} {
		items = append(items, objects.ModelPriceItem{ItemCode: rate.code, Pricing: objects.Pricing{
			Mode: objects.PricingModeContext,
			UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
				{UpTo: lo.ToPtr(int64(272_000)), PricePerUnit: decimal.RequireFromString(rate.short).Mul(decimal.NewFromInt(multiplier))},
				{PricePerUnit: decimal.RequireFromString(rate.long).Mul(decimal.NewFromInt(multiplier))},
			}},
		}})
	}
	return objects.ModelPrice{Items: items}
}

func TestComputeUsageCost_ContextThreshold(t *testing.T) {
	price := astraContextPrice(1)
	for _, tt := range []struct {
		name                            string
		prompt, output, cached, written int64
		want                            string
	}{
		{"below boundary", 271_999, 100_000, 100_000, 50_000, "6.94499"},
		{"exact boundary is short", 272_000, 100_000, 100_000, 50_000, "6.945"},
		{"above boundary prices whole request", 272_001, 100_000, 100_000, 50_000, "11.39002"},
		{"large output does not trigger long context", 100, 500_000, 0, 0, "25.001"},
		{"cached input counts toward threshold", 300_000, 1_000, 290_000, 0, "0.855"},
		{"entire prompt cached", 300_000, 0, 300_000, 0, "0.6"},
		{"empty usage", 0, 0, 0, 0, "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usage := &llm.Usage{PromptTokens: tt.prompt, CompletionTokens: tt.output, TotalTokens: tt.prompt + tt.output,
				PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: tt.cached, WriteCachedTokens: tt.written}}
			for _, tier := range []struct {
				name       string
				multiplier int64
			}{{"default", 1}, {"fast", 2}, {"ultrafast", 6}} {
				items, total := ComputeUsageCostForServiceTier(usage, price, "gpt-6-astra", tier.name)
				require.True(t, decimal.RequireFromString(tt.want).Mul(decimal.NewFromInt(tier.multiplier)).Equal(total), "%s: %s", tier.name, total)
				sum := decimal.Zero
				for _, item := range items {
					sum = sum.Add(item.Subtotal)
					require.Len(t, item.TierBreakdown, 1)
					require.Equal(t, item.Quantity, item.TierBreakdown[0].Units)
					require.True(t, item.Subtotal.Equal(item.TierBreakdown[0].Subtotal))
				}
				require.True(t, sum.Equal(total))
			}
		})
	}
}

func TestComputeUsageCost_ContextExplicitTiersAndSchedule(t *testing.T) {
	usage := &llm.Usage{PromptTokens: 300_000, CompletionTokens: 100_000}
	price := astraContextPrice(1)
	price.ServiceTierPrices = []objects.ServiceTierPrice{
		{ServiceTier: "priority", Items: astraContextPrice(2).Items},
		{ServiceTier: "ultrafast", Items: astraContextPrice(6).Items},
	}
	for _, tt := range []struct{ tier, want string }{{"default", "13.5"}, {"priority", "27"}, {"ultrafast", "81"}} {
		_, total := ComputeUsageCostForServiceTier(usage, price, "gpt-6-astra", tt.tier)
		require.Equal(t, tt.want, total.String()) // no double application of tier/context multipliers
	}
	price.ServiceTierPrices = nil
	price.Schedule = &objects.PriceSchedule{Timezone: "UTC", Overrides: []objects.PriceOverride{{
		Name: "scheduled context price", Priority: 1,
		When:  objects.OverrideWhen{DailyTime: &objects.DailyTimeRange{Start: "00:00", End: "23:59"}},
		Items: astraContextPrice(2).Items,
	}}}
	_, total := ComputeUsageCostForServiceTier(usage, price, "gpt-6-astra", "ultrafast", time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	require.Equal(t, "162", total.String())
}

func TestComputeUsageCost_ContextCacheVariants(t *testing.T) {
	price := astraContextPrice(1)
	price.Items[3].PromptWriteCacheVariants = []objects.PromptWriteCacheVariant{
		{VariantCode: objects.PromptWriteCacheVariantCode5Min, Pricing: astraContextPrice(2).Items[3].Pricing},
		{VariantCode: objects.PromptWriteCacheVariantCode1Hour, Pricing: astraContextPrice(4).Items[3].Pricing},
	}
	usage := &llm.Usage{PromptTokens: 300_000, PromptTokensDetails: &llm.PromptTokensDetails{
		WriteCachedTokens: 300_000, WriteCached5MinTokens: 100_000, WriteCached1HourTokens: 200_000,
	}}
	_, total := ComputeUsageCost(usage, price, time.Now())
	require.Equal(t, "25", total.String()) // 100k * 50 + 200k * 100, not each variant's short tier
}
