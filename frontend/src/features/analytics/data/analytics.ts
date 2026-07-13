import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';

// --- Zod Schemas ---

export const analyticsFilterSchema = z.object({
  startTime: z.string().nullable().optional(), // 'YYYY-MM-DD' 或 ISO timestamp
  endTime: z.string().nullable().optional(),
  projectIDs: z.array(z.string()).optional(),
  channelIDs: z.array(z.string()).optional(),
  modelIDs: z.array(z.string()).optional(),
  apiKeyIDs: z.array(z.string()).optional(),
  userIDs: z.array(z.string()).optional(),
});

export type AnalyticsFilter = z.infer<typeof analyticsFilterSchema>;

export const analyticsOverviewSchema = z.object({
  totalTokens: z.number(),
  totalInputTokens: z.number(),
  totalCachedInputTokens: z.number(),
  totalUncachedInputTokens: z.number(),
  totalOutputTokens: z.number(),
  totalRequests: z.number(),
  totalCost: z.number(),
});

export type AnalyticsOverview = z.infer<typeof analyticsOverviewSchema>;

export const analyticsDailyStatSchema = z.object({
  date: z.string(),
  inputTokens: z.number(),
  cachedInputTokens: z.number(),
  uncachedInputTokens: z.number(),
  outputTokens: z.number(),
  totalTokens: z.number(),
  requestCount: z.number(),
  cost: z.number(),
});

export type AnalyticsDailyStat = z.infer<typeof analyticsDailyStatSchema>;

export const analyticsDimensionStatSchema = z.object({
  id: z.string(),
  name: z.string(),
  requestCount: z.number(),
  inputTokens: z.number(),
  cachedInputTokens: z.number(),
  outputTokens: z.number(),
  totalTokens: z.number(),
  cost: z.number(),
});

export type AnalyticsDimensionStat = z.infer<typeof analyticsDimensionStatSchema>;

export const analyticsFilterOptionSchema = z.object({
  id: z.string(),
  label: z.string(),
});

export type AnalyticsFilterOption = z.infer<typeof analyticsFilterOptionSchema>;
export type AnalyticsDimension = 'channel' | 'model' | 'apiKey' | 'user';
export type AnalyticsFilterDimension = 'project' | AnalyticsDimension;
export interface AnalyticsDimensionResult {
  items: AnalyticsDimensionStat[];
  truncated: boolean;
}

const analyticsDimensionValues: Record<AnalyticsDimension, string> = {
  channel: 'CHANNEL',
  model: 'MODEL',
  apiKey: 'API_KEY',
  user: 'USER',
};

const analyticsFilterDimensionValues: Record<AnalyticsFilterDimension, string> = {
  project: 'PROJECT',
  channel: 'CHANNEL',
  model: 'MODEL',
  apiKey: 'API_KEY',
  user: 'USER',
};

export const ANALYTICS_DIMENSION_LIMIT = 100;
const ANALYTICS_FILTER_OPTION_LIMIT = 100;

// --- GraphQL Queries ---

const ANALYTICS_OVERVIEW_QUERY = `
  query GetAnalyticsOverview($filter: AnalyticsFilter) {
    analyticsOverview(filter: $filter) {
      totalTokens
      totalInputTokens
      totalCachedInputTokens
      totalUncachedInputTokens
      totalOutputTokens
      totalRequests
      totalCost
    }
  }
`;

const ANALYTICS_DAILY_STATS_QUERY = `
  query GetAnalyticsDailyStats($filter: AnalyticsFilter) {
    analyticsDailyStats(filter: $filter) {
      date
      inputTokens
      cachedInputTokens
      uncachedInputTokens
      outputTokens
      totalTokens
      requestCount
      cost
    }
  }
`;

const ANALYTICS_DIMENSION_STATS_QUERY = `
  query GetAnalyticsDimensionStats($filter: AnalyticsFilter, $dimension: AnalyticsDimension!, $first: Int!) {
    analyticsDimensionStats(filter: $filter, dimension: $dimension, first: $first) {
      id
      name
      requestCount
      inputTokens
      cachedInputTokens
      outputTokens
      totalTokens
      cost
    }
  }
`;

const ANALYTICS_FILTER_OPTIONS_QUERY = `
  query GetAnalyticsFilterOptions($dimension: AnalyticsFilterDimension!, $search: String, $first: Int!, $startTime: String, $endTime: String) {
    analyticsFilterOptions(dimension: $dimension, search: $search, first: $first, startTime: $startTime, endTime: $endTime) {
      id
      label
    }
  }
`;

