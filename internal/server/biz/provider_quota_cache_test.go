package biz

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/providerquotastatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz/provider_quota"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderQuotaService_GetQuotaStatus_ReturnsCorrectData(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	svc.quotaCache.Store(1, &QuotaChannelStatus{Status: providerquotastatus.StatusAvailable, Ready: true})
	svc.quotaCache.Store(2, &QuotaChannelStatus{Status: providerquotastatus.StatusExhausted, Ready: false})
	svc.quotaCache.Store(3, &QuotaChannelStatus{Status: providerquotastatus.StatusWarning, Ready: true})

	status1 := svc.GetQuotaStatus(t.Context(), 1)
	assert.NotNil(t, status1)
	assert.Equal(t, providerquotastatus.StatusAvailable, status1.Status)
	assert.True(t, status1.Ready)

	status2 := svc.GetQuotaStatus(t.Context(), 2)
	assert.NotNil(t, status2)
	assert.Equal(t, providerquotastatus.StatusExhausted, status2.Status)
	assert.False(t, status2.Ready)

	status3 := svc.GetQuotaStatus(t.Context(), 3)
	assert.NotNil(t, status3)
	assert.Equal(t, providerquotastatus.StatusWarning, status3.Status)
	assert.True(t, status3.Ready)
}

func TestProviderQuotaService_GetQuotaStatus_UnknownChannel(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	status := svc.GetQuotaStatus(t.Context(), 999)
	assert.Nil(t, status)
}

func TestProviderQuotaService_UpdateQuotaCache(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	svc.updateQuotaCache(1, "", providerquotastatus.StatusAvailable, true, nil)
	svc.updateQuotaCache(2, "", providerquotastatus.StatusExhausted, false, nil)

	status1 := svc.GetQuotaStatus(t.Context(), 1)
	assert.NotNil(t, status1)
	assert.Equal(t, providerquotastatus.StatusAvailable, status1.Status)
	assert.True(t, status1.Ready)

	status2 := svc.GetQuotaStatus(t.Context(), 2)
	assert.NotNil(t, status2)
	assert.Equal(t, providerquotastatus.StatusExhausted, status2.Status)
	assert.False(t, status2.Ready)
}

func TestProviderQuotaService_UpdateQuotaCache_Overwrite(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	svc.updateQuotaCache(1, "", providerquotastatus.StatusAvailable, true, nil)
	svc.updateQuotaCache(1, "", providerquotastatus.StatusExhausted, false, nil)

	status := svc.GetQuotaStatus(t.Context(), 1)
	assert.NotNil(t, status)
	assert.Equal(t, providerquotastatus.StatusExhausted, status.Status)
	assert.False(t, status.Ready)
}

func TestProviderQuotaService_ConcurrentAccess(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	var wg sync.WaitGroup
	const goroutines = 50

	wg.Add(goroutines)
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			svc.updateQuotaCache(id, "", providerquotastatus.StatusAvailable, true, nil)
		}(i)
	}

	wg.Add(goroutines)
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			_ = svc.GetQuotaStatus(t.Context(), id)
		}(i)
	}

	wg.Wait()

	for i := range goroutines {
		status := svc.GetQuotaStatus(t.Context(), i)
		assert.NotNil(t, status, "channel %d should have quota status", i)
		assert.Equal(t, providerquotastatus.StatusAvailable, status.Status)
		assert.True(t, status.Ready)
	}
}

func TestProviderQuotaService_ConcurrentReadWrite(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	svc.updateQuotaCache(1, "", providerquotastatus.StatusAvailable, true, nil)

	var wg sync.WaitGroup
	const iterations = 100

	wg.Add(iterations)
	for range iterations {
		go func() {
			defer wg.Done()
			svc.updateQuotaCache(1, "", providerquotastatus.StatusExhausted, false, nil)
		}()
	}

	wg.Add(iterations)
	for range iterations {
		go func() {
			defer wg.Done()
			_ = svc.GetQuotaStatus(t.Context(), 1)
		}()
	}

	wg.Wait()

	status := svc.GetQuotaStatus(t.Context(), 1)
	assert.NotNil(t, status)
	assert.Equal(t, providerquotastatus.StatusExhausted, status.Status)
	assert.False(t, status.Ready)
}

