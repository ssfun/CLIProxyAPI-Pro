import {
  createContext,
  useContext,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type PropsWithChildren,
} from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { EmptyState } from '@/components/ui/EmptyState';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from '@/components/ui/Table';
import { IconCheck, IconRefreshCw, IconSearch, IconX } from '@/components/ui/icons';
import { IconCopy } from '@/pro/icons';
import { copyToClipboard } from '@/utils/clipboard';
import {
  formatRemainingTime,
  formatTimeOnly,
  formatTimestamp,
  routingPolicyApi,
  schedulingBoardModelsLabel,
  schedulingRecoveryResultTone,
  schedulingBoardResumeTone,
  type SchedulingBoardAccount,
  type SchedulingBoardResponse,
  type SchedulingRecoveryResult,
} from '@/pro/modules/routing/routingPolicy';
import { SchedulingBoardQuickActions } from './SchedulingRecoveryActions';
import { useRoutingAccountPlans } from './useRoutingAccountPlans';
import { createLatestRequestGate } from '@/pro/modules/routing/latestRequestGate';
import { buildInspectionFocusLocationState } from '@/pro/shared/inspectionNavigation';
import {
  DEFAULT_PRO_PAGE_SIZE,
  PRO_PAGE_SIZE_OPTIONS,
  type ProPageSize,
} from '@/pro/shared/pagination';
import { ProPagination } from '@/pro/shared/ProPagination';
import { startPolling } from '@/pro/shared/polling';
import { ProFeatureTabs } from '@/pro/shared/ProFeatureTabs';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import {
  ProInformationDetails,
  type ProInformationDetailsTone,
} from '@/pro/shared/ProInformationDetails';
import { useProSurfaceState } from '@/pro/shared/useProSurfaceState';
import { useAuthStore, useNotificationStore } from '@/stores';
import styles from './RoutingPolicyPage.module.scss';

type SchedulingBoardView = 'all' | 'quota' | 'authTransient' | 'recheck' | 'overlap';

const VIEW_KEYS: SchedulingBoardView[] = ['all', 'quota', 'authTransient', 'recheck', 'overlap'];
function BoardSources({ account }: { account: SchedulingBoardAccount }) {
  const { t } = useTranslation();
  return (
    <div className={styles.sourceTagGroup}>
      {account.sources.map((source) => (
        <span
          key={source}
          className={
            source === 'inspection' ? styles.sourceTagInspection : styles.sourceTagUpstream
          }
        >
          {t(`routing_policy.sources.${source}`, { defaultValue: source })}
        </span>
      ))}
    </div>
  );
}

function BoardScope({ account }: { account: SchedulingBoardAccount }) {
  const { t } = useTranslation();
  if (account.scope === 'credential' || !account.models?.length) {
    return (
      <span className={styles.scopeTagCredential}>
        {t(
          account.scope === 'credential'
            ? 'routing_policy.runtime.all_models'
            : 'routing_policy.scopes.model'
        )}
      </span>
    );
  }
  return (
    <div className={styles.modelChipGroup}>
      {account.models.slice(0, 2).map((model) => (
        <code key={model} className={styles.modelChip} title={model}>
          {model}
        </code>
      ))}
      {account.models.length > 2 ? (
        <span className={styles.moreModelsBadge} title={account.models.slice(2).join(', ')}>
          {t('routing_policy.runtime.models_more', {
            count: account.models.length - 2,
          })}
        </span>
      ) : null}
    </div>
  );
}

function BoardResume({ account }: { account: SchedulingBoardAccount }) {
  const { t } = useTranslation();
  return (
    <span
      className={`${styles.resumeTag} ${styles[`resumeTag_${schedulingBoardResumeTone(account.resume)}`]}`}
    >
      {t(`routing_policy.resume.${account.resume}`, {
        defaultValue: account.resume,
      })}
    </span>
  );
}

