package biz

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/pkg/xcache/live"
	"github.com/looplj/axonhub/llm/httpclient"
)

func newTestChannelService(client *ent.Client) *ChannelService {
	mockSysSvc := &SystemService{
		AbstractService: &AbstractService{
			db: client,
		},
		Cache: xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
	}

	svc := &ChannelService{
		AbstractService: &AbstractService{
			db: client,
		},
		SystemService:      mockSysSvc,
		WebhookNotifier:    NewWebhookNotifier(mockSysSvc, httpclient.NewHttpClient()),
		channelPerfMetrics: make(map[int]*channelMetrics),
		channelErrorCounts: make(map[int]map[int]int),
		apiKeyErrorCounts:  make(map[int]map[string]map[int]int),
		perfWindowSeconds:  600,
	}

	svc.enabledChannelsCache = live.NewCache(live.Options[[]*Channel]{
		Name:            "test_enabled_channels",
		InitialValue:    []*Channel{},
		RefreshInterval: time.Hour,
		RefreshFunc:     svc.reloadEnabledChannels,
		OnSwap:          svc.onEnabledChannelsSwap,
	})

	return svc
}

func createTestChannelWithAPIKeys(t *testing.T, client *ent.Client, ctx context.Context, name string, apiKeys []string) *ent.Channel {
	t.Helper()

	creds := objects.ChannelCredentials{
		APIKeys: apiKeys,
	}

	ch, err := client.Channel.Create().
		SetName(name).
		SetType(channel.TypeOpenai).
		SetBaseURL("https://api.openai.com").
		SetCredentials(creds).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	return ch
}

func TestChannelService_checkAndHandleAPIKeyError(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	// Create a channel with multiple API keys
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2", "key3"})

	tests := []struct {
		name             string
		policy           *RetryPolicy
		perf             *PerformanceRecord
		expectedDisabled bool
		setupFunc        func()
	}{
		{
			name: "first error - should not disable",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = make(map[int]map[string]map[int]int)
			},
		},
		{
			name: "second error - should not disable",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 1}},
				}
			},
		},
		{
			name: "third error - should disable API key",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: true,
			setupFunc: func() {
				// Reset channel state first
				_, err := client.Channel.UpdateOneID(ch.ID).
					SetDisabledAPIKeys([]objects.DisabledAPIKey{}).
					SetStatus(channel.StatusEnabled).
					ClearErrorMessage().
					Save(ctx)
				require.NoError(t, err)

				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 2}},
				}
			},
		},
		{
			name: "different status code - should not disable",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 500,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 2}},
				}
			},
		},
		{
			name: "different API key - should not disable",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key2",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 2}},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc()
			}

			result := svc.checkAndHandleAPIKeyError(ctx, tt.perf, tt.policy)
			require.Equal(t, tt.expectedDisabled, result)

			if tt.expectedDisabled {
				// Verify API key is disabled
				updatedCh, err := client.Channel.Get(ctx, ch.ID)
				require.NoError(t, err)
				require.Len(t, updatedCh.DisabledAPIKeys, 1)
				require.Equal(t, tt.perf.APIKey, updatedCh.DisabledAPIKeys[0].Key)

				// Verify error counts are cleared for this API key
				svc.apiKeyErrorCountsLock.Lock()
				_, exists := svc.apiKeyErrorCounts[ch.ID][tt.perf.APIKey]
				svc.apiKeyErrorCountsLock.Unlock()
				require.False(t, exists)
			}
		})
	}
}

