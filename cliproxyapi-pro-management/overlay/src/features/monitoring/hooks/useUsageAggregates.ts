import { useCallback, useEffect, useRef, useState } from 'react';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores/useAuthStore';
import { getRangeStartMs, type MonitoringTimeRange } from './useMonitoringData';

export type UsageAggregateBucket = {
  bucketStartMs: number;
  bucketStart: string;
  provider?: string;
  model?: string;
  endpoint?: string;
  authIndex?: string;
  apiKeyHash?: string;
  lastSeenAtMs?: number;
  totalRequests: number;
  successCount: number;
  failureCount: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  estimatedCost: number;
  avgLatencyMs?: number;
  avgTtftMs?: number;
};

type UsageAggregateResponse = {
  items?: UsageAggregateBucket[];
  latest_id?: number;
  snapshot_at_ms?: number;
};

export type UsageAggregates = {
  trend: UsageAggregateBucket[];
  models: UsageAggregateBucket[];
  apiKeys: UsageAggregateBucket[];
  providers: UsageAggregateBucket[];
  accounts: UsageAggregateBucket[];
  allSummary: UsageAggregateBucket[];
  recentDailySummary: UsageAggregateBucket[];
  latestId: number;
  snapshotAtMs: number;
  scopeTimeRange: MonitoringTimeRange;
  scopeApiKeyHash: string;
};

type UseUsageAggregatesParams = {
  latestId: number;
  timeRange: MonitoringTimeRange;
  apiKeyHash: string;
  enabled?: boolean;
};

type UseUsageAggregatesReturn = {
  data: UsageAggregates | null;
  loading: boolean;
  refreshing: boolean;
  error: string;
  refresh: () => Promise<void>;
};

const AGGREGATE_REFRESH_DEBOUNCE_MS = 1000;

const normalizeItems = (payload: UsageAggregateResponse | null | undefined) =>
  Array.isArray(payload?.items) ? payload.items : [];

