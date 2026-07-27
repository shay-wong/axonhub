package responses

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/internal/pkg/xtest"
	"github.com/looplj/axonhub/llm/streams"
)

func TestResponsesStreamRoundTrip_UsesTerminalMetadataSnapshot(t *testing.T) {
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)
	providerEvents := []*httpclient.StreamEvent{
		{
			Type: "response.created",
			Data: []byte(`{"type":"response.created","response":{"id":"resp_early","object":"response","created_at":1700000000,"model":"gpt-5-early","previous_response_id":"resp_prev_early","service_tier":"default","status":"in_progress","output":[]}}`),
		},
		{
			Type: "response.completed",
			Data: []byte(`{"type":"response.completed","response":{"id":"resp_final","object":"response","created_at":1700000001,"model":"gpt-5-final","previous_response_id":"resp_prev_final","service_tier":"priority","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`),
		},
	}

	unified, err := outbound.TransformStream(t.Context(), nil, streams.SliceStream(providerEvents))
	require.NoError(t, err)
	inbound, err := NewInboundTransformer().TransformStream(t.Context(), unified)
	require.NoError(t, err)

	events, err := streams.All(inbound)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	var terminal StreamEvent
	require.NoError(t, json.Unmarshal(events[len(events)-1].Data, &terminal))
	require.Equal(t, StreamEventTypeResponseCompleted, terminal.Type)
	require.NotNil(t, terminal.Response)
	require.Equal(t, "resp_final", terminal.Response.ID)
	require.Equal(t, "gpt-5-final", terminal.Response.Model)
	require.Equal(t, int64(1700000001), terminal.Response.CreatedAt)
	require.NotNil(t, terminal.Response.PreviousResponseID)
	require.Equal(t, "resp_prev_final", *terminal.Response.PreviousResponseID)
	require.NotNil(t, terminal.Response.ServiceTier)
	require.Equal(t, "priority", *terminal.Response.ServiceTier)
	require.NotNil(t, terminal.Response.Usage)
	require.Equal(t, int64(5), terminal.Response.Usage.TotalTokens)
}

// Switching between text and refusal must keep each Responses content part independent.
func TestInboundTransformer_TransformStream_PreservesMixedTextAndRefusalParts(t *testing.T) {
	stream, err := NewInboundTransformer().TransformStream(t.Context(), streams.SliceStream([]*llm.Response{
		{
			ID: "resp_mixed_content", Model: "gpt-5", Created: 1700000000,
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{Role: "assistant"}}},
		},
		{
			ID: "resp_mixed_content", Model: "gpt-5", Created: 1700000000,
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{Content: llm.MessageContent{Content: lo.ToPtr("A")}}}},
		},
		{
			ID: "resp_mixed_content", Model: "gpt-5", Created: 1700000000,
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{Refusal: "R"}}},
		},
		{
			ID: "resp_mixed_content", Model: "gpt-5", Created: 1700000000,
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{Content: llm.MessageContent{Content: lo.ToPtr("B")}}}},
		},
		{
			ID: "resp_mixed_content", Model: "gpt-5", Created: 1700000000,
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{}, FinishReason: lo.ToPtr("stop")}},
		},
	}))
	require.NoError(t, err)

	events, err := streams.All(stream)
	require.NoError(t, err)

	var (
		doneItem *Item
		terminal *Response
	)
	for _, event := range events {
		var parsed StreamEvent
		require.NoError(t, json.Unmarshal(event.Data, &parsed))
		if parsed.Type == StreamEventTypeOutputItemDone && parsed.Item != nil && parsed.Item.Type == "message" {
			doneItem = parsed.Item
		}
		if parsed.Type == StreamEventTypeResponseCompleted {
			terminal = parsed.Response
		}
	}

	requireMixedParts := func(parts []Item) {
		require.Len(t, parts, 3)
		require.Equal(t, "output_text", parts[0].Type)
		require.Equal(t, "A", lo.FromPtr(parts[0].Text))
		require.Equal(t, "refusal", parts[1].Type)
		require.Equal(t, "R", lo.FromPtr(parts[1].Refusal))
		require.Equal(t, "output_text", parts[2].Type)
		require.Equal(t, "B", lo.FromPtr(parts[2].Text))
	}
	require.NotNil(t, doneItem)
	requireMixedParts(doneItem.Content.Items)
	require.NotNil(t, terminal)
	require.Len(t, terminal.Output, 1)
	requireMixedParts(terminal.Output[0].Content.Items)
}

