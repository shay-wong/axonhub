package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/internal/pkg/xtest"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestNewOutboundTransformer(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		baseURL     string
		expectError bool
	}{
		{
			name:        "valid parameters",
			apiKey:      "test-api-key",
			baseURL:     "https://api.openai.com",
			expectError: false,
		},
		{
			name:        "empty api key",
			apiKey:      "",
			baseURL:     "https://api.openai.com",
			expectError: true,
		},
		{
			name:        "empty base url",
			apiKey:      "test-api-key",
			baseURL:     "",
			expectError: true,
		},
		{
			name:        "base url with trailing slash",
			apiKey:      "test-api-key",
			baseURL:     "https://api.openai.com/",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformer, err := NewOutboundTransformer(tt.baseURL, tt.apiKey)
			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, transformer)
			} else {
				require.NoError(t, err)
				require.NotNil(t, transformer)
				require.Equal(t, tt.apiKey, transformer.config.APIKeyProvider.Get(context.Background()))
				// Base URL should be normalized with v1 version
				require.Equal(t, "https://api.openai.com/v1", transformer.config.BaseURL)
			}
		})
	}
}

func TestOutboundTransformer_TransformResponse_CanceledFinishReason(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	result, err := transformer.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"id":"resp_canceled","object":"response","created_at":1700000000,"status":"canceled","model":"gpt-5","output":[]}`),
	})
	require.NoError(t, err)
	require.Len(t, result.Choices, 1)
	require.NotNil(t, result.Choices[0].FinishReason)
	require.Equal(t, "cancelled", *result.Choices[0].FinishReason)
}

// Refusal content must survive both response conversion boundaries.
func TestResponsesNonStreamRefusalRoundTrip(t *testing.T) {
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	unified, err := outbound.TransformResponse(t.Context(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Body: []byte(`{
			"id":"resp_refusal",
			"object":"response",
			"model":"gpt-5.4-mini",
			"status":"incomplete",
			"incomplete_details":{"reason":"content_filter"},
			"output":[{"id":"msg_1","type":"message","role":"assistant","status":"incomplete","content":[{"type":"refusal","refusal":"I cannot help."}]}]
		}`),
	})
	require.NoError(t, err)
	require.Len(t, unified.Choices, 1)
	require.Equal(t, "I cannot help.", unified.Choices[0].Message.Refusal)

	httpResponse, err := NewInboundTransformer().TransformResponse(t.Context(), unified)
	require.NoError(t, err)

	var roundTrip Response
	require.NoError(t, json.Unmarshal(httpResponse.Body, &roundTrip))
	require.Equal(t, "incomplete", lo.FromPtr(roundTrip.Status))
	require.Equal(t, "content_filter", roundTrip.IncompleteDetails.Reason)
	require.Len(t, roundTrip.Output, 1)
	require.Len(t, roundTrip.Output[0].Content.Items, 1)
	require.Equal(t, "refusal", roundTrip.Output[0].Content.Items[0].Type)
	require.Equal(t, "I cannot help.", lo.FromPtr(roundTrip.Output[0].Content.Items[0].Refusal))
}

func TestResponsesNonStreamTerminalRoundTrip(t *testing.T) {
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)
	inbound := NewInboundTransformer()

	tests := []struct {
		name           string
		body           string
		wantStatus     string
		wantErrorCode  string
		wantIncomplete string
		wantOutputType string
	}{
		{
			name:          "failed",
			body:          `{"id":"resp_failed","object":"response","created_at":1700000001,"status":"failed","model":"gpt-5","output":[],"error":{"type":"invalid_request_error","code":"bad_input","message":"bad input"}}`,
			wantStatus:    "failed",
			wantErrorCode: "bad_input",
		},
		{
			name:           "incomplete max output tokens",
			body:           `{"id":"resp_incomplete","object":"response","created_at":1700000002,"status":"incomplete","model":"gpt-5","output":[],"incomplete_details":{"reason":"max_output_tokens"}}`,
			wantStatus:     "incomplete",
			wantIncomplete: "max_output_tokens",
		},
		{
			name:           "incomplete content filter",
			body:           `{"id":"resp_filtered","object":"response","created_at":1700000003,"status":"incomplete","model":"gpt-5","output":[],"incomplete_details":{"reason":"content_filter"}}`,
			wantStatus:     "incomplete",
			wantIncomplete: "content_filter",
		},
		{
			name:       "canceled",
			body:       `{"id":"resp_canceled","object":"response","created_at":1700000004,"status":"canceled","model":"gpt-5","output":[]}`,
			wantStatus: "canceled",
		},
		{
			name:           "incomplete with function call",
			body:           `{"id":"resp_incomplete_tool","object":"response","created_at":1700000005,"status":"incomplete","model":"gpt-5","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}","status":"completed"}],"incomplete_details":{"reason":"content_filter"}}`,
			wantStatus:     "incomplete",
			wantIncomplete: "content_filter",
			wantOutputType: "function_call",
		},
		{
			name:           "canceled with function call",
			body:           `{"id":"resp_canceled_tool","object":"response","created_at":1700000006,"status":"canceled","model":"gpt-5","output":[{"id":"fc_2","type":"function_call","call_id":"call_2","name":"lookup","arguments":"{}","status":"completed"}]}`,
			wantStatus:     "canceled",
			wantOutputType: "function_call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unified, err := outbound.TransformResponse(t.Context(), &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(tt.body)})
			require.NoError(t, err)
			httpResponse, err := inbound.TransformResponse(t.Context(), unified)
			require.NoError(t, err)

			var roundTripped Response
			require.NoError(t, json.Unmarshal(httpResponse.Body, &roundTripped))
			require.NotNil(t, roundTripped.Status)
			require.Equal(t, tt.wantStatus, *roundTripped.Status)
			if tt.wantOutputType == "" {
				require.Empty(t, roundTripped.Output)
			} else {
				require.NotEmpty(t, roundTripped.Output)
				require.Equal(t, tt.wantOutputType, roundTripped.Output[0].Type)
			}
			if tt.wantErrorCode != "" {
				require.NotNil(t, roundTripped.Error)
				require.Equal(t, tt.wantErrorCode, roundTripped.Error.Code)
			}
			if tt.wantIncomplete != "" {
				require.NotNil(t, roundTripped.IncompleteDetails)
				require.Equal(t, tt.wantIncomplete, roundTripped.IncompleteDetails.Reason)
			}
		})
	}
}

func TestOutboundTransformer_TransformResponse_ErrorOnlyBody(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	result, err := transformer.TransformResponse(t.Context(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"object":"response","status":"failed","output":[],"error":{"type":"invalid_request_error","code":"bad_input","message":"bad input"}}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result.Error)
	require.Equal(t, http.StatusInternalServerError, result.Error.StatusCode)
	require.Equal(t, "bad_input", result.Error.Detail.Code)
}

func TestOutboundTransformer_TransformResponse_PreservesAppliedServiceTier(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	result, err := transformer.TransformResponse(t.Context(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"id":"resp_tier","object":"response","created_at":1700000000,"status":"completed","model":"gpt-5","service_tier":"priority","output":[]}`),
	})
	require.NoError(t, err)
	require.Equal(t, "priority", result.ServiceTier)
}

