package responses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestOutboundTransformerStreamPreservesServiceTier(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-key")
	require.NoError(t, err)

	stream, err := transformer.TransformStream(t.Context(), nil, streams.SliceStream([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.created","response":{"id":"resp_tier","object":"response","created_at":1,"model":"gpt-5","status":"in_progress","service_tier":"auto","output":[]}}`)},
		{Data: []byte(`{"type":"response.completed","response":{"id":"resp_tier","object":"response","created_at":1,"model":"gpt-5","status":"completed","service_tier":"default","output":[],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`)},
	}))
	require.NoError(t, err)

	responses, err := streams.All(stream)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(responses), 3)
	require.Equal(t, "auto", responses[0].ServiceTier)
	require.Equal(t, "default", responses[len(responses)-2].ServiceTier)
}

func TestInboundTransformerStreamPreservesServiceTier(t *testing.T) {
	finishReason := "stop"
	stream, err := (&InboundTransformer{}).TransformStream(t.Context(), streams.SliceStream([]*llm.Response{
		{
			ID:          "resp_tier",
			Object:      "chat.completion.chunk",
			Model:       "gpt-5",
			Created:     1,
			ServiceTier: "auto",
			Choices:     []llm.Choice{{Delta: &llm.Message{Role: "assistant"}}},
		},
		{
			ID:          "resp_tier",
			Object:      "chat.completion.chunk",
			Model:       "gpt-5",
			Created:     1,
			ServiceTier: "default",
			Choices:     []llm.Choice{{Delta: &llm.Message{}, FinishReason: &finishReason}},
		},
		{
			ID:          "resp_tier",
			Object:      "chat.completion.chunk",
			Model:       "gpt-5",
			Created:     1,
			ServiceTier: "default",
			Usage:       &llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}))
	require.NoError(t, err)

	var createdTier, completedTier string
	for stream.Next() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
		if event.Response == nil || event.Response.ServiceTier == nil {
			continue
		}
		switch event.Type {
		case StreamEventTypeResponseCreated:
			createdTier = *event.Response.ServiceTier
		case StreamEventTypeResponseCompleted:
			completedTier = *event.Response.ServiceTier
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, "auto", createdTier)
	require.Equal(t, "default", completedTier)
}

func TestInboundTransformerResponsePreservesServiceTier(t *testing.T) {
	response := convertToResponsesAPIResponse(&llm.Response{ServiceTier: "ultrafast"})
	require.NotNil(t, response.ServiceTier)
	require.Equal(t, "ultrafast", *response.ServiceTier)
}

func TestResponsesOutboundServiceTierStaysWithinOpenAIFormats(t *testing.T) {
	tier := "priority"
	require.Equal(t, &tier, llm.OpenAIServiceTier(llm.APIFormatOpenAIChatCompletion, &tier))
	require.Nil(t, llm.OpenAIServiceTier(llm.APIFormatAnthropicMessage, &tier))
	canonicalTier := " PRIORITY "
	require.Equal(t, &tier, llm.OpenAIServiceTier(llm.APIFormatOpenAIChatCompletion, &canonicalTier))
}
