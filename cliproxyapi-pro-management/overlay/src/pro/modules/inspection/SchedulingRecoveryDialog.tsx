import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import {
  SchedulingRecoveryActions,
  routingPolicyApi,
  schedulingRecoveryResultTone,
  type SchedulingBoardAccount,
  type SchedulingRecoveryResult,
} from '@/pro/modules/routing';

type Props = {
  open: boolean;
  authId: string;
  authIndex: string;
  accountName: string;
  onClose: () => void;
  onResult?: (result: SchedulingRecoveryResult) => void | Promise<void>;
};

export function SchedulingRecoveryDialog({ open, authId, authIndex, accountName, onClose, onResult }: Props) {
  const { t } = useTranslation();
  const [account, setAccount] = useState<SchedulingBoardAccount | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [outcome, setOutcome] = useState<SchedulingRecoveryResult | null>(null);
  const requestIdRef = useRef(0);

  useEffect(() => {
    if (!open || !authId) return;
    const controller = new AbortController();
    const requestId = ++requestIdRef.current;
    setLoading(true);
    setOutcome(null);
    void routingPolicyApi.get(controller.signal).then((board) => {
      if (!controller.signal.aborted && requestId === requestIdRef.current) {
        setAccount(board.accounts.find((item) => item.authId === authId && item.authIndex === authIndex) ?? null);
        setError('');
      }
    }).catch((cause) => {
      if (!controller.signal.aborted && requestId === requestIdRef.current) setError(cause instanceof Error ? cause.message : String(cause));
    }).finally(() => {
      if (!controller.signal.aborted && requestId === requestIdRef.current) setLoading(false);
    });
    return () => {
      controller.abort();
      requestIdRef.current += 1;
    };
  }, [open, authId, authIndex]);

  const handleResult = async (result: SchedulingRecoveryResult) => {
    const requestId = requestIdRef.current;
    setOutcome(result);
    await Promise.all([
      routingPolicyApi.get().then((board) => {
        if (requestId === requestIdRef.current) {
          setAccount(board.accounts.find((item) => item.authId === authId && item.authIndex === authIndex) ?? null);
        }
      }),
      Promise.resolve(onResult?.(result)),
    ]);
  };

  return (
    <ProDetailDialog open={open} title={`${t('routing_policy.recovery.title')} · ${accountName}`} onClose={onClose}>
      {loading ? <p>{t('common.loading')}</p> : error ? <p role="alert">{error}</p> : account ? (
        <>
          <p>{t('routing_policy.recovery.current_reason')}: {account.reason || '-'}</p>
          <p>{t('routing_policy.runtime.models')}: {account.models?.join(', ') || t('routing_policy.runtime.all_models')}</p>
          <SchedulingRecoveryActions account={account} onResult={handleResult} />
        </>
      ) : <p role="status">{outcome
        ? t(schedulingRecoveryResultTone(outcome) === 'error'
          ? 'routing_policy.recovery.failed'
          : outcome.after?.authId
          ? 'routing_policy.recovery.still_restricted'
          : 'routing_policy.recovery.restored')
        : t('routing_policy.recovery.no_current_block')}</p>}
    </ProDetailDialog>
  );
}
