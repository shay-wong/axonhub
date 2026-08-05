import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const {
  buildAPIKeyCredentialsForUpdate,
  buildBulkCreateChannelsVariables,
  buildCreateChannelVariables,
  buildDuplicateChannelVariables,
  buildUpdateChannelVariables,
  getConfiguredAPIKeys,
  sanitizeChannelMutationInput,
  sanitizeChannelSettingsForInput,
} = await import('./channel-input.ts');

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

test('removes output-only OpenCode Go auth cookie configured flag from channel settings input', () => {
  const settings = {
    apiKeySelectionStrategy: 'weighted_sticky',
    providerQuota: {
      opencodeGo: {
        workspaceId: 'wk_123',
        authCookie: 'new-cookie',
        authCookieConfigured: true,
        clearAuthCookie: true,
      },
    },
  };

  const sanitized = sanitizeChannelSettingsForInput(settings);

  assert.notEqual(sanitized, settings);
  assert.notEqual(sanitized.providerQuota, settings.providerQuota);
  assert.notEqual(sanitized.providerQuota.opencodeGo, settings.providerQuota.opencodeGo);
  assert.deepEqual(sanitized.providerQuota.opencodeGo, {
    workspaceId: 'wk_123',
    authCookie: 'new-cookie',
    clearAuthCookie: true,
  });
  assert.equal(settings.providerQuota.opencodeGo.authCookieConfigured, true);
});

test('projects OpenCode Go settings through the GraphQL input allow-list', () => {
  const settings = {
    providerQuota: {
      opencodeGo: {
        workspaceId: 'wk_123',
        authCookie: 'new-cookie',
        authCookieConfigured: true,
        clearAuthCookie: true,
        futureOutputOnlyFlag: 'must-not-leak',
      },
    },
  };

  const sanitized = sanitizeChannelSettingsForInput(settings);

  assert.deepEqual(sanitized.providerQuota.opencodeGo, {
    workspaceId: 'wk_123',
    authCookie: 'new-cookie',
    clearAuthCookie: true,
  });
  assert.equal(settings.providerQuota.opencodeGo.futureOutputOnlyFlag, 'must-not-leak');
});

test('leaves settings object unchanged when output-only field is absent', () => {
  const settings = {
    providerQuota: {
      opencodeGo: {
        workspaceId: 'wk_123',
        authCookie: '',
      },
    },
  };

  assert.equal(sanitizeChannelSettingsForInput(settings), settings);
});

test('sanitizes mutation input through the shared helper', () => {
  const input = {
    name: 'OpenCode Go',
    settings: {
      providerQuota: {
        opencodeGo: {
          workspaceId: 'wk_123',
          authCookieConfigured: false,
        },
      },
    },
  };

  const sanitized = sanitizeChannelMutationInput(input);

  assert.notEqual(sanitized, input);
  assert.deepEqual(sanitized, {
    name: 'OpenCode Go',
    settings: {
      providerQuota: {
        opencodeGo: {
          workspaceId: 'wk_123',
        },
      },
    },
  });
});

test('builds sanitized create duplicate bulk and update mutation variables', () => {
  const input = {
    name: 'OpenCode Go',
    settings: {
      providerQuota: {
        opencodeGo: {
          workspaceId: 'wk_123',
          authCookieConfigured: false,
          futureOutputOnlyFlag: 'must-not-leak',
        },
      },
    },
  };

  const createVariables = buildCreateChannelVariables(input);
  const duplicateVariables = buildDuplicateChannelVariables('channel_1', input);
  const bulkVariables = buildBulkCreateChannelsVariables(input);
  const updateVariables = buildUpdateChannelVariables('channel_2', input);

  assert.deepEqual(createVariables.input.settings.providerQuota.opencodeGo, { workspaceId: 'wk_123' });
  assert.deepEqual(duplicateVariables.input.settings.providerQuota.opencodeGo, { workspaceId: 'wk_123' });
  assert.deepEqual(bulkVariables.input.settings.providerQuota.opencodeGo, { workspaceId: 'wk_123' });
  assert.deepEqual(updateVariables.input.settings.providerQuota.opencodeGo, { workspaceId: 'wk_123' });
  assert.equal(duplicateVariables.sourceID, 'channel_1');
  assert.equal(updateVariables.id, 'channel_2');
});

test('channel hooks use sanitized mutation variable builders', () => {
  const channelsSourcePath = join(import.meta.dirname, 'channels.ts');
  const channelsSource = readFileSync(channelsSourcePath, 'utf8');

  assert.match(channelsSource, /CREATE_CHANNEL_MUTATION,\s*buildCreateChannelVariables\(input\)/);
  assert.match(channelsSource, /DUPLICATE_CHANNEL_MUTATION,\s*buildDuplicateChannelVariables\(sourceID, input\)/);
  assert.match(channelsSource, /BULK_CREATE_CHANNELS_MUTATION,\s*buildBulkCreateChannelsVariables\(input\)/);
  assert.match(channelsSource, /UPDATE_CHANNEL_MUTATION,\s*buildUpdateChannelVariables\(id, input\)/);
});
