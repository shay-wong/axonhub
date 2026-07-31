package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func (svc *BackupService) Restore(ctx context.Context, data []byte, opts RestoreOptions) error {
	user, ok := contexts.GetUser(ctx)
	if !ok || user == nil {
		return fmt.Errorf("user not found in context")
	}

	if !user.IsOwner {
		return fmt.Errorf("only owners can perform restore operations")
	}

	var backupData BackupData
	if err := json.Unmarshal(data, &backupData); err != nil {
		return err
	}

	if !lo.Contains([]string{BackupVersion, BackupVersionV5, BackupVersionV4, BackupVersionV3, BackupVersionV2, BackupVersionV1}, backupData.Version) {
		log.Warn(ctx, "backup version mismatch",
			log.String("expected", BackupVersion),
			log.String("got", backupData.Version))

		return fmt.Errorf("backup version mismatch: expected %s, got %s", BackupVersion, backupData.Version)
	}

	tx, err := svc.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	txClient := tx.Client()

	if err := svc.restore(ctx, txClient, backupData, opts); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	committed = true

	if opts.IncludeSystemConfigs {
		svc.systemService.InvalidateSystemValueCaches(ctx, systemConfigBackupKeys...)
	}
	if opts.IncludeChannels {
		svc.invalidateRestoredChannelQuotas(ctx, backupData.Channels)
	}

	return nil
}

func (svc *BackupService) invalidateRestoredChannelQuotas(ctx context.Context, backupChannels []*BackupChannel) {
	if svc.providerQuotaInvalidator == nil {
		return
	}

	names := make([]string, 0, len(backupChannels))
	seen := make(map[string]struct{}, len(backupChannels))
	for _, backupChannel := range backupChannels {
		if backupChannel == nil || backupChannel.Name == "" {
			continue
		}
		if _, ok := seen[backupChannel.Name]; ok {
			continue
		}
		seen[backupChannel.Name] = struct{}{}
		names = append(names, backupChannel.Name)
	}
	if len(names) == 0 {
		return
	}

	restoredChannels, err := svc.db.Channel.Query().Where(channel.NameIn(names...)).All(ctx)
	if err != nil {
		log.Warn(ctx, "failed to load restored channels for provider quota invalidation", log.Cause(err))
		return
	}
	for _, restoredChannel := range restoredChannels {
		if err := svc.providerQuotaInvalidator.InvalidateChannelQuota(ctx, restoredChannel.ID); err != nil {
			log.Warn(ctx, "failed to invalidate provider quota after channel restore",
				log.Int("channel_id", restoredChannel.ID),
				log.Cause(err))
		}
	}
}

func (svc *BackupService) restore(ctx context.Context, db *ent.Client, backupData BackupData, opts RestoreOptions) error {
	if opts.IncludeChannels {
		if err := svc.restoreChannels(ctx, db, backupData.Channels, opts); err != nil {
			return err
		}
	}

	channelIDMap, err := svc.buildChannelIDMap(ctx, db, backupData.Channels)
	if err != nil {
		return err
	}

	if opts.IncludeSystemConfigs {
		if err := svc.restoreSystemConfigs(ctx, db, backupData.SystemConfigs, channelIDMap, opts.IncludeAPIKeys); err != nil {
			return err
		}
	}

	if opts.IncludeModelPrices {
		if err := svc.restoreChannelModelPrices(ctx, db, backupData.ChannelModelPrices, backupData.Version, opts); err != nil {
			return err
		}
	}

	if opts.IncludeModels {
		if err := svc.restoreModels(ctx, db, backupData.Models, opts, channelIDMap); err != nil {
			return err
		}
	}

	if opts.IncludeProjects {
		for _, projData := range backupData.Projects {
			if projData == nil {
				continue
			}

			remapProjectProfilesChannelIDs(projData.Profiles, channelIDMap)
		}

		if err := svc.restoreProjects(ctx, db, backupData.Projects, opts); err != nil {
			return err
		}
	}

	if opts.IncludeAPIKeys {
		if err := svc.restoreAPIKeys(ctx, db, backupData.APIKeys, opts, channelIDMap); err != nil {
			return err
		}
	}

	if opts.IncludeUsageStats || opts.IncludeRequestLogs {
		if err := svc.restoreUsageData(ctx, db, backupData.UsageRequests, backupData.RequestExecutions, backupData.UsageLogs, opts); err != nil {
			return err
		}
	}

	return nil
}

func (svc *BackupService) restoreSystemConfigs(ctx context.Context, db *ent.Client, configs []*BackupSystemConfig, channelIDMap map[int]int, includeSecrets bool) error {
	ctx = ent.NewContext(ctx, db)
	systemService := biz.NewSystemService(biz.SystemServiceParams{
		Ent:         db,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})
	hasStoragePolicy := lo.ContainsBy(configs, func(config *BackupSystemConfig) bool {
		return config != nil && config.Key == biz.SystemKeyStoragePolicy
	})

	for _, config := range configs {
		if config == nil || !lo.Contains(systemConfigBackupKeys, config.Key) {
			continue
		}
		if isSecretBearingSystemConfig(config.Key) && !includeSecrets {
			continue
		}

		if err := restoreSystemConfig(ctx, systemService, config.Key, config.Value, channelIDMap, hasStoragePolicy); err != nil {
			return fmt.Errorf("failed to restore system configuration %q: %w", config.Key, err)
		}
	}

	return nil
}

func restoreSystemConfig(ctx context.Context, svc *biz.SystemService, key, value string, channelIDMap map[int]int, hasStoragePolicy bool) error {
	switch key {
	case biz.SystemKeyBrandName:
		return svc.SetBrandName(ctx, value)
	case biz.SystemKeyBrandLogo:
		return svc.SetBrandLogo(ctx, value)
	case biz.SystemKeyTitle:
		return svc.SetTitle(ctx, value)
	case biz.SystemKeyStoreChunks:
		if hasStoragePolicy {
			return nil
		}
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value: %w", err)
		}
		policy, err := svc.StoragePolicy(ctx)
		if err != nil {
			return err
		}
		policy.StoreChunks = enabled
		return svc.SetStoragePolicy(ctx, policy)
	case biz.SystemKeyStoragePolicy:
		settings, err := decodeSystemConfig[biz.StoragePolicy](value)
		if err != nil {
			return err
		}
		return svc.SetStoragePolicy(ctx, &settings)
	case biz.SystemKeyRetryPolicy:
		settings, err := decodeSystemConfig[biz.RetryPolicy](value)
		if err != nil {
			return err
		}
		return svc.SetRetryPolicy(ctx, &settings)
	case biz.SystemKeyWebhookNotifierConfig:
		settings, err := decodeSystemConfig[biz.WebhookNotifierConfig](value)
		if err != nil {
			return err
		}
		return svc.SetWebhookNotifierConfig(ctx, &settings)
	case biz.SystemKeyModelSettings:
		settings, err := decodeSystemConfig[biz.SystemModelSettings](value)
		if err != nil {
			return err
		}
		for _, developer := range settings.DeveloperSettings {
			if developer != nil {
				modelSettings := &objects.ModelSettings{Associations: developer.Associations}
				remapModelSettingsChannelIDs(modelSettings, channelIDMap)
				developer.Associations = modelSettings.Associations
			}
		}
		return svc.SetModelSettings(ctx, settings)
	case biz.SystemKeyChannelSettings:
		settings, err := decodeSystemConfig[biz.SystemChannelSettings](value)
		if err != nil {
			return err
		}
		return svc.SetChannelSetting(ctx, settings)
	case biz.SystemKeyGeneralSettings:
		settings, err := decodeSystemConfig[biz.SystemGeneralSettings](value)
		if err != nil {
			return err
		}
		return svc.SetGeneralSettings(ctx, settings)
	case biz.SystemKeyUserAgentPassThrough:
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value: %w", err)
		}
		return svc.SetUserAgentPassThrough(ctx, enabled)
	case biz.SystemKeyPassThrough:
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value: %w", err)
		}
		return svc.SetPassThrough(ctx, enabled)
	case biz.SystemKeyQuotaEnforcementSettings:
		settings, err := decodeSystemConfig[biz.QuotaEnforcementSettings](value)
		if err != nil {
			return err
		}
		return svc.SetQuotaEnforcementSettings(ctx, settings)
	case biz.SystemKeyProviderQuotaCollectionSettings:
		settings, err := decodeSystemConfig[biz.ProviderQuotaCollectionSettings](value)
		if err != nil {
			return err
		}
		supported := biz.SupportedProviderQuotaTypes()
		for provider := range settings.Providers {
			if !lo.Contains(supported, provider) {
				return fmt.Errorf("unsupported provider quota type: %q", provider)
			}
		}
		providers := lo.Map(supported, func(provider string, _ int) biz.ProviderQuotaCollectionProvider {
			enabled, ok := settings.Providers[provider]
			return biz.ProviderQuotaCollectionProvider{Provider: provider, Enabled: !ok || enabled}
		})
		return svc.UpdateProviderQuotaCollectionSettings(ctx, &settings.Enabled, providers)
	case biz.SystemKeySecuritySettings:
		settings, err := decodeSystemConfig[biz.SecuritySettings](value)
		if err != nil {
			return err
		}
		return svc.SetSecuritySettings(ctx, settings)
	case biz.SystemKeyProxyPresets:
		presets, err := decodeSystemConfig[[]biz.ProxyPreset](value)
		if err != nil {
			return err
		}
		return svc.SetProxyPresets(ctx, presets)
	default:
		return nil
	}
}

func decodeSystemConfig[T any](value string) (T, error) {
	var config T
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return config, fmt.Errorf("invalid JSON value: %w", err)
	}
	return config, nil
}

