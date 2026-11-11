package responses_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestToolOutputNamespaceRoundTrip(t *testing.T) {
	items := []responses.Item{
		{Type: "function_call_output", Name: "codex_apps_open_page", Namespace: "agent", Output: &responses.Input{Text: lo.ToPtr(`{"page_id":null}`)}},
		{Type: "function_call_output", Name: "codex_apps_selected_page_part_1_of_1", Namespace: "agent", Output: &responses.Input{Text: lo.ToPtr("Companion Page snapshot part 1 of 1:\n{\"page_id\":null}")}},
		{Type: "function_call", CallID: "call-search", Name: "search", Namespace: "docs", Arguments: "{}"},
		{Type: "function_call_output", ID: "output-search", CallID: "call-search", Name: "search", Namespace: "docs", Output: &responses.Input{Text: lo.ToPtr("result")}},
		{Type: "custom_tool_call", CallID: "call-shell", Name: "shell", Input: lo.ToPtr("echo ok")},
		{Type: "custom_tool_call_output", ID: "output-shell", CallID: "call-shell", Name: "shell", Namespace: "tools__v1", Output: &responses.Input{Text: lo.ToPtr("ok")}},
		{Type: "function_call_output", Name: "agent__plain", Output: &responses.Input{Text: lo.ToPtr("no namespace")}},
	}
	body, err := json.Marshal(responses.Request{Model: "test", Input: responses.Input{Items: items}})
	require.NoError(t, err)
	for _, target := range []string{"responses", "codex-relay", "codex-official"} {
		for _, transport := range []string{responses.TransportHTTP, responses.TransportWebSocket} {
			for _, jsonRoundTrip := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json=%v", target, transport, jsonRoundTrip), func(t *testing.T) {
					req, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: body})
					require.NoError(t, err)
					if jsonRoundTrip {
						encoded, err := json.Marshal(req)
						require.NoError(t, err)
						var restored llm.Request
						require.NoError(t, json.Unmarshal(encoded, &restored))
						req = &restored
					}
					var outbound transformer.Outbound
					if target == "responses" {
						outbound, err = responses.NewOutboundTransformerWithConfig(&responses.Config{
							BaseURL: "https://relay.example/v1", Transport: transport, APIKeyProvider: auth.NewStaticKeyProvider("test-key"),
						})
					} else {
						baseURL := "https://relay.example/v1"
						if target == "codex-official" {
							baseURL = "https://chatgpt.com/backend-api/codex#"
						}
						outbound, err = codex.NewOutboundTransformer(codex.Params{
							BaseURL: baseURL, Transport: transport,
							TokenProvider: oauth.NewStaticTokenProvider(&oauth.OAuthCredentials{AccessToken: "test-token"}),
						})
					}
					require.NoError(t, err)
					for range 2 {
						wire, err := outbound.TransformRequest(t.Context(), req)
						require.NoError(t, err)
						if finalizer, ok := outbound.(transformer.TransportRequestFinalizer); ok {
							wire = finalizer.FinalizeTransportRequest(wire)
						}
						var result responses.Request
						require.NoError(t, json.Unmarshal(wire.Body, &result))
						require.Len(t, result.Input.Items, len(items))
						for i, want := range items {
							if strings.HasSuffix(want.Type, "_output") {
								require.Equal(t, want, result.Input.Items[i], "tool output %d", i)
							}
						}
					}
				})
			}
		}
	}
}

func TestToolOutputNamespaceDoesNotLeakToChatPayload(t *testing.T) {
	req, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"test","input":[{"type":"function_call","name":"search","namespace":"docs","call_id":"call-search","arguments":"{}"},{"type":"function_call_output","name":"search","namespace":"docs","call_id":"call-search","output":"ok"}]}`)})
	require.NoError(t, err)
	chat, err := openai.NewOutboundTransformer("https://relay.example/v1", "test-key")
	require.NoError(t, err)
	wire, err := chat.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	require.NotContains(t, string(wire.Body), `"namespace"`)
	require.NotContains(t, string(wire.Body), `"tool_call_namespace"`)
	// A subsequent Responses attempt must still receive the original namespace.
	outbound, err := responses.NewOutboundTransformer("https://relay.example/v1", "test-key")
	require.NoError(t, err)
	wire, err = outbound.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	var result responses.Request
	require.NoError(t, json.Unmarshal(wire.Body, &result))
	require.Equal(t, "docs", result.Input.Items[1].Namespace)
}
