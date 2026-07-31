package backup

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
)

type recordingProviderQuotaInvalidator struct {
	channelIDs []int
}

func (r *recordingProviderQuotaInvalidator) InvalidateChannelQuota(_ context.Context, channelID int) error {
	r.channelIDs = append(r.channelIDs, channelID)
	return nil
}

func TestBackupService_Restore_SystemConfigs(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()
	service.systemService = biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})

	_, err := client.System.Create().
		SetKey(biz.SystemKeyRetryPolicy).
		SetValue(`{"max_channel_retries":1}`).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.System.Create().
		SetKey(biz.SystemKeyProviderQuotaCollectionSettings).
		SetValue(`{"enabled":true,"providers":{"codex":true}}`).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.System.Create().
		SetKey(biz.SystemKeySecretKey).
		SetValue("target-secret").
		Save(ctx)
	require.NoError(t, err)

	cachedQuotaSettings, err := service.systemService.ProviderQuotaCollectionSettings(ctx)
	require.NoError(t, err)
	require.True(t, cachedQuotaSettings.Enabled)

	data, err := json.Marshal(BackupData{
		Version: BackupVersion,
		SystemConfigs: []*BackupSystemConfig{
			{Key: biz.SystemKeyRetryPolicy, Value: `{"max_channel_retries":4}`},
			{Key: biz.SystemKeyProviderQuotaCollectionSettings, Value: `{"enabled":false,"providers":{"codex":false}}`},
			{Key: biz.SystemKeyWebhookNotifierConfig, Value: `{"targets":[{"headers":[{"key":"Authorization","value":"webhook-secret"}]}]}`},
			{Key: biz.SystemKeyProxyPresets, Value: `[{"name":"old","url":"http://proxy.example.com"},{"name":"new","url":"http://proxy.example.com","password":"proxy-secret"},{"name":"second","url":"http://proxy-2.example.com"}]`},
			{Key: biz.SystemKeySecretKey, Value: "source-secret"},
		},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeSystemConfigs: true})
	require.NoError(t, err)

	retryPolicy, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeyRetryPolicy)).Only(ctx)
	require.NoError(t, err)
	var restoredRetryPolicy biz.RetryPolicy
	require.NoError(t, json.Unmarshal([]byte(retryPolicy.Value), &restoredRetryPolicy))
	require.Equal(t, 4, restoredRetryPolicy.MaxChannelRetries)

	quotaSettings, err := service.systemService.ProviderQuotaCollectionSettings(ctx)
	require.NoError(t, err)
	require.False(t, quotaSettings.Enabled)
	require.False(t, quotaSettings.Providers["codex"])

	secretKey, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeySecretKey)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "target-secret", secretKey.Value)

	_, err = client.System.Query().Where(system.KeyEQ(biz.SystemKeyWebhookNotifierConfig)).Only(ctx)
	require.True(t, ent.IsNotFound(err))
	_, err = client.System.Query().Where(system.KeyEQ(biz.SystemKeyProxyPresets)).Only(ctx)
	require.True(t, ent.IsNotFound(err))

	require.NoError(t, service.systemService.SetProxyPresets(ctx, []biz.ProxyPreset{{Name: "target", URL: "http://target.example.com"}}))
	err = service.Restore(ctx, data, RestoreOptions{IncludeSystemConfigs: true, IncludeAPIKeys: true})
	require.NoError(t, err)
	webhookConfig, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeyWebhookNotifierConfig)).Only(ctx)
	require.NoError(t, err)
	require.Contains(t, webhookConfig.Value, "webhook-secret")
	proxyPresets, err := service.systemService.ProxyPresets(ctx)
	require.NoError(t, err)
	require.Equal(t, []biz.ProxyPreset{
		{Name: "new", URL: "http://proxy.example.com", Password: "proxy-secret"},
		{Name: "second", URL: "http://proxy-2.example.com"},
	}, proxyPresets)
}

func TestBackupService_Restore_InvalidSystemConfigRollsBack(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()
	service.systemService = biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})

	require.NoError(t, service.systemService.SetTitle(ctx, "Original title"))
	data, err := json.Marshal(BackupData{
		Version: BackupVersion,
		SystemConfigs: []*BackupSystemConfig{
			{Key: biz.SystemKeyTitle, Value: "Restored title"},
			{Key: biz.SystemKeyModelSettings, Value: `{"model_blacklist_regex":"["}`},
		},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeSystemConfigs: true})
	require.ErrorContains(t, err, "invalid model blacklist regex")

	title, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeyTitle)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "Original title", title.Value)
}

func TestBackupService_Restore_InvalidProxyPresetsRollsBack(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()
	service.systemService = biz.NewSystemService(biz.SystemServiceParams{
		Ent:         client,
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
	})

	require.NoError(t, service.systemService.SetTitle(ctx, "Original title"))
	data, err := json.Marshal(BackupData{
		Version: BackupVersion,
		SystemConfigs: []*BackupSystemConfig{
			{Key: biz.SystemKeyTitle, Value: "Restored title"},
			{Key: biz.SystemKeyProxyPresets, Value: `{"not":"an array"}`},
		},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeSystemConfigs: true, IncludeAPIKeys: true})
	require.ErrorContains(t, err, "invalid JSON value")

	title, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeyTitle)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "Original title", title.Value)
}

func TestBackupService_Restore_StoragePolicyOverridesLegacyStoreChunks(t *testing.T) {
	for _, test := range []struct {
		name    string
		configs []*BackupSystemConfig
		want    bool
	}{
		{
			name: "legacy only",
			configs: []*BackupSystemConfig{
				{Key: biz.SystemKeyStoreChunks, Value: "true"},
			},
			want: true,
		},
		{
			name: "modern before legacy",
			configs: []*BackupSystemConfig{
				{Key: biz.SystemKeyStoragePolicy, Value: `{"store_chunks":false}`},
				{Key: biz.SystemKeyStoreChunks, Value: "true"},
			},
			want: false,
		},
		{
			name: "modern after legacy",
			configs: []*BackupSystemConfig{
				{Key: biz.SystemKeyStoreChunks, Value: "true"},
				{Key: biz.SystemKeyStoragePolicy, Value: `{"store_chunks":false}`},
			},
			want: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, service, ctx := setupBackupTest(t)
			defer client.Close()
			service.systemService = biz.NewSystemService(biz.SystemServiceParams{
				Ent:         client,
				CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
			})

			data, err := json.Marshal(BackupData{Version: BackupVersion, SystemConfigs: test.configs})
			require.NoError(t, err)
			require.NoError(t, service.Restore(ctx, data, RestoreOptions{IncludeSystemConfigs: true}))

			policy, err := service.systemService.StoragePolicy(ctx)
			require.NoError(t, err)
			require.Equal(t, test.want, policy.StoreChunks)
		})
	}
}

func TestBackupService_Restore_SystemModelSettingsRemapChannelIDs(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	const sourceChannelID = 456
	settings, err := json.Marshal(biz.SystemModelSettings{
		DeveloperSettings: []*biz.DeveloperModelSettings{{
			Developer: "openai",
			Associations: []*objects.ModelAssociation{{
				Type: "channel_model",
				ChannelModel: &objects.ChannelModelAssociation{
					ChannelID: sourceChannelID,
					ModelID:   "gpt-4",
				},
			}},
		}},
	})
	require.NoError(t, err)

	data, err := json.Marshal(BackupData{
		Version: BackupVersion,
		SystemConfigs: []*BackupSystemConfig{{
			Key:   biz.SystemKeyModelSettings,
			Value: string(settings),
		}},
		Channels: []*BackupChannel{{
			Channel: ent.Channel{
				ID:      sourceChannelID,
				Type:    channel.TypeOpenai,
				Name:    "System Settings Channel",
				BaseURL: "https://api.example.com",
				Status:  channel.StatusEnabled,
			},
			Credentials: objects.ChannelCredentials{APIKey: "backup-api-key"},
		}},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeSystemConfigs:    true,
		IncludeChannels:         true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	restoredChannel, err := client.Channel.Query().Where(channel.Name("System Settings Channel")).Only(ctx)
	require.NoError(t, err)
	require.NotEqual(t, sourceChannelID, restoredChannel.ID)

	restoredConfig, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeyModelSettings)).Only(ctx)
	require.NoError(t, err)
	var restoredSettings biz.SystemModelSettings
	require.NoError(t, json.Unmarshal([]byte(restoredConfig.Value), &restoredSettings))
	require.Equal(t, restoredChannel.ID, restoredSettings.DeveloperSettings[0].Associations[0].ChannelModel.ChannelID)
}

