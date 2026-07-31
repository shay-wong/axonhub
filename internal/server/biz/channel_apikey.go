package biz

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
)

const allAPIKeysPermanentlyDisabledMessagePrefix = "All API keys permanently disabled"

// DisableAPIKey permanently disables the specified upstream API key.
func (svc *ChannelService) DisableAPIKey(ctx context.Context, channelID int, key string, errorCode int, reason string) error {
	return svc.ApplyAPIKeyDisableAction(ctx, channelID, key, DisableActionPermanent, nil, errorCode, reason)
}

func (svc *ChannelService) ApplyAPIKeyDisableAction(
	ctx context.Context,
	channelID int,
	key string,
	action string,
	duration *time.Duration,
	errorCode int,
	reason string,
) error {
	if key == "" {
		return fmt.Errorf("api key cannot be empty")
	}
	if action == "" {
		action = DisableActionPermanent
	}
	if action == DisableActionNone {
		return nil
	}
	if action == DisableActionTemporary && (duration == nil || *duration <= 0) {
		return fmt.Errorf("temporary api key disable requires positive duration")
	}

	// 读取 channel
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}
	identity := (&Channel{Channel: ch}).APIKeyIdentity(key)

	// 检查 key 是否在 credentials 中
	allKeys := ch.Credentials.GetAllAPIKeys()

	found := slices.Contains(allKeys, key)
	if !found {
		// key 不在 credentials 中，忽略
		return nil
	}

	now := time.Now()
	_, disabledIndex, disabled := lo.FindIndexOf(ch.DisabledAPIKeys, func(dk objects.DisabledAPIKey) bool {
		if dk.Key != key {
			return false
		}
		return dk.DisabledUntil == nil || dk.DisabledUntil.After(now)
	})

	if disabled && (action != DisableActionPermanent || ch.DisabledAPIKeys[disabledIndex].DisabledUntil == nil) {
		// Keep an existing permanent disable, or any active disable when the new
		// action is not stronger.
		return nil
	}

	var disabledUntil *time.Time
	if action == DisableActionTemporary {
		until := now.Add(*duration)
		disabledUntil = &until
	}

	disabledKey := objects.DisabledAPIKey{
		Key:           key,
		DisabledAt:    now,
		DisabledUntil: disabledUntil,
		DisableAction: action,
		ErrorCode:     errorCode,
		Reason:        reason,
	}

	newDisabledKeys := slices.Clone(ch.DisabledAPIKeys)
	if disabled {
		newDisabledKeys[disabledIndex] = disabledKey
	} else {
		newDisabledKeys = append(newDisabledKeys, disabledKey)
	}

	// 计算 enabled keys
	enabledKeys := ch.Credentials.GetEnabledAPIKeysAt(newDisabledKeys, now)

	// 更新 channel
	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys)

	channelDisabled := false
	if len(enabledKeys) == 0 && allDisabledAPIKeysPermanent(ch.Credentials.GetAllAPIKeys(), newDisabledKeys, now) {
		channelDisabled = true
	}
	if channelDisabled {
		update.SetStatus(channel.StatusDisabled)
		update.SetErrorMessage(fmt.Sprintf("%s (last error: %d)", allAPIKeysPermanentlyDisabledMessagePrefix, errorCode))
		fields := []log.Field{
			log.Int("channel_id", channelID),
			log.String("channel_name", ch.Name),
		}
		log.Warn(ctx, "Channel disabled because all API keys are permanently disabled", append(fields, apiKeyIdentityLogFields(identity)...)...)
	}

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to disable api key: %w", err)
	}

	fields := []log.Field{
		log.Int("channel_id", channelID),
		log.Int("error_code", errorCode),
		log.String("action", action),
	}
	log.Info(ctx, "API key disabled", append(fields, apiKeyIdentityLogFields(identity)...)...)

	reloadCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := svc.enabledChannelsCache.Load(reloadCtx, true); err != nil {
		log.Warn(ctx, "Failed to synchronously reload channels after API key disable",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	}

	// Also notify other instances via the watcher for cross-instance cache invalidation.
	svc.asyncReloadChannels()

	return nil
}

