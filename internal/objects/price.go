package objects

import (
	"fmt"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/shopspring/decimal"
)

type PricingMode string

const (
	// PricingModeFlatFee means the request is charged a fixed fee.
	PricingModeFlatFee PricingMode = "flat_fee"

	// PricingModeUsagePerUnit means the request is charged a fee bases on the token usage.
	// e.g. $0.01 per token, if the usage is 1,500 then the fee is $0.01 x 1,500 = $15.00.
	PricingModeUsagePerUnit PricingMode = "usage_per_unit"

	// PricingModeTiered means the request is charged a fee based on the token usage tiers.
	// Each tier segment is billed separately at its own rate.
	// e.g. tiers are [{upTo: 1000, pricePerUnit: $0.01}, {upTo: nil, pricePerUnit: $0.02}],
	// if usage is 1,500 then the fee is (1000/1e6)*$0.01 + (500/1e6)*$0.02.
	PricingModeTiered PricingMode = "usage_tiered"

	// PricingModeVolume means the request is charged a fee based on the token usage volume tiers.
	// The tier matched by the total token count determines the unit price for ALL tokens.
	// e.g. tiers are [{upTo: 1000, pricePerUnit: $0.01}, {upTo: nil, pricePerUnit: $0.02}],
	// if usage is 1,500 then all tokens are billed at $0.02 => (1500/1e6)*$0.02.
	PricingModeVolume PricingMode = "usage_volume"
)

type Pricing struct {
	Mode PricingMode `json:"mode"`

	// FlatFee is the fixed fee for the pricing.
	FlatFee *decimal.Decimal `json:"flatFee,omitempty"`

	// UsagePerUnit is the price per token for the pricing.
	UsagePerUnit *decimal.Decimal `json:"usagePerUnit,omitempty"`

	// UsageTiered is the tiered pricing for the pricing.
	// Used by both UsageTiered and UsageVolume modes — they share the same data structure
	// but differ in calculation logic.
	UsageTiered *TieredPricing `json:"usageTiered,omitempty"`
}

func (p *Pricing) Equals(other *Pricing) bool {
	if p == nil || other == nil {
		return p == other
	}

	if p.Mode != other.Mode {
		return false
	}

	switch p.Mode {
	case PricingModeFlatFee:
		if (p.FlatFee == nil) != (other.FlatFee == nil) {
			return false
		}

		if p.FlatFee != nil && !p.FlatFee.Equal(*other.FlatFee) {
			return false
		}
	case PricingModeUsagePerUnit:
		if (p.UsagePerUnit == nil) != (other.UsagePerUnit == nil) {
			return false
		}

		if p.UsagePerUnit != nil && !p.UsagePerUnit.Equal(*other.UsagePerUnit) {
			return false
		}
	case PricingModeTiered, PricingModeVolume:
		return p.UsageTiered.Equals(other.UsageTiered)
	}

	return true
}

func (p *Pricing) Validate() error {
	if p == nil {
		return fmt.Errorf("pricing is nil")
	}

	switch p.Mode {
	case PricingModeFlatFee:
		if p.FlatFee == nil {
			return fmt.Errorf("flatFee is required")
		}
	case PricingModeUsagePerUnit:
		if p.UsagePerUnit == nil {
			return fmt.Errorf("usagePerUnit is required")
		}
	case PricingModeTiered, PricingModeVolume:
		if p.UsageTiered == nil {
			return fmt.Errorf("usageTiered is required")
		}

		if err := p.UsageTiered.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown pricing mode: %s", p.Mode)
	}

	return nil
}

