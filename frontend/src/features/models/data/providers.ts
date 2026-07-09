import { useQuery } from '@tanstack/react-query';
import { mergeProviderModels } from './providers-merge';
import providersDataRaw from './providers.json';
import { providersDataSchema, type ProvidersData } from './providers.schema';

const PROVIDERS_URL = 'https://raw.githubusercontent.com/ThinkInAIXYZ/PublicProviderConf/refs/heads/dev/dist/all.json';
const DEVELOPERS_URL =
  'https://raw.githubusercontent.com/looplj/axonhub/refs/heads/unstable/frontend/src/features/models/data/providers.json';
const localProvidersData = providersDataSchema.parse(providersDataRaw);
const LOCAL_OPENAI_MODEL_IDS = new Set(['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna']);

function mergeLocalProviderModels(remoteData: ProvidersData): ProvidersData {
  return mergeProviderModels(remoteData, localProvidersData, 'openai', LOCAL_OPENAI_MODEL_IDS);
}

export function useProvidersData() {
  return useQuery<ProvidersData>({
    queryKey: ['providers-data'],
    queryFn: async () => {
      try {
        const response = await fetch(PROVIDERS_URL);
        if (!response.ok) {
          throw new Error('Failed to fetch providers data');
        }
        const data = await response.json();
        return providersDataSchema.parse(data);
      } catch (error) {
        console.error('Failed to fetch remote providers data, falling back to local:', error);
        return localProvidersData;
      }
    },
    select: mergeLocalProviderModels,
    staleTime: 1000 * 60 * 60 * 24, // 1 day
    placeholderData: localProvidersData,
  });
}

export function useDevelopersData() {
  return useQuery<ProvidersData>({
    queryKey: ['developers-data'],
    queryFn: async () => {
      try {
        const response = await fetch(DEVELOPERS_URL);
        if (!response.ok) {
          throw new Error('Failed to fetch developers data');
        }
        const data = await response.json();
        return providersDataSchema.parse(data);
      } catch (error) {
        console.error('Failed to fetch remote developers data, falling back to local:', error);
        return localProvidersData;
      }
    },
    select: mergeLocalProviderModels,
    staleTime: 1000 * 60 * 60 * 24, // 1 day
    placeholderData: localProvidersData,
  });
}
