import type { ChannelModelPrice, ModelPriceItem, PricingMode, SaveChannelModelPriceInput } from './schema';

type PriceItemCode = ModelPriceItem['itemCode'];
type PromptWriteCacheVariantCode = NonNullable<ModelPriceItem['promptWriteCacheVariants']>[number]['variantCode'];

export type PriceFormPricing = {
  mode: PricingMode;
  flatFee?: string | null;
  usagePerUnit?: string | null;
  usageTiered?: {
    tiers: Array<{
      upTo?: number | null;
      pricePerUnit: string;
    }>;
  } | null;
};

export type PriceFormItem = {
  itemCode: PriceItemCode;
  pricing: PriceFormPricing;
  promptWriteCacheVariants?: Array<{
    variantCode: PromptWriteCacheVariantCode;
    pricing: PriceFormPricing;
  }> | null;
};

export type PriceFormPrice = {
  items: PriceFormItem[];
  serviceTierPrices?: Array<{
    serviceTier: string;
    items: PriceFormItem[];
  }> | null;
};

export type PriceFormData = {
  prices: Array<{
    modelId: string;
    price: PriceFormPrice;
  }>;
};

export type PriceFormValidationIssueCode =
  | 'priceRequired'
  | 'duplicateItemCode'
  | 'duplicateVariantCode'
  | 'serviceTierRequired'
  | 'duplicateServiceTier';

export type PriceFormValidationIssue = {
  code: PriceFormValidationIssueCode;
  path: Array<string | number>;
};

function decimalToFormValue(value: string | number | null | undefined): string {
  return value == null ? '' : String(value);
}

function mapPricingToForm(pricing: ModelPriceItem['pricing']): PriceFormPricing {
  return {
    mode: pricing.mode,
    flatFee: decimalToFormValue(pricing.flatFee),
    usagePerUnit: decimalToFormValue(pricing.usagePerUnit),
    usageTiered: pricing.usageTiered
      ? {
          tiers: pricing.usageTiered.tiers.map((tier) => ({
            upTo: tier.upTo,
            pricePerUnit: decimalToFormValue(tier.pricePerUnit),
          })),
        }
      : null,
  };
}

function mapItemToForm(item: ModelPriceItem): PriceFormItem {
  return {
    itemCode: item.itemCode,
    pricing: mapPricingToForm(item.pricing),
    promptWriteCacheVariants:
      item.promptWriteCacheVariants?.map((variant) => ({
        variantCode: variant.variantCode,
        pricing: mapPricingToForm(variant.pricing),
      })) || [],
  };
}

function mapPricingToInput(pricing: PriceFormPricing): ModelPriceItem['pricing'] {
  return {
    mode: pricing.mode,
    flatFee: pricing.flatFee || null,
    usagePerUnit: pricing.usagePerUnit || null,
    usageTiered: pricing.usageTiered
      ? {
          tiers: pricing.usageTiered.tiers.map((tier) => ({
            upTo: tier.upTo,
            pricePerUnit: tier.pricePerUnit,
          })),
        }
      : null,
  };
}

function mapItemToInput(item: PriceFormItem): ModelPriceItem {
  return {
    itemCode: item.itemCode,
    pricing: mapPricingToInput(item.pricing),
    promptWriteCacheVariants:
      item.promptWriteCacheVariants?.map((variant) => ({
        variantCode: variant.variantCode,
        pricing: mapPricingToInput(variant.pricing),
      })) || [],
  };
}

function cloneFormItems(items: PriceFormItem[]): PriceFormItem[] {
	return structuredClone(items);
}

function canonicalizeServiceTier(serviceTier: string): string {
	return serviceTier.trim().toLowerCase();
}

export function mapServerPricesToFormData(currentPrices: readonly ChannelModelPrice[]): PriceFormData {
  return {
    prices: currentPrices.map((price) => ({
      modelId: price.modelID,
      price: {
        items: price.price.items.map(mapItemToForm),
        serviceTierPrices:
          price.price.serviceTierPrices?.map((serviceTierPrice) => ({
            serviceTier: serviceTierPrice.serviceTier,
            items: serviceTierPrice.items.map(mapItemToForm),
          })) || [],
      },
    })),
  };
}

export function mapPriceFormDataToSaveInput(data: PriceFormData): SaveChannelModelPriceInput[] {
  return data.prices.map((price) => ({
    modelId: price.modelId,
    price: {
      items: price.price.items.map(mapItemToInput),
		serviceTierPrices:
			price.price.serviceTierPrices?.map((serviceTierPrice) => ({
				serviceTier: canonicalizeServiceTier(serviceTierPrice.serviceTier),
          items: serviceTierPrice.items.map(mapItemToInput),
        })) || [],
    },
  }));
}

export function replaceCatalogServiceTierPrices(
  currentPrice: PriceFormPrice,
  catalogPrice: Pick<PriceFormPrice, 'serviceTierPrices'>
): PriceFormPrice {
  return {
    ...currentPrice,
    serviceTierPrices:
      catalogPrice.serviceTierPrices?.map((serviceTierPrice) => ({
        serviceTier: serviceTierPrice.serviceTier,
        items: cloneFormItems(serviceTierPrice.items),
      })) || [],
  };
}