func (svc *BackupService) buildChannelIDMap(ctx context.Context, db *ent.Client, channels []*BackupChannel) (map[int]int, error) {
	idMap := map[int]int{}
	if len(channels) == 0 {
		return idMap, nil
	}

	// Collect all channel names and create a map from name to old ID
	nameToOldID := make(map[string]int)
	names := make([]string, 0, len(channels))

	for _, chData := range channels {
		if chData == nil {
			continue
		}

		oldID := chData.ID
		if oldID == 0 || chData.Name == "" {
			continue
		}

		nameToOldID[chData.Name] = oldID
		names = append(names, chData.Name)
	}

	if len(names) == 0 {
		return idMap, nil
	}

	// Batch query all channels by names, only select needed fields (id, name)
	existingChannels, err := db.Channel.Query().
		Where(channel.NameIn(names...)).
		Select(channel.FieldID, channel.FieldName).
		All(ctx)
	if err != nil {
		return nil, err
	}

	// Build the ID mapping
	for _, ch := range existingChannels {
		if oldID, ok := nameToOldID[ch.Name]; ok {
			idMap[oldID] = ch.ID
		}
	}

	return idMap, nil
}

type usageRestoreResolver struct {
	projectNames           map[string]int
	projectIDs             map[int]struct{}
	channelNames           map[string]int
	channelGenerations     map[int]usageBackupChannelIdentity
	dataStorageGenerations map[int]usageBackupDataStorageIdentity
	apiKeyKeys             map[string]int
}

func newUsageRestoreResolver(ctx context.Context, db *ent.Client) (*usageRestoreResolver, error) {
	projects, err := db.Project.Query().
		Select(project.FieldID, project.FieldName).
		All(ctx)
	if err != nil {
		return nil, err
	}

	channels, err := db.Channel.Query().
		Select(channel.FieldID, channel.FieldName).
		All(ctx)
	if err != nil {
		return nil, err
	}
	allChannels, err := db.Channel.Query().
		Select(channel.FieldID, channel.FieldName, channel.FieldDeletedAt).
		All(schematype.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}

	allDataStorages, err := db.DataStorage.Query().
		Select(datastorage.FieldID, datastorage.FieldName, datastorage.FieldDeletedAt).
		All(schematype.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}

	apiKeys, err := db.APIKey.Query().
		Select(apikey.FieldID, apikey.FieldKey).
		All(ctx)
	if err != nil {
		return nil, err
	}

	resolver := &usageRestoreResolver{
		projectNames:           make(map[string]int, len(projects)),
		projectIDs:             make(map[int]struct{}, len(projects)),
		channelNames:           make(map[string]int, len(channels)),
		channelGenerations:     make(map[int]usageBackupChannelIdentity, len(allChannels)),
		dataStorageGenerations: make(map[int]usageBackupDataStorageIdentity, len(allDataStorages)),
		apiKeyKeys:             make(map[string]int, len(apiKeys)),
	}

	for _, proj := range projects {
		resolver.projectIDs[proj.ID] = struct{}{}
		resolver.projectNames[proj.Name] = proj.ID
	}

	for _, ch := range channels {
		resolver.channelNames[ch.Name] = ch.ID
	}
	for _, ch := range allChannels {
		resolver.channelGenerations[ch.ID] = usageBackupChannelIdentity{
			name:      ch.Name,
			deletedAt: ch.DeletedAt,
		}
	}

	for _, storage := range allDataStorages {
		resolver.dataStorageGenerations[storage.ID] = usageBackupDataStorageIdentity{
			name:      storage.Name,
			deletedAt: storage.DeletedAt,
		}
	}

	for _, ak := range apiKeys {
		resolver.apiKeyKeys[ak.Key] = ak.ID
	}

	return resolver, nil
}

func (r *usageRestoreResolver) resolveProjectID(projectID int, projectName string) (int, bool) {
	if projectName != "" {
		id, ok := r.projectNames[projectName]
		return id, ok
	}

	if projectID == 0 {
		return 0, false
	}

	_, ok := r.projectIDs[projectID]
	return projectID, ok
}

func (r *usageRestoreResolver) resolveChannelID(_ int, channelName string, channelDeletedAt int) (int, bool) {
	if channelName == "" || channelDeletedAt != 0 {
		return 0, false
	}

	id, ok := r.channelNames[channelName]
	return id, ok
}

func (r *usageRestoreResolver) matchesChannelGeneration(channelID int, channelName string, channelDeletedAt int) bool {
	if channelID == 0 || channelName == "" {
		return false
	}

	generation, ok := r.channelGenerations[channelID]
	return ok && generation.name == channelName && generation.deletedAt == channelDeletedAt
}

func (r *usageRestoreResolver) matchesDataStorageGeneration(dataStorageID int, dataStorageName string, dataStorageDeletedAt int) bool {
	if dataStorageID == 0 || dataStorageName == "" {
		return false
	}

	generation, ok := r.dataStorageGenerations[dataStorageID]
	return ok && generation.name == dataStorageName && generation.deletedAt == dataStorageDeletedAt
}

func (r *usageRestoreResolver) resolveAPIKeyID(apiKeyKey string) (int, bool) {
	if apiKeyKey != "" {
		id, ok := r.apiKeyKeys[apiKeyKey]
		return id, ok
	}

	return 0, false
}

func remapModelSettingsChannelIDs(settings *objects.ModelSettings, channelIDMap map[int]int) {
	if settings == nil {
		return
	}

	settings.Associations = slices.DeleteFunc(settings.Associations, func(assoc *objects.ModelAssociation) bool {
		if assoc == nil {
			return true
		}

		if assoc.ChannelModel != nil {
			newID, ok := channelIDMap[assoc.ChannelModel.ChannelID]
			if !ok {
				return true
			}
			assoc.ChannelModel.ChannelID = newID
		}

		if assoc.ChannelRegex != nil {
			newID, ok := channelIDMap[assoc.ChannelRegex.ChannelID]
			if !ok {
				return true
			}
			assoc.ChannelRegex.ChannelID = newID
		}

		if assoc.Regex != nil {
			remapExcludeAssociationChannelIDs(assoc.Regex.Exclude, channelIDMap)
		}

		if assoc.ModelID != nil {
			remapExcludeAssociationChannelIDs(assoc.ModelID.Exclude, channelIDMap)
		}

		return false
	})
}

func remapExcludeAssociationChannelIDs(exclude []*objects.ExcludeAssociation, channelIDMap map[int]int) {
	for _, ex := range exclude {
		if ex == nil || len(ex.ChannelIds) == 0 {
			continue
		}

		mappedIDs := ex.ChannelIds[:0]
		for _, oldID := range ex.ChannelIds {
			if newID, ok := channelIDMap[oldID]; ok {
				mappedIDs = append(mappedIDs, newID)
			}
		}
		ex.ChannelIds = mappedIDs
	}
}

func remapAPIKeyProfilesChannelIDs(profiles *objects.APIKeyProfiles, channelIDMap map[int]int) {
	if profiles == nil || len(channelIDMap) == 0 {
		return
	}

	for i := range profiles.Profiles {
		profile := &profiles.Profiles[i]
		if len(profile.ChannelIDs) == 0 {
			continue
		}

		for j, oldID := range profile.ChannelIDs {
			if newID, ok := channelIDMap[oldID]; ok {
				profile.ChannelIDs[j] = newID
			}
		}
	}
}

func remapProjectProfilesChannelIDs(profiles *objects.ProjectProfiles, channelIDMap map[int]int) {
	if profiles == nil || len(channelIDMap) == 0 {
		return
	}

	for i := range profiles.Profiles {
		profile := &profiles.Profiles[i]
		if len(profile.ChannelIDs) == 0 {
			continue
		}

		for j, oldID := range profile.ChannelIDs {
			if newID, ok := channelIDMap[oldID]; ok {
				profile.ChannelIDs[j] = newID
			}
		}
	}
}

func (svc *BackupService) restoreProjects(ctx context.Context, db *ent.Client, projectsData []*BackupProject, opts RestoreOptions) error {
	if len(projectsData) == 0 {
		return nil
	}

	for _, projData := range projectsData {
		if projData == nil {
			continue
		}

		existing, err := db.Project.Query().
			Where(project.Name(projData.Name)).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}

		if existing != nil {
			switch opts.ProjectConflictStrategy {
			case ConflictStrategySkip:
				log.Info(ctx, "skipping existing project", log.String("name", projData.Name))
				continue
			case ConflictStrategyError:
				log.Error(ctx, "project already exists", log.String("name", projData.Name))
				return fmt.Errorf("project %s already exists", projData.Name)
			case ConflictStrategyOverwrite:
				_, err = db.Project.UpdateOneID(existing.ID).
					SetName(projData.Name).
					SetDescription(projData.Description).
					SetStatus(projData.Status).
					SetProfiles(projData.Profiles).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to restore project %s: %w", projData.Name, err)
				}
			}

			continue
		}

		_, err = db.Project.Create().
			SetName(projData.Name).
			SetDescription(projData.Description).
			SetStatus(projData.Status).
			SetProfiles(projData.Profiles).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to create project %s: %w", projData.Name, err)
		}
	}

	return nil
}

