interface RequestServiceTierSource {
  executions?: {
    edges?: Array<{
      node?: {
        requestedServiceTier?: string | null;
        speedMode?: string | null;
      } | null;
    }>;
  } | null;
}

export function getSpeedMode(request: RequestServiceTierSource): string {
  const execution = request.executions?.edges?.[0]?.node;
  const speedMode = execution?.speedMode?.trim().toLowerCase();
  if (speedMode === 'fast') return 'fast';

  const requestedServiceTier = execution?.requestedServiceTier?.trim().toLowerCase();
  return requestedServiceTier === 'priority' ? 'fast' : '';
}
