import { apiClient } from '@/services/api/client';
import { MANAGEMENT_API_PREFIX } from '@/utils/constants';
import type {
  AccountInspectionBackendLog as BackendLog,
  AccountInspectionBackendResponse,
  AccountInspectionBackendResultItem,
  AccountInspectionBackendSchedule,
  AccountInspectionBackendStatus,
  AccountInspectionAction,
  AccountInspectionExecutionAction,
  AccountInspectionResultItem,
} from '@/pro/modules/inspection/features/accountInspection';

export type AccountInspectionSchedule = AccountInspectionBackendSchedule;

export type AccountInspectionBackendLog = BackendLog;

export type AccountInspectionLogStreamMessage = {
  type: 'snapshot' | 'log' | 'status';
  schedule: AccountInspectionSchedule;
  status: AccountInspectionBackendStatus;
  log?: AccountInspectionBackendLog;
};

export type AccountInspectionActionOutcome = {
  action: 'delete' | 'disable' | 'enable';
  fileName: string;
  displayName: string;
  email?: string;
  name?: string;
  provider: string;
  authIndex: string;
  success: boolean;
  error: string;
  executedAction?: 'delete' | 'disable' | 'enable' | '';
  executedAt?: number;
};

export type AccountInspectionInspectOneItem = Pick<
  AccountInspectionResultItem,
  'key' | 'provider' | 'fileName' | 'email' | 'name' | 'authIndex' | 'disabled' | 'resultRef' | 'registrationEpoch'
> & {
  displayName: string;
};

export type AccountInspectionActionItem = Pick<
  AccountInspectionResultItem,
  'key' | 'provider' | 'fileName' | 'email' | 'name' | 'authIndex' | 'disabled'
> & {
  displayName: string;
  resultRef: string;
  suggested: boolean;
  action: AccountInspectionExecutionAction;
};

export type AccountInspectionBatchTarget = Omit<AccountInspectionActionItem, 'action'> & {
  action: AccountInspectionAction;
  registrationEpoch?: string;
  effect?: AccountInspectionBatchEffect;
};

export type AccountInspectionActionsResponse = AccountInspectionBackendResponse & {
  outcomes: AccountInspectionActionOutcome[];
  summary: { total: number; success: number; failed: number };
};

export type AccountInspectionBatchKind = 'inspect' | 'action' | 'recover';
export type AccountInspectionBatchItemStatus = 'ready' | 'running' | 'succeeded' | 'failed' | 'stale' | 'unsupported' | 'interrupted';
export type AccountInspectionBatchEffect = 'inspect' | 'quota_protection' | 'quota_recovery' | 'admin_disable' | 'admin_enable' | 'delete' | 'recovery_check' | 'unknown';
export type AccountInspectionBatchScope =
  | { type: 'selected'; items: AccountInspectionBatchTarget[] }
  | {
      type: 'filtered';
      filter: string;
      provider: string;
      search: string;
      pendingOnly: boolean;
      action?: AccountInspectionAction;
      suggested?: boolean;
    };
export type AccountInspectionBatchOutcome = {
  noop?: boolean;
  accountState?: 'absent' | 'unrestricted';
  success?: boolean;
  error?: string;
  warning?: string;
  result?: AccountInspectionBackendResultItem;
  after?: unknown;
  receipts?: Array<{
    before?: unknown;
    after?: unknown;
    steps?: string[];
    phases?: Array<{ source: string; model?: string; status: 'completed' | 'failed' | 'timeout' | 'skipped'; error?: string }>;
    error?: string;
  }>;
  outcome?: AccountInspectionBatchOutcome;
};
export type AccountInspectionBatchOperation = {
  operationId: string;
  kind: AccountInspectionBatchKind;
  state: 'prepared' | 'running' | 'completed' | 'interrupted';
  createdAt: number;
  expiresAt: number;
  items: Array<{
    key: string;
    status: AccountInspectionBatchItemStatus;
    effect: AccountInspectionBatchEffect;
    error?: string;
    outcome?: AccountInspectionBatchOutcome;
    item: AccountInspectionBatchTarget;
  }>;
  summary: {
    total: number;
    ready: number;
    running: number;
    succeeded: number;
    failed: number;
    stale: number;
    unsupported: number;
    interrupted?: number;
  };
};

export type AccountInspectionInspectOneResponse = AccountInspectionBackendResponse & {
  result: AccountInspectionBackendResultItem;
  error?: string;
};

export type AccountInspectionInspectManyOutcome = {
  key: string;
  fileName: string;
  displayName: string;
  email?: string;
  name?: string;
  provider: string;
  authIndex: string;
  success: boolean;
  error: string;
  result?: AccountInspectionBackendResultItem;
};

export type AccountInspectionInspectManyResponse = AccountInspectionBackendResponse & {
  outcomes: AccountInspectionInspectManyOutcome[];
  error?: string;
};

export type AccountInspectionScheduleResponse = AccountInspectionBackendResponse;

export type AccountInspectionDetailsOptions = {
  includeDetails?: boolean;
  resultLimit?: number;
  logLimit?: number;
  resultPage?: number;
  resultPageSize?: number;
  resultFilter?: string;
  resultPendingOnly?: boolean;
  resultProvider?: string;
  resultSearch?: string;
  logPage?: number;
  logPageSize?: number;
  logLevel?: string;
};

