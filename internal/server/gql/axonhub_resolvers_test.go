package gql

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelprobe"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
)

func setupTestQueryResolver(t *testing.T) (*queryResolver, context.Context, *ent.Client) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	resolver := &queryResolver{&Resolver{client: client}}

	return resolver, ctx, client
}

type dashboardUsageFixture struct {
	Resolver *queryResolver
	Ctx      context.Context
	Client   *ent.Client
	Project  *ent.Project
	APIKey   *ent.APIKey
	Channel  *ent.Channel
	Now      time.Time
}

func setupDashboardUsageFixture(t *testing.T) dashboardUsageFixture {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	ctx := ent.NewContext(t.Context(), client)
	ctx = authz.WithTestBypass(ctx)

	projectRow, err := client.Project.Create().
		SetName("Dashboard Project").
		SetDescription("dashboard test project").
		Save(ctx)
	require.NoError(t, err)

	apiKeyRow, err := client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetKey("dashboard-key").
		SetName("Dashboard API Key").
		SetType(apikey.TypeServiceAccount).
		SetStatus(apikey.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Dashboard Channel").
		SetCredentials(objects.ChannelCredentials{}).
		SetSupportedModels([]string{"gpt-4", "gpt-4-test"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	systemService := biz.NewSystemService(biz.SystemServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})
	resolver := &queryResolver{&Resolver{
		client:        client,
		systemService: systemService,
	}}

	now := time.Now().UTC().Truncate(time.Second)
	createDashboardUsageRecord(t, ctx, client, dashboardUsageRecord{
		ProjectID:        projectRow.ID,
		APIKeyID:         apiKeyRow.ID,
		ChannelID:        channelRow.ID,
		ModelID:          "gpt-4",
		RequestSource:    request.SourceAPI,
		UsageSource:      usagelog.SourceAPI,
		RequestStatus:    request.StatusCompleted,
		ExecutionStatus:  requestexecution.StatusCompleted,
		PromptTokens:     10,
		CompletionTokens: 20,
		CachedTokens:     3,
		ReasoningTokens:  4,
		TotalTokens:      30,
		TotalCost:        1.25,
		CreatedAt:        now,
	})
	createDashboardUsageRecord(t, ctx, client, dashboardUsageRecord{
		ProjectID:        projectRow.ID,
		APIKeyID:         apiKeyRow.ID,
		ChannelID:        channelRow.ID,
		ModelID:          "gpt-4-test",
		RequestSource:    request.SourceTest,
		UsageSource:      usagelog.SourceTest,
		RequestStatus:    request.StatusFailed,
		ExecutionStatus:  requestexecution.StatusFailed,
		PromptTokens:     100,
		CompletionTokens: 200,
		CachedTokens:     30,
		ReasoningTokens:  40,
		TotalTokens:      300,
		TotalCost:        12.5,
		CreatedAt:        now,
	})

	return dashboardUsageFixture{
		Resolver: resolver,
		Ctx:      ctx,
		Client:   client,
		Project:  projectRow,
		APIKey:   apiKeyRow,
		Channel:  channelRow,
		Now:      now,
	}
}

type dashboardUsageRecord struct {
	ProjectID        int
	APIKeyID         int
	ChannelID        int
	ModelID          string
	RequestSource    request.Source
	UsageSource      usagelog.Source
	RequestStatus    request.Status
	ExecutionStatus  requestexecution.Status
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	ReasoningTokens  int64
	TotalTokens      int64
	TotalCost        float64
	CreatedAt        time.Time
}

func createDashboardUsageRecord(t *testing.T, ctx context.Context, client *ent.Client, record dashboardUsageRecord) {
	t.Helper()

	req, err := client.Request.Create().
		SetProjectID(record.ProjectID).
		SetAPIKeyID(record.APIKeyID).
		SetChannelID(record.ChannelID).
		SetModelID(record.ModelID).
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(record.RequestStatus).
		SetSource(record.RequestSource).
		SetStream(true).
		SetMetricsLatencyMs(1000).
		SetMetricsFirstTokenLatencyMs(100).
		SetCreatedAt(record.CreatedAt).
		SetUpdatedAt(record.CreatedAt).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(record.ChannelID).
		SetSource(requestexecution.Source(record.RequestSource)).
		SetModelID(record.ModelID).
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(record.ExecutionStatus).
		SetStream(true).
		SetMetricsLatencyMs(1000).
		SetMetricsFirstTokenLatencyMs(100).
		SetCreatedAt(record.CreatedAt).
		SetUpdatedAt(record.CreatedAt).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.UsageLog.Create().
		SetRequestID(req.ID).
		SetProjectID(record.ProjectID).
		SetAPIKeyID(record.APIKeyID).
		SetChannelID(record.ChannelID).
		SetModelID(record.ModelID).
		SetSource(record.UsageSource).
		SetPromptTokens(record.PromptTokens).
		SetCompletionTokens(record.CompletionTokens).
		SetPromptCachedTokens(record.CachedTokens).
		SetCompletionReasoningTokens(record.ReasoningTokens).
		SetTotalTokens(record.TotalTokens).
		SetTotalCost(record.TotalCost).
		SetCreatedAt(record.CreatedAt).
		SetUpdatedAt(record.CreatedAt).
		Save(ctx)
	require.NoError(t, err)
}

func TestChannelResolver_DisabledAPIKeys_FiltersExpiredTemporaryKeys(t *testing.T) {
	_, ctx, client := setupTestQueryResolver(t)
	defer client.Close()

	resolver := &channelResolver{&Resolver{client: client}}
	ctx = contexts.WithUser(ctx, &ent.User{Scopes: []string{string(scopes.ScopeWriteChannels)}})

	expiredUntil := time.Now().Add(-time.Minute)
	activeUntil := time.Now().Add(time.Minute)
	ch := &ent.Channel{
		DisabledAPIKeys: []objects.DisabledAPIKey{
			{Key: "permanent-key"},
			{Key: "expired-temporary-key", DisabledUntil: &expiredUntil},
			{Key: "active-temporary-key", DisabledUntil: &activeUntil},
		},
	}

	keys, err := resolver.DisabledAPIKeys(ctx, ch)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.ElementsMatch(t, []string{"permanent-key", "active-temporary-key"}, []string{keys[0].Key, keys[1].Key})
}

func TestMutationResolver_TestChannel_SourceTestSkipsHealthStateTracking(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	defer upstream.Close()

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Resolver Test Source Channel").
		SetBaseURL(upstream.URL).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-api-key"}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	cacheConfig := xcache.Config{Mode: xcache.ModeMemory}
	systemService := biz.NewSystemService(biz.SystemServiceParams{
		CacheConfig: cacheConfig,
		Ent:         client,
	})
	channelService := biz.NewChannelServiceForTest(client)
	usageLogService := biz.NewUsageLogService(client, systemService, channelService)
	dataStorageService := &biz.DataStorageService{
		AbstractService: &biz.AbstractService{},
		SystemService:   systemService,
		Cache:           xcache.NewFromConfig[ent.DataStorage](cacheConfig),
	}
	requestService := biz.NewRequestService(client, cacheConfig, systemService, usageLogService, dataStorageService, biz.NewLiveStreamRegistry())
	promptProtectionRuleService := biz.NewPromptProtectionRuleService(biz.PromptProtectionRuleServiceParams{
		CacheConfig: cacheConfig,
		Ent:         client,
	})
	defer promptProtectionRuleService.Stop()

	require.NoError(t, systemService.SetRetryPolicy(ctx, &biz.RetryPolicy{
		Enabled: false,
		ChannelAutoDisable: biz.AutoDisablePolicy{
			Enabled: true,
			Statuses: []biz.AutoDisableStatusRule{
				{Status: http.StatusServiceUnavailable, Times: 1, Action: biz.DisableActionPermanent},
			},
		},
		APIKeyAutoDisable: biz.AutoDisablePolicy{
			Enabled: true,
			Statuses: []biz.AutoDisableStatusRule{
				{Status: http.StatusServiceUnavailable, Times: 1, Action: biz.DisableActionPermanent},
			},
		},
	}))

	mutationResolver := &mutationResolver{&Resolver{
		systemService:               systemService,
		channelService:              channelService,
		requestService:              requestService,
		promptProtectionRuleService: promptProtectionRuleService,
		TestChannelOrchestrator: orchestrator.NewTestChannelOrchestrator(
			channelService,
			requestService,
			systemService,
			usageLogService,
			promptProtectionRuleService,
			httpclient.NewHttpClientWithClient(upstream.Client()),
		),
	}}

	result, err := mutationResolver.TestChannel(ctx, TestChannelInput{
		ChannelID: objects.GUID{ID: channelRow.ID},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Success)

	require.Eventually(t, func() bool {
		count, err := client.RequestExecution.Query().Count(ctx)
		if err != nil {
			return false
		}

		return count == 1
	}, time.Second, 10*time.Millisecond)

	metrics, err := channelService.GetChannelMetrics(ctx, channelRow.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), metrics.RequestCount)
	require.Equal(t, int64(0), metrics.FailureCount)
	require.Equal(t, int64(0), metrics.ConsecutiveFailures)
	require.Nil(t, metrics.LastFailureAt)

	updatedCh, err := client.Channel.Get(ctx, channelRow.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Empty(t, updatedCh.DisabledAPIKeys)
}

func TestQueryResolver_ChannelSuccessRates_ExcludesTestSourceExecutions(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Dashboard Channel").
		SetCredentials(objects.ChannelCredentials{}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	now := time.Now()
	prodReq, err := client.Request.Create().
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(request.StatusCompleted).
		SetSource(request.SourceAPI).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	testReq, err := client.Request.Create().
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(request.StatusFailed).
		SetSource(request.SourceTest).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RequestExecution.Create().
		SetRequestID(prodReq.ID).
		SetChannelID(channelRow.ID).
		SetSource(requestexecution.SourceAPI).
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.RequestExecution.Create().
		SetRequestID(testReq.ID).
		SetChannelID(channelRow.ID).
		SetSource(requestexecution.SourceTest).
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusFailed).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	resolver := &queryResolver{&Resolver{
		client: client,
		systemService: biz.NewSystemService(biz.SystemServiceParams{
			CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
			Ent:         client,
		}),
	}}

	result, err := resolver.ChannelSuccessRates(ctx, lo.ToPtr("allTime"), nil)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, channelRow.ID, result[0].ChannelID.ID)
	require.Equal(t, 1, result[0].SuccessCount)
	require.Equal(t, 0, result[0].FailedCount)
	require.Equal(t, 1, result[0].TotalCount)
	require.Equal(t, 100.0, result[0].SuccessRate)
}

func TestQueryResolver_DashboardRequestStats_ExcludeTestSource(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	defer fixture.Client.Close()

	overview, err := fixture.Resolver.DashboardOverview(fixture.Ctx)
	require.NoError(t, err)
	require.Equal(t, 1, overview.TotalRequests)
	require.Equal(t, 0, overview.FailedRequests)
	require.Equal(t, 1, overview.RequestStats.RequestsToday)
	require.Equal(t, 1, overview.RequestStats.RequestsThisWeek)
	require.Equal(t, 1, overview.RequestStats.RequestsThisMonth)

	byChannel, err := fixture.Resolver.RequestStatsByChannel(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byChannel, 1)
	require.Equal(t, "Dashboard Channel", byChannel[0].ChannelName)
	require.Equal(t, 1, byChannel[0].Count)

	byModel, err := fixture.Resolver.RequestStatsByModel(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byModel, 1)
	require.Equal(t, "gpt-4", byModel[0].ModelID)
	require.Equal(t, 1, byModel[0].Count)

	byAPIKey, err := fixture.Resolver.RequestStatsByAPIKey(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byAPIKey, 1)
	require.Equal(t, fixture.APIKey.ID, byAPIKey[0].APIKeyID.ID)
	require.Equal(t, 1, byAPIKey[0].Count)
}

func TestQueryResolver_DashboardTokenStats_ExcludeTestSource(t *testing.T) {
	InvalidateAllTimeTokenStatsCache()
	t.Cleanup(InvalidateAllTimeTokenStatsCache)

	fixture := setupDashboardUsageFixture(t)
	defer fixture.Client.Close()

	tokenStats, err := fixture.Resolver.TokenStats(fixture.Ctx)
	require.NoError(t, err)
	require.Equal(t, 10, tokenStats.TotalInputTokensToday)
	require.Equal(t, 20, tokenStats.TotalOutputTokensToday)
	require.Equal(t, 3, tokenStats.TotalCachedTokensToday)
	require.Equal(t, 10, tokenStats.TotalInputTokensAllTime)
	require.Equal(t, 20, tokenStats.TotalOutputTokensAllTime)
	require.Equal(t, 3, tokenStats.TotalCachedTokensAllTime)

	byChannel, err := fixture.Resolver.TokenStatsByChannel(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byChannel, 1)
	require.Equal(t, fixture.Channel.ID, byChannel[0].ChannelID.ID)
	require.Equal(t, 10, byChannel[0].InputTokens)
	require.Equal(t, 20, byChannel[0].OutputTokens)
	require.Equal(t, 3, byChannel[0].CachedTokens)
	require.Equal(t, 4, byChannel[0].ReasoningTokens)
	require.Equal(t, 30, byChannel[0].TotalTokens)

	byModel, err := fixture.Resolver.TokenStatsByModel(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byModel, 1)
	require.Equal(t, "gpt-4", byModel[0].ModelID)
	require.Equal(t, 10, byModel[0].InputTokens)
	require.Equal(t, 20, byModel[0].OutputTokens)
	require.Equal(t, 3, byModel[0].CachedTokens)
	require.Equal(t, 4, byModel[0].ReasoningTokens)
	require.Equal(t, 30, byModel[0].TotalTokens)

	byAPIKey, err := fixture.Resolver.TokenStatsByAPIKey(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byAPIKey, 1)
	require.Equal(t, fixture.APIKey.ID, byAPIKey[0].APIKeyID.ID)
	require.Equal(t, 10, byAPIKey[0].InputTokens)
	require.Equal(t, 20, byAPIKey[0].OutputTokens)
	require.Equal(t, 3, byAPIKey[0].CachedTokens)
	require.Equal(t, 4, byAPIKey[0].ReasoningTokens)
	require.Equal(t, 30, byAPIKey[0].TotalTokens)
}

func TestQueryResolver_DashboardCostStats_ExcludeTestSource(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	defer fixture.Client.Close()

	byChannel, err := fixture.Resolver.CostStatsByChannel(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byChannel, 1)
	require.Equal(t, "Dashboard Channel", byChannel[0].ChannelName)
	require.Equal(t, 1.25, byChannel[0].Cost)

	byModel, err := fixture.Resolver.CostStatsByModel(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byModel, 1)
	require.Equal(t, "gpt-4", byModel[0].ModelID)
	require.Equal(t, 1.25, byModel[0].Cost)

	byAPIKey, err := fixture.Resolver.CostStatsByAPIKey(fixture.Ctx, lo.ToPtr("allTime"))
	require.NoError(t, err)
	require.Len(t, byAPIKey, 1)
	require.Equal(t, fixture.APIKey.ID, byAPIKey[0].APIKeyID.ID)
	require.Equal(t, 1.25, byAPIKey[0].Cost)
}

func TestQueryResolver_UsageStatsByUser_UsesUsageLogVisibilityPolicy(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:usage_stats_visibility?mode=memory&_fk=0")
	defer client.Close()

	setupCtx := ent.NewContext(t.Context(), client)
	setupCtx = authz.WithSystemBypass(setupCtx, "usage stats visibility test setup")

	projectRow, err := client.Project.Create().
		SetName("Usage Stats Project").
		SetDescription("usage stats test project").
		SetStatus(project.StatusActive).
		Save(setupCtx)
	require.NoError(t, err)

	ownerUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetPassword("password").
		SetFirstName("Owner").
		SetLastName("User").
		SetStatus(user.StatusActivated).
		Save(setupCtx)
	require.NoError(t, err)

	otherUser, err := client.User.Create().
		SetEmail("other@example.com").
		SetPassword("password").
		SetFirstName("Other").
		SetLastName("User").
		SetStatus(user.StatusActivated).
		Save(setupCtx)
	require.NoError(t, err)

	_, err = client.UserProject.Create().
		SetUserID(ownerUser.ID).
		SetProjectID(projectRow.ID).
		SetIsOwner(true).
		Save(setupCtx)
	require.NoError(t, err)

	_, err = client.UserProject.Create().
		SetUserID(otherUser.ID).
		SetProjectID(projectRow.ID).
		SetIsOwner(false).
		SetScopes([]string{string(scopes.ScopeReadRequests)}).
		Save(setupCtx)
	require.NoError(t, err)

	ownerUser, err = client.User.Query().
		Where(user.ID(ownerUser.ID)).
		WithProjectUsers().
		Only(setupCtx)
	require.NoError(t, err)

	otherUser, err = client.User.Query().
		Where(user.ID(otherUser.ID)).
		WithProjectUsers().
		Only(setupCtx)
	require.NoError(t, err)

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Usage Stats Channel").
		SetCredentials(objects.ChannelCredentials{}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	ownerServiceKey, err := client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetUserID(ownerUser.ID).
		SetKey("owner-service-key").
		SetName("Owner Service Key").
		SetType(apikey.TypeServiceAccount).
		SetStatus(apikey.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	ownerPersonalKey, err := client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetUserID(ownerUser.ID).
		SetKey("owner-personal-key").
		SetName("Owner Personal Key").
		SetType(apikey.TypePersonal).
		SetStatus(apikey.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	otherPersonalKey, err := client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetUserID(otherUser.ID).
		SetKey("other-personal-key").
		SetName("Other Personal Key").
		SetType(apikey.TypePersonal).
		SetStatus(apikey.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	createDashboardUsageRecord(t, setupCtx, client, dashboardUsageRecord{
		ProjectID:       projectRow.ID,
		APIKeyID:        ownerServiceKey.ID,
		ChannelID:       channelRow.ID,
		ModelID:         "gpt-4",
		RequestSource:   request.SourceAPI,
		UsageSource:     usagelog.SourceAPI,
		RequestStatus:   request.StatusCompleted,
		ExecutionStatus: requestexecution.StatusCompleted,
		TotalTokens:     100,
		TotalCost:       1,
		CreatedAt:       now,
	})
	createDashboardUsageRecord(t, setupCtx, client, dashboardUsageRecord{
		ProjectID:       projectRow.ID,
		APIKeyID:        ownerPersonalKey.ID,
		ChannelID:       channelRow.ID,
		ModelID:         "gpt-4",
		RequestSource:   request.SourceAPI,
		UsageSource:     usagelog.SourceAPI,
		RequestStatus:   request.StatusCompleted,
		ExecutionStatus: requestexecution.StatusCompleted,
		TotalTokens:     200,
		TotalCost:       2,
		CreatedAt:       now,
	})
	createDashboardUsageRecord(t, setupCtx, client, dashboardUsageRecord{
		ProjectID:       projectRow.ID,
		APIKeyID:        otherPersonalKey.ID,
		ChannelID:       channelRow.ID,
		ModelID:         "gpt-4",
		RequestSource:   request.SourceAPI,
		UsageSource:     usagelog.SourceAPI,
		RequestStatus:   request.StatusCompleted,
		ExecutionStatus: requestexecution.StatusCompleted,
		TotalTokens:     300,
		TotalCost:       3,
		CreatedAt:       now,
	})

	resolver := &queryResolver{&Resolver{client: client}}
	queryCtx := authz.NewUserContext(ent.NewContext(t.Context(), client), ownerUser.ID)
	queryCtx = contexts.WithUser(queryCtx, ownerUser)
	queryCtx = contexts.WithProjectID(queryCtx, projectRow.ID)

	stats, err := resolver.UsageStatsByUser(queryCtx, nil)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, ownerUser.ID, stats[0].UserID.ID)
	require.Equal(t, 2, stats[0].RequestCount)
	require.Equal(t, 300, stats[0].TotalTokens)
	require.InDelta(t, 3.0, stats[0].TotalCost, 0.001)

	nonOwnerCtx := authz.NewUserContext(ent.NewContext(t.Context(), client), otherUser.ID)
	nonOwnerCtx = contexts.WithUser(nonOwnerCtx, otherUser)
	nonOwnerCtx = contexts.WithProjectID(nonOwnerCtx, projectRow.ID)

	_, err = resolver.UsageStatsByUser(nonOwnerCtx, nil)
	require.ErrorContains(t, err, "only project owners")
}

func TestQueryResolver_DashboardUsageBreakdowns_ExcludeTestSource(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	defer fixture.Client.Close()

	dailyStats, err := fixture.Resolver.DailyRequestStats(fixture.Ctx)
	require.NoError(t, err)
	require.NotEmpty(t, dailyStats)
	today := fixture.Now.Format("2006-01-02")
	var todayStats *DailyRequestStats
	for _, stat := range dailyStats {
		if stat.Date == today {
			todayStats = stat
			break
		}
	}
	require.NotNil(t, todayStats)
	require.Equal(t, 1, todayStats.Count)
	require.Equal(t, 30, todayStats.Tokens)
	require.Equal(t, 1.25, todayStats.Cost)

	topProjects, err := fixture.Resolver.TopRequestsProjects(fixture.Ctx)
	require.NoError(t, err)
	require.Len(t, topProjects, 1)
	require.Equal(t, fixture.Project.ID, topProjects[0].ProjectID.ID)
	require.Equal(t, 1, topProjects[0].RequestCount)

	apiKeyStats, err := fixture.Resolver.APIKeyTokenUsageStats(fixture.Ctx, &APIKeyTokenUsageStatsInput{
		APIKeyIds: []*objects.GUID{{Type: ent.TypeAPIKey, ID: fixture.APIKey.ID}},
	})
	require.NoError(t, err)
	require.Len(t, apiKeyStats, 1)
	require.Equal(t, fixture.APIKey.ID, apiKeyStats[0].APIKeyID.ID)
	require.Equal(t, 10, apiKeyStats[0].InputTokens)
	require.Equal(t, 20, apiKeyStats[0].OutputTokens)
	require.Equal(t, 3, apiKeyStats[0].CachedTokens)
	require.Equal(t, 4, apiKeyStats[0].ReasoningTokens)
	require.Len(t, apiKeyStats[0].TopModels, 1)
	require.Equal(t, "gpt-4", apiKeyStats[0].TopModels[0].ModelID)
	require.Equal(t, 10, apiKeyStats[0].TopModels[0].InputTokens)
	require.Equal(t, 20, apiKeyStats[0].TopModels[0].OutputTokens)
	require.Equal(t, 3, apiKeyStats[0].TopModels[0].CachedTokens)
	require.Equal(t, 4, apiKeyStats[0].TopModels[0].ReasoningTokens)
}

func TestQueryResolver_ChannelPerformanceStats_UsesAlignedProbeRowsFirst(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	defer fixture.Client.Close()

	alignedTimestamp := fixture.Now.Truncate(time.Minute).Unix()
	_, err := fixture.Client.ChannelProbe.Create().
		SetChannelID(fixture.Channel.ID).
		SetTotalRequestCount(99).
		SetSuccessRequestCount(99).
		SetAvgTokensPerSecond(999).
		SetAvgTimeToFirstTokenMs(99).
		SetTimestamp(alignedTimestamp).
		Save(fixture.Ctx)
	require.NoError(t, err)

	stats, err := fixture.Resolver.ChannelPerformanceStats(fixture.Ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, fixture.Now.Format("2006-01-02"), stats[0].Date)
	require.Equal(t, strconv.Itoa(fixture.Channel.ID), stats[0].ChannelID)
	require.Equal(t, "Dashboard Channel", stats[0].ChannelName)
	require.Equal(t, 99, stats[0].RequestCount)
	require.NotNil(t, stats[0].Throughput)
	require.InDelta(t, 999, *stats[0].Throughput, 0.01)
	require.NotNil(t, stats[0].TtftMs)
	require.InDelta(t, 99, *stats[0].TtftMs, 0.01)
}

func TestQueryResolver_ChannelPerformanceStats_IgnoresUnalignedProbeRowsAndTestSource(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	defer fixture.Client.Close()

	unalignedTimestamp := fixture.Now.Truncate(time.Minute).Add(17 * time.Second).Unix()
	_, err := fixture.Client.ChannelProbe.Create().
		SetChannelID(fixture.Channel.ID).
		SetTotalRequestCount(99).
		SetSuccessRequestCount(99).
		SetAvgTokensPerSecond(999).
		SetAvgTimeToFirstTokenMs(99).
		SetTimestamp(unalignedTimestamp).
		Save(fixture.Ctx)
	require.NoError(t, err)

	stats, err := fixture.Resolver.ChannelPerformanceStats(fixture.Ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, fixture.Now.Format("2006-01-02"), stats[0].Date)
	require.Equal(t, strconv.Itoa(fixture.Channel.ID), stats[0].ChannelID)
	require.Equal(t, "Dashboard Channel", stats[0].ChannelName)
	require.Equal(t, 1, stats[0].RequestCount)
	require.NotNil(t, stats[0].Throughput)
	require.InDelta(t, 26.666, *stats[0].Throughput, 0.01)
	require.NotNil(t, stats[0].TtftMs)
	require.InDelta(t, 100, *stats[0].TtftMs, 0.01)

	probeCount, err := fixture.Client.ChannelProbe.Query().
		Where(channelprobe.ChannelIDEQ(fixture.Channel.ID)).
		Count(fixture.Ctx)
	require.NoError(t, err)
	require.Equal(t, 1, probeCount)
}

func TestQueryResolver_AllChannelSummarys_ProjectProfileUsesIntersection(t *testing.T) {
	resolver, ctx, client := setupTestQueryResolver(t)
	defer client.Close()

	idOnlyChannel, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("ID Only").
		SetCredentials(objects.ChannelCredentials{APIKey: "key-1"}).
		SetSupportedModels([]string{"id-only-model"}).
		SetDefaultTestModel("id-only-model").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	matchingChannel, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Matching").
		SetCredentials(objects.ChannelCredentials{APIKey: "key-2"}).
		SetSupportedModels([]string{"matching-model"}).
		SetDefaultTestModel("matching-model").
		SetStatus(channel.StatusEnabled).
		SetTags([]string{"allowed"}).
		Save(ctx)
	require.NoError(t, err)

	projectEntity, err := client.Project.Create().
		SetName("Project A").
		SetDescription("test project").
		SetProfiles(&objects.ProjectProfiles{
			ActiveProfile: "production",
			Profiles: []objects.ProjectProfile{
				{
					Name:        "production",
					ChannelIDs:  []int{idOnlyChannel.ID, matchingChannel.ID},
					ChannelTags: []string{"allowed"},
				},
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	projectCtx := contexts.WithProjectID(ctx, projectEntity.ID)

	channels, err := resolver.AllChannelSummarys(projectCtx, nil)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, matchingChannel.ID, channels[0].ID)
}

func TestQueryResolver_AllChannelSummarys_RequiresChannelReadScopeWithoutProject(t *testing.T) {
	resolver, ctx, client := setupTestQueryResolver(t)
	defer client.Close()

	_, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Protected").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"protected-model"}).
		SetDefaultTestModel("protected-model").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	unauthorizedCtx := ent.NewContext(context.Background(), client)
	_, err = resolver.AllChannelSummarys(unauthorizedCtx, nil)
	require.Error(t, err)
}

func TestQueryResolver_AllChannelTags_ProjectProfileFiltersVisibleTags(t *testing.T) {
	resolver, ctx, client := setupTestQueryResolver(t)
	defer client.Close()

	_, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Visible Channel").
		SetCredentials(objects.ChannelCredentials{APIKey: "key-visible"}).
		SetSupportedModels([]string{"visible-model"}).
		SetDefaultTestModel("visible-model").
		SetStatus(channel.StatusEnabled).
		SetTags([]string{"shared", "visible"}).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Hidden Channel").
		SetCredentials(objects.ChannelCredentials{APIKey: "key-hidden"}).
		SetSupportedModels([]string{"hidden-model"}).
		SetDefaultTestModel("hidden-model").
		SetStatus(channel.StatusEnabled).
		SetTags([]string{"shared", "hidden"}).
		Save(ctx)
	require.NoError(t, err)

	projectEntity, err := client.Project.Create().
		SetName("Project B").
		SetDescription("test project").
		SetProfiles(&objects.ProjectProfiles{
			ActiveProfile: "production",
			Profiles: []objects.ProjectProfile{
				{
					Name:        "production",
					ChannelTags: []string{"visible"},
				},
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	projectCtx := contexts.WithProjectID(ctx, projectEntity.ID)

	tags, err := resolver.AllChannelTags(projectCtx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"shared", "visible"}, lo.Uniq(tags))
}
