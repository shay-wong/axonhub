package gql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/scopes"
)

const (
	defaultAnalyticsRangeDays     = 30
	maxAnalyticsRangeDays         = 366
	defaultAnalyticsDimensionSize = 100
	maxAnalyticsDimensionSize     = 500
	defaultAnalyticsOptionSize    = 50
	maxAnalyticsOptionSize        = 100
)

type analyticsDateRange struct {
	startDay        time.Time
	endDay          time.Time
	startUTC        time.Time
	endExclusiveUTC time.Time
}

type analyticsTimezoneSegment struct {
	endUTC        time.Time
	offsetSeconds int
}

func parseAnalyticsDate(value string, loc *time.Location) (time.Time, error) {
	parsed, err := time.ParseInLocation(time.DateOnly, value, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("must use YYYY-MM-DD: %w", err)
	}

	return parsed, nil
}

func resolveAnalyticsDateRange(filter *AnalyticsFilter, loc *time.Location, nowUTC time.Time) (analyticsDateRange, error) {
	today := nowUTC.In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)

	endDay := today
	if filter != nil && filter.EndTime != nil {
		parsed, err := parseAnalyticsDate(*filter.EndTime, loc)
		if err != nil {
			return analyticsDateRange{}, fmt.Errorf("invalid analytics endTime: %w", err)
		}
		endDay = parsed
	}

	startDay := endDay.AddDate(0, 0, -defaultAnalyticsRangeDays+1)
	if filter != nil && filter.StartTime != nil {
		parsed, err := parseAnalyticsDate(*filter.StartTime, loc)
		if err != nil {
			return analyticsDateRange{}, fmt.Errorf("invalid analytics startTime: %w", err)
		}
		startDay = parsed
	}

	if startDay.After(endDay) {
		return analyticsDateRange{}, fmt.Errorf("analytics startTime must not be after endTime")
	}

	days := 1
	for day := startDay; day.Before(endDay); day = day.AddDate(0, 0, 1) {
		days++
		if days > maxAnalyticsRangeDays {
			return analyticsDateRange{}, fmt.Errorf("analytics date range must not exceed %d days", maxAnalyticsRangeDays)
		}
	}

	return analyticsDateRange{
		startDay:        startDay,
		endDay:          endDay,
		startUTC:        startDay.UTC(),
		endExclusiveUTC: endDay.AddDate(0, 0, 1).UTC(),
	}, nil
}

func analyticsTimezoneSegments(dateRange analyticsDateRange, loc *time.Location) []analyticsTimezoneSegment {
	const probeInterval = 6 * time.Hour

	current := dateRange.startUTC
	_, currentOffset := current.In(loc).Zone()
	segments := make([]analyticsTimezoneSegment, 0, 3)

	for current.Before(dateRange.endExclusiveUTC) {
		probeEnd := current.Add(probeInterval)
		if probeEnd.After(dateRange.endExclusiveUTC) {
			probeEnd = dateRange.endExclusiveUTC
		}

		probe := probeEnd
		if !probe.Before(dateRange.endExclusiveUTC) {
			probe = dateRange.endExclusiveUTC.Add(-time.Nanosecond)
		}
		_, probeOffset := probe.In(loc).Zone()
		if probeOffset == currentOffset {
			current = probeEnd
			continue
		}

		transition := findAnalyticsTimezoneTransition(current, probe, loc, currentOffset)
		segments = append(segments, analyticsTimezoneSegment{
			endUTC:        transition,
			offsetSeconds: currentOffset,
		})
		current = transition
		_, currentOffset = current.In(loc).Zone()
	}

	segments = append(segments, analyticsTimezoneSegment{
		endUTC:        dateRange.endExclusiveUTC,
		offsetSeconds: currentOffset,
	})
	return segments
}

func findAnalyticsTimezoneTransition(low, high time.Time, loc *time.Location, oldOffset int) time.Time {
	low = low.UTC().Truncate(time.Second)
	high = high.UTC().Truncate(time.Second)
	for high.Sub(low) > time.Second {
		mid := low.Add(high.Sub(low) / 2).Truncate(time.Second)
		if !mid.After(low) {
			mid = low.Add(time.Second)
		}
		_, offset := mid.In(loc).Zone()
		if offset == oldOffset {
			low = mid
		} else {
			high = mid
		}
	}
	return high
}