// Bulk rechecks are bounded by the backend's 30-minute run context. Keep a
// small transport grace period so Axios does not cancel an otherwise healthy
// batch before the backend can return its per-account outcomes.
export const ACCOUNT_INSPECTION_BULK_RECHECK_TIMEOUT_MS = 31 * 60 * 1000;

const buildAccountInspectionDetailParams = (options: boolean | AccountInspectionDetailsOptions = false) => {
  const normalized = typeof options === 'boolean' ? { includeDetails: options } : options;
  const params: Record<string, number | string> = { details: normalized.includeDetails ? 1 : 0 };
  if (normalized.resultLimit !== undefined) params.result_limit = normalized.resultLimit;
  if (normalized.logLimit !== undefined) params.log_limit = normalized.logLimit;
  if (normalized.resultPage !== undefined) params.result_page = normalized.resultPage;
  if (normalized.resultPageSize !== undefined) params.result_page_size = normalized.resultPageSize;
  if (normalized.resultFilter) params.result_filter = normalized.resultFilter;
  if (normalized.resultPendingOnly) params.result_pending_only = 1;
  if (normalized.resultProvider) params.result_provider = normalized.resultProvider;
  if (normalized.resultSearch) params.result_search = normalized.resultSearch;
  if (normalized.logPage !== undefined) params.log_page = normalized.logPage;
  if (normalized.logPageSize !== undefined) params.log_page_size = normalized.logPageSize;
  if (normalized.logLevel) params.log_level = normalized.logLevel;
  return params;
};

export const buildAccountInspectionLogsWebSocketUrl = (apiBase: string, includeDetails = false) => {
  const base = apiBase.replace(/\/?v0\/management\/?$/i, '').replace(/\/+$/i, '');
  const url = new URL(`${base}${MANAGEMENT_API_PREFIX}/account-inspection/logs`);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('details', includeDetails ? '1' : '0');
  return url.toString();
};

export const accountInspectionWebSocketProtocol = (managementKey: string) =>
  `cpa-management.${encodeURIComponent(managementKey)}`;

export const nextAccountInspectionReconnectDelay = (currentDelayMs: number) =>
  Math.min(Math.max(currentDelayMs, 1000) * 2, 30000);

export const refreshAccountInspectionAfterReconnect = async (
  loadSummary: () => Promise<unknown>,
  loadDetails: () => Promise<unknown>
) => {
  await Promise.allSettled([loadSummary(), loadDetails()]);
};

export const accountInspectionApi = {
  getSchedule: (includeDetails = false) =>
    apiClient.get<AccountInspectionScheduleResponse>('/account-inspection/schedule', {
      params: { details: includeDetails ? 1 : 0 },
    }),
  getStatus: (options: boolean | AccountInspectionDetailsOptions = false, signal?: AbortSignal) =>
    apiClient.get<AccountInspectionScheduleResponse>('/account-inspection/status', {
      params: buildAccountInspectionDetailParams(options),
      signal,
    }),
  updateSchedule: (schedule: AccountInspectionSchedule) =>
    apiClient.put<AccountInspectionScheduleResponse>('/account-inspection/schedule', schedule, {
      params: { details: 0 },
    }),
  runNow: () => apiClient.post<AccountInspectionScheduleResponse>('/account-inspection/run', {}, {
    params: { details: 0 },
  }),
  inspectOne: (item: AccountInspectionInspectOneItem, options: boolean | AccountInspectionDetailsOptions = true) =>
    apiClient.post<AccountInspectionInspectOneResponse>('/account-inspection/inspect-one', { item }, {
      params: buildAccountInspectionDetailParams(options),
    }),
  inspectMany: (items: AccountInspectionInspectOneItem[], options: boolean | AccountInspectionDetailsOptions = true) =>
    apiClient.post<AccountInspectionInspectManyResponse>('/account-inspection/inspect-many', { items }, {
      params: buildAccountInspectionDetailParams(options),
      timeout: ACCOUNT_INSPECTION_BULK_RECHECK_TIMEOUT_MS,
    }),
  pause: () => apiClient.post<AccountInspectionScheduleResponse>('/account-inspection/pause', {}, {
    params: { details: 0 },
  }),
  resume: () => apiClient.post<AccountInspectionScheduleResponse>('/account-inspection/resume', {}, {
    params: { details: 0 },
  }),
  stop: () => apiClient.post<AccountInspectionScheduleResponse>('/account-inspection/stop', {}, {
    params: { details: 0 },
  }),
  executeActions: (items: AccountInspectionActionItem[], options: boolean | AccountInspectionDetailsOptions = true) =>
    apiClient.post<AccountInspectionActionsResponse>('/account-inspection/actions', { items }, {
      params: buildAccountInspectionDetailParams(options),
    }),
  startBatch: (kind: AccountInspectionBatchKind, scope: AccountInspectionBatchScope, clientRequestId: string) =>
    apiClient.post<AccountInspectionBatchOperation>('/account-inspection/batches', { kind, scope, clientRequestId }),
  getBatch: (operationId: string) =>
    apiClient.get<AccountInspectionBatchOperation>(`/account-inspection/batches/${encodeURIComponent(operationId)}`),
  retryExecuteBatch: (operationId: string) =>
    apiClient.post<AccountInspectionBatchOperation>(
      `/account-inspection/batches/${encodeURIComponent(operationId)}/retry-execute`,
      {}
    ),
};
