import assert from 'node:assert/strict';
import test from 'node:test';
import { getProjectEffectiveScopes, hasScopeRequirements, PLAYGROUND_SCOPE_REQUIREMENTS } from '../../../config/route-permission.ts';
import { getAuthenticatedLanding } from './auth-redirect.ts';

function user({ isOwner = false, scopes = [], projects = [], projectScopes = [] } = {}) {
  return {
    isOwner,
    scopes,
    projects: projects.length > 0 ? projects : projectScopes.map((scopes, index) => ({ projectID: `project-${index + 1}`, scopes })),
  };
}

test('routes owners to the dashboard', () => {
  assert.equal(getAuthenticatedLanding(user({ isOwner: true })).path, '/');
});

test('routes users without both playground capabilities to settings', () => {
  assert.equal(getAuthenticatedLanding(user()).path, '/settings/profile');
  assert.equal(getAuthenticatedLanding(user({ scopes: ['read_channels'] })).path, '/settings/profile');
  assert.equal(getAuthenticatedLanding(user({ projectScopes: [['write_requests']] })).path, '/settings/profile');
});

test('routes users with channel read and request write capabilities to playground', () => {
  assert.equal(
    getAuthenticatedLanding(user({ scopes: ['read_channels'], projectScopes: [['write_requests']] })).path,
    '/project/playground'
  );
  assert.equal(getAuthenticatedLanding(user({ scopes: ['*'], projectScopes: [[]] })).path, '/project/playground');
});

test('uses effective project role scopes for the playground', () => {
  const authUser = user({
    scopes: ['read_channels'],
    projects: [{ projectID: 'project-1', scopes: [], effectiveScopes: ['write_requests'] }],
  });

  assert.equal(getAuthenticatedLanding(authUser).path, '/project/playground');
});

test('treats project owners as having all project scopes', () => {
  const authUser = user({
    scopes: ['read_channels'],
    projects: [{ projectID: 'project-1', isOwner: true, scopes: [] }],
  });

  assert.equal(getAuthenticatedLanding(authUser).path, '/project/playground');
  assert.deepEqual(getProjectEffectiveScopes(authUser.projects[0]), ['*']);
});

test('does not apply effective scopes across projects', () => {
  const authUser = user({
    scopes: ['read_channels'],
    projects: [
      { projectID: 'project-1', scopes: [], effectiveScopes: ['write_requests'] },
      { projectID: 'project-2', scopes: [], effectiveScopes: [] },
    ],
  });

  assert.deepEqual(getAuthenticatedLanding(authUser, 'project-2'), {
    path: '/project/playground',
    projectID: 'project-1',
  });
});

test('requires an assigned project even with system-level playground access', () => {
  assert.deepEqual(getAuthenticatedLanding(user({ scopes: ['*'] })), {
    path: '/settings/profile',
    projectID: null,
  });
});

test('replaces a stale selected project for system-level users', () => {
  assert.deepEqual(getAuthenticatedLanding(user({ scopes: ['*'], projectScopes: [[]] }), 'stale-project'), {
    path: '/project/playground',
    projectID: 'project-1',
  });
});

test('selects a project that satisfies the playground requirements', () => {
  const authUser = user({ scopes: ['read_channels'], projectScopes: [[], ['write_requests']] });

  assert.deepEqual(getAuthenticatedLanding(authUser, 'project-1'), {
    path: '/project/playground',
    projectID: 'project-2',
  });
  assert.deepEqual(getAuthenticatedLanding(authUser, 'project-2'), {
    path: '/project/playground',
    projectID: 'project-2',
  });
});

test('requires channel read at system level', () => {
  assert.equal(hasScopeRequirements([], ['read_channels', 'write_requests'], PLAYGROUND_SCOPE_REQUIREMENTS), false);
  assert.equal(hasScopeRequirements([], ['*'], PLAYGROUND_SCOPE_REQUIREMENTS), false);
  assert.equal(hasScopeRequirements(['read_channels'], ['write_requests'], PLAYGROUND_SCOPE_REQUIREMENTS), true);
});

test('handles old stored users without scope arrays', () => {
  assert.equal(getAuthenticatedLanding({ isOwner: false }).path, '/settings/profile');
});

test('hidden landing fallback skips routes without permission in the selected project', () => {
  const authUser = user({
    scopes: ['read_channels'],
    projectScopes: [['write_requests', 'read_requests'], ['read_api_keys']],
  });
  assert.deepEqual(getAuthenticatedLanding(authUser, 'project-1', {
    hiddenItems: ['/project/playground'],
    candidates: ['/project/api-keys', '/project/usage-stats', '/project/requests'],
  }), { path: '/project/requests', projectID: 'project-1' });
});

test('hidden landing fallback keeps a safe landing when no visible route is allowed', () => {
  const authUser = user({ scopes: ['read_channels'], projectScopes: [['write_requests']] });
  assert.deepEqual(getAuthenticatedLanding(authUser, null, {
    hiddenItems: ['/project/playground'],
    candidates: ['/project/api-keys'],
  }), { path: '/project/playground', projectID: 'project-1' });
  assert.deepEqual(getAuthenticatedLanding(user(), null, {
    hiddenItems: ['/project/playground'],
    candidates: ['/project/api-keys'],
  }), { path: '/settings/profile', projectID: null });
});

test('owner hidden landing respects project membership and menu visibility', () => {
  assert.deepEqual(getAuthenticatedLanding(user({ isOwner: true }), null, {
    hiddenItems: ['/'],
    candidates: ['/project/api-keys', '/channels'],
  }), { path: '/channels', projectID: null });
  assert.deepEqual(getAuthenticatedLanding(user({ isOwner: true, projectScopes: [[]] }), 'project-1', {
    hiddenItems: ['/'],
    candidates: ['/project/api-keys'],
  }), { path: '/project/api-keys', projectID: 'project-1' });
});

test('return destination permissions use the selected project and preserve details, query and hash', () => {
  const authUser = user({ projects: [
    { projectID: 'project-1', effectiveScopes: ['read_requests'] },
    { projectID: 'project-2', effectiveScopes: ['read_api_keys'] },
  ] });
  const navigation = { hiddenItems: [], candidates: [] };
  const returnPath = '/project/requests/request-id?status=error#details';
  assert.deepEqual(getAuthenticatedLanding(authUser, 'project-1', { ...navigation, returnPath }), {
    path: returnPath, projectID: 'project-1',
  });
  assert.deepEqual(getAuthenticatedLanding(authUser, 'project-2', { ...navigation, returnPath }), {
    path: '/settings/profile', projectID: null,
  });
  assert.deepEqual(getAuthenticatedLanding(authUser, 'stale-project', { ...navigation, returnPath }), {
    path: returnPath, projectID: 'project-1',
  });
  assert.equal(getAuthenticatedLanding(user({ isOwner: true }), null, { ...navigation, returnPath }).path, '/');
  assert.equal(getAuthenticatedLanding(authUser, 'project-1', { ...navigation, returnPath: '/system' }).path, '/settings/profile');
  assert.equal(getAuthenticatedLanding(authUser, 'project-1', { ...navigation, returnPath: '/unknown' }).path, '/settings/profile');
});