func (p *TieredPricing) Validate() error {
	if p == nil {
		return fmt.Errorf("usageTiered is nil")
	}

	if len(p.Tiers) == 0 {
		return fmt.Errorf("tiers is required")
	}

	lastIdx := len(p.Tiers) - 1
	var previousUpTo int64
	for i := range p.Tiers {
		tier := p.Tiers[i]
		if i == lastIdx {
			if tier.UpTo != nil {
				return fmt.Errorf("tiers[%d].upTo must be null", i)
			}

			continue
		}

		if tier.UpTo == nil {
			return fmt.Errorf("tiers[%d].upTo is required", i)
		}

		if *tier.UpTo <= 0 {
			return fmt.Errorf("tiers[%d].upTo must be greater than 0", i)
		}

		if i > 0 && *tier.UpTo <= previousUpTo {
			return fmt.Errorf("tiers[%d].upTo must be greater than tiers[%d].upTo", i, i-1)
		}

		previousUpTo = *tier.UpTo
	}

	return nil
}

func (p *PromptWriteCacheVariant) Validate() error {
	if p == nil {
		return fmt.Errorf("promptWriteCacheVariant is nil")
	}

	if err := p.Pricing.Validate(); err != nil {
		return fmt.Errorf("pricing: %w", err)
	}

	return nil
}

func (i *ModelPriceItem) Validate() error {
	if i == nil {
		return fmt.Errorf("modelPriceItem is nil")
	}

	if err := i.Pricing.Validate(); err != nil {
		return fmt.Errorf("pricing: %w", err)
	}

	variantCodes := make(map[PromptWriteCacheVariantCode]struct{}, len(i.PromptWriteCacheVariants))
	for idx := range i.PromptWriteCacheVariants {
		variant := &i.PromptWriteCacheVariants[idx]
		if _, ok := variantCodes[variant.VariantCode]; ok {
			return fmt.Errorf("promptWriteCacheVariants[%d]: duplicate variantCode %q", idx, variant.VariantCode)
		}
		variantCodes[variant.VariantCode] = struct{}{}

		if err := variant.Validate(); err != nil {
			return fmt.Errorf("promptWriteCacheVariants[%d]: %w", idx, err)
		}
	}

	return nil
}

func (p *ModelPrice) Validate() error {
	if p == nil {
		return fmt.Errorf("modelPrice is nil")
	}

	itemCodes := make(map[PriceItemCode]struct{}, len(p.Items))
	for idx := range p.Items {
		if _, ok := itemCodes[p.Items[idx].ItemCode]; ok {
			return fmt.Errorf("items[%d]: duplicate itemCode %q", idx, p.Items[idx].ItemCode)
		}
		itemCodes[p.Items[idx].ItemCode] = struct{}{}

		if err := p.Items[idx].Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", idx, err)
		}
	}

	serviceTiers := make(map[string]struct{}, len(p.ServiceTierPrices))
	for idx := range p.ServiceTierPrices {
		serviceTierPrice := &p.ServiceTierPrices[idx]
		serviceTier := llm.CanonicalServiceTier(serviceTierPrice.ServiceTier)
		if serviceTier == "" {
			return fmt.Errorf("serviceTierPrices[%d].serviceTier is required", idx)
		}
		if strings.TrimSpace(serviceTierPrice.ServiceTier) != serviceTierPrice.ServiceTier {
			return fmt.Errorf("serviceTierPrices[%d].serviceTier must not contain surrounding whitespace", idx)
		}

		if _, ok := serviceTiers[serviceTier]; ok {
			return fmt.Errorf("serviceTierPrices[%d]: duplicate serviceTier %q", idx, serviceTierPrice.ServiceTier)
		}
		serviceTiers[serviceTier] = struct{}{}

		tierItemCodes := make(map[PriceItemCode]struct{}, len(serviceTierPrice.Items))
		for itemIdx := range serviceTierPrice.Items {
			if _, ok := tierItemCodes[serviceTierPrice.Items[itemIdx].ItemCode]; ok {
				return fmt.Errorf("serviceTierPrices[%d].items[%d]: duplicate itemCode %q", idx, itemIdx, serviceTierPrice.Items[itemIdx].ItemCode)
			}
			tierItemCodes[serviceTierPrice.Items[itemIdx].ItemCode] = struct{}{}

			if err := serviceTierPrice.Items[itemIdx].Validate(); err != nil {
				return fmt.Errorf("serviceTierPrices[%d].items[%d]: %w", idx, itemIdx, err)
			}
		}
	}

	return nil
}

