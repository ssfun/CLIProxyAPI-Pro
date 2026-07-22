import { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState, type ChangeEvent, type CSSProperties, type DragEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import {
  IconChevronDown,
  IconChevronUp,
  IconRefreshCw,
  IconSearch,
  IconSlidersHorizontal,
  IconTrash2,
} from '@/components/ui/icons';
import {
  buildAccountRowsByAccount,
  buildLocalDayKey,
  formatShortDateTime,
  getRangeStartMs,
  joinUnique,
  useMonitoringData,
  type MonitoringAccountRow,
  type MonitoringEventRow,
  type MonitoringStatusTone,
  type MonitoringTimeRange,
} from '@/features/monitoring/hooks/useMonitoringData';
import { REALTIME_LOG_PAGE_SIZE, useRealtimeLogData } from '@/features/monitoring/hooks/useRealtimeLogData';
import { useUsageData, type UsageEventPageFilters, type UsagePayload } from '@/features/monitoring/hooks/useUsageData';
import { useUsageAggregates, type UsageAggregateBucket } from '@/features/monitoring/hooks/useUsageAggregates';
import { findMonitoringAuthIndexes } from '@/features/monitoring/monitoringAuthSearch';
import {
  buildAccountStatusData,
  buildAccountStatusRange,
  type AccountStatusData,
} from '@/features/monitoring/accountHealth';
import { MonitoringHealthStatusBar } from '@/features/monitoring/components/MonitoringHealthStatusBar';
import {
  RealtimeCostCell,
} from '@/features/monitoring/components/RealtimeCostCell';
import {
  ApiKeyRankingPanel,
  ModelStatsPanel,
  TokenDistributionPanel,
  TopUsageStats,
  UsageTrendHeader,
  UsageTrendPanel,
  type UsageMetricCard,
} from '@/features/monitoring/components/UsageAnalyticsPanels';
import {
  buildAggregateSummary,
  buildServerAccountRows,
} from '@/features/monitoring/monitoringAggregates';
import {
  addMonitoringSummaryRow,
  buildServerUsageTrendAnalytics,
  buildUsageTrendAnalytics,
  buildUsageTrendRangeLabel,
  createMonitoringSummaryAccumulator,
  finalizeMonitoringSummary,
  formatPercent,
  getAccountHealthTone,
  getAccountSortValue,
  getRankingMetricValue,
  type AccountHealthTone,
  type AccountSortMetric,
  type RankingMetric,
} from '@/features/monitoring/monitoringAnalytics';
import {
  ACCOUNT_SORT_OPTIONS,
  TIME_RANGE_OPTIONS,
} from '@/features/monitoring/monitoringOptions';
import {
  REALTIME_LOG_COLUMN_DEFAULT_WIDTHS,
  clampRealtimeLogColumnWidth,
  createDefaultRealtimeLogColumns,
  isRealtimeLogColumnKey,
  loadRealtimeLogColumns,
  loadRealtimeLogFollowEnabled,
  normalizeRealtimeLogColumns,
  saveRealtimeLogColumns,
  saveRealtimeLogFollowEnabled,
  type RealtimeLogColumnKey,
  type RealtimeLogColumnPreference,
} from '@/features/monitoring/realtimeLogPreferences';
import {
  buildRealtimeDiagnosticClipboardText,
  buildRealtimeLogPageRows,
  buildRealtimeMetaText,
  buildRealtimeStatusLabel,
  compactRealtimeErrorMessage,
  getClientPaginationRange,
  translateRealtimeErrorCategory,
  translateRealtimeErrorText,
  type RealtimeLogRow,
} from '@/features/monitoring/realtimeLogPresentation';
import { hasUsageBackupManifest } from '@/features/monitoring/usageBackup';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { apiClient } from '@/services/api/client';
import { useAuthStore, useConfigStore, useNotificationStore, useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { maskSensitiveText } from '@/utils/format';
import { getStatusFromError, isAntigravityFile, isClaudeFile, isCodexFile, isKimiFile, isXaiFile } from '@/utils/quota';
import {
  deleteModelPriceRule,
  formatCompactNumber,
  formatDurationMs,
  formatUsd,
  formatUsdPrecise,
  loadModelPriceRules,
  loadModelPriceSyncState,
  normalizeAuthIndex,
  recalculateModelPriceHistory,
  saveModelPriceRule,
  syncModelPricesFromModelsDev,
  type ModelPriceRule,
  type ModelPriceSyncChangeAction,
  type ModelPriceSyncResult,
  type ModelPriceSyncState,
  type ObservedModelPriceTarget,
} from '@/utils/usage';
import {
  ANTIGRAVITY_CONFIG,
  CLAUDE_CONFIG,
  CODEX_CONFIG,
  KIMI_CONFIG,
  XAI_CONFIG,
  type QuotaConfig,
  type QuotaStore,
} from '@/components/quota/quotaConfigs';
import { type QuotaRenderHelpers, type QuotaStatusState } from '@/components/quota/QuotaCard';
import { QuotaProgressBar as AuthFileQuotaProgressBar } from '@/features/authFiles/components/QuotaProgressBar';
import authFileQuotaStyles from '@/pages/AuthFilesPage.module.scss';
import quotaStyles from '@/pages/QuotaPage.module.scss';
import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';
import styles from './MonitoringCenterPage.module.scss';

type StatusFilter = 'all' | 'success' | 'failed';

const ACCOUNT_STATS_ANALYTICS_ROW_LIMIT = 6000;
const ACCOUNT_QUOTA_REQUEST_CONCURRENCY = 4;
type RealtimeLogColumnDefinition = {
  key: RealtimeLogColumnKey;
  label: string;
  colClassName: string;
  headerClassName?: string;
  cellClassName?: (row: RealtimeLogRow) => string | undefined;
  render: (row: RealtimeLogRow) => ReactNode;
  width: number;
};
const formatTokenCount = (value: number) => Math.max(0, Math.round(Number(value) || 0)).toLocaleString();

const getCacheHitRate = (row: Pick<MonitoringEventRow, 'inputTokens' | 'cachedTokens'>): number | null => (
  row.inputTokens > 0 ? Math.min(Math.max(row.cachedTokens / row.inputTokens, 0), 1) : null
);

const getRealtimeLogColumnContentTexts = (key: RealtimeLogColumnKey, row: RealtimeLogRow) => {
  switch (key) {
    case 'type':
      return [row.provider, row.account || row.authLabel || row.accountMasked || '-'];
    case 'model':
      return [row.model, row.modelAlias && row.modelAlias !== row.model ? row.modelAlias : buildRealtimeMetaText(row)];
    case 'reasoningEffort':
      return [row.reasoningEffort.trim() || '-'];
    case 'stream':
      return [row.stream ? 'Streaming' : 'Non-streaming'];
    case 'apiKey':
      return [row.clientApiKey.masked];
    case 'recent':
      return ['||||||||||'];
    case 'status':
      return [buildRealtimeStatusLabel(row, row.failed ? 'Failed' : 'Success')];
    case 'successRate':
      return [formatPercent(row.successRate)];
    case 'calls':
      return [formatCompactNumber(row.requestCount)];
    case 'ttft':
      return [formatDurationMs(row.ttftMs)];
    case 'latency':
      return [formatDurationMs(row.latencyMs)];
    case 'tokens':
      return [
        formatTokenCount(row.totalTokens),
        `I ${formatTokenCount(row.inputTokens)} O ${formatTokenCount(row.outputTokens)}`,
        row.reasoningTokens > 0 ? `R ${formatTokenCount(row.reasoningTokens)}` : '',
      ];
    case 'cacheRead':
      return [
        formatTokenCount(row.cachedTokens),
        row.inputTokens > 0 ? formatPercent(Math.min(row.cachedTokens / row.inputTokens, 1)) : '--',
      ];
    case 'cost':
      return [formatUsdPrecise(row.totalCost)];
    case 'time':
      return [new Date(row.timestampMs).toLocaleString()];
    default:
      return [];
  }
};

const estimateRealtimeLogColumnWidth = (
  key: RealtimeLogColumnKey,
  label: string,
  rows: RealtimeLogRow[]
) => {
  const maxTextLength = rows.reduce((maxLength, row) => {
    const rowMaxLength = getRealtimeLogColumnContentTexts(key, row)
      .reduce((innerMax, text) => Math.max(innerMax, text.length), 0);
    return Math.max(maxLength, rowMaxLength);
  }, label.length);
  const characterWidth = key === 'recent' ? 6 : key === 'tokens' || key === 'cacheRead' ? 8 : 7;
  const padding = key === 'status' ? 36 : key === 'tokens' || key === 'cacheRead' ? 34 : 28;
  return clampRealtimeLogColumnWidth(key, maxTextLength * characterWidth + padding);
};

const estimateRealtimeLogHeaderWidth = (key: RealtimeLogColumnKey, label: string) => {
  const textWidth = Array.from(label).reduce((total, char) => (
    total + (char.charCodeAt(0) > 255 ? 13 : 7)
  ), 0);
  return clampRealtimeLogColumnWidth(key, textWidth + 42);
};

const formatAccountOverviewScopeText = (rangeLabel: string, t: TFunction) => t('monitoring.account_scope_text', { range: rangeLabel });

type PriceTierDraft = {
	contextSize: string;
	input: string;
	output: string;
	cacheRead: string;
	cacheWrite: string;
};

type PriceDraft = {
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  tiers: PriceTierDraft[];
};

type PriceManagementView = 'rules' | 'sync';
type PriceSyncChangeFilter = 'all' | ModelPriceSyncChangeAction;

type PriceRuleTarget = {
  key: string;
  model: string;
  requests: number;
  lastSeenAtMs: number;
  rule?: ModelPriceRule;
};

type MonitoringSettings = {
  retentionDays: number;
	webdav: {
    enabled: boolean;
    intervalMinutes: number;
    retentionDays: number;
    url: string;
    username: string;
    password: string;
	};
	modelPriceSync: {
		enabled: boolean;
		intervalMinutes: number;
	};
};

type MonitoringSettingsDraft = {
  retentionDays: string;
  webdavEnabled: boolean;
  webdavIntervalMinutes: string;
  webdavRetentionDays: string;
  webdavUrl: string;
  webdavUsername: string;
	webdavPassword: string;
	modelPriceSyncEnabled: boolean;
	modelPriceSyncIntervalMinutes: string;
};

type UsageImportResult = {
  added?: number;
  skipped?: number;
  total?: number;
  failed?: number;
  modelPrices?: number;
  modelPriceRecords?: number;
  modelPriceRules?: number;
  quotaCache?: number;
  quotaCacheRecords?: number;
  routingCursors?: number;
  routingCursorRecords?: number;
  authRuntimeStats?: number;
  authRuntimeStatsRecords?: number;
  accountInspectionSchedule?: boolean;
  accountInspectionScheduleRecords?: number;
  accountInspectionSnapshot?: boolean;
  accountInspectionSnapshotRecords?: number;
  monitoringSettings?: boolean;
  monitoringSettingsRecords?: number;
  legacyBackup?: boolean;
};

type UsageResetResult = {
  deletedEvents: number;
  generation: number;
  resetAtMs: number;
};

const createMonitoringSettingsDraft = (settings?: MonitoringSettings): MonitoringSettingsDraft => ({
  retentionDays: String(settings?.retentionDays ?? 0),
  webdavEnabled: settings?.webdav.enabled ?? false,
  webdavIntervalMinutes: String(settings?.webdav.intervalMinutes ?? 1440),
  webdavRetentionDays: String(settings?.webdav.retentionDays ?? 0),
  webdavUrl: settings?.webdav.url ?? '',
  webdavUsername: settings?.webdav.username ?? '',
	webdavPassword: settings?.webdav.password ?? '',
	modelPriceSyncEnabled: settings?.modelPriceSync?.enabled ?? false,
	modelPriceSyncIntervalMinutes: String(settings?.modelPriceSync?.intervalMinutes ?? 1440),
});

const parseNonNegativeInteger = (value: string) => {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

const parsePositiveInteger = (value: string, fallback: number) => {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
};

const buildMonitoringSettingsFromDraft = (draft: MonitoringSettingsDraft): MonitoringSettings => ({
  retentionDays: parseNonNegativeInteger(draft.retentionDays),
  webdav: {
    enabled: draft.webdavEnabled,
    intervalMinutes: parsePositiveInteger(draft.webdavIntervalMinutes, 1440),
    retentionDays: parseNonNegativeInteger(draft.webdavRetentionDays),
    url: draft.webdavUrl.trim(),
    username: draft.webdavUsername.trim(),
		password: draft.webdavPassword,
	},
	modelPriceSync: {
		enabled: draft.modelPriceSyncEnabled,
		intervalMinutes: parsePositiveInteger(draft.modelPriceSyncIntervalMinutes, 1440),
	},
});


const SuccessFailureValue = ({ success, failure }: { success: number; failure: number }) => (
  <span className={styles.successFailureValue}>
    <span className={styles.goodText}>{formatCompactNumber(success)}</span>
    <span className={styles.badText}>({formatCompactNumber(failure)})</span>
  </span>
);

const buildAccountCardFileName = (row: MonitoringAccountRow, quotaEntries: AccountQuotaEntry[] = []) => {
  const quotaFileNames = Array.from(new Set(quotaEntries.map((entry) => entry.fileName).filter(Boolean)));
  if (quotaFileNames.length > 0) return joinUnique(quotaFileNames, 1);

  const fileName = row.authLabels.find((label) => label && label !== '-' && label.endsWith('.json'));
  return fileName || row.authLabels.find((label) => label && label !== '-') || row.accountMasked || row.account;
};

const buildAccountCardProviderText = (row: MonitoringAccountRow) => {
  const providers = row.providers.filter((provider) => provider && provider !== '-');
  return providers.length > 0 ? joinUnique(providers, 2) : '-';
};

const sortAccountOverviewCardMetrics = (metrics: AccountSummaryMetric[], t: TFunction) => {
  const labels: Record<string, string> = {
    'total-tokens': t('monitoring.token_metric_total'),
    'input-tokens': t('monitoring.token_metric_input'),
    'output-tokens': t('monitoring.token_metric_output'),
    'cached-tokens': t('monitoring.token_metric_cached'),
  };
  const order = ['total-tokens', 'input-tokens', 'output-tokens', 'cached-tokens'];
  return order
    .map((key) => {
      const metric = metrics.find((item) => item.key === key);
      return metric ? { ...metric, label: labels[key] } : undefined;
    })
    .filter(Boolean) as AccountSummaryMetric[];
};

const buildAccountSummaryMetrics = (
  row: MonitoringAccountRow,
  hasPrices: boolean,
  locale: string,
  t: TFunction
): AccountSummaryMetric[] => [
  {
    key: 'total-calls',
    label: t('monitoring.total_calls'),
    value: formatCompactNumber(row.totalCalls),
  },
  {
    key: 'success-calls',
    label: t('monitoring.success_calls'),
    value: <SuccessFailureValue success={row.successCalls} failure={row.failureCalls} />,
  },
  {
    key: 'success-rate',
    label: t('monitoring.call_success_rate'),
    value: formatPercent(row.successRate),
    valueClassName:
      row.successRate >= 0.95
        ? styles.goodText
        : row.successRate >= 0.85
          ? styles.warnText
          : styles.badText,
  },
  {
    key: 'total-tokens',
    label: t('monitoring.total_tokens'),
    value: formatCompactNumber(row.totalTokens),
  },
  {
    key: 'input-tokens',
    label: t('monitoring.input_tokens'),
    value: formatCompactNumber(row.inputTokens),
  },
  {
    key: 'output-tokens',
    label: t('monitoring.output_tokens'),
    value: formatCompactNumber(row.outputTokens),
  },
  {
    key: 'cached-tokens',
    label: t('monitoring.cached_tokens'),
    value: formatCompactNumber(row.cachedTokens),
  },
  {
    key: 'estimated-cost',
    label: t('monitoring.estimated_cost'),
    value: hasPrices ? formatUsd(row.totalCost) : '--',
  },
  {
    key: 'latest-request-time',
    label: t('monitoring.latest_request_time'),
    value: new Date(row.lastSeenAt).toLocaleString(locale),
  },
];

type AnyQuotaConfig = {
  type: QuotaConfig<QuotaStatusState, unknown>['type'];
  i18nPrefix: string;
  fetchQuota: (file: AuthFileItem, t: TFunction) => Promise<unknown>;
  storeSelector: (state: QuotaStore) => Record<string, QuotaStatusState>;
  storeSetter: keyof QuotaStore;
  buildLoadingState: () => QuotaStatusState;
  buildSuccessState: (data: unknown) => QuotaStatusState;
  buildErrorState: (message: string, status?: number) => QuotaStatusState;
  renderQuotaItems: (quota: QuotaStatusState, t: TFunction, helpers: QuotaRenderHelpers) => ReactNode;
};

const adaptQuotaConfig = <TState extends QuotaStatusState, TData>(
  config: QuotaConfig<TState, TData>
): AnyQuotaConfig => ({
  type: config.type,
  i18nPrefix: config.i18nPrefix,
  fetchQuota: config.fetchQuota,
  storeSelector: config.storeSelector,
  storeSetter: config.storeSetter,
  buildLoadingState: config.buildLoadingState,
  buildSuccessState: (data) => config.buildSuccessState(data as TData),
  buildErrorState: config.buildErrorState,
  renderQuotaItems: (quota, t, helpers) => config.renderQuotaItems(quota as TState, t, helpers),
});

const ACCOUNT_ANTIGRAVITY_QUOTA_CONFIG = adaptQuotaConfig(ANTIGRAVITY_CONFIG);
const ACCOUNT_CLAUDE_QUOTA_CONFIG = adaptQuotaConfig(CLAUDE_CONFIG);
const ACCOUNT_CODEX_QUOTA_CONFIG = adaptQuotaConfig(CODEX_CONFIG);
const ACCOUNT_KIMI_QUOTA_CONFIG = adaptQuotaConfig(KIMI_CONFIG);
const ACCOUNT_XAI_QUOTA_CONFIG = adaptQuotaConfig(XAI_CONFIG);

type AccountQuotaTarget = {
  key: string;
  authIndex: string;
  authLabel: string;
  fileName: string;
  file: AuthFileItem;
  config: AnyQuotaConfig;
};

type AccountQuotaEntry = {
  key: string;
  authLabel: string;
  fileName: string;
  providerLabel: string;
  quota?: QuotaStatusState;
  config: AnyQuotaConfig;
};

type AccountQuotaState = {
  status: 'idle' | 'loading' | 'success' | 'error';
  targetKey: string;
  error?: string;
  lastRefreshedAt?: number;
};

type AccountQuotaSourceRow = Pick<MonitoringEventRow, 'authIndex' | 'account' | 'authLabel'>;

type AccountSummaryMetric = {
  key: string;
  label: string;
  value: ReactNode;
  valueClassName?: string;
};

const roundCurrency = (value: number) => Math.round(value * 100) / 100;

const formatDeltaPercent = (current: number, previous: number) => {
  const roundedCurrent = roundCurrency(current);
  const roundedPrevious = roundCurrency(previous);
  if (roundedPrevious <= 0) return roundedCurrent > 0 ? '+100.0%' : '0.0%';
  const delta = (roundedCurrent - roundedPrevious) / roundedPrevious;
  return `${delta >= 0 ? '+' : ''}${(delta * 100).toFixed(1)}%`;
};

const createPriceDraft = (rule?: ModelPriceRule): PriceDraft => ({
	input: rule ? String(rule.base.input) : '',
	output: rule ? String(rule.base.output) : '',
	cacheRead: rule ? String(rule.base.cacheRead) : '',
	cacheWrite: rule ? String(rule.base.cacheWrite) : '',
	tiers: rule?.tiers?.map((tier) => ({
		contextSize: String(tier.contextSize),
		input: String(tier.input),
		output: String(tier.output),
		cacheRead: String(tier.cacheRead),
		cacheWrite: String(tier.cacheWrite),
	})) ?? [],
});

const parsePriceValue = (value: string) => {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

const formatModelPriceRate = (value: number | undefined) => {
  const normalized = Number(value) || 0;
  return `$${normalized.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 })}`;
};

const MODEL_PRICE_SYNC_RATE_FIELDS = [
  ['input', 'usage_stats.model_price_input'],
  ['output', 'usage_stats.model_price_output'],
  ['cacheRead', 'usage_stats.model_price_cache_read'],
  ['cacheWrite', 'usage_stats.model_price_cache_write'],
] as const;

const ACCOUNT_QUOTA_RENDER_HELPERS: QuotaRenderHelpers = {
  styles: {
    ...authFileQuotaStyles,
    quotaRow: `${authFileQuotaStyles.quotaRow} ${styles.accountQuotaRow}`,
    quotaRowHeader: `${authFileQuotaStyles.quotaRowHeader} ${styles.accountQuotaRowHeader}`,
    quotaModel: `${authFileQuotaStyles.quotaModel} ${styles.accountQuotaModel}`,
    quotaMeta: `${authFileQuotaStyles.quotaMeta} ${styles.accountQuotaMeta}`,
    quotaAmount: `${authFileQuotaStyles.quotaAmount} ${styles.accountQuotaAmount}`,
    codexPlanValue: `${authFileQuotaStyles.codexPlanValue} ${styles.accountQuotaPlanValue}`,
    premiumPlanValue: `${authFileQuotaStyles.premiumPlanValue} ${styles.accountQuotaPremiumPlanValue}`,
    codexResetCreditRow: `${authFileQuotaStyles.codexResetCreditRow} ${styles.accountQuotaResetCreditRow}`,
    codexResetCreditTime: `${authFileQuotaStyles.codexResetCreditTime} ${styles.accountQuotaResetCreditTime}`,
  },
  QuotaProgressBar: AuthFileQuotaProgressBar,
};

const getQuotaProviderLabel = (config: AnyQuotaConfig, t: TFunction) => {
  const titleKey = `${config.i18nPrefix}.title`;
  const translated = t(titleKey);
  if (translated !== titleKey) return translated;
  return config.type;
};

const getAccountQuotaConfig = (file: AuthFileItem): AnyQuotaConfig | undefined => {
  if (isAntigravityFile(file)) return ACCOUNT_ANTIGRAVITY_QUOTA_CONFIG;
  if (isClaudeFile(file)) return ACCOUNT_CLAUDE_QUOTA_CONFIG;
  if (isCodexFile(file)) return ACCOUNT_CODEX_QUOTA_CONFIG;
  if (isKimiFile(file)) return ACCOUNT_KIMI_QUOTA_CONFIG;
  if (isXaiFile(file)) return ACCOUNT_XAI_QUOTA_CONFIG;
  return undefined;
};

const resolveQuotaErrorMessage = (t: TFunction, quota?: QuotaStatusState): string => {
  if (!quota) return t('common.unknown_error');
  if (quota.errorStatus === 404) return t('common.quota_update_required');
  if (quota.errorStatus === 403) return t('common.quota_check_credential');
  return quota.error || t('common.unknown_error');
};

const hasUsableQuotaContent = (quota?: QuotaStatusState) => {
  if (!quota || quota.status !== 'success') return false;
  const record = quota as unknown as Record<string, unknown>;
  const billing = record.billing;
  return ['groups', 'windows', 'buckets', 'rows'].some((key) => {
    const value = record[key];
    return Array.isArray(value) && value.length > 0;
  }) || Boolean(
    record.planType
    || record.tierLabel
    || record.creditBalance !== undefined
    || (billing && typeof billing === 'object' && !Array.isArray(billing))
  );
};

const getQuotaForTarget = (store: QuotaStore, target: AccountQuotaTarget): QuotaStatusState | undefined => {
  return target.config.storeSelector(store)[target.fileName] as QuotaStatusState | undefined;
};

const requestAccountQuota = async (
  target: AccountQuotaTarget,
  t: TFunction
): Promise<QuotaStatusState> => {
  try {
    const data = await target.config.fetchQuota(target.file, t);
    return target.config.buildSuccessState(data) as QuotaStatusState;
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : t('common.unknown_error');
    return target.config.buildErrorState(message, getStatusFromError(err)) as QuotaStatusState;
  }
};

const settleWithConcurrency = async <T, R>(
  items: T[],
  concurrency: number,
  worker: (item: T) => Promise<R>
): Promise<Array<PromiseSettledResult<R>>> => {
  const results = new Array<PromiseSettledResult<R>>(items.length);
  let nextIndex = 0;
  const workerCount = Math.min(Math.max(concurrency, 1), items.length);
  await Promise.all(Array.from({ length: workerCount }, async () => {
    while (nextIndex < items.length) {
      const index = nextIndex;
      nextIndex += 1;
      try {
        results[index] = { status: 'fulfilled', value: await worker(items[index]) };
      } catch (reason) {
        results[index] = { status: 'rejected', reason };
      }
    }
  }));
  return results;
};

const buildAccountQuotaTargetsByAccount = (
  rows: AccountQuotaSourceRow[],
  authFilesByAuthIndex: Map<string, AuthFileItem>
) => {
  const grouped = new Map<string, Map<string, AccountQuotaTarget>>();

  rows.forEach((row) => {
    const authIndex = normalizeAuthIndex(row.authIndex);
    if (!authIndex || !row.account) return;

    const file = authFilesByAuthIndex.get(authIndex);
    if (!file) return;

    const quotaConfig = getAccountQuotaConfig(file);
    if (!quotaConfig) return;

    const dedupeKey = `${quotaConfig.type}::${authIndex}::${file.name}`;
    const bucket = grouped.get(row.account) ?? new Map<string, AccountQuotaTarget>();
    if (!bucket.has(dedupeKey)) {
      bucket.set(dedupeKey, {
        key: dedupeKey,
        authIndex,
        authLabel: row.authLabel || file.name || authIndex,
        fileName: file.name,
        file,
        config: quotaConfig,
      });
    }
    grouped.set(row.account, bucket);
  });

  return new Map(
    Array.from(grouped.entries()).map(([account, bucket]) => [
      account,
      Array.from(bucket.values()).sort((left, right) => left.authLabel.localeCompare(right.authLabel)),
    ])
  );
};

const buildAccountQuotaEntriesByAccount = (
  targetsByAccount: Map<string, AccountQuotaTarget[]>,
  quotaStore: QuotaStore,
  t: TFunction
) => new Map(
  Array.from(targetsByAccount.entries()).map(([account, targets]) => [
    account,
    targets.map((target) => ({
      key: target.key,
      authLabel: target.authLabel,
      fileName: target.fileName,
      providerLabel: getQuotaProviderLabel(target.config, t),
      quota: getQuotaForTarget(quotaStore, target),
      config: target.config,
    } satisfies AccountQuotaEntry)),
  ])
);

const getSuccessRateClassName = (rate: number) =>
  rate >= 0.95 ? styles.goodText : rate >= 0.85 ? styles.warnText : styles.badText;

const getAccountStatusDotClassName = (tone: AccountHealthTone) => {
  if (tone === 'good') return styles.accountStatusDotEnabled;
  if (tone === 'warn') return styles.accountStatusDotMixed;
  return styles.accountStatusDotDisabled;
};

function AccountHealthStatusPanel({
  row,
  hasPrices,
  locale,
  t,
  statusData,
  scopeText,
}: {
  row: MonitoringAccountRow;
  hasPrices: boolean;
  locale: string;
  t: TFunction;
  statusData: AccountStatusData;
  scopeText: string;
}) {
  const healthMetrics = [
    { key: 'total-calls', label: t('monitoring.total_calls'), value: formatCompactNumber(row.totalCalls) },
    {
      key: 'success-failure',
      label: t('monitoring.success_failure'),
      value: <SuccessFailureValue success={row.successCalls} failure={row.failureCalls} />,
    },
    { key: 'estimated-cost', label: t('monitoring.estimated_cost'), value: hasPrices ? formatUsd(row.totalCost) : '--', className: styles.primaryText },
    { key: 'success-rate', label: t('monitoring.success_rate'), value: formatPercent(row.successRate), className: getSuccessRateClassName(row.successRate) },
  ];

  return (
    <section className={styles.accountOverviewStatusSection}>
      <div className={styles.accountSectionHeader}>
        <strong>{t('monitoring.account_health_status')}</strong>
        <span className={styles.accountSectionInfo} title={t('monitoring.account_health_status_hint')}>
          i
        </span>
      </div>
      <div className={styles.healthMetricGrid}>
        {healthMetrics.map((metric) => (
          <div key={metric.key} className={styles.healthMetricItem}>
            <span>{metric.label}</span>
            <strong className={metric.className}>{metric.value}</strong>
          </div>
        ))}
      </div>
      <MonitoringHealthStatusBar statusData={statusData} locale={locale} t={t} showRate={false} />
      <div className={styles.accountScopeText}>{scopeText}</div>
    </section>
  );
}

function AccountTokenMetricGrid({ metrics, t }: { metrics: AccountSummaryMetric[]; t: TFunction }) {
  const getTokenMetricToneClassName = (key: string) => {
    if (key === 'input-tokens') return styles.accountMetricIconInput;
    if (key === 'output-tokens') return styles.accountMetricIconOutput;
    if (key === 'cached-tokens') return styles.accountMetricIconCached;
    return styles.accountMetricIconTotal;
  };

  return (
    <section className={styles.accountTokenPanel}>
      <div className={styles.accountSectionHeader}>
        <strong>{t('monitoring.token_usage')}</strong>
      </div>
      <div className={styles.accountOverviewMetricGrid}>
        {metrics.map((metric) => (
          <div key={metric.key} className={styles.accountOverviewMetricCard}>
            <span className={styles.accountOverviewMetricLabel}>
              <span
                className={[styles.accountMetricIcon, getTokenMetricToneClassName(metric.key)]
                  .filter(Boolean)
                  .join(' ')}
                aria-hidden="true"
              />
              {metric.label}
            </span>
            <strong className={[styles.accountOverviewMetricValue, metric.valueClassName].filter(Boolean).join(' ')}>
              {metric.value}
            </strong>
          </div>
        ))}
      </div>
    </section>
  );
}

function AccountModelUsageList({
  row,
  hasPrices,
  locale,
  t,
  limit = 1,
}: {
  row: MonitoringAccountRow;
  hasPrices: boolean;
  locale: string;
  t: TFunction;
  limit?: number;
}) {
  const [showAll, setShowAll] = useState(false);
  const [expandedModels, setExpandedModels] = useState<Record<string, boolean>>({});
  const hasExtraModels = row.models.length > limit;
  const visibleModels = showAll ? row.models : row.models.slice(0, limit);
  const toggleModel = (key: string) => setExpandedModels((previous) => ({ ...previous, [key]: !previous[key] }));

  return (
    <section className={styles.accountModelListPanel}>
      <div className={styles.accountSectionHeader}>
        <strong>{t('monitoring.top_models')}</strong>
        {hasExtraModels ? (
          <button type="button" className={styles.accountModelViewAllButton} onClick={() => setShowAll((previous) => !previous)}>
            {showAll ? t('monitoring.collapse') : t('monitoring.view_all')}
          </button>
        ) : null}
      </div>

      {visibleModels.length > 0 ? (
        <div className={styles.accountModelList}>
          {visibleModels.map((model) => {
            const modelKey = `${row.id}-${model.model}`;
            const isModelExpanded = Boolean(expandedModels[modelKey]);
            return (
              <div key={modelKey} className={styles.accountModelItem}>
                <button
                  type="button"
                  className={styles.accountModelRow}
                  onClick={() => toggleModel(modelKey)}
                  aria-expanded={isModelExpanded}
                >
                  <span className={styles.accountModelName} title={model.model}>{model.model}</span>
                  <span className={styles.accountModelMetaLine}>
                    <span className={styles.accountModelStat}><small>{t('monitoring.ranking_metric_requests')}</small><strong>{formatCompactNumber(model.totalCalls)}</strong></span>
                    <span className={styles.accountModelStat}><small>{t('monitoring.success_rate')}</small><strong className={getSuccessRateClassName(model.successRate)}>{formatPercent(model.successRate)}</strong></span>
                    <span className={styles.accountModelStat}><small>{t('monitoring.ranking_metric_tokens')}</small><strong>{formatCompactNumber(model.totalTokens)}</strong></span>
                    <span className={styles.accountModelStat}><small>{t('monitoring.ranking_metric_cost')}</small><strong>{hasPrices ? formatUsd(model.totalCost) : '--'}</strong></span>
                    <span className={styles.accountModelChevron} aria-hidden="true">{isModelExpanded ? <IconChevronDown size={14} /> : '›'}</span>
                  </span>
                </button>
                {isModelExpanded ? (
                  <div className={styles.accountModelExpanded}>
                    <div className={styles.accountModelExpandedItem}><small>{t('monitoring.input_tokens')}</small><strong>{formatCompactNumber(model.inputTokens)}</strong></div>
                    <div className={styles.accountModelExpandedItem}><small>{t('monitoring.output_tokens')}</small><strong>{formatCompactNumber(model.outputTokens)}</strong></div>
                    <div className={styles.accountModelExpandedItem}><small>{t('monitoring.cached_tokens')}</small><strong>{formatCompactNumber(model.cachedTokens)}</strong></div>
                    <div className={styles.accountModelExpandedItem}><small>{t('monitoring.latest_request_time')}</small><strong>{new Date(model.lastSeenAt).toLocaleString(locale)}</strong></div>
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : (
        <div className={styles.emptyBlockSmall}>{t('monitoring.no_model_data')}</div>
      )}
    </section>
  );
}

function AccountQuotaPanel({
  quotaState,
  quotaEntries,
  locale,
  t,
  onRefreshQuota,
}: {
  quotaState?: AccountQuotaState;
  quotaEntries: AccountQuotaEntry[];
  locale: string;
  t: TFunction;
  onRefreshQuota: () => void;
}) {
  const quotaLoading = quotaState?.status === 'loading';
  const lastQuotaSync = quotaState?.lastRefreshedAt && Number.isFinite(quotaState.lastRefreshedAt)
    ? new Date(quotaState.lastRefreshedAt).toLocaleString(locale)
    : '';
  const quotaTitle = quotaEntries.length === 1 ? quotaEntries[0].providerLabel : t('quota_management.title');

  const renderRefreshButton = () => (
    <button type="button" className={styles.quotaRefreshButton} onClick={onRefreshQuota} disabled={quotaLoading}>
      <IconRefreshCw size={14} className={quotaLoading ? styles.refreshIconSpinning : styles.refreshIcon} />
      <span>{t('codex_quota.refresh_button')}</span>
    </button>
  );

  return (
    <section className={styles.quotaSection}>
      <div className={styles.quotaSectionHeader}>
        <div className={styles.quotaSectionTitleGroup}>
          <strong>{quotaTitle}</strong>
          {lastQuotaSync ? <span>{`${t('monitoring.last_sync')}: ${lastQuotaSync}`}</span> : null}
        </div>
        {renderRefreshButton()}
      </div>

      {quotaLoading && quotaEntries.length === 0 ? <div className={styles.quotaStateMessage}>{t('codex_quota.loading')}</div> : null}
      {!quotaLoading && quotaState?.status === 'error' && quotaEntries.length === 0 ? (
        <div className={styles.quotaStateMessage}>{t('codex_quota.load_failed', { message: quotaState.error || t('common.unknown_error') })}</div>
      ) : null}
      {!quotaLoading && quotaState?.status === 'success' && quotaEntries.length === 0 ? <div className={styles.quotaStateMessage}>{t('monitoring.account_quota_empty')}</div> : null}
      {!quotaState && quotaEntries.length === 0 ? <div className={styles.quotaStateMessage}>{t('monitoring.account_quota_empty')}</div> : null}

      {quotaEntries.length > 0 ? (
        <div className={styles.quotaEntryGrid}>
          {quotaEntries.map((entry) => (
            <div key={entry.key} className={styles.quotaEntryCard}>
              <div className={styles.quotaEntryHeader}>
                <div className={styles.quotaEntryMain}>
                  <strong>{entry.authLabel}</strong>
                </div>
              </div>

              {entry.quota?.status === 'loading' ? (
                <div className={styles.quotaStateMessage}>{t(`${entry.config.i18nPrefix}.loading`)}</div>
              ) : entry.quota?.status === 'error' ? (
                <div className={styles.quotaStateMessage}>
                  {t(`${entry.config.i18nPrefix}.load_failed`, { message: resolveQuotaErrorMessage(t, entry.quota) })}
                </div>
              ) : hasUsableQuotaContent(entry.quota) ? (
                <div className={`${authFileQuotaStyles.quotaSection} ${styles.accountQuotaContent}`}>
                  {entry.config.renderQuotaItems(entry.quota!, t, ACCOUNT_QUOTA_RENDER_HELPERS)}
                </div>
              ) : (
                <div className={styles.quotaStateMessage}>{t(`${entry.config.i18nPrefix}.idle`)}</div>
              )}
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}

function AccountExpandedDetails({
  row,
  hasPrices,
  locale,
  t,
  quotaState,
  quotaEntries,
  onRefreshQuota,
}: {
  row: MonitoringAccountRow;
  hasPrices: boolean;
  locale: string;
  t: TFunction;
  quotaState?: AccountQuotaState;
  quotaEntries: AccountQuotaEntry[];
  onRefreshQuota: () => void;
}) {
  return (
    <div className={styles.accountOverviewCardBody}>
      <AccountQuotaPanel quotaState={quotaState} quotaEntries={quotaEntries} locale={locale} t={t} onRefreshQuota={onRefreshQuota} />
      <AccountModelUsageList row={row} hasPrices={hasPrices} locale={locale} t={t} />
    </div>
  );
}

function AccountOverviewCard({
  row,
  hasPrices,
  locale,
  t,
  isExpanded,
  statusData,
  scopeText,
  quotaState,
  quotaEntries,
  onToggle,
  onRefreshQuota,
}: {
  row: MonitoringAccountRow;
  hasPrices: boolean;
  locale: string;
  t: TFunction;
  isExpanded: boolean;
  statusData: AccountStatusData;
  scopeText: string;
  quotaState?: AccountQuotaState;
  quotaEntries: AccountQuotaEntry[];
  onToggle: () => void;
  onRefreshQuota: () => void;
}) {
  const summaryMetrics = buildAccountSummaryMetrics(row, hasPrices, locale, t);
  const cardMetrics = sortAccountOverviewCardMetrics(summaryMetrics, t);
  const tone = getAccountHealthTone(row);
  const latestRequestText = new Date(row.lastSeenAt).toLocaleString(locale);
  const accountLabel = buildAccountCardFileName(row, quotaEntries);
  const providerText = buildAccountCardProviderText(row);

  return (
    <Card
      className={[
        styles.accountOverviewCard,
        isExpanded ? styles.accountOverviewCardExpanded : '',
      ]
        .filter(Boolean)
        .join(' ')}
    >
      <div className={styles.accountOverviewCardHeader}>
        <div className={styles.accountTitleRow}>
          <button
            type="button"
            className={styles.accountButton}
            onClick={onToggle}
            aria-expanded={isExpanded}
            title={accountLabel}
          >
            <span className={styles.accountExpandGlyph} aria-hidden="true">
              {isExpanded ? <IconChevronUp size={15} /> : <IconChevronDown size={15} />}
            </span>
            <span className={styles.accountIdentityLine}>
              <span className={[styles.accountStatusDot, getAccountStatusDotClassName(tone)].filter(Boolean).join(' ')} aria-hidden="true" />
              <span className={styles.accountButtonLabel}>{accountLabel}</span>
            </span>
          </button>
          <span className={`${styles.accountHealthBadge} ${styles[`accountHealthBadge${tone}`]}`}>
            {tone === 'good' ? t('monitoring.health_good') : tone === 'warn' ? t('monitoring.health_warn') : t('monitoring.health_bad')}
          </span>
        </div>
        <div className={styles.accountMetaRow}>
          <span className={styles.accountOverviewCardTimestamp} title={providerText}>{providerText}</span>
          <span className={styles.accountMetaSeparator}>·</span>
          <span className={styles.accountOverviewCardTimestamp}>{t('monitoring.latest_request_time_value', { value: latestRequestText })}</span>
        </div>
      </div>

      <AccountHealthStatusPanel row={row} hasPrices={hasPrices} locale={locale} t={t} statusData={statusData} scopeText={scopeText} />
      <AccountTokenMetricGrid metrics={cardMetrics} t={t} />

      {isExpanded ? (
        <AccountExpandedDetails
          row={row}
          hasPrices={hasPrices}
          locale={locale}
          t={t}
          quotaState={quotaState}
          quotaEntries={quotaEntries}
          onRefreshQuota={onRefreshQuota}
        />
      ) : null}
    </Card>
  );
}

function AccountStatsPanel({
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

function StatusBadge({ tone, children }: { tone: MonitoringStatusTone; children: ReactNode }) {
  return <span className={`${styles.statusBadge} ${styles[`tone${tone}`]}`}>{children}</span>;
}

function RealtimeErrorDetailsPanel({
  row,
  t,
  language,
}: {
  row: RealtimeLogRow;
  t: ReturnType<typeof useTranslation>['t'];
  language?: string;
}) {
  const categoryText = translateRealtimeErrorCategory(row.errorCategoryKey, t, language);
  const statusText = buildRealtimeStatusLabel(row, t('monitoring.result_failed'));
  const summaryText = row.errorMessage
    ? compactRealtimeErrorMessage(row.errorMessage, 220)
    : row.errorSummary || row.diagnosticText || categoryText;
  const detailItems = [
    { label: translateRealtimeErrorText('http_status', t, language), value: row.statusCode !== null ? String(row.statusCode) : '-' },
    { label: translateRealtimeErrorText('error_code', t, language), value: row.errorCode || '-' },
    { label: translateRealtimeErrorText('upstream_request_id', t, language), value: row.upstreamRequestId || '-' },
    { label: translateRealtimeErrorText('retry_after', t, language), value: row.retryAfter || '-' },
  ].filter((item) => item.value !== '-');

  return (
    <div className={styles.realtimeErrorDetailsPanel}>
      <div className={styles.realtimeErrorOverview}>
        <div className={styles.realtimeErrorOverviewTop}>
          <StatusBadge tone="bad">{statusText}</StatusBadge>
          <span>{categoryText}</span>
        </div>
        <strong>{summaryText}</strong>
      </div>
      {row.errorMessage ? (
        <div className={styles.realtimeErrorMessageBlock}>
          <span>{translateRealtimeErrorText('error_message', t, language)}</span>
          <pre className={styles.realtimeErrorMessage}>{compactRealtimeErrorMessage(row.errorMessage, 1200)}</pre>
        </div>
      ) : null}
      {detailItems.length > 0 ? (
        <div className={styles.realtimeErrorDetailsGrid}>
          {detailItems.map((item) => (
            <div key={item.label} className={styles.realtimeErrorDetailItem}>
              <span>{item.label}</span>
              <strong>{maskSensitiveText(item.value)}</strong>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function RecentPattern({
  pattern,
  variant = 'default',
  label,
}: {
  pattern: boolean[];
  variant?: 'default' | 'plain';
  label?: string;
}) {
  const normalized = pattern.length > 0 ? pattern : Array.from({ length: 10 }, () => true);
  const successCount = normalized.filter(Boolean).length;
  const failureCount = normalized.length - successCount;
  const ariaLabel = label ?? `Recent ${normalized.length} requests: ${successCount} succeeded, ${failureCount} failed`;
  const containerClassName = [
    styles.patternBars,
    variant === 'plain' ? styles.patternBarsPlain : '',
  ]
    .filter(Boolean)
    .join(' ');
  const barClassName = [styles.patternBar, variant === 'plain' ? styles.patternBarPlain : '']
    .filter(Boolean)
    .join(' ');

  return (
    <div className={containerClassName} role="img" aria-label={ariaLabel}>
      {normalized.map((item, index) => (
        <span
          key={index}
          className={`${barClassName} ${item ? styles.patternSuccess : styles.patternFailed}`}
          aria-hidden="true"
        />
      ))}
    </div>
  );
}

export function MonitoringCenterPage() {
  const { t, i18n } = useTranslation();
  const config = useConfigStore((state) => state.config);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const quotaStore = useQuotaStore((state) => state);
  const [timeRange, setTimeRange] = useState<MonitoringTimeRange>('today');
  const [searchInput, setSearchInput] = useState('');
  const [selectedProvider, setSelectedProvider] = useState('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [selectedApiKey, setSelectedApiKey] = useState('all');
  const [selectedStatus, setSelectedStatus] = useState<StatusFilter>('all');
  const [expandedAccounts, setExpandedAccounts] = useState<Record<string, boolean>>({});
  const [selectedRealtimeErrorRow, setSelectedRealtimeErrorRow] = useState<RealtimeLogRow | null>(null);
  const [isPriceModalOpen, setIsPriceModalOpen] = useState(false);
  const [isMonitoringSettingsOpen, setIsMonitoringSettingsOpen] = useState(false);
  const [isMonitoringSettingsLoading, setIsMonitoringSettingsLoading] = useState(false);
  const [isMonitoringSettingsSaving, setIsMonitoringSettingsSaving] = useState(false);
  const [isMonitoringStatisticsResetting, setIsMonitoringStatisticsResetting] = useState(false);
  const [monitoringSettingsDraft, setMonitoringSettingsDraft] = useState<MonitoringSettingsDraft>(() => createMonitoringSettingsDraft());
  const [priceManagementView, setPriceManagementView] = useState<PriceManagementView>('rules');
  const [priceRuleSearch, setPriceRuleSearch] = useState('');
  const [priceSyncChangeFilter, setPriceSyncChangeFilter] = useState<PriceSyncChangeFilter>('all');
  const [priceSyncLockedOverrides, setPriceSyncLockedOverrides] = useState<string[]>([]);
  const [priceModel, setPriceModel] = useState('');
  const [priceDraft, setPriceDraft] = useState<PriceDraft>(() => createPriceDraft());
  const [priceRules, setPriceRules] = useState<ModelPriceRule[]>([]);
  const [observedPriceModels, setObservedPriceModels] = useState<ObservedModelPriceTarget[]>([]);
  const [priceSyncState, setPriceSyncState] = useState<ModelPriceSyncState>({ status: 'idle' });
  const [priceSyncResult, setPriceSyncResult] = useState<ModelPriceSyncResult | null>(null);
  const [isPriceLoading, setIsPriceLoading] = useState(false);
  const [isPriceSaving, setIsPriceSaving] = useState(false);
  const [isPriceSyncing, setIsPriceSyncing] = useState(false);
  const [isImportingUsage, setIsImportingUsage] = useState(false);
  const importInputRef = useRef<HTMLInputElement | null>(null);
  const [accountQuotaStates, setAccountQuotaStates] = useState<Record<string, AccountQuotaState>>({});
  const [isUsageTrendHidden, setIsUsageTrendHidden] = useState(false);
  const [modelRankingMetric, setModelRankingMetric] = useState<RankingMetric>('requests');
  const [apiKeyRankingMetric, setApiKeyRankingMetric] = useState<RankingMetric>('requests');
  const [usageTrendApiKey, setUsageTrendApiKey] = useState('all');
  const [accountStatsMetric, setAccountStatsMetric] = useState<AccountSortMetric>('recent');
  const [isAccountStatsHidden, setIsAccountStatsHidden] = useState(false);
  const [realtimeLogUsage, setRealtimeLogUsage] = useState<UsagePayload | null>(null);
  const [realtimeLogColumns, setRealtimeLogColumns] = useState<RealtimeLogColumnPreference[]>(loadRealtimeLogColumns);
  const [realtimeLogFollowEnabled, setRealtimeLogFollowEnabled] = useState(loadRealtimeLogFollowEnabled);
  const [draggedRealtimeLogColumnKey, setDraggedRealtimeLogColumnKey] = useState<RealtimeLogColumnKey | null>(null);
  const [isRealtimeColumnsMenuOpen, setIsRealtimeColumnsMenuOpen] = useState(false);
  const accountQuotaStatesRef = useRef<Record<string, AccountQuotaState>>({});
  const accountQuotaRequestIdsRef = useRef<Record<string, number>>({});
  const realtimeColumnsMenuRef = useRef<HTMLDivElement | null>(null);
  const deferredSearchInput = useDeferredValue(searchInput);
  const [deferredSearch, setDeferredSearch] = useState(searchInput);

  useEffect(() => {
    const timer = setTimeout(() => setDeferredSearch(deferredSearchInput), 300);
    return () => clearTimeout(timer);
  }, [deferredSearchInput]);

  const {
    usage,
    error: usageError,
		latestId,
		modelPrices,
		refreshUsage,
    loadEventPage,
  } = useUsageData();
  const deferredUsage = useDeferredValue(usage);

  const {
    loading: monitoringLoading,
    error: monitoringError,
    authFiles,
    allRows,
    filteredRows,
    refreshMeta,
  } = useMonitoringData({
    usage: deferredUsage,
    logUsage: realtimeLogUsage,
    serverFilteredLogs: true,
    config,
    modelPrices,
    timeRange,
    searchQuery: '',
    deletedCredentialLabel: t('monitoring.deleted_credential'),
    unattributedApiKeyLabel: t('monitoring.api_key_unattributed'),
  });

  const {
    data: usageAggregates,
    error: aggregatesError,
    refresh: refreshAggregates,
  } = useUsageAggregates({
    latestId,
    timeRange,
    apiKeyHash: usageTrendApiKey,
    enabled: connectionStatus === 'connected',
  });

  const searchMatchedAuthIndexFilter = useMemo(() => {
    return findMonitoringAuthIndexes(authFiles, allRows, deferredSearch);
  }, [allRows, authFiles, deferredSearch]);

  const buildRealtimeLogFilters = useCallback((): UsageEventPageFilters => {
    const nowMs = Date.now();
    const fromMs = getRangeStartMs(timeRange, nowMs);
    return {
      fromMs: Number.isFinite(fromMs) && fromMs > 0 ? fromMs : undefined,
      toMs: nowMs,
      provider: selectedProvider === 'all' ? undefined : selectedProvider,
      model: selectedModel === 'all' ? undefined : selectedModel,
      searchAuthIndexes: searchMatchedAuthIndexFilter || undefined,
      apiKeyHash: selectedApiKey === 'all' ? undefined : selectedApiKey,
      status: selectedStatus,
      search: deferredSearch,
      limit: REALTIME_LOG_PAGE_SIZE,
    };
  }, [deferredSearch, searchMatchedAuthIndexFilter, selectedApiKey, selectedModel, selectedProvider, selectedStatus, timeRange]);

  const handleRealtimeLogGenerationChange = useCallback(() => {
    setSelectedRealtimeErrorRow(null);
    void refreshAggregates();
  }, [refreshAggregates]);

  const {
    page: realtimeLogPage,
    matchedTotal: realtimeLogMatchedTotal,
    nextCursor: realtimeLogNextCursor,
    loading: realtimeLogLoading,
    error: realtimeLogError,
    pendingEventCount: pendingRealtimeEventCount,
    autoRefreshPaused: realtimeLogAutoRefreshPaused,
    wrapperRef: realtimeLogWrapperRef,
    handleScroll: handleRealtimeLogScroll,
    refresh: refreshRealtimeLogs,
    reset: resetRealtimeLogs,
    showPreviousPage: showPreviousRealtimeLogPage,
    showNextPage: showNextRealtimeLogPage,
  } = useRealtimeLogData({
    connectionStatus,
    latestId,
    generation: Number(usage?.generation) || 0,
    usage: realtimeLogUsage,
    setUsage: setRealtimeLogUsage,
    loadEventPage,
    buildFilters: buildRealtimeLogFilters,
    followEnabled: realtimeLogFollowEnabled,
    detailsOpen: Boolean(selectedRealtimeErrorRow),
    onGenerationChange: handleRealtimeLogGenerationChange,
  });

  const refreshAll = useCallback(async () => {
    await Promise.all([refreshUsage(), refreshMeta(false), refreshRealtimeLogs()]);
    await refreshAggregates();
  }, [refreshAggregates, refreshMeta, refreshRealtimeLogs, refreshUsage]);

  const fetchMonitoringSettings = useCallback(async () => {
    const response = await apiClient.get<{ settings: MonitoringSettings }>('/usage/settings');
    setMonitoringSettingsDraft(createMonitoringSettingsDraft(response.settings));
    return response.settings;
  }, []);

  const loadMonitoringSettings = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    setIsMonitoringSettingsLoading(true);
    try {
      await fetchMonitoringSettings();
      setIsMonitoringSettingsOpen(true);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringSettingsLoading(false);
    }
  }, [connectionStatus, fetchMonitoringSettings, showNotification, t]);

  const handleSaveMonitoringSettings = useCallback(async (closeModal = true) => {
    const settings = buildMonitoringSettingsFromDraft(monitoringSettingsDraft);
    if (settings.webdav.enabled && !settings.webdav.url) {
      showNotification(t('usage_stats.monitoring_settings_webdav_url_required'), 'warning');
      return;
    }
    setIsMonitoringSettingsSaving(true);
    try {
      const response = await apiClient.put<{ settings: MonitoringSettings }>('/usage/settings', { settings });
      setMonitoringSettingsDraft(createMonitoringSettingsDraft(response.settings));
      if (closeModal) setIsMonitoringSettingsOpen(false);
      showNotification(t('usage_stats.monitoring_settings_saved'), 'success');
      await refreshAll();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringSettingsSaving(false);
    }
  }, [monitoringSettingsDraft, refreshAll, showNotification, t]);

  const executeMonitoringStatisticsReset = useCallback(async () => {
    setIsMonitoringStatisticsResetting(true);
    try {
      const result = await apiClient.post<UsageResetResult>('/usage/reset', { confirm: true });
      setSelectedRealtimeErrorRow(null);
      resetRealtimeLogs();
      await Promise.all([refreshUsage(), refreshRealtimeLogs(), refreshAggregates()]);
      showNotification(t('usage_stats.monitoring_settings_reset_success', { count: result.deletedEvents }), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringStatisticsResetting(false);
    }
  }, [refreshAggregates, refreshRealtimeLogs, refreshUsage, resetRealtimeLogs, showNotification, t]);

  const handleMonitoringStatisticsReset = useCallback(() => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    showConfirmation({
      title: t('usage_stats.monitoring_settings_reset_confirm_title'),
      message: t('usage_stats.monitoring_settings_reset_confirm_message', {
        count: Number(usage?.total_requests) || 0,
      }),
      confirmText: t('usage_stats.monitoring_settings_reset_confirm_button'),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: executeMonitoringStatisticsReset,
    });
  }, [connectionStatus, executeMonitoringStatisticsReset, showConfirmation, showNotification, t, usage?.total_requests]);
  const handleExportUsage = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }

    try {
      const response = await apiClient.getRaw('/usage/export', { responseType: 'blob' });
      const blob = response.data instanceof Blob ? response.data : new Blob([response.data], { type: 'application/x-ndjson' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
      link.href = url;
      link.download = `usage-export-${timestamp}.jsonl`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      showNotification(t('usage_stats.export_success'), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    }
  }, [connectionStatus, showNotification, t]);

  const handleImportUsageClick = useCallback(() => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    importInputRef.current?.click();
  }, [connectionStatus, showNotification, t]);

  const executeUsageImport = useCallback(async (content: string, allowLegacy: boolean) => {
    setIsImportingUsage(true);
    try {
      const result = await apiClient.post<UsageImportResult>('/usage/import', content, {
        headers: { 'Content-Type': 'application/x-ndjson' },
        params: allowLegacy ? { allow_legacy: 1 } : undefined,
      });
      const importedExtras = [
        (result.modelPriceRecords ?? 0) > 0 ? t('usage_stats.import_model_prices_restored', { count: Math.max(result.modelPrices ?? 0, result.modelPriceRules ?? 0) }) : '',
        (result.quotaCacheRecords ?? 0) > 0 ? t('usage_stats.import_quota_cache_restored', { count: result.quotaCache ?? 0 }) : '',
        (result.routingCursorRecords ?? 0) > 0 ? t('usage_stats.import_routing_cursors_restored', { count: result.routingCursors ?? 0 }) : '',
        (result.authRuntimeStatsRecords ?? 0) > 0 ? t('usage_stats.import_auth_runtime_stats_restored', { count: result.authRuntimeStats ?? 0 }) : '',
        result.accountInspectionSchedule ? t('usage_stats.import_account_inspection_schedule_restored') : '',
        result.accountInspectionSnapshot ? t('usage_stats.import_account_inspection_snapshot_restored') : '',
        result.monitoringSettings ? t('usage_stats.import_monitoring_settings_restored') : '',
      ].filter(Boolean).join(' · ');
      showNotification(
        [
          t('usage_stats.import_success', {
            added: result.added ?? 0,
            skipped: result.skipped ?? 0,
            total: result.total ?? 0,
            failed: result.failed ?? 0,
          }),
          importedExtras,
        ].filter(Boolean).join(' · '),
        (result.failed ?? 0) > 0 ? 'warning' : 'success'
      );
      quotaPersistenceMiddleware.markStale();
      await quotaPersistenceMiddleware.ensureFresh();
      await refreshAll();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsImportingUsage(false);
    }
  }, [refreshAll, showNotification, t]);

  const handleImportUsageFile = useCallback(
    async (event: ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0];
      event.target.value = '';
      if (!file) return;

      try {
        const content = await file.text();
        if (!content.trim()) {
          showNotification(t('usage_stats.import_invalid'), 'error');
          return;
        }
        if (!hasUsageBackupManifest(content)) {
          showConfirmation({
            title: t('usage_stats.import_legacy_confirm_title'),
            message: t('usage_stats.import_legacy_confirm_message'),
            confirmText: t('usage_stats.import_legacy_confirm_button'),
            cancelText: t('common.cancel'),
            variant: 'danger',
            onConfirm: () => executeUsageImport(content, true),
          });
          return;
        }
        await executeUsageImport(content, false);
      } catch (error) {
        showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
      }
    },
    [executeUsageImport, showConfirmation, showNotification, t]
  );

  const handleCopyRealtimeDiagnostic = useCallback((row: RealtimeLogRow) => {
    const text = buildRealtimeDiagnosticClipboardText(row, t, i18n.language);
    if (!navigator.clipboard?.writeText) {
      showNotification(translateRealtimeErrorText('copy_diagnostic_failed', t, i18n.language), 'error');
      return;
    }
    void navigator.clipboard.writeText(text)
      .then(() => showNotification(translateRealtimeErrorText('copy_diagnostic_success', t, i18n.language), 'success'))
      .catch(() => showNotification(translateRealtimeErrorText('copy_diagnostic_failed', t, i18n.language), 'error'));
  }, [i18n.language, showNotification, t]);

  useHeaderRefresh(refreshAll);

  const combinedError = [usageError, monitoringError, realtimeLogError].filter(Boolean).join('；');
  const hasPrices = Object.keys(modelPrices).length > 0;

  useEffect(() => {
    saveRealtimeLogFollowEnabled(realtimeLogFollowEnabled);
  }, [realtimeLogFollowEnabled]);

  useEffect(() => {
    accountQuotaStatesRef.current = accountQuotaStates;
  }, [accountQuotaStates]);

  const setQuotaForConfig = useCallback(
    (quotaConfig: AnyQuotaConfig, updater: Record<string, QuotaStatusState> | ((prev: Record<string, QuotaStatusState>) => Record<string, QuotaStatusState>)) => {
      const setter = useQuotaStore.getState()[quotaConfig.storeSetter] as (value: typeof updater) => void;
      setter(updater);
    },
    []
  );

  const requestLogRows = filteredRows;

  const requestLogDerived = useMemo(() => {
    const providers = new Set<string>();
    const models = new Set<string>();
    const apiKeys = new Map<string, string>();

    allRows.forEach((row) => {
      if (row.provider) providers.add(row.provider);
      if (row.model) models.add(row.model);
      if (row.clientApiKey.hash && row.clientApiKey.hash !== '-' && !apiKeys.has(row.clientApiKey.hash)) {
        apiKeys.set(row.clientApiKey.hash, row.clientApiKey.masked);
      }
    });
    usageAggregates?.providers.forEach((bucket) => {
      if (bucket.provider) providers.add(bucket.provider);
    });
    usageAggregates?.models.forEach((bucket) => {
      if (bucket.model) models.add(bucket.model);
    });
    usageAggregates?.apiKeys.forEach((bucket) => {
      if (bucket.apiKeyHash && !apiKeys.has(bucket.apiKeyHash)) {
        apiKeys.set(bucket.apiKeyHash, maskSensitiveText(bucket.apiKeyHash));
      }
    });

    const sortedModels = Array.from(models).filter(Boolean).sort((left, right) => left.localeCompare(right));

    return {
      providerOptions: [
        { value: 'all', label: t('monitoring.filter_all_providers') },
        ...Array.from(providers)
          .filter(Boolean)
          .sort((left, right) => left.localeCompare(right))
          .map((value) => ({ value, label: value })),
      ],
      modelOptions: [
        { value: 'all', label: t('monitoring.filter_all_models') },
        ...sortedModels.map((value) => ({ value, label: value })),
      ],
      apiKeyOptions: [
        { value: 'all', label: t('monitoring.filter_all_api_keys') },
        ...Array.from(apiKeys.entries())
          .sort((left, right) => left[1].localeCompare(right[1]))
          .map(([value, label]) => ({ value, label })),
      ],
    };
  }, [allRows, t, usageAggregates]);
  const {
    providerOptions,
    modelOptions,
    apiKeyOptions,
  } = requestLogDerived;

  const statusOptions = useMemo(
    () => [
      { value: 'all', label: t('monitoring.filter_all_statuses') },
      { value: 'success', label: t('monitoring.filter_status_success') },
      { value: 'failed', label: t('monitoring.filter_status_failed') },
    ],
    [t]
  );

  useEffect(() => {
    if (selectedProvider !== 'all' && !providerOptions.some((option) => option.value === selectedProvider)) {
      setSelectedProvider('all');
    }
    if (selectedModel !== 'all' && !modelOptions.some((option) => option.value === selectedModel)) {
      setSelectedModel('all');
    }
    if (selectedApiKey !== 'all' && !apiKeyOptions.some((option) => option.value === selectedApiKey)) {
      setSelectedApiKey('all');
    }
  }, [apiKeyOptions, modelOptions, providerOptions, selectedApiKey, selectedModel, selectedProvider]);

  const authFilesByAuthIndex = useMemo(() => {
    const map = new Map<string, AuthFileItem>();
    authFiles.forEach((file) => {
      const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
      if (!authIndex || map.has(authIndex)) return;
      map.set(authIndex, file);
    });
    return map;
  }, [authFiles]);

  const scopedRowsState = useMemo(() => ({
    rows: requestLogRows,
    failureCount: requestLogRows.filter((row) => row.failed).length,
  }), [requestLogRows]);
  const scopedRows = scopedRowsState.rows;
  const scopedFailureCount = scopedRowsState.failureCount;

  const usageRowGroups = useMemo(() => {
    const nowMs = Math.max(
      Number(usageAggregates?.snapshotAtMs) || 0,
      allRows.reduce((latest, row) => Math.max(latest, row.timestampMs), 0)
    );
    const summaryWindowStartMs = nowMs - 30 * 60 * 1000;
    const todayStart = new Date(nowMs);
    todayStart.setHours(0, 0, 0, 0);
    const tomorrowStart = new Date(todayStart);
    tomorrowStart.setDate(tomorrowStart.getDate() + 1);
    const yesterdayStart = new Date(todayStart);
    yesterdayStart.setDate(yesterdayStart.getDate() - 1);
    const trendStartMs = getRangeStartMs(timeRange, nowMs);
    const trendStatsRows: MonitoringEventRow[] = [];
    const topSummaryAccumulator = createMonitoringSummaryAccumulator();
    const todaySummaryAccumulator = createMonitoringSummaryAccumulator();
    const trendSummaryAccumulator = createMonitoringSummaryAccumulator();
    let todayCost = 0;
    let yesterdayCost = 0;

    allRows.forEach((row) => {
      if (!row.statsIncluded) return;
      addMonitoringSummaryRow(topSummaryAccumulator, row, summaryWindowStartMs, nowMs);
      if (row.timestampMs >= todayStart.getTime() && row.timestampMs < tomorrowStart.getTime()) {
        addMonitoringSummaryRow(todaySummaryAccumulator, row, summaryWindowStartMs, nowMs);
        todayCost += row.totalCost;
      } else if (row.timestampMs >= yesterdayStart.getTime() && row.timestampMs < todayStart.getTime()) {
        yesterdayCost += row.totalCost;
      }
      if (row.timestampMs >= trendStartMs && row.timestampMs <= nowMs) {
        trendStatsRows.push(row);
        addMonitoringSummaryRow(trendSummaryAccumulator, row, summaryWindowStartMs, nowMs);
      }
    });

    return {
      trendStatsRows,
      topSummary: finalizeMonitoringSummary(topSummaryAccumulator),
      todaySummary: finalizeMonitoringSummary(todaySummaryAccumulator),
      trendSummary: finalizeMonitoringSummary(trendSummaryAccumulator),
      todayCost,
      yesterdayCost,
    };
  }, [allRows, timeRange, usageAggregates?.snapshotAtMs]);
  const {
    trendStatsRows,
    topSummary,
    todaySummary,
    yesterdayCost,
  } = usageRowGroups;

  const clientUsageTrendAnalytics = useMemo(
    () => buildUsageTrendAnalytics(trendStatsRows, timeRange, usageTrendApiKey, t('monitoring.filter_all_api_keys')),
    [trendStatsRows, timeRange, usageTrendApiKey, t]
  );
  const serverUsageTrendAnalytics = useMemo(
    () => buildServerUsageTrendAnalytics(
      usageAggregates,
      usageAggregates?.scopeTimeRange ?? timeRange,
      modelPrices,
      clientUsageTrendAnalytics.apiKeyOptions,
      usageAggregates?.scopeApiKeyHash ?? usageTrendApiKey,
      t('monitoring.api_key_unattributed')
    ),
    [clientUsageTrendAnalytics.apiKeyOptions, modelPrices, t, timeRange, usageAggregates, usageTrendApiKey]
  );
  const aggregateTrendScopeMatches = Boolean(
    usageAggregates
      && usageAggregates.scopeTimeRange === timeRange
      && usageAggregates.scopeApiKeyHash === usageTrendApiKey
  );
  const usageTrendAnalytics = useMemo(() => {
    if (!serverUsageTrendAnalytics || (aggregatesError && !aggregateTrendScopeMatches)) {
      return clientUsageTrendAnalytics;
    }
    if (serverUsageTrendAnalytics.apiKeyRows.length > 0 || clientUsageTrendAnalytics.apiKeyRows.length === 0) {
      return serverUsageTrendAnalytics;
    }
    return {
      ...serverUsageTrendAnalytics,
      apiKeyRows: clientUsageTrendAnalytics.apiKeyRows,
    };
  }, [aggregateTrendScopeMatches, aggregatesError, clientUsageTrendAnalytics, serverUsageTrendAnalytics]);
  const usageTrendApiKeyOptions = usageTrendAnalytics.apiKeyOptions;
  const usageTrendPoints = usageTrendAnalytics.trendPoints;
  const tokenDistributionPoints = usageTrendAnalytics.tokenDistributionPoints;
  useEffect(() => {
    if (usageTrendApiKey !== 'all' && !usageTrendApiKeyOptions.some((option) => option.value === usageTrendApiKey)) {
      setUsageTrendApiKey('all');
    }
  }, [usageTrendApiKey, usageTrendApiKeyOptions]);

  const modelRankingRows = useMemo(
    () => [...usageTrendAnalytics.modelRows]
      .sort((left, right) => (
        getRankingMetricValue(right, modelRankingMetric) - getRankingMetricValue(left, modelRankingMetric)
        || right.totalTokens - left.totalTokens
        || right.totalCalls - left.totalCalls
      )),
    [modelRankingMetric, usageTrendAnalytics.modelRows]
  );
  const modelRankingMetricTotal = usageTrendAnalytics.scopedTotals[modelRankingMetric];
  const apiKeyRankingRows = useMemo(
    () => [...usageTrendAnalytics.apiKeyRows]
      .sort((left, right) => (
        getRankingMetricValue(right, apiKeyRankingMetric) - getRankingMetricValue(left, apiKeyRankingMetric)
        || right.totalCalls - left.totalCalls
        || right.totalCost - left.totalCost
      ))
      .slice(0, 8),
    [apiKeyRankingMetric, usageTrendAnalytics.apiKeyRows]
  );
  const apiKeyRankingMetricTotal = useMemo(
    () => usageTrendAnalytics.apiKeyRows.reduce(
      (total, row) => total + getRankingMetricValue(row, apiKeyRankingMetric),
      0
    ),
    [apiKeyRankingMetric, usageTrendAnalytics.apiKeyRows]
  );
  const accountStatsFilteredRows = useMemo(
    () => trendStatsRows.length > ACCOUNT_STATS_ANALYTICS_ROW_LIMIT
      ? trendStatsRows.slice(0, ACCOUNT_STATS_ANALYTICS_ROW_LIMIT)
      : trendStatsRows,
    [trendStatsRows]
  );
  const clientAccountStatsRows = useMemo(
    () => buildAccountRowsByAccount(accountStatsFilteredRows, true),
    [accountStatsFilteredRows]
  );
  const serverAccountStatsRows = useMemo(
    () => usageAggregates
      ? buildServerAccountRows(usageAggregates.accounts, allRows, authFilesByAuthIndex, modelPrices, t('monitoring.deleted_credential'))
      : null,
    [allRows, authFilesByAuthIndex, modelPrices, t, usageAggregates]
  );
  const accountStatsRows = useMemo(
    () => [...(
      aggregatesError && usageAggregates?.scopeTimeRange !== timeRange
        ? clientAccountStatsRows
        : serverAccountStatsRows ?? clientAccountStatsRows
    )]
      .sort((left, right) => (
        getAccountSortValue(right, accountStatsMetric) - getAccountSortValue(left, accountStatsMetric)
        || right.lastSeenAt - left.lastSeenAt
        || right.totalCalls - left.totalCalls
      )),
    [accountStatsMetric, aggregatesError, clientAccountStatsRows, serverAccountStatsRows, timeRange, usageAggregates?.scopeTimeRange]
  );
  const serverTopSummary = useMemo(
    () => usageAggregates ? buildAggregateSummary(usageAggregates.allSummary, modelPrices) : null,
    [modelPrices, usageAggregates]
  );
  const recentDailySummaries = useMemo(() => {
    if (!usageAggregates) return null;
    const grouped = new Map<string, UsageAggregateBucket[]>();
    usageAggregates.recentDailySummary.forEach((bucket) => {
      const dayKey = buildLocalDayKey(bucket.bucketStartMs);
      const items = grouped.get(dayKey) ?? [];
      items.push(bucket);
      grouped.set(dayKey, items);
    });
    const now = new Date();
    const todayKey = buildLocalDayKey(now.getTime());
    now.setDate(now.getDate() - 1);
    const yesterdayKey = buildLocalDayKey(now.getTime());
    return {
      today: buildAggregateSummary(grouped.get(todayKey) ?? [], modelPrices),
      yesterday: buildAggregateSummary(grouped.get(yesterdayKey) ?? [], modelPrices),
    };
  }, [modelPrices, usageAggregates]);
  const effectiveTopSummary = serverTopSummary ?? topSummary;
  const effectiveTodaySummary = recentDailySummaries?.today ?? todaySummary;
  const effectiveTodayCost = effectiveTodaySummary.totalCost;
  const effectiveYesterdayCost = recentDailySummaries?.yesterday.totalCost ?? yesterdayCost;
  const timeRangeLabel = useMemo(() => buildUsageTrendRangeLabel(timeRange, t), [timeRange, t]);
  const realtimeLogTotalCount = realtimeLogMatchedTotal;
  const realtimeLogTotalPages = realtimeLogTotalCount > 0 ? Math.ceil(realtimeLogTotalCount / REALTIME_LOG_PAGE_SIZE) : 0;
  const normalizedRealtimeLogPage = Math.min(Math.max(1, realtimeLogPage), Math.max(1, realtimeLogTotalPages));
  const realtimeLogPageRows = useMemo(
    () => buildRealtimeLogPageRows(scopedRows, 1, REALTIME_LOG_PAGE_SIZE).rows,
    [scopedRows]
  );
  const realtimeLogPagination = getClientPaginationRange(
    normalizedRealtimeLogPage,
    REALTIME_LOG_PAGE_SIZE,
    realtimeLogTotalCount,
    realtimeLogPageRows.length
  );
  const realtimeLogColumnDefinitions = useMemo<Record<RealtimeLogColumnKey, RealtimeLogColumnDefinition>>(() => ({
    type: {
      key: 'type',
      label: t('monitoring.column_type'),
      colClassName: styles.realtimeTypeCol,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.type,
      render: (row) => (
        <div className={styles.primaryCell}>
          <span>{row.provider}</span>
          <small>{row.account || row.authLabel || row.accountMasked || '-'}</small>
        </div>
      ),
    },
    model: {
      key: 'model',
      label: t('monitoring.column_model'),
      colClassName: styles.realtimeModelCol,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.model,
      render: (row) => (
        <div className={styles.primaryCell}>
          <span className={styles.monoCell}>{row.model}</span>
          <small className={styles.monoCell}>
            {row.modelAlias && row.modelAlias !== row.model ? row.modelAlias : buildRealtimeMetaText(row)}
          </small>
          {row.modelAlias && row.modelAlias !== row.model ? (
            <small className={styles.monoCell}>{buildRealtimeMetaText(row)}</small>
          ) : null}
        </div>
      ),
    },
    reasoningEffort: {
      key: 'reasoningEffort',
      label: t('monitoring.column_reasoning_effort'),
      colClassName: styles.realtimeReasoningCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => `${styles.realtimeCenterCell} ${styles.realtimeNowrapCell}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.reasoningEffort,
      render: (row) => {
        const reasoningEffort = row.reasoningEffort.trim();
        return reasoningEffort ? (
          <span className={`${styles.realtimeReasoningBadge} ${styles.monoCell}`} title={reasoningEffort}>
            <StatusBadge tone="good">{reasoningEffort}</StatusBadge>
          </span>
        ) : (
          <span className={styles.mutedText}>-</span>
        );
      },
    },
    stream: {
      key: 'stream',
      label: t('monitoring.column_stream'),
      colClassName: styles.realtimeStreamCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => `${styles.realtimeCenterCell} ${styles.realtimeNowrapCell}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.stream,
      render: (row) => (
        <span className={`${styles.realtimeReasoningBadge} ${row.stream ? '' : styles.realtimeNonStreamingBadge}`}>
          <StatusBadge tone="good">
            {t(row.stream ? 'monitoring.stream_mode_streaming' : 'monitoring.stream_mode_non_streaming')}
          </StatusBadge>
        </span>
      ),
    },
    apiKey: {
      key: 'apiKey',
      label: t('monitoring.api_key_label'),
      colClassName: styles.realtimeApiKeyCol,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.apiKey,
      render: (row) => <span className={styles.monoCell}>{row.clientApiKey.masked}</span>,
    },
    recent: {
      key: 'recent',
      label: t('monitoring.recent_status'),
      colClassName: styles.realtimeRecentCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => `${styles.realtimeCenterCell} ${styles.realtimeNowrapCell}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.recent,
      render: (row) => (
        <div className={styles.recentStatusCell}>
          <RecentPattern
            pattern={row.recentPattern}
            variant="plain"
            label={t('monitoring.recent_pattern_label', {
              total: row.recentPattern.length,
              success: row.recentSuccessCount,
              failure: row.recentFailureCount,
            })}
          />
        </div>
      ),
    },
    status: {
      key: 'status',
      label: t('monitoring.request_status'),
      colClassName: styles.realtimeStatusCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => styles.realtimeCenterCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.status,
      render: (row) => (
        <div className={styles.primaryCell}>
          {row.failed ? (
            <button
              type="button"
              className={styles.realtimeStatusErrorButton}
              onClick={() => setSelectedRealtimeErrorRow(row)}
              title={translateRealtimeErrorText('error_details_click_hint', t, i18n.language)}
              aria-label={translateRealtimeErrorText('error_details_click_hint', t, i18n.language)}
            >
              <StatusBadge tone="bad">{buildRealtimeStatusLabel(row, t('monitoring.result_failed'))}</StatusBadge>
            </button>
          ) : (
            <StatusBadge tone="good">{t('monitoring.result_success')}</StatusBadge>
          )}
        </div>
      ),
    },
    successRate: {
      key: 'successRate',
      label: t('monitoring.column_success_rate'),
      colClassName: styles.realtimeRateCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: (row) => `${styles.realtimeMetricCell} ${getSuccessRateClassName(row.successRate)}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.successRate,
      render: (row) => formatPercent(row.successRate),
    },
    calls: {
      key: 'calls',
      label: t('monitoring.total_calls'),
      colClassName: styles.realtimeCountCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.calls,
      render: (row) => formatCompactNumber(row.requestCount),
    },
    ttft: {
      key: 'ttft',
      label: t('monitoring.column_ttft'),
      colClassName: styles.realtimeTtftCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.ttft,
      render: (row) => (
        <span
          className={
            row.ttftMs !== null && row.ttftMs >= 15000
              ? styles.badText
              : row.ttftMs !== null && row.ttftMs >= 8000
                ? styles.warnText
                : undefined
          }
        >
          {formatDurationMs(row.ttftMs, { locale: i18n.language })}
        </span>
      ),
    },
    latency: {
      key: 'latency',
      label: t('monitoring.column_latency'),
      colClassName: styles.realtimeLatencyCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.latency,
      render: (row) => (
        <span
          className={
            row.latencyMs !== null && row.latencyMs >= 30000
              ? styles.badText
              : row.latencyMs !== null && row.latencyMs >= 15000
                ? styles.warnText
                : undefined
          }
        >
          {formatDurationMs(row.latencyMs, { locale: i18n.language })}
        </span>
      ),
    },
    tokens: {
      key: 'tokens',
      label: t('monitoring.realtime_tokens_column'),
      colClassName: styles.realtimeUsageCol,
      cellClassName: () => styles.realtimeTokensTableCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.tokens,
      render: (row) => (
        <div className={`${styles.primaryCell} ${styles.realtimeTokenCell}`}>
          <span>{t('monitoring.realtime_tokens_total')}: <strong>{formatTokenCount(row.totalTokens)}</strong></span>
          <small>
            {t('monitoring.realtime_tokens_input')}: {formatTokenCount(row.inputTokens)}
            {' | '}
            {t('monitoring.realtime_tokens_output')}: {formatTokenCount(row.outputTokens)}
          </small>
          {row.reasoningTokens > 0 ? (
            <small>{t('monitoring.realtime_tokens_reasoning')}: {formatTokenCount(row.reasoningTokens)}</small>
          ) : null}
        </div>
      ),
    },
    cacheRead: {
      key: 'cacheRead',
      label: t('monitoring.realtime_cache_read_column'),
      colClassName: styles.realtimeCacheReadCol,
      cellClassName: () => styles.realtimeCacheReadTableCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.cacheRead,
      render: (row) => {
        const hitRate = getCacheHitRate(row);
        return (
          <div className={styles.realtimeCacheReadCell}>
            <strong>{formatTokenCount(row.cachedTokens)}</strong>
            <small className={hitRate !== null && hitRate < 0.8 ? styles.realtimeCacheHitLow : undefined}>
              {hitRate === null ? '--' : formatPercent(hitRate)} {t('monitoring.realtime_cache_hit')}
            </small>
          </div>
        );
      },
    },
    cost: {
      key: 'cost',
      label: t('monitoring.this_call_cost'),
      colClassName: styles.realtimeCostCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.cost,
      render: (row) => <RealtimeCostCell row={row} hasPrices={hasPrices} t={t} />,
    },
    time: {
      key: 'time',
      label: t('monitoring.column_time'),
      colClassName: styles.realtimeTimeCol,
      cellClassName: () => styles.realtimeTimeCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.time,
      render: (row) => new Date(row.timestampMs).toLocaleString(i18n.language),
    },
  }), [hasPrices, i18n.language, t]);
  const visibleRealtimeLogColumns = useMemo(
    () => realtimeLogColumns
      .filter((column) => column.visible)
      .map((column) => {
        const definition = realtimeLogColumnDefinitions[column.key];
        const contentWidth = column.width ?? estimateRealtimeLogColumnWidth(
          column.key,
          definition.label,
          realtimeLogPageRows
        );
        return {
          ...definition,
          width: Math.max(contentWidth, estimateRealtimeLogHeaderWidth(column.key, definition.label)),
        };
      })
      .filter(Boolean),
    [realtimeLogColumnDefinitions, realtimeLogColumns, realtimeLogPageRows]
  );
  const realtimeLogTableMinWidth = useMemo(
    () => visibleRealtimeLogColumns.reduce((total, column) => total + column.width, 0),
    [visibleRealtimeLogColumns]
  );
  const realtimeLogVisibleColumnCount = Math.max(1, visibleRealtimeLogColumns.length);
  const realtimeLogVisiblePreferenceCount = realtimeLogColumns.filter((column) => column.visible).length;

  const accountQuotaTargetsByAccount = useMemo(
    () => {
      if (!usageAggregates || aggregatesError) {
        return buildAccountQuotaTargetsByAccount(accountStatsFilteredRows, authFilesByAuthIndex);
      }
      const sources = Array.from(new Set(usageAggregates.accounts.map((bucket) => bucket.authIndex).filter(Boolean)))
        .map((authIndex) => {
          const file = authFilesByAuthIndex.get(authIndex as string);
          const account = file
            ? [file.email, file.account, file.label, file.name]
                .map((value) => typeof value === 'string' ? value.trim() : '')
                .find(Boolean) || authIndex as string
            : authIndex as string;
          return {
            authIndex: authIndex as string,
            account,
            authLabel: file?.name || account,
          } satisfies AccountQuotaSourceRow;
        });
      return buildAccountQuotaTargetsByAccount(sources, authFilesByAuthIndex);
    },
    [accountStatsFilteredRows, aggregatesError, authFilesByAuthIndex, usageAggregates]
  );
  const accountQuotaEntriesByAccount = useMemo(
    () => buildAccountQuotaEntriesByAccount(accountQuotaTargetsByAccount, quotaStore, t),
    [accountQuotaTargetsByAccount, quotaStore, t]
  );
  const quotaTargetsByAccountForLoading = accountQuotaTargetsByAccount;

  const priceRuleTargets = useMemo<PriceRuleTarget[]>(() => {
    const targets = new Map<string, PriceRuleTarget>();
    observedPriceModels.forEach((item) => {
      const key = item.model;
      const current = targets.get(key);
      targets.set(key, {
        key,
        model: item.model,
        requests: (current?.requests ?? 0) + item.requests,
        lastSeenAtMs: Math.max(current?.lastSeenAtMs ?? 0, item.lastSeenAtMs),
        rule: current?.rule,
      });
    });
    priceRules.forEach((rule) => {
      const key = rule.model;
      const current = targets.get(key);
      targets.set(key, {
        key,
        model: rule.model,
        requests: current?.requests ?? 0,
        lastSeenAtMs: current?.lastSeenAtMs ?? 0,
        rule,
      });
    });
    return Array.from(targets.values()).sort((left, right) => {
      const configuredDelta = Number(Boolean(left.rule)) - Number(Boolean(right.rule));
      if (configuredDelta !== 0) return configuredDelta;
      return right.lastSeenAtMs - left.lastSeenAtMs || left.key.localeCompare(right.key);
    });
  }, [observedPriceModels, priceRules]);

  const filteredPriceRuleTargets = useMemo(() => {
    const query = priceRuleSearch.trim().toLowerCase();
    if (!query) return priceRuleTargets;
    return priceRuleTargets.filter((item) => {
      const source = item.rule ? `${item.rule.sourceProvider ?? ''}/${item.rule.sourceModel ?? ''}` : '';
      return `${item.model} ${source}`.toLowerCase().includes(query);
    });
  }, [priceRuleSearch, priceRuleTargets]);

  const selectedPriceTarget = useMemo(
    () => priceRuleTargets.find((item) => item.model === priceModel) ?? null,
    [priceModel, priceRuleTargets]
  );

  const configuredPriceRuleCount = priceRuleTargets.filter((item) => Boolean(item.rule)).length;
  const unconfiguredPriceRuleCount = priceRuleTargets.length - configuredPriceRuleCount;
  const priceSyncStatus = isPriceSyncing ? 'syncing' : priceSyncState.status;
  const unmatchedPriceModels = priceSyncResult?.unmatched ?? priceSyncState.unmatchedModels ?? [];
  const unmatchedPriceModelCount = priceSyncResult ? unmatchedPriceModels.length : (priceSyncState.unmatched ?? unmatchedPriceModels.length);
  const priceSyncChanges = useMemo(() => priceSyncResult?.changes ?? [], [priceSyncResult]);
  const priceSyncLockedOverrideSet = useMemo(() => new Set(priceSyncLockedOverrides), [priceSyncLockedOverrides]);
  const priceSyncChangeCounts = useMemo(() => {
    const counts: Record<ModelPriceSyncChangeAction, number> = { added: 0, updated: 0, overridden: 0, locked: 0, unmatched: 0 };
    priceSyncChanges.forEach((change) => {
      const action = change.action === 'locked' && priceSyncLockedOverrideSet.has(change.model) ? 'overridden' : change.action;
      counts[action] += 1;
    });
    return counts;
  }, [priceSyncChanges, priceSyncLockedOverrideSet]);
  const filteredPriceSyncChanges = useMemo(
    () => priceSyncChangeFilter === 'all'
      ? priceSyncChanges
      : priceSyncChanges.filter((change) => {
        const action = change.action === 'locked' && priceSyncLockedOverrideSet.has(change.model) ? 'overridden' : change.action;
        return action === priceSyncChangeFilter;
      }),
    [priceSyncChangeFilter, priceSyncChanges, priceSyncLockedOverrideSet]
  );
  const lockedPriceSyncChanges = useMemo(() => priceSyncChanges.filter((change) => change.action === 'locked'), [priceSyncChanges]);
  const allLockedPriceSyncChangesSelected = lockedPriceSyncChanges.length > 0
    && lockedPriceSyncChanges.every((change) => priceSyncLockedOverrideSet.has(change.model));

  const selectedFiltersCount =
    [selectedProvider, selectedModel, selectedApiKey, selectedStatus].filter(
      (value) => value !== 'all'
    ).length + (deferredSearch.trim() ? 1 : 0);

  const usageMetricCards: UsageMetricCard[] = [
    {
      key: 'traffic',
      title: t('monitoring.traffic_title'),
      label: t('monitoring.today_requests'),
      value: formatCompactNumber(effectiveTodaySummary.totalCalls),
      accent: 'blue',
      footer: [
        { label: t('monitoring.total_requests_label'), value: formatCompactNumber(effectiveTopSummary.totalCalls) },
        { label: t('monitoring.total_success_rate'), value: formatPercent(effectiveTopSummary.successRate) },
      ],
    },
    {
      key: 'tokens',
      title: 'Token',
      label: t('monitoring.today_tokens'),
      value: formatCompactNumber(effectiveTodaySummary.totalTokens),
      accent: 'purple',
      footer: [
        { label: t('monitoring.total_tokens_label'), value: formatCompactNumber(effectiveTopSummary.totalTokens) },
        { label: t('monitoring.input_output_reasoning'), value: `${formatCompactNumber(effectiveTopSummary.inputTokens)} / ${formatCompactNumber(effectiveTopSummary.outputTokens)} / ${formatCompactNumber(effectiveTopSummary.reasoningTokens)}` },
      ],
    },
    {
      key: 'cache',
      title: t('monitoring.cache_title'),
      label: t('monitoring.today_cache_hit_rate'),
      value: formatPercent(effectiveTodaySummary.inputTokens > 0 ? effectiveTodaySummary.cachedTokens / effectiveTodaySummary.inputTokens : 0),
      accent: 'green',
      footer: [
        { label: t('monitoring.today_cached_tokens'), value: formatCompactNumber(effectiveTodaySummary.cachedTokens) },
        { label: t('monitoring.total_cache_hits'), value: `${formatCompactNumber(effectiveTopSummary.cachedTokens)} / ${formatPercent(effectiveTopSummary.inputTokens > 0 ? effectiveTopSummary.cachedTokens / effectiveTopSummary.inputTokens : 0)}` },
      ],
    },
    {
      key: 'billing',
      title: t('monitoring.billing_title'),
      label: t('monitoring.today_cost'),
      value: hasPrices ? formatUsd(effectiveTodayCost) : '--',
      accent: 'amber',
      footer: [
        { label: t('monitoring.vs_yesterday'), value: hasPrices ? formatDeltaPercent(effectiveTodayCost, effectiveYesterdayCost) : '--' },
        { label: t('monitoring.total_cost_label'), value: hasPrices ? formatUsd(effectiveTopSummary.totalCost) : '--' },
      ],
    },
  ];

  const clearFilters = useCallback(() => {
    setSearchInput('');
    setSelectedProvider('all');
    setSelectedModel('all');
    setSelectedApiKey('all');
    setSelectedStatus('all');
  }, []);

  const updateRealtimeLogColumns = useCallback((updater: (columns: RealtimeLogColumnPreference[]) => RealtimeLogColumnPreference[]) => {
    setRealtimeLogColumns((current) => {
      const next = normalizeRealtimeLogColumns(updater(current));
      saveRealtimeLogColumns(next);
      return next;
    });
  }, []);

  const toggleRealtimeLogColumn = useCallback((key: RealtimeLogColumnKey) => {
    updateRealtimeLogColumns((columns) => {
      const visibleCount = columns.filter((item) => item.visible).length;
      return columns.map((item) => {
        if (item.key !== key) return item;
        if (item.visible && visibleCount <= 1) return item;
        return { ...item, visible: !item.visible };
      });
    });
  }, [updateRealtimeLogColumns]);

  const reorderRealtimeLogColumn = useCallback((sourceKey: RealtimeLogColumnKey, targetKey: RealtimeLogColumnKey) => {
    if (sourceKey === targetKey) return;
    updateRealtimeLogColumns((columns) => {
      const sourceIndex = columns.findIndex((item) => item.key === sourceKey);
      const targetIndex = columns.findIndex((item) => item.key === targetKey);
      if (sourceIndex < 0 || targetIndex < 0) return columns;
      const next = [...columns];
      const [item] = next.splice(sourceIndex, 1);
      next.splice(targetIndex, 0, item);
      return next;
    });
  }, [updateRealtimeLogColumns]);

  const resizeRealtimeLogColumn = useCallback((key: RealtimeLogColumnKey, width: number) => {
    updateRealtimeLogColumns((columns) => columns.map((column) => (
      column.key === key ? { ...column, width: clampRealtimeLogColumnWidth(key, width) } : column
    )));
  }, [updateRealtimeLogColumns]);

  const handleRealtimeLogHeaderDragStart = useCallback((event: DragEvent<HTMLTableCellElement>, key: RealtimeLogColumnKey) => {
    setDraggedRealtimeLogColumnKey(key);
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', key);
  }, []);

  const handleRealtimeLogHeaderDragOver = useCallback((event: DragEvent<HTMLTableCellElement>) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
  }, []);

  const handleRealtimeLogHeaderDrop = useCallback((event: DragEvent<HTMLTableCellElement>, targetKey: RealtimeLogColumnKey) => {
    event.preventDefault();
    const sourceKey = draggedRealtimeLogColumnKey ?? event.dataTransfer.getData('text/plain');
    if (isRealtimeLogColumnKey(sourceKey)) {
      reorderRealtimeLogColumn(sourceKey, targetKey);
    }
    setDraggedRealtimeLogColumnKey(null);
  }, [draggedRealtimeLogColumnKey, reorderRealtimeLogColumn]);

  const handleRealtimeLogHeaderDragEnd = useCallback(() => {
    setDraggedRealtimeLogColumnKey(null);
  }, []);

  const startRealtimeLogColumnResize = useCallback((event: ReactMouseEvent<HTMLSpanElement>, key: RealtimeLogColumnKey) => {
    event.preventDefault();
    event.stopPropagation();

    const startX = event.clientX;
    const startWidth = visibleRealtimeLogColumns.find((column) => column.key === key)?.width ?? REALTIME_LOG_COLUMN_DEFAULT_WIDTHS[key];
    const handleMouseMove = (moveEvent: MouseEvent) => {
      resizeRealtimeLogColumn(key, startWidth + moveEvent.clientX - startX);
    };
    const handleMouseUp = () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
  }, [resizeRealtimeLogColumn, visibleRealtimeLogColumns]);

  const resetRealtimeLogColumns = useCallback(() => {
    updateRealtimeLogColumns(() => createDefaultRealtimeLogColumns());
  }, [updateRealtimeLogColumns]);

  useEffect(() => {
    if (!isRealtimeColumnsMenuOpen) return undefined;

    const handleDocumentMouseDown = (event: MouseEvent) => {
      if (event.target instanceof Node && realtimeColumnsMenuRef.current?.contains(event.target)) {
        return;
      }
      setIsRealtimeColumnsMenuOpen(false);
    };

    document.addEventListener('mousedown', handleDocumentMouseDown);
    return () => {
      document.removeEventListener('mousedown', handleDocumentMouseDown);
    };
  }, [isRealtimeColumnsMenuOpen]);

  const loadAccountQuota = useCallback(
    async (account: string, force: boolean = false) => {
      const currentState = accountQuotaStatesRef.current[account];
      const targets = quotaTargetsByAccountForLoading.get(account) ?? [];
      const targetKey = targets.map((target) => target.key).join('|');
      if (!force && currentState && currentState.status !== 'idle' && currentState.targetKey === targetKey) {
        return;
      }

      const requestId = (accountQuotaRequestIdsRef.current[account] ?? 0) + 1;
      accountQuotaRequestIdsRef.current[account] = requestId;

      setAccountQuotaStates((previous) => ({
        ...previous,
        [account]: {
          status: 'loading',
          targetKey,
          lastRefreshedAt: previous[account]?.lastRefreshedAt,
        },
      }));

      if (targets.length === 0) {
        if (accountQuotaRequestIdsRef.current[account] !== requestId) return;
        setAccountQuotaStates((previous) => ({
          ...previous,
          [account]: {
            status: 'success',
            targetKey,
            lastRefreshedAt: Date.now(),
          },
        }));
        return;
      }

      targets.forEach((target) => {
        setQuotaForConfig(target.config, (prev) => ({
          ...prev,
          [target.fileName]: target.config.buildLoadingState(),
        }));
      });

      const settled = await settleWithConcurrency(
        targets,
        ACCOUNT_QUOTA_REQUEST_CONCURRENCY,
        (target) => requestAccountQuota(target, t)
      );
      if (accountQuotaRequestIdsRef.current[account] !== requestId) return;

      settled.forEach((result, index) => {
        const target = targets[index];
        const quota = result.status === 'fulfilled'
          ? result.value
          : target.config.buildErrorState(
              result.reason instanceof Error ? result.reason.message : String(result.reason || t('common.unknown_error')),
              getStatusFromError(result.reason)
            ) as QuotaStatusState;

        setQuotaForConfig(target.config, (prev) => ({
          ...prev,
          [target.fileName]: quota,
        }));
      });

      const currentStore = useQuotaStore.getState();
      const entries = targets.map((target) => getQuotaForTarget(currentStore, target)).filter(Boolean) as QuotaStatusState[];
      const hasSuccess = entries.some((entry) => entry.status === 'success');
      const firstError = entries.find((entry) => entry.status === 'error')?.error;
      setAccountQuotaStates((previous) => ({
        ...previous,
        [account]: {
          status: hasSuccess ? 'success' : 'error',
          targetKey,
          error: hasSuccess ? '' : firstError || t('common.unknown_error'),
          lastRefreshedAt: Date.now(),
        },
      }));
    },
    [quotaTargetsByAccountForLoading, setQuotaForConfig, t]
  );

  const toggleAccountExpanded = useCallback((accountId: string, account: string) => {
    if (account && !expandedAccounts[accountId]) {
      void loadAccountQuota(account);
    }
    setExpandedAccounts((previous) => ({
      ...previous,
      [accountId]: !previous[accountId],
    }));
  }, [expandedAccounts, loadAccountQuota]);

  const refreshPriceManagement = useCallback(async () => {
    const [rulesPayload, syncState] = await Promise.all([loadModelPriceRules(), loadModelPriceSyncState()]);
    setPriceRules(rulesPayload.rules);
    setObservedPriceModels(rulesPayload.observedModels);
    setPriceSyncState(syncState);
    return rulesPayload;
  }, []);

  const selectPriceTarget = useCallback((model: string, rules = priceRules) => {
    setPriceModel(model);
    setPriceDraft(createPriceDraft(rules.find((rule) => rule.model === model)));
  }, [priceRules]);

  const openPriceManagement = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    setIsPriceModalOpen(true);
    setPriceManagementView('rules');
    setPriceRuleSearch('');
    setPriceSyncLockedOverrides([]);
    setIsPriceLoading(true);
    setIsMonitoringSettingsLoading(true);
    try {
      const [payload] = await Promise.all([refreshPriceManagement(), fetchMonitoringSettings()]);
      const selectedStillExists = payload.observedModels.some((item) => item.model === priceModel)
        || payload.rules.some((rule) => rule.model === priceModel);
      if (selectedStillExists) {
        selectPriceTarget(priceModel, payload.rules);
      } else {
        const nextTarget = payload.observedModels.find((item) => !payload.rules.some((rule) => rule.model === item.model))
          ?? payload.observedModels[0]
          ?? payload.rules[0];
        if (nextTarget) {
          selectPriceTarget(nextTarget.model, payload.rules);
        } else {
          setPriceModel('');
          setPriceDraft(createPriceDraft());
        }
      }
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setIsPriceLoading(false);
      setIsMonitoringSettingsLoading(false);
    }
  }, [connectionStatus, fetchMonitoringSettings, priceModel, refreshPriceManagement, selectPriceTarget, showNotification, t]);

  const handlePriceDraftChange = useCallback((field: Exclude<keyof PriceDraft, 'tiers'>, value: string) => {
    setPriceDraft((previous) => ({ ...previous, [field]: value }));
  }, []);

	const handlePriceTierChange = useCallback((index: number, field: keyof PriceTierDraft, value: string) => {
		setPriceDraft((previous) => ({
			...previous,
			tiers: previous.tiers.map((tier, tierIndex) => tierIndex === index ? { ...tier, [field]: value } : tier),
		}));
	}, []);

	const addPriceTier = useCallback(() => {
		setPriceDraft((previous) => ({
			...previous,
			tiers: [...previous.tiers, { contextSize: '', input: '', output: '', cacheRead: '', cacheWrite: '' }],
		}));
	}, []);

	const removePriceTier = useCallback((index: number) => {
		setPriceDraft((previous) => ({ ...previous, tiers: previous.tiers.filter((_, tierIndex) => tierIndex !== index) }));
	}, []);

	const resetPriceEditor = useCallback(() => {
		setPriceModel('');
		setPriceDraft(createPriceDraft());
	}, []);

	const handleSavePrice = useCallback(async () => {
		if (!priceModel) {
			return;
		}
		const rule: ModelPriceRule = {
			provider: '',
			model: priceModel,
			base: {
				input: parsePriceValue(priceDraft.input),
				output: parsePriceValue(priceDraft.output),
				cacheRead: parsePriceValue(priceDraft.cacheRead),
				cacheWrite: parsePriceValue(priceDraft.cacheWrite),
			},
			tiers: priceDraft.tiers
				.map((tier) => ({
					contextSize: parseNonNegativeInteger(tier.contextSize),
					input: parsePriceValue(tier.input),
					output: parsePriceValue(tier.output),
					cacheRead: parsePriceValue(tier.cacheRead),
					cacheWrite: parsePriceValue(tier.cacheWrite),
				}))
				.filter((tier) => tier.contextSize > 0),
		};
		setIsPriceSaving(true);
		try {
			await saveModelPriceRule(rule);
			await recalculateModelPriceHistory(false);
			await refreshPriceManagement();
			await refreshAll();
			showNotification(t('usage_stats.model_price_saved'), 'success');
		} catch (error) {
			showNotification(error instanceof Error ? error.message : String(error), 'error');
		} finally {
			setIsPriceSaving(false);
		}
	}, [priceDraft, priceModel, refreshAll, refreshPriceManagement, showNotification, t]);

	const handleDeletePrice = useCallback(
		async (model: string) => {
			try {
				await deleteModelPriceRule(model);
				const payload = await refreshPriceManagement();
				await refreshAll();
				if (priceModel === model) {
					const remainsObserved = payload.observedModels.some((item) => item.model === model);
					if (remainsObserved) {
						selectPriceTarget(model, payload.rules);
					} else {
						const nextTarget = payload.observedModels[0] ?? payload.rules[0];
						if (nextTarget) selectPriceTarget(nextTarget.model, payload.rules);
						else resetPriceEditor();
					}
				}
			} catch (error) {
				showNotification(error instanceof Error ? error.message : String(error), 'error');
			}
		},
		[priceModel, refreshAll, refreshPriceManagement, resetPriceEditor, selectPriceTarget, showNotification]
	);

	const handleSyncPrices = useCallback(async (dryRun = false) => {
		setIsPriceSyncing(true);
		setPriceSyncResult(null);
		setPriceSyncChangeFilter('all');
		if (dryRun) setPriceSyncLockedOverrides([]);
		try {
			const result = await syncModelPricesFromModelsDev(dryRun, dryRun ? [] : priceSyncLockedOverrides);
			setPriceSyncResult(result);
			if (!dryRun) setPriceSyncLockedOverrides([]);
			if (!dryRun) {
				const payload = await refreshPriceManagement();
				if (priceModel) selectPriceTarget(priceModel, payload.rules);
				await refreshAll();
			}
			showNotification(t(dryRun ? 'usage_stats.model_price_sync_preview_complete' : 'usage_stats.model_price_sync_complete', {
				added: result.added,
				updated: result.updated,
				overridden: result.overridden,
				locked: result.locked,
				unmatched: result.unmatched.length,
			}), 'success');
		} catch (error) {
			showNotification(error instanceof Error ? error.message : String(error), 'error');
		} finally {
			setIsPriceSyncing(false);
		}
	}, [priceModel, priceSyncLockedOverrides, refreshAll, refreshPriceManagement, selectPriceTarget, showNotification, t]);

  return (
    <div className={styles.page}>
      <section className={styles.masthead}>
        <div className={styles.mastheadGlow} aria-hidden="true" />

        <div className={styles.mastheadCopy}>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{t('monitoring.title')}</h1>
            <div className={styles.titleActions}>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={() => void handleExportUsage()}
              >
                {t('usage_stats.export')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={handleImportUsageClick}
                disabled={isImportingUsage}
              >
                {isImportingUsage ? t('common.loading') : t('usage_stats.import')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
				onClick={() => void openPriceManagement()}
              >
                {t('usage_stats.model_price_settings')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={() => void loadMonitoringSettings()}
                disabled={isMonitoringSettingsLoading}
                aria-busy={isMonitoringSettingsLoading}
              >
                {t('usage_stats.monitoring_settings')}
              </button>
              <input
                ref={importInputRef}
                type="file"
                accept=".jsonl,.ndjson,.json,application/x-ndjson,application/json"
                className={styles.hiddenFileInput}
                onChange={handleImportUsageFile}
              />
            </div>
          </div>
          <p className={styles.subtitle}>{t('monitoring.console_subtitle')}</p>

          <div className={styles.usageStatsHero}>
            <TopUsageStats cards={usageMetricCards} />
          </div>
        </div>
      </section>

      {!isUsageTrendHidden ? (
        <section className={styles.usageTrendSection}>
          <UsageTrendHeader
            range={timeRange}
            totalCalls={usageTrendAnalytics.scopedTotals.requests}
            apiKeyFilter={usageTrendApiKey}
            apiKeyOptions={usageTrendApiKeyOptions}
            onRangeChange={setTimeRange}
            onApiKeyFilterChange={setUsageTrendApiKey}
            onHide={() => setIsUsageTrendHidden(true)}
            t={t}
          />
          <div className={styles.usageTrendInsightsGrid}>
            <UsageTrendPanel
              points={usageTrendPoints}
              hasPrices={hasPrices}
              emptyText={t('monitoring.no_data')}
              t={t}
            />
            <ApiKeyRankingPanel
              title={t('monitoring.api_key_ranking_title')}
              subtitle={t('monitoring.api_key_ranking_desc')}
              rows={apiKeyRankingRows}
              metric={apiKeyRankingMetric}
              metricTotal={apiKeyRankingMetricTotal}
              onMetricChange={setApiKeyRankingMetric}
              emptyText={t('monitoring.no_data')}
              hasPrices={hasPrices}
              t={t}
            />
          </div>
          <div className={styles.rankingGrid}>
            <ModelStatsPanel
              title={t('monitoring.model_stats_title')}
              subtitle={t('monitoring.model_stats_desc')}
              rows={modelRankingRows}
              metric={modelRankingMetric}
              metricTotal={modelRankingMetricTotal}
              onMetricChange={setModelRankingMetric}
              emptyText={t('monitoring.no_data')}
              hasPrices={hasPrices}
              t={t}
            />
            <TokenDistributionPanel
              points={tokenDistributionPoints}
              emptyText={t('monitoring.no_data')}
              hasPrices={hasPrices}
              t={t}
            />
          </div>
        </section>
      ) : (
        <section className={styles.usageTrendCollapsed}>
          <div>
            <h2>{t('monitoring.usage_stats_title')}</h2>
            <p>{t('monitoring.analysis_hidden_desc')}</p>
          </div>
          <button type="button" className={styles.usageTrendHideButton} onClick={() => setIsUsageTrendHidden(false)}>
            {t('monitoring.show_analysis')}
          </button>
        </section>
      )}

      {!isAccountStatsHidden ? (
        <section className={styles.usageTrendSection}>
          <AccountStatsPanel
            rows={accountStatsRows}
            metric={accountStatsMetric}
            emptyText={t('monitoring.no_data')}
            hasPrices={hasPrices}
            locale={i18n.language}
            t={t}
            rangeLabel={timeRangeLabel}
            range={timeRange}
            onRangeChange={setTimeRange}
            onHide={() => setIsAccountStatsHidden(true)}
            expandedAccounts={expandedAccounts}
            accountQuotaStates={accountQuotaStates}
            accountQuotaEntriesByAccount={accountQuotaEntriesByAccount}
            onMetricChange={setAccountStatsMetric}
            onToggleAccount={toggleAccountExpanded}
            onRefreshQuota={(account) => void loadAccountQuota(account, true)}
          />
        </section>
      ) : (
        <section className={styles.usageTrendCollapsed}>
          <div>
            <h2>{t('monitoring.account_stats_title')}</h2>
            <p>{t('monitoring.account_stats_hidden_desc')}</p>
          </div>
          <button type="button" className={styles.usageTrendHideButton} onClick={() => setIsAccountStatsHidden(false)}>
            {t('monitoring.show_account_stats')}
          </button>
        </section>
      )}

      <section className={styles.usageTrendSection}>
        <div className={styles.usageTrendHeader}>
          <div className={styles.usageTrendCopy}>
            <h2>{t('monitoring.analysis_tab_logs')}</h2>
            <p>
              {selectedFiltersCount > 0
                ? t('monitoring.active_filters_hint', { count: selectedFiltersCount, rows: realtimeLogMatchedTotal })
                : t('monitoring.realtime_table_desc')}
            </p>
          </div>
          <div className={styles.usageTrendActions}>
            <div className={`${styles.rankingMetricSwitch} ${styles.timeRangeControl}`}>
              {TIME_RANGE_OPTIONS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  className={`${styles.rankingMetricButton} ${styles.timeRangeButton} ${timeRange === option.value ? styles.rankingMetricButtonActive : ''}`}
                  onClick={() => setTimeRange(option.value)}
                >
                  {t(option.labelKey)}
                </button>
              ))}
            </div>
          </div>
        </div>

        <Card className={styles.realtimePanel}>
        <div className={styles.filterGrid}>
          <Input
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            placeholder={t('monitoring.search_placeholder')}
            className={styles.toolbarHeaderSearchInput}
            rightElement={<IconSearch size={16} />}
            aria-label={t('monitoring.search_placeholder')}
          />
          <Select
            value={selectedApiKey}
            options={apiKeyOptions}
            onChange={setSelectedApiKey}
            ariaLabel={t('monitoring.filter_api_key')}
          />
          <Select
            value={selectedProvider}
            options={providerOptions}
            onChange={setSelectedProvider}
            ariaLabel={t('monitoring.filter_provider')}
          />
          <Select
            value={selectedModel}
            options={modelOptions}
            onChange={setSelectedModel}
            ariaLabel={t('monitoring.filter_model')}
          />
          <Select
            value={selectedStatus}
            options={statusOptions}
            onChange={(value) => setSelectedStatus(value as StatusFilter)}
            ariaLabel={t('monitoring.filter_status')}
          />
          <button type="button" className={styles.clearButton} onClick={clearFilters}>
            <IconSlidersHorizontal size={16} />
            <span>{t('monitoring.clear_filters')}</span>
          </button>
          <div className={styles.realtimeColumnsMenu} ref={realtimeColumnsMenuRef}>
            <button
              type="button"
              className={styles.clearButton}
              onClick={() => setIsRealtimeColumnsMenuOpen((open) => !open)}
              aria-expanded={isRealtimeColumnsMenuOpen}
            >
              <IconSlidersHorizontal size={16} />
              <span>{t('monitoring.realtime_columns_title')}</span>
            </button>
            {isRealtimeColumnsMenuOpen ? (
              <div className={styles.realtimeColumnsDropdown}>
                <div className={styles.realtimeColumnsDropdownHeader}>
                  <span>{t('monitoring.realtime_columns_hint')}</span>
                  <button type="button" className={styles.inlineActionButton} onClick={resetRealtimeLogColumns}>
                    {t('monitoring.realtime_columns_reset')}
                  </button>
                </div>
                <div className={styles.realtimeColumnsDropdownList}>
                  {realtimeLogColumns.map((column) => {
                    const definition = realtimeLogColumnDefinitions[column.key];
                    return (
                      <label key={column.key} className={styles.realtimeColumnToggle}>
                        <input
                          type="checkbox"
                          checked={column.visible}
                          disabled={column.visible && realtimeLogVisiblePreferenceCount <= 1}
                          onChange={() => toggleRealtimeLogColumn(column.key)}
                        />
                        <span>{definition.label}</span>
                      </label>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </div>
        </div>

        {combinedError ? <div className={styles.errorBox}>{combinedError}</div> : null}

        <div className={styles.realtimeLogStatusRow}>
          <div className={styles.inlineMetrics}>
            <span>{`${t('monitoring.log_rows')}: ${realtimeLogTotalCount}`}</span>
            <span>{`${t('monitoring.recent_failures')}: ${scopedFailureCount}`}</span>
            {realtimeLogMatchedTotal > 0 ? (
              <span>
                {t('monitoring.request_events_page_source_hint', {
                  from: realtimeLogPagination.from,
                  to: realtimeLogPagination.to,
                  total: realtimeLogMatchedTotal,
                  defaultValue: `Showing ${realtimeLogPagination.from}-${realtimeLogPagination.to} of ${realtimeLogMatchedTotal} matching events from a stable snapshot.`,
                })}
              </span>
            ) : null}
          </div>
          <label className={styles.realtimeFollowToggle} title={t('monitoring.request_events_live_follow_hint')}>
            <input
              type="checkbox"
              role="switch"
              checked={realtimeLogFollowEnabled}
              onChange={(event) => setRealtimeLogFollowEnabled(event.target.checked)}
            />
            <span className={styles.realtimeFollowTrack} aria-hidden="true"><span /></span>
            <span className={styles.realtimeFollowLabel}>{t('monitoring.request_events_live_follow')}</span>
          </label>
        </div>

        <div className={styles.realtimeTableShell}>
          {pendingRealtimeEventCount > 0 && realtimeLogAutoRefreshPaused ? (
            <div className={styles.realtimeUpdateBar} role="status" aria-live="polite">
              <div className={styles.realtimeUpdateCopy}>
                <strong>
                  {t('monitoring.request_events_new_available', {
                    count: pendingRealtimeEventCount,
                    defaultValue: `${pendingRealtimeEventCount} new events available`,
                  })}
                </strong>
                <span>{t('monitoring.request_events_paused_hint')}</span>
              </div>
              <button
                type="button"
                className={styles.inlineActionButton}
                onClick={() => void refreshRealtimeLogs('top')}
                disabled={realtimeLogLoading}
              >
                {t('monitoring.request_events_view_latest')}
              </button>
            </div>
          ) : null}

          <div
            ref={realtimeLogWrapperRef}
            className={`${styles.tableWrapper} ${styles.tableScrollWrapper} ${styles.realtimeTableWrapper}`}
            onScroll={handleRealtimeLogScroll}
            aria-busy={realtimeLogLoading}
          >
            <table
              className={`${styles.table} ${styles.realtimeTable}`}
              style={{ '--realtime-table-min-width': `${realtimeLogTableMinWidth}px` } as CSSProperties}
            >
              <colgroup>
                {visibleRealtimeLogColumns.map((column) => (
                  <col key={column.key} className={column.colClassName} style={{ width: `${column.width}px` }} />
                ))}
              </colgroup>
              <thead>
                <tr>
                  {visibleRealtimeLogColumns.map((column) => (
                    <th
                      key={column.key}
                      className={[
                        styles.realtimeDraggableHeader,
                        column.key === 'time' ? styles.realtimeFixedHeader : '',
                        column.headerClassName,
                        draggedRealtimeLogColumnKey === column.key ? styles.realtimeDraggableHeaderActive : '',
                      ].filter(Boolean).join(' ')}
                      draggable={column.key !== 'time'}
                      scope="col"
                      onDragStart={(event) => handleRealtimeLogHeaderDragStart(event, column.key)}
                      onDragOver={handleRealtimeLogHeaderDragOver}
                      onDrop={(event) => handleRealtimeLogHeaderDrop(event, column.key)}
                      onDragEnd={handleRealtimeLogHeaderDragEnd}
                    >
                      <span className={styles.realtimeHeaderContent}>{column.label}</span>
                      <span
                        className={styles.realtimeColumnResizeHandle}
                        role="separator"
                        aria-label={t('monitoring.realtime_column_resize', { column: column.label })}
                        onMouseDown={(event) => startRealtimeLogColumnResize(event, column.key)}
                      />
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {realtimeLogPageRows.map((row) => (
                  <tr
                    key={row.id}
                    data-realtime-row-id={row.id}
                    className={row.failed ? styles.logRowFailed : undefined}
                  >
                    {visibleRealtimeLogColumns.map((column) => (
                      <td key={column.key} className={column.cellClassName?.(row)}>
                        {column.render(row)}
                      </td>
                    ))}
                  </tr>
                ))}
                {realtimeLogPageRows.length === 0 ? (
                  <tr>
                    <td colSpan={realtimeLogVisibleColumnCount}>
                      <div className={styles.emptyTable}>
                        {monitoringLoading ? t('common.loading') : deferredSearch.trim() ? t('monitoring.no_filtered_data') : t('monitoring.no_data')}
                      </div>
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </div>
        {realtimeLogPagination.totalPages > 1 ? (
          <div className={quotaStyles.pagination}>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => void showPreviousRealtimeLogPage()}
              disabled={realtimeLogLoading || !realtimeLogPagination.hasPrevious}
              aria-label={t('monitoring.previous_page')}
            >
              {t('monitoring.previous_page')}
            </Button>
            <div className={quotaStyles.pageInfo}>
              {t('monitoring.pagination_info', {
                from: realtimeLogPagination.from,
                to: realtimeLogPagination.to,
                total: realtimeLogPagination.total,
                page: realtimeLogPagination.page,
                totalPages: realtimeLogPagination.totalPages,
                defaultValue: `${realtimeLogPagination.from}-${realtimeLogPagination.to} / ${realtimeLogPagination.total}`,
              })}
            </div>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => void showNextRealtimeLogPage()}
              disabled={realtimeLogLoading || !realtimeLogNextCursor || !realtimeLogPagination.hasNext}
              aria-label={t('monitoring.next_page')}
            >
              {t('monitoring.next_page')}
            </Button>
          </div>
        ) : null}
        </Card>
      </section>

      <Modal
        open={Boolean(selectedRealtimeErrorRow)}
        onClose={() => setSelectedRealtimeErrorRow(null)}
        title={translateRealtimeErrorText('error_details', t, i18n.language)}
        width={720}
        className={styles.monitorModal}
        footer={selectedRealtimeErrorRow ? (
          <div className={styles.monitorModalActions}>
            <Button variant="secondary" size="sm" onClick={() => handleCopyRealtimeDiagnostic(selectedRealtimeErrorRow)}>
              {translateRealtimeErrorText('copy_diagnostic', t, i18n.language)}
            </Button>
            <Button variant="primary" size="sm" onClick={() => setSelectedRealtimeErrorRow(null)}>
              {t('common.close')}
            </Button>
          </div>
        ) : null}
      >
        {selectedRealtimeErrorRow ? (
          <RealtimeErrorDetailsPanel row={selectedRealtimeErrorRow} t={t} language={i18n.language} />
        ) : null}
      </Modal>

      <Modal
        open={isMonitoringSettingsOpen}
        onClose={() => {
          if (!isMonitoringStatisticsResetting) setIsMonitoringSettingsOpen(false);
        }}
        title={t('usage_stats.monitoring_settings')}
        width={760}
        className={styles.monitorModal}
      >
        <div className={styles.monitoringSettingsEditor}>
          <div className={styles.settingsSectionCard}>
            <div className={styles.settingsSectionHeader}>
              <strong>{t('usage_stats.monitoring_settings_retention_title')}</strong>
              <span>{t('usage_stats.monitoring_settings_retention_desc')}</span>
            </div>
            <label className={styles.settingsField}>
              <span>{t('usage_stats.monitoring_settings_retention_days')}</span>
              <Input
                type="number"
                min="0"
                step="1"
                value={monitoringSettingsDraft.retentionDays}
                onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, retentionDays: event.target.value }))}
                placeholder="0"
              />
              <small>{t('usage_stats.monitoring_settings_retention_hint')}</small>
              <div className={styles.settingsScheduleNote}>{t('usage_stats.monitoring_settings_retention_schedule')}</div>
            </label>
          </div>

          <div className={styles.settingsSectionCard}>
            <div className={styles.settingsSectionHeader}>
              <strong>{t('usage_stats.monitoring_settings_webdav_title')}</strong>
              <span>{t('usage_stats.monitoring_settings_webdav_desc')}</span>
            </div>
            <label className={styles.settingsCheckboxField}>
              <input
                type="checkbox"
                checked={monitoringSettingsDraft.webdavEnabled}
                onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavEnabled: event.target.checked }))}
              />
              <span>{t('usage_stats.monitoring_settings_webdav_enabled')}</span>
            </label>
            <div className={styles.settingsGrid}>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_interval')}</span>
                <Input
                  type="number"
                  min="1"
                  step="1"
                  value={monitoringSettingsDraft.webdavIntervalMinutes}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavIntervalMinutes: event.target.value }))}
                  placeholder="1440"
                />
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_retention_days')}</span>
                <Input
                  type="number"
                  min="0"
                  step="1"
                  value={monitoringSettingsDraft.webdavRetentionDays}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavRetentionDays: event.target.value }))}
                  placeholder="0"
                />
                <small>{t('usage_stats.monitoring_settings_webdav_retention_hint')}</small>
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_url')}</span>
                <Input
                  value={monitoringSettingsDraft.webdavUrl}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavUrl: event.target.value }))}
                  placeholder="https://example.com/dav/path"
                />
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_username')}</span>
                <Input
                  value={monitoringSettingsDraft.webdavUsername}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavUsername: event.target.value }))}
                  autoComplete="username"
                />
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_password')}</span>
                <Input
                  type="password"
                  value={monitoringSettingsDraft.webdavPassword}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavPassword: event.target.value }))}
                  autoComplete="current-password"
                />
              </label>
            </div>
            <small className={styles.settingsHint}>{t('usage_stats.monitoring_settings_webdav_hint')}</small>
          </div>

          <div className={`${styles.settingsSectionCard} ${styles.settingsDangerSection}`}>
            <div className={styles.settingsSectionHeader}>
              <strong>{t('usage_stats.monitoring_settings_data_title')}</strong>
              <span>{t('usage_stats.monitoring_settings_data_desc')}</span>
            </div>
            <div className={styles.settingsDangerAction}>
              <div>
                <span>{t('usage_stats.monitoring_settings_data_count')}</span>
                <strong>{formatCompactNumber(Number(usage?.total_requests) || 0)}</strong>
              </div>
              <Button
                variant="danger"
                size="sm"
                className={styles.resetStatisticsButton}
                onClick={handleMonitoringStatisticsReset}
                disabled={isMonitoringStatisticsResetting || isMonitoringSettingsSaving}
              >
                <IconTrash2 size={15} />
                {isMonitoringStatisticsResetting
                  ? t('usage_stats.monitoring_settings_resetting')
                  : t('usage_stats.monitoring_settings_reset_button')}
              </Button>
            </div>
          </div>

          <div className={styles.priceActionsBar}>
            <Button variant="secondary" size="sm" onClick={() => setIsMonitoringSettingsOpen(false)} disabled={isMonitoringStatisticsResetting}>
              {t('common.cancel')}
            </Button>
            <Button variant="primary" size="sm" onClick={() => void handleSaveMonitoringSettings()} disabled={isMonitoringSettingsSaving || isMonitoringStatisticsResetting}>
              {isMonitoringSettingsSaving ? t('common.loading') : t('common.save')}
            </Button>
          </div>
        </div>
      </Modal>

      <Modal
        open={isPriceModalOpen}
        onClose={() => setIsPriceModalOpen(false)}
        title={t('usage_stats.model_price_settings')}
        width={960}
        className={`${styles.monitorModal} ${styles.priceManagerModal}`}
      >
        <div className={styles.priceManager}>
          <div className={styles.priceManagerTabs} role="tablist" aria-label={t('usage_stats.model_price_settings')}>
            <button
              type="button"
              role="tab"
              aria-selected={priceManagementView === 'rules'}
              className={`${styles.priceManagerTab} ${priceManagementView === 'rules' ? styles.priceManagerTabActive : ''}`}
              onClick={() => setPriceManagementView('rules')}
            >
              {t('usage_stats.model_price_tab_rules')}
              <span>{configuredPriceRuleCount}/{priceRuleTargets.length}</span>
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={priceManagementView === 'sync'}
              className={`${styles.priceManagerTab} ${priceManagementView === 'sync' ? styles.priceManagerTabActive : ''}`}
              onClick={() => setPriceManagementView('sync')}
            >
              {t('usage_stats.model_price_tab_sync')}
              {unconfiguredPriceRuleCount > 0 ? <span>{unconfiguredPriceRuleCount}</span> : null}
            </button>
          </div>

          {priceManagementView === 'rules' ? (
            <div className={styles.priceRuleWorkspace}>
              <aside className={styles.priceRuleSidebar}>
                <div className={styles.priceRuleSearch}>
                  <IconSearch size={15} />
                  <Input
                    value={priceRuleSearch}
                    onChange={(event) => setPriceRuleSearch(event.target.value)}
                    placeholder={t('usage_stats.model_price_search_placeholder')}
                  />
                </div>
                <div className={styles.priceRuleList}>
                  {isPriceLoading ? <div className={styles.priceRuleListEmpty}>{t('common.loading')}</div> : null}
                  {!isPriceLoading && filteredPriceRuleTargets.map((item) => {
                    const active = item.model === priceModel;
                    return (
                      <button
                        key={item.key}
                        type="button"
                        className={`${styles.priceRuleListItem} ${active ? styles.priceRuleListItemActive : ''}`}
                        onClick={() => selectPriceTarget(item.model)}
                      >
                        <span className={styles.priceRuleListIdentity}>
                          <strong title={item.model}>{item.model}</strong>
                        </span>
                        <span className={styles.priceRuleListMeta}>
                          <span className={item.rule ? styles.priceRuleConfigured : styles.priceRuleUnconfigured}>
                            {t(item.rule ? 'usage_stats.model_price_configured' : 'usage_stats.model_price_unconfigured')}
                          </span>
                          <small>{t('usage_stats.model_price_requests', { count: item.requests })}</small>
                        </span>
                      </button>
                    );
                  })}
                  {!isPriceLoading && filteredPriceRuleTargets.length === 0 ? (
                    <div className={styles.priceRuleListEmpty}>{t('usage_stats.model_price_search_empty')}</div>
                  ) : null}
                </div>
              </aside>

              <section className={styles.priceRuleEditorPane}>
                {selectedPriceTarget ? (
                  <>
                    <header className={styles.priceRuleEditorHeader}>
                      <div>
                        <h3 title={selectedPriceTarget.model}>{selectedPriceTarget.model}</h3>
                        <span>{t('usage_stats.model_price_model_scope')}</span>
                      </div>
                      <div className={styles.priceRuleEditorBadges}>
                        <span className={selectedPriceTarget.rule ? styles.priceRuleConfigured : styles.priceRuleUnconfigured}>
                          {t(selectedPriceTarget.rule ? 'usage_stats.model_price_configured' : 'usage_stats.model_price_unconfigured')}
                        </span>
                        {selectedPriceTarget.rule?.source ? <span>{selectedPriceTarget.rule.source}</span> : null}
                      </div>
                    </header>

                    <div className={styles.priceRuleEditorScroll}>
                      <section className={styles.priceRuleSection}>
                        <div className={styles.priceRuleSectionHeader}>
                          <h4>{t('usage_stats.model_price_base_rates')}</h4>
                          <span>USD / 1M</span>
                        </div>
                        <div className={styles.priceBaseGrid}>
                          {([
                            ['input', 'usage_stats.model_price_input'],
                            ['output', 'usage_stats.model_price_output'],
                            ['cacheRead', 'usage_stats.model_price_cache_read'],
                            ['cacheWrite', 'usage_stats.model_price_cache_write'],
                          ] as const).map(([field, label]) => (
                            <label className={styles.priceField} key={field}>
                              <span>{t(label)}</span>
                              <Input
                                type="number"
                                min="0"
                                step="0.0001"
                                value={priceDraft[field]}
                                onChange={(event) => handlePriceDraftChange(field, event.target.value)}
                                placeholder="0.0000"
                              />
                            </label>
                          ))}
                        </div>
                      </section>

                      <section className={styles.priceRuleSection}>
                        <div className={styles.priceRuleSectionHeader}>
                          <div>
                            <h4>{t('usage_stats.model_price_context_tier')}</h4>
                            <span>{t('usage_stats.model_price_tier_count', { count: priceDraft.tiers.length })}</span>
                          </div>
                          <Button variant="secondary" size="sm" onClick={addPriceTier}>
                            {t('usage_stats.model_price_tier_add')}
                          </Button>
                        </div>
                        <div className={styles.priceTierList}>
                          {priceDraft.tiers.map((tier, index) => (
                            <div className={styles.priceTierCompactRow} key={index}>
                              <span className={styles.priceTierIndex}>{index + 1}</span>
                              <label>
                                <span>{t('usage_stats.model_price_context_threshold')}</span>
                                <Input type="number" min="1" step="1" value={tier.contextSize} onChange={(event) => handlePriceTierChange(index, 'contextSize', event.target.value)} placeholder="272000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_input')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.input} onChange={(event) => handlePriceTierChange(index, 'input', event.target.value)} placeholder="0.0000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_output')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.output} onChange={(event) => handlePriceTierChange(index, 'output', event.target.value)} placeholder="0.0000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_cache_read')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.cacheRead} onChange={(event) => handlePriceTierChange(index, 'cacheRead', event.target.value)} placeholder="0.0000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_cache_write')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.cacheWrite} onChange={(event) => handlePriceTierChange(index, 'cacheWrite', event.target.value)} placeholder="0.0000" />
                              </label>
                              <button
                                type="button"
                                className={styles.priceTierRemoveButton}
                                onClick={() => removePriceTier(index)}
                                aria-label={t('usage_stats.model_price_tier_remove')}
                                title={t('usage_stats.model_price_tier_remove')}
                              >
                                <IconTrash2 size={15} />
                              </button>
                            </div>
                          ))}
                          {priceDraft.tiers.length === 0 ? (
                            <div className={styles.priceTierEmpty}>{t('usage_stats.model_price_tier_empty')}</div>
                          ) : null}
                        </div>
                      </section>
                    </div>

                    <footer className={styles.priceRuleEditorFooter}>
                      <div>
                        {selectedPriceTarget.rule ? (
                          <Button variant="secondary" size="sm" onClick={() => void handleDeletePrice(selectedPriceTarget.model)}>
                            {t('common.delete')}
                          </Button>
                        ) : null}
                      </div>
                      <div>
                        <Button variant="secondary" size="sm" onClick={() => setPriceDraft(createPriceDraft(selectedPriceTarget.rule))}>
                          {t('usage_stats.model_price_reset_changes')}
                        </Button>
                        <Button variant="primary" size="sm" onClick={() => void handleSavePrice()} disabled={isPriceSaving}>
                          {isPriceSaving ? t('common.loading') : t('common.save')}
                        </Button>
                      </div>
                    </footer>
                  </>
                ) : (
                  <div className={styles.priceRuleEditorEmpty}>{t('usage_stats.model_price_select_empty')}</div>
                )}
              </section>
            </div>
          ) : (
            <div className={styles.priceSyncView}>
              <header className={styles.priceSyncHeader}>
                <div>
                  <span className={`${styles.priceSyncStatusDot} ${styles[`priceSyncStatus${priceSyncStatus}`] ?? ''}`} />
                  <div>
                    <h3>{t(`usage_stats.model_price_sync_state_${priceSyncStatus}`, { defaultValue: priceSyncStatus })}</h3>
                    <span>
                      {priceSyncState.lastSuccessMs
                        ? t('usage_stats.model_price_last_sync', { value: formatShortDateTime(priceSyncState.lastSuccessMs) })
                        : t('usage_stats.model_price_sync_never')}
                    </span>
                  </div>
                </div>
                <div className={styles.priceSyncActions}>
                  <Button variant="secondary" size="sm" onClick={() => void handleSyncPrices(true)} disabled={isPriceSyncing || isPriceLoading}>
                    {t('usage_stats.model_price_sync_preview')}
                  </Button>
                  <Button
                    variant="primary"
                    size="sm"
                    className={styles.priceSyncApplyButton}
                    onClick={() => void handleSyncPrices(false)}
                    disabled={isPriceSyncing || isPriceLoading}
                  >
                    <IconRefreshCw size={14} className={styles.priceSyncApplyIcon} />
                    {isPriceSyncing
                      ? t('common.loading')
                      : priceSyncLockedOverrides.length > 0
                        ? t('usage_stats.model_price_sync_with_overrides', { count: priceSyncLockedOverrides.length })
                        : t('usage_stats.model_price_sync')}
                  </Button>
                </div>
              </header>

              <div className={styles.priceSyncMetrics}>
                {([
                  ['matched', priceSyncResult?.matched ?? priceSyncState.matched ?? 0],
                  ['added', priceSyncResult?.added ?? priceSyncState.added ?? 0],
                  ['updated', priceSyncResult?.updated ?? priceSyncState.updated ?? 0],
                  ['unmatched', unmatchedPriceModelCount],
                ] as const).map(([key, value]) => (
                  <div key={key} className={`${styles.priceSyncMetric} ${styles[`priceSyncMetric${key}`] ?? ''}`}>
                    <span>{t(`usage_stats.model_price_sync_metric_${key}`)}</span>
                    <strong>{formatCompactNumber(value)}</strong>
                  </div>
                ))}
              </div>

              {priceSyncState.error ? <div className={styles.priceSyncError}>{priceSyncState.error}</div> : null}

              {priceSyncResult ? (
                <section className={styles.priceSyncChangesSection}>
                  <div className={styles.priceSyncChangesHeader}>
                    <div>
                      <h4>{t(priceSyncResult.dryRun ? 'usage_stats.model_price_sync_preview_details' : 'usage_stats.model_price_sync_applied_details')}</h4>
                      <span>{t('usage_stats.model_price_sync_change_summary', {
                        added: priceSyncChangeCounts.added,
                        updated: priceSyncChangeCounts.updated,
                        overridden: priceSyncChangeCounts.overridden,
                        locked: priceSyncChangeCounts.locked,
                        unmatched: unmatchedPriceModelCount,
                      })}</span>
                    </div>
                    <div className={styles.priceSyncChangesToolbar}>
                      {lockedPriceSyncChanges.length > 0 ? (
                        <label className={styles.priceSyncOverrideAll}>
                          <input
                            type="checkbox"
                            checked={allLockedPriceSyncChangesSelected}
                            onChange={(event) => setPriceSyncLockedOverrides(event.target.checked ? lockedPriceSyncChanges.map((change) => change.model) : [])}
                          />
                          <span>{t('usage_stats.model_price_sync_override_all', { count: lockedPriceSyncChanges.length })}</span>
                        </label>
                      ) : null}
                      <div className={styles.priceSyncChangeFilters}>
                        {(['all', 'added', 'updated', 'overridden', 'locked', 'unmatched'] as const).map((filter) => {
                          const count = filter === 'all' ? priceSyncChanges.length : priceSyncChangeCounts[filter];
                          if (filter !== 'all' && count === 0) return null;
                          const filterLabel = filter === 'overridden' && priceSyncResult.dryRun
                            ? t('usage_stats.model_price_sync_override_selected')
                            : t(`usage_stats.model_price_sync_change_${filter}`);
                          return (
                            <button
                              type="button"
                              key={filter}
                              className={priceSyncChangeFilter === filter ? styles.priceSyncChangeFilterActive : ''}
                              onClick={() => setPriceSyncChangeFilter(filter)}
                            >
                              <span className={styles.priceSyncChangeFilterLabel}>{filterLabel}</span>
                              <span className={styles.priceSyncChangeFilterCount}>{count}</span>
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  </div>

                  <div className={styles.priceSyncChangeList}>
                    {filteredPriceSyncChanges.map((change) => {
                      const rateChanges = MODEL_PRICE_SYNC_RATE_FIELDS.filter(([field]) => (
                        change.after && (!change.before || change.before.base[field] !== change.after.base[field])
                      ));
                      const beforeTierCount = change.before?.tiers?.length ?? 0;
                      const afterTierCount = change.after?.tiers?.length ?? 0;
                      const overrideSelected = change.action === 'locked' && priceSyncLockedOverrideSet.has(change.model);
                      const displayedAction = overrideSelected ? 'overridden' : change.action;
                      return (
                        <article className={styles.priceSyncChangeRow} key={`${change.action}/${change.model}`}>
                          <div className={styles.priceSyncChangeIdentity}>
                            <span className={`${styles.priceSyncChangeBadge} ${styles[`priceSyncChange${displayedAction}`] ?? ''}`}>
                              {overrideSelected
                                ? t('usage_stats.model_price_sync_override_selected')
                                : t(`usage_stats.model_price_sync_change_${displayedAction}`)}
                            </span>
                            <div>
                              <strong title={change.model}>{change.model}</strong>
                              <small>
                                {change.sourceProvider
                                  ? `${change.sourceProvider}/${change.sourceModel || change.model}`
                                  : t('usage_stats.model_price_sync_change_no_source')}
                                {' · '}
                                {t('usage_stats.model_price_requests', { count: change.requests })}
                              </small>
                            </div>
                          </div>

                          {change.action !== 'unmatched' ? (
                            <div className={styles.priceSyncRateChanges}>
                              {rateChanges.map(([field, label]) => (
                                <div key={field}>
                                  <span>{t(label)}</span>
                                  <div>
                                    {change.before ? <del>{formatModelPriceRate(change.before.base[field])}</del> : null}
                                    {change.before ? <span aria-hidden="true">-&gt;</span> : null}
                                    <strong>{formatModelPriceRate(change.after?.base[field])}</strong>
                                  </div>
                                </div>
                              ))}
                              {beforeTierCount !== afterTierCount ? (
                                <div>
                                  <span>{t('usage_stats.model_price_context_tier')}</span>
                                  <div>
                                    <del>{beforeTierCount}</del>
                                    <span aria-hidden="true">-&gt;</span>
                                    <strong>{afterTierCount}</strong>
                                  </div>
                                </div>
                              ) : null}
                              {rateChanges.length === 0 && beforeTierCount === afterTierCount ? (
                                <small>{t(change.action === 'locked'
                                  ? overrideSelected
                                    ? 'usage_stats.model_price_sync_override_selected_hint'
                                    : 'usage_stats.model_price_sync_change_locked_hint'
                                  : 'usage_stats.model_price_sync_change_metadata_hint')}</small>
                              ) : null}
                              {change.action === 'locked' ? (
                                <label className={styles.priceSyncOverrideOption}>
                                  <input
                                    type="checkbox"
                                    checked={overrideSelected}
                                    onChange={(event) => setPriceSyncLockedOverrides((previous) => (
                                      event.target.checked
                                        ? Array.from(new Set([...previous, change.model]))
                                        : previous.filter((model) => model !== change.model)
                                    ))}
                                  />
                                  <span>{t(overrideSelected
                                    ? 'usage_stats.model_price_sync_override_selected'
                                    : 'usage_stats.model_price_sync_override_option')}</span>
                                </label>
                              ) : null}
                            </div>
                          ) : (
                            <span className={styles.priceSyncChangeHint}>{t('usage_stats.model_price_sync_change_unmatched_hint')}</span>
                          )}
                        </article>
                      );
                    })}
                    {filteredPriceSyncChanges.length === 0 ? (
                      <div className={styles.priceSyncChangesEmpty}>{t('usage_stats.model_price_sync_no_changes')}</div>
                    ) : null}
                  </div>
                </section>
              ) : unmatchedPriceModelCount > 0 ? (
                <section className={styles.priceSyncResultSection}>
                  <div className={styles.priceRuleSectionHeader}>
                    <div>
                      <h4>{t('usage_stats.model_price_sync_unmatched')}</h4>
                      <span>{unmatchedPriceModelCount}</span>
                    </div>
                  </div>
                  <div className={styles.priceUnmatchedList}>
                  {unmatchedPriceModels.map((item) => (
                    <div key={item.model}>
                      <span>
                        <strong title={item.model}>{item.model}</strong>
                        {item.alias ? <small title={item.alias}>{item.alias}</small> : null}
                      </span>
                      <small>{t('usage_stats.model_price_requests', { count: item.requests })}</small>
                    </div>
                  ))}
                  </div>
                </section>
              ) : null}

              <section className={styles.priceSyncSchedule}>
                <div>
                  <strong>{t('usage_stats.model_price_sync_schedule_title')}</strong>
                  <span>{t('usage_stats.model_price_sync_schedule_desc')}</span>
                </div>
                <div className={styles.priceSyncScheduleControls}>
                  <label className={styles.priceSyncScheduleToggle}>
                    <input
                      type="checkbox"
                      checked={monitoringSettingsDraft.modelPriceSyncEnabled}
                      onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, modelPriceSyncEnabled: event.target.checked }))}
                    />
                    <span>{t('usage_stats.model_price_sync_schedule_enabled')}</span>
                  </label>
                  <label className={styles.priceSyncScheduleInterval}>
                    <span>{t('usage_stats.model_price_sync_schedule_interval')}</span>
                    <Input
                      type="number"
                      min="60"
                      step="60"
                      value={monitoringSettingsDraft.modelPriceSyncIntervalMinutes}
                      onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, modelPriceSyncIntervalMinutes: event.target.value }))}
                      placeholder="1440"
                      disabled={!monitoringSettingsDraft.modelPriceSyncEnabled}
                    />
                  </label>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => void handleSaveMonitoringSettings(false)}
                    disabled={isMonitoringSettingsLoading || isMonitoringSettingsSaving}
                  >
                    {isMonitoringSettingsSaving ? t('common.loading') : t('common.save')}
                  </Button>
                </div>
              </section>
            </div>
          )}
        </div>
      </Modal>
    </div>
  );
}
