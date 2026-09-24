import type { AuthFileItem } from '@/types';
import { normalizeNumberValue } from '@/utils/quota';
import { isRecordValue, readBooleanValue, readStringValue } from '@/pro/shared/value';

export type AccountInspectionLogLevel = 'info' | 'success' | 'warning' | 'error';
export type AccountInspectionAction = 'keep' | 'delete' | 'disable' | 'enable';
export type AccountInspectionExecutionAction = Exclude<AccountInspectionAction, 'keep'>;
export type AccountInspectionProgressStatus = 'idle' | 'running' | 'paused' | 'stopped' | 'completed' | 'partial' | 'failed';
export type AccountInspectionDeepProbeStatus = 'success' | 'quota' | 'auth_error' | 'transient_error' | 'skipped' | '';
export type AccountInspectionAutoErrorAction = 'none' | 'disable' | 'delete';
export type AccountInspectionAntigravityQuotaMode = 'max-used' | 'claude-gpt';

export interface AccountInspectionConfigurableSettings {
  targetType: string;
  workers: number;
  providerWorkers: number;
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
  authId?: string;
  authIndex: string | null;
  accountId: string | null;
  provider: string;
  disabled: boolean;
  status: string;
  state: string;
  raw: AuthFileItem;
}

export interface AccountInspectionResultItem extends AccountInspectionAccount {
  registrationEpoch?: string;
  resultRef?: string;
  observedAt?: number;
  runId?: string;
  parentResultRef?: string;
  suggested?: boolean;
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
  quotaCooling?: boolean;
  quotaRetryAt?: number;
  executed?: boolean;
  executedAction?: AccountInspectionExecutionAction | '';
  executedAt?: number;
  executedEffect?: 'quota_protection' | 'quota_recovery' | 'admin_disable' | 'admin_enable' | 'delete' | '';
  executeError?: string;
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
  pendingActionCount?: number;
  pendingDeleteCount?: number;
  pendingDisableCount?: number;
  pendingEnableCount?: number;
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
  unknown?: number;
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
  runStats?: AccountInspectionRunStats;
  resultsPage?: AccountInspectionPageInfo;
  resultsLimited?: boolean;
  settings: AccountInspectionConfigurableSettings;
  state: AccountInspectionBackendRunState;
  lastError: string;
}

export interface AccountInspectionProviderRunStats {
  accounts: number;
  completed: number;
  failed: number;
  unknown: number;
  httpRequests: number;
  retries: number;
  realProbeRequests: number;
  quotaKnown: number;
  wallTimeMs: number;
  queueWaitMs: number;
  networkMs: number;
  refreshMs: number;
  primaryProbeMs: number;
  confirmProbeMs: number;
  maxAccountMs: number;
}

export interface AccountInspectionRunStats {
  wallTimeMs: number;
  providers: Record<string, AccountInspectionProviderRunStats>;
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
  registrationEpoch?: string;
  displayName: string;
  email?: string;
  name?: string;
  executed?: boolean;
  executeError?: string;
  resultRef?: string;
  observedAt?: number;
  runId?: string;
  parentResultRef?: string;
  suggested?: boolean;
  executedAction?: AccountInspectionExecutionAction | '';
  executedAt?: number;
  executedEffect?: 'quota_protection' | 'quota_recovery' | 'admin_disable' | 'admin_enable' | 'delete' | '';
};

export type AccountInspectionBackendStatus = {
  state: AccountInspectionBackendRunState;
  runSettings: AccountInspectionConfigurableSettings;
  lastStartedAt: number;
  lastFinishedAt: number;
  lastError: string;
  persistenceError?: string;
  progress?: AccountInspectionBackendProgress;
  summary: AccountInspectionSummary & {
    pendingActionCount?: number;
    pendingDeleteCount?: number;
    pendingDisableCount?: number;
    pendingEnableCount?: number;
    executedDeleteCount?: number;
    executedDisableCount?: number;
    executedEnableCount?: number;
    executedQuotaProtectionCount?: number;
    executedQuotaRecoveryCount?: number;
  };
  healthCounts?: AccountInspectionHealthCounts;
  providerHealthCounts?: Record<string, AccountInspectionHealthCounts>;
  runStats?: AccountInspectionRunStats;
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
    && isRecordValue(status.summary)
    && (!('runSettings' in status) || isRecordValue(status.runSettings));
};

export type AccountInspectionDisplayRunStatus = 'idle' | 'running' | 'paused' | 'completed' | 'partial' | 'stopped' | 'failed';

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
  providerWorkers: { min: 1, max: 4 },
  deleteWorkers: { min: 1, max: 4 },
  timeout: { min: 3000, max: 30000, step: 1000 },
  retries: { min: 0, max: 1 },
  usedPercentThreshold: { min: 0, max: 100 },
  sampleSize: { min: 0 },
  autoExecuteConfirmations: { min: 1, max: 5 },
  scheduleIntervalMinutes: { min: 1 },
} as const;

const ACCOUNT_INSPECTION_SETTINGS_STORAGE_KEY = 'cli-proxy-account-inspection-settings-v1';

