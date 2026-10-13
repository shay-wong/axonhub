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

func TestChannelCodexNativeImagesIgnoreLegacyMainModel(t *testing.T) {
	service, client := setupTestSystemService(t, xcache.Config{Mode: xcache.ModeMemory})
	defer client.Close()
	adminCtx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	apiCtx := authz.NewAPIKeyContext(ent.NewContext(t.Context(), client), 1, 1)
	c := &ent.Channel{Name: "native-images", Type: channel.TypeCodex, BaseURL: "https://relay.example/v1", DefaultTestModel: "legacy-test-model", Settings: &objects.ChannelSettings{CodexImageMainModel: "legacy-model"}, Credentials: objects.ChannelCredentials{APIKey: "test-key"}}
	ch, err := (&ChannelService{SystemService: service}).buildChannelWithOutbounds(c)
	require.NoError(t, err)
	t.Cleanup(func() { stopChannelOutbounds(ch) })
	outbound, err := BuildOutboundByAPIFormat(ch, llm.APIFormatOpenAIImageGeneration.String())
	require.NoError(t, err)
	// Previously saved global/channel values must not alter native Images.
	for _, model := range []string{"", "gpt-5.6-sol", "gpt-6-astra"} {
		require.NoError(t, service.SetModelSettings(adminCtx, SystemModelSettings{CodexImageMainModel: model}))
		req, err := outbound.TransformRequest(apiCtx, &llm.Request{
			Model: "gpt-image-2", RequestType: llm.RequestTypeImage, APIFormat: llm.APIFormatOpenAIImageGeneration,
			RawRequest: &httpclient.Request{Headers: http.Header{}}, Image: &llm.ImageRequest{Prompt: "a cat"},
		})
		require.NoError(t, err)
		var body struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(req.Body, &body))
		require.Equal(t, "gpt-image-2", body.Model)
		require.Equal(t, "https://relay.example/v1/images/generations", req.URL)
	}
}
