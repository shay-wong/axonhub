import assert from 'node:assert/strict';
import test from 'node:test';

const { getChannelStatusPolicy, getChannelStatusViewModel, isChannelTemporarilyDisabled } = await import('./channel-status-policy.ts');

const futureTime = () => new Date(Date.now() + 60_000).toISOString();
const pastTime = () => new Date(Date.now() - 60_000).toISOString();

function createChannel(overrides = {}) {
  return {
    id: 'channel_1',
    name: 'Test Channel',
    status: 'enabled',
    errorMessage: null,
    temporaryDisabledUntil: null,
    disabledAPIKeys: [],
    ...overrides,
  };
}

test('prioritizes error over temporary disable and disabled keys', () => {
  const policy = getChannelStatusPolicy(
    createChannel({
      errorMessage: 'provider_error',
      temporaryDisabledUntil: futureTime(),
      disabledAPIKeys: [{ key: 'sk-disabled' }],
    }),
    { canWrite: true }
  );

  assert.equal(policy.primaryItem?.kind, 'error');
  assert.equal(policy.primaryItem?.actionID, 'resolveError');
  assert.deepEqual(
    policy.statusItems.map((item) => item.kind),
    ['error', 'temporaryDisable', 'disabledKeys']
  );
  assert.deepEqual(
    policy.menuItems.map((item) => item.kind),
    ['error', 'temporaryDisable', 'disabledKeys']
  );
});

test('keeps read-only status visible but removes executable actions', () => {
  const policy = getChannelStatusPolicy(
    createChannel({
      errorMessage: 'provider_error',
      disabledAPIKeys: [{ key: 'sk-disabled' }],
    }),
    { canWrite: false }
  );

  assert.equal(policy.primaryItem?.kind, 'error');
  assert.equal(policy.primaryItem?.actionID, undefined);
  assert.deepEqual(policy.menuItems, []);

  const viewModel = getChannelStatusViewModel(policy);

  assert.equal(viewModel.primaryItem?.tooltipKind, 'error');
  assert.equal(viewModel.primaryItem?.action, undefined);
  assert.deepEqual(
    viewModel.statusItems.map((item) => item.kind),
    ['error', 'disabledKeys']
  );
  assert.deepEqual(viewModel.menuItems, []);
});

test('detects only future temporary disable windows', () => {
  assert.equal(isChannelTemporarilyDisabled(createChannel({ temporaryDisabledUntil: futureTime() })), true);
  assert.equal(isChannelTemporarilyDisabled(createChannel({ temporaryDisabledUntil: pastTime() })), false);
  assert.equal(isChannelTemporarilyDisabled(createChannel({ temporaryDisabledUntil: 'not-a-date' })), false);
});

test('does not special-case archived channels in status action policy', () => {
  const archivedPolicy = getChannelStatusPolicy(
    createChannel({
      status: 'archived',
      errorMessage: 'provider_error',
      temporaryDisabledUntil: futureTime(),
      disabledAPIKeys: [{ key: 'sk-disabled' }],
    }),
    { canWrite: true }
  );

  assert.equal(archivedPolicy.primaryItem?.kind, 'error');
  assert.equal(archivedPolicy.primaryItem?.actionID, 'resolveError');
  assert.deepEqual(
    archivedPolicy.menuItems.map((item) => item.actionID),
    ['resolveError', 'clearTemporaryDisable', 'manageDisabledKeys']
  );
});

test('keeps all coexisting status items available for inline quick actions', () => {
  const policy = getChannelStatusPolicy(
    createChannel({
      errorMessage: 'provider_error',
      temporaryDisabledUntil: futureTime(),
      disabledAPIKeys: [{ key: 'sk-disabled' }],
    }),
    { canWrite: true }
  );
  const viewModel = getChannelStatusViewModel(policy);

  assert.deepEqual(
    viewModel.statusItems.map((item) => item.action?.id),
    ['resolveError', 'clearTemporaryDisable', 'manageDisabledKeys']
  );
  assert.equal(viewModel.primaryItem?.action?.id, 'resolveError');
});

test('maps writable disabled keys to manage action view model', () => {
  const policy = getChannelStatusPolicy(
    createChannel({
      disabledAPIKeys: [{ key: 'sk-disabled' }],
    }),
    { canWrite: true }
  );
  const viewModel = getChannelStatusViewModel(policy);

  assert.equal(viewModel.primaryItem?.kind, 'disabledKeys');
  assert.equal(viewModel.primaryItem?.tooltipKind, 'disabledKeys');
  assert.deepEqual(viewModel.primaryItem?.action, {
    id: 'manageDisabledKeys',
    quickLabelKey: 'channels.actions.quickManageDisabledKeys',
    disabled: false,
    pending: false,
  });
  assert.deepEqual(
    viewModel.menuItems.map((item) => item.action.id),
    ['manageDisabledKeys']
  );
});

test('marks temporary disable action disabled while clear mutation is pending', () => {
  const policy = getChannelStatusPolicy(createChannel({ temporaryDisabledUntil: futureTime() }), { canWrite: true });
  const viewModel = getChannelStatusViewModel(policy, { clearTemporaryDisablePending: true });

  assert.equal(viewModel.primaryItem?.kind, 'temporaryDisable');
  assert.equal(viewModel.primaryItem?.tooltipKind, 'temporaryDisable');
  assert.deepEqual(viewModel.primaryItem?.action, {
    id: 'clearTemporaryDisable',
    quickLabelKey: 'channels.actions.quickClearTemporaryDisable',
    disabled: true,
    pending: true,
  });
  assert.deepEqual(viewModel.menuItems[0]?.action, viewModel.primaryItem?.action);
});

test('maps read-only disabled keys to read-only tooltip without actions', () => {
  const policy = getChannelStatusPolicy(
    createChannel({
      disabledAPIKeys: [{ key: 'sk-disabled' }],
    }),
    { canWrite: false }
  );
  const viewModel = getChannelStatusViewModel(policy);

  assert.equal(viewModel.primaryItem?.kind, 'disabledKeys');
  assert.equal(viewModel.primaryItem?.tooltipKind, 'disabledKeysReadOnly');
  assert.equal(viewModel.primaryItem?.action, undefined);
  assert.deepEqual(viewModel.menuItems, []);
});