func TestChannelService_checkAndHandleChannelError(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	// Create a channel without API keys (single key scenario)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-no-keys", []string{})

	tests := []struct {
		name             string
		policy           *RetryPolicy
		perf             *PerformanceRecord
		expectedDisabled bool
		setupFunc        func()
	}{
		{
			name: "first error - should not disable",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 2},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.channelErrorCounts = make(map[int]map[int]int)
				// Reset channel status
				_, err := client.Channel.UpdateOneID(ch.ID).
					SetStatus(channel.StatusEnabled).
					ClearErrorMessage().
					Save(ctx)
				require.NoError(t, err)
			},
		},
		{
			name: "second error - should disable channel",
			policy: &RetryPolicy{
				AutoDisableChannel: AutoDisableChannel{
					Enabled: true,
					Statuses: []AutoDisableChannelStatus{
						{Status: 401, Times: 2},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: true,
			setupFunc: func() {
				// Reset channel status
				_, err := client.Channel.UpdateOneID(ch.ID).
					SetStatus(channel.StatusEnabled).
					ClearErrorMessage().
					Save(ctx)
				require.NoError(t, err)

				svc.channelErrorCounts = map[int]map[int]int{
					ch.ID: {401: 1},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc()
			}

			result := svc.checkAndHandleChannelError(ctx, tt.perf, tt.policy)
			require.Equal(t, tt.expectedDisabled, result)

			if tt.expectedDisabled {
				// Give goroutine time to complete (markChannelUnavailable uses xcontext.DetachWithTimeout)
				time.Sleep(100 * time.Millisecond)

				// Verify channel is disabled
				updatedCh, err := client.Channel.Get(ctx, ch.ID)
				require.NoError(t, err)
				require.Equal(t, channel.StatusDisabled, updatedCh.Status)
				require.NotNil(t, updatedCh.ErrorMessage)

				// Verify error counts are cleared
				svc.channelErrorCountsLock.Lock()
				_, exists := svc.channelErrorCounts[ch.ID]
				svc.channelErrorCountsLock.Unlock()
				require.False(t, exists)
			}
		})
	}
}

func TestChannelService_checkAndHandleAPIKeyError_NoneActionResetsCountsWithoutDisabling(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "none-action-channel", []string{"key1"})
	policy := &RetryPolicy{
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 529, Times: 1, Action: DisableActionNone},
			},
		},
	}

	result := svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 529,
		Success:            false,
	}, policy)
	require.True(t, result)

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Empty(t, updatedCh.DisabledAPIKeys)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)

	svc.apiKeyErrorCountsLock.Lock()
	_, exists := svc.apiKeyErrorCounts[ch.ID]["key1"]
	svc.apiKeyErrorCountsLock.Unlock()
	require.False(t, exists)
}

func TestChannelService_checkAndHandleAPIKeyError_TemporaryUsesRetryAfterDuration(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "retry-after-key-channel", []string{"key1", "key2"})
	retryAfter := 2 * time.Minute
	policy := &RetryPolicy{
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 429, Times: 1, Action: DisableActionTemporary, UseRetryAfter: lo.ToPtr(true)},
			},
		},
	}

	start := time.Now()
	result := svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 429,
		RetryAfterDuration: &retryAfter,
		Success:            false,
	}, policy)
	require.True(t, result)

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Equal(t, DisableActionTemporary, updatedCh.DisabledAPIKeys[0].DisableAction)
	require.NotNil(t, updatedCh.DisabledAPIKeys[0].DisabledUntil)
	require.WithinDuration(t, start.Add(retryAfter), *updatedCh.DisabledAPIKeys[0].DisabledUntil, 5*time.Second)
}

func TestChannelService_checkAndHandleAPIKeyError_TemporaryRetryAfterFallsBackToDefaultDuration(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "fallback-key-channel", []string{"key1", "key2"})
	policy := &RetryPolicy{
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 429, Times: 1, Action: DisableActionTemporary, UseRetryAfter: lo.ToPtr(true)},
			},
		},
	}

	start := time.Now()
	result := svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 429,
		Success:            false,
	}, policy)
	require.True(t, result)

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.NotNil(t, updatedCh.DisabledAPIKeys[0].DisabledUntil)
	require.WithinDuration(t, start.Add(time.Duration(defaultAutoDisableFallbackDurationMinutes)*time.Minute), *updatedCh.DisabledAPIKeys[0].DisabledUntil, 5*time.Second)
}