// Compare each event.
var ignoreFields = cmp.FilterPath(func(p cmp.Path) bool {
	// Ignore dynamic fields that are generated at runtime
	if sf, ok := p.Last().(cmp.StructField); ok {
		switch sf.Name() {
		case "ID", "ItemID", "Obfuscation", "Logprobs", "Response":
			return true
		}
	}

	return false
}, cmp.Ignore())

func TestInboundTransformer_StreamTransformation_WithTestData(t *testing.T) {
	trans := NewInboundTransformer()

	tests := []struct {
		name                 string
		inputStreamFile      string
		expectedStreamFile   string
		expectedResponseFile string
	}{
		{
			name:                 "stream transformation with text and multiple tool calls",
			inputStreamFile:      "llm-tool-2.stream.jsonl",
			expectedStreamFile:   "tool-2.stream.jsonl",
			expectedResponseFile: "tool-2.response.json",
		},
		{
			name:                 "stream transformation with custom tool call",
			inputStreamFile:      "llm-custom_tool.stream.jsonl",
			expectedStreamFile:   "custom_tool.stream.jsonl",
			expectedResponseFile: "custom_tool.stream.response.json",
		},
		{
			name:                 "stream transformation with encrypted reasoning only (no summary items)",
			inputStreamFile:      "llm-encrypted_only.stream.jsonl",
			expectedStreamFile:   "encrypted_only.stream.jsonl",
			expectedResponseFile: "encrypted_only.response.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Load the input file (LLM format responses)
			llmResponses, err := xtest.LoadLlmResponses(t, tt.inputStreamFile)
			require.NoError(t, err)

			// Load expected events from the expected stream file
			expectedEvents, err := xtest.LoadStreamChunks(t, tt.expectedStreamFile)
			require.NoError(t, err)

			// Create a mock stream from LLM responses
			mockStream := streams.SliceStream(llmResponses)

			// Transform the stream (LLM -> OpenAI Responses API)
			transformedStream, err := trans.TransformStream(t.Context(), mockStream)
			require.NoError(t, err)

			// Collect all transformed events
			var actualEvents []StreamEvent

			for transformedStream.Next() {
				event := transformedStream.Current()

				var ev StreamEvent

				err := json.Unmarshal(event.Data, &ev)
				require.NoError(t, err)

				actualEvents = append(actualEvents, ev)
			}

			require.NoError(t, transformedStream.Err())

			// Verify event count
			require.Equal(t, len(expectedEvents), len(actualEvents), "Event count should match expected")

			for i, expectedEvent := range expectedEvents {
				var expected StreamEvent

				err := json.Unmarshal(expectedEvent.Data, &expected)
				require.NoError(t, err)

				actual := actualEvents[i]

				if !xtest.Equal(expected, actual, ignoreFields) {
					t.Fatalf("event %d mismatch:\n%s", i, cmp.Diff(expected, actual, ignoreFields))
				}
			}

			// Verify the last event is response.completed and compare with expectedResponseFile
			if tt.expectedResponseFile != "" {
				require.NotEmpty(t, actualEvents, "Expected at least one event")

				lastEvent := actualEvents[len(actualEvents)-1]
				require.Equal(t, StreamEventTypeResponseCompleted, lastEvent.Type,
					"Last event should be response.completed")
				require.NotNil(t, lastEvent.Response, "response.completed event should have Response")

				// Load expected response from file
				var expectedResponse Response

				err := xtest.LoadTestData(t, tt.expectedResponseFile, &expectedResponse)
				require.NoError(t, err)

				// Compare the response in the event with the expected response file
				// Ignore dynamic fields like ID, ItemID
				responseIgnoreFields := cmp.FilterPath(func(p cmp.Path) bool {
					if sf, ok := p.Last().(cmp.StructField); ok {
						switch sf.Name() {
						case "ID", "ItemID", "Obfuscation", "Logprobs":
							return true
						}
					}

					return false
				}, cmp.Ignore())

				if !xtest.Equal(expectedResponse, *lastEvent.Response, responseIgnoreFields) {
					t.Fatalf("response.completed response mismatch:\n%s",
						cmp.Diff(expectedResponse, *lastEvent.Response, responseIgnoreFields))
				}
			}
		})
	}
}

