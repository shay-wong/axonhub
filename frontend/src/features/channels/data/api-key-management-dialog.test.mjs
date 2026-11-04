import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';
import * as management from './api-key-management.ts';
import * as credentials from './channel-input.ts';
import * as disableStatus from './disabled-api-key-status.ts';

const require = createRequire(import.meta.url);
const source = readFileSync(new URL('../components/channels-api-key-management-dialog.tsx', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, target: ts.ScriptTarget.ES2022 },
}).outputText;

function render({ disabled = [], keys = ['key-a', 'key-b'], results = {}, oauth = false, canWrite = true, ...props } = {}) {
  let stateIndex = 0;
  let refIndex = 0;
  const row = { id: 'channel-1', name: 'Example', credentials: oauth ? { oauth: { accessToken: 'token' } } : { apiKeys: keys } };
  const exports = {};
  vm.runInNewContext(compiled, {
    exports,
    require(path) {
      if (path === 'react/jsx-runtime') return require(path);
      if (path === 'react')
        return {
          useState(initial) {
            const index = stateIndex++;
            return [index === 13 ? keys : index === 3 ? results : typeof initial === 'function' ? initial() : initial, () => {}];
          },
          useMemo: (fn) => fn(),
          useRef: () => ({ current: refIndex++ === 0 ? row.id : false }),
          useEffect() {},
        };
      if (path === '@tanstack/react-query') return { useQueryClient: () => ({ invalidateQueries() {} }) };
      if (path === 'react-i18next') return { useTranslation: () => ({ t: (key) => key }) };
      if (path === '@/hooks/usePermissions') return { usePermissions: () => ({ channelPermissions: { canWrite } }) };
      if (path.endsWith('useInterval')) return { default() {} };
      if (path.endsWith('channels-context')) return { useChannels: () => ({ currentRow: row, setOpen() {} }) };
      if (path.endsWith('data/channels'))
        return new Proxy(
          {},
          {
            get: (_, name) =>
              name === 'useChannelDisabledAPIKeys'
                ? () => ({ data: disabled, isLoading: false, isError: false, refetch() {} })
                : () => ({ isPending: false, mutateAsync() {} }),
          }
        );
      if (path.endsWith('api-key-management')) return management;
      if (path.endsWith('channel-input')) return credentials;
      if (path.endsWith('disabled-api-key-status')) return disableStatus;
      if (path.endsWith('api-key-display'))
        return { createAPIKeyNameMap: () => new Map(), formatAPIKeyIdentity: (key) => key, maskAPIKeySuffix: (key) => key };
      if (path.endsWith('schema')) return { OAUTH_CREDENTIAL_REF: '__oauth__' };
      if (path === 'date-fns') return { format: () => 'date' };
      return new Proxy({}, { get: (_, name) => name });
    },
  });
  return exports.ChannelsAPIKeyManagementDialog({ open: true, onOpenChange() {}, ...props });
}

function nodes(tree) {
  if (!tree || typeof tree !== 'object') return [];
  if (Array.isArray(tree)) return tree.flatMap(nodes);
  return [tree, ...nodes(tree.props?.children)];
}

function text(tree) {
  if (tree == null || typeof tree === 'boolean') return '';
  if (Array.isArray(tree)) return tree.map(text).join(' ');
  if (typeof tree !== 'object') return String(tree);
  return text(tree.props?.children);
}

test('unified dialog renders loading immediately and preserves detail retry handler', () => {
  assert.match(text(render({ loading: true })), /common.loading/);
  let retried = false;
  const tree = render({
    loadError: true,
    onRetry: () => {
      retried = true;
    },
  });
  assert.match(text(tree), /common.errors.loadFailed/);
  nodes(tree)
    .find((node) => node.type === 'Button' && text(node) === 'common.buttons.retry')
    .props.onClick();
  assert.equal(retried, true);
});

test('OAuth disabled row supports recovery while copy, test, delete and import are unavailable', () => {
  const tree = render({
    oauth: true,
    keys: [],
    disabled: [{ key: '__oauth__', disabledAt: '2026-01-01', errorCode: 401, reason: 'expired' }],
  });
  assert.match(text(tree), /channels.dialogs.disabledAPIKeys.oauthCredential/);
  assert.doesNotMatch(text(tree), /channels.dialogs.keyManagement.importTitle/);
  const row = nodes(tree).find((node) => node.type === 'TableRow' && text(node).includes('oauthCredential'));
  const buttons = nodes(row).filter((node) => node.type === 'Button');
  const action = (icon) => buttons.find((button) => nodes(button).some((child) => child.type === icon));
  assert.equal(action('IconCopy').props.disabled, true);
  assert.equal(action('IconPlayerPlay').props.disabled, true);
  assert.equal(action('IconTrash').props.disabled, true);
  assert.equal(render({ canWrite: false }), null);
});

test('restored key does not inherit stale disabled state from its earlier test result', () => {
  const tree = render({
    keys: ['key-a', 'key-b'],
    results: { 'key-a': { success: false, disabled: true, error: 'earlier failure', latency: 1 } },
  });
  assert.doesNotMatch(text(tree), /channels.dialogs.disabledAPIKeys.enableAll/);
  assert.match(text(tree), /earlier failure/);
});
