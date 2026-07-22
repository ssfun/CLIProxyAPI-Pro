/* eslint-disable react-refresh/only-export-components -- This module owns shared page models and JSX confirmation builders. */
import { startTransition, type CSSProperties } from 'react';
import type { TFunction } from 'i18next';
import {
  ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE,
  ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS,
  ACCOUNT_INSPECTION_SETTING_LIMITS,
  buildAccountInspectionBackendViewState,
  createIdleAccountInspectionProgressSnapshot,
  isSuggestedAction,
  type AccountInspectionAction,
  type AccountInspectionAntigravityQuotaMode,
  type AccountInspectionAutoErrorAction,
  type AccountInspectionConfigurableSettings,
  type AccountInspectionLogLevel,
  type AccountInspectionPageInfo,
  type AccountInspectionProgressSnapshot,
  type AccountInspectionResultItem,
  type AccountInspectionRunResult,
} from './accountInspection';
import type {
  AccountInspectionInspectOneItem,
  AccountInspectionScheduleResponse,
} from '@/services/api';
import { useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import {
  isDisabledAuthFile,
  isQuotaLowState,
  isRecordValue,
  normalizeNumberValue,
  readBooleanValue,
  readStringValue,
  resolveAuthProvider,
} from '@/utils/quota';
import { resolveProviderDisplayLabel } from '@/utils/sourceResolver';
import styles from './accountInspection.module.scss';

export type RunStatus = 'idle' | 'running' | 'paused' | 'success' | 'error';

export type ResultHealthStatus = 'healthy' | 'disabled' | 'authInvalid' | 'quotaExhausted' | 'inspectionError' | 'recoverable';

export type ResultFilter = 'pending' | 'accountInvalid' | 'requestError' | 'quotaExhausted' | 'recoverable' | 'highAvailable';

export type SettingsSectionKey = 'plan' | 'scope' | 'runtime' | 'antigravity' | 'auto';

export type ManualAccountInspectionAction = Exclude<AccountInspectionAction, 'keep'>;

export type QuotaAccountStatsState = Pick<
  ReturnType<typeof useQuotaStore.getState>,
  'antigravityQuota' | 'claudeQuota' | 'codexQuota' | 'kimiQuota' | 'xaiQuota'
>;

export type HealthCounts = {
  total: number;
  healthy: number;
  disabled: number;
  authInvalid: number;
  quotaExhausted: number;
  inspectionError: number;
  recoverable: number;
};

export type InspectionLogEntry = {
  id: string;
  level: AccountInspectionLogLevel;
  message: string;
  timestamp: number;
};

export type SummaryCard = {
  key: string;
  label: string;
  value: string;
  description?: string;
  tone?: 'neutral' | 'good' | 'warn' | 'bad';
};

export type InspectionSettingsDraft = {
  targetType: string;
  workers: string;
  deleteWorkers: string;
  timeout: string;
  retries: string;
  usedPercentThreshold: string;
  sampleSize: string;
  antigravityDeepProbeEnabled: boolean;
  antigravityDeepProbeModel: string;
  antigravityQuotaMode: AccountInspectionAntigravityQuotaMode;
  xaiDeepProbeEnabled: boolean;
  xaiDeepProbeModel: string;
  autoExecuteQuotaLimitDisable: boolean;
  autoExecuteQuotaRecoveryEnable: boolean;
  autoExecuteAccountInvalidAction: AccountInspectionAutoErrorAction;
  autoExecuteRequestErrorAction: AccountInspectionAutoErrorAction;
};

export type InspectionSettingsDraftField = Exclude<
  keyof InspectionSettingsDraft,
  'antigravityDeepProbeEnabled' | 'antigravityQuotaMode' | 'xaiDeepProbeEnabled' | 'autoExecuteQuotaLimitDisable' | 'autoExecuteQuotaRecoveryEnable' | 'autoExecuteAccountInvalidAction' | 'autoExecuteRequestErrorAction'
>;

export type ScheduleDraft = {
  enabled: boolean;
  intervalMinutes: string;
};

export type ProviderAccountStats = {
  provider: string;
  total: number;
  enabled: number;
  highAvailable: number;
  disabled: number;
  quotaLow: number;
  accountInvalid: number;
  requestError: number;
};

export type ResolvedTheme = 'light' | 'dark';

export type AuthFileAccountStats = {
  total: number;
  providerCount: number;
  enabled: number;
  highAvailable: number;
  disabled: number;
  quotaLow: number;
  accountInvalid: number;
  requestError: number;
  providers: ProviderAccountStats[];
};

export type AutoExecutionCounts = {
  delete: number;
  disable: number;
  enable: number;
};

export type InspectionResultViewRow = {
  item: AccountInspectionResultItem;
  healthStatus: ResultHealthStatus;
  manualActions: ManualAccountInspectionAction[];
};

export type InspectionResultsViewState = {
  rows: InspectionResultViewRow[];
  healthCounts: HealthCounts;
  actionableActionCounts: AutoExecutionCounts;
  filterRows: Record<ResultFilter, InspectionResultViewRow[]>;
  filterRowCounts: Record<ResultFilter, number>;
};

export const ACCOUNT_INSPECTION_LOG_LIMIT = 200;
export const ACCOUNT_INSPECTION_RESULT_PAGE_SIZE = 100;
export const ACCOUNT_INSPECTION_LOG_PAGE_SIZE = 100;
export const ACCOUNT_INSPECTION_ACTION_PAGE_SIZE = 500;
export const ACCOUNT_INSPECTION_DETAILS_IDLE_DELAY_MS = 250;
export const ACCOUNT_INSPECTION_AUTH_FILES_IDLE_DELAY_MS = 300;
export const ACCOUNT_INSPECTION_ASSET_STATS_CHUNK_SIZE = 500;
export const ACCOUNT_INSPECTION_EXPORT_DOWNLOAD_CONCURRENCY = 4;

export type AccountInspectionIdleCallback = (deadline: { didTimeout: boolean; timeRemaining: () => number }) => void;

export const appendInspectionLogEntry = (entries: InspectionLogEntry[], entry: InspectionLogEntry) =>
  [...entries, entry].slice(-ACCOUNT_INSPECTION_LOG_LIMIT);

export const getPaginationRange = (pageInfo: AccountInspectionPageInfo | null, visibleCount: number) => {
  if (!pageInfo || pageInfo.total <= 0 || visibleCount <= 0) {
    return {
      page: pageInfo?.page ?? 1,
      pageSize: pageInfo?.pageSize ?? visibleCount,
      total: pageInfo?.total ?? visibleCount,
      totalPages: pageInfo?.totalPages ?? (visibleCount > 0 ? 1 : 0),
      from: visibleCount > 0 ? 1 : 0,
      to: visibleCount,
      hasPrevious: (pageInfo?.page ?? 1) > 1,
      hasNext: Boolean(pageInfo?.hasMore),
    };
  }

  const from = (pageInfo.page - 1) * pageInfo.pageSize + 1;
  return {
    page: pageInfo.page,
    pageSize: pageInfo.pageSize,
    total: pageInfo.total,
    totalPages: pageInfo.totalPages,
    from,
    to: Math.min(pageInfo.total, from + visibleCount - 1),
    hasPrevious: pageInfo.page > 1,
    hasNext: pageInfo.hasMore,
  };
};

export const getProviderInitial = (label: string) => label.trim().charAt(0).toUpperCase() || '?';

export const getDocumentTheme = (): ResolvedTheme => {
  if (typeof document === 'undefined') return 'light';
  const root = document.documentElement;
  const theme = root.dataset.theme || root.getAttribute('data-theme') || root.className;
  return String(theme).toLowerCase().includes('dark') ? 'dark' : 'light';
};

export const emptyAutoExecutionCounts = (): AutoExecutionCounts => ({
  delete: 0,
  disable: 0,
  enable: 0,
});

export const createEmptyFilterRows = (): Record<ResultFilter, InspectionResultViewRow[]> => ({
  pending: [],
  accountInvalid: [],
  requestError: [],
  quotaExhausted: [],
  recoverable: [],
  highAvailable: [],
});

export const levelClassMap: Record<AccountInspectionLogLevel, string> = {
  info: styles.logInfo,
  success: styles.logSuccess,
  warning: styles.logWarning,
  error: styles.logError,
};

export const healthToneClass: Record<ResultHealthStatus, string> = {
  healthy: styles.healthHealthy,
  disabled: styles.healthDisabled,
  authInvalid: styles.healthAuthInvalid,
  quotaExhausted: styles.healthQuota,
  inspectionError: styles.healthError,
  recoverable: styles.healthRecoverable,
};

export const healthLabelKey: Record<ResultHealthStatus, string> = {
  healthy: 'monitoring.account_inspection_health_healthy',
  disabled: 'monitoring.account_inspection_health_disabled',
  authInvalid: 'monitoring.account_inspection_account_invalid',
  quotaExhausted: 'monitoring.account_inspection_health_quota_exhausted',
  inspectionError: 'monitoring.account_inspection_account_request_error',
  recoverable: 'monitoring.account_inspection_health_recoverable',
};

export const extractHealthHttpStatusCode = (item: AccountInspectionResultItem) => {
  if (item.statusCode !== null && item.statusCode >= 400) return item.statusCode;
  const errorText = [item.error, item.deepProbeError, item.tokenRefreshError].filter(Boolean).join(' ');
  const match = errorText.match(/\bHTTP\s+(\d{3})\b/i) ?? errorText.match(/\bstatus(?:\s+code)?\s*[:=]?\s*(\d{3})\b/i);
  return match ? Number(match[1]) : null;
};

export const buildHealthStatusCodeText = (item: AccountInspectionResultItem) => {
  const httpStatusCode = extractHealthHttpStatusCode(item);
  return httpStatusCode !== null ? String(httpStatusCode) : '';
};

export const buildHealthStatusLabel = (
  item: AccountInspectionResultItem,
  healthStatus: ResultHealthStatus,
  t: TFunction
) => {
  const label = t(healthLabelKey[healthStatus]);
  const code = buildHealthStatusCodeText(item);
  return code ? `${label} · ${code}` : label;
};

export const hasInspectionErrorDetails = (item: AccountInspectionResultItem) => Boolean(
  item.error
  || item.errorDetail?.trim()
  || item.errorCode?.trim()
  || item.deepProbeError
  || item.tokenRefreshError
  || extractHealthHttpStatusCode(item) !== null
);

export const parseInspectionErrorPayload = (value: string): unknown => {
  const text = value.trim();
  if (!text || (!text.startsWith('{') && !text.startsWith('['))) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
};

export const readInspectionErrorMessage = (value: unknown): string => {
  if (typeof value === 'string') return value.trim();
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  const record = value as Record<string, unknown>;
  const directMessage = readInspectionErrorMessage(record.message);
  if (directMessage) return directMessage;
  const nestedError = readInspectionErrorMessage(record.error);
  if (nestedError) return nestedError;
  return '';
};

export const buildInspectionErrorPresentation = (item: AccountInspectionResultItem) => {
  const candidates = [item.error, item.deepProbeError || '', item.tokenRefreshError || '']
    .map((value) => value.trim())
    .filter((value, index, values) => Boolean(value) && values.indexOf(value) === index)
    .sort((left, right) => right.length - left.length);
  const detailText = item.errorDetail?.trim() || candidates[0] || '';
  const detailPayload = parseInspectionErrorPayload(detailText);
  const parsedMessage = readInspectionErrorMessage(detailPayload);
  const httpStatusCode = extractHealthHttpStatusCode(item);
  const fallbackSummary = candidates.find((value) => parseInspectionErrorPayload(value) === null) || '';
  const summary = parsedMessage || fallbackSummary;
  const normalizedSummary = summary.replace(/\s+/g, ' ').trim();
  const statusOnlySummary = httpStatusCode !== null && normalizedSummary.toLowerCase() === `http ${httpStatusCode}`.toLowerCase();
  return {
    summary: statusOnlySummary ? '' : normalizedSummary,
    detail: detailPayload === null
      ? (item.errorDetail?.trim() ? detailText : '')
      : JSON.stringify(detailPayload, null, 2),
  };
};

export const resolveResultHealthStatus = (item: AccountInspectionResultItem): ResultHealthStatus => {
  if (item.action === 'delete' || (item.statusCode !== null && ACCOUNT_INVALID_ERROR_STATUSES.has(item.statusCode))) {
    return 'authInvalid';
  }
  if (item.error) return 'inspectionError';
  if (item.isQuota || item.action === 'disable') return 'quotaExhausted';
  if (item.action === 'enable') return 'recoverable';
  if (item.disabled) return 'disabled';
  return 'healthy';
};

export function InspectionErrorDetailsPanel({
  item,
  t,
}: {
  item: AccountInspectionResultItem;
  t: TFunction;
}) {
  const healthStatus = resolveResultHealthStatus(item);
  const httpStatusCode = extractHealthHttpStatusCode(item);
  const errorPresentation = buildInspectionErrorPresentation(item);
  const detailItems = [
    { label: t('monitoring.account_label'), value: item.fileName },
    { label: t('monitoring.filter_provider'), value: item.provider },
    { label: t('monitoring.account_inspection_http_status'), value: httpStatusCode !== null ? String(httpStatusCode) : '' },
    { label: t('monitoring.account_inspection_error_code'), value: item.errorCode?.trim() || '' },
  ].filter((detail) => detail.value);

  return (
    <div className={styles.errorDetailsPanel}>
      <div className={styles.errorOverview}>
        <span className={`${styles.healthBadge} ${healthToneClass[healthStatus]}`}>
          {buildHealthStatusLabel(item, healthStatus, t)}
        </span>
        {errorPresentation.summary ? <strong>{errorPresentation.summary}</strong> : null}
      </div>
      <div className={styles.errorDetailsGrid}>
        {detailItems.map((detail) => (
          <div key={detail.label} className={styles.errorDetailItem}>
            <span>{detail.label}</span>
            <strong>{detail.value}</strong>
          </div>
        ))}
      </div>
      {errorPresentation.detail ? (
        <div className={styles.errorMessageBlock}>
          <span>{t('monitoring.account_inspection_raw_error_response')}</span>
          <pre className={styles.errorMessage}>{errorPresentation.detail}</pre>
        </div>
      ) : null}
    </div>
  );
}

export const ACCOUNT_INVALID_ERROR_STATUSES = new Set([400, 401, 403, 404]);
export const ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET = new Set<string>(ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS);

export const readAuthFileField = (file: AuthFileItem, key: string) =>
  readStringValue((file as unknown as Record<string, unknown>)[key]);

export const isAccountInspectionApiKeyAuthFile = (file: AuthFileItem) => {
  const label = readAuthFileField(file, 'label').toLowerCase();
  const source = readAuthFileField(file, 'source').toLowerCase();
  const apiKey = readAuthFileField(file, 'api_key') || readAuthFileField(file, 'apiKey');
  const path = readAuthFileField(file, 'path');

  return label.includes('apikey') ||
    label.includes('api-key') ||
    (source.startsWith('config:') && Boolean(apiKey)) ||
    (Boolean(apiKey) && !path);
};

export const isInspectableAccountInspectionAuthFile = (file: AuthFileItem) => {
  const provider = resolveAuthProvider(file);
  return ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET.has(provider) && !isAccountInspectionApiKeyAuthFile(file);
};

export const readAuthFileStatusMessage = (file: AuthFileItem) => {
  const raw = file['status_message'] ?? file.statusMessage;
  if (raw === undefined || raw === null) return '';
  return String(raw).trim();
};

export const readAuthFileLastError = (file: AuthFileItem) => {
  const raw = file['last_error'] ?? file.lastError;
  return isRecordValue(raw) ? raw : null;
};

export const readAuthFileLastErrorCode = (file: AuthFileItem) => readStringValue(readAuthFileLastError(file)?.code);

export const readAuthFileLastErrorStatus = (file: AuthFileItem) => {
  const error = readAuthFileLastError(file);
  return error ? normalizeNumberValue(error.http_status ?? error.httpStatus ?? error.status) : null;
};

export const isAuthFileAccountInvalid = (file: AuthFileItem) =>
  readAuthFileLastErrorCode(file) === 'inspection_http_error' &&
  ACCOUNT_INVALID_ERROR_STATUSES.has(readAuthFileLastErrorStatus(file) ?? 0);

export const isAuthFileRequestError = (file: AuthFileItem) => {
  const code = readAuthFileLastErrorCode(file);
  if (code === 'inspection_probe_error' || code === 'antigravity_deep_probe_error') return true;
  if (isAuthFileAccountInvalid(file)) return false;
  if (readAuthFileLastError(file)) return true;
  if (readBooleanValue(file.unavailable ?? file['unavailable'])) return true;
  const status = String(file.status ?? file.state ?? '').trim().toLowerCase();
  return status === 'error' || readAuthFileStatusMessage(file).length > 0;
};

export const incrementProviderStats = (stats: ProviderAccountStats, disabled: boolean, highAvailable: boolean, quotaLow: boolean, accountInvalid: boolean, requestError: boolean) => {
  stats.total += 1;
  if (disabled) {
    stats.disabled += 1;
  } else {
    stats.enabled += 1;
  }
  if (highAvailable) stats.highAvailable += 1;
  if (quotaLow) stats.quotaLow += 1;
  if (accountInvalid) stats.accountInvalid += 1;
  if (requestError) stats.requestError += 1;
};

export const emptyProviderAccountStats = (provider: string): ProviderAccountStats => ({
  provider,
  total: 0,
  enabled: 0,
  highAvailable: 0,
  disabled: 0,
  quotaLow: 0,
  accountInvalid: 0,
  requestError: 0,
});

export const createEmptyAuthFileAccountStats = (): AuthFileAccountStats => ({
  total: 0,
  providerCount: 0,
  enabled: 0,
  highAvailable: 0,
  disabled: 0,
  quotaLow: 0,
  accountInvalid: 0,
  requestError: 0,
  providers: [],
});

export const finalizeAuthFileAccountStats = (
  stats: AuthFileAccountStats,
  providerStats: Map<string, ProviderAccountStats>
) => ({
  ...stats,
  providerCount: providerStats.size,
  providers: [...providerStats.values()].sort((left, right) => right.total - left.total || left.provider.localeCompare(right.provider)),
});

export const quotaUsedPercentFromRemaining = (item: unknown): number | null => {
  if (!isRecordValue(item)) return null;
  const usedPercent = normalizeNumberValue(item.usedPercent ?? item.used_percent);
  if (usedPercent !== null) return Math.max(0, Math.min(100, usedPercent));
  const remainingFraction = normalizeNumberValue(item.remainingFraction ?? item.remaining_fraction);
  if (remainingFraction === null) return null;
  const normalized = remainingFraction > 1 && remainingFraction <= 100 ? remainingFraction / 100 : remainingFraction;
  return Math.max(0, Math.min(100, (1 - Math.max(0, Math.min(1, normalized))) * 100));
};

export const maxQuotaUsedPercent = (items: unknown): number | null => {
  if (!Array.isArray(items)) return null;
  const values = items
    .map(quotaUsedPercentFromRemaining)
    .filter((value): value is number => value !== null);
  if (values.length === 0) return null;
  return Math.max(...values);
};

export const antigravityGroupUsedPercent = (group: unknown): number | null => {
  if (!isRecordValue(group)) return null;
  return maxQuotaUsedPercent(group.buckets);
};

export const maxAntigravityGroupUsedPercent = (groups: unknown[]): number | null => {
  const values = groups
    .map(antigravityGroupUsedPercent)
    .filter((value): value is number => value !== null);
  if (values.length === 0) return null;
  return Math.max(...values);
};

export const isAntigravityClaudeGptGroup = (group: unknown): boolean => {
  if (!isRecordValue(group)) return false;
  const normalize = (value: unknown) =>
    typeof value === 'string'
      ? value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
      : '';
  const id = normalize(group.id);
  const label = normalize(group.label);
  if (id === 'claude-gpt' || label === 'claude-gpt') return true;
  const combined = `${id}-${label}`;
  return combined.includes('claude') && (combined.includes('gpt') || combined.includes('openai'));
};

export const isAntigravityQuotaLow = (
  quota: unknown,
  usedPercentThreshold: number,
  quotaMode: AccountInspectionAntigravityQuotaMode
) => {
  if (!isRecordValue(quota) || quota.status !== 'success') return false;
  const groups = Array.isArray(quota.groups) ? quota.groups : [];
  const used = quotaMode === 'max-used'
    ? maxAntigravityGroupUsedPercent(groups)
    : (maxAntigravityGroupUsedPercent(groups.filter(isAntigravityClaudeGptGroup)) ?? maxAntigravityGroupUsedPercent(groups));
  return used !== null && used >= usedPercentThreshold;
};

export const isXaiQuotaLow = (quota: unknown, usedPercentThreshold: number) => {
  if (!isRecordValue(quota) || quota.status !== 'success') return false;
  if (!isRecordValue(quota.billing)) return false;
  const used =
    normalizeNumberValue(quota.billing.usagePercent ?? quota.billing.usage_percent)
    ?? normalizeNumberValue(quota.billing.usedPercent ?? quota.billing.used_percent)
    ?? maxAntigravityGroupUsedPercent(Array.isArray(quota.billing.productUsage) ? quota.billing.productUsage : []);
  return used !== null && used >= usedPercentThreshold;
};

export const isProviderQuotaLow = (
  provider: string,
  quotaStore: QuotaAccountStatsState,
  fileName: string,
  usedPercentThreshold: number,
  antigravityQuotaMode: AccountInspectionAntigravityQuotaMode
) => {
  switch (provider) {
    case 'antigravity':
      return isAntigravityQuotaLow(quotaStore.antigravityQuota[fileName], usedPercentThreshold, antigravityQuotaMode);
    case 'claude':
      return isQuotaLowState(quotaStore.claudeQuota[fileName], usedPercentThreshold);
    case 'codex':
      return isQuotaLowState(quotaStore.codexQuota[fileName], usedPercentThreshold);
    case 'kimi':
      return isQuotaLowState(quotaStore.kimiQuota[fileName], usedPercentThreshold);
    case 'xai':
      return isXaiQuotaLow(quotaStore.xaiQuota[fileName], usedPercentThreshold);
    default:
      return false;
  }
};

export const accumulateAuthFileAccountStats = (
  stats: AuthFileAccountStats,
  providerStats: Map<string, ProviderAccountStats>,
  file: AuthFileItem,
  quotaStore: QuotaAccountStatsState,
  usedPercentThreshold: number,
  antigravityQuotaMode: AccountInspectionAntigravityQuotaMode
) => {
  if (!isInspectableAccountInspectionAuthFile(file)) return;

  const provider = resolveAuthProvider(file) || 'unknown';
  const disabled = isDisabledAuthFile(file);
  const quotaLow = isProviderQuotaLow(
    provider,
    quotaStore,
    file.name,
    usedPercentThreshold,
    antigravityQuotaMode
  );
  const accountInvalid = isAuthFileAccountInvalid(file);
  const requestError = isAuthFileRequestError(file);
  const highAvailable = !disabled && !quotaLow && !accountInvalid && !requestError;

  stats.total += 1;
  if (disabled) {
    stats.disabled += 1;
  } else {
    stats.enabled += 1;
  }
  if (highAvailable) stats.highAvailable += 1;
  if (accountInvalid) stats.accountInvalid += 1;
  if (requestError) stats.requestError += 1;
  if (quotaLow) stats.quotaLow += 1;

  const providerEntry = providerStats.get(provider) ?? emptyProviderAccountStats(provider);
  incrementProviderStats(providerEntry, disabled, highAvailable, quotaLow, accountInvalid, requestError);
  providerStats.set(provider, providerEntry);
};

export type AuthFileAccountStatsJob = {
  cancelled: boolean;
  handle: number | null;
  cancelHandle: ((handle: number) => void) | null;
};

export const scheduleAuthFileAccountStats = (
  job: AuthFileAccountStatsJob,
  files: AuthFileItem[],
  quotaStore: QuotaAccountStatsState,
  usedPercentThreshold: number,
  antigravityQuotaMode: AccountInspectionAntigravityQuotaMode,
  onComplete: (stats: AuthFileAccountStats) => void
) => {
  const providerStats = new Map<string, ProviderAccountStats>();
  const stats = createEmptyAuthFileAccountStats();
  let index = 0;
  const windowWithIdleCallback = window as Window & {
    requestIdleCallback?: (callback: AccountInspectionIdleCallback, options?: { timeout?: number }) => number;
    cancelIdleCallback?: (handle: number) => void;
  };

  const finish = () => {
    if (job.cancelled) return;
    const nextStats = finalizeAuthFileAccountStats(stats, providerStats);
    startTransition(() => {
      if (!job.cancelled) onComplete(nextStats);
    });
  };

  const scheduleNextChunk = () => {
    if (windowWithIdleCallback.requestIdleCallback && windowWithIdleCallback.cancelIdleCallback) {
      job.cancelHandle = windowWithIdleCallback.cancelIdleCallback.bind(windowWithIdleCallback);
      job.handle = windowWithIdleCallback.requestIdleCallback(runChunk, { timeout: 250 });
      return;
    }
    job.cancelHandle = window.clearTimeout.bind(window);
    job.handle = window.setTimeout(() => runChunk(), 0);
  };

  const runChunk = (deadline?: { timeRemaining: () => number }) => {
    if (job.cancelled) return;
    do {
      const chunkEnd = Math.min(index + ACCOUNT_INSPECTION_ASSET_STATS_CHUNK_SIZE, files.length);
      while (index < chunkEnd) {
        accumulateAuthFileAccountStats(
          stats,
          providerStats,
          files[index],
          quotaStore,
          usedPercentThreshold,
          antigravityQuotaMode
        );
        index += 1;
      }
    } while (index < files.length && deadline && deadline.timeRemaining() > 8);

    if (index >= files.length) {
      finish();
      return;
    }

    scheduleNextChunk();
  };

  runChunk();
};

export const emptyHealthCounts = (): HealthCounts => ({
  total: 0,
  healthy: 0,
  disabled: 0,
  authInvalid: 0,
  quotaExhausted: 0,
  inspectionError: 0,
  recoverable: 0,
});

export const getManualActionsByHealthStatus = (
  item: AccountInspectionResultItem,
  healthStatus: ResultHealthStatus
): ManualAccountInspectionAction[] => {
  if (healthStatus === 'healthy') return [];
  return [item.disabled ? 'enable' : 'disable', 'delete'];
};

export const buildInspectionResultsViewState = (items: AccountInspectionResultItem[]): InspectionResultsViewState => {
  const healthCounts = emptyHealthCounts();
  const actionableActionCounts = emptyAutoExecutionCounts();
  const filterRows = createEmptyFilterRows();
  const filterRowCounts: Record<ResultFilter, number> = {
    highAvailable: 0,
    accountInvalid: 0,
    quotaExhausted: 0,
    requestError: 0,
    recoverable: 0,
    pending: 0,
  };
  const rows: InspectionResultViewRow[] = [];

  const pushResultRow = (
    target: InspectionResultViewRow[],
    item: AccountInspectionResultItem,
    healthStatus: ResultHealthStatus,
    existingRow: InspectionResultViewRow | null
  ) => {
    if (existingRow) {
      target.push(existingRow);
      return existingRow;
    }
    const row = {
      item,
      healthStatus,
      manualActions: getManualActionsByHealthStatus(item, healthStatus),
    };
    target.push(row);
    return row;
  };

  healthCounts.total = items.length;
  items.forEach((item) => {
    const healthStatus = resolveResultHealthStatus(item);
    let row: InspectionResultViewRow | null = null;
    row = pushResultRow(rows, item, healthStatus, row);

    switch (healthStatus) {
      case 'healthy':
        healthCounts.healthy += 1;
        filterRowCounts.highAvailable += 1;
        row = pushResultRow(filterRows.highAvailable, item, healthStatus, row);
        break;
      case 'disabled':
        healthCounts.disabled += 1;
        break;
      case 'authInvalid':
        healthCounts.authInvalid += 1;
        filterRowCounts.accountInvalid += 1;
        row = pushResultRow(filterRows.accountInvalid, item, healthStatus, row);
        break;
      case 'quotaExhausted':
        healthCounts.quotaExhausted += 1;
        filterRowCounts.quotaExhausted += 1;
        row = pushResultRow(filterRows.quotaExhausted, item, healthStatus, row);
        break;
      case 'inspectionError':
        healthCounts.inspectionError += 1;
        filterRowCounts.requestError += 1;
        row = pushResultRow(filterRows.requestError, item, healthStatus, row);
        break;
      case 'recoverable':
        healthCounts.recoverable += 1;
        filterRowCounts.recoverable += 1;
        row = pushResultRow(filterRows.recoverable, item, healthStatus, row);
        break;
    }

    if (isSuggestedAction(item) && !item.executed) {
      filterRowCounts.pending += 1;
      pushResultRow(filterRows.pending, item, healthStatus, row);
      if (item.action === 'delete') actionableActionCounts.delete += 1;
      if (item.action === 'disable') actionableActionCounts.disable += 1;
      if (item.action === 'enable') actionableActionCounts.enable += 1;
    }
  });

  return {
    rows,
    healthCounts,
    actionableActionCounts,
    filterRows,
    filterRowCounts,
  };
};

export const collectActionableInspectionResults = (items: AccountInspectionResultItem[]) => {
  const targets: AccountInspectionResultItem[] = [];
  items.forEach((item) => {
    if (isSuggestedAction(item) && !item.executed) {
      targets.push(item);
    }
  });
  return targets;
};

export const buildManualActionItem = (
  item: AccountInspectionResultItem,
  action: ManualAccountInspectionAction
): AccountInspectionResultItem => ({
  ...item,
  action,
  actionReason: item.actionReason || action,
});

export const summaryToneClass: Record<NonNullable<SummaryCard['tone']>, string> = {
  neutral: '',
  good: styles.summaryGood,
  warn: styles.summaryWarn,
  bad: styles.summaryBad,
};

export const INSPECTION_TARGET_OPTIONS = [
  { value: ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE, label: 'All' },
  ...ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS.map((provider) => ({
    value: provider,
    label: resolveProviderDisplayLabel(provider),
  })),
] as const;

export const AUTO_ERROR_ACTION_OPTIONS: Array<{ value: AccountInspectionAutoErrorAction; labelKey: string }> = [
  { value: 'none', labelKey: 'monitoring.account_inspection_settings_account_error_action_none' },
  { value: 'disable', labelKey: 'monitoring.account_inspection_settings_account_error_action_disable' },
  { value: 'delete', labelKey: 'monitoring.account_inspection_settings_account_error_action_delete' },
];

export const ANTIGRAVITY_QUOTA_MODE_OPTIONS: Array<{ value: AccountInspectionAntigravityQuotaMode; labelKey: string }> = [
  { value: 'claude-gpt', labelKey: 'monitoring.account_inspection_settings_antigravity_quota_mode_claude_gpt' },
  { value: 'max-used', labelKey: 'monitoring.account_inspection_settings_antigravity_quota_mode_max_used' },
];

export const WORKER_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.workers;
export const DELETE_WORKER_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.deleteWorkers;
export const TIMEOUT_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.timeout;
export const RETRY_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.retries;
export const THRESHOLD_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.usedPercentThreshold;
export const SAMPLE_SIZE_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.sampleSize;
export const SCHEDULE_INTERVAL_LIMITS = ACCOUNT_INSPECTION_SETTING_LIMITS.scheduleIntervalMinutes;

export const formatTimestamp = (value: number, locale: string) => new Date(value).toLocaleString(locale);

export const formatInspectionInterval = (minutes: number, locale: string) =>
  new Intl.NumberFormat(locale, { style: 'unit', unit: 'minute', unitDisplay: 'short' }).format(minutes);

export const buildHighAvailabilityBarStyle = (highAvailable: number, total: number): CSSProperties => {
  const share = total > 0 ? Math.min(Math.max(highAvailable / total, 0), 1) : 0;
  return {
    '--bar-width': `${share * 100}%`,
    '--bar-color': `hsl(${Math.round(share * 120)}, 72%, 44%)`,
  } as CSSProperties;
};

export const toSettingsDraft = (settings: AccountInspectionConfigurableSettings): InspectionSettingsDraft => ({
  targetType: settings.targetType,
  workers: String(settings.workers),
  deleteWorkers: String(settings.deleteWorkers),
  timeout: String(settings.timeout),
  retries: String(settings.retries),
  usedPercentThreshold: String(settings.usedPercentThreshold),
  sampleSize: String(settings.sampleSize),
  antigravityDeepProbeEnabled: settings.antigravityDeepProbeEnabled,
  antigravityDeepProbeModel: settings.antigravityDeepProbeModel,
  antigravityQuotaMode: settings.antigravityQuotaMode,
  xaiDeepProbeEnabled: settings.xaiDeepProbeEnabled,
  xaiDeepProbeModel: settings.xaiDeepProbeModel,
  autoExecuteQuotaLimitDisable: settings.autoExecuteQuotaLimitDisable,
  autoExecuteQuotaRecoveryEnable: settings.autoExecuteQuotaRecoveryEnable,
  autoExecuteAccountInvalidAction: settings.autoExecuteAccountInvalidAction,
  autoExecuteRequestErrorAction: settings.autoExecuteRequestErrorAction,
});

export const formatActionLabel = (action: AccountInspectionAction, t: TFunction) => {
  switch (action) {
    case 'delete':
      return t('monitoring.account_inspection_action_delete');
    case 'disable':
      return t('monitoring.account_inspection_action_disable');
    case 'enable':
      return t('monitoring.account_inspection_action_enable');
    case 'keep':
    default:
      return t('monitoring.account_inspection_action_keep');
  }
};

export const formatQuotaRemainingLabel = (value: number | null) => {
  if (value === null) return '--';
  return `${Math.max(0, 100 - value).toFixed(1)}%`;
};

export const formatTokenRefreshLabel = (
  item: AccountInspectionResultItem,
  t: TFunction
) => {
  if (item.tokenRefreshStatus === 'success') return t('monitoring.account_inspection_token_refresh_success');
  if (item.tokenRefreshStatus === 'failed') return t('monitoring.account_inspection_token_refresh_failed');
  if (item.nextRefreshAt && item.nextRefreshAt > Date.now()) return t('monitoring.account_inspection_token_refresh_pending');
  return t('monitoring.account_inspection_token_refresh_not_triggered');
};

export const formatTokenRefreshDetail = (
  item: AccountInspectionResultItem,
  locale: string,
  t: TFunction
) => {
  if (item.tokenRefreshStatus === 'failed') return item.tokenRefreshError || '';
  if (item.nextRefreshAt && item.nextRefreshAt > 0) {
    return t('monitoring.account_inspection_token_next_refresh_at', {
      time: formatTimestamp(item.nextRefreshAt, locale),
    });
  }
  return '';
};

export const tokenRefreshToneClass = (item: AccountInspectionResultItem) => {
  if (item.tokenRefreshStatus === 'success') return styles.stateTextGood;
  if (item.tokenRefreshStatus === 'failed') return styles.stateTextBad;
  if (item.nextRefreshAt && item.nextRefreshAt > Date.now()) return styles.stateTextWarn;
  return styles.stateTextMuted;
};

export const formatInspectionVerdictPrimary = (
  item: AccountInspectionResultItem,
  healthStatus: ResultHealthStatus,
  t: TFunction
) => {
  if (item.tokenRefreshStatus === 'failed') return t('monitoring.account_inspection_verdict_token_refresh_failed');

  switch (healthStatus) {
    case 'inspectionError':
      return t('monitoring.account_inspection_verdict_probe_error');
    case 'authInvalid':
      return t('monitoring.account_inspection_verdict_auth_invalid');
    case 'quotaExhausted':
      return item.disabled
        ? t('monitoring.account_inspection_verdict_quota_limited_disabled')
        : t('monitoring.account_inspection_verdict_quota_limited');
    case 'recoverable':
      return t('monitoring.account_inspection_verdict_quota_recovered');
    case 'disabled':
      return t('monitoring.account_inspection_verdict_disabled');
    case 'healthy':
    default:
      return item.disabled
        ? t('monitoring.account_inspection_verdict_healthy_disabled')
        : t('monitoring.account_inspection_verdict_healthy');
  }
};

export const inspectionToastTone = (healthStatus: ResultHealthStatus): 'success' | 'warning' | 'error' => {
  if (healthStatus === 'healthy' || healthStatus === 'recoverable') return 'success';
  if (healthStatus === 'inspectionError' || healthStatus === 'authInvalid') return 'error';
  return 'warning';
};

export const formatInspectionResultToast = (
  item: AccountInspectionResultItem,
  t: TFunction
) => {
  const healthStatus = resolveResultHealthStatus(item);
  const primary = formatInspectionVerdictPrimary(item, healthStatus, t);
  return {
    message: `${item.fileName}: ${primary}`,
    tone: inspectionToastTone(healthStatus),
  };
};

export const formatTokenRefreshToast = (
  item: AccountInspectionResultItem,
  fallbackError: string | undefined,
  locale: string,
  t: TFunction
) => {
  const detail = item.tokenRefreshError || fallbackError || formatTokenRefreshDetail(item, locale, t);
  if (item.tokenRefreshStatus === 'success') {
    return {
      message: detail
        ? `${item.fileName}: ${t('monitoring.account_inspection_token_refresh_success')} · ${detail}`
        : `${item.fileName}: ${t('monitoring.account_inspection_token_refresh_success')}`,
      tone: 'success' as const,
    };
  }
  if (item.tokenRefreshStatus === 'failed' || fallbackError) {
    return {
      message: detail
        ? `${item.fileName}: ${t('monitoring.account_inspection_token_refresh_failed')} · ${detail}`
        : `${item.fileName}: ${t('monitoring.account_inspection_token_refresh_failed')}`,
      tone: 'error' as const,
    };
  }
  return {
    message: `${item.fileName}: ${t('monitoring.account_inspection_token_refresh_not_triggered')}`,
    tone: 'warning' as const,
  };
};

export const formatCurrentStateLabel = (item: AccountInspectionResultItem, t: TFunction) => {
  if (item.disabled) return t('monitoring.account_inspection_state_disabled');
  return t('monitoring.account_inspection_state_enabled');
};

export const formatRunInspectionButtonLabel = (status: RunStatus, t: TFunction) => {
  if (status === 'paused') return t('monitoring.account_inspection_resume');
  if (status === 'running') return t('monitoring.account_inspection_running');
  return t('monitoring.account_inspection_run');
};

export const countActions = (items: AccountInspectionResultItem[]) => {
  const summary = {
    delete: 0,
    disable: 0,
    enable: 0,
  };

  items.forEach((item) => {
    if (item.action === 'delete') summary.delete += 1;
    if (item.action === 'disable') summary.disable += 1;
    if (item.action === 'enable') summary.enable += 1;
  });

  return summary;
};

export const toAccountInspectionApiItem = (item: AccountInspectionResultItem): AccountInspectionInspectOneItem => ({
  key: item.key,
  provider: item.provider,
  fileName: item.fileName,
  displayName: item.displayAccount,
  email: item.email,
  name: item.name,
  authIndex: item.authIndex,
  disabled: item.disabled,
});

export const buildActionRiskPreview = (items: AccountInspectionResultItem[], t: TFunction) =>
  items
    .filter((item) => item.action === 'delete' || item.action === 'disable')
    .slice(0, 5)
    .map((item) => ({
      key: item.key,
      account: item.fileName,
      provider: item.provider,
      action: formatActionLabel(item.action, t),
      reason: item.actionReason || item.error || '-',
      dangerous: item.action === 'delete',
    }));

export const buildExecuteConfirmationMessage = (
  items: AccountInspectionResultItem[],
  t: TFunction,
  hasAutoExecutePolicy: boolean
) => {
  const counts = countActions(items);
  const preview = buildActionRiskPreview(items, t);
  const hasDelete = counts.delete > 0;

  return (
    <div className={styles.confirmationBody}>
      <p>
        {t('monitoring.account_inspection_execute_confirm_body', {
          total: items.length,
          delete: counts.delete,
          disable: counts.disable,
          enable: counts.enable,
        })}
      </p>
      <div className={styles.confirmationStats}>
        <span className={hasDelete ? styles.confirmationDangerStat : ''}>{`${t('monitoring.account_inspection_action_delete')}: ${counts.delete}`}</span>
        <span>{`${t('monitoring.account_inspection_action_disable')}: ${counts.disable}`}</span>
        <span>{`${t('monitoring.account_inspection_action_enable')}: ${counts.enable}`}</span>
      </div>
      {preview.length > 0 ? (
        <div className={styles.confirmationPreview}>
          <strong>{t('monitoring.account_inspection_preview_title')}</strong>
          {preview.map((item) => (
            <div key={item.key} className={styles.confirmationPreviewRow}>
              <span>{item.account}</span>
              <small>{item.provider}</small>
              <strong className={item.dangerous ? styles.errorText : undefined}>{item.action}</strong>
              <em>{item.reason}</em>
            </div>
          ))}
        </div>
      ) : null}
      {hasAutoExecutePolicy ? (
        <p className={styles.warningText}>
          {t('monitoring.account_inspection_settings_auto_section_desc')}
        </p>
      ) : null}
      {hasDelete ? (
        <p className={styles.dangerText}>
          {t('monitoring.account_inspection_delete_irreversible_warning')}
        </p>
      ) : null}
    </div>
  );
};

export const buildConfirmationAccountCard = (
  item: AccountInspectionResultItem,
  t: TFunction
) => (
  <div className={styles.confirmationAccountCard}>
    <div>
      <span>{item.fileName}</span>
      <small>{item.provider}</small>
    </div>
    <strong>{item.disabled ? t('monitoring.account_inspection_state_disabled') : t('monitoring.account_inspection_state_enabled')}</strong>
  </div>
);

export const buildDeleteConfirmationMessage = (
  item: AccountInspectionResultItem,
  t: TFunction
) => (
  <div className={styles.confirmationBody}>
    <div className={`${styles.confirmationLead} ${styles.confirmationLeadDanger}`}>
      <strong>{t('monitoring.account_inspection_delete_single_title')}</strong>
      <span>
        {t('monitoring.account_inspection_delete_single_confirm_body', {
          account: item.fileName,
        })}
      </span>
    </div>
    {buildConfirmationAccountCard(item, t)}
    <div className={`${styles.confirmationNotice} ${styles.confirmationNoticeDanger}`}>
      {t('monitoring.account_inspection_delete_single_warning')}
    </div>
  </div>
);

export const buildRefreshTokenConfirmationMessage = (
  item: AccountInspectionResultItem,
  t: TFunction
) => (
  <div className={styles.confirmationBody}>
    <div className={styles.confirmationLead}>
      <strong>{t('monitoring.account_inspection_refresh_token_confirm_title')}</strong>
      <span>
        {t('monitoring.account_inspection_refresh_token_confirm_body', {
          account: item.fileName,
        })}
      </span>
    </div>
    {buildConfirmationAccountCard(item, t)}
    <div className={styles.confirmationNotice}>
      {t('monitoring.account_inspection_refresh_token_confirm_hint')}
    </div>
  </div>
);

export const withChanged = <S, K extends keyof S>(
  state: S,
  key: K,
  next: S[K],
  isEqual: (left: S[K], right: S[K]) => boolean
): S => {
  if (isEqual(next, state[key])) return state;
  return { ...state, [key]: next };
};

export const sameProgressSnapshot = (left: AccountInspectionProgressSnapshot, right: AccountInspectionProgressSnapshot) =>
  left.total === right.total &&
  left.completed === right.completed &&
  left.inFlight === right.inFlight &&
  left.pending === right.pending &&
  left.percent === right.percent &&
  left.status === right.status &&
  left.startedAt === right.startedAt &&
  left.summary.totalFiles === right.summary.totalFiles &&
  left.summary.probeSetCount === right.summary.probeSetCount &&
  left.summary.sampledCount === right.summary.sampledCount &&
  left.summary.disabledCount === right.summary.disabledCount &&
  left.summary.enabledCount === right.summary.enabledCount &&
  left.summary.deleteCount === right.summary.deleteCount &&
  left.summary.disableCount === right.summary.disableCount &&
  left.summary.enableCount === right.summary.enableCount &&
  left.summary.keepCount === right.summary.keepCount &&
  left.summary.errorCount === right.summary.errorCount;

export const sameInspectionSettings = (left: AccountInspectionConfigurableSettings, right: AccountInspectionConfigurableSettings) =>
  left.targetType === right.targetType &&
  left.workers === right.workers &&
  left.deleteWorkers === right.deleteWorkers &&
  left.timeout === right.timeout &&
  left.retries === right.retries &&
  left.usedPercentThreshold === right.usedPercentThreshold &&
  left.sampleSize === right.sampleSize &&
  left.antigravityDeepProbeEnabled === right.antigravityDeepProbeEnabled &&
  left.antigravityDeepProbeModel === right.antigravityDeepProbeModel &&
  left.antigravityQuotaMode === right.antigravityQuotaMode &&
  left.xaiDeepProbeEnabled === right.xaiDeepProbeEnabled &&
  left.xaiDeepProbeModel === right.xaiDeepProbeModel &&
  left.autoExecuteQuotaLimitDisable === right.autoExecuteQuotaLimitDisable &&
  left.autoExecuteQuotaRecoveryEnable === right.autoExecuteQuotaRecoveryEnable &&
  left.autoExecuteAccountInvalidAction === right.autoExecuteAccountInvalidAction &&
  left.autoExecuteRequestErrorAction === right.autoExecuteRequestErrorAction &&
  left.autoExecuteConfirmations === right.autoExecuteConfirmations;

export const sameSettingsDraft = (left: InspectionSettingsDraft, right: InspectionSettingsDraft) =>
  left.targetType === right.targetType &&
  left.workers === right.workers &&
  left.deleteWorkers === right.deleteWorkers &&
  left.timeout === right.timeout &&
  left.retries === right.retries &&
  left.usedPercentThreshold === right.usedPercentThreshold &&
  left.sampleSize === right.sampleSize &&
  left.antigravityDeepProbeEnabled === right.antigravityDeepProbeEnabled &&
  left.antigravityDeepProbeModel === right.antigravityDeepProbeModel &&
  left.antigravityQuotaMode === right.antigravityQuotaMode &&
  left.xaiDeepProbeEnabled === right.xaiDeepProbeEnabled &&
  left.xaiDeepProbeModel === right.xaiDeepProbeModel &&
  left.autoExecuteQuotaLimitDisable === right.autoExecuteQuotaLimitDisable &&
  left.autoExecuteQuotaRecoveryEnable === right.autoExecuteQuotaRecoveryEnable &&
  left.autoExecuteAccountInvalidAction === right.autoExecuteAccountInvalidAction &&
  left.autoExecuteRequestErrorAction === right.autoExecuteRequestErrorAction;

export const sameScheduleDraft = (left: ScheduleDraft, right: ScheduleDraft) =>
  left.enabled === right.enabled && left.intervalMinutes === right.intervalMinutes;

export type InspectionScheduleSnapshot = AccountInspectionScheduleResponse['schedule'];

export const sameScheduleSnapshot = (
  left: InspectionScheduleSnapshot | null,
  right: InspectionScheduleSnapshot | null
) => {
  if (left === right) return true;
  if (!left || !right) return false;
  return left.enabled === right.enabled &&
    left.intervalMinutes === right.intervalMinutes &&
    left.nextRunAt === right.nextRunAt &&
    sameInspectionSettings(left.settings, right.settings);
};

export const sameAutoExecutionCounts = (left: AutoExecutionCounts, right: AutoExecutionCounts) =>
  left.delete === right.delete && left.disable === right.disable && left.enable === right.enable;

export const sameRunStatus = (left: RunStatus, right: RunStatus) => left === right;

export const handleAccountInspectionControlError = (
  error: unknown,
  appendLog: (level: AccountInspectionLogLevel, message: string) => void,
  showNotification: (message: string, type?: 'info' | 'success' | 'warning' | 'error') => void,
  fallbackMessage: string
) => {
  const message = error instanceof Error ? error.message : String(error || fallbackMessage);
  appendLog('error', message);
  showNotification(message, 'error');
};

export type BackendInspectionViewState = ReturnType<typeof buildAccountInspectionBackendViewState>;

export type InspectionBackendState = {
  inspectionSettings: AccountInspectionConfigurableSettings;
  settingsDraft: InspectionSettingsDraft;
  scheduleDraft: ScheduleDraft;
  schedule: InspectionScheduleSnapshot | null;
  logs: InspectionLogEntry[];
  logsPage: AccountInspectionPageInfo | null;
  runStatus: RunStatus;
  progress: AccountInspectionProgressSnapshot;
  result: AccountInspectionRunResult | null;
  autoExecutionCounts: AutoExecutionCounts;
  restoredSnapshot: boolean;
};

export type InspectionBackendAction =
  | { type: 'configChanged'; settings: AccountInspectionConfigurableSettings; syncDraft: boolean }
  | { type: 'backendResponseReceived'; response: AccountInspectionScheduleResponse }
  | { type: 'clearSchedule' }
  | { type: 'appendLog'; level: AccountInspectionLogLevel; message: string; timestamp: number }
  | { type: 'clearLogs' }
  | { type: 'startRun'; timestamp: number }
  | { type: 'runFailed' }
  | { type: 'clearAutoExecutionCounts' }
  | { type: 'setResult'; result: AccountInspectionRunResult | null }
  | { type: 'resetSettings'; settings: AccountInspectionConfigurableSettings }
  | { type: 'setSettingsDraft'; draft: InspectionSettingsDraft }
  | { type: 'updateSettingsDraft'; values: Partial<InspectionSettingsDraft> }
  | { type: 'updateScheduleDraft'; values: Partial<ScheduleDraft> };

export const createInspectionBackendState = (settings: AccountInspectionConfigurableSettings): InspectionBackendState => ({
  inspectionSettings: settings,
  settingsDraft: toSettingsDraft(settings),
  scheduleDraft: { enabled: false, intervalMinutes: '360' },
  schedule: null,
  logs: [],
  logsPage: null,
  runStatus: 'idle',
  progress: createIdleAccountInspectionProgressSnapshot(),
  result: null,
  autoExecutionCounts: emptyAutoExecutionCounts(),
  restoredSnapshot: false,
});

export const applyBackendViewState = (
  state: InspectionBackendState,
  response: AccountInspectionScheduleResponse,
  viewState: BackendInspectionViewState
) => {
  let nextState = state;
  nextState = withChanged(nextState, 'inspectionSettings', viewState.settings, sameInspectionSettings);
  nextState = withChanged(nextState, 'settingsDraft', toSettingsDraft(viewState.settings), sameSettingsDraft);
  nextState = withChanged(nextState, 'scheduleDraft', viewState.scheduleDraft, sameScheduleDraft);
  nextState = withChanged(nextState, 'schedule', response.schedule, sameScheduleSnapshot);
  nextState = withChanged(nextState, 'autoExecutionCounts', viewState.autoExecutionCounts, sameAutoExecutionCounts);
  nextState = withChanged(nextState, 'progress', viewState.progress, sameProgressSnapshot);
  nextState = withChanged(nextState, 'runStatus', viewState.runStatus, sameRunStatus);
  nextState = withChanged(nextState, 'restoredSnapshot', viewState.restoredSnapshot, Object.is);
  if (viewState.logs) {
    nextState = withChanged(nextState, 'logs', viewState.logs, Object.is);
  }
  if (viewState.logsPage !== undefined) {
    nextState = withChanged(nextState, 'logsPage', viewState.logsPage ?? null, Object.is);
  }
  if (viewState.result !== undefined) {
    nextState = withChanged(nextState, 'result', viewState.result, Object.is);
  }
  return nextState;
};

export const inspectionBackendReducer = (
  state: InspectionBackendState,
  action: InspectionBackendAction
): InspectionBackendState => {
  switch (action.type) {
    case 'configChanged': {
      let nextState = withChanged(state, 'inspectionSettings', action.settings, sameInspectionSettings);
      if (action.syncDraft) {
        nextState = withChanged(nextState, 'settingsDraft', toSettingsDraft(action.settings), sameSettingsDraft);
      }
      return nextState;
    }
    case 'backendResponseReceived':
      return applyBackendViewState(state, action.response, buildAccountInspectionBackendViewState(action.response));
    case 'clearSchedule':
      return state.schedule === null ? state : { ...state, schedule: null };
    case 'appendLog':
      return {
        ...state,
        logs: appendInspectionLogEntry(state.logs, {
          id: `${action.timestamp}-${state.logs.length}`,
          level: action.level,
          message: action.message,
          timestamp: action.timestamp,
        }),
      };
    case 'clearLogs':
      return state.logs.length === 0 ? state : { ...state, logs: [] };
    case 'startRun':
      return {
        ...state,
        result: null,
        runStatus: 'running',
        restoredSnapshot: false,
        autoExecutionCounts: emptyAutoExecutionCounts(),
        progress: {
          ...createIdleAccountInspectionProgressSnapshot(),
          status: 'running',
          startedAt: action.timestamp,
          updatedAt: action.timestamp,
        },
      };
    case 'runFailed':
      return state.runStatus === 'error' ? state : { ...state, runStatus: 'error' };
    case 'clearAutoExecutionCounts':
      return withChanged(state, 'autoExecutionCounts', emptyAutoExecutionCounts(), sameAutoExecutionCounts);
    case 'setResult':
      return state.result === action.result ? state : { ...state, result: action.result };
    case 'resetSettings':
      return {
        ...state,
        inspectionSettings: action.settings,
        settingsDraft: toSettingsDraft(action.settings),
      };
    case 'setSettingsDraft':
      return withChanged(state, 'settingsDraft', action.draft, sameSettingsDraft);
    case 'updateSettingsDraft':
      return { ...state, settingsDraft: { ...state.settingsDraft, ...action.values } };
    case 'updateScheduleDraft':
      return { ...state, scheduleDraft: { ...state.scheduleDraft, ...action.values } };
    default:
      return state;
  }
};
