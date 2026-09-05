import { useCallback, useEffect, useRef, useState } from 'react';
import { useAuthStore } from '@/stores/useAuthStore';
import { startPolling } from '@/pro/shared/polling';
import { apiKeyPolicyApi, type APIKeyQuotaSummary } from './apiKeyPolicy';

export function useQuotaSummaries(
  enabled: boolean,
  polling: boolean,
  errorMessage: (error: unknown) => string
) {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const [quotaSummaries, setQuotaSummaries] = useState<APIKeyQuotaSummary[]>([]);
  const [quotaSnapshotAt, setQuotaSnapshotAt] = useState(0);
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quotaError, setQuotaError] = useState('');
  const revision = useRef(0);
  const inFlight = useRef(false);

  const loadQuotaSummaries = useCallback(
    async (quiet = false) => {
      if (!enabled || (quiet && inFlight.current)) return;
      const request = ++revision.current;
      inFlight.current = true;
      setQuotaLoading(!quiet);
      try {
        const response = await apiKeyPolicyApi.quotaSummaries();
        if (request !== revision.current) return;
        setQuotaSummaries(Array.isArray(response.items) ? response.items : []);
        setQuotaSnapshotAt(Number(response.snapshotAtMs) || Date.now());
        setQuotaError('');
      } catch (error) {
        if (request === revision.current) setQuotaError(errorMessage(error));
      } finally {
        if (request === revision.current) {
          inFlight.current = false;
          setQuotaLoading(false);
        }
      }
    },
    [enabled, errorMessage]
  );

  useEffect(() => {
    revision.current += 1;
    inFlight.current = false;
    setQuotaSummaries([]);
    setQuotaSnapshotAt(0);
    setQuotaError('');
    setQuotaLoading(false);
    void loadQuotaSummaries();
    return () => {
      revision.current += 1;
      inFlight.current = false;
    };
  }, [apiBase, managementKey, loadQuotaSummaries]);

  useEffect(() => {
    if (!enabled || !polling) return;
    return startPolling(async () => {
      if (!document.hidden) await loadQuotaSummaries(true);
    }, 15_000);
  }, [enabled, polling, loadQuotaSummaries]);

  // Manual refresh also supersedes a response captured before a successful mutation.
  const refreshQuotaAfterMutation = useCallback(() => loadQuotaSummaries(), [loadQuotaSummaries]);
  return {
    quotaSummaries,
    quotaSnapshotAt,
    quotaLoading,
    quotaError,
    loadQuotaSummaries,
    refreshQuotaAfterMutation,
  };
}
