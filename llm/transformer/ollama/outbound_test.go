package ollama

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestTransformRequestPreservesImageURLParts(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	req, err := transformer.TransformRequest(context.Background(), &llm.Request{
		Model: "qwen3.5:4b-q4_K_M",
		Messages: []llm.Message{
			{
				Role: "user",
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{
						{
							Type: "text",
							Text: lo.ToPtr("OCR this image."),
						},
						{
							Type: "image_url",
							ImageURL: &llm.ImageURL{
								URL: "data:image/png;base64,Zm9v",
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	var got ChatRequest
	require.NoError(t, json.Unmarshal(req.Body, &got))
	require.Len(t, got.Messages, 1)
	require.Equal(t, "OCR this image.", got.Messages[0].Content)
	require.Equal(t, []string{"Zm9v"}, got.Messages[0].Images)
}

func TestTransformRequestWithTools(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	tools := []llm.Tool{
		{
			Type: llm.ToolTypeFunction,
			Function: llm.Function{
				Name:        "get_current_weather",
				Description: "Get the current weather for a location",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"},"format":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location","format"]}`),
			},
		},
	}

	req, err := transformer.TransformRequest(context.Background(), &llm.Request{
		Model:  "qwen3",
		Tools:  tools,
		Stream: lo.ToPtr(false),
		Messages: []llm.Message{
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: lo.ToPtr("What is the weather in Paris?"),
				},
			},
		},
	})
	require.NoError(t, err)

	var got ChatRequest
	require.NoError(t, json.Unmarshal(req.Body, &got))
	require.Len(t, got.Tools, 1)
	require.Equal(t, "function", got.Tools[0].Type)
	require.Equal(t, "get_current_weather", got.Tools[0].Function.Name)
}

func TestTransformRequestWithMessageToolCalls(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	req, err := transformer.TransformRequest(context.Background(), &llm.Request{
		Model: "qwen3",
		Messages: []llm.Message{
			{
				Role: "assistant",
				Content: llm.MessageContent{
					Content: lo.ToPtr(""),
				},
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "get_current_weather",
							Arguments: `{"location":"Paris","format":"celsius"}`,
						},
						Index: 0,
					},
				},
			},
			{
				Role:       "tool",
				Content:    llm.MessageContent{Content: lo.ToPtr("22°C")},
				ToolCallID: lo.ToPtr("call-1"),
			},
		},
	})
	require.NoError(t, err)

	var got ChatRequest
	require.NoError(t, json.Unmarshal(req.Body, &got))
	require.Len(t, got.Messages, 2)
	require.Len(t, got.Messages[0].ToolCalls, 1)
	require.Equal(t, "get_current_weather", got.Messages[0].ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"location":"Paris","format":"celsius"}`, string(got.Messages[0].ToolCalls[0].Function.Arguments))
}

func TestTransformRequestWithMessageToolCallsEmptyArguments(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	req, err := transformer.TransformRequest(context.Background(), &llm.Request{
		Model: "qwen3",
		Messages: []llm.Message{
			{
				Role: "assistant",
				Content: llm.MessageContent{
					Content: lo.ToPtr(""),
				},
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "noop",
							Arguments: "",
						},
						Index: 0,
					},
				},
			},
		},
	})
	require.NoError(t, err)

	var got ChatRequest
	require.NoError(t, json.Unmarshal(req.Body, &got))
	require.Len(t, got.Messages, 1)
	require.Len(t, got.Messages[0].ToolCalls, 1)
	require.Equal(t, "noop", got.Messages[0].ToolCalls[0].Function.Name)
}

func TestToolCallArgumentsPreserveLargeInteger(t *testing.T) {
	const arguments = `{"request_id":9007199254740993}`
	const eventData = `{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"lookup","arguments":` + arguments + `}}]},"done":true}`

	t.Run("request", func(t *testing.T) {
		transformer, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)

		req, err := transformer.TransformRequest(t.Context(), &llm.Request{
			Model: "qwen3",
			Messages: []llm.Message{{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					Type:     "function",
					Function: llm.FunctionCall{Name: "lookup", Arguments: arguments},
				}},
			}},
		})
		require.NoError(t, err)

		var got ChatRequest
		require.NoError(t, json.Unmarshal(req.Body, &got))
		require.Equal(t, arguments, string(got.Messages[0].ToolCalls[0].Function.Arguments))
	})

	t.Run("response", func(t *testing.T) {
		transformer, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)

		resp, err := transformer.TransformResponse(t.Context(), &httpclient.Response{Body: []byte(eventData)})
		require.NoError(t, err)
		require.Equal(t, arguments, resp.Choices[0].Message.ToolCalls[0].Function.Arguments)
	})

	t.Run("stream chunk", func(t *testing.T) {
		outbound, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)
		transformer := outbound.(*OutboundTransformer)

		resp, err := transformer.TransformStreamChunk(t.Context(), &httpclient.StreamEvent{Data: []byte(eventData)})
		require.NoError(t, err)
		require.Equal(t, arguments, resp.Choices[0].Delta.ToolCalls[0].Function.Arguments)
	})

	t.Run("stream aggregate", func(t *testing.T) {
		outbound, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)
		transformer := outbound.(*OutboundTransformer)

		body, _, err := transformer.AggregateStreamChunks(t.Context(), nil, []*httpclient.StreamEvent{{Data: []byte(eventData)}})
		require.NoError(t, err)

		var got llm.Response
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, arguments, got.Choices[0].Message.ToolCalls[0].Function.Arguments)
	})
}

