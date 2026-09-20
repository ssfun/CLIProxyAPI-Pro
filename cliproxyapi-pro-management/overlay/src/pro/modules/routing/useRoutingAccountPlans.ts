import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useShallow } from 'zustand/react/shallow';
import { authFilesApi } from '@/services/api/authFiles';
import { useAuthStore, useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { startPolling } from '@/pro/shared/polling';
import { createLatestRequestGate } from './latestRequestGate';
import { resolveSchedulingBoardPlans } from './routingAccountPlans';
import type { SchedulingBoardAccount } from './routingPolicy';

const NO_ACCOUNTS: SchedulingBoardAccount[] = [];
export function useRoutingAccountPlans(accounts: SchedulingBoardAccount[] = NO_ACCOUNTS) {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const context = useMemo(
    () => ({ apiBase, managementKey, connectionStatus }),
    [apiBase, managementKey, connectionStatus]
  );
  const [snapshot, setSnapshot] = useState<{
    context: typeof context;
    files: AuthFileItem[];
  } | null>(null);
  const gate = useRef(createLatestRequestGate());
  const quota = useQuotaStore(
    useShallow((state) => ({
      antigravityQuota: state.antigravityQuota,
      claudeQuota: state.claudeQuota,
      codexQuota: state.codexQuota,
      geminiCliQuota: state.geminiCliQuota,
      kimiQuota: state.kimiQuota,
      xaiQuota: state.xaiQuota,
    }))
  );
  const reloadPlans = useCallback(async () => {
    if (context.connectionStatus !== 'connected') return;
    const request = gate.current.begin();
    try {
      const response = await authFilesApi.list();
      if (request.isCurrent()) setSnapshot({ context, files: response.files ?? [] });
    } catch {
      // Plan metadata is optional; its failure must not suppress the live board.
      if (request.isCurrent()) setSnapshot({ context, files: [] });
    } finally {
      request.finish();
    }
  }, [context]);
  useEffect(() => {
    const currentGate = gate.current;
    currentGate.invalidate();
    if (context.connectionStatus !== 'connected') return;
    void reloadPlans();
    const stop = startPolling(reloadPlans, 60000);
    return () => {
      stop();
      currentGate.invalidate();
    };
  }, [context, reloadPlans]);
  const plans = useMemo(
    () =>
      resolveSchedulingBoardPlans(
        accounts,
        snapshot?.context === context ? snapshot.files : [],
        quota,
        t
      ),
    [accounts, snapshot, context, quota, t]
  );
  return { plans, reloadPlans };
}
