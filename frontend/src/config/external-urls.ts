const DEFAULT_GITHUB_REPOSITORY = 'shay-wong/axonhub';
const DEFAULT_GITHUB_REF = 'beta';
const DEFAULT_PROVIDER_CATALOG_URL = 'https://raw.githubusercontent.com/ThinkInAIXYZ/PublicProviderConf/refs/heads/dev/dist/all.json';
const DEVELOPER_CATALOG_PATH = 'frontend/src/features/models/data/providers.json';

export interface ExternalURLEnvironment {
  VITE_GITHUB_REPOSITORY?: string;
  VITE_GITHUB_REF?: string;
  VITE_PROVIDER_CATALOG_URL?: string;
  VITE_DEVELOPER_CATALOG_URL?: string;
}

export interface ExternalURLs {
  repositoryURL: string;
  releasesURL: string;
  issuesURL: string;
  providerCatalogURL: string;
  developerCatalogURL: string;
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

function normalizeCatalogURL(value: string | undefined): string | undefined {
  const normalized = value?.trim();
  if (!normalized) return undefined;

  try {
    const url = new URL(normalized);
    const isLoopbackHTTP = url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname.toLowerCase());
    return url.protocol === 'https:' || isLoopbackHTTP ? url.toString() : undefined;
  } catch {
    return undefined;
  }
}

function encodeGitHubRef(value: string): string {
  return value
    .split('/')
    .filter(Boolean)
    .map((segment) => encodeURIComponent(segment))
    .join('/');
}

export function resolveExternalURLs(env: ExternalURLEnvironment): ExternalURLs {
  const repository = normalizeGitHubRepository(env.VITE_GITHUB_REPOSITORY) ?? DEFAULT_GITHUB_REPOSITORY;
  const repositoryRef = env.VITE_GITHUB_REF?.trim() || DEFAULT_GITHUB_REF;
  const repositoryURL = `https://github.com/${repository}`;
  const developerCatalogURL =
    normalizeCatalogURL(env.VITE_DEVELOPER_CATALOG_URL) ??
    `https://raw.githubusercontent.com/${repository}/${encodeGitHubRef(repositoryRef)}/${DEVELOPER_CATALOG_PATH}`;

  return {
    repositoryURL,
    releasesURL: `${repositoryURL}/releases`,
    issuesURL: `${repositoryURL}/issues`,
    providerCatalogURL: normalizeCatalogURL(env.VITE_PROVIDER_CATALOG_URL) ?? DEFAULT_PROVIDER_CATALOG_URL,
    developerCatalogURL,
  };
}
