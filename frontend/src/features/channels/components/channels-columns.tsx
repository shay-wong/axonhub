import { useCallback, useState, memo, useRef, useEffect } from 'react';
import { format } from 'date-fns';
import { DotsHorizontalIcon } from '@radix-ui/react-icons';
import { useIsMutating } from '@tanstack/react-query';
import { ColumnDef, Row, Table } from '@tanstack/react-table';
import {
  IconPlayerPlay,
  IconChevronDown,
  IconChevronRight,
  IconAlertTriangle,
  IconEdit,
  IconArchive,
  IconTrash,
  IconCheck,
  IconTransform,
  IconNetwork,
  IconAdjustments,
  IconRoute,
  IconCopy,
  IconCoin,
  IconLoader2,
  IconKeyOff,
  IconGauge,
  IconHistory,
  IconPlugConnected,
  IconShieldLock,
} from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { DataTableColumnHeader } from '@/components/data-table-column-header';
import { useChannels } from '../context/channels-context';
import { createAPIKeyNameMap, formatAPIKeyIdentity } from '../data/api-key-display';
import {
  ChannelStatusActionID,
  ChannelStatusTone,
  ChannelStatusViewItem,
  ChannelStatusViewMenuItem,
  getChannelStatusPolicy,
  getChannelStatusViewModel,
} from '../data/channel-status-policy';
import {
  CLEAR_CHANNEL_TEMPORARY_DISABLE_MUTATION_KEY,
  useClearChannelTemporaryDisable,
  useTestChannel,
  useUpdateChannel,
} from '../data/channels';
import { CHANNEL_CONFIGS, getProvider } from '../data/config_channels';
import { Channel } from '../data/schema';
import { ChannelHealthCell } from './channel-health-cell';
import { ChannelLimiterCell } from './channel-limiter-cell';
import { ChannelsStatusDialog } from './channels-status-dialog';

const WEIGHT_PRECISION = 4;
const MIN_WEIGHT = 0;
const MAX_WEIGHT = 100;

type StatusIconAction = {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  pending?: boolean;
};
type StatusIconComponent = typeof IconAlertTriangle;
type ChannelStatusIcon = {
  kind: ChannelStatusViewItem['kind'];
  tooltipKind: ChannelStatusViewItem['tooltipKind'];
  icon: StatusIconComponent;
  className: string;
  action?: StatusIconAction;
};
type ChannelCellProps = {
  row: Row<Channel>;
  canWrite: boolean;
};

const formatWeight = (value: number) => Number(value.toFixed(WEIGHT_PRECISION));
const clampWeight = (value: number) => formatWeight(Math.min(MAX_WEIGHT, Math.max(MIN_WEIGHT, value)));

function assertNever(value: never): never {
  throw new Error(`Unhandled channel status action: ${value}`);
}

function getConfiguredAPIKeys(channel: Channel): string[] {
  const keys = channel.credentials?.apiKeys?.filter((key) => key.trim().length > 0) ?? [];
  if (keys.length > 0) return keys;
  return channel.credentials?.apiKeyConfigs?.map((config) => config.key.trim()).filter((key) => key.length > 0) ?? [];
}

function DisabledAPIKeysTooltipContent({ channel, label }: { channel: Channel; label: string }) {
  const apiKeyNames = createAPIKeyNameMap(channel.credentials?.apiKeyConfigs);
  const identities = (channel.disabledAPIKeys ?? []).map((item) => ({
    key: item.key,
    label: formatAPIKeyIdentity(item.key, apiKeyNames.get(item.key.trim())),
  }));

  return (
    <div className='max-w-72 space-y-1.5'>
      <p className='text-sm text-amber-500'>{label}</p>
      {identities.map((identity) => (
        <code key={identity.key} className='bg-muted text-foreground block truncate rounded px-1.5 py-0.5 text-xs' title={identity.label}>
          {identity.label}
        </code>
      ))}
    </div>
  );
}