func (svc *BackupService) restoreChannelModelPrices(
	ctx context.Context,
	db *ent.Client,
	prices []*BackupChannelModelPrice,
	backupVersion string,
	opts RestoreOptions,
) error {
	if len(prices) == 0 {
		return nil
	}

	now := time.Now()
	channelCache := map[string]*ent.Channel{}
	updatedChannels := map[int]struct{}{}

	getChannel := func(name string) (*ent.Channel, error) {
		if ch, ok := channelCache[name]; ok {
			return ch, nil
		}

		ch, err := db.Channel.Query().
			Where(channel.Name(name)).
			First(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				channelCache[name] = nil
				return nil, nil
			}

			return nil, err
		}

		channelCache[name] = ch

		return ch, nil
	}

	for _, pData := range prices {
		if pData == nil {
			continue
		}
		pData.Price = pData.Price.CanonicalizedServiceTiers()

		if err := validateBackupModelPrice(&pData.Price, backupVersion); err != nil {
			return fmt.Errorf("invalid channel model price: channel=%s model_id=%s: %w", pData.ChannelName, pData.ModelID, err)
		}

		ch, err := getChannel(pData.ChannelName)
		if err != nil {
			return err
		}

		if ch == nil {
			log.Warn(ctx, "channel not found for restoring channel model price, skipping",
				log.String("channel", pData.ChannelName),
				log.String("model_id", pData.ModelID),
			)

			continue
		}

		existing, err := db.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID(pData.ModelID),
			).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}

		refID := pData.ReferenceID
		if refID == "" {
			return fmt.Errorf("channel model price reference ID is empty: channel=%s model_id=%s", pData.ChannelName, pData.ModelID)
		}

		if existing != nil {
			if existing.ReferenceID == refID && existing.Price.Equals(pData.Price) {
				continue
			}

			switch opts.ModelPriceConflictStrategy {
			case ConflictStrategySkip:
				continue
			case ConflictStrategyError:
				return fmt.Errorf("channel model price already exists: channel=%s model_id=%s", pData.ChannelName, pData.ModelID)
			case ConflictStrategyOverwrite:
				// Archive old versions
				_, err = db.ChannelModelPriceVersion.Update().
					Where(
						channelmodelpriceversion.ChannelModelPriceIDEQ(existing.ID),
						channelmodelpriceversion.StatusEQ(channelmodelpriceversion.StatusActive),
					).
					SetStatus(channelmodelpriceversion.StatusArchived).
					SetEffectiveEndAt(now).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to archive old channel model price versions: %w", err)
				}

				if _, err := db.ChannelModelPrice.UpdateOneID(existing.ID).
					SetPrice(pData.Price).
					SetReferenceID(refID).
					Save(ctx); err != nil {
					return fmt.Errorf("failed to restore channel model price: channel=%s model_id=%s: %w", pData.ChannelName, pData.ModelID, err)
				}

				// Create new version
				_, err = db.ChannelModelPriceVersion.Create().
					SetChannelID(ch.ID).
					SetModelID(pData.ModelID).
					SetChannelModelPriceID(existing.ID).
					SetPrice(pData.Price).
					SetStatus(channelmodelpriceversion.StatusActive).
					SetEffectiveStartAt(now).
					SetReferenceID(refID).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to create channel model price version: %w", err)
				}

				updatedChannels[ch.ID] = struct{}{}
			}

			continue
		}

		entity, err := db.ChannelModelPrice.Create().
			SetChannelID(ch.ID).
			SetModelID(pData.ModelID).
			SetPrice(pData.Price).
			SetReferenceID(refID).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to create channel model price: channel=%s model_id=%s: %w", pData.ChannelName, pData.ModelID, err)
		}

		// Create new version
		_, err = db.ChannelModelPriceVersion.Create().
			SetChannelID(ch.ID).
			SetModelID(pData.ModelID).
			SetChannelModelPriceID(entity.ID).
			SetPrice(pData.Price).
			SetStatus(channelmodelpriceversion.StatusActive).
			SetEffectiveStartAt(now).
			SetReferenceID(refID).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to create channel model price version: %w", err)
		}

		updatedChannels[ch.ID] = struct{}{}
	}

	// Force update channel updated_at to trigger reload cache.
	for chID := range updatedChannels {
		if err := db.Channel.UpdateOneID(chID).
			SetUpdatedAt(now).
			Exec(ctx); err != nil {
			return fmt.Errorf("failed to update channel updated_at: %w", err)
		}
	}

	return nil
}

func (svc *BackupService) restoreChannels(ctx context.Context, db *ent.Client, channels []*BackupChannel, opts RestoreOptions) error {
	for _, chData := range channels {
		existing, err := db.Channel.Query().
			Where(channel.Name(chData.Name)).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}

		credentials := chData.Credentials
		// Check if credentials are empty (no API key and no OAuth)
		if credentials.APIKey == "" && len(credentials.APIKeys) == 0 && credentials.OAuth == nil {
			continue
		}

		var baseURL *string
		if chData.BaseURL != "" {
			baseURL = &chData.BaseURL
		}

		if existing != nil {
			switch opts.ChannelConflictStrategy {
			case ConflictStrategySkip:
				log.Info(ctx, "skipping existing channel", log.String("channel", chData.Name))
				continue
			case ConflictStrategyError:
				log.Error(ctx, "channel already exists",
					log.String("channel", chData.Name))

				return fmt.Errorf("channel %s already exists", chData.Name)
			case ConflictStrategyOverwrite:
				update := db.Channel.UpdateOneID(existing.ID).
					SetNillableBaseURL(baseURL).
					SetStatus(chData.Status).
					SetCredentials(credentials).
					SetSupportedModels(chData.SupportedModels).
					SetNillableAutoSyncSupportedModels(lo.ToPtr(chData.AutoSyncSupportedModels)).
					SetAutoSyncModelPattern(chData.AutoSyncModelPattern).
					SetManualModels(chData.ManualModels).
					SetTags(chData.Tags).
					SetDefaultTestModel(chData.DefaultTestModel).
					SetSettings(chData.Settings).
					SetOrderingWeight(chData.OrderingWeight)

				if chData.Remark != nil {
					update.SetRemark(*chData.Remark)
				} else {
					update.ClearRemark()
				}

				if _, err := update.Save(ctx); err != nil {
					log.Error(ctx, "failed to restore channel",
						log.String("channel", chData.Name),
						log.Cause(err))

					return fmt.Errorf("failed to restore channel %s: %w", chData.Name, err)
				}
			}
		} else {
			create := db.Channel.Create().
				SetName(chData.Name).
				SetType(chData.Type).
				SetNillableBaseURL(baseURL).
				SetStatus(chData.Status).
				SetCredentials(credentials).
				SetSupportedModels(chData.SupportedModels).
				SetNillableAutoSyncSupportedModels(lo.ToPtr(chData.AutoSyncSupportedModels)).
				SetAutoSyncModelPattern(chData.AutoSyncModelPattern).
				SetManualModels(chData.ManualModels).
				SetTags(chData.Tags).
				SetDefaultTestModel(chData.DefaultTestModel).
				SetSettings(chData.Settings).
				SetOrderingWeight(chData.OrderingWeight)

			if chData.Remark != nil {
				create.SetRemark(*chData.Remark)
			}

			if _, err := create.Save(ctx); err != nil {
				log.Error(ctx, "failed to create channel",
					log.String("channel", chData.Name),
					log.Cause(err))

				return fmt.Errorf("failed to create channel %s: %w", chData.Name, err)
			}
		}
	}

	return nil
}

func validateBackupModelPrice(price *objects.ModelPrice, backupVersion string) error {
	if price == nil {
		return fmt.Errorf("modelPrice is nil")
	}
	if backupVersion == BackupVersion {
		return price.Validate()
	}

	if err := validateLegacyModelPrice(price); err != nil {
		return err
	}
	normalizeLegacyModelPrice(price)

	return price.Validate()
}

// validateLegacyModelPrice preserves the 1.0-1.4 backup contract where its
// behavior is unambiguous. Invalid tier boundaries are rejected instead of
// being rewritten, because changing them would change future billing.
func validateLegacyModelPrice(price *objects.ModelPrice) error {
	if price == nil {
		return fmt.Errorf("modelPrice is nil")
	}

	itemCodes := make(map[objects.PriceItemCode]struct{}, len(price.Items))
	for itemIndex := range price.Items {
		item := &price.Items[itemIndex]
		if _, exists := itemCodes[item.ItemCode]; exists {
			return fmt.Errorf(
				"items[%d]: duplicate itemCode %q cannot be restored because historical billing summed duplicate items; combine the items in the backup before restoring",
				itemIndex,
				item.ItemCode,
			)
		}
		itemCodes[item.ItemCode] = struct{}{}

		if err := validateLegacyPricing(&item.Pricing); err != nil {
			return fmt.Errorf("items[%d]: pricing: %w", itemIndex, err)
		}
		for variantIndex := range item.PromptWriteCacheVariants {
			if err := validateLegacyPricing(&item.PromptWriteCacheVariants[variantIndex].Pricing); err != nil {
				return fmt.Errorf("items[%d].promptWriteCacheVariants[%d]: pricing: %w", itemIndex, variantIndex, err)
			}
		}
	}

	return nil
}

func validateLegacyPricing(pricing *objects.Pricing) error {
	if pricing == nil {
		return fmt.Errorf("pricing is nil")
	}

	switch pricing.Mode {
	case objects.PricingModeFlatFee:
		if pricing.FlatFee == nil {
			return fmt.Errorf("flatFee is required")
		}
	case objects.PricingModeUsagePerUnit:
		if pricing.UsagePerUnit == nil {
			return fmt.Errorf("usagePerUnit is required")
		}
	case objects.PricingModeTiered, objects.PricingModeVolume:
		if pricing.UsageTiered == nil {
			return fmt.Errorf("usageTiered is required")
		}
		if len(pricing.UsageTiered.Tiers) == 0 {
			return fmt.Errorf("tiers is required")
		}
		lastIndex := len(pricing.UsageTiered.Tiers) - 1
		for tierIndex := range pricing.UsageTiered.Tiers {
			upTo := pricing.UsageTiered.Tiers[tierIndex].UpTo
			if tierIndex == lastIndex {
				if upTo != nil {
					return fmt.Errorf("tiers[%d].upTo must be null", tierIndex)
				}
				continue
			}
			if upTo == nil {
				return fmt.Errorf("tiers[%d].upTo is required", tierIndex)
			}
			if *upTo <= 0 {
				return fmt.Errorf("tiers[%d].upTo must be greater than 0; repair the legacy backup before restoring", tierIndex)
			}
			if tierIndex > 0 {
				previousUpTo := pricing.UsageTiered.Tiers[tierIndex-1].UpTo
				if previousUpTo != nil && *upTo <= *previousUpTo {
					return fmt.Errorf("tiers[%d].upTo must be greater than tiers[%d].upTo; repair the legacy backup before restoring", tierIndex, tierIndex-1)
				}
			}
		}
	default:
		return fmt.Errorf("unknown pricing mode: %s", pricing.Mode)
	}

	return nil
}

// normalizeLegacyModelPrice removes duplicate cache variants while preserving
// the historical first-match lookup behavior. Tier boundaries are validated,
// not rewritten, because changing them would alter billing semantics.
func normalizeLegacyModelPrice(price *objects.ModelPrice) {
	if price == nil {
		return
	}

	for itemIndex := range price.Items {
		item := &price.Items[itemIndex]

		// Cache variant lookup has always returned the first matching variant.
		// Keep that historical behavior while producing data accepted by the
		// current schema. Duplicate base item codes are rejected above because
		// the old calculator summed them and cannot be safely collapsed.
		variantIndexes := make(map[objects.PromptWriteCacheVariantCode]struct{}, len(item.PromptWriteCacheVariants))
		normalizedVariants := make([]objects.PromptWriteCacheVariant, 0, len(item.PromptWriteCacheVariants))
		for _, variant := range item.PromptWriteCacheVariants {
			if _, exists := variantIndexes[variant.VariantCode]; exists {
				continue
			}
			variantIndexes[variant.VariantCode] = struct{}{}

			normalizedVariants = append(normalizedVariants, variant)
		}
		item.PromptWriteCacheVariants = normalizedVariants
	}
}

