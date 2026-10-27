import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

const { formatExecutionAPIKeyLabel, formatExecutionAPIKeyIdentity, formatExecutionRequestHeaders } = await import('./execution-request-headers.ts');

test('prefers the recorded alias, falling back to key position for legacy executions', () => {
  assert.equal(formatExecutionAPIKeyLabel(' Primary Account ', 2), 'Primary Account');
  assert.equal(formatExecutionAPIKeyLabel('Single Key', null), 'Single Key');
  assert.equal(formatExecutionAPIKeyLabel('  ', 2), 'key2');
  assert.equal(formatExecutionAPIKeyLabel(null, 1), 'key1');
  assert.equal(formatExecutionAPIKeyLabel(undefined, null), '');
});

test('request list queries fetch aliases only with channel write permission', async () => {
  const source = await readFile(new URL('../data/requests.ts', import.meta.url), 'utf8');
  const ast = ts.createSourceFile('requests.ts', source, ts.ScriptTarget.Latest, true);
  const builder = ast.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === 'buildRequestsQuery');
  assert.ok(builder);
  const js = ts.transpileModule(builder.getText(ast), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const build = new Function(`${js}; return buildRequestsQuery;`)();
  for (const canViewChannels of [false, true]) {
    for (const canWriteChannels of [false, true]) {
      const query = build({ canViewChannels, canWriteChannels, canViewApiKeys: false, canViewCallerUser: false });
      assert.equal(query.includes('channelAPIKeyName'), canViewChannels && canWriteChannels);
      assert.ok(!query.includes('credentials'));
    }
  }
});

test('formats the execution API key snapshot', () => {
  assert.equal(formatExecutionAPIKeyIdentity('Primary Account', '1234'), 'Primary Account · ****1234');
  assert.equal(formatExecutionAPIKeyIdentity('', '1234'), '****1234');
  assert.equal(formatExecutionAPIKeyIdentity('Primary Account', ''), 'Primary Account');
});

test('annotates masked upstream authentication headers without mutating the source', () => {
  const headers = {
    Authorization: ['******'],
    'Content-Type': ['application/json'],
  };

  const formatted = formatExecutionRequestHeaders(headers, 'Primary Account', '1234', ['Authorization']);

  assert.deepEqual(formatted, {
    Authorization: ['Primary Account · ****1234'],
    'Content-Type': ['application/json'],
  });
  assert.deepEqual(headers.Authorization, ['******']);
});

test('leaves headers unchanged without an execution key snapshot', () => {
  const headers = { Authorization: ['******'] };
  assert.equal(formatExecutionRequestHeaders(headers), headers);
});

test('only annotates the exact masked headers recorded by the execution', () => {
  const headers = {
    Authorization: ['******'],
    'X-Google-Api-Key': ['******'],
    'X-Api-Token': ['not-masked'],
  };

  const formatted = formatExecutionRequestHeaders(headers, 'Primary Account', '1234', ['X-Google-Api-Key', 'X-Api-Token']);

  assert.deepEqual(formatted, {
    Authorization: ['******'],
    'X-Google-Api-Key': ['Primary Account · ****1234'],
    'X-Api-Token': ['not-masked'],
  });
});
