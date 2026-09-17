import { startPolling } from '@/pro/shared/polling';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconRefreshCw } from '@/components/ui/icons';
import { routingPolicyApi, type SchedulingBoardAccount, type SchedulingBoardResponse } from '@/pro/modules/routing/routingPolicy';
import { buildInspectionFocusLocationState } from '@/pro/shared/inspectionNavigation';
import { ProFeatureTabs } from '@/pro/shared/ProFeatureTabs';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import { ProInformationDetails, type ProInformationDetailsTone } from '@/pro/shared/ProInformationDetails';
import { useProSurfaceState } from '@/pro/shared/useProSurfaceState';
import { useAuthStore, useNotificationStore } from '@/stores';
import styles from './RoutingPolicyPage.module.scss';

type SchedulingBoardView = 'all' | 'quota' | 'authTransient' | 'recheck' | 'overlap';

const VIEW_KEYS: SchedulingBoardView[] = ['all', 'quota', 'authTransient', 'recheck', 'overlap'];

const formatTimestamp = (value: number | undefined, locale: string, emptyText: string): string => {
  if (!value) return emptyText;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return emptyText;
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
};

const remainingLabel = (
  account: SchedulingBoardAccount,
  t: ReturnType<typeof useTranslation>['t']
): string => {
  if (!account.retryAt || (account.remainingSeconds ?? 0) <= 0) {
    if (account.resume === 'recheck-quota') return t('routing_policy.runtime.due_recheck');
    if (account.resume === 'probe-request') return t('routing_policy.runtime.due_probe');
    if (!account.retryAt) return t('routing_policy.runtime.manual');
    return t('routing_policy.runtime.due_now');
  }
  const remaining = account.remainingSeconds ?? 0;
  if (remaining < 60) return t('routing_policy.runtime.remaining_seconds', { count: remaining });
  const minutes = Math.ceil(remaining / 60);
  return t('routing_policy.runtime.remaining_minutes', { count: minutes });
};

function SchedulingBoardDetailPanel({
  account,
  t,
  language,
}: {
  account: SchedulingBoardAccount;
  t: ReturnType<typeof useTranslation>['t'];
  language: string;
}) {
  const accountName = account.fileName || account.authIndex || account.authId || '-';
  const tone: ProInformationDetailsTone = account.kind === 'quota' ? 'warning' : account.kind === 'auth' ? 'danger' : 'neutral';
  return (
    <ProInformationDetails
      tone={tone}
      status={t(`routing_policy.resume.${account.resume}`, { defaultValue: account.resume })}
      context={t(`routing_policy.buckets.${account.bucket}`, { defaultValue: account.bucket })}
      summary={accountName}
      groups={[
        {
          title: t('routing_policy.runtime.account'),
          items: [
            { label: t('routing_policy.runtime.provider'), value: account.provider || '-' },
            { label: t('routing_policy.runtime.account'), value: accountName },
            { label: t('routing_policy.runtime.auth_index'), value: account.authIndex || '-' },
            { label: t('routing_policy.runtime.auth_id'), value: account.authId || '-' },
          ],
        },
        {
          title: t('routing_policy.runtime.restriction'),
          items: [
            { label: t('routing_policy.runtime.scope'), value: t(`routing_policy.scopes.${account.scope}`, { defaultValue: account.scope }) },
            { label: t('routing_policy.runtime.models'), value: account.models?.join(', ') || t('routing_policy.runtime.all_models') },
            { label: t('routing_policy.runtime.resume'), value: t(`routing_policy.resume.${account.resume}`, { defaultValue: account.resume }) },
            { label: t('routing_policy.runtime.retry_at'), value: formatTimestamp(account.retryAt, language, t('routing_policy.runtime.manual')) },
          ],
        },
      ]}
      detailLabel={t('routing_policy.runtime.reason_details')}
      detail={(
        <div className={styles.detailList}>
          {account.details.map((detail, index) => (
            <pre key={`${detail.source}-${detail.model || 'all'}-${index}`}>
              {`${t(`routing_policy.sources.${detail.source}`, { defaultValue: detail.source })} · ${t(`routing_policy.resume.${detail.resume}`, { defaultValue: detail.resume })}\n${detail.reason || '-'}`}
            </pre>
          ))}
        </div>
      )}
    />
  );
}