func allDisabledAPIKeysPermanent(allKeys []string, disabledKeys []objects.DisabledAPIKey, now time.Time) bool {
	if len(allKeys) == 0 {
		return false
	}

	disabledByKey := make(map[string]objects.DisabledAPIKey, len(disabledKeys))
	for _, disabledKey := range disabledKeys {
		if disabledKey.Key == "" {
			continue
		}
		if disabledKey.DisabledUntil != nil && !disabledKey.DisabledUntil.After(now) {
			continue
		}
		disabledByKey[disabledKey.Key] = disabledKey
	}

	for _, key := range allKeys {
		disabledKey, ok := disabledByKey[key]
		if !ok {
			return false
		}
		if disabledKey.DisabledUntil != nil {
			return false
		}
	}

	return true
}

func shouldRestoreChannelAfterAPIKeyEnable(ch *ent.Channel, disabledKeys []objects.DisabledAPIKey) bool {
	if ch == nil || ch.Status != channel.StatusDisabled || ch.ErrorMessage == nil {
		return false
	}
	if !strings.HasPrefix(*ch.ErrorMessage, allAPIKeysPermanentlyDisabledMessagePrefix) {
		return false
	}

	return len(ch.Credentials.GetEnabledAPIKeys(disabledKeys)) > 0
}

// EnableAPIKey 重新启用指定 key（从 disabled_api_keys 中移除）.
func (svc *ChannelService) EnableAPIKey(ctx context.Context, channelID int, key string) error {
	// 读取 channel
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	if len(ch.DisabledAPIKeys) == 0 {
		// 没有禁用的 key，忽略
		return nil
	}

	// 从 disabled_api_keys 中移除指定 key
	newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
	found := false

	for _, dk := range ch.DisabledAPIKeys {
		if dk.Key == key {
			found = true
			continue
		}

		newDisabledKeys = append(newDisabledKeys, dk)
	}

	if !found {
		// key 不在禁用列表中，忽略
		return nil
	}

	// 更新 channel
	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys)
	if shouldRestoreChannelAfterAPIKeyEnable(ch, newDisabledKeys) {
		update.SetStatus(channel.StatusEnabled)
		update.ClearErrorMessage()
	}

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to enable api key: %w", err)
	}

	fields := []log.Field{log.Int("channel_id", channelID)}
	identity := (&Channel{Channel: ch}).APIKeyIdentity(key)
	log.Info(ctx, "API key enabled", append(fields, apiKeyIdentityLogFields(identity)...)...)

	svc.asyncReloadChannels()

	return nil
}

// EnableAllAPIKeys 清空 disabled_api_keys.
func (svc *ChannelService) EnableAllAPIKeys(ctx context.Context, channelID int) error {
	// 读取 channel
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	if len(ch.DisabledAPIKeys) == 0 {
		// 没有禁用的 key，忽略
		return nil
	}

	// 更新 channel，清空 disabled_api_keys
	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{})
	if shouldRestoreChannelAfterAPIKeyEnable(ch, []objects.DisabledAPIKey{}) {
		update.SetStatus(channel.StatusEnabled)
		update.ClearErrorMessage()
	}

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to enable all api keys: %w", err)
	}

	log.Info(ctx, "All API keys enabled",
		log.Int("channel_id", channelID),
	)

	svc.asyncReloadChannels()

	return nil
}

// EnableSelectedAPIKeys re-enables multiple specific keys from disabled_api_keys.
func (svc *ChannelService) EnableSelectedAPIKeys(ctx context.Context, channelID int, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	if len(ch.DisabledAPIKeys) == 0 {
		return nil
	}

	keysToEnable := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keysToEnable[k] = struct{}{}
	}

	newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
	for _, dk := range ch.DisabledAPIKeys {
		if _, found := keysToEnable[dk.Key]; !found {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}

	if len(newDisabledKeys) == len(ch.DisabledAPIKeys) {
		return nil
	}

	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys)
	if shouldRestoreChannelAfterAPIKeyEnable(ch, newDisabledKeys) {
		update.SetStatus(channel.StatusEnabled)
		update.ClearErrorMessage()
	}

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to enable selected api keys: %w", err)
	}

	log.Info(ctx, "Selected API keys enabled",
		log.Int("channel_id", channelID),
		log.Int("count", len(keys)),
		log.Any("api_keys", (&Channel{Channel: ch}).APIKeyIdentities(keys)),
	)

	svc.asyncReloadChannels()

	return nil
}

