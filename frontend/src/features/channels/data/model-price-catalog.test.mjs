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
const { buildProviderModelPrice } = await import(moduleURL);

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
