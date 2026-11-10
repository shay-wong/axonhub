import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./channels-action-dialog.tsx', import.meta.url), 'utf8');
const ast = ts.createSourceFile('dialog.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const declarations = new Map();
const effects = [];
const handlers = {};
function visit(node) {
  if (ts.isVariableDeclaration(node)) declarations.set(node.name.getText(ast), node.initializer);
  if (ts.isCallExpression(node) && node.expression.getText(ast) === 'useEffect') effects.push(node.arguments[0]);
  if (ts.isJsxAttribute(node) && node.initializer && ts.isJsxExpression(node.initializer)) {
    const value = node.initializer.expression;
    if (node.name.text === 'onClick' && value?.getText(ast).includes('setConfirmOpenModelMapping(true)')) handlers.click = value;
    if (node.parent.parent.tagName.getText(ast) === 'ConfirmDialog' && node.parent.getText(ast).includes('confirmOpenModelMapping')) {
      if (node.name.text === 'onOpenChange') handlers.cancel = value;
      if (node.name.text === 'handleConfirm') handlers.confirm = value;
    }
  }
  ts.forEachChild(node, visit);
}
visit(ast);

function evaluate(node, scope) {
  assert.ok(node);
  const { outputText } = ts.transpileModule(`(${node.getText(ast)})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2023 },
  });
  return new Function('scope', `with (scope) { return ${outputText} }`)(scope);
}

function createHarness() {
  const row = {
    type: 'openai', supportedModels: ['gpt-test'], manualModels: [],
    credentials: { apiKeys: ['test-key'], apiKeyConfigs: [{ key: 'test-key', name: 'saved', weight: 100 }] },
    settings: { retryPerAPIKey: false, apiKeyStrategy: 'sticky' },
  };
  const snapshot = declarations.get('localSettingsSnapshot');
  const scope = Object.fromEntries(snapshot.arguments[0].properties.map(property => [property.name.text, '']));
  Object.assign(scope, {
    initialRow: row, open: true, dirtyFields: {}, apiKeyConfigsDirty: false,
    apiKeyConfigs: structuredClone(row.credentials.apiKeyConfigs), retryPerAPIKey: false,
    form: { reset() { scope.dirtyFields = {}; } },
    getSavedAuthMode: () => 'third-party',
    getInitialAPIKeyConfigs: value => structuredClone(value.credentials.apiKeyConfigs),
    getInitialAPIKeys: value => value.credentials.apiKeys,
    recallAPIKeyStrategy: value => value.apiKeyStrategy,
    recallQuotaRoutingMode: () => '', formatRetryableStatusCodes: () => '',
    formatRetryableErrorPatterns: () => '', formatMaxSingleChannelRetries: () => '',
    formatResponseTimeoutSeconds: () => '', getProviderFromChannelType: () => 'openai',
    getInitialApiFormatForChannel: () => 'openai/chat_completions',
    getResponsesTransportFromChannel: () => 'http',
    CHANNEL_CONFIGS: { openai: { apiFormat: 'openai/chat_completions' } },
    OPENAI_CHAT_COMPLETIONS: 'openai/chat_completions', ProxyType: { ENVIRONMENT: 'environment' },
    navigations: 0, confirmOpenModelMapping: false,
    onOpenModelMapping() { scope.navigations++; scope.open = false; },
  });
  // Execute the component's actual callbacks with state setters; no React render is needed.
  const setterNames = new Set(source.match(/\bset[A-Z]\w*(?=\()/g));
  for (const name of setterNames) {
    const state = name.slice(3, 4).toLowerCase() + name.slice(4);
    scope[name] = value => { scope[state] = typeof value === 'function' ? value(scope[state]) : value; };
  }
  scope.resetDialogState = evaluate(declarations.get('resetDialogState'), scope);
  scope.openModelMapping = evaluate(declarations.get('openModelMapping'), scope);
  const keyReload = effects.find(effect => effect.getText(ast).includes('const nextConfigs = getInitialAPIKeyConfigs(initialRow)'));
  scope.reopen = () => { scope.open = true; evaluate(keyReload, scope)(); };
  scope.render = () => {
    scope.hasFormChanges = evaluate(declarations.get('hasFormChanges'), scope);
    scope.localSettingsSnapshot = evaluate(snapshot, scope);
  };
  scope.render();
  scope.initialLocalSettings = { current: scope.localSettingsSnapshot };
  return scope;
}

for (const metadata of [{ name: 'edited' }, { weight: 250 }]) {
  test(`alias navigation confirms a Key ${Object.keys(metadata)[0]} edit and cancellation preserves it`, () => {
    const scope = createHarness();
    Object.assign(scope.apiKeyConfigs[0], metadata);
    scope.apiKeyConfigsDirty = true;
    scope.render();
    evaluate(handlers.click, scope)();
    assert.equal(scope.confirmOpenModelMapping, true);
    assert.equal(scope.navigations, 0);
    evaluate(handlers.cancel, scope)(false);
    assert.equal(scope.confirmOpenModelMapping, false);
    assert.equal(scope.apiKeyConfigsDirty, true);
    assert.deepEqual(scope.apiKeyConfigs[0], { key: 'test-key', name: 'saved', weight: 100, ...metadata });
    evaluate(handlers.confirm, scope)();
    assert.equal(scope.navigations, 1);
    scope.reopen();
    assert.deepEqual(scope.apiKeyConfigs, scope.initialRow.credentials.apiKeyConfigs);
    assert.equal(scope.apiKeyConfigsDirty, false);
  });
}

test('alias navigation confirms retry-per-Key changes and restores saved settings after discard', () => {
  const scope = createHarness();
  scope.retryPerAPIKey = true;
  scope.render();
  evaluate(handlers.click, scope)();
  assert.equal(scope.confirmOpenModelMapping, true);
  evaluate(handlers.cancel, scope)(false);
  assert.equal(scope.retryPerAPIKey, true);
  assert.equal(scope.navigations, 0);
  evaluate(handlers.confirm, scope)();
  assert.equal(scope.retryPerAPIKey, false);
  assert.equal(scope.navigations, 1);
  scope.reopen();
  scope.render();
  assert.equal(scope.retryPerAPIKey, false);
});