func TestProviderQuotaService_UpdateQuotaCache_WithLimits(t *testing.T) {
	svc := &ProviderQuotaService{
		quotaCache: sync.Map{},
	}

	limits := []provider_quota.QuotaLimitStatus{
		{Type: provider_quota.QuotaLimitTypeToken, Status: "available", UsageRatio: 0.3, Ready: true},
		{Type: provider_quota.QuotaLimitTypeImage, Status: "exhausted", UsageRatio: 1.0, Ready: false},
	}

	svc.updateQuotaCache(1, "", providerquotastatus.StatusWarning, true, limits)

	status := svc.GetQuotaStatus(t.Context(), 1)
	assert.NotNil(t, status)
	assert.Equal(t, providerquotastatus.StatusWarning, status.Status)
	assert.True(t, status.Ready)
	assert.Len(t, status.Limits, 2)

	assert.Equal(t, provider_quota.QuotaLimitTypeToken, status.Limits[0].Type)
	assert.Equal(t, "available", status.Limits[0].Status)
	assert.InDelta(t, 0.3, status.Limits[0].UsageRatio, 0.001)
	assert.True(t, status.Limits[0].Ready)

	assert.Equal(t, provider_quota.QuotaLimitTypeImage, status.Limits[1].Type)
	assert.Equal(t, "exhausted", status.Limits[1].Status)
	assert.InDelta(t, 1.0, status.Limits[1].UsageRatio, 0.001)
	assert.False(t, status.Limits[1].Ready)

	effectiveStatus, ready := status.EffectiveStatus(provider_quota.QuotaLimitTypeImage)
	assert.Equal(t, providerquotastatus.StatusExhausted, effectiveStatus)
	assert.False(t, ready)

	effectiveStatus, ready = status.EffectiveStatus(provider_quota.QuotaLimitTypeToken)
	assert.Equal(t, providerquotastatus.StatusAvailable, effectiveStatus)
	assert.True(t, ready)
}

