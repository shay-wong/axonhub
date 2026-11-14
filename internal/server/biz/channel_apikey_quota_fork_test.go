// 职责: 验证逐 Key 配额与 fork 禁用、恢复和结构化密钥身份的兼容。
// 关联文件:
//   internal/server/biz/channel_apikey.go
//   internal/server/biz/channel_apikey_quota.go
//   internal/server/biz/channel_events.go
//   internal/server/biz/provider_quota.go
// 遵循的 Rules:
//   .agents/rules/go-general.md
//   .agents/rules/biz-services.md
// 注意事项: 配额恢复只能解除配额拥有的禁用，恢复事件必须反映实际持久化状态。

package biz

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/events"
)

func TestQuotaKeyFork_TemporaryRuleTakesOwnershipAndPreservesKeyConfig(t *testing.T) {
	env := setupQuotaKeyTestEnv(t)
	ch := env.createOpenCodeChannel(t, quotaTestKeyA, quotaTestKeyB)
	configs := []objects.ChannelAPIKeyConfig{
		{Key: quotaTestKeyA, Name: "primary", Weight: 100},
		{Key: quotaTestKeyB, Name: "backup", Weight: 50},
	}
	_, err := env.client.Channel.UpdateOneID(ch.ID).
		SetCredentials(objects.ChannelCredentials{APIKeyConfigs: configs}).Save(env.ctx)
	require.NoError(t, err)

	env.server.usage(quotaTestKeyA, 100, 10, 10)
	env.server.usage(quotaTestKeyB, 10, 10, 10)
	env.poll(t)
	require.Equal(t, []string{quotaTestKeyA}, quotaOwnedKeys(env.reload(t, ch.ID)))

	duration := time.Hour
	require.NoError(t, env.channels.ApplyAPIKeyDisableAction(
		env.ctx, ch.ID, quotaTestKeyA, DisableActionTemporary, &duration, 429, "temporary rule",
	))
	env.server.usage(quotaTestKeyA, 10, 10, 10)
	env.poll(t)

	current := env.reload(t, ch.ID)
	require.Equal(t, channel.StatusEnabled, current.Status)
	require.Equal(t, configs, current.Credentials.APIKeyConfigs)
	require.Len(t, current.DisabledAPIKeys, 1)
	record := current.DisabledAPIKeys[0]
	require.False(t, record.QuotaDisabled)
	require.Equal(t, DisableActionTemporary, record.DisableAction)
	require.Equal(t, "temporary rule", record.Reason)
	require.NotNil(t, record.DisabledUntil)
	require.Equal(t, record.DisabledUntil, record.ExpiresAt)
	require.Equal(t, []string{quotaTestKeyB}, enabledKeysOf(current))
}

func TestQuotaKeyFork_MixedPermanentAndQuotaDisableStillPollsAndRecovers(t *testing.T) {
	env := setupQuotaKeyTestEnv(t)
	ch := env.createOpenCodeChannel(t, quotaTestKeyA, quotaTestKeyB)
	env.server.usage(quotaTestKeyA, 100, 10, 10)
	env.server.usage(quotaTestKeyB, 10, 10, 10)
	env.poll(t)

	require.NoError(t, env.channels.DisableAPIKey(env.ctx, ch.ID, quotaTestKeyB, 401, "manual disable"))
	disabled := env.reload(t, ch.ID)
	require.Equal(t, channel.StatusDisabled, disabled.Status)
	require.Contains(t, *disabled.ErrorMessage, allAPIKeysPermanentlyDisabledMessagePrefix)
	require.True(t, isQuotaPollableChannel(disabled))

	bus := events.NewBus()
	t.Cleanup(func() { require.NoError(t, bus.Close(context.Background())) })
	env.channels.EventBus = bus
	received := subscribeEvent(t, bus, events.TypeChannelCredentialEnabled)
	env.server.usage(quotaTestKeyA, 10, 10, 10)
	env.poll(t)

	current := env.reload(t, ch.ID)
	require.Equal(t, channel.StatusEnabled, current.Status)
	require.Nil(t, current.AutoDisabledAt)
	require.Equal(t, []string{quotaTestKeyA}, enabledKeysOf(current))
	require.Len(t, current.DisabledAPIKeys, 1)
	require.Equal(t, quotaTestKeyB, current.DisabledAPIKeys[0].Key)
	require.False(t, current.DisabledAPIKeys[0].QuotaDisabled)
	event := requireEvent(t, received)
	require.Equal(t, current.Status.String(), event.Subject.Channel.Status)
	require.Equal(t, credentialTriggerQuota, event.Data["trigger"])
	requireNoSecret(t, event, quotaTestKeyA)
}