func TestInboundTransformer_TransformStream_KeepsResponsesReasoningItemsSeparate(t *testing.T) {
	trans := NewInboundTransformer()

	stream, err := trans.TransformStream(t.Context(), streams.SliceStream([]*llm.Response{
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_reasoning_multi",
			Created: 1700000000,
			Model:   "gpt-5",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{Role: "assistant"},
			}},
		},
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_reasoning_multi",
			Created: 1700000000,
			Model:   "gpt-5",
			TransformerMetadata: map[string]any{
				responsesReasoningItemTransformerMetadataKey: responsesReasoningItemMetadata{ID: "rs_1", Done: true},
			},
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{ID: "rs_1", ReasoningSignature: lo.ToPtr("gAAAA_done_1")},
			}},
		},
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_reasoning_multi",
			Created: 1700000000,
			Model:   "gpt-5",
			TransformerMetadata: map[string]any{
				responsesReasoningItemTransformerMetadataKey: map[string]any{"id": "rs_2", "done": true},
			},
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{ID: "rs_2", ReasoningSignature: lo.ToPtr("gAAAA_done_2")},
			}},
		},
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_reasoning_multi",
			Created: 1700000000,
			Model:   "gpt-5",
			Choices: []llm.Choice{{
				Index:        0,
				Delta:        &llm.Message{},
				FinishReason: lo.ToPtr("stop"),
			}},
		},
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_reasoning_multi",
			Created: 1700000000,
			Model:   "gpt-5",
			Usage:   &llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	}))
	require.NoError(t, err)

	var actualEvents []StreamEvent
	for stream.Next() {
		event := stream.Current()
		var ev StreamEvent
		err := json.Unmarshal(event.Data, &ev)
		require.NoError(t, err)
		actualEvents = append(actualEvents, ev)
	}
	require.NoError(t, stream.Err())

	var doneItems []Item
	for _, event := range actualEvents {
		if event.Type == StreamEventTypeOutputItemDone && event.Item != nil && event.Item.Type == "reasoning" {
			doneItems = append(doneItems, *event.Item)
		}
	}

	require.Len(t, doneItems, 2)
	require.Equal(t, "rs_1", doneItems[0].ID)
	require.Equal(t, "gAAAA_done_1", lo.FromPtr(doneItems[0].EncryptedContent))
	require.Equal(t, "rs_2", doneItems[1].ID)
	require.Equal(t, "gAAAA_done_2", lo.FromPtr(doneItems[1].EncryptedContent))

	lastEvent := actualEvents[len(actualEvents)-1]
	require.Equal(t, StreamEventTypeResponseCompleted, lastEvent.Type)
	require.NotNil(t, lastEvent.Response)
	require.Len(t, lastEvent.Response.Output, 2)
	require.Equal(t, "rs_1", lastEvent.Response.Output[0].ID)
	require.Equal(t, "gAAAA_done_1", lo.FromPtr(lastEvent.Response.Output[0].EncryptedContent))
	require.Equal(t, "rs_2", lastEvent.Response.Output[1].ID)
	require.Equal(t, "gAAAA_done_2", lo.FromPtr(lastEvent.Response.Output[1].EncryptedContent))
}

func TestInboundTransformer_TransformStream_PreservesWebSearchCallsFromChunkMetadata(t *testing.T) {
	trans := NewInboundTransformer()

	stream, err := trans.TransformStream(t.Context(), streams.SliceStream([]*llm.Response{
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_stream_web_search_no_annotations",
			Created: 1700000000,
			Model:   "gpt-4o-search-preview",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Content: llm.MessageContent{Content: lo.ToPtr("Search result without inline citations")},
				},
			}},
		},
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_stream_web_search_no_annotations",
			Created: 1700000000,
			Model:   "gpt-4o-search-preview",
			TransformerMetadata: map[string]any{
				responsesWebSearchCallsTransformerMetadataKey: []Item{{
					ID:     "ws_456",
					Type:   "web_search_call",
					Status: lo.ToPtr("completed"),
					Action: NewWebSearchAction(&WebSearchAction{
						Type:  "search",
						Query: "latest ai news",
						Sources: []WebSearchSource{{
							Type:  "url",
							URL:   "https://example.com/source",
							Title: "Example Source",
						}},
					}),
				}},
			},
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: lo.ToPtr("stop"),
			}},
		},
		{
			Object:  "chat.completion.chunk",
			ID:      "resp_stream_web_search_no_annotations",
			Created: 1700000000,
			Model:   "gpt-4o-search-preview",
			Usage:   &llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	}))
	require.NoError(t, err)

	var actualEvents []StreamEvent
	for stream.Next() {
		event := stream.Current()
		var ev StreamEvent
		err := json.Unmarshal(event.Data, &ev)
		require.NoError(t, err)
		actualEvents = append(actualEvents, ev)
	}
	require.NoError(t, stream.Err())
	require.NotEmpty(t, actualEvents)

	lastEvent := actualEvents[len(actualEvents)-1]
	require.Equal(t, StreamEventTypeResponseCompleted, lastEvent.Type)
	require.NotNil(t, lastEvent.Response)
	require.Len(t, lastEvent.Response.Output, 2)
	require.Equal(t, "web_search_call", lastEvent.Response.Output[0].Type)
	require.Equal(t, "ws_456", lastEvent.Response.Output[0].ID)
	require.NotNil(t, lastEvent.Response.Output[0].Action)
	require.NotNil(t, lastEvent.Response.Output[0].Action.WebSearch)
	require.Equal(t, "latest ai news", lastEvent.Response.Output[0].Action.WebSearch.Query)
	require.Equal(t, "message", lastEvent.Response.Output[1].Type)
	require.NotNil(t, lastEvent.Response.Output[1].Content)
	require.Len(t, lastEvent.Response.Output[1].Content.Items, 1)
	require.Equal(t, "Search result without inline citations", lo.FromPtr(lastEvent.Response.Output[1].Content.Items[0].Text))
}