// Status Switch Cell Component to handle status toggle with confirmation dialog
const StatusSwitchCell = memo(({ row, canWrite }: ChannelCellProps) => {
  const channel = row.original;
  const [dialogOpen, setDialogOpen] = useState(false);
  const { channelPermissions } = usePermissions();

  const isEnabled = channel.status === 'enabled';
  const isArchived = channel.status === 'archived';

  const handleSwitchClick = useCallback(() => {
    if (canWrite && !isArchived) {
      setDialogOpen(true);
    }
  }, [canWrite, isArchived]);

  if (!channelPermissions.canWrite) {
    return <Badge variant='outline'>{channel.status}</Badge>;
  }

  return (
    <div className='flex justify-center'>
      <Switch
        checked={isEnabled}
        onCheckedChange={handleSwitchClick}
        disabled={!canWrite || isArchived}
        data-testid='channel-status-switch'
      />
      {dialogOpen && <ChannelsStatusDialog open={dialogOpen} onOpenChange={setDialogOpen} currentRow={channel} />}
    </div>
  );
});

StatusSwitchCell.displayName = 'StatusSwitchCell';

const ChannelStatusIconButton = ({
  icon: IconComponent,
  className,
  action,
}: {
  icon: StatusIconComponent;
  className: string;
  action?: StatusIconAction;
}) => {
  if (!action) {
    return <IconComponent className={cn('h-4 w-4 shrink-0', className)} />;
  }

  return (
    <button
      type='button'
      className={cn(
        'hover:bg-muted focus-visible:ring-ring shrink-0 rounded-sm p-0.5 transition-colors focus-visible:ring-2 focus-visible:outline-hidden',
        className
      )}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        action.onClick();
      }}
      disabled={action.disabled}
      aria-label={action.label}
      title={action.label}
    >
      {action.pending ? <IconLoader2 className='h-4 w-4 animate-spin' /> : <IconComponent className='h-4 w-4' />}
    </button>
  );
};

const CHANNEL_STATUS_ICON_BY_TONE: Record<ChannelStatusTone, { icon: StatusIconComponent; className: string }> = {
  destructive: { icon: IconAlertTriangle, className: 'text-destructive' },
  temporaryDisable: { icon: IconHistory, className: 'text-orange-500' },
  disabledKeys: { icon: IconKeyOff, className: 'text-amber-500' },
};

function useChannelStatusActions(channel: Channel, t: ReturnType<typeof useTranslation>['t'], canWrite: boolean) {
  const { setOpen, setCurrentRow } = useChannels();
  const clearTemporaryDisable = useClearChannelTemporaryDisable();
  const policy = getChannelStatusPolicy(channel, { canWrite });
  const isClearingTemporaryDisable =
    useIsMutating({
      mutationKey: CLEAR_CHANNEL_TEMPORARY_DISABLE_MUTATION_KEY,
      predicate: (mutation) => (mutation.state.variables as { channelID?: string } | undefined)?.channelID === channel.id,
    }) > 0;
  const viewModel = getChannelStatusViewModel(policy, { clearTemporaryDisablePending: isClearingTemporaryDisable });

  const handleRecover = useCallback(() => {
    setCurrentRow(channel);
    setOpen('errorResolved');
  }, [channel, setCurrentRow, setOpen]);

  const handleClearTemporaryDisable = useCallback(() => {
    clearTemporaryDisable.mutate({ channelID: channel.id });
  }, [channel.id, clearTemporaryDisable]);

  const handleDisabledKeys = useCallback(() => {
    setCurrentRow(channel);
    setOpen('disabledAPIKeys');
  }, [channel, setCurrentRow, setOpen]);

  const onClickByActionID: Record<ChannelStatusActionID, () => void> = {
    resolveError: handleRecover,
    clearTemporaryDisable: handleClearTemporaryDisable,
    manageDisabledKeys: handleDisabledKeys,
  };

  const toAction = (item: ChannelStatusViewMenuItem): StatusIconAction => ({
    label: t(item.action.quickLabelKey),
    onClick: onClickByActionID[item.action.id],
    disabled: item.action.disabled,
    pending: item.action.pending,
  });

  const getIconAction = (item: ChannelStatusViewItem): StatusIconAction | undefined => {
    if (!item.action) return undefined;
    return toAction({ ...item, action: item.action });
  };

  const getIcon = (item: ChannelStatusViewItem): ChannelStatusIcon => ({
    kind: item.kind,
    tooltipKind: item.tooltipKind,
    ...CHANNEL_STATUS_ICON_BY_TONE[item.tone],
    action: getIconAction(item),
  });

  return {
    policy,
    viewModel,
    toAction,
    statusIcons: viewModel.statusItems.map(getIcon),
  };
}

