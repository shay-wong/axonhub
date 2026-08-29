package objects

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelPrice_Equals(t *testing.T) {
	d1 := decimal.NewFromFloat(0.01)
	d2 := decimal.NewFromFloat(0.02)
	upTo1000 := int64(1000)

	tests := []struct {
		name     string
		p1       ModelPrice
		p2       ModelPrice
		expected bool
	}{
		{
			name: "Equal simple",
			p1: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &d1,
						},
					},
				},
			},
			p2: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &d1,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Not equal mode",
			p1: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode: PricingModeFlatFee,
						},
					},
				},
			},
			p2: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode: PricingModeUsagePerUnit,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Not equal usage per unit",
			p1: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &d1,
						},
					},
				},
			},
			p2: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &d2,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Equal tiered",
			p1: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode: PricingModeTiered,
							UsageTiered: &TieredPricing{
								Tiers: []PriceTier{
									{UpTo: &upTo1000, PricePerUnit: d1},
									{UpTo: nil, PricePerUnit: d2},
								},
							},
						},
					},
				},
			},
			p2: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode: PricingModeTiered,
							UsageTiered: &TieredPricing{
								Tiers: []PriceTier{
									{UpTo: &upTo1000, PricePerUnit: d1},
									{UpTo: nil, PricePerUnit: d2},
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Not equal tiered price",
			p1: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode: PricingModeTiered,
							UsageTiered: &TieredPricing{
								Tiers: []PriceTier{
									{UpTo: &upTo1000, PricePerUnit: d1},
								},
							},
						},
					},
				},
			},
			p2: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode: PricingModeTiered,
							UsageTiered: &TieredPricing{
								Tiers: []PriceTier{
									{UpTo: &upTo1000, PricePerUnit: d2},
								},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Equal with variants",
			p1: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeWriteCachedTokens,
						Pricing: Pricing{
							Mode: PricingModeFlatFee,
						},
						PromptWriteCacheVariants: []PromptWriteCacheVariant{
							{
								VariantCode: PromptWriteCacheVariantCode5Min,
								Pricing: Pricing{
									Mode:         PricingModeUsagePerUnit,
									UsagePerUnit: &d1,
								},
							},
						},
					},
				},
			},
			p2: ModelPrice{
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeWriteCachedTokens,
						Pricing: Pricing{
							Mode: PricingModeFlatFee,
						},
						PromptWriteCacheVariants: []PromptWriteCacheVariant{
							{
								VariantCode: PromptWriteCacheVariantCode5Min,
								Pricing: Pricing{
									Mode:         PricingModeUsagePerUnit,
									UsagePerUnit: &d1,
								},
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.p1.Equals(tt.p2))
			assert.Equal(t, tt.expected, tt.p2.Equals(tt.p1))
		})
	}
}

func TestModelPrice_EqualsVolume(t *testing.T) {
	d1 := decimal.NewFromFloat(0.01)
	d2 := decimal.NewFromFloat(0.02)
	upTo1000 := int64(1000)

	p1 := ModelPrice{
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode: PricingModeVolume,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: &upTo1000, PricePerUnit: d1},
							{UpTo: nil, PricePerUnit: d2},
						},
					},
				},
			},
		},
	}

	p2 := ModelPrice{
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode: PricingModeVolume,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: &upTo1000, PricePerUnit: d1},
							{UpTo: nil, PricePerUnit: d2},
						},
					},
				},
			},
		},
	}

	assert.True(t, p1.Equals(p2))

	// Different mode should not be equal
	p3 := ModelPrice{
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode: PricingModeVolume,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: &upTo1000, PricePerUnit: d1},
							{UpTo: nil, PricePerUnit: d2},
						},
					},
				},
			},
		},
	}

	p4 := ModelPrice{
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode: PricingModeTiered,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: &upTo1000, PricePerUnit: d1},
							{UpTo: nil, PricePerUnit: d2},
						},
					},
				},
			},
		},
	}

	assert.False(t, p3.Equals(p4))
}

