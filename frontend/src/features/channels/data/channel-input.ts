import type { ChannelAPIKeyConfig, ChannelCredentials, ChannelSettings, CreateChannelInput, UpdateChannelInput } from './schema';

const DEFAULT_API_KEY_WEIGHT = 100;

type OpenCodeGoQuotaSettings = NonNullable<NonNullable<NonNullable<ChannelSettings>['providerQuota']>['opencodeGo']>;
type ChannelCredentialsInput = NonNullable<UpdateChannelInput['credentials']>;

function normalizeAPIKeys(keys?: string[] | null): string[] {
  return [...new Set((keys ?? []).map((key) => key.trim()).filter(Boolean))];
}

export function getConfiguredAPIKeys(credentials?: ChannelCredentials | null): string[] {
  const configuredKeys = normalizeAPIKeys(credentials?.apiKeyConfigs?.map((config) => config.key));
  return configuredKeys.length > 0 ? configuredKeys : normalizeAPIKeys(credentials?.apiKeys);
}

export function buildAPIKeyCredentialsForUpdate(keys: string[], credentials?: ChannelCredentials | null): ChannelCredentialsInput {
  const normalizedKeys = normalizeAPIKeys(keys);
  const existingConfigs = credentials?.apiKeyConfigs;
  if (!existingConfigs?.length) {
    return { apiKeys: normalizedKeys };
  }

  const configsByKey = new Map<string, ChannelAPIKeyConfig>();
  for (const config of existingConfigs) {
    const key = config.key.trim();
    if (key && !configsByKey.has(key)) {
      configsByKey.set(key, config);
    }
  }

  return {
    apiKeyConfigs: normalizedKeys.map((key) => ({
      ...(configsByKey.get(key) ?? { weight: DEFAULT_API_KEY_WEIGHT }),
      key,
    })),
  };
}

export function sanitizeChannelSettingsForInput(settings: ChannelSettings | null | undefined): ChannelSettings | null | undefined {
  const opencodeGo = settings?.providerQuota?.opencodeGo;
  if (!settings?.providerQuota || !opencodeGo) {
    return settings;
  }

  const inputOpencodeGo = projectOpenCodeGoQuotaSettingsForInput(opencodeGo);
  if (Object.keys(inputOpencodeGo).length === Object.keys(opencodeGo).length) {
    return settings;
  }

  return {
    ...settings,
    providerQuota: {
      ...settings.providerQuota,
      opencodeGo: inputOpencodeGo,
    },
  };
}

export function sanitizeChannelMutationInput<T extends { settings?: ChannelSettings | null }>(input: T): T {
  const settings = sanitizeChannelSettingsForInput(input.settings);
  if (settings === input.settings) {
    return input;
  }

  return {
    ...input,
    settings,
  };
}

function projectOpenCodeGoQuotaSettingsForInput(opencodeGo: OpenCodeGoQuotaSettings): OpenCodeGoQuotaSettings {
  const inputOpencodeGo: OpenCodeGoQuotaSettings = {};

  if (Object.prototype.hasOwnProperty.call(opencodeGo, 'workspaceId')) {
    inputOpencodeGo.workspaceId = opencodeGo.workspaceId;
  }

  if (Object.prototype.hasOwnProperty.call(opencodeGo, 'authCookie')) {
    inputOpencodeGo.authCookie = opencodeGo.authCookie;
  }

  if (Object.prototype.hasOwnProperty.call(opencodeGo, 'clearAuthCookie')) {
    inputOpencodeGo.clearAuthCookie = opencodeGo.clearAuthCookie;
  }

  return inputOpencodeGo;
}

export function buildCreateChannelVariables(input: CreateChannelInput) {
  return {
    input: sanitizeChannelMutationInput(input),
  };
}

export function buildDuplicateChannelVariables(sourceID: string, input: CreateChannelInput) {
  return {
    sourceID,
    input: sanitizeChannelMutationInput(input),
  };
}

export function buildBulkCreateChannelsVariables<T extends { settings?: ChannelSettings | null }>(input: T) {
  return {
    input: sanitizeChannelMutationInput(input),
  };
}

export function buildUpdateChannelVariables(id: string, input: UpdateChannelInput) {
  return {
    id,
    input: sanitizeChannelMutationInput(input),
  };
}