func TestChannelService_checkAndHandleChannelError_TemporaryDisablesWithoutChangingStatus(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "temporary-channel", []string{})
	policy := &RetryPolicy{
		ChannelAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 1, Action: DisableActionTemporary, DurationMinutes: lo.ToPtr(3)},
			},
		},
	}

	start := time.Now()
	result := svc.checkAndHandleChannelError(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		ResponseStatusCode: 503,
		Success:            false,
	}, policy)
	require.True(t, result)

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.NotNil(t, updatedCh.TemporaryDisabledUntil)
	require.Equal(t, 503, *updatedCh.TemporaryDisabledErrorCode)
	require.NotEmpty(t, *updatedCh.TemporaryDisabledReason)
	require.WithinDuration(t, start.Add(3*time.Minute), *updatedCh.TemporaryDisabledUntil, 5*time.Second)
}

func TestChannelService_AutoDisableLegacyPolicyAPIKeyShortCircuitsChannelFallback(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, &RetryPolicy{
		AutoDisableChannel: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 401, Times: 1},
			},
		},
	}))
	ch := createTestChannelWithAPIKeys(t, client, ctx, "legacy-short-circuit", []string{"key1", "key2"})

	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 401,
		Success:            false,
		RequestCompleted:   true,
		EndTime:            time.Now(),
	})

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
}

func TestChannelService_RecordPerformance_APIKeyPolicyMissFallsBackToChannelAutoDisable(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, &RetryPolicy{
		ChannelAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 1, Action: DisableActionPermanent},
			},
		},
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 429, Times: 1, Action: DisableActionPermanent},
			},
		},
	}))
	ch := createTestChannelWithAPIKeys(t, client, ctx, "api-key-miss-channel-fallback", []string{"key1", "key2"})

	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 503,
		Success:            false,
		RequestCompleted:   true,
		EndTime:            time.Now(),
	})

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, updatedCh.Status)
	require.Empty(t, updatedCh.DisabledAPIKeys)
}

func TestChannelService_RecordPerformance_APIKeyPolicyHitShortCircuitsChannelAutoDisable(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, &RetryPolicy{
		ChannelAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 1, Action: DisableActionPermanent},
			},
		},
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 1, Action: DisableActionPermanent},
			},
		},
	}))
	ch := createTestChannelWithAPIKeys(t, client, ctx, "api-key-hit-short-circuit", []string{"key1", "key2"})

	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 503,
		Success:            false,
		RequestCompleted:   true,
		EndTime:            time.Now(),
	})

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Equal(t, "key1", updatedCh.DisabledAPIKeys[0].Key)
}

func TestChannelService_RecordPerformance_APIKeyPolicyMatchDoesNotTripChannelTemporaryDisable(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, &RetryPolicy{
		ChannelAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 429, Times: 3, Action: DisableActionTemporary, DurationMinutes: lo.ToPtr(10)},
			},
		},
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 429, Times: 3, Action: DisableActionTemporary, DurationMinutes: lo.ToPtr(10)},
			},
		},
	}))
	ch := createTestChannelWithAPIKeys(t, client, ctx, "api-key-policy-match-no-channel-temp-disable", []string{"key1", "key2"})

	for range 3 {
		svc.RecordPerformance(ctx, &PerformanceRecord{
			ChannelID:          ch.ID,
			APIKey:             "key1",
			ResponseStatusCode: 429,
			Success:            false,
			RequestCompleted:   true,
			EndTime:            time.Now(),
		})
	}

	for range 3 {
		svc.RecordPerformance(ctx, &PerformanceRecord{
			ChannelID:          ch.ID,
			APIKey:             "key2",
			ResponseStatusCode: 429,
			Success:            false,
			RequestCompleted:   true,
			EndTime:            time.Now(),
		})
	}

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Nil(t, updatedCh.TemporaryDisabledUntil)
	require.Len(t, updatedCh.DisabledAPIKeys, 2)

	svc.channelErrorCountsLock.Lock()
	_, hasChannelCounts := svc.channelErrorCounts[ch.ID]
	svc.channelErrorCountsLock.Unlock()
	require.False(t, hasChannelCounts)
}

