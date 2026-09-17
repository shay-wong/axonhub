package biz

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aptible/supercronic/cronexpr"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
)

var compiledAPIKeyRuleRegexes sync.Map

type autoDisableScope string

const (
	autoDisableScopeChannel autoDisableScope = "channel"
	autoDisableScopeGlobal  autoDisableScope = "global"
)

func (svc *ChannelService) markChannelUnavailable(
	ctx context.Context,
	channelID int,
	responseStatusCode int,
	threshold int,
	actualCount int,
	expiresAt *time.Time,
	reason string,
) {
	ctx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	if reason == "" {
		reason = deriveErrorMessage(responseStatusCode)
	}

	// Only disable channels that are currently enabled to avoid repeated disabling
	// of the same channel under sustained error traffic, which would keep resetting
	// the cache debounce timer and prevent the cache from ever refreshing.
	update := svc.db.Channel.Update().
		Where(
			channel.ID(channelID),
			channel.StatusEQ(channel.StatusEnabled),
		).
		SetStatus(channel.StatusDisabled).
		SetErrorMessage(reason).
		SetAutoDisabledAt(time.Now())
	if expiresAt != nil {
		update.SetAutoDisableExpiresAt(*expiresAt)
	} else {
		update.ClearAutoDisableExpiresAt()
	}

	affected, err := update.Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to disable channel on unrecoverable error",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
			log.Cause(err),
		)

		return
	}

	if affected == 0 {
		log.Debug(ctx, "Channel already disabled, skipping",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
		)

		// Another instance may have already disabled the channel in DB while this
		// instance still serves it from a stale in-memory cache. Force a local
		// refresh so candidate selection stops using the channel immediately.
		if err := svc.enabledChannelsCache.Load(ctx, true); err != nil {
			log.Warn(ctx, "Failed to refresh local cache for already-disabled channel",
				log.Int("channel_id", channelID),
				log.Cause(err),
			)
		}

		return
	}

	log.Warn(ctx, "Channel disabled due to unrecoverable error",
		log.Int("channel_id", channelID),
		log.Int("error_code", responseStatusCode),
	)

	// Fetch the updated channel for webhook notification
	updatedChannel, err := svc.db.Channel.Get(ctx, channelID)
	if err != nil {
		log.Error(ctx, "Failed to fetch disabled channel for webhook notification",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	} else {
		svc.asyncNotifyChannelAutoDisabled(ctx, ChannelAutoDisabledEvent{
			ChannelID:       updatedChannel.ID,
			ChannelName:     updatedChannel.Name,
			ChannelProvider: updatedChannel.Type.String(),
			ChannelBaseURL:  updatedChannel.BaseURL,
			ChannelStatus:   updatedChannel.Status.String(),
			StatusCode:      responseStatusCode,
			Threshold:       threshold,
			ActualCount:     actualCount,
			Reason:          reason,
			OccurredAt:      time.Now(),
		})
	}

	// Synchronously reload the local cache to immediately stop selecting this channel.
	// This avoids the debounce delay that could keep the disabled channel in the candidate pool.
	if err := svc.enabledChannelsCache.Load(ctx, true); err != nil {
		log.Warn(ctx, "Failed to synchronously reload channels after auto-disable",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	}

	// Also notify other instances via the watcher for cross-instance cache invalidation.
	svc.asyncReloadChannels()
}

func (svc *ChannelService) asyncNotifyChannelAutoDisabled(ctx context.Context, event ChannelAutoDisabledEvent) {
	notifyCtx := context.WithoutCancel(ctx)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error(notifyCtx, "channel auto-disabled webhook notification panicked", log.Any("panic", r))
			}
		}()

		svc.WebhookNotifier.NotifyChannelAutoDisabled(notifyCtx, event)
	}()
}

