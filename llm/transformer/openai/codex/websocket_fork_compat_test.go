package codex

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestCodexWebSocketLaneCanonicalSessionForkCompatibility(t *testing.T) {
	upgrader := websocket.Upgrader{}
	captured := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_lane","status":"completed","output":[]}}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	lane := shared.NewResponsesLane("lane-session", shared.NewResponsesBudget(1024))
	defer lane.Close()
	ctx := shared.WithResponsesLane(shared.WithSessionScope(t.Context(), "fork-compat"), lane)
	outbound, err := NewOutboundTransformer(Params{
		BaseURL: "wss://chatgpt.com/backend-api/codex#", Transport: responses.TransportWebSocket,
		TokenProvider: staticTokenGetter{creds: &oauth.OAuthCredentials{AccessToken: testAccessTokenWithAccountID(t), ExpiresAt: time.Now().Add(time.Hour)}},
	})
	require.NoError(t, err)
	executor := outbound.CustomizeExecutor(httpclient.NewHttpClientWithClient(server.Client()))
	request, err := outbound.TransformRequest(ctx, &llm.Request{
		Model: "gpt-6-astra", APIFormat: llm.APIFormatOpenAIResponseWebSocket,
		RawRequest: &httpclient.Request{Headers: http.Header{SessionHeader: {"client-session"}}},
		Messages:   []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("Hello")}}},
	})
	require.NoError(t, err)
	request.URL = server.URL + "/v1/responses"
	request.Headers.Set(SessionHeader, "stale-override")
	stream, err := executor.DoStream(ctx, request)
	require.NoError(t, err)
	defer stream.Close()
	require.True(t, stream.Next())
	select {
	case headers := <-captured:
		require.Equal(t, "client-session", headers.Get(SessionHeaderHyphen))
		require.Empty(t, headers.Get(SessionHeader))
	case <-time.After(time.Second):
		t.Fatal("missing websocket upgrade")
	}
}