func TestChannelService_RecordPerformance_SkipHealthStateTrackingDoesNotDisableAPIKeyOrChannel(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, &RetryPolicy{
		ChannelAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 1, Action: DisableActionPermanent},
			},
		},
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 1, Action: DisableActionPermanent},
			},
		},
	}))
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-source-skip-auto-disable", []string{"key1", "key2"})

	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:               ch.ID,
		APIKey:                  "key1",
		ResponseStatusCode:      503,
		Success:                 false,
		RequestCompleted:        true,
		EndTime:                 time.Now(),
		SkipHealthStateTracking: true,
	})

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Empty(t, updatedCh.DisabledAPIKeys)

	svc.apiKeyErrorCountsLock.Lock()
	_, hasAPIKeyCounts := svc.apiKeyErrorCounts[ch.ID]["key1"]
	svc.apiKeyErrorCountsLock.Unlock()
	require.False(t, hasAPIKeyCounts)

	svc.channelErrorCountsLock.Lock()
	_, hasChannelCounts := svc.channelErrorCounts[ch.ID]
	svc.channelErrorCountsLock.Unlock()
	require.False(t, hasChannelCounts)

	metrics, err := svc.GetChannelMetrics(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), metrics.RequestCount)
	require.Equal(t, int64(0), metrics.FailureCount)
	require.Equal(t, int64(0), metrics.ConsecutiveFailures)
	require.Nil(t, metrics.LastFailureAt)
}

func TestChannelService_RecordPerformance_SkipHealthStateTrackingSuccessDoesNotClearPolicyCounts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, &RetryPolicy{
		ChannelAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 3, Action: DisableActionPermanent},
			},
		},
		APIKeyAutoDisable: AutoDisablePolicy{
			Enabled: true,
			Statuses: []AutoDisableStatusRule{
				{Status: 503, Times: 3, Action: DisableActionPermanent},
			},
		},
	}))
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-success-preserves-auto-disable-counts", []string{"key1", "key2"})

	// 生产流量已累计到阈值前一位，测试成功不应清空这些策略状态。
	svc.channelErrorCounts = map[int]map[int]int{
		ch.ID: {503: 2},
	}
	svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
		ch.ID: {"key1": {503: 2}},
	}

	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:               ch.ID,
		APIKey:                  "key1",
		Success:                 true,
		RequestCompleted:        true,
		EndTime:                 time.Now(),
		SkipHealthStateTracking: true,
	})

	svc.channelErrorCountsLock.Lock()
	require.Equal(t, 2, svc.channelErrorCounts[ch.ID][503])
	svc.channelErrorCountsLock.Unlock()

	svc.apiKeyErrorCountsLock.Lock()
	require.Equal(t, 2, svc.apiKeyErrorCounts[ch.ID]["key1"][503])
	svc.apiKeyErrorCountsLock.Unlock()

	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 503,
		Success:            false,
		RequestCompleted:   true,
		EndTime:            time.Now(),
	})

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Equal(t, "key1", updatedCh.DisabledAPIKeys[0].Key)
}

func TestChannelService_markChannelUnavailable_RefreshesStaleLocalCacheWhenAlreadyDisabled(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	defer svc.enabledChannelsCache.Stop()

	ch := createTestChannelWithAPIKeys(t, client, ctx, "stale-cache-channel", []string{"key1"})

	require.NoError(t, svc.enabledChannelsCache.Load(ctx, true))
	require.NotNil(t, svc.GetEnabledChannel(ch.ID), "precondition: local cache should contain enabled channel")

	_, err := client.Channel.UpdateOneID(ch.ID).
		SetStatus(channel.StatusDisabled).
		SetErrorMessage("disabled elsewhere").
		Save(ctx)
	require.NoError(t, err)

	svc.markChannelUnavailable(ctx, ch.ID, 401, 2, 2)

	require.Nil(t, svc.GetEnabledChannel(ch.ID), "local cache should be refreshed even when DB row was already disabled")
}