func TestInboundTransformer_StreamTransformation_PreservesToolSearchItems(t *testing.T) {
	trans := NewInboundTransformer()

	callItem := Item{
		ID:        "tsc_123",
		Type:      "tool_search_call",
		CallID:    "call_abc123",
		Execution: "client",
		Status:    lo.ToPtr("completed"),
		Arguments: `{"goal":"Find the shipping ETA tool for order_42."}`,
	}
	outputItem := Item{
		ID:        "tso_123",
		Type:      "tool_search_output",
		CallID:    "call_abc123",
		Execution: "client",
		Status:    lo.ToPtr("completed"),
		Tools: []Tool{
			{
				Type:        "function",
				Name:        "get_shipping_eta",
				Description: "Look up shipping ETA details for an order.",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	}

	callRaw, err := json.Marshal(callItem)
	require.NoError(t, err)

	outputRaw, err := json.Marshal(outputItem)
	require.NoError(t, err)

	mockStream := streams.SliceStream([]*llm.Response{
		{
			ID:      "resp_tool_search",
			Model:   "gpt-5.4",
			Created: 1700000000,
			Choices: []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						Content: llm.MessageContent{
							MultipleContent: []llm.MessageContentPart{
								{
									Type:        "tool_search_call",
									ServerBlock: callRaw,
								},
								{
									Type:        "tool_search_output",
									ServerBlock: outputRaw,
								},
							},
						},
					},
					FinishReason: lo.ToPtr("tool_calls"),
				},
			},
			Usage: &llm.Usage{
				PromptTokens:     1,
				CompletionTokens: 1,
				TotalTokens:      2,
			},
		},
	})

	transformedStream, err := trans.TransformStream(t.Context(), mockStream)
	require.NoError(t, err)

	var actualEvents []StreamEvent
	for transformedStream.Next() {
		event := transformedStream.Current()

		var ev StreamEvent
		err := json.Unmarshal(event.Data, &ev)
		require.NoError(t, err)

		actualEvents = append(actualEvents, ev)
	}
	require.NoError(t, transformedStream.Err())

	var toolSearchEvents []StreamEvent
	for _, ev := range actualEvents {
		if ev.Type != StreamEventTypeOutputItemAdded && ev.Type != StreamEventTypeOutputItemDone {
			continue
		}
		if ev.Item == nil {
			continue
		}
		if ev.Item.Type == "tool_search_call" || ev.Item.Type == "tool_search_output" {
			toolSearchEvents = append(toolSearchEvents, ev)
		}
	}

	require.Len(t, toolSearchEvents, 4)
	require.Equal(t, StreamEventTypeOutputItemAdded, toolSearchEvents[0].Type)
	require.Equal(t, "tool_search_call", toolSearchEvents[0].Item.Type)
	require.Equal(t, "in_progress", *toolSearchEvents[0].Item.Status)
	require.Equal(t, StreamEventTypeOutputItemDone, toolSearchEvents[1].Type)
	require.Equal(t, "tool_search_call", toolSearchEvents[1].Item.Type)
	require.Equal(t, "completed", *toolSearchEvents[1].Item.Status)
	require.JSONEq(t, callItem.Arguments, toolSearchEvents[1].Item.Arguments)

	require.Equal(t, StreamEventTypeOutputItemAdded, toolSearchEvents[2].Type)
	require.Equal(t, "tool_search_output", toolSearchEvents[2].Item.Type)
	require.Equal(t, "in_progress", *toolSearchEvents[2].Item.Status)
	require.Equal(t, StreamEventTypeOutputItemDone, toolSearchEvents[3].Type)
	require.Equal(t, "tool_search_output", toolSearchEvents[3].Item.Type)
	require.Equal(t, "completed", *toolSearchEvents[3].Item.Status)
	require.Len(t, toolSearchEvents[3].Item.Tools, 1)
	require.Equal(t, "get_shipping_eta", toolSearchEvents[3].Item.Tools[0].Name)

	lastEvent := actualEvents[len(actualEvents)-1]
	require.Equal(t, StreamEventTypeResponseCompleted, lastEvent.Type)
	require.NotNil(t, lastEvent.Response)

	var outputTypes []string
	for _, item := range lastEvent.Response.Output {
		if item.Type == "tool_search_call" || item.Type == "tool_search_output" {
			outputTypes = append(outputTypes, item.Type)
		}
	}
	require.Equal(t, []string{"tool_search_call", "tool_search_output"}, outputTypes)
}