function collectPricingIssues(
  pricing: PriceFormPricing | null | undefined,
  pathPrefix: Array<string | number>,
  issues: PriceFormValidationIssue[]
) {
  const { mode, flatFee, usagePerUnit, usageTiered } = pricing || {};
  if (mode === 'flat_fee' && !flatFee) {
    issues.push({ code: 'priceRequired', path: [...pathPrefix, 'flatFee'] });
  }
  if (mode === 'usage_per_unit' && !usagePerUnit) {
    issues.push({ code: 'priceRequired', path: [...pathPrefix, 'usagePerUnit'] });
  }
  if (mode !== 'usage_tiered' && mode !== 'usage_volume') return;

  const tiers = usageTiered?.tiers || [];
  if (tiers.length === 0) {
    issues.push({ code: 'priceRequired', path: [...pathPrefix, 'usageTiered'] });
  }

  const lastTierIndex = tiers.length - 1;
  let previousUpTo: number | undefined;
  tiers.forEach((tier, tierIndex) => {
    if (!tier.pricePerUnit) {
      issues.push({ code: 'priceRequired', path: [...pathPrefix, 'usageTiered', 'tiers', tierIndex, 'pricePerUnit'] });
    }

    const isLastTier = tierIndex === lastTierIndex;
    if (isLastTier) {
      if (tier.upTo != null) {
        issues.push({ code: 'priceRequired', path: [...pathPrefix, 'usageTiered', 'tiers', tierIndex, 'upTo'] });
      }
      return;
    }

    if (tier.upTo == null) {
      issues.push({ code: 'priceRequired', path: [...pathPrefix, 'usageTiered', 'tiers', tierIndex, 'upTo'] });
      return;
    }

    if (tier.upTo <= 0 || (previousUpTo != null && tier.upTo <= previousUpTo)) {
      issues.push({ code: 'priceRequired', path: [...pathPrefix, 'usageTiered', 'tiers', tierIndex, 'upTo'] });
    }
    previousUpTo = tier.upTo;
  });
}

function collectItemIssues(items: PriceFormItem[], itemsPath: Array<string | number>, issues: PriceFormValidationIssue[]) {
  const itemIndexesByCode = new Map<string, number[]>();
  items.forEach((item, itemIndex) => {
    const indexes = itemIndexesByCode.get(item.itemCode) || [];
    indexes.push(itemIndex);
    itemIndexesByCode.set(item.itemCode, indexes);
  });
  itemIndexesByCode.forEach((indexes) => {
    if (indexes.length < 2) return;
    indexes.forEach((itemIndex) => {
      issues.push({ code: 'duplicateItemCode', path: [...itemsPath, itemIndex, 'itemCode'] });
    });
  });

  items.forEach((item, itemIndex) => {
    const itemPath = [...itemsPath, itemIndex];
    collectPricingIssues(item.pricing, [...itemPath, 'pricing'], issues);

    const variantIndexesByCode = new Map<string, number[]>();
    (item.promptWriteCacheVariants || []).forEach((variant, variantIndex) => {
      const indexes = variantIndexesByCode.get(variant.variantCode) || [];
      indexes.push(variantIndex);
      variantIndexesByCode.set(variant.variantCode, indexes);
    });
    variantIndexesByCode.forEach((indexes) => {
      if (indexes.length < 2) return;
      indexes.forEach((variantIndex) => {
        issues.push({
          code: 'duplicateVariantCode',
          path: [...itemPath, 'promptWriteCacheVariants', variantIndex, 'variantCode'],
        });
      });
    });

    (item.promptWriteCacheVariants || []).forEach((variant, variantIndex) => {
      collectPricingIssues(variant.pricing, [...itemPath, 'promptWriteCacheVariants', variantIndex, 'pricing'], issues);
    });
  });
}

export function collectPriceFormValidationIssues(data: PriceFormData): PriceFormValidationIssue[] {
  const issues: PriceFormValidationIssue[] = [];

  data.prices.forEach((price, priceIndex) => {
    const pricePath = ['prices', priceIndex, 'price'] as Array<string | number>;
    collectItemIssues(price.price.items, [...pricePath, 'items'], issues);

	const tierIndexesByName = new Map<string, number[]>();
	(price.price.serviceTierPrices || []).forEach((serviceTierPrice, serviceTierIndex) => {
		const serviceTier = canonicalizeServiceTier(serviceTierPrice.serviceTier);
      if (!serviceTier) {
        issues.push({
          code: 'serviceTierRequired',
          path: [...pricePath, 'serviceTierPrices', serviceTierIndex, 'serviceTier'],
        });
      } else {
        const indexes = tierIndexesByName.get(serviceTier) || [];
        indexes.push(serviceTierIndex);
        tierIndexesByName.set(serviceTier, indexes);
      }

      collectItemIssues(serviceTierPrice.items, [...pricePath, 'serviceTierPrices', serviceTierIndex, 'items'], issues);
    });

    tierIndexesByName.forEach((indexes) => {
      if (indexes.length < 2) return;
      indexes.forEach((serviceTierIndex) => {
        issues.push({
          code: 'duplicateServiceTier',
          path: [...pricePath, 'serviceTierPrices', serviceTierIndex, 'serviceTier'],
        });
      });
    });
  });

  return issues;
}
