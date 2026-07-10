package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestServiceTierRoundTrip(t *testing.T) {
	t.Run("request maps to unified model", func(t *testing.T) {
		request, err := convertToLLMRequest(&MessageRequest{
			Model:       "claude-sonnet",
			MaxTokens:   128,
			ServiceTier: "standard_only",
		})
		require.NoError(t, err)
		require.NotNil(t, request.ServiceTier)
		require.Equal(t, "standard_only", *request.ServiceTier)

		roundTripped := convertToAnthropicRequest(request)
		require.Equal(t, "standard_only", roundTripped.ServiceTier)
	})

	t.Run("request tier does not cross protocol or unsupported platform boundaries", func(t *testing.T) {
		priority := "priority"
		require.Empty(t, convertToAnthropicRequest(&llm.Request{
			APIFormat:   llm.APIFormatOpenAIResponse,
			ServiceTier: &priority,
		}).ServiceTier)

		standardOnly := "standard_only"
		require.Empty(t, convertToAnthropicRequestWithConfig(&llm.Request{
			APIFormat:   llm.APIFormatAnthropicMessage,
			ServiceTier: &standardOnly,
		}, &Config{Type: PlatformBedrock}).ServiceTier)
	})

	t.Run("response maps applied tier into usage", func(t *testing.T) {
		response := convertToAnthropicResponse(&llm.Response{
			ServiceTier: "priority",
			Usage:       &llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		})
		require.NotNil(t, response.Usage)
		require.Equal(t, "priority", response.Usage.ServiceTier)
	})
}

func TestAnthropicInboundStreamPreservesServiceTier(t *testing.T) {
	content := "ok"
	finishReason := "stop"
	source := streams.SliceStream([]*llm.Response{
		{
			ID:          "msg_tier",
			Model:       "claude-sonnet",
			ServiceTier: "priority",
			Choices: []llm.Choice{{
				Delta: &llm.Message{Content: llm.MessageContent{Content: &content}},
			}},
		},
		{
			ID:          "msg_tier",
			Model:       "claude-sonnet",
			ServiceTier: "priority",
			Choices:     []llm.Choice{{FinishReason: &finishReason}},
		},
		{
			ID:          "msg_tier",
			Model:       "claude-sonnet",
			ServiceTier: "priority",
			Usage:       &llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	})

	stream, err := (&InboundTransformer{}).TransformStream(t.Context(), source)
	require.NoError(t, err)

	var startTier, deltaTier string
	for stream.Next() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
		switch event.Type {
		case "message_start":
			startTier = event.Message.Usage.ServiceTier
		case "message_delta":
			deltaTier = event.Usage.ServiceTier
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, "priority", startTier)
	require.Equal(t, "priority", deltaTier)
}

func TestAnthropicOutboundStreamPreservesFinalServiceTier(t *testing.T) {
	stream := newOutboundStream(streams.SliceStream([]*httpclient.StreamEvent{
		{
			Type: "message_start",
			Data: []byte(`{"type":"message_start","message":{"id":"msg_tier","type":"message","role":"assistant","content":[],"model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":0}}}`),
		},
		{
			Type: "message_delta",
			Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"service_tier":"priority"}}`),
		},
		{Type: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}), PlatformDirect)

	responses, err := streams.All(stream)
	require.NoError(t, err)
	require.NotEmpty(t, responses)
	require.True(t, lo.SomeBy(responses, func(response *llm.Response) bool {
		return response != nil && response.ServiceTier == "priority"
	}))
}
