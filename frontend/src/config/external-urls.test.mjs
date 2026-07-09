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

test('defaults repository-owned URLs to the beta fork', () => {
  const urls = resolveExternalURLs({});

  assert.equal(urls.repositoryURL, 'https://github.com/shay-wong/axonhub');
  assert.equal(urls.releasesURL, 'https://github.com/shay-wong/axonhub/releases');
  assert.equal(
    urls.developerCatalogURL,
    'https://raw.githubusercontent.com/shay-wong/axonhub/beta/frontend/src/features/models/data/providers.json'
  );
});

test('derives repository URLs from the build repository and ref', () => {
  const urls = resolveExternalURLs({
    VITE_GITHUB_REPOSITORY: 'https://github.com/example/axonhub-fork.git',
    VITE_GITHUB_REF: 'feature/model catalog',
  });

  assert.equal(urls.repositoryURL, 'https://github.com/example/axonhub-fork');
  assert.equal(
    urls.developerCatalogURL,
    'https://raw.githubusercontent.com/example/axonhub-fork/feature/model%20catalog/frontend/src/features/models/data/providers.json'
  );
});

test('allows secure and loopback catalog overrides and ignores unsafe URLs', () => {
  const urls = resolveExternalURLs({
    VITE_PROVIDER_CATALOG_URL: 'https://catalog.example/providers.json',
    VITE_DEVELOPER_CATALOG_URL: 'http://localhost:4173/developers.json',
  });
  const unsafeURLs = resolveExternalURLs({
    VITE_PROVIDER_CATALOG_URL: 'http://catalog.example/providers.json',
    VITE_DEVELOPER_CATALOG_URL: 'file:///tmp/providers.json',
  });

  assert.equal(urls.providerCatalogURL, 'https://catalog.example/providers.json');
  assert.equal(urls.developerCatalogURL, 'http://localhost:4173/developers.json');
  assert.match(unsafeURLs.providerCatalogURL, /^https:\/\/raw\.githubusercontent\.com\/ThinkInAIXYZ\//);
  assert.match(unsafeURLs.developerCatalogURL, /^https:\/\/raw\.githubusercontent\.com\/shay-wong\/axonhub\/beta\//);
});

test('release builds use the artifact repository and exact release tag', () => {
  assert.ok(dockerPublishWorkflow.includes('AXONHUB_UPDATE_REPOSITORY=${{ vars.UPDATE_REPOSITORY || github.repository }}'));
  assert.ok(dockerPublishWorkflow.includes('VITE_GITHUB_REPOSITORY=${{ github.repository }}'));
  assert.ok(dockerPublishWorkflow.includes('VITE_GITHUB_REF=${{ steps.version.outputs.tag }}'));
  assert.ok(dockerPublishWorkflow.includes('DEFAULT_DOCKERHUB_NAMESPACE: ${{ github.repository_owner }}'));
  assert.equal(dockerPublishWorkflow.includes('looplj/axonhub'), false);
  assert.ok(releaseWorkflow.includes('AXONHUB_RELEASE_REPOSITORY: ${{ github.repository }}'));
  assert.ok(releaseWorkflow.includes('AXONHUB_UPDATE_REPOSITORY: ${{ vars.UPDATE_REPOSITORY || github.repository }}'));
});
