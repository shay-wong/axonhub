package codex

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// TestCodexOutbound_DefaultUserAgentHidesInboundClientUA pins the ownership of
// the outbound User-Agent (issue #2635): the transformer always presents the
// Codex-client-like default and never copies the inbound client's UA. Forwarding
// the client UA is opt-in through the orchestrator's user-agent pass-through
// middleware, which runs after this transformer and overwrites the default when
// the switch is enabled.
func TestCodexOutbound_DefaultUserAgentHidesInboundClientUA(t *testing.T) {
	ctx := context.Background()
	outbound := newTestCodexOutbound(t)

	const clientUA = "ZCode/3.14.4 (darwin arm64)"

	body := []byte(`{"model":"gpt-5-codex","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	rawReq, err := http.NewRequest(http.MethodPost, "http://localhost:8090/v1/chat/completions", bytes.NewReader(body))
	require.NoError(t, err)
	rawReq.Header.Set("Content-Type", "application/json")
	rawReq.Header.Set("User-Agent", clientUA)

	inboundRequest, err := httpclient.ReadHTTPRequest(rawReq)
	require.NoError(t, err)

	llmReq, err := openai.NewInboundTransformer().TransformRequest(ctx, inboundRequest)
	require.NoError(t, err)
	llmReq.RawRequest = inboundRequest

	req, err := outbound.TransformRequest(ctx, llmReq)
	require.NoError(t, err)

	assert.Equal(t, codexDefaultUserAgent, req.Headers.Get("User-Agent"))
	assert.NotEqual(t, clientUA, req.Headers.Get("User-Agent"))

	// The generic inbound-header merge must not reintroduce the client UA either:
	// User-Agent is a blocked header, so the transformer-owned value survives.
	merged := httpclient.MergeInboundRequest(req, inboundRequest)
	assert.Equal(t, codexDefaultUserAgent, merged.Headers.Get("User-Agent"))
	assert.NotEqual(t, clientUA, merged.Headers.Get("User-Agent"))
}

// TestCodexOutbound_DefaultUserAgentIsCodexLike keeps the fake identity
// consistent with the Version header the transformer fabricates.
func TestCodexOutbound_DefaultUserAgentIsCodexLike(t *testing.T) {
	assert.Equal(t, "codex_cli_rs/"+codexDefaultVersion, codexDefaultUserAgent)
}

// TestCodexOutbound_ScrubsClientFingerprintHeaders pins the codex-scoped half of
// issue #2635: client-private attribution headers must not reach the ChatGPT
// Codex backend. The scrub is deliberately scoped to this channel rather than
// added to httpclient's global blockedHeaders, because Http-Referer and X-Title
// are legitimate app-attribution headers for OpenRouter-style channels.
func TestCodexOutbound_ScrubsClientFingerprintHeaders(t *testing.T) {
	ctx := context.Background()
	outbound := newTestCodexOutbound(t)

	body := []byte(`{"model":"gpt-5-codex","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	rawReq, err := http.NewRequest(http.MethodPost, "http://localhost:8090/v1/chat/completions", bytes.NewReader(body))
	require.NoError(t, err)
	rawReq.Header.Set("Content-Type", "application/json")
	rawReq.Header.Set("User-Agent", "ZCode/3.14.4 (darwin arm64)")
	// Client fingerprint headers from the issue report.
	rawReq.Header.Set("Http-Referer", "https://editor.example/session")
	rawReq.Header.Set("X-Title", "ZCode")
	rawReq.Header.Set("X-Platform", "darwin")
	rawReq.Header.Set("X-Zcode-Session", "zcode-session")
	rawReq.Header.Set("X-Zcode-Client", "ZCode/3.14.4")
	rawReq.Header.Set("X-Os-Name", "macOS")
	// A non-fingerprint custom header must still be forwarded.
	rawReq.Header.Set("X-Custom", "client-value")
	// Originator is genuine Codex identity and must still be honoured.
	rawReq.Header.Set("Originator", "codex_cli_rs")

	inboundRequest, err := httpclient.ReadHTTPRequest(rawReq)
	require.NoError(t, err)

	llmReq, err := openai.NewInboundTransformer().TransformRequest(ctx, inboundRequest)
	require.NoError(t, err)
	llmReq.RawRequest = inboundRequest

	req, err := outbound.TransformRequest(ctx, llmReq)
	require.NoError(t, err)

	// MergeInboundRequest is what actually forwards leftover inbound headers, so
	// assert on the merged request, not just the transformer output.
	merged := httpclient.MergeInboundRequest(req, inboundRequest)
	for _, name := range []string{"Http-Referer", "X-Title", "X-Platform", "X-Zcode-Session", "X-Zcode-Client", "X-Os-Name"} {
		assert.Empty(t, merged.Headers.Get(name), "%s must not leak to the Codex upstream", name)
	}

	assert.Equal(t, "client-value", merged.Headers.Get("X-Custom"))
	assert.Equal(t, "codex_cli_rs", merged.Headers.Get("Originator"))
}

func TestCodexOutbound_FingerprintFilteringPreservesHeadersForFailover(t *testing.T) {
	for _, alphaSearch := range []bool{false, true} {
		name := "responses"
		if alphaSearch {
			name = "alpha-search"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			outbound := newTestCodexOutbound(t)
			raw := &httpclient.Request{
				Body:        []byte(`{"model":"gpt-5-codex","messages":[{"role":"user","content":"hello"}]}`),
				ContentType: "application/json",
				Headers: http.Header{
					"Content-Type": []string{"application/json"},
					"Http-Referer": []string{"https://editor.example/session"},
					"x-title":      []string{"Editor"},
					"X-PLATFORM":   []string{"darwin"},
					"x-zcode-id":   []string{"client-session"},
					"X-OS-NAME":    []string{"macOS"},
					"X-Custom":     []string{"keep"},
					"User-Agent":   []string{"Editor/1.0"},
				},
			}
			raw.Headers.Set(SessionHeader, "session-underscore")
			raw.Headers.Set(SessionHeaderHyphen, "session-hyphen")
			raw.Headers.Set(ResponsesLiteHeader, "false")
			original := raw.Headers.Clone()
			request, err := openai.NewInboundTransformer().TransformRequest(ctx, raw)
			require.NoError(t, err)
			request.RawRequest = raw
			if alphaSearch {
				request.RequestType = llm.RequestTypeAlphaSearch
				request.APIFormat = llm.APIFormatOpenAIAlphaSearch
				request.AlphaSearch = &llm.AlphaSearchRequest{Body: []byte(`{"model":"gpt-5-codex","commands":{"search_query":[]}}`)}
			}

			for range 2 {
				transformed, err := outbound.TransformRequest(ctx, request)
				require.NoError(t, err)
				merged := httpclient.MergeInboundRequest(transformed, raw)
				for _, header := range []string{"Http-Referer", "x-title", "X-PLATFORM", "x-zcode-id", "X-OS-NAME"} {
					require.NotContains(t, merged.Headers, header)
					require.Empty(t, merged.Headers.Get(header))
				}
				require.Equal(t, "keep", merged.Headers.Get("X-Custom"))
				require.Equal(t, codexDefaultUserAgent, merged.Headers.Get("User-Agent"))
				if !alphaSearch {
					require.Equal(t, "session-underscore", merged.Headers.Get(SessionHeaderHyphen))
					require.Empty(t, merged.Headers.Get(SessionHeader))
					require.Empty(t, merged.Headers.Get(ResponsesLiteHeader))
				}
				require.Equal(t, original, raw.Headers)
			}

			// Failover to an ordinary provider must still receive its attribution.
			ordinary := httpclient.MergeInboundRequest(&httpclient.Request{Headers: make(http.Header)}, raw)
			for _, header := range []string{"Http-Referer", "x-title", "X-PLATFORM", "x-zcode-id", "X-OS-NAME"} {
				require.Equal(t, original[header], ordinary.Headers[header])
			}
			require.Equal(t, original, raw.Headers)
		})
	}
}
