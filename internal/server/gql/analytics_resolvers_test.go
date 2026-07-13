package gql

import (
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

func TestResolveAnalyticsDateRange(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)

	t.Run("defaults to thirty inclusive days", func(t *testing.T) {
		dateRange, err := resolveAnalyticsDateRange(nil, time.UTC, now)
		require.NoError(t, err)
		require.Equal(t, "2026-06-14", dateRange.startDay.Format(time.DateOnly))
		require.Equal(t, "2026-07-13", dateRange.endDay.Format(time.DateOnly))
	})

	tests := []struct {
		name   string
		filter *AnalyticsFilter
		match  string
	}{
		{
			name:   "invalid date",
			filter: &AnalyticsFilter{StartTime: lo.ToPtr("2026-02-30")},
			match:  "invalid analytics startTime",
		},
		{
			name: "reversed range",
			filter: &AnalyticsFilter{
				StartTime: lo.ToPtr("2026-07-14"),
				EndTime:   lo.ToPtr("2026-07-13"),
			},
			match: "must not be after",
		},
		{
			name: "oversized range",
			filter: &AnalyticsFilter{
				StartTime: lo.ToPtr("2010-01-01"),
				EndTime:   lo.ToPtr("2026-07-13"),
			},
			match: "must not exceed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveAnalyticsDateRange(tt.filter, time.UTC, now)
			require.ErrorContains(t, err, tt.match)
		})
	}
}

func TestResolveAnalyticsLimit(t *testing.T) {
	limit, err := resolveAnalyticsLimit(nil, defaultAnalyticsDimensionSize, maxAnalyticsDimensionSize)
	require.NoError(t, err)
	require.Equal(t, defaultAnalyticsDimensionSize, limit)

	limit, err = resolveAnalyticsLimit(lo.ToPtr(25), defaultAnalyticsDimensionSize, maxAnalyticsDimensionSize)
	require.NoError(t, err)
	require.Equal(t, 25, limit)

	_, err = resolveAnalyticsLimit(lo.ToPtr(maxAnalyticsDimensionSize+1), defaultAnalyticsDimensionSize, maxAnalyticsDimensionSize)
	require.ErrorContains(t, err, "must be between")
}