func TestQuotaKeyFork_ManualRecoveryUsesPersistedChannelState(t *testing.T) {
	for _, operation := range []struct {
		name   string
		enable func(*ChannelService, context.Context, int, string) error
	}{
		{"single", func(s *ChannelService, ctx context.Context, id int, key string) error {
			return s.EnableAPIKey(ctx, id, key)
		}},
		{"selected", func(s *ChannelService, ctx context.Context, id int, key string) error {
			return s.EnableSelectedAPIKeys(ctx, id, []string{key})
		}},
		{"all", func(s *ChannelService, ctx context.Context, id int, _ string) error {
			return s.EnableAllAPIKeys(ctx, id)
		}},
	} {
		for _, manual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/manual=%t", operation.name, manual), func(t *testing.T) {
				env := setupQuotaKeyTestEnv(t)
				ch := env.createOpenCodeChannel(t, quotaTestKeyA)
				env.server.usage(quotaTestKeyA, 100, 10, 10)
				env.poll(t)
				want := channel.StatusEnabled
				if manual {
					want = channel.StatusDisabled
					_, err := env.client.Channel.UpdateOneID(ch.ID).ClearAutoDisabledAt().Save(env.ctx)
					require.NoError(t, err)
				}

				bus := events.NewBus()
				t.Cleanup(func() { require.NoError(t, bus.Close(context.Background())) })
				env.channels.EventBus = bus
				received := subscribeEvent(t, bus, events.TypeChannelCredentialEnabled)
				require.NoError(t, operation.enable(env.channels, env.ctx, ch.ID, quotaTestKeyA))
				current := env.reload(t, ch.ID)
				require.Equal(t, want, current.Status)
				require.Nil(t, current.AutoDisabledAt)
				require.Empty(t, current.DisabledAPIKeys)
				event := requireEvent(t, received)
				require.Equal(t, current.Status.String(), event.Subject.Channel.Status)
			})
		}
	}
}

type quotaDeleteLockProbe struct {
	channels    *ChannelService
	invalidator ChannelProviderQuotaInvalidator
	unlocked    bool
}

func (probe *quotaDeleteLockProbe) InvalidateChannelQuota(ctx context.Context, channelID int) error {
	if !probe.channels.apiKeyOpsLock.TryLock() {
		return fmt.Errorf("quota invalidation called while the API key lock is held")
	}
	probe.channels.apiKeyOpsLock.Unlock()
	probe.unlocked = true

	return probe.invalidator.InvalidateChannelQuota(ctx, channelID)
}

func TestQuotaKeyFork_DeleteInvalidatesAfterUnlock(t *testing.T) {
	env := setupQuotaKeyTestEnv(t)
	const thirdKey = "opencode-secret-cccc-3333"
	ch := env.createOpenCodeChannel(t, quotaTestKeyA, quotaTestKeyB, thirdKey)
	env.server.usage(quotaTestKeyA, 100, 10, 10)
	env.server.usage(quotaTestKeyB, 10, 10, 10)
	env.server.usage(thirdKey, 10, 10, 10)
	env.poll(t)
	require.NoError(t, env.channels.DisableAPIKey(env.ctx, ch.ID, quotaTestKeyB, 401, "manual disable"))
	probe := &quotaDeleteLockProbe{channels: env.channels, invalidator: env.quota}
	env.channels.SetChannelProviderQuotaInvalidator(probe)

	_, err := env.channels.DeleteDisabledAPIKeys(env.ctx, ch.ID, []string{quotaTestKeyB})
	require.NoError(t, err)
	require.True(t, probe.unlocked, "the quota callback must be able to acquire the API key lock")
	current := env.reload(t, ch.ID)
	require.Equal(t, []string{quotaTestKeyA, thirdKey}, current.Credentials.GetAllAPIKeys())
	require.Empty(t, quotaOwnedKeys(current), "credential changes release stale quota ownership")
	require.Equal(t, channel.StatusEnabled, current.Status)
}