func (svc *ChannelService) temporarilyDisableChannel(ctx context.Context, channelID int, duration time.Duration, responseStatusCode int, reason string) {
	if duration <= 0 {
		log.Warn(ctx, "temporary channel disable ignored because duration is not positive",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
		)

		return
	}

	ctx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	disabledUntil := time.Now().Add(duration)
	affected, err := svc.db.Channel.Update().
		Where(
			channel.ID(channelID),
			channel.StatusEQ(channel.StatusEnabled),
		).
		SetTemporaryDisabledUntil(disabledUntil).
		SetTemporaryDisabledErrorCode(responseStatusCode).
		SetTemporaryDisabledReason(reason).
		Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to temporarily disable channel",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
			log.Cause(err),
		)

		return
	}

	if affected == 0 {
		log.Debug(ctx, "Channel not eligible for temporary disable",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
		)
		return
	}

	log.Warn(ctx, "Channel temporarily disabled due to auto-disable rule",
		log.Int("channel_id", channelID),
		log.Int("error_code", responseStatusCode),
		log.Time("disabled_until", disabledUntil),
	)

	if err := svc.enabledChannelsCache.Load(ctx, true); err != nil {
		log.Warn(ctx, "Failed to synchronously reload channels after temporary auto-disable",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	}

	svc.asyncReloadChannels()
}

func (svc *ChannelService) ClearChannelTemporaryDisable(ctx context.Context, channelID int) (*ent.Channel, error) {
	ch, err := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		ClearTemporaryDisabledUntil().
		ClearTemporaryDisabledErrorCode().
		ClearTemporaryDisabledReason().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to clear channel temporary disable: %w", err)
	}

	reloadCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := svc.enabledChannelsCache.Load(reloadCtx, true); err != nil {
		log.Warn(ctx, "Failed to reload channels after clearing temporary disable",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	}
	svc.asyncReloadChannels()

	return ch, nil
}

// checkAndHandleChannelError checks if the channel should be disabled based on the error status code.
func (svc *ChannelService) checkAndHandleChannelError(ctx context.Context, perf *PerformanceRecord, policy *RetryPolicy) bool {
	policy = normalizedRetryPolicyForAutoDisable(policy)
	if policy == nil {
		return false
	}
	if !policy.ChannelAutoDisable.Enabled {
		return false
	}

	for _, statusConfig := range policy.ChannelAutoDisable.Statuses {
		if statusConfig.Status != perf.ResponseStatusCode {
			continue
		}

		svc.channelErrorCountsLock.Lock()

		if svc.channelErrorCounts[perf.ChannelID] == nil {
			svc.channelErrorCounts[perf.ChannelID] = make(map[int]int)
		}

		svc.channelErrorCounts[perf.ChannelID][perf.ResponseStatusCode]++
		count := svc.channelErrorCounts[perf.ChannelID][perf.ResponseStatusCode]
		svc.channelErrorCountsLock.Unlock()

		if count >= statusConfig.Times {
			switch statusConfig.Action {
			case DisableActionNone:
				svc.channelErrorCountsLock.Lock()
				delete(svc.channelErrorCounts, perf.ChannelID)
				svc.channelErrorCountsLock.Unlock()
			case DisableActionPermanent:
				svc.markChannelUnavailable(ctx, perf.ChannelID, perf.ResponseStatusCode, statusConfig.Times, count, nil, "")
				svc.channelErrorCountsLock.Lock()
				delete(svc.channelErrorCounts, perf.ChannelID)
				svc.channelErrorCountsLock.Unlock()
			case DisableActionTemporary:
				duration := resolveAutoDisableDuration(statusConfig, perf)
				reason := fmt.Sprintf("Auto-disabled temporarily after %d consecutive errors with status %d", count, perf.ResponseStatusCode)
				svc.temporarilyDisableChannel(ctx, perf.ChannelID, duration, perf.ResponseStatusCode, reason)
				svc.channelErrorCountsLock.Lock()
				delete(svc.channelErrorCounts, perf.ChannelID)
				svc.channelErrorCountsLock.Unlock()
			default:
				log.Warn(ctx, "Unknown channel auto-disable action",
					log.Int("channel_id", perf.ChannelID),
					log.Int("error_code", perf.ResponseStatusCode),
					log.String("action", statusConfig.Action),
				)
				return false
			}

			return true
		}
	}

	return false
}

func apiKeyAutoDisableMatchesStatus(policy *RetryPolicy, statusCode int) bool {
	policy = normalizedRetryPolicyForAutoDisable(policy)
	if policy == nil || !policy.APIKeyAutoDisable.Enabled {
		return false
	}

	for _, statusConfig := range policy.APIKeyAutoDisable.Statuses {
		if statusConfig.Status == statusCode {
			return true
		}
	}

	return false
}