func TestAnalyticsTimezoneSegmentsTrackDSTTransitions(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	dateRange, err := resolveAnalyticsDateRange(&AnalyticsFilter{
		StartTime: lo.ToPtr("2026-03-07"),
		EndTime:   lo.ToPtr("2026-03-09"),
	}, loc, time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	segments := analyticsTimezoneSegments(dateRange, loc)
	require.Len(t, segments, 2)
	require.Equal(t, -5*60*60, segments[0].offsetSeconds)
	require.Equal(t, time.Date(2026, time.March, 8, 7, 0, 0, 0, time.UTC), segments[0].endUTC)
	require.Equal(t, -4*60*60, segments[1].offsetSeconds)

	expression := analyticsDateExpression(dialect.SQLite, "created_at", dateRange, loc)
	require.Contains(t, expression, "CASE")
	require.Contains(t, expression, "-18000 seconds")
	require.Contains(t, expression, "-14400 seconds")
}

func TestQueryResolver_AnalyticsUsesProductionUsageAndBoundedRange(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	createDashboardUsageRecord(t, fixture.Ctx, fixture.Client, dashboardUsageRecord{
		ProjectID:        fixture.Project.ID,
		APIKeyID:         fixture.APIKey.ID,
		ChannelID:        fixture.Channel.ID,
		ModelID:          "gpt-4-old",
		RequestSource:    request.SourceAPI,
		UsageSource:      usagelog.SourceAPI,
		RequestStatus:    request.StatusCompleted,
		ExecutionStatus:  requestexecution.StatusCompleted,
		PromptTokens:     1000,
		CompletionTokens: 2000,
		TotalTokens:      3000,
		TotalCost:        30,
		CreatedAt:        fixture.Now.AddDate(0, 0, -31),
	})

	defaultOverview, err := fixture.Resolver.AnalyticsOverview(fixture.Ctx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, defaultOverview.TotalRequests)
	require.Equal(t, 30, defaultOverview.TotalTokens)

	date := fixture.Now.Format(time.DateOnly)
	filter := &AnalyticsFilter{StartTime: &date, EndTime: &date}

	overview, err := fixture.Resolver.AnalyticsOverview(fixture.Ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 1, overview.TotalRequests)
	require.Equal(t, 30, overview.TotalTokens)
	require.Equal(t, 1.25, overview.TotalCost)

	daily, err := fixture.Resolver.AnalyticsDailyStats(fixture.Ctx, filter)
	require.NoError(t, err)
	require.Len(t, daily, 1)
	require.Equal(t, 1, daily[0].RequestCount)
	require.Equal(t, 30, daily[0].TotalTokens)

	modelStats, err := fixture.Resolver.AnalyticsDimensionStats(
		fixture.Ctx,
		filter,
		AnalyticsDimensionModel,
		lo.ToPtr(10),
	)
	require.NoError(t, err)
	require.Len(t, modelStats, 1)
	require.Equal(t, "gpt-4", modelStats[0].ID)
}

func TestQueryResolver_AnalyticsMetadataUsesDashboardScopeAndProductionUsage(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	_ = fixture.Resolver.systemService.TimeLocation(fixture.Ctx)

	createDashboardUsageRecord(t, fixture.Ctx, fixture.Client, dashboardUsageRecord{
		ProjectID:       fixture.Project.ID,
		APIKeyID:        fixture.APIKey.ID,
		ChannelID:       fixture.Channel.ID,
		ModelID:         "metadata-test-only",
		RequestSource:   request.SourceTest,
		UsageSource:     usagelog.SourceTest,
		RequestStatus:   request.StatusCompleted,
		ExecutionStatus: requestexecution.StatusCompleted,
		TotalTokens:     999,
		CreatedAt:       fixture.Now.AddDate(-1, 0, 0),
	})

	userRow := &ent.User{ID: 999, Scopes: []string{string(scopes.ScopeReadDashboard)}}
	ctx := contexts.WithUser(t.Context(), userRow)
	ctx = authz.NewUserContext(ctx, userRow.ID)

	metadata, err := fixture.Resolver.AnalyticsMetadata(ctx)
	require.NoError(t, err)
	require.NotNil(t, metadata.EarliestDate)
	require.Equal(t, fixture.Now.Format(time.DateOnly), *metadata.EarliestDate)
}

func TestQueryResolver_AnalyticsIdentityDimensionsRequireTheirScopes(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	_ = fixture.Resolver.systemService.TimeLocation(fixture.Ctx)

	userRow := &ent.User{ID: 999, Scopes: []string{string(scopes.ScopeReadDashboard)}}
	ctx := contexts.WithUser(t.Context(), userRow)
	ctx = authz.NewUserContext(ctx, userRow.ID)

	tests := []struct {
		name      string
		dimension AnalyticsDimension
		scope     scopes.ScopeSlug
	}{
		{name: "channel", dimension: AnalyticsDimensionChannel, scope: scopes.ScopeReadChannels},
		{name: "api key", dimension: AnalyticsDimensionAPIKey, scope: scopes.ScopeReadAPIKeys},
		{name: "user", dimension: AnalyticsDimensionUser, scope: scopes.ScopeReadUsers},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixture.Resolver.AnalyticsDimensionStats(ctx, nil, tt.dimension, nil)
			require.ErrorContains(t, err, string(tt.scope))
		})
	}

	_, err := fixture.Resolver.AnalyticsFilterOptions(ctx, AnalyticsFilterDimensionAPIKey, nil, nil, nil, nil)
	require.ErrorContains(t, err, string(scopes.ScopeReadAPIKeys))
}

func TestQueryResolver_AnalyticsRejectsProjectLevelIdentityScopes(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	_ = fixture.Resolver.systemService.TimeLocation(fixture.Ctx)

	userRow := &ent.User{
		ID:     999,
		Scopes: []string{string(scopes.ScopeReadDashboard)},
		Edges: ent.UserEdges{
			ProjectUsers: []*ent.UserProject{
				{
					ProjectID: fixture.Project.ID,
					Scopes: []string{
						string(scopes.ScopeReadAPIKeys),
						string(scopes.ScopeReadUsers),
					},
				},
			},
		},
	}
	ctx := contexts.WithUser(t.Context(), userRow)
	ctx = contexts.WithProjectID(ctx, fixture.Project.ID)
	ctx = authz.NewUserContext(ctx, userRow.ID)

	_, err := fixture.Resolver.AnalyticsDimensionStats(ctx, nil, AnalyticsDimensionAPIKey, nil)
	require.ErrorContains(t, err, "required system scope "+string(scopes.ScopeReadAPIKeys))

	_, err = fixture.Resolver.AnalyticsDimensionStats(ctx, nil, AnalyticsDimensionUser, nil)
	require.ErrorContains(t, err, "required system scope "+string(scopes.ScopeReadUsers))

	_, err = fixture.Resolver.AnalyticsFilterOptions(ctx, AnalyticsFilterDimensionAPIKey, nil, nil, nil, nil)
	require.ErrorContains(t, err, "required system scope "+string(scopes.ScopeReadAPIKeys))
}

