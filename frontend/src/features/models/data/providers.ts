import { useQuery } from '@tanstack/react-query';
import { resolveExternalURLs } from '@/config/external-urls';
import providersDataRaw from './providers.json';
import { providersDataSchema, type ProvidersData } from './providers.schema';

const localProvidersData = providersDataSchema.parse(providersDataRaw);
const { providerCatalogURL, developerCatalogURL } = resolveExternalURLs(import.meta.env);

async function fetchProvidersData(url: string, catalogName: string): Promise<ProvidersData> {
  try {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error(`Failed to fetch ${catalogName} data`);
    }
    const data = await response.json();
    return providersDataSchema.parse(data);
  } catch (error) {
    console.error(`Failed to fetch remote ${catalogName} data, falling back to local:`, error);
    return localProvidersData;
  }
}

function useCatalogData(queryKey: string, url: string, catalogName: string) {
  return useQuery<ProvidersData>({
    queryKey: [queryKey, url],
    queryFn: () => fetchProvidersData(url, catalogName),
    staleTime: 1000 * 60 * 60 * 24, // 1 day
    placeholderData: localProvidersData,
  });
}

export function useProvidersData() {
  return useCatalogData('providers-data', providerCatalogURL, 'providers');
}

export function useDevelopersData() {
  return useCatalogData('developers-data', developerCatalogURL, 'developers');
}
