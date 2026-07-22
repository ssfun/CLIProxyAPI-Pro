import { useCallback, useEffect, useMemo, useState, type ChangeEvent } from 'react';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { IconSearch } from '@/components/ui/icons';
import { buildAccountStatusData, buildAccountStatusRange } from '../accountHealth';
import type { AccountQuotaEntry, AccountQuotaState } from '../accountQuota';
import { getAccountHealthTone, type AccountHealthTone, type AccountSortMetric } from '../monitoringAnalytics';
import { ACCOUNT_SORT_OPTIONS, TIME_RANGE_OPTIONS } from '../monitoringOptions';
import type { MonitoringAccountRow, MonitoringTimeRange } from '../hooks/useMonitoringData';
import { AccountOverviewCard } from './AccountOverviewPanels';
import quotaStyles from '@/pages/QuotaPage.module.scss';
import styles from '../monitoring.module.scss';

const formatAccountOverviewScopeText = (rangeLabel: string, t: TFunction) => (
  t('monitoring.account_scope_text', { range: rangeLabel })
);

export function AccountStatsPanel({
  rows,
  metric,
  emptyText,
  hasPrices,
  locale,
  t,
  rangeLabel,
  range,
  onRangeChange,
  onHide,
  expandedAccounts,
  accountQuotaStates,
  accountQuotaEntriesByAccount,
  onMetricChange,
  onToggleAccount,
  onRefreshQuota,
}: {
  rows: MonitoringAccountRow[];
  metric: AccountSortMetric;
  emptyText: string;
  hasPrices: boolean;
  locale: string;
  t: TFunction;
  rangeLabel: string;
  range: MonitoringTimeRange;
  onRangeChange: (range: MonitoringTimeRange) => void;
  onHide: () => void;
  expandedAccounts: Record<string, boolean>;
  accountQuotaStates: Record<string, AccountQuotaState>;
  accountQuotaEntriesByAccount: Map<string, AccountQuotaEntry[]>;
  onMetricChange: (metric: AccountSortMetric) => void;
  onToggleAccount: (accountId: string, account: string) => void;
  onRefreshQuota: (account: string) => void;
}) {
  const ACCOUNT_CARD_MIN_WIDTH = 330;
  const ACCOUNT_CARD_GAP = 16;
  const ROWS_PER_PAGE = 2;

  const [cardPage, setCardPage] = useState(0);
  const [gridEl, setGridEl] = useState<HTMLDivElement | null>(null);
  const gridRef = useCallback((el: HTMLDivElement | null) => setGridEl(el), []);
  const [gridCols, setGridCols] = useState(3);
  const [accountSearch, setAccountSearch] = useState('');
  const [accountProviderFilter, setAccountProviderFilter] = useState('all');
  const [accountHealthFilter, setAccountHealthFilter] = useState<'all' | AccountHealthTone>('all');

  useEffect(() => {
    if (!gridEl) return;
    const update = () => {
      const cols = Math.max(1, Math.floor((gridEl.clientWidth + ACCOUNT_CARD_GAP) / (ACCOUNT_CARD_MIN_WIDTH + ACCOUNT_CARD_GAP)));
      setGridCols((current) => (current === cols ? current : cols));
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(gridEl);
    return () => observer.disconnect();
  }, [gridEl]);

  const providerOptions = useMemo(() => {
    const set = new Set<string>();
    rows.forEach((row) => row.providers.forEach((p) => { if (p && p !== '-') set.add(p); }));
    return Array.from(set).sort();
  }, [rows]);

  const filteredRows = useMemo(() => {
    const query = accountSearch.trim().toLowerCase();
    return rows.filter((row) => {
      if (query) {
        const haystack = [row.accountMasked, row.account, ...row.authLabels, ...row.providers].join(' ').toLowerCase();
        if (!haystack.includes(query)) return false;
      }
      if (accountProviderFilter !== 'all') {
        if (!row.providers.includes(accountProviderFilter)) return false;
      }
      if (accountHealthFilter !== 'all') {
        if (getAccountHealthTone(row) !== accountHealthFilter) return false;
      }
      return true;
    });
  }, [rows, accountSearch, accountProviderFilter, accountHealthFilter]);

  const hasActiveFilters = accountSearch.trim() !== '' || accountProviderFilter !== 'all' || accountHealthFilter !== 'all';

  const itemsPerPage = gridCols * ROWS_PER_PAGE;
  const totalPages = Math.max(1, Math.ceil(filteredRows.length / itemsPerPage));
  const safePageIndex = Math.min(cardPage, totalPages - 1);
  const visibleRows = useMemo(
    () => filteredRows.slice(safePageIndex * itemsPerPage, (safePageIndex + 1) * itemsPerPage),
    [filteredRows, itemsPerPage, safePageIndex]
  );

  const accountStatusRange = useMemo(
    () => buildAccountStatusRange(rows, range),
    [rows, range]
  );

  const accountStatusDataById = useMemo(() => {
    const entries = visibleRows.map((row) => [row.id, buildAccountStatusData(row.rows ?? [], accountStatusRange)] as const);
    return new Map(entries);
  }, [accountStatusRange, visibleRows]);

  useEffect(() => {
    setCardPage(0);
  }, [accountSearch, accountProviderFilter, accountHealthFilter, metric, range, itemsPerPage]);

  return (
    <>
      <div className={styles.usageTrendHeader}>
        <div className={styles.usageTrendCopy}>
          <h2>{t('monitoring.account_stats_title')}</h2>
          <p>{t('monitoring.account_stats_desc')}</p>
        </div>
        <button type="button" className={`${styles.rankingMetricButton} ${styles.usageTrendHideButton} ${styles.mobileHeaderHideButton}`} onClick={onHide}>
          {t('monitoring.hide_analysis')}
        </button>
        <div className={styles.usageTrendActions}>
          <div className={`${styles.rankingMetricSwitch} ${styles.timeRangeControl}`}>
            {TIME_RANGE_OPTIONS.map((option) => (
              <button
                key={option.value}
                type="button"
                className={`${styles.rankingMetricButton} ${styles.timeRangeButton} ${range === option.value ? styles.rankingMetricButtonActive : ''}`}
                onClick={() => onRangeChange(option.value)}
              >
                {t(option.labelKey)}
              </button>
            ))}
          </div>
          <button type="button" className={`${styles.rankingMetricButton} ${styles.usageTrendHideButton}`} onClick={onHide}>
            {t('monitoring.hide_analysis')}
          </button>
        </div>
      </div>

      <Card className={styles.accountStatsCard}>
        <div className={styles.accountStatsToolbar}>
          <div className={styles.accountStatsFilters}>
            <Input
              value={accountSearch}
              onChange={(event: ChangeEvent<HTMLInputElement>) => setAccountSearch(event.target.value)}
              placeholder={t('monitoring.search_account')}
              className={styles.accountStatsSearchInput}
              rightElement={<IconSearch size={14} />}
              aria-label={t('monitoring.search_account')}
            />
            {providerOptions.length > 0 && (
              <select
                value={accountProviderFilter}
                onChange={(event) => setAccountProviderFilter(event.target.value)}
                className={styles.accountStatsSelect}
                aria-label={t('monitoring.filter_provider')}
              >
                <option value="all">{t('monitoring.filter_all_providers')}</option>
                {providerOptions.map((p) => (
                  <option key={p} value={p}>{p}</option>
                ))}
              </select>
            )}
            <select
              value={accountHealthFilter}
              onChange={(event) => setAccountHealthFilter(event.target.value as 'all' | AccountHealthTone)}
              className={styles.accountStatsSelect}
              aria-label={t('monitoring.filter_health_status')}
            >
              <option value="all">{t('monitoring.filter_all_statuses')}</option>
              <option value="good">{t('monitoring.health_good')}</option>
              <option value="warn">{t('monitoring.health_warn_filter')}</option>
              <option value="bad">{t('monitoring.health_bad')}</option>
            </select>
            {hasActiveFilters && (
              <button
                type="button"
                className={styles.accountStatsClearButton}
                onClick={() => { setAccountSearch(''); setAccountProviderFilter('all'); setAccountHealthFilter('all'); }}
              >
                {t('monitoring.clear_filters')}
              </button>
            )}
          </div>
          <div className={styles.rankingMetricSwitch} role="group" aria-label={t('monitoring.account_sort_aria')}>
            {ACCOUNT_SORT_OPTIONS.map((option) => (
              <button
                key={option.value}
                type="button"
                className={`${styles.rankingMetricButton} ${metric === option.value ? styles.rankingMetricButtonActive : ''}`}
                onClick={() => onMetricChange(option.value)}
                disabled={option.value === 'cost' && !hasPrices}
              >
                {t(option.labelKey)}
              </button>
            ))}
          </div>
        </div>

        {filteredRows.length > 0 ? (
          <>
            <div ref={gridRef} className={styles.accountOverviewCardGrid}>
              {visibleRows.map((row) => {
                const statusData = accountStatusDataById.get(row.id) ?? buildAccountStatusData([], accountStatusRange);
                return (
                  <AccountOverviewCard
                    key={row.id}
                    row={row}
                    hasPrices={hasPrices}
                    locale={locale}
                    t={t}
                    isExpanded={Boolean(expandedAccounts[row.id])}
                    statusData={statusData}
                    scopeText={formatAccountOverviewScopeText(rangeLabel, t)}
                    quotaState={accountQuotaStates[row.account]}
                    quotaEntries={accountQuotaEntriesByAccount.get(row.account) ?? []}
                    onToggle={() => onToggleAccount(row.id, row.account)}
                    onRefreshQuota={() => onRefreshQuota(row.account)}
                  />
                );
              })}
            </div>
            {totalPages > 1 && (
              <div className={quotaStyles.pagination}>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={safePageIndex === 0}
                  onClick={() => setCardPage((p) => Math.max(0, p - 1))}
                  aria-label={t('monitoring.previous_page')}
                >
                  {t('auth_files.pagination_prev', { defaultValue: t('monitoring.previous_page') })}
                </Button>
                <div className={quotaStyles.pageInfo}>
                  {t('auth_files.pagination_info', {
                    current: safePageIndex + 1,
                    total: totalPages,
                    count: filteredRows.length,
                    defaultValue: `${safePageIndex + 1} / ${totalPages} · ${filteredRows.length}`,
                  })}
                </div>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={safePageIndex >= totalPages - 1}
                  onClick={() => setCardPage((p) => Math.min(totalPages - 1, p + 1))}
                  aria-label={t('monitoring.next_page')}
                >
                  {t('auth_files.pagination_next', { defaultValue: t('monitoring.next_page') })}
                </Button>
              </div>
            )}
          </>
        ) : (
          <div className={styles.emptyBlockSmall}>{hasActiveFilters ? t('monitoring.no_matching_accounts') : emptyText}</div>
        )}
      </Card>
    </>
  );
}
