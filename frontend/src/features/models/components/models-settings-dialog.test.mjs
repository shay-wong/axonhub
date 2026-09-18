import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./models-settings-dialog.tsx', import.meta.url), 'utf8');
const ast = ts.createSourceFile('dialog.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
let optionsCallback;
function visit(node) {
  if (ts.isVariableDeclaration(node) && node.name.getText(ast) === 'imageMainModelOptions') {
    optionsCallback = node.initializer.arguments[0].getText(ast);
  }
  ts.forEachChild(node, visit);
}
visit(ast);
assert.ok(optionsCallback);
const getOptions = new Function('channels', 'codexImageMainModel', `return (${optionsCallback})();`);

test('global image model choices use upstream IDs and retain default and saved selections', () => {
  const channel = (type, status, entries) => ({ node: { type, status, allModelEntries: entries } });
  const channels = { edges: [
    channel('codex', 'enabled', [
      { requestModel: 'my-alias', actualModel: 'gpt-upstream' },
      { requestModel: 'gpt-upstream', actualModel: 'gpt-upstream' },
      { requestModel: 'gpt-6-astra', actualModel: 'gpt-6-astra' },
    ]),
    channel('codex', 'disabled', [{ actualModel: 'disabled-model' }]),
    channel('openai', 'enabled', [{ actualModel: 'other-provider-model' }]),
  ] };
  assert.deepEqual(getOptions(channels, 'saved-model').map(({ value }) => value), ['gpt-6-astra', 'saved-model', 'gpt-upstream']);
  assert.deepEqual(getOptions(undefined, 'gpt-6-astra'), [{ value: 'gpt-6-astra', label: 'gpt-6-astra' }]);
  assert.match(source, /<AutoCompleteSelect\s/);
  assert.match(source, /portalContainer=\{dialogContentRef.current\}/);
});

test('global model settings read and both write paths carry the image model', () => {
  const system = readFileSync(new URL('../../system/data/system.ts', import.meta.url), 'utf8');
  const associations = readFileSync(new URL('./models-association-dialog.tsx', import.meta.url), 'utf8');
  assert.match(system, /systemModelSettings\s*\{[^}]*\bcodexImageMainModel\b/s);
  assert.match(source, /setCodexImageMainModel\(settings.codexImageMainModel \|\| 'gpt-6-astra'\)/);
  assert.match(source, /const input: UpdateModelSettingsInput = \{[^}]*\bcodexImageMainModel,/s);
  assert.match(associations, /codexImageMainModel: settings!.codexImageMainModel/);
});
