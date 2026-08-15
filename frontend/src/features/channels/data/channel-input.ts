import type { ChannelAPIKeyConfig, ChannelCredentials, UpdateChannelInput } from './schema';

const DEFAULT_API_KEY_WEIGHT = 100;

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
