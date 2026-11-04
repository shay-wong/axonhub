import assert from 'node:assert/strict';
import test from 'node:test';
import { getManagedAPIKeyRows, getManagementConfiguredKeys, hasOAuthCredentials } from './api-key-management.ts';

test('configured identities include legacy and Vertex keys but never OAuth JSON or stale scalar fallbacks', () => {
  assert.deepEqual(getManagementConfiguredKeys({ apiKey: ' legacy-key ' }), ['legacy-key']);
  assert.deepEqual(getManagementConfiguredKeys({ apiKey: 'old', apiKeys: ['current'] }), ['current']);
  assert.deepEqual(
    getManagementConfiguredKeys({ apiKeys: ['shared'], vertexApiKeys: [{ apiKey: 'shared', projectID: 'p', region: 'r' }] }),
    ['shared']
  );
  assert.deepEqual(getManagementConfiguredKeys({ apiKey: '{"access_token":"token"}' }), []);
  assert.deepEqual(getManagementConfiguredKeys({ oauth: { accessToken: 'token' }, apiKeys: ['stale'] }), []);
});

test('OAuth controls recognize both structured and legacy stored credentials', () => {
  assert.equal(hasOAuthCredentials({ oauth: { accessToken: 'token' } }), true);
  assert.equal(hasOAuthCredentials({ apiKey: '  {"access_token":"token"}' }), true);
  assert.equal(hasOAuthCredentials({ apiKey: 'regular-key' }), false);
  assert.equal(hasOAuthCredentials({ oauth: {} }), false);
  assert.equal(hasOAuthCredentials(undefined), false);
});

test('management keeps configured identities and disabled-only OAuth credentials without duplicate rows', () => {
  const now = Date.parse('2026-11-04T00:00:00Z');
  const disabled = [
    {
      key: 'team-key',
      disabledAt: '2026-11-03T00:00:00Z',
      disabledUntil: '2026-11-04T00:00:10Z',
      errorCode: 429,
      reason: 'Quota exhausted',
    },
    { key: '__oauth__', disabledAt: '2026-11-03T00:00:00Z', errorCode: 401, reason: 'Invalid credential' },
  ];
  const rows = getManagedAPIKeyRows(['pro-key', 'team-key', 'team-key'], disabled, now);
  assert.deepEqual(
    rows.map((row) => row.key),
    ['pro-key', 'team-key', '__oauth__']
  );
  assert.equal(rows[1].disabled.reason, 'Quota exhausted');
  assert.equal(rows[1].disabled.disableStatus.remainingSeconds, 10);
  assert.equal(rows[2].disabled.disableStatus.kind, 'permanent');
  const expired = getManagedAPIKeyRows(['team-key'], disabled, now + 10_000);
  assert.equal(expired[0].disabled, undefined);
  assert.equal(expired[1].key, '__oauth__');
  assert.deepEqual(
    getManagedAPIKeyRows(['team-key'], [], now).map((row) => row.key),
    ['team-key']
  );
});
