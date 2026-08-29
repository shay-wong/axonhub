import assert from 'node:assert/strict';
import test from 'node:test';

const { getSpeedMode } = await import('./service-tier.ts');

test('uses the speed mode captured from the latest execution', () => {
  const request = {
    executions: {
      edges: [{ node: { speedMode: ' Fast ' } }, { node: { speedMode: 'standard' } }],
    },
  };

  assert.equal(getSpeedMode(request), 'fast');
});

test('does not reuse a speed mode from an older execution', () => {
  const request = {
    executions: {
      edges: [{ node: { speedMode: null } }, { node: { speedMode: 'fast' } }],
    },
  };

  assert.equal(getSpeedMode(request), '');
});

test('supports Anthropic fast without a requested service tier', () => {
  const request = {
    executions: { edges: [{ node: { speedMode: 'fast', requestedServiceTier: null } }] },
  };

  assert.equal(getSpeedMode(request), 'fast');
});

test('ignores standard provider and billing tiers', () => {
  const request = {
    executions: { edges: [{ node: { speedMode: null, requestedServiceTier: 'default' } }] },
    usageLogs: { edges: [{ node: { appliedServiceTier: 'priority', serviceTier: 'priority' } }] },
  };

  assert.equal(getSpeedMode(request), '');
});

test('derives speed mode from requested service tier for old records', () => {
  assert.equal(getSpeedMode({ executions: { edges: [{ node: { requestedServiceTier: ' Priority ' } }] } }), 'fast');
  assert.equal(getSpeedMode({ executions: { edges: [{ node: { requestedServiceTier: ' ULTRAFAST ' } }] } }), 'ultrafast');
  assert.equal(getSpeedMode({ executions: { edges: [{ node: { requestedServiceTier: ' Flex ' } }] } }), '');
  assert.equal(getSpeedMode({}), '');
});

test('uses captured ultrafast mode', () => {
  assert.equal(getSpeedMode({ executions: { edges: [{ node: { speedMode: ' Ultrafast ' } }] } }), 'ultrafast');
});

test('does not expose non-speed service tiers from captured speed mode', () => {
  assert.equal(getSpeedMode({ executions: { edges: [{ node: { speedMode: 'flex' } }] } }), '');
});
