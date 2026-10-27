package gql

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"
	sqlschema "entgo.io/ent/dialect/sql/schema"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/migrate/schemahook"
	"github.com/looplj/axonhub/internal/ent/providerquotastatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestUsageLogResolverRequestExecutionID(t *testing.T) {
	resolver := &usageLogResolver{}

	guid, err := resolver.RequestExecutionID(t.Context(), &ent.UsageLog{})
	require.NoError(t, err)
	require.Nil(t, guid)

	guid, err = resolver.RequestExecutionID(t.Context(), &ent.UsageLog{RequestExecutionID: 42})
	require.NoError(t, err)
	require.Equal(t, ent.TypeRequestExecution, guid.Type)
	require.Equal(t, 42, guid.ID)
}

func TestPromptResolverProjectID(t *testing.T) {
	projectID, err := (&promptResolver{}).ProjectID(t.Context(), &ent.Prompt{ProjectID: 42})

	require.NoError(t, err)
	require.Equal(t, ent.TypeProject, projectID.Type)
	require.Equal(t, 42, projectID.ID)
}

func TestChannelResolver_ProviderQuotaStatus_HidesDisabledCollectionProvider(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	systemService := biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})
	channelEntity, err := client.Channel.Create().
		SetName("MiniMax").
		SetType(channel.TypeMinimax).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ProviderQuotaStatus.Create().
		SetChannelID(channelEntity.ID).
		SetProviderType(providerquotastatus.ProviderTypeMinimax).
		SetStatus(providerquotastatus.StatusUnknown).
		SetQuotaData(map[string]any{"error": "plan required"}).
		SetReady(false).
		SetNextCheckAt(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, systemService.UpdateProviderQuotaCollectionSettings(ctx, nil, []biz.ProviderQuotaCollectionProvider{
		{Provider: "minimax", Enabled: false},
	}))
	resolver := &channelResolver{&Resolver{systemService: systemService}}

	status, err := resolver.ProviderQuotaStatus(ctx, channelEntity)
	require.NoError(t, err)
	require.Nil(t, status)

	require.NoError(t, systemService.UpdateProviderQuotaCollectionSettings(ctx, nil, []biz.ProviderQuotaCollectionProvider{
		{Provider: "minimax", Enabled: true},
	}))
	status, err = resolver.ProviderQuotaStatus(ctx, channelEntity)
	require.NoError(t, err)
	require.NotNil(t, status)
	require.Equal(t, providerquotastatus.ProviderTypeMinimax, status.ProviderType)

	channelWithoutProviderType, err := client.Channel.Query().
		Where(channel.IDEQ(channelEntity.ID)).
		WithProviderQuotaStatus(func(query *ent.ProviderQuotaStatusQuery) {
			query.Select(providerquotastatus.FieldStatus)
		}).
		Only(ctx)
	require.NoError(t, err)
	status, err = resolver.ProviderQuotaStatus(ctx, channelWithoutProviderType)
	require.NoError(t, err)
	require.NotNil(t, status)
	require.Equal(t, providerquotastatus.ProviderTypeMinimax, status.ProviderType)
}

func TestChannelResolver_ProviderQuotaStatus_HidesDisabledProviderWithColdSettingsCache(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	systemService := biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})
	channelEntity, err := client.Channel.Create().
		SetName("MiniMax").
		SetType(channel.TypeMinimax).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ProviderQuotaStatus.Create().
		SetChannelID(channelEntity.ID).
		SetProviderType(providerquotastatus.ProviderTypeMinimax).
		SetStatus(providerquotastatus.StatusUnknown).
		SetQuotaData(map[string]any{"error": "plan required"}).
		SetReady(false).
		SetNextCheckAt(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, systemService.UpdateProviderQuotaCollectionSettings(ctx, nil, []biz.ProviderQuotaCollectionProvider{
		{Provider: "minimax", Enabled: false},
	}))
	systemService.InvalidateSystemValueCaches(ctx, biz.SystemKeyProviderQuotaCollectionSettings)

	apiKeyCtx := authz.NewAPIKeyContext(ent.NewContext(t.Context(), client), 1, 1)
	status, err := (&channelResolver{&Resolver{systemService: systemService}}).ProviderQuotaStatus(apiKeyCtx, channelEntity)

	require.NoError(t, err)
	require.Nil(t, status)
}

