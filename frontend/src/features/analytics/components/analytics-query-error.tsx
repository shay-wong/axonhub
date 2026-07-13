import { AlertCircle, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';

interface AnalyticsQueryErrorProps {
  error: unknown;
  onRetry: () => void;
}

export function AnalyticsQueryError({ error, onRetry }: AnalyticsQueryErrorProps) {
  const { t } = useTranslation();
  const detail = error instanceof Error ? error.message : null;

  return (
    <Card className='border-destructive/50'>
      <CardContent className='flex min-h-32 flex-col items-center justify-center gap-3 py-6 text-center'>
        <AlertCircle className='text-destructive h-6 w-6' />
        <div className='space-y-1'>
          <p className='font-medium'>{t('common.errors.loadFailed')}</p>
          {detail && <p className='text-muted-foreground max-w-2xl text-sm'>{detail}</p>}
        </div>
        <Button type='button' variant='outline' size='sm' onClick={onRetry}>
          <RefreshCw className='h-4 w-4' />
          {t('common.buttons.retry')}
        </Button>
      </CardContent>
    </Card>
  );
}
