package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalServiceTier(t *testing.T) {
	require.Equal(t, ServiceTierPriority, CanonicalServiceTier(" Priority "))
	require.Equal(t, ServiceTierUltrafast, CanonicalServiceTier(" ULTRAFAST "))
	require.Equal(t, "provider-specific", CanonicalServiceTier("provider-specific"))
}

func TestOpenAIServiceTier(t *testing.T) {
	tier := " PRIORITY "

	require.Equal(t, ServiceTierPriority, *OpenAIServiceTier(APIFormatOpenAIResponse, &tier))
	require.Nil(t, OpenAIServiceTier(APIFormatAnthropicMessage, &tier))
	require.Nil(t, OpenAIServiceTier(APIFormatOpenAIResponse, nil))
}
