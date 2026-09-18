package codex

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestCodexImageMainModel(t *testing.T) {
	for _, setting := range []struct{ value, want string }{
		{"", "gpt-6-astra"}, {"  ", "gpt-6-astra"}, {" gpt-5.6-sol ", "gpt-5.6-sol"},
	} {
		t.Run(setting.value, func(t *testing.T) {
			outbound, err := NewOutboundTransformer(Params{
				ImageMainModel: setting.value,
				TokenProvider: staticTokenGetter{creds: &oauth.OAuthCredentials{
					AccessToken: testAccessTokenWithAccountID(t), ExpiresAt: time.Now().Add(time.Hour),
				}},
			})
			require.NoError(t, err)
			for _, format := range []llm.APIFormat{llm.APIFormatOpenAIImageGeneration, llm.APIFormatOpenAIImageEdit} {
				input := &llm.Request{
					Model: "gpt-image-2", RequestType: llm.RequestTypeImage, APIFormat: format,
					RawRequest: &httpclient.Request{Headers: http.Header{}},
					Image:      &llm.ImageRequest{Prompt: "a cat", Images: [][]byte{[]byte("image")}},
				}
				req, err := outbound.TransformRequest(t.Context(), input)
				require.NoError(t, err)
				var payload responses.Request
				require.NoError(t, json.Unmarshal(req.Body, &payload))
				require.Equal(t, setting.want, payload.Model)
				require.Equal(t, "gpt-image-2", payload.Tools[0].Model)
				require.Equal(t, "gpt-image-2", input.Model)
			}
			// A configured image main model must not replace an explicit chat model.
			req, err := outbound.TransformRequest(t.Context(), &llm.Request{
				Model: "gpt-5.6-terra", RawRequest: &httpclient.Request{Headers: http.Header{}},
			})
			require.NoError(t, err)
			var payload responses.Request
			require.NoError(t, json.Unmarshal(req.Body, &payload))
			require.Equal(t, "gpt-5.6-terra", payload.Model)
		})
	}
}
