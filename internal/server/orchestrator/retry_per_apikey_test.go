package orchestrator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func TestRetryPerAPIKeyRespectsOptInAndConfiguredBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  *bool
		global   int
		override *int
		want     int
	}{
		{"legacy global limit", nil, 1, nil, 2},
		{"off global limit", lo.ToPtr(false), 1, nil, 2},
		{"off zero channel override", lo.ToPtr(false), 3, lo.ToPtr(0), 1},
		{"off channel override", nil, 0, lo.ToPtr(2), 3},
		{"on fills available keys", lo.ToPtr(true), 1, nil, 4},
		{"on extends zero channel override", lo.ToPtr(true), 3, lo.ToPtr(0), 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client := setupTest(t)
			ctx = contexts.EnsureContainer(ctx)
			svc := newTestChannelServiceForChannels(client)
			entity := createRetryGuardChannel(t, ctx, client, "retry-opt-in", objects.ChannelCredentials{APIKeys: []string{"a", "b", "c", "d"}})
			client.Channel.UpdateOneID(entity.ID).SetSettings(&objects.ChannelSettings{
				RetryPerAPIKey:          tc.enabled,
				MaxSingleChannelRetries: tc.override,
			}).SaveX(ctx)
			outbound := newRetryGuardTransformer(svc, retryGuardSnapshot(t, ctx, svc, entity.ID), "")
			outbound.state.RetryPolicy = &biz.RetryPolicy{Enabled: true}
			outbound.state.ChannelModelsCandidates = []*ChannelModelsCandidate{outbound.state.CurrentCandidate}
			var attempted []string
			executor := &credentialRefreshExecutor{do: func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
				key := strings.TrimPrefix(req.Headers.Get("Authorization"), "Bearer ")
				require.NotContains(t, attempted, key)
				attempted = append(attempted, key)
				outbound.state.Perf = &biz.PerformanceRecord{APIKey: key}
				return nil, &httpclient.Error{StatusCode: http.StatusTooManyRequests}
			}}
			p := pipeline.NewFactory(executor).Pipeline(openai.NewInboundTransformer(), outbound, pipeline.WithRetry(0, tc.global, 0))
			_, err := p.Process(ctx, buildTestRequest("test-model", "hello", false))
			require.Error(t, err)
			require.Len(t, attempted, tc.want)
		})
	}
}