func analyticsDateExpression(dialectName, createdAtCol string, dateRange analyticsDateRange, loc *time.Location) string {
	switch dialectName {
	case dialect.SQLite, dialect.MySQL:
		segments := analyticsTimezoneSegments(dateRange, loc)
		if len(segments) == 1 {
			return analyticsFixedOffsetDateExpression(dialectName, createdAtCol, segments[0].offsetSeconds)
		}

		var expression strings.Builder
		expression.WriteString("CASE")
		for _, segment := range segments[:len(segments)-1] {
			boundary := segment.endUTC.Format("2006-01-02 15:04:05")
			condition := fmt.Sprintf("%s < '%s'", createdAtCol, boundary)
			if dialectName == dialect.SQLite {
				condition = fmt.Sprintf("datetime(substr(%s, 1, 19)) < datetime('%s')", createdAtCol, boundary)
			}
			fmt.Fprintf(
				&expression,
				" WHEN %s THEN %s",
				condition,
				analyticsFixedOffsetDateExpression(dialectName, createdAtCol, segment.offsetSeconds),
			)
		}
		lastSegment := segments[len(segments)-1]
		fmt.Fprintf(
			&expression,
			" ELSE %s END",
			analyticsFixedOffsetDateExpression(dialectName, createdAtCol, lastSegment.offsetSeconds),
		)
		return expression.String()
	case dialect.Postgres:
		timezone := strings.ReplaceAll(loc.String(), "'", "''")
		return fmt.Sprintf("to_char(%s AT TIME ZONE '%s', 'YYYY-MM-DD')", createdAtCol, timezone)
	default:
		return fmt.Sprintf("DATE(%s)", createdAtCol)
	}
}

func analyticsFixedOffsetDateExpression(dialectName, createdAtCol string, offsetSeconds int) string {
	switch dialectName {
	case dialect.SQLite:
		return fmt.Sprintf("strftime('%%Y-%%m-%%d', datetime(substr(%s, 1, 19), '%+d seconds'))", createdAtCol, offsetSeconds)
	case dialect.MySQL:
		return fmt.Sprintf(
			"DATE_FORMAT(CONVERT_TZ(%s, '+00:00', '%s'), '%%Y-%%m-%%d')",
			createdAtCol,
			xtime.FormatUTCOffset(offsetSeconds),
		)
	default:
		return fmt.Sprintf("DATE(%s)", createdAtCol)
	}
}

func (r *queryResolver) buildAnalyticsWhere(s *sql.Selector, filter *AnalyticsFilter, dateRange analyticsDateRange) {
	s.Where(sql.And(
		sql.GTE(s.C(usagelog.FieldCreatedAt), dateRange.startUTC),
		sql.LT(s.C(usagelog.FieldCreatedAt), dateRange.endExclusiveUTC),
	))

	if filter == nil {
		return
	}

	if len(filter.ProjectIDs) > 0 {
		ids := lo.Map(filter.ProjectIDs, func(g *objects.GUID, _ int) int { return g.ID })
		s.Where(sql.InInts(s.C(usagelog.FieldProjectID), ids...))
	}

	if len(filter.ChannelIDs) > 0 {
		ids := lo.Map(filter.ChannelIDs, func(g *objects.GUID, _ int) int { return g.ID })
		s.Where(sql.InInts(s.C(usagelog.FieldChannelID), ids...))
	}

	if len(filter.ModelIDs) > 0 {
		vals := make([]any, len(filter.ModelIDs))
		for i, v := range filter.ModelIDs {
			vals[i] = v
		}
		s.Where(sql.In(s.C(usagelog.FieldModelID), vals...))
	}

	if len(filter.APIKeyIDs) > 0 {
		apiKeyIDs := lo.Map(filter.APIKeyIDs, func(g *objects.GUID, _ int) int { return g.ID })
		s.Where(sql.InInts(s.C(usagelog.FieldAPIKeyID), apiKeyIDs...))
	}

	if len(filter.UserIDs) > 0 {
		userIDs := lo.Map(filter.UserIDs, func(g *objects.GUID, _ int) int { return g.ID })
		apiKeyTable := sql.Table(apikey.Table)
		apiKeyIDsByUser := sql.Select(apiKeyTable.C(apikey.FieldID)).
			From(apiKeyTable).
			Where(sql.InInts(apiKeyTable.C(apikey.FieldUserID), userIDs...))
		s.Where(sql.In(
			s.C(usagelog.FieldAPIKeyID),
			apiKeyIDsByUser,
		))
	}
}