func TestOutboundTransformer_buildFullRequestURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		rawURL   bool
		expected string
	}{
		{
			name:     "no v1 prefix",
			baseURL:  "https://api.openai.com",
			rawURL:   false,
			expected: "https://api.openai.com/v1/responses",
		},
		{
			name:     "with v1 suffix",
			baseURL:  "https://api.openai.com/v1",
			rawURL:   false,
			expected: "https://api.openai.com/v1/responses",
		},
		{
			name:     "with v1 in path",
			baseURL:  "https://api.openai.com/v1/custom",
			rawURL:   false,
			expected: "https://api.openai.com/v1/custom/responses",
		},
		{
			name:     "raw url with # suffix",
			baseURL:  "https://api.openai.com/custom#",
			rawURL:   true,
			expected: "https://api.openai.com/custom/responses",
		},
		{
			name:     "websocket codex base with # suffix",
			baseURL:  "wss://chatgpt.com/backend-api/codex#",
			rawURL:   true,
			expected: "wss://chatgpt.com/backend-api/codex/responses",
		},
		{
			name:     "raw url with explicit config",
			baseURL:  "https://api.openai.com/custom#",
			rawURL:   true,
			expected: "https://api.openai.com/custom/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				transformer *OutboundTransformer
				err         error
			)

			if tt.rawURL && strings.HasSuffix(tt.baseURL, "#") {
				transformer, err = NewOutboundTransformer(tt.baseURL, "test-key")
			} else {
				transformer, err = NewOutboundTransformerWithConfig(&Config{
					BaseURL:        tt.baseURL,
					APIKeyProvider: auth.NewStaticKeyProvider("test-key"),
					RawURL:         tt.rawURL,
				})
			}

			require.NoError(t, err)

			url, err := transformer.buildFullRequestURL(nil)
			require.NoError(t, err)
			require.Equal(t, tt.expected, url)
		})
	}
}

func TestOutboundTransformer_APIFormat(t *testing.T) {
	transformer, _ := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.Equal(t, llm.APIFormatOpenAIResponse, transformer.APIFormat())
}

func TestOutboundTransformer_TransformError(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	tests := []struct {
		name        string
		rawErr      *httpclient.Error
		wantMessage string
		wantType    string
		wantError   string
	}{
		{
			name: "openai error body",
			rawErr: &httpclient.Error{
				StatusCode: http.StatusBadRequest,
				Body:       []byte(`{"error":{"message":"bad request","type":"invalid_request_error","code":"invalid_request_error"}}`),
			},
			wantMessage: "bad request",
			wantType:    "invalid_request_error",
			wantError:   "Request failed: Bad Request, error: bad request, code: invalid_request_error, type: invalid_request_error",
		},
		{
			name: "plain text body",
			rawErr: &httpclient.Error{
				StatusCode: http.StatusBadGateway,
				Body:       []byte("upstream overloaded"),
			},
			wantMessage: "upstream overloaded",
			wantType:    "api_error",
			wantError:   "Request failed: Bad Gateway, error: upstream overloaded, type: api_error",
		},
		{
			name: "empty body falls back to status text",
			rawErr: &httpclient.Error{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
			},
			wantMessage: "Bad Gateway",
			wantType:    "api_error",
			wantError:   "Request failed: Bad Gateway, error: Bad Gateway, type: api_error",
		},
		{
			name: "unknown status falls back to raw status",
			rawErr: &httpclient.Error{
				StatusCode: 599,
				Status:     "599 Network Connect Timeout Error",
			},
			wantMessage: "599 Network Connect Timeout Error",
			wantType:    "api_error",
			wantError:   "Request failed: , error: 599 Network Connect Timeout Error, type: api_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.TransformError(context.Background(), tt.rawErr)

			require.Equal(t, tt.rawErr.StatusCode, result.StatusCode)
			require.Equal(t, tt.wantMessage, result.Detail.Message)
			require.Equal(t, tt.wantType, result.Detail.Type)
			require.Equal(t, tt.wantError, result.Error())
		})
	}
}

func TestOutboundTransformer_TransformRequest_AccountIdentity(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.openai.com",
		APIKeyProvider: auth.NewStaticKeyProvider("test-api-key"),
	})
	require.NoError(t, err)

	req := &llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hi")}},
		},
	}

	hreq, err := transformer.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, hreq.Metadata)
}

func TestOutboundTransformer_TransformRequest_OmitsMetadataWhenEmpty(t *testing.T) {
	transformer, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.openai.com",
		APIKeyProvider: auth.NewStaticKeyProvider(""),
	})
	require.NoError(t, err)

	req := &llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hi")}},
		},
	}

	hreq, err := transformer.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, hreq.Metadata)
}

func TestOutboundTransformer_TransformRequest_WebSearchRequiredToolChoice(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	req := &llm.Request{
		Model: "gpt-4o-search-preview",
		Messages: []llm.Message{{
			Role: "user",
			Content: llm.MessageContent{
				Content: lo.ToPtr("latest ai news"),
			},
		}},
		Tools: []llm.Tool{{
			Type: llm.ToolTypeWebSearch,
		}},
		ToolChoice: &llm.ToolChoice{
			ToolChoice: lo.ToPtr("required"),
		},
	}

	hreq, err := transformer.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(hreq.Body, &payload)
	require.NoError(t, err)
	require.Equal(t, "required", payload["tool_choice"])
}

