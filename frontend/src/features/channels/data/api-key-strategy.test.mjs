import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { z } from 'zod';
import ts from 'typescript';
import { API_KEY_STRATEGIES, recallAPIKeyStrategy } from './api-key-strategy.ts';

test('all eight canonical strategies remain available', () => {
  assert.equal(API_KEY_STRATEGIES.length, 8);
  for (const apiKeyStrategy of API_KEY_STRATEGIES) {
    assert.equal(recallAPIKeyStrategy({ apiKeyStrategy }), apiKeyStrategy);
  }
});

test('unknown or empty policies safely use sticky', () => {
  assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: '' }), 'sticky');
  assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: 'unknown' }), 'sticky');
  assert.equal(recallAPIKeyStrategy(null), 'sticky');
  assert.equal(recallAPIKeyStrategy(undefined), 'sticky');
});

test('schema decoding accepts canonical modes and safely defaults empty or unknown policies', () => {
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
  for (const value of API_KEY_STRATEGIES) {
    assert.equal(strategySchema.parse(value), value);
  }
  for (const empty of ['', null, undefined]) {
    assert.equal(recallAPIKeyStrategy({ apiKeyStrategy: strategySchema.parse(empty) }), 'sticky');
  }
  assert.equal(strategySchema.parse('unrecognized'), 'sticky');
});

test('the dialog has one canonical strategy editor and never submits the retired field', async () => {
  const { readFile } = await import('node:fs/promises');
  const source = await readFile(new URL('../components/channels-action-dialog.tsx', import.meta.url), 'utf8');
  assert.equal((source.match(/<ApiKeyStrategyFields/g) ?? []).length, 1);
  assert.doesNotMatch(source, /apiKeySelectionStrategy/);
});
