import assert from 'node:assert/strict';
import test from 'node:test';

const { promptSchema } = await import('./schema.ts');

test('accepts the GraphQL project GUID returned for prompts', () => {
  const prompt = promptSchema.parse({
    id: 'gid://axonhub/Prompt/7',
    createdAt: '2026-07-21T00:00:00Z',
    updatedAt: '2026-07-21T00:00:00Z',
    projectID: 'gid://axonhub/Project/42',
    name: 'Example',
    description: '',
    role: 'system',
    content: 'Test',
    status: 'enabled',
    order: 0,
    settings: { action: { type: 'prepend' }, conditions: [] },
  });

  assert.equal(prompt.projectID, 'gid://axonhub/Project/42');
});
