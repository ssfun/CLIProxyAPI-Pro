import type { Config, AuthFileItem } from '@/types';
import { isDisabledAuthFile, isRecordValue, normalizeNumberValue, readBooleanValue, readStringValue, resolveAuthProvider, resolveCodexChatgptAccountId } from '@/utils/quota';
import { normalizeAuthIndex } from '@/utils/usage';

export type AccountInspectionLogLevel = 'info' | 'success' | 'warning' | 'error';
export type AccountInspectionAction = 'keep' | 'delete' | 'disable' | 'enable';
export type AccountInspectionExecutionAction = Exclude<AccountInspectionAction, 'keep'>;
export type AccountInspectionProgressStatus = 'idle' | 'running' | 'paused' | 'stopped' | 'completed' | 'failed';
export type AccountInspectionDeepProbeStatus = 'success' | 'quota' | 'auth_error' | 'transient_error' | 'skipped' | '';
export type AccountInspectionAutoErrorAction = 'none' | 'disable' | 'delete';
export type AccountInspectionAntigravityQuotaMode = 'max-used' | 'claude-gpt';

export interface AccountInspectionSettings {
  baseUrl: string;
  token: string;
  targetType: string;
  workers: number;
  deleteWorkers: number;
  timeout: number;
  retries: number;
  usedPercentThreshold: number;
  sampleSize: number;
}

export interface AccountInspectionConfigurableSettings {
  targetType: string;
  workers: number;
  deleteWorkers: number;
  timeout: number;
  retries: number;
  usedPercentThreshold: number;
  sampleSize: number;
  antigravityDeepProbeEnabled: boolean;
  antigravityDeepProbeModel: string;
  antigravityQuotaMode: AccountInspectionAntigravityQuotaMode;
  xaiDeepProbeEnabled: boolean;
  xaiDeepProbeModel: string;
  autoExecuteQuotaLimitDisable: boolean;
  autoExecuteQuotaRecoveryEnable: boolean;
  autoExecuteAccountInvalidAction: AccountInspectionAutoErrorAction;
  autoExecuteRequestErrorAction: AccountInspectionAutoErrorAction;
  autoExecuteConfirmations: number;
}

export interface AccountInspectionAccount {
  key: string;
  fileName: string;
  displayAccount: string;
  email?: string;
  name?: string;
  authIndex: string | null;
  accountId: string | null;
  provider: string;
  disabled: boolean;
  status: string;
  state: string;
  raw: AuthFileItem;
}

export interface AccountInspectionResultItem extends AccountInspectionAccount {
  action: AccountInspectionAction;
  actionReason: string;
  statusCode: number | null;
  usedPercent: number | null;
  isQuota: boolean;
  error: string;
  errorDetail?: string;
  errorCode?: string;
  deepProbeTriggered?: boolean;
  deepProbeStatus?: AccountInspectionDeepProbeStatus;
  deepProbeError?: string;
  tokenRefreshTriggered?: boolean;
  tokenRefreshStatus?: 'success' | 'failed' | '';
  tokenRefreshError?: string;
  nextRefreshAt?: number;
  executed?: boolean;
}

export interface AccountInspectionSummary {
  totalFiles: number;
  probeSetCount: number;
  sampledCount: number;
  disabledCount: number;
  enabledCount: number;
  deleteCount: number;
  disableCount: number;
  enableCount: number;
  keepCount: number;
  errorCount: number;
  usedPercentThreshold: number;
  sampled: boolean;
  plannedActionPreview: string[];
}

export interface AccountInspectionProgressSummary {
  totalFiles: number;
  probeSetCount: number;
  sampledCount: number;
  disabledCount: number;
  enabledCount: number;
  deleteCount: number;
  disableCount: number;
  enableCount: number;
  keepCount: number;
  errorCount: number;
}

export interface AccountInspectionHealthCounts {
  total: number;
  healthy: number;
  disabled: number;
  authInvalid: number;
  quotaExhausted: number;
  inspectionError: number;
  recoverable: number;
}

export interface AccountInspectionPageInfo {
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  hasMore: boolean;
}

