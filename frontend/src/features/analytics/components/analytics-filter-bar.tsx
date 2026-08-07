import { useState, useCallback } from 'react';
import { IconCalendar, IconX, IconFilter } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAnalyticsFilterStore } from '@/stores/analyticsStore';
import { cn } from '@/lib/utils';
import { useDebounce } from '@/hooks/use-debounce';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Calendar } from '@/components/ui/calendar';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useAnalyticsFilterOptions } from '../data/analytics';
import type { AnalyticsFilterDimension } from '../data/analytics';
import { AnalyticsFacetedFilter } from './analytics-faceted-filter';

// Calendar Date → 'YYYY-MM-DD' 字符串（直接取本地年月日，不做时区转换）
function formatDate(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

// 'YYYY-MM-DD' → Date（用于 Calendar selected）
function parseDate(dateStr: string): Date {
  const [y, m, d] = dateStr.split('-').map(Number);
  return new Date(y, m - 1, d);
}

interface DateRangePickerProps {
  startDate: string | null;
  endDate: string | null;
  onStartChange: (date: Date | null) => void;
  onEndChange: (date: Date | null) => void;
}

function DateRangePicker({ startDate, endDate, onStartChange, onEndChange }: DateRangePickerProps) {
  const { t } = useTranslation();
  const [startOpen, setStartOpen] = useState(false);
  const [endOpen, setEndOpen] = useState(false);

  const handleStartDateSelect = useCallback(
    (date: Date | undefined) => {
      if (date && endDate) {
        const end = parseDate(endDate);
        if (date > end) {
          toast.error(t('analytics.filter.startDateAfterEndError'));
          return;
        }
      }
      onStartChange(date || null);
      setStartOpen(false);
    },
    [endDate, onStartChange, t]
  );

  const handleEndDateSelect = useCallback(
    (date: Date | undefined) => {
      if (date && startDate) {
        const start = parseDate(startDate);
        if (date < start) {
          toast.error(t('analytics.filter.endDateBeforeStartError'));
          return;
        }
      }
      onEndChange(date || null);
      setEndOpen(false);
    },
    [startDate, onEndChange, t]
  );

  return (
    <div className='flex items-center gap-2'>
      <Popover open={startOpen} onOpenChange={setStartOpen}>
        <PopoverTrigger asChild>
          <Button
            variant='outline'
            className={cn('h-8 w-[130px] justify-start text-left text-xs font-normal', !startDate && 'text-muted-foreground')}
          >
            <IconCalendar className='mr-1 h-3 w-3' />
            {startDate || t('analytics.filter.startDate')}
          </Button>
        </PopoverTrigger>
        <PopoverContent className='w-auto p-0' align='start'>
          <Calendar mode='single' selected={startDate ? parseDate(startDate) : undefined} onSelect={handleStartDateSelect} initialFocus />
        </PopoverContent>
      </Popover>

      <span className='text-muted-foreground'>~</span>

      <Popover open={endOpen} onOpenChange={setEndOpen}>
        <PopoverTrigger asChild>
          <Button
            variant='outline'
            className={cn('h-8 w-[130px] justify-start text-left text-xs font-normal', !endDate && 'text-muted-foreground')}
          >
            <IconCalendar className='mr-1 h-3 w-3' />
            {endDate || t('analytics.filter.endDate')}
          </Button>
        </PopoverTrigger>
        <PopoverContent className='w-auto p-0' align='start'>
          <Calendar
            mode='single'
            selected={endDate ? parseDate(endDate) : undefined}
            onSelect={handleEndDateSelect}
            disabled={{ after: new Date() }}
            initialFocus
          />
        </PopoverContent>
      </Popover>
    </div>
  );
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

  const handleStartDate = useCallback(
    (date: Date | null) => {
      setStartTime(date ? formatDate(date) : null);
    },
    [setStartTime]
  );

  const handleEndDate = useCallback(
    (date: Date | null) => {
      setEndTime(date ? formatDate(date) : null);
    },
    [setEndTime]
  );

  const setQuickRange = useCallback(
    (days: number) => {
      const now = new Date();
      const start = new Date();
      start.setDate(now.getDate() - days + 1);
      start.setHours(0, 0, 0, 0);
      setStartTime(formatDate(start));
      setEndTime(formatDate(now));
    },
    [setStartTime, setEndTime]
  );

  const setQuickMonth = useCallback(() => {
    const now = new Date();
    const start = new Date(now.getFullYear(), now.getMonth(), 1);
    setStartTime(formatDate(start));
    setEndTime(formatDate(now));
  }, [setStartTime, setEndTime]);

  const setQuickYear = useCallback(() => {
    const now = new Date();
    const start = new Date(now.getFullYear(), 0, 1);
    setStartTime(formatDate(start));
    setEndTime(formatDate(now));
  }, [setStartTime, setEndTime]);

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

        {/* Date Range */}
        <DateRangePicker
          startDate={filter.startTime ?? null}
          endDate={filter.endTime ?? null}
          onStartChange={handleStartDate}
          onEndChange={handleEndDate}
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
