import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('./providers.schema.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2022,
  },
});
const zodURL = import.meta.resolve('zod');
const moduleSource = outputText.replace("from 'zod'", `from '${zodURL}'`);
const moduleURL = `data:text/javascript;base64,${Buffer.from(moduleSource).toString('base64')}`;
const { providersDataSchema } = await import(moduleURL);
const bundledProvidersData = JSON.parse(await readFile(new URL('./providers.json', import.meta.url), 'utf8'));

test('parses the bundled provider catalog', () => {
  assert.doesNotThrow(() => providersDataSchema.parse(bundledProvidersData));
});

test('preserves extended GPT-5.6 capabilities and tiered pricing', () => {
  const data = providersDataSchema.parse({
    providers: {
      openai: {
        models: [
          {
            id: 'gpt-5.6-sol',
            description: 'Frontier GPT-5.6 model',
            reasoning_options: [{ type: 'effort', values: ['none', 'max'] }],
            structured_output: true,
            limit: { context: 1_050_000, input: 922_000, output: 128_000 },
            experimental: {
              modes: {
                fast: {
                  cost: { input: 10, output: 60, cache_read: 1, cache_write: 12.5 },
                  provider: { body: { service_tier: 'priority' } },
                },
              },
            },
            cost: {
              input: 5,
              output: 30,
              cache_read: 0.5,
              cache_write: 6.25,
              tiers: [
                {
                  input: 10,
                  output: 45,
                  cache_read: 1,
                  cache_write: 12.5,
                  tier: { type: 'context', size: 272_000 },
                },
              ],
              context_over_200k: { input: 10, output: 45, cache_read: 1, cache_write: 12.5 },
            },
          },
        ],
      },
    },
  });
  const model = data.providers.openai.models[0];

  assert.equal(model.structured_output, true);
  assert.equal(model.limit.input, 922_000);
  assert.equal(model.experimental.modes.fast.provider.body.service_tier, 'priority');
  assert.equal(model.cost.tiers[0].tier.size, 272_000);
  assert.equal(model.cost.context_over_200k.output, 45);
});