func TestQuotaKeyFork_LegacyManualKeyNormalizationAcrossPolls(t *testing.T) {
	env := setupQuotaKeyTestEnv(t)
	ch := env.createOpenCodeChannel(t, quotaTestKeyA, quotaTestKeyB)
	manual := objects.DisabledAPIKey{
		Key:           "  " + quotaTestKeyA + "  ",
		DisabledAt:    time.Now().UTC(),
		DisableAction: DisableActionPermanent,
		ErrorCode:     401,
		Reason:        "legacy manual disable",
	}
	_, err := env.client.Channel.UpdateOneID(ch.ID).
		SetCredentials(objects.ChannelCredentials{APIKeyConfigs: []objects.ChannelAPIKeyConfig{
			{Key: quotaTestKeyA, Name: "primary", Weight: 100},
			{Key: quotaTestKeyB, Name: "backup", Weight: 50},
		}}).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{manual}).Save(env.ctx)
	require.NoError(t, err)

	for _, step := range []struct {
		name       string
		manualUsed float64
		otherUsed  float64
		status     channel.Status
		quotaOwned []string
	}{
		{"manual key funded", 10, 10, channel.StatusEnabled, nil},
		{"manual key exhausted", 100, 10, channel.StatusEnabled, nil},
		{"every key unavailable", 100, 100, channel.StatusDisabled, []string{quotaTestKeyB}},
		{"only manual key funded", 10, 100, channel.StatusDisabled, []string{quotaTestKeyB}},
		{"other key recovered", 10, 10, channel.StatusEnabled, nil},
	} {
		t.Run(step.name, func(t *testing.T) {
			env.server.usage(quotaTestKeyA, step.manualUsed, 10, 10)
			env.server.usage(quotaTestKeyB, step.otherUsed, 10, 10)
			env.poll(t)
			current := env.reload(t, ch.ID)
			require.Equal(t, step.status, current.Status)
			require.Len(t, current.DisabledAPIKeys, 1+len(step.quotaOwned))
			require.Equal(t, manual, current.DisabledAPIKeys[0], "manual identity and ownership must not be rewritten")
			require.ElementsMatch(t, step.quotaOwned, quotaOwnedKeys(current))

			stored, err := env.client.ProviderQuotaStatus.Query().Only(env.ctx)
			require.NoError(t, err)
			accounts := apiKeyQuotaRows(t, stored.QuotaData)
			require.Len(t, accounts, 2)
			require.Equal(t, true, accounts[0]["disabled"], "normalization must survive ownership refresh")
			require.Equal(t, false, accounts[0]["quotaDisabled"], "manual disable takes precedence over observations")
			env.reloadCache(t)
			cached := env.channels.GetEnabledChannel(ch.ID)
			if step.status == channel.StatusDisabled {
				require.Nil(t, cached, "a pool with no available key must not remain routable")
			} else {
				require.NotNil(t, cached)
				require.Equal(t, []string{quotaTestKeyB}, cached.GetEnabledAPIKeys())
			}
		})
	}
}

