import { useState, useCallback, useMemo } from 'react';
import { IconX, IconFilter } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAnalyticsFilterStore } from '@/stores/analyticsStore';
import { useDebounce } from '@/hooks/use-debounce';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { useAnalyticsFilterOptions } from '../data/analytics';
import type { AnalyticsFilterDimension } from '../data/analytics';
import { DateRangePicker } from '@/components/date-range-picker';
import type { DateTimeRangeValue } from '@/utils/date-range';
import { dateTimeRangeToTimestamps, startOfLocalDay, timestampsToDateTimeRange } from '../utils/date-range';
import { AnalyticsFacetedFilter } from './analytics-faceted-filter';

// Formats the selected range with second precision for the trigger label.
function formatDateTimeLabel(range: DateTimeRangeValue | undefined, placeholder: string): string {
  if (!range?.from && !range?.to) return placeholder;

  const format = (day: Date, time: { hh: string; mm: string; ss: string }) => {
    const date = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, '0')}-${String(day.getDate()).padStart(2, '0')}`;

    return `${date} ${time.hh}:${time.mm}:${time.ss}`;
  };

  const from = range.from ? format(range.from, range.startTime) : '...';
  const to = range.to ? format(range.to, range.endTime) : '...';

  return `${from} ~ ${to}`;
}

export function AnalyticsFilterBar() {
  const { t } = useTranslation();
  const filter = useAnalyticsFilterStore((state) => state.filter);
  const { setStartTime, setEndTime, setProjectIDs, setChannelIDs, setModelIDs, setAPIKeyIDs, setUserIDs, resetFilter } =
    useAnalyticsFilterStore();
  const { hasSystemScope } = usePermissions();
  const canReadDashboard = hasSystemScope('read_dashboard');
  const canReadProjects = hasSystemScope('read_projects');
  const canReadChannels = hasSystemScope('read_channels');
  const canReadAPIKeys = hasSystemScope('read_api_keys');
  const canReadUsers = hasSystemScope('read_users');
  const canFilterByUsers = canReadUsers && canReadAPIKeys;

  const [searches, setSearches] = useState<Record<AnalyticsFilterDimension, string>>({
    project: '',
    channel: '',
    model: '',
    apiKey: '',
    user: '',
  });
  const updateSearch = useCallback((dimension: AnalyticsFilterDimension, value: string) => {
    setSearches((current) => ({ ...current, [dimension]: value }));
  }, []);

  const debouncedProjectSearch = useDebounce(searches.project, 300);
  const debouncedChannelSearch = useDebounce(searches.channel, 300);
  const debouncedModelSearch = useDebounce(searches.model, 300);
  const debouncedAPIKeySearch = useDebounce(searches.apiKey, 300);
  const debouncedUserSearch = useDebounce(searches.user, 300);

  const projectOptionsQuery = useAnalyticsFilterOptions('project', debouncedProjectSearch, canReadProjects);
  const channelOptionsQuery = useAnalyticsFilterOptions('channel', debouncedChannelSearch, canReadChannels);
  const modelOptionsQuery = useAnalyticsFilterOptions('model', debouncedModelSearch, canReadDashboard, {
    startTime: filter.startTime,
    endTime: filter.endTime,
  });
  const apiKeyOptionsQuery = useAnalyticsFilterOptions('apiKey', debouncedAPIKeySearch, canReadAPIKeys);
  const userOptionsQuery = useAnalyticsFilterOptions('user', debouncedUserSearch, canFilterByUsers);

  const toFacetedOptions = (options: { id: string; label: string }[] | undefined) =>
    (options ?? []).map((option) => ({ label: option.label, value: option.id }));
  const projectOptions = toFacetedOptions(projectOptionsQuery.data);
  const channelOptions = toFacetedOptions(channelOptionsQuery.data);
  const modelOptions = toFacetedOptions(modelOptionsQuery.data);
  const apiKeyOptions = toFacetedOptions(apiKeyOptionsQuery.data);
  const userOptions = toFacetedOptions(userOptionsQuery.data);

  const bounds = useMemo(
    () => timestampsToDateTimeRange(filter.startTime, filter.endTime),
    [filter.startTime, filter.endTime]
  );

  const applyRange = useCallback(
    (start: Date | null, end: Date | null) => {
      setStartTime(start ? start.toISOString() : null);
      setEndTime(end ? end.toISOString() : null);
    },
    [setStartTime, setEndTime]
  );

  const handleRangeChange = useCallback(
    (range: DateTimeRangeValue | undefined) => {
      // Pass the stored bounds so an untouched range keeps its milliseconds.
      const { startTime, endTime } = dateTimeRangeToTimestamps(range, bounds);

      if (startTime && endTime && new Date(startTime) > new Date(endTime)) {
        toast.error(t('analytics.filter.startAfterEndError'));

        return;
      }

      setStartTime(startTime);
      setEndTime(endTime);
    },
    [bounds, setStartTime, setEndTime, t]
  );

  const setQuickRange = useCallback(
    (days: number) => {
      const now = new Date();
      const start = startOfLocalDay(now);
      start.setDate(start.getDate() - days + 1);

      applyRange(start, now);
    },
    [applyRange]
  );

  const setQuickMonth = useCallback(() => {
    const now = new Date();

    applyRange(new Date(now.getFullYear(), now.getMonth(), 1), now);
  }, [applyRange]);

  const setQuickYear = useCallback(() => {
    const now = new Date();

    applyRange(new Date(now.getFullYear(), 0, 1), now);
  }, [applyRange]);

  const hasFilters =
    filter.startTime ||
    filter.endTime ||
    (canReadProjects && filter.projectIDs) ||
    (canReadChannels && filter.channelIDs) ||
    (canReadDashboard && filter.modelIDs) ||
    (canReadAPIKeys && filter.apiKeyIDs) ||
    (canFilterByUsers && filter.userIDs);

  return (
    <div className='bg-card space-y-3 rounded-lg border p-4'>
      {/* Date Filters */}
      <div className='flex flex-wrap items-center gap-2'>
        <div className='flex items-center gap-1.5 text-sm font-medium'>
          <IconFilter className='text-muted-foreground h-4 w-4' />
          {t('analytics.filter.dateRange')}
        </div>

        {/* Date Range — precise to the second */}
        <DateRangePicker
          value={bounds.range}
          onChange={handleRangeChange}
          formatLabel={(range) => formatDateTimeLabel(range, t('analytics.filter.dateRangePlaceholder'))}
        />

        {/* Quick Range Buttons */}
        <div className='flex flex-wrap items-center gap-1'>
          <Button variant='outline' size='sm' className='h-8 text-xs' onClick={() => setQuickRange(1)}>
            {t('analytics.filter.today')}
          </Button>
          <Button variant='outline' size='sm' className='h-8 text-xs' onClick={() => setQuickRange(7)}>
            {t('analytics.filter.last7Days')}
          </Button>
          <Button variant='outline' size='sm' className='h-8 text-xs' onClick={() => setQuickRange(30)}>
            {t('analytics.filter.last30Days')}
          </Button>
          <Button variant='outline' size='sm' className='h-8 text-xs' onClick={setQuickMonth}>
            {t('analytics.filter.thisMonth')}
          </Button>
          <Button variant='outline' size='sm' className='h-8 text-xs' onClick={setQuickYear}>
            {t('analytics.filter.thisYear')}
          </Button>
          <Button variant='outline' size='sm' className='h-8 text-xs' onClick={() => setQuickRange(366)}>
            {t('analytics.filter.last12Months')}
          </Button>
        </div>
      </div>

      {/* Dimension Filters */}
      <div className='flex flex-wrap items-center gap-2'>
        {canReadProjects && (
          <AnalyticsFacetedFilter
            title={t('analytics.filter.project')}
            options={projectOptions}
            selectedValues={filter.projectIDs || []}
            onSelectedValuesChange={setProjectIDs}
            search={searches.project}
            onSearchChange={(value) => updateSearch('project', value)}
            isLoading={projectOptionsQuery.isLoading}
            isFetching={projectOptionsQuery.isFetching}
            isError={projectOptionsQuery.isError}
            onRetry={() => void projectOptionsQuery.refetch()}
          />
        )}

        {canReadChannels && (
          <AnalyticsFacetedFilter
            title={t('analytics.filter.channel')}
            options={channelOptions}
            selectedValues={filter.channelIDs || []}
            onSelectedValuesChange={setChannelIDs}
            search={searches.channel}
            onSearchChange={(value) => updateSearch('channel', value)}
            isLoading={channelOptionsQuery.isLoading}
            isFetching={channelOptionsQuery.isFetching}
            isError={channelOptionsQuery.isError}
            onRetry={() => void channelOptionsQuery.refetch()}
          />
        )}

        {canReadDashboard && (
          <AnalyticsFacetedFilter
            title={t('analytics.filter.model')}
            options={modelOptions}
            selectedValues={filter.modelIDs || []}
            onSelectedValuesChange={setModelIDs}
            search={searches.model}
            onSearchChange={(value) => updateSearch('model', value)}
            isLoading={modelOptionsQuery.isLoading}
            isFetching={modelOptionsQuery.isFetching}
            isError={modelOptionsQuery.isError}
            onRetry={() => void modelOptionsQuery.refetch()}
          />
        )}

        {canReadAPIKeys && (
          <AnalyticsFacetedFilter
            title={t('analytics.filter.apiKey')}
            options={apiKeyOptions}
            selectedValues={filter.apiKeyIDs || []}
            onSelectedValuesChange={setAPIKeyIDs}
            search={searches.apiKey}
            onSearchChange={(value) => updateSearch('apiKey', value)}
            isLoading={apiKeyOptionsQuery.isLoading}
            isFetching={apiKeyOptionsQuery.isFetching}
            isError={apiKeyOptionsQuery.isError}
            onRetry={() => void apiKeyOptionsQuery.refetch()}
          />
        )}

        {canFilterByUsers && (
          <AnalyticsFacetedFilter
            title={t('analytics.filter.user')}
            options={userOptions}
            selectedValues={filter.userIDs || []}
            onSelectedValuesChange={setUserIDs}
            search={searches.user}
            onSearchChange={(value) => updateSearch('user', value)}
            isLoading={userOptionsQuery.isLoading}
            isFetching={userOptionsQuery.isFetching}
            isError={userOptionsQuery.isError}
            onRetry={() => void userOptionsQuery.refetch()}
          />
        )}

        {/* Reset Button */}
        {hasFilters && (
          <Button variant='ghost' size='sm' className='text-muted-foreground h-8 text-xs' onClick={resetFilter}>
            <IconX className='mr-1 h-3 w-3' />
            {t('analytics.filter.reset')}
          </Button>
        )}
      </div>
    </div>
  );
}
