const DEFAULT_GITHUB_REPOSITORY = 'shay-wong/axonhub';

export interface ExternalURLEnvironment {
  VITE_GITHUB_REPOSITORY?: string;
}

export interface ExternalURLs {
  repositoryURL: string;
  releasesURL: string;
  issuesURL: string;
}

function normalizeGitHubRepository(value: string | undefined): string | undefined {
  const normalized = value
    ?.trim()
    .replace(/^https:\/\/github\.com\//, '')
    .replace(/^git@github\.com:/, '')
    .replace(/\.git$/, '')
    .replace(/^\/+|\/+$/g, '');

  if (!normalized || !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(normalized)) return undefined;
  return normalized;
}

export function resolveExternalURLs(env: ExternalURLEnvironment): ExternalURLs {
  const repository = normalizeGitHubRepository(env.VITE_GITHUB_REPOSITORY) ?? DEFAULT_GITHUB_REPOSITORY;
  const repositoryURL = `https://github.com/${repository}`;

  return {
    repositoryURL,
    releasesURL: `${repositoryURL}/releases`,
    issuesURL: `${repositoryURL}/issues`,
  };
}
