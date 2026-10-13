package codex

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/transformer"
)

func TestCodexNativeImagesPreserveHeaderContracts(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIImageGeneration, llm.APIFormatOpenAIImageEdit} {
		t.Run(format.String(), func(t *testing.T) {
			out, err := NewImageOutboundTransformer(ImageParams{
				BaseURL:       "https://relay.example/v1",
				TokenProvider: oauth.NewStaticTokenProvider(&oauth.OAuthCredentials{AccessToken: "test-token"}),
			})
			require.NoError(t, err)
			finalizer, ok := out.(transformer.TransportRequestFinalizer)
			require.True(t, ok)
			raw := &httpclient.Request{Headers: http.Header{
				"User-Agent":   {"PrivateEditor/1.0"},
				"Originator":   {"codex_cli_rs"},
				"Http-Referer": {"https://editor.example/session"},
				"x-title":      {"Editor"},
				"X-PLATFORM":   {"darwin"},
				"x-zcode-id":   {"private-session"},
				"X-OS-NAME":    {"macOS"},
				"X-Custom":     {"keep"},
			}}
			raw.Headers.Set(SessionHeader, " legacy-session ")
			raw.Headers.Set(SessionHeaderHyphen, "modern-session")
			original := raw.Headers.Clone()
			input := &llm.Request{
				Model: "gpt-image-2", RequestType: llm.RequestTypeImage, APIFormat: format,
				RawRequest: raw, Image: &llm.ImageRequest{Prompt: "draw", Images: [][]byte{[]byte("image")}},
			}
			for range 2 {
				request, err := out.TransformRequest(t.Context(), input)
				require.NoError(t, err)
				request = httpclient.MergeInboundRequest(request, raw)
				require.Equal(t, codexDefaultUserAgent, request.Headers.Get("User-Agent"))
				require.Equal(t, "legacy-session", request.Headers.Get(SessionHeaderHyphen))
				require.Empty(t, request.Headers.Get(SessionHeader))
				for _, header := range []string{"Http-Referer", "x-title", "X-PLATFORM", "x-zcode-id", "X-OS-NAME"} {
					require.NotContains(t, request.Headers, header)
					require.Empty(t, request.Headers.Get(header))
				}
				require.Equal(t, "codex_cli_rs", request.Headers.Get("Originator"))
				require.Equal(t, "keep", request.Headers.Get("X-Custom"))
				body := string(request.Body)
				url := request.URL
				// The finalizer runs after user-agent opt-in and channel header overrides.
				request.Headers.Set("User-Agent", raw.Headers.Get("User-Agent"))
				request.Headers.Set(SessionHeader, "legacy-override")
				request.Headers.Set(SessionHeaderHyphen, "modern-override")
				request = finalizer.FinalizeTransportRequest(request)
				require.Equal(t, "legacy-session", request.Headers.Get(SessionHeaderHyphen))
				require.Empty(t, request.Headers.Get(SessionHeader))
				require.Equal(t, "PrivateEditor/1.0", request.Headers.Get("User-Agent"))
				require.Equal(t, body, string(request.Body))
				require.Equal(t, url, request.URL)
				require.Equal(t, format.String(), request.APIFormat)
				require.Equal(t, "application/json", request.Headers.Get("Content-Type"))
				require.Equal(t, "application/json", request.Headers.Get("Accept"))
				require.Equal(t, original, raw.Headers)
			}
		})
	}
}
