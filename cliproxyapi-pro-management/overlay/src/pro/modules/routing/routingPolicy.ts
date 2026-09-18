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
}

export interface SchedulingBoardAccount {
  provider: string;
  authId: string;
  authIndex: string;
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

type SchedulingBoardRawResponse = {
  generatedAt?: number;
  summary?: Partial<SchedulingBoardSummary> | null;
  accounts?: SchedulingBoardAccount[] | null;
};

const emptySummary = (): SchedulingBoardSummary => ({
  blocked: 0,
  quota: 0,
  authTransient: 0,
  recheck: 0,
  overlap: 0,
  excluded: 0,
});

export const normalizeSchedulingBoardResponse = (
  response: SchedulingBoardRawResponse | null | undefined
): SchedulingBoardResponse => ({
  generatedAt: Number(response?.generatedAt) || 0,
  summary: {
    ...emptySummary(),
    ...(response?.summary ?? {}),
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
};

export const schedulingBoardModelsLabel = (
  account: Pick<SchedulingBoardAccount, 'scope' | 'models'>,
  allModelsLabel: string,
  emptyLabel = '-'
): string => {
  if (account.scope === 'credential') return allModelsLabel;
  return account.models?.join(', ') || emptyLabel;
};
