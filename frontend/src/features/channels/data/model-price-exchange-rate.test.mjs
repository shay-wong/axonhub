import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { buildProviderModelPrice, mergeItemsWithProviderCost } from './model-price-catalog.ts';
import { replaceCatalogServiceTierPrices } from './model-price-form.ts';

const source = readFileSync(new URL('../components/channels-model-price-dialog.tsx', import.meta.url), 'utf8');
const ast = ts.createSourceFile('channels-model-price-dialog.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
let applyCallback;
function findApplyCallback(node) {
  if (ts.isVariableDeclaration(node) && node.name.getText(ast) === 'applyProviderModelToIndex') {
    applyCallback = node.initializer.arguments[0].getText(ast);
  }
  ts.forEachChild(node, findApplyCallback);
}
findApplyCallback(ast);
assert.ok(applyCallback, 'execute the actual dialog callback rather than duplicating its price calculation');

const { outputText } = ts.transpileModule(
  `export function bind(buildProviderModelPrice, mergeItemsWithProviderCost, replaceCatalogServiceTierPrices,
    getValues, setValue, multiplier, exchangeRate) { return ${applyCallback}; }`,
  { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }
);
const { bind } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`);

test('dialog exchange rate scales base, context, Fast and Ultrafast prices while preserving manual schedule and cache variants', () => {
  const model = {
    id: 'gpt-6-astra',
    cost: {
      input: 2,
      output: 4,
      cache_write: 1,
      tiers: [{ tier: { type: 'context', size: 272000 }, input: 4 }],
    },
    experimental: {
      modes: {
        fast: { cost: { input: 4, output: 8 }, provider: { body: { service_tier: 'priority' } } },
        ultrafast: { cost: { input: 12, output: 24 }, provider: { body: { service_tier: 'ultrafast' } } },
      },
    },
  };
  const currentPrice = {
    items: [
      {
        itemCode: 'prompt_write_cached_tokens',
        pricing: { mode: 'usage_per_unit', usagePerUnit: '9' },
        promptWriteCacheVariants: [
          { variantCode: 'one_hour', pricing: { mode: 'usage_per_unit', usagePerUnit: '7' } },
        ],
      },
    ],
    schedule: {
      timezone: 'Asia/Shanghai',
      overrides: [
        {
          name: 'Night',
          priority: 1,
          when: { dailyTime: { start: '00:00', end: '06:00' } },
          items: [{ itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '0.5' } }],
        },
      ],
    },
  };
  const original = structuredClone(currentPrice);
  let result;
  const apply = bind(
    buildProviderModelPrice,
    mergeItemsWithProviderCost,
    replaceCatalogServiceTierPrices,
    (path) => {
      if (path === 'prices.0.price') return currentPrice;
      assert.equal(path, 'prices.0.price.items');
      return currentPrice.items;
    },
    (path, value, options) => {
      assert.equal(path, 'prices.0.price');
      assert.deepEqual(options, { shouldDirty: true, shouldValidate: true });
      result = value;
    },
    1.5,
    7.14
  );
  apply(0, model);

  for (const [items, promptRates, outputRate] of [
    [result.items, ['21.4200', '42.8400'], '42.8400'],
    [result.serviceTierPrices.find((tier) => tier.serviceTier === 'priority').items, ['42.8400', '85.6800'], '85.6800'],
    [result.serviceTierPrices.find((tier) => tier.serviceTier === 'ultrafast').items, ['128.5200', '257.0400'], '257.0400'],
  ]) {
    assert.deepEqual(items.find((item) => item.itemCode === 'prompt_tokens').pricing, {
      mode: 'usage_context',
      usageTiered: { tiers: [{ upTo: 272000, pricePerUnit: promptRates[0] }, { upTo: null, pricePerUnit: promptRates[1] }] },
    });
    assert.deepEqual(items.find((item) => item.itemCode === 'completion_tokens').pricing, {
      mode: 'usage_per_unit',
      usagePerUnit: outputRate,
    });
  }
  const cacheItem = result.items.find((item) => item.itemCode === 'prompt_write_cached_tokens');
  assert.equal(cacheItem.pricing.usagePerUnit, '10.7100');
  assert.deepEqual(cacheItem.promptWriteCacheVariants, original.items[0].promptWriteCacheVariants);
  assert.deepEqual(result.schedule, original.schedule);
  assert.deepEqual(currentPrice, original);
});