func (svc *BackupService) restoreModels(ctx context.Context, db *ent.Client, models []*BackupModel, opts RestoreOptions, channelIDMap map[int]int) error {
	for _, modelData := range models {
		if modelData == nil {
			continue
		}

		remapModelSettingsChannelIDs(modelData.Settings, channelIDMap)

		existing, err := db.Model.Query().
			Where(
				model.Developer(modelData.Developer),
				model.ModelID(modelData.ModelID),
			).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}

		if existing != nil {
			switch opts.ModelConflictStrategy {
			case ConflictStrategySkip:
				log.Info(ctx, "skipping existing model", log.String("model", modelData.ModelID))
				continue
			case ConflictStrategyError:
				log.Error(ctx, "model already exists",
					log.String("model", modelData.ModelID))

				return fmt.Errorf("model %s already exists", modelData.ModelID)
			case ConflictStrategyOverwrite:
				update := db.Model.UpdateOneID(existing.ID).
					SetName(modelData.Name).
					SetIcon(modelData.Icon).
					SetGroup(modelData.Group).
					SetModelCard(modelData.ModelCard).
					SetSettings(modelData.Settings).
					SetStatus(modelData.Status)

				if modelData.Remark != nil {
					update.SetRemark(*modelData.Remark)
				} else {
					update.ClearRemark()
				}

				if _, err := update.Save(ctx); err != nil {
					log.Error(ctx, "failed to restore model",
						log.String("model", modelData.ModelID),
						log.Cause(err))

					return fmt.Errorf("failed to restore model %s: %w", modelData.ModelID, err)
				}
			}
		} else {
			create := db.Model.Create().
				SetDeveloper(modelData.Developer).
				SetModelID(modelData.ModelID).
				SetType(modelData.Type).
				SetName(modelData.Name).
				SetIcon(modelData.Icon).
				SetGroup(modelData.Group).
				SetModelCard(modelData.ModelCard).
				SetSettings(modelData.Settings).
				SetStatus(modelData.Status)

			if modelData.Remark != nil {
				create.SetRemark(*modelData.Remark)
			}

			if _, err := create.Save(ctx); err != nil {
				log.Error(ctx, "failed to create model",
					log.String("model", modelData.ModelID),
					log.Cause(err))

				return fmt.Errorf("failed to create model %s: %w", modelData.ModelID, err)
			}
		}
	}

	return nil
}

func (svc *BackupService) restoreAPIKeys(ctx context.Context, db *ent.Client, apiKeys []*BackupAPIKey, opts RestoreOptions, channelIDMap map[int]int) error {
	user, ok := contexts.GetUser(ctx)
	if !ok || user == nil {
		return fmt.Errorf("user not found in context for restoring API keys")
	}

	for _, akData := range apiKeys {
		if akData == nil {
			continue
		}

		remapAPIKeyProfilesChannelIDs(akData.Profiles, channelIDMap)

		existing, err := db.APIKey.Query().
			Where(apikey.Key(akData.Key)).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}

		if existing != nil {
			switch opts.APIKeyConflictStrategy {
			case ConflictStrategySkip:
				log.Info(ctx, "skipping existing API key", log.String("name", akData.Name))
				continue
			case ConflictStrategyError:
				log.Error(ctx, "API key already exists",
					log.String("name", akData.Name))

				return fmt.Errorf("API key %s already exists", akData.Name)
			case ConflictStrategyOverwrite:
				update := db.APIKey.UpdateOneID(existing.ID).
					SetName(akData.Name).
					SetType(akData.Type).
					SetStatus(akData.Status).
					SetScopes(akData.Scopes).
					SetProfiles(akData.Profiles)

				if _, err := update.Save(ctx); err != nil {
					log.Error(ctx, "failed to restore API key",
						log.String("name", akData.Name),
						log.Cause(err))

					return fmt.Errorf("failed to restore API key %s: %w", akData.Name, err)
				}
			}
		} else {
			projectName := akData.ProjectName
			if projectName == "" {
				projectName = "Default"
			}

			proj, err := db.Project.Query().
				Where(project.Name(projectName)).
				First(ctx)
			if err != nil {
				if ent.IsNotFound(err) {
					log.Warn(ctx, "project not found, skipping API key",
						log.String("project", projectName),
						log.String("api_key", akData.Name))

					continue
				}

				return err
			}

			create := db.APIKey.Create().
				SetKey(akData.Key).
				SetName(akData.Name).
				SetType(akData.Type).
				SetStatus(akData.Status).
				SetScopes(akData.Scopes).
				SetProfiles(akData.Profiles).
				SetUserID(user.ID).
				SetProjectID(proj.ID)

			if _, err := create.Save(ctx); err != nil {
				log.Error(ctx, "failed to create API key",
					log.String("name", akData.Name),
					log.Cause(err))

				return fmt.Errorf("failed to create API key %s: %w", akData.Name, err)
			}
		}
	}

	return nil
}

func (svc *BackupService) restoreUsageData(
	ctx context.Context,
	db *ent.Client,
	requestsData []*BackupUsageRequest,
	requestExecutions []*BackupRequestExecution,
	usageLogs []*BackupUsageLog,
	opts RestoreOptions,
) error {
	resolver, err := newUsageRestoreResolver(ctx, db)
	if err != nil {
		return err
	}

	requestIDMap := map[int]int{}
	if opts.IncludeRequestLogs {
		requestIDMap, err = svc.restoreUsageRequests(ctx, db, requestsData, resolver)
		if err != nil {
			return err
		}
	}

	if opts.IncludeUsageStats {
		if err := svc.ensureUsageLogRequests(ctx, db, usageLogs, requestIDMap, resolver); err != nil {
			return err
		}
	}

	requestExecutionIDMap := map[int]int{}
	hasExecutionBackup := len(requestExecutions) > 0
	if hasExecutionBackup {
		requestExecutionIDMap, err = svc.restoreRequestExecutions(ctx, db, requestExecutions, requestIDMap, resolver, opts.IncludeRequestLogs)
		if err != nil {
			return err
		}
	}

	if opts.IncludeUsageStats {
		return svc.restoreUsageLogs(ctx, db, usageLogs, requestIDMap, requestExecutionIDMap, hasExecutionBackup, resolver)
	}

	return nil
}

func (svc *BackupService) restoreUsageRequests(
	ctx context.Context,
	db *ent.Client,
	requestsData []*BackupUsageRequest,
	resolver *usageRestoreResolver,
) (map[int]int, error) {
	idMap := map[int]int{}
	if len(requestsData) == 0 {
		return idMap, nil
	}

	existingRequests, err := existingUsageRequests(ctx, db, requestsData)
	if err != nil {
		return nil, err
	}

	for _, reqData := range requestsData {
		if reqData == nil {
			continue
		}

		oldID := reqData.ID
		if oldID == 0 {
			continue
		}

		projectID, ok := resolver.resolveProjectID(reqData.ProjectID, reqData.ProjectName)
		if !ok {
			log.Warn(ctx, "project not found for restoring usage request, skipping",
				log.Int("request_id", oldID),
				log.String("project", reqData.ProjectName),
			)
			continue
		}

		channelID, ok := resolver.resolveChannelID(reqData.ChannelID, reqData.ChannelName, reqData.ChannelDeletedAt)
		if !ok && hasBackupChannelRef(reqData.ChannelID, reqData.ChannelName) {
			log.Warn(ctx, "channel not found for restoring usage request, restoring with null channel",
				log.Int("request_id", oldID),
				log.Int("channel_id", reqData.ChannelID),
				log.String("channel", reqData.ChannelName),
			)
		}
		matchChannelID := channelID
		if matchChannelID == 0 && resolver.matchesChannelGeneration(reqData.ChannelID, reqData.ChannelName, reqData.ChannelDeletedAt) {
			matchChannelID = reqData.ChannelID
		}

		apiKeyID, ok := resolver.resolveAPIKeyID(reqData.APIKeyKey)
		if !ok && reqData.APIKeyKey != "" {
			log.Warn(ctx, "API key not found for restoring usage request, restoring with null API key",
				log.Int("request_id", oldID),
			)
		}

		if existing, ok := existingRequests.byID[oldID]; ok {
			if sameUsageRequest(existing, reqData, projectID, matchChannelID, apiKeyID) {
				idMap[oldID] = existing.ID
				continue
			}
		}
		if existing, ok := uniqueUsageRequest(ctx, existingRequests.byFingerprint[usageRequestBackupFingerprint(reqData)], oldID, "full fingerprint"); ok {
			idMap[oldID] = existing.ID
			continue
		}

		created, err := db.Request.Create().
			SetCreatedAt(reqData.CreatedAt).
			SetUpdatedAt(reqData.UpdatedAt).
			SetProjectID(projectID).
			SetSource(reqData.Source).
			SetModelID(reqData.ModelID).
			SetFormat(reqData.Format).
			SetRequestBody(reqData.RequestBody).
			SetStatus(reqData.Status).
			SetStream(reqData.Stream).
			SetClientIP(reqData.ClientIP).
			SetNillableAPIKeyID(nilIfZero(apiKeyID)).
			SetNillableChannelID(nilIfZero(channelID)).
			SetNillableReasoningEffort(nilIfEmpty(reqData.ReasoningEffort)).
			SetRequestHeaders(reqData.RequestHeaders).
			SetResponseBody(reqData.ResponseBody).
			SetResponseChunks(reqData.ResponseChunks).
			SetNillableExternalID(nilIfEmpty(reqData.ExternalID)).
			SetNillableMetricsLatencyMs(reqData.MetricsLatencyMs).
			SetNillableMetricsFirstTokenLatencyMs(reqData.MetricsFirstTokenLatencyMs).
			SetNillableMetricsReasoningDurationMs(reqData.MetricsReasoningDurationMs).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to restore usage request %d: %w", oldID, err)
		}

		idMap[oldID] = created.ID
	}

	return idMap, nil
}