// Action Cell Component to handle hooks properly
const ActionCell = memo(({ row, canWrite }: ChannelCellProps) => {
  const { t } = useTranslation();
  const channel = row.original;
  const { setOpen, setCurrentRow } = useChannels();
  const testChannel = useTestChannel();
  const channelStatusActions = useChannelStatusActions(channel, t, canWrite);
  const isArchived = channel.status === 'archived';
  const {
    policy: { disabledKeysCount },
    viewModel,
    toAction,
  } = channelStatusActions;
  const apiKeysCount = getConfiguredAPIKeys(channel).length;
  const hasMultipleAPIKeys = canWrite && apiKeysCount > 1;

  const handleDefaultTest = async () => {
    try {
      await testChannel.mutateAsync({
        channelID: channel.id,
        modelID: channel.defaultTestModel || undefined,
      });
    } catch (_error) {}
  };

  const handleOpenTestDialog = useCallback(() => {
    setCurrentRow(channel);
    setOpen('test');
  }, [channel, setCurrentRow, setOpen]);

  const handleEdit = useCallback(() => {
    setCurrentRow(channel);
    setOpen('edit');
  }, [channel, setCurrentRow, setOpen]);

  return (
    <div className='flex items-center justify-center gap-1'>
      <Button size='sm' variant='outline' className='h-8 w-8 p-0' onClick={handleEdit}>
        <IconEdit className='h-3 w-3' />
      </Button>
      <Button size='sm' variant='outline' className='h-8 px-3' onClick={handleDefaultTest} disabled={testChannel.isPending}>
        <IconPlayerPlay className='mr-1 h-3 w-3' />
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size='sm' variant='outline' className='h-8 w-8 p-0' data-testid='row-actions'>
            <DotsHorizontalIcon className='h-3 w-3' />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-[160px]'>
          <DropdownMenuItem onClick={handleOpenTestDialog}>
            <IconPlayerPlay size={16} className='mr-2' />
            {t('channels.actions.test')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('testHistory');
            }}
          >
            <IconHistory size={16} className='mr-2' />
            {t('channels.actions.testHistory')}
          </DropdownMenuItem>
          <DropdownMenuSeparator />

          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('duplicate');
            }}
          >
            <IconCopy size={16} className='mr-2' />
            {t('common.actions.duplicate')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('modelMapping');
            }}
          >
            <IconRoute size={16} className='mr-2' />
            {t('channels.dialogs.settings.modelMapping.title')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('price');
            }}
          >
            <IconCoin size={16} className='mr-2' />
            {t('channels.actions.modelPrice')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('overrides');
            }}
          >
            <IconAdjustments size={16} className='mr-2' />
            {t('channels.dialogs.settings.overrides.action')}
          </DropdownMenuItem>

          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('proxy');
            }}
          >
            <IconNetwork size={16} className='mr-2' />
            {t('channels.dialogs.proxy.action')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('transformOptions');
            }}
          >
            <IconTransform size={16} className='mr-2' />
            {t('channels.dialogs.transformOptions.action')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('rateLimit');
            }}
          >
            <IconGauge size={16} className='mr-2' />
            {t('channels.dialogs.rateLimit.action')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('endpoints');
            }}
          >
            <IconPlugConnected size={16} className='mr-2' />
            {t('channels.endpoints.title')}
          </DropdownMenuItem>
          {hasMultipleAPIKeys && (
            <DropdownMenuItem
              onClick={() => {
                setCurrentRow(channel);
                setOpen('testAPIKeys');
              }}
            >
              <IconPlayerPlay size={16} className='mr-2' />
              {t('channels.actions.testAPIKeys', { count: apiKeysCount })}
            </DropdownMenuItem>
          )}
          {canWrite && (
            <DropdownMenuItem
              onClick={() => {
                setCurrentRow(channel);
                setOpen('apiKeyRules');
              }}
            >
              <IconShieldLock size={16} className='mr-2' />
              {t('channels.dialogs.apiKeyRules.action')}
            </DropdownMenuItem>
          )}
          {viewModel.menuItems.map((item) => {
            const action = toAction(item);

            switch (item.action.id) {
              case 'manageDisabledKeys':
                return (
                  <DropdownMenuItem key={item.action.id} onClick={action.onClick} className='text-orange-500!'>
                    <IconKeyOff size={16} className='mr-2' />
                    {t('channels.actions.disabledAPIKeys', { count: disabledKeysCount })}
                  </DropdownMenuItem>
                );
              case 'clearTemporaryDisable':
                return (
                  <DropdownMenuItem key={item.action.id} onClick={action.onClick} className='text-orange-500!' disabled={action.disabled}>
                    {action.pending ? <IconLoader2 size={16} className='mr-2 animate-spin' /> : <IconHistory size={16} className='mr-2' />}
                    {t('channels.actions.clearTemporaryDisable')}
                  </DropdownMenuItem>
                );
              case 'resolveError':
                return (
                  <DropdownMenuItem key={item.action.id} onClick={action.onClick} className='text-green-600!'>
                    <IconCheck size={16} className='mr-2' />
                    {t('channels.actions.markErrorResolved')}
                  </DropdownMenuItem>
                );
              default:
                return assertNever(item.action.id);
            }
          })}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('archive');
            }}
            className={isArchived ? 'text-green-600!' : 'text-orange-500!'}
          >
            {isArchived ? <IconCheck size={16} className='mr-2' /> : <IconArchive size={16} className='mr-2' />}
            {t(isArchived ? 'common.buttons.restore' : 'common.buttons.archive')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(channel);
              setOpen('delete');
            }}
            className='text-red-500!'
          >
            <IconTrash size={16} className='mr-2' />
            {t('common.buttons.delete')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
});

