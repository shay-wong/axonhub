'use client';

import React, { useState, useEffect, useCallback } from 'react';
import { Loader2, Plus, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  useRetryPolicy,
  useUpdateRetryPolicy,
  type AutoDisableChannelInput,
  type AutoDisableChannelStatusInput,
  type RetryPolicyInput,
} from '../data/system';

type AutoDisablePolicyKey = 'channelAutoDisable' | 'apiKeyAutoDisable';
type AutoDisableRuleField = 'status' | 'times' | 'action' | 'durationMinutes' | 'useRetryAfter';

const DEFAULT_AUTO_DISABLE_RULE: AutoDisableChannelStatusInput = {
  status: 500,
  times: 3,
  action: 'temporary',
  durationMinutes: 5,
  useRetryAfter: false,
};

function cloneStatusRules(rules?: AutoDisableChannelStatusInput[] | null): AutoDisableChannelStatusInput[] {
  return (rules || []).map((rule) => ({
    status: rule.status,
    times: rule.times,
    action: rule.action || 'permanent',
    durationMinutes: rule.durationMinutes ?? (rule.action === 'temporary' ? 5 : null),
    useRetryAfter: rule.status === 429 ? Boolean(rule.useRetryAfter) : false,
  }));
}

function normalizePolicyForForm(policy?: AutoDisableChannelInput | null): AutoDisableChannelInput {
  return {
    enabled: policy?.enabled || false,
    statuses: cloneStatusRules(policy?.statuses),
  };
}

