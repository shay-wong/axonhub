package llm

import "strings"

const (
	// ServiceTierDefault is the provider's standard processing tier.
	ServiceTierDefault = "default"
	// ServiceTierPriority is the OpenAI/Codex wire tier used by Codex fast mode.
	ServiceTierPriority = "priority"
	// ServiceTierUltrafast is the OpenAI/Codex wire tier used by Ultrafast mode.
	ServiceTierUltrafast = "ultrafast"
)

// CanonicalServiceTier normalizes provider tier values for storage and price matching.
// Unknown tiers remain distinct and deliberately use a model's base price when no exact
// override is configured.
func CanonicalServiceTier(tier string) string {
	return strings.ToLower(strings.TrimSpace(tier))
}

// OpenAIServiceTier returns a canonical tier only for OpenAI-compatible request formats.
func OpenAIServiceTier(format APIFormat, tier *string) *string {
	if tier == nil {
		return nil
	}

	switch format {
	case "", APIFormatOpenAIChatCompletion, APIFormatOpenAICompletion,
		APIFormatOpenAIResponse, APIFormatOpenAIResponseCompact:
		canonical := CanonicalServiceTier(*tier)
		if canonical != "" {
			return &canonical
		}
	}

	return nil
}
