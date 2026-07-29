import type { AuthUser } from '@/stores/authStore';
import { getProjectEffectiveScopes, hasScopeRequirements, PLAYGROUND_SCOPE_REQUIREMENTS } from '../../../config/route-permission.ts';

type LandingPath = '/' | '/project/playground' | '/settings/profile';

export function getAuthenticatedLanding(
  user: AuthUser,
  selectedProjectID?: string | null
): { path: LandingPath; projectID: string | null } {
  if (user.isOwner) {
    return { path: '/', projectID: null };
  }

  const systemScopes = user.scopes ?? [];
  const projects = user.projects ?? [];

  const selectedProject = projects.find((project) => project.projectID === selectedProjectID);
  const playgroundProject =
    selectedProject && hasScopeRequirements(systemScopes, getProjectEffectiveScopes(selectedProject), PLAYGROUND_SCOPE_REQUIREMENTS)
      ? selectedProject
      : projects.find((project) => hasScopeRequirements(systemScopes, getProjectEffectiveScopes(project), PLAYGROUND_SCOPE_REQUIREMENTS));

  return playgroundProject
    ? { path: '/project/playground', projectID: playgroundProject.projectID }
    : { path: '/settings/profile', projectID: null };
}
