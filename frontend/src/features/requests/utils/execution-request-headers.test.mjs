import assert from 'node:assert/strict';
import test from 'node:test';

const { formatExecutionAPIKeyIdentity, formatExecutionRequestHeaders } = await import('./execution-request-headers.ts');

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
