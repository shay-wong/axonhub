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
const unstableDockerWorkflow = await readFile(new URL('../../../.github/workflows/docker-unstable.yml', import.meta.url), 'utf8');
const sharedDockerWorkflow = await readFile(new URL('../../../.github/workflows/docker-build.yml', import.meta.url), 'utf8');
const releaseWorkflow = await readFile(new URL('../../../.github/workflows/release.yml', import.meta.url), 'utf8');
const desktopWorkflow = await readFile(new URL('../../../.github/workflows/desktop-release.yml', import.meta.url), 'utf8');
const desktopConfig = await readFile(new URL('../../../cmd/axonhub-desktop/mygo.config.ts', import.meta.url), 'utf8');

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
  assert.ok(dockerPublishWorkflow.includes('uses: ./.github/workflows/docker-build.yml'));
  assert.ok(dockerPublishWorkflow.includes('mode: release'));
  assert.ok(dockerPublishWorkflow.includes('ref: ${{ inputs.tag || github.ref }}'));
  assert.ok(dockerPublishWorkflow.includes('tag: ${{ inputs.tag || github.ref_name }}'));
  assert.ok(sharedDockerWorkflow.includes('AXONHUB_UPDATE_REPOSITORY=${{ vars.UPDATE_REPOSITORY || github.repository }}'));
  assert.ok(sharedDockerWorkflow.includes('VITE_GITHUB_REPOSITORY=${{ github.repository }}'));
  assert.ok(dockerPublishWorkflow.includes('DEFAULT_DOCKERHUB_NAMESPACE: ${{ github.repository_owner }}'));
  assert.equal(dockerPublishWorkflow.includes('looplj/axonhub'), false);
  assert.equal(sharedDockerWorkflow.includes('looplj/axonhub'), false);
  assert.ok(releaseWorkflow.includes('AXONHUB_RELEASE_REPOSITORY: ${{ github.repository }}'));
  assert.ok(releaseWorkflow.includes('AXONHUB_UPDATE_REPOSITORY: ${{ vars.UPDATE_REPOSITORY || github.repository }}'));
});

test('unstable Docker builds use the fork beta branch and artifact owner', () => {
  assert.ok(unstableDockerWorkflow.includes('ref: beta'));
  assert.ok(unstableDockerWorkflow.includes('uses: ./.github/workflows/docker-build.yml'));
  assert.ok(unstableDockerWorkflow.includes('mode: unstable'));
  assert.ok(unstableDockerWorkflow.includes('dockerhub_image: ${{ needs.prepare.outputs.dockerhub_image }}'));
  assert.ok(sharedDockerWorkflow.includes('AXONHUB_UPDATE_REPOSITORY=${{ vars.UPDATE_REPOSITORY || github.repository }}'));
  assert.ok(sharedDockerWorkflow.includes("AXONHUB_UPDATE_CHANNEL=${{ steps.version.outputs.channel || 'beta' }}"));
  assert.ok(sharedDockerWorkflow.includes('${IMAGE}:unstable-${ARCH}'));
  assert.equal(unstableDockerWorkflow.includes('ref: unstable'), false);
  assert.equal(unstableDockerWorkflow.includes('looplj/axonhub'), false);
});

test('desktop releases receive the fork tag and require a configured signing key', () => {
  const desktopJob = releaseWorkflow.slice(releaseWorkflow.indexOf('\n  desktop:'));
  assert.ok(desktopJob.includes("if: ${{ vars.MYGO_UPDATER_PUBLIC_KEY != '' }}"));
  assert.ok(desktopJob.includes('tag: ${{ inputs.tag || github.ref_name }}'));
  assert.ok(desktopWorkflow.includes('AXONHUB_RELEASE_REPOSITORY: ${{ github.repository }}'));
  assert.ok(desktopWorkflow.includes('VITE_GITHUB_REPOSITORY: ${{ github.repository }}'));
  assert.ok(desktopWorkflow.includes('MYGO_UPDATER_PUBLIC_KEY: ${{ vars.MYGO_UPDATER_PUBLIC_KEY }}'));
  assert.ok(desktopWorkflow.includes('MYGO_UPDATER_PRIVATE_KEY: ${{ secrets.MYGO_UPDATER_PRIVATE_KEY }}'));
});

test('desktop config uses fork updates only with an explicit public key', () => {
  const { outputText } = ts.transpileModule(desktopConfig, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
  const evaluate = (env) => {
    const exports = {};
    new Function('exports', 'require', 'process', outputText)(exports, (name) => {
      assert.equal(name, 'node:fs');
      return { readFileSync: () => 'v1.0.0-rc2-fork.1\n', mkdirSync() {}, writeFileSync() {} };
    }, { env });
    return exports.default;
  };
  assert.equal(evaluate({}).updates, undefined);
  assert.equal(evaluate({ MYGO_UPDATER_PUBLIC_KEY: '  ' }).updates, undefined);
  const config = evaluate({ MYGO_UPDATER_PUBLIC_KEY: ' test-public-key ' });
  assert.equal(config.version, '1.0.0-rc2-fork.1');
  assert.equal(config.updates.github, 'shay-wong/axonhub');
  assert.equal(config.updates.publicKey, 'test-public-key');
  assert.equal(config.updates.tagPrefix, 'v');
  assert.equal(evaluate({ AXONHUB_RELEASE_REPOSITORY: 'example/fork', MYGO_UPDATER_PUBLIC_KEY: 'key' }).updates.github, 'example/fork');
});