ActionCell.displayName = 'ActionCell';

const ExpandCell = ({ row }: { row: any }) => (
  <div className='flex justify-center'>
    <Button
      variant='ghost'
      size='sm'
      className='h-6 w-6 p-0'
      onClick={(e) => {
        e.stopPropagation();
        row.toggleExpanded();
      }}
    >
      {row.getIsExpanded() ? <IconChevronDown className='h-4 w-4' /> : <IconChevronRight className='h-4 w-4' />}
    </Button>
  </div>
);

// ExpandCell.displayName = 'ExpandCell'; // Removed since it's not memoized now, but can keep if desired

function getChannelWebsiteURL(baseURL: string): string | null {
  try {
    const url = new URL(baseURL);
    return url.origin;
  } catch {
    return null;
  }
}

function getProxyURLSummary(proxyURL: string): { label: string; detail?: string } {
  try {
    const url = new URL(proxyURL);
    const pathname = url.pathname === '/' ? '' : url.pathname;
    return {
      label: url.host || proxyURL,
      detail: `${url.protocol}//${url.host}${pathname}`,
    };
  } catch {
    return { label: proxyURL };
  }
}

function renderChannelStatusTooltipContent(
  icon: ChannelStatusIcon,
  channel: Channel,
  disabledKeysCount: number,
  t: ReturnType<typeof useTranslation>['t']
) {
  switch (icon.tooltipKind) {
    case 'error':
      return (
        <div className='space-y-1'>
          <p className='text-destructive text-sm'>
            {t(`channels.messages.${channel.errorMessage}`, {
              defaultValue: channel.errorMessage,
            })}
          </p>
        </div>
      );
    case 'temporaryDisable':
      return (
        <div className='space-y-1 text-sm text-orange-500'>
          <p>{t('channels.temporaryDisable.tooltip')}</p>
          {channel.temporaryDisabledUntil && (
            <p className='text-muted-foreground'>
              {t('channels.temporaryDisable.until', {
                time: format(new Date(channel.temporaryDisabledUntil), 'yyyy-MM-dd HH:mm:ss'),
              })}
            </p>
          )}
          {channel.temporaryDisabledErrorCode && (
            <p className='text-muted-foreground'>
              {t('channels.temporaryDisable.errorCode', { code: channel.temporaryDisabledErrorCode })}
            </p>
          )}
        </div>
      );
    case 'disabledKeys':
      return (
        <DisabledAPIKeysTooltipContent
          channel={channel}
          label={t('channels.actions.disabledAPIKeys', { count: disabledKeysCount })}
        />
      );
    case 'disabledKeysReadOnly':
      return (
        <DisabledAPIKeysTooltipContent
          channel={channel}
          label={t('channels.actions.disabledAPIKeysReadOnly', { count: disabledKeysCount })}
        />
      );
    default:
      return assertNever(icon.tooltipKind);
  }
}