func TestModelPrice_Validate(t *testing.T) {
	t.Run("flat_fee requires flatFee", func(t *testing.T) {
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeUsage,
					Pricing:  Pricing{Mode: PricingModeFlatFee},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "flatFee is required")
	})

	t.Run("usage_per_unit requires usagePerUnit", func(t *testing.T) {
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeUsage,
					Pricing:  Pricing{Mode: PricingModeUsagePerUnit},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "usagePerUnit is required")
	})

	t.Run("tiered requires last upTo null and others non-null", func(t *testing.T) {
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeUsage,
					Pricing: Pricing{
						Mode: PricingModeTiered,
						UsageTiered: &TieredPricing{
							Tiers: []PriceTier{
								{UpTo: nil, PricePerUnit: decimal.NewFromFloat(0.01)},
								{UpTo: nil, PricePerUnit: decimal.NewFromFloat(0.02)},
							},
						},
					},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tiers[0].upTo is required")
	})

	t.Run("tiered last upTo must be null", func(t *testing.T) {
		upTo1000 := int64(1000)
		upTo2000 := int64(2000)
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeUsage,
					Pricing: Pricing{
						Mode: PricingModeTiered,
						UsageTiered: &TieredPricing{
							Tiers: []PriceTier{
								{UpTo: &upTo1000, PricePerUnit: decimal.NewFromFloat(0.01)},
								{UpTo: &upTo2000, PricePerUnit: decimal.NewFromFloat(0.02)},
							},
						},
					},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tiers[1].upTo must be null")
	})

	t.Run("volume requires usageTiered", func(t *testing.T) {
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeUsage,
					Pricing:  Pricing{Mode: PricingModeVolume},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "usageTiered is required")
	})

	t.Run("volume validates tiers same as tiered", func(t *testing.T) {
		upTo1000 := int64(1000)
		upTo2000 := int64(2000)
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeUsage,
					Pricing: Pricing{
						Mode: PricingModeVolume,
						UsageTiered: &TieredPricing{
							Tiers: []PriceTier{
								{UpTo: &upTo1000, PricePerUnit: decimal.NewFromFloat(0.01)},
								{UpTo: &upTo2000, PricePerUnit: decimal.NewFromFloat(0.02)},
							},
						},
					},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tiers[1].upTo must be null")
	})

	t.Run("variant pricing is also validated", func(t *testing.T) {
		d := decimal.NewFromFloat(0.01)
		mp := ModelPrice{
			Items: []ModelPriceItem{
				{
					ItemCode: PriceItemCodeWriteCachedTokens,
					Pricing:  Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: &d},
					PromptWriteCacheVariants: []PromptWriteCacheVariant{
						{
							VariantCode: PromptWriteCacheVariantCode5Min,
							Pricing:     Pricing{Mode: PricingModeUsagePerUnit},
						},
					},
				},
			},
		}

		err := mp.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "promptWriteCacheVariants[0]")
		assert.Contains(t, err.Error(), "usagePerUnit is required")
	})
}

func TestModelPrice_ValidateTierBounds(t *testing.T) {
	pricingForBounds := func(mode PricingMode, bounds ...int64) Pricing {
		tiers := make([]PriceTier, 0, len(bounds)+1)
		for _, bound := range bounds {
			bound := bound
			tiers = append(tiers, PriceTier{UpTo: &bound, PricePerUnit: decimal.NewFromInt(1)})
		}
		tiers = append(tiers, PriceTier{PricePerUnit: decimal.NewFromInt(2)})

		return Pricing{
			Mode: mode,
			UsageTiered: &TieredPricing{
				Tiers: tiers,
			},
		}
	}

	validBasePrice := decimal.NewFromInt(1)
	tests := []struct {
		name            string
		mode            PricingMode
		bounds          []int64
		serviceTierPath bool
		wantError       string
	}{
		{
			name:      "usage tiered accepts positive increasing bounds",
			mode:      PricingModeTiered,
			bounds:    []int64{100, 200},
			wantError: "",
		},
		{
			name:      "usage volume accepts positive increasing bounds",
			mode:      PricingModeVolume,
			bounds:    []int64{100, 200},
			wantError: "",
		},
		{
			name:      "usage tiered rejects decreasing bounds",
			mode:      PricingModeTiered,
			bounds:    []int64{200, 100},
			wantError: "tiers[1].upTo must be greater than tiers[0].upTo",
		},
		{
			name:      "usage volume rejects duplicate bounds",
			mode:      PricingModeVolume,
			bounds:    []int64{100, 100},
			wantError: "tiers[1].upTo must be greater than tiers[0].upTo",
		},
		{
			name:            "service tier usage tiered rejects zero bound",
			mode:            PricingModeTiered,
			bounds:          []int64{0},
			serviceTierPath: true,
			wantError:       "serviceTierPrices[0].items[0]: pricing: tiers[0].upTo must be greater than 0",
		},
		{
			name:            "service tier usage volume rejects negative bound",
			mode:            PricingModeVolume,
			bounds:          []int64{-1},
			serviceTierPath: true,
			wantError:       "serviceTierPrices[0].items[0]: pricing: tiers[0].upTo must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := ModelPriceItem{
				ItemCode: PriceItemCodeUsage,
				Pricing:  pricingForBounds(tt.mode, tt.bounds...),
			}
			price := ModelPrice{Items: []ModelPriceItem{item}}
			if tt.serviceTierPath {
				price = ModelPrice{
					Items: []ModelPriceItem{
						{
							ItemCode: PriceItemCodeUsage,
							Pricing: Pricing{
								Mode:         PricingModeUsagePerUnit,
								UsagePerUnit: &validBasePrice,
							},
						},
					},
					ServiceTierPrices: []ServiceTierPrice{
						{ServiceTier: "priority", Items: []ModelPriceItem{item}},
					},
				}
			}

			err := price.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestModelPrice_ItemsForServiceTier(t *testing.T) {
	baseInput := decimal.NewFromInt(5)
	baseCompletion := decimal.NewFromInt(30)
	fastInput := decimal.NewFromInt(10)
	fastCache := decimal.NewFromInt(1)
	ultrafastInput := decimal.NewFromInt(20)

	price := ModelPrice{
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode:         PricingModeUsagePerUnit,
					UsagePerUnit: &baseInput,
				},
			},
			{
				ItemCode: PriceItemCodeCompletion,
				Pricing: Pricing{
					Mode:         PricingModeUsagePerUnit,
					UsagePerUnit: &baseCompletion,
				},
			},
		},
		ServiceTierPrices: []ServiceTierPrice{
			{
				ServiceTier: "priority",
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &fastInput,
						},
					},
					{
						ItemCode: PriceItemCodePromptCachedToken,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &fastCache,
						},
					},
				},
			},
			{
				ServiceTier: "ultrafast",
				Items: []ModelPriceItem{
					{
						ItemCode: PriceItemCodeUsage,
						Pricing: Pricing{
							Mode:         PricingModeUsagePerUnit,
							UsagePerUnit: &ultrafastInput,
						},
					},
				},
			},
		},
	}

	priorityItems := price.ItemsForServiceTier("priority")
	require.Equal(t, priorityItems, price.ItemsForServiceTier(" PRIORITY "))
	require.Len(t, priorityItems, 3)
	require.Equal(t, PriceItemCodeUsage, priorityItems[0].ItemCode)
	require.True(t, fastInput.Equal(*priorityItems[0].Pricing.UsagePerUnit))
	require.Equal(t, PriceItemCodeCompletion, priorityItems[1].ItemCode)
	require.True(t, baseCompletion.Equal(*priorityItems[1].Pricing.UsagePerUnit))
	require.Equal(t, PriceItemCodePromptCachedToken, priorityItems[2].ItemCode)
	require.True(t, fastCache.Equal(*priorityItems[2].Pricing.UsagePerUnit))
	ultrafastItems := price.ItemsForServiceTier("ultrafast")
	require.True(t, ultrafastInput.Equal(*ultrafastItems[0].Pricing.UsagePerUnit))
	require.Equal(t, price.Items, price.ItemsForServiceTier(""))
	require.Equal(t, price.Items, price.ItemsForServiceTier("default"))
	require.Equal(t, price.Items, price.ItemsForServiceTier("unknown"))

	canonicalized := price
	canonicalized.ServiceTierPrices[0].ServiceTier = " PRIORITY "
	normalized := canonicalized.CanonicalizedServiceTiers()
	require.Equal(t, "priority", normalized.ServiceTierPrices[0].ServiceTier)
	require.Equal(t, " PRIORITY ", canonicalized.ServiceTierPrices[0].ServiceTier)
}