const BoardTimeContext = createContext(0);

function BoardClock({ children }: PropsWithChildren) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  return <BoardTimeContext.Provider value={now}>{children}</BoardTimeContext.Provider>;
}

function BoardCountdown({ at, resume }: { at: number; resume: string }) {
  const { t } = useTranslation();
  const now = useContext(BoardTimeContext);
  return <>{formatRemainingTime(at, resume, t, '-', now)}</>;
}

function BoardIdentity({
  account,
  copied,
  planLabel,
  onSelect,
  onCopy,
}: {
  account: SchedulingBoardAccount;
  copied: boolean;
  planLabel: string;
  onSelect: () => void;
  onCopy: (event: MouseEvent) => void;
}) {
  const { t } = useTranslation();
  const name = account.fileName || account.authIndex || account.authId;
  return (
    <div className={styles.accountCell}>
      <div className={styles.accountRow}>
        <button type="button" className={styles.accountButton} onClick={onSelect} title={name}>
          {name}
        </button>
        <button
          type="button"
          className={styles.copyButton}
          onClick={onCopy}
          title={t(
            copied ? 'routing_policy.runtime.copied' : 'routing_policy.runtime.copy_auth_id'
          )}
          aria-label={t('routing_policy.runtime.copy_auth_id')}
        >
          {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
        </button>
      </div>
      <div className={styles.accountMeta}>
        <span className={styles.providerName}>
          {t(`routing_policy.providers.${account.provider}`, { defaultValue: account.provider })}
        </span>
        <span
          className={styles.planBadge}
          title={`${t('routing_policy.runtime.plan')}: ${planLabel}`}
        >
          {planLabel}
        </span>
      </div>
    </div>
  );
}

function BoardActions({
  account,
  onSelect,
  onRecovered,
  onRecoveryError,
}: {
  account: SchedulingBoardAccount;
  onSelect: () => void;
  onRecovered: (result?: SchedulingRecoveryResult) => void | Promise<void>;
  onRecoveryError: (error: unknown) => void | Promise<void>;
}) {
  const { t } = useTranslation();
  return (
    <div
      className={styles.rowActions}
      data-routing-row-actions
      data-routing-auth-id={account.authId}
    >
      <Button data-routing-action="details" variant="ghost" size="sm" onClick={onSelect}>
        {t('routing_policy.runtime.details_short')}
      </Button>
      <SchedulingBoardQuickActions
        account={account}
        onResult={onRecovered}
        onError={onRecoveryError}
      />
    </div>
  );
}

function BoardRestriction({ account }: { account: SchedulingBoardAccount }) {
  const { t } = useTranslation();
  return (
    <div className={styles.restrictionCell}>
      <BoardSources account={account} />
      <div className={styles.scopeLine}>
        <span className={styles.metaLabel}>{t('routing_policy.runtime.affected_scope')}</span>
        <BoardScope account={account} />
      </div>
    </div>
  );
}

function BoardAccountTimes({ account }: { account: SchedulingBoardAccount }) {
  const { t, i18n } = useTranslation();
  const actionDetails = account.details.filter((detail) => detail.retryAt === account.nextActionAt);
  const actionKind =
    actionDetails.length && actionDetails.every((detail) => detail.resume === 'recheck-quota')
      ? 'recheck_short'
      : actionDetails.length && actionDetails.every((detail) => detail.resume === 'probe-request')
        ? 'probe_short'
        : 'action_short';
  const events = [
    { at: account.nextActionAt, label: actionKind, resume: 'action' },
    { at: account.nextTransitionAt, label: 'transition_short', resume: 'auto-expire' },
  ].filter((event) => event.at && event.at > 0);
  return (
    <div className={styles.recoveryCell}>
      {events.length ? (
        events.map(({ at, label, resume }) => (
          <div className={styles.recoveryEvent} key={label}>
            <span className={styles.eventLabel}>{t(`routing_policy.runtime.${label}`)}</span>
            <div className={styles.eventValue}>
              <time
                dateTime={new Date(at!).toISOString()}
                title={formatTimestamp(at, i18n.language, '-')}
              >
                {formatTimestamp(at, i18n.language, '-', true)}
              </time>
              <span className={styles.eventCountdown}>
                <BoardCountdown at={at!} resume={resume} />
              </span>
            </div>
          </div>
        ))
      ) : (
        <span className={styles.noSchedule}>{t('routing_policy.runtime.not_scheduled')}</span>
      )}
    </div>
  );
}

function BoardRecovery({ account }: { account: SchedulingBoardAccount }) {
  return (
    <div className={styles.recoveryGroup}>
      <BoardResume account={account} />
      <BoardAccountTimes account={account} />
    </div>
  );
}

function SchedulingBoardDetailPanel({
  account,
  planLabel,
  t,
  language,
  onOpenInspection,
}: {
  account: SchedulingBoardAccount;
  planLabel: string;
  t: ReturnType<typeof useTranslation>['t'];
  language: string;
  onOpenInspection?: (account: SchedulingBoardAccount) => void;
}) {
  const accountName = account.fileName || account.authIndex || account.authId || '-';
  const tone: ProInformationDetailsTone =
    account.kind === 'quota' ? 'warning' : account.kind === 'auth' ? 'danger' : 'neutral';

  return (
    <div className={styles.detailContainer}>
      <ProInformationDetails
        tone={tone}
        status={t(`routing_policy.resume.${account.resume}`, {
          defaultValue: account.resume,
        })}
        context={t(`routing_policy.buckets.${account.bucket}`, {
          defaultValue: account.bucket,
        })}
        summary={accountName}
        groups={[
          {
            title: t('routing_policy.runtime.account'),
            items: [
              {
                label: t('routing_policy.runtime.provider'),
                value: account.provider || '-',
              },
              { label: t('routing_policy.runtime.plan'), value: planLabel },
              {
                label: t('routing_policy.runtime.account'),
                value: accountName,
              },
              {
                label: t('routing_policy.runtime.auth_index'),
                value: account.authIndex || '-',
              },
              {
                label: t('routing_policy.runtime.auth_id'),
                value: account.authId || '-',
              },
            ],
          },
          {
            title: t('routing_policy.runtime.restriction'),
            items: [
              {
                label: t('routing_policy.runtime.scope'),
                value: t(`routing_policy.scopes.${account.scope}`, {
                  defaultValue: account.scope,
                }),
              },
              {
                label: t('routing_policy.runtime.models'),
                value: schedulingBoardModelsLabel(account, t('routing_policy.runtime.all_models')),
              },
              {
                label: t('routing_policy.runtime.resume'),
                value: t(`routing_policy.resume.${account.resume}`, {
                  defaultValue: account.resume,
                }),
              },
              {
                label: t('routing_policy.runtime.next_action_at'),
                value: formatTimestamp(
                  account.nextActionAt,
                  language,
                  t('routing_policy.runtime.not_scheduled')
                ),
              },
              {
                label: t('routing_policy.runtime.next_transition_at'),
                value: formatTimestamp(
                  account.nextTransitionAt,
                  language,
                  t('routing_policy.runtime.not_scheduled')
                ),
              },
            ],
          },
        ]}
      />
      <section className={styles.detailReasons}>
        <h3>{t('routing_policy.runtime.reason_details')}</h3>
        <div className={styles.detailList}>
          {account.details.map((detail, index) => {
            const isInspection = detail.source === 'inspection';
            return (
              <div
                key={`${detail.source}-${detail.model || 'all'}-${index}`}
                className={styles.detailItemCard}
              >
                <div className={styles.detailItemHeader}>
                  <div className={styles.detailBadges}>
                    <span
                      className={
                        isInspection ? styles.sourceTagInspection : styles.sourceTagUpstream
                      }
                    >
                      {t(`routing_policy.sources.${detail.source}`, {
                        defaultValue: detail.source,
                      })}
                    </span>
                    {detail.httpStatus ? (
                      <span className={styles.detailHttpStatus}>HTTP {detail.httpStatus}</span>
                    ) : null}
                    <span className={styles.detailScopeTag}>
                      {detail.scope === 'credential'
                        ? t('routing_policy.runtime.all_models')
                        : detail.model || '-'}
                    </span>
                  </div>
                  <span
                    className={`${styles.resumeTag} ${
                      styles[`resumeTag_${schedulingBoardResumeTone(detail.resume)}`]
                    }`}
                  >
                    {t(`routing_policy.resume.${detail.resume}`, {
                      defaultValue: detail.resume,
                    })}
                  </span>
                </div>
                <div className={styles.detailItemReason}>
                  {t(`routing_policy.reasons.${detail.reason}`, {
                    defaultValue: detail.reason || '-',
                  })}
                </div>
                <div className={styles.detailItemFooter}>
                  <span>
                    {t('routing_policy.runtime.retry_at')}:{' '}
                    {formatTimestamp(
                      detail.retryAt,
                      language,
                      t('routing_policy.runtime.not_scheduled')
                    )}
                    {detail.retryAt ? (
                      <>
                        {' '}
                        · <BoardCountdown at={detail.retryAt} resume={detail.resume} />
                      </>
                    ) : null}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      </section>
      {account.inspection && onOpenInspection ? (
        <div className={styles.detailActionFooter}>
          <Button variant="secondary" size="sm" onClick={() => onOpenInspection(account)}>
            {t('routing_policy.runtime.open_inspection')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

export function RoutingPolicyPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);

  const [activeView, setActiveView] = useState<SchedulingBoardView>('all');
  const [data, setData] = useState<SchedulingBoardResponse | null>(null);
  const { plans, reloadPlans } = useRoutingAccountPlans(data?.accounts);
  const [loading, setLoading] = useState(true);
  const [runtimeError, setRuntimeError] = useState('');
  const [selectedAuthId, setSelectedAuthId] = useState<string | null>(null);

  const [keyword, setKeyword] = useState('');
  const [providerFilter, setProviderFilter] = useState('all');
  const [scopeFilter, setScopeFilter] = useState('all');

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<ProPageSize>(DEFAULT_PRO_PAGE_SIZE);
  const [copiedAuthId, setCopiedAuthId] = useState<string | null>(null);

  const requestGate = useRef(createLatestRequestGate());
  const { activeSurface, openSurface, closeSurface } = useProSurfaceState<'runtime-detail'>();

  const setSelectedAccount = useCallback(
    (account: SchedulingBoardAccount | null) => {
      if (account) {
        setSelectedAuthId(account.authId);
        openSurface('runtime-detail');
      } else if (activeSurface === 'runtime-detail') {
        closeSurface();
      }
    },
    [activeSurface, closeSurface, openSurface]
  );

  const applyResponse = useCallback((response: SchedulingBoardResponse) => {
    setData(response);
    setRuntimeError('');
  }, []);

  const loadBoard = useCallback(
    async ({ notify = false, showLoading = false } = {}) => {
      if (connectionStatus !== 'connected') {
        setData(null);
        setRuntimeError('');
        setLoading(false);
        return;
      }
      const request = requestGate.current.begin();
      if (showLoading) setLoading(true);
      try {
        const response = await routingPolicyApi.get(request.signal);
        if (request.isCurrent()) {
          applyResponse(response);
          if (notify) {
            showNotification(
              t('routing_policy.runtime.refresh_success', {
                defaultValue: '数据已刷新',
              }),
              'success'
            );
          }
        }
      } catch (error) {
        if (request.isCurrent()) {
          setRuntimeError(error instanceof Error ? error.message : String(error || ''));
          if (notify) showNotification(t('routing_policy.load_failed'), 'error');
        }
      } finally {
        if (request.isCurrent()) setLoading(false);
        request.finish();
      }
    },
    [applyResponse, connectionStatus, showNotification, t]
  );

  useEffect(() => {
    const gate = requestGate.current;
    gate.invalidate();
    if (connectionStatus !== 'connected') {
      setData(null);
      setRuntimeError('');
      setLoading(false);
      return undefined;
    }
    void loadBoard({ notify: false, showLoading: true });
    const stop = startPolling(() => loadBoard(), 15000);
    return () => {
      stop();
      gate.invalidate();
    };
  }, [connectionStatus, loadBoard]);

  const providerOptions = useMemo(() => {
    const providers = Array.from(
      new Set((data?.accounts ?? []).map((account) => account.provider).filter(Boolean))
    ).sort();
    return [
      {
        value: 'all',
        label: t('routing_policy.runtime.all_providers', {
          defaultValue: 'All providers',
        }),
      },
      ...providers.map((provider) => ({
        value: provider,
        label: t(`routing_policy.providers.${provider}`, {
          defaultValue: provider,
        }),
      })),
    ];
  }, [data?.accounts, t]);

  const scopeOptions = useMemo(
    () => [
      {
        value: 'all',
        label: t('routing_policy.runtime.all_scopes', {
          defaultValue: 'All scopes',
        }),
      },
      {
        value: 'credential',
        label: t('routing_policy.scopes.credential', {
          defaultValue: 'Whole account',
        }),
      },
      {
        value: 'model',
        label: t('routing_policy.scopes.model', {
          defaultValue: 'Selected models',
        }),
      },
    ],
    [t]
  );

  const filteredAccounts = useMemo(() => {
    let rows = data?.accounts ?? [];
    if (activeView !== 'all') {
      rows = rows.filter((account) => account.bucket === activeView);
    }
    if (providerFilter !== 'all') {
      rows = rows.filter((account) => account.provider === providerFilter);
    }
    if (scopeFilter !== 'all') {
      rows = rows.filter((account) => account.scope === scopeFilter);
    }
    const term = keyword.trim().toLowerCase();
    if (term) {
      rows = rows.filter((account) => {
        const fileName = (account.fileName || '').toLowerCase();
        const authIndex = (account.authIndex || '').toLowerCase();
        const authId = (account.authId || '').toLowerCase();
        const provider = (account.provider || '').toLowerCase();
        const models = (account.models || []).map((m) => m.toLowerCase());
        return (
          fileName.includes(term) ||
          authIndex.includes(term) ||
          authId.includes(term) ||
          provider.includes(term) ||
          models.some((m) => m.includes(term))
        );
      });
    }
    return rows;
  }, [activeView, data?.accounts, keyword, providerFilter, scopeFilter]);

  const totalPages = Math.max(1, Math.ceil(filteredAccounts.length / pageSize));

  useEffect(() => {
    if (page > totalPages) {
      setPage(totalPages);
    }
  }, [page, totalPages]);

  const pagedAccounts = useMemo(() => {
    const start = (page - 1) * pageSize;
    return filteredAccounts.slice(start, start + pageSize);
  }, [filteredAccounts, page, pageSize]);

  const selectedAccount = useMemo(
    () => data?.accounts.find((account) => account.authId === selectedAuthId) ?? null,
    [data?.accounts, selectedAuthId]
  );

  const openInspection = useCallback(
    (account: SchedulingBoardAccount) => {
      closeSurface();
      navigate('/account-inspection', {
        state: buildInspectionFocusLocationState({
          authId: account.authId,
          authIndex: account.authIndex,
          fileName: account.fileName,
        }),
      });
    },
    [closeSurface, navigate]
  );

  const handleCopyAuthId = useCallback((authId: string, event: MouseEvent) => {
    event.stopPropagation();
    void copyToClipboard(authId).then((ok) => {
      if (ok) {
        setCopiedAuthId(authId);
        setTimeout(() => {
          setCopiedAuthId((current) => (current === authId ? null : current));
        }, 2000);
      }
    });
  }, []);

  const hasActiveFilter = Boolean(
    keyword.trim() || providerFilter !== 'all' || scopeFilter !== 'all'
  );

  const switchView = useCallback((view: SchedulingBoardView) => {
    setActiveView(view);
    setPage(1);
  }, []);

  const resetFilters = useCallback(() => {
    setKeyword('');
    setProviderFilter('all');
    setScopeFilter('all');
    setPage(1);
  }, []);

  const handleRecoveryResult = useCallback(async (result?: SchedulingRecoveryResult) => {
    await loadBoard();
    if (!result) return;
    const tone = schedulingRecoveryResultTone(result);
    showNotification(
      t(tone === 'error'
        ? 'routing_policy.recovery.failed'
        : result.after?.authId
          ? 'routing_policy.recovery.still_restricted'
          : 'routing_policy.recovery.restored'),
      tone
    );
  }, [loadBoard, showNotification, t]);

  const handleRecoveryError = useCallback(async (error: unknown) => {
    showNotification(
      error instanceof Error ? error.message : t('routing_policy.recovery.failed'),
      'error'
    );
    await loadBoard();
  }, [loadBoard, showNotification, t]);

  return (
    <BoardClock>
      <div className={styles.container}>
        <div className={styles.sectionHeaderWithAction}>
          <div>
            <h1>{t('routing_policy.title')}</h1>
            <p>{t('routing_policy.subtitle')}</p>
          </div>
          <div className={styles.headerActions}>
            {data?.generatedAt ? (
              <span
                className={styles.syncStatus}
                title={formatTimestamp(data.generatedAt, i18n.language, '-')}
              >
                <span className={`${styles.syncDot} ${runtimeError ? styles.syncDotStale : ''}`} />
                {t('routing_policy.runtime.last_updated', {
                  time: formatTimeOnly(data.generatedAt, i18n.language, '-'),
                })}
              </span>
            ) : null}
            <Button
              variant="secondary"
              size="sm"
              className={styles.refreshButton}
              onClick={() => {
                void loadBoard({ notify: true, showLoading: true });
                void reloadPlans();
              }}
              disabled={loading || connectionStatus !== 'connected'}
            >
              <IconRefreshCw size={15} className={loading ? styles.spinningIcon : undefined} />
              {t('common.refresh')}
            </Button>
          </div>
        </div>

        <Card className={styles.overviewCard}>
          <div className={styles.overviewLabel} title={t('routing_policy.summary.global_hint')}>
            {t('routing_policy.summary.overview_label')}
          </div>
          <div className={styles.overviewGrid}>
            <div className={styles.overviewMetric}>
              <small>{t('routing_policy.summary.blocked')}</small>
              <strong>{data?.summary.blocked ?? '—'}</strong>
            </div>
            <div className={styles.overviewMetric}>
              <small title={t('routing_policy.summary.excluded_hint')}>
                {t('routing_policy.summary.excluded')}
              </small>
              <strong>{data?.summary.excluded ?? '—'}</strong>
            </div>
            {(
              [
                { key: 'nextActionAt', label: 'next_recheck_probe', resume: 'action' },
                { key: 'nextTransitionAt', label: 'next_status_transition', resume: 'auto-expire' },
              ] as const
            ).map(({ key, label, resume }) => (
              <div key={key} className={styles.overviewEvent}>
                <small>{t(`routing_policy.summary.${label}`)}</small>
                <strong>
                  {data?.summary[key] ? (
                    <BoardCountdown at={data.summary[key]} resume={resume} />
                  ) : data ? (
                    t('routing_policy.runtime.not_scheduled')
                  ) : (
                    '—'
                  )}
                </strong>
                {data?.summary[key] ? (
                  <span title={formatTimestamp(data.summary[key], i18n.language, '-')}>
                    {formatTimestamp(data.summary[key], i18n.language, '-')}
                  </span>
                ) : null}
              </div>
            ))}
          </div>
        </Card>

        {(runtimeError || connectionStatus !== 'connected') && (
          <div role="status" className={styles.staleNotice}>
            <strong>{t('routing_policy.runtime.stale')}</strong>
            {runtimeError ? (
              <span>{runtimeError}</span>
            ) : (
              <span>{t('routing_policy.runtime.disconnected_notice')}</span>
            )}
          </div>
        )}

        <Card className={styles.boardCard}>
          <div className={styles.filterSection}>
            <ProFeatureTabs
              ariaLabel={t('routing_policy.title')}
              activeKey={activeView}
              onChange={(key) => switchView(key as SchedulingBoardView)}
              items={VIEW_KEYS.map((view) => ({
                key: view,
                label: t(`routing_policy.views.${view}`),
                badge:
                  view === 'all'
                    ? data?.summary.blocked
                    : data?.summary?.[view === 'authTransient' ? 'authTransient' : view],
              }))}
            />

            <div className={styles.filterGrid}>
              <Input
                type="search"
                value={keyword}
                onChange={(e) => {
                  setKeyword(e.target.value);
                  setPage(1);
                }}
                placeholder={t('routing_policy.runtime.search_placeholder')}
                aria-label={t('routing_policy.runtime.search_aria_label')}
                className={styles.toolbarHeaderSearchInput}
                rightElement={<IconSearch size={16} />}
              />

              <Select
                value={providerFilter}
                options={providerOptions}
                onChange={(value) => {
                  setProviderFilter(value);
                  setPage(1);
                }}
                ariaLabel={t('routing_policy.runtime.all_providers')}
              />

              <Select
                value={scopeFilter}
                options={scopeOptions}
                onChange={(value) => {
                  setScopeFilter(value);
                  setPage(1);
                }}
                ariaLabel={t('routing_policy.runtime.all_scopes')}
              />

              {hasActiveFilter && (
                <Button variant="secondary" className={styles.clearButton} onClick={resetFilters}>
                  <IconX size={14} /> {t('routing_policy.runtime.filter_reset')}
                </Button>
              )}
            </div>
          </div>

          {loading && !data ? (
            <div className={styles.loadingState}>
              <IconRefreshCw size={24} className={styles.spinningIcon} />
              <p>{t('common.loading')}</p>
            </div>
          ) : connectionStatus !== 'connected' ? (
            <div className={styles.loadingState}>
              <p>{t('routing_policy.runtime.disconnected_notice')}</p>
            </div>
          ) : !data ? (
            <EmptyState
              title={t('routing_policy.load_failed')}
              description={t('routing_policy.runtime.unavailable')}
              action={
                <Button variant="secondary" onClick={() => void loadBoard({ showLoading: true })}>
                  {t('common.refresh')}
                </Button>
              }
            />
          ) : data.accounts.length === 0 ? (
            <EmptyState
              title={t('routing_policy.runtime.healthy_title')}
              description={t('routing_policy.runtime.healthy_desc')}
            />
          ) : filteredAccounts.length === 0 ? (
            <EmptyState
              title={t('routing_policy.runtime.filter_empty')}
              description={t('routing_policy.runtime.filter_empty_hint')}
              action={
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    resetFilters();
                    switchView('all');
                  }}
                >
                  {t('routing_policy.runtime.filter_reset')}
                </Button>
              }
            />
          ) : (
            <>
              <div className={styles.desktopTable} data-routing-scroll-region="table">
                <Table className={styles.routingTable}>
                  <TableHeader>
                    <TableRow>
                      <TableHead className={styles.colAccount}>
                        {t('routing_policy.runtime.account')}
                      </TableHead>
                      <TableHead className={styles.colRestriction}>
                        {t('routing_policy.runtime.restriction_group')}
                      </TableHead>
                      <TableHead
                        className={styles.colRecovery}
                        title={t('routing_policy.summary.global_hint')}
                      >
                        {t('routing_policy.runtime.recovery_group')}
                      </TableHead>
                      <TableHead className={styles.colActions} alignRight>
                        {t('routing_policy.runtime.actions')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {pagedAccounts.map((account) => (
                      <TableRow key={account.authId}>
                        <TableCell>
                          <BoardIdentity
                            account={account}
                            planLabel={
                              plans.get(account.authId) || t('routing_policy.runtime.plan_unknown')
                            }
                            copied={copiedAuthId === account.authId}
                            onSelect={() => setSelectedAccount(account)}
                            onCopy={(event) => handleCopyAuthId(account.authId, event)}
                          />
                        </TableCell>
                        <TableCell>
                          <BoardRestriction account={account} />
                        </TableCell>
                        <TableCell>
                          <BoardRecovery account={account} />
                        </TableCell>
                        <TableCell alignRight>
                          <BoardActions
                            account={account}
                            onSelect={() => setSelectedAccount(account)}
                            onRecovered={handleRecoveryResult}
                            onRecoveryError={handleRecoveryError}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <div className={styles.mobileCards} data-routing-scroll-region="cards">
                {pagedAccounts.map((account) => (
                  <article key={account.authId} className={styles.mobileCard}>
                    <BoardIdentity
                      account={account}
                      planLabel={
                        plans.get(account.authId) || t('routing_policy.runtime.plan_unknown')
                      }
                      copied={copiedAuthId === account.authId}
                      onSelect={() => setSelectedAccount(account)}
                      onCopy={(event) => handleCopyAuthId(account.authId, event)}
                    />
                    <div className={styles.mobileSections}>
                      <section className={styles.mobileSection}>
                        <h3>{t('routing_policy.runtime.restriction_group')}</h3>
                        <BoardRestriction account={account} />
                      </section>
                      <section className={styles.mobileSection}>
                        <h3>{t('routing_policy.runtime.recovery_group')}</h3>
                        <BoardRecovery account={account} />
                      </section>
                    </div>
                    <div className={styles.mobileFooter}>
                      <BoardActions
                        account={account}
                        onSelect={() => setSelectedAccount(account)}
                        onRecovered={handleRecoveryResult}
                        onRecoveryError={handleRecoveryError}
                      />
                    </div>
                  </article>
                ))}
              </div>

              <div className={styles.pagination}>
                <ProPagination
                  pageSizeOptions={PRO_PAGE_SIZE_OPTIONS}
                  page={page}
                  pageSize={pageSize}
                  total={filteredAccounts.length}
                  onPageChange={setPage}
                  onPageSizeChange={(nextSize) => {
                    setPageSize(nextSize);
                    setPage(1);
                  }}
                  idPrefix="routing-board"
                />
              </div>
            </>
          )}
        </Card>

        <ProDetailDialog
          open={activeSurface === 'runtime-detail'}
          title={t('routing_policy.runtime.details_title')}
          onClose={() => setSelectedAccount(null)}
          onAfterClose={() => setSelectedAuthId(null)}
        >
          {selectedAccount ? (
            <SchedulingBoardDetailPanel
              account={selectedAccount}
              planLabel={
                plans.get(selectedAccount.authId) || t('routing_policy.runtime.plan_unknown')
              }
              t={t}
              language={i18n.language}
              onOpenInspection={openInspection}
            />
          ) : selectedAuthId ? (
            <p>{t(data ? 'routing_policy.runtime.no_longer_listed' : 'routing_policy.runtime.unavailable')}</p>
          ) : null}
        </ProDetailDialog>
      </div>
    </BoardClock>
  );
}
