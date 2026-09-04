package anthropic

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/internal/pkg/xjson"
	"github.com/looplj/axonhub/llm/internal/pkg/xtest"
	"github.com/looplj/axonhub/llm/streams"
)

type failingResponseStream struct {
	items []*llm.Response
	index int
	err   error
}

func TestInboundStream_EmitsErrorWithoutMessageLifecycle(t *testing.T) {
	stream, err := NewInboundTransformer().TransformStream(t.Context(), streams.SliceStream([]*llm.Response{{
		Error: &llm.ResponseError{Detail: llm.ErrorDetail{
			Type:    "invalid_request_error",
			Code:    "context_length_exceeded",
			Message: "context window exceeded",
		}},
		Choices: []llm.Choice{{FinishReason: lo.ToPtr("error")}},
	}, llm.DoneResponse}))
	require.NoError(t, err)

	events, err := streams.All(stream)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "error", events[0].Type)
	require.JSONEq(t, `{
		"type":"error",
		"error":{"type":"invalid_request_error","message":"context window exceeded"}
	}`, string(events[0].Data))
}

func TestInboundTransformer_MapsCancellationToAnthropicError(t *testing.T) {
	response := &llm.Response{Choices: []llm.Choice{{FinishReason: lo.ToPtr("cancelled")}}}
	transformer := NewInboundTransformer()

	nonStream, err := transformer.TransformResponse(t.Context(), response)
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, nonStream.StatusCode)
	require.JSONEq(t, `{
		"type":"server_error",
		"request_id":"",
		"error":{"type":"server_error","message":"upstream response cancelled"}
	}`, string(nonStream.Body))

	stream, err := transformer.TransformStream(t.Context(), streams.SliceStream([]*llm.Response{response, llm.DoneResponse}))
	require.NoError(t, err)
	events, err := streams.All(stream)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "error", events[0].Type)
	require.JSONEq(t, `{
		"type":"error",
		"error":{"type":"server_error","message":"upstream response cancelled"}
	}`, string(events[0].Data))
}

func TestInboundStream_MapsContentFilterToRefusal(t *testing.T) {
	stream, err := NewInboundTransformer().TransformStream(t.Context(), streams.SliceStream([]*llm.Response{
		{
			ID:      "msg_filtered",
			Model:   "claude-sonnet-4-5",
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{Role: "assistant", Content: llm.MessageContent{Content: lo.ToPtr("blocked")}}}},
		},
		{
			ID:      "msg_filtered",
			Model:   "claude-sonnet-4-5",
			Choices: []llm.Choice{{Index: 0, Delta: &llm.Message{}, FinishReason: lo.ToPtr("content_filter")}},
		},
		{
			ID:    "msg_filtered",
			Model: "claude-sonnet-4-5",
			Usage: &llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
		llm.DoneResponse,
	}))
	require.NoError(t, err)

	events, err := streams.All(stream)
	require.NoError(t, err)
	var stopReason *string
	for _, event := range events {
		var decoded StreamEvent
		require.NoError(t, json.Unmarshal(event.Data, &decoded))
		if decoded.Type == "message_delta" && decoded.Delta != nil {
			stopReason = decoded.Delta.StopReason
		}
	}
	require.NotNil(t, stopReason)
	require.Equal(t, "refusal", *stopReason)
}

func (s *failingResponseStream) Next() bool {
	return s.index < len(s.items)
}

func (s *failingResponseStream) Current() *llm.Response {
	item := s.items[s.index]
	s.index++

	return item
}

func (s *failingResponseStream) Err() error {
	return s.err
}

func (s *failingResponseStream) Close() error {
	return nil
}

