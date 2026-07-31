import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const columns = await readFile(new URL('./requests/components/requests-columns.tsx', import.meta.url), 'utf8');
const table = await readFile(new URL('./requests/components/requests-table.tsx', import.meta.url), 'utf8');
const mobileHook = await readFile(new URL('../hooks/use-mobile.tsx', import.meta.url), 'utf8');

test('mobile request table hides the beta speed-mode column by default below 768px', () => {
  const hiddenColumnIDs = columns.match(/DEFAULT_MOBILE_HIDDEN_COLUMN_IDS = \[([\s\S]*?)\];/)?.[1];

  assert.ok(hiddenColumnIDs);
  assert.match(hiddenColumnIDs, /'requestedServiceTier'/);
  assert.match(mobileHook, /MOBILE_BREAKPOINT = 768/);
  assert.match(table, /window\.innerWidth < MOBILE_BREAKPOINT/);
  assert.match(table, /Object\.fromEntries\(DEFAULT_MOBILE_HIDDEN_COLUMN_IDS\.map\(\(id\) => \[id, false\]\)\)/);
});

test('legacy user visibility overrides stay hidden on desktop and mobile', () => {
  const legacy = { source: false, requestedServiceTier: false };
  const mobileDefaults = { source: false, requestedServiceTier: false, channel: false };

  assert.match(table, /else \{\s+overrides = parsed as VisibilityState;\s+\}/);
  assert.deepEqual({ ...legacy }, legacy);
  assert.deepEqual({ ...mobileDefaults, ...legacy }, mobileDefaults);
});
