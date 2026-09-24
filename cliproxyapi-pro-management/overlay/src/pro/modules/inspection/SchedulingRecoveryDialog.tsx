import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import {
  SchedulingRecoveryActions,
  SchedulingRecoveryOutcome,
  routingPolicyApi,
  type SchedulingBoardAccount,
  type SchedulingRecoveryResult,
} from '@/pro/modules/routing';

type Props = {
  open: boolean;
  authId: string;
  authIndex: string;
  accountName: string;
  onClose: () => void;
  onResult?: (result?: SchedulingRecoveryResult) => void | Promise<void>;
};

export function SchedulingRecoveryDialog({ open, authId, authIndex, accountName, onClose, onResult }: Props) {
  const { t } = useTranslation();
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
          <p>{t('routing_policy.recovery.current_reason')}: {currentAccount.reason || '-'}</p>
          <p>{t('routing_policy.runtime.models')}: {currentAccount.models?.join(', ') || t('routing_policy.runtime.all_models')}</p>
          <SchedulingRecoveryActions account={currentAccount} onResult={handleResult} />
        </>
      ) : currentOutcome
        ? <SchedulingRecoveryOutcome result={currentOutcome} />
        : <p role="status">{t('routing_policy.recovery.no_current_block')}</p>}
    </ProDetailDialog>
  );
}
