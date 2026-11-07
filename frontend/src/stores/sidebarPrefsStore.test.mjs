import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./sidebarPrefsStore.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
}).outputText;

function loadStore(initial = {}, { failWrites = false } = {}) {
  const values = new Map(Object.entries(initial));
  const localStorage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      if (failWrites) throw new Error('storage unavailable');
      values.set(key, value);
    },
  };
  const exports = {};
  const create = () => (initializer) => {
    let state;
    const set = (update) => {
      state = { ...state, ...(typeof update === 'function' ? update(state) : update) };
    };
    const get = () => state;
    state = initializer(set, get);
    return { getState: get };
  };
  Function('exports', 'require', 'localStorage', compiled)(exports, (id) => {
    if (id === 'zustand') return { create };
    throw new Error(`Unexpected import: ${id}`);
  }, localStorage);
  return { exports, values };
}

test('migrates an existing hidden system item to all split system pages once', () => {
  const { exports, values } = loadStore({ axonhub_hidden_nav_items: JSON.stringify(['/channels', '/system']) });
  assert.deepEqual(exports.getHiddenNavItems(), [
    '/channels',
    '/system',
    '/system/requests',
    '/system/integrations',
    '/system/operations',
  ]);
  assert.equal(values.get(exports.HIDDEN_NAV_ITEMS_MIGRATION_VERSION_KEY), '1');

  values.set(exports.HIDDEN_NAV_ITEMS_STORAGE_KEY, JSON.stringify(['/system']));
  assert.deepEqual(exports.getHiddenNavItems(), ['/system']);
});

test('reset stays empty after the one-time migration', () => {
  const { exports, values } = loadStore({ axonhub_hidden_nav_items: JSON.stringify(['/system']) });
  exports.useSidebarPrefsStore.getState().reset();
  assert.deepEqual(JSON.parse(values.get(exports.HIDDEN_NAV_ITEMS_STORAGE_KEY)), []);
  assert.deepEqual(exports.getHiddenNavItems(), []);
});

test('does not hide split pages for users who had not hidden system settings', () => {
  const { exports } = loadStore({ axonhub_hidden_nav_items: JSON.stringify(['/channels']) });
  assert.deepEqual(exports.getHiddenNavItems(), ['/channels']);
});

test('marks new profiles as migrated before they customize navigation', () => {
  const { exports, values } = loadStore();
  assert.deepEqual(exports.getHiddenNavItems(), []);
  values.set(exports.HIDDEN_NAV_ITEMS_STORAGE_KEY, JSON.stringify(['/system']));
  assert.deepEqual(exports.getHiddenNavItems(), ['/system']);
});

test('returns the in-memory migration when persisting it fails', () => {
  const { exports } = loadStore({ axonhub_hidden_nav_items: JSON.stringify(['/channels', '/system']) }, { failWrites: true });
  assert.deepEqual(exports.getHiddenNavItems(), [
    '/channels',
    '/system',
    '/system/requests',
    '/system/integrations',
    '/system/operations',
  ]);
});
