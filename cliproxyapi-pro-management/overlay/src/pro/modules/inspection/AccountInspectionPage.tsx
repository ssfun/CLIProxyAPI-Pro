import { startTransition, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState, type CSSProperties } from 'react';
import { useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconChevronDown,
  IconChevronUp,
  IconDownload,
  IconRefreshCw,
  IconSearch,
  IconX,
} from '@/components/ui/icons';
import { getAuthFileIcon } from '@/features/authFiles/constants';
import {
  ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE,
  accountInspectionBackendResultToItem,
  buildExecutionFailureMessage,
  DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
  hasAccountInspectionAutoExecutePolicies,
  isAccountInspectionBackendResponse,
  isSuggestedAction,
  loadAccountInspectionConfigurableSettings,
  normalizeAntigravityQuotaMode,
  normalizeAutoErrorAction,
  saveAccountInspectionConfigurableSettings,
  type AccountInspectionLogLevel,
  type AccountInspectionResultItem,
} from '@/pro/modules/inspection/features/accountInspection';
import { readInspectionFocusLocationState } from '@/pro/shared/inspectionNavigation';
import { SchedulingRecoveryDialog } from './SchedulingRecoveryDialog';
import { InspectionRecordsPanel } from './InspectionRecordsPanel';
import { ProDetailDialog, ProSettingsSheet } from '@/pro/shared/ProSurface';
import { useProSurfaceState } from '@/pro/shared/useProSurfaceState';
import {
  ACCOUNT_INSPECTION_AUTH_FILES_IDLE_DELAY_MS,
  ACCOUNT_INSPECTION_EXPORT_DOWNLOAD_CONCURRENCY,
  ACCOUNT_INSPECTION_LOG_PAGE_SIZE,
  ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET,
  ANTIGRAVITY_QUOTA_MODE_OPTIONS,
  AUTO_ERROR_ACTION_OPTIONS,
  AUTO_EXECUTE_CONFIRMATION_LIMITS,
  DELETE_WORKER_LIMITS,
  INSPECTION_TARGET_OPTIONS,
  InspectionErrorDetailsPanel,
  PROVIDER_WORKER_LIMITS,
  RETRY_LIMITS,
  SAMPLE_SIZE_LIMITS,
  SCHEDULE_INTERVAL_LIMITS,
  THRESHOLD_LIMITS,
  TIMEOUT_LIMITS,
  WORKER_LIMITS,
  buildDeleteConfirmationMessage,
  buildExecuteConfirmationMessage,
  buildHealthStatusLabel,
  buildHighAvailabilityBarStyle,
  buildInspectionResultsViewState,
  buildManualActionItem,
  createEmptyAuthFileAccountStats,
  createInspectionBackendState,
  formatActionLabel,
  formatAccountInspectionDuration,
  formatCurrentStateLabel,
  formatInspectionInterval,
  formatInspectionExecutionLabel,
  formatInspectionResultToast,
  formatQuotaRemainingLabel,
  formatRunInspectionButtonLabel,
  formatTimestamp,
  formatTokenRefreshDetail,
  formatTokenRefreshLabel,
  getDocumentTheme,
  getPaginationRange,
  getProviderInitial,
  handleAccountInspectionControlError,
  healthToneClass,
  inspectionBackendReducer,
  isInspectableAccountInspectionAuthFile,
  isSchedulingRecoveryAction,
  levelClassMap,
  resolveAccountInspectionAccountLabel,
  resolveAccountInspectionPlanLabel,
  resolveResultHealthStatus,
  scheduleAuthFileAccountStats,
  summaryToneClass,
  toAccountInspectionApiItem,
  tokenRefreshToneClass,
  type AccountInspectionIdleCallback,
  type AuthFileAccountStats,
  type AuthFileAccountStatsJob,
  type InspectionSettingsDraftField,
  type ManualAccountInspectionAction,
  type ProviderAccountStats,
  type ResolvedTheme,
  type ResultReasonFilter,
  type ResultStatusFilter,
  type SummaryCard,
} from '@/pro/modules/inspection/features/accountInspectionPageModel';
import {
  DEFAULT_PRO_PAGE_SIZE,
  PRO_PAGE_SIZE_OPTIONS,
  normalizeProPageSize,
  resolveProPaginationCopy,
} from '@/pro/shared/pagination';
import {
  buildZipArchive,
  downloadBlobFile,
  mapWithConcurrency,
} from '@/pro/modules/inspection/features/accountInspectionExport';
import {
  accountInspectionApi,
  accountInspectionWebSocketProtocol,
  buildAccountInspectionLogsWebSocketUrl,
  nextAccountInspectionReconnectDelay,
  refreshAccountInspectionAfterReconnect,
  type AccountInspectionLogStreamMessage,
  type AccountInspectionBatchKind,
  type AccountInspectionBatchOperation,
  type AccountInspectionBatchScope,
  type AccountInspectionScheduleResponse,
} from './api';
import { apiClient } from '@/services/api/client';
import { authFilesApi } from '@/services/api/authFiles';
import { quotaPersistenceMiddleware } from '@/pro/modules/quota';
import { useAuthStore, useNotificationStore, useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { resolveAuthProvider } from '@/utils/quota';
import { resolveProviderDisplayLabel } from '@/pro/shared/provider';
import quotaStyles from '@/features/quota/QuotaPage.module.scss';
import styles from '@/pro/modules/inspection/features/accountInspection.module.scss';
import { useInspectionDetailsLoader } from '@/pro/modules/inspection/hooks/useInspectionDetailsLoader';

type ResultBulkAction = 'suggested' | 'recheck' | 'recover' | ManualAccountInspectionAction;
type ResultBulkScope = 'selected' | 'filtered' | 'fixed';

export function AccountInspectionPage() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const claudeQuota = useQuotaStore((state) => state.claudeQuota);
  const codexQuota = useQuotaStore((state) => state.codexQuota);
  const geminiCliQuota = useQuotaStore((state) => state.geminiCliQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);
  const [initialAutoExecutionPolicy] = useState(() =>
    hasAccountInspectionAutoExecutePolicies(loadAccountInspectionConfigurableSettings())
  );

  const [backendState, dispatchBackendState] = useReducer(
    inspectionBackendReducer,
    undefined,
    () => createInspectionBackendState(loadAccountInspectionConfigurableSettings())
  );
  const {
    inspectionSettings,
    settingsDraft,
    scheduleDraft,
    schedule,
    logs,
    logsPage,
    runStatus,
    progress,
    result,
    autoExecutionCounts,
    restoredSnapshot,
    lastError,
    persistenceError,
    settingsDirty,
  } = backendState;
  const [selectedDetailResult, setSelectedDetailResultState] = useState<AccountInspectionResultItem | null>(null);
  const [selectedRecoveryResult, setSelectedRecoveryResult] = useState<AccountInspectionResultItem | null>(null);
  const { activeSurface, openSurface, closeSurface } = useProSurfaceState<'settings' | 'detail' | 'recovery'>();
  const isSettingsModalOpen = activeSurface === 'settings';
  const setIsSettingsModalOpen = useCallback((open: boolean) => {
    if (open) openSurface('settings');
    else if (activeSurface === 'settings') closeSurface();
  }, [activeSurface, closeSurface, openSurface]);
  const setSelectedDetailResult = useCallback((item: AccountInspectionResultItem | null) => {
    if (item) {
      setSelectedDetailResultState(item);
      openSurface('detail');
    } else if (activeSurface === 'detail') {
      closeSurface();
    }
  }, [activeSurface, closeSurface, openSurface]);
  const [scheduleLoading, setScheduleLoading] = useState(false);
  const [logsCollapsed, setLogsCollapsed] = useState(false);
  const [resultStatusFilter, setResultStatusFilter] = useState<ResultStatusFilter>(
    initialAutoExecutionPolicy ? 'accountIssues' : 'all'
  );
  const [resultReasonFilter, setResultReasonFilter] = useState<ResultReasonFilter | null>(null);
  const [resultPendingOnly, setResultPendingOnly] = useState(!initialAutoExecutionPolicy);
  const [selectedResultProvider, setSelectedResultProvider] = useState<string>(ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE);
  const [resultSearchInput, setResultSearchInput] = useState('');
  const deferredResultSearchInput = useDeferredValue(resultSearchInput);
  const [resultSearch, setResultSearch] = useState('');
  const [resultPage, setResultPage] = useState(1);
  const [resultPageSize, setResultPageSize] = useState(DEFAULT_PRO_PAGE_SIZE);
  const [logLevelFilter, setLogLevelFilter] = useState<AccountInspectionLogLevel | 'all'>('all');
  const [logPage, setLogPage] = useState(1);
  const [authFiles, setAuthFiles] = useState<AuthFileItem[]>([]);
  const [authFilesLoaded, setAuthFilesLoaded] = useState(false);
  const [authFileStats, setAuthFileStats] = useState<AuthFileAccountStats>(() => createEmptyAuthFileAccountStats());
  const [authFileStatsReady, setAuthFileStatsReady] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [recheckingKey, setRecheckingKey] = useState<string | null>(null);
  const [selectedResultKeys, setSelectedResultKeys] = useState<Set<string>>(() => new Set());
  const [resultBulkAction, setResultBulkAction] = useState<ResultBulkAction>('suggested');
  const [resultBulkScope, setResultBulkScope] = useState<ResultBulkScope>('selected');
  const [bulkActionLoading, setBulkActionLoading] = useState(false);
  const [batchOperations, setBatchOperations] = useState<AccountInspectionBatchOperation[]>([]);
  const [batchError, setBatchError] = useState('');
  const [batchHydrated, setBatchHydrated] = useState(false);
  const [scopedPending, setScopedPending] = useState<{ key: string; count: number } | null>(null);
  const batchRunning = batchOperations.some((operation) => operation.state === 'running');
  const singleMutationBlocked = !batchHydrated || batchRunning;
  const openRecoveryForItem = useCallback((item: AccountInspectionResultItem) => {
    if (singleMutationBlocked) return;
    if (!item.authId || !item.authIndex) {
      showNotification(t('routing_policy.recovery.unavailable_version'), 'info');
      return;
    }
    setSelectedRecoveryResult(item);
    openSurface('recovery');
  }, [openSurface, showNotification, singleMutationBlocked, t]);
  const batchStorageKey = useMemo(() => {
    let hash = 2166136261;
    for (const char of `${apiBase}\0${managementKey}`) {
      hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
    }
    return `account-inspection-batches:${(hash >>> 0).toString(16)}`;
  }, [apiBase, managementKey]);
  const [detailsLoadError, setDetailsLoadError] = useState('');
  const [detailsRetryNonce, setDetailsRetryNonce] = useState(0);
  const [exportingAuthFiles, setExportingAuthFiles] = useState(false);
  const [selectedAssetProvider, setSelectedAssetProvider] = useState<string>('all');
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => getDocumentTheme());
  const activeResultFilter = resultReasonFilter ?? resultStatusFilter;
  const paginationCopy = resolveProPaginationCopy(i18n.resolvedLanguage ?? i18n.language);
  const logListRef = useRef<HTMLDivElement | null>(null);
  const resultsPanelRef = useRef<HTMLDivElement | null>(null);
  const resultsTableViewportRef = useRef<HTMLDivElement | null>(null);
  const selectAllResultsRef = useRef<HTMLInputElement | null>(null);
  const refreshedBackendFinishedAtRef = useRef(0);
  const loadedBackendDetailsKeyRef = useRef('');
  const backendScheduleRequestIdRef = useRef(0);


  useEffect(() => {
    const root = document.documentElement;
    const observer = new MutationObserver(() => setResolvedTheme(getDocumentTheme()));
    observer.observe(root, { attributes: true, attributeFilter: ['class', 'data-theme'] });
    return () => observer.disconnect();
  }, []);


  useEffect(() => {
    const focus = readInspectionFocusLocationState(location.state);
    if (!focus) return;
    const query = focus.fileName || focus.authIndex || focus.authId;
    if (!query) return;
    setResultStatusFilter('all');
    setResultReasonFilter(null);
    setResultPendingOnly(false);
    setSelectedResultProvider(ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE);
    setResultSearchInput(query);
    setResultSearch(query);
    setResultPage(1);
  }, [location.state]);

  useEffect(() => {
    const timer = window.setTimeout(() => setResultSearch(deferredResultSearchInput.trim()), 300);
    return () => window.clearTimeout(timer);
  }, [deferredResultSearchInput]);

  const loadAuthFiles = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      setAuthFiles([]);
      setAuthFilesLoaded(false);
      return;
    }

    try {
      const response = await authFilesApi.list();
      const files = Array.isArray(response.files) ? response.files : [];
      startTransition(() => {
        setAuthFiles(files);
        setAuthFilesLoaded(true);
      });
    } catch {
      setAuthFiles([]);
      setAuthFilesLoaded(false);
    }
  }, [connectionStatus]);

  useEffect(() => {
    if (connectionStatus !== 'connected') {
      void loadAuthFiles();
      return;
    }

    let cancelled = false;
    const loadWhenReady = () => {
      if (!cancelled) void loadAuthFiles();
    };
    const windowWithIdleCallback = window as Window & {
      requestIdleCallback?: (callback: AccountInspectionIdleCallback, options?: { timeout?: number }) => number;
      cancelIdleCallback?: (handle: number) => void;
    };
    if (windowWithIdleCallback.requestIdleCallback) {
      const idleHandle = windowWithIdleCallback.requestIdleCallback(loadWhenReady, {
        timeout: 1200,
      });
      return () => {
        cancelled = true;
        windowWithIdleCallback.cancelIdleCallback?.(idleHandle);
      };
    }

    const timeoutId = window.setTimeout(loadWhenReady, ACCOUNT_INSPECTION_AUTH_FILES_IDLE_DELAY_MS);
    return () => {
      cancelled = true;
      window.clearTimeout(timeoutId);
    };
  }, [connectionStatus, loadAuthFiles]);

  const applyBackendResponse = useCallback((response: AccountInspectionScheduleResponse, deferred = false) => {
    if (!isAccountInspectionBackendResponse(response)) return;
    const applyState = () => dispatchBackendState({ type: 'backendResponseReceived', response });
    if (deferred) {
      startTransition(applyState);
    } else {
      applyState();
    }

    if (
      response.status.state !== 'running' &&
      response.status.state !== 'paused' &&
      response.status.state !== 'stopping' &&
      response.status.lastFinishedAt > 0 &&
      refreshedBackendFinishedAtRef.current !== response.status.lastFinishedAt
    ) {
      refreshedBackendFinishedAtRef.current = response.status.lastFinishedAt;
      quotaPersistenceMiddleware.markStale();
      void Promise.all([
        loadAuthFiles(),
        quotaPersistenceMiddleware.ensureFresh(),
      ]);
    }
  }, [loadAuthFiles]);

  const loadInspectionDetailsPage = useCallback(async (signal?: AbortSignal) => {
    const response = await accountInspectionApi.getStatus({
      includeDetails: true,
      resultPage,
      resultPageSize,
      resultFilter: activeResultFilter,
      resultPendingOnly,
      resultProvider: selectedResultProvider,
      resultSearch,
      logPage,
      logPageSize: ACCOUNT_INSPECTION_LOG_PAGE_SIZE,
      logLevel: logLevelFilter,
    }, signal);
    if (signal?.aborted) {
      throw signal.reason ?? new DOMException('Account inspection details request aborted', 'AbortError');
    }
    applyBackendResponse(response, true);
    return response;
  }, [activeResultFilter, applyBackendResponse, logLevelFilter, logPage, resultPage, resultPageSize, resultPendingOnly, resultSearch, selectedResultProvider]);
  const inspectionDetailsLoaderRef = useRef(loadInspectionDetailsPage);

  useEffect(() => {
    inspectionDetailsLoaderRef.current = loadInspectionDetailsPage;
  }, [loadInspectionDetailsPage]);

  const currentInspectionDetailOptions = useMemo(() => ({
    includeDetails: true,
    resultPage,
    resultPageSize,
    resultFilter: activeResultFilter,
    resultPendingOnly,
    resultProvider: selectedResultProvider,
    resultSearch,
    logPage,
    logPageSize: ACCOUNT_INSPECTION_LOG_PAGE_SIZE,
    logLevel: logLevelFilter,
  }), [activeResultFilter, logLevelFilter, logPage, resultPage, resultPageSize, resultPendingOnly, resultSearch, selectedResultProvider]);

  useEffect(() => {
    if (connectionStatus !== 'connected') return;
    let cancelled = false;
    setBatchHydrated(false);
    setBatchOperations([]);
    let ids: string[] = [];
    try {
      const stored = JSON.parse(window.sessionStorage.getItem(batchStorageKey) || '[]');
      if (Array.isArray(stored)) ids = stored.filter((id): id is string => typeof id === 'string' && /^[a-f0-9]{32}$/.test(id));
    } catch {
      // Browser storage may be unavailable; the current tab still keeps live receipts.
    }
    void Promise.allSettled(ids.map(accountInspectionApi.getBatch)).then((results) => {
      if (cancelled) return;
      setBatchOperations(results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []));
      setBatchHydrated(true);
    });
    return () => { cancelled = true; };
  }, [batchStorageKey, connectionStatus]);

  useEffect(() => {
    if (!batchHydrated || connectionStatus !== 'connected') return;
    try {
      window.sessionStorage.setItem(batchStorageKey, JSON.stringify(batchOperations.map((operation) => operation.operationId)));
    } catch {
      // Receipt persistence is best effort when session storage is unavailable.
    }
  }, [batchHydrated, batchOperations, batchStorageKey, connectionStatus]);

  const batchRefreshRef = useRef({ currentInspectionDetailOptions, applyBackendResponse, loadAuthFiles });
  useLayoutEffect(() => {
    batchRefreshRef.current = { currentInspectionDetailOptions, applyBackendResponse, loadAuthFiles };
  }, [currentInspectionDetailOptions, applyBackendResponse, loadAuthFiles]);
  const runningBatchIds = batchOperations.filter((operation) => operation.state === 'running').map((operation) => operation.operationId).join('|');

  useEffect(() => {
    if (!runningBatchIds) return;
    const operationIds = runningBatchIds.split('|');
    let cancelled = false;
    let polling = false;
    const refresh = async () => {
      if (polling || cancelled) return;
      polling = true;
      try {
        const operations = await Promise.all(operationIds.map(accountInspectionApi.getBatch));
        if (cancelled) return;
        const byId = new Map(operations.map((operation) => [operation.operationId, operation]));
        setBatchOperations((current) => current.map((operation) => byId.get(operation.operationId) ?? operation));
        if (operations.every((operation) => operation.state === 'completed')) {
          const { currentInspectionDetailOptions: options, applyBackendResponse: apply, loadAuthFiles: load } = batchRefreshRef.current;
          const response = await accountInspectionApi.getStatus(options);
          if (!cancelled) {
            apply(response);
            void load();
          }
        }
      } catch (error) {
        if (!cancelled) setBatchError(error instanceof Error ? error.message : String(error));
      } finally {
        polling = false;
      }
    };
    const timer = window.setInterval(() => void refresh(), 1500);
    void refresh();
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [runningBatchIds]);

  const loadBackendSchedule = useCallback(async () => {
    const requestId = backendScheduleRequestIdRef.current + 1;
    backendScheduleRequestIdRef.current = requestId;
    if (connectionStatus !== 'connected') {
      dispatchBackendState({ type: 'clearSchedule' });
      return;
    }
    try {
      const response = await accountInspectionApi.getStatus();
      if (backendScheduleRequestIdRef.current !== requestId) return;
      applyBackendResponse(response);
    } catch {
      // Preserve the last server snapshot across transient transport failures.
    }
  }, [applyBackendResponse, connectionStatus]);

  useEffect(() => {
    void loadBackendSchedule();
  }, [loadBackendSchedule]);

  useEffect(() => {
    if (connectionStatus !== 'connected' || !apiBase || !managementKey) return;
    let closed = false;
    let socket: WebSocket | null = null;
    let reconnectTimer: number | null = null;
    let pollTimer: number | null = null;
    let reconnectDelay = 1000;

    const stopPolling = () => {
      if (pollTimer !== null) {
        window.clearInterval(pollTimer);
        pollTimer = null;
      }
    };
    const startPolling = () => {
      if (closed || pollTimer !== null) return;
      void loadBackendSchedule();
      pollTimer = window.setInterval(() => void loadBackendSchedule(), 5000);
    };
    const scheduleReconnect = () => {
      if (closed || reconnectTimer !== null) return;
      const delay = reconnectDelay;
      reconnectDelay = nextAccountInspectionReconnectDelay(reconnectDelay);
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = null;
        connect();
      }, delay);
    };
    const connect = () => {
      if (closed) return;
      try {
        socket = new WebSocket(
          buildAccountInspectionLogsWebSocketUrl(apiBase),
          accountInspectionWebSocketProtocol(managementKey)
        );
      } catch {
        startPolling();
        scheduleReconnect();
        return;
      }

      socket.onopen = () => {
        if (closed) return;
        reconnectDelay = 1000;
        stopPolling();
        void refreshAccountInspectionAfterReconnect(
          loadBackendSchedule,
          inspectionDetailsLoaderRef.current
        );
      };
      socket.onmessage = (event) => {
        if (closed || typeof event.data !== 'string') return;
        try {
          const message = JSON.parse(event.data) as AccountInspectionLogStreamMessage;
          if (message.log) {
            dispatchBackendState({
              type: 'appendLog',
              level: message.log.level,
              message: message.log.message,
              timestamp: message.log.time,
            });
            if (message.type === 'log') {
              return;
            }
          }
          applyBackendResponse({
            schedule: message.schedule,
            status: message.status,
          });
        } catch {
          return;
        }
      };
      socket.onerror = () => socket?.close();
      socket.onclose = () => {
        socket = null;
        if (closed) return;
        startPolling();
        scheduleReconnect();
      };
    };

    connect();

    return () => {
      closed = true;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      stopPolling();
      socket?.close();
    };
  }, [apiBase, applyBackendResponse, connectionStatus, loadBackendSchedule, managementKey]);

  const inspectionDetailsKey = [progress.startedAt, activeResultFilter, resultPendingOnly, selectedResultProvider, resultSearch, resultPage, resultPageSize, logLevelFilter, logPage].join(':');
  const handleDetailsLoadError = useCallback((message: string) => setDetailsLoadError(message), []);
  useInspectionDetailsLoader({
    enabled: connectionStatus === 'connected' && progress.status !== 'running' && progress.status !== 'paused' && progress.startedAt > 0 && (progress.total > 0 || progress.summary.totalFiles > 0),
    detailKey: inspectionDetailsKey,
    retryNonce: detailsRetryNonce,
    load: loadInspectionDetailsPage,
    loadedKeyRef: loadedBackendDetailsKeyRef,
    onError: handleDetailsLoadError,
  });

  useEffect(() => {
    setResultPage(1);
  }, [activeResultFilter, resultPendingOnly, resultSearch, selectedResultProvider]);

  useEffect(() => {
    setLogPage(1);
  }, [logLevelFilter]);

  const appendLog = useCallback((level: AccountInspectionLogLevel, message: string) => {
    dispatchBackendState({ type: 'appendLog', level, message, timestamp: Date.now() });
  }, []);

  useEffect(() => {
    if (logsCollapsed) return;
    const element = logListRef.current;
    if (!element) return;
    element.scrollTop = element.scrollHeight;
  }, [logs, logsCollapsed]);

  const startFreshInspection = useCallback(
    async (preserveLogs: boolean = false, introMessage: string = '') => {
      if (connectionStatus !== 'connected') {
        const message = t('notification.connection_required');
        showNotification(message, 'warning');
        return;
      }

      if (!preserveLogs) {
        dispatchBackendState({ type: 'clearLogs' });
      }
      if (introMessage) {
        appendLog('info', introMessage);
      }

      dispatchBackendState({ type: 'startRun', timestamp: Date.now() });
      setLogsCollapsed(false);
      setResultPage(1);
      setLogPage(1);

      try {
        const response = await accountInspectionApi.runNow();
        applyBackendResponse(response);
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
        dispatchBackendState({ type: 'runFailed' });
        setLogsCollapsed(false);
      }
    },
    [appendLog, applyBackendResponse, connectionStatus, showNotification, t]
  );

  const handleRunInspection = useCallback(() => {
    if (runStatus === 'paused') {
      setLogsCollapsed(false);
      void accountInspectionApi.resume()
        .then(applyBackendResponse)
        .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')));
      return;
    }

    void startFreshInspection(false);
  }, [appendLog, applyBackendResponse, runStatus, showNotification, startFreshInspection, t]);

  const handleExportAuthFiles = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }

    setExportingAuthFiles(true);
    try {
      const response = await authFilesApi.list();
      const files = Array.isArray(response.files)
        ? response.files.filter((file) =>
            isInspectableAccountInspectionAuthFile(file) &&
            (selectedAssetProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE ||
              resolveAuthProvider(file) === selectedAssetProvider)
          )
        : [];
      const downloadableFiles = files.filter((file) => typeof file.name === 'string' && file.name.trim());
      const entries = await mapWithConcurrency(
        downloadableFiles,
        ACCOUNT_INSPECTION_EXPORT_DOWNLOAD_CONCURRENCY,
        async (file) => {
          const name = file.name.trim();
          const downloadResponse = await apiClient.getRaw(`/auth-files/download?name=${encodeURIComponent(name)}`, {
            responseType: 'blob',
          });
          const blob = downloadResponse.data instanceof Blob
            ? downloadResponse.data
            : new Blob([downloadResponse.data], { type: 'application/json' });
          return {
            name,
            content: await blob.text(),
          };
        }
      );

      if (entries.length === 0) {
        showNotification(t('monitoring.account_inspection_auth_files_export_empty'), 'info');
        return;
      }

      const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
      const archive = await buildZipArchive(entries);
      const providerSuffix = selectedAssetProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
        ? ''
        : `-${selectedAssetProvider}`;
      downloadBlobFile(`auth-files-export${providerSuffix}-${timestamp}.zip`, archive);
      showNotification(t('monitoring.account_inspection_auth_files_export_success', { count: entries.length }), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setExportingAuthFiles(false);
    }
  }, [connectionStatus, selectedAssetProvider, showNotification, t]);

  const handlePauseInspection = useCallback(() => {
    if (runStatus !== 'running') return;
    void accountInspectionApi.pause()
      .then(applyBackendResponse)
      .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')));
  }, [appendLog, applyBackendResponse, runStatus, showNotification, t]);

  const handleStopInspection = useCallback(() => {
    void accountInspectionApi.stop()
      .then((response) => {
        appendLog('warning', t('monitoring.account_inspection_stopped'));
        applyBackendResponse(response);
        setLogsCollapsed(false);
        dispatchBackendState({ type: 'clearAutoExecutionCounts' });
      })
      .catch((error) => handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error')));
  }, [appendLog, applyBackendResponse, showNotification, t]);

  const executeItems = useCallback(
    async (items: AccountInspectionResultItem[]) => {
      if (singleMutationBlocked) return;
      if (restoredSnapshot) {
        showNotification(t('monitoring.account_inspection_restored_snapshot_action_blocked'), 'warning');
        return;
      }
      const currentResult = result;
      if (!currentResult) return;
      const targets = items.filter((item) => item.action !== 'keep');
      if (targets.some((item) => !item.resultRef)) {
        showNotification(t('monitoring.account_inspection_result_ref_required'), 'warning');
        return;
      }
      const actionItems = targets.flatMap((item) => {
        if (item.action === 'keep') return [];
        return [{
          ...toAccountInspectionApiItem(item),
          action: item.action,
          resultRef: item.resultRef!,
          suggested: item.suggested ?? isSuggestedAction(item),
        }];
      });
      if (actionItems.length === 0) {
        showNotification(t('monitoring.account_inspection_no_pending_actions'), 'info');
        return;
      }

      setExecuting(true);
      setLogsCollapsed(false);
      appendLog('info', t('monitoring.account_inspection_execute_started'));

      try {
        const response = await accountInspectionApi.executeActions(actionItems, currentInspectionDetailOptions);
        const failed = response.outcomes.filter((item) => !item.success);
        if (failed.length > 0) {
          showNotification(
            `${t('monitoring.account_inspection_execute_partial')}: ${failed
              .slice(0, 2)
              .map((item) => buildExecutionFailureMessage({
                action: item.action,
                fileName: item.fileName,
                displayAccount: item.displayName,
                email: item.email,
                name: item.name,
                provider: item.provider,
                authIndex: item.authIndex || null,
                success: item.success,
                error: item.error,
              }))
              .join('；')}`,
            'warning'
          );
        } else {
          showNotification(t('monitoring.account_inspection_execute_success'), 'success');
        }
        applyBackendResponse(response);
        void loadAuthFiles();
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
      } finally {
        setExecuting(false);
      }
    },
    [appendLog, applyBackendResponse, currentInspectionDetailOptions, loadAuthFiles, restoredSnapshot, result, showNotification, singleMutationBlocked, t]
  );

  const allResults = useMemo(
    () => (result ? result.results : []),
    [result]
  );

  const resultsViewState = useMemo(
    () => buildInspectionResultsViewState(allResults),
    [allResults]
  );

  const {
    healthCounts,
    actionableActionCounts,
    filterRows,
  } = resultsViewState;
  const displayedHealthCounts = result?.healthCounts ?? healthCounts;

  const hasAutoExecutionPolicy = hasAccountInspectionAutoExecutePolicies(inspectionSettings);
  const previousAutoExecutionPolicyRef = useRef(hasAutoExecutionPolicy);

  useEffect(() => {
    if (previousAutoExecutionPolicyRef.current === hasAutoExecutionPolicy) return;
    previousAutoExecutionPolicyRef.current = hasAutoExecutionPolicy;
    setResultReasonFilter(null);
    setResultStatusFilter(hasAutoExecutionPolicy ? 'accountIssues' : 'all');
    setResultPendingOnly(!hasAutoExecutionPolicy);
  }, [hasAutoExecutionPolicy]);

  const filteredResultRows = useMemo(() => {
    const rows = filterRows[activeResultFilter];
    return resultPendingOnly
      ? rows.filter(({ item }) => item.executedEffect !== 'delete' && isSuggestedAction(item) && !item.executed)
      : rows;
  }, [activeResultFilter, filterRows, resultPendingOnly]);
  const resultPageInfo = result?.resultsPage ?? null;
  const visibleResultRows = filteredResultRows;
  const selectedVisibleResultRows = useMemo(
    () => visibleResultRows.filter(({ item }) => item.executedEffect !== 'delete' && selectedResultKeys.has(item.key)),
    [selectedResultKeys, visibleResultRows]
  );
  const selectableVisibleResultRows = visibleResultRows.filter(({ item }) => item.executedEffect !== 'delete');
  const allVisibleResultsSelected = selectableVisibleResultRows.length > 0 && selectedVisibleResultRows.length === selectableVisibleResultRows.length;
  const batchFilterLabel = ({
    all: t('monitoring.account_inspection_filter_all'),
    accountIssues: t('monitoring.account_inspection_filter_account_issues'),
    quotaChanges: t('monitoring.account_inspection_filter_quota_changes'),
    highAvailable: t('monitoring.account_inspection_high_available'),
    unknown: t('monitoring.account_inspection_health_unknown'),
    accountInvalid: t('monitoring.account_inspection_account_invalid'),
    requestError: t('monitoring.account_inspection_account_request_error'),
    quotaExhausted: t('monitoring.account_inspection_health_quota_exhausted'),
    recoverable: t('monitoring.account_inspection_health_recoverable'),
  } as Record<typeof activeResultFilter, string>)[activeResultFilter];

  const filteredLogs = useMemo(
    () => (logLevelFilter === 'all' ? logs : logs.filter((entry) => entry.level === logLevelFilter)),
    [logLevelFilter, logs]
  );
  const logPageInfo = logsPage;
  const visibleLogs = filteredLogs;
  const resultPagination = getPaginationRange(resultPageInfo, visibleResultRows.length);
  const logPagination = getPaginationRange(logPageInfo, visibleLogs.length);
  const scopedPendingKey = JSON.stringify([
    activeResultFilter, selectedResultProvider, resultSearch,
    result?.finishedAt, result?.summary.pendingActionCount,
  ]);
  const scopedPendingCount = scopedPending?.key === scopedPendingKey ? scopedPending.count : null;
  const hasResult = Boolean(result);

  useEffect(() => {
    if (!hasResult || connectionStatus !== 'connected') return;
    const controller = new AbortController();
    void accountInspectionApi.getStatus({
      includeDetails: true,
      resultPage: 1,
      resultPageSize: 1,
      logPageSize: 1,
      resultFilter: activeResultFilter,
      resultProvider: selectedResultProvider,
      resultSearch,
      resultPendingOnly: true,
    }, controller.signal).then((response) => {
      if (!controller.signal.aborted && response.status.resultsPage) {
        setScopedPending({ key: scopedPendingKey, count: response.status.resultsPage.total });
      }
    }).catch(() => {
      // The button performs its own fresh count check; a transient read failure need not block it.
    });
    return () => controller.abort();
  }, [activeResultFilter, connectionStatus, hasResult, resultSearch, scopedPendingKey, selectedResultProvider]);

  useEffect(() => {
    if (!selectAllResultsRef.current) return;
    selectAllResultsRef.current.indeterminate = selectedVisibleResultRows.length > 0 && !allVisibleResultsSelected;
  }, [allVisibleResultsSelected, selectedVisibleResultRows.length]);

  useEffect(() => {
    setSelectedResultKeys(new Set());
  }, [activeResultFilter, resultPage, resultPageSize, resultPendingOnly, resultSearch, selectedResultProvider]);

  useEffect(() => {
    if (resultsTableViewportRef.current) {
      resultsTableViewportRef.current.scrollTop = 0;
    }
  }, [activeResultFilter, resultPage, resultPageSize, resultPendingOnly, resultSearch, selectedResultProvider]);

  const handleExecuteSingle = useCallback(
    (item: AccountInspectionResultItem, manualAction?: ManualAccountInspectionAction, isSuggestedOverride = false) => {
      if (singleMutationBlocked) return;
      const target = manualAction
        ? { ...buildManualActionItem(item, manualAction), suggested: isSuggestedOverride }
        : { ...item, suggested: true };
      const actionLabel = formatInspectionExecutionLabel(target, t);
      const isDelete = target.action === 'delete';
      showConfirmation({
        dedupeKey: `account-inspection:execute:${target.key}:${target.action}`,
        title: isDelete
          ? t('monitoring.account_inspection_delete_single_title')
          : t('monitoring.account_inspection_execute_single_title'),
        message: isDelete
          ? buildDeleteConfirmationMessage(target, t)
          : buildExecuteConfirmationMessage(
            [target],
            t
          ),
        confirmText: actionLabel,
        cancelText: t('common.cancel'),
        variant: isDelete ? 'danger' : 'primary',
        onConfirm: () => executeItems([target]),
      });
    },
    [executeItems, showConfirmation, singleMutationBlocked, t]
  );

  const handleRecheckSingle = useCallback(
    async (item: AccountInspectionResultItem) => {
      if (singleMutationBlocked) return;
      if (restoredSnapshot) {
        showNotification(t('monitoring.account_inspection_restored_snapshot_action_blocked'), 'warning');
        return;
      }
      if (connectionStatus !== 'connected') {
        showNotification(t('notification.connection_required'), 'warning');
        return;
      }
      setRecheckingKey(item.key);
      setLogsCollapsed(false);
      appendLog('info', t('monitoring.account_inspection_recheck_started', {
        account: item.fileName,
      }));
      try {
        const response = await accountInspectionApi.inspectOne(toAccountInspectionApiItem(item), currentInspectionDetailOptions);
        applyBackendResponse(response);
        if (response.result?.key) {
          const toast = formatInspectionResultToast(accountInspectionBackendResultToItem(response.result), t);
          showNotification(toast.message, toast.tone);
        } else if (response.error) {
          showNotification(response.error, 'error');
        }
      } catch (error) {
        handleAccountInspectionControlError(error, appendLog, showNotification, t('common.unknown_error'));
      } finally {
        setRecheckingKey(null);
      }
    },
    [appendLog, applyBackendResponse, connectionStatus, currentInspectionDetailOptions, restoredSnapshot, showNotification, singleMutationBlocked, t]
  );

  const toggleResultSelection = useCallback((key: string, selected: boolean) => {
    if (selected && visibleResultRows.some(({ item }) => item.key === key && item.executedEffect === 'delete')) return;
    setSelectedResultKeys((current) => {
      const next = new Set(current);
      if (selected) next.add(key);
      else next.delete(key);
      return next;
    });
  }, [visibleResultRows]);

  const toggleVisibleResultSelection = useCallback((selected: boolean) => {
    setSelectedResultKeys(selected ? new Set(visibleResultRows.filter(({ item }) => item.executedEffect !== 'delete').map(({ item }) => item.key)) : new Set());
  }, [visibleResultRows]);

  const confirmBatch = useCallback((operations: AccountInspectionBatchOperation[], scope: ResultBulkScope, preservePrevious = false) => {
    setBatchOperations((current) => preservePrevious
      ? [...current.filter((existing) => !operations.some((operation) => operation.operationId === existing.operationId)), ...operations]
      : operations);
    const summary = operations.reduce((total, operation) => ({
      total: total.total + operation.summary.total,
      ready: total.ready + operation.summary.ready,
      stale: total.stale + operation.summary.stale,
      unsupported: total.unsupported + operation.summary.unsupported,
    }), { total: 0, ready: 0, stale: 0, unsupported: 0 });
    const actionGroups = new Map<string, number>();
    for (const operation of operations) {
      for (const entry of operation.items) {
        if (entry.status !== 'ready') continue;
        actionGroups.set(entry.effect, (actionGroups.get(entry.effect) ?? 0) + 1);
      }
    }
    const scopeLabel = t(scope === 'selected'
      ? 'monitoring.account_inspection_batch_scope_selected'
      : scope === 'filtered'
        ? 'monitoring.account_inspection_batch_scope_filtered'
        : 'monitoring.account_inspection_batch_scope_fixed');
    if (summary.ready === 0) {
      showNotification(t('monitoring.account_inspection_batch_no_ready'), 'warning');
      return;
    }
    const operationIds = operations.map((operation) => operation.operationId).join(':');
    const selectedTargets = operations.flatMap((operation) => operation.items)
      .filter((entry) => entry.status === 'ready')
      .map(({ item }) => `${item.key}:${item.action}`)
      .sort()
      .join('|');
    const confirmationIdentity = scope === 'selected'
      ? { dedupeKey: `account-inspection:execute:selected:${selectedTargets}:${operationIds}` }
      : scope === 'filtered'
        ? { dedupeKey: `account-inspection:execute:filtered:${operationIds}` }
        : { dedupeKey: `account-inspection:execute:batch:${operationIds}` };
    showConfirmation({
      ...confirmationIdentity,
      title: t('monitoring.account_inspection_batch_preflight_title'),
      message: (
        <div className={styles.batchPreflight}>
          <strong>{scopeLabel} · {t('monitoring.account_inspection_batch_ready_count', { count: summary.ready })}</strong>
          {scope === 'filtered' && operations.some((operation) => operation.items.some(({ item }) => item.suggested))
            ? <span>{t('monitoring.account_inspection_batch_filtered_suggestions')}</span> : null}
          <span>{t('monitoring.account_inspection_batch_preflight_counts', {
            total: summary.total,
            ready: summary.ready,
            stale: summary.stale,
            unsupported: summary.unsupported,
          })}</span>
          <div className={styles.batchPreflightStats}>
            {Array.from(actionGroups, ([group, count]) => (
              <span key={group}>{t(`monitoring.account_inspection_batch_group_${group}`)} · {count}</span>
            ))}
          </div>
          {actionGroups.has('inspect') || actionGroups.has('quota_recovery') || actionGroups.has('recovery_check')
            ? <span>{t('monitoring.account_inspection_batch_upstream_cost')}</span> : null}
          {scope !== 'fixed' ? <span>{t('monitoring.account_inspection_batch_filter_context', {
            filter: batchFilterLabel,
            provider: selectedResultProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
              ? t('monitoring.filter_all_providers') : resolveProviderDisplayLabel(selectedResultProvider),
            search: resultSearch || t('monitoring.account_inspection_batch_no_search'),
            pending: resultPendingOnly || scope === 'filtered' && operations.some((operation) => operation.items.some(({ item }) => item.suggested))
              ? t('monitoring.account_inspection_filter_pending_only')
              : t('monitoring.account_inspection_batch_all_states'),
          })}</span> : null}
          <div className={styles.batchPreflightList}>
            {operations.flatMap((operation) => operation.items.map(({ key, item, status, error }) => (
              <span key={`${operation.operationId}:${key}`}>
                {item.displayName || item.fileName} · {t(`monitoring.account_inspection_batch_status_${status}`)}
                {error ? ` · ${error}` : ''}
              </span>
            )))}
          </div>
        </div>
      ),
      confirmText: t('monitoring.account_inspection_batch_confirm', { count: summary.ready }),
      cancelText: t('common.cancel'),
      variant: operations.some((operation) => operation.items.some(({ item, status }) => item.action === 'delete' && status === 'ready')) ? 'danger' : 'primary',
      onConfirm: () => {
        setSelectedResultKeys(new Set());
        setBatchError('');
        setBulkActionLoading(true);
        void Promise.all(operations.map(async (operation) => {
          try {
            return await accountInspectionApi.executeBatch(operation.operationId);
          } catch (error) {
            try {
              return await accountInspectionApi.getBatch(operation.operationId);
            } catch {
              throw error;
            }
          }
        }))
          .then((started) => {
            const byId = new Map(started.map((operation) => [operation.operationId, operation]));
            setBatchOperations((current) => current.map((operation) => byId.get(operation.operationId) ?? operation));
            if (started.every((operation) => operation.state === 'completed')) {
              const { currentInspectionDetailOptions: options, applyBackendResponse: apply, loadAuthFiles: load } = batchRefreshRef.current;
              void accountInspectionApi.getStatus(options)
                .then((response) => { apply(response); void load(); })
                .catch((error) => setBatchError(error instanceof Error ? error.message : String(error)));
            }
          })
          .catch((error) => setBatchError(error instanceof Error ? error.message : String(error)))
          .finally(() => setBulkActionLoading(false));
      },
    });
  }, [batchFilterLabel, resultPendingOnly, resultSearch, selectedResultProvider, showConfirmation, showNotification, t]);

  const handleExecuteSelectedResults = useCallback(async () => {
    if (restoredSnapshot || runStatus === 'running' || batchOperations.some((operation) => operation.state === 'running')) return;
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    const kind: AccountInspectionBatchKind = resultBulkAction === 'recheck'
      ? 'inspect'
      : resultBulkAction === 'recover' ? 'recover' : 'action';
    let scope: AccountInspectionBatchScope;
    if (resultBulkScope === 'selected') {
      const selected = selectedVisibleResultRows.map(({ item }) => item);
      const targets = resultBulkAction === 'suggested'
        ? selected.filter((item) => isSuggestedAction(item) && !item.executed)
        : selected;
      if (targets.length === 0) {
        showNotification(t('monitoring.account_inspection_no_selected_suggestions'), 'info');
        return;
      }
      scope = {
        type: 'selected',
        items: targets.map((item) => ({
          ...toAccountInspectionApiItem(item),
          resultRef: item.resultRef || '',
          action: resultBulkAction === 'recheck' ? 'keep'
            : resultBulkAction === 'recover' ? 'enable'
              : resultBulkAction === 'suggested' ? item.action : resultBulkAction,
          suggested: resultBulkAction === 'suggested',
        })),
      };
    } else {
      scope = {
        type: 'filtered',
        filter: activeResultFilter,
        provider: selectedResultProvider,
        search: resultSearch,
        pendingOnly: resultPendingOnly,
      };
      if (resultBulkAction === 'suggested') scope.suggested = true;
      else if (resultBulkAction === 'recheck') scope.action = 'keep';
      else if (resultBulkAction === 'recover') {
        scope.action = 'enable';
        scope.suggested = false;
      } else if (resultBulkAction === 'disable' || resultBulkAction === 'enable' || resultBulkAction === 'delete') {
        scope.action = resultBulkAction;
        scope.suggested = false;
      }
    }
    setBulkActionLoading(true);
    setBatchError('');
    try {
      const operation = await accountInspectionApi.preflightBatch(kind, scope);
      confirmBatch([operation], resultBulkScope);
    } catch (error) {
      setBatchError(error instanceof Error ? error.message : String(error));
    } finally {
      setBulkActionLoading(false);
    }
  }, [activeResultFilter, batchOperations, confirmBatch, connectionStatus, restoredSnapshot, resultBulkAction, resultBulkScope, resultPendingOnly, resultSearch, runStatus, selectedResultProvider, selectedVisibleResultRows, showNotification, t]);

  const handleExecutePlanned = useCallback(async () => {
    if (!result || restoredSnapshot || runStatus === 'running' || batchOperations.some((operation) => operation.state === 'running')) return;
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    setBulkActionLoading(true);
    setBatchError('');
    try {
      const current = await accountInspectionApi.getStatus({
        includeDetails: true,
        resultPage: 1,
        resultPageSize: 1,
        logPageSize: 1,
        resultFilter: activeResultFilter,
        resultProvider: selectedResultProvider,
        resultSearch,
        resultPendingOnly: true,
      });
      if (current.status.resultsPage?.total === 0) {
        setScopedPending({ key: scopedPendingKey, count: 0 });
        showNotification(t('monitoring.account_inspection_batch_no_ready'), 'info');
        return;
      }
      const operation = await accountInspectionApi.preflightBatch('action', {
        type: 'filtered',
        filter: activeResultFilter,
        provider: selectedResultProvider,
        search: resultSearch,
        pendingOnly: true,
        suggested: true,
      });
      confirmBatch([operation], 'filtered');
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      setBatchError(message);
      showNotification(message, 'error');
    } finally {
      setBulkActionLoading(false);
    }
  }, [activeResultFilter, batchOperations, confirmBatch, connectionStatus, restoredSnapshot, result, resultSearch, runStatus, scopedPendingKey, selectedResultProvider, showNotification, t]);

  const handleRetryBatchFailures = useCallback(async (operationId: string) => {
    setBulkActionLoading(true);
    setBatchError('');
    try {
      const prepared = await accountInspectionApi.retryBatch(operationId);
      confirmBatch([prepared], 'selected', true);
    } catch (error) {
      setBatchError(error instanceof Error ? error.message : String(error));
    } finally {
      setBulkActionLoading(false);
    }
  }, [confirmBatch]);

  const quotaStore = useMemo(
    () => ({ antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, kimiQuota, xaiQuota }),
    [antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, kimiQuota, xaiQuota]
  );

  const authFilesByName = useMemo(
    () => new Map(authFiles.map((file) => [file.name, file])),
    [authFiles]
  );

  useEffect(() => {
    if (!authFilesLoaded) {
      setAuthFileStats(createEmptyAuthFileAccountStats());
      setAuthFileStatsReady(false);
      return;
    }

    setAuthFileStatsReady(false);
    const job: AuthFileAccountStatsJob = { cancelled: false, handle: null, cancelHandle: null };
    scheduleAuthFileAccountStats(
      job,
      authFiles,
      quotaStore,
      inspectionSettings.usedPercentThreshold,
      inspectionSettings.antigravityQuotaMode,
      (nextStats) => {
        setAuthFileStats(nextStats);
        setAuthFileStatsReady(true);
      }
    );

    return () => {
      job.cancelled = true;
      if (job.handle !== null) job.cancelHandle?.(job.handle);
    };
  }, [authFiles, authFilesLoaded, inspectionSettings.antigravityQuotaMode, inspectionSettings.usedPercentThreshold, quotaStore]);

  useEffect(() => {
    if (!authFileStatsReady) return;
    if (selectedAssetProvider === 'all') return;
    if (authFileStats.providers.some((provider) => provider.provider === selectedAssetProvider)) return;
    setSelectedAssetProvider('all');
  }, [authFileStats.providers, authFileStatsReady, selectedAssetProvider]);

  const selectedAssetStats = useMemo<AuthFileAccountStats | ProviderAccountStats>(() => {
    if (selectedAssetProvider === 'all') return authFileStats;
    return authFileStats.providers.find((provider) => provider.provider === selectedAssetProvider) ?? authFileStats;
  }, [authFileStats, selectedAssetProvider]);

  const selectedAssetLabel = selectedAssetProvider === 'all'
    ? t('monitoring.filter_all_accounts')
    : resolveProviderDisplayLabel(selectedAssetProvider);
  const exportAuthFilesLabel = selectedAssetProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
    ? t('monitoring.account_inspection_auth_files_export_all')
    : t('monitoring.account_inspection_auth_files_export_selected', { provider: selectedAssetLabel });

  const accountAssetCards = useMemo<SummaryCard[]>(() => [
    {
      key: 'total',
      label: t('auth_files.problem_filter_all'),
      value: authFileStatsReady ? String(selectedAssetStats.total) : '--',
      description: selectedAssetLabel,
    },
    {
      key: 'enabled',
      label: t('auth_files.problem_filter_enabled'),
      value: authFileStatsReady ? String(selectedAssetStats.enabled) : '--',
      description: t('monitoring.account_inspection_account_enabled_desc'),
    },
    {
      key: 'disabled',
      label: t('auth_files.problem_filter_disabled'),
      value: authFileStatsReady ? String(selectedAssetStats.disabled) : '--',
      description: t('monitoring.account_inspection_account_disabled_desc'),
    },
    {
      key: 'problem',
      label: t('auth_files.problem_filter_problem'),
      value: authFileStatsReady ? String(selectedAssetStats.problem) : '--',
      description: t('monitoring.account_inspection_account_problem_desc'),
      tone: authFileStatsReady && selectedAssetStats.problem > 0 ? 'bad' : 'neutral',
    },
  ], [authFileStatsReady, selectedAssetLabel, selectedAssetStats, t]);

  const actionStats = useMemo(() => {
    const autoTotal = autoExecutionCounts.delete + autoExecutionCounts.disable + autoExecutionCounts.enable + autoExecutionCounts.quotaProtection + autoExecutionCounts.quotaRecovery;
    const manualDelete = result?.summary.pendingDeleteCount ?? actionableActionCounts.delete;
    const manualDisable = result?.summary.pendingDisableCount ?? actionableActionCounts.disable;
    const manualEnable = result?.summary.pendingEnableCount ?? actionableActionCounts.enable;
    const manualTotal = result?.summary.pendingActionCount ?? manualDelete + manualDisable + manualEnable;
    return {
      autoTotal,
      manualTotal,
      autoDelete: autoExecutionCounts.delete,
      autoDisable: autoExecutionCounts.disable,
      autoEnable: autoExecutionCounts.enable,
      autoQuotaProtection: autoExecutionCounts.quotaProtection,
      autoQuotaRecovery: autoExecutionCounts.quotaRecovery,
      manualDelete,
      manualDisable,
      manualEnable,
      keep: result?.summary.keepCount ?? 0,
      error: result?.summary.errorCount ?? 0,
    };
  }, [actionableActionCounts, autoExecutionCounts, result]);

  const pendingActionCount = actionStats.manualTotal;
  const inspectionScopeLabel = inspectionSettings.targetType === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE
    ? t('monitoring.filter_all_providers')
    : resolveProviderDisplayLabel(inspectionSettings.targetType);
  const settingEnabledLabel = t('monitoring.account_inspection_setting_enabled');
  const settingDisabledLabel = t('monitoring.account_inspection_setting_disabled');
  const quotaLimitAutoLabel = inspectionSettings.autoExecuteQuotaLimitDisable ? settingEnabledLabel : settingDisabledLabel;
  const quotaRecoveryAutoLabel = inspectionSettings.autoExecuteQuotaRecoveryEnable ? settingEnabledLabel : settingDisabledLabel;
  const accountInvalidActionLabel = t(
    AUTO_ERROR_ACTION_OPTIONS.find((option) => option.value === inspectionSettings.autoExecuteAccountInvalidAction)?.labelKey
      ?? 'monitoring.account_inspection_settings_account_error_action_none'
  );
  const requestErrorActionLabel = t(
    AUTO_ERROR_ACTION_OPTIONS.find((option) => option.value === inspectionSettings.autoExecuteRequestErrorAction)?.labelKey
      ?? 'monitoring.account_inspection_settings_account_error_action_none'
  );
  const scheduleStatusLabel = schedule?.enabled
    ? formatInspectionInterval(schedule.intervalMinutes, i18n.language)
    : settingDisabledLabel;
  const autoExecutionResultLabel = !result
    ? t('monitoring.account_inspection_auto_execute_pending')
    : actionStats.autoTotal > 0
      ? [
          `${t('monitoring.account_inspection_action_enable')}: ${actionStats.autoEnable}`,
          `${t('monitoring.account_inspection_action_disable')}: ${actionStats.autoDisable}`,
          `${t('monitoring.account_inspection_action_delete')}: ${actionStats.autoDelete}`,
          `${t('monitoring.account_inspection_action_quota_protection')}: ${actionStats.autoQuotaProtection}`,
          `${t('monitoring.account_inspection_quota_recovery_enable_short')}: ${actionStats.autoQuotaRecovery}`,
        ].join(' · ')
      : t('monitoring.account_inspection_auto_execute_no_actions');
  const showInspectionResults = useCallback((filter: ResultStatusFilter | ResultReasonFilter) => {
    if (filter === 'all' || filter === 'accountIssues' || filter === 'quotaChanges' || filter === 'highAvailable' || filter === 'unknown') {
      setResultStatusFilter(filter);
      setResultReasonFilter(null);
    } else {
      setResultStatusFilter(filter === 'accountInvalid' || filter === 'requestError' ? 'accountIssues' : 'quotaChanges');
      setResultReasonFilter(filter);
    }
    setResultPendingOnly(false);
    requestAnimationFrame(() => resultsPanelRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }));
  }, []);

  const operationPhase = useMemo(() => {
    if (executing || bulkActionLoading) return t('monitoring.account_inspection_phase_executing');
    if (runStatus === 'paused') return t('monitoring.account_inspection_phase_paused');
    if (runStatus === 'running') {
      if (progress.completed <= 0 && progress.inFlight <= 0) {
        return t('monitoring.account_inspection_phase_initializing');
      }
      return t('monitoring.account_inspection_phase_probing');
    }
    if (runStatus === 'failed') return t('monitoring.account_inspection_phase_failed');
    if (runStatus === 'partial') return t('monitoring.account_inspection_phase_partial');
    if (runStatus === 'stopped') return t('monitoring.account_inspection_phase_stopped');
    if (result && pendingActionCount > 0) return t('monitoring.account_inspection_phase_review');
    if (result) return t('monitoring.account_inspection_phase_completed');
    return t('monitoring.account_inspection_phase_idle');
  }, [bulkActionLoading, executing, pendingActionCount, progress.completed, progress.inFlight, result, runStatus, t]);

  const resultEmptyMessage = runStatus === 'running'
    ? t('monitoring.account_inspection_results_generating')
    : runStatus === 'failed'
      ? t('monitoring.account_inspection_results_error_empty')
      : t('monitoring.account_inspection_empty');
  const accountIssueResultCount = displayedHealthCounts.authInvalid + displayedHealthCounts.inspectionError;
  const quotaChangeResultCount = displayedHealthCounts.quotaExhausted + displayedHealthCounts.recoverable;
  const resultStatusFilterOptions = useMemo(() => [
    { value: 'all', label: `${t('monitoring.account_inspection_filter_all')} · ${displayedHealthCounts.total}` },
    { value: 'accountIssues', label: `${t('monitoring.account_inspection_filter_account_issues')} · ${accountIssueResultCount}` },
    { value: 'quotaChanges', label: `${t('monitoring.account_inspection_filter_quota_changes')} · ${quotaChangeResultCount}` },
    { value: 'highAvailable', label: `${t('monitoring.account_inspection_high_available')} · ${displayedHealthCounts.healthy}` },
    { value: 'unknown', label: `${t('monitoring.account_inspection_health_unknown')} · ${displayedHealthCounts.unknown ?? 0}` },
  ], [accountIssueResultCount, displayedHealthCounts.healthy, displayedHealthCounts.total, displayedHealthCounts.unknown, quotaChangeResultCount, t]);
  const resultReasonLabels = useMemo<Record<ResultReasonFilter, string>>(() => ({
    accountInvalid: t('monitoring.account_inspection_account_invalid'),
    requestError: t('monitoring.account_inspection_account_request_error'),
    quotaExhausted: t('monitoring.account_inspection_health_quota_exhausted'),
    recoverable: t('monitoring.account_inspection_health_recoverable'),
  }), [t]);
  const resultBulkActionOptions = useMemo(() => [
    { value: 'suggested', label: t('monitoring.account_inspection_bulk_action_suggested') },
    { value: 'recheck', label: t('monitoring.account_inspection_bulk_action_recheck') },
    { value: 'recover', label: t('monitoring.account_inspection_bulk_action_recover') },
    { value: 'disable', label: t('monitoring.account_inspection_action_disable') },
    { value: 'enable', label: t('monitoring.account_inspection_action_enable') },
    { value: 'delete', label: t('monitoring.account_inspection_action_delete') },
  ], [t]);
  const resultProviderOptions = useMemo(
    () => {
      const providers = new Set<string>();
      authFileStats.providers.forEach((provider) => providers.add(provider.provider));
      result?.results.forEach((item) => {
        const provider = item.provider.trim().toLowerCase();
        if (ACCOUNT_INSPECTION_SUPPORTED_PROVIDER_SET.has(provider)) providers.add(provider);
      });
      return [
        { value: ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE, label: t('monitoring.filter_all_providers') },
        ...Array.from(providers).map((provider) => ({
          value: provider,
          label: resolveProviderDisplayLabel(provider),
        })),
      ];
    },
    [authFileStats.providers, result, t]
  );

  useEffect(() => {
    if (selectedResultProvider === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE) return;
    if (resultProviderOptions.some((option) => option.value === selectedResultProvider)) return;
    setSelectedResultProvider(ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE);
  }, [resultProviderOptions, selectedResultProvider]);
  const logLevelOptions = useMemo<Array<{ key: AccountInspectionLogLevel | 'all'; label: string }>>(() => [
    { key: 'all', label: t('monitoring.account_inspection_filter_all') },
    { key: 'success', label: t('monitoring.account_inspection_log_success') },
    { key: 'warning', label: t('monitoring.account_inspection_log_warning') },
    { key: 'error', label: t('monitoring.account_inspection_log_error') },
  ], [t]);
  const progressLabel =
    progress.total > 0
      ? t('monitoring.account_inspection_progress_status', {
          completed: progress.completed,
          total: progress.total,
          inFlight: progress.inFlight,
          pending: progress.pending,
          percent: progress.percent,
        })
      : t('monitoring.account_inspection_progress_idle');
  const completedDuration = result && ['completed', 'partial', 'stopped', 'failed'].includes(result.state)
    ? formatAccountInspectionDuration(result.startedAt, result.finishedAt, t)
    : null;
  const completedProgressLabel = completedDuration
    ? t(result?.state === 'completed' ? 'monitoring.account_inspection_completed_with_duration' : 'monitoring.account_inspection_ended_with_duration', { duration: completedDuration })
    : t('monitoring.account_inspection_phase_completed');
  const openSettingsModal = useCallback(() => {
    dispatchBackendState({ type: 'discardSettingsDraft' });
    setIsSettingsModalOpen(true);
  }, [setIsSettingsModalOpen]);

  const handleSettingsDraftChange = useCallback(
    (field: InspectionSettingsDraftField, value: string) => {
      dispatchBackendState({
        type: 'updateSettingsDraft',
        values: { [field]: value },
      });
    },
    []
  );

  const handleAntigravityDeepProbeChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { antigravityDeepProbeEnabled: value },
    });
  }, []);

  const handleXAIDeepProbeChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { xaiDeepProbeEnabled: value },
    });
  }, []);

  const handleAntigravityQuotaModeChange = useCallback((value: string) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { antigravityQuotaMode: normalizeAntigravityQuotaMode(value) },
    });
  }, []);

  const handleAutoExecuteQuotaLimitChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { autoExecuteQuotaLimitDisable: value },
    });
  }, []);

  const handleAutoExecuteQuotaRecoveryChange = useCallback((value: boolean) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: { autoExecuteQuotaRecoveryEnable: value },
    });
  }, []);

  const handleAutoExecuteAccountInvalidActionChange = useCallback((value: string) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: {
        autoExecuteAccountInvalidAction: normalizeAutoErrorAction(value),
      },
    });
  }, []);

  const handleAutoExecuteRequestErrorActionChange = useCallback((value: string) => {
    dispatchBackendState({
      type: 'updateSettingsDraft',
      values: {
        autoExecuteRequestErrorAction: normalizeAutoErrorAction(value),
      },
    });
  }, []);

  const parseIntegerInRange = useCallback(
    (value: string, label: string, min: number, max?: number) => {
      const parsed = Number(value.trim());
      if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < min || (max !== undefined && parsed > max)) {
        throw new Error(
          max === undefined
            ? t('monitoring.account_inspection_settings_invalid_integer', { field: label, min })
            : t('monitoring.account_inspection_settings_invalid_integer_range', { field: label, min, max })
        );
      }
      return parsed;
    },
    [t]
  );

  const handleSaveSettings = useCallback(async () => {
    const targetType = settingsDraft.targetType.trim().toLowerCase();
    if (!targetType) {
      showNotification(t('monitoring.account_inspection_settings_target_type_required'), 'error');
      return;
    }

    try {
      const nextSettings = {
        targetType,
        workers: parseIntegerInRange(
          settingsDraft.workers,
          t('monitoring.account_inspection_settings_workers_label'),
          WORKER_LIMITS.min,
          WORKER_LIMITS.max
        ),
        providerWorkers: parseIntegerInRange(
          settingsDraft.providerWorkers,
          t('monitoring.account_inspection_settings_provider_workers_label'),
          PROVIDER_WORKER_LIMITS.min,
          PROVIDER_WORKER_LIMITS.max
        ),
        deleteWorkers: parseIntegerInRange(
          settingsDraft.deleteWorkers,
          t('monitoring.account_inspection_settings_delete_workers_label'),
          DELETE_WORKER_LIMITS.min,
          DELETE_WORKER_LIMITS.max
        ),
        timeout: parseIntegerInRange(
          settingsDraft.timeout,
          t('monitoring.account_inspection_settings_timeout_label'),
          TIMEOUT_LIMITS.min,
          TIMEOUT_LIMITS.max
        ),
        retries: parseIntegerInRange(
          settingsDraft.retries,
          t('monitoring.account_inspection_settings_retries_label'),
          RETRY_LIMITS.min,
          RETRY_LIMITS.max
        ),
        sampleSize: parseIntegerInRange(
          settingsDraft.sampleSize,
          t('monitoring.account_inspection_settings_sample_size_label'),
          SAMPLE_SIZE_LIMITS.min
        ),
        usedPercentThreshold: (() => {
          const parsed = Number(settingsDraft.usedPercentThreshold.trim());
          if (!Number.isFinite(parsed) || parsed < THRESHOLD_LIMITS.min || parsed > THRESHOLD_LIMITS.max) {
            throw new Error(
              t('monitoring.account_inspection_settings_invalid_threshold', {
                field: t('monitoring.account_inspection_settings_used_percent_threshold_label'),
              })
            );
          }
          return parsed;
        })(),
        antigravityDeepProbeEnabled: settingsDraft.antigravityDeepProbeEnabled,
        antigravityDeepProbeModel: settingsDraft.antigravityDeepProbeModel,
        antigravityQuotaMode: settingsDraft.antigravityQuotaMode,
        xaiDeepProbeEnabled: settingsDraft.xaiDeepProbeEnabled,
        xaiDeepProbeModel: settingsDraft.xaiDeepProbeModel,
        autoExecuteQuotaLimitDisable: settingsDraft.autoExecuteQuotaLimitDisable,
        autoExecuteQuotaRecoveryEnable: settingsDraft.autoExecuteQuotaRecoveryEnable,
        autoExecuteAccountInvalidAction: settingsDraft.autoExecuteAccountInvalidAction,
        autoExecuteRequestErrorAction: settingsDraft.autoExecuteRequestErrorAction,
        autoExecuteConfirmations: parseIntegerInRange(
          settingsDraft.autoExecuteConfirmations,
          t('monitoring.account_inspection_settings_auto_execute_confirmations_label'),
          AUTO_EXECUTE_CONFIRMATION_LIMITS.min,
          AUTO_EXECUTE_CONFIRMATION_LIMITS.max
        ),
      };

      const intervalMinutes = parseIntegerInRange(
        scheduleDraft.intervalMinutes,
        t('monitoring.account_inspection_schedule_interval_label'),
        SCHEDULE_INTERVAL_LIMITS.min
      );
      setScheduleLoading(true);
      const response = await accountInspectionApi.updateSchedule({
        enabled: scheduleDraft.enabled,
        intervalMinutes,
        nextRunAt: scheduleDraft.enabled
          ? (schedule?.nextRunAt ?? 0)
          : 0,
        settings: nextSettings,
      });
      saveAccountInspectionConfigurableSettings(nextSettings);
      applyBackendResponse(response);
      setIsSettingsModalOpen(false);
      showNotification(t('monitoring.account_inspection_settings_saved'), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setScheduleLoading(false);
    }
  }, [applyBackendResponse, parseIntegerInRange, schedule, scheduleDraft.enabled, scheduleDraft.intervalMinutes, setIsSettingsModalOpen, settingsDraft, showNotification, t]);

  const handleResetSettings = useCallback(() => {
    dispatchBackendState({ type: 'resetSettings', settings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS });
    showNotification(t('monitoring.account_inspection_settings_reset_draft'), 'info');
  }, [showNotification, t]);

  const confirmSettingsModalClose = useCallback((): Promise<boolean> => {
    if (!settingsDirty) return Promise.resolve(true);
    return new Promise<boolean>((resolve) => {
      const accepted = showConfirmation({
        dedupeKey: 'account-inspection-settings:close-workspace',
        title: t('monitoring.account_inspection_settings_unsaved_title'),
        message: t('monitoring.account_inspection_settings_unsaved_desc'),
        confirmText: t('monitoring.account_inspection_settings_discard'),
        cancelText: t('common.stay'),
        variant: 'danger',
        onConfirm: () => resolve(true),
        onCancel: () => resolve(false),
      });
      if (!accepted) resolve(false);
    });
  }, [settingsDirty, showConfirmation, t]);
  const discardSettingsModalDraft = useCallback(() => {
    dispatchBackendState({ type: 'discardSettingsDraft' });
  }, []);
  const closeSettingsModal = useCallback(() => {
    setIsSettingsModalOpen(false);
  }, [setIsSettingsModalOpen]);

  const draftScheduleStatusLabel = scheduleDraft.enabled
    ? formatInspectionInterval(Number(scheduleDraft.intervalMinutes) || 0, i18n.language)
    : settingDisabledLabel;
  const draftQuotaModeLabel = t(
    ANTIGRAVITY_QUOTA_MODE_OPTIONS.find((option) => option.value === settingsDraft.antigravityQuotaMode)?.labelKey
      ?? 'monitoring.account_inspection_settings_antigravity_quota_mode_claude_gpt'
  );
  return (
    <div className={styles.page}>
      <Card className={styles.heroCard}>
        <div className={styles.heroHeader}>
          <div className={styles.heroCopy}>
            <h1 className={styles.heroTitle}>{t('monitoring.account_inspection_title')}</h1>
            <p className={styles.heroSubtitle}>{t('monitoring.account_inspection_desc')}</p>
          </div>
        </div>

        <div className={styles.assetOverviewGrid}>
          <div className={styles.assetKpiGrid}>
            {accountAssetCards.map((card, index) => (
              <Card
                key={card.key}
                className={[styles.professionalMetricCard, styles[`professionalMetricCard${index + 1}`], summaryToneClass[card.tone ?? 'neutral']]
                  .filter(Boolean)
                  .join(' ')}
              >
                <div className={styles.professionalMetricHeader}>
                  <span className={styles.professionalMetricIcon} aria-hidden="true" />
                  <strong>{card.label}</strong>
                </div>
                <div className={styles.professionalMetricBody}>
                  <span>{card.description}</span>
                  <strong>{card.value}</strong>
                </div>
              </Card>
            ))}
          </div>

          <Card className={styles.providerDistributionCard}>
            <div className={styles.providerDistributionHeader}>
              <div>
                <h3>{t('monitoring.account_inspection_provider_distribution_title')}</h3>
                <p>{authFileStatsReady ? t('monitoring.account_inspection_provider_distribution_desc', { count: authFileStats.providerCount }) : t('common.loading')}</p>
              </div>
              <Button
                variant="secondary"
                size="sm"
                className={styles.providerDistributionSelection}
                onClick={handleExportAuthFiles}
                loading={exportingAuthFiles}
                disabled={exportingAuthFiles || connectionStatus !== 'connected'}
                aria-label={exportAuthFilesLabel}
                title={exportAuthFilesLabel}
              >
                {exportingAuthFiles ? null : <IconDownload size={15} />}
                {exportAuthFilesLabel}
              </Button>
            </div>
            <div className={styles.providerSelectorList}>
              <button
                type="button"
                className={[styles.providerSelectorRow, selectedAssetProvider === 'all' ? styles.providerSelectorRowActive : '']
                  .filter(Boolean)
                  .join(' ')}
                onClick={() => setSelectedAssetProvider('all')}
              >
                <div>
                  <span className={styles.providerSelectorTitle}>
                    <span className={styles.providerLogoFallback} aria-hidden="true">Σ</span>
                    <strong>{t('monitoring.filter_all_accounts')}</strong>
                  </span>
                  <span>{authFileStatsReady ? `${authFileStats.total} ${t('monitoring.account_inspection_account_total')} · ${authFileStats.highAvailable} ${t('monitoring.account_inspection_high_available')}` : t('common.loading')}</span>
                </div>
                <span aria-hidden="true">
                  <i style={buildHighAvailabilityBarStyle(authFileStats.highAvailable, authFileStats.total)} />
                </span>
              </button>
              {authFileStats.providers.length > 0 ? authFileStats.providers.map((provider) => (
                  <button
                    type="button"
                    key={provider.provider}
                    className={[styles.providerSelectorRow, selectedAssetProvider === provider.provider ? styles.providerSelectorRowActive : '']
                      .filter(Boolean)
                      .join(' ')}
                    onClick={() => setSelectedAssetProvider(provider.provider)}
                  >
                    <div>
                      <span className={styles.providerSelectorTitle}>
                        {getAuthFileIcon(provider.provider, resolvedTheme) ? (
                          <img src={getAuthFileIcon(provider.provider, resolvedTheme) ?? ''} alt="" aria-hidden="true" />
                        ) : (
                          <span className={styles.providerLogoFallback} aria-hidden="true">{getProviderInitial(resolveProviderDisplayLabel(provider.provider))}</span>
                        )}
                        <strong>{resolveProviderDisplayLabel(provider.provider)}</strong>
                      </span>
                      <span>{`${provider.total} ${t('monitoring.account_inspection_account_total')} · ${provider.highAvailable} ${t('monitoring.account_inspection_high_available')}`}</span>
                    </div>
                    <span aria-hidden="true">
                      <i style={buildHighAvailabilityBarStyle(provider.highAvailable, provider.total)} />
                    </span>
                  </button>
              )) : <div className={styles.emptyBlockSmall}>{authFileStatsReady ? t('monitoring.account_inspection_empty') : t('common.loading')}</div>}
            </div>
          </Card>
        </div>
      </Card>

      {restoredSnapshot ? (
        <div className={styles.restoredSnapshotNotice} role="status">
          <strong>{t('monitoring.account_inspection_restored_snapshot_title')}</strong>
          <span>{t('monitoring.account_inspection_restored_snapshot_desc')}</span>
        </div>
      ) : null}

      {persistenceError ? (
        <div className={styles.inspectionAlert} role="alert">
          <strong>{t('monitoring.account_inspection_persistence_error_title')}</strong>
          <span>{persistenceError}</span>
        </div>
      ) : null}

      <section className={styles.operationSection}>
        <div className={styles.operationModuleHeader}>
          <div>
            <h2>{t('monitoring.account_inspection_control_title')}</h2>
            <p>{t('monitoring.account_inspection_control_desc')}</p>
          </div>
          <div className={styles.heroActions}>
            <Button
              variant="secondary"
              className={styles.heroActionButton}
              onClick={openSettingsModal}
              disabled={(runStatus === 'running' || runStatus === 'paused') || executing}
            >
              {t('monitoring.account_inspection_settings_button')}
            </Button>
          </div>
        </div>
        <div className={styles.inspectionOperationGrid}>
          <div className={styles.operationMainColumn}>
            <div className={styles.operationMainStack}>
              <Card className={styles.inspectionStatusCard}>
                <div className={styles.inspectionProgressHero}>
                  <div className={styles.progressRing} style={{ '--progress': `${Math.max(0, Math.min(100, progress.percent))}%` } as CSSProperties}>
                    <strong>{`${progress.percent}%`}</strong>
                  </div>
                  <div className={styles.inspectionStatusCopy}>
                    <strong>{operationPhase}</strong>
                    <span>{result && ['completed', 'partial', 'stopped', 'failed'].includes(result.state) ? completedProgressLabel : progressLabel}</span>
                    <small>{`${t('monitoring.last_sync')}: ${result?.finishedAt ? formatTimestamp(result.finishedAt, i18n.language) : '--'}`}</small>
                    {lastError ? <small className={styles.inspectionStatusError}>{lastError}</small> : null}
                  </div>
                </div>
                <div className={styles.inspectionConfigGrid}>
                  <span>
                    <small>{t('monitoring.account_inspection_detection_scope')}</small>
                    <strong>{result ? (result.settings.targetType === ACCOUNT_INSPECTION_ALL_PROVIDER_TYPE ? t('monitoring.filter_all_providers') : resolveProviderDisplayLabel(result.settings.targetType)) : inspectionScopeLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_quota_threshold_short')}</small>
                    <strong>{`${result?.settings.usedPercentThreshold ?? inspectionSettings.usedPercentThreshold}%`}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_sample_size')}</small>
                    <strong>{result?.settings.sampleSize || inspectionSettings.sampleSize || t('monitoring.account_inspection_all_accounts')}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_scheduled_inspection_short')}</small>
                    <strong>{scheduleStatusLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_quota_limit_disable_short')}</small>
                    <strong>{quotaLimitAutoLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_quota_recovery_enable_short')}</small>
                    <strong>{quotaRecoveryAutoLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_account_invalid_action_short')}</small>
                    <strong>{accountInvalidActionLabel}</strong>
                  </span>
                  <span>
                    <small>{t('monitoring.account_inspection_request_error_action_short')}</small>
                    <strong>{requestErrorActionLabel}</strong>
                  </span>
                </div>
                <div className={styles.inspectionNextRunText}>
                  <span>{t('monitoring.account_inspection_next_execution_short')}</span>
                  <strong>{schedule?.enabled && schedule.nextRunAt ? formatTimestamp(schedule.nextRunAt, i18n.language) : '--'}</strong>
                </div>
              </Card>

              <Card className={styles.inspectionControlCard}>
                <h3>{t('monitoring.account_inspection_control_title')}</h3>
                <div className={styles.inspectionControlActions}>
                  <Button
                    variant="primary"
                    onClick={handleRunInspection}
                    loading={runStatus === 'running'}
                    disabled={runStatus === 'running' || executing || connectionStatus !== 'connected'}
                  >
                    {formatRunInspectionButtonLabel(runStatus, t)}
                  </Button>
                  <Button variant="secondary" onClick={handlePauseInspection} disabled={runStatus !== 'running' || executing}>
                    {t('monitoring.account_inspection_pause')}
                  </Button>
                  <Button variant="danger" onClick={handleStopInspection} disabled={(runStatus !== 'running' && runStatus !== 'paused') || executing}>
                    {t('monitoring.account_inspection_stop')}
                  </Button>
                </div>
              </Card>
            </div>
          </div>

          <Card className={styles.actionStudioCard}>
            <div className={styles.resultOverviewSection}>
              <h3>{t('monitoring.account_inspection_inspection_summary_title')}</h3>
              <div className={styles.resultOverviewGrid}>
                <button type="button" className={styles.resultOverviewItem} onClick={() => showInspectionResults('all')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_result_total')}</small>
                  <strong>{displayedHealthCounts.total}</strong>
                </button>
                <button type="button" className={`${styles.resultOverviewItem} ${styles.resultOverviewGood}`} onClick={() => showInspectionResults('highAvailable')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_high_available')}</small>
                  <strong>{displayedHealthCounts.healthy}</strong>
                </button>
                <button type="button" className={styles.resultOverviewItem} onClick={() => showInspectionResults('recoverable')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_health_recoverable')}</small>
                  <strong>{displayedHealthCounts.recoverable}</strong>
                </button>
                <button type="button" className={`${styles.resultOverviewItem} ${styles.resultOverviewWarn}`} onClick={() => showInspectionResults('quotaExhausted')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_health_quota_exhausted')}</small>
                  <strong>{displayedHealthCounts.quotaExhausted}</strong>
                </button>
                <button type="button" className={`${styles.resultOverviewItem} ${styles.resultOverviewBad}`} onClick={() => showInspectionResults('accountInvalid')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_account_invalid')}</small>
                  <strong>{displayedHealthCounts.authInvalid}</strong>
                </button>
                <button type="button" className={`${styles.resultOverviewItem} ${styles.resultOverviewBad}`} onClick={() => showInspectionResults('requestError')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_account_request_error')}</small>
                  <strong>{displayedHealthCounts.inspectionError}</strong>
                </button>
                <button type="button" className={`${styles.resultOverviewItem} ${styles.resultOverviewWarn}`} onClick={() => showInspectionResults('unknown')} disabled={!result}>
                  <small>{t('monitoring.account_inspection_health_unknown')}</small>
                  <strong>{displayedHealthCounts.unknown ?? 0}</strong>
                </button>
              </div>
              {result?.runStats && Object.keys(result.runStats.providers).length > 0 ? (
                <div className={styles.runMetrics}>
                  <strong>{t('monitoring.account_inspection_run_metrics_title')}</strong>
                  <ul>
                    {Object.entries(result.runStats.providers).sort(([left], [right]) => left.localeCompare(right)).map(([provider, stats]) => (
                      <li key={provider}>
                        <strong>{resolveProviderDisplayLabel(provider)}</strong>
                        <span>{stats.accounts} {t('monitoring.account_inspection_run_metrics_accounts')}</span>
                        <span>{stats.httpRequests} {t('monitoring.account_inspection_run_metrics_requests')}</span>
                        <span>{(stats.wallTimeMs / 1000).toFixed(1)}s</span>
                        {stats.realProbeRequests > 0 ? <span>{stats.realProbeRequests} {t('monitoring.account_inspection_run_metrics_real_probes')}</span> : null}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
            </div>

            {hasAutoExecutionPolicy ? (
              <div className={styles.strategyResultSection}>
                <div className={styles.strategySectionHeader}>
                  <h3>{t('monitoring.account_inspection_auto_execution_breakdown')}</h3>
                  {result ? <button type="button" onClick={() => showInspectionResults('accountInvalid')}>{t('monitoring.account_inspection_view_results')}</button> : null}
                </div>
                {result ? (
                  <>
                    <div className={styles.strategyTotalsRow}>
                      <span>{`${t('monitoring.account_inspection_action_enable')} ${actionStats.autoEnable}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_disable')} ${actionStats.autoDisable}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_delete')} ${actionStats.autoDelete}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_quota_protection')} ${actionStats.autoQuotaProtection}`}</span>
                      <span>{`${t('monitoring.account_inspection_quota_recovery_enable_short')} ${actionStats.autoQuotaRecovery}`}</span>
                      <span>{`${t('monitoring.account_inspection_action_keep')} ${actionStats.keep}`}</span>
                    </div>
                    <div className={styles.strategyActivityRow}>
                      <span className={styles.strategyActivityIcon} aria-hidden="true" />
                      <span>{formatTimestamp(result.finishedAt, i18n.language)}</span>
                      <strong>{autoExecutionResultLabel}</strong>
                    </div>
                  </>
                ) : (
                  <div className={styles.manualPendingEmpty}>
                    <span aria-hidden="true">•</span>
                    <strong>{t('monitoring.account_inspection_auto_execution_waiting_title')}</strong>
                    <small>{autoExecutionResultLabel}</small>
                  </div>
                )}
              </div>
            ) : (
              <div className={styles.manualPendingSection}>
                <h3>{`${t('monitoring.account_inspection_manual_execution_breakdown')} (${actionStats.manualTotal})`}</h3>
                {actionStats.manualTotal > 0 ? (
                  <div className={styles.manualPendingList}>
                    <span>{`${t('monitoring.account_inspection_action_delete')}: ${actionStats.manualDelete}`}</span>
                    <span>{`${t('monitoring.account_inspection_action_disable')}: ${actionStats.manualDisable}`}</span>
                    <span>{`${t('monitoring.account_inspection_action_enable')}: ${actionStats.manualEnable}`}</span>
                    <div className={styles.manualPendingActions}>
                      <small>{t('monitoring.account_inspection_filtered_pending_count', { count: scopedPendingCount ?? '—' })}</small>
                      <Button
                        variant="primary"
                        size="sm"
                        onClick={() => void handleExecutePlanned()}
                        loading={bulkActionLoading || executing}
                        disabled={!batchHydrated || restoredSnapshot || !result || runStatus === 'running' || executing || bulkActionLoading || batchRunning || pendingActionCount === 0 || scopedPendingCount === 0}
                      >
                        {bulkActionLoading || executing ? t('monitoring.account_inspection_executing') : t('monitoring.account_inspection_execute_now')}
                      </Button>
                      <small>{t('monitoring.account_inspection_execute_filtered_hint')}</small>
                    </div>
                  </div>
                ) : (
                  <div className={styles.manualPendingEmpty}>
                    <span aria-hidden="true">✓</span>
                    <strong>{t('monitoring.account_inspection_no_pending_actions')}</strong>
                    <small>{t('monitoring.account_inspection_auto_execute_no_actions')}</small>
                  </div>
                )}
              </div>
            )}
          </Card>
        </div>
      </section>

      <div ref={resultsPanelRef} className={styles.resultsSection}>
        <div className={styles.operationModuleHeader}>
          <div>
            <h2>{t('monitoring.account_inspection_results_title')}</h2>
            <p>{t('monitoring.account_inspection_results_desc')}</p>
          </div>
          {result ? (
            <div className={styles.resultMeta}>
              <strong>{t('monitoring.account_inspection_results_count', { count: resultPagination.total })}</strong>
              <span>{t('monitoring.account_inspection_results_updated_at', { time: formatTimestamp(result.finishedAt, i18n.language) })}</span>
            </div>
          ) : null}
        </div>
        <Card className={styles.panel}>

        {detailsLoadError ? (
          <div className={styles.inspectionInlineError} role="alert">
            <span>{t('monitoring.account_inspection_details_load_failed', { error: detailsLoadError })}</span>
            <Button size="sm" variant="secondary" onClick={() => setDetailsRetryNonce((value) => value + 1)}>
              {t('monitoring.account_inspection_retry_load')}
            </Button>
          </div>
        ) : null}

        {batchError ? <div className={styles.inspectionInlineError} role="alert">{batchError}</div> : null}
        {batchOperations.map((operation) => (
          <section key={operation.operationId} className={styles.batchReceiptPanel} aria-label={t('monitoring.account_inspection_batch_receipt_title')}>
            <div className={styles.batchReceiptHeader}>
              <div>
                <strong>{t('monitoring.account_inspection_batch_receipt_title')}</strong>
                <span>{t(`monitoring.account_inspection_batch_kind_${operation.kind}`)} · {t(`monitoring.account_inspection_batch_state_${operation.state}`)}</span>
              </div>
              <span>{t('monitoring.account_inspection_batch_receipt_counts', {
                total: operation.summary.total,
                succeeded: operation.summary.succeeded,
                failed: operation.summary.failed,
                pending: operation.summary.ready + operation.summary.running,
                skipped: operation.summary.stale + operation.summary.unsupported,
                interrupted: operation.summary.interrupted ?? 0,
              })}</span>
            </div>
            <div className={styles.batchReceiptList} role="list">
              {operation.items.map(({ key, item, status, effect, error, outcome }) => {
                const detail = outcome?.outcome ?? outcome;
                const receipts = detail?.receipts;
                const recoveryRan = Boolean(receipts?.length);
                const stillRestricted = Boolean(detail?.after && typeof detail.after === 'object' &&
                  'authId' in detail.after && typeof detail.after.authId === 'string' && detail.after.authId.trim());
                const inspected = detail?.result ? accountInspectionBackendResultToItem(detail.result) : null;
                const inspectedHealth = inspected ? resolveResultHealthStatus(inspected) : null;
                const successLabel = recoveryRan
                  ? t(stillRestricted
                    ? 'monitoring.account_inspection_batch_recovery_still_restricted'
                    : 'monitoring.account_inspection_batch_recovery_released')
                  : operation.kind === 'inspect'
                    ? t(inspectedHealth === 'healthy'
                      ? 'monitoring.account_inspection_batch_inspection_healthy'
                      : 'monitoring.account_inspection_batch_inspection_unhealthy', {
                        status: inspected && inspectedHealth ? buildHealthStatusLabel(inspected, inspectedHealth, t) : '-',
                      })
                    : t('monitoring.account_inspection_batch_action_completed');
                return (
                  <div key={key} className={styles.batchReceiptItem} role="listitem">
                    <strong>{item.displayName || item.fileName}</strong>
                    <small>{t(`monitoring.account_inspection_batch_group_${effect}`)}</small>
                    <span>{status === 'succeeded' ? successLabel : t(`monitoring.account_inspection_batch_status_${status}`)}</span>
                    {recoveryRan && stillRestricted && status !== 'succeeded'
                      ? <small>{t('monitoring.account_inspection_batch_recovery_still_restricted')}</small> : null}
                    {(error || detail?.error || detail?.warning) ? <small className={styles.inspectionStatusError}>{error || detail?.error || detail?.warning}</small> : null}
                    {receipts?.flatMap((receipt, receiptIndex) => (receipt.phases ?? []).map((phase, phaseIndex) => (
                      <small key={`${receiptIndex}:${phaseIndex}`}>
                        {phase.source}{phase.model ? ` · ${phase.model}` : ''} · {t(`routing_policy.recovery.phase_${phase.status}`)}
                        {phase.error ? ` · ${phase.error}` : ''}
                      </small>
                    )))}
                  </div>
                );
              })}
            </div>
            {operation.state === 'interrupted' || (operation.summary.interrupted ?? 0) > 0 ? (
              <p className={styles.inspectionStatusError}>{t('monitoring.account_inspection_batch_interrupted_notice')}</p>
            ) : null}
            {operation.state === 'prepared' && operation.summary.ready > 0 ? (
              <Button size="sm" variant="secondary" onClick={() => confirmBatch([operation], 'fixed', true)}>
                {t('monitoring.account_inspection_batch_resume_prepared')}
              </Button>
            ) : null}
            {operation.state === 'completed' && operation.summary.failed + operation.summary.stale > 0 ? (
              <Button size="sm" variant="secondary" loading={bulkActionLoading} onClick={() => void handleRetryBatchFailures(operation.operationId)}>
                {t('monitoring.account_inspection_batch_retry_failed', { count: operation.summary.failed + operation.summary.stale })}
              </Button>
            ) : null}
          </section>
        ))}

        {result ? (
          <>
            <div className={[
              styles.filterGrid,
              styles.resultToolbar,
              !hasAutoExecutionPolicy ? styles.resultToolbarWithPending : '',
            ].filter(Boolean).join(' ')}>
              <Input
                type="search"
                value={resultSearchInput}
                onChange={(event) => setResultSearchInput(event.target.value)}
                placeholder={t('monitoring.account_inspection_search_placeholder')}
                aria-label={t('monitoring.account_inspection_search_label')}
                className={styles.toolbarHeaderSearchInput}
                rightElement={<IconSearch size={16} />}
              />
              <Select
                value={selectedResultProvider}
                options={resultProviderOptions}
                onChange={setSelectedResultProvider}
                ariaLabel={t('monitoring.account_inspection_filter_provider')}
              />
              <Select
                value={resultStatusFilter}
                options={resultStatusFilterOptions}
                onChange={(value) => {
                  const nextFilter = value as ResultStatusFilter;
                  setResultStatusFilter(nextFilter);
                  setResultReasonFilter(null);
                  if (nextFilter === 'highAvailable') setResultPendingOnly(false);
                }}
                ariaLabel={t('monitoring.account_inspection_result')}
              />
              {!hasAutoExecutionPolicy ? (
                <div className={styles.resultPendingToggle}>
                  <ToggleSwitch
                    checked={resultPendingOnly}
                    onChange={setResultPendingOnly}
                    label={t('monitoring.account_inspection_filter_pending_only')}
                    ariaLabel={t('monitoring.account_inspection_filter_pending_only')}
                    disabled={resultStatusFilter === 'highAvailable'}
                  />
                </div>
              ) : null}
            </div>
            {resultReasonFilter ? (
              <div className={styles.resultActiveFilters}>
                <span className={styles.resultReasonChip}>
                  {resultReasonLabels[resultReasonFilter]}
                  <button
                    type="button"
                    onClick={() => setResultReasonFilter(null)}
                    title={t('common.close')}
                    aria-label={t('common.close')}
                  >
                    <IconX size={13} />
                  </button>
                </span>
              </div>
            ) : null}
            {resultPagination.total > 0 ? (
              <div className={styles.resultSelectionBar}>
                <div className={styles.resultSelectionSummary}>
                  <strong>{t(resultBulkScope === 'selected'
                    ? 'monitoring.account_inspection_batch_scope_selected'
                    : 'monitoring.account_inspection_batch_scope_filtered')}</strong>
                  <span>{resultBulkScope === 'selected'
                    ? t('monitoring.account_inspection_selected_count', { count: selectedVisibleResultRows.length })
                    : t('monitoring.account_inspection_batch_filtered_count', { count: resultPagination.total })}</span>
                  {resultBulkScope === 'filtered' ? <small>{t('monitoring.account_inspection_batch_filtered_hint')}</small> : null}
                  {resultBulkScope === 'filtered' && resultBulkAction === 'suggested'
                    ? <small>{t('monitoring.account_inspection_batch_filtered_suggestions')}</small> : null}
                </div>
                <div className={styles.resultSelectionActions}>
                  <Select
                    value={resultBulkScope}
                    options={[
                      { value: 'selected', label: t('monitoring.account_inspection_batch_scope_selected') },
                      { value: 'filtered', label: t('monitoring.account_inspection_batch_scope_filtered') },
                    ]}
                    onChange={(value) => setResultBulkScope(value as ResultBulkScope)}
                    ariaLabel={t('monitoring.account_inspection_batch_scope_label')}
                    className={styles.resultBulkActionSelect}
                    fullWidth={false}
                    size="sm"
                  />
                  <Select
                    value={resultBulkAction}
                    options={resultBulkActionOptions}
                    onChange={(value) => setResultBulkAction(value as ResultBulkAction)}
                    ariaLabel={t('monitoring.account_inspection_bulk_action_label')}
                    className={styles.resultBulkActionSelect}
                    triggerClassName={styles.resultBulkActionTrigger}
                    fullWidth={false}
                    size="sm"
                  />
                  <Button
                    size="sm"
                    variant={resultBulkAction === 'delete' ? 'danger' : 'primary'}
                    onClick={() => void handleExecuteSelectedResults()}
                    loading={bulkActionLoading || executing}
                    disabled={!batchHydrated || restoredSnapshot || runStatus === 'running' || executing || bulkActionLoading || recheckingKey !== null || batchOperations.some((operation) => operation.state === 'running') || (resultBulkScope === 'selected' && selectedVisibleResultRows.length === 0)}
                  >
                    {t('monitoring.account_inspection_batch_preflight_button')}
                  </Button>
                  {selectedVisibleResultRows.length > 0 ? (
                    <button type="button" className={styles.clearSelectionButton} onClick={() => setSelectedResultKeys(new Set())}>
                      {t('monitoring.account_inspection_clear_selection')}
                    </button>
                  ) : null}
                </div>
              </div>
            ) : null}
            <div
              ref={resultsTableViewportRef}
              className={`${styles.tableWrap} ${styles.resultsTableViewport}`}
            >
              <table className={styles.table}>
                <colgroup>
                  <col className={styles.accountColumn} />
                  <col className={styles.planColumn} />
                  <col className={styles.healthColumn} />
                  <col className={styles.quotaColumn} />
                  <col className={styles.tokenColumn} />
                  <col className={styles.operationColumn} />
                </colgroup>
                <thead>
                  <tr>
                    <th>
                      <div className={styles.accountHeaderCell}>
                        <input
                          ref={selectAllResultsRef}
                          type="checkbox"
                          checked={allVisibleResultsSelected}
                          onChange={(event) => toggleVisibleResultSelection(event.target.checked)}
                          disabled={selectableVisibleResultRows.length === 0}
                          aria-label={t('monitoring.account_inspection_select_visible_results')}
                        />
                        <span>{t('monitoring.account_label')}</span>
                      </div>
                    </th>
                    <th>{t('monitoring.account_inspection_account_plan')}</th>
                    <th>{t('monitoring.account_inspection_result')}</th>
                    <th>{t('monitoring.account_inspection_remaining_quota')}</th>
                    <th>{t('monitoring.account_inspection_token_status')}</th>
                    <th>{t('common.action')}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredResultRows.length > 0 ? (
                    visibleResultRows.map(({ item, healthStatus, manualActions }) => {
                      const tokenRefreshDetail = formatTokenRefreshDetail(item, i18n.language, t);
                      const healthStatusLabel = buildHealthStatusLabel(item, healthStatus, t);
                      const accountLabel = resolveAccountInspectionAccountLabel(item);
                      const planLabel = resolveAccountInspectionPlanLabel(
                        item,
                        authFilesByName.get(item.fileName),
                        quotaStore,
                        t
                      );
                      const suggestedAction = isSuggestedAction(item) && !item.executed
                        ? item.action as ManualAccountInspectionAction
                        : null;
                      const additionalActions = manualActions.filter((action) => action !== suggestedAction);
                      return (
                        <tr key={item.key}>
                          <td className={styles.accountTableCell} data-label={t('monitoring.account_label')}>
                            <div className={styles.accountCellLayout}>
                              {item.executedEffect !== 'delete' ? <input
                                type="checkbox"
                                checked={selectedResultKeys.has(item.key)}
                                onChange={(event) => toggleResultSelection(item.key, event.target.checked)}
                                aria-label={t('monitoring.account_inspection_select_account', { account: accountLabel })}
                              /> : <span aria-hidden="true" />}
                              <div className={styles.primaryCell}>
                                <strong title={item.fileName}>{accountLabel}</strong>
                                <small>
                                  <span>{resolveProviderDisplayLabel(item.provider)}</span>
                                  <span aria-hidden="true"> · </span>
                                  <span className={item.executedEffect === 'delete' ? styles.stateTextMuted : item.disabled ? styles.stateTextMuted : item.quotaCooling ? styles.stateTextWarn : styles.stateTextGood}>{item.executedEffect === 'delete' ? t('monitoring.account_inspection_effect_delete') : formatCurrentStateLabel(item, t)}</span>
                                  {item.quotaCooling && <span title={item.actionReason}> · {item.quotaRetryAt ? t('monitoring.account_inspection_quota_retry_at', { time: formatTimestamp(item.quotaRetryAt, i18n.language) }) : t('monitoring.account_inspection_quota_manual_release')}</span>}
                                  {item.executed && <span> · {t('monitoring.account_inspection_suggestion_processed')}</span>}
                                  {item.executedEffect && <span> · {t('monitoring.account_inspection_last_execution')}: {t(`monitoring.account_inspection_effect_${item.executedEffect}`)}</span>}
                                  {!item.executedEffect && item.executedAction && <span> · {t('monitoring.account_inspection_last_manual_override')}: {formatActionLabel(item.executedAction, t)}{item.executedAt ? ` · ${formatTimestamp(item.executedAt, i18n.language)}` : ''}</span>}
                                  {item.executeError && <span className={styles.stateTextBad}> · {item.executeError}</span>}
                                  {item.isQuota && item.action === 'disable' && <span className={styles.stateTextWarn}> · {t('monitoring.account_inspection_quota_protection')}</span>}
                                </small>
                              </div>
                            </div>
                          </td>
                          <td className={styles.planTableCell} data-label={t('monitoring.account_inspection_account_plan')}><span className={styles.planCell} title={planLabel}>{planLabel}</span></td>
                          <td className={styles.resultTableCell} data-label={t('monitoring.account_inspection_result')}>
                            <button
                              type="button"
                              className={styles.resultDetailButton}
                              onClick={() => setSelectedDetailResult(item)}
                              title={t('monitoring.account_inspection_result_details')}
                              aria-label={t('monitoring.account_inspection_result_details')}
                            >
                              <span className={`${styles.healthBadge} ${healthToneClass[healthStatus]}`}>{healthStatusLabel}</span>
                            </button>
                          </td>
                          <td className={styles.quotaTableCell} data-label={t('monitoring.account_inspection_remaining_quota')}>
                            <div className={styles.quotaCell}>
                              <span>{formatQuotaRemainingLabel(item.usedPercent)}</span>
                            </div>
                          </td>
                          <td className={styles.tokenTableCell} data-label={t('monitoring.account_inspection_token_status')}>
                            <div className={styles.tokenRefreshCell}>
                              <span className={tokenRefreshToneClass(item)} title={tokenRefreshDetail || undefined}>{formatTokenRefreshLabel(item, t)}</span>
                            </div>
                          </td>
                          <td className={styles.operationCell} data-label={t('common.action')}>
                            <div className={styles.operationActions}>
                              {item.executedEffect !== 'delete' && item.authId && item.authIndex && !item.quotaCooling ? (
                                <Button
                                  size="sm"
                                  variant="secondary"
                                  onClick={() => openRecoveryForItem(item)}
                                  disabled={singleMutationBlocked || restoredSnapshot || runStatus === 'running' || executing || bulkActionLoading || recheckingKey !== null}
                                >
                                  {t('routing_policy.recovery.open')}
                                </Button>
                              ) : null}
                              {item.executedEffect !== 'delete' ? <button
                                type="button"
                                className={styles.iconActionButton}
                                onClick={() => void handleRecheckSingle(item)}
                                disabled={singleMutationBlocked || restoredSnapshot || runStatus === 'running' || executing || bulkActionLoading || recheckingKey !== null}
                                title={t('monitoring.account_inspection_recheck_account')}
                                aria-label={t('monitoring.account_inspection_recheck_account')}
                              >
                                <IconRefreshCw size={15} className={recheckingKey === item.key ? styles.spinningIcon : undefined} />
                              </button> : null}
                              {item.executedEffect !== 'delete' && suggestedAction ? (
                                <Button
                                  size="sm"
                                  variant={suggestedAction === 'delete' ? 'danger' : 'primary'}
                                  onClick={() => {
                                    if (isSchedulingRecoveryAction(buildManualActionItem(item, suggestedAction))) {
                                      openRecoveryForItem(item);
                                    } else {
                                      handleExecuteSingle(item, suggestedAction, true);
                                    }
                                  }}
                                  disabled={singleMutationBlocked || restoredSnapshot || runStatus === 'running' || executing || bulkActionLoading || recheckingKey !== null}
                                >
                                  {isSchedulingRecoveryAction(buildManualActionItem(item, suggestedAction))
                                    ? t('monitoring.account_inspection_release_quota')
                                    : formatInspectionExecutionLabel({ ...item, action: suggestedAction, suggested: true }, t)}
                                </Button>
                              ) : null}
                              {item.executedEffect !== 'delete' && additionalActions.map((action) => (
                                <Button
                                  key={action}
                                  size="sm"
                                  variant={action === 'delete' ? 'danger' : 'secondary'}
                                  onClick={() => {
                                    if (isSchedulingRecoveryAction(buildManualActionItem(item, action))) {
                                      openRecoveryForItem(item);
                                    } else {
                                      handleExecuteSingle(item, action);
                                    }
                                  }}
                                  disabled={singleMutationBlocked || restoredSnapshot || runStatus === 'running' || executing || bulkActionLoading || recheckingKey !== null}
                                >
                                  {isSchedulingRecoveryAction(buildManualActionItem(item, action))
                                    ? t('monitoring.account_inspection_release_quota')
                                    : formatActionLabel(action, t)}
                                </Button>
                              ))}
                            </div>
                          </td>
                        </tr>
                      );
                    })
                  ) : (
                    <tr><td colSpan={6}><div className={styles.emptyBlockSmall}>{t('monitoring.account_inspection_no_filtered_results')}</div></td></tr>
                  )}
                </tbody>
              </table>
            </div>
            {resultPagination.total > 0 ? (
              <div className={styles.paginationBar}>
                <div className={styles.paginationPageSizeControl}>
                  <span id="account-inspection-result-page-size-label">
                    {t('monitoring.pagination_page_size', { defaultValue: paginationCopy.pageSizeLabel })}
                  </span>
                  <Select
                    id="account-inspection-result-page-size"
                    value={String(resultPageSize)}
                    options={PRO_PAGE_SIZE_OPTIONS.map((pageSize) => ({
                      value: String(pageSize),
                      label: t('monitoring.pagination_page_size_value', {
                        count: pageSize,
                        defaultValue: paginationCopy.pageSizeValue(pageSize),
                      }),
                    }))}
                    onChange={(value) => {
                      const nextPageSize = normalizeProPageSize(value);
                      if (nextPageSize === resultPageSize) return;
                      setResultPageSize(nextPageSize);
                      setResultPage(1);
                    }}
                    ariaLabelledBy="account-inspection-result-page-size-label"
                    className={styles.paginationPageSizeSelect}
                  />
                </div>
                {resultPagination.totalPages > 1 ? (
                  <div className={styles.paginationNavigation}>
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => setResultPage((page) => Math.max(1, page - 1))}
                      disabled={!resultPagination.hasPrevious}
                      aria-label={t('monitoring.previous_page')}
                    >
                      {t('monitoring.previous_page')}
                    </Button>
                    <div className={quotaStyles.pageInfo}>
                      {t('monitoring.pagination_info', {
                        from: resultPagination.from,
                        to: resultPagination.to,
                        total: resultPagination.total,
                        page: resultPagination.page,
                        totalPages: resultPagination.totalPages,
                        defaultValue: `${resultPagination.from}-${resultPagination.to} / ${resultPagination.total}`,
                      })}
                    </div>
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => setResultPage((page) => page + 1)}
                      disabled={!resultPagination.hasNext}
                      aria-label={t('monitoring.next_page')}
                    >
                      {t('monitoring.next_page')}
                    </Button>
                  </div>
                ) : null}
              </div>
            ) : null}
          </>
        ) : (
          <div className={styles.emptyState}>
            <strong>{resultEmptyMessage}</strong>
            <span>{connectionStatus === 'connected' ? t('monitoring.account_inspection_empty_hint') : t('notification.connection_required')}</span>
          </div>
        )}
        </Card>
      </div>

      <div className={styles.logsSection}>
        <div className={styles.operationModuleHeader}>
          <div>
            <h2>{t('monitoring.account_inspection_logs_title')}</h2>
            <p>{t('monitoring.account_inspection_logs_desc')}</p>
          </div>
          <div className={styles.logHeaderActions}>
            <div className={styles.resultFilterControl}>
              {logLevelOptions.map((option) => (
                <button key={option.key} type="button" className={[styles.resultFilterButton, logLevelFilter === option.key ? styles.resultFilterButtonActive : ''].filter(Boolean).join(' ')} onClick={() => setLogLevelFilter(option.key)}>
                  <span>{option.label}</span>
                </button>
              ))}
            </div>
            <button type="button" className={styles.foldButton} onClick={() => setLogsCollapsed((previous) => !previous)} disabled={logs.length === 0}>
              {logsCollapsed ? <IconChevronDown size={16} /> : <IconChevronUp size={16} />}
              <span>{logsCollapsed ? t('monitoring.account_inspection_expand_logs') : t('monitoring.account_inspection_fold_logs')}</span>
            </button>
          </div>
        </div>

        <Card className={styles.panel}>
          {!logsCollapsed ? (
            <div ref={logListRef} className={styles.logList}>
              {filteredLogs.length > 0 ? visibleLogs.map((entry) => (
                <div key={entry.id} className={`${styles.logRow} ${levelClassMap[entry.level]}`}>
                  <span className={styles.logTime}>{formatTimestamp(entry.timestamp, i18n.language)}</span>
                  <span className={styles.logMessage}>{entry.message}</span>
                </div>
              )) : <div className={styles.emptyBlock}>{t('monitoring.account_inspection_logs_empty')}</div>}
              {logPagination.totalPages > 1 ? (
                <div className={quotaStyles.pagination}>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => setLogPage((page) => Math.max(1, page - 1))}
                    disabled={!logPagination.hasPrevious}
                    aria-label={t('monitoring.previous_page')}
                  >
                    {t('monitoring.previous_page')}
                  </Button>
                  <div className={quotaStyles.pageInfo}>
                    {t('monitoring.pagination_info', {
                      from: logPagination.from,
                      to: logPagination.to,
                      total: logPagination.total,
                      page: logPagination.page,
                      totalPages: logPagination.totalPages,
                      defaultValue: `${logPagination.from}-${logPagination.to} / ${logPagination.total}`,
                    })}
                  </div>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => setLogPage((page) => page + 1)}
                    disabled={!logPagination.hasNext}
                    aria-label={t('monitoring.next_page')}
                  >
                    {t('monitoring.next_page')}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : (
            <div className={styles.logCollapsedBar}><span>{t('monitoring.account_inspection_logs_collapsed', { count: filteredLogs.length })}</span></div>
          )}
        </Card>
      </div>

      <ProDetailDialog
        open={activeSurface === 'detail' && Boolean(selectedDetailResult)}
        onClose={() => setSelectedDetailResult(null)}
        onAfterClose={() => setSelectedDetailResultState(null)}
        title={t('monitoring.account_inspection_result_details')}
        footer={(
          <div className={styles.errorModalActions}>
            <Button variant="primary" size="sm" onClick={() => setSelectedDetailResult(null)}>
              {t('common.close')}
            </Button>
          </div>
        )}
      >
        {selectedDetailResult ? (
          <>
            <InspectionErrorDetailsPanel item={selectedDetailResult} t={t} />
            <InspectionRecordsPanel key={`${selectedDetailResult.key}:${selectedDetailResult.resultRef || ''}`} item={selectedDetailResult} />
          </>
        ) : null}
      </ProDetailDialog>

      <SchedulingRecoveryDialog
        open={activeSurface === 'recovery' && Boolean(selectedRecoveryResult)}
        actionsDisabled={singleMutationBlocked}
        authId={selectedRecoveryResult?.authId || ''}
        authIndex={selectedRecoveryResult?.authIndex || ''}
        accountName={selectedRecoveryResult ? resolveAccountInspectionAccountLabel(selectedRecoveryResult) : ''}
        onClose={closeSurface}
        onResult={async () => {
          const response = await accountInspectionApi.getStatus(currentInspectionDetailOptions);
          applyBackendResponse(response);
        }}
      />

      <ProSettingsSheet
        open={isSettingsModalOpen}
        onClose={closeSettingsModal}
        confirmClose={confirmSettingsModalClose}
        onDiscard={discardSettingsModalDraft}
        title={t('monitoring.account_inspection_settings_title')}
        className={styles.settingsModal}
        dirty={settingsDirty}
        saving={scheduleLoading}
        cancelLabel={t('common.cancel')}
        saveLabel={t('common.save')}
        dirtyLabel={t('common.unsaved_changes_title')}
        onSave={handleSaveSettings}
        footerStart={(
          <Button variant="secondary" onClick={handleResetSettings} disabled={scheduleLoading}>
            {t('monitoring.account_inspection_settings_reset_button')}
          </Button>
        )}
      >
        <div className={styles.settingsWorkbench}>
          <div className={styles.settingsWorkbenchMain}>
            <section className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>01</small>
                  <strong>{t('monitoring.account_inspection_schedule_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_schedule_section_desc')}</span>
                </div>
                <ToggleSwitch
                  checked={scheduleDraft.enabled}
                  onChange={(value) => dispatchBackendState({ type: 'updateScheduleDraft', values: { enabled: value } })}
                  ariaLabel={t('monitoring.account_inspection_schedule_enabled_label')}
                />
              </div>
              <div className={styles.settingsSplitGrid}>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_schedule_interval_label')}
                    type="number"
                    value={scheduleDraft.intervalMinutes}
                    onChange={(event) => dispatchBackendState({ type: 'updateScheduleDraft', values: { intervalMinutes: event.target.value } })}
                    min={SCHEDULE_INTERVAL_LIMITS.min}
                    step={1}
                  />
                  <div className={styles.settingsHint}>{t('monitoring.account_inspection_schedule_interval_hint')}</div>
                </div>
                <div className={styles.settingsInsightPanel}>
                  <small>{t('monitoring.account_inspection_schedule_next_run')}</small>
                  <strong>{schedule?.nextRunAt ? formatTimestamp(schedule.nextRunAt, i18n.language) : '--'}</strong>
                  <span>{scheduleDraft.enabled ? draftScheduleStatusLabel : settingDisabledLabel}</span>
                </div>
              </div>
            </section>

            <section className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>02</small>
                  <strong>{t('monitoring.account_inspection_settings_basic_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_basic_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsThreeGrid}>
                <div className={styles.settingsFormPanel}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_target_type_label')}</label>
                  <Select
                    value={settingsDraft.targetType}
                    options={INSPECTION_TARGET_OPTIONS}
                    onChange={(value) => handleSettingsDraftChange('targetType', value)}
                    ariaLabel={t('monitoring.account_inspection_settings_target_type_label')}
                  />
                  <div className={styles.settingsHint}>{t('monitoring.account_inspection_settings_target_type_hint')}</div>
                </div>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_settings_used_percent_threshold_label')}
                    hint={t('monitoring.account_inspection_settings_threshold_hint')}
                    type="number"
                    value={settingsDraft.usedPercentThreshold}
                    onChange={(event) => handleSettingsDraftChange('usedPercentThreshold', event.target.value)}
                    min={THRESHOLD_LIMITS.min}
                    max={THRESHOLD_LIMITS.max}
                    step={0.1}
                  />
                </div>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_settings_sample_size_label')}
                    hint={t('monitoring.account_inspection_settings_sample_size_hint')}
                    type="number"
                    value={settingsDraft.sampleSize}
                    onChange={(event) => handleSettingsDraftChange('sampleSize', event.target.value)}
                    min={SAMPLE_SIZE_LIMITS.min}
                    step={1}
                  />
                </div>
              </div>
            </section>

            <section className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>03</small>
                  <strong>{t('monitoring.account_inspection_settings_runtime_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_runtime_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsMatrixGrid}>
                <Input
                  label={t('monitoring.account_inspection_settings_workers_label')}
                  hint={t('monitoring.account_inspection_settings_workers_hint', { min: WORKER_LIMITS.min, max: WORKER_LIMITS.max })}
                  type="number"
                  value={settingsDraft.workers}
                  onChange={(event) => handleSettingsDraftChange('workers', event.target.value)}
                  min={WORKER_LIMITS.min}
                  max={WORKER_LIMITS.max}
                  step={1}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_provider_workers_label')}
                  hint={t('monitoring.account_inspection_settings_provider_workers_hint', { min: PROVIDER_WORKER_LIMITS.min, max: PROVIDER_WORKER_LIMITS.max })}
                  type="number"
                  value={settingsDraft.providerWorkers}
                  onChange={(event) => handleSettingsDraftChange('providerWorkers', event.target.value)}
                  min={PROVIDER_WORKER_LIMITS.min}
                  max={PROVIDER_WORKER_LIMITS.max}
                  step={1}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_delete_workers_label')}
                  hint={t('monitoring.account_inspection_settings_delete_workers_hint', { min: DELETE_WORKER_LIMITS.min, max: DELETE_WORKER_LIMITS.max })}
                  type="number"
                  value={settingsDraft.deleteWorkers}
                  onChange={(event) => handleSettingsDraftChange('deleteWorkers', event.target.value)}
                  min={DELETE_WORKER_LIMITS.min}
                  max={DELETE_WORKER_LIMITS.max}
                  step={1}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_timeout_label')}
                  hint={t('monitoring.account_inspection_settings_timeout_hint', { min: TIMEOUT_LIMITS.min, max: TIMEOUT_LIMITS.max })}
                  type="number"
                  value={settingsDraft.timeout}
                  onChange={(event) => handleSettingsDraftChange('timeout', event.target.value)}
                  min={TIMEOUT_LIMITS.min}
                  max={TIMEOUT_LIMITS.max}
                  step={TIMEOUT_LIMITS.step}
                />
                <Input
                  label={t('monitoring.account_inspection_settings_retries_label')}
                  hint={t('monitoring.account_inspection_settings_retries_hint', { min: RETRY_LIMITS.min, max: RETRY_LIMITS.max })}
                  type="number"
                  value={settingsDraft.retries}
                  onChange={(event) => handleSettingsDraftChange('retries', event.target.value)}
                  min={RETRY_LIMITS.min}
                  max={RETRY_LIMITS.max}
                  step={1}
                />
              </div>
            </section>

            <section className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>04</small>
                  <strong>{t('monitoring.account_inspection_settings_advanced_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_advanced_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsFocusGrid}>
                <div className={styles.settingsFocusCard}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_antigravity_quota_mode_label')}</label>
                  <Select
                    value={settingsDraft.antigravityQuotaMode}
                    options={ANTIGRAVITY_QUOTA_MODE_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }))}
                    onChange={handleAntigravityQuotaModeChange}
                    ariaLabel={t('monitoring.account_inspection_settings_antigravity_quota_mode_label')}
                  />
                  <span>{t('monitoring.account_inspection_settings_antigravity_quota_mode_hint')}</span>
                  <strong>{draftQuotaModeLabel}</strong>
                </div>
                <div className={styles.settingsFocusCard}>
                  <div className={styles.settingsPolicyControl}>
                    <ToggleSwitch
                      checked={settingsDraft.antigravityDeepProbeEnabled}
                      onChange={handleAntigravityDeepProbeChange}
                      label={t('monitoring.account_inspection_settings_antigravity_deep_probe_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_antigravity_deep_probe_label')}
                      labelPosition="left"
                    />
                  </div>
                  <span>{t('monitoring.account_inspection_settings_antigravity_deep_probe_hint')}</span>
                  <div className={!settingsDraft.antigravityDeepProbeEnabled ? styles.settingsMutedField : undefined}>
                    <Input
                      label={t('monitoring.account_inspection_settings_antigravity_deep_probe_model_label')}
                      hint={t('monitoring.account_inspection_settings_antigravity_deep_probe_model_hint')}
                      value={settingsDraft.antigravityDeepProbeModel}
                      onChange={(event) => handleSettingsDraftChange('antigravityDeepProbeModel', event.target.value)}
                      disabled={!settingsDraft.antigravityDeepProbeEnabled}
                    />
                  </div>
                </div>
                <div className={styles.settingsFocusCard}>
                  <div className={styles.settingsPolicyControl}>
                    <ToggleSwitch
                      checked={settingsDraft.xaiDeepProbeEnabled}
                      onChange={handleXAIDeepProbeChange}
                      label={t('monitoring.account_inspection_settings_xai_deep_probe_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_xai_deep_probe_label')}
                      labelPosition="left"
                    />
                  </div>
                  <span>{t('monitoring.account_inspection_settings_xai_deep_probe_hint')}</span>
                  <div className={!settingsDraft.xaiDeepProbeEnabled ? styles.settingsMutedField : undefined}>
                    <Input
                      label={t('monitoring.account_inspection_settings_xai_deep_probe_model_label')}
                      hint={t('monitoring.account_inspection_settings_xai_deep_probe_model_hint')}
                      value={settingsDraft.xaiDeepProbeModel}
                      onChange={(event) => handleSettingsDraftChange('xaiDeepProbeModel', event.target.value)}
                      disabled={!settingsDraft.xaiDeepProbeEnabled}
                    />
                  </div>
                </div>
              </div>
            </section>

            <section className={styles.settingsWorkbenchSection}>
              <div className={styles.settingsWorkbenchHeader}>
                <div>
                  <small>05</small>
                  <strong>{t('monitoring.account_inspection_settings_auto_section_title')}</strong>
                  <span>{t('monitoring.account_inspection_settings_auto_section_desc')}</span>
                </div>
              </div>
              <div className={styles.settingsAutomationGrid}>
                <div className={styles.settingsAutomationStack}>
                  <div className={styles.settingsAutomationCard}>
                    <ToggleSwitch
                      checked={settingsDraft.autoExecuteQuotaLimitDisable}
                      onChange={handleAutoExecuteQuotaLimitChange}
                      label={t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_label')}
                      labelPosition="left"
                    />
                    <span>{t('monitoring.account_inspection_settings_auto_execute_quota_limit_disable_hint')}</span>
                  </div>
                  <div className={styles.settingsAutomationCard}>
                    <ToggleSwitch
                      checked={settingsDraft.autoExecuteQuotaRecoveryEnable}
                      onChange={handleAutoExecuteQuotaRecoveryChange}
                      label={t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_label')}
                      ariaLabel={t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_label')}
                      labelPosition="left"
                    />
                    <span>{t('monitoring.account_inspection_settings_auto_execute_quota_recovery_enable_hint')}</span>
                  </div>
                </div>
                <div className={styles.settingsRiskPanel}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_label')}</label>
                  <Select
                    value={settingsDraft.autoExecuteAccountInvalidAction}
                    options={AUTO_ERROR_ACTION_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }))}
                    onChange={handleAutoExecuteAccountInvalidActionChange}
                    ariaLabel={t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_label')}
                  />
                  <span>{t('monitoring.account_inspection_settings_auto_execute_account_invalid_action_hint')}</span>
                  {settingsDraft.autoExecuteAccountInvalidAction === 'delete' ? (
                    <div className={styles.settingsDangerNote}>
                      {t('monitoring.account_inspection_delete_irreversible_warning')}
                    </div>
                  ) : null}
                </div>
                <div className={styles.settingsRiskPanel}>
                  <label className={styles.settingsLabel}>{t('monitoring.account_inspection_settings_auto_execute_request_error_action_label')}</label>
                  <Select
                    value={settingsDraft.autoExecuteRequestErrorAction}
                    options={AUTO_ERROR_ACTION_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }))}
                    onChange={handleAutoExecuteRequestErrorActionChange}
                    ariaLabel={t('monitoring.account_inspection_settings_auto_execute_request_error_action_label')}
                  />
                  <span>{t('monitoring.account_inspection_settings_auto_execute_request_error_action_hint')}</span>
                  {settingsDraft.autoExecuteRequestErrorAction === 'delete' ? (
                    <div className={styles.settingsDangerNote}>
                      {t('monitoring.account_inspection_delete_irreversible_warning')}
                    </div>
                  ) : null}
                </div>
                <div className={styles.settingsFormPanel}>
                  <Input
                    label={t('monitoring.account_inspection_settings_auto_execute_confirmations_label')}
                    hint={t('monitoring.account_inspection_settings_auto_execute_confirmations_hint', {
                      min: AUTO_EXECUTE_CONFIRMATION_LIMITS.min,
                      max: AUTO_EXECUTE_CONFIRMATION_LIMITS.max,
                    })}
                    type="number"
                    value={settingsDraft.autoExecuteConfirmations}
                    onChange={(event) => handleSettingsDraftChange('autoExecuteConfirmations', event.target.value)}
                    min={AUTO_EXECUTE_CONFIRMATION_LIMITS.min}
                    max={AUTO_EXECUTE_CONFIRMATION_LIMITS.max}
                    step={1}
                  />
                </div>
              </div>
            </section>
          </div>
        </div>

      </ProSettingsSheet>
    </div>
  );
}
