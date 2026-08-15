import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const columns = await readFile(new URL('./requests/components/requests-columns.tsx', import.meta.url), 'utf8');
const table = await readFile(new URL('./requests/components/requests-table.tsx', import.meta.url), 'utf8');
const mobileHook = await readFile(new URL('../hooks/use-mobile.tsx', import.meta.url), 'utf8');
const visibilitySource = await readFile(new URL('./requests/utils/column-visibility.ts', import.meta.url), 'utf8');
const { outputText: visibilityModule } = ts.transpileModule(visibilitySource, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
});
const { migrateRequestColumnVisibilityPayload, REQUEST_COLUMN_VISIBILITY_STORAGE_VERSION } = await import(
  `data:text/javascript;base64,${Buffer.from(visibilityModule).toString('base64')}`
);

test('mobile request table hides the beta speed-mode column by default below 768px', () => {
  const hiddenColumnIDs = columns.match(/DEFAULT_MOBILE_HIDDEN_COLUMN_IDS = \[([\s\S]*?)\];/)?.[1];

  assert.ok(hiddenColumnIDs);
  assert.match(hiddenColumnIDs, /'requestedServiceTier'/);
  assert.match(mobileHook, /MOBILE_BREAKPOINT = 768/);
  assert.match(table, /window\.innerWidth < MOBILE_BREAKPOINT/);
  assert.match(table, /Object\.fromEntries\(DEFAULT_MOBILE_HIDDEN_COLUMN_IDS\.map\(\(id\) => \[id, false\]\)\)/);
});

test('migrates v2 visibility overrides to the redesigned request columns', () => {
  assert.equal(REQUEST_COLUMN_VISIBILITY_STORAGE_VERSION, 6);
  assert.deepEqual(
    migrateRequestColumnVisibilityPayload({
      v: 2,
      overrides: {
        source: false,
        requestedServiceTier: false,
        apiKey: false,
        tokens: false,
        readCache: false,
        writeCache: false,
        latency: true,
      },
    }),
    {
      source: false,
      requestedServiceTier: false,
      caller: false,
      tokens: false,
      readCache: false,
      writeCache: false,
      duration: true,
    }
  );
});

test('preserves current IDs and legacy consolidated visibility semantics', () => {
  assert.deepEqual(
    migrateRequestColumnVisibilityPayload({
      v: 2,
      overrides: {
        caller: true,
        apiKey: false,
        usage: false,
        tokens: true,
        readCache: false,
        writeCache: false,
        duration: false,
        latency: true,
      },
    }),
    { caller: true, tokens: false, readCache: false, writeCache: false, duration: false }
  );
  assert.deepEqual(migrateRequestColumnVisibilityPayload({ v: 2, overrides: { tokens: false } }), {
    tokens: true,
    readCache: true,
    writeCache: true,
  });
  assert.deepEqual(
    migrateRequestColumnVisibilityPayload({ v: 3, overrides: { usage: false, caller: true, duration: false } }),
    { caller: true, duration: false, tokens: false, readCache: false, writeCache: false }
  );
  assert.deepEqual(migrateRequestColumnVisibilityPayload({ v: 6, overrides: { tokens: false, readCache: true } }), {
    tokens: false,
    readCache: true,
  });
  assert.deepEqual(migrateRequestColumnVisibilityPayload({ source: false, requestedServiceTier: true, createdAt: false }), {
    requestedServiceTier: true,
    createdAt: false,
  });
});

test('caller column is omitted without API key read permission', () => {
  assert.match(columns, /\.\.\.\(permissions\.canViewApiKeys\s*\?\s*\(\[\s*\{\s*id: 'caller'/);
});