func hasBackupChannelRef(channelID int, channelName string) bool {
	return channelID != 0 || channelName != ""
}

type existingUsageRequestLookup struct {
	byID                  map[int]*ent.Request
	byFingerprint         map[string][]*ent.Request
	byMetadataFingerprint map[string][]*ent.Request
}

func existingUsageRequests(
	ctx context.Context,
	db *ent.Client,
	requestsData []*BackupUsageRequest,
) (*existingUsageRequestLookup, error) {
	ids := make([]int, 0, len(requestsData))
	createdAt := make([]time.Time, 0, len(requestsData))
	createdAtSeen := map[time.Time]struct{}{}
	for _, reqData := range requestsData {
		if reqData == nil {
			continue
		}

		if reqData.ID != 0 {
			ids = append(ids, reqData.ID)
		}

		if !reqData.CreatedAt.IsZero() {
			if _, ok := createdAtSeen[reqData.CreatedAt]; ok {
				continue
			}
			createdAtSeen[reqData.CreatedAt] = struct{}{}
			createdAt = append(createdAt, reqData.CreatedAt)
		}
	}

	lookup := &existingUsageRequestLookup{
		byID:                  map[int]*ent.Request{},
		byFingerprint:         map[string][]*ent.Request{},
		byMetadataFingerprint: map[string][]*ent.Request{},
	}
	for start := 0; start < len(ids); start += backupBatchSize {
		end := min(start+backupBatchSize, len(ids))
		requests, err := db.Request.Query().
			Where(request.IDIn(ids[start:end]...)).
			WithProject().
			WithChannel().
			WithAPIKey().
			All(schematype.SkipSoftDelete(ctx))
		if err != nil {
			return nil, err
		}

		for _, req := range requests {
			addExistingUsageRequest(lookup, req)
		}
	}

	for start := 0; start < len(createdAt); start += backupBatchSize {
		end := min(start+backupBatchSize, len(createdAt))
		requests, err := db.Request.Query().
			Where(request.CreatedAtIn(createdAt[start:end]...)).
			WithProject().
			WithChannel().
			WithAPIKey().
			All(schematype.SkipSoftDelete(ctx))
		if err != nil {
			return nil, err
		}

		for _, req := range requests {
			addExistingUsageRequest(lookup, req)
		}
	}

	return lookup, nil
}

func addExistingUsageRequest(lookup *existingUsageRequestLookup, req *ent.Request) {
	lookup.byID[req.ID] = req
	fullWithAPIKey := usageRequestExistingFingerprint(req, true)
	fullWithoutAPIKey := usageRequestExistingFingerprint(req, false)
	metadataWithAPIKey := usageRequestExistingMetadataFingerprint(req, true)
	metadataWithoutAPIKey := usageRequestExistingMetadataFingerprint(req, false)
	appendUsageRequestCandidate(lookup.byFingerprint, fullWithAPIKey, req)
	appendUsageRequestCandidate(lookup.byFingerprint, fullWithoutAPIKey, req)
	appendUsageRequestCandidate(lookup.byMetadataFingerprint, metadataWithAPIKey, req)
	appendUsageRequestCandidate(lookup.byMetadataFingerprint, metadataWithoutAPIKey, req)
}

func appendUsageRequestCandidate(index map[string][]*ent.Request, fingerprint string, req *ent.Request) {
	for _, existing := range index[fingerprint] {
		if existing.ID == req.ID {
			return
		}
	}

	index[fingerprint] = append(index[fingerprint], req)
}

func usageRequestBackupFingerprint(req *BackupUsageRequest) string {
	return usageRequestFingerprint(
		req.CreatedAt,
		req.ModelID,
		req.Format,
		string(req.Source),
		string(req.Status),
		req.Stream,
		req.ClientIP,
		req.ExternalID,
		req.ReasoningEffort,
		req.ProjectName,
		req.ChannelName,
		req.ChannelDeletedAt,
		req.APIKeyKey,
	)
}

func usageRequestExistingFingerprint(req *ent.Request, includeAPIKey bool) string {
	projectName := ""
	if req.Edges.Project != nil {
		projectName = req.Edges.Project.Name
	}

	channelName := ""
	channelDeletedAt := 0
	if req.Edges.Channel != nil {
		channelName = req.Edges.Channel.Name
		channelDeletedAt = req.Edges.Channel.DeletedAt
	}

	apiKeyKey := ""
	if includeAPIKey && req.Edges.APIKey != nil {
		apiKeyKey = req.Edges.APIKey.Key
	}

	return usageRequestFingerprint(
		req.CreatedAt,
		req.ModelID,
		req.Format,
		string(req.Source),
		string(req.Status),
		req.Stream,
		req.ClientIP,
		req.ExternalID,
		req.ReasoningEffort,
		projectName,
		channelName,
		channelDeletedAt,
		apiKeyKey,
	)
}

func usageRequestBackupMetadataFingerprint(req *BackupUsageRequest) string {
	return usageRequestMetadataFingerprint(
		req.CreatedAt,
		req.ModelID,
		req.Format,
		string(req.Source),
		req.Stream,
		req.ReasoningEffort,
		req.ProjectName,
		req.ChannelName,
		req.ChannelDeletedAt,
		req.APIKeyKey,
	)
}

func usageRequestExistingMetadataFingerprint(req *ent.Request, includeAPIKey bool) string {
	projectName := ""
	if req.Edges.Project != nil {
		projectName = req.Edges.Project.Name
	}

	channelName := ""
	channelDeletedAt := 0
	if req.Edges.Channel != nil {
		channelName = req.Edges.Channel.Name
		channelDeletedAt = req.Edges.Channel.DeletedAt
	}

	apiKeyKey := ""
	if includeAPIKey && req.Edges.APIKey != nil {
		apiKeyKey = req.Edges.APIKey.Key
	}

	return usageRequestMetadataFingerprint(
		req.CreatedAt,
		req.ModelID,
		req.Format,
		string(req.Source),
		req.Stream,
		req.ReasoningEffort,
		projectName,
		channelName,
		channelDeletedAt,
		apiKeyKey,
	)
}

func usageRequestFingerprint(
	createdAt time.Time,
	modelID string,
	format string,
	source string,
	status string,
	stream bool,
	clientIP string,
	externalID string,
	reasoningEffort string,
	projectName string,
	channelName string,
	channelDeletedAt int,
	apiKeyKey string,
) string {
	parts := []string{
		createdAt.UTC().Format(time.RFC3339Nano),
		modelID,
		format,
		source,
		status,
		fmt.Sprintf("%t", stream),
		clientIP,
		externalID,
		reasoningEffort,
		projectName,
		channelName,
		fmt.Sprintf("%d", channelDeletedAt),
		apiKeyKey,
	}

	return strings.Join(parts, "\x00")
}

func usageRequestMetadataFingerprint(
	createdAt time.Time,
	modelID string,
	format string,
	source string,
	stream bool,
	reasoningEffort string,
	projectName string,
	channelName string,
	channelDeletedAt int,
	apiKeyKey string,
) string {
	parts := []string{
		createdAt.UTC().Format(time.RFC3339Nano),
		modelID,
		format,
		source,
		fmt.Sprintf("%t", stream),
		reasoningEffort,
		projectName,
		channelName,
		fmt.Sprintf("%d", channelDeletedAt),
		apiKeyKey,
	}

	return strings.Join(parts, "\x00")
}

func sameUsageRequest(existing *ent.Request, backup *BackupUsageRequest, projectID, channelID, apiKeyID int) bool {
	apiKeyMatches := backup.APIKeyKey == "" || existing.APIKeyID == apiKeyID

	return existing.ProjectID == projectID &&
		existing.ChannelID == channelID &&
		apiKeyMatches &&
		existing.ModelID == backup.ModelID &&
		existing.Format == backup.Format &&
		existing.Source == backup.Source &&
		existing.Status == backup.Status &&
		existing.Stream == backup.Stream &&
		existing.ClientIP == backup.ClientIP &&
		existing.ExternalID == backup.ExternalID &&
		existing.ReasoningEffort == backup.ReasoningEffort &&
		existing.CreatedAt.Equal(backup.CreatedAt)
}

func sameUsageRequestMetadata(existing *ent.Request, backup *BackupUsageRequest, projectID, channelID, apiKeyID int) bool {
	apiKeyMatches := backup.APIKeyKey == "" || existing.APIKeyID == apiKeyID

	return existing.ProjectID == projectID &&
		existing.ChannelID == channelID &&
		apiKeyMatches &&
		existing.ModelID == backup.ModelID &&
		existing.Format == backup.Format &&
		existing.Source == backup.Source &&
		existing.Stream == backup.Stream &&
		existing.ReasoningEffort == backup.ReasoningEffort &&
		existing.CreatedAt.Equal(backup.CreatedAt)
}

type requestExecutionRestorePlan struct {
	data                      *BackupRequestExecution
	requestID                 int
	projectID                 int
	channelID                 int
	targetFingerprint         string
	sourceFingerprint         string
	targetMetadataFingerprint string
	sourceMetadataFingerprint string
}

