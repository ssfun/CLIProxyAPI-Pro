import { apiClient } from '@/services/api/client';

export type SchedulingBoardBucket = 'quota' | 'authTransient' | 'recheck' | 'overlap';
export type SchedulingBoardKind = 'quota' | 'auth' | 'transient' | 'model';
export type SchedulingBoardResume =
  | 'auto-expire'
  | 'await-state-change'
  | 'refresh-token'
  | 'reauthenticate'
  | 'recheck-quota'
  | 'probe-request'
  | 'manual'
  | 'multiple';
export type SchedulingBoardScope = 'credential' | 'model';

export type SchedulingBoardResumeTone = 'good' | 'warning' | 'danger' | 'info' | 'neutral';

export interface SchedulingBoardSummary {
  blocked: number;
  quota: number;
  authTransient: number;
  recheck: number;
  overlap: number;
  excluded: number;
  nextRetryAt?: number;
  nextActionAt?: number;
  nextTransitionAt?: number;
}

export interface SchedulingBoardDetail {
  source: 'upstream' | 'inspection' | string;
  scope: SchedulingBoardScope | string;
  model?: string;
  kind: SchedulingBoardKind | string;
  resume: SchedulingBoardResume | string;
  retryAt?: number;
  reason: string;
  httpStatus?: number;
  revision?: string;
}

export interface SchedulingBoardAccount {
  provider: string;
  authId: string;
  authIndex: string;
  registrationEpoch?: string;
  fileName: string;
  scope: SchedulingBoardScope | string;
  models?: string[];
  kind: SchedulingBoardKind | string;
  bucket: SchedulingBoardBucket | string;
  sources: string[];
  resume: SchedulingBoardResume | string;
  retryAt?: number;
  nextActionAt?: number;
  nextTransitionAt?: number;
  remainingSeconds?: number;
  reason: string;
  httpStatus?: number;
  inspection: boolean;
  overlap: boolean;
  details: SchedulingBoardDetail[];
}

export interface SchedulingBoardResponse {
  generatedAt: number;
  summary: SchedulingBoardSummary;
  accounts: SchedulingBoardAccount[];
}

export interface SchedulingRecoveryRequest {
  authId: string;
  authIndex: string;
  registrationEpoch: string;
  source?: 'upstream' | 'inspection';
  model?: string;
  revision?: string;
}

export interface SchedulingRecoveryResult {
  before?: SchedulingBoardAccount;
  after?: SchedulingBoardAccount;
  steps?: string[];
  phases?: Array<{
    source: 'inspection' | 'upstream';
    model?: string;
    status: 'completed' | 'failed' | 'timeout' | 'skipped';
    error?: string;
  }>;
  test?: {
    success: boolean;
    model?: string;
    latency_ms?: number;
    error?: string;
    error_code?: string;
    http_status?: number;
  } | null;
}

export const schedulingRecoveryResultTone = (result: SchedulingRecoveryResult) => {
  if (result.test?.success === false || result.phases?.some((phase) =>
    phase.status === 'failed' || phase.status === 'timeout'
  )) return 'error' as const;
  if (result.after?.authId) return 'warning' as const;
  return 'success' as const;
};

type SchedulingBoardRawResponse = {
  generatedAt?: number;
  summary?: Partial<SchedulingBoardSummary> | null;
  accounts?: SchedulingBoardAccount[] | null;
};

export const normalizeSchedulingBoardResponse = (
  response: SchedulingBoardRawResponse | null | undefined
): SchedulingBoardResponse => ({
  generatedAt: Number(response?.generatedAt) || 0,
  summary: {
    blocked: Number(response?.summary?.blocked) || 0,
    quota: Number(response?.summary?.quota) || 0,
    authTransient: Number(response?.summary?.authTransient) || 0,
    recheck: Number(response?.summary?.recheck) || 0,
    overlap: Number(response?.summary?.overlap) || 0,
    excluded: Number(response?.summary?.excluded) || 0,
    nextRetryAt: Number(response?.summary?.nextRetryAt) || 0,
    nextActionAt: Number(response?.summary?.nextActionAt) || 0,
    nextTransitionAt: Number(response?.summary?.nextTransitionAt) || 0,
  },
  accounts: Array.isArray(response?.accounts) ? response.accounts : [],
});

export const routingPolicyApi = {
  async get(signal?: AbortSignal): Promise<SchedulingBoardResponse> {
    return normalizeSchedulingBoardResponse(
      await apiClient.get<SchedulingBoardRawResponse>('/routing-policy', { signal })
    );
  },
  check: (request: SchedulingRecoveryRequest) =>
    apiClient.post<SchedulingRecoveryResult>('/routing-policy/check', request, { timeout: 65000 }),
  release: (request: SchedulingRecoveryRequest) =>
    apiClient.post<SchedulingRecoveryResult>('/routing-policy/restrictions/release', request),
};

export const schedulingBoardModelsLabel = (
  account: Pick<SchedulingBoardAccount, 'scope' | 'models'>,
  allModelsLabel: string,
  emptyLabel = '-'
): string => {
  if (account.scope === 'credential') return allModelsLabel;
  return account.models?.join(', ') || emptyLabel;
};

export const schedulingBoardResumeTone = (
  resume: SchedulingBoardResume | string
): SchedulingBoardResumeTone => {
  switch (resume) {
    case 'auto-expire':
      return 'good';
    case 'recheck-quota':
      return 'info';
    case 'probe-request':
    case 'refresh-token':
    case 'multiple':
      return 'warning';
    case 'reauthenticate':
    case 'manual':
      return 'danger';
    case 'await-state-change':
    default:
      return 'neutral';
  }
};

export const formatRemainingTime = (
  retryAt: number | undefined,
  resume: string,
  t: (key: string, options?: Record<string, unknown>) => string,
  emptyText = '-',
  now = Date.now()
): string => {
  if (retryAt && retryAt > 0) {
    const diffSeconds = Math.ceil((retryAt - now) / 1000);

    if (diffSeconds > 0) {
      if (diffSeconds < 60) {
        return t('routing_policy.runtime.remaining_seconds', { count: diffSeconds });
      }
      const minutes = Math.ceil(diffSeconds / 60);
      if (minutes < 60) {
        return t('routing_policy.runtime.remaining_minutes', { count: minutes });
      }
      const hours = Math.ceil(minutes / 60);
      return t('routing_policy.runtime.remaining_hours', { count: hours, defaultValue: `${hours}h left` });
    }
    if (resume === 'action') return t('routing_policy.runtime.due_action');
    if (resume === 'recheck-quota') {
      return t('routing_policy.runtime.due_recheck');
    }
    if (resume === 'probe-request') {
      return t('routing_policy.runtime.due_probe');
    }
    return t('routing_policy.runtime.due_now');
  }
  return emptyText;
};

export const formatTimestamp = (
  value: number | undefined,
  locale: string,
  emptyText: string,
  compact = false
): string => {
  if (!value) return emptyText;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return emptyText;
  return new Intl.DateTimeFormat(locale, {
    year: compact ? undefined : 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: compact ? undefined : '2-digit',
  }).format(date);
};

export const formatTimeOnly = (
  value: number | undefined,
  locale: string,
  emptyText: string
): string => {
  if (!value) return emptyText;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return emptyText;
  return new Intl.DateTimeFormat(locale, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
};
