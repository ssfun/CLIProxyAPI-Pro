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
  onResult: (result?: SchedulingRecoveryResult) => void | Promise<void>;
};

const restrictionKey = (detail: SchedulingBoardDetail) =>
  `${detail.source}:${detail.scope}:${detail.model || ''}:${detail.revision || ''}`;

export function SchedulingRecoveryOutcome({ result }: { result: SchedulingRecoveryResult }) {
  const { t } = useTranslation();
  const resultTone = schedulingRecoveryResultTone(result);
  return (
    <div className={styles.outcome} role="status">
      <p className={styles[resultTone]}>
        {resultTone === 'error'
          ? t('routing_policy.recovery.failed')
          : result.after?.authId
          ? t('routing_policy.recovery.still_restricted')
          : t('routing_policy.recovery.restored')}
      </p>
      {result.phases?.map((phase, index) => (
        <p key={`${phase.source}:${phase.model || ''}:${index}`} className={styles[phase.status === 'completed' ? 'success' : phase.status === 'skipped' ? 'warning' : 'error']}>
          {t(`routing_policy.recovery.phase_${phase.source}`)}
          {phase.model ? ` · ${phase.model}` : ''}: {t(`routing_policy.recovery.phase_${phase.status}`)}
          {phase.error ? ` · ${phase.error}` : ''}
        </p>
      ))}
      {!result.phases?.length && result.test && !result.test.success && result.test.error ? (
        <p className={styles.error}>{result.test.error}</p>
      ) : null}
      {result.after?.reason ? (
        <p>{t('routing_policy.recovery.current_reason')}: {result.after.reason}</p>
      ) : null}
    </div>
  );
}

export function SchedulingRecoveryActions({ account, onResult }: Props) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [confirmKey, setConfirmKey] = useState<string | null>(null);
  const [result, setResult] = useState<SchedulingRecoveryResult | null>(null);

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
    setResult(null);
    let response: SchedulingRecoveryResult | undefined;
    try {
      response = await operation();
      setResult(response);
      setConfirmKey(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('routing_policy.recovery.failed'));
    } finally {
      try {
        await onResult(response);
      } catch (refreshError) {
        setError(refreshError instanceof Error ? refreshError.message : t('routing_policy.recovery.refresh_failed'));
      }
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
      {result ? <SchedulingRecoveryOutcome result={result} /> : null}
      {error ? <p className={styles.error} role="alert">{error}</p> : null}
      <div className={styles.targeted}>
        <h4>{t('routing_policy.recovery.targeted_title')}</h4>
        {account.details.filter((detail) => detail.source === 'inspection' || detail.source === 'upstream').map((detail, index) => {
          const key = `${restrictionKey(detail)}:${index}`;
          const label = `${t(`routing_policy.sources.${detail.source}`, { defaultValue: detail.source })} · ${detail.model || t('routing_policy.runtime.all_models')}`;
          return (
            <div className={styles.manualRow} key={key}>
              <span className={styles.detailLabel}>
                <strong>{label}</strong>
                <small>{t(`routing_policy.reasons.${detail.reason}`, { defaultValue: detail.reason || '-' })}</small>
              </span>
              <Button
                variant="secondary"
                size="sm"
                disabled={busy}
                onClick={() => void run(() => routingPolicyApi.check({
                  ...requestBase,
                  source: detail.source as 'inspection' | 'upstream',
                  model: detail.model || '',
                  revision: detail.revision,
                }))}
              >
                {t(detail.source === 'inspection'
                  ? 'routing_policy.recovery.check_quota'
                  : 'routing_policy.recovery.verify_request')}
              </Button>
            </div>
          );
        })}
      </div>
      <details className={styles.manual}>
        <summary>{t('routing_policy.recovery.manual_title')}</summary>
        <p>{t('routing_policy.recovery.manual_notice')}</p>
        {account.details.filter((detail) => detail.revision && (
          detail.source === 'inspection' || detail.source === 'upstream' && Boolean(detail.retryAt)
        )).map((detail, index) => {
          const key = `${restrictionKey(detail)}:${index}`;
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