func TestBackupService_Restore_SystemModelSettingsDropsUnmappedChannelIDs(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	localChannel := createBackupTestChannel(t, client, ctx, "Local Channel", channel.TypeOpenai)
	settings, err := json.Marshal(biz.SystemModelSettings{
		DeveloperSettings: []*biz.DeveloperModelSettings{{
			Developer: "openai",
			Associations: []*objects.ModelAssociation{{
				Type: "channel_model",
				ChannelModel: &objects.ChannelModelAssociation{
					ChannelID: localChannel.ID,
					ModelID:   "gpt-4",
				},
			}},
		}},
	})
	require.NoError(t, err)

	data, err := json.Marshal(BackupData{
		Version: BackupVersion,
		SystemConfigs: []*BackupSystemConfig{{
			Key:   biz.SystemKeyModelSettings,
			Value: string(settings),
		}},
		Channels: []*BackupChannel{{
			Channel: ent.Channel{ID: localChannel.ID, Name: "Source Channel"},
		}},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeSystemConfigs: true})
	require.NoError(t, err)

	restoredConfig, err := client.System.Query().Where(system.KeyEQ(biz.SystemKeyModelSettings)).Only(ctx)
	require.NoError(t, err)
	var restoredSettings biz.SystemModelSettings
	require.NoError(t, json.Unmarshal([]byte(restoredConfig.Value), &restoredSettings))
	require.Empty(t, restoredSettings.DeveloperSettings[0].Associations)
}

func TestBackupService_Restore(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()
	invalidator := &recordingProviderQuotaInvalidator{}
	service.providerQuotaInvalidator = invalidator

	ch1 := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	existingPrice := createBackupTestChannelModelPrice(t, client, ctx, ch1.ID, "gpt-4")
	m1 := createBackupTestModel(t, client, ctx, "openai", "gpt-4")

	data, err := service.Backup(ctx, BackupOptions{
		IncludeChannels:    true,
		IncludeModels:      true,
		IncludeModelPrices: true,
	})
	require.NoError(t, err)

	channelsBefore, err := client.Channel.Query().Count(ctx)
	require.NoError(t, err)

	modelsBefore, err := client.Model.Query().Count(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:         true,
		IncludeModels:           true,
		IncludeModelPrices:      true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	channelsAfter, err := client.Channel.Query().Count(ctx)
	require.NoError(t, err)

	modelsAfter, err := client.Model.Query().Count(ctx)
	require.NoError(t, err)

	require.Equal(t, channelsBefore, channelsAfter)
	require.Equal(t, modelsBefore, modelsAfter)
	require.Equal(t, []int{ch1.ID}, invalidator.channelIDs)

	restoredChannel, err := client.Channel.Query().
		Where(channel.Name(ch1.Name)).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, ch1.Name, restoredChannel.Name)
	require.Equal(t, ch1.BaseURL, restoredChannel.BaseURL)

	restoredModel, err := client.Model.Query().
		Where(model.ModelID(m1.ModelID)).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, m1.Name, restoredModel.Name)
	require.Equal(t, m1.Developer, restoredModel.Developer)

	restoredPrice, err := client.ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(ch1.ID),
			channelmodelprice.ModelID("gpt-4"),
		).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, existingPrice.ReferenceID, restoredPrice.ReferenceID)
}

func TestBackupService_Restore_ModelPricesOnly(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch1 := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)

	backupData := BackupData{
		Version:  BackupVersion,
		Channels: []*BackupChannel{},
		Models:   []*BackupModel{},
		APIKeys:  []*BackupAPIKey{},
		ChannelModelPrices: []*BackupChannelModelPrice{
			{
				ChannelName: ch1.Name,
				ModelID:     "gpt-4",
				Price: objects.ModelPrice{
					Items: []objects.ModelPriceItem{
						{
							ItemCode: objects.PriceItemCodeUsage,
							Pricing: objects.Pricing{
								Mode: objects.PricingModeFlatFee,
								FlatFee: func() *decimal.Decimal {
									d := decimal.NewFromFloat(1)
									return &d
								}(),
							},
						},
					},
				},
				ReferenceID: "ref-gpt-4",
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:         false,
		IncludeModels:           false,
		IncludeAPIKeys:          false,
		IncludeModelPrices:      true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
		APIKeyConflictStrategy:  ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	restoredPrice, err := client.ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(ch1.ID),
			channelmodelprice.ModelID("gpt-4"),
		).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, "ref-gpt-4", restoredPrice.ReferenceID)
}

