package biz

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestChannelCodexNativeImagesPreservesRequestedModel(t *testing.T) {
	service, client := setupTestSystemService(t, xcache.Config{Mode: xcache.ModeMemory})
	defer client.Close()
	apiCtx := authz.NewAPIKeyContext(ent.NewContext(t.Context(), client), 1, 1)
	c := &ent.Channel{Name: "native-images", Type: channel.TypeCodex, BaseURL: "https://relay.example/v1", DefaultTestModel: "gpt-6-astra", Credentials: objects.ChannelCredentials{APIKey: "test-key"}}
	ch, err := (&ChannelService{SystemService: service}).buildChannelWithOutbounds(c)
	require.NoError(t, err)
	t.Cleanup(func() { stopChannelOutbounds(ch) })
	outbound, err := BuildOutboundByAPIFormat(ch, llm.APIFormatOpenAIImageGeneration.String())
	require.NoError(t, err)
	for _, model := range []string{"gpt-image-2", "gpt-image-2.5-sunburst"} {
		req, err := outbound.TransformRequest(apiCtx, &llm.Request{
			Model: model, RequestType: llm.RequestTypeImage, APIFormat: llm.APIFormatOpenAIImageGeneration,
			RawRequest: &httpclient.Request{Headers: http.Header{}}, Image: &llm.ImageRequest{Prompt: "a cat"},
		})
		require.NoError(t, err)
		var body struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(req.Body, &body))
		require.Equal(t, model, body.Model)
		require.Equal(t, "https://relay.example/v1/images/generations", req.URL)
	}
}