function ChannelStatusIconWithTooltip({
  icon,
  channel,
  disabledKeysCount,
  t,
}: {
  icon: ChannelStatusIcon;
  channel: Channel;
  disabledKeysCount: number;
  t: ReturnType<typeof useTranslation>['t'];
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className='inline-flex shrink-0'>
          <ChannelStatusIconButton icon={icon.icon} className={icon.className} action={icon.action} />
        </span>
      </TooltipTrigger>
      <TooltipContent>{renderChannelStatusTooltipContent(icon, channel, disabledKeysCount, t)}</TooltipContent>
    </Tooltip>
  );
}

// Memoized cell components to avoid recreating on every render
const NameCell = memo(({ row, canWrite }: ChannelCellProps) => {
  const { t } = useTranslation();
  const channel = row.original;
  const channelStatusActions = useChannelStatusActions(channel, t, canWrite);
  const {
    policy: { hasError, disabledKeysCount },
  } = channelStatusActions;
  const statusIcons = channelStatusActions.statusIcons;
  const websiteURL = getChannelWebsiteURL(channel.baseURL);

  const nameElement = websiteURL ? (
    <a
      href={websiteURL}
      target='_blank'
      rel='noopener noreferrer'
      className={cn('truncate font-medium hover:underline', hasError ? 'text-destructive' : '')}
      onClick={(e) => e.stopPropagation()}
    >
      {row.getValue('name')}
    </a>
  ) : (
    <div className={cn('truncate font-medium', hasError && 'text-destructive')}>{row.getValue('name')}</div>
  );

  const content = (
    <div className='flex justify-center'>
      <div className='flex max-w-56 items-center gap-2'>
        {statusIcons.length > 0 && (
          <span className='flex shrink-0 items-center gap-1'>
            {statusIcons.map((icon) => (
              <ChannelStatusIconWithTooltip key={icon.kind} icon={icon} channel={channel} disabledKeysCount={disabledKeysCount} t={t} />
            ))}
          </span>
        )}
        {nameElement}
      </div>
    </div>
  );

  return content;
});

NameCell.displayName = 'NameCell';

const ProviderCell = memo(({ row }: { row: Row<Channel> }) => {
  const { t } = useTranslation();
  const type = row.original.type;
  const config = CHANNEL_CONFIGS[type];
  const provider = getProvider(type);
  const IconComponent = config.icon;
  return (
    <div className='flex justify-center'>
      <Badge variant='outline' className={cn('capitalize', config.color)}>
        <div className='flex items-center gap-2'>
          <IconComponent size={16} className='shrink-0' />
          <span>{t(`channels.providers.${provider}`)}</span>
        </div>
      </Badge>
    </div>
  );
});

