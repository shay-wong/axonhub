'use client';

import { useEffect, useState } from 'react';
import { resolveExternalURLs } from '@/config/external-urls';
import { ExternalLink, RefreshCw, CheckCircle, AlertCircle, Download, Power, RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { systemApi } from '@/lib/api-client';
import { usePermissions } from '@/hooks/usePermissions';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { useSystemVersion, useCheckForUpdate } from '../data/system';

const { repositoryURL, releasesURL, issuesURL } = resolveExternalURLs(import.meta.env);

const waitForRestart = async () => {
  await new Promise((resolve) => setTimeout(resolve, 1000));
  for (let attempt = 0; attempt < 30; attempt += 1) {
    try {
      const response = await fetch('/health', { cache: 'no-store' });
      if (response.ok) {
        window.location.reload();
        return;
      }
    } catch {
      // The process is expected to be briefly unavailable while restarting.
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
};

interface AboutSettingsProps {
  includeBeta: boolean;
  onIncludeBetaChange: (includeBeta: boolean) => void;
}

export function AboutSettings({ includeBeta, onIncludeBetaChange }: AboutSettingsProps) {
  const { t } = useTranslation();
  const { hasSystemScope } = usePermissions();
  const [isInstalling, setIsInstalling] = useState(false);
  const [isRollingBack, setIsRollingBack] = useState(false);
  const [isRestarting, setIsRestarting] = useState(false);
  const [needRestart, setNeedRestart] = useState(false);
  const [installedAction, setInstalledAction] = useState<'update' | 'rollback' | null>(null);
  const [rollbackVersions, setRollbackVersions] = useState<{ version: string; publishedAt: string; releaseUrl: string }[]>([]);
  const [selectedRollbackVersion, setSelectedRollbackVersion] = useState('');
  const [rollbackVersionsLoading, setRollbackVersionsLoading] = useState(false);
  const { data: version, isLoading: versionLoading } = useSystemVersion();
  const { data: updateCheck, isFetching: isCheckingForUpdate, refetch: checkUpdate } = useCheckForUpdate(includeBeta);
  const canUpdate = hasSystemScope('write_settings') && Boolean(version?.buildTime) && !version?.platform.startsWith('windows/');

  useEffect(() => {
    if (!canUpdate) return;

    setRollbackVersionsLoading(true);
    systemApi
      .getRollbackVersions()
      .then(({ versions }) => setRollbackVersions(versions))
      .catch((error) => toast.error(error instanceof Error ? error.message : t('system.about.rollback.loadFailed')))
      .finally(() => setRollbackVersionsLoading(false));
  }, [canUpdate, t]);

  const installUpdate = async () => {
    setIsInstalling(true);
    try {
      const result = await systemApi.installUpdate(includeBeta);
      setNeedRestart(result.needRestart);
      setInstalledAction('update');
      toast.success(t('system.about.updateCheck.installSuccess'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('system.about.updateCheck.installFailed'));
    } finally {
      setIsInstalling(false);
    }
  };

  const restart = async () => {
    setIsRestarting(true);
    try {
      await systemApi.restart();
      toast.success(t('system.about.updateCheck.restarting'));
      await waitForRestart();
    } catch (error) {
      setIsRestarting(false);
      toast.error(error instanceof Error ? error.message : t('system.about.updateCheck.restartFailed'));
    }
  };

  const rollback = async () => {
    if (!selectedRollbackVersion) return;

    setIsRollingBack(true);
    try {
      const result = await systemApi.rollback(selectedRollbackVersion);
      setNeedRestart(result.needRestart);
      setInstalledAction('rollback');
      toast.success(t('system.about.rollback.success', { version: selectedRollbackVersion }));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('system.about.rollback.failed'));
    } finally {
      setIsRollingBack(false);
    }
  };

  if (versionLoading) {
    return (
      <div className='space-y-6'>
        <Card>
          <CardHeader>
            <Skeleton className='h-6 w-48' />
            <Skeleton className='h-4 w-72' />
          </CardHeader>
          <CardContent className='space-y-4'>
            {[1, 2, 3, 4, 5].map((i) => (
              <Skeleton key={i} className='h-4 w-full' />
            ))}
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle>{t('system.about.title')}</CardTitle>
          <CardDescription>{t('system.about.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-6'>
          {/* Version Info */}
          <div className='space-y-4'>
            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>{t('system.about.version')}</span>
              <Badge variant='secondary' className='font-mono'>
                {version?.version || '-'}
              </Badge>
            </div>

            {version?.commit && (
              <div className='flex items-center justify-between'>
                <span className='text-muted-foreground text-sm'>{t('system.about.commit')}</span>
                <span className='font-mono text-sm'>{version.commit.substring(0, 7)}</span>
              </div>
            )}

            {version?.buildTime && (
              <div className='flex items-center justify-between'>
                <span className='text-muted-foreground text-sm'>{t('system.about.buildTime')}</span>
                <span className='text-sm'>{version.buildTime}</span>
              </div>
            )}

            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>{t('system.about.goVersion')}</span>
              <span className='text-sm'>{version?.goVersion || '-'}</span>
            </div>

            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>{t('system.about.platform')}</span>
              <span className='text-sm'>{version?.platform || '-'}</span>
            </div>

            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>{t('system.about.uptime')}</span>
              <span className='text-sm'>{version?.uptime || '-'}</span>
            </div>
          </div>

          {/* Update Check */}
          <div className='border-t pt-6'>
            <div className='flex items-center justify-between gap-4'>
              <div className='space-y-2'>
                <h4 className='text-sm font-medium'>{t('system.about.updateCheck.title')}</h4>
                <p className='text-muted-foreground text-sm'>{t('system.about.updateCheck.description')}</p>
                <div className='flex items-center gap-2'>
                  <Switch id='include-beta-releases' checked={includeBeta} onCheckedChange={onIncludeBetaChange} />
                  <Label htmlFor='include-beta-releases' className='text-muted-foreground text-sm font-normal'>
                    {t('system.about.updateCheck.includeBeta')}
                  </Label>
                </div>
              </div>
              <Button variant='outline' size='sm' onClick={() => checkUpdate()} disabled={isCheckingForUpdate}>
                <RefreshCw className={`mr-2 h-4 w-4 ${isCheckingForUpdate ? 'animate-spin' : ''}`} />
                {t('system.about.updateCheck.button')}
              </Button>
            </div>

            {updateCheck && !isCheckingForUpdate && (
              <div className='mt-4 rounded-lg border p-4'>
                {updateCheck.hasUpdate ? (
                  <div className='flex items-start gap-3'>
                    <AlertCircle className='mt-0.5 h-5 w-5 text-amber-500' />
                    <div className='flex-1 space-y-2'>
                      <p className='text-sm font-medium'>{t('system.about.updateCheck.newVersionAvailable')}</p>
                      <p className='text-muted-foreground text-sm'>
                        {t('system.about.updateCheck.currentVersion')}: {updateCheck.currentVersion} →{' '}
                        {t('system.about.updateCheck.latestVersion')}: {updateCheck.latestVersion}
                      </p>
                      <div className='flex flex-wrap items-center gap-2'>
                        <Button variant='link' size='sm' className='h-auto p-0' asChild>
                          <a href={updateCheck.releaseUrl} target='_blank' rel='noopener noreferrer'>
                            {t('system.about.updateCheck.viewRelease')}
                            <ExternalLink className='ml-1 h-3 w-3' />
                          </a>
                        </Button>
                        {canUpdate && !needRestart && (
                          <AlertDialog>
                            <AlertDialogTrigger asChild>
                              <Button size='sm' disabled={isInstalling || isRollingBack}>
                                <Download className={`mr-2 h-4 w-4 ${isInstalling ? 'animate-pulse' : ''}`} />
                                {isInstalling ? t('system.about.updateCheck.installing') : t('system.about.updateCheck.install')}
                              </Button>
                            </AlertDialogTrigger>
                            <AlertDialogContent>
                              <AlertDialogHeader>
                                <AlertDialogTitle>{t('system.about.updateCheck.confirmTitle')}</AlertDialogTitle>
                                <AlertDialogDescription>
                                  {t('system.about.updateCheck.confirmDescription', {
                                    version: updateCheck.latestVersion,
                                  })}
                                </AlertDialogDescription>
                              </AlertDialogHeader>
                              <AlertDialogFooter>
                                <AlertDialogCancel>{t('system.about.updateCheck.cancel')}</AlertDialogCancel>
                                <AlertDialogAction onClick={installUpdate}>{t('system.about.updateCheck.confirm')}</AlertDialogAction>
                              </AlertDialogFooter>
                            </AlertDialogContent>
                          </AlertDialog>
                        )}
                        {canUpdate && needRestart && installedAction === 'update' && (
                          <Button size='sm' onClick={restart} disabled={isRestarting}>
                            <Power className={`mr-2 h-4 w-4 ${isRestarting ? 'animate-pulse' : ''}`} />
                            {isRestarting ? t('system.about.updateCheck.restarting') : t('system.about.updateCheck.restart')}
                          </Button>
                        )}
                      </div>
                    </div>
                  </div>
                ) : (
                  <div className='flex items-center gap-3'>
                    <CheckCircle className='h-5 w-5 text-green-500' />
                    <p className='text-sm'>{t('system.about.updateCheck.upToDate')}</p>
                  </div>
                )}
              </div>
            )}
          </div>

          {canUpdate && (
            <div className='border-t pt-6'>
              <div className='space-y-4'>
                <div>
                  <h4 className='text-sm font-medium'>{t('system.about.rollback.title')}</h4>
                  <p className='text-muted-foreground text-sm'>{t('system.about.rollback.description')}</p>
                </div>

                <div className='flex flex-wrap items-center gap-2'>
                  <Select
                    value={selectedRollbackVersion}
                    onValueChange={setSelectedRollbackVersion}
                    disabled={rollbackVersionsLoading || rollbackVersions.length === 0 || isInstalling || isRollingBack || needRestart}
                  >
                    <SelectTrigger className='w-full sm:w-72'>
                      <SelectValue
                        placeholder={
                          rollbackVersionsLoading
                            ? t('system.about.rollback.loading')
                            : rollbackVersions.length === 0
                              ? t('system.about.rollback.empty')
                              : t('system.about.rollback.select')
                        }
                      />
                    </SelectTrigger>
                    <SelectContent>
                      {rollbackVersions.map((release) => (
                        <SelectItem key={release.version} value={release.version}>
                          {release.version} · {new Date(release.publishedAt).toLocaleDateString()}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>

                  <AlertDialog>
                    <AlertDialogTrigger asChild>
                      <Button
                        variant='outline'
                        size='sm'
                        disabled={!selectedRollbackVersion || isInstalling || isRollingBack || needRestart}
                      >
                        <RotateCcw className={`mr-2 h-4 w-4 ${isRollingBack ? 'animate-pulse' : ''}`} />
                        {isRollingBack ? t('system.about.rollback.installing') : t('system.about.rollback.button')}
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>{t('system.about.rollback.confirmTitle')}</AlertDialogTitle>
                        <AlertDialogDescription>
                          {t('system.about.rollback.confirmDescription', { version: selectedRollbackVersion })}
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>{t('system.about.updateCheck.cancel')}</AlertDialogCancel>
                        <AlertDialogAction onClick={rollback}>{t('system.about.rollback.confirm')}</AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>

                  {selectedRollbackVersion && (
                    <Button variant='link' size='sm' className='h-auto p-0' asChild>
                      <a
                        href={rollbackVersions.find((release) => release.version === selectedRollbackVersion)?.releaseUrl}
                        target='_blank'
                        rel='noopener noreferrer'
                      >
                        {t('system.about.updateCheck.viewRelease')}
                        <ExternalLink className='ml-1 h-3 w-3' />
                      </a>
                    </Button>
                  )}
                </div>

                {needRestart && installedAction === 'rollback' && (
                  <Button size='sm' onClick={restart} disabled={isRestarting}>
                    <Power className={`mr-2 h-4 w-4 ${isRestarting ? 'animate-pulse' : ''}`} />
                    {isRestarting ? t('system.about.updateCheck.restarting') : t('system.about.updateCheck.restart')}
                  </Button>
                )}
              </div>
            </div>
          )}

          {/* Links */}
          <div className='border-t pt-6'>
            <h4 className='mb-4 text-sm font-medium'>{t('system.about.links.title')}</h4>
            <div className='flex flex-wrap gap-4'>
              <Button variant='outline' size='sm' asChild>
                <a href={repositoryURL} target='_blank' rel='noopener noreferrer'>
                  GitHub
                  <ExternalLink className='ml-1 h-3 w-3' />
                </a>
              </Button>
              <Button variant='outline' size='sm' asChild>
                <a href={releasesURL} target='_blank' rel='noopener noreferrer'>
                  {t('system.about.links.releases')}
                  <ExternalLink className='ml-1 h-3 w-3' />
                </a>
              </Button>
              <Button variant='outline' size='sm' asChild>
                <a href={issuesURL} target='_blank' rel='noopener noreferrer'>
                  {t('system.about.links.issues')}
                  <ExternalLink className='ml-1 h-3 w-3' />
                </a>
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