func TestBackupService_Restore_RemapChannelIDsInModelSettingsAndAPIKeyProfiles(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	createBackupTestProject(t, client, ctx, "Default", "Default Project")

	oldChannelID := 123
	backupData := BackupData{
		Version: BackupVersion,
		Channels: []*BackupChannel{
			{
				Channel: ent.Channel{
					ID:      oldChannelID,
					Type:    channel.TypeOpenai,
					Name:    "Channel From Backup",
					BaseURL: "https://api.example.com",
					Status:  channel.StatusEnabled,
				},
				Credentials: objects.ChannelCredentials{APIKey: "backup-api-key"},
			},
		},
		Models: []*BackupModel{
			{
				Model: ent.Model{
					Developer: "openai",
					ModelID:   "gpt-4",
					Type:      model.TypeChat,
					Name:      "GPT-4",
					Icon:      "test-icon",
					Group:     "test",
					Settings: &objects.ModelSettings{
						Associations: []*objects.ModelAssociation{
							{
								Type:     "channel_model",
								Priority: 0,
								ChannelModel: &objects.ChannelModelAssociation{
									ChannelID: oldChannelID,
									ModelID:   "gpt-4",
								},
								Regex: &objects.RegexAssociation{
									Pattern: ".*",
									Exclude: []*objects.ExcludeAssociation{
										{ChannelIds: []int{oldChannelID}},
									},
								},
							},
						},
					},
					Status: model.StatusEnabled,
				},
			},
		},
		APIKeys: []*BackupAPIKey{
			{
				APIKey: ent.APIKey{
					Key:    "sk-backup-key",
					Name:   "Backup API Key",
					Type:   "user",
					Status: "enabled",
					Scopes: []string{"chat"},
					Profiles: &objects.APIKeyProfiles{
						ActiveProfile: "default",
						Profiles: []objects.APIKeyProfile{
							{
								Name:       "default",
								ChannelIDs: []int{oldChannelID},
								ModelIDs:   []string{"gpt-4"},
							},
						},
					},
				},
				ProjectName: "Default",
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:         true,
		IncludeModels:           true,
		IncludeAPIKeys:          true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
		APIKeyConflictStrategy:  ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	restoredChannel, err := client.Channel.Query().Where(channel.Name("Channel From Backup")).First(ctx)
	require.NoError(t, err)
	require.NotEqual(t, oldChannelID, restoredChannel.ID)

	restoredModel, err := client.Model.Query().Where(model.ModelID("gpt-4")).First(ctx)
	require.NoError(t, err)
	require.NotNil(t, restoredModel.Settings)
	require.Len(t, restoredModel.Settings.Associations, 1)
	require.NotNil(t, restoredModel.Settings.Associations[0].ChannelModel)
	require.Equal(t, restoredChannel.ID, restoredModel.Settings.Associations[0].ChannelModel.ChannelID)
	require.NotNil(t, restoredModel.Settings.Associations[0].Regex)
	require.Len(t, restoredModel.Settings.Associations[0].Regex.Exclude, 1)
	require.Equal(t, []int{restoredChannel.ID}, restoredModel.Settings.Associations[0].Regex.Exclude[0].ChannelIds)

	restoredKey, err := client.APIKey.Query().Where(apikey.Key("sk-backup-key")).First(ctx)
	require.NoError(t, err)
	require.NotNil(t, restoredKey.Profiles)
	require.Len(t, restoredKey.Profiles.Profiles, 1)
	require.Equal(t, []int{restoredChannel.ID}, restoredKey.Profiles.Profiles[0].ChannelIDs)
}

func TestBackupService_Restore_RemapChannelIDsInProjectProfiles(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	oldChannelID := 456
	backupData := BackupData{
		Version: BackupVersion,
		Projects: []*BackupProject{
			{
				Project: ent.Project{
					Name:        "Project With Profiles",
					Description: "project with channel restrictions",
					Status:      project.StatusActive,
					Profiles: &objects.ProjectProfiles{
						ActiveProfile: "production",
						Profiles: []objects.ProjectProfile{
							{
								Name:        "production",
								ChannelIDs:  []int{oldChannelID},
								ChannelTags: []string{"allowed"},
							},
						},
					},
				},
			},
		},
		Channels: []*BackupChannel{
			{
				Channel: ent.Channel{
					ID:      oldChannelID,
					Type:    channel.TypeOpenai,
					Name:    "Project Channel From Backup",
					BaseURL: "https://api.example.com",
					Status:  channel.StatusEnabled,
				},
				Credentials: objects.ChannelCredentials{APIKey: "backup-api-key"},
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeProjects:         true,
		IncludeChannels:         true,
		ProjectConflictStrategy: ConflictStrategyOverwrite,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	restoredChannel, err := client.Channel.Query().Where(channel.Name("Project Channel From Backup")).First(ctx)
	require.NoError(t, err)
	require.NotEqual(t, oldChannelID, restoredChannel.ID)

	restoredProject, err := client.Project.Query().Where(project.Name("Project With Profiles")).First(ctx)
	require.NoError(t, err)
	require.NotNil(t, restoredProject.Profiles)
	require.Len(t, restoredProject.Profiles.Profiles, 1)
	require.Equal(t, []int{restoredChannel.ID}, restoredProject.Profiles.Profiles[0].ChannelIDs)
}

func TestBackupService_Restore_NewData(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	baseURL := "https://new-api.example.com"
	autoSync := true

	backupData := BackupData{
		Version: BackupVersion,
		Channels: []*BackupChannel{
			{
				Channel: ent.Channel{
					Type:                    channel.TypeOpenai,
					Name:                    "New Channel",
					BaseURL:                 baseURL,
					Status:                  channel.StatusEnabled,
					SupportedModels:         []string{"new-model-1"},
					AutoSyncSupportedModels: autoSync,
					Tags:                    []string{"new"},
					DefaultTestModel:        "new-model-1",
					OrderingWeight:          10,
				},
				Credentials: objects.ChannelCredentials{
					APIKey: "test-api-key",
				},
			},
		},
		Models: []*BackupModel{
			{
				Model: ent.Model{
					Developer: "new-developer",
					ModelID:   "new-model",
					Type:      model.TypeChat,
					Name:      "New Model",
					Icon:      "new-icon",
					Group:     "new-group",
					Status:    model.StatusEnabled,
				},
			},
		},
		ChannelModelPrices: []*BackupChannelModelPrice{
			{
				ChannelName: "New Channel",
				ModelID:     "new-model-1",
				Price: objects.ModelPrice{
					Items: []objects.ModelPriceItem{
						{
							ItemCode: objects.PriceItemCodeUsage,
							Pricing: objects.Pricing{
								Mode: objects.PricingModeFlatFee,
								FlatFee: func() *decimal.Decimal {
									d := decimal.NewFromFloat(1)
									return &d
								}(),
							},
						},
					},
				},
				ReferenceID: "ref-new-model-1",
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:         true,
		IncludeModels:           true,
		IncludeModelPrices:      true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	channels, err := client.Channel.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, channels)

	models, err := client.Model.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, models)

	newChannel, err := client.Channel.Query().
		Where(channel.Name("New Channel")).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, "New Channel", newChannel.Name)

	newModel, err := client.Model.Query().
		Where(model.ModelID("new-model")).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, "New Model", newModel.Name)

	priceCount, err := client.ChannelModelPrice.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, priceCount)
}

func TestBackupService_Restore_UpdateExisting(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch1 := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	m1 := createBackupTestModel(t, client, ctx, "openai", "gpt-4")

	baseURL := "https://updated-api.example.com"
	autoSync := false

	backupData := BackupData{
		Version: BackupVersion,
		Channels: []*BackupChannel{
			{
				Channel: ent.Channel{
					Type:                    ch1.Type,
					Name:                    ch1.Name,
					BaseURL:                 baseURL,
					Status:                  channel.StatusDisabled,
					SupportedModels:         []string{"updated-model"},
					AutoSyncSupportedModels: autoSync,
					Tags:                    []string{"updated"},
					DefaultTestModel:        "updated-model",
					OrderingWeight:          20,
				},
				Credentials: objects.ChannelCredentials{
					APIKey: "test-api-key",
				},
			},
		},
		Models: []*BackupModel{
			{
				Model: ent.Model{
					Developer: m1.Developer,
					ModelID:   m1.ModelID,
					Type:      m1.Type,
					Name:      "Updated Model",
					Icon:      "updated-icon",
					Group:     "updated-group",
					Status:    model.StatusDisabled,
				},
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:         true,
		IncludeModels:           true,
		IncludeModelPrices:      true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	channels, err := client.Channel.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, channels)

	models, err := client.Model.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, models)

	updatedChannel, err := client.Channel.Query().
		Where(channel.Name(ch1.Name)).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, ch1.Name, updatedChannel.Name)
	require.Equal(t, "https://updated-api.example.com", updatedChannel.BaseURL)
	require.Equal(t, channel.StatusDisabled, updatedChannel.Status)
	require.Equal(t, []string{"updated-model"}, updatedChannel.SupportedModels)
	require.Equal(t, false, updatedChannel.AutoSyncSupportedModels)
	require.Equal(t, []string{"updated"}, updatedChannel.Tags)
	require.Equal(t, "updated-model", updatedChannel.DefaultTestModel)
	require.Equal(t, 20, updatedChannel.OrderingWeight)

	updatedModel, err := client.Model.Query().
		Where(model.ModelID(m1.ModelID)).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, "Updated Model", updatedModel.Name)
	require.Equal(t, model.StatusDisabled, updatedModel.Status)
	require.Equal(t, "updated-icon", updatedModel.Icon)
	require.Equal(t, "updated-group", updatedModel.Group)
}

func TestBackupService_Restore_InvalidJSON(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	invalidData := []byte("invalid json")

	err := service.Restore(ctx, invalidData, RestoreOptions{
		IncludeChannels:         true,
		IncludeModels:           true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
	})
	require.Error(t, err)
}

func TestBackupService_Restore_InvalidVersion(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	backupData := BackupData{
		Version:  "invalid-version",
		Channels: []*BackupChannel{},
		Models:   []*BackupModel{},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:         true,
		IncludeModels:           true,
		ChannelConflictStrategy: ConflictStrategyOverwrite,
		ModelConflictStrategy:   ConflictStrategyOverwrite,
	})
	require.Error(t, err)
}

func TestBackupService_Restore_ModelPriceConflictStrategy_Skip(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch1 := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	existingPrice := createBackupTestChannelModelPrice(t, client, ctx, ch1.ID, "gpt-4")

	newPricePerUnit := decimal.NewFromFloat(999.99)
	backupData := BackupData{
		Version:  BackupVersion,
		Channels: []*BackupChannel{},
		Models:   []*BackupModel{},
		ChannelModelPrices: []*BackupChannelModelPrice{
			{
				ChannelName: ch1.Name,
				ModelID:     "gpt-4",
				Price: objects.ModelPrice{
					Items: []objects.ModelPriceItem{
						{
							ItemCode: objects.PriceItemCodeUsage,
							Pricing: objects.Pricing{
								Mode:         objects.PricingModeUsagePerUnit,
								UsagePerUnit: &newPricePerUnit,
							},
						},
					},
				},
				ReferenceID: "new-ref-id",
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:            false,
		IncludeModels:              false,
		IncludeAPIKeys:             false,
		IncludeModelPrices:         true,
		ModelPriceConflictStrategy: ConflictStrategySkip,
	})
	require.NoError(t, err)

	restoredPrice, err := client.ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(ch1.ID),
			channelmodelprice.ModelID("gpt-4"),
		).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, existingPrice.ReferenceID, restoredPrice.ReferenceID)
}

func TestBackupService_Restore_ModelPriceConflictStrategy_Overwrite(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch1 := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	_ = createBackupTestChannelModelPrice(t, client, ctx, ch1.ID, "gpt-4")

	newPricePerUnit := decimal.NewFromFloat(999.99)
	backupData := BackupData{
		Version:  BackupVersion,
		Channels: []*BackupChannel{},
		Models:   []*BackupModel{},
		ChannelModelPrices: []*BackupChannelModelPrice{
			{
				ChannelName: ch1.Name,
				ModelID:     "gpt-4",
				Price: objects.ModelPrice{
					Items: []objects.ModelPriceItem{
						{
							ItemCode: objects.PriceItemCodeUsage,
							Pricing: objects.Pricing{
								Mode:         objects.PricingModeUsagePerUnit,
								UsagePerUnit: &newPricePerUnit,
							},
						},
					},
				},
				ReferenceID: "overwritten-ref-id",
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:            false,
		IncludeModels:              false,
		IncludeAPIKeys:             false,
		IncludeModelPrices:         true,
		ModelPriceConflictStrategy: ConflictStrategyOverwrite,
	})
	require.NoError(t, err)

	restoredPrice, err := client.ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(ch1.ID),
			channelmodelprice.ModelID("gpt-4"),
		).
		First(ctx)
	require.NoError(t, err)
	require.Equal(t, "overwritten-ref-id", restoredPrice.ReferenceID)
}

func TestBackupService_Restore_ModelPriceConflictStrategy_Error(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch1 := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	_ = createBackupTestChannelModelPrice(t, client, ctx, ch1.ID, "gpt-4")

	newPricePerUnit := decimal.NewFromFloat(999.99)
	backupData := BackupData{
		Version:  BackupVersion,
		Channels: []*BackupChannel{},
		Models:   []*BackupModel{},
		ChannelModelPrices: []*BackupChannelModelPrice{
			{
				ChannelName: ch1.Name,
				ModelID:     "gpt-4",
				Price: objects.ModelPrice{
					Items: []objects.ModelPriceItem{
						{
							ItemCode: objects.PriceItemCodeUsage,
							Pricing: objects.Pricing{
								Mode:         objects.PricingModeUsagePerUnit,
								UsagePerUnit: &newPricePerUnit,
							},
						},
					},
				},
				ReferenceID: "new-ref-id",
			},
		},
	}

	data, err := json.MarshalIndent(backupData, "", "  ")
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeChannels:            false,
		IncludeModels:              false,
		IncludeAPIKeys:             false,
		IncludeModelPrices:         true,
		ModelPriceConflictStrategy: ConflictStrategyError,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel model price already exists")
}

func TestBackupService_Restore_UsageStats(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	_, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeAPIKeys:    true,
		IncludeUsageStats: true,
	})
	require.NoError(t, err)
	var backupData BackupData
	require.NoError(t, json.Unmarshal(data, &backupData))
	backupData.UsageLogs[0].RequestedServiceTier = " PRIORITY "
	backupData.UsageLogs[0].AppliedServiceTier = " PRIORITY "
	backupData.UsageLogs[0].ServiceTier = " PRIORITY "
	data, err = json.Marshal(backupData)
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)

	_, err = client.Request.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats: true,
	})
	require.NoError(t, err)

	requestsCount, err := client.Request.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, requestsCount)

	usageLogs, err := client.UsageLog.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, usageLogs, 1)
	require.Equal(t, int64(150), usageLogs[0].TotalTokens)
	require.Equal(t, int64(20), usageLogs[0].PromptCachedTokens)
	require.NotZero(t, usageLogs[0].RequestExecutionID)
	require.Equal(t, "priority", usageLogs[0].RequestedServiceTier)
	require.Equal(t, "priority", usageLogs[0].AppliedServiceTier)
	require.Equal(t, "priority", usageLogs[0].ServiceTier)
	require.NotNil(t, usageLogs[0].TotalCost)
	require.Equal(t, *usage.TotalCost, *usageLogs[0].TotalCost)
	require.Equal(t, "price-ref", usageLogs[0].CostPriceReferenceID)

	restoredRequest, err := client.Request.Get(ctx, usageLogs[0].RequestID)
	require.NoError(t, err)
	require.Equal(t, "gpt-4", restoredRequest.ModelID)
	require.Equal(t, proj.ID, restoredRequest.ProjectID)
	require.Equal(t, ch.ID, restoredRequest.ChannelID)
	require.Equal(t, ak.ID, restoredRequest.APIKeyID)
	require.JSONEq(t, `{}`, string(restoredRequest.RequestBody))

	restoredExecution, err := usageLogs[0].QueryRequestExecution().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, restoredRequest.ID, restoredExecution.RequestID)
	require.Equal(t, ch.ID, restoredExecution.ChannelID)
	require.Equal(t, "priority", restoredExecution.RequestedServiceTier)
	require.Equal(t, "fast", restoredExecution.SpeedMode)
	require.Equal(t, "Primary Account", restoredExecution.ChannelAPIKeyName)
	require.Equal(t, "1234", restoredExecution.ChannelAPIKeySuffix)
	require.Equal(t, []string{"Authorization"}, restoredExecution.ChannelAPIKeyHeaders)
	require.JSONEq(t, `{}`, string(restoredExecution.RequestBody))

	expectedExecution := *backupData.RequestExecutions[0]
	expectedExecution.ID = restoredExecution.ID
	expectedExecution.ProjectID = restoredExecution.ProjectID
	expectedExecution.RequestID = restoredExecution.RequestID
	expectedExecution.ChannelID = restoredExecution.ChannelID
	expectedExecution.DataStorageID = restoredExecution.DataStorageID
	expectedExecution.ProjectName = ""
	expectedExecution.ChannelName = ""
	expectedExecution.ChannelDeletedAt = 0
	expectedExecution.DataStorageName = ""
	expectedExecution.DataStorageDeletedAt = 0
	require.Equal(t, &expectedExecution, backupRequestExecution(restoredExecution, nil, nil, nil, true))
	executionCountBeforeSecondRestore, err := client.RequestExecution.Query().Count(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats: true,
	})
	require.NoError(t, err)

	requestsCount, err = client.Request.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, requestsCount)

	usageLogsAfterSecondRestore, err := client.UsageLog.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, usageLogsAfterSecondRestore, 1)
	require.Equal(t, restoredRequest.ID, usageLogsAfterSecondRestore[0].RequestID)
	executionsAfterSecondRestore, err := client.RequestExecution.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, executionCountBeforeSecondRestore, executionsAfterSecondRestore)
}