export interface AccountInspectionRunResult {
  results: AccountInspectionResultItem[];
  summary: AccountInspectionSummary;
  startedAt: number;
  finishedAt: number;
  healthCounts?: AccountInspectionHealthCounts;
  providerHealthCounts?: Record<string, AccountInspectionHealthCounts>;
  resultsPage?: AccountInspectionPageInfo;
  resultsLimited?: boolean;
}

export interface AccountInspectionProgressSnapshot {
  total: number;
  completed: number;
  inFlight: number;
  pending: number;
  percent: number;
  status: AccountInspectionProgressStatus;
  summary: AccountInspectionProgressSummary;
  startedAt: number;
  updatedAt: number;
}

export type AccountInspectionBackendRunState = 'idle' | 'running' | 'paused' | 'stopping' | 'stopped' | 'completed' | 'partial' | 'failed';

export type AccountInspectionBackendProgress = {
  total: number;
  completed: number;
  inFlight: number;
  pending: number;
};

export type AccountInspectionBackendLog = {
  time: number;
  level: AccountInspectionLogLevel;
  message: string;
};

export type AccountInspectionBackendResultItem = Omit<AccountInspectionResultItem, 'displayAccount' | 'accountId' | 'status' | 'state' | 'raw'> & {
  displayName: string;
  email?: string;
  name?: string;
  executed?: boolean;
  executeError?: string;
};

export type AccountInspectionBackendStatus = {
  state: AccountInspectionBackendRunState;
  lastStartedAt: number;
  lastFinishedAt: number;
  lastError: string;
  progress?: AccountInspectionBackendProgress;
  summary: AccountInspectionSummary & {
    executedDeleteCount?: number;
    executedDisableCount?: number;
    executedEnableCount?: number;
  };
  healthCounts?: AccountInspectionHealthCounts;
  providerHealthCounts?: Record<string, AccountInspectionHealthCounts>;
  logsPage?: AccountInspectionPageInfo;
  resultsPage?: AccountInspectionPageInfo;
  logsLimited?: boolean;
  resultsLimited?: boolean;
  restoredSnapshot?: boolean;
  logs: AccountInspectionBackendLog[] | null;
  results: AccountInspectionBackendResultItem[] | null;
};

export type AccountInspectionBackendSchedule = {
  enabled: boolean;
  intervalMinutes: number;
  nextRunAt: number;
  settings: AccountInspectionConfigurableSettings;
};

export type AccountInspectionBackendResponse = {
  schedule: AccountInspectionBackendSchedule;
  status: AccountInspectionBackendStatus;
};

export const isAccountInspectionBackendResponse = (value: unknown): value is AccountInspectionBackendResponse => {
  if (!isRecordValue(value)) return false;
  const schedule = value.schedule;
  const status = value.status;
  return isRecordValue(schedule)
    && isRecordValue(schedule.settings)
    && isRecordValue(status)
    && isRecordValue(status.summary);
};

export type AccountInspectionDisplayRunStatus = 'idle' | 'running' | 'paused' | 'success' | 'error';

export interface AccountInspectionExecutionOutcome {
  action: AccountInspectionExecutionAction;
  fileName: string;
  displayAccount: string;
  email?: string;
  name?: string;
  provider: string;
  authIndex: string | null;
  success: boolean;
  error: string;
}

export interface AccountInspectionExecutionResult {
  outcomes: AccountInspectionExecutionOutcome[];
  refreshedFiles: AuthFileItem[];
  refreshError: string;
}

export const ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE = 'all';

export const ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS = [
  'antigravity',
  'claude',
  'codex',
  'gemini-cli',
  'kimi',
  'xai',
] as const;

const ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET = new Set<string>(ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS);

export type AccountInspectionSupportedProvider = typeof ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS[number];

export const ACCOUNT_INSPECTION_SETTING_LIMITS = {
  workers: { min: 1, max: 8 },
  deleteWorkers: { min: 1, max: 4 },
  timeout: { min: 3000, max: 30000, step: 1000 },
  retries: { min: 0, max: 1 },
  usedPercentThreshold: { min: 0, max: 100 },
  sampleSize: { min: 0 },
  autoExecuteConfirmations: { min: 1, max: 5 },
  scheduleIntervalMinutes: { min: 1 },
} as const;