func (svc *BackupService) restoreRequestExecutions(
	ctx context.Context,
	db *ent.Client,
	executions []*BackupRequestExecution,
	requestIDMap map[int]int,
	resolver *usageRestoreResolver,
	includeContent bool,
) (map[int]int, error) {
	plans := make([]requestExecutionRestorePlan, 0, len(executions))
	seenIDs := make(map[int]struct{}, len(executions))
	for _, execution := range executions {
		if execution == nil || execution.ID == 0 {
			continue
		}
		execution = requestExecutionForRestore(execution, includeContent)
		if _, duplicate := seenIDs[execution.ID]; duplicate {
			continue
		}
		seenIDs[execution.ID] = struct{}{}

		requestID, ok := requestIDMap[execution.RequestID]
		if !ok {
			log.Warn(ctx, "request not found for restoring request execution, skipping",
				log.Int("request_execution_id", execution.ID),
				log.Int("request_id", execution.RequestID),
			)
			continue
		}
		projectID, ok := resolver.resolveProjectID(execution.ProjectID, execution.ProjectName)
		if !ok {
			log.Warn(ctx, "project not found for restoring request execution, skipping",
				log.Int("request_execution_id", execution.ID),
				log.String("project", execution.ProjectName),
			)
			continue
		}

		channelID, _ := resolver.resolveChannelID(execution.ChannelID, execution.ChannelName, execution.ChannelDeletedAt)
		// Restored execution payloads are stored inline. Matching a same-name storage
		// does not prove its blobs exist under the new request/execution IDs.
		targetDataStorageID := 0
		targetFingerprint := requestExecutionFingerprint(execution, requestID, projectID, channelID, targetDataStorageID, includeContent)
		sourceChannelID := channelID
		if sourceChannelID == 0 && resolver.matchesChannelGeneration(execution.ChannelID, execution.ChannelName, execution.ChannelDeletedAt) {
			sourceChannelID = execution.ChannelID
		}
		sourceDataStorageID := targetDataStorageID
		if sourceDataStorageID == 0 && resolver.matchesDataStorageGeneration(execution.DataStorageID, execution.DataStorageName, execution.DataStorageDeletedAt) {
			sourceDataStorageID = execution.DataStorageID
		}

		plans = append(plans, requestExecutionRestorePlan{
			data:                      execution,
			requestID:                 requestID,
			projectID:                 projectID,
			channelID:                 channelID,
			targetFingerprint:         targetFingerprint,
			sourceFingerprint:         requestExecutionFingerprint(execution, requestID, projectID, sourceChannelID, sourceDataStorageID, includeContent),
			targetMetadataFingerprint: requestExecutionFingerprint(execution, requestID, projectID, channelID, targetDataStorageID, false),
			sourceMetadataFingerprint: requestExecutionFingerprint(execution, requestID, projectID, sourceChannelID, sourceDataStorageID, false),
		})
	}

	existing, err := loadExistingRequestExecutions(ctx, db, plans, includeContent)
	if err != nil {
		return nil, err
	}
	existingByID := make(map[int]*ent.RequestExecution, len(existing))
	existingByFingerprint := make(map[string][]*ent.RequestExecution, len(existing))
	for _, execution := range existing {
		existingByID[execution.ID] = execution
		fingerprint := existingRequestExecutionFingerprint(execution, includeContent)
		existingByFingerprint[fingerprint] = append(existingByFingerprint[fingerprint], execution)
	}

	resolved := make(map[int]int, len(plans))
	usedExistingIDs := make(map[int]struct{}, len(plans))
	pendingPlans := make([]requestExecutionRestorePlan, 0, usageBackupBatchSize)
	builders := make([]*ent.RequestExecutionCreate, 0, usageBackupBatchSize)
	flush := func() error {
		if len(builders) == 0 {
			return nil
		}

		created, err := db.RequestExecution.CreateBulk(builders...).Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to restore request executions: %w", err)
		}
		for i, execution := range created {
			resolved[pendingPlans[i].data.ID] = execution.ID
		}
		pendingPlans = pendingPlans[:0]
		builders = builders[:0]
		return nil
	}

	for _, plan := range plans {
		if existingExecution, ok := existingByID[plan.data.ID]; ok {
			fingerprint := existingRequestExecutionFingerprint(existingExecution, includeContent)
			metadataFingerprint := existingRequestExecutionFingerprint(existingExecution, false)
			matchesContent := fingerprint == plan.targetFingerprint || fingerprint == plan.sourceFingerprint
			matchesMetadata := metadataFingerprint == plan.targetMetadataFingerprint || metadataFingerprint == plan.sourceMetadataFingerprint
			if matchesContent || (!includeContent && matchesMetadata) || (includeContent && existingExecution.DataStorageID != 0 && matchesMetadata) {
				resolved[plan.data.ID] = existingExecution.ID
				usedExistingIDs[existingExecution.ID] = struct{}{}
				continue
			}
		}

		matchedExisting := false
		fingerprintCandidates := append(
			append([]*ent.RequestExecution(nil), existingByFingerprint[plan.targetFingerprint]...),
			existingByFingerprint[plan.sourceFingerprint]...,
		)
		for _, existingExecution := range fingerprintCandidates {
			if _, used := usedExistingIDs[existingExecution.ID]; used {
				continue
			}
			resolved[plan.data.ID] = existingExecution.ID
			usedExistingIDs[existingExecution.ID] = struct{}{}
			matchedExisting = true
			break
		}
		if matchedExisting {
			continue
		}

		pendingPlans = append(pendingPlans, plan)
		builders = append(builders, newRequestExecutionRestoreBuilder(db, plan))
		if len(builders) >= usageBackupBatchSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}

	if err := flush(); err != nil {
		return nil, err
	}

	return resolved, nil
}

func requestExecutionForRestore(execution *BackupRequestExecution, includeContent bool) *BackupRequestExecution {
	if includeContent {
		return execution
	}

	sanitized := *execution
	sanitized.ExternalID = ""
	sanitized.RequestBody = objects.JSONRawMessage("{}")
	sanitized.ResponseBody = nil
	sanitized.ResponseChunks = nil
	sanitized.ErrorMessage = ""
	sanitized.RequestHeaders = nil
	sanitized.RequestURL = ""
	return &sanitized
}

func loadExistingRequestExecutions(
	ctx context.Context,
	db *ent.Client,
	plans []requestExecutionRestorePlan,
	includeContent bool,
) ([]*ent.RequestExecution, error) {
	ids := make([]int, 0, len(plans))
	requestIDs := make([]int, 0, len(plans))
	requestIDSeen := make(map[int]struct{}, len(plans))
	for _, plan := range plans {
		ids = append(ids, plan.data.ID)
		if _, seen := requestIDSeen[plan.requestID]; !seen {
			requestIDSeen[plan.requestID] = struct{}{}
			requestIDs = append(requestIDs, plan.requestID)
		}
	}

	existingByID := make(map[int]*ent.RequestExecution, len(plans))
	for start := 0; start < len(ids); start += usageBackupBatchSize {
		end := min(start+usageBackupBatchSize, len(ids))
		query := db.RequestExecution.Query().
			Where(requestexecution.IDIn(ids[start:end]...))
		if !includeContent {
			query.Select(requestExecutionMetadataFields()...)
		}
		executions, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load request execution ID candidates: %w", err)
		}
		for _, execution := range executions {
			existingByID[execution.ID] = execution
		}
	}

	for start := 0; start < len(requestIDs); start += usageBackupBatchSize {
		end := min(start+usageBackupBatchSize, len(requestIDs))
		query := db.RequestExecution.Query().
			Where(requestexecution.RequestIDIn(requestIDs[start:end]...))
		if !includeContent {
			query.Select(requestExecutionMetadataFields()...)
		}
		executions, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load request execution request candidates: %w", err)
		}
		for _, execution := range executions {
			existingByID[execution.ID] = execution
		}
	}

	result := make([]*ent.RequestExecution, 0, len(existingByID))
	for _, execution := range existingByID {
		result = append(result, execution)
	}
	return result, nil
}

func requestExecutionMetadataFields() []string {
	return []string{
		requestexecution.FieldID,
		requestexecution.FieldCreatedAt,
		requestexecution.FieldUpdatedAt,
		requestexecution.FieldProjectID,
		requestexecution.FieldRequestID,
		requestexecution.FieldChannelID,
		requestexecution.FieldDataStorageID,
		requestexecution.FieldSource,
		requestexecution.FieldModelID,
		requestexecution.FieldFormat,
		requestexecution.FieldRequestedServiceTier,
		requestexecution.FieldSpeedMode,
		requestexecution.FieldChannelAPIKeyName,
		requestexecution.FieldChannelAPIKeySuffix,
		requestexecution.FieldChannelAPIKeyHeaders,
		requestexecution.FieldResponseStatusCode,
		requestexecution.FieldStatus,
		requestexecution.FieldStream,
		requestexecution.FieldMetricsLatencyMs,
		requestexecution.FieldMetricsFirstTokenLatencyMs,
		requestexecution.FieldMetricsReasoningDurationMs,
		requestexecution.FieldPassThroughApplied,
	}
}

func newRequestExecutionRestoreBuilder(db *ent.Client, plan requestExecutionRestorePlan) *ent.RequestExecutionCreate {
	data := plan.data
	requestBody := data.RequestBody
	if len(requestBody) == 0 {
		requestBody = objects.JSONRawMessage("{}")
	}

	builder := db.RequestExecution.Create().
		SetCreatedAt(data.CreatedAt).
		SetUpdatedAt(data.UpdatedAt).
		SetProjectID(plan.projectID).
		SetRequestID(plan.requestID).
		SetNillableChannelID(nilIfZero(plan.channelID)).
		SetNillableExternalID(nilIfEmpty(data.ExternalID)).
		SetSource(data.Source).
		SetModelID(data.ModelID).
		SetFormat(data.Format).
		SetNillableRequestedServiceTier(nilIfEmpty(llm.CanonicalServiceTier(data.RequestedServiceTier))).
		SetNillableSpeedMode(nilIfEmpty(strings.ToLower(strings.TrimSpace(data.SpeedMode)))).
		SetNillableChannelAPIKeyName(nilIfEmpty(data.ChannelAPIKeyName)).
		SetNillableChannelAPIKeySuffix(nilIfEmpty(data.ChannelAPIKeySuffix)).
		SetRequestBody(requestBody).
		SetNillableErrorMessage(nilIfEmpty(data.ErrorMessage)).
		SetNillableResponseStatusCode(data.ResponseStatusCode).
		SetStatus(data.Status).
		SetStream(data.Stream).
		SetNillableMetricsLatencyMs(data.MetricsLatencyMs).
		SetNillableMetricsFirstTokenLatencyMs(data.MetricsFirstTokenLatencyMs).
		SetNillableMetricsReasoningDurationMs(data.MetricsReasoningDurationMs).
		SetNillableRequestURL(nilIfEmpty(data.RequestURL)).
		SetPassThroughApplied(data.PassThroughApplied)
	if len(data.ChannelAPIKeyHeaders) > 0 {
		builder.SetChannelAPIKeyHeaders(data.ChannelAPIKeyHeaders)
	}

	if len(data.ResponseBody) > 0 {
		builder.SetResponseBody(data.ResponseBody)
	}
	if data.ResponseChunks != nil {
		builder.SetResponseChunks(data.ResponseChunks)
	}
	if len(data.RequestHeaders) > 0 {
		builder.SetRequestHeaders(data.RequestHeaders)
	}

	return builder
}