func TestBackupService_Restore_UsageStatsOnlyReusesUnchangedSourceRecords(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	req, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)
	execution, err := usage.QueryRequestExecution().Only(ctx)
	require.NoError(t, err)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeAPIKeys:    true,
		IncludeUsageStats: true,
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeUsageStats: true})
	require.NoError(t, err)

	require.Equal(t, 1, mustCountRequests(t, client, ctx))
	require.Equal(t, 1, mustCountRequestExecutions(t, client, ctx))
	require.Equal(t, 1, mustCountUsageLogs(t, client, ctx))
	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, req.ID, restoredUsage.RequestID)
	require.Equal(t, execution.ID, restoredUsage.RequestExecutionID)
}

func TestBackupService_Restore_UsageStatsOnlyReusesParentsWhenUsageLogWasDeleted(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	req, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)
	execution, err := usage.QueryRequestExecution().Only(ctx)
	require.NoError(t, err)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeAPIKeys:    true,
		IncludeUsageStats: true,
	})
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)
	err = service.Restore(ctx, data, RestoreOptions{IncludeUsageStats: true})
	require.NoError(t, err)

	require.Equal(t, 1, mustCountRequests(t, client, ctx))
	require.Equal(t, 1, mustCountRequestExecutions(t, client, ctx))
	require.Equal(t, 1, mustCountUsageLogs(t, client, ctx))
	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, req.ID, restoredUsage.RequestID)
	require.Equal(t, execution.ID, restoredUsage.RequestExecutionID)
}

