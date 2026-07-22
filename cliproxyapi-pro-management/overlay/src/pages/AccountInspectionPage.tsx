import { startTransition, useCallback, useEffect, useMemo, useReducer, useRef, useState, type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { Modal } from '@/components/ui/Modal';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconChevronDown,
  IconChevronUp,
  IconDownload,
} from '@/components/ui/icons';
import { getAuthFileIcon } from '@/features/authFiles/constants';
import {
  ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE,
  ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS,
  ACCOUNT_INSPECTION_SETTING_LIMITS,
  accountInspectionBackendResultToItem,
  buildAccountInspectionBackendViewState,
  buildExecutionFailureMessage,
  clearAccountInspectionConfigurableSettings,
  createIdleAccountInspectionProgressSnapshot,
  DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
  hasAccountInspectionAutoExecutePolicies,
  isSuggestedAction,
  loadAccountInspectionConfigurableSettings,
  normalizeAntigravityQuotaMode,
  normalizeAutoErrorAction,
  saveAccountInspectionConfigurableSettings,
  type AccountInspectionAction,
  type AccountInspectionAntigravityQuotaMode,
  type AccountInspectionAutoErrorAction,
  type AccountInspectionConfigurableSettings,
  type AccountInspectionLogLevel,
  type AccountInspectionPageInfo,
  type AccountInspectionProgressSnapshot,
  type AccountInspectionResultItem,
  type AccountInspectionRunResult,
} from '@/features/monitoring/accountInspection';
import {
  accountInspectionApi,
  accountInspectionWebSocketProtocol,
  apiClient,
  buildAccountInspectionLogsWebSocketUrl,
  nextAccountInspectionReconnectDelay,
  type AccountInspectionInspectOneItem,
  type AccountInspectionLogStreamMessage,
  type AccountInspectionScheduleResponse,
} from '@/services/api';
import { authFilesApi } from '@/services/api/authFiles';
import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';
import { useAuthStore, useConfigStore, useNotificationStore, useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { isDisabledAuthFile, isQuotaLowState, isRecordValue, normalizeNumberValue, readBooleanValue, readStringValue, resolveAuthProvider } from '@/utils/quota';
import { resolveProviderDisplayLabel } from '@/utils/sourceResolver';
import quotaStyles from '@/pages/QuotaPage.module.scss';
import styles from './AccountInspectionPage.module.scss';

type RunStatus = 'idle' | 'running' | 'paused' | 'success' | 'error';

type ResultHealthStatus = 'healthy' | 'disabled' | 'authInvalid' | 'quotaExhausted' | 'inspectionError' | 'recoverable';

type ResultFilter = 'pending' | 'accountInvalid' | 'requestError' | 'quotaExhausted' | 'recoverable' | 'highAvailable';

type SettingsSectionKey = 'plan' | 'scope' | 'runtime' | 'antigravity' | 'auto';

type ManualAccountInspectionAction = Exclude<AccountInspectionAction, 'keep'>;

type QuotaAccountStatsState = Pick<
  ReturnType<typeof useQuotaStore.getState>,
  'antigravityQuota' | 'claudeQuota' | 'codexQuota' | 'kimiQuota' | 'xaiQuota'
>;

type HealthCounts = {
  total: number;
  healthy: number;
  disabled: number;
  authInvalid: number;
  quotaExhausted: number;
  inspectionError: number;
  recoverable: number;
};

type InspectionLogEntry = {
  id: string;
  level: AccountInspectionLogLevel;
  message: string;
  timestamp: number;
};

type SummaryCard = {
  key: string;
  label: string;
  value: string;
  description?: string;
  tone?: 'neutral' | 'good' | 'warn' | 'bad';
};

type InspectionSettingsDraft = {
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

type InspectionSettingsDraftField = Exclude<
  keyof InspectionSettingsDraft,
  'antigravityDeepProbeEnabled' | 'antigravityQuotaMode' | 'xaiDeepProbeEnabled' | 'autoExecuteQuotaLimitDisable' | 'autoExecuteQuotaRecoveryEnable' | 'autoExecuteAccountInvalidAction' | 'autoExecuteRequestErrorAction'
>;

type ScheduleDraft = {
  enabled: boolean;
  intervalMinutes: string;
};

type ProviderAccountStats = {
  provider: string;
  total: number;
  enabled: number;
  highAvailable: number;
  disabled: number;
  quotaLow: number;
  accountInvalid: number;
  requestError: number;
};

type ResolvedTheme = 'light' | 'dark';

type AuthFileAccountStats = {
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

type AutoExecutionCounts = {
  delete: number;
  disable: number;
  enable: number;
};

type InspectionResultViewRow = {
  item: AccountInspectionResultItem;
  healthStatus: ResultHealthStatus;
  manualActions: ManualAccountInspectionAction[];
};

type InspectionResultsViewState = {
  rows: InspectionResultViewRow[];
  healthCounts: HealthCounts;
  actionableActionCounts: AutoExecutionCounts;
  filterRows: Record<ResultFilter, InspectionResultViewRow[]>;
  filterRowCounts: Record<ResultFilter, number>;
};

type AuthFileExportEntry = {
  name: string;
  content: string;
};


type ZipFileEntry = {
  path: string;
  data: Uint8Array;
  compressedData: Uint8Array;
  compressionMethod: 0 | 8;
  crc32: number;
};

const crcTable = Array.from({ length: 256 }, (_, index) => {
  let value = index;
  for (let bit = 0; bit < 8; bit += 1) {
    value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
  }
  return value >>> 0;
});

const ACCOUNT_INSPECTION_LOG_LIMIT = 200;
const ACCOUNT_INSPECTION_RESULT_PAGE_SIZE = 100;
const ACCOUNT_INSPECTION_LOG_PAGE_SIZE = 100;
const ACCOUNT_INSPECTION_ACTION_PAGE_SIZE = 500;
const ACCOUNT_INSPECTION_DETAILS_IDLE_DELAY_MS = 250;
const ACCOUNT_INSPECTION_AUTH_FILES_IDLE_DELAY_MS = 300;
const ACCOUNT_INSPECTION_ASSET_STATS_CHUNK_SIZE = 500;
const ACCOUNT_INSPECTION_EXPORT_DOWNLOAD_CONCURRENCY = 4;
const ACCOUNT_INSPECTION_EXPORT_ZIP_CONCURRENCY = 4;

type AccountInspectionIdleCallback = (deadline: { didTimeout: boolean; timeRemaining: () => number }) => void;

const appendInspectionLogEntry = (entries: InspectionLogEntry[], entry: InspectionLogEntry) =>
  [...entries, entry].slice(-ACCOUNT_INSPECTION_LOG_LIMIT);

const getPaginationRange = (pageInfo: AccountInspectionPageInfo | null, visibleCount: number) => {
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

const getProviderInitial = (label: string) => label.trim().charAt(0).toUpperCase() || '?';

const getDocumentTheme = (): ResolvedTheme => {
  if (typeof document === 'undefined') return 'light';
  const root = document.documentElement;
  const theme = root.dataset.theme || root.getAttribute('data-theme') || root.className;
  return String(theme).toLowerCase().includes('dark') ? 'dark' : 'light';
};

const emptyAutoExecutionCounts = (): AutoExecutionCounts => ({
  delete: 0,
  disable: 0,
  enable: 0,
});

const createEmptyFilterRows = (): Record<ResultFilter, InspectionResultViewRow[]> => ({
  pending: [],
  accountInvalid: [],
  requestError: [],
  quotaExhausted: [],
  recoverable: [],
  highAvailable: [],
});

const getCrc32 = (data: Uint8Array) => {
  let crc = 0xffffffff;
  data.forEach((byte) => {
    crc = crcTable[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  });
  return (crc ^ 0xffffffff) >>> 0;
};

const getDosTimestamp = (date: Date) => {
  const year = Math.max(1980, date.getFullYear());
  return {
    time: (date.getHours() << 11) | (date.getMinutes() << 5) | Math.floor(date.getSeconds() / 2),
    date: ((year - 1980) << 9) | ((date.getMonth() + 1) << 5) | date.getDate(),
  };
};

const writeUint16 = (view: DataView, offset: number, value: number) => {
  view.setUint16(offset, value, true);
};

const writeUint32 = (view: DataView, offset: number, value: number) => {
  view.setUint32(offset, value >>> 0, true);
};

const concatUint8Arrays = (parts: Uint8Array[]) => {
  const output = new Uint8Array(parts.reduce((total, part) => total + part.length, 0));
  let offset = 0;
  parts.forEach((part) => {
    output.set(part, offset);
    offset += part.length;
  });
  return output;
};

const sanitizeZipPathSegment = (value: string) => {
  const sanitized = value
    .replace(/[\\/:*?"<>|]/g, '_')
    .split('')
    .map((character) => character.charCodeAt(0) <= 0x1f ? '_' : character)
    .join('');
  return sanitized.replace(/^\.+$/, '_').trim() || 'unknown';
};

const getAuthFileZipPath = (entry: AuthFileExportEntry, usedPaths: Set<string>) => {
  const rawName = sanitizeZipPathSegment(entry.name);
  const baseName = rawName.toLowerCase().endsWith('.json') ? rawName : `${rawName}.json`;
  let path = baseName;
  let index = 2;

  while (usedPaths.has(path)) {
    const dotIndex = baseName.toLowerCase().lastIndexOf('.json');
    const stem = dotIndex >= 0 ? baseName.slice(0, dotIndex) : baseName;
    path = `${stem}-${index}.json`;
    index += 1;
  }

  usedPaths.add(path);
  return path;
};

const toArrayBuffer = (data: Uint8Array) => {
  const arrayBuffer = new ArrayBuffer(data.byteLength);
  new Uint8Array(arrayBuffer).set(data);
  return arrayBuffer;
};

const compressZipData = async (data: Uint8Array): Promise<{ data: Uint8Array; method: 0 | 8 }> => {
  const CompressionStreamCtor = (globalThis as typeof globalThis & {
    CompressionStream?: new (format: 'deflate-raw') => TransformStream<Uint8Array, Uint8Array>;
  }).CompressionStream;

  if (!CompressionStreamCtor) {
    return { data, method: 0 };
  }

  try {
    const stream = new Blob([toArrayBuffer(data)]).stream().pipeThrough(new CompressionStreamCtor('deflate-raw'));
    return { data: new Uint8Array(await new Response(stream).arrayBuffer()), method: 8 };
  } catch {
    return { data, method: 0 };
  }
};

const buildZipArchive = async (entries: AuthFileExportEntry[]) => {
  const encoder = new TextEncoder();
  const usedPaths = new Set<string>();
  const files: ZipFileEntry[] = await mapWithConcurrency(
    entries,
    ACCOUNT_INSPECTION_EXPORT_ZIP_CONCURRENCY,
    async (entry) => {
      const path = getAuthFileZipPath(entry, usedPaths);
      const data = encoder.encode(entry.content);
      const compressed = await compressZipData(data);
      return {
        path,
        data,
        compressedData: compressed.data,
        compressionMethod: compressed.method,
        crc32: getCrc32(data),
      };
    }
  );
  const timestamp = getDosTimestamp(new Date());
  const parts: Uint8Array[] = [];
  const centralDirectoryParts: Uint8Array[] = [];
  let offset = 0;

  files.forEach((file) => {
    const fileName = encoder.encode(file.path);
    const localHeader = new Uint8Array(30 + fileName.length);
    const localView = new DataView(localHeader.buffer);

    writeUint32(localView, 0, 0x04034b50);
    writeUint16(localView, 4, 20);
    writeUint16(localView, 6, 0x0800);
    writeUint16(localView, 8, file.compressionMethod);
    writeUint16(localView, 10, timestamp.time);
    writeUint16(localView, 12, timestamp.date);
    writeUint32(localView, 14, file.crc32);
    writeUint32(localView, 18, file.compressedData.length);
    writeUint32(localView, 22, file.data.length);
    writeUint16(localView, 26, fileName.length);
    localHeader.set(fileName, 30);

    parts.push(localHeader, file.compressedData);

    const centralDirectoryHeader = new Uint8Array(46 + fileName.length);
    const centralView = new DataView(centralDirectoryHeader.buffer);

    writeUint32(centralView, 0, 0x02014b50);
    writeUint16(centralView, 4, 20);
    writeUint16(centralView, 6, 20);
    writeUint16(centralView, 8, 0x0800);
    writeUint16(centralView, 10, file.compressionMethod);
    writeUint16(centralView, 12, timestamp.time);
    writeUint16(centralView, 14, timestamp.date);
    writeUint32(centralView, 16, file.crc32);
    writeUint32(centralView, 20, file.compressedData.length);
    writeUint32(centralView, 24, file.data.length);
    writeUint16(centralView, 28, fileName.length);
    writeUint32(centralView, 42, offset);
    centralDirectoryHeader.set(fileName, 46);

    centralDirectoryParts.push(centralDirectoryHeader);
    offset += localHeader.length + file.compressedData.length;
  });

  const centralDirectory = concatUint8Arrays(centralDirectoryParts);
  const endOfCentralDirectory = new Uint8Array(22);
  const endView = new DataView(endOfCentralDirectory.buffer);
  writeUint32(endView, 0, 0x06054b50);
  writeUint16(endView, 8, files.length);
  writeUint16(endView, 10, files.length);
  writeUint32(endView, 12, centralDirectory.length);
  writeUint32(endView, 16, offset);

  return new Blob(
    [...parts, centralDirectory, endOfCentralDirectory].map(toArrayBuffer),
    { type: 'application/zip' }
  );
};

const mapWithConcurrency = async <Input, Output>(
  items: Input[],
  concurrency: number,
  mapper: (item: Input, index: number) => Promise<Output>
) => {
  if (items.length === 0) return [] as Output[];
  const results = new Array<Output>(items.length);
  const workerCount = Math.max(1, Math.min(concurrency, items.length));
  let nextIndex = 0;

  await Promise.all(Array.from({ length: workerCount }, async () => {
    while (nextIndex < items.length) {
      const index = nextIndex;
      nextIndex += 1;
      results[index] = await mapper(items[index], index);
    }
  }));

  return results;
};

const downloadBlobFile = (fileName: string, blob: Blob) => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
};

const levelClassMap: Record<AccountInspectionLogLevel, string> = {
  info: styles.logInfo,
  success: styles.logSuccess,
  warning: styles.logWarning,
  error: styles.logError,
};

const healthToneClass: Record<ResultHealthStatus, string> = {
  healthy: styles.healthHealthy,
  disabled: styles.healthDisabled,
  authInvalid: styles.healthAuthInvalid,
  quotaExhausted: styles.healthQuota,
  inspectionError: styles.healthError,
  recoverable: styles.healthRecoverable,
};

const healthLabelKey: Record<ResultHealthStatus, string> = {
  healthy: 'monitoring.account_inspection_health_healthy',
  disabled: 'monitoring.account_inspection_health_disabled',
  authInvalid: 'monitoring.account_inspection_account_invalid',
  quotaExhausted: 'monitoring.account_inspection_health_quota_exhausted',
  inspectionError: 'monitoring.account_inspection_account_request_error',
  recoverable: 'monitoring.account_inspection_health_recoverable',
};

const extractHealthHttpStatusCode = (item: AccountInspectionResultItem) => {
  if (item.statusCode !== null && item.statusCode >= 400) return item.statusCode;
  const errorText = [item.error, item.deepProbeError, item.tokenRefreshError].filter(Boolean).join(' ');
  const match = errorText.match(/\bHTTP\s+(\d{3})\b/i) ?? errorText.match(/\bstatus(?:\s+code)?\s*[:=]?\s*(\d{3})\b/i);
  return match ? Number(match[1]) : null;
};

const buildHealthStatusCodeText = (item: AccountInspectionResultItem) => {
  const httpStatusCode = extractHealthHttpStatusCode(item);
  return httpStatusCode !== null ? String(httpStatusCode) : '';
};

const buildHealthStatusLabel = (
  item: AccountInspectionResultItem,
  healthStatus: ResultHealthStatus,
  t: ReturnType<typeof useTranslation>['t']
) => {
  const label = t(healthLabelKey[healthStatus]);
  const code = buildHealthStatusCodeText(item);
  return code ? `${label} · ${code}` : label;
};

const hasInspectionErrorDetails = (item: AccountInspectionResultItem) => Boolean(
  item.error
  || item.errorDetail?.trim()
  || item.errorCode?.trim()
  || item.deepProbeError
  || item.tokenRefreshError
  || extractHealthHttpStatusCode(item) !== null
);

const parseInspectionErrorPayload = (value: string): unknown => {
  const text = value.trim();
  if (!text || (!text.startsWith('{') && !text.startsWith('['))) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
};

const readInspectionErrorMessage = (value: unknown): string => {
  if (typeof value === 'string') return value.trim();
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  const record = value as Record<string, unknown>;
  const directMessage = readInspectionErrorMessage(record.message);
  if (directMessage) return directMessage;
  const nestedError = readInspectionErrorMessage(record.error);
  if (nestedError) return nestedError;
  return '';
};

const buildInspectionErrorPresentation = (item: AccountInspectionResultItem) => {
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

const resolveResultHealthStatus = (item: AccountInspectionResultItem): ResultHealthStatus => {
  if (item.action === 'delete' || (item.statusCode !== null && ACCOUNT_INVALID_ERROR_STATUSES.has(item.statusCode))) {
    return 'authInvalid';
  }
  if (item.error) return 'inspectionError';
  if (item.isQuota || item.action === 'disable') return 'quotaExhausted';
  if (item.action === 'enable') return 'recoverable';
  if (item.disabled) return 'disabled';
  return 'healthy';
};

function InspectionErrorDetailsPanel({
  item,
  t,
}: {
  item: AccountInspectionResultItem;
  t: ReturnType<typeof useTranslation>['t'];
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

const ACCOUNT_INVALID_ERROR_STATUSES = new Set([400, 401, 403, 404]);
const ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET = new Set<string>(ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS);

const readAuthFileField = (file: AuthFileItem, key: string) =>
  readStringValue((file as unknown as Record<string, unknown>)[key]);

const isAccountInspectionApiKeyAuthFile = (file: AuthFileItem) => {
  const label = readAuthFileField(file, 'label').toLowerCase();
  const source = readAuthFileField(file, 'source').toLowerCase();
  const apiKey = readAuthFileField(file, 'api_key') || readAuthFileField(file, 'apiKey');
  const path = readAuthFileField(file, 'path');

  return label.includes('apikey') ||
    label.includes('api-key') ||
    (source.startsWith('config:') && Boolean(apiKey)) ||
    (Boolean(apiKey) && !path);
};

const isInspectableAccountInspectionAuthFile = (file: AuthFileItem) => {
  const provider = resolveAuthProvider(file);
  return ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET.has(provider) && !isAccountInspectionApiKeyAuthFile(file);
};

const readAuthFileStatusMessage = (file: AuthFileItem) => {
  const raw = file['status_message'] ?? file.statusMessage;
  if (raw === undefined || raw === null) return '';
  return String(raw).trim();
};

const readAuthFileLastError = (file: AuthFileItem) => {
  const raw = file['last_error'] ?? file.lastError;
  return isRecordValue(raw) ? raw : null;
};

const readAuthFileLastErrorCode = (file: AuthFileItem) => readStringValue(readAuthFileLastError(file)?.code);

const readAuthFileLastErrorStatus = (file: AuthFileItem) => {
  const error = readAuthFileLastError(file);
  return error ? normalizeNumberValue(error.http_status ?? error.httpStatus ?? error.status) : null;
};

const isAuthFileAccountInvalid = (file: AuthFileItem) =>
  readAuthFileLastErrorCode(file) === 'inspection_http_error' &&
  ACCOUNT_INVALID_ERROR_STATUSES.has(readAuthFileLastErrorStatus(file) ?? 0);

const isAuthFileRequestError = (file: AuthFileItem) => {
  const code = readAuthFileLastErrorCode(file);
  if (code === 'inspection_probe_error' || code === 'antigravity_deep_probe_error') return true;
  if (isAuthFileAccountInvalid(file)) return false;
  if (readAuthFileLastError(file)) return true;
  if (readBooleanValue(file.unavailable ?? file['unavailable'])) return true;
  const status = String(file.status ?? file.state ?? '').trim().toLowerCase();
  return status === 'error' || readAuthFileStatusMessage(file).length > 0;
};

const incrementProviderStats = (stats: ProviderAccountStats, disabled: boolean, highAvailable: boolean, quotaLow: boolean, accountInvalid: boolean, requestError: boolean) => {
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

const emptyProviderAccountStats = (provider: string): ProviderAccountStats => ({
  provider,
  total: 0,
  enabled: 0,
  highAvailable: 0,
  disabled: 0,
  quotaLow: 0,
  accountInvalid: 0,
  requestError: 0,
});

const createEmptyAuthFileAccountStats = (): AuthFileAccountStats => ({
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

const finalizeAuthFileAccountStats = (
  stats: AuthFileAccountStats,
  providerStats: Map<string, ProviderAccountStats>
) => ({
  ...stats,
  providerCount: providerStats.size,
  providers: [...providerStats.values()].sort((left, right) => right.total - left.total || left.provider.localeCompare(right.provider)),
});

const quotaUsedPercentFromRemaining = (item: unknown): number | null => {
  if (!isRecordValue(item)) return null;
  const usedPercent = normalizeNumberValue(item.usedPercent ?? item.used_percent);
  if (usedPercent !== null) return Math.max(0, Math.min(100, usedPercent));
  const remainingFraction = normalizeNumberValue(item.remainingFraction ?? item.remaining_fraction);
  if (remainingFraction === null) return null;
  const normalized = remainingFraction > 1 && remainingFraction <= 100 ? remainingFraction / 100 : remainingFraction;
  return Math.max(0, Math.min(100, (1 - Math.max(0, Math.min(1, normalized))) * 100));
};

const maxQuotaUsedPercent = (items: unknown): number | null => {
  if (!Array.isArray(items)) return null;
  const values = items
    .map(quotaUsedPercentFromRemaining)
    .filter((value): value is number => value !== null);
  if (values.length === 0) return null;
  return Math.max(...values);
};

const antigravityGroupUsedPercent = (group: unknown): number | null => {
  if (!isRecordValue(group)) return null;
  return maxQuotaUsedPercent(group.buckets);
};

const maxAntigravityGroupUsedPercent = (groups: unknown[]): number | null => {
  const values = groups
    .map(antigravityGroupUsedPercent)
    .filter((value): value is number => value !== null);
  if (values.length === 0) return null;
  return Math.max(...values);
};

const isAntigravityClaudeGptGroup = (group: unknown): boolean => {
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

const isAntigravityQuotaLow = (
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

const isXaiQuotaLow = (quota: unknown, usedPercentThreshold: number) => {
  if (!isRecordValue(quota) || quota.status !== 'success') return false;
  if (!isRecordValue(quota.billing)) return false;
  const used =
    normalizeNumberValue(quota.billing.usagePercent ?? quota.billing.usage_percent)
    ?? normalizeNumberValue(quota.billing.usedPercent ?? quota.billing.used_percent)
    ?? maxAntigravityGroupUsedPercent(Array.isArray(quota.billing.productUsage) ? quota.billing.productUsage : []);
  return used !== null && used >= usedPercentThreshold;
};

const isProviderQuotaLow = (
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

const accumulateAuthFileAccountStats = (
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

type AuthFileAccountStatsJob = {
  cancelled: boolean;
  handle: number | null;
  cancelHandle: ((handle: number) => void) | null;
};

const scheduleAuthFileAccountStats = (
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

const emptyHealthCounts = (): HealthCounts => ({
  total: 0,
  healthy: 0,
  disabled: 0,
  authInvalid: 0,
  quotaExhausted: 0,
  inspectionError: 0,
  recoverable: 0,
});

const getManualActionsByHealthStatus = (
  item: AccountInspectionResultItem,
  healthStatus: ResultHealthStatus
): ManualAccountInspectionAction[] => {
  if (healthStatus === 'healthy') return [];
  return [item.disabled ? 'enable' : 'disable', 'delete'];
};

const buildInspectionResultsViewState = (items: AccountInspectionResultItem[]): InspectionResultsViewState => {
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

const collectActionableInspectionResults = (items: AccountInspectionResultItem[]) => {
  const targets: AccountInspectionResultItem[] = [];
  items.forEach((item) => {
    if (isSuggestedAction(item) && !item.executed) {
      targets.push(item);
    }
  });
  return targets;
};

const buildManualActionItem = (
  item: AccountInspectionResultItem,
  action: ManualAccountInspectionAction
): AccountInspectionResultItem => ({
  ...item,
  action,
  actionReason: item.actionReason || action,
});

const summaryToneClass: Record<NonNullable<SummaryCard['tone']>, string> = {
  neutral: '',
  good: styles.summaryGood,
  warn: styles.summaryWarn,
  bad: styles.summaryBad,
};

const INSPECTION_TARGET_OPTIONS = [
  { value: ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE, label: 'All' },
  ...ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS.map((provider) => ({
    value: provider,
    label: resolveProviderDisplayLabel(provider),
  })),
] as const;

const AUTO_ERROR_ACTION_OPTIONS: Array<{ value: AccountInspectionAutoErrorAction; labelKey: string }> = [
  { value: 'none', labelKey: 'monitoring.account_inspection_settings_account_error_action_none' },
  { value: 'disable', labelKey: 'monitoring.account_inspection_settings_account_error_action_disable' },
  { value: 'delete', labelKey: 'monitoring.account_inspection_settings_account_error_action_delete' },
];

const ANTIGRAVITY_QUOTA_MODE_OPTIONS: Array<{ value: AccountInspectionAntigravityQuotaMode; labelKey: string }> = [
  { value: 'claude-gpt', labelKey: 'monitoring.account_inspection_settings_antigravity_quota_mode_claude_gpt' },
  { value: 'max-used', labelKey: 'monitoring.account_inspection_settings_antigravity_quota_mode_max_used' },
];

const {
  workers: WORKER_LIMITS,
  deleteWorkers: DELETE_WORKER_LIMITS,
  timeout: TIMEOUT_LIMITS,
  retries: RETRY_LIMITS,
  usedPercentThreshold: THRESHOLD_LIMITS,
  sampleSize: SAMPLE_SIZE_LIMITS,
  scheduleIntervalMinutes: SCHEDULE_INTERVAL_LIMITS,
} = ACCOUNT_INSPECTION_SETTING_LIMITS;

const formatTimestamp = (value: number, locale: string) => new Date(value).toLocaleString(locale);

const formatInspectionInterval = (minutes: number, locale: string) =>
  new Intl.NumberFormat(locale, { style: 'unit', unit: 'minute', unitDisplay: 'short' }).format(minutes);

const buildHighAvailabilityBarStyle = (highAvailable: number, total: number): CSSProperties => {
  const share = total > 0 ? Math.min(Math.max(highAvailable / total, 0), 1) : 0;
  return {
    '--bar-width': `${share * 100}%`,
    '--bar-color': `hsl(${Math.round(share * 120)}, 72%, 44%)`,
  } as CSSProperties;
};

const toSettingsDraft = (settings: AccountInspectionConfigurableSettings): InspectionSettingsDraft => ({
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

const formatActionLabel = (action: AccountInspectionAction, t: ReturnType<typeof useTranslation>['t']) => {
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

const formatQuotaRemainingLabel = (value: number | null) => {
  if (value === null) return '--';
  return `${Math.max(0, 100 - value).toFixed(1)}%`;
};

const formatTokenRefreshLabel = (
  item: AccountInspectionResultItem,
  t: ReturnType<typeof useTranslation>['t']
) => {
  if (item.tokenRefreshStatus === 'success') return t('monitoring.account_inspection_token_refresh_success');
  if (item.tokenRefreshStatus === 'failed') return t('monitoring.account_inspection_token_refresh_failed');
  if (item.nextRefreshAt && item.nextRefreshAt > Date.now()) return t('monitoring.account_inspection_token_refresh_pending');
  return t('monitoring.account_inspection_token_refresh_not_triggered');
};

const formatTokenRefreshDetail = (
  item: AccountInspectionResultItem,
  locale: string,
  t: ReturnType<typeof useTranslation>['t']
) => {
  if (item.tokenRefreshStatus === 'failed') return item.tokenRefreshError || '';
  if (item.nextRefreshAt && item.nextRefreshAt > 0) {
    return t('monitoring.account_inspection_token_next_refresh_at', {
      time: formatTimestamp(item.nextRefreshAt, locale),
    });
  }
  return '';
};

const tokenRefreshToneClass = (item: AccountInspectionResultItem) => {
  if (item.tokenRefreshStatus === 'success') return styles.stateTextGood;
  if (item.tokenRefreshStatus === 'failed') return styles.stateTextBad;
  if (item.nextRefreshAt && item.nextRefreshAt > Date.now()) return styles.stateTextWarn;
  return styles.stateTextMuted;
};

const formatInspectionVerdictPrimary = (
  item: AccountInspectionResultItem,
  healthStatus: ResultHealthStatus,
  t: ReturnType<typeof useTranslation>['t']
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

const inspectionToastTone = (healthStatus: ResultHealthStatus): 'success' | 'warning' | 'error' => {
  if (healthStatus === 'healthy' || healthStatus === 'recoverable') return 'success';
  if (healthStatus === 'inspectionError' || healthStatus === 'authInvalid') return 'error';
  return 'warning';
};

const formatInspectionResultToast = (
  item: AccountInspectionResultItem,
  t: ReturnType<typeof useTranslation>['t']
) => {
  const healthStatus = resolveResultHealthStatus(item);
  const primary = formatInspectionVerdictPrimary(item, healthStatus, t);
  return {
    message: `${item.fileName}: ${primary}`,
    tone: inspectionToastTone(healthStatus),
  };
};

const formatTokenRefreshToast = (
  item: AccountInspectionResultItem,
  fallbackError: string | undefined,
  locale: string,
  t: ReturnType<typeof useTranslation>['t']
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

const formatCurrentStateLabel = (item: AccountInspectionResultItem, t: ReturnType<typeof useTranslation>['t']) => {
  if (item.disabled) return t('monitoring.account_inspection_state_disabled');
  return t('monitoring.account_inspection_state_enabled');
};

const formatRunInspectionButtonLabel = (status: RunStatus, t: ReturnType<typeof useTranslation>['t']) => {
  if (status === 'paused') return t('monitoring.account_inspection_resume');
  if (status === 'running') return t('monitoring.account_inspection_running');
  return t('monitoring.account_inspection_run');
};

const countActions = (items: AccountInspectionResultItem[]) => {
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

const toAccountInspectionApiItem = (item: AccountInspectionResultItem): AccountInspectionInspectOneItem => ({
  key: item.key,
  provider: item.provider,
  fileName: item.fileName,
  displayName: item.displayAccount,
  email: item.email,
  name: item.name,
  authIndex: item.authIndex,
  disabled: item.disabled,
});

const buildActionRiskPreview = (items: AccountInspectionResultItem[], t: ReturnType<typeof useTranslation>['t']) =>
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

const buildExecuteConfirmationMessage = (
  items: AccountInspectionResultItem[],
  t: ReturnType<typeof useTranslation>['t'],
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

const buildConfirmationAccountCard = (
  item: AccountInspectionResultItem,
  t: ReturnType<typeof useTranslation>['t']
) => (
  <div className={styles.confirmationAccountCard}>
    <div>
      <span>{item.fileName}</span>
      <small>{item.provider}</small>
    </div>
    <strong>{item.disabled ? t('monitoring.account_inspection_state_disabled') : t('monitoring.account_inspection_state_enabled')}</strong>
  </div>
);

const buildDeleteConfirmationMessage = (
  item: AccountInspectionResultItem,
  t: ReturnType<typeof useTranslation>['t']
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

const buildRefreshTokenConfirmationMessage = (
  item: AccountInspectionResultItem,
  t: ReturnType<typeof useTranslation>['t']
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

const withChanged = <S, K extends keyof S>(
  state: S,
  key: K,
  next: S[K],
  isEqual: (left: S[K], right: S[K]) => boolean
): S => {
  if (isEqual(next, state[key])) return state;
  return { ...state, [key]: next };
};

const sameProgressSnapshot = (left: AccountInspectionProgressSnapshot, right: AccountInspectionProgressSnapshot) =>
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

const sameInspectionSettings = (left: AccountInspectionConfigurableSettings, right: AccountInspectionConfigurableSettings) =>
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

const sameSettingsDraft = (left: InspectionSettingsDraft, right: InspectionSettingsDraft) =>
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

const sameScheduleDraft = (left: ScheduleDraft, right: ScheduleDraft) =>
  left.enabled === right.enabled && left.intervalMinutes === right.intervalMinutes;

type InspectionScheduleSnapshot = AccountInspectionScheduleResponse['schedule'];

const sameScheduleSnapshot = (
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

const sameAutoExecutionCounts = (left: AutoExecutionCounts, right: AutoExecutionCounts) =>
  left.delete === right.delete && left.disable === right.disable && left.enable === right.enable;

const sameRunStatus = (left: RunStatus, right: RunStatus) => left === right;

const handleAccountInspectionControlError = (
  error: unknown,
  appendLog: (level: AccountInspectionLogLevel, message: string) => void,
  showNotification: (message: string, type?: 'info' | 'success' | 'warning' | 'error') => void,
  fallbackMessage: string
) => {
  const message = error instanceof Error ? error.message : String(error || fallbackMessage);
  appendLog('error', message);
  showNotification(message, 'error');
};

type BackendInspectionViewState = ReturnType<typeof buildAccountInspectionBackendViewState>;

type InspectionBackendState = {
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

type InspectionBackendAction =
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

const createInspectionBackendState = (settings: AccountInspectionConfigurableSettings): InspectionBackendState => ({
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

const applyBackendViewState = (
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

const inspectionBackendReducer = (
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

export function AccountInspectionPage() {
  const { t, i18n } = useTranslation();
  const config = useConfigStore((state) => state.config);
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const claudeQuota = useQuotaStore((state) => state.claudeQuota);
  const codexQuota = useQuotaStore((state) => state.codexQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);

  const [backendState, dispatchBackendState] = useReducer(
    inspectionBackendReducer,
    config,
    (initialConfig) => createInspectionBackendState(loadAccountInspectionConfigurableSettings(initialConfig))
  );
  const {
    inspectionSettings,
    settingsDraft,
    scheduleDraft,
    schedule,
    logs,
    logsPage,
    runStatus,
    progress,
    result,
    autoExecutionCounts,
    restoredSnapshot,
  } = backendState;
  const [isSettingsModalOpen, setIsSettingsModalOpen] = useState(false);
  const [selectedErrorResult, setSelectedErrorResult] = useState<AccountInspectionResultItem | null>(null);
  const [scheduleLoading, setScheduleLoading] = useState(false);
  const [logsCollapsed, setLogsCollapsed] = useState(false);
  const [resultFilter, setResultFilter] = useState<ResultFilter>('accountInvalid');
  const [selectedResultProvider, setSelectedResultProvider] = useState<string>(ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE);
  const [resultPage, setResultPage] = useState(1);
  const [logLevelFilter, setLogLevelFilter] = useState<AccountInspectionLogLevel | 'all'>('all');
  const [logPage, setLogPage] = useState(1);
  const [authFiles, setAuthFiles] = useState<AuthFileItem[]>([]);
  const [authFilesLoaded, setAuthFilesLoaded] = useState(false);
  const [authFileStats, setAuthFileStats] = useState<AuthFileAccountStats>(() => createEmptyAuthFileAccountStats());
  const [authFileStatsReady, setAuthFileStatsReady] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [loadingFullInspectionDetails, setLoadingFullInspectionDetails] = useState(false);
  const [recheckingKey, setRecheckingKey] = useState<string | null>(null);
  const [refreshingTokenKey, setRefreshingTokenKey] = useState<string | null>(null);
  const [exportingAuthFiles, setExportingAuthFiles] = useState(false);
  const [selectedAssetProvider, setSelectedAssetProvider] = useState<string>('all');
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => getDocumentTheme());
  const logListRef = useRef<HTMLDivElement | null>(null);
  const resultsPanelRef = useRef<HTMLDivElement | null>(null);
  const settingsMainRef = useRef<HTMLDivElement | null>(null);
  const settingsSectionRefs = useRef<Record<SettingsSectionKey, HTMLElement | null>>({
    plan: null,
    scope: null,
    runtime: null,
    antigravity: null,
    auto: null,
  });
  const refreshedBackendFinishedAtRef = useRef(0);
  const loadedBackendDetailsKeyRef = useRef('');
  const backendScheduleRequestIdRef = useRef(0);


  useEffect(() => {
    const root = document.documentElement;
    const observer = new MutationObserver(() => setResolvedTheme(getDocumentTheme()));
    observer.observe(root, { attributes: true, attributeFilter: ['class', 'data-theme'] });
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    dispatchBackendState({
      type: 'configChanged',
      settings: loadAccountInspectionConfigurableSettings(config),
      syncDraft: !isSettingsModalOpen,
    });
  }, [config, isSettingsModalOpen]);

  const loadAuthFiles = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      setAuthFiles([]);
      setAuthFilesLoaded(false);
      return;
    }

    try {
      const response = await authFilesApi.list();
      const files = Array.isArray(response.files) ? response.files : [];
      startTransition(() => {
        setAuthFiles(files);
        setAuthFilesLoaded(true);
      });
    } catch {
      setAuthFiles([]);
      setAuthFilesLoaded(false);
    }
  }, [connectionStatus]);

  useEffect(() => {
    if (connectionStatus !== 'connected') {
      void loadAuthFiles();
      return;
    }

    let cancelled = false;
    const loadWhenReady = () => {
      if (!cancelled) void loadAuthFiles();
    };
    const windowWithIdleCallback = window as Window & {
      requestIdleCallback?: (callback: AccountInspectionIdleCallback, options?: { timeout?: number }) => number;
      cancelIdleCallback?: (handle: number) => void;
    };
    if (windowWithIdleCallback.requestIdleCallback) {
      const idleHandle = windowWithIdleCallback.requestIdleCallback(loadWhenReady, {
        timeout: 1200,
      });
      return () => {
        cancelled = true;
        windowWithIdleCallback.cancelIdleCallback?.(idleHandle);
      };
    }

    const timeoutId = window.setTimeout(loadWhenReady, ACCOUNT_INSPECTION_AUTH_FILES_IDLE_DELAY_MS);
    return () => {
      cancelled = true;
      window.clearTimeout(timeoutId);
    };
  }, [connectionStatus, loadAuthFiles]);

  const applyBackendResponse = useCallback((response: AccountInspectionScheduleResponse, deferred = false) => {
    const applyState = () => dispatchBackendState({ type: 'backendResponseReceived', response });
    if (deferred) {
      startTransition(applyState);
    } else {
      applyState();
    }

    if (
      response.status.state !== 'running' &&
      response.status.state !== 'paused' &&
      response.status.state !== 'stopping' &&
      response.status.lastFinishedAt > 0 &&
      refreshedBackendFinishedAtRef.current !== response.status.lastFinishedAt
    ) {
      refreshedBackendFinishedAtRef.current = response.status.lastFinishedAt;
      quotaPersistenceMiddleware.markStale();
      void Promise.all([
        loadAuthFiles(),
        quotaPersistenceMiddleware.ensureFresh(),
      ]);
    }
  }, [loadAuthFiles]);

  const loadInspectionDetailsPage = useCallback(async () => {
    const response = await accountInspectionApi.getStatus({
      includeDetails: true,
      resultPage,
      resultPageSize: ACCOUNT_INSPECTION_RESULT_PAGE_SIZE,
      resultFilter,
      resultProvider: selectedResultProvider,
      logPage,
      logPageSize: ACCOUNT_INSPECTION_LOG_PAGE_SIZE,
      logLevel: logLevelFilter,
    });
    applyBackendResponse(response, true);
    return response;
  }, [applyBackendResponse, logLevelFilter, logPage, resultFilter, resultPage, selectedResultProvider]);

  const currentInspectionDetailOptions = useMemo(() => ({
    includeDetails: true,
    resultPage,
    resultPageSize: ACCOUNT_INSPECTION_RESULT_PAGE_SIZE,
    resultFilter,
    resultProvider: selectedResultProvider,
    logPage,
    logPageSize: ACCOUNT_INSPECTION_LOG_PAGE_SIZE,
    logLevel: logLevelFilter,
  }), [logLevelFilter, logPage, resultFilter, resultPage, selectedResultProvider]);

  const loadBackendSchedule = useCallback(async () => {
    const requestId = backendScheduleRequestIdRef.current + 1;
    backendScheduleRequestIdRef.current = requestId;
    if (connectionStatus !== 'connected') {
      dispatchBackendState({ type: 'clearSchedule' });
      return;
    }
    try {
      const response = await accountInspectionApi.getStatus();
      if (backendScheduleRequestIdRef.current !== requestId) return;
      applyBackendResponse(response);
    } catch {
      // Preserve the last server snapshot across transient transport failures.
    }
  }, [applyBackendResponse, connectionStatus]);

  useEffect(() => {
    void loadBackendSchedule();
  }, [loadBackendSchedule]);

  useEffect(() => {
    if (connectionStatus !== 'connected' || !apiBase || !managementKey) return;
    let closed = false;
    let socket: WebSocket | null = null;
    let reconnectTimer: number | null = null;
    let pollTimer: number | null = null;
    let reconnectDelay = 1000;

    const stopPolling = () => {
      if (pollTimer !== null) {
        window.clearInterval(pollTimer);
        pollTimer = null;
      }
    };
    const startPolling = () => {
      if (closed || pollTimer !== null) return;
      void loadBackendSchedule();
      pollTimer = window.setInterval(() => void loadBackendSchedule(), 5000);
    };
    const scheduleReconnect = () => {
      if (closed || reconnectTimer !== null) return;
      const delay = reconnectDelay;
      reconnectDelay = nextAccountInspectionReconnectDelay(reconnectDelay);
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = null;
        connect();
      }, delay);
    };
    const connect = () => {
      if (closed) return;
      try {
        socket = new WebSocket(
          buildAccountInspectionLogsWebSocketUrl(apiBase),
          accountInspectionWebSocketProtocol(managementKey)
        );
      } catch {
        startPolling();
        scheduleReconnect();
        return;
      }

      socket.onopen = () => {
        if (closed) return;
        reconnectDelay = 1000;
        stopPolling();
        void loadBackendSchedule();
      };
      socket.onmessage = (event) => {
        if (closed || typeof event.data !== 'string') return;
        try {
          const message = JSON.parse(event.data) as AccountInspectionLogStreamMessage;
          if (message.log) {
            dispatchBackendState({
              type: 'appendLog',
              level: message.log.level,
              message: message.log.message,
              timestamp: message.log.time,
            });
            if (message.type === 'log') {
              return;
            }
          }
          applyBackendResponse({
            schedule: message.schedule,
            status: message.status,
          });
        } catch {
          return;
        }
      };
      socket.onerror = () => socket?.close();
      socket.onclose = () => {
        socket = null;
        if (closed) return;
        startPolling();
        scheduleReconnect();
      };
    };

    connect();

    return () => {
      closed = true;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      stopPolling();
      socket?.close();
    };
  }, [apiBase, applyBackendResponse, connectionStatus, loadBackendSchedule, managementKey]);

  useEffect(() => {
    if (connectionStatus !== 'connected') return;
    if (progress.status === 'running' || progress.status === 'paused') return;
    if (progress.startedAt <= 0 || (progress.total <= 0 && progress.summary.totalFiles <= 0)) return;
    const detailKey = [
      progress.startedAt,
      resultFilter,
      selectedResultProvider,
      resultPage,
      logLevelFilter,
      logPage,
    ].join(':');
    if (loadedBackendDetailsKeyRef.current === detailKey) return;

    let cancelled = false;
    const loadDetails = async () => {
      try {
        await loadInspectionDetailsPage();
        if (cancelled) return;
        loadedBackendDetailsKeyRef.current = detailKey;
      } catch {
        // Keep the detail fetch retryable; a transient miss should not leave the table empty.
      }
    };
    const windowWithIdleCallback = window as Window & {
      requestIdleCallback?: (callback: AccountInspectionIdleCallback, options?: { timeout?: number }) => number;
      cancelIdleCallback?: (handle: number) => void;
    };
    if (windowWithIdleCallback.requestIdleCallback) {
      const idleHandle = windowWithIdleCallback.requestIdleCallback(() => void loadDetails(), {
        timeout: 1500,
      });
      return () => {
        cancelled = true;
        windowWithIdleCallback.cancelIdleCallback?.(idleHandle);
      };
    }
    const timeoutId = window.setTimeout(() => void loadDetails(), ACCOUNT_INSPECTION_DETAILS_IDLE_DELAY_MS);
    return () => {
      cancelled = true;
      window.clearTimeout(timeoutId);
    };
  }, [
    connectionStatus,
    loadInspectionDetailsPage,
    logLevelFilter,
    logPage,
    progress.startedAt,
    progress.status,
    progress.summary.totalFiles,
    progress.total,
    resultFilter,
    resultPage,
    selectedResultProvider,
  ]);

  useEffect(() => {
    setResultPage(1);
  }, [resultFilter, selectedResultProvider]);

  useEffect(() => {
    setLogPage(1);
  }, [logLevelFilter]);

  const appendLog = useCallback((level: AccountInspectionLogLevel, message: string) => {
    dispatchBackendState({ type: 'appendLog', level, message, timestamp: Date.now() });
  }, []);

  useEffect(() => {
    if (logsCollapsed) return;
    const element = logListRef.current;
    if (!element) return;
    element.scrollTop = element.scrollHeight;
  }, [logs, logsCollapsed]);

  const startFreshInspection = useCallback(
    async (preserveLogs: boolean = false, introMessage: string = '') => {
      if (connectionStatus !== 'connected') {
        const message = t('notification.connection_required');
        showNotification(message, 'warning');
        return;
      }

      if (!preserveLogs) {
        dispatchBackendState({ type: 'clearLogs' });
      }
      if (introMessage) {
        appendLog('info', introMessage);
      }

      dispatchBackendState({ type: 'startRun', timestamp: Date.now() });
      setLogsCollapsed(false);
      setResultPage(1);
      setLogPage(1);

      try {
        const response = await accountInspectionApi.runNow();
        applyBackendResponse(response);
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
        dispatchBackendState({ type: 'runFailed' });
        setLogsCollapsed(false);
      }
    },
    [appendLog, applyBackendResponse, connectionStatus, showNotification, t]
  );

  const handleRunInspection = useCallback(() => {
    if (runStatus === 'paused') {
      setLogsCollapsed(false);
      void accountInspectionApi.resume()
        .then(applyBackendResponse)
        .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')));
      return;
    }

    void startFreshInspection(false);
  }, [appendLog, applyBackendResponse, runStatus, showNotification, startFreshInspection, t]);

  const handleExportAuthFiles = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }

    setExportingAuthFiles(true);
    try {
      const response = await authFilesApi.list();
      const files = Array.isArray(response.files)
        ? response.files.filter((file) =>
            isInspectableAccountInspectionAuthFile(file) &&
            (selectedAssetProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE ||
              resolveAuthProvider(file) === selectedAssetProvider)
          )
        : [];
      const downloadableFiles = files.filter((file) => typeof file.name === 'string' && file.name.trim());
      const entries = await mapWithConcurrency(
        downloadableFiles,
        ACCOUNT_INSPECTION_EXPORT_DOWNLOAD_CONCURRENCY,
        async (file) => {
          const name = file.name.trim();
          const downloadResponse = await apiClient.getRaw(`/auth-files/download?name=${encodeURIComponent(name)}`, {
            responseType: 'blob',
          });
          const blob = downloadResponse.data instanceof Blob
            ? downloadResponse.data
            : new Blob([downloadResponse.data], { type: 'application/json' });
          return {
            name,
            content: await blob.text(),
          };
        }
      );

      if (entries.length === 0) {
        showNotification(t('monitoring.account_inspection_auth_files_export_empty'), 'info');
        return;
      }

      const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
      const archive = await buildZipArchive(entries);
      const providerSuffix = selectedAssetProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
        ? ''
        : `-${selectedAssetProvider}`;
      downloadBlobFile(`auth-files-export${providerSuffix}-${timestamp}.zip`, archive);
      showNotification(t('monitoring.account_inspection_auth_files_export_success', { count: entries.length }), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setExportingAuthFiles(false);
    }
  }, [connectionStatus, selectedAssetProvider, showNotification, t]);

  const handlePauseInspection = useCallback(() => {
    if (runStatus !== 'running') return;
    void accountInspectionApi.pause()
      .then(applyBackendResponse)
      .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')));
  }, [appendLog, applyBackendResponse, runStatus, showNotification, t]);

  const handleStopInspection = useCallback(() => {
    void accountInspectionApi.stop()
      .then((response) => {
        appendLog('warning', t('monitoring.account_inspection_stopped'));
        applyBackendResponse(response);
        setLogsCollapsed(false);
        dispatchBackendState({ type: 'clearAutoExecutionCounts' });
      })
      .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')));
  }, [appendLog, applyBackendResponse, showNotification, t]);

  const executeItems = useCallback(
    async (items: AccountInspectionResultItem[]) => {
      if (restoredSnapshot) {
        showNotification(t('monitoring.account_inspection_restored_snapshot_action_blocked'), 'warning');
        return;
      }
      const currentResult = result;
      if (!currentResult) return;
      const targets = items.filter(isSuggestedAction);
      const actionItems = targets.flatMap((item) => {
        if (item.action === 'keep') return [];
        return [{
          ...toAccountInspectionApiItem(item),
          action: item.action,
        }];
      });
      if (actionItems.length === 0) {
        showNotification(t('monitoring.account_inspection_no_pending_actions'), 'info');
        return;
      }

      setExecuting(true);
      setLogsCollapsed(false);
      appendLog('info', t('monitoring.account_inspection_execute_started'));

      try {
        const response = await accountInspectionApi.executeActions(actionItems, currentInspectionDetailOptions);
        const failed = response.outcomes.filter((item) => !item.success);
        if (failed.length > 0) {
          showNotification(
            `${t('monitoring.account_inspection_execute_partial')}: ${failed
              .slice(0, 2)
              .map((item) => buildExecutionFailureMessage({
                action: item.action,
                fileName: item.fileName,
                displayAccount: item.displayName,
                email: item.email,
                name: item.name,
                provider: item.provider,
                authIndex: item.authIndex || null,
                success: item.success,
                error: item.error,
              }))
              .join('；')}`,
            'warning'
          );
        } else {
          showNotification(t('monitoring.account_inspection_execute_success'), 'success');
        }
        applyBackendResponse(response);
        void loadAuthFiles();
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
      } finally {
        setExecuting(false);
      }
    },
    [appendLog, applyBackendResponse, currentInspectionDetailOptions, loadAuthFiles, restoredSnapshot, result, showNotification, t]
  );

  const allResults = useMemo(
    () => (result ? result.results : []),
    [result]
  );

  const resultsViewState = useMemo(
    () => buildInspectionResultsViewState(allResults),
    [allResults]
  );

  const {
    healthCounts,
    actionableActionCounts,
    filterRows,
  } = resultsViewState;
  const displayedHealthCounts = result?.healthCounts ?? healthCounts;

  const hasAutoExecutionPolicy = hasAccountInspectionAutoExecutePolicies(inspectionSettings);

  const filteredResultRows = useMemo(() => {
    return filterRows[resultFilter];
  }, [filterRows, resultFilter]);
  const resultPageInfo = result?.resultsPage ?? null;
  const visibleResultRows = filteredResultRows;

  const filteredLogs = useMemo(
    () => (logLevelFilter === 'all' ? logs : logs.filter((entry) => entry.level === logLevelFilter)),
    [logLevelFilter, logs]
  );
  const logPageInfo = logsPage;
  const visibleLogs = filteredLogs;
  const resultPagination = getPaginationRange(resultPageInfo, visibleResultRows.length);
  const logPagination = getPaginationRange(logPageInfo, visibleLogs.length);

  const handleExecutePlanned = useCallback(() => {
    if (!result) return;

    const confirmTargets = (targets: AccountInspectionResultItem[]) => {
      const counts = countActions(targets);
      showConfirmation({
        title: t('monitoring.account_inspection_execute_confirm_title'),
        message: buildExecuteConfirmationMessage(
          targets,
          t,
          hasAccountInspectionAutoExecutePolicies(inspectionSettings)
        ),
        confirmText: t('monitoring.account_inspection_execute_confirm_button', {
          count: targets.length,
        }),
        cancelText: t('common.cancel'),
        variant: counts.delete > 0 ? 'danger' : 'primary',
        onConfirm: () => executeItems(targets),
      });
    };

    const loadPendingTargets = async () => {
      const targets: AccountInspectionResultItem[] = [];
      let page = 1;
      for (;;) {
        const response = await accountInspectionApi.getStatus({
          includeDetails: true,
          resultFilter: 'pending',
          resultProvider: selectedResultProvider,
          resultPage: page,
          resultPageSize: ACCOUNT_INSPECTION_ACTION_PAGE_SIZE,
          logPage: 1,
          logPageSize: 1,
        });
        const pageResult = buildAccountInspectionBackendViewState(response).result;
        targets.push(...collectActionableInspectionResults(pageResult?.results ?? []));
        const pageInfo = response.status.resultsPage;
        if (!pageInfo?.hasMore) break;
        page += 1;
      }
      return targets;
    };

    setLoadingFullInspectionDetails(true);
    void loadPendingTargets()
      .then((targets) => {
        const counts = countActions(targets);
        if (counts.delete + counts.disable + counts.enable <= 0) {
          showNotification(t('monitoring.account_inspection_no_pending_actions'), 'info');
          return;
        }
        confirmTargets(targets);
      })
      .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')))
      .finally(() => setLoadingFullInspectionDetails(false));
  }, [appendLog, executeItems, inspectionSettings, result, selectedResultProvider, showConfirmation, showNotification, t]);

  const handleExecuteSingle = useCallback(
    (item: AccountInspectionResultItem, manualAction?: ManualAccountInspectionAction) => {
      const target = manualAction ? buildManualActionItem(item, manualAction) : item;
      const actionLabel = formatActionLabel(target.action, t);
      const isDelete = target.action === 'delete';
      showConfirmation({
        title: isDelete
          ? t('monitoring.account_inspection_delete_single_title')
          : t('monitoring.account_inspection_execute_single_title'),
        message: isDelete
          ? buildDeleteConfirmationMessage(target, t)
          : buildExecuteConfirmationMessage(
            [target],
            t,
            hasAccountInspectionAutoExecutePolicies(inspectionSettings)
          ),
        confirmText: actionLabel,
        cancelText: t('common.cancel'),
        variant: isDelete ? 'danger' : 'primary',
        onConfirm: () => executeItems([target]),
      });
    },
    [executeItems, inspectionSettings, showConfirmation, t]
  );

  const handleRecheckSingle = useCallback(
    async (item: AccountInspectionResultItem) => {
      if (restoredSnapshot) {
        showNotification(t('monitoring.account_inspection_restored_snapshot_action_blocked'), 'warning');
        return;
      }
      if (connectionStatus !== 'connected') {
        showNotification(t('notification.connection_required'), 'warning');
        return;
      }
      setRecheckingKey(item.key);
      setLogsCollapsed(false);
      appendLog('info', t('monitoring.account_inspection_recheck_started', {
        account: item.fileName,
      }));
      try {
        const response = await accountInspectionApi.inspectOne(toAccountInspectionApiItem(item), currentInspectionDetailOptions);
        applyBackendResponse(response);
        if (response.result?.key) {
          const toast = formatInspectionResultToast(accountInspectionBackendResultToItem(response.result), t);
          showNotification(toast.message, toast.tone);
        } else if (response.error) {
          showNotification(response.error, 'error');
        }
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
      } finally {
        setRecheckingKey(null);
      }
    },
    [appendLog, applyBackendResponse, connectionStatus, currentInspectionDetailOptions, restoredSnapshot, showNotification, t]
  );

  const refreshTokenSingle = useCallback(
    async (item: AccountInspectionResultItem) => {
      if (restoredSnapshot) {
        showNotification(t('monitoring.account_inspection_restored_snapshot_action_blocked'), 'warning');
        return;
      }
      setRefreshingTokenKey(item.key);
      setLogsCollapsed(false);
      appendLog('info', t('monitoring.account_inspection_refresh_token_started', {
        account: item.fileName,
      }));
      try {
        const response = await accountInspectionApi.refreshToken(toAccountInspectionApiItem(item), currentInspectionDetailOptions);
        applyBackendResponse(response);
        const refreshedItem = response.result?.key ? accountInspectionBackendResultToItem(response.result) : null;
        const toast = refreshedItem
          ? formatTokenRefreshToast(refreshedItem, response.error, i18n.language, t)
          : { message: response.error || t('common.unknown_error'), tone: 'error' as const };
        showNotification(toast.message, toast.tone);
        void loadAuthFiles();
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
      } finally {
        setRefreshingTokenKey(null);
      }
    },
    [appendLog, applyBackendResponse, currentInspectionDetailOptions, i18n.language, loadAuthFiles, restoredSnapshot, showNotification, t]
  );

  const handleRefreshTokenSingle = useCallback(
    (item: AccountInspectionResultItem) => {
      if (connectionStatus !== 'connected') {
        showNotification(t('notification.connection_required'), 'warning');
        return;
      }
      showConfirmation({
        title: t('monitoring.account_inspection_refresh_token_confirm_title'),
        message: buildRefreshTokenConfirmationMessage(item, t),
        confirmText: t('monitoring.account_inspection_refresh_token_action'),
        cancelText: t('common.cancel'),
        variant: 'primary',
        onConfirm: () => void refreshTokenSingle(item),
      });
    },
    [connectionStatus, refreshTokenSingle, showConfirmation, showNotification, t]
  );



  const quotaStore = useMemo(
    () => ({ antigravityQuota, claudeQuota, codexQuota, kimiQuota, xaiQuota }),
    [antigravityQuota, claudeQuota, codexQuota, kimiQuota, xaiQuota]
  );

  useEffect(() => {
    if (!authFilesLoaded) {
      setAuthFileStats(createEmptyAuthFileAccountStats());
      setAuthFileStatsReady(false);
      return;
    }

    setAuthFileStatsReady(false);
    const job: AuthFileAccountStatsJob = { cancelled: false, handle: null, cancelHandle: null };
    scheduleAuthFileAccountStats(
      job,
      authFiles,
      quotaStore,
      inspectionSettings.usedPercentThreshold,
      inspectionSettings.antigravityQuotaMode,
      (nextStats) => {
        setAuthFileStats(nextStats);
        setAuthFileStatsReady(true);
      }
    );

    return () => {
      job.cancelled = true;
      if (job.handle !== null) job.cancelHandle?.(job.handle);
    };
  }, [authFiles, authFilesLoaded, inspectionSettings.antigravityQuotaMode, inspectionSettings.usedPercentThreshold, quotaStore]);

  useEffect(() => {
    if (!authFileStatsReady) return;
    if (selectedAssetProvider === 'all') return;
    if (authFileStats.providers.some((provider) => provider.provider === selectedAssetProvider)) return;
    setSelectedAssetProvider('all');
  }, [authFileStats.providers, authFileStatsReady, selectedAssetProvider]);

  const selectedAssetStats = useMemo<AuthFileAccountStats | ProviderAccountStats>(() => {
    if (selectedAssetProvider === 'all') return authFileStats;
    return authFileStats.providers.find((provider) => provider.provider === selectedAssetProvider) ?? authFileStats;
  }, [authFileStats, selectedAssetProvider]);

  const selectedAssetLabel = selectedAssetProvider === 'all'
    ? t('monitoring.filter_all_accounts')
    : resolveProviderDisplayLabel(selectedAssetProvider);
  const exportAuthFilesLabel = selectedAssetProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
    ? t('monitoring.account_inspection_auth_files_export_all')
    : t('monitoring.account_inspection_auth_files_export_selected', { provider: selectedAssetLabel });

  const accountAssetCards = useMemo<SummaryCard[]>(() => [
    {
      key: 'total',
      label: t('monitoring.account_inspection_account_total'),
      value: authFileStatsReady ? String(selectedAssetStats.total) : '--',
      description: selectedAssetLabel,
    },
    {
      key: 'enabled',
      label: t('monitoring.account_inspection_account_enabled'),
      value: authFileStatsReady ? String(selectedAssetStats.enabled) : '--',
      description: t('monitoring.account_inspection_inventory_health'),
      tone: authFileStatsReady && selectedAssetStats.enabled > 0 ? 'good' : 'neutral',
    },
    {
      key: 'disabled',
      label: t('monitoring.account_inspection_account_disabled'),
      value: authFileStatsReady ? String(selectedAssetStats.disabled) : '--',
      description: t('monitoring.account_inspection_blast_radius'),
      tone: authFileStatsReady && selectedAssetStats.disabled > 0 ? 'warn' : 'neutral',
    },
    {
      key: 'quotaLow',
      label: t('monitoring.account_inspection_account_quota_low'),
      value: authFileStatsReady ? String(selectedAssetStats.quotaLow) : '--',
      description: t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_label'),
      tone: authFileStatsReady && selectedAssetStats.quotaLow > 0 ? 'bad' : 'neutral',
    },
    {
      key: 'accountInvalid',
      label: t('monitoring.account_inspection_account_invalid'),
      value: authFileStatsReady ? String(selectedAssetStats.accountInvalid) : '--',
      description: t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_label'),
      tone: authFileStatsReady && selectedAssetStats.accountInvalid > 0 ? 'bad' : 'neutral',
    },
    {
      key: 'requestError',
      label: t('monitoring.account_inspection_account_request_error'),
      value: authFileStatsReady ? String(selectedAssetStats.requestError) : '--',
      description: t('monitoring.account_inspection_settings_auto_execute_request_error_action_label'),
      tone: authFileStatsReady && selectedAssetStats.requestError > 0 ? 'bad' : 'neutral',
    },
  ], [authFileStatsReady, selectedAssetLabel, selectedAssetStats, t]);

  const actionStats = useMemo(() => {
    const autoTotal = autoExecutionCounts.delete + autoExecutionCounts.disable + autoExecutionCounts.enable;
    const manualDelete = result?.summary.deleteCount ?? actionableActionCounts.delete;
    const manualDisable = result?.summary.disableCount ?? actionableActionCounts.disable;
    const manualEnable = result?.summary.enableCount ?? actionableActionCounts.enable;
    const manualTotal = manualDelete + manualDisable + manualEnable;
    return {
      autoTotal,
      manualTotal,
      autoDelete: autoExecutionCounts.delete,
      autoDisable: autoExecutionCounts.disable,
      autoEnable: autoExecutionCounts.enable,
      manualDelete,
      manualDisable,
      manualEnable,
      keep: result?.summary.keepCount ?? 0,
      error: result?.summary.errorCount ?? 0,
    };
  }, [actionableActionCounts, autoExecutionCounts, result]);

  const pendingActionCount = actionStats.manualTotal;
  const inspectionScopeLabel = inspectionSettings.targetType === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
    ? t('monitoring.filter_all_providers')
    : resolveProviderDisplayLabel(inspectionSettings.targetType);
  const settingEnabledLabel = t('monitoring.account_inspection_setting_enabled');
  const settingDisabledLabel = t('monitoring.account_inspection_setting_disabled');
  const quotaLimitAutoLabel = inspectionSettings.autoExecuteQuotaLimitDisable ? settingEnabledLabel : settingDisabledLabel;
  const quotaRecoveryAutoLabel = inspectionSettings.autoExecuteQuotaRecoveryEnable ? settingEnabledLabel : settingDisabledLabel;
  const accountInvalidActionLabel = t(
    AUTO_ERROR_ACTION_OPTIONS.find((option) => option.value === inspectionSettings.autoExecuteAccountInvalidAction)?.labelKey
      ?? 'monitoring.account_inspection_settings_account_error_action_none'
  );
  const requestErrorActionLabel = t(
    AUTO_ERROR_ACTION_OPTIONS.find((option) => option.value === inspectionSettings.autoExecuteRequestErrorAction)?.labelKey
      ?? 'monitoring.account_inspection_settings_account_error_action_none'
  );
  const scheduleStatusLabel = schedule?.enabled
    ? formatInspectionInterval(schedule.intervalMinutes, i18n.language)
    : settingDisabledLabel;
  const autoExecutionResultLabel = !result
    ? t('monitoring.account_inspection_auto_execute_pending')
    : actionStats.autoTotal > 0
      ? [
          `${t('monitoring.account_inspection_action_enable')}: ${actionStats.autoEnable}`,
          `${t('monitoring.account_inspection_action_disable')}: ${actionStats.autoDisable}`,
          `${t('monitoring.account_inspection_action_delete')}: ${actionStats.autoDelete}`,
        ].join(' · ')
      : t('monitoring.account_inspection_auto_execute_no_actions');
  const showInspectionResults = useCallback((filter: ResultFilter) => {
    setResultFilter(filter);
    requestAnimationFrame(() => resultsPanelRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }));
  }, []);

  const operationPhase = useMemo(() => {
    if (executing || loadingFullInspectionDetails) return t('monitoring.account_inspection_phase_executing');
    if (runStatus === 'paused') return t('monitoring.account_inspection_phase_paused');
    if (runStatus === 'running') {
      if (progress.completed <= 0 && progress.inFlight <= 0) {
        return t('monitoring.account_inspection_phase_initializing');
      }
      return t('monitoring.account_inspection_phase_probing');
    }
    if (runStatus === 'error') return t('monitoring.account_inspection_phase_failed');
    if (result && pendingActionCount > 0) return t('monitoring.account_inspection_phase_review');
    if (result) return t('monitoring.account_inspection_phase_completed');
    return t('monitoring.account_inspection_phase_idle');
  }, [executing, loadingFullInspectionDetails, pendingActionCount, progress.completed, progress.inFlight, result, runStatus, t]);

  const resultEmptyMessage = runStatus === 'running'
    ? t('monitoring.account_inspection_results_generating')
    : runStatus === 'error'
      ? t('monitoring.account_inspection_results_error_empty')
      : t('monitoring.account_inspection_empty');
  const resultFilterTabs = useMemo<Array<{ key: ResultFilter; label: string }>>(() => [
    { key: 'pending', label: t('monitoring.account_inspection_filter_pending') },
    { key: 'accountInvalid', label: t('monitoring.account_inspection_account_invalid') },
    { key: 'requestError', label: t('monitoring.account_inspection_account_request_error') },
    { key: 'quotaExhausted', label: t('monitoring.account_inspection_health_quota_exhausted') },
    { key: 'recoverable', label: t('monitoring.account_inspection_health_recoverable') },
    { key: 'highAvailable', label: t('monitoring.account_inspection_high_available') },
  ], [t]);
  const resultProviderOptions = useMemo(
    () => {
      const providers = new Set<string>();
      authFileStats.providers.forEach((provider) => providers.add(provider.provider));
      result?.results.forEach((item) => {
        const provider = item.provider.trim().toLowerCase();
        if (ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET.has(provider)) providers.add(provider);
      });
      return [
        { value: ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE, label: t('monitoring.filter_all_providers') },
        ...Array.from(providers).map((provider) => ({
          value: provider,
          label: resolveProviderDisplayLabel(provider),
        })),
      ];
    },
    [authFileStats.providers, result, t]
  );

  useEffect(() => {
    if (selectedResultProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE) return;
    if (resultProviderOptions.some((option) => option.value === selectedResultProvider)) return;
    setSelectedResultProvider(ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE);
  }, [resultProviderOptions, selectedResultProvider]);
  const logLevelOptions = useMemo<Array<{ key: AccountInspectionLogLevel | 'all'; label: string }>>(() => [
    { key: 'all', label: t('monitoring.account_inspection_filter_all') },
    { key: 'success', label: t('monitoring.account_inspection_log_success') },
    { key: 'warning', label: t('monitoring.account_inspection_log_warning') },
    { key: 'error', label: t('monitoring.account_inspection_log_error') },
  ], [t]);
  const progressLabel =
    progress.total > 0
      ? t('monitoring.account_inspection_progress_status', {
          completed: progress.completed,
          total: progress.total,
          inFlight: progress.inFlight,
          pending: progress.pending,
          percent: progress.percent,
        })
      : t('monitoring.account_inspection_progress_idle');
  const setSettingsSectionRef = useCallback(
    (section: SettingsSectionKey) => (element: HTMLElement | null) => {
      settingsSectionRefs.current[section] = element;
    },
    []
  );

  const scrollToSettingsSection = useCallback((section: SettingsSectionKey) => {
    const container = settingsMainRef.current;
    const target = settingsSectionRefs.current[section];
    if (!container || !target) return;
    container.scrollTo({
      top: target.offsetTop - container.offsetTop,
      behavior: 'smooth',
    });
  }, []);

  const openSettingsModal = useCallback(() => {
    dispatchBackendState({ type: 'setSettingsDraft', draft: toSettingsDraft(inspectionSettings) });
    setIsSettingsModalOpen(true);
  }, [inspectionSettings]);

  const handleSettingsDraftChange = useCallback(
    (field: InspectionSettingsDraftField, value: string) => {
      dispatchBackendState({
        type: 'updateSettingsDraft',
        values: { [field]: value },
      });
    },
    []
  );

  const handleAntigravityDeepProbeChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { antigravityDeepProbeEnabled: value },
    });
  }, []);

  const handleXAIDeepProbeChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { xaiDeepProbeEnabled: value },
    });
  }, []);

  const handleAntigravityQuotaModeChange = useCallback((value: string) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { antigravityQuotaMode: normalizeAntigravityQuotaMode(value) },
    });
  }, []);

  const handleAutoExecuteQuotaLimitChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { autoExecuteQuotaLimitDisable: value },
    });
  }, []);

  const handleAutoExecuteQuotaRecoveryChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { autoExecuteQuotaRecoveryEnable: value },
    });
  }, []);

  const handleAutoExecuteAccountInvalidActionChange = useCallback((value: string) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: {
        autoExecuteAccountInvalidAction: normalizeAutoErrorAction(value),
      },
    });
  }, []);

  const handleAutoExecuteRequestErrorActionChange = useCallback((value: string) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: {
        autoExecuteRequestErrorAction: normalizeAutoErrorAction(value),
      },
    });
  }, []);

  const parseIntegerInRange = useCallback(
    (value: string, label: string, min: number, max?: number) => {
      const parsed = Number(value.trim());
      if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < min || (max !== undefined && parsed > max)) {
        throw new Error(
          max === undefined
            ? t('monitoring.account_inspection_settings_invalid_integer', { field: label, min })
            : t('monitoring.account_inspection_settings_invalid_integer_range', { field: label, min, max })
        );
      }
      return parsed;
    },
    [t]
  );

  const handleSaveSettings = useCallback(async () => {
    const targetType = settingsDraft.targetType.trim().toLowerCase();
    if (!targetType) {
      showNotification(t('monitoring.account_inspection_settings_target_type_required'), 'error');
      return;
    }

    try {
      const nextSettings = saveAccountInspectionConfigurableSettings({
        targetType,
        workers: parseIntegerInRange(
          settingsDraft.workers,
          t('monitoring.account_inspection_settings_workers_label'),
          WORKER_LIMITS.min,
          WORKER_LIMITS.max
        ),
        deleteWorkers: parseIntegerInRange(
          settingsDraft.deleteWorkers,
          t('monitoring.account_inspection_settings_delete_workers_label'),
          DELETE_WORKER_LIMITS.min,
          DELETE_WORKER_LIMITS.max
        ),
        timeout: parseIntegerInRange(
          settingsDraft.timeout,
          t('monitoring.account_inspection_settings_timeout_label'),
          TIMEOUT_LIMITS.min,
          TIMEOUT_LIMITS.max
        ),
        retries: parseIntegerInRange(
          settingsDraft.retries,
          t('monitoring.account_inspection_settings_retries_label'),
          RETRY_LIMITS.min,
          RETRY_LIMITS.max
        ),
        sampleSize: parseIntegerInRange(
          settingsDraft.sampleSize,
          t('monitoring.account_inspection_settings_sample_size_label'),
          SAMPLE_SIZE_LIMITS.min
        ),
        usedPercentThreshold: (() => {
          const parsed = Number(settingsDraft.usedPercentThreshold.trim());
          if (!Number.isFinite(parsed) || parsed < THRESHOLD_LIMITS.min || parsed > THRESHOLD_LIMITS.max) {
            throw new Error(
              t('monitoring.account_inspection_settings_invalid_threshold', {
                field: t('monitoring.account_inspection_settings_used_percent_threshold_label'),
              })
            );
          }
          return parsed;
        })(),
        antigravityDeepProbeEnabled: settingsDraft.antigravityDeepProbeEnabled,
        antigravityDeepProbeModel: settingsDraft.antigravityDeepProbeModel,
        antigravityQuotaMode: settingsDraft.antigravityQuotaMode,
        xaiDeepProbeEnabled: settingsDraft.xaiDeepProbeEnabled,
        xaiDeepProbeModel: settingsDraft.xaiDeepProbeModel,
        autoExecuteQuotaLimitDisable: settingsDraft.autoExecuteQuotaLimitDisable,
        autoExecuteQuotaRecoveryEnable: settingsDraft.autoExecuteQuotaRecoveryEnable,
        autoExecuteAccountInvalidAction: settingsDraft.autoExecuteAccountInvalidAction,
        autoExecuteRequestErrorAction: settingsDraft.autoExecuteRequestErrorAction,
        autoExecuteConfirmations: inspectionSettings.autoExecuteConfirmations,
      });

      const intervalMinutes = parseIntegerInRange(
        scheduleDraft.intervalMinutes,
        t('monitoring.account_inspection_schedule_interval_label'),
        SCHEDULE_INTERVAL_LIMITS.min
      );
      setScheduleLoading(true);
      const response = await accountInspectionApi.updateSchedule({
        enabled: scheduleDraft.enabled,
        intervalMinutes,
        nextRunAt: scheduleDraft.enabled
          ? (schedule?.nextRunAt ?? 0)
          : 0,
        settings: nextSettings,
      });
      applyBackendResponse(response);
      setIsSettingsModalOpen(false);
      showNotification(t('monitoring.account_inspection_settings_saved'), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setScheduleLoading(false);
    }
  }, [applyBackendResponse, inspectionSettings.autoExecuteConfirmations, parseIntegerInRange, scheduleDraft.enabled, scheduleDraft.intervalMinutes, schedule?.nextRunAt, settingsDraft, showNotification, t]);

  const handleResetSettings = useCallback(() => {
    clearAccountInspectionConfigurableSettings();
    const nextSettings = saveAccountInspectionConfigurableSettings(DEFAULT_ACCOUNT_INSPECTION_SETTINGS);
    dispatchBackendState({ type: 'resetSettings', settings: nextSettings });
    showNotification(t('monitoring.account_inspection_settings_reset'), 'success');
  }, [showNotification, t]);

  const draftInspectionScopeLabel = settingsDraft.targetType === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
    ? t('monitoring.filter_all_providers')
    : resolveProviderDisplayLabel(settingsDraft.targetType);
  const draftScheduleStatusLabel = scheduleDraft.enabled
    ? formatInspectionInterval(Number(scheduleDraft.intervalMinutes) || 0, i18n.language)
    : settingDisabledLabel;
  const draftQuotaModeLabel = t(
    ANTIGRAVITY_QUOTA_MODE_OPTIONS.find((option) => option.value === settingsDraft.antigravityQuotaMode)?.labelKey
      ?? 'monitoring.account_inspection_settings_antigravity_quota_mode_claude_gpt'
  );
  const draftAccountInvalidActionLabel = t(
    AUTO_ERROR_ACTION_OPTIONS.find((option) => option.value === settingsDraft.autoExecuteAccountInvalidAction)?.labelKey
      ?? 'monitoring.account_inspection_settings_account_error_action_none'
  );
  const draftRequestErrorActionLabel = t(
    AUTO_ERROR_ACTION_OPTIONS.find((option) => option.value === settingsDraft.autoExecuteRequestErrorAction)?.labelKey
      ?? 'monitoring.account_inspection_settings_account_error_action_none'
  );
  const draftAutoPolicyLabel = [
    settingsDraft.autoExecuteQuotaLimitDisable ? t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_label') : '',
    settingsDraft.autoExecuteQuotaRecoveryEnable ? t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_label') : '',
    settingsDraft.autoExecuteAccountInvalidAction !== 'none' ? `${t('monitoring.account_inspection_account_invalid')}: ${draftAccountInvalidActionLabel}` : '',
    settingsDraft.autoExecuteRequestErrorAction !== 'none' ? `${t('monitoring.account_inspection_account_request_error')}: ${draftRequestErrorActionLabel}` : '',
  ].filter(Boolean).join(' · ') || settingDisabledLabel;

  return (
    <div className={styles.page}>
      <Card className={styles.heroCard}>
        <div className={styles.heroHeader}>
          <div className={styles.heroCopy}>
            <h1 className={styles.heroTitle}>{t('monitoring.account_inspection_title')}</h1>
            <p className={styles.heroSubtitle}>{t('monitoring.account_inspection_desc')}</p>
          </div>
        </div>

        <div className={styles.assetOverviewGrid}>
          <div className={styles.assetKpiGrid}>
            {accountAssetCards.map((card, index) => (
              <Card
                key={card.key}
                className={[styles.professionalMetricCard, styles[`professionalMetricCard${index + 1}`], summaryToneClass[card.tone ?? 'neutral']]
                  .filter(Boolean)
                  .join(' ')}
              >
                <div className={styles.professionalMetricHeader}>
                  <span className={styles.professionalMetricIcon} aria-hidden="true" />
                  <strong>{card.label}</strong>
                </div>
                <div className={styles.professionalMetricBody}>
                  <span>{card.description}</span>
                  <strong>{card.value}</strong>
                </div>
              </Card>
            ))}
          </div>

          <Card className={styles.providerDistributionCard}>
            <div className={styles.providerDistributionHeader}>
              <div>
                <h3>{t('monitoring.account_inspection_provider_distribution_title')}</h3>
                <p>{authFileStatsReady ? t('monitoring.account_inspection_provider_distribution_desc', { count: authFileStats.providerCount }) : t('common.loading')}</p>
              </div>
              <Button
                variant="secondary"
                size="sm"
                className={styles.providerDistributionSelection}
                onClick={handleExportAuthFiles}
                loading={exportingAuthFiles}
                disabled={exportingAuthFiles || connectionStatus !== 'connected'}
                aria-label={exportAuthFilesLabel}
                title={exportAuthFilesLabel}
              >
                {exportingAuthFiles ? null : <IconDownload size={15} />}
                {exportAuthFilesLabel}
              </Button>
            </div>
            <div className={styles.providerSelectorList}>
              <button
                type="button"
                className={[styles.providerSelectorRow, selectedAssetProvider === 'all' ? styles.providerSelectorRowActive : '']
                  .filter(Boolean)
                  .join(' ')}
                onClick={() => setSelectedAssetProvider('all')}
              >
                <div>
                  <span className={styles.providerSelectorTitle}>
                    <span className={styles.providerLogoFallback} aria-hidden="true">Σ</span>
                    <strong>{t('monitoring.filter_all_accounts')}</strong>
                  </span>
                  <span>{authFileStatsReady ? `${authFileStats.total} ${t('monitoring.account_inspection_account_total')} · ${authFileStats.highAvailable} ${t('monitoring.account_inspection_high_available')}` : t('common.loading')}</span>
                </div>
                <span aria-hidden="true">
                  <i style={buildHighAvailabilityBarStyle(authFileStats.highAvailable, authFileStats.total)} />
                </span>
              </button>
              {authFileStats.providers.length > 0 ? authFileStats.providers.map((provider) => (
                  <button
                    type="button"
                    key={provider.provider}
                    className={[styles.providerSelectorRow, selectedAssetProvider === provider.provider ? styles.providerSelectorRowActive : '']
                      .filter(Boolean)
                      .join(' ')}
                    onClick={() => setSelectedAssetProvider(provider.provider)}
                  >
                    <div>
                      <span className={styles.providerSelectorTitle}>
                        {getAuthFileIcon(provider.provider, resolvedTheme) ? (
                          <img src={getAuthFileIcon(provider.provider, resolvedTheme) ?? ''} alt="" aria-hidden="true" />
                        ) : (
                          <span className={styles.providerLogoFallback} aria-hidden="true">{getProviderInitial(resolveProviderDisplayLabel(provider.provider))}</span>
                        )}
                        <strong>{resolveProviderDisplayLabel(provider.provider)}</strong>
                      </span>
                      <span>{`${provider.total} ${t('monitoring.account_inspection_account_total')} · ${provider.highAvailable} ${t('monitoring.account_inspection_high_available')}`}</span>
                    </div>
                    <span aria-hidden="true">
                      <i style={buildHighAvailabilityBarStyle(provider.highAvailable, provider.total)} />
                    </span>
                  </button>
              )) : <div className={styles.emptyBlockSmall}>{authFileStatsReady ? t('monitoring.account_inspection_empty') : t('common.loading')}</div>}
            </div>
          </Card>
        </div>
      </Card>

      {restoredSnapshot ? (
        <div className={styles.restoredSnapshotNotice} role="status">
          <strong>{t('monitoring.account_inspection_restored_snapshot_title')}</strong>
          <span>{t('monitoring.account_inspection_restored_snapshot_desc')}</span>
        </div>
      ) : null}

      <section className={styles.operationSection}>
        <div className={styles.operationModuleHeader}>
          <div>
            <h2>{t('monitoring.account_inspection_control_title')}</h2>
            <p>{t('monitoring.account_inspection_control_desc')}</p>
          </div>
          <div className={styles.heroActions}>
            <Button
              variant="secondary"
              className={styles.heroActionButton}
              onClick={openSettingsModal}
              disabled={(runStatus === 'running' || runStatus === 'paused') || executing}
            >
              {t('monitoring.account_inspection_settings_button')}
            </Button>
          </div>
        </div>
        <div className={styles.inspectionOperationGrid}>
          <div className={styles.operationMainColumn}>
            <div className={styles.operationMainStack}>
              <Card className={styles.inspectionStatusCard}>
                <div className={styles.inspectionProgressHero}>
                  <div className={styles.progressRing} style={{ '--progress': `${Math.max(0, Math.min(100, progress.percent))}%` } as CSSProperties}>
                    <strong>{`${progress.percent}%`}</strong>
                  </div>
                  <div className={styles.inspectionStatusCopy}>
                    <strong>{operationPhase}</strong>
                    <span>{progress.percent >= 100 ? t('monitoring.account_inspection_phase_completed') : progressLabel}</span>
                    <small>{`${t('monitoring.last_sync')}: ${result?.finishedAt ? formatTimestamp(result.finishedAt, i18n.language) : '--'}`}</small>
                  </div>
                </div>
                <div className={styles.inspectionConfigGrid}>
                  <span>
                    <small>{t('monitoring.account_inspection_detection_scope')}</small>
                    <strong>{inspectionScopeLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_quota_threshold_short')}</small>
                    <strong>{`${inspectionSettings.usedPercentThreshold}%`}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_sample_size')}</small>
                    <strong>{inspectionSettings.sampleSize || t('monitoring.account_inspection_all_accounts')}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_scheduled_inspection_short')}</small>
                    <strong>{scheduleStatusLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_quota_limit_disable_short')}</small>
                    <strong>{quotaLimitAutoLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_quota_recovery_enable_short')}</small>
                    <strong>{quotaRecoveryAutoLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_account_invalid_action_short')}</small>
                    <strong>{accountInvalidActionLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_request_error_action_short')}</small>
                    <strong>{requestErrorActionLabel}</strong>
                  </span>
                </div>
                <div className={styles.inspectionNextRunText}>
                  <span>{t('monitoring.account_inspection_next_execution_short')}</span>
                  <strong>{schedule?.enabled && schedule.nextRunAt ? formatTimestamp(schedule.nextRunAt, i18n.language) : '--'}</strong>
                </div>
              </Card>

              <Card className={styles.inspectionControlCard}>
                <h3>{t('monitoring.account_inspection_control_title')}</h3>
                <div className={styles.inspectionControlActions}>
                  <Button
                    variant="primary"
                    onClick={handleRunInspection}
                    loading={runStatus === 'running'}
                    disabled={runStatus === 'running' || executing || connectionStatus !== 'connected'}
                  >
                    {formatRunInspectionButtonLabel(runStatus, t)}
                  </Button>
                  <Button variant="secondary" onClick={handlePauseInspection} disabled={runStatus !== 'running' || executing}>
                    {t('monitoring.account_inspection_pause')}
                  </Button>
                  <Button variant="danger" onClick={handleStopInspection} disabled={(runStatus !== 'running' && runStatus !== 'paused') || executing}>
                    {t('monitoring.account_inspection_stop')}
                  </Button>
                </div>
              </Card>
            </div>
          </div>

          <Card className={styles.actionStudioCard}>
            <div className={styles.resultOverviewSection}>
              <h3>{t('monitoring.account_inspection_inspection_summary_title')}</h3>
              <div className={styles.resultOverviewGrid}>
                <span>
                  <small>{t('monitoring.account_inspection_result_total')}</small>
                  <strong>{displayedHealthCounts.total}</strong>
                </span>
                <span className={styles.resultOverviewGood}>
                  <small>{t('monitoring.account_inspection_high_available')}</small>
                  <strong>{displayedHealthCounts.healthy}</strong>
                </span>
                <span>
                  <small>{t('monitoring.account_inspection_health_recoverable')}</small>
                  <strong>{displayedHealthCounts.recoverable}</strong>
                </span>
                <span className={styles.resultOverviewWarn}>
                  <small>{t('monitoring.account_inspection_health_quota_exhausted')}</small>
                  <strong>{displayedHealthCounts.quotaExhausted}</strong>
                </span>
                <span className={styles.resultOverviewBad}>
                  <small>{t('monitoring.account_inspection_account_invalid')}</small>
                  <strong>{displayedHealthCounts.authInvalid}</strong>
                </span>
                <span className={styles.resultOverviewBad}>
                  <small>{t('monitoring.account_inspection_account_request_error')}</small>
                  <strong>{displayedHealthCounts.inspectionError}</strong>
                </span>
              </div>
            </div>

            {hasAutoExecutionPolicy ? (
              <div className={styles.strategyResultSection}>
                <div className={styles.strategySectionHeader}>
                  <h3>{t('monitoring.account_inspection_auto_execution_breakdown')}</h3>
                  {result ? <button type="button" onClick={() => showInspectionResults('accountInvalid')}>{t('monitoring.account_inspection_view_results')}</button> : null}
                </div>
                {result ? (
                  <>
                    <div className={styles.strategyTotalsRow}>
                      <span>{`${t('monitoring.account_inspection_action_enable')} ${actionStats.autoEnable}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_disable')} ${actionStats.autoDisable}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_delete')} ${actionStats.autoDelete}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_keep')} ${actionStats.keep}`}</span>
                    </div>
                    <div className={styles.strategyActivityRow}>
                      <span className={styles.strategyActivityIcon} aria-hidden="true" />
                      <span>{formatTimestamp(result.finishedAt, i18n.language)}</span>
                      <strong>{autoExecutionResultLabel}</strong>
                    </div>
                  </>
                ) : (
                  <div className={styles.manualPendingEmpty}>
                    <span aria-hidden="true">•</span>
                    <strong>{t('monitoring.account_inspection_auto_execution_waiting_title')}</strong>
                    <small>{autoExecutionResultLabel}</small>
                  </div>
                )}
              </div>
            ) : (
              <div className={styles.manualPendingSection}>
                <h3>{`${t('monitoring.account_inspection_manual_execution_breakdown')} (${actionStats.manualTotal})`}</h3>
                {actionStats.manualTotal > 0 ? (
                  <div className={styles.manualPendingList}>
                    <span>{`${t('monitoring.account_inspection_action_delete')}: ${actionStats.manualDelete}`}</span>
                    <span>{`${t('monitoring.account_inspection_action_disable')}: ${actionStats.manualDisable}`}</span>
                    <span>{`${t('monitoring.account_inspection_action_enable')}: ${actionStats.manualEnable}`}</span>
                    <div className={styles.manualPendingActions}>
                      <Button
                        variant="primary"
                        size="sm"
                        onClick={handleExecutePlanned}
                        loading={executing || loadingFullInspectionDetails}
                        disabled={restoredSnapshot || !result || runStatus === 'running' || executing || loadingFullInspectionDetails || pendingActionCount === 0}
                      >
                        {executing || loadingFullInspectionDetails ? t('monitoring.account_inspection_executing') : t('monitoring.account_inspection_execute_now')}
                      </Button>
                    </div>
                  </div>
                ) : (
                  <div className={styles.manualPendingEmpty}>
                    <span aria-hidden="true">✓</span>
                    <strong>{t('monitoring.account_inspection_no_pending_actions')}</strong>
                    <small>{t('monitoring.account_inspection_auto_execute_no_actions')}</small>
                  </div>
                )}
              </div>
            )}
          </Card>
        </div>
      </section>

      <div ref={resultsPanelRef} className={styles.resultsSection}>
        <div className={styles.operationModuleHeader}>
          <div>
            <h2>{t('monitoring.account_inspection_results_title')}</h2>
            <p>{t('monitoring.account_inspection_results_desc')}</p>
          </div>
          {result ? (
            <div className={styles.resultToolbar}>
              <Select
                value={selectedResultProvider}
                options={resultProviderOptions}
                onChange={setSelectedResultProvider}
                ariaLabel={t('monitoring.account_inspection_filter_provider')}
                className={styles.resultProviderSelect}
                triggerClassName={styles.resultProviderSelectTrigger}
                dropdownClassName={styles.resultProviderSelectDropdown}
                fullWidth={false}
                size="sm"
              />
              <div className={styles.resultFilterControl}>
                {resultFilterTabs.map((tab) => (
                  <button
                    key={tab.key}
                    type="button"
                    className={[styles.resultFilterButton, resultFilter === tab.key ? styles.resultFilterButtonActive : '']
                      .filter(Boolean)
                      .join(' ')}
                    onClick={() => setResultFilter(tab.key)}
                  >
                    <span>{tab.label}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : null}
        </div>
        <Card className={styles.panel}>

        {result ? (
          <>
            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <colgroup>
                  <col className={styles.accountColumn} />
                  <col className={styles.healthColumn} />
                  <col className={styles.enabledColumn} />
                  <col className={styles.quotaColumn} />
                  <col className={styles.tokenColumn} />
                  <col className={styles.verdictColumn} />
                  <col className={styles.operationColumn} />
                </colgroup>
                <thead>
                  <tr>
                    <th>{t('monitoring.account_label')}</th>
                    <th>{t('monitoring.account_inspection_health_status')}</th>
                    <th>{t('monitoring.account_inspection_enabled_status')}</th>
                    <th>{t('monitoring.account_inspection_remaining_quota')}</th>
                    <th>{t('monitoring.account_inspection_token_status')}</th>
                    <th>{t('monitoring.account_inspection_verdict')}</th>
                    <th>{t('common.action')}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredResultRows.length > 0 ? (
                    visibleResultRows.map(({ item, healthStatus, manualActions }) => {
                      const tokenRefreshDetail = formatTokenRefreshDetail(item, i18n.language, t);
                      const healthStatusLabel = buildHealthStatusLabel(item, healthStatus, t);
                      const showErrorDetails = hasInspectionErrorDetails(item);
                      return (
                        <tr key={item.key}>
                          <td><div className={styles.primaryCell}><span>{item.fileName}</span><small>{item.provider}</small></div></td>
                          <td>
                            <div className={styles.healthCell}>
                              {showErrorDetails ? (
                                <button
                                  type="button"
                                  className={styles.healthErrorButton}
                                  onClick={() => setSelectedErrorResult(item)}
                                  title={t('monitoring.account_inspection_error_details_click_hint')}
                                  aria-label={t('monitoring.account_inspection_error_details_click_hint')}
                                >
                                  <span className={`${styles.healthBadge} ${healthToneClass[healthStatus]}`}>{healthStatusLabel}</span>
                                </button>
                              ) : (
                                <span className={`${styles.healthBadge} ${healthToneClass[healthStatus]}`}>{healthStatusLabel}</span>
                              )}
                            </div>
                          </td>
                          <td><span className={item.disabled ? styles.stateTextMuted : styles.stateTextGood}>{formatCurrentStateLabel(item, t)}</span></td>
                          <td>
                            <div className={styles.quotaCell}>
                              <span>{formatQuotaRemainingLabel(item.usedPercent)}</span>
                            </div>
                          </td>
                          <td>
                            <div className={styles.tokenRefreshCell}>
                              <span className={tokenRefreshToneClass(item)} title={tokenRefreshDetail || undefined}>{formatTokenRefreshLabel(item, t)}</span>
                            </div>
                          </td>
                          <td>
                            <div className={styles.verdictCell}>
                              <strong>{formatInspectionVerdictPrimary(item, healthStatus, t)}</strong>
                            </div>
                          </td>
                          <td className={styles.operationCell}>
                            <div className={styles.operationActions}>
                              <Button
                                size="sm"
                                variant="secondary"
                                onClick={() => void handleRefreshTokenSingle(item)}
                                loading={refreshingTokenKey === item.key}
                                disabled={restoredSnapshot || runStatus === 'running' || executing || recheckingKey !== null || refreshingTokenKey !== null}
                                title={t('monitoring.account_inspection_refresh_token_tooltip')}
                              >
                                {t('monitoring.account_inspection_refresh_token_action')}
                              </Button>
                              <Button size="sm" variant="secondary" onClick={() => void handleRecheckSingle(item)} loading={recheckingKey === item.key} disabled={restoredSnapshot || runStatus === 'running' || executing || recheckingKey !== null || refreshingTokenKey !== null}>
                                {t('monitoring.account_inspection_recheck_account')}
                              </Button>
                              {manualActions.map((action) => (
                                <Button key={action} size="sm" variant={action === 'delete' ? 'danger' : 'secondary'} onClick={() => handleExecuteSingle(item, action)} disabled={restoredSnapshot || runStatus === 'running' || executing || recheckingKey !== null || refreshingTokenKey !== null}>
                                  {formatActionLabel(action, t)}
                                </Button>
                              ))}
                            </div>
                          </td>
                        </tr>
                      );
                    })
                  ) : (
                    <tr><td colSpan={7}><div className={styles.emptyBlockSmall}>{t('monitoring.account_inspection_no_filtered_results')}</div></td></tr>
                  )}
                </tbody>
              </table>
            </div>
            {resultPagination.totalPages > 1 ? (
              <div className={quotaStyles.pagination}>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => setResultPage((page) => Math.max(1, page - 1))}
                  disabled={!resultPagination.hasPrevious}
                  aria-label={t('monitoring.previous_page')}
                >
                  {t('monitoring.previous_page')}
                </Button>
                <div className={quotaStyles.pageInfo}>
                  {t('monitoring.pagination_info', {
                    from: resultPagination.from,
                    to: resultPagination.to,
                    total: resultPagination.total,
                    page: resultPagination.page,
                    totalPages: resultPagination.totalPages,
                    defaultValue: `${resultPagination.from}-${resultPagination.to} / ${resultPagination.total}`,
                  })}
                </div>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => setResultPage((page) => page + 1)}
                  disabled={!resultPagination.hasNext}
                  aria-label={t('monitoring.next_page')}
                >
                  {t('monitoring.next_page')}
                </Button>
              </div>
            ) : null}
          </>
        ) : (
          <div className={styles.emptyState}>
            <strong>{resultEmptyMessage}</strong>
            <span>{connectionStatus === 'connected' ? t('monitoring.account_inspection_empty_hint') : t('notification.connection_required')}</span>
          </div>
        )}
        </Card>
      </div>

      <div className={styles.logsSection}>
        <div className={styles.operationModuleHeader}>
          <div>
            <h2>{t('monitoring.account_inspection_logs_title')}</h2>
            <p>{t('monitoring.account_inspection_logs_desc')}</p>
          </div>
          <div className={styles.logHeaderActions}>
            <div className={styles.resultFilterControl}>
              {logLevelOptions.map((option) => (
                <button key={option.key} type="button" className={[styles.resultFilterButton, logLevelFilter === option.key ? styles.resultFilterButtonActive : ''].filter(Boolean).join(' ')} onClick={() => setLogLevelFilter(option.key)}>
                  <span>{option.label}</span>
                </button>
              ))}
            </div>
            <button type="button" className={styles.foldButton} onClick={() => setLogsCollapsed((previous) => !previous)} disabled={logs.length === 0}>
              {logsCollapsed ? <IconChevronDown size={16} /> : <IconChevronUp size={16} />}
              <span>{logsCollapsed ? t('monitoring.account_inspection_expand_logs') : t('monitoring.account_inspection_fold_logs')}</span>
            </button>
          </div>
        </div>

        <Card className={styles.panel}>
          {!logsCollapsed ? (
            <div ref={logListRef} className={styles.logList}>
              {filteredLogs.length > 0 ? visibleLogs.map((entry) => (
                <div key={entry.id} className={`${styles.logRow} ${levelClassMap[entry.level]}`}>
                  <span className={styles.logTime}>{formatTimestamp(entry.timestamp, i18n.language)}</span>
                  <span className={styles.logMessage}>{entry.message}</span>
                </div>
              )) : <div className={styles.emptyBlock}>{t('monitoring.account_inspection_logs_empty')}</div>}
              {logPagination.totalPages > 1 ? (
                <div className={quotaStyles.pagination}>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => setLogPage((page) => Math.max(1, page - 1))}
                    disabled={!logPagination.hasPrevious}
                    aria-label={t('monitoring.previous_page')}
                  >
                    {t('monitoring.previous_page')}
                  </Button>
                  <div className={quotaStyles.pageInfo}>
                    {t('monitoring.pagination_info', {
                      from: logPagination.from,
                      to: logPagination.to,
                      total: logPagination.total,
                      page: logPagination.page,
                      totalPages: logPagination.totalPages,
                      defaultValue: `${logPagination.from}-${logPagination.to} / ${logPagination.total}`,
                    })}
                  </div>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => setLogPage((page) => page + 1)}
                    disabled={!logPagination.hasNext}
                    aria-label={t('monitoring.next_page')}
                  >
                    {t('monitoring.next_page')}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : (
            <div className={styles.logCollapsedBar}><span>{t('monitoring.account_inspection_logs_collapsed', { count: filteredLogs.length })}</span></div>
          )}
        </Card>
      </div>

      <Modal
        open={Boolean(selectedErrorResult)}
        onClose={() => setSelectedErrorResult(null)}
        title={t('monitoring.account_inspection_error')}
        width={720}
        className={styles.errorModal}
        footer={(
          <div className={styles.errorModalActions}>
            <Button variant="primary" size="sm" onClick={() => setSelectedErrorResult(null)}>
              {t('common.close')}
            </Button>
          </div>
        )}
      >
        {selectedErrorResult ? (
          <InspectionErrorDetailsPanel item={selectedErrorResult} t={t} />
        ) : null}
      </Modal>

      <Modal
        open={isSettingsModalOpen}
        onClose={() => setIsSettingsModalOpen(false)}
        title={t('monitoring.account_inspection_settings_title')}
        width={1120}
        className={styles.settingsModal}
      >
        <div className={styles.settingsWorkbench}>
          <aside className={styles.settingsSidebar}>
            <div className={styles.settingsSidebarIntro}>
              <strong>{t('monitoring.account_inspection_settings_title')}</strong>
              <span>{t('monitoring.account_inspection_settings_desc')}</span>
            </div>
            <div className={styles.settingsSidebarNav}>
              {[
                { key: 'plan' as const, label: t('monitoring.account_inspection_schedule_section_title'), meta: draftScheduleStatusLabel },
                { key: 'scope' as const, label: t('monitoring.account_inspection_settings_basic_section_title'), meta: draftInspectionScopeLabel },
                { key: 'runtime' as const, label: t('monitoring.account_inspection_settings_runtime_section_title'), meta: `${settingsDraft.workers} / ${settingsDraft.timeout}ms` },
                { key: 'antigravity' as const, label: t('monitoring.account_inspection_settings_advanced_section_title'), meta: draftQuotaModeLabel },
                { key: 'auto' as const, label: t('monitoring.account_inspection_settings_auto_section_title'), meta: draftAutoPolicyLabel },
              ].map((item) => (
                <button
                  key={item.key}
                  type="button"
                  onClick={() => scrollToSettingsSection(item.key)}
                >
                  <strong>{item.label}</strong>
                  <span>{item.meta}</span>
                </button>
              ))}
            </div>
            <div className={styles.settingsSidebarStatus}>
              <span>
                <small>{t('monitoring.account_inspection_quota_threshold_short')}</small>
                <strong>{`${settingsDraft.usedPercentThreshold || '--'}%`}</strong>
              </span>
              <span>
                <small>{t('monitoring.account_inspection_account_invalid_action_short')}</small>
                <strong>{draftAccountInvalidActionLabel}</strong>
              </span>
              <span>
                <small>{t('monitoring.account_inspection_request_error_action_short')}</small>
                <strong>{draftRequestErrorActionLabel}</strong>
              </span>
            </div>
          </aside>

          <div ref={settingsMainRef} className={styles.settingsWorkbenchMain}>
            <section className={styles.settingsHeroPanel}>
              <div>
                <strong>{t('monitoring.account_inspection_settings_overview_title')}</strong>
                <span>{t('monitoring.account_inspection_settings_overview_desc')}</span>
              </div>
              <div className={styles.settingsSummaryGrid}>
                <span>
                  <small>{t('monitoring.account_inspection_detection_scope')}</small>
                  <strong>{draftInspectionScopeLabel}</strong>
                </span>
                <span>
                  <small>{t('monitoring.account_inspection_scheduled_inspection_short')}</small>
                  <strong>{draftScheduleStatusLabel}</strong>
                </span>
                <span>
                  <small>{t('monitoring.account_inspection_settings_antigravity_quota_mode_label')}</small>
                  <strong>{draftQuotaModeLabel}</strong>
                </span>
                <span>
                  <small>{t('monitoring.account_inspection_settings_auto_section_title')}</small>
                  <strong>{draftAutoPolicyLabel}</strong>
                </span>
              </div>
            </section>

            <section ref={setSettingsSectionRef('plan')} className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>01</small>
                  <strong>{t('monitoring.account_inspection_schedule_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_schedule_section_desc')}</span>
                </div>
                <ToggleSwitch
                  checked={scheduleDraft.enabled}
                  onChange={(value) => dispatchBackendState({ type: 'updateScheduleDraft', values: { enabled: value } })}
                  ariaLabel={t('monitoring.account_inspection_schedule_enabled_label')}
                />
              </div>
              <div className={styles.settingsSplitGrid}>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_schedule_interval_label')}
                    type="number"
                    value={scheduleDraft.intervalMinutes}
                    onChange={(event) => dispatchBackendState({ type: 'updateScheduleDraft', values: { intervalMinutes: event.target.value } })}
                    min={SCHEDULE_INTERVAL_LIMITS.min}
                    step={1}
                  />
                  <div className={styles.settingsHint}>{t('monitoring.account_inspection_schedule_interval_hint')}</div>
                </div>
                <div className={styles.settingsInsightPanel}>
                  <small>{t('monitoring.account_inspection_schedule_next_run')}</small>
                  <strong>{schedule?.nextRunAt ? formatTimestamp(schedule.nextRunAt, i18n.language) : '--'}</strong>
                  <span>{scheduleDraft.enabled ? draftScheduleStatusLabel : settingDisabledLabel}</span>
                </div>
              </div>
            </section>

            <section ref={setSettingsSectionRef('scope')} className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>02</small>
                  <strong>{t('monitoring.account_inspection_settings_basic_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_basic_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsThreeGrid}>
                <div className={styles.settingsFormPanel}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_target_type_label')}</label>
                  <Select
                    value={settingsDraft.targetType}
                    options={INSPECTION_TARGET_OPTIONS}
                    onChange={(value) => handleSettingsDraftChange('targetType', value)}
                    ariaLabel={t('monitoring.account_inspection_settings_target_type_label')}
                  />
                  <div className={styles.settingsHint}>{t('monitoring.account_inspection_settings_target_type_hint')}</div>
                </div>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_settings_used_percent_threshold_label')}
                    hint={t('monitoring.account_inspection_settings_threshold_hint')}
                    type="number"
                    value={settingsDraft.usedPercentThreshold}
                    onChange={(event) => handleSettingsDraftChange('usedPercentThreshold', event.target.value)}
                    min={THRESHOLD_LIMITS.min}
                    max={THRESHOLD_LIMITS.max}
                    step={0.1}
                  />
                </div>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_settings_sample_size_label')}
                    hint={t('monitoring.account_inspection_settings_sample_size_hint')}
                    type="number"
                    value={settingsDraft.sampleSize}
                    onChange={(event) => handleSettingsDraftChange('sampleSize', event.target.value)}
                    min={SAMPLE_SIZE_LIMITS.min}
                    step={1}
                  />
                </div>
              </div>
            </section>

            <section ref={setSettingsSectionRef('runtime')} className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>03</small>
                  <strong>{t('monitoring.account_inspection_settings_runtime_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_runtime_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsMatrixGrid}>
                <Input
                  label={t('monitoring.account_inspection_settings_workers_label')}
                  hint={t('monitoring.account_inspection_settings_workers_hint', { min: WORKER_LIMITS.min, max: WORKER_LIMITS.max })}
                  type="number"
                  value={settingsDraft.workers}
                  onChange={(event) => handleSettingsDraftChange('workers', event.target.value)}
                  min={WORKER_LIMITS.min}
                  max={WORKER_LIMITS.max}
                  step={1}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_delete_workers_label')}
                  hint={t('monitoring.account_inspection_settings_delete_workers_hint', { min: DELETE_WORKER_LIMITS.min, max: DELETE_WORKER_LIMITS.max })}
                  type="number"
                  value={settingsDraft.deleteWorkers}
                  onChange={(event) => handleSettingsDraftChange('deleteWorkers', event.target.value)}
                  min={DELETE_WORKER_LIMITS.min}
                  max={DELETE_WORKER_LIMITS.max}
                  step={1}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_timeout_label')}
                  hint={t('monitoring.account_inspection_settings_timeout_hint', { min: TIMEOUT_LIMITS.min, max: TIMEOUT_LIMITS.max })}
                  type="number"
                  value={settingsDraft.timeout}
                  onChange={(event) => handleSettingsDraftChange('timeout', event.target.value)}
                  min={TIMEOUT_LIMITS.min}
                  max={TIMEOUT_LIMITS.max}
                  step={TIMEOUT_LIMITS.step}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_retries_label')}
                  hint={t('monitoring.account_inspection_settings_retries_hint', { min: RETRY_LIMITS.min, max: RETRY_LIMITS.max })}
                  type="number"
                  value={settingsDraft.retries}
                  onChange={(event) => handleSettingsDraftChange('retries', event.target.value)}
                  min={RETRY_LIMITS.min}
                  max={RETRY_LIMITS.max}
                  step={1}
                />
              </div>
            </section>

            <section ref={setSettingsSectionRef('antigravity')} className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>04</small>
                  <strong>{t('monitoring.account_inspection_settings_advanced_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_advanced_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsFocusGrid}>
                <div className={styles.settingsFocusCard}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_antigravity_quota_mode_label')}</label>
                  <Select
                    value={settingsDraft.antigravityQuotaMode}
                    options={ANTIGRAVITY_QUOTA_MODE_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }))}
                    onChange={handleAntigravityQuotaModeChange}
                    ariaLabel={t('monitoring.account_inspection_settings_antigravity_quota_mode_label')}
                  />
                  <span>{t('monitoring.account_inspection_settings_antigravity_quota_mode_hint')}</span>
                  <strong>{draftQuotaModeLabel}</strong>
                </div>
                <div className={styles.settingsFocusCard}>
                  <div className={styles.settingsPolicyControl}>
                    <ToggleSwitch
                      checked={settingsDraft.antigravityDeepProbeEnabled}
                      onChange={handleAntigravityDeepProbeChange}
                      label={t('monitoring.account_inspection_settings_antigravity_deep_probe_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_antigravity_deep_probe_label')}
                      labelPosition="left"
                    />
                  </div>
                  <span>{t('monitoring.account_inspection_settings_antigravity_deep_probe_hint')}</span>
                  <div className={!settingsDraft.antigravityDeepProbeEnabled ? styles.settingsMutedField : undefined}>
                    <Input
                      label={t('monitoring.account_inspection_settings_antigravity_deep_probe_model_label')}
                      hint={t('monitoring.account_inspection_settings_antigravity_deep_probe_model_hint')}
                      value={settingsDraft.antigravityDeepProbeModel}
                      onChange={(event) => handleSettingsDraftChange('antigravityDeepProbeModel', event.target.value)}
                      disabled={!settingsDraft.antigravityDeepProbeEnabled}
                    />
                  </div>
                </div>
                <div className={styles.settingsFocusCard}>
                  <div className={styles.settingsPolicyControl}>
                    <ToggleSwitch
                      checked={settingsDraft.xaiDeepProbeEnabled}
                      onChange={handleXAIDeepProbeChange}
                      label={t('monitoring.account_inspection_settings_xai_deep_probe_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_xai_deep_probe_label')}
                      labelPosition="left"
                    />
                  </div>
                  <span>{t('monitoring.account_inspection_settings_xai_deep_probe_hint')}</span>
                  <div className={!settingsDraft.xaiDeepProbeEnabled ? styles.settingsMutedField : undefined}>
                    <Input
                      label={t('monitoring.account_inspection_settings_xai_deep_probe_model_label')}
                      hint={t('monitoring.account_inspection_settings_xai_deep_probe_model_hint')}
                      value={settingsDraft.xaiDeepProbeModel}
                      onChange={(event) => handleSettingsDraftChange('xaiDeepProbeModel', event.target.value)}
                      disabled={!settingsDraft.xaiDeepProbeEnabled}
                    />
                  </div>
                </div>
              </div>
            </section>

            <section ref={setSettingsSectionRef('auto')} className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>05</small>
                  <strong>{t('monitoring.account_inspection_settings_auto_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_auto_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsAutomationGrid}>
                <div className={styles.settingsAutomationStack}>
                  <div className={styles.settingsAutomationCard}>
                    <ToggleSwitch
                      checked={settingsDraft.autoExecuteQuotaLimitDisable}
                      onChange={handleAutoExecuteQuotaLimitChange}
                      label={t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_label')}
                      labelPosition="left"
                    />
                    <span>{t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_hint')}</span>
                  </div>
                  <div className={styles.settingsAutomationCard}>
                    <ToggleSwitch
                      checked={settingsDraft.autoExecuteQuotaRecoveryEnable}
                      onChange={handleAutoExecuteQuotaRecoveryChange}
                      label={t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_label')}
                      labelPosition="left"
                    />
                    <span>{t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_hint')}</span>
                  </div>
                </div>
                <div className={styles.settingsRiskPanel}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_label')}</label>
                  <Select
                    value={settingsDraft.autoExecuteAccountInvalidAction}
                    options={AUTO_ERROR_ACTION_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }))}
                    onChange={handleAutoExecuteAccountInvalidActionChange}
                    ariaLabel={t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_label')}
                  />
                  <span>{t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_hint')}</span>
                  {settingsDraft.autoExecuteAccountInvalidAction === 'delete' ? (
                    <div className={styles.settingsDangerNote}>
                      {t('monitoring.account_inspection_delete_irreversible_warning')}
                    </div>
                  ) : null}
                </div>
                <div className={styles.settingsRiskPanel}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_auto_execute_request_error_action_label')}</label>
                  <Select
                    value={settingsDraft.autoExecuteRequestErrorAction}
                    options={AUTO_ERROR_ACTION_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }))}
                    onChange={handleAutoExecuteRequestErrorActionChange}
                    ariaLabel={t('monitoring.account_inspection_settings_auto_execute_request_error_action_label')}
                  />
                  <span>{t('monitoring.account_inspection_settings_auto_execute_request_error_action_hint')}</span>
                  {settingsDraft.autoExecuteRequestErrorAction === 'delete' ? (
                    <div className={styles.settingsDangerNote}>
                      {t('monitoring.account_inspection_delete_irreversible_warning')}
                    </div>
                  ) : null}
                </div>
              </div>
            </section>
          </div>
        </div>

        <div className={styles.settingsActionsBar}>
          <Button variant="secondary" onClick={handleResetSettings}>
            {t('monitoring.account_inspection_settings_reset_button')}
          </Button>
          <div className={styles.settingsActionsRight}>
            <Button variant="secondary" onClick={() => setIsSettingsModalOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button variant="primary" onClick={() => void handleSaveSettings()} loading={scheduleLoading}>
              {t('common.save')}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