export const ACCOUNT_INSPECTION_SETTINGS_STORAGE_KEY = 'cli-proxy-account-inspection-settings-v1';

export const DEFAULT_ACCOUNT_INSPECTION_SETTINGS: AccountInspectionConfigurableSettings = {
  targetType: ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE,
  workers: 4,
  deleteWorkers: 4,
  timeout: 15000,
  retries: 0,
  usedPercentThreshold: 100,
  sampleSize: 0,
  antigravityDeepProbeEnabled: false,
  antigravityDeepProbeModel: 'claude-sonnet-4-6',
  antigravityQuotaMode: 'claude-gpt',
  xaiDeepProbeEnabled: false,
  xaiDeepProbeModel: 'grok-4.5',
  autoExecuteQuotaLimitDisable: false,
  autoExecuteQuotaRecoveryEnable: false,
  autoExecuteAccountInvalidAction: 'none',
  autoExecuteRequestErrorAction: 'none',
  autoExecuteConfirmations: 1,
};

type IntegerBounds = {
  min: number;
  max?: number;
};

type ClampIntegerOptions = {
  clampBelowMin?: boolean;
};

const clampInteger = (
  value: number | undefined | null,
  fallback: number,
  bounds: IntegerBounds,
  options: ClampIntegerOptions = {}
) => {
  if (!Number.isFinite(value) || value === undefined || value === null) return fallback;
  const integer = Math.floor(value);
  if (integer < bounds.min) return options.clampBelowMin ? bounds.min : fallback;
  return Math.min(bounds.max ?? integer, integer);
};

const normalizeThreshold = (value: number | undefined) => {
  if (!Number.isFinite(value) || value === undefined || value < 0) return NaN;
  if (value > 0 && value <= 1) {
    return value * 100;
  }
  return value;
};

export const normalizeAutoErrorAction = (value: unknown): AccountInspectionAutoErrorAction => {
  const normalized = readStringValue(value).toLowerCase();
  return normalized === 'disable' || normalized === 'delete' ? normalized : 'none';
};

export const normalizeAntigravityQuotaMode = (value: unknown): AccountInspectionAntigravityQuotaMode => {
  const normalized = readStringValue(value).toLowerCase();
  return normalized === 'max-used' ? 'max-used' : 'claude-gpt';
};

export const formatAccountInspectionIdentity = (
  item: Pick<AccountInspectionAccount, 'displayAccount' | 'email' | 'name' | 'fileName'>
) => {
  const label = item.email || item.name || item.displayAccount;
  if (label && label !== '-') {
    return item.fileName ? `${label}[${item.fileName}]` : label;
  }
  return item.fileName;
};

const readAuthFileName = (file: AuthFileItem) => {
  const name = readStringValue(file.name);
  if (name) return name;
  const id = readStringValue(file.id);
  if (id) return id;
  const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
  return authIndex || 'unknown-auth-file';
};

const readAuthEmail = (file: AuthFileItem) => {
  const idToken = file.id_token;
  return readStringValue(file.email) ||
    (typeof idToken === 'object' && idToken !== null ? readStringValue((idToken as Record<string, unknown>).email) : '');
};

const readDisplayAccount = (file: AuthFileItem) =>
  readAuthEmail(file) ||
  readStringValue(file.name) ||
  '-';

const toInspectionAccount = (file: AuthFileItem): AccountInspectionAccount => ({
  key: `${readAuthFileName(file)}::${normalizeAuthIndex(file['auth_index'] ?? file.authIndex) || '-'}`,
  fileName: readAuthFileName(file),
  displayAccount: readDisplayAccount(file),
  email: readAuthEmail(file) || undefined,
  name: readStringValue(file.name) || undefined,
  authIndex: normalizeAuthIndex(file['auth_index'] ?? file.authIndex),
  accountId: resolveCodexChatgptAccountId(file),
  provider: resolveAuthProvider(file),
  disabled: isDisabledAuthFile(file),
  status: readStringValue(file.status),
  state: readStringValue(file.state),
  raw: file,
});