// checkAndHandleAPIKeyError checks if the API key should be disabled based on the error status code.
// Returns true if the API key was disabled.
func (svc *ChannelService) checkAndHandleAPIKeyError(ctx context.Context, perf *PerformanceRecord, policy *RetryPolicy) bool {
	policy = normalizedRetryPolicyForAutoDisable(policy)
	if policy == nil {
		return false
	}
	if !policy.APIKeyAutoDisable.Enabled {
		return false
	}

	for _, statusConfig := range policy.APIKeyAutoDisable.Statuses {
		if statusConfig.Status != perf.ResponseStatusCode {
			continue
		}

		svc.apiKeyErrorCountsLock.Lock()

		if svc.apiKeyErrorCounts[perf.ChannelID] == nil {
			svc.apiKeyErrorCounts[perf.ChannelID] = make(map[string]map[int]int)
		}

		if svc.apiKeyErrorCounts[perf.ChannelID][perf.APIKey] == nil {
			svc.apiKeyErrorCounts[perf.ChannelID][perf.APIKey] = make(map[int]int)
		}

		svc.apiKeyErrorCounts[perf.ChannelID][perf.APIKey][perf.ResponseStatusCode]++
		count := svc.apiKeyErrorCounts[perf.ChannelID][perf.APIKey][perf.ResponseStatusCode]
		svc.apiKeyErrorCountsLock.Unlock()

		if count >= statusConfig.Times {
			reason := fmt.Sprintf("Auto-disabled after %d consecutive errors with status %d", count, perf.ResponseStatusCode)
			identityFields := apiKeyIdentityLogFields(ChannelAPIKeyIdentity{
				Name:   perf.APIKeyName,
				Suffix: perf.APIKeySuffix,
			})
			switch statusConfig.Action {
			case DisableActionNone:
			case DisableActionPermanent:
				if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason); err != nil {
					fields := []log.Field{
						log.Int("channel_id", perf.ChannelID),
						log.Int("error_code", perf.ResponseStatusCode),
						log.Cause(err),
					}
					log.Error(ctx, "Failed to disable API key", append(fields, identityFields...)...)

					return false
				}
			case DisableActionTemporary:
				duration := resolveAutoDisableDuration(statusConfig, perf)
				if err := svc.ApplyAPIKeyDisableAction(ctx, perf.ChannelID, perf.APIKey, DisableActionTemporary, &duration, perf.ResponseStatusCode, reason); err != nil {
					fields := []log.Field{
						log.Int("channel_id", perf.ChannelID),
						log.Int("error_code", perf.ResponseStatusCode),
						log.Cause(err),
					}
					log.Error(ctx, "Failed to temporarily disable API key", append(fields, identityFields...)...)

					return false
				}
			default:
				fields := []log.Field{
					log.Int("channel_id", perf.ChannelID),
					log.Int("error_code", perf.ResponseStatusCode),
					log.String("action", statusConfig.Action),
				}
				log.Warn(ctx, "Unknown API key auto-disable action", append(fields, identityFields...)...)
				return false
			}

			svc.apiKeyErrorCountsLock.Lock()
			delete(svc.apiKeyErrorCounts[perf.ChannelID], perf.APIKey)
			svc.apiKeyErrorCountsLock.Unlock()

			return true
		}
	}

	return false
}

func resolveAutoDisableDuration(rule AutoDisableStatusRule, perf *PerformanceRecord) time.Duration {
	if rule.UseRetryAfter != nil && *rule.UseRetryAfter && perf != nil && perf.RetryAfterDuration != nil && *perf.RetryAfterDuration > 0 {
		return *perf.RetryAfterDuration
	}

	if rule.DurationMinutes != nil && *rule.DurationMinutes > 0 {
		return time.Duration(*rule.DurationMinutes) * time.Minute
	}

	return time.Duration(defaultAutoDisableFallbackDurationMinutes) * time.Minute
}

