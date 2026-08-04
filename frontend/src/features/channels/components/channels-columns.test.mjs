import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./channels-columns.tsx', import.meta.url), 'utf8');
const file = ts.createSourceFile('channels-columns.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

test('imports the permissions hook used by channel cells', () => {
  const importsUsePermissions = file.statements.some(
    (statement) =>
      ts.isImportDeclaration(statement) &&
      statement.moduleSpecifier.text === '@/hooks/usePermissions' &&
      statement.importClause?.namedBindings?.elements.some((element) => element.name.text === 'usePermissions'),
  );

  assert.equal(importsUsePermissions, true);
});