const readConfigurableSettingsFromConfig = (
  config?: Config | null
): Partial<AccountInspectionConfigurableSettings> => {
  const clean = config?.clean ?? null;
  return {
    targetType: readStringValue(clean?.targetType),
    workers: normalizeNumberValue(clean?.workers) ?? undefined,
    deleteWorkers: normalizeNumberValue(clean?.deleteWorkers) ?? undefined,
    timeout: normalizeNumberValue(clean?.timeout) ?? undefined,
    retries: normalizeNumberValue(clean?.retries) ?? undefined,
    usedPercentThreshold: normalizeNumberValue(clean?.usedPercentThreshold) ?? undefined,
    sampleSize: normalizeNumberValue(clean?.sampleSize) ?? undefined,
    autoExecuteQuotaLimitDisable: undefined,
    autoExecuteQuotaRecoveryEnable: undefined,
    autoExecuteAccountInvalidAction: undefined,
    autoExecuteRequestErrorAction: undefined,
    autoExecuteConfirmations: undefined,
    antigravityDeepProbeEnabled: undefined,
    antigravityDeepProbeModel: undefined,
    antigravityQuotaMode: undefined,
    xaiDeepProbeEnabled: undefined,
    xaiDeepProbeModel: undefined,
  };
};

const normalizeInspectionTargetType = (value: unknown) => {
  const targetType = readStringValue(value).toLowerCase();
  return targetType === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE ||
    ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET.has(targetType)
    ? targetType
    : DEFAULT_ACCOUNT_INSPECTION_SETTINGS.targetType;
};

const normalizeConfigurableSettings = (
  input?: Partial<AccountInspectionConfigurableSettings> | null
): AccountInspectionConfigurableSettings => {
  const merged = {
    ...DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
    ...(input ?? {}),
  };

  const threshold = normalizeThreshold(merged.usedPercentThreshold);
  const workers = clampInteger(
    normalizeNumberValue(merged.workers),
    DEFAULT_ACCOUNT_INSPECTION_SETTINGS.workers,
    ACCOUNT_INSPECTION_SETTING_LIMITS.workers
  );

  return {
    targetType: normalizeInspectionTargetType(merged.targetType),
    workers,
    deleteWorkers: clampInteger(
      normalizeNumberValue(merged.deleteWorkers),
      workers,
      ACCOUNT_INSPECTION_SETTING_LIMITS.deleteWorkers
    ),
    timeout: clampInteger(
      normalizeNumberValue(merged.timeout),
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.timeout,
      ACCOUNT_INSPECTION_SETTING_LIMITS.timeout,
      { clampBelowMin: true }
    ),
    retries: clampInteger(
      normalizeNumberValue(merged.retries),
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.retries,
      ACCOUNT_INSPECTION_SETTING_LIMITS.retries
    ),
    usedPercentThreshold: Number.isFinite(threshold)
      ? Math.max(
          ACCOUNT_INSPECTION_SETTING_LIMITS.usedPercentThreshold.min,
          Math.min(ACCOUNT_INSPECTION_SETTING_LIMITS.usedPercentThreshold.max, threshold)
        )
      : DEFAULT_ACCOUNT_INSPECTION_SETTINGS.usedPercentThreshold,
    sampleSize: clampInteger(
      normalizeNumberValue(merged.sampleSize),
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.sampleSize,
      ACCOUNT_INSPECTION_SETTING_LIMITS.sampleSize
    ),
    autoExecuteQuotaLimitDisable: readBooleanValue(
      merged.autoExecuteQuotaLimitDisable,
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.autoExecuteQuotaLimitDisable
    ),
    autoExecuteQuotaRecoveryEnable: readBooleanValue(
      merged.autoExecuteQuotaRecoveryEnable,
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.autoExecuteQuotaRecoveryEnable
    ),
    antigravityDeepProbeEnabled: readBooleanValue(
      merged.antigravityDeepProbeEnabled,
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.antigravityDeepProbeEnabled
    ),
    antigravityDeepProbeModel: readStringValue(merged.antigravityDeepProbeModel) ||
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.antigravityDeepProbeModel,
    antigravityQuotaMode: normalizeAntigravityQuotaMode(merged.antigravityQuotaMode),
    xaiDeepProbeEnabled: readBooleanValue(
      merged.xaiDeepProbeEnabled,
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.xaiDeepProbeEnabled
    ),
    xaiDeepProbeModel: readStringValue(merged.xaiDeepProbeModel) ||
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.xaiDeepProbeModel,
    autoExecuteAccountInvalidAction: normalizeAutoErrorAction(merged.autoExecuteAccountInvalidAction),
    autoExecuteRequestErrorAction: normalizeAutoErrorAction(merged.autoExecuteRequestErrorAction),
    autoExecuteConfirmations: clampInteger(
      normalizeNumberValue(merged.autoExecuteConfirmations),
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.autoExecuteConfirmations,
      ACCOUNT_INSPECTION_SETTING_LIMITS.autoExecuteConfirmations,
      { clampBelowMin: true }
    ),
  };
};

