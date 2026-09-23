import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import {
  routingPolicyApi,
  schedulingRecoveryResultTone,
  type SchedulingBoardAccount,
  type SchedulingBoardDetail,
  type SchedulingRecoveryResult,
} from './routingPolicy';
import styles from './SchedulingRecoveryActions.module.scss';

type Props = {
  account: SchedulingBoardAccount;
  onResult: (result: SchedulingRecoveryResult) => void | Promise<void>;
};

const restrictionKey = (detail: SchedulingBoardDetail) =>
  `${detail.source}:${detail.scope}:${detail.model || ''}:${detail.revision || ''}`;

export function SchedulingRecoveryActions({ account, onResult }: Props) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [confirmKey, setConfirmKey] = useState<string | null>(null);
  const [result, setResult] = useState<SchedulingRecoveryResult | null>(null);
  const resultTone = result ? schedulingRecoveryResultTone(result) : null;

  if (!account.registrationEpoch) {
    return <p>{t('routing_policy.recovery.unavailable_version')}</p>;
  }

  const requestBase = {
    authId: account.authId,
    authIndex: account.authIndex,
    registrationEpoch: account.registrationEpoch || '',
  };
  const run = async (operation: () => Promise<SchedulingRecoveryResult>) => {
    setBusy(true);
    setError('');
    try {
      const response = await operation();
      setResult(response);
      setConfirmKey(null);
      try {
        await onResult(response);
      } catch (refreshError) {
        setError(refreshError instanceof Error ? refreshError.message : t('routing_policy.recovery.refresh_failed'));
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('routing_policy.recovery.failed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className={styles.recovery} aria-label={t('routing_policy.recovery.title')}>
      <h3>{t('routing_policy.recovery.title')}</h3>
      <p>{t('routing_policy.recovery.explanation')}</p>
      <Button
        variant="primary"
        size="sm"
        loading={busy}
        disabled={busy}
        onClick={() => void run(() => routingPolicyApi.check(requestBase))}
      >
        {t('routing_policy.recovery.check')}
      </Button>
      <small>{t('routing_policy.recovery.request_notice')}</small>
      {result ? (
        <p className={styles[resultTone!]} role="status">
          {resultTone === 'error'
            ? t('routing_policy.recovery.failed')
            : result.after?.authId
            ? t('routing_policy.recovery.still_restricted')
            : t('routing_policy.recovery.restored')}
          {result.test && !result.test.success && result.test.error
            ? ` · ${result.test.error}`
            : null}
        </p>
      ) : null}
      {error ? <p className={styles.error} role="alert">{error}</p> : null}
      <details className={styles.manual}>
        <summary>{t('routing_policy.recovery.manual_title')}</summary>
        <p>{t('routing_policy.recovery.manual_notice')}</p>
        {account.details.filter((detail) => detail.revision && (
          detail.source === 'inspection' || detail.source === 'upstream' && Boolean(detail.retryAt)
        )).map((detail) => {
          const key = restrictionKey(detail);
          const label = `${t(`routing_policy.sources.${detail.source}`, { defaultValue: detail.source })} · ${detail.model || t('routing_policy.runtime.all_models')}`;
          return (
            <div className={styles.manualRow} key={key}>
              <span>{label}</span>
              {confirmKey === key ? (
                <span className={styles.buttons}>
                  <Button
                    variant="danger"
                    size="sm"
                    disabled={busy}
                    loading={busy}
                    onClick={() => void run(() => routingPolicyApi.release({
                      ...requestBase,
                      source: detail.source as 'inspection' | 'upstream',
                      model: detail.model || '',
                      revision: detail.revision,
                    }))}
                  >
                    {t('routing_policy.recovery.confirm_release')}
                  </Button>
                  <Button variant="ghost" size="sm" disabled={busy} onClick={() => setConfirmKey(null)}>
                    {t('common.cancel')}
                  </Button>
                </span>
              ) : (
                <Button variant="secondary" size="sm" disabled={busy} onClick={() => setConfirmKey(key)}>
                  {t('routing_policy.recovery.release')}
                </Button>
              )}
            </div>
          );
        })}
      </details>
    </section>
  );
}