// DeleteDisabledAPIKeysResult is the result of deleting disabled API keys.
type DeleteDisabledAPIKeysResult struct {
	Success bool
	Message string
}

// DeleteDisabledAPIKeys removes disabled API keys from both disabled_api_keys list and credentials.
// It ensures at least one API key remains and prevents deletion for OAuth channels.
func (svc *ChannelService) DeleteDisabledAPIKeys(ctx context.Context, channelID int, keys []string) (*DeleteDisabledAPIKeysResult, error) {
	if len(keys) == 0 {
		return &DeleteDisabledAPIKeysResult{Success: true}, nil
	}

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}

	// Check if channel uses OAuth - cannot delete keys for OAuth channels
	if ch.Credentials.IsOAuth() {
		return nil, fmt.Errorf("cannot delete API keys for OAuth channels")
	}

	keysToDelete := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keysToDelete[k] = struct{}{}
	}

	// Remove from disabled_api_keys
	newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
	for _, dk := range ch.DisabledAPIKeys {
		if _, found := keysToDelete[dk.Key]; !found {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}

	// Remove from credentials
	newCredentials := ch.Credentials
	if len(newCredentials.APIKeys) > 0 {
		filteredKeys := make([]string, 0, len(newCredentials.APIKeys))
		for _, k := range newCredentials.APIKeys {
			if _, found := keysToDelete[k]; !found {
				filteredKeys = append(filteredKeys, k)
			}
		}

		newCredentials.APIKeys = filteredKeys
	}

	if len(newCredentials.APIKeyConfigs) > 0 {
		filteredConfigs := make([]objects.ChannelAPIKeyConfig, 0, len(newCredentials.APIKeyConfigs))
		for _, config := range newCredentials.APIKeyConfigs {
			if _, found := keysToDelete[config.Key]; !found {
				filteredConfigs = append(filteredConfigs, config)
			}
		}

		newCredentials.APIKeyConfigs = filteredConfigs
	}

	if newCredentials.APIKey != "" {
		if _, found := keysToDelete[newCredentials.APIKey]; found {
			newCredentials.APIKey = ""
		}
	}

	// Ensure at least one API key remains
	allKeys := newCredentials.GetAllAPIKeys()
	if len(allKeys) == 0 {
		// Restore at least one key from the keys being deleted
		// Prefer the first key that was supposed to be deleted
		restoredKey := keys[0]
		if len(ch.Credentials.APIKeyConfigs) > 0 {
			restoredConfig := objects.ChannelAPIKeyConfig{Key: restoredKey, Weight: 100}
			for _, config := range ch.Credentials.APIKeyConfigs {
				if config.Key == restoredKey {
					restoredConfig = config
					break
				}
			}
			newCredentials.APIKeyConfigs = []objects.ChannelAPIKeyConfig{restoredConfig}
		} else {
			newCredentials.APIKeys = []string{restoredKey}
		}
	}

	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys).
		SetCredentials(newCredentials)

	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to delete disabled api keys: %w", err)
	}
	if !reflect.DeepEqual(ch.Credentials, newCredentials) {
		svc.invalidateProviderQuotaAfterCommit(ctx, channelID)
	}

	log.Info(ctx, "Disabled API keys deleted",
		log.Int("channel_id", channelID),
		log.Int("count", len(keys)),
		log.Any("api_keys", (&Channel{Channel: ch}).APIKeyIdentities(keys)),
	)

	// Check if we had to preserve a key
	result := &DeleteDisabledAPIKeysResult{Success: true}
	if len(allKeys) == 0 {
		result.Message = "ONE_KEY_PRESERVED"
	}

	svc.asyncReloadChannels()

	return result, nil
}