func TestModelPrice_ServiceTierPricesValidationAndEquality(t *testing.T) {
	baseInput := decimal.NewFromInt(5)
	fastInput := decimal.NewFromInt(10)
	baseItems := []ModelPriceItem{
		{
			ItemCode: PriceItemCodeUsage,
			Pricing: Pricing{
				Mode:         PricingModeUsagePerUnit,
				UsagePerUnit: &baseInput,
			},
		},
	}
	fastItems := []ModelPriceItem{
		{
			ItemCode: PriceItemCodeUsage,
			Pricing: Pricing{
				Mode:         PricingModeUsagePerUnit,
				UsagePerUnit: &fastInput,
			},
		},
	}

	price := ModelPrice{
		Items: baseItems,
		ServiceTierPrices: []ServiceTierPrice{
			{ServiceTier: "priority", Items: fastItems},
		},
	}
	require.NoError(t, price.Validate())
	require.True(t, price.Equals(price))

	different := price
	different.ServiceTierPrices = []ServiceTierPrice{
		{ServiceTier: "priority", Items: baseItems},
	}
	require.False(t, price.Equals(different))

	missingTier := price
	missingTier.ServiceTierPrices = []ServiceTierPrice{
		{ServiceTier: "", Items: fastItems},
	}
	err := missingTier.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "serviceTier is required")

	duplicateTier := price
	duplicateTier.ServiceTierPrices = []ServiceTierPrice{
		{ServiceTier: "priority", Items: fastItems},
		{ServiceTier: "Priority", Items: fastItems},
	}
	err = duplicateTier.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate serviceTier")

	paddedTier := price
	paddedTier.ServiceTierPrices = []ServiceTierPrice{
		{ServiceTier: " priority ", Items: fastItems},
	}
	err = paddedTier.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "surrounding whitespace")

	duplicateTierItem := price
	duplicateTierItem.ServiceTierPrices = []ServiceTierPrice{
		{ServiceTier: "priority", Items: append(append([]ModelPriceItem(nil), fastItems...), fastItems[0])},
	}
	err = duplicateTierItem.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate itemCode")
}