func existingRequestExecutionFingerprint(execution *ent.RequestExecution, includeContent bool) string {
	return requestExecutionFingerprint(
		backupRequestExecution(execution, nil, nil, nil, includeContent),
		execution.RequestID,
		execution.ProjectID,
		execution.ChannelID,
		execution.DataStorageID,
		includeContent,
	)
}

func requestExecutionFingerprint(
	execution *BackupRequestExecution,
	requestID int,
	projectID int,
	channelID int,
	dataStorageID int,
	includeContent bool,
) string {
	h := sha256.New()
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%d", requestID))
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%d", projectID))
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%d", channelID))
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%d", dataStorageID))
	writeRequestExecutionFingerprintField(h, execution.CreatedAt.UTC().Format(time.RFC3339Nano))
	writeRequestExecutionFingerprintField(h, execution.UpdatedAt.UTC().Format(time.RFC3339Nano))
	writeRequestExecutionFingerprintField(h, string(execution.Source))
	writeRequestExecutionFingerprintField(h, execution.ModelID)
	writeRequestExecutionFingerprintField(h, execution.Format)
	writeRequestExecutionFingerprintField(h, llm.CanonicalServiceTier(execution.RequestedServiceTier))
	writeRequestExecutionFingerprintField(h, strings.ToLower(strings.TrimSpace(execution.SpeedMode)))
	writeRequestExecutionFingerprintField(h, execution.ChannelAPIKeyName)
	writeRequestExecutionFingerprintField(h, execution.ChannelAPIKeySuffix)
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%d", len(execution.ChannelAPIKeyHeaders)))
	for _, headerName := range execution.ChannelAPIKeyHeaders {
		writeRequestExecutionFingerprintField(h, headerName)
	}
	writeRequestExecutionFingerprintField(h, optionalIntFingerprint(execution.ResponseStatusCode))
	writeRequestExecutionFingerprintField(h, string(execution.Status))
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%t", execution.Stream))
	writeRequestExecutionFingerprintField(h, optionalInt64Fingerprint(execution.MetricsLatencyMs))
	writeRequestExecutionFingerprintField(h, optionalInt64Fingerprint(execution.MetricsFirstTokenLatencyMs))
	writeRequestExecutionFingerprintField(h, optionalInt64Fingerprint(execution.MetricsReasoningDurationMs))
	writeRequestExecutionFingerprintField(h, fmt.Sprintf("%t", execution.PassThroughApplied))
	if includeContent {
		writeRequestExecutionFingerprintField(h, execution.ExternalID)
		writeRequestExecutionFingerprintField(h, string(execution.RequestBody))
		writeRequestExecutionFingerprintField(h, string(execution.ResponseBody))
		writeRequestExecutionFingerprintField(h, fmt.Sprintf("%d", len(execution.ResponseChunks)))
		for _, chunk := range execution.ResponseChunks {
			writeRequestExecutionFingerprintField(h, string(chunk))
		}
		writeRequestExecutionFingerprintField(h, execution.ErrorMessage)
		writeRequestExecutionFingerprintField(h, string(execution.RequestHeaders))
		writeRequestExecutionFingerprintField(h, execution.RequestURL)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeRequestExecutionFingerprintField(h hash.Hash, value string) {
	_, _ = fmt.Fprintf(h, "%d:", len(value))
	_, _ = h.Write([]byte(value))
}

func optionalIntFingerprint(value *int) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *value)
}

func optionalInt64Fingerprint(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *value)
}

func (svc *BackupService) restoreUsageLogs(
	ctx context.Context,
	db *ent.Client,
	usageLogs []*BackupUsageLog,
	requestIDMap map[int]int,
	requestExecutionIDMap map[int]int,
	hasExecutionBackup bool,
	resolver *usageRestoreResolver,
) error {
	if len(usageLogs) == 0 {
		return nil
	}

	if !hasExecutionBackup {
		var err error
		requestExecutionIDMap, err = resolveUsageLogRequestExecutions(ctx, db, usageLogs, requestIDMap, resolver)
		if err != nil {
			return err
		}
	}

	requestIDs := make([]int, 0, len(requestIDMap))
	for _, requestID := range requestIDMap {
		requestIDs = append(requestIDs, requestID)
	}

	existingLogRequestIDs := map[int]struct{}{}
	for start := 0; start < len(requestIDs); start += backupBatchSize {
		end := min(start+backupBatchSize, len(requestIDs))
		logs, err := db.UsageLog.Query().
			Where(usagelog.RequestIDIn(requestIDs[start:end]...)).
			Select(usagelog.FieldRequestID).
			All(ctx)
		if err != nil {
			return err
		}

		for _, usageLog := range logs {
			existingLogRequestIDs[usageLog.RequestID] = struct{}{}
		}
	}

	restoredLogRequestIDs := map[int]struct{}{}
	builders := make([]*ent.UsageLogCreate, 0, min(len(usageLogs), backupBatchSize))
	flush := func() error {
		if len(builders) == 0 {
			return nil
		}

		if _, err := db.UsageLog.CreateBulk(builders...).Save(ctx); err != nil {
			return fmt.Errorf("failed to restore usage logs: %w", err)
		}

		builders = builders[:0]

		return nil
	}

	for _, usageData := range usageLogs {
		if usageData == nil {
			continue
		}

		requestID, ok := requestIDMap[usageData.RequestID]
		if !ok {
			log.Warn(ctx, "request not found for restoring usage log, skipping",
				log.Int("usage_log_id", usageData.ID),
				log.Int("request_id", usageData.RequestID),
			)
			continue
		}

		if _, existing := existingLogRequestIDs[requestID]; existing {
			log.Warn(ctx, "usage log already exists for request, skipping",
				log.Int("usage_log_id", usageData.ID),
				log.Int("request_id", usageData.RequestID),
			)
			continue
		}

		if _, duplicate := restoredLogRequestIDs[requestID]; duplicate {
			log.Warn(ctx, "duplicate usage log for request in backup, skipping",
				log.Int("usage_log_id", usageData.ID),
				log.Int("request_id", usageData.RequestID),
			)
			continue
		}

		projectID, ok := resolver.resolveProjectID(usageData.ProjectID, usageData.ProjectName)
		if !ok {
			log.Warn(ctx, "project not found for restoring usage log, skipping",
				log.Int("usage_log_id", usageData.ID),
				log.String("project", usageData.ProjectName),
			)
			continue
		}

		channelID, ok := resolver.resolveChannelID(usageData.ChannelID, usageData.ChannelName, usageData.ChannelDeletedAt)
		if !ok && hasBackupChannelRef(usageData.ChannelID, usageData.ChannelName) {
			log.Warn(ctx, "channel not found for restoring usage log, restoring with null channel",
				log.Int("usage_log_id", usageData.ID),
				log.Int("channel_id", usageData.ChannelID),
				log.String("channel", usageData.ChannelName),
			)
		}
		matchChannelID := channelID
		if matchChannelID == 0 && resolver.matchesChannelGeneration(usageData.ChannelID, usageData.ChannelName, usageData.ChannelDeletedAt) {
			matchChannelID = usageData.ChannelID
		}

		apiKeyID, ok := resolver.resolveAPIKeyID(usageData.APIKeyKey)
		if !ok && usageData.APIKeyKey != "" {
			log.Warn(ctx, "API key not found for restoring usage log, restoring with null API key",
				log.Int("usage_log_id", usageData.ID),
			)
		}

		requestExecutionID := requestExecutionIDMap[usageData.RequestExecutionID]
		if usageData.RequestExecutionID != 0 && requestExecutionID == 0 {
			log.Warn(ctx, "request execution not found for restoring usage log, restoring without execution linkage",
				log.Int("usage_log_id", usageData.ID),
				log.Int("request_execution_id", usageData.RequestExecutionID),
			)
		}

		builders = append(builders, db.UsageLog.Create().
			SetCreatedAt(usageData.CreatedAt).
			SetUpdatedAt(usageData.UpdatedAt).
			SetRequestID(requestID).
			SetNillableRequestExecutionID(nilIfZero(requestExecutionID)).
			SetNillableAPIKeyID(nilIfZero(apiKeyID)).
			SetProjectID(projectID).
			SetNillableChannelID(nilIfZero(channelID)).
			SetModelID(usageData.ModelID).
			SetPromptTokens(usageData.PromptTokens).
			SetCompletionTokens(usageData.CompletionTokens).
			SetTotalTokens(usageData.TotalTokens).
			SetPromptAudioTokens(usageData.PromptAudioTokens).
			SetPromptCachedTokens(usageData.PromptCachedTokens).
			SetPromptWriteCachedTokens(usageData.PromptWriteCachedTokens).
			SetPromptWriteCachedTokens5m(usageData.PromptWriteCachedTokens5m).
			SetPromptWriteCachedTokens1h(usageData.PromptWriteCachedTokens1h).
			SetCompletionAudioTokens(usageData.CompletionAudioTokens).
			SetCompletionReasoningTokens(usageData.CompletionReasoningTokens).
			SetCompletionAcceptedPredictionTokens(usageData.CompletionAcceptedPredictionTokens).
			SetCompletionRejectedPredictionTokens(usageData.CompletionRejectedPredictionTokens).
			SetSource(usageData.Source).
			SetFormat(usageData.Format).
			SetNillableRequestedServiceTier(nilIfEmpty(llm.CanonicalServiceTier(usageData.RequestedServiceTier))).
			SetNillableAppliedServiceTier(nilIfEmpty(llm.CanonicalServiceTier(usageData.AppliedServiceTier))).
			SetNillableServiceTier(nilIfEmpty(llm.CanonicalServiceTier(usageData.ServiceTier))).
			SetNillableTotalCost(usageData.TotalCost).
			SetCostItems(usageData.CostItems).
			SetNillableCostPriceReferenceID(nilIfEmpty(usageData.CostPriceReferenceID)))
		restoredLogRequestIDs[requestID] = struct{}{}

		if len(builders) >= backupBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}

	return flush()
}