func TestChannelService_reloadEnabledChannels_SkipsTemporaryAPIKeyExhaustedChannelBeforeBuild(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	defer svc.enabledChannelsCache.Stop()

	disabledUntil := time.Now().Add(time.Hour)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "temporary-key-exhausted-cache", []string{"key1"})
	_, err := client.Channel.UpdateOneID(ch.ID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{
			{
				Key:           "key1",
				DisabledAt:    time.Now(),
				DisabledUntil: &disabledUntil,
				DisableAction: DisableActionTemporary,
				ErrorCode:     429,
				Reason:        "temporary test disable",
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	require.NotPanics(t, func() {
		require.NoError(t, svc.enabledChannelsCache.Load(ctx, true))
	})
	require.Nil(t, svc.GetEnabledChannel(ch.ID), "temporary API key exhaustion should be filtered before provider construction")
}

func TestChannelService_reloadEnabledChannels_RestoresTemporarilyDisabledChannelAfterExpiryWithoutForce(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	defer svc.enabledChannelsCache.Stop()

	disabledUntil := time.Now().Add(25 * time.Millisecond)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "temporary-channel-restore", []string{"key1"})
	_, err := client.Channel.UpdateOneID(ch.ID).
		SetTemporaryDisabledUntil(disabledUntil).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, svc.enabledChannelsCache.Load(ctx, true))
	require.Nil(t, svc.GetEnabledChannel(ch.ID), "precondition: temporarily disabled channel should be filtered")

	require.Eventually(t, func() bool {
		if err := svc.enabledChannelsCache.Load(ctx, false); err != nil {
			return false
		}

		return svc.GetEnabledChannel(ch.ID) != nil
	}, time.Second, 10*time.Millisecond)
}

func TestChannelService_reloadEnabledChannels_RestoresTemporaryAPIKeyExhaustedChannelAfterExpiryWithoutForce(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	defer svc.enabledChannelsCache.Stop()

	disabledUntil := time.Now().Add(25 * time.Millisecond)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "temporary-key-restore", []string{"key1"})
	_, err := client.Channel.UpdateOneID(ch.ID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{
			{
				Key:           "key1",
				DisabledAt:    time.Now(),
				DisabledUntil: &disabledUntil,
				DisableAction: DisableActionTemporary,
				ErrorCode:     429,
				Reason:        "temporary test disable",
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, svc.enabledChannelsCache.Load(ctx, true))
	require.Nil(t, svc.GetEnabledChannel(ch.ID), "precondition: exhausted channel should be filtered")

	require.Eventually(t, func() bool {
		if err := svc.enabledChannelsCache.Load(ctx, false); err != nil {
			return false
		}

		return svc.GetEnabledChannel(ch.ID) != nil
	}, time.Second, 10*time.Millisecond)
}

func TestChannelService_DisableAllAPIKeysDisablesChannel(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	// Create a channel with 2 API keys
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-2-keys", []string{"key1", "key2"})

	// Disable first key
	err := svc.DisableAPIKey(ctx, ch.ID, "key1", 401, "Test reason 1")
	require.NoError(t, err)

	// Verify channel is still enabled
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)

	// Disable second key - should disable the entire channel
	err = svc.DisableAPIKey(ctx, ch.ID, "key2", 401, "Test reason 2")
	require.NoError(t, err)

	// Verify channel is now disabled
	updatedCh, err = client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 2)
	require.NotNil(t, updatedCh.ErrorMessage)
}