export function RoutingPolicyPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const [activeView, setActiveView] = useState<SchedulingBoardView>('all');
  const [data, setData] = useState<SchedulingBoardResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [runtimeError, setRuntimeError] = useState('');
  const [selectedAccount, setSelectedAccountState] = useState<SchedulingBoardAccount | null>(null);
  const { activeSurface, openSurface, closeSurface } = useProSurfaceState<'runtime-detail'>();
  const setSelectedAccount = useCallback((account: SchedulingBoardAccount | null) => {
    if (account) {
      setSelectedAccountState(account);
      openSurface('runtime-detail');
    } else if (activeSurface === 'runtime-detail') {
      closeSurface();
    }
  }, [activeSurface, closeSurface, openSurface]);

  const applyResponse = useCallback((response: SchedulingBoardResponse) => {
    setData(response);
    setRuntimeError('');
  }, []);

  const loadBoard = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      setData(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      applyResponse(await routingPolicyApi.get());
    } catch (error) {
      setRuntimeError(error instanceof Error ? error.message : String(error || ''));
      showNotification(t('routing_policy.load_failed'), 'error');
    } finally {
      setLoading(false);
    }
  }, [applyResponse, connectionStatus, showNotification, t]);

  useEffect(() => {
    void loadBoard();
  }, [loadBoard]);

  useEffect(() => {
    if (connectionStatus !== 'connected') return undefined;
    return startPolling(async () => {
      try {
        applyResponse(await routingPolicyApi.get());
      } catch (error) {
        setRuntimeError(error instanceof Error ? error.message : String(error || ''));
      }
    }, 15000);
  }, [applyResponse, connectionStatus]);

  const accounts = useMemo(() => {
    const rows = data?.accounts ?? [];
    if (activeView === 'all') return rows;
    return rows.filter((account) => account.bucket === activeView);
  }, [activeView, data?.accounts]);

  const openInspection = useCallback((account: SchedulingBoardAccount) => {
    navigate('/account-inspection', {
      state: buildInspectionFocusLocationState({
        authId: account.authId,
        authIndex: account.authIndex,
        fileName: account.fileName,
      }),
    });
  }, [navigate]);

  return (
    <div className={styles.container}>
      <section className={styles.panel}>
        <div className={styles.sectionHeaderWithAction}>
          <div>
            <h1>{t('routing_policy.title')}</h1>
            <p>{t('routing_policy.subtitle')}</p>
          </div>
          <Button variant="secondary" size="sm" onClick={() => void loadBoard()} disabled={loading}>
            <IconRefreshCw size={15} /> {t('common.refresh')}
          </Button>
        </div>
        <div className={styles.summaryGrid}>
          {(['blocked', 'quota', 'authTransient', 'recheck', 'overlap', 'excluded'] as const).map((key) => (
            <div key={key} className={styles.summaryCard}>
              <small>{t(`routing_policy.summary.${key}`)}</small>
              <strong>{data?.summary?.[key] ?? 0}</strong>
            </div>
          ))}
        </div>
        <p className={styles.nextRetry}>
          {data?.summary.nextRetryAt
            ? t('routing_policy.summary.next_retry', { time: formatTimestamp(data.summary.nextRetryAt, i18n.language, '-') })
            : t('routing_policy.summary.no_retry')}
        </p>
      </section>

      {(runtimeError || connectionStatus !== 'connected') && (
        <div role="status" className={styles.staleNotice}>
          {t('routing_policy.runtime.stale')}
          {runtimeError && <span>{runtimeError}</span>}
        </div>
      )}

      <ProFeatureTabs
        ariaLabel={t('routing_policy.title')}
        activeKey={activeView}
        onChange={(key) => setActiveView(key as SchedulingBoardView)}
        items={VIEW_KEYS.map((view) => ({
          key: view,
          label: t(`routing_policy.views.${view}`),
          badge: view === 'all' ? data?.summary.blocked : data?.summary?.[view === 'authTransient' ? 'authTransient' : view],
        }))}
      />

      <section className={styles.panel}>
        {accounts.length ? (
          <div className={`${styles.tableScroller} ${styles.runtimeTableScroller}`}>
            <table className={`${styles.table} ${styles.runtimeTable}`}>
              <thead>
                <tr>
                  <th>{t('routing_policy.runtime.provider')}</th>
                  <th>{t('routing_policy.runtime.account')}</th>
                  <th>{t('routing_policy.runtime.scope')}</th>
                  <th>{t('routing_policy.runtime.resume')}</th>
                  <th>{t('routing_policy.runtime.retry_at')}</th>
                  <th className={styles.runtimeActionHeader}>{t('routing_policy.runtime.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {accounts.map((account) => (
                  <tr key={`${account.authIndex}:${account.authId}`}>
                    <td><span className={styles.providerTag}>{account.provider}</span></td>
                    <td>
                      <button type="button" className={styles.accountButton} onClick={() => setSelectedAccount(account)}>
                        <strong title={account.fileName || account.authIndex}>{account.fileName || account.authIndex}</strong>
                        <small>{t(`routing_policy.buckets.${account.bucket}`, { defaultValue: account.bucket })}</small>
                      </button>
                    </td>
                    <td>{account.models?.join(', ') || t('routing_policy.runtime.all_models')}</td>
                    <td>{t(`routing_policy.resume.${account.resume}`, { defaultValue: account.resume })}</td>
                    <td>{remainingLabel(account, t)}</td>
                    <td>
                      <Button variant="secondary" size="sm" onClick={() => openInspection(account)}>
                        {t('routing_policy.runtime.open_inspection')}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className={styles.emptyState}>
            <p>{loading ? t('common.loading') : t('routing_policy.runtime.no_active')}</p>
          </div>
        )}
      </section>

      <ProDetailDialog
        open={activeSurface === 'runtime-detail'}
        title={t('routing_policy.runtime.details_title')}
        onClose={() => setSelectedAccount(null)}
        onAfterClose={() => setSelectedAccountState(null)}
      >
        {selectedAccount ? <SchedulingBoardDetailPanel account={selectedAccount} t={t} language={i18n.language} /> : null}
      </ProDetailDialog>
    </div>
  );
}
