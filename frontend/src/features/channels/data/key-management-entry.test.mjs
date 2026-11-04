import React from 'react';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import test from 'node:test';
import vm from 'node:vm';
import { renderToStaticMarkup } from 'react-dom/server';
import ts from 'typescript';

const require = createRequire(import.meta.url);
const source = readFileSync(new URL('../components/channels-dialogs.tsx', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
}).outputText;

function renderDialogs(details, canWrite = true) {
  const row = { id: 'channel-1', name: 'Example' };
  const context = { open: 'keyManagement', currentRow: row, setOpen() {}, setCurrentRow() {}, selectedChannels: [] };
  const exports = {};
  vm.runInNewContext(compiled, {
    exports,
    require(path) {
      if (path === 'react' || path === 'react/jsx-runtime') return require(path);
      if (path === '@/hooks/usePermissions') return { usePermissions: () => ({ channelPermissions: { canWrite } }) };
      if (path.endsWith('channels-context')) return { useChannels: () => context };
      if (path.endsWith('data/channels')) return { useChannelDetails: () => details };
      if (path.endsWith('channels-api-key-management-dialog')) {
        return {
          ChannelsAPIKeyManagementDialog: ({ open, loading, loadError }) =>
            React.createElement('section', { 'data-open': open, 'data-loading': loading, 'data-error': loadError }, 'Key management'),
        };
      }
      return new Proxy({}, { get: () => () => null });
    },
  });
  return renderToStaticMarkup(React.createElement(exports.ChannelsDialogs));
}

test('key management shell opens before channel details arrive', () => {
  const html = renderDialogs({ data: undefined, isError: false });
  assert.match(html, /data-open="true"/);
  assert.match(html, /data-loading="true"/);
  assert.match(html, /Key management/);
});

test('detail failure keeps the dialog visible with an error state', () => {
  const html = renderDialogs({ data: undefined, isError: true });
  assert.match(html, /data-open="true"/);
  assert.match(html, /data-loading="false"/);
  assert.match(html, /data-error="true"/);
});

test('disabled-key icon and menu share the key-management entry', () => {
  const columns = readFileSync(new URL('../components/channels-columns.tsx', import.meta.url), 'utf8');
  assert.match(columns, /const handleDisabledKeys[\s\S]*?setOpen\('keyManagement'\)/);
  assert.doesNotMatch(columns, /setOpen\('disabledAPIKeys'\)/);
  assert.doesNotMatch(source, /ChannelsDisabledAPIKeysDialog/);
});

test('read-only users cannot mount the credential-management dialog', () => {
  assert.doesNotMatch(renderDialogs({ data: undefined, isError: false }, false), /Key management/);
});