ProviderCell.displayName = 'ProviderCell';

const TagsCell = memo(({ row }: { row: Row<Channel> }) => {
  const tags = (row.getValue('tags') as string[]) || [];
  if (tags.length === 0) {
    return (
      <div className='flex justify-center'>
        <span className='text-muted-foreground text-xs'>-</span>
      </div>
    );
  }
  return (
    <div className='flex max-w-48 flex-wrap justify-center gap-1'>
      {tags.slice(0, 2).map((tag) => (
        <Badge key={tag} variant='outline' className='text-xs'>
          {tag}
        </Badge>
      ))}
      {tags.length > 2 && (
        <Badge variant='outline' className='text-xs'>
          +{tags.length - 2}
        </Badge>
      )}
    </div>
  );
});

TagsCell.displayName = 'TagsCell';

const ProxyCell = memo(({ row }: { row: Row<Channel> }) => {
  const { t } = useTranslation();
  const proxy = row.original.settings?.proxy;

  if (!proxy || proxy.type === 'disabled') {
    return (
      <div className='flex justify-center'>
        <span className='text-muted-foreground text-xs'>-</span>
      </div>
    );
  }

  if (proxy.type === 'environment') {
    return (
      <div className='flex justify-center'>
        <span className='text-muted-foreground text-xs'>{t('channels.dialogs.proxy.types.environment')}</span>
      </div>
    );
  }

  const proxyURL = proxy.url?.trim();
  if (!proxyURL) {
    return (
      <div className='flex justify-center'>
        <span className='text-muted-foreground text-xs'>-</span>
      </div>
    );
  }

  const { label, detail } = getProxyURLSummary(proxyURL);
  const content = (
    <div className='flex justify-center'>
      <span className='max-w-40 truncate font-mono text-xs'>{label}</span>
    </div>
  );

  if (detail && detail !== label) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>{content}</TooltipTrigger>
        <TooltipContent>{detail}</TooltipContent>
      </Tooltip>
    );
  }

  return content;
});

ProxyCell.displayName = 'ProxyCell';

const SupportedModelsCell = memo(({ row }: { row: Row<Channel> }) => {
  const { t } = useTranslation();
  const channel = row.original;
  const models = row.getValue('supportedModels') as string[];
  const { setOpen, setCurrentRow } = useChannels();

  const handleOpenModelsDialog = useCallback(() => {
    setCurrentRow(channel);
    setOpen('viewModels');
  }, [channel, setCurrentRow, setOpen]);

  return (
    <div className='flex items-center justify-center gap-2'>
      <div className='flex flex-wrap justify-center gap-1 overflow-hidden'>
        {models.slice(0, 5).map((model) => (
          <Badge key={model} variant='secondary' className='block max-w-48 truncate text-left text-xs'>
            {model}
          </Badge>
        ))}
        {models.length > 5 && (
          <Badge
            variant='secondary'
            className='hover:bg-primary hover:text-primary-foreground cursor-pointer text-xs transition-colors'
            onClick={handleOpenModelsDialog}
            title={t('channels.actions.viewModels')}
          >
            +{models.length - 5}
          </Badge>
        )}
      </div>
    </div>
  );
});

SupportedModelsCell.displayName = 'SupportedModelsCell';