func TestInboundStream_FinalizesOnCleanExhaustionWithoutFinishReason(t *testing.T) {
	transformer := NewInboundTransformer()
	text := "Done"
	thinking := "Thinking"
	serverToolUseRaw := json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_01ABC123","name":"tool_search_tool_regex","input":{"query":"weather"}}`)
	toolSearchResultRaw := json.RawMessage(`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_01ABC123","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"get_weather"}]}}`)

	tests := []struct {
		name                     string
		delta                    *llm.Message
		usage                    *llm.Usage
		expectedStopReason       string
		expectedContentBlockType string
	}{
		{
			name: "tool call",
			delta: &llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					Index: 0,
					ID:    "call_123",
					Type:  "function",
					Function: llm.FunctionCall{
						Name:      "search",
						Arguments: `{"query":"test"}`,
					},
				}},
			},
			usage: &llm.Usage{
				CompletionTokens: 7,
			},
			expectedStopReason: "tool_use",
		},
		{
			name: "tool call with empty arguments",
			delta: &llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					Index: 0,
					ID:    "call_empty",
					Type:  "function",
					Function: llm.FunctionCall{
						Name: "list",
					},
				}},
			},
			expectedStopReason: "tool_use",
		},
		{
			name: "server tool call",
			delta: &llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					Index: 0,
					ID:    "server_tool_123",
					Type:  "function",
					Function: llm.FunctionCall{
						Name:      "web_search",
						Arguments: `{"query":"test"}`,
					},
					TransformerMetadata: map[string]any{
						TransformerMetadataKeyAnthropicType: "server_tool_use",
					},
				}},
			},
			expectedStopReason: "end_turn",
		},
		{
			name: "server tool use multiple content block",
			delta: &llm.Message{
				Role: "assistant",
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{{
						Type:        "server_tool_use",
						ServerBlock: serverToolUseRaw,
					}},
				},
			},
			expectedStopReason:       "end_turn",
			expectedContentBlockType: "server_tool_use",
		},
		{
			name: "tool search result multiple content block",
			delta: &llm.Message{
				Role: "assistant",
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{{
						Type:        "tool_search_tool_result",
						ServerBlock: toolSearchResultRaw,
					}},
				},
			},
			expectedStopReason:       "end_turn",
			expectedContentBlockType: "tool_search_tool_result",
		},
		{
			name: "mcp tool call",
			delta: &llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					Index: 0,
					ID:    "mcp_tool_123",
					Type:  "function",
					Function: llm.FunctionCall{
						Name:      "mcp_search",
						Arguments: `{"query":"test"}`,
					},
					TransformerMetadata: map[string]any{
						TransformerMetadataKeyAnthropicType: "mcp_tool_use",
					},
				}},
			},
			expectedStopReason: "end_turn",
		},
		{
			name: "text",
			delta: &llm.Message{
				Role: "assistant",
				Content: llm.MessageContent{
					Content: &text,
				},
			},
			expectedStopReason: "end_turn",
		},
		{
			name: "thinking",
			delta: &llm.Message{
				Role:             "assistant",
				ReasoningContent: &thinking,
			},
			expectedStopReason: "end_turn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []*llm.Response{
				{
					ID:     "msg_missing_finish_reason",
					Object: "chat.completion.chunk",
					Model:  "claude-sonnet-4-6",
					Choices: []llm.Choice{{
						Index: 0,
						Delta: tt.delta,
					}},
				},
				{
					ID:      "msg_missing_finish_reason",
					Object:  "chat.completion.chunk",
					Model:   "claude-sonnet-4-6",
					Choices: []llm.Choice{{Index: 0}},
					Usage:   tt.usage,
				},
			}

			events := collectInboundStreamEvents(t, transformer, input)
			require.GreaterOrEqual(t, len(events), 3)
			if tt.expectedContentBlockType != "" {
				require.Contains(t, lo.Map(events, func(event StreamEvent, _ int) string {
					if event.ContentBlock == nil {
						return ""
					}
					return event.ContentBlock.Type
				}), tt.expectedContentBlockType)
			}

			terminalEvents := events[len(events)-3:]
			require.Equal(t, "content_block_stop", terminalEvents[0].Type)
			require.Equal(t, "message_delta", terminalEvents[1].Type)
			require.NotNil(t, terminalEvents[1].Delta)
			require.Equal(t, tt.expectedStopReason, *terminalEvents[1].Delta.StopReason)
			require.NotNil(t, terminalEvents[1].Usage)
			if tt.usage == nil {
				require.Zero(t, terminalEvents[1].Usage.OutputTokens)
			} else {
				require.Equal(t, int64(7), terminalEvents[1].Usage.OutputTokens)
			}
			require.Equal(t, "message_stop", terminalEvents[2].Type)
		})
	}
}

func TestInboundStream_RejectsIncompleteToolArgumentsOnCleanExhaustion(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
	}{
		{name: "truncated object", arguments: `{"content":"unfinished`},
		{name: "null", arguments: `null`},
		{name: "array", arguments: `[]`},
		{name: "string", arguments: `"value"`},
		{name: "number", arguments: `1`},
		{name: "boolean", arguments: `true`},
		{name: "whitespace", arguments: ` `},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformer := NewInboundTransformer()
			input := []*llm.Response{{
				ID:     "msg_incomplete_tool",
				Object: "chat.completion.chunk",
				Model:  "test-model",
				Choices: []llm.Choice{{
					Index: 0,
					Delta: &llm.Message{
						Role: "assistant",
						ToolCalls: []llm.ToolCall{{
							Index: 0,
							ID:    "call_incomplete",
							Type:  "function",
							Function: llm.FunctionCall{
								Name:      "write",
								Arguments: tt.arguments,
							},
						}},
					},
				}},
			}}

			stream, err := transformer.TransformStream(t.Context(), streams.SliceStream(input))
			require.NoError(t, err)

			var events []StreamEvent
			for stream.Next() {
				var event StreamEvent
				require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
				events = append(events, event)
			}

			require.ErrorContains(t, stream.Err(), "invalid tool call arguments")
			require.Zero(t, countStreamEvents(events, "content_block_stop"))
			require.Zero(t, countStreamEvents(events, "message_delta"))
			require.Zero(t, countStreamEvents(events, "message_stop"))
		})
	}
}

