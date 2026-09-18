import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./channels-action-dialog.tsx', import.meta.url), 'utf8');
const ast = ts.createSourceFile('dialog.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
let patchExpression;
function visit(node) {
  if (ts.isVariableDeclaration(node) && node.name.getText(ast) === 'channelSettingsPatch') {
    patchExpression = node.initializer.getText(ast);
  }
  ts.forEachChild(node, visit);
}
visit(ast);
assert.ok(patchExpression, 'test must execute the actual shared create/edit settings patch');
const patch = new Function('derivedChannelType', 'values', `
  const passThroughUserAgent = null, passThroughBody = null, retryableStatusCodes = [];
  const showRegularAPIKeyFields = false;
  return (${patchExpression});
`);
const mergeSource = readFileSync(new URL('../utils/merge.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(mergeSource, { compilerOptions: { module: ts.ModuleKind.ESNext } });
const { mergeChannelSettingsForUpdate } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`);

test('Codex create/edit/copy preserve image model settings and apply the blank default', () => {
  for (const codexImageMainModel of [undefined, null, '', '  ']) {
    const settings = mergeChannelSettingsForUpdate(undefined, patch('codex', { settings: { codexImageMainModel } }));
    assert.equal(settings.codexImageMainModel, 'gpt-6-astra');
  }
  const existing = { codexImageMainModel: 'gpt-custom', extraModelPrefix: 'prefix-', proxy: { type: 'environment' } };
  const copied = mergeChannelSettingsForUpdate(existing, patch('codex', { settings: existing }));
  assert.equal(copied.codexImageMainModel, 'gpt-custom');
  const edited = mergeChannelSettingsForUpdate(existing, patch('codex', { settings: { codexImageMainModel: ' new-model ' } }));
  assert.equal(edited.codexImageMainModel, 'new-model');
  assert.equal(edited.extraModelPrefix, existing.extraModelPrefix);
  assert.deepEqual(edited.proxy, existing.proxy);
  assert.equal(mergeChannelSettingsForUpdate(existing, {}).codexImageMainModel, 'gpt-custom');
  assert.equal(Object.hasOwn(patch('openai', {}), 'codexImageMainModel'), false);
  assert.match(source, /name='settings.codexImageMainModel'/);
  assert.match(source, /value=\{field.value \?\? 'gpt-6-astra'\}/);
  assert.equal(source.split('...channelSettingsPatch').length - 1, 2);
});
