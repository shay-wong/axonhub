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

func TestExplicit429RetriesNeverReuseFailedKeys(t *testing.T) {
	for _, tc := range []struct {
		name        string
		keys        []string
		perKey      bool
		global      int
		override    *int
		wantPrimary int
	}{
		{"single key switches despite spare retries", []string{"a"}, false, 5, nil, 1},
		{"multiple keys stop when exhausted", []string{"a", "b", "c"}, false, 5, nil, 3},
		{"global budget limits key rotation", []string{"a", "b", "c"}, false, 1, nil, 2},
		{"zero channel override prevents key rotation", []string{"a", "b", "c"}, false, 5, lo.ToPtr(0), 1},
		{"per-key budget covers keys once", []string{"a", "b", "c"}, true, 0, lo.ToPtr(0), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client := setupTest(t)
			ctx = contexts.EnsureContainer(ctx)
			svc := newTestChannelServiceForChannels(client)
			primary := createRetryGuardChannel(t, ctx, client, "429-primary", objects.ChannelCredentials{APIKeys: tc.keys})
			client.Channel.UpdateOneID(primary.ID).SetSettings(&objects.ChannelSettings{
				RetryableStatusCodes:    []int{http.StatusTooManyRequests},
				RetryPerAPIKey:          lo.ToPtr(tc.perKey),
				MaxSingleChannelRetries: tc.override,
			}).SaveX(ctx)
			backup := createRetryGuardChannel(t, ctx, client, "429-backup", objects.ChannelCredentials{APIKey: "backup"})
			outbound := newRetryGuardTransformer(svc, retryGuardSnapshot(t, ctx, svc, primary.ID), "")
			outbound.state.RetryPolicy = &biz.RetryPolicy{Enabled: true}
			outbound.state.ChannelModelsCandidates = []*ChannelModelsCandidate{
				outbound.state.CurrentCandidate,
				{Channel: retryGuardSnapshot(t, ctx, svc, backup.ID), Models: []biz.ChannelModelEntry{{ActualModel: "test-model"}}},
			}
			var attempted []string
			executor := &credentialRefreshExecutor{do: func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
				key := strings.TrimPrefix(req.Headers.Get("Authorization"), "Bearer ")
				require.NotContains(t, attempted, key, "explicit 429 must not reselect a failed key")
				attempted = append(attempted, key)
				outbound.state.Perf = &biz.PerformanceRecord{APIKey: key}
				if key == "backup" {
					return &httpclient.Response{StatusCode: http.StatusOK, Body: buildMockOpenAIResponse("backup-success", "test-model", "ok", 1, 1)}, nil
				}
				return nil, &httpclient.Error{StatusCode: http.StatusTooManyRequests}
			}}
			p := pipeline.NewFactory(executor).Pipeline(openai.NewInboundTransformer(), outbound, pipeline.WithRetry(1, tc.global, 0))
			_, err := p.Process(ctx, buildTestRequest("test-model", "hello", false))
			require.NoError(t, err)
			require.Len(t, attempted, tc.wantPrimary+1)
			require.Equal(t, "backup", attempted[len(attempted)-1])
			for _, key := range attempted[:len(attempted)-1] {
				require.True(t, contexts.IsChannelAPIKeyExcluded(ctx, primary.ID, key))
			}
		})
	}
}