func TestInboundStream_PreservesExplicitFinishReasonWithMalformedToolArguments(t *testing.T) {
	transformer := NewInboundTransformer()
	finishReason := "tool_calls"
	input := []*llm.Response{
		{
			ID:     "msg_incomplete_tool",
			Object: "chat.completion.chunk",
			Model:  "test-model",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{{
						Index: 0,
						ID:    "call_incomplete",
						Type:  "function",
						Function: llm.FunctionCall{
							Name:      "write",
							Arguments: `{"content":"unfinished`,
						},
					}},
				},
			}},
		},
		{
			ID:     "msg_incomplete_tool",
			Object: "chat.completion.chunk",
			Model:  "test-model",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &finishReason,
			}},
			Usage: &llm.Usage{CompletionTokens: 10},
		},
	}

	stream, err := transformer.TransformStream(t.Context(), streams.SliceStream(input))
	require.NoError(t, err)

	var events []StreamEvent
	for stream.Next() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
		events = append(events, event)
	}

	require.NoError(t, stream.Err())
	require.Equal(t, 1, countStreamEvents(events, "content_block_stop"))
	require.Equal(t, 1, countStreamEvents(events, "message_delta"))
	require.Equal(t, 1, countStreamEvents(events, "message_stop"))
	for _, event := range events {
		if event.Type == "message_delta" {
			require.NotNil(t, event.Delta)
			require.NotNil(t, event.Delta.StopReason)
			require.Equal(t, "tool_use", *event.Delta.StopReason)
			require.NotNil(t, event.Usage)
			require.Equal(t, int64(10), event.Usage.OutputTokens)
		}
	}
}

func TestInboundStream_RejectsInvalidEarlierReadArgumentsOnCleanExhaustion(t *testing.T) {
	transformer := NewInboundTransformer()
	input := []*llm.Response{{
		ID:     "msg_multiple_tools",
		Object: "chat.completion.chunk",
		Model:  "test-model",
		Choices: []llm.Choice{{
			Index: 0,
			Delta: &llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						Index: 0,
						ID:    "call_invalid",
						Type:  "function",
						Function: llm.FunctionCall{
							Name:      "Read",
							Arguments: `{"content":"unfinished`,
						},
					},
					{
						Index: 1,
						ID:    "call_valid",
						Type:  "function",
						Function: llm.FunctionCall{
							Name:      "bash",
							Arguments: `{"command":"true"}`,
						},
					},
				},
			},
		}},
	}}

	stream, err := transformer.TransformStream(t.Context(), streams.SliceStream(input))
	require.NoError(t, err)

	var events []StreamEvent
	for stream.Next() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
		events = append(events, event)
	}

	require.ErrorContains(t, stream.Err(), "invalid tool call arguments")
	require.Zero(t, countStreamEvents(events, "message_delta"))
	require.Zero(t, countStreamEvents(events, "message_stop"))
}

func TestInboundStream_PreservesExplicitFinishReasonWithoutUsage(t *testing.T) {
	transformer := NewInboundTransformer()
	finishReason := "tool_calls"
	input := []*llm.Response{
		{
			ID:     "msg_incomplete_tool",
			Object: "chat.completion.chunk",
			Model:  "test-model",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{{
						Index: 0,
						ID:    "call_incomplete",
						Type:  "function",
						Function: llm.FunctionCall{
							Name:      "write",
							Arguments: `{"content":"unfinished`,
						},
					}},
				},
			}},
		},
		{
			ID:     "msg_incomplete_tool",
			Object: "chat.completion.chunk",
			Model:  "test-model",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &finishReason,
			}},
		},
	}

	stream, err := transformer.TransformStream(t.Context(), streams.SliceStream(input))
	require.NoError(t, err)

	var events []StreamEvent
	for stream.Next() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
		events = append(events, event)
	}

	require.NoError(t, stream.Err())
	require.Equal(t, 1, countStreamEvents(events, "message_delta"))
	require.Equal(t, 1, countStreamEvents(events, "message_stop"))
	for _, event := range events {
		if event.Type == "message_delta" {
			require.NotNil(t, event.Delta)
			require.NotNil(t, event.Delta.StopReason)
			require.Equal(t, "tool_use", *event.Delta.StopReason)
			require.NotNil(t, event.Usage)
			require.Zero(t, event.Usage.OutputTokens)
		}
	}
}

func TestInboundStream_DoesNotDuplicateTerminalEvents(t *testing.T) {
	transformer := NewInboundTransformer()
	text := "Done"
	finishReason := "stop"

	events := collectInboundStreamEvents(t, transformer, []*llm.Response{
		{
			ID:     "msg_with_finish_reason",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: &text,
					},
				},
			}},
		},
		{
			ID:     "msg_with_finish_reason",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &finishReason,
			}},
			Usage: &llm.Usage{},
		},
	})

	require.Equal(t, 1, countStreamEvents(events, "message_delta"))
	require.Equal(t, 1, countStreamEvents(events, "message_stop"))
}

func TestInboundStream_DoesNotFinalizeOnSourceError(t *testing.T) {
	transformer := NewInboundTransformer()
	text := "Partial"
	sourceErr := errors.New("upstream stream failed")
	source := &failingResponseStream{
		items: []*llm.Response{{
			ID:     "msg_stream_error",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: &text,
					},
				},
			}},
		}},
		err: sourceErr,
	}

	stream, err := transformer.TransformStream(t.Context(), source)
	require.NoError(t, err)

	var events []StreamEvent
	for stream.Next() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
		events = append(events, event)
	}

	require.ErrorIs(t, stream.Err(), sourceErr)
	require.Zero(t, countStreamEvents(events, "message_delta"))
	require.Zero(t, countStreamEvents(events, "message_stop"))
}