func TestToolCallArgumentsRejectNonObjectJSON(t *testing.T) {
	const invalidArguments = `[]`
	const eventData = `{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"lookup","arguments":` + invalidArguments + `}}]},"done":true}`

	t.Run("request", func(t *testing.T) {
		transformer, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)

		for _, arguments := range []string{invalidArguments, `{"request_id":`} {
			_, err = transformer.TransformRequest(t.Context(), &llm.Request{
				Model: "qwen3",
				Messages: []llm.Message{{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{{
						Type:     "function",
						Function: llm.FunctionCall{Name: "lookup", Arguments: arguments},
					}},
				}},
			})
			require.ErrorContains(t, err, "failed to parse tool call arguments for lookup")
		}
	})

	t.Run("response", func(t *testing.T) {
		transformer, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)

		_, err = transformer.TransformResponse(t.Context(), &httpclient.Response{Body: []byte(eventData)})
		require.ErrorContains(t, err, "failed to parse tool call arguments for lookup")
	})

	t.Run("stream chunk", func(t *testing.T) {
		outbound, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)
		transformer := outbound.(*OutboundTransformer)

		_, err = transformer.TransformStreamChunk(t.Context(), &httpclient.StreamEvent{Data: []byte(eventData)})
		require.ErrorContains(t, err, "failed to parse tool call arguments for lookup")
	})

	t.Run("stream aggregate", func(t *testing.T) {
		outbound, err := NewOutboundTransformerWithConfig(&Config{BaseURL: "http://localhost:11434"})
		require.NoError(t, err)
		transformer := outbound.(*OutboundTransformer)

		_, _, err = transformer.AggregateStreamChunks(t.Context(), nil, []*httpclient.StreamEvent{{Data: []byte(eventData)}})
		require.ErrorContains(t, err, "failed to parse tool call arguments for lookup")
	})
}

func TestTransformResponseWithToolCalls(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	respBody := `{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_current_weather","arguments":{"location":"Paris","format":"celsius"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}`

	resp, err := transformer.TransformResponse(context.Background(), &httpclient.Response{
		Body: []byte(respBody),
	})
	require.NoError(t, err)
	require.Len(t, resp.Choices, 1)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "get_current_weather", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"location":"Paris","format":"celsius"}`, resp.Choices[0].Message.ToolCalls[0].Function.Arguments)
}

func TestTransformResponseWithToolCallsSetsFinishReason(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	respBody := `{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_current_weather","arguments":{"location":"Paris","format":"celsius"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}`

	resp, err := transformer.TransformResponse(context.Background(), &httpclient.Response{
		Body: []byte(respBody),
	})
	require.NoError(t, err)
	require.Len(t, resp.Choices, 1)
	require.Equal(t, "tool_calls", *resp.Choices[0].FinishReason)
}

func TestTransformStreamChunkWithToolCalls(t *testing.T) {
	ollamaTransformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	transformer, ok := ollamaTransformer.(*OutboundTransformer)
	require.True(t, ok)

	eventData := `{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_current_weather","arguments":{"location":"Paris","format":"celsius"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}`

	resp, err := transformer.TransformStreamChunk(context.Background(), &httpclient.StreamEvent{
		Data: []byte(eventData),
	})
	require.NoError(t, err)
	require.Len(t, resp.Choices, 1)
	require.Len(t, resp.Choices[0].Delta.ToolCalls, 1)
	require.Equal(t, "get_current_weather", resp.Choices[0].Delta.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"location":"Paris","format":"celsius"}`, resp.Choices[0].Delta.ToolCalls[0].Function.Arguments)
}

func TestTransformStreamChunkWithToolCallsSetsFinishReason(t *testing.T) {
	ollamaTransformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL: "http://localhost:11434",
	})
	require.NoError(t, err)

	transformer, ok := ollamaTransformer.(*OutboundTransformer)
	require.True(t, ok)

	eventData := `{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_current_weather","arguments":{"location":"Paris","format":"celsius"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}`

	resp, err := transformer.TransformStreamChunk(context.Background(), &httpclient.StreamEvent{
		Data: []byte(eventData),
	})
	require.NoError(t, err)
	require.Len(t, resp.Choices, 1)
	require.Equal(t, "tool_calls", *resp.Choices[0].FinishReason)
}