export const loadAccountInspectionConfigurableSettings = (
  config?: Config | null
): AccountInspectionConfigurableSettings => {
  const configSettings = readConfigurableSettingsFromConfig(config);

  try {
    if (typeof localStorage === 'undefined') {
      return normalizeConfigurableSettings(configSettings);
    }
    const raw = localStorage.getItem(ACCOUNT_INSPECTION_SETTINGS_STORAGE_KEY);
    if (!raw) {
      return normalizeConfigurableSettings(configSettings);
    }
    const parsed: unknown = JSON.parse(raw);
    if (!isRecordValue(parsed)) {
      return normalizeConfigurableSettings(configSettings);
    }
    return normalizeConfigurableSettings({
      ...configSettings,
      ...parsed,
    });
  } catch {
    return normalizeConfigurableSettings(configSettings);
  }
};

export const saveAccountInspectionConfigurableSettings = (
  settings: Partial<AccountInspectionConfigurableSettings>
): AccountInspectionConfigurableSettings => {
  const normalized = normalizeConfigurableSettings(settings);

  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(ACCOUNT_INSPECTION_SETTINGS_STORAGE_KEY, JSON.stringify(normalized));
    }
  } catch {
    console.warn('保存 账号巡检配置失败');
  }

  return normalized;
};

export const clearAccountInspectionConfigurableSettings = () => {
  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.removeItem(ACCOUNT_INSPECTION_SETTINGS_STORAGE_KEY);
    }
  } catch {
    console.warn('清除 账号巡检配置失败');
  }
};

const accountInspectionItemKey = (item: Pick<AccountInspectionAccount, 'fileName' | 'authIndex'>) =>
  `${item.fileName}::${item.authIndex ?? '-'}`;

const sortResults = (items: AccountInspectionResultItem[]) =>
  [...items].sort(
    (left, right) =>
      left.fileName.localeCompare(right.fileName) ||
      left.displayAccount.localeCompare(right.displayAccount) ||
      left.key.localeCompare(right.key)
  );

const summarizeResults = (results: AccountInspectionResultItem[]) => {
  const summary = {
    deleteCount: 0,
    disableCount: 0,
    enableCount: 0,
    keepCount: 0,
    errorCount: 0,
    disabledCount: 0,
    enabledCount: 0,
    plannedActionPreview: [] as string[],
  };

  results.forEach((item) => {
    if (item.disabled) {
      summary.disabledCount += 1;
    } else {
      summary.enabledCount += 1;
    }
    if (item.error) summary.errorCount += 1;

    switch (item.action) {
      case 'delete':
        summary.deleteCount += 1;
        break;
      case 'disable':
        summary.disableCount += 1;
        break;
      case 'enable':
        summary.enableCount += 1;
        break;
      case 'keep':
      default:
        summary.keepCount += 1;
        break;
    }

    if (item.action !== 'keep' && summary.plannedActionPreview.length < 10) {
      summary.plannedActionPreview.push(`${formatAccountInspectionIdentity(item)} -> ${item.action}`);
    }
  });

  return summary;
};

const buildPlannedActionPreview = (results: AccountInspectionResultItem[]) => {
  const preview: string[] = [];
  for (const item of results) {
    if (item.action === 'keep') continue;
    preview.push(`${formatAccountInspectionIdentity(item)} -> ${item.action}`);
    if (preview.length >= 10) break;
  }
  return preview;
};

