package gemini

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

func TestInboundTransformer_TransformError_TruncatedLLMResponseError(t *testing.T) {
	transformer := NewInboundTransformer()

	result := transformer.TransformError(t.Context(), &llm.ResponseError{
		StatusCode: http.StatusBadGateway,
		Detail: llm.ErrorDetail{
			Message:   "upstream body capped",
			Truncated: true,
		},
	})

	require.NotNil(t, result)
	require.Equal(t, http.StatusBadGateway, result.StatusCode)
	require.JSONEq(t, `{"error":{"code":502,"message":"upstream body capped","status":"UNKNOWN","truncated":true}}`, string(result.Body))
}
