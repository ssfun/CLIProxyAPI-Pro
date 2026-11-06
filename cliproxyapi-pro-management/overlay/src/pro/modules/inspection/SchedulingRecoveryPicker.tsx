import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import styles from './SchedulingRecoveryPicker.module.scss';
import { Button } from '@/components/ui/Button';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import { routingPolicyApi, type SchedulingBoardAccount } from '@/pro/modules/routing';
import { SchedulingRecoveryDialog } from './SchedulingRecoveryDialog';

type Props = { open: boolean; disabled: boolean; onClose: () => void };

// Never display fileName/authId: API-key identities can contain credential material.
const accountLabel = (account: SchedulingBoardAccount) => `${account.provider} · #${account.authIndex}`;

export function SchedulingRecoveryPicker({ open, disabled, onClose }: Props) {
  const { t } = useTranslation();
  const [refresh, setRefresh] = useState(0);
  const session = useMemo(() => ({ open, refresh }), [open, refresh]);
  const activeSession = useRef(session);
  useLayoutEffect(() => { activeSession.current = session; }, [session]);
  const [state, setState] = useState<{ session: object; accounts: SchedulingBoardAccount[]; error?: string } | null>(null);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<SchedulingBoardAccount | null>(null);
  useEffect(() => {
    setSelected(null);
    setSearch('');
    if (!open) return;
    const controller = new AbortController();
    void routingPolicyApi.get(controller.signal).then((board) => {
      if (!controller.signal.aborted && activeSession.current === session) setState({ session, accounts: board.accounts });
    }).catch((error) => {
      if (!controller.signal.aborted && activeSession.current === session) setState({ session, accounts: [], error: error instanceof Error ? error.message : String(error) });
    });
    return () => controller.abort();
  }, [open, session]);
  const current = state?.session === session ? state : null;
  const accounts = current?.accounts.filter((account) => accountLabel(account).toLowerCase().includes(search.trim().toLowerCase())) ?? [];
  return <>
    <ProDetailDialog open={open && !selected} title={t('routing_policy.recovery.picker_title')} onClose={onClose}>
      <p>{t('routing_policy.recovery.picker_hint')}</p>
      <Input aria-label={t('routing_policy.recovery.picker_search')} placeholder={t('routing_policy.recovery.picker_search')} value={search} onChange={(event) => setSearch(event.target.value)} />
      <Button variant="secondary" onClick={() => setRefresh((value) => value + 1)} disabled={!current}>{t('common.refresh')}</Button>
      {!current ? <p role="status">{t('common.loading')}</p> : current.error ? <p role="alert">{current.error}</p> : accounts.length === 0 ? <p role="status">{t('routing_policy.recovery.picker_empty')}</p> : <ul className={styles.accounts}>
        {accounts.map((account) => <li key={`${account.authId}:${account.authIndex}`}>
          <Button variant="secondary" onClick={() => setSelected(account)}>{accountLabel(account)}</Button>
        </li>)}
      </ul>}
    </ProDetailDialog>
    <SchedulingRecoveryDialog open={open && Boolean(selected)} actionsDisabled={disabled} authId={selected?.authId ?? ''} authIndex={selected?.authIndex ?? ''} accountName={selected ? accountLabel(selected) : ''} onClose={() => { setSelected(null); setRefresh((value) => value + 1); }} />
  </>;
}
