package biz

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestRequestBodyForPersistence(t *testing.T) {
	t.Run("valid wire JSON is authoritative", func(t *testing.T) {
		body, err := requestBodyForPersistence(httpclient.Request{
			Body:     []byte(`{"service_tier":"priority"}`),
			JSONBody: []byte(`{"service_tier":"default"}`),
		})
		require.NoError(t, err)
		require.JSONEq(t, `{"service_tier":"priority"}`, string(body))
	})

	t.Run("non JSON wire body uses logging representation", func(t *testing.T) {
		body, err := requestBodyForPersistence(httpclient.Request{
			Body:     []byte("multipart body"),
			JSONBody: []byte(`{"service_tier":"priority"}`),
		})
		require.NoError(t, err)
		require.JSONEq(t, `{"service_tier":"priority"}`, string(body))
	})
}

func TestChannelAPIKeySnapshot(t *testing.T) {
	channel := &Channel{Channel: &ent.Channel{Credentials: objects.ChannelCredentials{
		APIKeyConfigs: []objects.ChannelAPIKeyConfig{{
			Key:  "sk-upstream-1234",
			Name: "Primary Account",
		}},
	}}}

	identity := channel.APIKeyIdentity("sk-upstream-1234")
	require.Equal(t, "Primary Account", identity.Name)
	require.Equal(t, "1234", identity.Suffix)

	identity = channel.APIKeyIdentity("short")
	require.Empty(t, identity.Name)
	require.Empty(t, identity.Suffix)
}

func TestRequestHeaderNamesUsingAPIKey(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"Bearer sk-upstream-1234"},
		"X-Custom-Key":  []string{"sk-upstream-1234"},
		"X-Unrelated":   []string{"prefix-sk-upstream-1234-suffix"},
	}

	require.Equal(t, []string{"Authorization", "X-Custom-Key"}, requestHeaderNamesUsingAPIKey(headers, "sk-upstream-1234"))
	require.Empty(t, requestHeaderNamesUsingAPIKey(headers, "different-key"))
}

func TestCreateRequestExecutionPersistsAPIKeyIdentityAndMasksHeader(t *testing.T) {
	svc, client, ctx := setupTestRequestService(t)
	defer client.Close()

	proj, err := client.Project.Create().
		SetName("api-key-identity-project").
		SetStatus(project.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("api-key-identity-channel").
		SetBaseURL("https://api.example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKeyConfigs: []objects.ChannelAPIKeyConfig{{
			Key:  "sk-upstream-1234",
			Name: "Primary Account",
		}}}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		Save(ctx)
	require.NoError(t, err)

	req, err := client.Request.Create().
		SetProjectID(proj.ID).
		SetModelID("gpt-4").
		SetRequestBody([]byte(`{"model":"gpt-4"}`)).
		SetStatus(request.StatusProcessing).
		SetStream(false).
		Save(ctx)
	require.NoError(t, err)

	ctx = contexts.WithChannelAPIKey(ctx, "sk-upstream-1234")
	execution, err := svc.CreateRequestExecution(
		ctx,
		&Channel{Channel: channelRow},
		"gpt-4",
		req,
		httpclient.Request{
			Headers: http.Header{
				"Authorization":    []string{"Bearer different-key"},
				"X-Google-Api-Key": []string{"sk-upstream-1234"},
			},
			JSONBody: []byte(`{"model":"gpt-4"}`),
		},
		llm.APIFormatOpenAIChatCompletion,
		"",
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "Primary Account", execution.ChannelAPIKeyName)
	require.Equal(t, "1234", execution.ChannelAPIKeySuffix)
	require.Equal(t, []string{"X-Google-Api-Key"}, execution.ChannelAPIKeyHeaders)
	require.NotContains(t, string(execution.RequestHeaders), "sk-upstream-1234")
	require.Contains(t, string(execution.RequestHeaders), "******")
}