func TestChannelService_SuccessClearsErrorCounts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1"})

	// Set up some error counts
	svc.channelErrorCounts = map[int]map[int]int{
		ch.ID: {401: 2, 500: 1},
	}
	svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
		ch.ID: {"key1": {401: 2}},
	}

	// Record a successful request
	perf := &PerformanceRecord{
		ChannelID:        ch.ID,
		APIKey:           "key1",
		Success:          true,
		RequestCompleted: true,
		EndTime:          time.Now(),
	}

	svc.IncrementChannelSelection(ch.ID)
	svc.RecordPerformance(ctx, perf)

	// Verify channel error counts are cleared
	svc.channelErrorCountsLock.Lock()
	_, channelExists := svc.channelErrorCounts[ch.ID]
	svc.channelErrorCountsLock.Unlock()
	require.False(t, channelExists)

	// Verify API key error counts are cleared
	svc.apiKeyErrorCountsLock.Lock()
	_, keyExists := svc.apiKeyErrorCounts[ch.ID]["key1"]
	svc.apiKeyErrorCountsLock.Unlock()
	require.False(t, keyExists)
}

func TestChannelService_MultipleStatusCodes(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2"})

	policy := &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled: true,
			Statuses: []AutoDisableChannelStatus{
				{Status: 401, Times: 2},
				{Status: 403, Times: 1},
			},
		},
	}

	// Test 401 - needs 2 times
	svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
		ch.ID: {"key1": {401: 1}},
	}

	perf401 := &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 401,
		Success:            false,
	}

	result := svc.checkAndHandleAPIKeyError(ctx, perf401, policy)
	require.True(t, result)

	// Reset for 403 test
	_, err := client.Channel.UpdateOneID(ch.ID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{}).
		Save(ctx)
	require.NoError(t, err)

	svc.apiKeyErrorCounts = make(map[int]map[string]map[int]int)

	// Test 403 - needs only 1 time
	perf403 := &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key2",
		ResponseStatusCode: 403,
		Success:            false,
	}

	result = svc.checkAndHandleAPIKeyError(ctx, perf403, policy)
	require.True(t, result)

	// Verify key2 is disabled
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Equal(t, "key2", updatedCh.DisabledAPIKeys[0].Key)
}

func TestChannelService_ConcurrentErrorTracking(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2", "key3"})

	policy := &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled: true,
			Statuses: []AutoDisableChannelStatus{
				{Status: 401, Times: 5},
			},
		},
	}

	// Simulate concurrent error reporting
	var wg sync.WaitGroup

	numGoroutines := 10

	for i := range numGoroutines {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			perf := &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			}
			svc.checkAndHandleAPIKeyError(ctx, perf, policy)
		}(i)
	}

	wg.Wait()

	// Verify counts are tracked correctly (should be at least 5 to trigger disable)
	// The key should be disabled since we had 10 errors and threshold is 5
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)

	// Should have disabled key1
	require.GreaterOrEqual(t, len(updatedCh.DisabledAPIKeys), 1)
}

func TestChannelService_DisableAPIKeyIdempotent(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2"})

	// Disable key1 first time
	err := svc.DisableAPIKey(ctx, ch.ID, "key1", 401, "Reason 1")
	require.NoError(t, err)

	// Disable key1 second time - should be idempotent
	err = svc.DisableAPIKey(ctx, ch.ID, "key1", 401, "Reason 2")
	require.NoError(t, err)

	// Verify only one entry
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
}

func TestChannelService_DisableAPIKeyNotFound(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2"})

	// Try to disable a key that doesn't exist - should be ignored
	err := svc.DisableAPIKey(ctx, ch.ID, "nonexistent-key", 401, "Reason")
	require.NoError(t, err)

	// Verify no keys are disabled
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 0)
}

func TestChannelService_DisableAPIKeyEmptyKey(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1"})

	// Try to disable an empty key - should return error
	err := svc.DisableAPIKey(ctx, ch.ID, "", 401, "Reason")
	require.Error(t, err)
}
