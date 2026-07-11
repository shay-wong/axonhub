import assert from 'node:assert/strict';
import test from 'node:test';

const { createAPIKeyNameMap, formatAPIKeyIdentity, maskAPIKeySuffix } = await import('./api-key-display.ts');

test('formats channel API key aliases with a safe suffix', () => {
  assert.equal(formatAPIKeyIdentity('sk-upstream-1234', 'Primary Account'), 'Primary Account · ****1234');
  assert.equal(formatAPIKeyIdentity('sk-upstream-1234'), '****1234');
  assert.equal(maskAPIKeySuffix('short'), '****');
});

test('builds a trimmed API key alias lookup', () => {
  const names = createAPIKeyNameMap([
    { key: ' sk-primary-1234 ', name: ' Primary Account ', weight: 100 },
    { key: 'sk-backup-5678', name: '', weight: 100 },
  ]);

  assert.equal(names.get('sk-primary-1234'), 'Primary Account');
  assert.equal(names.has('sk-backup-5678'), false);
});
