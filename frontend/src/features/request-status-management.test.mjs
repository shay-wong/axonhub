import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const files = {
  threadData: await readFile(new URL('./threads/data/threads.ts', import.meta.url), 'utf8'),
  traceData: await readFile(new URL('./traces/data/traces.ts', import.meta.url), 'utf8'),
  threadColumns: await readFile(new URL('./threads/components/threads-columns.tsx', import.meta.url), 'utf8'),
  traceColumns: await readFile(new URL('./traces/components/traces-columns.tsx', import.meta.url), 'utf8'),
};

function count(source, value) {
  return source.split(value).length - 1;
}

test('status mutations include the selected project header', () => {
  for (const source of [files.threadData, files.traceData]) {
    const statusHooks = source.slice(source.indexOf('// Status mutation hooks'));
    assert.equal(count(statusHooks, 'const selectedProjectId = useSelectedProjectId();'), 4);
    assert.equal(count(statusHooks, "const headers = selectedProjectId ? { 'X-Project-ID': selectedProjectId } : undefined;"), 4);
    assert.equal(count(statusHooks, '{ id },\n        headers'), 4);
  }
});

test('status action columns require write_requests', () => {
  for (const source of [files.threadColumns, files.traceColumns]) {
    assert.match(source, /const canWrite = hasScope\('write_requests'\);/);
    assert.match(source, /\.\.\.\(canWrite\s*\?\s*\[/);
  }
});