func resolveAnalyticsLimit(requested *int, defaultValue, maxValue int) (int, error) {
	if requested == nil {
		return defaultValue, nil
	}
	if *requested < 1 || *requested > maxValue {
		return 0, fmt.Errorf("analytics first must be between 1 and %d", maxValue)
	}

	return *requested, nil
}

func validateAnalyticsFilterScopes(ctx context.Context, filter *AnalyticsFilter) error {
	if filter == nil {
		return nil
	}

	required := []struct {
		active bool
		scope  scopes.ScopeSlug
	}{
		{active: len(filter.ProjectIDs) > 0, scope: scopes.ScopeReadProjects},
		{active: len(filter.ChannelIDs) > 0, scope: scopes.ScopeReadChannels},
		{active: len(filter.APIKeyIDs) > 0, scope: scopes.ScopeReadAPIKeys},
		{active: len(filter.UserIDs) > 0, scope: scopes.ScopeReadUsers},
		{active: len(filter.UserIDs) > 0, scope: scopes.ScopeReadAPIKeys},
	}

	for _, item := range required {
		if item.active {
			if err := authz.RequireSystemScope(ctx, item.scope); err != nil {
				return err
			}
		}
	}

	return nil
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func analyticsFilterOptionID(entityType string, id int) string {
	return fmt.Sprintf("gid://axonhub/%s/%d", entityType, id)
}

func (r *queryResolver) queryAnalyticsFilterOptions(ctx context.Context, dateFilter *AnalyticsFilter, dimension AnalyticsFilterDimension, search *string, limit int) ([]*AnalyticsFilterOption, error) {
	searchTerm := ""
	if search != nil {
		searchTerm = strings.TrimSpace(*search)
	}

	switch dimension {
	case AnalyticsFilterDimensionProject:
		if err := authz.RequireSystemScope(ctx, scopes.ScopeReadProjects); err != nil {
			return nil, err
		}
		query := r.client.Project.Query().Limit(limit).Order(project.ByName())
		if searchTerm != "" {
			query.Where(project.NameContainsFold(searchTerm))
		}
		rows, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to query analytics project options: %w", err)
		}
		return lo.Map(rows, func(row *ent.Project, _ int) *AnalyticsFilterOption {
			return &AnalyticsFilterOption{ID: analyticsFilterOptionID("Project", row.ID), Label: row.Name}
		}), nil

	case AnalyticsFilterDimensionChannel:
		if err := authz.RequireSystemScope(ctx, scopes.ScopeReadChannels); err != nil {
			return nil, err
		}
		query := r.client.Channel.Query().Limit(limit).Order(channel.ByName())
		if searchTerm != "" {
			query.Where(channel.NameContainsFold(searchTerm))
		}
		rows, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to query analytics channel options: %w", err)
		}
		return lo.Map(rows, func(row *ent.Channel, _ int) *AnalyticsFilterOption {
			return &AnalyticsFilterOption{ID: analyticsFilterOptionID("Channel", row.ID), Label: row.Name}
		}), nil

	case AnalyticsFilterDimensionAPIKey:
		if err := authz.RequireSystemScope(ctx, scopes.ScopeReadAPIKeys); err != nil {
			return nil, err
		}
		query := r.client.APIKey.Query().
			Where(apikey.TypeNEQ(apikey.TypeNoauth)).
			Limit(limit).
			Order(apikey.ByName())
		if searchTerm != "" {
			query.Where(apikey.NameContainsFold(searchTerm))
		}
		rows, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to query analytics API key options: %w", err)
		}
		return lo.Map(rows, func(row *ent.APIKey, _ int) *AnalyticsFilterOption {
			return &AnalyticsFilterOption{ID: analyticsFilterOptionID("APIKey", row.ID), Label: row.Name}
		}), nil

	case AnalyticsFilterDimensionUser:
		if err := authz.RequireSystemScope(ctx, scopes.ScopeReadUsers); err != nil {
			return nil, err
		}
		query := r.client.User.Query().Limit(limit).Order(user.ByEmail())
		if searchTerm != "" {
			query.Where(user.Or(
				user.EmailContainsFold(searchTerm),
				user.FirstNameContainsFold(searchTerm),
				user.LastNameContainsFold(searchTerm),
			))
		}
		rows, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to query analytics user options: %w", err)
		}
		return lo.Map(rows, func(row *ent.User, _ int) *AnalyticsFilterOption {
			label := trimSpace(fmt.Sprintf("%s %s", row.FirstName, row.LastName))
			if label == "" {
				label = row.Email
			}
			return &AnalyticsFilterOption{ID: analyticsFilterOptionID("User", row.ID), Label: label}
		}), nil

	case AnalyticsFilterDimensionModel:
		dateRange, err := resolveAnalyticsDateRange(dateFilter, r.systemService.TimeLocation(ctx), xtime.UTCNow())
		if err != nil {
			return nil, err
		}
		type modelOption struct {
			ID string `json:"id"`
		}
		var rows []modelOption
		scopeCtx := authz.WithSystemScopeDecision(ctx, scopes.ScopeReadDashboard)
		query := r.productionUsageLogQuery().Limit(limit)
		if searchTerm != "" {
			query.Where(usagelog.ModelIDContainsFold(searchTerm))
		}
		err = query.Modify(func(s *sql.Selector) {
			s.Where(sql.And(
				sql.GTE(s.C(usagelog.FieldCreatedAt), dateRange.startUTC),
				sql.LT(s.C(usagelog.FieldCreatedAt), dateRange.endExclusiveUTC),
			))
			s.Select(sql.As(s.C(usagelog.FieldModelID), "id")).
				GroupBy(s.C(usagelog.FieldModelID)).
				OrderBy(s.C(usagelog.FieldModelID))
		}).Scan(scopeCtx, &rows)
		if err != nil {
			return nil, fmt.Errorf("failed to query analytics model options: %w", err)
		}
		return lo.Map(rows, func(row modelOption, _ int) *AnalyticsFilterOption {
			return &AnalyticsFilterOption{ID: row.ID, Label: row.ID}
		}), nil
	default:
		return nil, fmt.Errorf("unsupported analytics filter dimension: %s", dimension)
	}
}

