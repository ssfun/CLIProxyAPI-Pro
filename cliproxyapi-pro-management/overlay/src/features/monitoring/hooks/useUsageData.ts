import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores/useAuthStore';
import { computeApiUrl } from '@/utils/connection';
import { isRecordValue } from '@/utils/quota';
import {
  loadLegacyModelPrices,
  loadModelPricesFromSqlite,
  saveModelPricesToSqlite,
  type ModelPrice,
} from '@/utils/usage';
import { processUsageSseBlocks } from '../usageStream';

export interface UsagePayload {
  total_requests?: number;
  success_count?: number;
  failure_count?: number;
  total_tokens?: number;
  latest_id?: number;
  generation?: number;
  reset_at_ms?: number;
  details_count?: number;
  details_limit?: number;
  details_limited?: boolean;
  matched_total?: number;
  snapshot_max_id?: number;
  page_cursor?: string;
  next_cursor?: string;
  has_more?: boolean;
  apis?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface UseUsageDataReturn {
  usage: UsagePayload | null;
  loading: boolean;
  refreshing: boolean;
  error: string;
  lastRefreshedAt: Date | null;
  lastEventAt: Date | null;
  latestId: number;
  syncStatus: UsageSyncStatus;
  modelPrices: Record<string, ModelPrice>;
  setModelPrices: (prices: Record<string, ModelPrice>) => void;
  refreshUsage: () => Promise<void>;
  loadEventPage: (filters: UsageEventPageFilters) => Promise<UsageEventPage>;
}

export type UsageEventStatusFilter = 'all' | 'success' | 'failed';

export type UsageEventPageFilters = {
  cursor?: string;
  signal?: AbortSignal;
  fromMs?: number;
  toMs?: number;
  provider?: string;
  model?: string;
  authIndex?: string;
  searchAuthIndexes?: string;
  apiKeyHash?: string;
  status?: UsageEventStatusFilter;
  search?: string;
  limit?: number;
};

export type UsageEventPage = {
  usage: UsagePayload;
  matchedTotal: number;
  pageCursor: string;
  nextCursor: string;
  hasMore: boolean;
  snapshotMaxId: number;
};

export type UsageSyncStatus = 'loading' | 'syncing' | 'live' | 'reconnecting' | 'paused' | 'error';

const toNumber = (value: unknown) => (Number.isFinite(Number(value)) ? Number(value) : 0);

type UsageModelEntry = { details?: unknown[]; [key: string]: unknown };
type UsageApiEntry = { models?: Record<string, UsageModelEntry>; [key: string]: unknown };
type UsageDetailRef = {
  endpoint: string;
  model: string;
  detail: unknown;
  timestampMs: number;
  index: number;
};

const asUsageApiEntry = (value: unknown): UsageApiEntry =>
  isRecordValue(value) ? (value as UsageApiEntry) : {};

const USAGE_DETAIL_RETENTION_LIMIT = 5000;
const USAGE_DETAIL_RETENTION_TRIM_BUFFER = 500;

const usageDetailKey = (endpoint: string, model: string) => `${endpoint}\n${model}`;

const countUsagePayloadDetails = (payload: UsagePayload | null) => {
  if (!payload?.apis) return 0;
  let count = 0;
  Object.values(payload.apis).forEach((apiEntry) => {
    Object.values(asUsageApiEntry(apiEntry).models ?? {}).forEach((modelEntry) => {
      count += Array.isArray(modelEntry.details) ? modelEntry.details.length : 0;
    });
  });
  return count;
};

const readDetailTimestampMs = (detail: unknown) => {
  if (!isRecordValue(detail)) return 0;
  const explicitTimestampMs = Number(detail.__timestampMs ?? detail.timestamp_ms ?? detail.timestampMs);
  if (Number.isFinite(explicitTimestampMs) && explicitTimestampMs > 0) return explicitTimestampMs;
  const timestamp = typeof detail.timestamp === 'string' ? Date.parse(detail.timestamp) : NaN;
  return Number.isFinite(timestamp) ? timestamp : 0;
};

const readDetailId = (detail: unknown) => {
  if (!isRecordValue(detail)) return 0;
  return toNumber(detail.id ?? detail.event_id ?? detail.eventId);
};

const readDetailTotalTokens = (detail: unknown) => {
  if (!isRecordValue(detail) || !isRecordValue(detail.tokens)) return 0;
  return toNumber(detail.tokens.total_tokens);
};

const readDetailFailed = (detail: unknown) => isRecordValue(detail) && Boolean(detail.failed);

const findLatestEventTimestampMs = (payload: UsagePayload | null) => {
  let latestTimestampMs = 0;
  Object.values(payload?.apis ?? {}).forEach((apiEntry) => {
    Object.values(asUsageApiEntry(apiEntry).models ?? {}).forEach((modelEntry) => {
      (Array.isArray(modelEntry.details) ? modelEntry.details : []).forEach((detail) => {
        latestTimestampMs = Math.max(latestTimestampMs, readDetailTimestampMs(detail));
      });
    });
  });
  return latestTimestampMs;
};

const filterUsagePayloadAfterId = (payload: UsagePayload | null, afterId: number): UsagePayload | null => {
  if (!payload?.apis || afterId <= 0) return payload;
  if (toNumber(payload.latest_id) <= afterId) return null;

  let hasEventIds = false;
  let latestId = afterId;
  let totalRequests = 0;
  let successCount = 0;
  let failureCount = 0;
  let totalTokens = 0;
  const apis: Record<string, unknown> = {};

  Object.entries(payload.apis).forEach(([endpoint, apiEntry]) => {
    const existingApi = asUsageApiEntry(apiEntry);
    const models: Record<string, UsageModelEntry> = {};
    Object.entries(existingApi.models ?? {}).forEach(([model, modelEntry]) => {
      const details = (Array.isArray(modelEntry.details) ? modelEntry.details : []).filter((detail) => {
        const detailId = readDetailId(detail);
        if (detailId <= 0) return false;
        hasEventIds = true;
        if (detailId <= afterId) return false;
        latestId = Math.max(latestId, detailId);
        totalRequests += 1;
        if (readDetailFailed(detail)) {
          failureCount += 1;
        } else {
          successCount += 1;
        }
        totalTokens += readDetailTotalTokens(detail);
        return true;
      });
      if (details.length > 0) {
        models[model] = { ...modelEntry, details };
      }
    });
    if (Object.keys(models).length > 0) {
      apis[endpoint] = { ...existingApi, models };
    }
  });

  if (!hasEventIds) return payload;
  if (totalRequests === 0) return null;
  return {
    ...payload,
    total_requests: totalRequests,
    success_count: successCount,
    failure_count: failureCount,
    total_tokens: totalTokens,
    latest_id: latestId,
    details_count: totalRequests,
    apis,
  };
};

const isUsageDetailRefAhead = (left: UsageDetailRef, right: UsageDetailRef) =>
  left.timestampMs > right.timestampMs ||
  (left.timestampMs === right.timestampMs && left.index > right.index);

const isSameUsageDetailRefPosition = (left: UsageDetailRef, right: UsageDetailRef) =>
  left.timestampMs === right.timestampMs && left.index === right.index;

const compareUsageDetailRefsDescending = (left: UsageDetailRef, right: UsageDetailRef) =>
  right.timestampMs - left.timestampMs || right.index - left.index;

const partitionUsageDetailRefs = (values: UsageDetailRef[], left: number, right: number, pivotIndex: number) => {
  const pivotValue = values[pivotIndex];
  [values[pivotIndex], values[right]] = [values[right], values[pivotIndex]];
  let storeIndex = left;
  for (let index = left; index < right; index += 1) {
    if (isUsageDetailRefAhead(values[index], pivotValue)) {
      [values[storeIndex], values[index]] = [values[index], values[storeIndex]];
      storeIndex += 1;
    }
  }
  [values[right], values[storeIndex]] = [values[storeIndex], values[right]];
  return storeIndex;
};

const selectUsageDetailRef = (values: UsageDetailRef[], targetIndex: number) => {
  let left = 0;
  let right = values.length - 1;
  while (left <= right) {
    const pivotIndex = left + Math.floor((right - left) / 2);
    const nextPivotIndex = partitionUsageDetailRefs(values, left, right, pivotIndex);
    if (nextPivotIndex === targetIndex) return values[nextPivotIndex];
    if (targetIndex < nextPivotIndex) {
      right = nextPivotIndex - 1;
    } else {
      left = nextPivotIndex + 1;
    }
  }
  return values[targetIndex] ?? 0;
};

const trimUsagePayloadDetails = (payload: UsagePayload | null): UsagePayload | null => {
  if (!payload?.apis) return payload;

  const detailsCount = countUsagePayloadDetails(payload);
  if (detailsCount <= USAGE_DETAIL_RETENTION_LIMIT + USAGE_DETAIL_RETENTION_TRIM_BUFFER) {
    return {
      ...payload,
      details_count: detailsCount,
      details_limited: Boolean(payload.details_limited),
    };
  }

  const refs: UsageDetailRef[] = [];
  let index = 0;
  Object.entries(payload.apis).forEach(([endpoint, apiEntry]) => {
    Object.entries(asUsageApiEntry(apiEntry).models ?? {}).forEach(([model, modelEntry]) => {
      const details = Array.isArray(modelEntry.details) ? modelEntry.details : [];
      details.forEach((detail) => {
        refs.push({ endpoint, model, detail, timestampMs: readDetailTimestampMs(detail), index });
        index += 1;
      });
    });
  });

  const cutoffRef = selectUsageDetailRef([...refs], USAGE_DETAIL_RETENTION_LIMIT - 1);
  const retainedRefs = refs
    .filter((ref) => isUsageDetailRefAhead(ref, cutoffRef) || isSameUsageDetailRefPosition(ref, cutoffRef))
    .sort(compareUsageDetailRefsDescending);
  const retained = new Map<string, unknown[]>();
  let retainedCount = 0;

  retainedRefs.forEach((ref) => {
    const key = usageDetailKey(ref.endpoint, ref.model);
    const details = retained.get(key) ?? [];
    details.push(ref.detail);
    retained.set(key, details);
    retainedCount += 1;
  });

  const apis: Record<string, unknown> = {};
  Object.entries(payload.apis).forEach(([endpoint, apiEntry]) => {
    const existingApi = asUsageApiEntry(apiEntry);
    const models: Record<string, UsageModelEntry> = {};
    Object.entries(existingApi.models ?? {}).forEach(([model, modelEntry]) => {
      const details = retained.get(usageDetailKey(endpoint, model));
      if (!details || details.length === 0) return;
      models[model] = { ...modelEntry, details: [...details].reverse() };
    });
    if (Object.keys(models).length > 0) {
      apis[endpoint] = { ...existingApi, models };
    }
  });

  return {
    ...payload,
    details_count: retainedCount,
    details_limited: true,
    apis,
  };
};

const mergeUsagePayload = (current: UsagePayload | null, next: UsagePayload | null): UsagePayload | null => {
  if (!next) return current;
  if (!current) return trimUsagePayloadDetails(next);

  const currentLatestId = toNumber(current.latest_id);
  const nextLatestId = toNumber(next.latest_id);
  if (nextLatestId <= currentLatestId) return current;

  let mergedApis = current.apis;
  Object.entries(next.apis ?? {}).forEach(([endpoint, apiEntry]) => {
    const existingApi = asUsageApiEntry(current.apis?.[endpoint]);
    const nextApi = asUsageApiEntry(apiEntry);
    const models: Record<string, UsageModelEntry> = { ...(existingApi.models ?? {}) };

    Object.entries(nextApi.models ?? {}).forEach(([model, modelEntry]) => {
      const existingModel = models[model];
      models[model] = {
        ...(existingModel ?? {}),
        ...(modelEntry ?? {}),
        details: [
          ...(Array.isArray(existingModel?.details) ? existingModel.details : []),
          ...(Array.isArray(modelEntry?.details) ? modelEntry.details : []),
        ],
      };
    });

    const writableApis: Record<string, unknown> = mergedApis === current.apis ? { ...(current.apis ?? {}) } : (mergedApis ?? {});
    mergedApis = writableApis;
    writableApis[endpoint] = {
      ...existingApi,
      ...nextApi,
      models,
    };
  });

  return trimUsagePayloadDetails({
    ...current,
    total_requests: toNumber(current.total_requests) + toNumber(next.total_requests),
    success_count: toNumber(current.success_count) + toNumber(next.success_count),
    failure_count: toNumber(current.failure_count) + toNumber(next.failure_count),
    total_tokens: toNumber(current.total_tokens) + toNumber(next.total_tokens),
    latest_id: nextLatestId,
    generation: next.generation ?? current.generation,
    reset_at_ms: next.reset_at_ms ?? current.reset_at_ms,
    details_count: toNumber(current.details_count) + toNumber(next.details_count),
    details_limit: Math.max(toNumber(current.details_limit), toNumber(next.details_limit)),
    details_limited: Boolean(current.details_limited || next.details_limited),
    apis: mergedApis,
  });
};

const buildUsageStreamUrl = (apiBase: string, afterId: number, generation: number) => {
  const base = computeApiUrl(apiBase);
  if (!base) return '';
  const url = new URL(`${base}/usage/stream`);
  url.searchParams.set('after_id', String(Math.max(afterId, 0)));
  if (generation > 0) url.searchParams.set('generation', String(generation));
  return url.toString();
};

const nextUsageReconnectDelay = (currentDelay: number) => Math.min(currentDelay * 2, 30000);

type MutableRef<T> = { current: T };

type UsageStateWriter = {
  setUsage: Dispatch<SetStateAction<UsagePayload | null>>;
  setLoading: (loading: boolean) => void;
  setError: (error: string) => void;
  setLastRefreshedAt: (date: Date | null) => void;
  setLastEventAt: (date: Date | null) => void;
  setLatestId: (latestId: number) => void;
  setSnapshotReady: (ready: boolean) => void;
  setSyncStatus: (status: UsageSyncStatus) => void;
};

const USAGE_INITIAL_LIMIT = 1000;
const USAGE_INCREMENTAL_LIMIT = 3000;
const USAGE_INCREMENTAL_FLUSH_DELAY_MS = 150;
const USAGE_EVENT_PAGE_LIMIT = 100;

const loadUsageEventPage = async (filters: UsageEventPageFilters): Promise<UsageEventPage> => {
  const cursor = filters.cursor?.trim() ?? '';
  const payload = await apiClient.get<UsagePayload>('/usage/events', {
    signal: filters.signal,
    params: cursor
      ? { cursor, limit: filters.limit ?? USAGE_EVENT_PAGE_LIMIT }
      : {
          direction: 'before',
          limit: filters.limit ?? USAGE_EVENT_PAGE_LIMIT,
          from_ms: filters.fromMs && Number.isFinite(filters.fromMs) ? filters.fromMs : undefined,
          to_ms: filters.toMs && Number.isFinite(filters.toMs) ? filters.toMs : undefined,
          provider: filters.provider?.trim() || undefined,
          model: filters.model?.trim() || undefined,
          auth_index: filters.authIndex?.trim() || undefined,
          search_auth_indexes: filters.searchAuthIndexes?.trim() || undefined,
          api_key_hash: filters.apiKeyHash?.trim() || undefined,
          status: filters.status && filters.status !== 'all' ? filters.status : undefined,
          search: filters.search?.trim() || undefined,
        },
  });
  return {
    usage: payload ?? { apis: {} },
    matchedTotal: toNumber(payload?.matched_total),
    pageCursor: typeof payload?.page_cursor === 'string' ? payload.page_cursor : cursor,
    nextCursor: typeof payload?.next_cursor === 'string' ? payload.next_cursor : '',
    hasMore: Boolean(payload?.has_more),
    snapshotMaxId: toNumber(payload?.snapshot_max_id),
  };
};

const loadUsageSnapshot = async ({
  requestIdRef,
  latestIdRef,
  setUsage,
  setLoading,
  setError,
  setLastRefreshedAt,
  setLastEventAt,
  setLatestId,
  setSnapshotReady,
  setSyncStatus,
  syncGenerationRef,
  datasetGenerationRef,
}: UsageStateWriter & {
  requestIdRef: MutableRef<number>;
  latestIdRef: MutableRef<number>;
  syncGenerationRef: MutableRef<number>;
  datasetGenerationRef: MutableRef<number>;
}): Promise<boolean> => {
  const syncGeneration = syncGenerationRef.current + 1;
  syncGenerationRef.current = syncGeneration;
  const requestId = requestIdRef.current + 1;
  requestIdRef.current = requestId;
  setSnapshotReady(false);
  setLoading(true);
  setSyncStatus('loading');
  setError('');

  try {
    const payload = await apiClient.get<UsagePayload>('/usage', { params: { limit: USAGE_INITIAL_LIMIT } });
    if (requestIdRef.current !== requestId || syncGenerationRef.current !== syncGeneration) return false;
    const nextLatestId = toNumber(payload?.latest_id);
    datasetGenerationRef.current = toNumber(payload?.generation);
    latestIdRef.current = nextLatestId;
    setLatestId(nextLatestId);
    setUsage(trimUsagePayloadDetails(payload ?? null));
    setLastRefreshedAt(new Date());
    const latestEventTimestampMs = findLatestEventTimestampMs(payload ?? null);
    setLastEventAt(latestEventTimestampMs > 0 ? new Date(latestEventTimestampMs) : null);
    setSyncStatus('reconnecting');
    return true;
  } catch (err) {
    if (requestIdRef.current !== requestId || syncGenerationRef.current !== syncGeneration) return false;
    setError(err instanceof Error ? err.message : String(err));
    setSyncStatus('error');
    return false;
  } finally {
    if (requestIdRef.current === requestId && syncGenerationRef.current === syncGeneration) {
      setLoading(false);
      setSnapshotReady(true);
    }
  }
};

const loadUsageIncrementalSnapshot = async ({
  latestIdRef,
  datasetGenerationRef,
  incrementalLoadingRef,
  incrementalPendingRef,
  loadUsage,
  applyUsagePayload,
  setSyncStatus,
}: {
  latestIdRef: MutableRef<number>;
  datasetGenerationRef: MutableRef<number>;
  incrementalLoadingRef: MutableRef<boolean>;
  incrementalPendingRef: MutableRef<boolean>;
  loadUsage: () => Promise<boolean>;
  applyUsagePayload: (payload: UsagePayload | null) => boolean;
  setSyncStatus?: (status: UsageSyncStatus) => void;
}): Promise<boolean> => {
  if (incrementalLoadingRef.current) {
    incrementalPendingRef.current = true;
    return true;
  }

  incrementalLoadingRef.current = true;
  let success = true;
  try {
    do {
      incrementalPendingRef.current = false;
      const afterId = latestIdRef.current;
      if (afterId <= 0) {
        success = await loadUsage();
        continue;
      }

      try {
        setSyncStatus?.('syncing');
        const payload = await apiClient.get<UsagePayload>('/usage/events', {
          params: {
            after_id: afterId,
            limit: USAGE_INCREMENTAL_LIMIT,
            generation: datasetGenerationRef.current || undefined,
          },
        });
        const applied = applyUsagePayload(payload ?? null);
        if (!applied) {
          success = await loadUsage();
          continue;
        }
        if (payload?.details_limited) {
          incrementalPendingRef.current = true;
        }
      } catch {
        success = await loadUsage();
      }
    } while (incrementalPendingRef.current);
  } finally {
    incrementalLoadingRef.current = false;
  }
  return success;
};

const connectUsageStream = async ({
  apiBase,
  managementKey,
  signal,
  latestIdRef,
  datasetGenerationRef,
  applyUsagePayload,
  loadUsageIncremental,
  reloadUsage,
  onOpen,
}: {
  apiBase: string;
  managementKey: string;
  signal: AbortSignal;
  latestIdRef: MutableRef<number>;
  datasetGenerationRef: MutableRef<number>;
  applyUsagePayload: (payload: UsagePayload | null) => boolean;
  loadUsageIncremental: () => Promise<boolean>;
  reloadUsage: () => Promise<boolean>;
  onOpen: () => void;
}) => {
  const decoder = new TextDecoder();
  let buffer = '';
  const url = buildUsageStreamUrl(apiBase, latestIdRef.current, datasetGenerationRef.current);
  if (!url) return;

  const response = await fetch(url, {
    headers: { Authorization: `Bearer ${managementKey}` },
    signal,
  });
  if (!response.ok || !response.body) {
    throw new Error(`Usage stream failed: ${response.status}`);
  }
  onOpen();

  const reader = response.body.getReader();
  while (!signal.aborted) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    buffer = buffer.replace(/\r\n/g, '\n');
    const parts = buffer.split('\n\n');
    buffer = parts.pop() ?? '';
    const keepStream = processUsageSseBlocks(parts, {
      currentGeneration: () => datasetGenerationRef.current,
      setGeneration: (generation) => { datasetGenerationRef.current = generation; },
      applyUsage: (payload) => { applyUsagePayload(payload as UsagePayload); },
      reloadUsage: () => { void reloadUsage(); },
      loadIncremental: () => { void loadUsageIncremental(); },
    });
    if (!keepStream) return;
  }
};

