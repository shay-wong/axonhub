import assert from 'node:assert/strict';
import test from 'node:test';

const { formatDisabledAPIKeyCountdown, getActiveDisabledAPIKeyStatus } =
  await import('./disabled-api-key-status.ts');

// Temporary key disables must disappear exactly when routing treats the key as active again.
test('formats the remaining disable window and detects expiration', () => {
  const now = Date.parse('2026-07-23T07:00:00.000Z');
  const disabledUntil = '2026-07-23T08:01:02.000Z';

  assert.deepEqual(getActiveDisabledAPIKeyStatus(disabledUntil, now), {
    kind: 'temporary',
    disabledUntilMs: Date.parse(disabledUntil),
    remainingSeconds: 3662,
  });
  assert.equal(formatDisabledAPIKeyCountdown(3662), '01:01:02');
  assert.equal(getActiveDisabledAPIKeyStatus(disabledUntil, Date.parse(disabledUntil)), null);
  assert.deepEqual(getActiveDisabledAPIKeyStatus(null, now), { kind: 'permanent' });
  assert.equal(getActiveDisabledAPIKeyStatus('not-a-date', now), null);
});