func TestBackupService_Restore_UsageStatsOnlyDoesNotReuseAmbiguousRequestCandidate(t *testing.T) {
	sourceClient, sourceService, sourceCtx := setupBackupTestWithDSN(t, "file:usage-identity-source?mode=memory&_fk=1")
	defer sourceClient.Close()
	sourceUser, _ := sourceClient.User.Query().First(sourceCtx)
	sourceProject := createBackupTestProject(t, sourceClient, sourceCtx, "Project1", "Test Project")
	sourceChannel := createBackupTestChannel(t, sourceClient, sourceCtx, "Channel 1", channel.TypeOpenai)
	sourceAPIKey := createBackupTestAPIKey(t, sourceClient, sourceCtx, sourceUser, sourceProject, "API Key 1", "sk-test-key-1")
	sourceRequest, _ := createBackupTestUsage(t, sourceClient, sourceCtx, sourceProject, sourceChannel, sourceAPIKey)
	data, err := sourceService.Backup(sourceCtx, BackupOptions{
		IncludeAPIKeys:    true,
		IncludeUsageStats: true,
	})
	require.NoError(t, err)

	targetClient, targetService, targetCtx := setupBackupTestWithDSN(t, "file:usage-identity-target?mode=memory&_fk=1")
	defer targetClient.Close()
	targetUser, _ := targetClient.User.Query().First(targetCtx)
	targetProject := createBackupTestProject(t, targetClient, targetCtx, sourceProject.Name, "Target Project")
	targetChannel := createBackupTestChannel(t, targetClient, targetCtx, sourceChannel.Name, channel.TypeOpenai)
	targetAPIKey := createBackupTestAPIKey(t, targetClient, targetCtx, targetUser, targetProject, "API Key 1", "sk-test-key-1")

	_, err = targetClient.Request.Create().
		SetProjectID(targetProject.ID).
		SetAPIKeyID(targetAPIKey.ID).
		SetChannelID(targetChannel.ID).
		SetSource("api").
		SetModelID("unrelated-model").
		SetFormat(sourceRequest.Format).
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus("completed").
		SetStream(sourceRequest.Stream).
		SetClientIP("").
		Save(targetCtx)
	require.NoError(t, err)

	ambiguousIDs := make(map[int]struct{}, 2)
	for range 2 {
		candidate, err := targetClient.Request.Create().
			SetCreatedAt(sourceRequest.CreatedAt).
			SetUpdatedAt(sourceRequest.UpdatedAt).
			SetProjectID(targetProject.ID).
			SetAPIKeyID(targetAPIKey.ID).
			SetChannelID(targetChannel.ID).
			SetSource(sourceRequest.Source).
			SetModelID(sourceRequest.ModelID).
			SetFormat(sourceRequest.Format).
			SetRequestBody(objects.JSONRawMessage(`{}`)).
			SetStatus("processing").
			SetStream(sourceRequest.Stream).
			SetClientIP("").
			Save(targetCtx)
		require.NoError(t, err)
		ambiguousIDs[candidate.ID] = struct{}{}
	}

	err = targetService.Restore(targetCtx, data, RestoreOptions{IncludeUsageStats: true})
	require.NoError(t, err)
	require.Equal(t, 4, mustCountRequests(t, targetClient, targetCtx))
	restoredUsage, err := targetClient.UsageLog.Query().Only(targetCtx)
	require.NoError(t, err)
	_, ambiguous := ambiguousIDs[restoredUsage.RequestID]
	require.False(t, ambiguous)
}

