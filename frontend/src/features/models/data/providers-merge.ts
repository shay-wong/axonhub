import type { ProviderModel, ProvidersData } from './providers.schema';

function mergeOptionalObjects<T extends object>(fallback: T | undefined, preferred: T | undefined): T | undefined {
  if (!fallback && !preferred) return undefined;
  return { ...(fallback ?? {}), ...(preferred ?? {}) } as T;
}

function mergeProviderModel(fallback: ProviderModel, preferred: ProviderModel): ProviderModel {
  return {
    ...fallback,
    ...preferred,
    reasoning: mergeOptionalObjects(fallback.reasoning, preferred.reasoning),
    modalities: mergeOptionalObjects(fallback.modalities, preferred.modalities),
    limit: mergeOptionalObjects(fallback.limit ?? undefined, preferred.limit ?? undefined),
    cost: mergeOptionalObjects(fallback.cost, preferred.cost),
  };
}

export function mergeProviderModels(
  remoteData: ProvidersData,
  localData: ProvidersData,
  providerID: string,
  localModelIDs: ReadonlySet<string>
): ProvidersData {
  const localProvider = localData.providers[providerID];
  if (!localProvider) return remoteData;

  const localModels = (localProvider.models ?? []).filter((model) => localModelIDs.has(model.id));
  if (localModels.length === 0) return remoteData;

  const remoteProvider = remoteData.providers[providerID];
  if (!remoteProvider) {
    return {
      ...remoteData,
      providers: {
        ...remoteData.providers,
        [providerID]: localProvider,
      },
    };
  }

  const localModelsByID = new Map(localModels.map((model) => [model.id, model]));
  const seenLocalModelIDs = new Set<string>();
  const remoteModels = (remoteProvider.models ?? []).flatMap((remoteModel) => {
    const localModel = localModelsByID.get(remoteModel.id);
    if (!localModel) return [remoteModel];
    if (seenLocalModelIDs.has(remoteModel.id)) return [];

    seenLocalModelIDs.add(remoteModel.id);
    return [mergeProviderModel(localModel, remoteModel)];
  });
  const missingLocalModels = localModels.filter((model) => !seenLocalModelIDs.has(model.id));

  return {
    ...remoteData,
    providers: {
      ...remoteData.providers,
      [providerID]: {
        ...localProvider,
        ...remoteProvider,
        models: [...missingLocalModels, ...remoteModels],
      },
    },
  };
}
