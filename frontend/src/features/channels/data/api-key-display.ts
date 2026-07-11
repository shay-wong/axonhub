import type { ChannelAPIKeyConfig } from './schema';

export function createAPIKeyNameMap(configs?: ChannelAPIKeyConfig[] | null): Map<string, string> {
  const names = new Map<string, string>();

  for (const config of configs ?? []) {
    const key = config.key.trim();
    const name = config.name?.trim();
    if (key && name) {
      names.set(key, name);
    }
  }

  return names;
}

export function maskAPIKeySuffix(key: string): string {
  const normalizedKey = key.trim();
  if (normalizedKey.length <= 8) {
    return '****';
  }

  return `****${normalizedKey.slice(-4)}`;
}

export function formatAPIKeyIdentity(key: string, name?: string | null): string {
  const normalizedName = name?.trim();
  const maskedKey = maskAPIKeySuffix(key);

  return normalizedName ? `${normalizedName} · ${maskedKey}` : maskedKey;
}