export const createIdleAccountInspectionProgressSnapshot = (): AccountInspectionProgressSnapshot => ({
  total: 0,
  completed: 0,
  inFlight: 0,
  pending: 0,
  percent: 0,
  status: 'idle',
  summary: {
    totalFiles: 0,
    probeSetCount: 0,
    sampledCount: 0,
    disabledCount: 0,
    enabledCount: 0,
    deleteCount: 0,
    disableCount: 0,
    enableCount: 0,
    keepCount: 0,
    errorCount: 0,
  },
  startedAt: Date.now(),
  updatedAt: Date.now(),
});

export const accountInspectionBackendResultToItem = (
  item: NonNullable<AccountInspectionBackendStatus['results']>[number]
): AccountInspectionResultItem => ({
  key: item.key,
  fileName: item.fileName,
  displayAccount: item.displayName,
  email: item.email,
  name: item.name,
  authIndex: item.authIndex || null,
  accountId: null,
  provider: item.provider,
  disabled: item.disabled,
  status: '',
  state: '',
  raw: {
    name: item.fileName,
    type: item.provider,
    provider: item.provider,
    authIndex: item.authIndex,
    disabled: item.disabled,
  },
  action: item.action,
  actionReason: item.actionReason,
  statusCode: item.statusCode ?? null,
  usedPercent: item.usedPercent ?? null,
  isQuota: item.isQuota,
  error: item.executeError || item.error || '',
  errorDetail: item.errorDetail || '',
  errorCode: item.errorCode || '',
  deepProbeTriggered: item.deepProbeTriggered ?? false,
  deepProbeStatus: item.deepProbeStatus ?? '',
  deepProbeError: item.deepProbeError ?? '',
  tokenRefreshTriggered: item.tokenRefreshTriggered ?? false,
  tokenRefreshStatus: item.tokenRefreshStatus ?? '',
  tokenRefreshError: item.tokenRefreshError ?? '',
  nextRefreshAt: item.nextRefreshAt ?? 0,
  executed: item.executed,
});

export const accountInspectionBackendProgressStatus = (
  status: AccountInspectionBackendStatus
): AccountInspectionProgressSnapshot['status'] => {
  if (status.state === 'paused') return 'paused';
  if (status.state === 'running' || status.state === 'stopping') return 'running';
  if (status.state === 'failed') return 'failed';
  if (status.state === 'stopped') return 'stopped';
  if (status.state === 'completed' || status.state === 'partial' || status.lastFinishedAt > 0) return 'completed';
  return 'idle';
};

export const accountInspectionBackendRunStatus = (
  status: AccountInspectionBackendStatus
): AccountInspectionDisplayRunStatus => {
  if (status.state === 'paused') return 'paused';
  if (status.state === 'running' || status.state === 'stopping') return 'running';
  if (status.state === 'failed') return 'error';
  if (status.state === 'stopped') return 'idle';
  if (status.state === 'completed' || status.state === 'partial' || status.lastFinishedAt > 0) {
    return status.lastError ? 'error' : 'success';
  }
  return 'idle';
};

const buildAccountInspectionBackendRunResult = (
  response: AccountInspectionBackendResponse,
  results: AccountInspectionResultItem[],
  startedAt: number,
  finishedAt: number
): AccountInspectionRunResult | null => {
  if (results.length === 0 && response.status.lastFinishedAt <= 0) return null;

  const settings = normalizeConfigurableSettings(response.schedule.settings);
  return {
    results,
    summary: {
      ...response.status.summary,
      usedPercentThreshold: settings.usedPercentThreshold,
      sampled: settings.sampleSize > 0,
      plannedActionPreview: buildPlannedActionPreview(results),
    },
    startedAt,
    finishedAt,
    healthCounts: response.status.healthCounts,
    providerHealthCounts: response.status.providerHealthCounts,
    resultsPage: response.status.resultsPage,
    resultsLimited: response.status.resultsLimited ?? false,
  };
};