func normalizedRetryPolicyForAutoDisable(policy *RetryPolicy) *RetryPolicy {
	if policy == nil {
		return nil
	}

	normalized := *policy
	normalized.AutoDisableChannel = cloneAutoDisablePolicy(policy.AutoDisableChannel)
	normalized.ChannelAutoDisable = cloneAutoDisablePolicy(policy.ChannelAutoDisable)
	normalized.APIKeyAutoDisable = cloneAutoDisablePolicy(policy.APIKeyAutoDisable)
	normalizeRetryPolicy(&normalized)

	return &normalized
}

// EvaluateAPIKeyRulesForFailure evaluates auto-disable rules for a failure that
// was persisted outside the normal performance middleware path. Empty API keys
// stay on RecordPerformance; this bypass exists only for keyed failures.
func (svc *ChannelService) EvaluateAPIKeyRulesForFailure(
	ctx context.Context,
	channelID int,
	apiKey string,
	responseStatusCode int,
	errorMessage string,
) bool {
	if channelID == 0 || apiKey == "" {
		return false
	}

	_, acted := svc.evaluateAutoDisableForFailure(ctx, &PerformanceRecord{
		ChannelID:          channelID,
		APIKey:             apiKey,
		ResponseStatusCode: responseStatusCode,
		ErrorMessage:       errorMessage,
	})
	return acted
}

func (svc *ChannelService) evaluateAutoDisableForFailure(ctx context.Context, perf *PerformanceRecord) (matched, acted bool) {
	if perf == nil || perf.SkipHealthStateTracking {
		return false, false
	}
	ch := svc.GetEnabledChannel(perf.ChannelID)
	if ch == nil {
		return false, false
	}

	switch ch.Policies.EffectiveAutoDisableMode() {
	case objects.APIKeyAutoDisableModeOff:
		return false, false
	case objects.APIKeyAutoDisableModeCustom:
		if !perf.TransportFailure {
			matched, acted = svc.evaluateRules(ctx, perf, ch.Policies.APIKeyAutoDisableRules, autoDisableScopeChannel)
		}
		if matched {
			return matched, acted
		}
	}

	policy := svc.SystemService.RetryPolicyOrDefault(ctx)
	if !perf.TransportFailure && policy.AutoDisableChannel.Enabled {
		matched, acted = svc.evaluateRules(ctx, perf, policy.AutoDisableChannel.Rules, autoDisableScopeGlobal)
		if matched {
			return matched, acted
		}
	}

	// A matching credential policy owns the failure, even below its threshold.
	if perf.APIKey != "" && !perf.TransportFailure && apiKeyAutoDisableMatchesStatus(policy, perf.ResponseStatusCode) {
		return true, svc.checkAndHandleAPIKeyError(ctx, perf, policy)
	}
	return false, svc.checkAndHandleChannelError(ctx, perf, policy)
}

// checkAndHandleChannelAPIKeyRules evaluates the channel's own rules only. Tests
// and internal callers that already know they want the channel layer use this.
func (svc *ChannelService) checkAndHandleChannelAPIKeyRules(ctx context.Context, perf *PerformanceRecord) (matched, acted bool) {
	if perf == nil || perf.APIKey == "" || perf.TransportFailure || perf.SkipHealthStateTracking {
		return false, false
	}
	ch := svc.GetEnabledChannel(perf.ChannelID)
	if ch == nil {
		return false, false
	}

	return svc.evaluateRules(ctx, perf, ch.Policies.APIKeyAutoDisableRules, autoDisableScopeChannel)
}

