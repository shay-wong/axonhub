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
  navigation?: { hiddenItems: string[]; candidates: string[]; returnPath?: string }
): { path: string; projectID: string | null } {
  const systemScopes = user.scopes ?? [];
  const projects = user.projects ?? [];

  const selectedProject = projects.find((project) => project.projectID === selectedProjectID);
  const canAccessPath = (path: string, project: (typeof projects)[number] | undefined) => {
    if (path.startsWith('/project/') && !project) return false;
    const group = routeConfigs.find((group) => group.routes.some((route) => route.path === path));
    const route = group?.routes.find((route) => route.path === path);
    if (!route) return false;
    if (user.isOwner) return true;
    if (route.requireProjectOwner && !project?.isOwner) return false;
    const projectScopes = getProjectEffectiveScopes(project);
    if (route.scopeRequirements) return hasScopeRequirements(systemScopes, projectScopes, route.scopeRequirements);

    const level = route.scopeLevel ?? group?.scopeLevel ?? 'any';
    if (project?.isOwner && level !== 'system') return true;
    return hasRouteAccess(
      level === 'system' ? systemScopes : level === 'project' ? projectScopes : [...systemScopes, ...projectScopes],
      route
    );
  };

  // The caller has already checked URL safety; apply the same route permissions
  // as the fallback without borrowing scopes from a different project.
  if (navigation?.returnPath) {
    const pathname = navigation.returnPath.split(/[?#]/, 1)[0];
    const route = routeConfigs.flatMap((group) => group.routes)
      .filter((route) => pathname === route.path || (route.path !== '/' && pathname.startsWith(`${route.path}/`)))
      .sort((a, b) => b.path.length - a.path.length)[0];
    const project = selectedProject ?? projects[0];
    if (route && canAccessPath(route.path, project)) {
      return { path: navigation.returnPath, projectID: pathname.startsWith('/project/') ? project!.projectID : null };
    }
  }

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
  const path = navigation.candidates.find((candidate) => {
    if (navigation.hiddenItems.includes(candidate)) return false;
    return canAccessPath(candidate, project);
  });

  return path ? { path, projectID: path.startsWith('/project/') ? project!.projectID : null } : landing;
}
