package responses

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestResponsesFailedAfterInlineToolResultForkCompatibility(t *testing.T) {
	request := &httpclient.Request{TransformerMetadata: map[string]any{
		llm.TransformerMetadataKeyAnthropicFunctionToolSearchName: "ToolSearch",
	}}
	stream, err := new(OutboundTransformer).TransformStream(t.Context(), request, streams.SliceStream([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.output_item.done","item":{"id":"result_1","type":"tool_search_output","call_id":"call_1","execution":"client","status":"completed","tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{}}}]}}`)},
		{Data: []byte(`{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"server_is_overloaded","message":"overloaded"}}}`)},
	}))
	require.NoError(t, err)
	require.True(t, stream.Next())
	require.Len(t, stream.Current().Choices[0].Delta.InlineToolResults, 1)
	var failed bool
	for stream.Next() {
		failed = failed || stream.Current().ProviderTerminalOutcome == llm.ResponseTerminalOutcomeFailed
	}
	require.NoError(t, stream.Err(), "an emitted tool result must not become a retryable empty response")
	require.True(t, failed)
}

func TestResponsesRetryableFailureSnapshotForkCompatibility(t *testing.T) {
	stream, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.failed","response":{"id":"resp_failed","model":"gpt-6-astra","created_at":123,"status":"failed","service_tier":"ultrafast","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"error":{"code":"server_is_overloaded","message":"overloaded","request_id":"req_failed"}}}`)},
	}))
	require.NoError(t, err)
	require.True(t, stream.Next())
	chunk := stream.Current()
	require.Equal(t, "resp_failed", chunk.ID)
	require.Equal(t, "gpt-6-astra", chunk.Model)
	require.Equal(t, "ultrafast", chunk.ServiceTier)
	require.NotNil(t, chunk.Usage)
	require.Equal(t, int64(12), chunk.Usage.TotalTokens)
	require.False(t, stream.Next())
	require.ErrorIs(t, stream.Err(), llm.ErrEmptyResponse)
	var responseErr *llm.ResponseError
	require.ErrorAs(t, stream.Err(), &responseErr)
	require.Equal(t, "req_failed", responseErr.Detail.RequestID)
}

func TestResponsesFailedTerminalRefusalForkCompatibility(t *testing.T) {
	stream, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.failed","response":{"id":"resp_refusal","status":"failed","service_tier":"ultrafast","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"error":{"code":"content_filter","message":"filtered"},"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I cannot help."}]}]}}`)},
	}))
	require.NoError(t, err)
	var refusal strings.Builder
	var failed *llm.Response
	for stream.Next() {
		chunk := stream.Current()
		for _, choice := range chunk.Choices {
			if choice.Delta != nil {
				refusal.WriteString(choice.Delta.Refusal)
			}
		}
		if chunk.ProviderTerminalOutcome == llm.ResponseTerminalOutcomeFailed {
			failed = chunk
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, "I cannot help.", refusal.String())
	require.NotNil(t, failed)
	require.Equal(t, "ultrafast", failed.ServiceTier)
	require.NotNil(t, failed.Usage)
	require.Equal(t, int64(12), failed.Usage.TotalTokens)
}

func TestResponsesTerminalStopsReadingForkCompatibility(t *testing.T) {
	source := &responsesErrorAfterStream{items: []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.completed","response":{"id":"resp_done","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`)},
		{Data: []byte(`{"type":"error","error":{"code":"server_error","message":"late failure"}}`)},
	}}
	stream, err := new(OutboundTransformer).TransformStream(t.Context(), nil, source)
	require.NoError(t, err)
	chunks, err := streams.All(stream)
	require.NoError(t, err)
	require.Equal(t, 1, source.index)
	require.Equal(t, 1, countDoneResponses(chunks))
	var usageChunks int
	for _, chunk := range chunks {
		if chunk.Usage != nil {
			usageChunks++
			require.Equal(t, int64(12), chunk.Usage.TotalTokens)
		}
	}
	require.Equal(t, 1, usageChunks)
}