// evaluateRules finds the first matching rule and increments that rule's
// independent counter. Other rules and unmatched failures leave counts alone.
func (svc *ChannelService) evaluateRules(
	ctx context.Context,
	perf *PerformanceRecord,
	rules []objects.APIKeyAutoDisableRule,
	scope autoDisableScope,
) (matched, acted bool) {
	if len(rules) == 0 {
		return false, false
	}

	for ruleIndex, rule := range rules {
		if !matchesAPIKeyRule(rule, perf) {
			continue
		}

		ruleKey := apiKeyRuleCounterKey(autoDisableCounterIdentity(perf), scope, ruleIndex, rule)
		// Every failure accepted by one rule contributes to that rule's single
		// consecutive counter, including alternating configured status codes.
		const countKey = 0

		svc.apiKeyErrorCountsLock.Lock()
		if svc.apiKeyErrorCounts[perf.ChannelID] == nil {
			svc.apiKeyErrorCounts[perf.ChannelID] = make(map[string]map[int]int)
		}
		if svc.apiKeyRuleActionsInFlight == nil {
			svc.apiKeyRuleActionsInFlight = make(map[int]map[string]bool)
		}
		if svc.apiKeyRuleActionsInFlight[perf.ChannelID] == nil {
			svc.apiKeyRuleActionsInFlight[perf.ChannelID] = make(map[string]bool)
		}
		if svc.apiKeyErrorCounts[perf.ChannelID][ruleKey] == nil {
			svc.apiKeyErrorCounts[perf.ChannelID][ruleKey] = make(map[int]int)
		}
		svc.apiKeyErrorCounts[perf.ChannelID][ruleKey][countKey]++
		count := svc.apiKeyErrorCounts[perf.ChannelID][ruleKey][countKey]
		threshold := max(rule.Times, 1)
		_, actionInFlight := svc.apiKeyRuleActionsInFlight[perf.ChannelID][ruleKey]
		shouldAct := count >= threshold && !actionInFlight
		if shouldAct {
			svc.apiKeyErrorCounts[perf.ChannelID][ruleKey][countKey] -= threshold
			svc.apiKeyRuleActionsInFlight[perf.ChannelID][ruleKey] = false
		}
		svc.apiKeyErrorCountsLock.Unlock()

		if shouldAct {
			actionSucceeded := svc.executeMatchedRuleAction(ctx, perf, rule, count)
			svc.apiKeyErrorCountsLock.Lock()
			if streakReset, stillClaimed := svc.apiKeyRuleActionsInFlight[perf.ChannelID][ruleKey]; stillClaimed {
				delete(svc.apiKeyRuleActionsInFlight[perf.ChannelID], ruleKey)
				if !actionSucceeded && !streakReset {
					if svc.apiKeyErrorCounts[perf.ChannelID] == nil {
						svc.apiKeyErrorCounts[perf.ChannelID] = make(map[string]map[int]int)
					}
					if svc.apiKeyErrorCounts[perf.ChannelID][ruleKey] == nil {
						svc.apiKeyErrorCounts[perf.ChannelID][ruleKey] = make(map[int]int)
					}
					svc.apiKeyErrorCounts[perf.ChannelID][ruleKey][countKey] += threshold
				}
				if svc.apiKeyErrorCounts[perf.ChannelID][ruleKey][countKey] == 0 {
					delete(svc.apiKeyErrorCounts[perf.ChannelID], ruleKey)
				}
			}
			svc.apiKeyErrorCountsLock.Unlock()
			return true, actionSucceeded
		}

		return true, false
	}

	return false, false
}

func (svc *ChannelService) clearAutoDisableCountsOnSuccess(perf *PerformanceRecord) {
	svc.channelErrorCountsLock.Lock()
	delete(svc.channelErrorCounts, perf.ChannelID)
	svc.channelErrorCountsLock.Unlock()

	svc.apiKeyErrorCountsLock.Lock()
	defer svc.apiKeyErrorCountsLock.Unlock()

	identity := autoDisableCounterIdentity(perf)
	prefix := identity + ":"
	for key := range svc.apiKeyErrorCounts[perf.ChannelID] {
		if key == identity || strings.HasPrefix(key, prefix) {
			delete(svc.apiKeyErrorCounts[perf.ChannelID], key)
		}
	}
	for key := range svc.apiKeyRuleActionsInFlight[perf.ChannelID] {
		if key == identity || strings.HasPrefix(key, prefix) {
			svc.apiKeyRuleActionsInFlight[perf.ChannelID][key] = true
		}
	}
}

func autoDisableCounterIdentity(perf *PerformanceRecord) string {
	if perf.APIKey != "" {
		return perf.APIKey
	}

	return fmt.Sprintf("channel:%d", perf.ChannelID)
}

func apiKeyRuleCounterKey(identity string, scope autoDisableScope, ruleIndex int, rule objects.APIKeyAutoDisableRule) string {
	disableDurationMinutes := 0
	if rule.DisableDurationMinutes != nil {
		disableDurationMinutes = *rule.DisableDurationMinutes
	}

	return fmt.Sprintf(
		"%s:%s:rule:%d:%v:%v:%d:%s:%d:%s:%s",
		identity,
		scope,
		ruleIndex,
		rule.StatusCodes,
		rule.KeywordPatterns,
		rule.Times,
		rule.Action,
		disableDurationMinutes,
		rule.DisableUntilCron,
		rule.DisableUntilTimezone,
	)
}

