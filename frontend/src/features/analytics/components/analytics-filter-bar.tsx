import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { IconCalendar, IconX, IconFilter, IconLoader2, IconRefresh, IconSearch } from '@tabler/icons-react';
import { cn } from '@/lib/utils';
import { useDebounce } from '@/hooks/use-debounce';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Calendar } from '@/components/ui/calendar';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import { useAnalyticsFilterStore } from '@/stores/analyticsStore';
import { useAnalyticsFilterOptions } from '../data/analytics';
import type { AnalyticsFilterDimension, AnalyticsFilterOption } from '../data/analytics';

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

interface MultiSelectProps {
  label: string;
  placeholder: string;
  options: AnalyticsFilterOption[];
  selected: string[];
  onChange: (values: string[]) => void;
  search: string;
  onSearchChange: (value: string) => void;
  isLoading?: boolean;
  isFetching?: boolean;
  isError?: boolean;
  onRetry: () => void;
}

function MultiSelect({
  label,
  placeholder,
  options,
  selected,
  onChange,
  search,
  onSearchChange,
  isLoading,
  isFetching,
  isError,
  onRetry,
}: MultiSelectProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);

  const toggle = (id: string) => {
    if (selected.includes(id)) {
      onChange(selected.filter((value) => value !== id));
    } else {
      onChange([...selected, id]);
    }
  };

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (!nextOpen) {
      onSearchChange('');
    }
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          variant='outline'
          role='combobox'
          aria-expanded={open}
          className='h-8 min-w-[140px] justify-between text-xs font-normal'
        >
          <span className='truncate'>
            {selected.length > 0 ? `${label} (${selected.length})` : placeholder}
          </span>
        </Button>
      </PopoverTrigger>
      <PopoverContent className='w-[220px] p-0' align='start'>
        <div className='border-b p-2'>
          <div className='relative'>
            <IconSearch className='absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground' />
            <Input
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder={placeholder}
              className='h-8 px-7 text-xs'
            />
            {isFetching && (
              <IconLoader2 className='absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-muted-foreground' />
            )}
          </div>
        </div>
        <div className='max-h-[300px] overflow-auto p-1'>
          {isError ? (
            <div className='flex flex-col items-center gap-2 px-2 py-4 text-center text-xs text-muted-foreground'>
              <span>{t('common.errors.loadFailed')}</span>
              <Button type='button' variant='ghost' size='sm' className='h-7 text-xs' onClick={onRetry}>
                <IconRefresh className='mr-1 h-3 w-3' />
                {t('common.buttons.retry')}
              </Button>
            </div>
          ) : isLoading ? (
            <div className='flex items-center justify-center py-4'>
              <Skeleton className='h-4 w-full' />
            </div>
          ) : options.length === 0 ? (
            <div className='px-2 py-4 text-center text-xs text-muted-foreground'>
              {placeholder}
            </div>
          ) : (
            options.map((option) => (
              <button
                key={option.id}
                type='button'
                className={cn(
                  'flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-xs hover:bg-accent',
                  selected.includes(option.id) && 'bg-accent'
                )}
                onClick={() => toggle(option.id)}
              >
                <input
                  type='checkbox'
                  checked={selected.includes(option.id)}
                  onChange={() => {}}
                  className='h-3 w-3'
                />
                <span className='truncate'>{option.label}</span>
              </button>
            ))
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
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
            className={cn(
              'h-8 w-[130px] justify-start text-left text-xs font-normal',
              !startDate && 'text-muted-foreground'
            )}
          >
            <IconCalendar className='mr-1 h-3 w-3' />
            {startDate || t('analytics.filter.startDate')}
          </Button>
        </PopoverTrigger>
        <PopoverContent className='w-auto p-0' align='start'>
          <Calendar
            mode='single'
            selected={startDate ? parseDate(startDate) : undefined}
            onSelect={handleStartDateSelect}
            initialFocus
          />
        </PopoverContent>
      </Popover>

      <span className='text-muted-foreground'>~</span>

      <Popover open={endOpen} onOpenChange={setEndOpen}>
        <PopoverTrigger asChild>
          <Button
            variant='outline'
            className={cn(
              'h-8 w-[130px] justify-start text-left text-xs font-normal',
              !endDate && 'text-muted-foreground'
            )}
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
  const {
    setStartTime,
    setEndTime,
    setProjectIDs,
    setChannelIDs,
    setModelIDs,
    setAPIKeyIDs,
    setUserIDs,
    resetFilter,
  } = useAnalyticsFilterStore();
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

  const projectOptions = projectOptionsQuery.data ?? [];
  const channelOptions = channelOptionsQuery.data ?? [];
  const modelOptions = modelOptionsQuery.data ?? [];
  const apiKeyOptions = apiKeyOptionsQuery.data ?? [];
  const userOptions = userOptionsQuery.data ?? [];

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
    <div className='space-y-3 rounded-lg border bg-card p-4'>
      {/* Date Filters */}
      <div className='flex flex-wrap items-center gap-2'>
        <div className='flex items-center gap-1.5 text-sm font-medium'>
          <IconFilter className='h-4 w-4 text-muted-foreground' />
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
          <MultiSelect
            label={t('analytics.filter.project')}
            placeholder={t('analytics.filter.selectProject')}
            options={projectOptions}
            selected={filter.projectIDs || []}
            onChange={setProjectIDs}
            search={searches.project}
            onSearchChange={(value) => updateSearch('project', value)}
            isLoading={projectOptionsQuery.isLoading}
            isFetching={projectOptionsQuery.isFetching}
            isError={projectOptionsQuery.isError}
            onRetry={() => void projectOptionsQuery.refetch()}
          />
        )}

        {canReadChannels && (
          <MultiSelect
            label={t('analytics.filter.channel')}
            placeholder={t('analytics.filter.selectChannel')}
            options={channelOptions}
            selected={filter.channelIDs || []}
            onChange={setChannelIDs}
            search={searches.channel}
            onSearchChange={(value) => updateSearch('channel', value)}
            isLoading={channelOptionsQuery.isLoading}
            isFetching={channelOptionsQuery.isFetching}
            isError={channelOptionsQuery.isError}
            onRetry={() => void channelOptionsQuery.refetch()}
          />
        )}

        {canReadDashboard && (
          <MultiSelect
            label={t('analytics.filter.model')}
            placeholder={t('analytics.filter.selectModel')}
            options={modelOptions}
            selected={filter.modelIDs || []}
            onChange={setModelIDs}
            search={searches.model}
            onSearchChange={(value) => updateSearch('model', value)}
            isLoading={modelOptionsQuery.isLoading}
            isFetching={modelOptionsQuery.isFetching}
            isError={modelOptionsQuery.isError}
            onRetry={() => void modelOptionsQuery.refetch()}
          />
        )}

        {canReadAPIKeys && (
          <MultiSelect
            label={t('analytics.filter.apiKey')}
            placeholder={t('analytics.filter.selectAPIKey')}
            options={apiKeyOptions}
            selected={filter.apiKeyIDs || []}
            onChange={setAPIKeyIDs}
            search={searches.apiKey}
            onSearchChange={(value) => updateSearch('apiKey', value)}
            isLoading={apiKeyOptionsQuery.isLoading}
            isFetching={apiKeyOptionsQuery.isFetching}
            isError={apiKeyOptionsQuery.isError}
            onRetry={() => void apiKeyOptionsQuery.refetch()}
          />
        )}

        {canFilterByUsers && (
          <MultiSelect
            label={t('analytics.filter.user')}
            placeholder={t('analytics.filter.selectUser')}
            options={userOptions}
            selected={filter.userIDs || []}
            onChange={setUserIDs}
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
          <Button variant='ghost' size='sm' className='h-8 text-xs text-muted-foreground' onClick={resetFilter}>
            <IconX className='mr-1 h-3 w-3' />
            {t('analytics.filter.reset')}
          </Button>
        )}
      </div>

      {/* Active Filters Display */}
      {hasFilters && (
        <div className='flex flex-wrap gap-1'>
          {filter.startTime && (
            <Badge variant='secondary' className='text-xs'>
              {t('analytics.filter.startDate')}: {filter.startTime}
              <button type='button' className='ml-1' onClick={() => setStartTime(null)}>
                <IconX className='h-3 w-3' />
              </button>
            </Badge>
          )}
          {filter.endTime && (
            <Badge variant='secondary' className='text-xs'>
              {t('analytics.filter.endDate')}: {filter.endTime}
              <button type='button' className='ml-1' onClick={() => setEndTime(null)}>
                <IconX className='h-3 w-3' />
              </button>
            </Badge>
          )}
          {canReadProjects && filter.projectIDs?.map((id) => {
            const name = projectOptions.find((option) => option.id === id)?.label || id;
            return (
              <Badge key={id} variant='secondary' className='text-xs'>
                {name}
                <button type='button' className='ml-1' onClick={() => setProjectIDs(filter.projectIDs!.filter((i) => i !== id))}>
                  <IconX className='h-3 w-3' />
                </button>
              </Badge>
            );
          })}
          {canReadChannels && filter.channelIDs?.map((id) => {
            const name = channelOptions.find((option) => option.id === id)?.label || id;
            return (
              <Badge key={id} variant='secondary' className='text-xs'>
                {name}
                <button type='button' className='ml-1' onClick={() => setChannelIDs(filter.channelIDs!.filter((i) => i !== id))}>
                  <IconX className='h-3 w-3' />
                </button>
              </Badge>
            );
          })}
          {canReadDashboard && filter.modelIDs?.map((id) => (
            <Badge key={id} variant='secondary' className='text-xs'>
              {id}
              <button type='button' className='ml-1' onClick={() => setModelIDs(filter.modelIDs!.filter((i) => i !== id))}>
                <IconX className='h-3 w-3' />
              </button>
            </Badge>
          ))}
          {canReadAPIKeys && filter.apiKeyIDs?.map((id) => {
            const name = apiKeyOptions.find((option) => option.id === id)?.label || id;
            return (
              <Badge key={id} variant='secondary' className='text-xs'>
                {name}
                <button type='button' className='ml-1' onClick={() => setAPIKeyIDs(filter.apiKeyIDs!.filter((i) => i !== id))}>
                  <IconX className='h-3 w-3' />
                </button>
              </Badge>
            );
          })}
          {canFilterByUsers && filter.userIDs?.map((id) => {
            const name = userOptions.find((option) => option.id === id)?.label || id;
            return (
              <Badge key={id} variant='secondary' className='text-xs'>
                {name}
                <button type='button' className='ml-1' onClick={() => setUserIDs(filter.userIDs!.filter((i) => i !== id))}>
                  <IconX className='h-3 w-3' />
                </button>
              </Badge>
            );
          })}
        </div>
      )}
    </div>
  );
}