export function useUsageAggregates({
  latestId,
  timeRange,
  apiKeyHash,
  enabled = true,
}: UseUsageAggregatesParams): UseUsageAggregatesReturn {
  const [data, setData] = useState<UsageAggregates | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [refreshNonce, setRefreshNonce] = useState(0);
  const requestIdRef = useRef(0);
  const queryGenerationRef = useRef(0);
  const lastFetchedAtRef = useRef(0);
  const refreshInFlightRef = useRef(false);
  const refreshPendingRef = useRef(false);
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const hasDataRef = useRef(false);
  const activeConnectionKeyRef = useRef('');
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const connectionKey = `${apiBase}\n${managementKey}`;
  const effectiveEnabled = enabled
    && connectionStatus === 'connected'
    && Boolean(apiBase)
    && Boolean(managementKey);

  const load = useCallback(async () => {
    if (!effectiveEnabled) return;
    if (refreshInFlightRef.current) {
      refreshPendingRef.current = true;
      return;
    }
    refreshInFlightRef.current = true;
    const queryGeneration = queryGenerationRef.current;
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    setRefreshing(true);
    setError('');

    const nowMs = Date.now();
    const rangeStartMs = Number.isFinite(getRangeStartMs(timeRange, nowMs))
      ? getRangeStartMs(timeRange, nowMs)
      : 0;
    const allTrendStart = new Date(nowMs);
    allTrendStart.setHours(0, 0, 0, 0);
    allTrendStart.setDate(allTrendStart.getDate() - 23);
    const todayStart = new Date(nowMs);
    todayStart.setHours(0, 0, 0, 0);
    const yesterdayStart = new Date(todayStart);
    yesterdayStart.setDate(yesterdayStart.getDate() - 1);
    const trendFromMs = timeRange === 'all' ? allTrendStart.getTime() : rangeStartMs;
    const interval = timeRange === 'today' ? 'hour' : 'day';
    const timezoneOffsetMinutes = -new Date().getTimezoneOffset();
    const trendParams: Record<string, string | number> = {
      from_ms: Math.max(trendFromMs, 0),
      to_ms: nowMs,
      interval,
      group_by: 'model',
      limit: 10000,
      timezone_offset_minutes: timezoneOffsetMinutes,
    };
    if (apiKeyHash !== 'all') {
      trendParams.api_key_hash = apiKeyHash;
    }
    const rankingParams = {
      from_ms: Math.max(rangeStartMs, 0),
      to_ms: nowMs,
      interval: 'all',
      limit: 10000,
      timezone_offset_minutes: timezoneOffsetMinutes,
    };

    try {
      const [trendPayload, accountPayload, apiKeyPayload, allSummaryPayload, recentDailySummaryPayload] = await Promise.all([
        apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
          params: trendParams,
        }),
        apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
          params: { ...rankingParams, group_by: 'auth_index,provider,model' },
        }),
        apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
          params: { ...rankingParams, group_by: 'api_key_hash,model' },
        }),
        apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
          params: {
            from_ms: 0,
            to_ms: nowMs,
            interval: 'all',
            group_by: 'model',
            limit: 10000,
            timezone_offset_minutes: timezoneOffsetMinutes,
          },
        }),
        apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
          params: {
            from_ms: yesterdayStart.getTime(),
            to_ms: nowMs,
            interval: 'day',
            group_by: 'model',
            limit: 10000,
            timezone_offset_minutes: timezoneOffsetMinutes,
          },
        }),
      ]);
      if (requestIdRef.current !== requestId || queryGenerationRef.current !== queryGeneration) return;
      const snapshotAtMs = Math.max(
        Number(trendPayload?.snapshot_at_ms) || 0,
        Number(accountPayload?.snapshot_at_ms) || 0,
        Number(apiKeyPayload?.snapshot_at_ms) || 0,
        Number(allSummaryPayload?.snapshot_at_ms) || 0,
        Number(recentDailySummaryPayload?.snapshot_at_ms) || 0
      );
      const accountItems = normalizeItems(accountPayload);
      const apiKeyItems = normalizeItems(apiKeyPayload);
      setData({
        trend: normalizeItems(trendPayload),
        models: apiKeyItems,
        apiKeys: apiKeyItems,
        providers: accountItems,
        accounts: accountItems,
        allSummary: normalizeItems(allSummaryPayload),
        recentDailySummary: normalizeItems(recentDailySummaryPayload),
        latestId: Math.min(
          Number(trendPayload?.latest_id) || 0,
          Number(accountPayload?.latest_id) || 0,
          Number(apiKeyPayload?.latest_id) || 0,
          Number(allSummaryPayload?.latest_id) || 0,
          Number(recentDailySummaryPayload?.latest_id) || 0
        ),
        snapshotAtMs,
        scopeTimeRange: timeRange,
        scopeApiKeyHash: apiKeyHash,
      });
      hasDataRef.current = true;
      lastFetchedAtRef.current = Date.now();
      setLoading(false);
    } catch (err) {
      if (requestIdRef.current !== requestId || queryGenerationRef.current !== queryGeneration) return;
      setError(err instanceof Error ? err.message : String(err));
      setLoading(false);
    } finally {
      if (requestIdRef.current === requestId) {
        refreshInFlightRef.current = false;
        setRefreshing(false);
        if (refreshPendingRef.current) {
          refreshPendingRef.current = false;
          setRefreshNonce((value) => value + 1);
        }
      }
    }
  }, [apiKeyHash, effectiveEnabled, timeRange]);

  const loadRef = useRef(load);

  useEffect(() => {
    loadRef.current = load;
  }, [load]);

  useEffect(() => {
    const connectionChanged = activeConnectionKeyRef.current !== connectionKey;
    activeConnectionKeyRef.current = connectionKey;
    queryGenerationRef.current += 1;
    if (connectionChanged || !effectiveEnabled) {
      requestIdRef.current += 1;
      refreshInFlightRef.current = false;
      refreshPendingRef.current = false;
      hasDataRef.current = false;
      setData(null);
    }
    lastFetchedAtRef.current = 0;
    refreshPendingRef.current = refreshInFlightRef.current;
    setError('');
    setLoading(effectiveEnabled && !hasDataRef.current);
    if (refreshTimerRef.current) {
      window.clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = null;
    }
    setRefreshNonce((value) => value + 1);
  }, [apiKeyHash, connectionKey, effectiveEnabled, timeRange]);

  useEffect(() => {
    if (!effectiveEnabled) {
      if (refreshTimerRef.current) {
        window.clearTimeout(refreshTimerRef.current);
        refreshTimerRef.current = null;
      }
      setLoading(false);
      return;
    }
    if (refreshTimerRef.current) return;
    refreshTimerRef.current = window.setTimeout(() => {
      refreshTimerRef.current = null;
      void loadRef.current();
    }, lastFetchedAtRef.current > 0 ? AGGREGATE_REFRESH_DEBOUNCE_MS : 0);
  }, [effectiveEnabled, latestId, refreshNonce, timeRange]);

  useEffect(() => () => {
    if (refreshTimerRef.current) {
      window.clearTimeout(refreshTimerRef.current);
    }
  }, []);

  return { data, loading, refreshing, error, refresh: load };
}