func TestOutboundTransformer_TransformRequest_ImageGenerationToolChoice(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	req := &llm.Request{
		Model:     "gpt-5.5",
		APIFormat: llm.APIFormatOpenAIResponse,
		Messages: []llm.Message{{
			Role: "user",
			Content: llm.MessageContent{
				Content: lo.ToPtr("Generate an image."),
			},
		}},
		Tools: []llm.Tool{{
			Type: llm.ToolTypeImageGeneration,
			ImageGeneration: &llm.ImageGeneration{
				Model: "gpt-image-2",
			},
		}},
		ToolChoice: &llm.ToolChoice{
			NamedToolChoice: &llm.NamedToolChoice{Type: llm.ToolTypeImageGeneration},
		},
	}

	hreq, err := transformer.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(hreq.Body, &payload)
	require.NoError(t, err)
	require.Equal(t, "image_generation", payload["tool_choice"].(map[string]any)["type"])
	require.NotContains(t, payload["tool_choice"].(map[string]any), "name")
	require.Equal(t, "image_generation", payload["tools"].([]any)[0].(map[string]any)["type"])
	require.Equal(t, "gpt-image-2", payload["tools"].([]any)[0].(map[string]any)["model"])
}

func TestOutboundTransformer_TransformRequest_ReplaysProviderRawToolsAndToolChoice(t *testing.T) {
	inbound := NewInboundTransformer()
	inboundReq := &httpclient.Request{
		Body: []byte(`{
			"model": "gpt-4o",
			"input": "Search and run shell.",
			"tools": [
				{
					"type": "tool_search",
					"name": "search_docs",
					"namespace": "docs"
				},
				{
					"type": "function",
					"name": "get_weather",
					"parameters": {"type": "object", "properties": {}}
				}
			],
			"tool_choice": {
				"type": "tool_search",
				"tools": [
					{"type": "tool_search", "name": "search_docs"}
				]
			}
		}`),
	}

	llmReq, err := inbound.TransformRequest(context.Background(), inboundReq)
	require.NoError(t, err)
	llmReq.Model = "mapped-model"

	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	httpReq, err := outbound.TransformRequest(context.Background(), llmReq)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(httpReq.Body, &payload)
	require.NoError(t, err)
	require.Equal(t, "mapped-model", payload["model"])

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	rawTool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_search", rawTool["type"])
	require.Equal(t, "docs", rawTool["namespace"])

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_search", toolChoice["type"])
	require.Len(t, toolChoice["tools"], 1)
}

func TestOutboundTransformer_TransformRequest_BridgesAnthropicFunctionToolSearch(t *testing.T) {
	anthropicInbound := anthropic.NewInboundTransformer()
	anthropicReq := &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body: []byte(`{
			"model": "claude-opus-4-8",
			"max_tokens": 1024,
			"messages": [{"role": "user", "content": "find the right tool"}],
			"tools": [
				{
					"name": "ToolSearch",
					"description": "Find deferred tools",
					"input_schema": {
						"type": "object",
						"properties": {"query": {"type": "string"}}
					}
				},
				{
					"name": "mcp__plugin_oh_my_codex__session_search",
					"description": "Search session",
					"defer_loading": true,
					"input_schema": {"type": "object", "properties": {}}
				}
			],
			"tool_choice": {"type": "tool", "name": "ToolSearch"}
		}`),
	}

	llmReq, err := anthropicInbound.TransformRequest(context.Background(), anthropicReq)
	require.NoError(t, err)
	llmReq.Model = "mapped-model"

	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	httpReq, err := outbound.TransformRequest(context.Background(), llmReq)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(httpReq.Body, &payload)
	require.NoError(t, err)
	require.Equal(t, "mapped-model", payload["model"])

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	toolSearch, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_search", toolSearch["type"])
	require.Equal(t, "client", toolSearch["execution"])
	require.Equal(t, "Find deferred tools", toolSearch["description"])
	require.NotContains(t, toolSearch, "defer_loading")

	deferredFunction, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", deferredFunction["type"])
	require.Equal(t, "mcp__plugin_oh_my_codex__session_search", deferredFunction["name"])
	require.Equal(t, true, deferredFunction["defer_loading"])

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_search", toolChoice["type"])
	require.NotContains(t, toolChoice, "name")
}

func TestOutboundTransformer_TransformResponse_BridgedToolSearchOutputRoundTripsToAnthropicToolResult(t *testing.T) {
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	llmResp, err := outbound.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Request: &httpclient.Request{
			TransformerMetadata: map[string]any{
				llm.TransformerMetadataKeyAnthropicFunctionToolSearchName: "ToolSearch",
			},
		},
		Body: []byte(`{
			"id": "resp_search",
			"object": "response",
			"created_at": 1700000000,
			"status": "completed",
			"model": "mapped-model",
			"output": [
				{
					"type": "tool_search_call",
					"call_id": "call_search",
					"execution": "client",
					"status": "completed",
					"arguments": {"query":"select:get_weather"}
				},
				{
					"type": "tool_search_output",
					"call_id": "call_search",
					"execution": "client",
					"status": "completed",
					"tools": [
						{
							"type": "function",
							"name": "get_weather",
							"description": "Get weather",
							"parameters": {"type":"object","properties":{"city":{"type":"string"}}}
						}
					]
				}
			]
		}`),
	})
	require.NoError(t, err)
	require.Len(t, llmResp.Choices, 1)
	require.Len(t, llmResp.Choices[0].Message.ToolCalls, 1)
	require.Len(t, llmResp.Choices[0].Message.InlineToolResults, 1)

	anthropicResp, err := anthropic.NewInboundTransformer().TransformResponse(context.Background(), llmResp)
	require.NoError(t, err)

	var got anthropic.Message
	err = json.Unmarshal(anthropicResp.Body, &got)
	require.NoError(t, err)
	require.Len(t, got.Content, 2)
	require.Equal(t, "tool_use", got.Content[0].Type)
	require.Equal(t, "call_search", got.Content[0].ID)
	require.NotNil(t, got.Content[0].Name)
	require.Equal(t, "ToolSearch", *got.Content[0].Name)
	require.Equal(t, "tool_result", got.Content[1].Type)
	require.NotNil(t, got.Content[1].ToolUseID)
	require.Equal(t, "call_search", *got.Content[1].ToolUseID)
	require.NotNil(t, got.Content[1].Content)
	require.NotNil(t, got.Content[1].Content.Content)
	require.JSONEq(t, `{"tools":[{"type":"function","name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}`, *got.Content[1].Content.Content)
}

