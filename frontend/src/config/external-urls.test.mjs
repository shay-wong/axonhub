import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('./external-urls.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2022,
  },
});
const moduleURL = `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`;
const { resolveExternalURLs } = await import(moduleURL);
const dockerPublishWorkflow = await readFile(new URL('../../../.github/workflows/docker-publish.yml', import.meta.url), 'utf8');
const releaseWorkflow = await readFile(new URL('../../../.github/workflows/release.yml', import.meta.url), 'utf8');

test('defaults repository-owned URLs to the fork', () => {
  const urls = resolveExternalURLs({});

  assert.equal(urls.repositoryURL, 'https://github.com/shay-wong/axonhub');
  assert.equal(urls.releasesURL, 'https://github.com/shay-wong/axonhub/releases');
});

test('derives repository URLs from the build repository', () => {
  const urls = resolveExternalURLs({
    VITE_GITHUB_REPOSITORY: 'https://github.com/example/axonhub-fork.git',
  });

  assert.equal(urls.repositoryURL, 'https://github.com/example/axonhub-fork');
});

test('release builds use the artifact repository and exact release tag', () => {
  assert.ok(dockerPublishWorkflow.includes('AXONHUB_UPDATE_REPOSITORY=${{ vars.UPDATE_REPOSITORY || github.repository }}'));
  assert.ok(dockerPublishWorkflow.includes('VITE_GITHUB_REPOSITORY=${{ github.repository }}'));
  assert.ok(dockerPublishWorkflow.includes('DEFAULT_DOCKERHUB_NAMESPACE: ${{ github.repository_owner }}'));
  assert.equal(dockerPublishWorkflow.includes('looplj/axonhub'), false);
  assert.ok(releaseWorkflow.includes('AXONHUB_RELEASE_REPOSITORY: ${{ github.repository }}'));
  assert.ok(releaseWorkflow.includes('AXONHUB_UPDATE_REPOSITORY: ${{ vars.UPDATE_REPOSITORY || github.repository }}'));
});
