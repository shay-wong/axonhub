package gql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

// 模型性能按实际执行模型归属，且主请求被清理后仍应保留统计。
func TestModelPerformanceStatsUsesExecutionModelAfterRequestGC(t *testing.T) {
	resolver, ctx := newDashboardUsageTestResolver(t)
	client := resolver.client

	ch := createDashboardUsageChannel(t, ctx, client, "channel-a")

	req, err := client.Request.Create().
		SetModelID("request-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(request.StatusCompleted).
		SetChannelID(ch.ID).
		SetStream(true).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetStream(true).
		SetMetricsLatencyMs(2000).
		SetMetricsFirstTokenLatencyMs(500).
		Save(ctx)
	require.NoError(t, err)

	// A retry produces another completed execution for the same request. The
	// dashboard must count the request once, not once per execution.
	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetStream(true).
		SetMetricsLatencyMs(3000).
		SetMetricsFirstTokenLatencyMs(600).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusFailed).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.UsageLog.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetPromptTokens(10).
		SetCompletionTokens(100).
		SetTotalTokens(110).
		Save(ctx)
	require.NoError(t, err)

	stats, err := resolver.ModelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "channel-model", stats[0].ModelID)
	require.Equal(t, 1, stats[0].RequestCount)

	channelStats, err := resolver.ChannelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Len(t, channelStats, 1)
	require.Equal(t, "channel-a", channelStats[0].ChannelName)
	require.Equal(t, 1, channelStats[0].RequestCount)
	require.NotNil(t, channelStats[0].Throughput)

	// 清空缓存后重新查询，确保验证的是保留下来的执行和用量记录。
	require.NoError(t, client.Request.DeleteOneID(req.ID).Exec(ctx))
	requestCount, err := client.Request.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, requestCount)
	resetDashboardQueryCache()

	statsAfterGC, err := resolver.ModelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Equal(t, stats, statsAfterGC)

	channelStatsAfterGC, err := resolver.ChannelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Equal(t, channelStats, channelStatsAfterGC)
}

func TestFetchChannelNamesPropagatesLookupErrors(t *testing.T) {
	resolver, ctx := newDashboardUsageTestResolver(t)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err := resolver.fetchChannelNames(canceledCtx, []int{1})
	require.Error(t, err, "channel-name lookup failures must not be hidden behind fallback labels")
}
