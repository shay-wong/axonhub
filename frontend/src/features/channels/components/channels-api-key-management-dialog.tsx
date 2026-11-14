'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { format } from 'date-fns';
import { useQueryClient } from '@tanstack/react-query';
import { IconAlertTriangle, IconCopy, IconKey, IconLoader2, IconPlayerPlay, IconRefresh, IconTrash, IconUpload } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { copyTextToClipboard } from '@/lib/clipboard';
import useInterval from '@/hooks/useInterval';
import { usePermissions } from '@/hooks/usePermissions';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useChannels } from '../context/channels-context';
import { createAPIKeyNameMap, formatAPIKeyIdentity, maskAPIKeySuffix } from '../data/api-key-display';
import { getManagedAPIKeyRows, getManagementConfiguredKeys, hasOAuthCredentials } from '../data/api-key-management';
import { buildAPIKeyCredentialsForUpdate } from '../data/channel-input';
import {
  fetchLatestChannel,
  useChannelDisabledAPIKeys,
  useDeleteDisabledChannelAPIKeys,
  useDisableChannelAPIKey,
  useEnableChannelAPIKey,
  useEnableAllChannelAPIKeys,
  useEnableSelectedChannelAPIKeys,
  useTestChannelAPIKey,
  useUpdateChannel,
} from '../data/channels';
import { formatDisabledAPIKeyCountdown } from '../data/disabled-api-key-status';
import { OAUTH_CREDENTIAL_REF, type ChannelCredentials, type TestAPIKeyResult } from '../data/schema';

interface ChannelsAPIKeyManagementDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  loading?: boolean;
  loadError?: boolean;
  onRetry?: () => void;
}

/**
 * 统一密钥管理弹窗：
 * - 始终可从行操作菜单进入（无论 1 个还是多个 key）
 * - 批量导入（多行文本，一行一个）
 * - 单个/批量：测试、复制、禁用/启用、删除
 * - 密钥状态（正常 / 已禁用 / 测试中 / 测试结果）实时动态更新
 */
