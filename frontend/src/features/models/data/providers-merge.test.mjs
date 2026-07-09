import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('./providers-merge.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2022,
  },
});
const moduleURL = `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`;
const { mergeProviderModels } = await import(moduleURL);

const localModelIDs = new Set(['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna']);

test('adds missing local models without replacing unrelated remote models', () => {
  const localData = {
    providers: {
      openai: {
        display_name: 'Local OpenAI',
        models: [
          { id: 'gpt-5.6-sol', family: 'gpt', cost: { input: 5, cache_write: 6.25 } },
          { id: 'gpt-5.6-terra', family: 'gpt-mini', cost: { input: 2.5, cache_write: 3.125 } },
        ],
      },
    },
  };
  const remoteData = {
    providers: {
      openai: {
        display_name: 'Remote OpenAI',
        models: [{ id: 'gpt-5.5', cost: { input: 5 } }],
      },
    },
  };

  const result = mergeProviderModels(remoteData, localData, 'openai', localModelIDs);

  assert.equal(result.providers.openai.display_name, 'Remote OpenAI');
  assert.deepEqual(
    result.providers.openai.models.map((model) => model.id),
    ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.5']
  );
});

test('prefers remote fields, fills missing nested values locally, and removes targeted duplicates', () => {
  const localData = {
    providers: {
      openai: {
        models: [
          {
            id: 'gpt-5.6-sol',
            family: 'gpt',
            modalities: { input: ['text', 'image'], output: ['text'] },
            cost: { input: 5, output: 30, cache_read: 0.5, cache_write: 6.25 },
          },
        ],
      },
    },
  };
  const remoteData = {
    providers: {
      openai: {
        models: [
          {
            id: 'gpt-5.6-sol',
            family: 'remote-gpt',
            release_date: '2026-07-09',
            modalities: { input: ['text', 'image', 'pdf'] },
            cost: { input: 5, output: 30, cache_read: 0.5 },
          },
          { id: 'gpt-5.6-sol', family: 'duplicate' },
          { id: 'gpt-5.5' },
        ],
      },
    },
  };

  const result = mergeProviderModels(remoteData, localData, 'openai', localModelIDs);
  const models = result.providers.openai.models;
  const mergedModel = models.find((model) => model.id === 'gpt-5.6-sol');

  assert.equal(models.filter((model) => model.id === 'gpt-5.6-sol').length, 1);
  assert.equal(mergedModel.family, 'remote-gpt');
  assert.equal(mergedModel.release_date, '2026-07-09');
  assert.deepEqual(mergedModel.modalities, { input: ['text', 'image', 'pdf'], output: ['text'] });
  assert.deepEqual(mergedModel.cost, { input: 5, output: 30, cache_read: 0.5, cache_write: 6.25 });
  assert.ok(models.some((model) => model.id === 'gpt-5.5'));
});

test('uses the bundled provider when the remote catalog omits it', () => {
  const localData = {
    providers: {
      openai: {
        display_name: 'OpenAI',
        models: [{ id: 'gpt-5.6-luna', cost: { input: 1, output: 6 } }],
      },
    },
  };
  const remoteData = { providers: { anthropic: { models: [{ id: 'claude-test' }] } } };

  const result = mergeProviderModels(remoteData, localData, 'openai', localModelIDs);

  assert.equal(result.providers.openai.display_name, 'OpenAI');
  assert.equal(result.providers.openai.models[0].id, 'gpt-5.6-luna');
  assert.ok(result.providers.anthropic);
});