func TestBackupService_Restore_FullRequestLogsHydratesExternalStorageAndRestoresInline(t *testing.T) {
	sourceClient, sourceService, sourceCtx := setupBackupTestWithDSN(t, "file:external-source?mode=memory&_fk=1")
	defer sourceClient.Close()
	sourceStorageService := attachBackupTestDataStorageService(t, sourceClient, sourceService)

	sourceUser, _ := sourceClient.User.Query().First(sourceCtx)
	sourceProject := createBackupTestProject(t, sourceClient, sourceCtx, "Project1", "Test Project")
	sourceChannel := createBackupTestChannel(t, sourceClient, sourceCtx, "Channel 1", channel.TypeOpenai)
	sourceAPIKey := createBackupTestAPIKey(t, sourceClient, sourceCtx, sourceUser, sourceProject, "API Key 1", "sk-test-key-1")
	sourceStorage := createBackupTestFSDataStorage(t, sourceClient, sourceCtx, "request-storage", t.TempDir())

	sourceRequest, err := sourceClient.Request.Create().
		SetProjectID(sourceProject.ID).
		SetAPIKeyID(sourceAPIKey.ID).
		SetChannelID(sourceChannel.ID).
		SetDataStorageID(sourceStorage.ID).
		SetSource("api").
		SetModelID("gpt-4").
		SetFormat("openai/chat_completions").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus("completed").
		SetStream(true).
		SetClientIP("127.0.0.1").
		SetContentSaved(true).
		SetContentStorageID(sourceStorage.ID).
		SetContentStorageKey("/old/request/audio.mp3").
		SetContentSavedAt(time.Now().UTC()).
		Save(sourceCtx)
	require.NoError(t, err)
	sourceExecution, err := sourceClient.RequestExecution.Create().
		SetProjectID(sourceProject.ID).
		SetRequestID(sourceRequest.ID).
		SetChannelID(sourceChannel.ID).
		SetDataStorageID(sourceStorage.ID).
		SetSource("api").
		SetModelID("gpt-4").
		SetFormat("openai/chat_completions").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus("completed").
		SetStream(true).
		Save(sourceCtx)
	require.NoError(t, err)
	_, err = sourceClient.UsageLog.Create().
		SetRequestID(sourceRequest.ID).
		SetRequestExecutionID(sourceExecution.ID).
		SetAPIKeyID(sourceAPIKey.ID).
		SetProjectID(sourceProject.ID).
		SetChannelID(sourceChannel.ID).
		SetModelID("gpt-4").
		SetPromptTokens(10).
		SetCompletionTokens(2).
		SetTotalTokens(12).
		SetSource("api").
		SetFormat("openai/chat_completions").
		Save(sourceCtx)
	require.NoError(t, err)

	requestBody := []byte(`{"model":"external-request"}`)
	responseBody := []byte(`{"id":"external-response"}`)
	responseChunks := []byte(`[{"delta":"external-request-chunk"}]`)
	executionRequestBody := []byte(`{"model":"external-execution"}`)
	executionResponseBody := []byte(`{"id":"external-execution-response"}`)
	executionResponseChunks := []byte(`[{"delta":"external-execution-chunk"}]`)
	require.NoError(t, sourceStorageService.SaveData(sourceCtx, sourceStorage, biz.GenerateRequestBodyKey(sourceProject.ID, sourceRequest.ID), requestBody))
	require.NoError(t, sourceStorageService.SaveData(sourceCtx, sourceStorage, biz.GenerateResponseBodyKey(sourceProject.ID, sourceRequest.ID), responseBody))
	require.NoError(t, sourceStorageService.SaveData(sourceCtx, sourceStorage, biz.GenerateResponseChunksKey(sourceProject.ID, sourceRequest.ID), responseChunks))
	require.NoError(t, sourceStorageService.SaveData(sourceCtx, sourceStorage, biz.GenerateExecutionRequestBodyKey(sourceProject.ID, sourceRequest.ID, sourceExecution.ID), executionRequestBody))
	require.NoError(t, sourceStorageService.SaveData(sourceCtx, sourceStorage, biz.GenerateExecutionResponseBodyKey(sourceProject.ID, sourceRequest.ID, sourceExecution.ID), executionResponseBody))
	require.NoError(t, sourceStorageService.SaveData(sourceCtx, sourceStorage, biz.GenerateExecutionResponseChunksKey(sourceProject.ID, sourceRequest.ID, sourceExecution.ID), executionResponseChunks))

	data, err := sourceService.Backup(sourceCtx, BackupOptions{
		IncludeAPIKeys:     true,
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)
	var backupData BackupData
	require.NoError(t, json.Unmarshal(data, &backupData))
	require.Len(t, backupData.UsageRequests, 1)
	require.JSONEq(t, string(requestBody), string(backupData.UsageRequests[0].RequestBody))
	require.JSONEq(t, string(responseBody), string(backupData.UsageRequests[0].ResponseBody))
	require.JSONEq(t, string(responseChunks), mustJSON(t, backupData.UsageRequests[0].ResponseChunks))
	require.Len(t, backupData.RequestExecutions, 1)
	require.JSONEq(t, string(executionRequestBody), string(backupData.RequestExecutions[0].RequestBody))
	require.JSONEq(t, string(executionResponseBody), string(backupData.RequestExecutions[0].ResponseBody))
	require.JSONEq(t, string(executionResponseChunks), mustJSON(t, backupData.RequestExecutions[0].ResponseChunks))
	require.False(t, backupData.UsageRequests[0].ContentSaved)
	require.Nil(t, backupData.UsageRequests[0].ContentStorageID)
	require.Nil(t, backupData.UsageRequests[0].ContentStorageKey)
	require.Nil(t, backupData.UsageRequests[0].ContentSavedAt)

	_, err = sourceClient.UsageLog.Delete().Exec(sourceCtx)
	require.NoError(t, err)
	err = sourceService.Restore(sourceCtx, data, RestoreOptions{
		IncludeRequestLogs: true,
		IncludeUsageStats:  true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, mustCountRequests(t, sourceClient, sourceCtx))
	require.Equal(t, 1, mustCountRequestExecutions(t, sourceClient, sourceCtx))
	restoredSourceUsage, err := sourceClient.UsageLog.Query().Only(sourceCtx)
	require.NoError(t, err)
	require.Equal(t, sourceRequest.ID, restoredSourceUsage.RequestID)
	require.Equal(t, sourceExecution.ID, restoredSourceUsage.RequestExecutionID)

	targetClient, targetService, targetCtx := setupBackupTestWithDSN(t, "file:external-target?mode=memory&_fk=1")
	defer targetClient.Close()
	targetUser, _ := targetClient.User.Query().First(targetCtx)
	targetProject := createBackupTestProject(t, targetClient, targetCtx, sourceProject.Name, "Target Project")
	targetChannel := createBackupTestChannel(t, targetClient, targetCtx, sourceChannel.Name, channel.TypeOpenai)
	targetAPIKey := createBackupTestAPIKey(t, targetClient, targetCtx, targetUser, targetProject, "API Key 1", sourceAPIKey.Key)
	_ = createBackupTestFSDataStorage(t, targetClient, targetCtx, sourceStorage.Name, t.TempDir())
	createBackupTestUsage(t, targetClient, targetCtx, targetProject, targetChannel, targetAPIKey)

	err = targetService.Restore(targetCtx, data, RestoreOptions{
		IncludeRequestLogs: true,
		IncludeUsageStats:  true,
	})
	require.NoError(t, err)
	restoredUsage, err := targetClient.UsageLog.Query().
		Where(usagelog.TotalTokensEQ(12)).
		Only(targetCtx)
	require.NoError(t, err)
	restoredRequest, err := targetClient.Request.Get(targetCtx, restoredUsage.RequestID)
	require.NoError(t, err)
	restoredExecution, err := restoredUsage.QueryRequestExecution().Only(targetCtx)
	require.NoError(t, err)
	require.Zero(t, restoredRequest.DataStorageID)
	require.Zero(t, restoredExecution.DataStorageID)
	require.False(t, restoredRequest.ContentSaved)
	require.Nil(t, restoredRequest.ContentStorageID)
	require.Nil(t, restoredRequest.ContentStorageKey)
	require.Nil(t, restoredRequest.ContentSavedAt)
	require.JSONEq(t, string(requestBody), string(restoredRequest.RequestBody))
	require.JSONEq(t, string(responseBody), string(restoredRequest.ResponseBody))
	require.JSONEq(t, string(responseChunks), mustJSON(t, restoredRequest.ResponseChunks))
	require.JSONEq(t, string(executionRequestBody), string(restoredExecution.RequestBody))
	require.JSONEq(t, string(executionResponseBody), string(restoredExecution.ResponseBody))
	require.JSONEq(t, string(executionResponseChunks), mustJSON(t, restoredExecution.ResponseChunks))
}

func TestBackupService_Backup_UsageStatsOnlyDoesNotReadExternalRequestContent(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()
	_ = attachBackupTestDataStorageService(t, client, service)

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	directory := t.TempDir()
	storage := createBackupTestFSDataStorage(t, client, ctx, "request-storage", directory)
	createBackupTestUsageWithDataStorage(t, client, ctx, proj, ch, ak, storage)
	require.NoError(t, os.RemoveAll(directory))

	data, err := service.Backup(ctx, BackupOptions{IncludeUsageStats: true})
	require.NoError(t, err)
	require.NotContains(t, string(data), "sensitive-header")
	require.NotContains(t, string(data), "sensitive-tenant")

	var backupData BackupData
	require.NoError(t, json.Unmarshal(data, &backupData))
	require.Len(t, backupData.RequestExecutions, 1)
	require.JSONEq(t, `{}`, string(backupData.RequestExecutions[0].RequestBody))
}

func TestBackupService_Restore_LegacyModelPriceNormalizesVariantCodes(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	firstTierBound := int64(1)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{
			Mode: objects.PricingModeTiered,
			UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
				{UpTo: &firstTierBound, PricePerUnit: decimal.NewFromFloat(0.01)},
				{UpTo: nil, PricePerUnit: decimal.NewFromFloat(0.02)},
			}},
		},
	}, {
		ItemCode: objects.PriceItemCodeWriteCachedTokens,
		Pricing: objects.Pricing{
			Mode:         objects.PricingModeUsagePerUnit,
			UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.03)),
		},
		PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{
			{
				VariantCode: objects.PromptWriteCacheVariantCode5Min,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.04)),
				},
			},
			{
				VariantCode: objects.PromptWriteCacheVariantCode5Min,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.99)),
				},
			},
			{
				VariantCode: objects.PromptWriteCacheVariantCode1Hour,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.05)),
				},
			},
			{
				VariantCode: objects.PromptWriteCacheVariantCode1Hour,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.98)),
				},
			},
		},
	}}}
	data, err := json.Marshal(BackupData{
		Version: BackupVersionV5,
		ChannelModelPrices: []*BackupChannelModelPrice{{
			ChannelName: ch.Name,
			ModelID:     "gpt-4",
			Price:       price,
			ReferenceID: "legacy-price",
		}},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeModelPrices: true})
	require.NoError(t, err)
	restored, err := client.ChannelModelPrice.Query().Only(ctx)
	require.NoError(t, err)
	require.NoError(t, restored.Price.Validate())
	require.Len(t, restored.Price.Items, 2)
	restoredUpTo := restored.Price.Items[0].Pricing.UsageTiered.Tiers[0].UpTo
	require.NotNil(t, restoredUpTo)
	require.EqualValues(t, firstTierBound, *restoredUpTo)
	require.Nil(t, restored.Price.Items[0].Pricing.UsageTiered.Tiers[1].UpTo)
	require.Len(t, restored.Price.Items[1].PromptWriteCacheVariants, 2)
	require.Equal(t, objects.PromptWriteCacheVariantCode5Min, restored.Price.Items[1].PromptWriteCacheVariants[0].VariantCode)
	require.True(t, restored.Price.Items[1].PromptWriteCacheVariants[0].Pricing.UsagePerUnit.Equal(decimal.NewFromFloat(0.04)))
	require.Equal(t, objects.PromptWriteCacheVariantCode1Hour, restored.Price.Items[1].PromptWriteCacheVariants[1].VariantCode)
	require.True(t, restored.Price.Items[1].PromptWriteCacheVariants[1].Pricing.UsagePerUnit.Equal(decimal.NewFromFloat(0.05)))
}