export function ChannelsAPIKeyManagementDialog({
  open,
  onOpenChange,
  loading = false,
  loadError = false,
  onRetry,
}: ChannelsAPIKeyManagementDialogProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { channelPermissions } = usePermissions();
  const canWrite = channelPermissions.canWrite;
  const { currentRow, setOpen } = useChannels();
  const [bulkImportText, setBulkImportText] = useState('');
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set());
  const [testingKey, setTestingKey] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, TestAPIKeyResult>>({});
  const [confirmDisableKey, setConfirmDisableKey] = useState<string | null>(null);
  const [confirmKeepDisabledKey, setConfirmKeepDisabledKey] = useState<string | null>(null);
  const [confirmEnableKey, setConfirmEnableKey] = useState<string | null>(null);
  const [confirmDeleteKey, setConfirmDeleteKey] = useState<string | null>(null);
  const [confirmDeleteSelected, setConfirmDeleteSelected] = useState(false);
  const [confirmEnableSelected, setConfirmEnableSelected] = useState(false);
  const [confirmEnableAll, setConfirmEnableAll] = useState(false);
  const [nowMs, setNowMs] = useState(Date.now);
  const [reconciling, setReconciling] = useState(false);
  const [reconcileError, setReconcileError] = useState(false);
  const initializedChannel = useRef<string | null>(null);
  // 本弹窗独立的 key 列表：基于 currentRow 快照初始化，导入/删除后本地更新，
  // 避免依赖不会随 query 刷新而更新的 currentRow 快照。
  const [localKeys, setLocalKeys] = useState<string[]>([]);
  // Vertex 密钥是项目绑定的三元组：列表里展示 apiKey，但每次提交都必须把绑定原样带回，
  // 否则后端整体替换 credentials 时会丢掉 projectID/region。
  const [vertexBindings, setVertexBindings] = useState<NonNullable<ChannelCredentials['vertexApiKeys']>>([]);
  const abortRef = useRef(false);

  const testSingleKey = useTestChannelAPIKey();
  const disableAPIKey = useDisableChannelAPIKey();
  const enableAPIKey = useEnableChannelAPIKey();
  const enableAllAPIKeys = useEnableAllChannelAPIKeys();
  const enableSelectedAPIKeys = useEnableSelectedChannelAPIKeys();
  const updateChannel = useUpdateChannel();
  const deleteDisabledAPIKeys = useDeleteDisabledChannelAPIKeys();

  const validKeys = useMemo(() => [...new Set(localKeys.filter((key) => key.trim().length > 0))], [localKeys]);
  const apiKeyNames = useMemo(() => createAPIKeyNameMap(currentRow?.credentials?.apiKeyConfigs), [currentRow?.credentials?.apiKeyConfigs]);
  // 服务端实时禁用的 key 集合（所有 disable/enable mutation 都会 invalidate 此 query）
  const {
    data: disabledKeys = [],
    isLoading: disabledLoading,
    isError: disabledError,
    refetch: refetchDisabledKeys,
  } = useChannelDisabledAPIKeys(currentRow?.id || '', {
    enabled: canWrite && open && !!currentRow?.id,
  });
  const rows = useMemo(() => getManagedAPIKeyRows(validKeys, disabledKeys, nowMs), [validKeys, disabledKeys, nowMs]);
  const effectivelyDisabledSet = useMemo(() => new Set(rows.filter((row) => row.disabled).map((row) => row.key)), [rows]);
  const selectedDisabledKeys = [...selectedKeys].filter((key) => effectivelyDisabledSet.has(key));
  const hasTemporaryDisabledKeys = rows.some((row) => row.disabled?.disableStatus.kind === 'temporary');
  const visibleKeySignature = rows.map((row) => row.key).join('\0');
  useInterval(
    () => {
      const now = Date.now();
      const expired = rows.some(
        (row) => row.disabled?.disableStatus.kind === 'temporary' && row.disabled.disableStatus.disabledUntilMs <= now
      );
      setNowMs(now);
      if (expired) {
        void queryClient.invalidateQueries({ queryKey: ['channels'] });
        void queryClient.invalidateQueries({ queryKey: ['channelDisabledAPIKeys', currentRow?.id] });
      }
    },
    open && hasTemporaryDisabledKeys ? 1000 : null
  );
  useEffect(() => {
    const visible = new Set(visibleKeySignature.split('\0'));
    setSelectedKeys((previous) => {
      const next = new Set([...previous].filter((key) => visible.has(key)));
      return next.size === previous.size ? previous : next;
    });
    setConfirmEnableKey((key) => (key && visible.has(key) ? key : null));
    setConfirmDeleteKey((key) => (key && visible.has(key) ? key : null));
  }, [visibleKeySignature]);

  // 当前可用的 key 数量（未被禁用的），与编辑弹窗的 mustKeepOneEnabled 保护一致
  const enabledKeysCount = useMemo(
    () => validKeys.filter((key) => !effectivelyDisabledSet.has(key)).length,
    [validKeys, effectivelyDisabledSet]
  );
  const isLastKey = validKeys.length <= 1;

  const isPending =
    !canWrite ||
    reconciling ||
    reconcileError ||
    loading ||
    loadError ||
    disabledLoading ||
    disabledError ||
    testingKey !== null ||
    disableAPIKey.isPending ||
    enableAPIKey.isPending ||
    enableAllAPIKeys.isPending ||
    enableSelectedAPIKeys.isPending ||
    updateChannel.isPending ||
    deleteDisabledAPIKeys.isPending;

  useEffect(() => {
    if (!open) {
      initializedChannel.current = null;
      abortRef.current = true;
    } else if (!loading && !loadError && currentRow && initializedChannel.current !== currentRow.id) {
      initializedChannel.current = currentRow.id;
      setNowMs(Date.now());
      setBulkImportText('');
      setLocalKeys(getManagementConfiguredKeys(currentRow.credentials));
      setVertexBindings(currentRow?.credentials?.vertexApiKeys ?? []);
      setSelectedKeys(new Set());
      setTestingKey(null);
      setTestResults({});
      setConfirmDisableKey(null);
      setConfirmKeepDisabledKey(null);
      setConfirmEnableKey(null);
      setConfirmDeleteKey(null);
      setConfirmDeleteSelected(false);
      setConfirmEnableAll(false);
      setConfirmEnableSelected(false);
      setReconciling(false);
      setReconcileError(false);
      abortRef.current = false;
    }
    // 仅在弹窗打开或切换渠道时初始化一次 localKeys；
    // 依赖 currentRow.credentials 会导致导入/删除后的本地更新被覆盖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentRow, loading, loadError]);

  useEffect(() => {
    abortRef.current = !open;
    return () => {
      abortRef.current = true;
    };
  }, [open]);

  if (!currentRow || !canWrite) {
    return null;
  }
  const isVertexChannel = currentRow.type === 'gemini_vertex';
  const isOAuthCredential = hasOAuthCredentials(currentRow.credentials) || effectivelyDisabledSet.has(OAUTH_CREDENTIAL_REF);

  // Vertex 密钥只能作为三元组提交：平铺进 apiKeys 会变成没有项目绑定的密钥，
  // 所以列表里的 Vertex 密钥要从 apiKeys 里排除，绑定则按存活的 key 过滤后原样带回。
  const buildCredentials = (keys: string[]) => {
    if (!isVertexChannel) {
      return buildAPIKeyCredentialsForUpdate(keys, currentRow.credentials);
    }
    const boundKeys = new Set(vertexBindings.map((binding) => binding.apiKey));
    const keptKeys = new Set(keys);
    return {
      ...buildAPIKeyCredentialsForUpdate(keys, currentRow.credentials),
      apiKeys: keys.filter((key) => !boundKeys.has(key)),
      vertexApiKeys: vertexBindings.filter((binding) => keptKeys.has(binding.apiKey)),
    };
  };

  const handleClose = () => {
    abortRef.current = true;
    setOpen(null);
    onOpenChange(false);
    setBulkImportText('');
    setSelectedKeys(new Set());
    setTestingKey(null);
    setTestResults({});
    setConfirmDisableKey(null);
    setConfirmKeepDisabledKey(null);
    setConfirmEnableKey(null);
    setConfirmDeleteKey(null);
    setConfirmDeleteSelected(false);
  };

  /** 批量导入：多行文本，一行一个 key，去重、去空白，追加到渠道密钥列表 */
  const handleBulkImport = async () => {
    if (isPending || isVertexChannel || isOAuthCredential) {
      return;
    }

    const newKeys = bulkImportText
      .split('\n')
      .map((k) => k.trim())
      .filter((k) => k.length > 0);
    if (newKeys.length === 0) {
      return;
    }

    const existing = new Set(validKeys);
    // 先对输入自身去重（同一 key 粘贴多行时只保留一个）
    const deduped = [...new Set(newKeys)].filter((k) => !existing.has(k));
    if (deduped.length === 0) {
      setBulkImportText('');
      return;
    }

    try {
      await updateChannel.mutateAsync({
        id: currentRow.id,
        input: {
          credentials: buildCredentials([...validKeys, ...deduped]),
        },
      });
      // 本地同步更新，避免依赖不会刷新的 currentRow 快照
      setLocalKeys((prev) => [...prev, ...deduped]);
      setBulkImportText('');
    } catch {
      // handled by hook
    }
  };

  const reconcileCredentials = async () => {
    setReconciling(true);
    try {
      const latest = await fetchLatestChannel(currentRow.id);
      if (!abortRef.current) {
        setLocalKeys(getManagementConfiguredKeys(latest.credentials));
        setVertexBindings(latest.credentials?.vertexApiKeys ?? []);
        setReconcileError(false);
        queryClient.setQueryData(['channel', currentRow.id], latest);
      }
    } catch {
      if (!abortRef.current) setReconcileError(true);
    } finally {
      if (!abortRef.current) setReconciling(false);
    }
  };

  /** 删除 key（活动 key 从 credentials 移除；已禁用 key 走 deleteDisabledAPIKeys） */
  const deleteKeys = async (keysToDelete: string[]) => {
    if (
      isPending ||
      isOAuthCredential ||
      keysToDelete.length === 0 ||
      keysToDelete.includes(OAUTH_CREDENTIAL_REF) ||
      (validKeys.length > 0 && validKeys.every((key) => keysToDelete.includes(key)))
    ) {
      return;
    }

    const disabled = keysToDelete.filter((key) => effectivelyDisabledSet.has(key));
    const active = keysToDelete.filter((key) => !effectivelyDisabledSet.has(key));

    if (disabled.length > 0) {
      const result = await deleteDisabledAPIKeys.mutateAsync({ channelID: currentRow.id, keys: disabled });
      if (result.message === 'ONE_KEY_PRESERVED') {
        await reconcileCredentials();
        return;
      }
      // 这一步已在服务端提交；后续活动 key 删除失败也不能把它们重新写回。
      setLocalKeys((previous) => previous.filter((key) => !disabled.includes(key)));
      setVertexBindings((previous) => previous.filter((binding) => !disabled.includes(binding.apiKey)));
    }
    // 注意：remainingKeys 必须同时过滤 active 与 disabled，
    // 避免把上面 deleteDisabledAPIKeys 已从 credentials 移除的 disabled key 写回去。
    const removed = new Set([...active, ...disabled]);
    const remainingKeys = validKeys.filter((key) => !removed.has(key));
    if (active.length > 0) {
      await updateChannel.mutateAsync({
        id: currentRow.id,
        input: { credentials: buildCredentials(remainingKeys) },
      });
    }
    // 本地同步更新（disabled 删除走 deleteDisabledAPIKeys 不触发 updateChannel，
    // 但同样要从弹窗列表里移除）
    setLocalKeys(remainingKeys);
    setVertexBindings((prev) => prev.filter((binding) => !removed.has(binding.apiKey)));
  };

  const handleDeleteSingle = async (key: string) => {
    try {
      await deleteKeys([key]);
      setConfirmDeleteKey(null);
    } catch {
      // handled by hooks
    }
  };

  const handleDeleteSelected = async () => {
    if (
      selectedKeys.size === 0 ||
      selectedKeys.has(OAUTH_CREDENTIAL_REF) ||
      (validKeys.length > 0 && validKeys.every((key) => selectedKeys.has(key)))
    ) {
      return;
    }
    try {
      await deleteKeys(Array.from(selectedKeys));
      setConfirmDeleteSelected(false);
      setSelectedKeys(new Set());
    } catch {
      // handled by hooks
    }
  };

  const handleTestSingle = async (key: string) => {
    if (isPending || key === OAUTH_CREDENTIAL_REF) {
      return;
    }
    setTestingKey(key);
    try {
      const result = await testSingleKey.mutateAsync({
        channelID: currentRow.id,
        key,
        modelID: currentRow.defaultTestModel || undefined,
      });
      if (!abortRef.current) {
        setTestResults((prev) => ({ ...prev, [key]: result }));
      }
    } catch {
      if (!abortRef.current) {
        setTestResults((prev) => ({
          ...prev,
          [key]: {
            keyPrefix: maskAPIKeySuffix(key),
            success: false,
            latency: 0,
            error: t('channels.dialogs.testAPIKeys.requestFailed'),
            disabled: false,
          },
        }));
      }
    } finally {
      if (!abortRef.current) {
        void refetchDisabledKeys();
        setTestingKey(null);
      }
    }
  };

  const handleTestSelected = async () => {
    const keysToTest = validKeys.filter((key) => selectedKeys.has(key));
    if (keysToTest.length === 0 || isPending) {
      return;
    }

    abortRef.current = false;
    for (const key of keysToTest) {
      if (abortRef.current) {
        break;
      }
      setTestingKey(key);
      try {
        const result = await testSingleKey.mutateAsync({
          channelID: currentRow.id,
          key,
          modelID: currentRow.defaultTestModel || undefined,
        });
        if (!abortRef.current) {
          setTestResults((prev) => ({ ...prev, [key]: result }));
        }
      } catch {
        if (!abortRef.current) {
          setTestResults((prev) => ({
            ...prev,
            [key]: {
              keyPrefix: maskAPIKeySuffix(key),
              success: false,
              latency: 0,
              error: t('channels.dialogs.testAPIKeys.requestFailed'),
              disabled: false,
            },
          }));
        }
      }
      if (!abortRef.current) {
        setTestingKey(null);
      }
    }
    if (!abortRef.current) {
      void refetchDisabledKeys();
      setTestingKey(null);
    }
  };

  const handleToggleDisable = async (key: string) => {
    if (isPending || key === OAUTH_CREDENTIAL_REF || enabledKeysCount <= 1) return;
    try {
      await disableAPIKey.mutateAsync({ channelID: currentRow.id, key });
      setConfirmDisableKey(null);
      setConfirmKeepDisabledKey(null);
    } catch {
      // handled by hooks
    }
  };

  const handleToggleEnable = async (key: string) => {
    if (isPending || !effectivelyDisabledSet.has(key)) return;
    try {
      await enableAPIKey.mutateAsync({ channelID: currentRow.id, key });
      setConfirmEnableKey(null);
    } catch {
      // handled by hooks
    }
  };

  const handleEnableBatch = async (all: boolean) => {
    if (isPending) return;
    try {
      if (all) await enableAllAPIKeys.mutateAsync({ channelID: currentRow.id });
      else if (selectedDisabledKeys.length)
        await enableSelectedAPIKeys.mutateAsync({ channelID: currentRow.id, keys: selectedDisabledKeys });
      setConfirmEnableAll(false);
      setConfirmEnableSelected(false);
      setSelectedKeys(new Set());
    } catch {
      // handled by hooks
    }
  };

  const handleSelectAll = () => {
    if (selectedKeys.size === rows.length) {
      setSelectedKeys(new Set());
    } else {
      setSelectedKeys(new Set(rows.map((row) => row.key)));
    }
  };

  const isAllSelected = rows.length > 0 && selectedKeys.size === rows.length;
  const isSomeSelected = selectedKeys.size > 0 && selectedKeys.size < rows.length;

  const getKeyResult = (key: string): TestAPIKeyResult | undefined => testResults[key];

  return (
    <Dialog open={open} onOpenChange={(isOpen) => (isOpen ? onOpenChange(true) : handleClose())}>
      <DialogContent className='flex max-h-[90vh] flex-col overflow-hidden sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <IconKey className='h-5 w-5' />
            {t('channels.dialogs.keyManagement.title')}
          </DialogTitle>
          <DialogDescription>{t('channels.dialogs.keyManagement.description', { name: currentRow.name })}</DialogDescription>
        </DialogHeader>

        {loadError ? (
          <div role='alert' className='flex flex-col items-center gap-3 py-8'>
            <p>{t('common.errors.loadFailed')}</p>
            <Button variant='outline' onClick={onRetry}>
              {t('common.buttons.retry')}
            </Button>
          </div>
        ) : loading || initializedChannel.current !== currentRow.id ? (
          <div role='status' className='flex items-center justify-center gap-2 py-8'>
            <IconLoader2 className='h-5 w-5 animate-spin' />
            {t('common.loading')}
          </div>
        ) : (
          <div className='flex min-h-0 flex-1 flex-col gap-4'>
            {reconcileError && (
              <div role='alert' className='flex items-center justify-between gap-3 text-sm'>
                <span>{t('common.errors.loadFailed')}</span>
                <Button variant='outline' size='sm' disabled={reconciling} onClick={() => void reconcileCredentials()}>
                  {t('common.buttons.retry')}
                </Button>
              </div>
            )}
            {disabledError && (
              <div role='alert' className='flex items-center justify-between gap-3 text-sm'>
                <span>{t('common.errors.loadFailed')}</span>
                <Button variant='outline' size='sm' onClick={() => void refetchDisabledKeys()}>
                  {t('common.buttons.retry')}
                </Button>
              </div>
            )}
            {/* 批量导入区 */}
            {!isOAuthCredential && (
              <div className='bg-muted/40 rounded-md border p-3'>
                <div className='flex items-center justify-between gap-2'>
                  <div className='flex items-center gap-2 text-sm font-medium'>
                    <IconUpload className='h-4 w-4' />
                    {t('channels.dialogs.keyManagement.importTitle')}
                  </div>
                  <span className='text-muted-foreground text-xs'>{t('channels.dialogs.keyManagement.importHint')}</span>
                </div>
                <div className='mt-2 flex items-start gap-2'>
                  <Textarea
                    value={bulkImportText}
                    onChange={(e) => setBulkImportText(e.target.value)}
                    placeholder={t('channels.dialogs.keyManagement.importPlaceholder')}
                    className='min-h-[64px] flex-1 font-mono text-xs'
                    disabled={isVertexChannel || isPending}
                  />
                  <Button onClick={handleBulkImport} disabled={isVertexChannel || isPending || bulkImportText.trim().length === 0}>
                    {updateChannel.isPending ? (
                      <IconLoader2 className='mr-2 h-4 w-4 animate-spin' />
                    ) : (
                      <IconUpload className='mr-2 h-4 w-4' />
                    )}
                    {t('channels.dialogs.keyManagement.importButton')}
                  </Button>
                </div>
              </div>
            )}

            {/* 表格 */}
            <div className='flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border'>
              {/* 批量操作头 */}
              <div className='bg-muted/40 flex flex-wrap items-center justify-between gap-4 rounded-t-lg border-b px-4 py-2'>
                <div className='flex items-center gap-2'>
                  <Checkbox
                    checked={isAllSelected || (isSomeSelected && 'indeterminate')}
                    onCheckedChange={handleSelectAll}
                    aria-label={t('common.columns.selectAll')}
                  />
                  <span className='text-muted-foreground text-sm'>
                    {selectedKeys.size > 0
                      ? t('channels.dialogs.keyManagement.selectedCount', { count: selectedKeys.size })
                      : t('channels.dialogs.keyManagement.totalCount', { count: rows.length })}
                  </span>
                </div>
                <div className='flex flex-wrap items-center gap-1.5'>
                  {effectivelyDisabledSet.size > 0 && (
                    <>
                      <Popover open={confirmEnableSelected} onOpenChange={setConfirmEnableSelected}>
                        <PopoverTrigger asChild>
                          <Button size='sm' variant='outline' disabled={isPending || selectedDisabledKeys.length === 0}>
                            <IconRefresh className='mr-1 h-3.5 w-3.5' />
                            {t('channels.dialogs.disabledAPIKeys.enableSelected', { count: selectedDisabledKeys.length })}
                          </Button>
                        </PopoverTrigger>
                        <PopoverContent className='w-80'>
                          <div className='flex flex-col gap-3'>
                            <p className='text-sm'>
                              {t('channels.dialogs.disabledAPIKeys.confirmEnableSelected', { count: selectedDisabledKeys.length })}
                            </p>
                            <div className='max-h-32 space-y-1 overflow-y-auto'>
                              {selectedDisabledKeys.map((key) => (
                                <code key={key} className='bg-muted block truncate rounded px-2 py-1 text-xs'>
                                  {key === OAUTH_CREDENTIAL_REF
                                    ? t('channels.dialogs.disabledAPIKeys.oauthCredential')
                                    : formatAPIKeyIdentity(key, apiKeyNames.get(key.trim()))}
                                </code>
                              ))}
                            </div>
                            <div className='flex justify-end gap-2'>
                              <Button size='sm' variant='outline' onClick={() => setConfirmEnableSelected(false)}>
                                {t('common.buttons.cancel')}
                              </Button>
                              <Button
                                size='sm'
                                onClick={() => handleEnableBatch(false)}
                                disabled={isPending || selectedDisabledKeys.length === 0}
                              >
                                {t('common.buttons.confirm')}
                              </Button>
                            </div>
                          </div>
                        </PopoverContent>
                      </Popover>
                      <Popover open={confirmEnableAll} onOpenChange={setConfirmEnableAll}>
                        <PopoverTrigger asChild>
                          <Button size='sm' variant='outline' disabled={isPending}>
                            {t('channels.dialogs.disabledAPIKeys.enableAll')}
                          </Button>
                        </PopoverTrigger>
                        <PopoverContent className='w-80'>
                          <div className='flex flex-col gap-3'>
                            <p className='text-sm'>
                              {t('channels.dialogs.disabledAPIKeys.confirmEnableAll', { count: effectivelyDisabledSet.size })}
                            </p>
                            <div className='flex justify-end gap-2'>
                              <Button size='sm' variant='outline' onClick={() => setConfirmEnableAll(false)}>
                                {t('common.buttons.cancel')}
                              </Button>
                              <Button size='sm' onClick={() => handleEnableBatch(true)} disabled={isPending}>
                                {t('common.buttons.confirm')}
                              </Button>
                            </div>
                          </div>
                        </PopoverContent>
                      </Popover>
                    </>
                  )}
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={handleTestSelected}
                    disabled={isPending || !validKeys.some((key) => selectedKeys.has(key))}
                  >
                    <IconPlayerPlay className='mr-1 h-3.5 w-3.5' />
                    {t('channels.dialogs.keyManagement.testSelected', { count: selectedKeys.size })}
                  </Button>
                  <Popover open={confirmDeleteSelected} onOpenChange={setConfirmDeleteSelected}>
                    <PopoverTrigger asChild>
                      <Button
                        size='sm'
                        variant='outline'
                        className='text-destructive'
                        disabled={
                          isPending ||
                          selectedKeys.size === 0 ||
                          selectedKeys.has(OAUTH_CREDENTIAL_REF) ||
                          (validKeys.length > 0 && validKeys.every((key) => selectedKeys.has(key)))
                        }
                      >
                        <IconTrash className='mr-1 h-3.5 w-3.5' />
                        {t('channels.dialogs.keyManagement.deleteSelected', { count: selectedKeys.size })}
                      </Button>
                    </PopoverTrigger>
                    <PopoverContent className='w-80'>
                      <div className='flex flex-col gap-3'>
                        <p className='text-sm'>{t('channels.dialogs.keyManagement.confirmDeleteSelected', { count: selectedKeys.size })}</p>
                        <div className='flex justify-end gap-2'>
                          <Button size='sm' variant='outline' onClick={() => setConfirmDeleteSelected(false)}>
                            {t('common.buttons.cancel')}
                          </Button>
                          <Button size='sm' variant='destructive' onClick={handleDeleteSelected} disabled={isPending}>
                            {t('common.buttons.confirm')}
                          </Button>
                        </div>
                      </div>
                    </PopoverContent>
                  </Popover>
                </div>
              </div>

              <ScrollArea className='min-h-0 flex-1'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className='w-12'></TableHead>
                      <TableHead>{t('channels.dialogs.keyManagement.keyColumn')}</TableHead>
                      <TableHead className='w-40'>{t('channels.dialogs.keyManagement.statusColumn')}</TableHead>
                      <TableHead className='w-28'>{t('channels.dialogs.keyManagement.latencyColumn')}</TableHead>
                      <TableHead className='w-36'>{t('channels.dialogs.keyManagement.actionsColumn')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {disabledLoading && rows.length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={5} className='h-24 text-center'>
                          <IconLoader2 className='text-muted-foreground mx-auto h-5 w-5 animate-spin' />
                        </TableCell>
                      </TableRow>
                    ) : rows.length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={5} className='text-muted-foreground h-32 text-center text-sm'>
                          {t('channels.dialogs.keyManagement.noKeys')}
                        </TableCell>
                      </TableRow>
                    ) : (
                      rows.map(({ key, disabled: dk }) => {
                        const isTesting = testingKey === key;
                        const isDisabled = Boolean(dk);
                        const isQuotaDisabled = dk?.quotaDisabled === true;
                        const isOAuth = key === OAUTH_CREDENTIAL_REF;
                        const result = getKeyResult(key);
                        const handleCopy = async () => {
                          if (!canWrite || isOAuth) return;
                          try {
                            await copyTextToClipboard(key);
                            toast.success(t('channels.dialogs.keyManagement.copySuccess'));
                          } catch {
                            toast.error(t('common.errors.copyFailed'));
                          }
                        };
                        return (
                          <TableRow key={key}>
                            <TableCell>
                              <Checkbox
                                checked={selectedKeys.has(key)}
                                onCheckedChange={(checked) => {
                                  setSelectedKeys((prev) => {
                                    const next = new Set(prev);
                                    if (checked) {
                                      next.add(key);
                                    } else {
                                      next.delete(key);
                                    }
                                    return next;
                                  });
                                }}
                              />
                            </TableCell>
                            <TableCell className='font-medium'>
                              <div className='flex items-center gap-2'>
                                {isTesting && <IconLoader2 className='text-muted-foreground h-3 w-3 animate-spin' />}
                                <code className='bg-muted rounded px-2 py-0.5 font-mono text-sm'>
                                  {isOAuth
                                    ? t('channels.dialogs.disabledAPIKeys.oauthCredential')
                                    : formatAPIKeyIdentity(key, apiKeyNames.get(key.trim()))}
                                </code>
                              </div>
                              {dk && (
                                <div className='text-muted-foreground mt-2 space-y-1 text-xs'>
                                  <div className='flex flex-wrap gap-x-2'>
                                    {!isQuotaDisabled && (
                                      <span>
                                        {t(
                                          dk.disableStatus.kind === 'temporary'
                                            ? 'channels.dialogs.disabledAPIKeys.temporaryErrorCodeTooltip'
                                            : 'channels.dialogs.disabledAPIKeys.permanentErrorCodeTooltip',
                                          { code: dk.errorCode }
                                        )}
                                      </span>
                                    )}
                                    <span>
                                      {t('channels.dialogs.disabledAPIKeys.disabledAt', {
                                        time: format(new Date(dk.disabledAt), 'yyyy-MM-dd HH:mm:ss'),
                                      })}
                                    </span>
                                  </div>
                                  {dk.disableStatus.kind === 'temporary' && (
                                    <div className='flex flex-wrap gap-x-2'>
                                      <span>
                                        {t('channels.dialogs.disabledAPIKeys.remaining', {
                                          time: formatDisabledAPIKeyCountdown(dk.disableStatus.remainingSeconds),
                                        })}
                                      </span>
                                      <span>
                                        {t('channels.dialogs.disabledAPIKeys.recoversAt', {
                                          time: format(new Date(dk.disableStatus.disabledUntilMs), 'yyyy-MM-dd HH:mm:ss'),
                                        })}
                                      </span>
                                    </div>
                                  )}
                                  {dk.reason && (
                                    <div className='flex min-w-0 items-start gap-1'>
                                      <span className='shrink-0 font-medium'>{t('channels.dialogs.disabledAPIKeys.reason')}:</span>
                                      <span className='min-w-0 break-words whitespace-pre-wrap'>{dk.reason}</span>
                                    </div>
                                  )}
                                </div>
                              )}
                              {isQuotaDisabled && (
                                <div className='text-muted-foreground mt-1 text-xs'>
                                  {t('channels.dialogs.keyManagement.quotaDisabled')}
                                </div>
                              )}
                              {result?.error && (
                                <div className='text-destructive mt-1 flex items-start gap-1 text-xs'>
                                  <IconAlertTriangle className='mt-0.5 h-3 w-3 shrink-0' />
                                  <span className='break-all whitespace-normal'>{result.error}</span>
                                </div>
                              )}
                            </TableCell>
                            <TableCell>
                              {isDisabled ? (
                                <Badge variant='secondary'>
                                  {t(
                                    !isQuotaDisabled && dk?.disableStatus.kind === 'permanent'
                                      ? 'channels.dialogs.disabledAPIKeys.permanent'
                                      : 'channels.dialogs.keyManagement.disabled'
                                  )}
                                </Badge>
                              ) : result ? (
                                result.success ? (
                                  <Badge variant='default' className='border-green-200 bg-green-100 text-green-800'>
                                    {t('channels.dialogs.keyManagement.success')}
                                  </Badge>
                                ) : (
                                  <Badge variant='destructive'>{t('channels.dialogs.keyManagement.failed')}</Badge>
                                )
                              ) : (
                                <span className='text-muted-foreground'>-</span>
                              )}
                            </TableCell>
                            <TableCell>{result && result.latency > 0 ? `${result.latency.toFixed(2)}s` : '-'}</TableCell>
                            <TableCell>
                              <div className='flex items-center gap-0.5'>
                                <Tooltip>
                                  <TooltipTrigger asChild>
                                    <Button
                                      size='sm'
                                      variant='ghost'
                                      className='h-7 w-7 p-0'
                                      onClick={() => handleTestSingle(key)}
                                      disabled={isPending || isOAuth}
                                    >
                                      <IconPlayerPlay className='h-4 w-4' />
                                    </Button>
                                  </TooltipTrigger>
                                  <TooltipContent>{t('channels.dialogs.keyManagement.test')}</TooltipContent>
                                </Tooltip>
                                <Tooltip>
                                  <TooltipTrigger asChild>
                                    <Button size='sm' variant='ghost' className='h-7 w-7 p-0' onClick={handleCopy} disabled={isOAuth}>
                                      <IconCopy className='h-4 w-4' />
                                    </Button>
                                  </TooltipTrigger>
                                  <TooltipContent>{t('channels.dialogs.keyManagement.copy')}</TooltipContent>
                                </Tooltip>

                                {isDisabled ? (
                                  <>
                                    <Popover open={confirmEnableKey === key} onOpenChange={(o) => setConfirmEnableKey(o ? key : null)}>
                                      <PopoverTrigger asChild>
                                        <Button size='sm' variant='ghost' className='h-7 w-7 p-0' disabled={isPending}>
                                          <IconRefresh className='h-4 w-4' />
                                        </Button>
                                      </PopoverTrigger>
                                      <PopoverContent className='w-64'>
                                        <div className='flex flex-col gap-3'>
                                          <p className='text-sm'>{t('channels.dialogs.keyManagement.confirmEnable')}</p>
                                          <div className='flex justify-end gap-2'>
                                            <Button size='sm' variant='outline' onClick={() => setConfirmEnableKey(null)}>
                                              {t('common.buttons.cancel')}
                                            </Button>
                                            <Button size='sm' onClick={() => handleToggleEnable(key)} disabled={isPending}>
                                              {t('common.buttons.confirm')}
                                            </Button>
                                          </div>
                                        </div>
                                      </PopoverContent>
                                    </Popover>
                                    {isQuotaDisabled && (
                                      <Popover
                                        open={confirmKeepDisabledKey === key}
                                        onOpenChange={(o) => setConfirmKeepDisabledKey(o ? key : null)}
                                      >
                                        <PopoverTrigger asChild>
                                          <Button size='sm' variant='outline' className='h-7 px-2' disabled={isPending}>
                                            {t('channels.dialogs.keyManagement.keepDisabled')}
                                          </Button>
                                        </PopoverTrigger>
                                        <PopoverContent className='w-72'>
                                          <div className='flex flex-col gap-3'>
                                            <p className='text-sm'>{t('channels.dialogs.keyManagement.confirmKeepDisabled')}</p>
                                            <div className='flex justify-end gap-2'>
                                              <Button size='sm' variant='outline' onClick={() => setConfirmKeepDisabledKey(null)}>
                                                {t('common.buttons.cancel')}
                                              </Button>
                                              <Button size='sm' onClick={() => handleToggleDisable(key)} disabled={isPending}>
                                                {t('common.buttons.confirm')}
                                              </Button>
                                            </div>
                                          </div>
                                        </PopoverContent>
                                      </Popover>
                                    )}
                                  </>
                                ) : (
                                  <Popover open={confirmDisableKey === key} onOpenChange={(o) => setConfirmDisableKey(o ? key : null)}>
                                    <PopoverTrigger asChild>
                                      <Button
                                        size='sm'
                                        variant='ghost'
                                        className='h-7 w-7 p-0'
                                        disabled={isPending || isOAuth || enabledKeysCount <= 1}
                                      >
                                        <IconAlertTriangle className='h-4 w-4' />
                                      </Button>
                                    </PopoverTrigger>
                                    <PopoverContent className='w-64'>
                                      <div className='flex flex-col gap-3'>
                                        <p className='text-sm'>{t('channels.dialogs.keyManagement.confirmDisable')}</p>
                                        <div className='flex justify-end gap-2'>
                                          <Button size='sm' variant='outline' onClick={() => setConfirmDisableKey(null)}>
                                            {t('common.buttons.cancel')}
                                          </Button>
                                          <Button size='sm' onClick={() => handleToggleDisable(key)} disabled={isPending}>
                                            {t('common.buttons.confirm')}
                                          </Button>
                                        </div>
                                      </div>
                                    </PopoverContent>
                                  </Popover>
                                )}

                                <Popover open={confirmDeleteKey === key} onOpenChange={(o) => setConfirmDeleteKey(o ? key : null)}>
                                  <PopoverTrigger asChild>
                                    <Button
                                      size='sm'
                                      variant='ghost'
                                      className='text-destructive h-7 w-7 p-0'
                                      disabled={isPending || isOAuth || (validKeys.includes(key) && isLastKey)}
                                    >
                                      <IconTrash className='h-4 w-4' />
                                    </Button>
                                  </PopoverTrigger>
                                  <PopoverContent className='w-64'>
                                    <div className='flex flex-col gap-3'>
                                      <p className='text-sm'>{t('channels.dialogs.keyManagement.confirmDelete')}</p>
                                      <div className='flex justify-end gap-2'>
                                        <Button size='sm' variant='outline' onClick={() => setConfirmDeleteKey(null)}>
                                          {t('common.buttons.cancel')}
                                        </Button>
                                        <Button
                                          size='sm'
                                          variant='destructive'
                                          onClick={() => handleDeleteSingle(key)}
                                          disabled={isPending}
                                        >
                                          {t('common.buttons.confirm')}
                                        </Button>
                                      </div>
                                    </div>
                                  </PopoverContent>
                                </Popover>
                              </div>
                            </TableCell>
                          </TableRow>
                        );
                      })
                    )}
                  </TableBody>
                </Table>
              </ScrollArea>
            </div>
          </div>
        )}

        <DialogFooter align='between' className='items-center'>
          <div className='text-muted-foreground text-xs'>
            {isPending ? t('channels.dialogs.keyManagement.processing') : t('channels.dialogs.keyManagement.footerHint')}
          </div>
          <Button variant='outline' onClick={handleClose}>
            {t('common.buttons.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
