export const API_KEY_STRATEGIES = [
  'sticky',
  'random',
  'round_robin',
  'round_robin_success',
  'priority',
  'fixed',
  'weighted_sticky',
  'failover',
] as const;

export type APIKeyStrategyValue = (typeof API_KEY_STRATEGIES)[number];

export function recallAPIKeyStrategy(settings?: {
  apiKeyStrategy?: string | null;
} | null): APIKeyStrategyValue {
  const strategy = settings?.apiKeyStrategy;
  return API_KEY_STRATEGIES.includes(strategy as APIKeyStrategyValue) ? (strategy as APIKeyStrategyValue) : 'sticky';
}
