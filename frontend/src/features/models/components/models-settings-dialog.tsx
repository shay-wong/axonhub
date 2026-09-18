'use client';

import React, { useCallback } from 'react';
import { Loader2, Settings2, RefreshCcw, Layers, ListTree, BrainCircuit, Ban, EyeOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { AutoCompleteSelect } from '@/components/auto-complete-select';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { useAllChannelSummarys } from '@/features/channels/data/channels';
import { useModelSettings, useUpdateModelSettings, type UpdateModelSettingsInput } from '@/features/system/data/system';
import { useModels } from '../context/models-context';

export function ModelSettingsDialog() {
  const { t } = useTranslation();
  const { open, setOpen } = useModels();
  const { data: settings, isLoading } = useModelSettings();
  const updateModelSettings = useUpdateModelSettings();

  const isOpen = open === 'settings';
  const dialogContentRef = React.useRef<HTMLDivElement>(null);
  const { data: channels, isLoading: isModelsLoading } = useAllChannelSummarys(undefined, { enabled: isOpen });

  const [fallbackEnabled, setFallbackEnabled] = React.useState(false);
  const [queryAllChannelModels, setQueryAllChannelModels] = React.useState(false);
  const [defaultModelAPIIncludeAll, setDefaultModelAPIIncludeAll] = React.useState(false);
  const [autoReasoningEffort, setAutoReasoningEffort] = React.useState(false);
  const [modelBlacklistRegex, setModelBlacklistRegex] = React.useState('');
  const [hideUnroutableModelsInList, setHideUnroutableModelsInList] = React.useState(false);
  const [codexImageMainModel, setCodexImageMainModel] = React.useState('gpt-6-astra');
  const imageMainModelOptions = React.useMemo(() => {
    const models = channels?.edges
      .filter(({ node }) => node.type === 'codex' && node.status === 'enabled')
      .flatMap(({ node }) => node.allModelEntries.map((entry) => entry.actualModel)) ?? [];
    return [...new Set(['gpt-6-astra', codexImageMainModel, ...models])]
      .filter(Boolean)
      .map((model) => ({ value: model, label: model }));
  }, [channels, codexImageMainModel]);

  React.useEffect(() => {
    if (settings) {
      setFallbackEnabled(settings.fallbackToChannelsOnModelNotFound);
      setQueryAllChannelModels(settings.queryAllChannelModels);
      setDefaultModelAPIIncludeAll(settings.defaultModelAPIIncludeAll);
      setAutoReasoningEffort(settings.autoReasoningEffort);
      setModelBlacklistRegex(settings.modelBlacklistRegex ?? '');
      setHideUnroutableModelsInList(settings.hideUnroutableModelsInList);
      setCodexImageMainModel(settings.codexImageMainModel || 'gpt-6-astra');
    }
  }, [settings]);

  const handleSave = useCallback(async () => {
    const input: UpdateModelSettingsInput = {
      fallbackToChannelsOnModelNotFound: fallbackEnabled,
      queryAllChannelModels: queryAllChannelModels,
      defaultModelAPIIncludeAll: defaultModelAPIIncludeAll,
      autoReasoningEffort: autoReasoningEffort,
      modelBlacklistRegex: modelBlacklistRegex,
      hideUnroutableModelsInList: hideUnroutableModelsInList,
      codexImageMainModel,
      developerSettings: settings?.developerSettings || [],
    };
    await updateModelSettings.mutateAsync(input);
    setOpen(null);
  }, [updateModelSettings, fallbackEnabled, queryAllChannelModels, defaultModelAPIIncludeAll, autoReasoningEffort, modelBlacklistRegex, hideUnroutableModelsInList, codexImageMainModel, settings?.developerSettings, setOpen]);

  const handleClose = useCallback(() => {
    setOpen(null);
  }, [setOpen]);

  return (
    <Dialog open={isOpen} onOpenChange={handleClose}>
      <DialogContent ref={dialogContentRef} className='flex max-h-[90vh] w-full max-w-full flex-col overflow-hidden sm:max-w-[720px]'>
        <DialogHeader className='shrink-0'>
          <DialogTitle className='flex items-center gap-2 text-lg sm:text-xl'>
            <Settings2 className='h-5 w-5' />
            {t('models.dialogs.settings.title')}
          </DialogTitle>
          <DialogDescription className='text-sm sm:text-base'>{t('models.dialogs.settings.description')}</DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <div className='flex items-center justify-center py-12'>
            <Loader2 className='h-8 w-8 animate-spin' />
          </div>
        ) : (
          <div className='min-h-0 flex-1 space-y-4 overflow-y-auto pr-1'>
            <Card>
              <CardHeader className='pb-0'>
                <CardTitle className='text-sm sm:text-base'>{t('models.dialogs.settings.codexImageMainModel.label')}</CardTitle>
              </CardHeader>
              <CardContent className='space-y-2 pt-1'>
                <p className='text-muted-foreground text-sm'>{t('models.dialogs.settings.codexImageMainModel.description')}</p>
                <AutoCompleteSelect
                  selectedValue={codexImageMainModel}
                  onSelectedValueChange={setCodexImageMainModel}
                  items={imageMainModelOptions}
                  isLoading={isModelsLoading}
                  placeholder={t('models.dialogs.settings.codexImageMainModel.label')}
                  emptyMessage={t('models.dialogs.settings.codexImageMainModel.empty')}
                  portalContainer={dialogContentRef.current}
                />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='pb-0'>
                <CardTitle className='flex items-center gap-2 text-sm sm:text-base'>
                  <RefreshCcw className='text-muted-foreground h-4 w-4' />
                  {t('models.dialogs.settings.fallbackToChannels.label')}
                </CardTitle>
              </CardHeader>
              <CardContent className='pt-1'>
                <div className='flex items-center justify-between'>
                  <p className='text-muted-foreground pr-4 text-sm'>{t('models.dialogs.settings.fallbackToChannels.description')}</p>
                  <Switch
                    id='fallback-enabled'
                    checked={fallbackEnabled}
                    onCheckedChange={setFallbackEnabled}
                    disabled={updateModelSettings.isPending}
                    className='scale-100 sm:scale-75'
                  />
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='pb-0'>
                <CardTitle className='flex items-center gap-2 text-sm sm:text-base'>
                  <Layers className='text-muted-foreground h-4 w-4' />
                  {t('models.dialogs.settings.queryAllChannelModels.label')}
                </CardTitle>
              </CardHeader>
              <CardContent className='pt-1'>
                <div className='flex items-center justify-between'>
                  <p className='text-muted-foreground pr-4 text-sm'>{t('models.dialogs.settings.queryAllChannelModels.description')}</p>
                  <Switch
                    id='query-all-channel-models'
                    checked={queryAllChannelModels}
                    onCheckedChange={setQueryAllChannelModels}
                    disabled={updateModelSettings.isPending}
                    className='scale-100 sm:scale-75'
                  />
                </div>
              </CardContent>
            </Card>

            <Card className={!queryAllChannelModels ? 'opacity-50' : undefined}>
              <CardHeader className='pb-0'>
                <CardTitle className='flex items-center gap-2 text-sm sm:text-base'>
                  <Ban className='text-muted-foreground h-4 w-4' />
                  {t('models.dialogs.settings.modelBlacklistRegex.label')}
                </CardTitle>
              </CardHeader>
              <CardContent className='pt-1 space-y-2'>
                <p className='text-muted-foreground text-sm'>{t('models.dialogs.settings.modelBlacklistRegex.description')}</p>
                <Input
                  id='model-blacklist-regex'
                  value={modelBlacklistRegex}
                  onChange={(e) => setModelBlacklistRegex(e.target.value)}
                  placeholder={t('models.dialogs.settings.modelBlacklistRegex.placeholder')}
                  disabled={!queryAllChannelModels || updateModelSettings.isPending}
                />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='pb-0'>
                <CardTitle className='flex items-center gap-2 text-sm sm:text-base'>
                  <ListTree className='text-muted-foreground h-4 w-4' />
                  {t('models.dialogs.settings.defaultModelAPIIncludeAll.label')}
                </CardTitle>
              </CardHeader>
              <CardContent className='pt-1'>
                <div className='flex items-center justify-between'>
                  <p className='text-muted-foreground pr-4 text-sm'>{t('models.dialogs.settings.defaultModelAPIIncludeAll.description')}</p>
                  <Switch
                    id='default-model-api-include-all'
                    checked={defaultModelAPIIncludeAll}
                    onCheckedChange={setDefaultModelAPIIncludeAll}
                    disabled={updateModelSettings.isPending}
                    className='scale-100 sm:scale-75'
                  />
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='pb-0'>
                <CardTitle className='flex items-center gap-2 text-sm sm:text-base'>
                  <EyeOff className='text-muted-foreground h-4 w-4' />
                  {t('models.dialogs.settings.hideUnroutableModelsInList.label')}
                </CardTitle>
              </CardHeader>
              <CardContent className='pt-1'>
                <div className='flex items-center justify-between'>
                  <p className='text-muted-foreground pr-4 text-sm'>{t('models.dialogs.settings.hideUnroutableModelsInList.description')}</p>
                  <Switch
                    id='hide-unroutable-models-in-list'
                    checked={hideUnroutableModelsInList}
                    onCheckedChange={setHideUnroutableModelsInList}
                    disabled={updateModelSettings.isPending}
                    className='scale-100 sm:scale-75'
                  />
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='pb-0'>
                <CardTitle className='flex items-center gap-2 text-sm sm:text-base'>
                  <BrainCircuit className='text-muted-foreground h-4 w-4' />
                  {t('models.dialogs.settings.autoReasoningEffort.label')}
                </CardTitle>
              </CardHeader>
              <CardContent className='pt-1'>
                <div className='flex items-center justify-between'>
                  <p className='text-muted-foreground pr-4 text-sm'>{t('models.dialogs.settings.autoReasoningEffort.description')}</p>
                  <Switch
                    id='auto-reasoning-effort'
                    checked={autoReasoningEffort}
                    onCheckedChange={setAutoReasoningEffort}
                    disabled={updateModelSettings.isPending}
                    className='scale-100 sm:scale-75'
                  />
                </div>
              </CardContent>
            </Card>
          </div>
        )}

        <DialogFooter className='flex shrink-0 flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3 sm:gap-2'>
          <Button variant='outline' onClick={handleClose} disabled={updateModelSettings.isPending} className='w-full sm:w-auto h-10 sm:h-9'>
            {t('common.buttons.cancel')}
          </Button>
          <Button onClick={handleSave} disabled={updateModelSettings.isPending || isLoading} className='w-full sm:w-auto h-10 sm:h-9'>
            {updateModelSettings.isPending ? (
              <>
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                {t('common.buttons.saving')}
              </>
            ) : (
              t('common.buttons.save')
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
