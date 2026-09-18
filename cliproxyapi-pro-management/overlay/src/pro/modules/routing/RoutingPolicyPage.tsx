import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  IconCheck,
  IconRefreshCw,
  IconSearch,
  IconShield,
  IconX,
} from '@/components/ui/icons';
import { IconCopy } from '@/pro/icons';
import {
  formatRemainingTime,
  formatTimeOnly,
  formatTimestamp,
  routingPolicyApi,
  schedulingBoardModelsLabel,
  schedulingBoardResumeTone,
  type SchedulingBoardAccount,
  type SchedulingBoardResponse,
} from '@/pro/modules/routing/routingPolicy';
import { createLatestRequestGate } from '@/pro/modules/routing/latestRequestGate';
import { buildInspectionFocusLocationState } from '@/pro/shared/inspectionNavigation';
import {
  DEFAULT_PRO_PAGE_SIZE,
  PRO_PAGE_SIZE_OPTIONS,
  normalizeProPageSize,
  resolveProPaginationCopy,
  type ProPageSize,
} from '@/pro/shared/pagination';
import { startPolling } from '@/pro/shared/polling';
import { ProFeatureTabs } from '@/pro/shared/ProFeatureTabs';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import { ProInformationDetails, type ProInformationDetailsTone } from '@/pro/shared/ProInformationDetails';
import { useProSurfaceState } from '@/pro/shared/useProSurfaceState';
import { useAuthStore, useNotificationStore } from '@/stores';
import styles from './RoutingPolicyPage.module.scss';

type SchedulingBoardView = 'all' | 'quota' | 'authTransient' | 'recheck' | 'overlap';

const VIEW_KEYS: SchedulingBoardView[] = ['all', 'quota', 'authTransient', 'recheck', 'overlap'];

