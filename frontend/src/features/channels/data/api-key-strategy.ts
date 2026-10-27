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
  apiKeySelectionStrategy?: string | null;
} | null): APIKeyStrategyValue {
  // 仅新字段未设置时回读旧配置，避免旧值覆盖用户已选的新策略。
  const strategy = settings?.apiKeyStrategy || settings?.apiKeySelectionStrategy;
  return API_KEY_STRATEGIES.includes(strategy as APIKeyStrategyValue) ? (strategy as APIKeyStrategyValue) : 'sticky';
}