// newEntClientWithDB mirrors enttest.NewEntClient but also exposes the
// underlying *sql.DB so tests can insert rows the ent schema no longer
// accepts (e.g. a retired provider_type value left behind before the
// startup purge).
func newEntClientWithDB(t *testing.T, dsn string) (*ent.Client, *sql.DB) {
	t.Helper()

	sqlDB, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	client := enttest.NewClient(t,
		enttest.WithOptions(ent.Driver(entsql.OpenDB("sqlite3", sqlDB))),
		enttest.WithMigrateOptions(
			migrate.WithGlobalUniqueID(false),
			migrate.WithForeignKeys(false),
			migrate.WithDropIndex(true),
			migrate.WithDropColumn(true),
			sqlschema.WithHooks(schemahook.V0_3_0),
		),
	)
	t.Cleanup(func() { _ = client.Close() })
	return client, sqlDB
}

func TestChannelResolver_ProviderQuotaStatus_RetiredProviderRowReturnsNil(t *testing.T) {
	client, sqlDB := newEntClientWithDB(t, "file:ent_retired_quota?mode=memory&_fk=1")

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	systemService := biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})
	channelEntity, err := client.Channel.Create().
		SetName("retired-Wafer").
		SetType(channel.TypeOpenai).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		Save(ctx)
	require.NoError(t, err)

	// Insert a status row left over from before the 'wafer' provider type was
	// retired. The ent enum no longer accepts that value, so a raw insert
	// reproduces the surviving-row state (purge disabled / pre-upgrade).
	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO provider_quota_status
			(id, created_at, updated_at, deleted_at, provider_type, status, quota_data,
			 next_reset_at, ready, next_check_at, account_key, channel_id)
		 VALUES (1, '2026-01-01 00:00:00', '2026-01-01 00:00:00', 0, 'wafer', 'unknown',
			'{}', NULL, 0, '2026-01-02 00:00:00', '', ?)`,
		channelEntity.ID,
	)
	require.NoError(t, err)

	resolver := &channelResolver{&Resolver{systemService: systemService}}

	// A surviving retired-provider row must surface as "no status" without an
	// error so it cannot break the quota view for every channel.
	status, err := resolver.ProviderQuotaStatus(ctx, channelEntity)
	require.NoError(t, err)
	require.Nil(t, status)

	// A supported provider type keeps working normally.
	supportedChannelEntity, err := client.Channel.Create().
		SetName("MiniMax").
		SetType(channel.TypeMinimax).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ProviderQuotaStatus.Create().
		SetChannelID(supportedChannelEntity.ID).
		SetProviderType(providerquotastatus.ProviderTypeMinimax).
		SetStatus(providerquotastatus.StatusUnknown).
		SetQuotaData(map[string]any{"remaining": 100}).
		SetReady(false).
		SetNextCheckAt(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	status, err = resolver.ProviderQuotaStatus(ctx, supportedChannelEntity)
	require.NoError(t, err)
	require.NotNil(t, status)
	require.Equal(t, providerquotastatus.ProviderTypeMinimax, status.ProviderType)
}

func TestChannelResolver_ProviderQuotaStatus_ReturnsNilWhenStatusDoesNotExist(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	systemService := biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})
	channelEntity, err := client.Channel.Create().
		SetName("Without quota status").
		SetType(channel.TypeOpenai).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		Save(ctx)
	require.NoError(t, err)

	resolver := &channelResolver{&Resolver{systemService: systemService}}
	status, err := resolver.ProviderQuotaStatus(ctx, channelEntity)

	require.NoError(t, err)
	require.Nil(t, status)
}

func TestRequestExecutionResolver_ChannelAPIKeySuffix(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent_req_exec?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	channelEntity, err := client.Channel.Create().
		SetName("OpenAI Channel").
		SetType(channel.TypeOpenai).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		Save(ctx)
	require.NoError(t, err)

	suffix := "ZxWL"
	exec := &ent.RequestExecution{
		ChannelID:           channelEntity.ID,
		ChannelAPIKeySuffix: suffix,
	}

	resolver := &requestExecutionResolver{&Resolver{client: client}}

	// 1. Channel administrators can read suffix.
	writeChannelsCtx := contexts.WithUser(ctx, &ent.User{Scopes: []string{string(scopes.ScopeWriteChannels)}})
	got, err := resolver.ChannelAPIKeySuffix(writeChannelsCtx, exec)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "ZxWL", *got)

	// 2. An empty persisted suffix returns nil.
	execNoSuffix := &ent.RequestExecution{
		ChannelID: channelEntity.ID,
	}
	got, err = resolver.ChannelAPIKeySuffix(writeChannelsCtx, execNoSuffix)
	require.NoError(t, err)
	require.Nil(t, got)

	// 3. Channel read permission alone must not expose key identities.
	unauthorizedCtx := contexts.WithUser(ent.NewContext(t.Context(), client), &ent.User{Scopes: []string{string(scopes.ScopeReadChannels)}})
	got, err = resolver.ChannelAPIKeySuffix(unauthorizedCtx, exec)
	require.NoError(t, err)
	require.Nil(t, got)
}
