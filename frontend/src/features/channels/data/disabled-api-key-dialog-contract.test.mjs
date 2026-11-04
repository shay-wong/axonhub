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
  const dialog = read('features/channels/components/channels-api-key-management-dialog.tsx');
  const queryStart = channelsData.indexOf('const GET_CHANNEL_DISABLED_API_KEYS_QUERY');
  const queryEnd = channelsData.indexOf('const GET_CHANNEL_MODEL_PRICES_QUERY', queryStart);
  const hookStart = channelsData.indexOf('export function useChannelDisabledAPIKeys');
  const hookEnd = channelsData.indexOf('export function useDisableChannelAPIKey', hookStart);

  assert.match(channelsData.slice(queryStart, queryEnd), /disabledAPIKeys\s*\{[\s\S]*\breason\b/);
  assert.match(channelsData.slice(hookStart, hookEnd), /reason\?:\s*string\s*\|\s*null/);
  const reasonSpan = dialog.match(/<span className='([^']*)'>\{dk\.reason\}<\/span>/);
  assert.ok(reasonSpan, 'the full disable reason must be rendered as text');
  assert.ok(reasonSpan[1].split(/\s+/).includes('whitespace-pre-wrap'));
  assert.ok(reasonSpan[1].split(/\s+/).includes('break-words'));
  assert.doesNotMatch(reasonSpan[1], /truncate|line-clamp/);
});

test('disabled API key reason label is localized', () => {
  const en = JSON.parse(read('locales/en/channels.json'));
  const zh = JSON.parse(read('locales/zh-CN/channels.json'));

  assert.equal(en['channels.dialogs.disabledAPIKeys.reason'], 'Disable reason');
  assert.equal(zh['channels.dialogs.disabledAPIKeys.reason'], '禁用原因');
});