func TestOutboundTransformer_TransformResponse_BridgedToolSearchRejectsMalformedOutput(t *testing.T) {
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	_, err = outbound.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Request: &httpclient.Request{
			TransformerMetadata: map[string]any{
				llm.TransformerMetadataKeyAnthropicFunctionToolSearchName: "ToolSearch",
			},
		},
		Body: []byte(`{
			"id": "resp_search",
			"object": "response",
			"created_at": 1700000000,
			"status": "completed",
			"model": "mapped-model",
			"output": [
				{
					"type": "tool_search_output",
					"call_id": "call_search",
					"execution": "client",
					"status": "completed"
				}
			]
		}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool_search_output requires tools")
}

func TestOutboundTransformer_TransformRequest_BridgedToolSearchRejectsMalformedToolResult(t *testing.T) {
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	_, err = outbound.TransformRequest(context.Background(), &llm.Request{
		Model:       "mapped-model",
		RequestType: llm.RequestTypeChat,
		APIFormat:   llm.APIFormatAnthropicMessage,
		Messages: []llm.Message{
			{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID: "call_search",
						Function: llm.FunctionCall{
							Name:      "ToolSearch",
							Arguments: `{"query":"select:get_weather"}`,
						},
						TransformerMetadata: map[string]any{
							llm.TransformerMetadataKeyOpenAIResponsesToolResultItemType: "tool_search_output",
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: lo.ToPtr("call_search"),
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{
						{
							Type: "text",
							Text: lo.ToPtr("not json"),
							TransformerMetadata: map[string]any{
								llm.TransformerMetadataKeyOpenAIResponsesToolResultItemType: "tool_search_output",
							},
						},
					},
				},
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool_search_output requires content")
}

func TestOutboundTransformer_TransformRequest_ReplaysNamespaceTool(t *testing.T) {
	inbound := NewInboundTransformer()
	inboundReq := &httpclient.Request{
		Body: []byte(`{
			"model": "gpt-4o",
			"input": "List the projects.",
			"tools": [
				{
					"type": "namespace",
					"name": "mcp__codebase_memory_mcp",
					"tools": [
						{"type": "function", "name": "list_projects", "parameters": {"type": "object"}},
						{"type": "function", "name": "get_project", "parameters": {"type": "object"}}
					]
				},
				{"type": "function", "name": "get_weather", "parameters": {"type": "object"}}
			]
		}`),
	}

	llmReq, err := inbound.TransformRequest(context.Background(), inboundReq)
	require.NoError(t, err)
	require.Len(t, llmReq.Tools, 3)

	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	httpReq, err := outbound.TransformRequest(context.Background(), llmReq)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(httpReq.Body, &payload)
	require.NoError(t, err)

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	namespaceTool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "namespace", namespaceTool["type"])
	require.Equal(t, "mcp__codebase_memory_mcp", namespaceTool["name"])
	require.Len(t, namespaceTool["tools"], 2)

	functionTool, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "get_weather", functionTool["name"])
}

func TestOutboundTransformer_TransformRequest_ReplaysProviderRawInputItems(t *testing.T) {
	inbound := NewInboundTransformer()
	inboundReq := &httpclient.Request{
		Body: []byte(`{
			"model": "gpt-4o",
			"input": [
				{
					"type": "tool_search_call",
					"call_id": "call_search",
					"status": "completed",
					"arguments": {"query":"image generation","limit":10}
				},
				{
					"type": "message",
					"role": "user",
					"content": [{"type":"input_text","text":"hello"}]
				}
			]
		}`),
	}

	llmReq, err := inbound.TransformRequest(context.Background(), inboundReq)
	require.NoError(t, err)

	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	httpReq, err := outbound.TransformRequest(context.Background(), llmReq)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(httpReq.Body, &payload)
	require.NoError(t, err)

	input, ok := payload["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	rawItem, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_search_call", rawItem["type"])
	arguments, ok := rawItem["arguments"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image generation", arguments["query"])
	require.Equal(t, float64(10), arguments["limit"])

	message, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", message["type"])
}

func TestOutboundTransformer_TransformRequest_DoesNotReplayRawToolWhenToolsChanged(t *testing.T) {
	inbound := NewInboundTransformer()
	inboundReq := &httpclient.Request{
		Body: []byte(`{
			"model": "gpt-4o",
			"input": "Search and run shell.",
			"tools": [
				{"type": "tool_search", "name": "search_docs", "namespace": "docs"},
				{"type": "function", "name": "get_weather", "parameters": {"type": "object", "properties": {}}}
			]
		}`),
	}

	llmReq, err := inbound.TransformRequest(context.Background(), inboundReq)
	require.NoError(t, err)
	llmReq.Tools = []llm.Tool{{
		Type: "function",
		Function: llm.Function{
			Name:       "different_tool",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}}

	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	httpReq, err := outbound.TransformRequest(context.Background(), llmReq)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(httpReq.Body, &payload)
	require.NoError(t, err)

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "different_tool", tool["name"])
}

func TestProviderExtensions_NotSerializedWithLLMRequest(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{{
			Role:    "user",
			Content: llm.MessageContent{Content: lo.ToPtr("hi")},
		}},
		ProviderExtensions: &llm.ProviderExtensions{
			OpenAIResponses: &llm.OpenAIResponsesProviderExtensions{
				Request: &llm.OpenAIResponsesRequestExtensions{
					RawTools: []llm.OpenAIResponsesRawFragment{{
						Type: "tool_search",
						Raw:  json.RawMessage(`{"secret":"raw prompt"}`),
					}},
					RawToolChoice: json.RawMessage(`{"secret":"raw choice"}`),
				},
			},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)
	require.NotContains(t, string(data), "raw prompt")
	require.NotContains(t, string(data), "raw choice")
	require.NotContains(t, string(data), "provider_extensions")
}

func TestOutboundTransformer_TransformRequest(t *testing.T) {
	transformer, _ := NewOutboundTransformer("https://api.openai.com", "test-api-key")

	tests := []struct {
		name        string
		chatReq     *llm.Request
		expectError bool
		validate    func(t *testing.T, result *httpclient.Request, chatReq *llm.Request)
	}{
		{
			name:        "nil request",
			chatReq:     nil,
			expectError: true,
		},
		{
			name: "simple text request",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello, world!"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				require.Equal(t, http.MethodPost, result.Method)
				require.Equal(t, "https://api.openai.com/v1/responses", result.URL)
				require.Equal(t, "application/json", result.Headers.Get("Content-Type"))
				require.Equal(t, "application/json", result.Headers.Get("Accept"))
				require.NotNil(t, result.Auth)
				require.Equal(t, "bearer", result.Auth.Type)
				require.Equal(t, "test-api-key", result.Auth.APIKey)

				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Equal(t, chatReq.Model, req.Model)
				require.Equal(t, chatReq.Messages[0].Content.Content, req.Input.Text)
			},
		},
		{
			name: "request with system message",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "system",
						Content: llm.MessageContent{
							Content: lo.ToPtr("You are a helpful assistant."),
						},
					},
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello!"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Equal(t, "You are a helpful assistant.", req.Instructions)
			},
		},
		{
			name: "request with multimodal content",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							MultipleContent: []llm.MessageContentPart{
								{
									Type: "text",
									Text: lo.ToPtr("What's in this image?"),
								},
								{
									Type: "image_url",
									ImageURL: &llm.ImageURL{
										URL: "data:image/jpeg;base64,/9j/4AAQSkZJRg...",
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "request with image generation tool",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Generate an image of a cat"),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: llm.ToolTypeImageGeneration,
						ImageGeneration: &llm.ImageGeneration{
							Quality:           "high",
							Size:              "1024x1024",
							OutputFormat:      "png",
							OutputCompression: func() *int64 { v := int64(80); return &v }(),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Len(t, req.Tools, 1)
				require.Equal(t, llm.ToolTypeImageGeneration, req.Tools[0].Type)
				require.Equal(t, "high", req.Tools[0].Quality)
				require.Equal(t, "1024x1024", req.Tools[0].Size)
				require.Equal(t, "png", req.Tools[0].OutputFormat)
				require.Equal(t, int64(80), *req.Tools[0].OutputCompression)
			},
		},
		{
			name: "request with web search tool",
			chatReq: &llm.Request{
				Model: "gpt-4o-search-preview",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("latest ai news"),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: llm.ToolTypeWebSearch,
						WebSearch: &llm.WebSearch{
							AllowedDomains: []string{"openai.com"},
							UserLocation: llm.WebSearchToolUserLocation{
								Type:    "approximate",
								Country: "US",
							},
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Equal(t, []Tool{
					{
						Type: "web_search",
						Filters: &WebSearchFilters{
							AllowedDomains: []string{"openai.com"},
						},
						UserLocation: &WebSearchUserLocation{
							Type:    "approximate",
							Country: "US",
						},
					},
				}, req.Tools)
			},
		},
		{
			name: "request with google search tool maps to web_search",
			chatReq: &llm.Request{
				Model: "gpt-5.4",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Search the web for the latest AI announcement."),
						},
					},
				},
				Tools: []llm.Tool{{
					Type: llm.ToolTypeGoogleSearch,
					Google: &llm.GoogleTools{
						Search: &llm.GoogleSearch{},
					},
				}},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var raw map[string]any

				err := json.Unmarshal(result.Body, &raw)
				require.NoError(t, err)

				tools, ok := raw["tools"].([]any)
				require.True(t, ok)
				require.Len(t, tools, 1)

				tool, ok := tools[0].(map[string]any)
				require.True(t, ok)
				require.Equal(t, llm.ToolTypeWebSearch, tool["type"])
			},
		},
		{
			name: "request with unsupported tool type is skipped",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: "unsupported_tool",
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				// Unsupported tools should be skipped
				require.Len(t, req.Tools, 0)
			},
		},
		{
			name: "request with function tool",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("What's the weather?"),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: "function",
						Function: llm.Function{
							Name:        "get_weather",
							Description: "Get weather information",
							Parameters:  []byte(`{"type":"object","properties":{"location":{"type":"string"}}}`),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Len(t, req.Tools, 1)
				require.Equal(t, "function", req.Tools[0].Type)
				require.Equal(t, "get_weather", req.Tools[0].Name)
				require.Equal(t, "Get weather information", req.Tools[0].Description)
				require.Nil(t, req.Tools[0].DeferLoading)
			},
		},
		{
			name: "request with deferred function tool",
			chatReq: &llm.Request{
				Model: "gpt-5.4",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Find the shipping ETA tool first."),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type:         "function",
						DeferLoading: lo.ToPtr(true),
						Function: llm.Function{
							Name:        "get_shipping_eta",
							Description: "Look up shipping ETA details for an order.",
							Parameters:  []byte(`{"type":"object","properties":{"order_id":{"type":"string"}}}`),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Len(t, req.Tools, 1)
				require.Equal(t, "function", req.Tools[0].Type)
				require.NotNil(t, req.Tools[0].DeferLoading)
				require.True(t, *req.Tools[0].DeferLoading)
			},
		},
		{
			name: "request with zero-arg function tool normalizes empty object schema",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Run the tool"),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: "function",
						Function: llm.Function{
							Name:        "ping",
							Description: "Ping tool",
							Parameters:  []byte(`{"type":"object"}`),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Len(t, req.Tools, 1)
				require.Equal(t, "object", req.Tools[0].Parameters["type"])
				require.Equal(t, map[string]any{}, req.Tools[0].Parameters["properties"])
			},
		},
		{
			name: "request with tool search tool",
			chatReq: &llm.Request{
				Model: "gpt-5.4",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Find the shipping ETA tool first."),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: llm.ToolTypeToolSearch,
						ToolSearch: &llm.ToolSearchTool{
							Execution:   "client",
							Description: "Find the project-specific tools needed to continue the task.",
							Parameters:  []byte(`{"type":"object","properties":{"goal":{"type":"string"}},"required":["goal"],"additionalProperties":false}`),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Len(t, req.Tools, 1)
				require.Equal(t, "tool_search", req.Tools[0].Type)
				require.Equal(t, "client", req.Tools[0].Execution)
				require.Equal(t, "Find the project-specific tools needed to continue the task.", req.Tools[0].Description)
				require.Nil(t, req.Tools[0].DeferLoading)
				require.Equal(t, map[string]any{
					"type": "object",
					"properties": map[string]any{
						"goal": map[string]any{"type": "string"},
					},
					"required":             []any{"goal"},
					"additionalProperties": false,
				}, req.Tools[0].Parameters)
			},
		},
		{
			name: "request with reasoning effort and budget - effort takes priority",
			chatReq: &llm.Request{
				Model:           "o3",
				ReasoningEffort: "high",
				ReasoningBudget: lo.ToPtr(int64(5000)),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Solve this problem"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.Reasoning)
				require.Equal(t, "high", req.Reasoning.Effort)
				// MaxTokens should be nil when effort is specified (priority rule)
				require.Nil(t, req.Reasoning.MaxTokens)
			},
		},
		{
			name: "request with reasoning effort only",
			chatReq: &llm.Request{
				Model:           "o3",
				ReasoningEffort: "medium",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Solve this problem"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.Reasoning)
				require.Equal(t, "medium", req.Reasoning.Effort)
				require.Nil(t, req.Reasoning.MaxTokens)
			},
		},
		{
			name: "request with reasoning budget only",
			chatReq: &llm.Request{
				Model:           "o3",
				ReasoningBudget: lo.ToPtr(int64(3000)),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Solve this problem"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.Reasoning)
				require.Empty(t, req.Reasoning.Effort)
				require.NotNil(t, req.Reasoning.MaxTokens)
				require.Equal(t, int64(3000), *req.Reasoning.MaxTokens)
			},
		},
		{
			name: "request with tool choice auto",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
				ToolChoice: &llm.ToolChoice{
					ToolChoice: lo.ToPtr("auto"),
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.ToolChoice)
				require.NotNil(t, req.ToolChoice.Mode)
				require.Equal(t, "auto", *req.ToolChoice.Mode)
			},
		},
		{
			name: "request with top_p and top_logprobs",
			chatReq: &llm.Request{
				Model:       "gpt-4o",
				TopP:        lo.ToPtr(0.9),
				TopLogprobs: lo.ToPtr(int64(5)),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.TopP)
				require.Equal(t, 0.9, *req.TopP)
				require.NotNil(t, req.TopLogprobs)
				require.Equal(t, int64(5), *req.TopLogprobs)
			},
		},
		{
			name: "request with streaming enabled",
			chatReq: &llm.Request{
				Model:  "gpt-4o",
				Stream: func() *bool { v := true; return &v }(),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.Stream)
				require.True(t, *req.Stream)
			},
		},
		{
			name: "request with parallel tool calls",
			chatReq: &llm.Request{
				Model:             "gpt-4o",
				ParallelToolCalls: lo.ToPtr(false),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
				Tools: []llm.Tool{
					{
						Type: "function",
						Function: llm.Function{
							Name:        "test_function",
							Description: "Test function",
							Parameters:  []byte(`{"type":"object","properties":{}}`),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.ParallelToolCalls)
				require.False(t, *req.ParallelToolCalls)
			},
		},
		{
			name: "request with parallel tool calls but no tools",
			chatReq: &llm.Request{
				Model:             "gpt-4o",
				ParallelToolCalls: lo.ToPtr(true),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
				// No tools provided
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.Nil(t, req.ParallelToolCalls, "ParallelToolCalls should be nil when no tools are provided")
			},
		},
		{
			name: "request with text options",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				ResponseFormat: &llm.ResponseFormat{
					Type: "json_object",
				},
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: func() *string { s := "Return JSON"; return &s }(),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.Text)
			},
		},
		{
			name: "request with include field",
			chatReq: &llm.Request{
				Model: "gpt-4o",
				TransformerMetadata: map[string]any{
					"include": []string{"file_search_call.results", "reasoning.encrypted_content"},
				},
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Hello"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.Include)
				require.Equal(t, []string{"file_search_call.results", "reasoning.encrypted_content"}, req.Include)
			},
		},
		{
			name: "request with previous_response_id",
			chatReq: &llm.Request{
				Model:              "gpt-5.4",
				PreviousResponseID: lo.ToPtr("resp_prev_123"),
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: lo.ToPtr("Continue"),
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Request, chatReq *llm.Request) {
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)
				require.NotNil(t, req.PreviousResponseID)
				require.Equal(t, "resp_prev_123", *req.PreviousResponseID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := transformer.TransformRequest(context.Background(), tt.chatReq)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)

				if tt.validate != nil {
					tt.validate(t, result, tt.chatReq)
				}
			}
		})
	}
}

func TestOutboundTransformer_TransformRequest_UsesSharedSessionIDAsPromptCacheKeyFallback(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	ctx := shared.WithSessionID(context.Background(), "shared-session-123")

	req := &llm.Request{
		Model: "gpt-5.4",
		Messages: []llm.Message{
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: lo.ToPtr("Hello"),
				},
			},
		},
	}

	httpReq, err := transformer.TransformRequest(ctx, req)
	require.NoError(t, err)

	var payload Request

	err = json.Unmarshal(httpReq.Body, &payload)
	require.NoError(t, err)
	require.NotNil(t, payload.PromptCacheKey)
	require.Equal(t, "shared-session-123-"+conversationAnchor(req.Messages), *payload.PromptCacheKey)
}

func TestOutboundTransformer_TransformRequest_PromptCacheKeyScopedPerConversation(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	ctx := shared.WithSessionID(context.Background(), "shared-session-123")

	newReq := func(firstUser string, extraTurns ...llm.Message) *llm.Request {
		messages := []llm.Message{
			{Role: "system", Content: llm.MessageContent{Content: lo.ToPtr("You are an agent.")}},
			{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr(firstUser)}},
		}
		messages = append(messages, extraTurns...)

		return &llm.Request{Model: "gpt-5.4", Messages: messages}
	}

	cacheKey := func(req *llm.Request) string {
		httpReq, err := transformer.TransformRequest(ctx, req)
		require.NoError(t, err)

		var payload Request

		require.NoError(t, json.Unmarshal(httpReq.Body, &payload))
		require.NotNil(t, payload.PromptCacheKey)

		return *payload.PromptCacheKey
	}

	// Later turns of the same conversation keep the same cache key.
	turn1 := cacheKey(newReq("task A"))
	turn2 := cacheKey(newReq("task A",
		llm.Message{Role: "assistant", Content: llm.MessageContent{Content: lo.ToPtr("working")}},
		llm.Message{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("continue")}},
	))
	require.Equal(t, turn1, turn2)

	// Sibling conversations in the same session get distinct cache keys.
	require.NotEqual(t, turn1, cacheKey(newReq("task B")))

	// Client-provided keys are preserved untouched.
	explicit := newReq("task A")
	explicit.PromptCacheKey = lo.ToPtr("client-key")
	require.Equal(t, "client-key", cacheKey(explicit))

	// A large shared instruction prefix must not starve the first user
	// message out of the fingerprint: sibling conversations still get
	// distinct keys.
	largeSystem := strings.Repeat("shared instructions. ", 2048)
	largeReq := func(firstUser string) *llm.Request {
		return &llm.Request{
			Model: "gpt-5.4",
			Messages: []llm.Message{
				{Role: "system", Content: llm.MessageContent{Content: lo.ToPtr(largeSystem)}},
				{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr(firstUser)}},
			},
		}
	}
	require.NotEqual(t, cacheKey(largeReq("task A")), cacheKey(largeReq("task B")))

	// Non-text content contributes to the fingerprint: first user messages
	// that differ only by an image part get distinct keys.
	imageReq := func(imageURL string) *llm.Request {
		return &llm.Request{
			Model: "gpt-5.4",
			Messages: []llm.Message{
				{Role: "user", Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{
						{Type: "text", Text: lo.ToPtr("describe this image")},
						{Type: "image_url", ImageURL: &llm.ImageURL{URL: imageURL}},
					},
				}},
			},
		}
	}
	require.NotEqual(t,
		cacheKey(imageReq("https://example.com/a.png")),
		cacheKey(imageReq("https://example.com/b.png")),
	)
}

func TestOutboundTransformer_TransformResponse(t *testing.T) {
	transformer, _ := NewOutboundTransformer("https://api.openai.com", "test-api-key")

	tests := []struct {
		name        string
		httpResp    *httpclient.Response
		expectError bool
		validate    func(t *testing.T, result *llm.Response)
	}{
		{
			name:        "nil response",
			httpResp:    nil,
			expectError: true,
		},
		{
			name: "HTTP error status",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusBadRequest,
				Body:       []byte(`{"error": {"message": "Bad request"}}`),
			},
			expectError: true,
		},
		{
			name: "empty response body",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusOK,
				Body:       []byte{},
			},
			expectError: true,
		},
		{
			name: "invalid JSON response",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusOK,
				Body:       []byte(`{invalid json}`),
			},
			expectError: true,
		},
		{
			name: "valid response with text output",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"id": "resp_123",
					"object": "response",
					"created_at": 1759161016,
					"status": "completed",
					"model": "gpt-4o",
					"output": [
						{
							"id": "msg_123",
							"type": "message",
							"status": "completed",
							"content": [
								{
									"type": "output_text",
									"text": "Hello! How can I help you?"
								}
							],
							"role": "assistant"
						}
					],
					"usage": {
						"input_tokens": 10,
						"output_tokens": 20,
						"total_tokens": 30
					}
				}`),
			},
			expectError: false,
			validate: func(t *testing.T, result *llm.Response) {
				require.Equal(t, "chat.completion", result.Object)
				require.Equal(t, "resp_123", result.ID)
				require.Equal(t, "gpt-4o", result.Model)
				require.Len(t, result.Choices, 1)
				require.Equal(t, "assistant", result.Choices[0].Message.Role)
				require.NotNil(t, result.Choices[0].Message.Content.Content)
				require.Equal(t, "Hello! How can I help you?", *result.Choices[0].Message.Content.Content)
				require.NotNil(t, result.Usage)
				require.Equal(t, int64(10), result.Usage.PromptTokens)
				require.Equal(t, int64(20), result.Usage.CompletionTokens)
				require.Equal(t, int64(30), result.Usage.TotalTokens)
			},
		},
		{
			name: "response with image generation result",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"id": "resp_456",
					"object": "response",
					"created_at": 1759161016,
					"status": "completed",
					"model": "gpt-4o",
					"output": [
						{
							"id": "img_123",
							"type": "image_generation_call",
							"status": "completed",
							"result": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="
						}
					]
				}`),
			},
			expectError: false,
			validate: func(t *testing.T, result *llm.Response) {
				require.Equal(t, "chat.completion", result.Object)
				require.Equal(t, "resp_456", result.ID)
				require.Len(t, result.Choices, 1)
				require.Equal(t, "assistant", result.Choices[0].Message.Role)
				require.Len(t, result.Choices[0].Message.Content.MultipleContent, 1)
				require.Equal(t, "image_url", result.Choices[0].Message.Content.MultipleContent[0].Type)
				require.NotNil(t, result.Choices[0].Message.Content.MultipleContent[0].ImageURL)
				require.Contains(t, result.Choices[0].Message.Content.MultipleContent[0].ImageURL.URL, "data:image/png;base64,")
			},
		},
		{
			name: "response with encrypted reasoning",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"id": "resp_789",
					"object": "response",
					"created_at": 1759161016,
					"status": "completed",
					"model": "gpt-4o",
					"output": [
						{
							"id": "rs_123",
							"type": "reasoning",
							"summary": [],
							"encrypted_content": "encrypted_data_here"
						}
					]
				}`),
			},
			expectError: false,
			validate: func(t *testing.T, result *llm.Response) {
				require.Len(t, result.Choices, 1)
				require.NotNil(t, result.Choices[0].Message)
				require.NotNil(t, result.Choices[0].Message.ReasoningSignature)
				require.Equal(t, "encrypted_data_here", *result.Choices[0].Message.ReasoningSignature)
			},
		},
		{
			name: "response with previous_response_id",
			httpResp: &httpclient.Response{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"id": "resp_456",
					"object": "response",
					"created_at": 1759161016,
					"status": "completed",
					"model": "gpt-5.4",
					"previous_response_id": "resp_prev_123",
					"output": [
						{
							"id": "msg_456",
							"type": "message",
							"status": "completed",
							"content": [
								{
									"type": "output_text",
									"text": "Continued response"
								}
							],
							"role": "assistant"
						}
					]
				}`),
			},
			expectError: false,
			validate: func(t *testing.T, result *llm.Response) {
				require.NotNil(t, result.PreviousResponseID)
				require.Equal(t, "resp_prev_123", *result.PreviousResponseID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := transformer.TransformResponse(context.Background(), tt.httpResp)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)

				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

func TestOutboundTransformer_TransformImageEditResponse(t *testing.T) {
	transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	httpReq, err := transformer.TransformRequest(t.Context(), &llm.Request{
		Model:       "gpt-image-1",
		RequestType: llm.RequestTypeImage,
		APIFormat:   llm.APIFormatOpenAIImageEdit,
		Image: &llm.ImageRequest{
			Prompt:       "edit this image",
			Images:       [][]byte{[]byte("source-image")},
			OutputFormat: "webp",
			Quality:      "high",
			Size:         "1024x1024",
		},
	})
	require.NoError(t, err)
	require.Equal(t, llm.RequestTypeImage.String(), httpReq.RequestType)
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), httpReq.APIFormat)

	result, err := transformer.TransformResponse(t.Context(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Request:    httpReq,
		Body: []byte(`{
			"id": "resp_image_123",
			"object": "response",
			"created_at": 1759161016,
			"status": "completed",
			"model": "gpt-image-1",
			"output": [
				{
					"id": "img_123",
					"type": "image_generation_call",
					"status": "completed",
					"result": "data:image/webp;base64,aW1hZ2UtZGF0YQ=="
				}
			]
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, "image.generation", result.Object)
	require.Equal(t, llm.RequestTypeImage, result.RequestType)
	require.Equal(t, "gpt-image-1", result.Model)
	require.NotNil(t, result.Image)
	require.Equal(t, "webp", result.Image.OutputFormat)
	require.Equal(t, "high", result.Image.Quality)
	require.Equal(t, "1024x1024", result.Image.Size)
	require.Len(t, result.Image.Data, 1)
	require.Equal(t, "aW1hZ2UtZGF0YQ==", result.Image.Data[0].B64JSON)
}