type TieredPricing struct {
	Tiers []PriceTier `json:"tiers"`
}

func (p *TieredPricing) Equals(other *TieredPricing) bool {
	if p == nil || other == nil {
		return p == other
	}

	if len(p.Tiers) != len(other.Tiers) {
		return false
	}

	for i := range p.Tiers {
		if !p.Tiers[i].Equals(&other.Tiers[i]) {
			return false
		}
	}

	return true
}

// PriceTier is the price tier for the tiered pricing.
type PriceTier struct {
	// UpTo is the upper bound of the token usage for the price tier.
	// If the upper bound is nil, it means no upper bound, it must be the last price tier.
	UpTo *int64 `json:"upTo,omitempty"`

	// PricePerUnit is the price per token for the price tier.
	PricePerUnit decimal.Decimal `json:"pricePerUnit"`
}

func (p *PriceTier) Equals(other *PriceTier) bool {
	if p == nil || other == nil {
		return p == other
	}

	if (p.UpTo == nil) != (other.UpTo == nil) {
		return false
	}

	if p.UpTo != nil && *p.UpTo != *other.UpTo {
		return false
	}

	return p.PricePerUnit.Equal(other.PricePerUnit)
}

type PriceItemCode string

const (
	// PriceItemCodeUsage is the price item code for the token usage.
	PriceItemCodeUsage PriceItemCode = "prompt_tokens"

	// PriceItemCodeCompletion is the price item code for the token completion.
	PriceItemCodeCompletion PriceItemCode = "completion_tokens"

	// PriceItemCodePromptCachedToken is the price item code for the cached token usage.
	PriceItemCodePromptCachedToken PriceItemCode = "prompt_cached_tokens"

	// PriceItemCodeWriteCachedTokens is the price item code for the cached token write.
	//nolint:gosec // not token.
	PriceItemCodeWriteCachedTokens PriceItemCode = "prompt_write_cached_tokens"
)

type PromptWriteCacheVariantCode string

const (
	// PromptWriteCacheVariantCode5Min is the variant code for cached token write in 5 minutes.
	PromptWriteCacheVariantCode5Min PromptWriteCacheVariantCode = "five_min"

	// PromptWriteCacheVariantCode1Hour is the variant code for cached token write in 1 hour.
	PromptWriteCacheVariantCode1Hour PromptWriteCacheVariantCode = "one_hour"
)

// PromptWriteCacheVariant is the variant for cached token write.
type PromptWriteCacheVariant struct {
	// VariantCode is the code of the variant.
	VariantCode PromptWriteCacheVariantCode `json:"variantCode"`

	// Pricing is the pricing for the variant.
	Pricing Pricing `json:"pricing"`
}

func (p *PromptWriteCacheVariant) Equals(other *PromptWriteCacheVariant) bool {
	if p == nil || other == nil {
		return p == other
	}

	if p.VariantCode != other.VariantCode {
		return false
	}

	return p.Pricing.Equals(&other.Pricing)
}

// FindPromptWriteCacheVariantPricing finds the variant pricing for the item prompt write cached tokens.
// If the variant pricing is not found, it will return the item pricing.
func (i *ModelPriceItem) FindPromptWriteCacheVariantPricing(variantCode PromptWriteCacheVariantCode) Pricing {
	for _, v := range i.PromptWriteCacheVariants {
		if v.VariantCode == variantCode {
			return v.Pricing
		}
	}

	return i.Pricing
}

type ModelPriceItem struct {
	// ItemCode is the code of the item.
	ItemCode PriceItemCode `json:"itemCode"`

	// Pricing is the pricing for the item.
	Pricing Pricing `json:"pricing"`

	// PromptWriteCacheVariants is the list of variants for the item prompt write cached tokens.
	// If the variants present, it will find the variant price first, if not hit, it will use the item pricing.
	PromptWriteCacheVariants []PromptWriteCacheVariant `json:"promptWriteCacheVariants,omitempty"`
}