func TestQuotaKeyFork_LegacyManualOwnershipOverridesDuplicateQuotaRecord(t *testing.T) {
	for _, quotaFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("quotaFirst=%t", quotaFirst), func(t *testing.T) {
			env := setupQuotaKeyTestEnv(t)
			ch := env.createOpenCodeChannel(t, quotaTestKeyA, quotaTestKeyB)
			env.server.usage(quotaTestKeyA, 10, 10, 10)
			env.server.usage(quotaTestKeyB, 10, 10, 10)
			env.poll(t)
			manual := objects.DisabledAPIKey{
				Key:        "  " + quotaTestKeyA + "  ",
				DisabledAt: time.Now().UTC(),
				ErrorCode:  401,
				Reason:     "legacy manual disable",
			}
			quota := objects.DisabledAPIKey{
				Key:           quotaTestKeyA,
				DisabledAt:    manual.DisabledAt,
				QuotaDisabled: true,
			}
			records := []objects.DisabledAPIKey{manual, quota}
			if quotaFirst {
				records[0], records[1] = records[1], records[0]
			}
			_, err := env.client.Channel.UpdateOneID(ch.ID).SetDisabledAPIKeys(records).Save(env.ctx)
			require.NoError(t, err)
			bus := events.NewBus()
			t.Cleanup(func() { require.NoError(t, bus.Close(context.Background())) })
			env.channels.EventBus = bus
			subscribeEvent(t, bus, events.TypeChannelCredentialEnabled)

			for _, used := range []float64{100, 10} {
				env.server.usage(quotaTestKeyA, used, 10, 10)
				env.server.usage(quotaTestKeyB, 10, 10, 10)
				env.poll(t)
				// Cleanup can change the snapshot version before the collection pass.
				env.poll(t)
				current := env.reload(t, ch.ID)
				require.Equal(t, []objects.DisabledAPIKey{manual}, current.DisabledAPIKeys)
				require.Equal(t, []string{quotaTestKeyB}, enabledKeysOf(current))
				stored, err := env.client.ProviderQuotaStatus.Query().Only(env.ctx)
				require.NoError(t, err)
				account := apiKeyQuotaRows(t, stored.QuotaData)[0]
				require.Equal(t, true, account["disabled"])
				require.Equal(t, false, account["quotaDisabled"])
			}
			require.Zero(t, bus.Stats().Published, "removing duplicate ownership must not announce a manual key as enabled")
		})
	}
}

func TestQuotaKeyFork_LegacyQuotaRecordRecoversWithNormalizedEventIdentity(t *testing.T) {
	env := setupQuotaKeyTestEnv(t)
	ch := env.createOpenCodeChannel(t, quotaTestKeyA, quotaTestKeyB)
	env.server.usage(quotaTestKeyA, 10, 10, 10)
	env.server.usage(quotaTestKeyB, 10, 10, 10)
	env.poll(t)
	quota := objects.DisabledAPIKey{
		Key:           "  " + quotaTestKeyA + "  ",
		DisabledAt:    time.Now().UTC(),
		QuotaDisabled: true,
	}
	_, err := env.client.Channel.UpdateOneID(ch.ID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{quota}).Save(env.ctx)
	require.NoError(t, err)
	env.server.usage(quotaTestKeyA, 100, 10, 10)
	env.poll(t)
	current := env.reload(t, ch.ID)
	require.Equal(t, []objects.DisabledAPIKey{quota}, current.DisabledAPIKeys)
	require.Equal(t, []string{quotaTestKeyB}, enabledKeysOf(current))

	bus := events.NewBus()
	t.Cleanup(func() { require.NoError(t, bus.Close(context.Background())) })
	env.channels.EventBus = bus
	received := subscribeEvent(t, bus, events.TypeChannelCredentialEnabled)
	env.server.usage(quotaTestKeyA, 10, 10, 10)
	env.poll(t)
	require.Empty(t, env.reload(t, ch.ID).DisabledAPIKeys)
	event := requireEvent(t, received)
	require.Equal(t, events.Fingerprint(quotaTestKeyA), event.Subject.Credential.Fingerprint)
	requireNoSecret(t, event, quotaTestKeyA)
}
