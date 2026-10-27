import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { z } from 'zod';
import ts from 'typescript';
import { recallAPIKeyStrategy } from './api-key-strategy.ts';

test('canonical strategy wins over legacy selection strategy', () => {
  assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: 'random', apiKeySelectionStrategy: 'failover' }), 'random');
});

test('legacy selection strategies map into the canonical policy', () => {
  assert.equal(recallAPIKeyStrategy({ apiKeySelectionStrategy: 'trace_sticky' }), 'sticky');
  assert.equal(recallAPIKeyStrategy({ apiKeySelectionStrategy: 'weighted_sticky' }), 'weighted_sticky');
  assert.equal(recallAPIKeyStrategy({ apiKeySelectionStrategy: 'failover' }), 'failover');
});

test('unknown or empty policies safely use sticky', () => {
  assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: '' }), 'sticky');
  assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: 'unknown', apiKeySelectionStrategy: 'failover' }), 'sticky');
  assert.equal(recallAPIKeyStrategy(null), 'sticky');
});

test('schema decoding preserves a legacy weighted policy and accepts all canonical modes', () => {
  const source = readFileSync(new URL('./schema.ts', import.meta.url), 'utf8');
  const ast = ts.createSourceFile('schema.ts', source, ts.ScriptTarget.Latest, true);
  let field;
  function visit(node) {
    if (ts.isPropertyAssignment(node) && node.name.getText(ast) === 'apiKeyStrategy') field = node.initializer;
    ts.forEachChild(node, visit);
  }
  visit(ast);
  assert.ok(field);
  const strategySchema = new Function('z', `return ${field.getText(ast)}`)(z);
  for (const value of ['sticky', 'random', 'round_robin', 'round_robin_success', 'priority', 'fixed', 'weighted_sticky', 'failover']) {
    assert.equal(strategySchema.parse(value), value);
  }
  for (const empty of ['', null, undefined]) {
    assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: strategySchema.parse(empty), apiKeySelectionStrategy: 'weighted_sticky' }), 'weighted_sticky');
  }
  assert.equal(strategySchema.parse('unrecognized'), 'sticky');
});

test('the dialog has one canonical strategy editor and clears the legacy field on save', async () => {
  const { readFile } = await import('node:fs/promises');
  const source = await readFile(new URL('../components/channels-action-dialog.tsx', import.meta.url), 'utf8');
  assert.equal((source.match(/<ApiKeyStrategyFields/g) ?? []).length, 1);
  assert.match(source, /apiKeySelectionStrategy:\s*''/);
  assert.doesNotMatch(source, /apiKeySelectionStrategy\.label/);
});
