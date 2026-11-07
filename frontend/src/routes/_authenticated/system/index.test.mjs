import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./index.tsx', import.meta.url), 'utf8');
const functionSource = source.match(/export function getLegacySystemTabRedirect[\s\S]*?\n}/)?.[0];
assert.ok(functionSource, 'legacy system tab mapping function must exist');
const code = ts.transpileModule(`${source.match(/const LEGACY_SYSTEM_TAB_SECTIONS[\s\S]*?as const;/)?.[0]}\n${functionSource}`, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const { getLegacySystemTabRedirect } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('redirects every legacy system tab to its split settings page', () => {
  assert.deepEqual(getLegacySystemTabRedirect('retry'), { section: 'requests', tab: 'retry' });
  assert.deepEqual(getLegacySystemTabRedirect('proxy'), { section: 'requests', tab: 'proxy' });
  assert.deepEqual(getLegacySystemTabRedirect('quota'), { section: 'requests', tab: 'quota' });
  assert.deepEqual(getLegacySystemTabRedirect('webhook'), { section: 'integrations', tab: 'webhook' });
  assert.deepEqual(getLegacySystemTabRedirect('claude-code'), { section: 'integrations', tab: 'claude-code' });
  assert.deepEqual(getLegacySystemTabRedirect('codex'), { section: 'integrations', tab: 'codex' });
  assert.deepEqual(getLegacySystemTabRedirect('storage'), { section: 'operations', tab: 'storage' });
  assert.deepEqual(getLegacySystemTabRedirect('diagnostics'), { section: 'operations', tab: 'diagnostics' });
  assert.deepEqual(getLegacySystemTabRedirect('backup'), { section: 'operations', tab: 'backup' });
});

test('keeps general, about and unknown tabs on the root system page', () => {
  assert.equal(getLegacySystemTabRedirect('general'), null);
  assert.equal(getLegacySystemTabRedirect('about'), null);
  assert.equal(getLegacySystemTabRedirect('unknown'), null);
  assert.equal(getLegacySystemTabRedirect('constructor'), null);
  assert.equal(getLegacySystemTabRedirect('toString'), null);
  assert.equal(getLegacySystemTabRedirect('__proto__'), null);
  assert.equal(getLegacySystemTabRedirect(undefined), null);
});