// dimStats holds aggregated dimension statistics from raw SQL queries.
type dimStats struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	RequestCount int     `json:"request_count"`
	InputTokens  int64   `json:"input_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

func (r *queryResolver) queryChannelStats(ctx context.Context, filter *AnalyticsFilter, dateRange analyticsDateRange, limit int) ([]dimStats, error) {
	type channelStatsRaw struct {
		ChannelID    *int    `json:"channel_id"`
		RequestCount int     `json:"request_count"`
		InputTokens  int64   `json:"input_tokens"`
		CachedTokens int64   `json:"cached_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalTokens  int64   `json:"total_tokens"`
		Cost         float64 `json:"cost"`
	}

	var rawResults []channelStatsRaw
	scopeCtx := authz.WithSystemScopeDecision(ctx, scopes.ScopeReadDashboard)
	err := r.productionUsageLogQuery().
		Limit(limit).
		Modify(func(s *sql.Selector) {
			r.buildAnalyticsWhere(s, filter, dateRange)
			s.Select(
				s.C(usagelog.FieldChannelID),
				sql.As(sql.Count(s.C(usagelog.FieldID)), "request_count"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptTokens)), "input_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptCachedTokens)), "cached_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldCompletionTokens)), "output_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalTokens)), "total_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalCost)), "cost"),
			).
				GroupBy(s.C(usagelog.FieldChannelID)).
				OrderBy(sql.Desc("total_tokens"))
		}).
		Scan(scopeCtx, &rawResults)
	if err != nil {
		return nil, fmt.Errorf("failed to get analytics stats by channel: %w", err)
	}

	channelIDs := make([]int, 0, len(rawResults))
	for _, raw := range rawResults {
		if raw.ChannelID != nil {
			channelIDs = append(channelIDs, *raw.ChannelID)
		}
	}

	channelNames := make(map[int]string, len(channelIDs))
	if len(channelIDs) > 0 {
		channels, queryErr := r.client.Channel.Query().
			Where(channel.IDIn(channelIDs...)).
			All(schematype.SkipSoftDelete(ctx))
		if queryErr != nil {
			return nil, fmt.Errorf("failed to get analytics channel details: %w", queryErr)
		}
		for _, channelRow := range channels {
			channelNames[channelRow.ID] = channelRow.Name
		}
	}

	results := make([]dimStats, 0, len(rawResults))
	for _, raw := range rawResults {
		id := "unattributed"
		name := "Unattributed"
		if raw.ChannelID != nil {
			id = fmt.Sprintf("%d", *raw.ChannelID)
			name = channelNames[*raw.ChannelID]
			if name == "" {
				name = fmt.Sprintf("Channel #%d", *raw.ChannelID)
			}
		}
		results = append(results, dimStats{
			ID:           id,
			Name:         name,
			RequestCount: raw.RequestCount,
			InputTokens:  raw.InputTokens,
			CachedTokens: raw.CachedTokens,
			OutputTokens: raw.OutputTokens,
			TotalTokens:  raw.TotalTokens,
			Cost:         raw.Cost,
		})
	}

	return results, nil
}

