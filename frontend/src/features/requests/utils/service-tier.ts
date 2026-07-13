interface RequestServiceTierSource {
  executions?: {
    edges?: Array<{
      node?: {
        requestedServiceTier?: string | null;
      } | null;
    }>;
  } | null;
}

export function getRequestedServiceTier(request: RequestServiceTierSource): string {
  return request.executions?.edges?.[0]?.node?.requestedServiceTier?.trim().toLowerCase() || '';
}
