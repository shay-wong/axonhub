import type { ProviderModel } from '@/features/models/data/providers.schema';
import type { PriceItemCode } from './schema';

type TokenCost = {
  input?: number;
  output?: number;
  cache_read?: number;
  cache_write?: number;
};

type CatalogModelPriceItem = {
  itemCode: PriceItemCode;
  pricing: {
    mode: 'usage_per_unit';
    usagePerUnit: string;
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

function buildItemsFromCost(cost: TokenCost | undefined, multiplier: number, fallbackToZero: boolean): CatalogModelPriceItem[] {
  const items = costItemCodes.flatMap(([costKey, itemCode]) => {
    const value = cost?.[costKey];
    if (value == null) return [];

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

function serviceTierFromProvider(provider: Record<string, unknown> | undefined): string | null {
  const body = provider?.body;
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return null;

	const serviceTier = (body as Record<string, unknown>).service_tier;
	return typeof serviceTier === 'string' && serviceTier.trim() ? serviceTier.trim().toLowerCase() : null;
}

export function buildProviderModelPrice(model: ProviderModel, multiplier: number = 1): CatalogModelPrice {
  const serviceTierPrices = new Map<string, CatalogModelPriceItem[]>();

  for (const mode of Object.values(model.experimental?.modes ?? {})) {
    const serviceTier = serviceTierFromProvider(mode.provider);
    if (!serviceTier) continue;

    serviceTierPrices.set(serviceTier, buildItemsFromCost(mode.cost, multiplier, false));
  }

  return {
    items: buildItemsFromCost(model.cost, multiplier, true),
    serviceTierPrices: Array.from(serviceTierPrices, ([serviceTier, items]) => ({ serviceTier, items })),
  };
}