const OrderingWeightCell = memo(({ row, canWrite }: ChannelCellProps) => {
  const channel = row.original;
  const initialWeight = row.getValue('orderingWeight') as number | null;
  const [isEditing, setIsEditing] = useState(false);
  const [weight, setWeight] = useState<string>(initialWeight?.toString() || '1');
  const updateChannel = useUpdateChannel();
  const { channelPermissions } = usePermissions();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [isEditing]);

  const handleDoubleClick = useCallback(() => {
    if (!canWrite) return;
    setIsEditing(true);
    setWeight(initialWeight?.toString() || '1');
  }, [canWrite, initialWeight]);

  const handleSave = useCallback(async () => {
    const weightValue = clampWeight(Number(weight));
    if (weightValue === initialWeight) {
      setIsEditing(false);
      return;
    }

    try {
      await updateChannel.mutateAsync({
        id: channel.id,
        input: { orderingWeight: weightValue },
      });
      setIsEditing(false);
    } catch (_error) {
      // Error handled by mutation hook
    }
  }, [channel.id, weight, initialWeight, updateChannel]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        handleSave();
      } else if (e.key === 'Escape') {
        setIsEditing(false);
        setWeight(initialWeight?.toString() || '1');
      }
    },
    [handleSave, initialWeight]
  );

  if (isEditing && canWrite) {
    return (
      <div className='flex justify-center px-2'>
        <Input
          ref={inputRef}
          type='number'
          inputMode='decimal'
          step='any'
          min={MIN_WEIGHT}
          max={MAX_WEIGHT}
          value={weight}
          onChange={(e) => setWeight(e.target.value)}
          onBlur={handleSave}
          onKeyDown={handleKeyDown}
          className='h-7 w-20 text-center font-mono text-sm'
          disabled={updateChannel.isPending}
        />
      </div>
    );
  }

  return (
    <div className={cn('group flex items-center justify-center gap-2', canWrite && 'cursor-pointer')} onDoubleClick={handleDoubleClick}>
      <span className={cn('font-mono text-sm', initialWeight == null && 'text-muted-foreground')}>{initialWeight ?? '-'}</span>
      {updateChannel.isPending && <IconLoader2 className='text-muted-foreground h-3 w-3 animate-spin' />}
    </div>
  );
});

OrderingWeightCell.displayName = 'OrderingWeightCell';

const CreatedAtCell = memo(({ row }: { row: Row<Channel> }) => {
  const raw = row.getValue('createdAt') as unknown;
  const date = raw instanceof Date ? raw : new Date(raw as string);

  if (Number.isNaN(date.getTime())) {
    return (
      <div className='flex justify-center'>
        <span className='text-muted-foreground text-xs'>-</span>
      </div>
    );
  }

  return (
    <div className='flex justify-center'>
      <Tooltip>
        <TooltipTrigger asChild>
          <div className='text-muted-foreground cursor-help text-sm'>{format(date, 'yyyy-MM-dd')}</div>
        </TooltipTrigger>
        <TooltipContent>{format(date, 'yyyy-MM-dd HH:mm:ss')}</TooltipContent>
      </Tooltip>
    </div>
  );
});

CreatedAtCell.displayName = 'CreatedAtCell';

