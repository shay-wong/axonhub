import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const directory = dirname(fileURLToPath(import.meta.url));
const read = (path) => readFileSync(resolve(directory, path), 'utf8');

test('about settings exposes permission-gated update, rollback, and restart actions', () => {
  const source = read('../components/about-settings.tsx');
  assert.match(source, /hasSystemScope\('write_settings'\)/);
  assert.match(source, /Boolean\(version\?\.buildTime\)/);
  assert.match(source, /!version\?\.platform\.startsWith\('windows\/'\)/);
  assert.match(source, /systemApi\.installUpdate\(includeBeta\)/);
  assert.match(source, /systemApi\s*\.getRollbackVersions\(\)/);
  assert.match(source, /systemApi\.rollback\(selectedRollbackVersion\)/);
  assert.match(source, /<Select\s/);
  assert.match(source, /<AlertDialogTitle>\{t\('system\.about\.rollback\.confirmTitle'\)\}/);
  assert.match(source, /systemApi\.restart\(\)/);
  assert.match(source, /fetch\('\/health', \{ cache: 'no-store' \}\)/);
  assert.match(source, /window\.location\.reload\(\)/);
});

test('update actions have English and Chinese translations', () => {
  for (const locale of ['en', 'zh-CN']) {
    const messages = JSON.parse(read(`../../../locales/${locale}/system.json`));
    for (const key of [
      'system.about.updateCheck.install',
      'system.about.updateCheck.confirmDescription',
      'system.about.updateCheck.restart',
      'system.about.rollback.title',
      'system.about.rollback.confirmDescription',
      'system.about.rollback.success',
    ]) {
      assert.equal(typeof messages[key], 'string', `${locale}: ${key}`);
      assert.notEqual(messages[key].trim(), '', `${locale}: ${key}`);
    }
  }
});
