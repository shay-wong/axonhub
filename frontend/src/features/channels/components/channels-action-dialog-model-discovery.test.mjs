import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./channels-action-dialog.tsx', import.meta.url), 'utf8');
const ast = ts.createSourceFile('dialog.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
let callback;
function visit(node) {
  if (ts.isVariableDeclaration(node) && node.name.getText(ast) === 'handleFetchModels') {
    callback = node.initializer.arguments[0].getText(ast);
  }
  ts.forEachChild(node, visit);
}
visit(ast);
assert.ok(callback);
const { outputText } = ts.transpileModule(`export function createHandler(deps) {
  const { form, disabledKeySet, fetchModels, availableModelFetchAPIKeys, modelFetchAPIKey,
    isEdit, currentRow, authMode, getFetchModelsApiKey, toast } = deps;
  return ${callback};
}`, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 } });
const { createHandler } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`);

test('Vertex discovery skips disabled keys, keeps each project binding and stops at first success', async () => {
  const calls = [];
  const errors = [];
  const rows = ['disabled', 'bad', 'good', 'unused'].map((key) => ({ apiKey: key, projectID: `project-${key}`, region: 'global' }));
  const values = { type: 'gemini_vertex', baseURL: 'https://vertex.example/v1', 'credentials.vertexApiKeys': rows };
  const handler = createHandler({
    form: { getValues: (name) => values[name] },
    disabledKeySet: new Set(['disabled']),
    fetchModels: { mutateAsync: async (input) => {
      calls.push(input);
      return { models: [], error: input.apiKey === 'good' ? undefined : 'rejected' };
    } },
    availableModelFetchAPIKeys: [], modelFetchAPIKey: '',
    isEdit: true, currentRow: { id: 'channel-1' }, authMode: 'third-party',
    getFetchModelsApiKey: () => '', toast: { error: (error) => errors.push(error) },
  });
  await handler();
  assert.deepEqual(errors, []);
  assert.deepEqual(calls.map(({ apiKey, projectID, region }) => ({ apiKey, projectID, region })), [
    { apiKey: 'bad', projectID: 'project-bad', region: 'global' },
    { apiKey: 'good', projectID: 'project-good', region: 'global' },
  ]);
});