export function useUsageData(): UseUsageDataReturn {
  const [usage, setUsage] = useState<UsagePayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [lastRefreshedAt, setLastRefreshedAt] = useState<Date | null>(null);
  const [lastEventAt, setLastEventAt] = useState<Date | null>(null);
  const [latestId, setLatestId] = useState(0);
  const [syncStatus, setSyncStatus] = useState<UsageSyncStatus>('loading');
  const [snapshotReady, setSnapshotReady] = useState(false);
  const [pageVisible, setPageVisible] = useState(() => typeof document === 'undefined' || document.visibilityState !== 'hidden');
  const [modelPrices, setModelPricesState] = useState<Record<string, ModelPrice>>({});
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const connectionKey = `${apiBase}\n${managementKey}`;
  const requestIdRef = useRef(0);
  const latestIdRef = useRef(0);
  const datasetGenerationRef = useRef(0);
  const incrementalLoadingRef = useRef(false);
  const incrementalPendingRef = useRef(false);
  const incrementalPromiseRef = useRef<Promise<boolean> | null>(null);
  const refreshingRef = useRef(false);
  const syncGenerationRef = useRef(0);
  const pendingUsagePayloadRef = useRef<UsagePayload | null>(null);
  const pendingUsageFlushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearPendingUsagePayload = useCallback(() => {
    pendingUsagePayloadRef.current = null;
    if (pendingUsageFlushTimerRef.current) {
      clearTimeout(pendingUsageFlushTimerRef.current);
      pendingUsageFlushTimerRef.current = null;
    }
  }, []);

  const flushPendingUsagePayload = useCallback(() => {
    if (pendingUsageFlushTimerRef.current) {
      clearTimeout(pendingUsageFlushTimerRef.current);
      pendingUsageFlushTimerRef.current = null;
    }
    const payload = pendingUsagePayloadRef.current;
    pendingUsagePayloadRef.current = null;
    if (!payload) return;
    setUsage((current) => mergeUsagePayload(current, payload));
    setLastRefreshedAt(new Date());
    const latestEventTimestampMs = findLatestEventTimestampMs(payload);
    if (latestEventTimestampMs > 0) {
      setLastEventAt(new Date(latestEventTimestampMs));
    }
  }, []);

  const loadUsage = useCallback(() => {
    if (connectionStatus !== 'connected' || !apiBase || !managementKey) {
      return Promise.resolve(false);
    }
    clearPendingUsagePayload();
    return loadUsageSnapshot({
      requestIdRef,
      latestIdRef,
      setUsage,
      setLoading,
      setError,
      setLastRefreshedAt,
      setLastEventAt,
      setLatestId,
      setSnapshotReady,
      setSyncStatus,
      syncGenerationRef,
      datasetGenerationRef,
    });
  }, [apiBase, clearPendingUsagePayload, connectionStatus, managementKey]);

  const applyUsagePayload = useCallback((payload: UsagePayload | null) => {
    const nextGeneration = toNumber(payload?.generation);
    if (nextGeneration > 0 && datasetGenerationRef.current > 0 && nextGeneration !== datasetGenerationRef.current) {
      return false;
    }
    if (nextGeneration > 0) datasetGenerationRef.current = nextGeneration;
    const filteredPayload = filterUsagePayloadAfterId(payload, latestIdRef.current);
    const nextLatestId = toNumber(filteredPayload?.latest_id);
    if (!filteredPayload || nextLatestId <= latestIdRef.current) return true;
    latestIdRef.current = nextLatestId;
    setLatestId(nextLatestId);
    setError('');
    pendingUsagePayloadRef.current = pendingUsagePayloadRef.current
      ? mergeUsagePayload(pendingUsagePayloadRef.current, filteredPayload)
      : trimUsagePayloadDetails(filteredPayload);
    if (pendingUsageFlushTimerRef.current) return true;
    pendingUsageFlushTimerRef.current = setTimeout(flushPendingUsagePayload, USAGE_INCREMENTAL_FLUSH_DELAY_MS);
    return true;
  }, [flushPendingUsagePayload]);

  const loadUsageIncremental = useCallback(() => {
    if (incrementalPromiseRef.current) {
      incrementalPendingRef.current = true;
      return incrementalPromiseRef.current;
    }
    const syncGeneration = syncGenerationRef.current;
    setSyncStatus('syncing');
    const promise = loadUsageIncrementalSnapshot({
      latestIdRef,
      datasetGenerationRef,
      incrementalLoadingRef,
      incrementalPendingRef,
      loadUsage: () => syncGenerationRef.current === syncGeneration ? loadUsage() : Promise.resolve(false),
      applyUsagePayload: (payload) => syncGenerationRef.current === syncGeneration && applyUsagePayload(payload),
      setSyncStatus,
    }).then((success) => {
      if (success && syncGenerationRef.current === syncGeneration) {
        setSyncStatus(pageVisible ? 'live' : 'paused');
      }
      return success;
    }).finally(() => {
      if (incrementalPromiseRef.current === promise) {
        incrementalPromiseRef.current = null;
      }
    });
    incrementalPromiseRef.current = promise;
    return promise;
  }, [applyUsagePayload, loadUsage, pageVisible]);

  const refreshUsage = useCallback(async () => {
    if (refreshingRef.current || connectionStatus !== 'connected' || !apiBase || !managementKey) return;
    const syncGeneration = syncGenerationRef.current;
    refreshingRef.current = true;
    setRefreshing(true);
    try {
      const [success] = await Promise.all([
        loadUsage(),
        loadModelPricesFromSqlite()
          .then((prices) => {
            if (syncGenerationRef.current === syncGeneration) setModelPricesState(prices);
          })
          .catch((err) => console.error('Failed to refresh model prices from sqlite:', err)),
      ]);
      if (success && syncGenerationRef.current === syncGeneration) {
        setLastRefreshedAt(new Date());
        setError('');
      }
    } catch (err) {
      if (syncGenerationRef.current === syncGeneration) {
        setError(err instanceof Error ? err.message : String(err));
        setSyncStatus('error');
      }
    } finally {
      if (syncGenerationRef.current === syncGeneration) {
        refreshingRef.current = false;
        setRefreshing(false);
      }
    }
  }, [apiBase, connectionStatus, loadUsage, managementKey]);

  useEffect(() => {
    let cancelled = false;
    syncGenerationRef.current += 1;
    requestIdRef.current += 1;
    latestIdRef.current = 0;
    datasetGenerationRef.current = 0;
    incrementalLoadingRef.current = false;
    incrementalPendingRef.current = false;
    incrementalPromiseRef.current = null;
    refreshingRef.current = false;
    clearPendingUsagePayload();
    setUsage(null);
    setLatestId(0);
    setLastRefreshedAt(null);
    setLastEventAt(null);
    setError('');
    setRefreshing(false);
    setSnapshotReady(false);

    if (connectionStatus !== 'connected' || !apiBase || !managementKey) {
      setLoading(false);
      setSyncStatus('paused');
      setModelPricesState({});
      return () => {
        cancelled = true;
        syncGenerationRef.current += 1;
        requestIdRef.current += 1;
        clearPendingUsagePayload();
      };
    }

    setLoading(true);
    setSyncStatus('loading');
    const legacyPrices = loadLegacyModelPrices();
    setModelPricesState(legacyPrices);

    const syncModelPrices = async () => {
      try {
        const sqlitePrices = await loadModelPricesFromSqlite();
        if (cancelled) return;
        if (Object.keys(sqlitePrices).length > 0) {
          setModelPricesState(sqlitePrices);
          return;
        }
        if (Object.keys(legacyPrices).length > 0) {
          await saveModelPricesToSqlite(legacyPrices);
        }
      } catch (err) {
        console.error('Failed to sync model prices with sqlite:', err);
      }
    };

    void syncModelPrices();
    void loadUsage();

    return () => {
      cancelled = true;
      syncGenerationRef.current += 1;
      requestIdRef.current += 1;
      clearPendingUsagePayload();
    };
  }, [apiBase, clearPendingUsagePayload, connectionKey, connectionStatus, loadUsage, managementKey]);

  useEffect(() => clearPendingUsagePayload, [clearPendingUsagePayload]);

  useEffect(() => {
    const handleVisibilityChange = () => setPageVisible(document.visibilityState !== 'hidden');
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => document.removeEventListener('visibilitychange', handleVisibilityChange);
  }, []);

  useEffect(() => {
    if (!snapshotReady || !pageVisible || connectionStatus !== 'connected' || !apiBase || !managementKey) {
      if (!pageVisible) setSyncStatus('paused');
      return;
    }

    const controller = new AbortController();
    const syncGeneration = syncGenerationRef.current;
    let reconnectDelay = 1000;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    const connect = async () => {
      setSyncStatus(reconnectDelay > 1000 ? 'reconnecting' : 'syncing');
      try {
        await connectUsageStream({
          apiBase,
          managementKey,
          signal: controller.signal,
          latestIdRef,
          datasetGenerationRef,
          applyUsagePayload: (payload) => {
            if (syncGenerationRef.current === syncGeneration) {
              return applyUsagePayload(payload);
            }
            return false;
          },
          loadUsageIncremental: async () => {
            if (syncGenerationRef.current === syncGeneration) {
              return loadUsageIncremental();
            }
            return false;
          },
          reloadUsage: async () => {
            if (syncGenerationRef.current === syncGeneration) {
              return loadUsage();
            }
            return false;
          },
          onOpen: () => {
            if (syncGenerationRef.current === syncGeneration) {
              setSyncStatus('live');
            }
          },
        });
        reconnectDelay = 1000;
      } catch (err) {
        if (!controller.signal.aborted) {
          console.warn('Usage SSE stream disconnected:', err);
          setSyncStatus('reconnecting');
        }
      }

      if (!controller.signal.aborted) {
        timeoutId = setTimeout(() => {
          void (async () => {
            if (syncGenerationRef.current === syncGeneration) {
              await loadUsageIncremental();
            }
            if (!controller.signal.aborted && syncGenerationRef.current === syncGeneration) {
              await connect();
            }
          })();
        }, reconnectDelay);
        reconnectDelay = nextUsageReconnectDelay(reconnectDelay);
      }
    };

    void connect();
    return () => {
      controller.abort();
      if (timeoutId) clearTimeout(timeoutId);
    };
  }, [apiBase, applyUsagePayload, connectionStatus, loadUsage, loadUsageIncremental, managementKey, pageVisible, snapshotReady]);

  const setModelPrices = useCallback((prices: Record<string, ModelPrice>) => {
    setModelPricesState(prices);
    void saveModelPricesToSqlite(prices).catch((err) => {
      console.error('Failed to save model prices to sqlite:', err);
    });
  }, []);

  const loadEventPage = useCallback((filters: UsageEventPageFilters) => loadUsageEventPage(filters), []);

  return {
    usage,
    loading,
    refreshing,
    error,
    lastRefreshedAt,
    lastEventAt,
    latestId,
    syncStatus,
    modelPrices,
    setModelPrices,
    refreshUsage,
    loadEventPage,
  };
}