func (r *queryResolver) queryModelStats(ctx context.Context, filter *AnalyticsFilter, dateRange analyticsDateRange, limit int) ([]dimStats, error) {
	var results []dimStats

	scopeCtx := authz.WithSystemScopeDecision(ctx, scopes.ScopeReadDashboard)
	err := r.productionUsageLogQuery().
		Limit(limit).
		Modify(func(s *sql.Selector) {
			r.buildAnalyticsWhere(s, filter, dateRange)

			s.Select(
				sql.As(s.C(usagelog.FieldModelID), "id"),
				sql.As(s.C(usagelog.FieldModelID), "name"),
				sql.As(sql.Count(s.C(usagelog.FieldID)), "request_count"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptTokens)), "input_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptCachedTokens)), "cached_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldCompletionTokens)), "output_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalTokens)), "total_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalCost)), "cost"),
			).
				GroupBy(s.C(usagelog.FieldModelID)).
				OrderBy(sql.Desc("total_tokens"))
		}).
		Scan(scopeCtx, &results)
	if err != nil {
		return nil, fmt.Errorf("failed to get analytics stats by model: %w", err)
	}

	return results, nil
}

func (r *queryResolver) queryAPIKeyStats(ctx context.Context, filter *AnalyticsFilter, dateRange analyticsDateRange, limit int) ([]dimStats, error) {
	type apiKeyStatsRaw struct {
		APIKeyID     *int    `json:"api_key_id"`
		RequestCount int     `json:"request_count"`
		InputTokens  int64   `json:"input_tokens"`
		CachedTokens int64   `json:"cached_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalTokens  int64   `json:"total_tokens"`
		Cost         float64 `json:"cost"`
	}

	var rawResults []apiKeyStatsRaw

	scopeCtx := authz.WithSystemScopeDecision(ctx, scopes.ScopeReadDashboard)
	err := r.productionUsageLogQuery().
		Limit(limit).
		Modify(func(s *sql.Selector) {
			r.buildAnalyticsWhere(s, filter, dateRange)

			s.Select(
				sql.As(s.C(usagelog.FieldAPIKeyID), "api_key_id"),
				sql.As(sql.Count(s.C(usagelog.FieldID)), "request_count"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptTokens)), "input_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptCachedTokens)), "cached_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldCompletionTokens)), "output_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalTokens)), "total_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalCost)), "cost"),
			).
				GroupBy(s.C(usagelog.FieldAPIKeyID)).
				OrderBy(sql.Desc("total_tokens"))
		}).
		Scan(scopeCtx, &rawResults)
	if err != nil {
		return nil, fmt.Errorf("failed to get analytics stats by apiKey: %w", err)
	}

	apiKeyIDsForNames := make([]int, 0, len(rawResults))
	for _, raw := range rawResults {
		if raw.APIKeyID != nil {
			apiKeyIDsForNames = append(apiKeyIDsForNames, *raw.APIKeyID)
		}
	}

	apiKeyNames := make(map[int]string, len(apiKeyIDsForNames))
	if len(apiKeyIDsForNames) > 0 {
		apiKeys, qErr := r.client.APIKey.Query().
			Where(apikey.IDIn(apiKeyIDsForNames...)).
			All(schematype.SkipSoftDelete(ctx))
		if qErr != nil {
			return nil, fmt.Errorf("failed to get API key details: %w", qErr)
		}
		for _, apiKeyRow := range apiKeys {
			apiKeyNames[apiKeyRow.ID] = apiKeyRow.Name
		}
	}

	results := make([]dimStats, 0, len(rawResults))
	for _, raw := range rawResults {
		id := "unattributed"
		name := "Unattributed"
		if raw.APIKeyID != nil {
			id = fmt.Sprintf("%d", *raw.APIKeyID)
			name = apiKeyNames[*raw.APIKeyID]
			if name == "" {
				name = fmt.Sprintf("API Key #%d", *raw.APIKeyID)
			}
		}
		results = append(results, dimStats{
			ID:           id,
			Name:         name,
			RequestCount: raw.RequestCount,
			InputTokens:  raw.InputTokens,
			CachedTokens: raw.CachedTokens,
			OutputTokens: raw.OutputTokens,
			TotalTokens:  raw.TotalTokens,
			Cost:         raw.Cost,
		})
	}

	return results, nil
}

func (r *queryResolver) queryUserStats(ctx context.Context, filter *AnalyticsFilter, dateRange analyticsDateRange, limit int) ([]dimStats, error) {
	type userStatsRaw struct {
		UserID       *int    `json:"user_id"`
		RequestCount int     `json:"request_count"`
		InputTokens  int64   `json:"input_tokens"`
		CachedTokens int64   `json:"cached_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalTokens  int64   `json:"total_tokens"`
		Cost         float64 `json:"cost"`
	}

	var rawResults []userStatsRaw

	scopeCtx := authz.WithSystemScopeDecision(ctx, scopes.ScopeReadDashboard)
	err := r.productionUsageLogQuery().
		Limit(limit).
		Modify(func(s *sql.Selector) {
			apiKeyTable := sql.Table(apikey.Table)
			s.LeftJoin(apiKeyTable).On(
				s.C(usagelog.FieldAPIKeyID),
				apiKeyTable.C(apikey.FieldID),
			)
			r.buildAnalyticsWhere(s, filter, dateRange)

			s.Select(
				sql.As(apiKeyTable.C(apikey.FieldUserID), "user_id"),
				sql.As(sql.Count(s.C(usagelog.FieldID)), "request_count"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptTokens)), "input_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldPromptCachedTokens)), "cached_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldCompletionTokens)), "output_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalTokens)), "total_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(usagelog.FieldTotalCost)), "cost"),
			).
				GroupBy(apiKeyTable.C(apikey.FieldUserID)).
				OrderBy(sql.Desc("total_tokens"))
		}).
		Scan(scopeCtx, &rawResults)
	if err != nil {
		return nil, fmt.Errorf("failed to get analytics stats by user: %w", err)
	}

	userIDs := make([]int, 0, len(rawResults))
	for _, raw := range rawResults {
		if raw.UserID != nil {
			userIDs = append(userIDs, *raw.UserID)
		}
	}

	userNames := make(map[int]string, len(userIDs))
	if len(userIDs) > 0 {
		users, queryErr := r.client.User.Query().
			Where(user.IDIn(userIDs...)).
			All(schematype.SkipSoftDelete(ctx))
		if queryErr != nil {
			return nil, fmt.Errorf("failed to get analytics user details: %w", queryErr)
		}
		for _, userRow := range users {
			name := trimSpace(fmt.Sprintf("%s %s", userRow.FirstName, userRow.LastName))
			if name == "" {
				name = userRow.Email
			}
			userNames[userRow.ID] = name
		}
	}

	results := make([]dimStats, 0, len(rawResults))
	for _, raw := range rawResults {
		id := "unattributed"
		name := "Unattributed"
		if raw.UserID != nil {
			id = fmt.Sprintf("%d", *raw.UserID)
			name = userNames[*raw.UserID]
			if name == "" {
				name = fmt.Sprintf("User #%d", *raw.UserID)
			}
		}
		results = append(results, dimStats{
			ID:           id,
			Name:         name,
			RequestCount: raw.RequestCount,
			InputTokens:  raw.InputTokens,
			CachedTokens: raw.CachedTokens,
			OutputTokens: raw.OutputTokens,
			TotalTokens:  raw.TotalTokens,
			Cost:         raw.Cost,
		})
	}

	return results, nil
}

func dimStatsToDimensionStats(items []dimStats) []*AnalyticsDimensionStat {
	return lo.Map(items, func(item dimStats, _ int) *AnalyticsDimensionStat {
		return &AnalyticsDimensionStat{
			ID:                item.ID,
			Name:              item.Name,
			RequestCount:      item.RequestCount,
			InputTokens:       safeIntFromInt64(item.InputTokens),
			CachedInputTokens: safeIntFromInt64(item.CachedTokens),
			OutputTokens:      safeIntFromInt64(item.OutputTokens),
			TotalTokens:       safeIntFromInt64(item.TotalTokens),
			Cost:              item.Cost,
		}
	})
}
