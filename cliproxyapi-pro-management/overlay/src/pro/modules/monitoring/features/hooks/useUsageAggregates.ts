import { createSummaryCache } from '../summaryCache';
import { useCallback, useEffect, useRef, useState } from 'react';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores/useAuthStore';
import {
  getTimeRangeKey,
  getLocalTimeZone,
  resolveTimeRange,
  type TimeRangeSelection,
} from '../timeRange';

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
  cacheInputTokens: number;
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
  allSummary: UsageAggregateBucket[];
  recentDailySummary: UsageAggregateBucket[];
  latestId: number;
  snapshotAtMs: number;
  scopeTimeRange: TimeRangeSelection;
  scopeTimeRangeKey: string;
  scopeApiKeyHash: string;
  scopeConnectionKey?: string;
  summarySnapshotAtMs?: number;
};

type UseUsageAggregatesParams = {
  latestId: number;
  generation?: number;
  timeRange: TimeRangeSelection;
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
  generation = 0,
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
  const summaryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latestIdRef = useRef(latestId);
  const hasDataRef = useRef(false);
  const summaryCache =
    useRef(createSummaryCache<[UsageAggregateResponse, UsageAggregateResponse]>());
  const forceSummaryRef = useRef(false);
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const timeRangeKey = getTimeRangeKey(timeRange);
  const effectiveEnabled =
    enabled && connectionStatus === 'connected' && Boolean(apiBase) && Boolean(managementKey);
  const connectionKey = effectiveEnabled ? `${apiBase}\u0000${managementKey}` : '';
  const activeConnectionKeyRef = useRef(connectionKey);
  const activeGenerationRef = useRef(generation);

  const loadRef = useRef<((forceSummary?: boolean) => Promise<void>) | null>(null);

  const load = useCallback(
    async (forceSummary = false) => {
      if (forceSummary) forceSummaryRef.current = true;
      if (!effectiveEnabled) return;
      if (refreshInFlightRef.current) {
        refreshPendingRef.current = true;
        return;
      }
      refreshInFlightRef.current = true;
      const force = forceSummaryRef.current;
      forceSummaryRef.current = false;
      const queryGeneration = queryGenerationRef.current;
      const requestId = requestIdRef.current + 1;
      requestIdRef.current = requestId;
      setRefreshing(true);
      setError('');

      const nowMs = Date.now();
      const resolvedRange = resolveTimeRange(timeRange, nowMs);
      const rangeStartMs = resolvedRange.fromMs;
      const todayStart = new Date(nowMs);
      todayStart.setHours(0, 0, 0, 0);
      const yesterdayStart = new Date(todayStart);
      yesterdayStart.setDate(yesterdayStart.getDate() - 1);
      const interval = resolvedRange.interval;
      const timezoneOffsetMinutes = -new Date().getTimezoneOffset();
      const timezone = getLocalTimeZone();
      const trendParams: Record<string, string | number> = {
        from_ms: Math.max(rangeStartMs, 0),
        to_ms: resolvedRange.toMs,
        interval,
        limit: 10000,
        timezone_offset_minutes: timezoneOffsetMinutes,
      };
      if (timezone) trendParams.timezone = timezone;
      if (apiKeyHash !== 'all') {
        trendParams.api_key_hash = apiKeyHash;
      }
      const rankingParams = {
        from_ms: Math.max(rangeStartMs, 0),
        to_ms: resolvedRange.toMs,
        interval: 'all',
        limit: 10000,
        timezone_offset_minutes: timezoneOffsetMinutes,
        ...(timezone ? { timezone } : {}),
      };

      let fetchedSummaries = false;
      try {
        const [trendPayload, providerPayload, apiKeyPayload, summaries] = await Promise.all([
          apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
            params: trendParams,
          }),
          apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
            params: { ...rankingParams, group_by: 'provider' },
          }),
          apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
            params: { ...rankingParams, group_by: 'api_key_hash,model' },
          }),
          summaryCache.current.load(
            `${connectionKey}\u0000${generation}\u0000${todayStart.getTime()}`,
            () => {
              fetchedSummaries = true;
              return Promise.all([
                apiClient.get<UsageAggregateResponse>('/usage/aggregates', {
                  params: {
                    from_ms: 0,
                    to_ms: nowMs,
                    interval: 'all',
                    group_by: 'model',
                    limit: 10000,
                    timezone_offset_minutes: timezoneOffsetMinutes,
                    ...(timezone ? { timezone } : {}),
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
                    ...(timezone ? { timezone } : {}),
                  },
                }),
              ]);
            },
            force
          ),
        ]);
        if (requestIdRef.current !== requestId || queryGenerationRef.current !== queryGeneration)
          return;
        const [allSummaryPayload, recentDailySummaryPayload] = summaries;
        if (summaryTimerRef.current !== null) {
          window.clearTimeout(summaryTimerRef.current);
          summaryTimerRef.current = null;
        }
        const summaryLatestId = Math.min(
          Number(allSummaryPayload.latest_id) || 0,
          Number(recentDailySummaryPayload.latest_id) || 0
        );
        const observedLatestId = Math.max(
          latestIdRef.current,
          Number(trendPayload.latest_id) || 0,
          Number(providerPayload.latest_id) || 0,
          Number(apiKeyPayload.latest_id) || 0
        );
        // Coalesce cached updates at the original expiry, including the last event before idle.
        // Only cache hits schedule a trailing refresh; a fresh response never starts idle polling.
        if (!fetchedSummaries && summaryLatestId < observedLatestId) {
          summaryTimerRef.current = window.setTimeout(() => {
            summaryTimerRef.current = null;
            void loadRef.current?.(true);
          }, summaryCache.current.remainingTtlMs());
        }
        const snapshotAtMs = Math.max(
          Number(trendPayload?.snapshot_at_ms) || 0,
          Number(providerPayload?.snapshot_at_ms) || 0,
          Number(apiKeyPayload?.snapshot_at_ms) || 0,
          Number(allSummaryPayload?.snapshot_at_ms) || 0,
          Number(recentDailySummaryPayload?.snapshot_at_ms) || 0
        );
        const providerItems = normalizeItems(providerPayload);
        const apiKeyItems = normalizeItems(apiKeyPayload);
        setData({
          trend: normalizeItems(trendPayload),
          models: apiKeyItems,
          apiKeys: apiKeyItems,
          providers: providerItems,
          allSummary: normalizeItems(allSummaryPayload),
          recentDailySummary: normalizeItems(recentDailySummaryPayload),
          latestId: Math.min(
            Number(trendPayload?.latest_id) || 0,
            Number(providerPayload?.latest_id) || 0,
            Number(apiKeyPayload?.latest_id) || 0
          ),
          snapshotAtMs,
          summarySnapshotAtMs: Math.min(
            Number(allSummaryPayload.snapshot_at_ms) || nowMs,
            Number(recentDailySummaryPayload.snapshot_at_ms) || nowMs
          ),
          scopeTimeRange: timeRange,
          scopeTimeRangeKey: timeRangeKey,
          scopeApiKeyHash: apiKeyHash,
          scopeConnectionKey: connectionKey,
        });
        hasDataRef.current = true;
        lastFetchedAtRef.current = Date.now();
        setLoading(false);
      } catch (err) {
        if (requestIdRef.current !== requestId || queryGenerationRef.current !== queryGeneration)
          return;
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
    },
    [apiKeyHash, connectionKey, effectiveEnabled, generation, timeRange, timeRangeKey]
  );

  useEffect(() => {
    latestIdRef.current = latestId;
  }, [latestId]);

  useEffect(() => {
    loadRef.current = load;
  }, [load]);

  useEffect(() => {
    const connectionChanged = activeConnectionKeyRef.current !== connectionKey;
    activeConnectionKeyRef.current = connectionKey;
    const datasetChanged = activeGenerationRef.current !== generation;
    activeGenerationRef.current = generation;
    queryGenerationRef.current += 1;
    requestIdRef.current += 1;
    refreshInFlightRef.current = false;
    refreshPendingRef.current = false;
    if (connectionChanged || datasetChanged) {
      if (summaryTimerRef.current !== null) {
        window.clearTimeout(summaryTimerRef.current);
        summaryTimerRef.current = null;
      }
      summaryCache.current.clear();
      hasDataRef.current = false;
      setData(null);
      lastFetchedAtRef.current = 0;
    }
    setError('');
    setLoading(effectiveEnabled && !hasDataRef.current);
    if (refreshTimerRef.current) {
      window.clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = null;
    }
    setRefreshNonce((value) => value + 1);
  }, [apiKeyHash, connectionKey, effectiveEnabled, generation, timeRangeKey]);

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
    refreshTimerRef.current = window.setTimeout(
      () => {
        refreshTimerRef.current = null;
        void loadRef.current?.();
      },
      lastFetchedAtRef.current > 0 ? AGGREGATE_REFRESH_DEBOUNCE_MS : 0
    );
  }, [effectiveEnabled, latestId, refreshNonce, timeRangeKey]);

  useEffect(
    () => () => {
      requestIdRef.current += 1;
      summaryCache.current.clear();
      if (summaryTimerRef.current !== null) {
        window.clearTimeout(summaryTimerRef.current);
        summaryTimerRef.current = null;
      }
      if (refreshTimerRef.current) {
        window.clearTimeout(refreshTimerRef.current);
      }
    },
    []
  );

  const refresh = useCallback(() => load(true), [load]);
  const connectionScopedData = data?.scopeConnectionKey === connectionKey ? data : null;
  return { data: connectionScopedData, loading, refreshing, error, refresh };
}