// --- Helper: convert filter to GraphQL input ---

// 直接发 YYYY-MM-DD 字符串，后端用系统时区解析（同仪表盘模式）
export function toGraphQLFilter(filter: AnalyticsFilter | null): Record<string, unknown> | null {
  if (!filter) return null;

  const result: Record<string, unknown> = {};

  if (filter.startTime) result.startTime = filter.startTime;
  if (filter.endTime) result.endTime = filter.endTime;
  if (filter.projectIDs && filter.projectIDs.length > 0) result.projectIDs = filter.projectIDs;
  if (filter.channelIDs && filter.channelIDs.length > 0) result.channelIDs = filter.channelIDs;
  if (filter.modelIDs && filter.modelIDs.length > 0) result.modelIDs = filter.modelIDs;
  if (filter.apiKeyIDs && filter.apiKeyIDs.length > 0) result.apiKeyIDs = filter.apiKeyIDs;
  if (filter.userIDs && filter.userIDs.length > 0) result.userIDs = filter.userIDs;

  return Object.keys(result).length > 0 ? result : null;
}

// --- React Query Hooks ---

export function useAnalyticsOverview(filter: AnalyticsFilter | null) {
  return useQuery({
    queryKey: ['analyticsOverview', filter],
    queryFn: async () => {
      const gqlFilter = toGraphQLFilter(filter);
      const data = await graphqlRequest<{ analyticsOverview: AnalyticsOverview }>(
        ANALYTICS_OVERVIEW_QUERY,
        { filter: gqlFilter }
      );
      return analyticsOverviewSchema.parse(data.analyticsOverview);
    },
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsDailyStats(filter: AnalyticsFilter | null) {
  return useQuery({
    queryKey: ['analyticsDailyStats', filter],
    queryFn: async () => {
      const gqlFilter = toGraphQLFilter(filter);
      const data = await graphqlRequest<{ analyticsDailyStats: AnalyticsDailyStat[] }>(
        ANALYTICS_DAILY_STATS_QUERY,
        { filter: gqlFilter }
      );
      return data.analyticsDailyStats.map((item) => analyticsDailyStatSchema.parse(item));
    },
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsDimensionStats(
  filter: AnalyticsFilter | null,
  dimension: AnalyticsDimension,
  enabled = true
) {
  const gqlDimension = analyticsDimensionValues[dimension];

  return useQuery({
    queryKey: ['analyticsDimensionStats', filter, dimension],
    queryFn: async () => {
      const gqlFilter = toGraphQLFilter(filter);
      const data = await graphqlRequest<{ analyticsDimensionStats: AnalyticsDimensionStat[] }>(
        ANALYTICS_DIMENSION_STATS_QUERY,
        { filter: gqlFilter, dimension: gqlDimension, first: ANALYTICS_DIMENSION_LIMIT + 1 }
      );
      const items = data.analyticsDimensionStats.map((item) => analyticsDimensionStatSchema.parse(item));
      return {
        items: items.slice(0, ANALYTICS_DIMENSION_LIMIT),
        truncated: items.length > ANALYTICS_DIMENSION_LIMIT,
      } satisfies AnalyticsDimensionResult;
    },
    enabled,
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsFilterOptions(
  dimension: AnalyticsFilterDimension,
  search: string,
  enabled = true,
  dateFilter: Pick<AnalyticsFilter, 'startTime' | 'endTime'> | null = null
) {
  const normalizedSearch = search.trim();
  const gqlDimension = analyticsFilterDimensionValues[dimension];

  return useQuery({
    queryKey: ['analyticsFilterOptions', dimension, normalizedSearch, dateFilter],
    queryFn: async () => {
      const data = await graphqlRequest<{ analyticsFilterOptions: AnalyticsFilterOption[] }>(
        ANALYTICS_FILTER_OPTIONS_QUERY,
        {
          dimension: gqlDimension,
          search: normalizedSearch || null,
          first: ANALYTICS_FILTER_OPTION_LIMIT,
          startTime: dateFilter?.startTime || null,
          endTime: dateFilter?.endTime || null,
        }
      );
      return data.analyticsFilterOptions.map((item) => analyticsFilterOptionSchema.parse(item));
    },
    enabled,
    staleTime: 60 * 1000,
    placeholderData: (previousData) => previousData,
  });
}