func TestQueryResolver_AnalyticsModelOptionsUseSelectedDateRange(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	createDashboardUsageRecord(t, fixture.Ctx, fixture.Client, dashboardUsageRecord{
		ProjectID:        fixture.Project.ID,
		APIKeyID:         fixture.APIKey.ID,
		ChannelID:        fixture.Channel.ID,
		ModelID:          "gpt-4-historical",
		RequestSource:    request.SourceAPI,
		UsageSource:      usagelog.SourceAPI,
		RequestStatus:    request.StatusCompleted,
		ExecutionStatus:  requestexecution.StatusCompleted,
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
		CreatedAt:        fixture.Now.AddDate(0, 0, -45),
	})

	startTime := fixture.Now.AddDate(0, 0, -60).Format(time.DateOnly)
	endTime := fixture.Now.Format(time.DateOnly)
	search := "historical"
	options, err := fixture.Resolver.AnalyticsFilterOptions(
		fixture.Ctx,
		AnalyticsFilterDimensionModel,
		&search,
		nil,
		&startTime,
		&endTime,
	)
	require.NoError(t, err)
	require.Equal(t, []*AnalyticsFilterOption{{ID: "gpt-4-historical", Label: "gpt-4-historical"}}, options)
}

func TestQueryResolver_AnalyticsPreservesDeletedChannelAttribution(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	require.NoError(t, fixture.Client.Channel.DeleteOne(fixture.Channel).Exec(fixture.Ctx))

	date := fixture.Now.Format(time.DateOnly)
	stats, err := fixture.Resolver.AnalyticsDimensionStats(
		fixture.Ctx,
		&AnalyticsFilter{StartTime: &date, EndTime: &date},
		AnalyticsDimensionChannel,
		lo.ToPtr(10),
	)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, fixture.Channel.Name, stats[0].Name)
	require.Equal(t, 30, stats[0].TotalTokens)
}

func TestQueryResolver_AnalyticsUserDimensionSupportsProjectFilter(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)
	date := fixture.Now.Format(time.DateOnly)

	stats, err := fixture.Resolver.AnalyticsDimensionStats(
		fixture.Ctx,
		&AnalyticsFilter{
			StartTime: &date,
			EndTime:   &date,
			ProjectIDs: []*objects.GUID{{
				Type: "Project",
				ID:   fixture.Project.ID,
			}},
		},
		AnalyticsDimensionUser,
		lo.ToPtr(10),
	)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "unattributed", stats[0].ID)
}

func TestQueryResolver_AnalyticsUserFilterIncludesOtherUsersPersonalKeys(t *testing.T) {
	fixture := setupDashboardUsageFixture(t)

	adminUser, err := fixture.Client.User.Create().
		SetEmail("analytics-admin@example.com").
		SetPassword("password").
		SetStatus(user.StatusActivated).
		SetScopes([]string{
			string(scopes.ScopeReadDashboard),
			string(scopes.ScopeReadAPIKeys),
			string(scopes.ScopeReadUsers),
		}).
		Save(fixture.Ctx)
	require.NoError(t, err)

	targetUser, err := fixture.Client.User.Create().
		SetEmail("analytics-target@example.com").
		SetPassword("password").
		SetStatus(user.StatusActivated).
		Save(fixture.Ctx)
	require.NoError(t, err)

	personalKey, err := fixture.Client.APIKey.Create().
		SetProjectID(fixture.Project.ID).
		SetUserID(targetUser.ID).
		SetKey("analytics-target-personal-key").
		SetName("analytics-target-personal-key").
		SetType(apikey.TypePersonal).
		SetStatus(apikey.StatusEnabled).
		Save(fixture.Ctx)
	require.NoError(t, err)

	createDashboardUsageRecord(t, fixture.Ctx, fixture.Client, dashboardUsageRecord{
		ProjectID:        fixture.Project.ID,
		APIKeyID:         personalKey.ID,
		ChannelID:        fixture.Channel.ID,
		ModelID:          "gpt-4-personal",
		RequestSource:    request.SourceAPI,
		UsageSource:      usagelog.SourceAPI,
		RequestStatus:    request.StatusCompleted,
		ExecutionStatus:  requestexecution.StatusCompleted,
		PromptTokens:     20,
		CompletionTokens: 30,
		TotalTokens:      50,
		TotalCost:        2.5,
		CreatedAt:        fixture.Now,
	})

	ctx := contexts.WithUser(t.Context(), adminUser)
	ctx = authz.NewUserContext(ctx, adminUser.ID)
	date := fixture.Now.Format(time.DateOnly)
	overview, err := fixture.Resolver.AnalyticsOverview(ctx, &AnalyticsFilter{
		StartTime: &date,
		EndTime:   &date,
		UserIDs:   []*objects.GUID{{Type: "User", ID: targetUser.ID}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, overview.TotalRequests)
	require.Equal(t, 50, overview.TotalTokens)
	require.Equal(t, 2.5, overview.TotalCost)
}