// ServiceTierPrice is an alternative set of price items for a provider service tier.
type ServiceTierPrice struct {
	ServiceTier string           `json:"serviceTier"`
	Items       []ModelPriceItem `json:"items"`
}

func (p *ServiceTierPrice) Equals(other *ServiceTierPrice) bool {
	if p == nil || other == nil {
		return p == other
	}

	if p.ServiceTier != other.ServiceTier || len(p.Items) != len(other.Items) {
		return false
	}

	for idx := range p.Items {
		if !p.Items[idx].Equals(&other.Items[idx]) {
			return false
		}
	}

	return true
}

func (i *ModelPriceItem) Equals(other *ModelPriceItem) bool {
	if i == nil || other == nil {
		return i == other
	}

	if i.ItemCode != other.ItemCode {
		return false
	}

	if !i.Pricing.Equals(&other.Pricing) {
		return false
	}

	if len(i.PromptWriteCacheVariants) != len(other.PromptWriteCacheVariants) {
		return false
	}

	for idx := range i.PromptWriteCacheVariants {
		if !i.PromptWriteCacheVariants[idx].Equals(&other.PromptWriteCacheVariants[idx]) {
			return false
		}
	}

	return true
}

// ModelPrice is the price for the thing.
type ModelPrice struct {
	// Items is the list of price items for the price.
	Items []ModelPriceItem `json:"items"`

	// ServiceTierPrices contains alternative price items keyed by provider service tier.
	ServiceTierPrices []ServiceTierPrice `json:"serviceTierPrices,omitempty"`
}

// CanonicalizedServiceTiers returns a copy whose service-tier price keys use the
// canonical storage and matching representation.
func (p ModelPrice) CanonicalizedServiceTiers() ModelPrice {
	if len(p.ServiceTierPrices) == 0 {
		return p
	}

	p.ServiceTierPrices = append([]ServiceTierPrice(nil), p.ServiceTierPrices...)
	for idx := range p.ServiceTierPrices {
		p.ServiceTierPrices[idx].ServiceTier = llm.CanonicalServiceTier(p.ServiceTierPrices[idx].ServiceTier)
	}

	return p
}

// ItemsForServiceTier merges canonical service-tier overrides over the base price items.
// Missing and unknown tiers deliberately use the base price items.
func (p *ModelPrice) ItemsForServiceTier(serviceTier string) []ModelPriceItem {
	serviceTier = llm.CanonicalServiceTier(serviceTier)
	for idx := range p.ServiceTierPrices {
		if llm.CanonicalServiceTier(p.ServiceTierPrices[idx].ServiceTier) == serviceTier {
			items := append([]ModelPriceItem(nil), p.Items...)
			itemIndexes := make(map[PriceItemCode]int, len(items))
			for itemIdx := range items {
				if _, exists := itemIndexes[items[itemIdx].ItemCode]; !exists {
					itemIndexes[items[itemIdx].ItemCode] = itemIdx
				}
			}

			for _, override := range p.ServiceTierPrices[idx].Items {
				if itemIdx, exists := itemIndexes[override.ItemCode]; exists {
					items[itemIdx] = override
					continue
				}

				itemIndexes[override.ItemCode] = len(items)
				items = append(items, override)
			}

			return items
		}
	}

	return p.Items
}

func (p *ModelPrice) Equals(other ModelPrice) bool {
	if len(p.Items) != len(other.Items) || len(p.ServiceTierPrices) != len(other.ServiceTierPrices) {
		return false
	}

	for i := range p.Items {
		if !p.Items[i].Equals(&other.Items[i]) {
			return false
		}
	}

	for i := range p.ServiceTierPrices {
		if !p.ServiceTierPrices[i].Equals(&other.ServiceTierPrices[i]) {
			return false
		}
	}

	return true
}