func TestInboundTransformer_TransformStream_EmitsUpstreamErrorEvents(t *testing.T) {
	tests := []struct {
		name      string
		source    streams.Stream[*llm.Response]
		wantTypes []StreamEventType
		assert    func(t *testing.T, events []StreamEvent)
	}{
		{
			name:      "emits error event before response starts",
			source:    &errorResponseStream{err: errors.New("upstream boom")},
			wantTypes: []StreamEventType{StreamEventTypeError},
			assert: func(t *testing.T, events []StreamEvent) {
				require.Equal(t, "stream_error", events[0].Code)
				require.Equal(t, "upstream boom", events[0].Message)
			},
		},
		{
			name: "emits response.failed after response starts",
			source: &errorResponseStream{
				items: []*llm.Response{{
					ID:      "resp_123",
					Model:   "gpt-test",
					Created: 123,
				}},
				err: errors.New("upstream boom"),
			},
			wantTypes: []StreamEventType{
				StreamEventTypeResponseCreated,
				StreamEventTypeResponseInProgress,
				StreamEventTypeResponseFailed,
			},
			assert: func(t *testing.T, events []StreamEvent) {
				failed := events[len(events)-1]
				require.NotNil(t, failed.Response)
				require.NotNil(t, failed.Response.Status)
				require.Equal(t, "failed", *failed.Response.Status)
				require.NotNil(t, failed.Response.Error)
				require.Equal(t, "stream_error", failed.Response.Error.Code)
				require.Equal(t, "upstream boom", failed.Response.Error.Message)
			},
		},
		{
			name: "preserves typed response errors",
			source: streams.SliceStream([]*llm.Response{{
				ID:      "resp_context_limit",
				Model:   "gpt-test",
				Created: 123,
				Error: &llm.ResponseError{Detail: llm.ErrorDetail{
					Type:    "invalid_request_error",
					Code:    "context_length_exceeded",
					Message: "Your input exceeds the context window of this model.",
				}},
			}}),
			wantTypes: []StreamEventType{
				StreamEventTypeResponseCreated,
				StreamEventTypeResponseInProgress,
				StreamEventTypeResponseFailed,
			},
			assert: func(t *testing.T, events []StreamEvent) {
				failed := events[len(events)-1]
				require.NotNil(t, failed.Response)
				require.NotNil(t, failed.Response.Error)
				require.Equal(t, "invalid_request_error", failed.Response.Error.Type)
				require.Equal(t, "context_length_exceeded", failed.Response.Error.Code)
				require.Equal(t, "Your input exceeds the context window of this model.", failed.Response.Error.Message)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformedStream, err := NewInboundTransformer().TransformStream(t.Context(), tt.source)
			require.NoError(t, err)

			actualEvents := make([]StreamEvent, 0, len(tt.wantTypes))
			for range 10 {
				if !transformedStream.Next() {
					break
				}

				event := transformedStream.Current()
				require.NotNil(t, event)

				var actual StreamEvent
				err := json.Unmarshal(event.Data, &actual)
				require.NoError(t, err)

				actualEvents = append(actualEvents, actual)
			}

			require.Len(t, actualEvents, len(tt.wantTypes))
			for i, wantType := range tt.wantTypes {
				require.Equal(t, wantType, actualEvents[i].Type)
			}

			require.False(t, transformedStream.Next())
			require.NoError(t, transformedStream.Err())

			tt.assert(t, actualEvents)
		})
	}
}

type errorResponseStream struct {
	items []*llm.Response
	index int
	err   error
}

func (s *errorResponseStream) Next() bool {
	if s.index < len(s.items) {
		s.index++
		return true
	}

	return false
}

func (s *errorResponseStream) Current() *llm.Response {
	if s.index == 0 || s.index > len(s.items) {
		return nil
	}

	return s.items[s.index-1]
}

func (s *errorResponseStream) Err() error {
	if s.index >= len(s.items) {
		return s.err
	}

	return nil
}

func (s *errorResponseStream) Close() error {
	return nil
}