func TestBackupService_Restore_LegacyModelPriceRejectsInvalidTierBounds(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	zero := int64(0)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{
			Mode: objects.PricingModeTiered,
			UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
				{UpTo: &zero, PricePerUnit: decimal.NewFromFloat(0.01)},
				{UpTo: nil, PricePerUnit: decimal.NewFromFloat(0.02)},
			}},
		},
	}}}
	data, err := json.Marshal(BackupData{
		Version: BackupVersionV4,
		ChannelModelPrices: []*BackupChannelModelPrice{{
			ChannelName: ch.Name,
			ModelID:     "gpt-4",
			Price:       price,
			ReferenceID: "legacy-price",
		}},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeModelPrices: true})
	require.ErrorContains(t, err, "repair the legacy backup before restoring")
	count, err := client.ChannelModelPrice.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBackupService_Restore_LegacyModelPriceRejectsNonIncreasingTierBounds(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	firstTierBound := int64(2)
	secondTierBound := int64(1)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{
			Mode: objects.PricingModeTiered,
			UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
				{UpTo: &firstTierBound, PricePerUnit: decimal.NewFromFloat(0.01)},
				{UpTo: &secondTierBound, PricePerUnit: decimal.NewFromFloat(0.02)},
				{UpTo: nil, PricePerUnit: decimal.NewFromFloat(0.03)},
			}},
		},
	}}}
	data, err := json.Marshal(BackupData{
		Version: BackupVersionV4,
		ChannelModelPrices: []*BackupChannelModelPrice{{
			ChannelName: ch.Name,
			ModelID:     "gpt-4",
			Price:       price,
			ReferenceID: "legacy-price",
		}},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeModelPrices: true})
	require.ErrorContains(t, err, "tiers[1].upTo must be greater than tiers[0].upTo")
	count, err := client.ChannelModelPrice.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBackupService_Restore_LegacyModelPriceRejectsDuplicateItemCodes(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{
			Mode:         objects.PricingModeUsagePerUnit,
			UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.01)),
		},
	}, {
		ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{
			Mode:         objects.PricingModeUsagePerUnit,
			UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.02)),
		},
	}}}
	data, err := json.Marshal(BackupData{
		Version: BackupVersionV4,
		ChannelModelPrices: []*BackupChannelModelPrice{{
			ChannelName: ch.Name,
			ModelID:     "gpt-4",
			Price:       price,
			ReferenceID: "legacy-price",
		}},
	})
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{IncludeModelPrices: true})
	require.ErrorContains(t, err, "historical billing summed duplicate items")
	require.ErrorContains(t, err, "combine the items in the backup")
	count, err := client.ChannelModelPrice.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBackupService_Restore_CurrentModelPriceRejectsDuplicateCodes(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	for _, test := range []struct {
		name  string
		price objects.ModelPrice
		want  string
	}{
		{
			name: "items",
			price: objects.ModelPrice{Items: []objects.ModelPriceItem{{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.01)),
				},
			}, {
				ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.02)),
				},
			}}},
			want: "duplicate itemCode",
		},
		{
			name: "variants",
			price: objects.ModelPrice{Items: []objects.ModelPriceItem{{
				ItemCode: objects.PriceItemCodeWriteCachedTokens,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.01)),
				},
				PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{
					{
						VariantCode: objects.PromptWriteCacheVariantCode5Min,
						Pricing: objects.Pricing{
							Mode:         objects.PricingModeUsagePerUnit,
							UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.02)),
						},
					},
					{
						VariantCode: objects.PromptWriteCacheVariantCode5Min,
						Pricing: objects.Pricing{
							Mode:         objects.PricingModeUsagePerUnit,
							UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.03)),
						},
					},
				},
			}}},
			want: "duplicate variantCode",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(BackupData{
				Version: BackupVersion,
				ChannelModelPrices: []*BackupChannelModelPrice{{
					ChannelName: ch.Name,
					ModelID:     "gpt-4-" + test.name,
					Price:       test.price,
					ReferenceID: "current-price",
				}},
			})
			require.NoError(t, err)

			err = service.Restore(ctx, data, RestoreOptions{IncludeModelPrices: true})
			require.ErrorContains(t, err, test.want)
		})
	}

	count, err := client.ChannelModelPrice.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func mustCountRequests(t *testing.T, client *ent.Client, ctx context.Context) int {
	t.Helper()
	count, err := client.Request.Query().Count(ctx)
	require.NoError(t, err)
	return count
}

func mustCountRequestExecutions(t *testing.T, client *ent.Client, ctx context.Context) int {
	t.Helper()
	count, err := client.RequestExecution.Query().Count(ctx)
	require.NoError(t, err)
	return count
}

func mustCountUsageLogs(t *testing.T, client *ent.Client, ctx context.Context) int {
	t.Helper()
	count, err := client.UsageLog.Query().Count(ctx)
	require.NoError(t, err)
	return count
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestBackupService_Restore_UsageStatsPreservesResolvableRequestExecution(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	_, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, usage.RequestExecutionID, restoredUsage.RequestExecutionID)
	require.Equal(t, "priority", restoredUsage.RequestedServiceTier)
	require.Equal(t, "priority", restoredUsage.AppliedServiceTier)
	require.Equal(t, "priority", restoredUsage.ServiceTier)

	restoredExecution, err := restoredUsage.QueryRequestExecution().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, usage.RequestExecutionID, restoredExecution.ID)

	inverseUsage, err := restoredExecution.QueryUsageLog().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, restoredUsage.ID, inverseUsage.ID)
}

func TestBackupService_Restore_UsageStatsPreservesExecutionAfterChannelDeletion(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	_, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)
	originalExecution, err := usage.QueryRequestExecution().Only(ctx)
	require.NoError(t, err)

	err = client.Channel.DeleteOneID(ch.ID).Exec(ctx)
	require.NoError(t, err)

	usage, err = client.UsageLog.Get(ctx, usage.ID)
	require.NoError(t, err)
	require.Equal(t, ch.ID, usage.ChannelID)
	originalExecution, err = client.RequestExecution.Get(ctx, originalExecution.ID)
	require.NoError(t, err)
	require.Equal(t, ch.ID, originalExecution.ChannelID)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, originalExecution.ID, restoredUsage.RequestExecutionID)
	require.Zero(t, restoredUsage.ChannelID)
}

func TestBackupService_Restore_UsageStatsPreservesExecutionAfterDataStorageDeletion(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	dataStorage, err := client.DataStorage.Create().
		SetName("Deleted Storage").
		SetDescription("Test storage").
		SetPrimary(false).
		SetType(datastorage.TypeDatabase).
		SetSettings(&objects.DataStorageSettings{}).
		SetStatus(datastorage.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	_, usage := createBackupTestUsageWithDataStorage(t, client, ctx, proj, ch, ak, dataStorage)
	originalExecution, err := usage.QueryRequestExecution().Only(ctx)
	require.NoError(t, err)

	err = client.DataStorage.DeleteOneID(dataStorage.ID).Exec(ctx)
	require.NoError(t, err)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, originalExecution.ID, restoredUsage.RequestExecutionID)
}

