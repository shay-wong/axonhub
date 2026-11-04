import { getConfiguredAPIKeys } from './channel-input.ts';
import { getActiveDisabledAPIKeyStatus } from './disabled-api-key-status.ts';
import type { ChannelCredentials, DisabledAPIKey } from './schema';

export function hasOAuthCredentials(credentials: ChannelCredentials | null | undefined) {
  if (credentials?.oauth?.accessToken) return true;
  const legacy = credentials?.apiKey?.trim();
  return Boolean(legacy?.startsWith('{') && legacy.includes('"access_token"'));
}

export function getManagementConfiguredKeys(credentials: ChannelCredentials | null | undefined) {
  if (hasOAuthCredentials(credentials)) return [];
  const keys = getConfiguredAPIKeys(credentials);
  const vertexKeys = (credentials?.vertexApiKeys ?? []).map((binding) => binding.apiKey);
  const legacyKey = !keys.length && !vertexKeys.length ? credentials?.apiKey?.trim() : undefined;
  return [...new Set([...keys, ...vertexKeys, ...(legacyKey ? [legacyKey] : [])])];
}

export function getManagedAPIKeyRows(keys: string[], disabledKeys: DisabledAPIKey[], nowMs: number) {
  const disabled = new Map(
    disabledKeys.flatMap((key) => {
      const disableStatus = getActiveDisabledAPIKeyStatus(key.disabledUntil, nowMs);
      return disableStatus ? [[key.key, { ...key, disableStatus }] as const] : [];
    })
  );
  return [...new Set([...keys, ...disabled.keys()])].map((key) => ({ key, disabled: disabled.get(key) }));
}
