package anthropic

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

// The Anthropic API has no "content_filter" stop reason; the equivalent is "refusal".
func TestInboundTransformer_TransformResponse_ContentFilterStopReason(t *testing.T) {
	transformer := NewInboundTransformer()
	finishReason := "content_filter"
	text := "I can't help with that."

	resp, err := transformer.TransformResponse(t.Context(), &llm.Response{
		ID:      "msg_filtered",
		Object:  "chat.completion",
		Model:   "claude-sonnet-4-6",
		Created: 1234567890,
		Choices: []llm.Choice{{
			Index: 0,
			Message: &llm.Message{
				Role:    "assistant",
				Content: llm.MessageContent{Content: &text},
			},
			FinishReason: &finishReason,
		}},
	})
	require.NoError(t, err)

	var msg Message
	require.NoError(t, json.Unmarshal(resp.Body, &msg))
	require.NotNil(t, msg.StopReason)
	require.Equal(t, "refusal", *msg.StopReason)
}

func TestInboundStream_ContentFilterStopReason(t *testing.T) {
	transformer := NewInboundTransformer()
	finishReason := "content_filter"

	events := collectInboundStreamEvents(t, transformer, []*llm.Response{
		{
			ID:     "msg_filtered",
			Object: "chat.completion.chunk",
			Model:  "test-model",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &finishReason,
			}},
		},
	})

	var stopReason *string
	for _, event := range events {
		if event.Type == "message_delta" && event.Delta != nil {
			stopReason = event.Delta.StopReason
		}
	}
	require.NotNil(t, stopReason)
	require.Equal(t, "refusal", *stopReason)
}

func TestInboundTransformer_TransformResponse_UnknownFinishReason(t *testing.T) {
	tests := []struct {
		finishReason string
		want         string
		wantError    bool
	}{
		{"error", "", true},
		{"cancelled", "", true},
		{"unknown", "end_turn", false},
		{"pause_turn", "pause_turn", false},
		{"stop_sequence", "stop_sequence", false},
	}

	for _, tt := range tests {
		t.Run(tt.finishReason, func(t *testing.T) {
			transformer := NewInboundTransformer()
			finishReason := tt.finishReason
			text := "partial"

			resp, err := transformer.TransformResponse(t.Context(), &llm.Response{
				ID:      "msg_unknown",
				Object:  "chat.completion",
				Model:   "claude-sonnet-4-6",
				Created: 1234567890,
				Choices: []llm.Choice{{
					Index: 0,
					Message: &llm.Message{
						Role:    "assistant",
						Content: llm.MessageContent{Content: &text},
					},
					FinishReason: &finishReason,
				}},
			})
			require.NoError(t, err)
			if tt.wantError {
				require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
				var errorResponse AnthropicError
				require.NoError(t, json.Unmarshal(resp.Body, &errorResponse))
				require.Equal(t, "server_error", errorResponse.Error.Type)
				require.NotEmpty(t, errorResponse.Error.Message)
				return
			}

			require.Equal(t, http.StatusOK, resp.StatusCode)
			var msg Message
			require.NoError(t, json.Unmarshal(resp.Body, &msg))
			require.NotNil(t, msg.StopReason)
			require.Equal(t, tt.want, *msg.StopReason)
		})
	}
}