func TestBackupService_Restore_UsageStatsDoesNotReuseDeletedChannelIDAcrossDatabases(t *testing.T) {
	sourceClient, sourceService, sourceCtx := setupBackupTestWithDSN(t, "file:backup-source?mode=memory&_fk=1")
	defer sourceClient.Close()

	sourceUser, _ := sourceClient.User.Query().First(sourceCtx)
	sourceProject := createBackupTestProject(t, sourceClient, sourceCtx, "Project1", "Test Project")
	sourceChannel := createBackupTestChannel(t, sourceClient, sourceCtx, "Deleted Source Channel", channel.TypeOpenai)
	sourceAPIKey := createBackupTestAPIKey(t, sourceClient, sourceCtx, sourceUser, sourceProject, "API Key 1", "sk-test-key-1")
	createBackupTestUsage(t, sourceClient, sourceCtx, sourceProject, sourceChannel, sourceAPIKey)

	err := sourceClient.Channel.DeleteOneID(sourceChannel.ID).Exec(sourceCtx)
	require.NoError(t, err)

	data, err := sourceService.Backup(sourceCtx, BackupOptions{
		IncludeProjects:    true,
		IncludeAPIKeys:     true,
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	var backupData BackupData
	require.NoError(t, json.Unmarshal(data, &backupData))
	require.Equal(t, sourceChannel.Name, backupData.UsageRequests[0].ChannelName)
	require.NotZero(t, backupData.UsageRequests[0].ChannelDeletedAt)
	require.Equal(t, sourceChannel.Name, backupData.UsageLogs[0].ChannelName)
	require.NotZero(t, backupData.UsageLogs[0].ChannelDeletedAt)
	require.Len(t, backupData.RequestExecutions, 1)
	require.Equal(t, sourceChannel.Name, backupData.RequestExecutions[0].ChannelName)
	require.NotZero(t, backupData.RequestExecutions[0].ChannelDeletedAt)

	targetClient, targetService, targetCtx := setupBackupTestWithDSN(t, "file:backup-target?mode=memory&_fk=1")
	defer targetClient.Close()

	unrelatedChannel := createBackupTestChannel(t, targetClient, targetCtx, sourceChannel.Name, channel.TypeOpenai)
	require.Equal(t, sourceChannel.ID, unrelatedChannel.ID)

	err = targetService.Restore(targetCtx, data, RestoreOptions{
		IncludeProjects:    true,
		IncludeAPIKeys:     true,
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	restoredRequest, err := targetClient.Request.Query().Only(targetCtx)
	require.NoError(t, err)
	require.Zero(t, restoredRequest.ChannelID)

	restoredUsage, err := targetClient.UsageLog.Query().Only(targetCtx)
	require.NoError(t, err)
	require.Zero(t, restoredUsage.ChannelID)
	require.NotZero(t, restoredUsage.RequestExecutionID)

	restoredExecution, err := restoredUsage.QueryRequestExecution().Only(targetCtx)
	require.NoError(t, err)
	require.Equal(t, restoredRequest.ID, restoredExecution.RequestID)
	require.Zero(t, restoredExecution.ChannelID)
	require.JSONEq(t, `{"model":"gpt-4"}`, string(restoredExecution.RequestBody))
}

func TestBackupService_Restore_UsageStatsOnlyDoesNotRestoreExecutionContentFromFullBackup(t *testing.T) {
	sourceClient, sourceService, sourceCtx := setupBackupTestWithDSN(t, "file:content-source?mode=memory&_fk=1")
	defer sourceClient.Close()

	sourceUser, _ := sourceClient.User.Query().First(sourceCtx)
	sourceProject := createBackupTestProject(t, sourceClient, sourceCtx, "Project1", "Test Project")
	sourceChannel := createBackupTestChannel(t, sourceClient, sourceCtx, "Channel 1", channel.TypeOpenai)
	sourceAPIKey := createBackupTestAPIKey(t, sourceClient, sourceCtx, sourceUser, sourceProject, "API Key 1", "sk-test-key-1")
	createBackupTestUsage(t, sourceClient, sourceCtx, sourceProject, sourceChannel, sourceAPIKey)

	data, err := sourceService.Backup(sourceCtx, BackupOptions{
		IncludeProjects:    true,
		IncludeAPIKeys:     true,
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)
	require.Contains(t, string(data), "sensitive-header")
	require.Contains(t, string(data), "sensitive-tenant")

	targetClient, targetService, targetCtx := setupBackupTestWithDSN(t, "file:content-target?mode=memory&_fk=1")
	defer targetClient.Close()

	targetChannel := createBackupTestChannel(t, targetClient, targetCtx, sourceChannel.Name, channel.TypeOpenai)
	require.Equal(t, sourceChannel.ID, targetChannel.ID)

	err = targetService.Restore(targetCtx, data, RestoreOptions{
		IncludeProjects:   true,
		IncludeAPIKeys:    true,
		IncludeUsageStats: true,
	})
	require.NoError(t, err)

	restoredUsage, err := targetClient.UsageLog.Query().Only(targetCtx)
	require.NoError(t, err)
	restoredExecution, err := restoredUsage.QueryRequestExecution().Only(targetCtx)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(restoredExecution.RequestBody))
	require.Empty(t, restoredExecution.ExternalID)
	require.Empty(t, restoredExecution.ResponseBody)
	require.Empty(t, restoredExecution.ResponseChunks)
	require.Empty(t, restoredExecution.ErrorMessage)
	require.Empty(t, restoredExecution.RequestHeaders)
	require.Empty(t, restoredExecution.RequestURL)
}

func TestBackupService_Restore_UsageStatsResolvesRequestExecutionIDCollisionByFingerprint(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	_, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)
	originalExecution, err := usage.QueryRequestExecution().Only(ctx)
	require.NoError(t, err)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	collidingExecution, err := client.RequestExecution.Create().
		SetCreatedAt(originalExecution.CreatedAt.Add(time.Second)).
		SetProjectID(originalExecution.ProjectID).
		SetRequestID(originalExecution.RequestID).
		SetChannelID(originalExecution.ChannelID).
		SetSource(requestexecution.SourceAPI).
		SetModelID(originalExecution.ModelID).
		SetFormat(originalExecution.Format).
		SetRequestBody(originalExecution.RequestBody).
		SetRequestURL(originalExecution.RequestURL).
		SetStatus(requestexecution.StatusCompleted).
		SetStream(originalExecution.Stream).
		Save(ctx)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	requestExecutions := payload["request_executions"].([]any)
	requestExecutions[0].(map[string]any)["id"] = collidingExecution.ID
	usageLogs := payload["usage_logs"].([]any)
	usageLogs[0].(map[string]any)["request_execution_id"] = collidingExecution.ID
	data, err = json.Marshal(payload)
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, originalExecution.ID, restoredUsage.RequestExecutionID)
	require.NotEqual(t, collidingExecution.ID, restoredUsage.RequestExecutionID)
}

func TestBackupService_Restore_UsageStatsWithoutAuditFields(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	createBackupTestUsage(t, client, ctx, proj, ch, ak)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	var backupPayload map[string]any
	require.NoError(t, json.Unmarshal(data, &backupPayload))
	backupPayload["version"] = BackupVersionV4
	delete(backupPayload, "request_executions")
	usageLogs := backupPayload["usage_logs"].([]any)
	usageLog := usageLogs[0].(map[string]any)
	delete(usageLog, "request_execution_id")
	delete(usageLog, "requested_service_tier")
	delete(usageLog, "applied_service_tier")
	delete(usageLog, "service_tier")
	data, err = json.Marshal(backupPayload)
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)
	_, err = client.Request.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	restoredUsage, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Zero(t, restoredUsage.RequestExecutionID)
	require.Empty(t, restoredUsage.RequestedServiceTier)
	require.Empty(t, restoredUsage.AppliedServiceTier)
	require.Empty(t, restoredUsage.ServiceTier)
}

func TestBackupService_Restore_UsageStatsWithRequestLogs(t *testing.T) {
	client, service, ctx := setupBackupTest(t)
	defer client.Close()

	user, _ := client.User.Query().First(ctx)
	proj := createBackupTestProject(t, client, ctx, "Project1", "Test Project")
	ch := createBackupTestChannel(t, client, ctx, "Channel 1", channel.TypeOpenai)
	ak := createBackupTestAPIKey(t, client, ctx, user, proj, "API Key 1", "sk-test-key-1")
	_, usage := createBackupTestUsage(t, client, ctx, proj, ch, ak)

	data, err := service.Backup(ctx, BackupOptions{
		IncludeAPIKeys:     true,
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	_, err = client.UsageLog.Delete().Exec(ctx)
	require.NoError(t, err)

	_, err = client.Request.Delete().Exec(ctx)
	require.NoError(t, err)

	err = service.Restore(ctx, data, RestoreOptions{
		IncludeUsageStats:  true,
		IncludeRequestLogs: true,
	})
	require.NoError(t, err)

	requests, err := client.Request.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.JSONEq(t, `{"model":"gpt-4"}`, string(requests[0].RequestBody))
	require.Equal(t, "127.0.0.1", requests[0].ClientIP)

	usageLogs, err := client.UsageLog.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, usageLogs, 1)
	require.Equal(t, requests[0].ID, usageLogs[0].RequestID)
	require.Equal(t, int64(150), usageLogs[0].TotalTokens)
	require.NotNil(t, usageLogs[0].TotalCost)
	require.Equal(t, *usage.TotalCost, *usageLogs[0].TotalCost)
}
