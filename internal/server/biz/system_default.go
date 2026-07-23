package biz

import "github.com/samber/lo"

const (
	defaultAutoDisableFallbackDurationMinutes = 5
	defaultChannelTestSystemPrompt            = "You are a helpful assistant."
	defaultChannelTestUserPrompt              = "Hello world, I'm AxonHub.\nPlease tell me who you are?"
	maxChannelTestPromptRunes                 = 4096
)

var defaultStoragePolicy = StoragePolicy{
	StoreChunks:       false,
	LivePreview:       false,
	StoreRequestBody:  true,
	StoreResponseBody: true,
	CleanupOptions: []CleanupOption{
		{
			ResourceType: "requests",
			Enabled:      false,
			CleanupDays:  3,
		},
		{
			ResourceType: "usage_logs",
			Enabled:      false,
			CleanupDays:  30,
		},
	},
}

var defaultRetryPolicy = RetryPolicy{
	MaxChannelRetries:       3,
	MaxSingleChannelRetries: 2,
	RetryDelayMs:            1000,
	LoadBalancerStrategy:    "adaptive",
	Enabled:                 true,
	UpstreamErrorPolicy: UpstreamErrorPolicy{
		Mode: UpstreamErrorModePassthrough,
	},
}

var defaultAutoDisableStatusRules = []AutoDisableStatusRule{
	{Status: 401, Times: 1, Action: DisableActionPermanent},
	{Status: 403, Times: 1, Action: DisableActionPermanent},
	{Status: 429, Times: 3, Action: DisableActionTemporary, DurationMinutes: lo.ToPtr(defaultAutoDisableFallbackDurationMinutes), UseRetryAfter: lo.ToPtr(true)},
	{Status: 503, Times: 3, Action: DisableActionTemporary, DurationMinutes: lo.ToPtr(defaultAutoDisableFallbackDurationMinutes)},
	{Status: 529, Times: 3, Action: DisableActionTemporary, DurationMinutes: lo.ToPtr(defaultAutoDisableFallbackDurationMinutes)},
}

var defaultModelSettings = SystemModelSettings{
	FallbackToChannelsOnModelNotFound: true,
	QueryAllChannelModels:             true,
	DefaultModelAPIIncludeAll:         false,
	AutoReasoningEffort:               false,
	ModelBlacklistRegex:               "",
	DeveloperSettings:                 []*DeveloperModelSettings{},
}

var defaultChannelSetting = SystemChannelSettings{
	Probe: ChannelProbeSetting{
		Enabled:   true,
		Frequency: ProbeFrequency5Min,
	},
	AutoSync: ChannelModelAutoSyncSetting{
		Frequency: AutoSyncFrequencyOneHour,
	},
	TestSystemPrompt: defaultChannelTestSystemPrompt,
	TestUserPrompt:   defaultChannelTestUserPrompt,
}

var defaultGeneralSettings = SystemGeneralSettings{
	CurrencyCode: "USD",
	Timezone:     "UTC",
}

var defaultAutoBackupSettings = AutoBackupSettings{
	Enabled:            false,
	Frequency:          BackupFrequencyDaily,
	IncludeChannels:    true,
	IncludeModels:      true,
	IncludeAPIKeys:     false,
	IncludeModelPrices: true,
	IncludeUsageStats:  false,
	IncludeRequestLogs: false,
	RetentionDays:      30,
}

var defaultVideoStorageSettings = VideoStorageSettings{
	Enabled:             false,
	DataStorageID:       0,
	ScanIntervalMinutes: 1,
	ScanLimit:           50,
}

var defaultQuotaEnforcementSettings = QuotaEnforcementSettings{
	Enabled: false,
	Mode:    QuotaEnforcementModeExhaustedOnly,
}

var defaultSecuritySettings = SecuritySettings{
	BlockedIPs:              []string{},
	ShowRequestLogIPBanIcon: true,
}
