import type { AuthUser } from '@/stores/authStore';
import {
  getProjectEffectiveScopes,
  hasRouteAccess,
  hasScopeRequirements,
  PLAYGROUND_SCOPE_REQUIREMENTS,
  routeConfigs,
} from '../../../config/route-permission.ts';

export function getAuthenticatedLanding(
  user: AuthUser,
  selectedProjectID?: string | null,
  navigation?: { hiddenItems: string[]; candidates: string[] }
): { path: string; projectID: string | null } {
  const systemScopes = user.scopes ?? [];
  const projects = user.projects ?? [];

  const selectedProject = projects.find((project) => project.projectID === selectedProjectID);
  const playgroundProject =
    selectedProject && hasScopeRequirements(systemScopes, getProjectEffectiveScopes(selectedProject), PLAYGROUND_SCOPE_REQUIREMENTS)
      ? selectedProject
      : projects.find((project) => hasScopeRequirements(systemScopes, getProjectEffectiveScopes(project), PLAYGROUND_SCOPE_REQUIREMENTS));

  const landing = user.isOwner
    ? { path: '/', projectID: null }
    : playgroundProject
      ? { path: '/project/playground', projectID: playgroundProject.projectID }
      : { path: '/settings/profile', projectID: null };

  if (!navigation?.hiddenItems.includes(landing.path)) return landing;

  const project = playgroundProject ?? selectedProject ?? projects[0];
  const projectScopes = getProjectEffectiveScopes(project);
  const path = navigation.candidates.find((candidate) => {
    if (navigation.hiddenItems.includes(candidate)) return false;
    if (candidate.startsWith('/project/') && !project) return false;
    if (user.isOwner) return true;

    const group = routeConfigs.find((group) => group.routes.some((route) => route.path === candidate));
    const route = group?.routes.find((route) => route.path === candidate);
    if (!route || (route.requireProjectOwner && !project?.isOwner)) return false;
    if (route.scopeRequirements) return hasScopeRequirements(systemScopes, projectScopes, route.scopeRequirements);

    const level = route.scopeLevel ?? group?.scopeLevel ?? 'any';
    if (project?.isOwner && level !== 'system') return true;
    return hasRouteAccess(
      level === 'system' ? systemScopes : level === 'project' ? projectScopes : [...systemScopes, ...projectScopes],
      route
    );
  });

  return path ? { path, projectID: path.startsWith('/project/') ? project!.projectID : null } : landing;
}
