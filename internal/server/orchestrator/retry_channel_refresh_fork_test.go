package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

type credentialRefreshExecutor struct {
	mockExecutor
	do func(context.Context, *httpclient.Request) (*httpclient.Response, error)
}

func (e *credentialRefreshExecutor) Do(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return e.do(ctx, req)
}

func TestRefreshChannelBeforeRetry_PreservesKeyRotationBudget(t *testing.T) {
	for _, tc := range []struct {
		keys   int
		budget int
	}{
		{keys: 3, budget: 0},
		{keys: 5, budget: 2},
	} {
		t.Run(fmt.Sprintf("%d keys with configured budget %d", tc.keys, tc.budget), func(t *testing.T) {
			ctx, client := setupTest(t)
			ctx = contexts.EnsureContainer(ctx)
			svc := newTestChannelServiceForChannels(client)
			keys := make([]string, tc.keys)
			for i := range keys {
				keys[i] = fmt.Sprintf("key-%d", i)
			}
			entity := createRetryGuardChannel(t, ctx, client, "rotation-budget", objects.ChannelCredentials{APIKeys: keys})
			snapshot := retryGuardSnapshot(t, ctx, svc, entity.ID)
			outbound := newRetryGuardTransformer(svc, snapshot, "")
			outbound.state.RetryPolicy = &biz.RetryPolicy{Enabled: true}
			outbound.state.ChannelModelsCandidates = []*ChannelModelsCandidate{outbound.state.CurrentCandidate}
			var attempts []string
			var disabled []objects.DisabledAPIKey
			executor := &credentialRefreshExecutor{do: func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
				key := strings.TrimPrefix(req.Headers.Get("Authorization"), "Bearer ")
				require.NotEmpty(t, key)
				require.NotContains(t, attempts, key, "a failed key must not be selected again")
				attempts = append(attempts, key)
				outbound.state.Perf = &biz.PerformanceRecord{APIKey: key}
				if len(attempts) == len(keys) {
					return &httpclient.Response{StatusCode: http.StatusOK, Body: buildMockOpenAIResponse("retry-success", "test-model", "ok", 1, 1)}, nil
				}
				// Commit each disable before the retry, without refreshing the pinned
				// candidate or relying on timing of the asynchronous health worker.
				disabled = append(disabled, objects.DisabledAPIKey{Key: key})
				client.Channel.UpdateOneID(entity.ID).SetDisabledAPIKeys(disabled).SaveX(ctx)
				return nil, &httpclient.Error{StatusCode: http.StatusTooManyRequests, Body: []byte(`{"error":{"message":"key exhausted"}}`)}
			}}
			p := pipeline.NewFactory(executor).Pipeline(openai.NewInboundTransformer(), outbound, pipeline.WithRetry(0, tc.budget, 0))
			_, err := p.Process(ctx, buildTestRequest("test-model", "hello", false))
			require.NoError(t, err)
			require.ElementsMatch(t, keys, attempts, "try every remaining key before giving up")

			// A different candidate starts with its own budget, even though the
			// prior candidate had more keys before its credentials were refreshed.
			next := createRetryGuardChannel(t, ctx, client, "next-budget", objects.ChannelCredentials{APIKeys: []string{"next-a", "next-b"}})
			nextCandidate := &ChannelModelsCandidate{
				Channel: retryGuardSnapshot(t, ctx, svc, next.ID),
				Models:  []biz.ChannelModelEntry{{ActualModel: "test-model"}},
			}
			outbound.state.ChannelModelsCandidates = append(outbound.state.ChannelModelsCandidates, nextCandidate)
			require.NoError(t, outbound.NextChannel(ctx))
			require.Equal(t, 1, outbound.MaxSameChannelRetries(), "do not carry the previous candidate's budget into failover")
		})
	}
}

func TestRefreshChannelBeforeRetry_PreservesDisabledDiagnosticKey(t *testing.T) {
	for _, source := range []string{"context", "request", "execution"} {
		t.Run(source, func(t *testing.T) {
			ctx, client := setupTest(t)
			ctx = contexts.EnsureContainer(ctx)
			svc := newTestChannelServiceForChannels(client)
			entity := createRetryGuardChannel(t, ctx, client, "diagnostic-key", objects.ChannelCredentials{APIKeys: []string{"disabled-key", "healthy-key"}})
			client.Channel.UpdateOneID(entity.ID).SetDisabledAPIKeys([]objects.DisabledAPIKey{{Key: "disabled-key"}}).SaveX(ctx)
			snapshot, err := svc.GetChannelWithKey(ctx, entity.ID, "disabled-key")
			require.NoError(t, err)
			outbound := newRetryGuardTransformer(svc, snapshot, "")
			outbound.state.RetryPolicy = &biz.RetryPolicy{Enabled: true}
			outbound.state.ChannelModelsCandidates = []*ChannelModelsCandidate{outbound.state.CurrentCandidate}
			switch source {
			case "context":
				ctx = contexts.WithSource(ctx, request.SourceTest)
			case "request":
				outbound.state.Request = &ent.Request{Source: request.SourceTest}
			case "execution":
				outbound.state.RequestExec = &ent.RequestExecution{Source: requestexecution.SourceTest}
			}
			var attempts []string
			executor := &credentialRefreshExecutor{do: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
				key := strings.TrimPrefix(req.Headers.Get("Authorization"), "Bearer ")
				require.NotEmpty(t, key)
				attempts = append(attempts, key)
				outbound.state.Perf = &biz.PerformanceRecord{APIKey: key}
				if key == "healthy-key" {
					return &httpclient.Response{StatusCode: http.StatusOK, Body: buildMockOpenAIResponse("wrong-key", "test-model", "ok", 1, 1)}, nil
				}
				return nil, &httpclient.Error{StatusCode: http.StatusInternalServerError, Body: []byte(`{"error":{"message":"disabled key failed"}}`)}
			}}
			p := pipeline.NewFactory(executor).Pipeline(openai.NewInboundTransformer(), outbound, pipeline.WithRetry(0, 2, 0))
			_, err = p.Process(ctx, buildTestRequest("test-model", "hello", false))
			require.Error(t, err, "a successful request with another key must not pass this key's diagnostic")
			require.Equal(t, []string{"disabled-key", "disabled-key", "disabled-key"}, attempts)
		})
	}
}
