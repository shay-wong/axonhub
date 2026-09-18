package biz

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestChannelCodexImageMainModel(t *testing.T) {
	service, client := setupTestSystemService(t, xcache.Config{Mode: xcache.ModeMemory})
	defer client.Close()
	adminCtx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	apiCtx := authz.NewAPIKeyContext(ent.NewContext(t.Context(), client), 1, 1)
	c := &ent.Channel{Settings: &objects.ChannelSettings{CodexImageMainModel: "legacy-model"}, Credentials: objects.ChannelCredentials{APIKey: "test-key"}}
	ch := buildChannel(c, nil)
	outbound, err := (&ChannelService{SystemService: service}).buildCodexOutbound(c, ch, "https://relay.example/v1", "", "", nil)
	require.NoError(t, err)
	// Reuse the transformer to verify live settings updates on cached channels.
	for _, setting := range []struct {
		model string
		want  string
	}{
		{"", "gpt-6-astra"},
		{" gpt-5.6-sol ", "gpt-5.6-sol"},
		{"  ", "gpt-6-astra"},
	} {
		if setting.model != "" {
			require.NoError(t, service.SetModelSettings(adminCtx, SystemModelSettings{CodexImageMainModel: setting.model}))
		}
		req, err := outbound.TransformRequest(apiCtx, &llm.Request{
			Model: "gpt-image-2", RequestType: llm.RequestTypeImage, APIFormat: llm.APIFormatOpenAIImageGeneration,
			RawRequest: &httpclient.Request{Headers: http.Header{}}, Image: &llm.ImageRequest{Prompt: "a cat"},
		})
		require.NoError(t, err)
		var body struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(req.Body, &body))
		require.Equal(t, setting.want, body.Model)
	}
}
