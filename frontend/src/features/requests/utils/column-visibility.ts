export type RequestColumnVisibility = Record<string, boolean>;

export const REQUEST_COLUMN_VISIBILITY_STORAGE_VERSION = 6;

const WRAPPED_STORAGE_VERSIONS = new Set([2, 3, 4, 5, REQUEST_COLUMN_VISIBILITY_STORAGE_VERSION]);
const LEGACY_RESPONSIVE_DEFAULT_IDS = new Set([
  'apiFormat',
  'passThrough',
  'reasoningEffort',
  'requestedServiceTier',
  'stream',
  'source',
  'clientIP',
  'channel',
  'apiKey',
  'tokens',
  'readCache',
  'writeCache',
  'cost',
  'latency',
  'details',
]);
const LEGACY_COLUMN_GROUPS = [
  { target: 'caller', sources: ['apiKey'] },
  { target: 'usage', sources: ['tokens', 'readCache', 'writeCache'] },
  { target: 'duration', sources: ['latency'] },
] as const;
const CURRENT_USAGE_COLUMN_IDS = ['tokens', 'readCache', 'writeCache'] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function readBooleanVisibility(value: unknown): RequestColumnVisibility {
  if (!isRecord(value)) return {};

  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, boolean] => typeof entry[1] === 'boolean'));
}

export function migrateRequestColumnVisibilityPayload(payload: unknown): RequestColumnVisibility {
  if (!isRecord(payload)) return {};

  const version = payload.v;
  const stored = typeof version === 'number' ? (WRAPPED_STORAGE_VERSIONS.has(version) ? payload.overrides : undefined) : payload;
  const visibility = readBooleanVisibility(stored);

  if (version === undefined) {
    Object.entries(visibility).forEach(([id, visible]) => {
      if (visible === false && LEGACY_RESPONSIVE_DEFAULT_IDS.has(id)) delete visibility[id];
    });
  }

  if (version === undefined || version === 2) {
    for (const { target, sources } of LEGACY_COLUMN_GROUPS) {
      if (visibility[target] === undefined) {
        const hasLegacyValue = sources.some((source) => visibility[source] !== undefined);
        if (hasLegacyValue) {
          visibility[target] = sources.some((source) => visibility[source] !== false);
        }
      }
      sources.forEach((source) => delete visibility[source]);
    }
  }

  if (version === undefined || version === 2 || version === 3) {
    const usage = visibility.usage;
    if (usage !== undefined) {
      CURRENT_USAGE_COLUMN_IDS.forEach((id) => {
        if (visibility[id] === undefined) visibility[id] = usage;
      });
    }
    delete visibility.usage;
  }

  return visibility;
}