function SchedulingBoardDetailPanel({
  account,
  t,
  language,
  onOpenInspection,
}: {
  account: SchedulingBoardAccount;
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
              {
                label: t('routing_policy.runtime.scope'),
                value: t(`routing_policy.scopes.${account.scope}`, { defaultValue: account.scope }),
              },
              {
                label: t('routing_policy.runtime.models'),
                value: schedulingBoardModelsLabel(account, t('routing_policy.runtime.all_models')),
              },
              {
                label: t('routing_policy.runtime.resume'),
                value: t(`routing_policy.resume.${account.resume}`, { defaultValue: account.resume }),
              },
              {
                label: t('routing_policy.runtime.next_action_at'),
                value: formatTimestamp(account.nextActionAt, language, t('routing_policy.runtime.not_scheduled')),
              },
              {
                label: t('routing_policy.runtime.next_transition_at'),
                value: formatTimestamp(account.nextTransitionAt, language, t('routing_policy.runtime.not_scheduled')),
              },
            ],
          },
        ]}
        detailLabel={t('routing_policy.runtime.reason_details')}
        detail={(
          <div className={styles.detailList}>
            {account.details.map((detail, index) => {
              const isInspection = detail.source === 'inspection';
              const remaining = detail.retryAt ? formatRemainingTime(undefined, detail.retryAt, detail.resume, t) : '';
              return (
                <div key={`${detail.source}-${detail.model || 'all'}-${index}`} className={styles.detailItemCard}>
                  <div className={styles.detailItemHeader}>
                    <div className={styles.detailBadges}>
                      <span className={isInspection ? styles.sourceTagInspection : styles.sourceTagUpstream}>
                        {t(`routing_policy.sources.${detail.source}`, { defaultValue: detail.source })}
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
                      {t(`routing_policy.resume.${detail.resume}`, { defaultValue: detail.resume })}
                    </span>
                  </div>
                  <div className={styles.detailItemReason}>
                    {t(`routing_policy.reasons.${detail.reason}`, { defaultValue: detail.reason || '-' })}
                  </div>
                  <div className={styles.detailItemFooter}>
                    <span>
                      {t('routing_policy.runtime.retry_at')}: {formatTimestamp(detail.retryAt, language, t('routing_policy.runtime.not_scheduled'))}
                      {remaining && remaining !== '-' ? ` · ${remaining}` : ''}
                    </span>
                  </div>
                </div>
              );
            })}
            {account.inspection && onOpenInspection ? (
              <div className={styles.detailActionFooter}>
                <Button variant="primary" size="sm" onClick={() => onOpenInspection(account)}>
                  {t('routing_policy.runtime.open_inspection')}
                </Button>
              </div>
            ) : null}
          </div>
        )}
      />
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
  const paginationCopy = resolveProPaginationCopy(i18n.resolvedLanguage ?? i18n.language);

  const setSelectedAccount = useCallback((account: SchedulingBoardAccount | null) => {
    if (account) {
      setSelectedAuthId(account.authId);
      openSurface('runtime-detail');
    } else if (activeSurface === 'runtime-detail') {
      closeSurface();
    }
  }, [activeSurface, closeSurface, openSurface]);

  const applyResponse = useCallback((response: SchedulingBoardResponse) => {
    setData(response);
    setRuntimeError('');
  }, []);

  const loadBoard = useCallback(async ({ notify = false, showLoading = false } = {}) => {
    if (connectionStatus !== 'connected') {
      setData(null);
      setLoading(false);
      return;
    }
    const request = requestGate.current.begin();
    if (showLoading) setLoading(true);
    try {
      const response = await routingPolicyApi.get(request.signal);
      if (request.isCurrent()) applyResponse(response);
    } catch (error) {
      if (request.isCurrent()) {
        setRuntimeError(error instanceof Error ? error.message : String(error || ''));
        if (notify) showNotification(t('routing_policy.load_failed'), 'error');
      }
    } finally {
      if (request.isCurrent() && showLoading) setLoading(false);
      request.finish();
    }
  }, [applyResponse, connectionStatus, showNotification, t]);

  useEffect(() => {
    const gate = requestGate.current;
    gate.invalidate();
    if (connectionStatus !== 'connected') {
      setData(null);
      setLoading(false);
      return undefined;
    }
    void loadBoard({ notify: true, showLoading: true });
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
      { value: 'all', label: t('routing_policy.runtime.all_providers', { defaultValue: 'All providers' }) },
      ...providers.map((provider) => ({
        value: provider,
        label: t(`routing_policy.providers.${provider}`, { defaultValue: provider }),
      })),
    ];
  }, [data?.accounts, t]);

  const scopeOptions = useMemo(() => [
    { value: 'all', label: t('routing_policy.runtime.all_scopes', { defaultValue: 'All scopes' }) },
    { value: 'credential', label: t('routing_policy.scopes.credential', { defaultValue: 'Whole account' }) },
    { value: 'model', label: t('routing_policy.scopes.model', { defaultValue: 'Selected models' }) },
  ], [t]);

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

  useEffect(() => {
    if (activeSurface !== 'runtime-detail' || !selectedAuthId || !data || selectedAccount) return;
    closeSurface();
  }, [activeSurface, closeSurface, data, selectedAccount, selectedAuthId]);

  const openInspection = useCallback((account: SchedulingBoardAccount) => {
    navigate('/account-inspection', {
      state: buildInspectionFocusLocationState({
        authId: account.authId,
        authIndex: account.authIndex,
        fileName: account.fileName,
      }),
    });
  }, [navigate]);

  const handleCopyAuthId = useCallback((authId: string, event: MouseEvent) => {
    event.stopPropagation();
    if (!navigator.clipboard) return;
    void navigator.clipboard.writeText(authId).then(() => {
      setCopiedAuthId(authId);
      setTimeout(() => {
        setCopiedAuthId((current) => (current === authId ? null : current));
      }, 2000);
    });
  }, []);

  const hasActiveFilter = Boolean(keyword.trim() || providerFilter !== 'all' || scopeFilter !== 'all');

  return (
    <div className={styles.container}>
      <section className={styles.panel}>
        <div className={styles.sectionHeaderWithAction}>
          <div>
            <h1>{t('routing_policy.title')}</h1>
            <p>{t('routing_policy.subtitle')}</p>
          </div>
          <div className={styles.headerActions}>
            {data?.generatedAt ? (
              <span className={styles.syncStatus} title={formatTimestamp(data.generatedAt, i18n.language, '-')}>
                <span className={styles.syncDot} />
                {t('routing_policy.runtime.live_syncing')}: {formatTimeOnly(data.generatedAt, i18n.language, '-')}
              </span>
            ) : null}
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void loadBoard({ notify: true, showLoading: true })}
              disabled={loading}
            >
              <IconRefreshCw size={15} className={loading ? styles.spinningIcon : undefined} /> {t('common.refresh')}
            </Button>
          </div>
        </div>

        <div className={styles.summaryGrid}>
          <div className={`${styles.summaryCard} ${styles.cardBlocked}`}>
            <small>{t('routing_policy.summary.blocked')}</small>
            <strong>{data?.summary?.blocked ?? 0}</strong>
          </div>
          <div className={`${styles.summaryCard} ${styles.cardQuota}`}>
            <small>{t('routing_policy.summary.quota')}</small>
            <strong>{data?.summary?.quota ?? 0}</strong>
          </div>
          <div className={`${styles.summaryCard} ${styles.cardAuth}`}>
            <small>{t('routing_policy.summary.authTransient')}</small>
            <strong>{data?.summary?.authTransient ?? 0}</strong>
          </div>
          <div className={`${styles.summaryCard} ${styles.cardRecheck}`}>
            <small>{t('routing_policy.summary.recheck')}</small>
            <strong>{data?.summary?.recheck ?? 0}</strong>
          </div>
          <div className={`${styles.summaryCard} ${styles.cardOverlap}`}>
            <small>{t('routing_policy.summary.overlap')}</small>
            <strong>{data?.summary?.overlap ?? 0}</strong>
          </div>
          <div className={`${styles.summaryCard} ${styles.cardExcluded}`} title={t('routing_policy.summary.excluded_hint')}>
            <small>{t('routing_policy.summary.excluded')}</small>
            <strong>{data?.summary?.excluded ?? 0}</strong>
          </div>
        </div>

        <div className={styles.timelineBanner}>
          <div className={styles.timelineItem}>
            <div className={styles.timelineHeader}>
              <span className={styles.timelineDot} />
              <small>{t('routing_policy.summary.next_auto_recovery')}</small>
            </div>
            <div className={styles.timelineBody}>
              {data?.summary.nextRetryAt ? (
                <>
                  <span className={styles.timelineCountdown}>
                    {formatRemainingTime(undefined, data.summary.nextRetryAt, 'auto-expire', t)}
                  </span>
                  <span className={styles.timelineTime}>
                    {formatTimestamp(data.summary.nextRetryAt, i18n.language, '-')}
                  </span>
                </>
              ) : (
                <span className={styles.timelineEmpty}>{t('routing_policy.summary.no_retry')}</span>
              )}
            </div>
          </div>

          <div className={styles.timelineItem}>
            <div className={styles.timelineHeader}>
              <span className={`${styles.timelineDot} ${styles.timelineDotInfo}`} />
              <small>{t('routing_policy.summary.next_recheck_probe')}</small>
            </div>
            <div className={styles.timelineBody}>
              {data?.summary.nextActionAt ? (
                <>
                  <span className={styles.timelineCountdown}>
                    {formatRemainingTime(undefined, data.summary.nextActionAt, 'recheck-quota', t)}
                  </span>
                  <span className={styles.timelineTime}>
                    {formatTimestamp(data.summary.nextActionAt, i18n.language, '-')}
                  </span>
                </>
              ) : (
                <span className={styles.timelineEmpty}>{t('routing_policy.summary.no_action')}</span>
              )}
            </div>
          </div>

          <div className={styles.timelineItem}>
            <div className={styles.timelineHeader}>
              <span className={`${styles.timelineDot} ${styles.timelineDotWarn}`} />
              <small>{t('routing_policy.summary.next_status_transition')}</small>
            </div>
            <div className={styles.timelineBody}>
              {data?.summary.nextTransitionAt ? (
                <>
                  <span className={styles.timelineCountdown}>
                    {formatRemainingTime(undefined, data.summary.nextTransitionAt, 'auto-expire', t)}
                  </span>
                  <span className={styles.timelineTime}>
                    {formatTimestamp(data.summary.nextTransitionAt, i18n.language, '-')}
                  </span>
                </>
              ) : (
                <span className={styles.timelineEmpty}>{t('routing_policy.summary.no_transition')}</span>
              )}
            </div>
          </div>
        </div>
      </section>

      {(runtimeError || connectionStatus !== 'connected') && (
        <div role="status" className={styles.staleNotice}>
          <strong>{t('routing_policy.runtime.stale')}</strong>
          {runtimeError && <span>{runtimeError}</span>}
        </div>
      )}

      <div className={styles.filterSection}>
        <ProFeatureTabs
          ariaLabel={t('routing_policy.title')}
          activeKey={activeView}
          onChange={(key) => {
            setActiveView(key as SchedulingBoardView);
            setPage(1);
          }}
          items={VIEW_KEYS.map((view) => ({
            key: view,
            label: t(`routing_policy.views.${view}`),
            badge: view === 'all' ? data?.summary.blocked : data?.summary?.[view === 'authTransient' ? 'authTransient' : view],
          }))}
        />

        <div className={styles.toolbar}>
          <div className={styles.searchWrap}>
            <Input
              type="search"
              value={keyword}
              onChange={(e) => {
                setKeyword(e.target.value);
                setPage(1);
              }}
              placeholder={t('routing_policy.runtime.search_placeholder')}
              aria-label={t('routing_policy.runtime.search_aria_label')}
              rightElement={<IconSearch size={16} />}
            />
          </div>

          <div className={styles.selectWrap}>
            <Select
              value={providerFilter}
              options={providerOptions}
              onChange={(value) => {
                setProviderFilter(value);
                setPage(1);
              }}
              size="sm"
              ariaLabel={t('routing_policy.runtime.all_providers')}
            />
          </div>

          <div className={styles.selectWrap}>
            <Select
              value={scopeFilter}
              options={scopeOptions}
              onChange={(value) => {
                setScopeFilter(value);
                setPage(1);
              }}
              size="sm"
              ariaLabel={t('routing_policy.runtime.all_scopes')}
            />
          </div>

          {hasActiveFilter && (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setKeyword('');
                setProviderFilter('all');
                setScopeFilter('all');
                setPage(1);
              }}
            >
              <IconX size={14} /> {t('routing_policy.runtime.filter_reset')}
            </Button>
          )}
        </div>
      </div>

      <section className={styles.panel}>
        {loading && !data ? (
          <div className={styles.loadingState}>
            <IconRefreshCw size={24} className={styles.spinningIcon} />
            <p>{t('common.loading')}</p>
          </div>
        ) : (data?.accounts?.length ?? 0) === 0 ? (
          <div className={styles.healthyState}>
            <div className={styles.healthyIconWrap}>
              <IconShield size={36} />
            </div>
            <h3>{t('routing_policy.runtime.healthy_title')}</h3>
            <p>{t('routing_policy.runtime.healthy_desc')}</p>
          </div>
        ) : filteredAccounts.length === 0 ? (
          <div className={styles.filterEmptyState}>
            <div className={styles.filterEmptyIconWrap}>
              <IconSearch size={32} />
            </div>
            <h3>{t('routing_policy.runtime.filter_empty')}</h3>
            <p>{t('routing_policy.runtime.filter_empty_hint')}</p>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setKeyword('');
                setProviderFilter('all');
                setScopeFilter('all');
              }}
            >
              {t('routing_policy.runtime.filter_reset')}
            </Button>
          </div>
        ) : (
          <>
            <div className={`${styles.tableScroller} ${styles.runtimeTableScroller}`}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th style={{ width: '120px' }}>{t('routing_policy.runtime.provider')}</th>
                    <th style={{ width: '280px' }}>{t('routing_policy.runtime.account')}</th>
                    <th style={{ width: '150px' }}>{t('routing_policy.runtime.source')}</th>
                    <th style={{ width: '200px' }}>{t('routing_policy.runtime.scope')}</th>
                    <th style={{ width: '150px' }}>{t('routing_policy.runtime.resume')}</th>
                    <th style={{ width: '180px' }}>{t('routing_policy.runtime.expected_recovery')}</th>
                    <th style={{ width: '140px' }} className={styles.runtimeActionHeader}>
                      {t('routing_policy.runtime.actions')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {pagedAccounts.map((account) => {
                    const remaining = formatRemainingTime(account.remainingSeconds, account.retryAt, account.resume, t);
                    const hasCountdown = account.retryAt && account.retryAt > 0;
                    return (
                      <tr key={`${account.authIndex}:${account.authId}`}>
                        <td>
                          <span className={styles.providerTag}>{account.provider}</span>
                        </td>
                        <td>
                          <div className={styles.accountCell}>
                            <div className={styles.accountRow}>
                              <button
                                type="button"
                                className={styles.accountButton}
                                onClick={() => setSelectedAccount(account)}
                                title={t('routing_policy.runtime.details_click_hint')}
                              >
                                <strong>{account.fileName || account.authIndex}</strong>
                              </button>
                              <button
                                type="button"
                                className={styles.copyButton}
                                onClick={(e) => handleCopyAuthId(account.authId, e)}
                                title={
                                  copiedAuthId === account.authId
                                    ? t('routing_policy.runtime.copied')
                                    : t('routing_policy.runtime.copy_auth_id')
                                }
                                aria-label={t('routing_policy.runtime.copy_auth_id')}
                              >
                                {copiedAuthId === account.authId ? <IconCheck size={13} /> : <IconCopy size={13} />}
                              </button>
                            </div>
                            <div className={styles.authIdText} title={account.authId}>
                              <code>{account.authId}</code>
                            </div>
                          </div>
                        </td>
                        <td>
                          <div className={styles.sourceTagGroup}>
                            {account.sources.map((src) => (
                              <span
                                key={src}
                                className={src === 'inspection' ? styles.sourceTagInspection : styles.sourceTagUpstream}
                              >
                                {t(`routing_policy.sources.${src}`, { defaultValue: src })}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td>
                          {account.scope === 'credential' ? (
                            <span className={styles.scopeTagCredential}>{t('routing_policy.scopes.credential')}</span>
                          ) : account.models && account.models.length > 0 ? (
                            <div className={styles.modelChipGroup}>
                              {account.models.slice(0, 2).map((model) => (
                                <code key={model} className={styles.modelChip} title={model}>
                                  {model}
                                </code>
                              ))}
                              {account.models.length > 2 && (
                                <span
                                  className={styles.moreModelsBadge}
                                  title={account.models.slice(2).join(', ')}
                                >
                                  {t('routing_policy.runtime.models_more', { count: account.models.length - 2 })}
                                </span>
                              )}
                            </div>
                          ) : (
                            <span className={styles.scopeTagCredential}>{t('routing_policy.scopes.model')}</span>
                          )}
                        </td>
                        <td>
                          <span
                            className={`${styles.resumeTag} ${
                              styles[`resumeTag_${schedulingBoardResumeTone(account.resume)}`]
                            }`}
                          >
                            {t(`routing_policy.resume.${account.resume}`, { defaultValue: account.resume })}
                          </span>
                        </td>
                        <td>
                          <div className={styles.recoveryCell}>
                            <strong className={styles.recoveryCountdown}>{remaining}</strong>
                            {hasCountdown ? (
                              <small className={styles.recoveryTimestamp}>
                                {formatTimestamp(account.retryAt, i18n.language, '-')}
                              </small>
                            ) : null}
                          </div>
                        </td>
                        <td className={styles.runtimeActionCell}>
                          {account.inspection ? (
                            <Button
                              variant="secondary"
                              size="sm"
                              onClick={() => openInspection(account)}
                              title={t('routing_policy.runtime.open_inspection')}
                            >
                              {t('routing_policy.runtime.open_inspection')}
                            </Button>
                          ) : (
                            <Button
                              variant="secondary"
                              size="sm"
                              onClick={() => setSelectedAccount(account)}
                              title={t('routing_policy.runtime.view_details')}
                            >
                              {t('routing_policy.runtime.view_details')}
                            </Button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={styles.paginationBar}>
              <div className={styles.paginationPageSizeControl}>
                <span id="routing-board-page-size-label">
                  {paginationCopy.pageSizeLabel}
                </span>
                <Select
                  id="routing-board-page-size"
                  value={String(pageSize)}
                  options={PRO_PAGE_SIZE_OPTIONS.map((size) => ({
                    value: String(size),
                    label: paginationCopy.pageSizeValue(size),
                  }))}
                  onChange={(value) => {
                    const nextSize = normalizeProPageSize(value);
                    setPageSize(nextSize);
                    setPage(1);
                  }}
                  ariaLabelledBy="routing-board-page-size-label"
                  size="sm"
                />
              </div>
              <div className={styles.paginationNavigation}>
                <span className={styles.pageInfo}>
                  {t('routing_policy.runtime.pagination_info', {
                    current: page,
                    total: totalPages,
                    count: filteredAccounts.length,
                    defaultValue: `Page ${page} / ${totalPages} (${filteredAccounts.length} total)`,
                  })}
                </span>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  disabled={page <= 1}
                  aria-label={t('common.previous_page', { defaultValue: 'Previous page' })}
                >
                  &lt;
                </Button>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  disabled={page >= totalPages}
                  aria-label={t('common.next_page', { defaultValue: 'Next page' })}
                >
                  &gt;
                </Button>
              </div>
            </div>
          </>
        )}
      </section>

      <ProDetailDialog
        open={activeSurface === 'runtime-detail'}
        title={t('routing_policy.runtime.details_title')}
        onClose={() => setSelectedAccount(null)}
        onAfterClose={() => setSelectedAuthId(null)}
      >
        {selectedAccount ? (
          <SchedulingBoardDetailPanel
            account={selectedAccount}
            t={t}
            language={i18n.language}
            onOpenInspection={openInspection}
          />
        ) : selectedAuthId ? (
          <p>{t('routing_policy.runtime.no_longer_listed')}</p>
        ) : null}
      </ProDetailDialog>
    </div>
  );
}