func TestOutboundTransformer_TransformRequest_WithTestData(t *testing.T) {
	tests := []struct {
		name        string
		requestFile string
		validate    func(t *testing.T, result *httpclient.Request, expectedReq *llm.Request)
	}{
		{
			name:        "image generation request transformation",
			requestFile: "image-generation.request.json",
			validate: func(t *testing.T, result *httpclient.Request, expectedReq *llm.Request) {
				// Verify basic HTTP request properties
				require.Equal(t, http.MethodPost, result.Method)
				require.Equal(t, "https://api.openai.com/v1/responses", result.URL)
				require.Equal(t, "application/json", result.Headers.Get("Content-Type"))
				require.Equal(t, "application/json", result.Headers.Get("Accept"))
				require.NotEmpty(t, result.Body)

				// Verify auth
				require.NotNil(t, result.Auth)
				require.Equal(t, "bearer", result.Auth.Type)
				require.Equal(t, "test-api-key", result.Auth.APIKey)

				// Parse the transformed request
				var req Request

				err := json.Unmarshal(result.Body, &req)
				require.NoError(t, err)

				// Verify model
				require.Equal(t, expectedReq.Model, req.Model)

				// Verify tools transformation
				if len(expectedReq.Tools) > 0 {
					require.NotNil(t, req.Tools)
					require.Len(t, req.Tools, len(expectedReq.Tools))

					for i, tool := range expectedReq.Tools {
						require.Equal(t, tool.Type, req.Tools[i].Type)

						if tool.ImageGeneration != nil {
							require.Equal(t, tool.ImageGeneration.Quality, req.Tools[i].Quality)
							require.Equal(t, tool.ImageGeneration.Size, req.Tools[i].Size)
							require.Equal(t, tool.ImageGeneration.OutputFormat, req.Tools[i].OutputFormat)
						}
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Load the test request data
			var expectedReq llm.Request

			err := xtest.LoadTestData(t, tt.requestFile, &expectedReq)
			if err != nil {
				t.Skipf("Test data file %s not found, skipping test", tt.requestFile)
				return
			}

			// Create transformer
			transformer, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
			require.NoError(t, err)

			// Transform the request
			result, err := transformer.TransformRequest(context.Background(), &expectedReq)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Run validation
			tt.validate(t, result, &expectedReq)
		})
	}
}

func TestOutboundTransformer_TransformResponse_WithTestData(t *testing.T) {
	transformer, _ := NewOutboundTransformer("https://api.openai.com", "test-api-key")

	tests := []struct {
		name         string
		responseFile string
		validate     func(t *testing.T, result *llm.Response)
	}{
		{
			name:         "stop response transformation",
			responseFile: "stop.response.json",
			validate: func(t *testing.T, result *llm.Response) {
				require.Equal(t, "chat.completion", result.Object)
				require.NotEmpty(t, result.ID)
				require.Equal(t, "gpt-4o", result.Model)
				require.Len(t, result.Choices, 1)
				require.Equal(t, "assistant", result.Choices[0].Message.Role)
				require.NotNil(t, result.Choices[0].Message.Content.Content)
				require.Contains(t, *result.Choices[0].Message.Content.Content, "weather")
				require.NotNil(t, result.Usage)
				require.Greater(t, result.Usage.TotalTokens, int64(0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var responseData json.RawMessage
			// Load the test response data
			err := xtest.LoadTestData(t, tt.responseFile, &responseData)
			if err != nil {
				t.Errorf("Test data file %s not found, skipping test", tt.responseFile)
				return
			}

			// Create HTTP response
			httpResp := &httpclient.Response{
				StatusCode: http.StatusOK,
				Body:       responseData,
			}

			// Transform the response
			result, err := transformer.TransformResponse(context.Background(), httpResp)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Run validation
			tt.validate(t, result)
		})
	}
}
