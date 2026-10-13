import type { ProviderModel } from '@/features/models/data/providers.schema';
import type { PriceFormItem } from './model-price-form';
import type { PriceItemCode } from './schema';

type TokenCost = {
  input?: number;
  output?: number;
  cache_read?: number;
  cache_write?: number;
};

type ContextCost = TokenCost & {
  tiers?: Array<TokenCost & { tier: { type: string; size?: number } }>;
  context_over_200k?: TokenCost;
};

type CatalogModelPriceItem = {
  itemCode: PriceItemCode;
  pricing:
    | {
        mode: 'usage_per_unit';
        usagePerUnit: string;
      }
    | {
        mode: 'usage_context';
        usageTiered: { tiers: Array<{ upTo: number | null; pricePerUnit: string }> };
      };
};

type CatalogModelPrice = {
  items: CatalogModelPriceItem[];
  serviceTierPrices: Array<{
    serviceTier: string;
    items: CatalogModelPriceItem[];
  }>;
};

const costItemCodes: ReadonlyArray<[keyof TokenCost, PriceItemCode]> = [
  ['input', 'prompt_tokens'],
  ['output', 'completion_tokens'],
  ['cache_read', 'prompt_cached_tokens'],
  ['cache_write', 'prompt_write_cached_tokens'],
];

function contextTiers(cost: ContextCost | undefined) {
  const tiers = cost?.tiers?.filter(({ tier }) => tier.type === 'context' && Number.isSafeInteger(tier.size) && tier.size! > 0) ?? [];
  // Explicit thresholds take precedence over the older, misleading 200k field
  // still included alongside 272k tiers in the OpenAI catalog.
  if (tiers.length) return [...tiers].sort((a, b) => a.tier.size! - b.tier.size!);
  return cost?.context_over_200k ? [{ ...cost.context_over_200k, tier: { type: 'context', size: 200_000 } }] : [];
}

function buildItemsFromCost(cost: ContextCost | undefined, multiplier: number, fallbackToZero: boolean): CatalogModelPriceItem[] {
  const contextPrices = contextTiers(cost);
  const items = costItemCodes.flatMap(([costKey, itemCode]): CatalogModelPriceItem[] => {
    const value = cost?.[costKey];
    if (value == null) return [];

    let previousRate = value;
    const tiers: Array<{ upTo: number | null; pricePerUnit: string }> = [];
    for (const contextPrice of contextPrices) {
      const rate = contextPrice[costKey];
      if (rate == null || rate === previousRate) continue;
      tiers.push({ upTo: contextPrice.tier.size!, pricePerUnit: (previousRate * multiplier).toFixed(4) });
      previousRate = rate;
    }
    if (tiers.length) {
      tiers.push({ upTo: null, pricePerUnit: (previousRate * multiplier).toFixed(4) });
      return [{ itemCode, pricing: { mode: 'usage_context', usageTiered: { tiers } } }];
    }

    return [
      {
        itemCode,
        pricing: {
          mode: 'usage_per_unit' as const,
          usagePerUnit: (value * multiplier).toFixed(4),
        },
      },
    ];
  });

  if (items.length > 0 || !fallbackToZero) return items;

  return [
    {
      itemCode: 'prompt_tokens',
      pricing: { mode: 'usage_per_unit', usagePerUnit: '0' },
    },
  ];
}

function costWithModeContext(model: ProviderModel, serviceTier: string, cost: ContextCost | undefined): ContextCost | undefined {
  if (!cost || contextTiers(cost).length) return cost;
  // These published OpenAI models keep the same per-item long/short ratio in
  // Fast (and, where supported, Ultrafast). Do not generalize to other models.
  const fastModels = ['gpt-6-astra', 'gpt-6.1-sol', 'gpt-6-sol', 'gpt-6-luna', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna'];
  const supported =
    serviceTier === 'priority'
      ? fastModels.includes(model.id)
      : serviceTier === 'ultrafast' && ['gpt-6-astra', 'gpt-6.1-sol'].includes(model.id);
  if (!supported) return cost;

  const tiers = contextTiers(model.cost).map((entry) => {
    const prices: TokenCost = {};
    for (const [key] of costItemCodes) {
      const base = model.cost?.[key];
      if (base != null && base > 0 && cost[key] != null && entry[key] != null) {
        prices[key] = (cost[key]! * entry[key]!) / base;
      }
    }
    return { ...prices, tier: entry.tier };
  });
  return { ...cost, tiers };
}

function serviceTierFromProvider(provider: Record<string, unknown> | undefined): string | null {
  const body = provider?.body;
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return null;

  const providerBody = body as Record<string, unknown>;
  const serviceTier = providerBody.service_tier;
  if (typeof serviceTier === 'string' && serviceTier.trim()) {
    const normalizedTier = serviceTier.trim().toLowerCase();
    return normalizedTier === 'fast' ? 'priority' : normalizedTier;
  }

  const speed = providerBody.speed;
  // Anthropic Fast reuses the existing priority price bucket internally; the
  // request log still persists and displays the provider-native speed=fast mode.
  return typeof speed === 'string' && speed.trim().toLowerCase() === 'fast' ? 'priority' : null;
}

export function buildProviderModelPrice(model: ProviderModel, multiplier: number = 1): CatalogModelPrice {
  const serviceTierPrices = new Map<string, CatalogModelPriceItem[]>();
  const modes = typeof model.experimental === 'object' ? model.experimental.modes : undefined;

  for (const mode of Object.values(modes ?? {})) {
    const serviceTier = serviceTierFromProvider(mode.provider);
    if (!serviceTier) continue;

    serviceTierPrices.set(serviceTier, buildItemsFromCost(costWithModeContext(model, serviceTier, mode.cost), multiplier, false));
  }

  return {
    items: buildItemsFromCost(model.cost, multiplier, true),
    serviceTierPrices: Array.from(serviceTierPrices, ([serviceTier, items]) => ({ serviceTier, items })),
  };
}

export function mergeItemsWithProviderCost(currentItems: PriceFormItem[], model: ProviderModel, multiplier: number = 1): PriceFormItem[] {
  // The zero-price placeholder is useful when adding a model, but must never
  // replace an existing price when the catalog has no published base rates.
  if (!costItemCodes.some(([costKey]) => model.cost?.[costKey] != null)) return [...currentItems];

  const byCode = new Map(currentItems.map((item) => [item.itemCode, item]));
  for (const item of buildProviderModelPrice(model, multiplier).items) {
    const existing = byCode.get(item.itemCode);
    byCode.set(item.itemCode, existing ? { ...existing, pricing: item.pricing } : item);
  }
  return Array.from(byCode.values());
}
