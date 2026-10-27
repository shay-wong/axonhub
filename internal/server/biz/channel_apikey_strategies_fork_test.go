package biz

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/objects"
)

func TestMultiKeyStrategiesHonorStructuredKeysAndRequestExclusions(t *testing.T) {
	for _, strategy := range []string{
		APIKeySelectionStrategyWeightedSticky,
		APIKeySelectionStrategyFailover,
		objects.APIKeyStrategySticky,
		objects.APIKeyStrategyRandom,
		objects.APIKeyStrategyRoundRobin,
		objects.APIKeyStrategyRoundRobinSuccess,
		objects.APIKeyStrategyPriority,
		objects.APIKeyStrategyFixed,
	} {
		t.Run(strategy, func(t *testing.T) {
			ch := withStrategy(newKeyChannel(1, nil), strategy)
			ch.Credentials.APIKeyConfigs = []objects.ChannelAPIKeyConfig{
				{Key: "structured-a", Name: "primary", Weight: 100},
				{Key: "structured-b", Name: "backup", Weight: 100},
			}
			svc := &ChannelService{}
			ch.apiKeyState = svc.apiKeySelectionStateFor(ch)
			svc.publishAPIKeySelectionSnapshot(ch)
			provider := newMultiKeyProvider(ch)
			ctx := contexts.EnsureContainer(context.Background())
			first := provider.Get(ctx)
			require.Contains(t, []string{"structured-a", "structured-b"}, first)
			contexts.ExcludeChannelAPIKey(ctx, ch.ID, first)
			second := provider.Get(ctx)
			require.NotEqual(t, first, second)
			selected, ok := contexts.GetChannelAPIKey(ctx)
			require.True(t, ok)
			require.Equal(t, second, selected)
			contexts.ExcludeChannelAPIKey(ctx, ch.ID, second)
			require.Panics(t, func() { provider.Get(ctx) })
		})
	}
}

func TestUnifiedAPIKeyStrategyPreservesLegacyAndCanonicalPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		legacy    string
		canonical *string
		want      any
	}{
		{"default sticky", "", nil, &sharedStickyKeyProvider{}},
		{"legacy sticky", APIKeySelectionStrategyTraceSticky, nil, &sharedStickyKeyProvider{}},
		{"legacy weighted", APIKeySelectionStrategyWeightedSticky, nil, &WeightedTraceStickyKeyProvider{}},
		{"legacy failover", APIKeySelectionStrategyFailover, nil, &FailoverAPIKeyProvider{}},
		{"canonical weighted", "", lo.ToPtr(APIKeySelectionStrategyWeightedSticky), &WeightedTraceStickyKeyProvider{}},
		{"canonical failover", "", lo.ToPtr(APIKeySelectionStrategyFailover), &FailoverAPIKeyProvider{}},
		{"canonical wins", APIKeySelectionStrategyFailover, lo.ToPtr(objects.APIKeyStrategySticky), &sharedStickyKeyProvider{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newKeyChannel(1, []string{"a", "b"})
			ch.Settings = &objects.ChannelSettings{
				APIKeyStrategy:          tc.canonical,
				APIKeySelectionStrategy: tc.legacy,
			}
			require.IsType(t, tc.want, innerProvider(t, ch))
		})
	}
}

func TestUnifiedAPIKeyStrategyKeepsWeightBasedFailover(t *testing.T) {
	ch := withStrategy(newKeyChannel(1, nil), APIKeySelectionStrategyFailover)
	ch.Credentials.APIKeyConfigs = []objects.ChannelAPIKeyConfig{
		{Key: "first", Weight: 1}, {Key: "higher-weight", Weight: 100},
	}
	provider := getAPIKeyProvider(ch)
	ctx := contexts.EnsureContainer(context.Background())
	require.Equal(t, "higher-weight", provider.Get(ctx))
	contexts.ExcludeChannelAPIKey(ctx, ch.ID, "higher-weight")
	require.Equal(t, "first", provider.Get(ctx))
	require.Equal(t, "higher-weight", provider.Get(context.Background()))
}

func TestStatefulKeyStrategiesDoNotPersistRequestExclusions(t *testing.T) {
	for _, strategy := range []string{objects.APIKeyStrategyFixed, objects.APIKeyStrategyRoundRobinSuccess} {
		t.Run(strategy, func(t *testing.T) {
			ch := withStrategy(newKeyChannel(1, []string{"a", "b"}), strategy)
			svc := &ChannelService{}
			ch.apiKeyState = svc.apiKeySelectionStateFor(ch)
			svc.publishAPIKeySelectionSnapshot(ch)
			provider := newMultiKeyProvider(ch)
			ctx := contexts.EnsureContainer(context.Background())
			require.Equal(t, "a", provider.Get(ctx))
			contexts.ExcludeChannelAPIKey(ctx, ch.ID, "a")
			require.Equal(t, "b", provider.Get(ctx))
			svc.onAPIKeySuccess(ch.ID, "b")
			require.Equal(t, "a", provider.Get(context.Background()))
		})
	}
}

func TestMultiKeyStrategiesRecoverExpiredDisableWithoutReload(t *testing.T) {
	for _, strategy := range []string{
		objects.APIKeyStrategySticky, objects.APIKeyStrategyRandom, objects.APIKeyStrategyRoundRobin,
		objects.APIKeyStrategyRoundRobinSuccess, objects.APIKeyStrategyPriority, objects.APIKeyStrategyFixed,
	} {
		t.Run(strategy, func(t *testing.T) {
			ch := withStrategy(newKeyChannel(1, []string{"a", "b"}, "a", "b"), strategy)
			ch.DisabledAPIKeys[0].DisabledUntil = lo.ToPtr(time.Now().Add(-time.Second))
			svc := &ChannelService{}
			ch.apiKeyState = svc.apiKeySelectionStateFor(ch)
			svc.publishAPIKeySelectionSnapshot(ch)
			require.Equal(t, "a", newMultiKeyProvider(ch).Get(context.Background()))
		})
	}
}