export const buildAccountInspectionBackendViewState = (
  response: AccountInspectionBackendResponse,
  now = Date.now()
) => {
  const settings = normalizeConfigurableSettings(response.schedule.settings);
  const startedAt = response.status.lastStartedAt || now;
  const finishedAt = response.status.lastFinishedAt || startedAt;
  const results = (response.status.results ?? []).map(accountInspectionBackendResultToItem);
  const progressStatus = accountInspectionBackendProgressStatus(response.status);
  const summaryTotal = response.status.summary.sampledCount || results.length;
  const total = response.status.progress?.total || summaryTotal;
  const isBackendActive = response.status.state === 'running' || response.status.state === 'paused' || response.status.state === 'stopping';
  const completed = response.status.progress?.completed ?? (isBackendActive ? 0 : total);
  const inFlight = response.status.progress?.inFlight ?? (response.status.state === 'running' ? 1 : 0);
  const pending = response.status.progress?.pending ?? Math.max(0, total - completed - inFlight);
  const hasSnapshot = Array.isArray(response.status.logs) || Array.isArray(response.status.results);

  return {
    settings,
    scheduleDraft: {
      enabled: response.schedule.enabled,
      intervalMinutes: String(response.schedule.intervalMinutes),
    },
    logs: hasSnapshot
      ? (response.status.logs ?? []).map((entry, index) => ({
          id: `backend-${entry.time}-${index}`,
          level: entry.level,
          message: entry.message,
          timestamp: entry.time,
        }))
      : undefined,
    logsPage: hasSnapshot ? response.status.logsPage : undefined,
    autoExecutionCounts: {
      delete: response.status.summary.executedDeleteCount ?? 0,
      disable: response.status.summary.executedDisableCount ?? 0,
      enable: response.status.summary.executedEnableCount ?? 0,
    },
    restoredSnapshot: response.status.restoredSnapshot ?? false,
    result: hasSnapshot
      ? buildAccountInspectionBackendRunResult(response, results, startedAt, finishedAt)
      : undefined,
    progress: {
      total,
      completed,
      inFlight,
      pending,
      percent: total > 0 ? Math.round((completed / total) * 100) : progressStatus === 'completed' ? 100 : 0,
      status: progressStatus,
      summary: response.status.summary,
      startedAt,
      updatedAt: now,
    },
    runStatus: accountInspectionBackendRunStatus(response.status),
  };
};


export const buildExecutionFailureMessage = (outcome: AccountInspectionExecutionOutcome) =>
  `${formatAccountInspectionIdentity(outcome)}：${outcome.error || '执行失败'}`;

export const isSuggestedAction = (item: AccountInspectionResultItem) => item.action !== 'keep';

export const hasAccountInspectionAutoExecutePolicies = (settings: AccountInspectionConfigurableSettings) =>
  settings.autoExecuteQuotaLimitDisable ||
  settings.autoExecuteQuotaRecoveryEnable ||
  settings.autoExecuteAccountInvalidAction !== 'none' ||
  settings.autoExecuteRequestErrorAction !== 'none';

export const applyAccountInspectionExecutionResult = (
  previousResult: AccountInspectionRunResult,
  execution: AccountInspectionExecutionResult
): AccountInspectionRunResult => {
  const successfulOutcomes = new Map(
    execution.outcomes.filter((item) => item.success).map((item) => [accountInspectionItemKey(item), item] as const)
  );
  const refreshedAccounts = new Map(
    execution.refreshedFiles.map((file) => {
      const account = toInspectionAccount(file);
      return [accountInspectionItemKey(account), account] as const;
    })
  );

  const nextResults = sortResults(
    previousResult.results.map((item) => {
      const refreshedAccount = refreshedAccounts.get(accountInspectionItemKey(item));
      const baseItem: AccountInspectionResultItem = refreshedAccount
        ? {
            ...item,
            ...refreshedAccount,
            raw: refreshedAccount.raw,
          }
        : item;
      const outcome = successfulOutcomes.get(accountInspectionItemKey(baseItem));

      if (!outcome) {
        return baseItem;
      }

      return {
        ...baseItem,
        disabled: outcome.action === 'disable' ? true : outcome.action === 'enable' ? false : baseItem.disabled,
        action: 'keep',
        actionReason: '无需处理',
        error: '',
        executed: true,
      };
    })
  );

  const summary = summarizeResults(nextResults);

  return {
    ...previousResult,
    results: nextResults,
    summary: {
      ...previousResult.summary,
      ...summary,
    },
    finishedAt: Date.now(),
  };
};

export const buildSuggestedActionCountLabel = (summary: AccountInspectionSummary) =>
  summary.deleteCount + summary.disableCount + summary.enableCount;