func matchesAPIKeyRule(rule objects.APIKeyAutoDisableRule, perf *PerformanceRecord) bool {
	if len(rule.StatusCodes) == 0 && len(rule.KeywordPatterns) == 0 {
		return false
	}
	if len(rule.StatusCodes) > 0 && !slices.Contains(rule.StatusCodes, perf.ResponseStatusCode) {
		return false
	}
	if len(rule.KeywordPatterns) == 0 {
		return true
	}
	if perf.ErrorMessage == "" {
		return false
	}

	lowerMessage := strings.ToLower(perf.ErrorMessage)
	for _, pattern := range rule.KeywordPatterns {
		if re := compiledAPIKeyRuleRegex(pattern); re != nil {
			if re.MatchString(perf.ErrorMessage) {
				return true
			}
			continue
		}
		// Patterns may be plain keywords or regular expressions. Treat syntax
		// that is not a valid expression as a case-insensitive literal keyword.
		if strings.Contains(lowerMessage, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

func compiledAPIKeyRuleRegex(pattern string) *regexp.Regexp {
	cacheKey := "(?i)" + pattern
	if cached, ok := compiledAPIKeyRuleRegexes.Load(cacheKey); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}

	re, err := regexp.Compile(cacheKey)
	if err != nil {
		compiledAPIKeyRuleRegexes.Store(cacheKey, (*regexp.Regexp)(nil))
		return nil
	}
	compiledAPIKeyRuleRegexes.Store(cacheKey, re)
	return re
}

// nextAPIKeyRuleCronOccurrence resolves when a disable_until_cron rule lets the
// credential back in: the first cron occurrence strictly after the failure. The
// absolute instant is stored on the disable record, so recovery does not depend
// on any instance re-evaluating the expression later.
func nextAPIKeyRuleCronOccurrence(rule objects.APIKeyAutoDisableRule, now time.Time) (time.Time, error) {
	loc := time.UTC

	if rule.DisableUntilTimezone != "" {
		parsed, err := time.LoadLocation(rule.DisableUntilTimezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid timezone %q: %w", rule.DisableUntilTimezone, err)
		}

		loc = parsed
	}

	expr, err := cronexpr.Parse(rule.DisableUntilCron)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", rule.DisableUntilCron, err)
	}

	next := expr.Next(now.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron expression %q never fires again", rule.DisableUntilCron)
	}

	return next, nil
}

func (svc *ChannelService) executeMatchedRuleAction(
	ctx context.Context,
	perf *PerformanceRecord,
	rule objects.APIKeyAutoDisableRule,
	count int,
) bool {
	if perf.APIKey == "" {
		return svc.executeChannelRuleAction(ctx, perf, rule, count)
	}

	return svc.executeAPIKeyRuleAction(ctx, perf, rule, count)
}

func (svc *ChannelService) executeAPIKeyRuleAction(
	ctx context.Context,
	perf *PerformanceRecord,
	rule objects.APIKeyAutoDisableRule,
	count int,
) bool {
	reason := fmt.Sprintf("Disabled by auto-disable rule after %d consecutive errors", count)

	action := rule.Action

	// An OAuth channel's credential cannot be deleted: DeleteDisabledAPIKeys
	// rejects OAuth channels outright, so running the delete step would report the
	// action as failed, restore the error counter and retry on every subsequent
	// failure. Fall back to keeping it disabled, which is the same observable
	// outcome for a channel holding a single credential.
	if action == objects.APIKeyAutoDisableActionPermanentDelete && perf.APIKey == objects.OAuthCredentialRef {
		action = objects.APIKeyAutoDisableActionPermanent
	}

	switch action {
	case objects.APIKeyAutoDisableActionUntilCron:
		expiresAt, err := nextAPIKeyRuleCronOccurrence(rule, time.Now())
		if err != nil {
			log.Error(ctx, "Failed to resolve api key rule cron schedule",
				log.Int("channel_id", perf.ChannelID),
				log.String("cron", rule.DisableUntilCron),
				log.Cause(err),
			)

			return false
		}

		reason = fmt.Sprintf("Disabled until %s by auto-disable rule after %d consecutive errors",
			expiresAt.Format(time.RFC3339), count)

		if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason, &expiresAt); err != nil {
			log.Error(ctx, "Failed to disable API key until cron occurrence",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)

			return false
		}

		return true

	case objects.APIKeyAutoDisableActionPermanent:
		// No expiry, so the cleanup task never revives it: the credential stays on
		// the channel until an operator re-enables it.
		if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason); err != nil {
			log.Error(ctx, "Failed to permanently disable API key by auto-disable rule",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)

			return false
		}

		return true

	case objects.APIKeyAutoDisableActionPermanentDelete:
		if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason); err != nil {
			log.Error(ctx, "Failed to permanently disable API key by auto-disable rule",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)

			return false
		}

		result, err := svc.DeleteDisabledAPIKeys(ctx, perf.ChannelID, []string{perf.APIKey})
		if err != nil {
			log.Error(ctx, "Failed to delete API key disabled by auto-disable rule",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)

			return false
		}

		// Channels must retain at least one credential. Keep that last key
		// permanently disabled when deletion cannot remove it, otherwise the
		// delete helper would make the rule a no-op by re-enabling the channel.
		if result.Message == "ONE_KEY_PRESERVED" {
			if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason); err != nil {
				log.Error(ctx, "Failed to keep preserved API key disabled by auto-disable rule",
					log.Int("channel_id", perf.ChannelID),
					log.Cause(err),
				)

				return false
			}
		}

		return true
	}

	if rule.DisableDurationMinutes == nil {
		return false
	}
	duration := time.Duration(*rule.DisableDurationMinutes) * time.Minute
	reason = fmt.Sprintf("Temporarily disabled for %d minutes by auto-disable rule after %d consecutive errors", *rule.DisableDurationMinutes, count)

	if err := svc.ApplyAPIKeyDisableAction(
		ctx,
		perf.ChannelID,
		perf.APIKey,
		DisableActionTemporary,
		&duration,
		perf.ResponseStatusCode,
		reason,
	); err != nil {
		log.Error(ctx, "Failed to temporarily disable API key by auto-disable rule",
			log.Int("channel_id", perf.ChannelID),
			log.Cause(err),
		)
		return false
	}

	return true
}

