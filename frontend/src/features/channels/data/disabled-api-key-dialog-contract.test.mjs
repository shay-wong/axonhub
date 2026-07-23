import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const dataDir = import.meta.dirname;
const srcRoot = join(dataDir, '..', '..', '..');

function read(relativePath) {
  return readFileSync(join(srcRoot, relativePath), 'utf8');
}

test('disabled API key dialog queries and fully renders the disable reason', () => {
  // The management dialog uses a dedicated query, so the reason must be selected and typed there explicitly.
  const channelsData = read('features/channels/data/channels.ts');
  const dialog = read('features/channels/components/channels-disabled-api-keys-dialog.tsx');
  const queryStart = channelsData.indexOf('const GET_CHANNEL_DISABLED_API_KEYS_QUERY');
  const queryEnd = channelsData.indexOf('const GET_CHANNEL_MODEL_PRICES_QUERY', queryStart);
  const hookStart = channelsData.indexOf('export function useChannelDisabledAPIKeys');
  const hookEnd = channelsData.indexOf('export function useDisableChannelAPIKey', hookStart);

  assert.match(channelsData.slice(queryStart, queryEnd), /disabledAPIKeys\s*\{[\s\S]*\breason\b/);
  assert.match(channelsData.slice(hookStart, hookEnd), /reason\?:\s*string\s*\|\s*null/);
  assert.match(dialog, /whitespace-pre-wrap break-words[^>]*>\{dk\.reason\}<\/span>/);
  assert.doesNotMatch(dialog, /truncate[^>]*>\{dk\.reason\}/);
});

test('disabled API key reason label is localized', () => {
  const en = JSON.parse(read('locales/en/channels.json'));
  const zh = JSON.parse(read('locales/zh-CN/channels.json'));

  assert.equal(en['channels.dialogs.disabledAPIKeys.reason'], 'Disable reason');
  assert.equal(zh['channels.dialogs.disabledAPIKeys.reason'], '禁用原因');
});