func resolveUsageLogRequestExecutions(
	ctx context.Context,
	db *ent.Client,
	usageLogs []*BackupUsageLog,
	requestIDMap map[int]int,
	resolver *usageRestoreResolver,
) (map[int]int, error) {
	type executionIdentity struct {
		requestID  int
		channelID  int
		modelID    string
		format     string
		requestURL string
		createdAt  time.Time
	}

	executionIDs := make([]int, 0, len(usageLogs))
	expectedIdentities := make(map[int]executionIdentity, len(usageLogs))
	for _, usageData := range usageLogs {
		if usageData == nil || usageData.RequestExecutionID == 0 || usageData.RequestExecutionCreatedAt.IsZero() {
			continue
		}
		requestID, ok := requestIDMap[usageData.RequestID]
		if !ok {
			continue
		}
		channelID := 0
		if resolvedChannelID, ok := resolver.resolveChannelID(usageData.ChannelID, usageData.ChannelName, usageData.ChannelDeletedAt); ok {
			channelID = resolvedChannelID
		} else if resolver.matchesChannelGeneration(usageData.ChannelID, usageData.ChannelName, usageData.ChannelDeletedAt) {
			channelID = usageData.ChannelID
		}
		if _, ok := expectedIdentities[usageData.RequestExecutionID]; ok {
			continue
		}

		expectedIdentities[usageData.RequestExecutionID] = executionIdentity{
			requestID:  requestID,
			channelID:  channelID,
			modelID:    usageData.ModelID,
			format:     usageData.RequestExecutionFormat,
			requestURL: usageData.RequestExecutionRequestURL,
			createdAt:  usageData.RequestExecutionCreatedAt,
		}
		executionIDs = append(executionIDs, usageData.RequestExecutionID)
	}

	resolved := make(map[int]int, len(executionIDs))
	for start := 0; start < len(executionIDs); start += usageBackupBatchSize {
		end := min(start+usageBackupBatchSize, len(executionIDs))
		executions, err := db.RequestExecution.Query().
			Where(requestexecution.IDIn(executionIDs[start:end]...)).
			Select(
				requestexecution.FieldID,
				requestexecution.FieldRequestID,
				requestexecution.FieldChannelID,
				requestexecution.FieldModelID,
				requestexecution.FieldFormat,
				requestexecution.FieldRequestURL,
				requestexecution.FieldCreatedAt,
			).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve request executions for usage logs: %w", err)
		}

		for _, execution := range executions {
			expected := expectedIdentities[execution.ID]
			if expected.requestID == execution.RequestID &&
				expected.channelID == execution.ChannelID &&
				expected.modelID == execution.ModelID &&
				expected.format == execution.Format &&
				expected.requestURL == execution.RequestURL &&
				expected.createdAt.Equal(execution.CreatedAt) {
				resolved[execution.ID] = execution.ID
			}
		}
	}

	return resolved, nil
}

func (svc *BackupService) ensureUsageLogRequests(
	ctx context.Context,
	db *ent.Client,
	usageLogs []*BackupUsageLog,
	requestIDMap map[int]int,
	resolver *usageRestoreResolver,
) error {
	shellRequests := usageLogRequestShells(usageLogs, requestIDMap)
	existingRequests, err := existingUsageRequests(ctx, db, shellRequests)
	if err != nil {
		return err
	}

	for _, usageData := range usageLogs {
		if usageData == nil || usageData.RequestID == 0 {
			continue
		}

		if _, ok := requestIDMap[usageData.RequestID]; ok {
			continue
		}

		projectID, ok := resolver.resolveProjectID(usageData.ProjectID, usageData.ProjectName)
		if !ok {
			continue
		}

		channelID, ok := resolver.resolveChannelID(usageData.ChannelID, usageData.ChannelName, usageData.ChannelDeletedAt)
		if !ok && hasBackupChannelRef(usageData.ChannelID, usageData.ChannelName) {
			log.Warn(ctx, "channel not found for restoring usage log request shell, restoring with null channel",
				log.Int("usage_log_id", usageData.ID),
				log.Int("channel_id", usageData.ChannelID),
				log.String("channel", usageData.ChannelName),
			)
		}
		matchChannelID := channelID
		if matchChannelID == 0 && resolver.matchesChannelGeneration(usageData.ChannelID, usageData.ChannelName, usageData.ChannelDeletedAt) {
			matchChannelID = usageData.ChannelID
		}

		apiKeyID, ok := resolver.resolveAPIKeyID(usageData.APIKeyKey)
		if !ok && usageData.APIKeyKey != "" {
			log.Warn(ctx, "API key not found for restoring usage log request shell, restoring with null API key",
				log.Int("usage_log_id", usageData.ID),
			)
		}

		shellData := usageLogRequestShell(usageData)
		hasRequestMetadata := !usageData.RequestCreatedAt.IsZero()
		if existing, ok := existingRequests.byID[usageData.RequestID]; ok {
			matches := sameUsageRequest(existing, shellData, projectID, matchChannelID, apiKeyID)
			if hasRequestMetadata {
				matches = sameUsageRequestMetadata(existing, shellData, projectID, matchChannelID, apiKeyID)
			}
			if matches {
				requestIDMap[usageData.RequestID] = existing.ID
				continue
			}
		}
		if hasRequestMetadata {
			if existing, ok := uniqueUsageRequest(ctx, existingRequests.byMetadataFingerprint[usageRequestBackupMetadataFingerprint(shellData)], usageData.RequestID, "metadata fingerprint"); ok {
				requestIDMap[usageData.RequestID] = existing.ID
				continue
			}
		}
		if existing, ok := uniqueUsageRequest(ctx, existingRequests.byFingerprint[usageRequestBackupFingerprint(shellData)], usageData.RequestID, "full fingerprint"); ok {
			requestIDMap[usageData.RequestID] = existing.ID
			continue
		}
		if apiKeyID == 0 {
			shellData.APIKeyKey = ""
			if hasRequestMetadata {
				if existing, ok := uniqueUsageRequest(ctx, existingRequests.byMetadataFingerprint[usageRequestBackupMetadataFingerprint(shellData)], usageData.RequestID, "metadata fingerprint without API key"); ok {
					requestIDMap[usageData.RequestID] = existing.ID
					continue
				}
			}
			if existing, ok := uniqueUsageRequest(ctx, existingRequests.byFingerprint[usageRequestBackupFingerprint(shellData)], usageData.RequestID, "full fingerprint without API key"); ok {
				requestIDMap[usageData.RequestID] = existing.ID
				continue
			}
		}

		created, err := db.Request.Create().
			SetCreatedAt(shellData.CreatedAt).
			SetUpdatedAt(shellData.UpdatedAt).
			SetProjectID(projectID).
			SetSource(shellData.Source).
			SetModelID(shellData.ModelID).
			SetFormat(shellData.Format).
			SetRequestBody(objects.JSONRawMessage("{}")).
			SetStatus(shellData.Status).
			SetStream(shellData.Stream).
			SetClientIP("").
			SetNillableAPIKeyID(nilIfZero(apiKeyID)).
			SetNillableChannelID(nilIfZero(channelID)).
			SetNillableReasoningEffort(nilIfEmpty(shellData.ReasoningEffort)).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to create usage log request shell %d: %w", usageData.RequestID, err)
		}

		requestIDMap[usageData.RequestID] = created.ID
	}

	return nil
}

func uniqueUsageRequest(ctx context.Context, candidates []*ent.Request, requestID int, matchKind string) (*ent.Request, bool) {
	if len(candidates) != 1 {
		if len(candidates) > 1 {
			log.Warn(ctx, "ambiguous request candidates while restoring usage data; creating a new shell",
				log.Int("request_id", requestID),
				log.String("match_kind", matchKind),
				log.Int("candidate_count", len(candidates)),
			)
		}
		return nil, false
	}

	return candidates[0], true
}

func usageLogRequestShells(
	usageLogs []*BackupUsageLog,
	requestIDMap map[int]int,
) []*BackupUsageRequest {
	shells := make([]*BackupUsageRequest, 0, len(usageLogs))
	seen := map[int]struct{}{}
	for _, usageData := range usageLogs {
		if usageData == nil || usageData.RequestID == 0 {
			continue
		}
		if _, ok := requestIDMap[usageData.RequestID]; ok {
			continue
		}
		if _, ok := seen[usageData.RequestID]; ok {
			continue
		}

		seen[usageData.RequestID] = struct{}{}
		shells = append(shells, usageLogRequestShell(usageData))
	}

	return shells
}

func usageLogRequestShell(usageData *BackupUsageLog) *BackupUsageRequest {
	createdAt := usageData.RequestCreatedAt
	if createdAt.IsZero() {
		createdAt = usageData.CreatedAt
	}
	source := usageData.RequestSource
	if source == "" {
		source = request.Source(usageData.Source)
	}
	modelID := usageData.RequestModelID
	if modelID == "" {
		modelID = usageData.ModelID
	}
	format := usageData.RequestFormat
	if format == "" {
		format = usageData.Format
	}
	return &BackupUsageRequest{
		Request: ent.Request{
			ID:              usageData.RequestID,
			CreatedAt:       createdAt,
			UpdatedAt:       usageData.UpdatedAt,
			Source:          source,
			ModelID:         modelID,
			ReasoningEffort: usageData.RequestReasoningEffort,
			Format:          format,
			RequestBody:     objects.JSONRawMessage("{}"),
			Status:          request.StatusCompleted,
			Stream:          usageData.RequestStream,
			ClientIP:        "",
		},
		ProjectName:      usageData.ProjectName,
		ChannelName:      usageData.ChannelName,
		ChannelDeletedAt: usageData.ChannelDeletedAt,
		APIKeyKey:        usageData.APIKeyKey,
	}
}

func nilIfZero(v int) *int {
	if v == 0 {
		return nil
	}

	return &v
}

func nilIfEmpty(v string) *string {
	if v == "" {
		return nil
	}

	return &v
}
