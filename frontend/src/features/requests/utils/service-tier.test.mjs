import assert from 'node:assert/strict';
import test from 'node:test';

const { getRequestedServiceTier } = await import('./service-tier.ts');

test('uses the tier requested by the latest execution', () => {
  const request = {
    executions: {
      edges: [{ node: { requestedServiceTier: ' Priority ' } }, { node: { requestedServiceTier: 'default' } }],
    },
  };

  assert.equal(getRequestedServiceTier(request), 'priority');
});

test('does not reuse a tier from an older execution', () => {
  const request = {
    executions: {
      edges: [{ node: { requestedServiceTier: null } }, { node: { requestedServiceTier: 'priority' } }],
    },
  };

  assert.equal(getRequestedServiceTier(request), '');
});

test('ignores the provider-applied or billing-effective tier', () => {
  const fastRequestWithDefaultBilling = {
    executions: { edges: [{ node: { requestedServiceTier: 'priority' } }] },
    usageLogs: { edges: [{ node: { appliedServiceTier: 'default', serviceTier: 'default' } }] },
  };
  const defaultRequestWithPriorityBilling = {
    executions: { edges: [{ node: { requestedServiceTier: 'default' } }] },
    usageLogs: { edges: [{ node: { appliedServiceTier: 'priority', serviceTier: 'priority' } }] },
  };

  assert.equal(getRequestedServiceTier(fastRequestWithDefaultBilling), 'priority');
  assert.equal(getRequestedServiceTier(defaultRequestWithPriorityBilling), 'default');
});

test('preserves unknown requested tiers and supports old records', () => {
  assert.equal(getRequestedServiceTier({ executions: { edges: [{ node: { requestedServiceTier: ' Flex ' } }] } }), 'flex');
  assert.equal(getRequestedServiceTier({}), '');
});