func countStreamEvents(events []StreamEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}

	return count
}

func TestInboundStream_EmitsCitationsDeltaBeforeContentBlockStop(t *testing.T) {
	transformer := NewInboundTransformer()

	text := "Annotated answer"
	stop := "stop"

	input := []*llm.Response{
		{
			ID:     "msg_annotations_stream",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: &text,
					},
					Annotations: []llm.Annotation{
						{
							Type: "url_citation",
							URLCitation: &llm.URLCitation{
								URL:   "https://example.com/1",
								Title: "Source One",
							},
						},
						{
							Type: "url_citation",
							URLCitation: &llm.URLCitation{
								URL:   "https://example.com/2",
								Title: "Source Two",
							},
						},
					},
				},
			}},
		},
		{
			ID:     "msg_annotations_stream",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &stop,
			}},
			Usage: &llm.Usage{},
		},
	}

	events := collectInboundStreamEvents(t, transformer, input)

	assertCitationsDeltaBeforeContentBlockStop(t, events, []TextCitation{
		{
			Type:  "url_citation",
			URL:   "https://example.com/1",
			Title: "Source One",
		},
		{
			Type:  "url_citation",
			URL:   "https://example.com/2",
			Title: "Source Two",
		},
	})
}

func TestInboundStream_RemovesEmptyReadPages(t *testing.T) {
	transformer := NewInboundTransformer()
	finishReason := "tool_calls"

	input := []*llm.Response{
		{
			ID:     "msg_read_pages_stream",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
				},
			}},
		},
		{
			ID:     "msg_read_pages_stream",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{{
						Index: 0,
						ID:    "call_read",
						Type:  "function",
						Function: llm.FunctionCall{
							Name:      "Read",
							Arguments: `{"file_path":`,
						},
					}},
				},
			}},
		},
		{
			ID:     "msg_read_pages_stream",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{{
						Index: 0,
						Function: llm.FunctionCall{
							Arguments: `"/tmp/a.go","pages":""}`,
						},
					}},
				},
			}},
		},
		{
			ID:     "msg_read_pages_stream",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &finishReason,
			}},
			Usage: &llm.Usage{},
		},
	}

	events := collectInboundStreamEvents(t, transformer, input)

	var argumentDeltas []string
	for _, event := range events {
		if event.Type == "content_block_delta" && event.Delta != nil &&
			event.Delta.Type != nil && *event.Delta.Type == "input_json_delta" &&
			event.Delta.PartialJSON != nil {
			argumentDeltas = append(argumentDeltas, *event.Delta.PartialJSON)
		}
	}

	require.Len(t, argumentDeltas, 1)
	require.JSONEq(t, `{"file_path":"/tmp/a.go"}`, argumentDeltas[0])
}

func TestInboundStream_EmitsCitationsDeltaWhenAnnotationsArriveBeforeText(t *testing.T) {
	transformer := NewInboundTransformer()

	text := "Annotated after metadata"
	stop := "stop"

	input := []*llm.Response{
		{
			ID:     "msg_annotations_before_text",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					Annotations: []llm.Annotation{
						{
							Type: "url_citation",
							URLCitation: &llm.URLCitation{
								URL:   "https://example.com/metadata-first",
								Title: "Metadata First",
							},
						},
					},
				},
			}},
		},
		{
			ID:     "msg_annotations_before_text",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: &text,
					},
				},
			}},
		},
		{
			ID:     "msg_annotations_before_text",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &stop,
			}},
			Usage: &llm.Usage{},
		},
	}

	events := collectInboundStreamEvents(t, transformer, input)

	assertCitationsDeltaBeforeContentBlockStop(t, events, []TextCitation{{
		Type:  "url_citation",
		URL:   "https://example.com/metadata-first",
		Title: "Metadata First",
	}})
}

func TestInboundStream_EmitsCitationsDeltaFromChoiceMessageAnnotations(t *testing.T) {
	transformer := NewInboundTransformer()

	text := "Annotated via choice.message"
	stop := "stop"

	input := []*llm.Response{
		{
			ID:     "msg_choice_message_annotations",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index: 0,
				Message: &llm.Message{
					Annotations: []llm.Annotation{
						{
							Type: "url_citation",
							URLCitation: &llm.URLCitation{
								URL:   "https://example.com/message-annotations",
								Title: "Message Annotation",
							},
						},
					},
				},
				Delta: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: &text,
					},
				},
			}},
		},
		{
			ID:     "msg_choice_message_annotations",
			Object: "chat.completion.chunk",
			Model:  "claude-sonnet-4-6",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &stop,
			}},
			Usage: &llm.Usage{},
		},
	}

	events := collectInboundStreamEvents(t, transformer, input)

	assertCitationsDeltaBeforeContentBlockStop(t, events, []TextCitation{{
		Type:  "url_citation",
		URL:   "https://example.com/message-annotations",
		Title: "Message Annotation",
	}})
}

