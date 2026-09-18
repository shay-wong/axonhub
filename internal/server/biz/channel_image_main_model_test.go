package biz

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestChannelCodexImageMainModel(t *testing.T) {
	for _, setting := range []struct {
		settings *objects.ChannelSettings
		want     string
	}{
		{nil, "gpt-6-astra"},
		{&objects.ChannelSettings{CodexImageMainModel: "gpt-5.6-sol"}, "gpt-5.6-sol"},
	} {
		c := &ent.Channel{Settings: setting.settings, Credentials: objects.ChannelCredentials{APIKey: "test-key"}}
		ch := buildChannel(c, nil)
		outbound, err := (&ChannelService{}).buildCodexOutbound(c, ch, "https://relay.example/v1", "", "", nil)
		require.NoError(t, err)
		req, err := outbound.TransformRequest(t.Context(), &llm.Request{
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
