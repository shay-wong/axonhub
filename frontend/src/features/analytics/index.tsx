import { useMemo } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Header } from '@/components/layout/header';
import { Button } from '@/components/ui/button';
import { usePermissions } from '@/hooks/usePermissions';
import { useAnalyticsFilterStore } from '@/stores/analyticsStore';
import { useAnalyticsOverview, useAnalyticsDailyStats, useAnalyticsDimensionStats } from './data/analytics';
import type { AnalyticsDimension } from './data/analytics';
import { AnalyticsFilterBar } from './components/analytics-filter-bar';
import { AnalyticsQueryError } from './components/analytics-query-error';
import { OverviewCards } from './components/overview-cards';
import { CombinedTrendChart } from './components/combined-trend-chart';
import { DimensionPieCharts } from './components/dimension-pie-charts';
import { DimensionDetailTable } from './components/dimension-detail-table';
import { useGeneralSettings } from '@/features/system/data/system';

export default function AnalyticsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const filter = useAnalyticsFilterStore((state) => state.filter);
  const { data: generalSettings } = useGeneralSettings();
  const { hasSystemScope } = usePermissions();

  const currencyCode = generalSettings?.currencyCode || 'USD';
  const canReadDashboard = hasSystemScope('read_dashboard');
  const canReadProjects = hasSystemScope('read_projects');
  const canReadChannels = hasSystemScope('read_channels');
  const canReadAPIKeys = hasSystemScope('read_api_keys');
  const canReadUsers = hasSystemScope('read_users');
  const canFilterByUsers = canReadUsers && canReadAPIKeys;

  const authorizedFilter = useMemo(
    () => ({
      ...filter,
      projectIDs: canReadProjects ? filter.projectIDs : undefined,
      channelIDs: canReadChannels ? filter.channelIDs : undefined,
      modelIDs: canReadDashboard ? filter.modelIDs : undefined,
      apiKeyIDs: canReadAPIKeys ? filter.apiKeyIDs : undefined,
      userIDs: canFilterByUsers ? filter.userIDs : undefined,
    }),
    [filter, canReadProjects, canReadChannels, canReadDashboard, canReadAPIKeys, canFilterByUsers]
  );

  const availableDimensions = useMemo<AnalyticsDimension[]>(() => {
    const dimensions: AnalyticsDimension[] = [];
    if (canReadChannels) dimensions.push('channel');
    if (canReadDashboard) dimensions.push('model');
    if (canReadAPIKeys) dimensions.push('apiKey');
    if (canReadUsers) dimensions.push('user');
    return dimensions;
  }, [canReadChannels, canReadDashboard, canReadAPIKeys, canReadUsers]);

  const overviewQuery = useAnalyticsOverview(authorizedFilter);
  const dailyStatsQuery = useAnalyticsDailyStats(authorizedFilter);
  const channelStatsQuery = useAnalyticsDimensionStats(authorizedFilter, 'channel', canReadChannels);
  const modelStatsQuery = useAnalyticsDimensionStats(authorizedFilter, 'model', canReadDashboard);
  const apiKeyStatsQuery = useAnalyticsDimensionStats(authorizedFilter, 'apiKey', canReadAPIKeys);
  const userStatsQuery = useAnalyticsDimensionStats(authorizedFilter, 'user', canReadUsers);

  const dimensionQueries = [
    { enabled: canReadChannels, query: channelStatsQuery },
    { enabled: canReadDashboard, query: modelStatsQuery },
    { enabled: canReadAPIKeys, query: apiKeyStatsQuery },
    { enabled: canReadUsers, query: userStatsQuery },
  ];
  const analyticsQueries = [
    overviewQuery,
    dailyStatsQuery,
    ...dimensionQueries.filter((item) => item.enabled).map((item) => item.query),
  ];
  const failedAnalyticsQueries = analyticsQueries.filter((query) => query.isError);
  const hasAnalyticsError = failedAnalyticsQueries.length > 0;
  const analyticsError = failedAnalyticsQueries[0]?.error;
  const retryFailedAnalyticsQueries = () => {
    void Promise.all(failedAnalyticsQueries.map((query) => query.refetch()));
  };

  const isDimensionLoading = dimensionQueries.some((item) => item.enabled && item.query.isLoading);
  const truncatedDimensions: Record<AnalyticsDimension, boolean> = {
    channel: channelStatsQuery.data?.truncated ?? false,
    model: modelStatsQuery.data?.truncated ?? false,
    apiKey: apiKeyStatsQuery.data?.truncated ?? false,
    user: userStatsQuery.data?.truncated ?? false,
  };

  return (
    <div className='flex-1 space-y-6 p-8 pt-6'>
      <Header />
      <Button onClick={() => navigate({ to: '/' })} variant='outline' className='self-start'>
        {t('dashboard.channelSuccessRates.backToDashboard')}
      </Button>
      <AnalyticsFilterBar />
      {hasAnalyticsError ? (
        <AnalyticsQueryError error={analyticsError} onRetry={retryFailedAnalyticsQueries} />
      ) : (
        <>
          <OverviewCards overview={overviewQuery.data} isLoading={overviewQuery.isLoading} />
          <CombinedTrendChart data={dailyStatsQuery.data ?? []} isLoading={dailyStatsQuery.isLoading} currencyCode={currencyCode} />
          <DimensionPieCharts
            channelStats={channelStatsQuery.data?.items ?? []}
            modelStats={modelStatsQuery.data?.items ?? []}
            apiKeyStats={apiKeyStatsQuery.data?.items ?? []}
            userStats={userStatsQuery.data?.items ?? []}
            isLoading={isDimensionLoading}
            currencyCode={currencyCode}
            availableDimensions={availableDimensions}
            truncatedDimensions={truncatedDimensions}
          />
          <DimensionDetailTable
            channelStats={channelStatsQuery.data?.items ?? []}
            modelStats={modelStatsQuery.data?.items ?? []}
            apiKeyStats={apiKeyStatsQuery.data?.items ?? []}
            userStats={userStatsQuery.data?.items ?? []}
            isLoading={isDimensionLoading}
            availableDimensions={availableDimensions}
            truncatedDimensions={truncatedDimensions}
          />
        </>
      )}
    </div>
  );
}
