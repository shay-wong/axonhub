import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('./model-price-catalog.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2022,
  },
});
const moduleURL = `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`;
const { buildProviderModelPrice, mergeItemsWithProviderCost } = await import(moduleURL);

test('updating existing prices imports context rates and preserves cache variants and uncatalogued items', () => {
  const currentItems = [
    { itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '5' } },
    { itemCode: 'completion_tokens', pricing: { mode: 'flat_fee', flatFee: '9' } },
    {
      itemCode: 'prompt_write_cached_tokens',
      pricing: { mode: 'usage_per_unit', usagePerUnit: '7' },
      promptWriteCacheVariants: [{ variantCode: 'one_hour', pricing: { mode: 'usage_per_unit', usagePerUnit: '12' } }],
    },
  ];
  const original = structuredClone(currentItems);
  const merged = mergeItemsWithProviderCost(
    currentItems,
    {
      id: 'context-model',
      cost: {
        input: 10,
        cache_write: 12.5,
        cache_read: 1,
        tiers: [{ tier: { type: 'context', size: 272000 }, input: 20, cache_write: 25 }],
      },
    },
    2
  );
  assert.deepEqual(merged[0].pricing, {
    mode: 'usage_context',
    usageTiered: {
      tiers: [
        { upTo: 272000, pricePerUnit: '20.0000' },
        { upTo: null, pricePerUnit: '40.0000' },
      ],
    },
  });
  assert.deepEqual(merged[1], original[1]);
  assert.deepEqual(merged[2].pricing, {
    mode: 'usage_context',
    usageTiered: {
      tiers: [
        { upTo: 272000, pricePerUnit: '25.0000' },
        { upTo: null, pricePerUnit: '50.0000' },
      ],
    },
  });
  assert.deepEqual(merged[2].promptWriteCacheVariants, original[2].promptWriteCacheVariants);
  assert.deepEqual(merged[3], { itemCode: 'prompt_cached_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '2.0000' } });
  assert.deepEqual(currentItems, original);
});

test('updating with absent catalog prices preserves manual prices without inserting zero placeholders', () => {
  const items = [{ itemCode: 'completion_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '9' } }];
  for (const model of [{ id: 'no-cost' }, { id: 'empty-cost', cost: {} }]) {
    assert.deepEqual(mergeItemsWithProviderCost(items, model), items);
    assert.deepEqual(mergeItemsWithProviderCost([], model), []);
  }
  // Explicit published zero is a real price, not missing metadata.
  assert.deepEqual(mergeItemsWithProviderCost(items, { id: 'free', cost: { output: 0 } }), [
    { itemCode: 'completion_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '0.0000' } },
  ]);
});

test('imports actual Astra catalog context thresholds and prices for all service tiers', async () => {
  const catalog = JSON.parse(await readFile(new URL('../../models/data/providers.json', import.meta.url), 'utf8'));
  const astra = catalog.providers.openai.models.find((model) => model.id === 'gpt-6-astra');
  assert.ok(astra);
  const price = buildProviderModelPrice(astra, 1.5);
  // Explicit 272k thresholds must win over the catalog's legacy context_over_200k field.
  for (const [items, rates] of [
    [
      price.items,
      [
        [15, 30],
        [75, 112.5],
        [1.5, 3],
        [18.75, 37.5],
      ],
    ],
    [
      price.serviceTierPrices.find((tier) => tier.serviceTier === 'priority').items,
      [
        [30, 60],
        [150, 225],
        [3, 6],
        [37.5, 75],
      ],
    ],
    [
      price.serviceTierPrices.find((tier) => tier.serviceTier === 'ultrafast').items,
      [
        [90, 180],
        [450, 675],
        [9, 18],
        [112.5, 225],
      ],
    ],
  ]) {
    assert.equal(items.length, 4);
    items.forEach((item, index) => {
      assert.equal(item.pricing.mode, 'usage_context');
      assert.deepEqual(item.pricing.usageTiered.tiers, [
        { upTo: 272000, pricePerUnit: rates[index][0].toFixed(4) },
        { upTo: null, pricePerUnit: rates[index][1].toFixed(4) },
      ]);
    });
  }
});

test('imports explicit mode context prices and multiple thresholds without guessing missing fields', () => {
  const price = buildProviderModelPrice({
    id: 'custom-model',
    cost: { input: 1, output: 2 },
    experimental: {
      modes: {
        fast: {
          provider: { body: { service_tier: 'fast' } },
          cost: {
            input: 2,
            output: 4,
            tiers: [
              { tier: { type: 'context', size: 200 }, input: 6 },
              { tier: { type: 'context', size: 100 }, input: 3 },
            ],
          },
        },
      },
    },
  });
  assert.deepEqual(price.serviceTierPrices[0].items[0].pricing, {
    mode: 'usage_context',
    usageTiered: {
      tiers: [
        { upTo: 100, pricePerUnit: '2.0000' },
        { upTo: 200, pricePerUnit: '3.0000' },
        { upTo: null, pricePerUnit: '6.0000' },
      ],
    },
  });
  assert.deepEqual(price.serviceTierPrices[0].items[1].pricing, { mode: 'usage_per_unit', usagePerUnit: '4.0000' });
});

test('supports legacy 200k metadata without inventing mode prices for unlisted models', () => {
  const price = buildProviderModelPrice({
    id: 'custom-model',
    cost: { input: 1, context_over_200k: { input: 2 } },
    experimental: { modes: { fast: { cost: { input: 3 }, provider: { body: { service_tier: 'priority' } } } } },
  });
  assert.deepEqual(price.items[0].pricing.usageTiered.tiers, [
    { upTo: 200000, pricePerUnit: '1.0000' },
    { upTo: null, pricePerUnit: '2.0000' },
  ]);
  assert.deepEqual(price.serviceTierPrices[0].items[0].pricing, { mode: 'usage_per_unit', usagePerUnit: '3.0000' });
});

test('explicit service tier context prices override inferred official ratios', () => {
  const price = buildProviderModelPrice({
    id: 'gpt-6-astra',
    cost: { input: 10, tiers: [{ tier: { type: 'context', size: 272000 }, input: 20 }] },
    experimental: {
      modes: {
        fast: {
          provider: { body: { service_tier: 'priority' } },
          cost: { input: 20, tiers: [{ tier: { type: 'context', size: 300000 }, input: 70 }] },
        },
      },
    },
  });
  assert.deepEqual(price.serviceTierPrices[0].items[0].pricing.usageTiered.tiers, [
    { upTo: 300000, pricePerUnit: '20.0000' },
    { upTo: null, pricePerUnit: '70.0000' },
  ]);
});

test('maps Codex fast mode to the priority provider tier price', () => {
  const price = buildProviderModelPrice(
    {
      id: 'gpt-test',
      cost: { input: 5, output: 30, cache_read: 0.5, cache_write: 6.25 },
      experimental: {
        modes: {
          fast: {
            cost: { input: 10, output: 60, cache_read: 1, cache_write: 12.5 },
            provider: { body: { service_tier: ' PRIORITY ' } },
          },
          pro: {
            provider: { body: { reasoning: { mode: 'pro' } } },
          },
        },
      },
    },
    1.5
  );

  assert.deepEqual(
    price.items.map((item) => [item.itemCode, item.pricing.usagePerUnit]),
    [
      ['prompt_tokens', '7.5000'],
      ['completion_tokens', '45.0000'],
      ['prompt_cached_tokens', '0.7500'],
      ['prompt_write_cached_tokens', '9.3750'],
    ]
  );
  assert.deepEqual(price.serviceTierPrices, [
    {
      serviceTier: 'priority',
      items: [
        { itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '15.0000' } },
        { itemCode: 'completion_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '90.0000' } },
        { itemCode: 'prompt_cached_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '1.5000' } },
        { itemCode: 'prompt_write_cached_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '18.7500' } },
      ],
    },
  ]);
});

test('maps OpenAI fast service tier to the existing priority price bucket', () => {
  const price = buildProviderModelPrice({
    id: 'gpt-test',
    cost: { input: 10, output: 50 },
    experimental: {
      modes: {
        fast: {
          cost: { input: 20, output: 100 },
          provider: { body: { service_tier: ' FAST ' } },
        },
        ultrafast: {
          cost: { input: 60, output: 300 },
          provider: { body: { service_tier: 'ultrafast' } },
        },
      },
    },
  });

  assert.deepEqual(price.serviceTierPrices, [
    {
      serviceTier: 'priority',
      items: [
        { itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '20.0000' } },
        { itemCode: 'completion_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '100.0000' } },
      ],
    },
    {
      serviceTier: 'ultrafast',
      items: [
        { itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '60.0000' } },
        { itemCode: 'completion_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '300.0000' } },
      ],
    },
  ]);
});

test('maps Anthropic fast mode to the priority provider tier price', () => {
  const price = buildProviderModelPrice({
    id: 'claude-test',
    cost: { input: 5, output: 25 },
    experimental: {
      modes: {
        fast: {
          cost: { input: 10, output: 50 },
          provider: {
            body: { speed: 'fast' },
            headers: { 'anthropic-beta': 'fast-mode-2026-02-01' },
          },
        },
      },
    },
  });

  assert.deepEqual(price.serviceTierPrices, [
    {
      serviceTier: 'priority',
      items: [
        { itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '10.0000' } },
        { itemCode: 'completion_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '50.0000' } },
      ],
    },
  ]);
});

test('ignores boolean experimental flags when building prices', () => {
  const price = buildProviderModelPrice({
    id: 'preview-model',
    cost: { input: 5, output: 25 },
    experimental: true,
  });

  assert.deepEqual(price.serviceTierPrices, []);
});

test('leaves missing service-tier cost fields to inherit from the base price', () => {
  const price = buildProviderModelPrice(
    {
      id: 'gpt-partial-fast',
      cost: { input: 5, output: 30, cache_read: 0.5 },
      experimental: {
        modes: {
          fast: {
            cost: { input: 10 },
            provider: { body: { service_tier: 'priority' } },
          },
        },
      },
    },
    2
  );

  assert.deepEqual(
    price.serviceTierPrices[0].items.map((item) => [item.itemCode, item.pricing.usagePerUnit]),
    [['prompt_tokens', '20.0000']]
  );
});
