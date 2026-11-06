import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ProInformationDetails } from '@/pro/shared/ProInformationDetails';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import {
  SchedulingRecoveryActions,
  SchedulingRecoveryOutcome,
  routingPolicyApi,
  formatTimestamp,
  schedulingBoardModelsLabel,
  type SchedulingBoardAccount,
  type SchedulingRecoveryResult,
} from '@/pro/modules/routing';

type Props = {
  open: boolean;
  actionsDisabled?: boolean;
  authId: string;
  authIndex: string;
  accountName: string;
  onClose: () => void;
  onResult?: (result?: SchedulingRecoveryResult) => void | Promise<void>;
};

export function SchedulingRecoveryDialog({ open, actionsDisabled = false, authId, authIndex, accountName, onClose, onResult }: Props) {
  const { t, i18n } = useTranslation();
  // A close/reopen of the same account is a new operation session.
  const session = useMemo(() => ({ open, authId, authIndex }), [open, authId, authIndex]);
  const activeSessionRef = useRef(session);
  useLayoutEffect(() => {
    activeSessionRef.current = session;
  }, [session]);
  const [account, setAccount] = useState<{ session: object; value: SchedulingBoardAccount | null } | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<{ session: object; message: string } | null>(null);
  const [outcome, setOutcome] = useState<{ session: object; value: SchedulingRecoveryResult } | null>(null);
  const requestIdRef = useRef(0);

  useEffect(() => {
    if (!open || !authId) return;
    const controller = new AbortController();
    const requestId = ++requestIdRef.current;
    setLoading(true);
    setOutcome(null);
    setError(null);
    void routingPolicyApi.get(controller.signal).then((board) => {
      if (!controller.signal.aborted && requestId === requestIdRef.current && activeSessionRef.current === session) {
        setAccount({ session, value: board.accounts.find((item) => item.authId === authId && item.authIndex === authIndex) ?? null });
        setError(null);
      }
    }).catch((cause) => {
      if (!controller.signal.aborted && requestId === requestIdRef.current && activeSessionRef.current === session) {
        setError({ session, message: cause instanceof Error ? cause.message : String(cause) });
      }
    }).finally(() => {
      if (!controller.signal.aborted && requestId === requestIdRef.current && activeSessionRef.current === session) setLoading(false);
    });
    return () => {
      controller.abort();
      requestIdRef.current += 1;
    };
  }, [open, authId, authIndex, session]);

  const handleResult = async (result?: SchedulingRecoveryResult) => {
    if (!open || activeSessionRef.current !== session) return;
    if (result) setOutcome({ session, value: result });
    let refreshError: unknown;
    try {
      const board = await routingPolicyApi.get();
      if (activeSessionRef.current !== session) return;
      setAccount({ session, value: board.accounts.find((item) => item.authId === authId && item.authIndex === authIndex) ?? null });
    } catch (cause) {
      refreshError = cause;
    }
    if (activeSessionRef.current !== session) return;
    await onResult?.(result);
    if (refreshError) throw refreshError;
  };

  const currentAccount = open && account?.session === session && account.value?.authId === authId && account.value.authIndex === authIndex
    ? account.value : null;
  const currentOutcome = open && outcome?.session === session ? outcome.value : null;
  const currentError = open && error?.session === session ? error.message : '';
  const awaitingCurrentAccount = open && (!account || account.session !== session);

  return (
    <ProDetailDialog open={open} title={`${t('routing_policy.recovery.title')} · ${accountName}`} onClose={onClose}>
      {currentError ? <p role="alert">{currentError}</p> : loading || awaitingCurrentAccount ? <p>{t('common.loading')}</p> : currentAccount ? (
        <>
          <ProInformationDetails
            layout="compact"
            status={t('routing_policy.recovery.current_reason')}
            summary={t(`routing_policy.reasons.${currentAccount.reason}`, { defaultValue: currentAccount.reason || '-' })}
            groups={[{
              title: t('routing_policy.runtime.restriction'),
              items: [
                { label: t('routing_policy.runtime.models'), value: schedulingBoardModelsLabel(currentAccount, t('routing_policy.runtime.all_models')) },
                { label: t('routing_policy.runtime.next_action_at'), value: formatTimestamp(currentAccount.nextActionAt, i18n.language, t('routing_policy.runtime.not_scheduled')) },
                { label: t('routing_policy.runtime.next_transition_at'), value: formatTimestamp(currentAccount.nextTransitionAt, i18n.language, t('routing_policy.runtime.not_scheduled')) },
              ],
            }]}
          />
          <section aria-label={t('routing_policy.runtime.reason_details')}>
            <h3>{t('routing_policy.runtime.reason_details')}</h3>
            {currentAccount.details.map((detail, index) => (
              <ProInformationDetails
                layout="compact"
                key={`${detail.source}-${detail.model || 'all'}-${index}`}
                status={t(`routing_policy.sources.${detail.source}`, { defaultValue: detail.source })}
                context={detail.httpStatus ? `HTTP ${detail.httpStatus}` : undefined}
                summary={t(`routing_policy.reasons.${detail.reason}`, { defaultValue: detail.reason || '-' })}
                groups={[{
                  title: t('routing_policy.runtime.restriction'),
                  items: [
                    { label: t('routing_policy.runtime.scope'), value: t(`routing_policy.scopes.${detail.scope}`, { defaultValue: detail.scope }) },
                    { label: t('routing_policy.runtime.models'), value: detail.scope === 'credential' ? t('routing_policy.runtime.all_models') : detail.model || '-' },
                    { label: t('routing_policy.runtime.resume'), value: t(`routing_policy.resume.${detail.resume}`, { defaultValue: detail.resume }) },
                    { label: t('routing_policy.runtime.retry_at'), value: formatTimestamp(detail.retryAt, i18n.language, t('routing_policy.runtime.not_scheduled')) },
                  ],
                }]}
              />
            ))}
          </section>
          <SchedulingRecoveryActions account={currentAccount} onResult={handleResult} disabled={actionsDisabled} />
        </>
      ) : currentOutcome
        ? <SchedulingRecoveryOutcome result={currentOutcome} />
        : <p role="status">{t('routing_policy.recovery.no_current_block')}</p>}
    </ProDetailDialog>
  );
}