export function RetrySettings() {
  const { t } = useTranslation();
  const { data: retryPolicyData, isLoading } = useRetryPolicy();
  const updateRetryPolicy = useUpdateRetryPolicy();
  const retryPolicy = retryPolicyData?.retryPolicy;
  const defaultAutoDisableStatusRules = retryPolicyData?.defaultAutoDisableStatusRules ?? [];

  const [formData, setFormData] = useState<RetryPolicyInput>({
    enabled: true,
    maxChannelRetries: 3,
    maxSingleChannelRetries: 2,
    retryDelayMs: 1000,
    streamFirstEventTimeoutSeconds: 0,
    nonStreamResponseTimeoutSeconds: 0,
    loadBalancerStrategy: 'adaptive',
    emptyResponseDetection: false,
    upstreamErrorPolicy: {
      mode: 'passthrough',
      customMessage: '',
    },
    autoDisableChannel: {
      enabled: false,
      statuses: [],
    },
    channelAutoDisable: {
      enabled: false,
      statuses: [],
    },
    apiKeyAutoDisable: {
      enabled: false,
      statuses: [],
    },
  });

  useEffect(() => {
    if (retryPolicy) {
      setFormData({
        enabled: retryPolicy.enabled,
        maxChannelRetries: retryPolicy.maxChannelRetries,
        maxSingleChannelRetries: retryPolicy.maxSingleChannelRetries,
        retryDelayMs: retryPolicy.retryDelayMs,
        streamFirstEventTimeoutSeconds: retryPolicy.streamFirstEventTimeoutSeconds,
        nonStreamResponseTimeoutSeconds: retryPolicy.nonStreamResponseTimeoutSeconds,
        loadBalancerStrategy: retryPolicy.loadBalancerStrategy,
        emptyResponseDetection: retryPolicy.emptyResponseDetection,
        upstreamErrorPolicy: {
          mode: retryPolicy.upstreamErrorPolicy?.mode || 'passthrough',
          customMessage: retryPolicy.upstreamErrorPolicy?.customMessage || '',
        },
        autoDisableChannel: normalizePolicyForForm(retryPolicy.autoDisableChannel),
        channelAutoDisable: normalizePolicyForForm(retryPolicy.channelAutoDisable),
        apiKeyAutoDisable: normalizePolicyForForm(retryPolicy.apiKeyAutoDisable),
      });
    }
  }, [retryPolicy]);

  const handleInputChange = useCallback((field: keyof RetryPolicyInput, value: string | boolean | number) => {
    setFormData((prev) => ({
      ...prev,
      [field]: value,
    }));
  }, []);

  const handleUpstreamErrorPolicyChange = useCallback((field: 'mode' | 'customMessage', value: string) => {
    setFormData((prev) => ({
      ...prev,
      upstreamErrorPolicy: {
        ...prev.upstreamErrorPolicy,
        [field]: value,
      },
    }));
  }, []);

  const updateAutoDisablePolicy = useCallback(
    (policyKey: AutoDisablePolicyKey, updater: (policy: AutoDisableChannelInput) => AutoDisableChannelInput) => {
      setFormData((prev) => {
        const nextPolicy = updater(normalizePolicyForForm(prev[policyKey]));
        return {
          ...prev,
          [policyKey]: nextPolicy,
        };
      });
    },
    []
  );

  const handleAutoDisableEnabledChange = useCallback(
    (policyKey: AutoDisablePolicyKey, enabled: boolean) => {
      updateAutoDisablePolicy(policyKey, (policy) => ({
        ...policy,
        enabled,
        statuses:
          enabled && (policy.statuses?.length || 0) === 0
            ? cloneStatusRules(defaultAutoDisableStatusRules)
            : policy.statuses || [],
      }));
    },
    [defaultAutoDisableStatusRules, updateAutoDisablePolicy]
  );

  const handleStatusChange = useCallback(
    (policyKey: AutoDisablePolicyKey, index: number, field: AutoDisableRuleField, value: string | number | boolean | null) => {
      updateAutoDisablePolicy(policyKey, (policy) => ({
        ...policy,
        statuses: (policy.statuses || []).map((rule, i) => {
          if (i !== index) return rule;

          const nextRule = { ...rule };
          switch (field) {
            case 'status':
              nextRule.status = typeof value === 'number' ? value : 0;
              if (nextRule.status !== 429) {
                nextRule.useRetryAfter = false;
              }
              break;
            case 'times':
              nextRule.times = typeof value === 'number' ? value : 1;
              break;
            case 'action':
              nextRule.action = typeof value === 'string' ? value : 'permanent';
              if (nextRule.action !== 'temporary') {
                nextRule.durationMinutes = null;
              } else if (!nextRule.durationMinutes) {
                nextRule.durationMinutes = 5;
              }
              break;
            case 'durationMinutes':
              nextRule.durationMinutes = typeof value === 'number' ? value : null;
              break;
            case 'useRetryAfter':
              nextRule.useRetryAfter = Boolean(value);
              break;
          }
          return nextRule;
        }),
      }));
    },
    [updateAutoDisablePolicy]
  );

  const addStatus = useCallback(
    (policyKey: AutoDisablePolicyKey) => {
      updateAutoDisablePolicy(policyKey, (policy) => ({
        ...policy,
        statuses: [...(policy.statuses || []), { ...DEFAULT_AUTO_DISABLE_RULE }],
      }));
    },
    [updateAutoDisablePolicy]
  );

  const removeStatus = useCallback(
    (policyKey: AutoDisablePolicyKey, index: number) => {
      updateAutoDisablePolicy(policyKey, (policy) => ({
        ...policy,
        statuses: policy.statuses?.filter((_, i) => i !== index) || [],
      }));
    },
    [updateAutoDisablePolicy]
  );

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      const channelAutoDisable = normalizePolicyForForm(formData.channelAutoDisable);
      await updateRetryPolicy.mutateAsync({
        ...formData,
        channelAutoDisable,
        apiKeyAutoDisable: normalizePolicyForForm(formData.apiKeyAutoDisable),
        autoDisableChannel: channelAutoDisable,
      });
    },
    [updateRetryPolicy, formData]
  );

  const renderAutoDisablePolicy = useCallback(
    (policyKey: AutoDisablePolicyKey) => {
      const policy = normalizePolicyForForm(formData[policyKey]);
      const rules = policy.statuses || [];
      const prefix = `system.retry.autoDisable.${policyKey}`;

      return (
        <div className='space-y-4 rounded-md border p-4'>
          <div className='flex items-center justify-between gap-4'>
            <div className='space-y-0.5'>
              <Label htmlFor={`auto-disable-${policyKey}`} className='text-base'>
                {t(`${prefix}.label`)}
              </Label>
              <div className='text-muted-foreground text-sm'>{t(`${prefix}.description`)}</div>
            </div>
            <Switch
              id={`auto-disable-${policyKey}`}
              checked={policy.enabled || false}
              onCheckedChange={(checked) => handleAutoDisableEnabledChange(policyKey, checked)}
            />
          </div>

          {policy.enabled && (
            <div className='space-y-3'>
              <div className='flex items-center justify-between gap-3'>
                <Label className='text-sm font-medium'>{t('system.retry.autoDisable.statuses.label')}</Label>
                <Button type='button' variant='outline' size='sm' onClick={() => addStatus(policyKey)}>
                  <Plus className='mr-1 h-4 w-4' />
                  {t('system.retry.autoDisable.statuses.add')}
                </Button>
              </div>

              {rules.length > 0 ? (
                <div className='space-y-2'>
                  {rules.map((rule, index) => {
                    const action = rule.action || 'permanent';
                    const status = Number(rule.status) || 0;
                    const showRetryAfter = action === 'temporary' && status === 429;

                    return (
                      <div key={`${policyKey}-${index}`} className='space-y-3 rounded-md border p-3'>
                        <div className='grid gap-3 md:grid-cols-[96px_96px_160px_1fr_auto] md:items-end'>
                          <div className='space-y-1'>
                            <Label className='text-xs'>{t('system.retry.autoDisable.statuses.status')}</Label>
                            <Input
                              type='number'
                              value={rule.status}
                              onChange={(e) => handleStatusChange(policyKey, index, 'status', parseInt(e.target.value) || 0)}
                              min='400'
                              max='599'
                            />
                          </div>

                          <div className='space-y-1'>
                            <Label className='text-xs'>{t('system.retry.autoDisable.statuses.times')}</Label>
                            <Input
                              type='number'
                              value={rule.times}
                              onChange={(e) => handleStatusChange(policyKey, index, 'times', parseInt(e.target.value) || 1)}
                              min='1'
                              max='100'
                            />
                          </div>

                          <div className='space-y-1'>
                            <Label className='text-xs'>{t('system.retry.autoDisable.statuses.action')}</Label>
                            <Select
                              value={action}
                              onValueChange={(value) => handleStatusChange(policyKey, index, 'action', value)}
                            >
                              <SelectTrigger>
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value='none'>{t('system.retry.autoDisable.actions.none')}</SelectItem>
                                <SelectItem value='permanent'>{t('system.retry.autoDisable.actions.permanent')}</SelectItem>
                                <SelectItem value='temporary'>{t('system.retry.autoDisable.actions.temporary')}</SelectItem>
                              </SelectContent>
                            </Select>
                          </div>

                          <div className='space-y-1'>
                            <Label className='text-xs'>{t('system.retry.autoDisable.statuses.duration')}</Label>
                            <div className='flex items-center gap-2'>
                              <Input
                                type='number'
                                value={action === 'temporary' ? rule.durationMinutes || 5 : ''}
                                onChange={(e) =>
                                  handleStatusChange(policyKey, index, 'durationMinutes', parseInt(e.target.value) || 1)
                                }
                                min='1'
                                max='10080'
                                disabled={action !== 'temporary'}
                                className='w-28'
                              />
                              <span className='text-muted-foreground text-sm'>{t('system.retry.autoDisable.statuses.minutes')}</span>
                            </div>
                          </div>

                          <Button type='button' variant='ghost' size='icon' onClick={() => removeStatus(policyKey, index)}>
                            <Trash2 className='h-4 w-4' />
                          </Button>
                        </div>

                        {action === 'temporary' && (
                          <div className='flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2'>
                            <div className='space-y-0.5'>
                              <Label className='text-xs'>{t('system.retry.autoDisable.statuses.useRetryAfter')}</Label>
                              <p className='text-muted-foreground text-xs'>
                                {t('system.retry.autoDisable.statuses.useRetryAfterHint')}
                              </p>
                            </div>
                            <Switch
                              checked={showRetryAfter && Boolean(rule.useRetryAfter)}
                              disabled={status !== 429}
                              onCheckedChange={(checked) => handleStatusChange(policyKey, index, 'useRetryAfter', checked)}
                            />
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : (
                <div className='rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200'>
                  {t('system.retry.autoDisable.statuses.emptyEnabled')}
                </div>
              )}
            </div>
          )}
        </div>
      );
    },
    [addStatus, formData, handleAutoDisableEnabledChange, handleStatusChange, removeStatus, t]
  );

  if (isLoading) {
    return (
      <div className='flex items-center justify-center p-8'>
        <Loader2 className='h-8 w-8 animate-spin' />
      </div>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('system.retry.title')}</CardTitle>
        <CardDescription>{t('system.retry.description')}</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className='space-y-6'>
          {/* Enable/Disable Retry */}
          <div className='flex items-center justify-between' id='retry-enabled-switch'>
            <div className='space-y-0.5'>
              <Label htmlFor='retry-enabled' className='text-base'>
                {t('system.retry.enabled.label')}
              </Label>
              <div className='text-muted-foreground text-sm'>{t('system.retry.enabled.description')}</div>
            </div>
            <Switch id='retry-enabled' checked={formData.enabled} onCheckedChange={(checked) => handleInputChange('enabled', checked)} />
          </div>

          <Separator />

          <div className='space-y-4'>
            <div className='space-y-2'>
              <Label htmlFor='upstream-error-mode'>{t('system.retry.upstreamErrorPolicy.label')}</Label>
              <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.upstreamErrorPolicy.description')}</div>
              <Select
                value={formData.upstreamErrorPolicy?.mode || 'passthrough'}
                onValueChange={(value) => value && handleUpstreamErrorPolicyChange('mode', value)}
              >
                <SelectTrigger id='upstream-error-mode' className='w-56'>
                  <SelectValue placeholder={t('system.retry.upstreamErrorPolicy.placeholder')} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='passthrough'>{t('system.retry.upstreamErrorPolicy.options.passthrough')}</SelectItem>
                  <SelectItem value='hidden'>{t('system.retry.upstreamErrorPolicy.options.hidden')}</SelectItem>
                  <SelectItem value='custom'>{t('system.retry.upstreamErrorPolicy.options.custom')}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {formData.upstreamErrorPolicy?.mode === 'custom' && (
              <div className='space-y-2'>
                <Label htmlFor='upstream-error-custom-message'>{t('system.retry.upstreamErrorPolicy.customMessage.label')}</Label>
                <Textarea
                  id='upstream-error-custom-message'
                  value={formData.upstreamErrorPolicy?.customMessage || ''}
                  onChange={(e) => handleUpstreamErrorPolicyChange('customMessage', e.target.value)}
                  placeholder={t('system.retry.upstreamErrorPolicy.customMessage.placeholder')}
                  className='min-h-20'
                />
              </div>
            )}
          </div>

          <Separator />

          {/* Retry Configuration - Only show when enabled */}
          {formData.enabled && (
            <div className='space-y-4'>
              <div className='space-y-2'>
                <Label htmlFor='load-balancer-strategy'>{t('system.retry.loadBalancerStrategy.label')}</Label>
                <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.loadBalancerStrategy.description')}</div>
                <Select
                  value={formData.loadBalancerStrategy || 'adaptive'}
                  onValueChange={(value) => value && handleInputChange('loadBalancerStrategy', value)}
                >
                  <SelectTrigger id='load-balancer-strategy' className='w-56'>
                    <SelectValue placeholder={t('system.retry.loadBalancerStrategy.placeholder')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='adaptive'>{t('system.retry.loadBalancerStrategy.options.adaptive')}</SelectItem>
                    <SelectItem value='failover'>{t('system.retry.loadBalancerStrategy.options.failover')}</SelectItem>
                    <SelectItem value='circuit-breaker'>{t('system.retry.loadBalancerStrategy.options.circuitBreaker')}</SelectItem>
                  </SelectContent>
                </Select>

                {/* Strategy Documentation */}
                {formData.loadBalancerStrategy && (
                  <div className='bg-muted/50 mt-3 rounded-md border p-3'>
                    <div className='text-muted-foreground text-xs leading-relaxed'>
                      {t(`system.retry.loadBalancerStrategy.documentation.${formData.loadBalancerStrategy}`)}
                    </div>
                  </div>
                )}
              </div>

              {/* Max Channel Retries */}
              <div className='space-y-2' id='retry-max-retries'>
                <Label htmlFor='max-channel-retries'>{t('system.retry.maxChannelRetries.label')}</Label>
                <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.maxChannelRetries.description')}</div>
                <Input
                  id='max-channel-retries'
                  type='number'
                  min='0'
                  max='10'
                  value={formData.maxChannelRetries}
                  onChange={(e) => handleInputChange('maxChannelRetries', parseInt(e.target.value) || 0)}
                  className='w-32'
                />
              </div>

              {/* Max Single Channel Retries */}
              <div className='space-y-2'>
                <Label htmlFor='max-single-channel-retries'>{t('system.retry.maxSingleChannelRetries.label')}</Label>
                <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.maxSingleChannelRetries.description')}</div>
                <Input
                  id='max-single-channel-retries'
                  type='number'
                  min='0'
                  max='5'
                  value={formData.maxSingleChannelRetries}
                  onChange={(e) => handleInputChange('maxSingleChannelRetries', parseInt(e.target.value) || 0)}
                  className='w-32'
                />
              </div>

              {/* Retry Delay */}
              <div className='space-y-2'>
                <Label htmlFor='retry-delay'>{t('system.retry.retryDelayMs.label')}</Label>
                <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.retryDelayMs.description')}</div>
                <div className='flex items-center space-x-2'>
                  <Input
                    id='retry-delay'
                    type='number'
                    min='100'
                    max='10000'
                    step='100'
                    value={formData.retryDelayMs}
                    onChange={(e) => handleInputChange('retryDelayMs', parseInt(e.target.value) || 1000)}
                    className='w-32'
                  />
                  <span className='text-muted-foreground text-sm'>ms</span>
                </div>
              </div>

              {/* Response Timeouts */}
              <div className='grid gap-4 md:grid-cols-2'>
                <div className='space-y-2'>
                  <Label htmlFor='stream-first-event-timeout'>{t('system.retry.streamFirstEventTimeoutSeconds.label')}</Label>
                  <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.streamFirstEventTimeoutSeconds.description')}</div>
                  <div className='flex items-center space-x-2'>
                    <Input
                      id='stream-first-event-timeout'
                      type='number'
                      min='0'
                      max='600'
                      value={formData.streamFirstEventTimeoutSeconds}
                      onChange={(e) => handleInputChange('streamFirstEventTimeoutSeconds', parseInt(e.target.value) || 0)}
                      className='w-32'
                    />
                    <span className='text-muted-foreground text-sm'>s</span>
                  </div>
                </div>

                <div className='space-y-2'>
                  <Label htmlFor='non-stream-response-timeout'>{t('system.retry.nonStreamResponseTimeoutSeconds.label')}</Label>
                  <div className='text-muted-foreground mb-2 text-sm'>{t('system.retry.nonStreamResponseTimeoutSeconds.description')}</div>
                  <div className='flex items-center space-x-2'>
                    <Input
                      id='non-stream-response-timeout'
                      type='number'
                      min='0'
                      max='600'
                      value={formData.nonStreamResponseTimeoutSeconds}
                      onChange={(e) => handleInputChange('nonStreamResponseTimeoutSeconds', parseInt(e.target.value) || 0)}
                      className='w-32'
                    />
                    <span className='text-muted-foreground text-sm'>s</span>
                  </div>
                </div>
              </div>

              {/* Empty Response Detection */}
              <div className='flex items-center justify-between'>
                <div className='space-y-0.5'>
                  <Label htmlFor='empty-response-detection' className='text-base'>
                    {t('system.retry.emptyResponseDetection.label')}
                  </Label>
                  <div className='text-muted-foreground text-sm'>{t('system.retry.emptyResponseDetection.description')}</div>
                </div>
                <Switch
                  id='empty-response-detection'
                  checked={formData.emptyResponseDetection || false}
                  onCheckedChange={(checked) => handleInputChange('emptyResponseDetection', checked)}
                />
              </div>

              <Separator />

              {/* Auto Disable */}
              <div className='space-y-4'>
                <div className='space-y-1'>
                  <h3 className='text-base font-medium'>{t('system.retry.autoDisable.title')}</h3>
                  <p className='text-muted-foreground text-sm'>{t('system.retry.autoDisable.description')}</p>
                </div>
                {renderAutoDisablePolicy('channelAutoDisable')}
                {renderAutoDisablePolicy('apiKeyAutoDisable')}
              </div>
            </div>
          )}

          <Separator />

          {/* Submit Button */}
          <div className='flex justify-end'>
            <Button type='submit' disabled={updateRetryPolicy.isPending} className='min-w-24'>
              {updateRetryPolicy.isPending ? <Loader2 className='h-4 w-4 animate-spin' /> : t('common.buttons.save')}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