func collectInboundStreamEvents(t *testing.T, transformer *InboundTransformer, input []*llm.Response) []StreamEvent {
	t.Helper()

	stream, err := transformer.TransformStream(t.Context(), streams.SliceStream(input))
	require.NoError(t, err)

	var events []StreamEvent
	for stream.Next() {
		raw := stream.Current()
		var event StreamEvent
		err := json.Unmarshal(raw.Data, &event)
		require.NoError(t, err)
		events = append(events, event)
	}
	require.NoError(t, stream.Err())

	return events
}

func assertCitationsDeltaBeforeContentBlockStop(t *testing.T, events []StreamEvent, expected []TextCitation) {
	t.Helper()

	var (
		citationEventIndexes  []int
		contentBlockStopIndex = -1
		actualCitations       []TextCitation
	)

	for i, event := range events {
		if event.Type == "content_block_delta" && event.Delta != nil && event.Delta.Type != nil && *event.Delta.Type == "citations_delta" {
			citationEventIndexes = append(citationEventIndexes, i)
			require.NotNil(t, event.Delta.Citation)
			actualCitations = append(actualCitations, *event.Delta.Citation)
			require.Nil(t, event.Delta.Citation.EncryptedIndex)
			require.Nil(t, event.Delta.Citation.CitedText)
		}
		if event.Type == "content_block_stop" && event.Index != nil && *event.Index == 0 && contentBlockStopIndex == -1 {
			contentBlockStopIndex = i
		}
	}

	require.Len(t, citationEventIndexes, len(expected))
	require.NotEqual(t, -1, contentBlockStopIndex)
	for _, idx := range citationEventIndexes {
		require.Less(t, idx, contentBlockStopIndex)
	}
	require.Equal(t, expected, actualCitations)
}

func TestInboundStream_NormalizesOpenAIWebSearchCitationTypeFromChunkMetadata(t *testing.T) {
	transformer := NewInboundTransformer()

	text := "Annotated answer"
	stop := "stop"

	input := []*llm.Response{
		{
			ID:     "msg_annotations_stream_web_search",
			Object: "chat.completion.chunk",
			Model:  "gpt-4o-search-preview",
			TransformerMetadata: map[string]any{
				"openai_responses_web_search_calls": []map[string]any{{
					"id":     "ws_123",
					"type":   "web_search_call",
					"status": "completed",
					"action": map[string]any{
						"type":  "search",
						"query": "latest ai news",
					},
				}},
			},
			Choices: []llm.Choice{{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: &text,
					},
					Annotations: []llm.Annotation{{
						Type: "url_citation",
						URLCitation: &llm.URLCitation{
							URL:   "https://example.com/result",
							Title: "Example Result",
						},
					}},
				},
			}},
		},
		{
			ID:     "msg_annotations_stream_web_search",
			Object: "chat.completion.chunk",
			Model:  "gpt-4o-search-preview",
			Choices: []llm.Choice{{
				Index:        0,
				FinishReason: &stop,
			}},
			Usage: &llm.Usage{},
		},
	}

	events := collectInboundStreamEvents(t, transformer, input)

	assertCitationsDeltaBeforeContentBlockStop(t, events, []TextCitation{{
		Type:  "web_search_result_location",
		URL:   "https://example.com/result",
		Title: "Example Result",
	}})
}

