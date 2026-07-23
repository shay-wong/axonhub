import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const source = readFileSync(join(import.meta.dirname, 'system.ts'), 'utf8');

function getHookSource(name, nextName) {
  const start = source.indexOf(`export function ${name}`);
  const end = source.indexOf(`export function ${nextName}`, start);

  assert.notEqual(start, -1, `${name} should exist`);
  assert.notEqual(end, -1, `${nextName} should follow ${name}`);

  return source.slice(start, end);
}

test('brand settings remain available without read_settings permission', () => {
  // Brand name, title, and logo are public presentation data used outside the settings page.
  const hook = getHookSource('useBrandSettings', 'useStoragePolicy');

  assert.match(hook, /enabled:\s*options\?\.enabled !== false/);
  assert.doesNotMatch(hook, /hasSystemScope\('read_settings'\)/);
});

test('protected system settings still require read_settings permission', () => {
  const hook = getHookSource('useStoragePolicy', 'useUpdateBrandSettings');

  assert.match(hook, /enabled:\s*hasSystemScope\('read_settings'\)/);
});