export const createColumns = (t: ReturnType<typeof useTranslation>['t'], canWrite: boolean): ColumnDef<Channel>[] => {
  return [
    {
      id: 'expand',
      header: () => null,
      meta: {
        className: 'w-8 min-w-8 text-center',
      },
      cell: ExpandCell,
      enableSorting: false,
      enableHiding: false,
    },
    ...(canWrite
      ? [
          {
            id: 'select',
            header: ({ table }: { table: Table<Channel> }) => (
              <div className='flex justify-center'>
                <Checkbox
                  checked={table.getIsAllPageRowsSelected() || (table.getIsSomePageRowsSelected() && 'indeterminate')}
                  onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
                  aria-label={t('common.columns.selectAll')}
                  className='translate-y-[2px]'
                />
              </div>
            ),
            cell: ({ row }: { row: Row<Channel> }) => (
              <div className='flex justify-center'>
                <Checkbox
                  checked={row.getIsSelected()}
                  onCheckedChange={(value) => row.toggleSelected(!!value)}
                  aria-label={t('common.columns.selectRow')}
                  className='translate-y-[2px]'
                />
              </div>
            ),
            meta: {
              className: 'text-center',
            },
            enableSorting: false,
            enableHiding: false,
          },
        ]
      : []),
    {
      accessorKey: 'name',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('common.columns.name')} className='justify-center' />,
      cell: ({ row }) => <NameCell row={row} canWrite={canWrite} />,
      meta: {
        className: 'md:table-cell min-w-48 text-center',
      },
      enableHiding: false,
      enableSorting: true,
    },
    {
      id: 'provider',
      accessorKey: 'type',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('channels.columns.provider')} className='justify-center' />,
      cell: ProviderCell,
      meta: {
        className: 'text-center',
      },
      filterFn: (row, _id, value) => {
        return value.includes(row.original.type);
      },
      enableSorting: true,
      enableHiding: false,
    },
    {
      accessorKey: 'status',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('common.columns.status')} className='justify-center' />,
      cell: ({ row }) => <StatusSwitchCell row={row} canWrite={canWrite} />,
      meta: {
        className: 'text-center',
      },
      enableSorting: true,
      enableHiding: false,
    },

    {
      accessorKey: 'tags',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('channels.columns.tags')} className='justify-center' />,
      cell: TagsCell,
      meta: {
        className: 'text-center',
      },
      filterFn: (row, id, value) => {
        const tags = (row.getValue(id) as string[]) || [];
        // Single select: value is a string, not an array
        return tags.includes(value as string);
      },
      enableSorting: false,
      enableHiding: true,
    },
    {
      id: 'model',
      accessorFn: () => '', // Virtual column for filtering only
      header: () => null,
      cell: () => null,
      filterFn: () => true, // Server-side filtering, always return true
      enableSorting: false,
      enableHiding: true,
      enableColumnFilter: false,
      enableGlobalFilter: false,
    },
    {
      accessorKey: 'supportedModels',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('channels.columns.supportedModels')} className='justify-center' />
      ),
      cell: SupportedModelsCell,
      meta: {
        className: 'max-w-64 text-center',
      },
      enableSorting: false,
    },
    {
      id: 'proxy',
      accessorFn: (row) => row.settings?.proxy?.url ?? row.settings?.proxy?.type ?? '',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('channels.columns.proxy')} className='justify-center' />,
      cell: ProxyCell,
      meta: {
        className: 'w-32 min-w-32 text-center',
      },
      enableSorting: false,
      enableHiding: true,
    },
    {
      id: 'health',
      accessorKey: 'health',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('channels.columns.health')} className='justify-center' />,
      cell: ({ row }: { row: Row<Channel> }) => {
        const probePoints = (row.original as any).probePoints || [];
        const limiterStats = row.original.liveLimiterStats;
        return (
          <div className='flex flex-col items-center gap-1'>
            <ChannelHealthCell points={probePoints} />
            {limiterStats ? <ChannelLimiterCell stats={limiterStats} /> : null}
          </div>
        );
      },
      meta: {
        className: 'text-center',
      },
      enableSorting: false,
      enableHiding: true,
    },
    {
      accessorKey: 'orderingWeight',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('channels.columns.orderingWeight')} className='justify-center' />
      ),
      cell: ({ row }) => <OrderingWeightCell row={row} canWrite={canWrite} />,
      meta: {
        className: 'w-20 min-w-20 text-center',
      },
      sortingFn: 'alphanumeric',
      enableSorting: true,
      enableHiding: true,
    },
    {
      accessorKey: 'createdAt',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('common.columns.createdAt')} className='justify-center' />,
      cell: CreatedAtCell,
      meta: {
        className: 'text-center',
      },
      enableSorting: true,
      enableHiding: false,
    },
    ...(canWrite
      ? [
          {
            id: 'action',
            header: ({ column }: { column: any }) => (
              <DataTableColumnHeader column={column} title={t('common.columns.actions')} className='justify-center' />
            ),
            cell: ({ row }: { row: Row<Channel> }) => <ActionCell row={row} canWrite={canWrite} />,
            meta: {
              className: 'text-center',
            },
            enableSorting: false,
            enableHiding: false,
          },
        ]
      : []),
  ];
};