func (svc *ChannelService) executeChannelRuleAction(
	ctx context.Context,
	perf *PerformanceRecord,
	rule objects.APIKeyAutoDisableRule,
	count int,
) bool {
	reason := fmt.Sprintf("Disabled by auto-disable rule after %d consecutive errors", count)
	action := rule.Action
	if action == objects.APIKeyAutoDisableActionPermanentDelete {
		log.Warn(ctx, "permanent_disable_delete degraded to permanent_disable for keyless channel",
			log.Int("channel_id", perf.ChannelID),
		)
		action = objects.APIKeyAutoDisableActionPermanent
	}

	var expiresAt *time.Time
	switch action {
	case objects.APIKeyAutoDisableActionUntilCron:
		next, err := nextAPIKeyRuleCronOccurrence(rule, time.Now())
		if err != nil {
			log.Error(ctx, "Failed to resolve channel auto-disable cron schedule",
				log.Int("channel_id", perf.ChannelID),
				log.String("cron", rule.DisableUntilCron),
				log.Cause(err),
			)
			return false
		}
		expiresAt = &next
		reason = fmt.Sprintf("Disabled until %s by auto-disable rule after %d consecutive errors",
			next.Format(time.RFC3339), count)
	case objects.APIKeyAutoDisableActionTemporary:
		if rule.DisableDurationMinutes != nil {
			disabledUntil := time.Now().Add(time.Duration(*rule.DisableDurationMinutes) * time.Minute)
			expiresAt = &disabledUntil
			reason = fmt.Sprintf("Temporarily disabled for %d minutes by auto-disable rule after %d consecutive errors", *rule.DisableDurationMinutes, count)
		}
	}

	svc.markChannelUnavailable(ctx, perf.ChannelID, perf.ResponseStatusCode, max(rule.Times, 1), count, expiresAt, reason)
	return true
}