export const DEFAULT_ACCOUNT_INSPECTION_SETTINGS: AccountInspectionConfigurableSettings = {
  targetType: ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE,
  workers: 4,
  providerWorkers: 2,
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
  autoExecuteQuotaRecoveryEnable: true,
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

const formatAccountInspectionIdentity = (
  item: Pick<AccountInspectionAccount, 'displayAccount' | 'email' | 'name' | 'fileName'>
) => {
  const label = item.email || item.name || item.displayAccount;
  if (label && label !== '-') {
    return item.fileName ? `${label}[${item.fileName}]` : label;
  }
  return item.fileName;
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
    providerWorkers: clampInteger(
      normalizeNumberValue(merged.providerWorkers),
      DEFAULT_ACCOUNT_INSPECTION_SETTINGS.providerWorkers,
      ACCOUNT_INSPECTION_SETTING_LIMITS.providerWorkers
    ),
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

export const loadAccountInspectionConfigurableSettings = (): AccountInspectionConfigurableSettings => {
  try {
    if (typeof localStorage === 'undefined') {
      return normalizeConfigurableSettings();
    }
    const raw = localStorage.getItem(ACCOUNT_INSPECTION_SETTINGS_STORAGE_KEY);
    if (!raw) {
      return normalizeConfigurableSettings();
    }
    const parsed: unknown = JSON.parse(raw);
    if (!isRecordValue(parsed)) {
      return normalizeConfigurableSettings();
    }
    return normalizeConfigurableSettings(parsed);
  } catch {
    return normalizeConfigurableSettings();
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
  authId: item.authId,
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
  resultRef: item.resultRef,
  registrationEpoch: item.registrationEpoch || '',
  observedAt: item.observedAt ?? 0,
  runId: item.runId || '',
  parentResultRef: item.parentResultRef || '',
  suggested: item.suggested ?? item.action !== 'keep',
  statusCode: item.statusCode ?? null,
  usedPercent: item.usedPercent ?? null,
  isQuota: item.isQuota,
  error: item.error || '',
  executeError: item.executeError || '',
  errorDetail: item.errorDetail || '',
  errorCode: item.errorCode || '',
  deepProbeTriggered: item.deepProbeTriggered ?? false,
  deepProbeStatus: item.deepProbeStatus ?? '',
  deepProbeError: item.deepProbeError ?? '',
  tokenRefreshTriggered: item.tokenRefreshTriggered ?? false,
  tokenRefreshStatus: item.tokenRefreshStatus ?? '',
  tokenRefreshError: item.tokenRefreshError ?? '',
  nextRefreshAt: item.nextRefreshAt ?? 0,
  quotaCooling: item.quotaCooling ?? false,
  quotaRetryAt: item.quotaRetryAt ?? 0,
  executed: item.executed,
  executedAction: item.executedAction ?? '',
  executedAt: item.executedAt ?? 0,
  executedEffect: item.executedEffect ?? '',
});

const accountInspectionBackendProgressStatus = (
  status: AccountInspectionBackendStatus
): AccountInspectionProgressSnapshot['status'] => {
  if (status.state === 'paused') return 'paused';
  if (status.state === 'running' || status.state === 'stopping') return 'running';
  if (status.state === 'failed') return 'failed';
  if (status.state === 'partial') return 'partial';
  if (status.state === 'stopped') return 'stopped';
  if (status.state === 'completed' || status.lastFinishedAt > 0) return 'completed';
  return 'idle';
};

const accountInspectionBackendRunStatus = (
  status: AccountInspectionBackendStatus
): AccountInspectionDisplayRunStatus => {
  if (status.state === 'paused') return 'paused';
  if (status.state === 'running' || status.state === 'stopping') return 'running';
  if (status.state === 'failed') return 'failed';
  if (status.state === 'stopped') return 'stopped';
  if (status.state === 'partial') return 'partial';
  if (status.state === 'completed') return 'completed';
  return 'idle';
};

const buildAccountInspectionBackendRunResult = (
  response: AccountInspectionBackendResponse,
  results: AccountInspectionResultItem[],
  startedAt: number,
  finishedAt: number
): AccountInspectionRunResult | null => {
  if (results.length === 0 && response.status.lastFinishedAt <= 0) return null;

  const settings = normalizeConfigurableSettings(response.status.runSettings ?? response.schedule.settings);
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
    runStats: response.status.runStats,
    resultsPage: response.status.resultsPage,
    resultsLimited: response.status.resultsLimited ?? false,
    settings,
    state: response.status.state,
    lastError: response.status.lastError || response.status.persistenceError || '',
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
      quotaProtection: response.status.summary.executedQuotaProtectionCount ?? 0,
      quotaRecovery: response.status.summary.executedQuotaRecoveryCount ?? 0,
    },
    restoredSnapshot: response.status.restoredSnapshot ?? false,
    lastError: response.status.lastError || '',
    persistenceError: response.status.persistenceError || '',
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

export const isSuggestedAction = (item: AccountInspectionResultItem) => item.suggested ?? item.action !== 'keep';

export const hasAccountInspectionAutoExecutePolicies = (settings: AccountInspectionConfigurableSettings) =>
  settings.autoExecuteQuotaLimitDisable ||
  settings.autoExecuteAccountInvalidAction !== 'none' ||
  settings.autoExecuteRequestErrorAction !== 'none';