func TestHasOpenCodeGoQuotaCredentialsRequiresCookie(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID string
		authCookie  string
		want        bool
	}{
		{
			name:        "complete quota credentials",
			workspaceID: "wk_123",
			authCookie:  "cookie",
			want:        true,
		},
		{
			name:        "missing workspace id still enters checker",
			workspaceID: "",
			authCookie:  "cookie",
			want:        true,
		},
		{
			name:        "missing auth cookie",
			workspaceID: "wk_123",
			authCookie:  "",
			want:        false,
		},
		{
			name:        "blank values",
			workspaceID: " ",
			authCookie:  " ",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasOpenCodeGoQuotaCredentials(&ent.Channel{
				Settings: &objects.ChannelSettings{
					ProviderQuota: &objects.ChannelProviderQuotaSettings{
						OpencodeGo: &objects.OpenCodeGoQuotaSettings{
							WorkspaceID: tt.workspaceID,
							AuthCookie:  tt.authCookie,
						},
					},
				},
			})

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProviderQuotaService_CheckChannelQuotaSavesOpenCodeGoMissingWorkspaceError(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	now := time.Date(2026, 6, 30, 1, 0, 0, 0, time.UTC)
	svc := &ProviderQuotaService{
		AbstractService: &AbstractService{db: client},
		SystemService:   NewSystemService(SystemServiceParams{Ent: client}),
		checkers: map[string]provider_quota.QuotaChecker{
			"opencode_go": provider_quota.NewOpenCodeGoQuotaChecker(httpclient.NewHttpClient()),
		},
		checkInterval: time.Hour,
		quotaCache:    sync.Map{},
	}

	ch, err := client.Channel.Create().
		SetType(channel.TypeOpencodeGo).
		SetName("OpenCode Go Missing Workspace").
		SetBaseURL("https://opencode.ai/zen/go/v1").
		SetCredentials(objects.ChannelCredentials{}).
		SetSettings(&objects.ChannelSettings{
			ProviderQuota: &objects.ChannelProviderQuotaSettings{
				OpencodeGo: &objects.OpenCodeGoQuotaSettings{
					AuthCookie: "cookie",
				},
			},
		}).
		SetSupportedModels([]string{"opencode/go"}).
		SetDefaultTestModel("opencode/go").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	svc.checkChannelQuota(ctx, ch, now)

	status, err := client.ProviderQuotaStatus.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, ch.ID, status.ChannelID)
	require.Equal(t, providerquotastatus.ProviderType("opencode_go"), status.ProviderType)
	require.Equal(t, providerquotastatus.StatusUnknown, status.Status)
	require.False(t, status.Ready)
	require.Equal(t, "missing OpenCode Go workspace id", status.QuotaData["error"])
	require.EqualValues(t, 1, status.QuotaData["error_count"])
}

func TestMergeAndExtractLimitsRoundTrip(t *testing.T) {
	svc := &ProviderQuotaService{}
	resetAt := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("basic round trip", func(t *testing.T) {
		quotaData := provider_quota.QuotaData{
			Status:       "available",
			ProviderType: "test",
			RawData:      map[string]any{"key": "value"},
			Limits: []provider_quota.QuotaLimitStatus{
				{Type: provider_quota.QuotaLimitTypeToken, Status: "available", UsageRatio: 0.3, Ready: true},
				{Type: provider_quota.QuotaLimitTypeImage, Status: "exhausted", UsageRatio: 1.0, Ready: false, NextResetAt: &resetAt},
			},
		}

		merged := svc.mergeLimitsIntoQuotaData(quotaData)
		extracted := extractLimitsFromQuotaData(merged)

		assert.Len(t, extracted, 2)
		tokenLimits := lo.Filter(extracted, func(l provider_quota.QuotaLimitStatus, _ int) bool {
			return l.Type == provider_quota.QuotaLimitTypeToken
		})
		assert.Len(t, tokenLimits, 1)
		assert.Equal(t, "available", tokenLimits[0].Status)
		assert.InDelta(t, 0.3, tokenLimits[0].UsageRatio, 0.001)
		assert.True(t, tokenLimits[0].Ready)
		assert.Nil(t, tokenLimits[0].NextResetAt)

		imageLimits := lo.Filter(extracted, func(l provider_quota.QuotaLimitStatus, _ int) bool {
			return l.Type == provider_quota.QuotaLimitTypeImage
		})
		assert.Len(t, imageLimits, 1)
		assert.Equal(t, "exhausted", imageLimits[0].Status)
		assert.InDelta(t, 1.0, imageLimits[0].UsageRatio, 0.001)
		assert.False(t, imageLimits[0].Ready)
		assert.NotNil(t, imageLimits[0].NextResetAt)
		assert.Equal(t, resetAt, *imageLimits[0].NextResetAt)

		assert.Equal(t, "value", merged["key"])
	})

	t.Run("empty limits", func(t *testing.T) {
		quotaData := provider_quota.QuotaData{
			Status:       "available",
			ProviderType: "test",
		}

		merged := svc.mergeLimitsIntoQuotaData(quotaData)
		extracted := extractLimitsFromQuotaData(merged)

		assert.Nil(t, extracted)
	})

	t.Run("preserves raw data", func(t *testing.T) {
		quotaData := provider_quota.QuotaData{
			Status:       "available",
			ProviderType: "test",
			RawData:      map[string]any{"existing": "data"},
			Limits: []provider_quota.QuotaLimitStatus{
				{Type: provider_quota.QuotaLimitTypeToken, Status: "available", UsageRatio: 0.5, Ready: true},
			},
		}

		merged := svc.mergeLimitsIntoQuotaData(quotaData)
		assert.Equal(t, "data", merged["existing"])
		assert.NotNil(t, merged["_limits"])
	})
}