func TestInboundTransformer_StreamTransformation_ToolSearchServerBlocks(t *testing.T) {
	transformer := NewInboundTransformer()

	serverToolUseRaw := json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_01ABC123","name":"tool_search_tool_regex","input":{"query":"weather"}}`)
	toolSearchResultRaw := json.RawMessage(`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_01ABC123","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"get_weather"}]}}`)

	mockStream := streams.SliceStream([]*llm.Response{
		{
			ID:    "msg_tool_search",
			Model: "claude-opus-4-7",
			Choices: []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						Role: "assistant",
						Content: llm.MessageContent{
							MultipleContent: []llm.MessageContentPart{
								{
									Type:        "server_tool_use",
									ServerBlock: serverToolUseRaw,
								},
								{
									Type:        "tool_search_tool_result",
									ServerBlock: toolSearchResultRaw,
								},
							},
						},
					},
					FinishReason: lo.ToPtr("tool_calls"),
				},
			},
			Usage: &llm.Usage{
				PromptTokens:     12,
				CompletionTokens: 4,
				TotalTokens:      16,
			},
		},
	})

	transformedStream, err := transformer.TransformStream(t.Context(), mockStream)
	require.NoError(t, err)

	var actualEvents []*httpclient.StreamEvent
	for transformedStream.Next() {
		actualEvents = append(actualEvents, transformedStream.Current())
	}
	require.NoError(t, transformedStream.Err())

	var contentStarts []*StreamEvent
	var contentDeltas []*StreamEvent
	for _, event := range actualEvents {
		var streamEvent StreamEvent
		err := json.Unmarshal(event.Data, &streamEvent)
		require.NoError(t, err)

		switch streamEvent.Type {
		case "content_block_start":
			contentStarts = append(contentStarts, &streamEvent)
		case "content_block_delta":
			contentDeltas = append(contentDeltas, &streamEvent)
		}
	}

	require.Len(t, contentStarts, 2)
	require.Equal(t, "server_tool_use", contentStarts[0].ContentBlock.Type)
	require.Equal(t, "tool_search_tool_result", contentStarts[1].ContentBlock.Type)

	require.Len(t, contentDeltas, 2)
	require.Equal(t, "input_json_delta", *contentDeltas[0].Delta.Type)
	require.JSONEq(t, `{"query":"weather"}`, *contentDeltas[0].Delta.PartialJSON)
	require.Equal(t, "input_json_delta", *contentDeltas[1].Delta.Type)
	require.JSONEq(t, `{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"get_weather"}]}`, *contentDeltas[1].Delta.PartialJSON)

	aggregatedBytes, _, err := transformer.AggregateStreamChunks(t.Context(), actualEvents)
	require.NoError(t, err)

	var aggregatedResp Message
	err = json.Unmarshal(aggregatedBytes, &aggregatedResp)
	require.NoError(t, err)

	require.Len(t, aggregatedResp.Content, 2)
	require.Equal(t, "server_tool_use", aggregatedResp.Content[0].Type)
	require.Equal(t, "tool_search_tool_result", aggregatedResp.Content[1].Type)
	require.JSONEq(t, `{"query":"weather"}`, string(aggregatedResp.Content[0].Input))

	var toolSearchPayload map[string]json.RawMessage
	err = json.Unmarshal(aggregatedResp.Content[1].ServerContent, &toolSearchPayload)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"get_weather"}]}`, string(toolSearchPayload["content"]))
}

func TestInboundTransformer_StreamTransformation_WithTestData(t *testing.T) {
	transformer := NewInboundTransformer()

	tests := []struct {
		name                string
		inputStreamFile     string
		expectedInputTokens int64
		expectedStreamFile  string
		expectedAggregated  func(t *testing.T, result *Message)
	}{
		{
			name:                "stream transformation with stop finish reason",
			inputStreamFile:     "llm-stop.stream.jsonl",
			expectedStreamFile:  "anthropic-stop.stream.jsonl",
			expectedInputTokens: 21,
			expectedAggregated: func(t *testing.T, result *Message) {
				t.Helper()
				// Verify aggregated response
				require.Equal(t, "msg_bdrk_01Fbg5HKuVfmtT6mAMxQoCSn", result.ID)
				require.Equal(t, "message", result.Type)
				require.Equal(t, "claude-3-7-sonnet-20250219", result.Model)
				require.NotEmpty(t, result.Content)
				require.Equal(t, "assistant", result.Role)

				// Verify the complete content
				expectedContent := "1 2 3 4 5\n6 7 8 9 10\n11 12 13 14 15\n16 17 18 19 20"
				require.Equal(t, expectedContent, *result.Content[0].Text)
			},
		},
		{
			name:                "stream transformation with parallel multiple tool calls",
			inputStreamFile:     "llm-parallel_multiple_tool.stream.jsonl",
			expectedStreamFile:  "anthropic-parallel_multiple_tool.stream.jsonl",
			expectedInputTokens: 104,
			expectedAggregated: func(t *testing.T, result *Message) {
				t.Helper()
				// Verify aggregated response
				require.Equal(t, "chatcmpl-C2WBYGbjjGZj4CJNJI1FSlzO8U4vj", result.ID)
				require.Equal(t, "message", result.Type)
				require.Equal(t, "gpt-4o-2024-11-20", result.Model)
				require.NotEmpty(t, result.Content)
				require.Equal(t, "assistant", result.Role)
				require.Equal(t, "tool_use", *result.StopReason)

				// Verify we have 2 tool use content blocks
				require.Len(t, result.Content, 2)

				// Verify first tool call (get_user_city)
				require.Equal(t, "tool_use", result.Content[0].Type)
				require.Equal(t, "call_tooG2dAMZaICWBfsYU5LYyvs", result.Content[0].ID)
				require.Equal(t, "get_user_city", *result.Content[0].Name)

				var cityInput map[string]any

				err := json.Unmarshal(result.Content[0].Input, &cityInput)
				require.NoError(t, err)
				require.Equal(t, "123", cityInput["user_id"])

				// Verify second tool call (get_user_language)
				require.Equal(t, "tool_use", result.Content[1].Type)
				require.Equal(t, "call_Ul0yUvKCpLfl5c32FHPcASEB", result.Content[1].ID)
				require.Equal(t, "get_user_language", *result.Content[1].Name)

				var langInput map[string]any

				err = json.Unmarshal(result.Content[1].Input, &langInput)
				require.NoError(t, err)
				require.Equal(t, "123", langInput["user_id"])
			},
		},
		{
			name:                "stream transformation with thinking content and parallel tool calls",
			inputStreamFile:     "llm-think.stream.jsonl",
			expectedStreamFile:  "anthropic-think.stream.jsonl",
			expectedInputTokens: 587,
			expectedAggregated: func(t *testing.T, result *Message) {
				t.Helper()
				// Verify aggregated response
				require.Equal(t, "msg_bdrk_01DDaPSX8bJqM5dRkdv32TkC", result.ID)
				require.Equal(t, "message", result.Type)
				require.Equal(t, "claude-sonnet-4-20250514", result.Model)
				require.NotEmpty(t, result.Content)
				require.Equal(t, "assistant", result.Role)
				require.Equal(t, "tool_use", *result.StopReason)

				// Verify we have 4 content blocks: thinking, text, and 2 tool uses
				require.Len(t, result.Content, 4)

				// Verify thinking content block
				require.Equal(t, "thinking", result.Content[0].Type)

				expectedThinking := "The user is asking for the weather in San Francisco, CA. To get the weather, I need to:\n\n1. First get the coordinates (latitude and longitude) of San Francisco, CA using the get_coordinates function\n2. Then get the temperature unit for the US using get_temperature_unit function \n3. Finally use the get_weather function with the coordinates and appropriate unit\n\nLet me start with getting the coordinates and temperature unit."
				require.Equal(t, expectedThinking, *result.Content[0].Thinking)

				// Verify text content block
				require.Equal(t, "text", result.Content[1].Type)

				expectedText := "I'll help you get the weather for San Francisco, CA. Let me first get the coordinates and determine the appropriate temperature unit for the US."
				require.Equal(t, expectedText, *result.Content[1].Text)

				// Verify first tool call (get_coordinates)
				require.Equal(t, "tool_use", result.Content[2].Type)
				require.Equal(t, "toolu_bdrk_01RjxXDSvxn69XRfWLjn6Sur", result.Content[2].ID)
				require.Equal(t, "get_coordinates", *result.Content[2].Name)

				var coordInput map[string]any

				err := json.Unmarshal(result.Content[2].Input, &coordInput)
				require.NoError(t, err)
				require.Equal(t, "San Francisco, CA", coordInput["location"])

				// Verify second tool call (get_temperature_unit)
				require.Equal(t, "tool_use", result.Content[3].Type)
				require.Equal(t, "toolu_bdrk_01E6Gr52e4i9TLwsDn8Sgimg", result.Content[3].ID)
				require.Equal(t, "get_temperature_unit", *result.Content[3].Name)

				var unitInput map[string]any

				err = json.Unmarshal(result.Content[3].Input, &unitInput)
				require.NoError(t, err)
				require.Equal(t, "United States", unitInput["country"])
			},
		},
		{
			name:                "stream transformation with OpenRouter content and tool call",
			inputStreamFile:     "or-tool.stream.jsonl",
			expectedStreamFile:  "anthropic-or-tool.stream.jsonl",
			expectedInputTokens: 18810,
			expectedAggregated: func(t *testing.T, result *Message) {
				t.Helper()
				// Verify aggregated response
				require.Equal(t, "gen-1761365834-YlzLUYrcuUQ4OsjtP1qS", result.ID)
				require.Equal(t, "message", result.Type)
				require.Equal(t, "minimax/minimax-m2:free", result.Model)
				require.NotEmpty(t, result.Content)
				require.Equal(t, "assistant", result.Role)
				require.Equal(t, "tool_use", *result.StopReason)

				// Verify we have 2 content blocks: text, and 1 tool use
				require.Len(t, result.Content, 2)

				// Verify text content block
				require.Equal(t, "text", result.Content[0].Type)
				require.Contains(t, *result.Content[0].Text, "代码Review结果")

				// Verify tool call (TodoWrite)
				require.Equal(t, "tool_use", result.Content[1].Type)
				require.Equal(t, "call_function_6091710012_1", result.Content[1].ID)
				require.Equal(t, "TodoWrite", *result.Content[1].Name)

				var toolInput map[string]any

				err := json.Unmarshal(result.Content[1].Input, &toolInput)
				require.NoError(t, err)
				require.NotNil(t, toolInput["todos"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The input file contains OpenAI format responses
			openaiResponses, err := xtest.LoadLlmResponses(t, tt.inputStreamFile)
			require.NoError(t, err)

			// The expected file contains expected Anthropic format events
			expectedEvents, err := xtest.LoadStreamChunks(t, tt.expectedStreamFile)
			require.NoError(t, err)

			// Create a mock stream from OpenAI responses
			mockStream := streams.SliceStream(openaiResponses)

			// Transform the stream (OpenAI -> Anthropic)
			transformedStream, err := transformer.TransformStream(t.Context(), mockStream)
			require.NoError(t, err)

			// Collect all transformed events
			var actualEvents []*httpclient.StreamEvent

			for transformedStream.Next() {
				event := transformedStream.Current()
				actualEvents = append(actualEvents, event)
			}

			require.NoError(t, transformedStream.Err())

			// Verify the number of events matches
			require.Equal(t, len(expectedEvents), len(actualEvents), "Number of events should match")

			// Verify each event
			for i, expected := range expectedEvents {
				actual := actualEvents[i]

				// Verify event type
				require.Equal(t, expected.Type, actual.Type, "Event %d: Type should match", i)

				// Parse and compare event data
				var expectedStreamEvent StreamEvent

				err := json.Unmarshal(expected.Data, &expectedStreamEvent)
				require.NoError(t, err)

				var actualStreamEvent StreamEvent

				err = json.Unmarshal(actual.Data, &actualStreamEvent)
				require.NoError(t, err)

				// Verify stream event type
				require.Equal(
					t,
					expectedStreamEvent.Type,
					actualStreamEvent.Type,
					"Event %d: Stream event type should match, expected: %v, actual: %v",
					i,
					string(xjson.MustMarshal(expectedStreamEvent)),
					string(xjson.MustMarshal(actualStreamEvent)),
				)

				// Verify specific fields based on event type
				switch expectedStreamEvent.Type {
				case "message_start":
					require.NotNil(t, expectedStreamEvent.Message)
					require.NotNil(t, actualStreamEvent.Message)
					require.Equal(t, expectedStreamEvent.Message.ID, actualStreamEvent.Message.ID, "Event %d: Message ID should match", i)
					require.Equal(t, expectedStreamEvent.Message.Model, actualStreamEvent.Message.Model, "Event %d: Model should match", i)
					require.Equal(t, expectedStreamEvent.Message.Role, actualStreamEvent.Message.Role, "Event %d: Role should match", i)

					if expectedStreamEvent.Message.Usage != nil && actualStreamEvent.Message.Usage != nil {
						require.Equal(
							t,
							expectedStreamEvent.Message.Usage.InputTokens,
							actualStreamEvent.Message.Usage.InputTokens,
							"Event %d: Input tokens should match",
							i,
						)
						require.Equal(
							t,
							expectedStreamEvent.Message.Usage.OutputTokens,
							actualStreamEvent.Message.Usage.OutputTokens,
							"Event %d: Output tokens should match",
							i,
						)
					}

				case "content_block_start":
					require.NotNil(t, expectedStreamEvent.ContentBlock)
					require.NotNil(t, actualStreamEvent.ContentBlock)
					require.Equal(t, expectedStreamEvent.ContentBlock.Type, actualStreamEvent.ContentBlock.Type, "Event %d: Content block type should match", i)

					// Additional validation for tool_use content blocks
					if expectedStreamEvent.ContentBlock.Type == "tool_use" {
						require.Equal(t, expectedStreamEvent.ContentBlock.ID, actualStreamEvent.ContentBlock.ID, "Event %d: Tool use ID should match", i)

						if expectedStreamEvent.ContentBlock.Name != nil && actualStreamEvent.ContentBlock.Name != nil {
							require.Equal(
								t,
								*expectedStreamEvent.ContentBlock.Name,
								*actualStreamEvent.ContentBlock.Name,
								"Event %d: Tool use name should match",
								i,
							)
						}
					}

				case "content_block_delta":
					require.NotNil(t, expectedStreamEvent.Delta)
					require.NotNil(t, actualStreamEvent.Delta)

					if !xtest.Equal(expectedStreamEvent.Delta, actualStreamEvent.Delta, cmpopts.IgnoreFields(StreamDelta{}, "Signature")) {
						t.Errorf("Index: %d, Diff: %s ", i, cmp.Diff(expectedStreamEvent.Delta, actualStreamEvent.Delta))
					}

				case "content_block_stop":
					require.Equal(
						t,
						expectedStreamEvent.Index,
						actualStreamEvent.Index,
						"Event %d: Index should match, expected: %v, actual: %v",
						i,
						*expectedStreamEvent.Index,
						*actualStreamEvent.Index,
					)

				case "message_delta":
					require.NotNil(t, expectedStreamEvent.Delta)
					require.NotNil(t, actualStreamEvent.Delta)

					require.Equal(t, expectedStreamEvent.Delta.StopReason, actualStreamEvent.Delta.StopReason)

					if !xtest.Equal(expectedStreamEvent.Delta, actualStreamEvent.Delta, cmpopts.IgnoreFields(StreamDelta{}, "Signature")) {
						t.Errorf("Index: %d, Diff: %s ", i, cmp.Diff(expectedStreamEvent.Delta, actualStreamEvent.Delta))
					}

					if expectedStreamEvent.Usage != nil && actualStreamEvent.Usage != nil {
						// Aggregate input tokens from the message_start event.
						require.Equal(t, tt.expectedInputTokens, actualStreamEvent.Usage.InputTokens, "Event %d: Usage input tokens should match", i)
						require.Equal(
							t,
							expectedStreamEvent.Usage.OutputTokens,
							actualStreamEvent.Usage.OutputTokens,
							"Event %d: Usage output tokens should match",
							i,
						)
					}

				case "message_stop":
					// No specific fields to verify for message_stop
				}
			}

			// Test aggregation
			aggregatedBytes, _, err := transformer.AggregateStreamChunks(t.Context(), actualEvents)
			require.NoError(t, err)

			var aggregatedResp Message

			err = json.Unmarshal(aggregatedBytes, &aggregatedResp)
			require.NoError(t, err)

			// Run custom validation if provided
			if tt.expectedAggregated != nil {
				tt.expectedAggregated(t, &aggregatedResp)
			}
		})
	}
}
