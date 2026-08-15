import assert from 'node:assert/strict';
import test from 'node:test';

const { buildAPIKeyCredentialsForUpdate, getConfiguredAPIKeys } = await import('./channel-input.ts');

test('API key management keeps structured aliases and weights while importing and deleting keys', () => {
  const credentials = {
    apiKeyConfigs: [
      { key: ' primary ', name: 'Primary', weight: 80 },
      { key: 'backup', name: 'Backup', weight: 20 },
    ],
  };

  assert.deepEqual(getConfiguredAPIKeys(credentials), ['primary', 'backup']);
  assert.deepEqual(buildAPIKeyCredentialsForUpdate(['primary', 'new-key'], credentials), {
    apiKeyConfigs: [
      { key: 'primary', name: 'Primary', weight: 80 },
      { key: 'new-key', weight: 100 },
    ],
  });
});

test('API key management keeps legacy API key storage for legacy channels', () => {
  const credentials = { apiKeys: [' first ', 'first', 'second'] };

  assert.deepEqual(getConfiguredAPIKeys(credentials), ['first', 'second']);
  assert.deepEqual(buildAPIKeyCredentialsForUpdate(['second', ' third '], credentials), {
    apiKeys: ['second', 'third'],
  });
});
