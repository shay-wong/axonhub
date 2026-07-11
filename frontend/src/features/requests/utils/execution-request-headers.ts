export function formatExecutionAPIKeyIdentity(name?: string | null, suffix?: string | null): string {
  const normalizedName = name?.trim();
  const normalizedSuffix = suffix?.trim();
  const maskedKey = normalizedSuffix ? `****${normalizedSuffix}` : '';

  return [normalizedName, maskedKey].filter(Boolean).join(' · ');
}

export function formatExecutionRequestHeaders(
  headers: unknown,
  channelAPIKeyName?: string | null,
  channelAPIKeySuffix?: string | null,
  channelAPIKeyHeaders?: string[] | null
): unknown {
  if (!headers || typeof headers !== 'object' || Array.isArray(headers)) {
    return headers;
  }

  const identity = formatExecutionAPIKeyIdentity(channelAPIKeyName, channelAPIKeySuffix);
  if (!identity) {
    return headers;
  }
  const identityHeaders = new Set((channelAPIKeyHeaders ?? []).map((headerName) => headerName.toLowerCase()));
  if (identityHeaders.size === 0) {
    return headers;
  }

  let changed = false;
  const formatted = Object.fromEntries(
    Object.entries(headers).map(([key, value]) => {
      if (!identityHeaders.has(key.toLowerCase()) || !isMaskedHeaderValue(value)) {
        return [key, value];
      }

      changed = true;
      return [key, Array.isArray(value) ? [identity] : identity];
    })
  );

  return changed ? formatted : headers;
}

function isMaskedHeaderValue(value: unknown): boolean {
  if (typeof value === 'string') {
    return value === '******';
  }
  return Array.isArray(value) && value.length > 0 && value.every((item) => item === '******');
}
