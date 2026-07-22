import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
  type UIEvent,
} from 'react';
import type {
  UsageEventPage,
  UsageEventPageFilters,
  UsagePayload,
} from '@/features/monitoring/hooks/useUsageData';

export const REALTIME_LOG_PAGE_SIZE = 100;
const REALTIME_LOG_TOP_THRESHOLD_PX = 8;
const REALTIME_LOG_AUTO_REFRESH_DELAY_MS = 1000;

export type RealtimeLogScrollMode = 'preserve' | 'top';

type RealtimeLogScrollSnapshot = {
  mode: RealtimeLogScrollMode;
  top: number;
  left: number;
  anchorId: string;
  anchorOffset: number;
};

type UseRealtimeLogDataParams = {
  connectionStatus: string;
  latestId: number;
  generation: number;
  usage: UsagePayload | null;
  setUsage: Dispatch<SetStateAction<UsagePayload | null>>;
  loadEventPage: (filters: UsageEventPageFilters) => Promise<UsageEventPage>;
  buildFilters: () => UsageEventPageFilters;
  followEnabled: boolean;
  detailsOpen: boolean;
  onGenerationChange: () => void;
};

export function useRealtimeLogData({
  connectionStatus,
  latestId,
  generation,
  usage,
  setUsage,
  loadEventPage,
  buildFilters,
  followEnabled,
  detailsOpen,
  onGenerationChange,
}: UseRealtimeLogDataParams) {
  const [page, setPage] = useState(1);
  const [matchedTotal, setMatchedTotal] = useState(0);
  const [nextCursor, setNextCursor] = useState('');
  const [pageCursors, setPageCursors] = useState<string[]>(['']);
  const [snapshotMaxId, setSnapshotMaxId] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [atTop, setAtTop] = useState(true);
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const pendingScrollSnapshotRef = useRef<RealtimeLogScrollSnapshot | null>(null);
  const requestIdRef = useRef(0);
  const abortControllerRef = useRef<AbortController | null>(null);
  const generationRef = useRef(0);

  const captureScroll = useCallback((mode: RealtimeLogScrollMode): RealtimeLogScrollSnapshot | null => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return null;
    const wrapperRect = wrapper.getBoundingClientRect();
    const rows = Array.from(wrapper.querySelectorAll<HTMLTableRowElement>('tbody tr[data-realtime-row-id]'));
    const anchor = rows.find((row) => row.getBoundingClientRect().bottom > wrapperRect.top + 1);
    return {
      mode,
      top: wrapper.scrollTop,
      left: wrapper.scrollLeft,
      anchorId: anchor?.dataset.realtimeRowId ?? '',
      anchorOffset: anchor ? anchor.getBoundingClientRect().top - wrapperRect.top : 0,
    };
  }, []);

  const handleScroll = useCallback((event: UIEvent<HTMLDivElement>) => {
    setAtTop(event.currentTarget.scrollTop <= REALTIME_LOG_TOP_THRESHOLD_PX);
  }, []);

  const reset = useCallback(() => {
    requestIdRef.current += 1;
    abortControllerRef.current?.abort();
    abortControllerRef.current = null;
    pendingScrollSnapshotRef.current = null;
    setUsage(null);
    setPage(1);
    setMatchedTotal(0);
    setNextCursor('');
    setPageCursors(['']);
    setSnapshotMaxId(0);
    setLoading(false);
    setError('');
    setAtTop(true);
  }, [setUsage]);

  const fetchPage = useCallback(async (
    nextPage: number,
    cursor = '',
    scrollMode: RealtimeLogScrollMode = 'preserve'
  ) => {
    if (connectionStatus !== 'connected') return false;
    const requestId = requestIdRef.current + 1;
    const scrollSnapshot = captureScroll(scrollMode);
    requestIdRef.current = requestId;
    abortControllerRef.current?.abort();
    const controller = new AbortController();
    abortControllerRef.current = controller;
    setLoading(true);
    setError('');
    try {
      const result = await loadEventPage({ ...buildFilters(), cursor, signal: controller.signal });
      if (requestIdRef.current !== requestId) return false;
      pendingScrollSnapshotRef.current = scrollSnapshot;
      setUsage(result.usage);
      setMatchedTotal(result.matchedTotal);
      setNextCursor(result.nextCursor);
      setSnapshotMaxId(result.snapshotMaxId);
      setPageCursors((current) => {
        const next = current.slice(0, Math.max(nextPage, 1));
        next[nextPage - 1] = result.pageCursor;
        return next;
      });
      setPage(nextPage);
      return true;
    } catch (reason) {
      if (requestIdRef.current !== requestId || controller.signal.aborted) return false;
      setError(reason instanceof Error ? reason.message : String(reason));
      return false;
    } finally {
      if (requestIdRef.current === requestId) setLoading(false);
      if (abortControllerRef.current === controller) abortControllerRef.current = null;
    }
  }, [buildFilters, captureScroll, connectionStatus, loadEventPage, setUsage]);

  const refresh = useCallback(async (scrollMode: RealtimeLogScrollMode = 'preserve') => {
    setPageCursors(['']);
    return fetchPage(1, '', scrollMode);
  }, [fetchPage]);

  const showPreviousPage = useCallback(async () => {
    if (loading || page <= 1) return;
    const previousPage = page - 1;
    await fetchPage(previousPage, pageCursors[previousPage - 1] ?? '', 'top');
  }, [fetchPage, loading, page, pageCursors]);

  const showNextPage = useCallback(async () => {
    if (loading || !nextCursor) return;
    await fetchPage(page + 1, nextCursor, 'top');
  }, [fetchPage, loading, nextCursor, page]);

  const pendingEventCount = snapshotMaxId > 0 ? Math.max(latestId - snapshotMaxId, 0) : 0;
  const autoRefreshPaused = page !== 1 || !followEnabled || !atTop || detailsOpen;
  const canAutoRefresh = connectionStatus === 'connected'
    && page === 1
    && !loading
    && pendingEventCount > 0
    && !autoRefreshPaused;

  useLayoutEffect(() => {
    const snapshot = pendingScrollSnapshotRef.current;
    const wrapper = wrapperRef.current;
    if (!snapshot || !wrapper) return;
    pendingScrollSnapshotRef.current = null;

    wrapper.scrollLeft = snapshot.left;
    if (snapshot.mode === 'top') {
      wrapper.scrollTop = 0;
      setAtTop(true);
      return;
    }

    const wrapperRect = wrapper.getBoundingClientRect();
    const anchor = Array.from(wrapper.querySelectorAll<HTMLTableRowElement>('tbody tr[data-realtime-row-id]'))
      .find((row) => row.dataset.realtimeRowId === snapshot.anchorId);
    if (anchor) {
      wrapper.scrollTop += anchor.getBoundingClientRect().top - wrapperRect.top - snapshot.anchorOffset;
    } else {
      wrapper.scrollTop = snapshot.top;
    }
    setAtTop(wrapper.scrollTop <= REALTIME_LOG_TOP_THRESHOLD_PX);
  }, [usage]);

  useEffect(() => {
    if (connectionStatus !== 'connected') {
      reset();
      return;
    }
    void refresh('top');
  }, [connectionStatus, refresh, reset]);

  useEffect(() => {
    const previousGeneration = generationRef.current;
    generationRef.current = generation;
    if (previousGeneration <= 0 || generation <= 0 || previousGeneration === generation) return;
    reset();
    onGenerationChange();
    void refresh('top');
  }, [generation, onGenerationChange, refresh, reset]);

  useEffect(() => {
    if (!canAutoRefresh) return;
    const timer = setTimeout(() => {
      void refresh('top');
    }, REALTIME_LOG_AUTO_REFRESH_DELAY_MS);
    return () => clearTimeout(timer);
  }, [canAutoRefresh, pendingEventCount, refresh]);

  useEffect(() => () => {
    requestIdRef.current += 1;
    abortControllerRef.current?.abort();
  }, []);

  return {
    page,
    matchedTotal,
    nextCursor,
    snapshotMaxId,
    loading,
    error,
    pendingEventCount,
    autoRefreshPaused,
    wrapperRef,
    handleScroll,
    refresh,
    reset,
    showPreviousPage,
    showNextPage,
  };
}
