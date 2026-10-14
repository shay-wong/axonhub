package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIServiceTierWebSocketForkCompatibility(t *testing.T) {
	for _, tier := range []string{"fast", "ultrafast"} {
		t.Run(tier, func(t *testing.T) {
			actual := OpenAIServiceTier(APIFormatOpenAIResponseWebSocket, &tier)
			require.NotNil(t, actual)
			require.Equal(t, CanonicalServiceTier(tier), *actual)
		})
	}
}
