import { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState, type ChangeEvent, type CSSProperties, type DragEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import {
  IconSearch,
  IconSlidersHorizontal,
} from '@/components/ui/icons';
import {
  buildAccountRowsByAccount,
  buildLocalDayKey,
  getRangeStartMs,
  useMonitoringData,
  type MonitoringEventRow,
  type MonitoringTimeRange,
} from '@/features/monitoring/hooks/useMonitoringData';
import { REALTIME_LOG_PAGE_SIZE, useRealtimeLogData } from '@/features/monitoring/hooks/useRealtimeLogData';
import { useUsageData, type UsageEventPageFilters, type UsagePayload } from '@/features/monitoring/hooks/useUsageData';
import { useUsageAggregates, type UsageAggregateBucket } from '@/features/monitoring/hooks/useUsageAggregates';
import { findMonitoringAuthIndexes } from '@/features/monitoring/monitoringAuthSearch';
import {
  buildAccountQuotaEntriesByAccount,
  buildAccountQuotaTargetsByAccount,
  getQuotaForTarget,
  requestAccountQuota,
  settleWithConcurrency,
  type AccountQuotaSourceRow,
  type AccountQuotaState,
  type AnyQuotaConfig,
} from '@/features/monitoring/accountQuota';
import { AccountStatsPanel } from '@/features/monitoring/components/AccountStatsPanel';
import { ModelPriceManagerModal } from '@/features/monitoring/components/ModelPriceManagerModal';
import { MonitoringSettingsModal } from '@/features/monitoring/components/MonitoringSettingsModal';
import {
  RealtimeErrorDetailsPanel,
  RecentPattern,
  StatusBadge,
} from '@/features/monitoring/components/RealtimeLogDetails';
import {
  RealtimeCostCell,
} from '@/features/monitoring/components/RealtimeCostCell';
import {
  ApiKeyRankingPanel,
  ModelStatsPanel,
  TokenDistributionPanel,
  TopUsageStats,
  UsageTrendHeader,
  UsageTrendPanel,
  type UsageMetricCard,
} from '@/features/monitoring/components/UsageAnalyticsPanels';
import {
  buildAggregateSummary,
  buildServerAccountRows,
} from '@/features/monitoring/monitoringAggregates';
import {
  addMonitoringSummaryRow,
  buildServerUsageTrendAnalytics,
  buildUsageTrendAnalytics,
  buildUsageTrendRangeLabel,
  createMonitoringSummaryAccumulator,
  finalizeMonitoringSummary,
  formatPercent,
  getAccountSortValue,
  getRankingMetricValue,
  type AccountSortMetric,
  type RankingMetric,
} from '@/features/monitoring/monitoringAnalytics';
import { TIME_RANGE_OPTIONS } from '@/features/monitoring/monitoringOptions';
import {
  buildMonitoringSettingsFromDraft,
  createMonitoringSettingsDraft,
  type MonitoringSettings,
  type MonitoringSettingsDraft,
} from '@/features/monitoring/monitoringSettings';
import {
  createPriceDraft,
  formatDeltaPercent,
  parsePriceContextSize,
  parsePriceValue,
  type PriceDraft,
  type PriceManagementView,
  type PriceRuleTarget,
  type PriceSyncChangeFilter,
  type PriceTierDraft,
} from '@/features/monitoring/modelPricePresentation';
import {
  REALTIME_LOG_COLUMN_DEFAULT_WIDTHS,
  clampRealtimeLogColumnWidth,
  createDefaultRealtimeLogColumns,
  isRealtimeLogColumnKey,
  loadRealtimeLogColumns,
  loadRealtimeLogFollowEnabled,
  normalizeRealtimeLogColumns,
  saveRealtimeLogColumns,
  saveRealtimeLogFollowEnabled,
  type RealtimeLogColumnKey,
  type RealtimeLogColumnPreference,
} from '@/features/monitoring/realtimeLogPreferences';
import {
  buildRealtimeDiagnosticClipboardText,
  buildRealtimeLogPageRows,
  buildRealtimeMetaText,
  buildRealtimeStatusLabel,
  getClientPaginationRange,
  translateRealtimeErrorText,
  type RealtimeLogRow,
} from '@/features/monitoring/realtimeLogPresentation';
import { hasUsageBackupManifest } from '@/features/monitoring/usageBackup';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { apiClient } from '@/services/api/client';
import { useAuthStore, useConfigStore, useNotificationStore, useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { maskSensitiveText } from '@/utils/format';
import { getStatusFromError } from '@/utils/quota';
import {
  deleteModelPriceRule,
  formatCompactNumber,
  formatDurationMs,
  formatUsd,
  formatUsdPrecise,
  loadModelPriceRules,
  loadModelPriceSyncState,
  normalizeAuthIndex,
  recalculateModelPriceHistory,
  saveModelPriceRule,
  syncModelPricesFromModelsDev,
  type ModelPriceRule,
  type ModelPriceSyncResult,
  type ModelPriceSyncState,
  type ObservedModelPriceTarget,
} from '@/utils/usage';
import type { QuotaStatusState } from '@/components/quota/QuotaCard';
import quotaStyles from '@/pages/QuotaPage.module.scss';
import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';
import styles from '@/features/monitoring/monitoring.module.scss';

type StatusFilter = 'all' | 'success' | 'failed';

const ACCOUNT_STATS_ANALYTICS_ROW_LIMIT = 6000;
const ACCOUNT_QUOTA_REQUEST_CONCURRENCY = 4;
type RealtimeLogColumnDefinition = {
  key: RealtimeLogColumnKey;
  label: string;
  colClassName: string;
  headerClassName?: string;
  cellClassName?: (row: RealtimeLogRow) => string | undefined;
  render: (row: RealtimeLogRow) => ReactNode;
  width: number;
};
const formatTokenCount = (value: number) => Math.max(0, Math.round(Number(value) || 0)).toLocaleString();

const getCacheHitRate = (row: Pick<MonitoringEventRow, 'inputTokens' | 'cachedTokens'>): number | null => (
  row.inputTokens > 0 ? Math.min(Math.max(row.cachedTokens / row.inputTokens, 0), 1) : null
);

const getSuccessRateClassName = (rate: number) => (
  rate >= 0.95 ? styles.goodText : rate >= 0.85 ? styles.warnText : styles.badText
);

const getRealtimeLogColumnContentTexts = (key: RealtimeLogColumnKey, row: RealtimeLogRow) => {
  switch (key) {
    case 'type':
      return [row.provider, row.account || row.authLabel || row.accountMasked || '-'];
    case 'model':
      return [row.model, row.modelAlias && row.modelAlias !== row.model ? row.modelAlias : buildRealtimeMetaText(row)];
    case 'reasoningEffort':
      return [row.reasoningEffort.trim() || '-'];
    case 'stream':
      return [row.stream ? 'Streaming' : 'Non-streaming'];
    case 'apiKey':
      return [row.clientApiKey.masked];
    case 'recent':
      return ['||||||||||'];
    case 'status':
      return [buildRealtimeStatusLabel(row, row.failed ? 'Failed' : 'Success')];
    case 'successRate':
      return [formatPercent(row.successRate)];
    case 'calls':
      return [formatCompactNumber(row.requestCount)];
    case 'ttft':
      return [formatDurationMs(row.ttftMs)];
    case 'latency':
      return [formatDurationMs(row.latencyMs)];
    case 'tokens':
      return [
        formatTokenCount(row.totalTokens),
        `I ${formatTokenCount(row.inputTokens)} O ${formatTokenCount(row.outputTokens)}`,
        row.reasoningTokens > 0 ? `R ${formatTokenCount(row.reasoningTokens)}` : '',
      ];
    case 'cacheRead':
      return [
        formatTokenCount(row.cachedTokens),
        row.inputTokens > 0 ? formatPercent(Math.min(row.cachedTokens / row.inputTokens, 1)) : '--',
      ];
    case 'cost':
      return [formatUsdPrecise(row.totalCost)];
    case 'time':
      return [new Date(row.timestampMs).toLocaleString()];
    default:
      return [];
  }
};

const estimateRealtimeLogColumnWidth = (
  key: RealtimeLogColumnKey,
  label: string,
  rows: RealtimeLogRow[]
) => {
  const maxTextLength = rows.reduce((maxLength, row) => {
    const rowMaxLength = getRealtimeLogColumnContentTexts(key, row)
      .reduce((innerMax, text) => Math.max(innerMax, text.length), 0);
    return Math.max(maxLength, rowMaxLength);
  }, label.length);
  const characterWidth = key === 'recent' ? 6 : key === 'tokens' || key === 'cacheRead' ? 8 : 7;
  const padding = key === 'status' ? 36 : key === 'tokens' || key === 'cacheRead' ? 34 : 28;
  return clampRealtimeLogColumnWidth(key, maxTextLength * characterWidth + padding);
};

const estimateRealtimeLogHeaderWidth = (key: RealtimeLogColumnKey, label: string) => {
  const textWidth = Array.from(label).reduce((total, char) => (
    total + (char.charCodeAt(0) > 255 ? 13 : 7)
  ), 0);
  return clampRealtimeLogColumnWidth(key, textWidth + 42);
};

type UsageImportResult = {
  added?: number;
  skipped?: number;
  total?: number;
  failed?: number;
  modelPrices?: number;
  modelPriceRecords?: number;
  modelPriceRules?: number;
  quotaCache?: number;
  quotaCacheRecords?: number;
  routingCursors?: number;
  routingCursorRecords?: number;
  authRuntimeStats?: number;
  authRuntimeStatsRecords?: number;
  accountInspectionSchedule?: boolean;
  accountInspectionScheduleRecords?: number;
  accountInspectionSnapshot?: boolean;
  accountInspectionSnapshotRecords?: number;
  monitoringSettings?: boolean;
  monitoringSettingsRecords?: number;
  legacyBackup?: boolean;
};

type UsageResetResult = {
  deletedEvents: number;
  generation: number;
  resetAtMs: number;
};

export function MonitoringCenterPage() {
  const { t, i18n } = useTranslation();
  const config = useConfigStore((state) => state.config);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const quotaStore = useQuotaStore((state) => state);
  const [timeRange, setTimeRange] = useState<MonitoringTimeRange>('today');
  const [searchInput, setSearchInput] = useState('');
  const [selectedProvider, setSelectedProvider] = useState('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [selectedApiKey, setSelectedApiKey] = useState('all');
  const [selectedStatus, setSelectedStatus] = useState<StatusFilter>('all');
  const [expandedAccounts, setExpandedAccounts] = useState<Record<string, boolean>>({});
  const [selectedRealtimeErrorRow, setSelectedRealtimeErrorRow] = useState<RealtimeLogRow | null>(null);
  const [isPriceModalOpen, setIsPriceModalOpen] = useState(false);
  const [isMonitoringSettingsOpen, setIsMonitoringSettingsOpen] = useState(false);
  const [isMonitoringSettingsLoading, setIsMonitoringSettingsLoading] = useState(false);
  const [isMonitoringSettingsSaving, setIsMonitoringSettingsSaving] = useState(false);
  const [isMonitoringStatisticsResetting, setIsMonitoringStatisticsResetting] = useState(false);
  const [monitoringSettingsDraft, setMonitoringSettingsDraft] = useState<MonitoringSettingsDraft>(() => createMonitoringSettingsDraft());
  const [priceManagementView, setPriceManagementView] = useState<PriceManagementView>('rules');
  const [priceRuleSearch, setPriceRuleSearch] = useState('');
  const [priceSyncChangeFilter, setPriceSyncChangeFilter] = useState<PriceSyncChangeFilter>('all');
  const [priceSyncLockedOverrides, setPriceSyncLockedOverrides] = useState<string[]>([]);
  const [priceModel, setPriceModel] = useState('');
  const [priceDraft, setPriceDraft] = useState<PriceDraft>(() => createPriceDraft());
  const [priceRules, setPriceRules] = useState<ModelPriceRule[]>([]);
  const [observedPriceModels, setObservedPriceModels] = useState<ObservedModelPriceTarget[]>([]);
  const [priceSyncState, setPriceSyncState] = useState<ModelPriceSyncState>({ status: 'idle' });
  const [priceSyncResult, setPriceSyncResult] = useState<ModelPriceSyncResult | null>(null);
  const [isPriceLoading, setIsPriceLoading] = useState(false);
  const [isPriceSaving, setIsPriceSaving] = useState(false);
  const [isPriceSyncing, setIsPriceSyncing] = useState(false);
  const [isImportingUsage, setIsImportingUsage] = useState(false);
  const importInputRef = useRef<HTMLInputElement | null>(null);
  const [accountQuotaStates, setAccountQuotaStates] = useState<Record<string, AccountQuotaState>>({});
  const [isUsageTrendHidden, setIsUsageTrendHidden] = useState(false);
  const [modelRankingMetric, setModelRankingMetric] = useState<RankingMetric>('requests');
  const [apiKeyRankingMetric, setApiKeyRankingMetric] = useState<RankingMetric>('requests');
  const [usageTrendApiKey, setUsageTrendApiKey] = useState('all');
  const [accountStatsMetric, setAccountStatsMetric] = useState<AccountSortMetric>('recent');
  const [isAccountStatsHidden, setIsAccountStatsHidden] = useState(false);
  const [realtimeLogUsage, setRealtimeLogUsage] = useState<UsagePayload | null>(null);
  const [realtimeLogColumns, setRealtimeLogColumns] = useState<RealtimeLogColumnPreference[]>(loadRealtimeLogColumns);
  const [realtimeLogFollowEnabled, setRealtimeLogFollowEnabled] = useState(loadRealtimeLogFollowEnabled);
  const [draggedRealtimeLogColumnKey, setDraggedRealtimeLogColumnKey] = useState<RealtimeLogColumnKey | null>(null);
  const [isRealtimeColumnsMenuOpen, setIsRealtimeColumnsMenuOpen] = useState(false);
  const accountQuotaStatesRef = useRef<Record<string, AccountQuotaState>>({});
  const accountQuotaRequestIdsRef = useRef<Record<string, number>>({});
  const realtimeColumnsMenuRef = useRef<HTMLDivElement | null>(null);
  const deferredSearchInput = useDeferredValue(searchInput);
  const [deferredSearch, setDeferredSearch] = useState(searchInput);

  useEffect(() => {
    const timer = setTimeout(() => setDeferredSearch(deferredSearchInput), 300);
    return () => clearTimeout(timer);
  }, [deferredSearchInput]);

  const {
    usage,
    error: usageError,
		latestId,
		modelPrices,
		refreshUsage,
    loadEventPage,
  } = useUsageData();
  const deferredUsage = useDeferredValue(usage);

  const {
    loading: monitoringLoading,
    error: monitoringError,
    authFiles,
    allRows,
    filteredRows,
    refreshMeta,
  } = useMonitoringData({
    usage: deferredUsage,
    logUsage: realtimeLogUsage,
    serverFilteredLogs: true,
    config,
    modelPrices,
    timeRange,
    searchQuery: '',
    deletedCredentialLabel: t('monitoring.deleted_credential'),
    unattributedApiKeyLabel: t('monitoring.api_key_unattributed'),
  });

  const {
    data: usageAggregates,
    error: aggregatesError,
    refresh: refreshAggregates,
  } = useUsageAggregates({
    latestId,
    timeRange,
    apiKeyHash: usageTrendApiKey,
    enabled: connectionStatus === 'connected',
  });

  const searchMatchedAuthIndexFilter = useMemo(() => {
    return findMonitoringAuthIndexes(authFiles, allRows, deferredSearch);
  }, [allRows, authFiles, deferredSearch]);

  const buildRealtimeLogFilters = useCallback((): UsageEventPageFilters => {
    const nowMs = Date.now();
    const fromMs = getRangeStartMs(timeRange, nowMs);
    return {
      fromMs: Number.isFinite(fromMs) && fromMs > 0 ? fromMs : undefined,
      toMs: nowMs,
      provider: selectedProvider === 'all' ? undefined : selectedProvider,
      model: selectedModel === 'all' ? undefined : selectedModel,
      searchAuthIndexes: searchMatchedAuthIndexFilter || undefined,
      apiKeyHash: selectedApiKey === 'all' ? undefined : selectedApiKey,
      status: selectedStatus,
      search: deferredSearch,
      limit: REALTIME_LOG_PAGE_SIZE,
    };
  }, [deferredSearch, searchMatchedAuthIndexFilter, selectedApiKey, selectedModel, selectedProvider, selectedStatus, timeRange]);

  const handleRealtimeLogGenerationChange = useCallback(() => {
    setSelectedRealtimeErrorRow(null);
    void refreshAggregates();
  }, [refreshAggregates]);

  const {
    page: realtimeLogPage,
    matchedTotal: realtimeLogMatchedTotal,
    nextCursor: realtimeLogNextCursor,
    loading: realtimeLogLoading,
    error: realtimeLogError,
    pendingEventCount: pendingRealtimeEventCount,
    autoRefreshPaused: realtimeLogAutoRefreshPaused,
    wrapperRef: realtimeLogWrapperRef,
    handleScroll: handleRealtimeLogScroll,
    refresh: refreshRealtimeLogs,
    reset: resetRealtimeLogs,
    showPreviousPage: showPreviousRealtimeLogPage,
    showNextPage: showNextRealtimeLogPage,
  } = useRealtimeLogData({
    connectionStatus,
    latestId,
    generation: Number(usage?.generation) || 0,
    usage: realtimeLogUsage,
    setUsage: setRealtimeLogUsage,
    loadEventPage,
    buildFilters: buildRealtimeLogFilters,
    followEnabled: realtimeLogFollowEnabled,
    detailsOpen: Boolean(selectedRealtimeErrorRow),
    onGenerationChange: handleRealtimeLogGenerationChange,
  });

  const refreshAll = useCallback(async () => {
    await Promise.all([refreshUsage(), refreshMeta(false), refreshRealtimeLogs()]);
    await refreshAggregates();
  }, [refreshAggregates, refreshMeta, refreshRealtimeLogs, refreshUsage]);

  const fetchMonitoringSettings = useCallback(async () => {
    const response = await apiClient.get<{ settings: MonitoringSettings }>('/usage/settings');
    setMonitoringSettingsDraft(createMonitoringSettingsDraft(response.settings));
    return response.settings;
  }, []);

  const loadMonitoringSettings = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    setIsMonitoringSettingsLoading(true);
    try {
      await fetchMonitoringSettings();
      setIsMonitoringSettingsOpen(true);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringSettingsLoading(false);
    }
  }, [connectionStatus, fetchMonitoringSettings, showNotification, t]);

  const handleSaveMonitoringSettings = useCallback(async (closeModal = true) => {
    const settings = buildMonitoringSettingsFromDraft(monitoringSettingsDraft);
    if (settings.webdav.enabled && !settings.webdav.url) {
      showNotification(t('usage_stats.monitoring_settings_webdav_url_required'), 'warning');
      return;
    }
    setIsMonitoringSettingsSaving(true);
    try {
      const response = await apiClient.put<{ settings: MonitoringSettings }>('/usage/settings', { settings });
      setMonitoringSettingsDraft(createMonitoringSettingsDraft(response.settings));
      if (closeModal) setIsMonitoringSettingsOpen(false);
      showNotification(t('usage_stats.monitoring_settings_saved'), 'success');
      await refreshAll();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringSettingsSaving(false);
    }
  }, [monitoringSettingsDraft, refreshAll, showNotification, t]);

  const executeMonitoringStatisticsReset = useCallback(async () => {
    setIsMonitoringStatisticsResetting(true);
    try {
      const result = await apiClient.post<UsageResetResult>('/usage/reset', { confirm: true });
      setSelectedRealtimeErrorRow(null);
      resetRealtimeLogs();
      await Promise.all([refreshUsage(), refreshRealtimeLogs(), refreshAggregates()]);
      showNotification(t('usage_stats.monitoring_settings_reset_success', { count: result.deletedEvents }), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringStatisticsResetting(false);
    }
  }, [refreshAggregates, refreshRealtimeLogs, refreshUsage, resetRealtimeLogs, showNotification, t]);

  const handleMonitoringStatisticsReset = useCallback(() => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    showConfirmation({
      title: t('usage_stats.monitoring_settings_reset_confirm_title'),
      message: t('usage_stats.monitoring_settings_reset_confirm_message', {
        count: Number(usage?.total_requests) || 0,
      }),
      confirmText: t('usage_stats.monitoring_settings_reset_confirm_button'),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: executeMonitoringStatisticsReset,
    });
  }, [connectionStatus, executeMonitoringStatisticsReset, showConfirmation, showNotification, t, usage?.total_requests]);
  const handleExportUsage = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }

    try {
      const response = await apiClient.getRaw('/usage/export', { responseType: 'blob' });
      const blob = response.data instanceof Blob ? response.data : new Blob([response.data], { type: 'application/x-ndjson' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
      link.href = url;
      link.download = `usage-export-${timestamp}.jsonl`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      showNotification(t('usage_stats.export_success'), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    }
  }, [connectionStatus, showNotification, t]);

  const handleImportUsageClick = useCallback(() => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    importInputRef.current?.click();
  }, [connectionStatus, showNotification, t]);

  const executeUsageImport = useCallback(async (content: string, allowLegacy: boolean) => {
    setIsImportingUsage(true);
    try {
      const result = await apiClient.post<UsageImportResult>('/usage/import', content, {
        headers: { 'Content-Type': 'application/x-ndjson' },
        params: allowLegacy ? { allow_legacy: 1 } : undefined,
      });
      const importedExtras = [
        (result.modelPriceRecords ?? 0) > 0 ? t('usage_stats.import_model_prices_restored', { count: Math.max(result.modelPrices ?? 0, result.modelPriceRules ?? 0) }) : '',
        (result.quotaCacheRecords ?? 0) > 0 ? t('usage_stats.import_quota_cache_restored', { count: result.quotaCache ?? 0 }) : '',
        (result.routingCursorRecords ?? 0) > 0 ? t('usage_stats.import_routing_cursors_restored', { count: result.routingCursors ?? 0 }) : '',
        (result.authRuntimeStatsRecords ?? 0) > 0 ? t('usage_stats.import_auth_runtime_stats_restored', { count: result.authRuntimeStats ?? 0 }) : '',
        result.accountInspectionSchedule ? t('usage_stats.import_account_inspection_schedule_restored') : '',
        result.accountInspectionSnapshot ? t('usage_stats.import_account_inspection_snapshot_restored') : '',
        result.monitoringSettings ? t('usage_stats.import_monitoring_settings_restored') : '',
      ].filter(Boolean).join(' · ');
      showNotification(
        [
          t('usage_stats.import_success', {
            added: result.added ?? 0,
            skipped: result.skipped ?? 0,
            total: result.total ?? 0,
            failed: result.failed ?? 0,
          }),
          importedExtras,
        ].filter(Boolean).join(' · '),
        (result.failed ?? 0) > 0 ? 'warning' : 'success'
      );
      quotaPersistenceMiddleware.markStale();
      await quotaPersistenceMiddleware.ensureFresh();
      await refreshAll();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsImportingUsage(false);
    }
  }, [refreshAll, showNotification, t]);

  const handleImportUsageFile = useCallback(
    async (event: ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0];
      event.target.value = '';
      if (!file) return;

      try {
        const content = await file.text();
        if (!content.trim()) {
          showNotification(t('usage_stats.import_invalid'), 'error');
          return;
        }
        if (!hasUsageBackupManifest(content)) {
          showConfirmation({
            title: t('usage_stats.import_legacy_confirm_title'),
            message: t('usage_stats.import_legacy_confirm_message'),
            confirmText: t('usage_stats.import_legacy_confirm_button'),
            cancelText: t('common.cancel'),
            variant: 'danger',
            onConfirm: () => executeUsageImport(content, true),
          });
          return;
        }
        await executeUsageImport(content, false);
      } catch (error) {
        showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
      }
    },
    [executeUsageImport, showConfirmation, showNotification, t]
  );

  const handleCopyRealtimeDiagnostic = useCallback((row: RealtimeLogRow) => {
    const text = buildRealtimeDiagnosticClipboardText(row, t, i18n.language);
    if (!navigator.clipboard?.writeText) {
      showNotification(translateRealtimeErrorText('copy_diagnostic_failed', t, i18n.language), 'error');
      return;
    }
    void navigator.clipboard.writeText(text)
      .then(() => showNotification(translateRealtimeErrorText('copy_diagnostic_success', t, i18n.language), 'success'))
      .catch(() => showNotification(translateRealtimeErrorText('copy_diagnostic_failed', t, i18n.language), 'error'));
  }, [i18n.language, showNotification, t]);

  useHeaderRefresh(refreshAll);

  const combinedError = [usageError, monitoringError, realtimeLogError].filter(Boolean).join('；');
  const hasPrices = Object.keys(modelPrices).length > 0;

  useEffect(() => {
    saveRealtimeLogFollowEnabled(realtimeLogFollowEnabled);
  }, [realtimeLogFollowEnabled]);

  useEffect(() => {
    accountQuotaStatesRef.current = accountQuotaStates;
  }, [accountQuotaStates]);

  const setQuotaForConfig = useCallback(
    (quotaConfig: AnyQuotaConfig, updater: Record<string, QuotaStatusState> | ((prev: Record<string, QuotaStatusState>) => Record<string, QuotaStatusState>)) => {
      const setter = useQuotaStore.getState()[quotaConfig.storeSetter] as (value: typeof updater) => void;
      setter(updater);
    },
    []
  );

  const requestLogRows = filteredRows;

  const requestLogDerived = useMemo(() => {
    const providers = new Set<string>();
    const models = new Set<string>();
    const apiKeys = new Map<string, string>();

    allRows.forEach((row) => {
      if (row.provider) providers.add(row.provider);
      if (row.model) models.add(row.model);
      if (row.clientApiKey.hash && row.clientApiKey.hash !== '-' && !apiKeys.has(row.clientApiKey.hash)) {
        apiKeys.set(row.clientApiKey.hash, row.clientApiKey.masked);
      }
    });
    usageAggregates?.providers.forEach((bucket) => {
      if (bucket.provider) providers.add(bucket.provider);
    });
    usageAggregates?.models.forEach((bucket) => {
      if (bucket.model) models.add(bucket.model);
    });
    usageAggregates?.apiKeys.forEach((bucket) => {
      if (bucket.apiKeyHash && !apiKeys.has(bucket.apiKeyHash)) {
        apiKeys.set(bucket.apiKeyHash, maskSensitiveText(bucket.apiKeyHash));
      }
    });

    const sortedModels = Array.from(models).filter(Boolean).sort((left, right) => left.localeCompare(right));

    return {
      providerOptions: [
        { value: 'all', label: t('monitoring.filter_all_providers') },
        ...Array.from(providers)
          .filter(Boolean)
          .sort((left, right) => left.localeCompare(right))
          .map((value) => ({ value, label: value })),
      ],
      modelOptions: [
        { value: 'all', label: t('monitoring.filter_all_models') },
        ...sortedModels.map((value) => ({ value, label: value })),
      ],
      apiKeyOptions: [
        { value: 'all', label: t('monitoring.filter_all_api_keys') },
        ...Array.from(apiKeys.entries())
          .sort((left, right) => left[1].localeCompare(right[1]))
          .map(([value, label]) => ({ value, label })),
      ],
    };
  }, [allRows, t, usageAggregates]);
  const {
    providerOptions,
    modelOptions,
    apiKeyOptions,
  } = requestLogDerived;

  const statusOptions = useMemo(
    () => [
      { value: 'all', label: t('monitoring.filter_all_statuses') },
      { value: 'success', label: t('monitoring.filter_status_success') },
      { value: 'failed', label: t('monitoring.filter_status_failed') },
    ],
    [t]
  );

  useEffect(() => {
    if (selectedProvider !== 'all' && !providerOptions.some((option) => option.value === selectedProvider)) {
      setSelectedProvider('all');
    }
    if (selectedModel !== 'all' && !modelOptions.some((option) => option.value === selectedModel)) {
      setSelectedModel('all');
    }
    if (selectedApiKey !== 'all' && !apiKeyOptions.some((option) => option.value === selectedApiKey)) {
      setSelectedApiKey('all');
    }
  }, [apiKeyOptions, modelOptions, providerOptions, selectedApiKey, selectedModel, selectedProvider]);

  const authFilesByAuthIndex = useMemo(() => {
    const map = new Map<string, AuthFileItem>();
    authFiles.forEach((file) => {
      const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
      if (!authIndex || map.has(authIndex)) return;
      map.set(authIndex, file);
    });
    return map;
  }, [authFiles]);

  const scopedRowsState = useMemo(() => ({
    rows: requestLogRows,
    failureCount: requestLogRows.filter((row) => row.failed).length,
  }), [requestLogRows]);
  const scopedRows = scopedRowsState.rows;
  const scopedFailureCount = scopedRowsState.failureCount;

  const usageRowGroups = useMemo(() => {
    const nowMs = Math.max(
      Number(usageAggregates?.snapshotAtMs) || 0,
      allRows.reduce((latest, row) => Math.max(latest, row.timestampMs), 0)
    );
    const summaryWindowStartMs = nowMs - 30 * 60 * 1000;
    const todayStart = new Date(nowMs);
    todayStart.setHours(0, 0, 0, 0);
    const tomorrowStart = new Date(todayStart);
    tomorrowStart.setDate(tomorrowStart.getDate() + 1);
    const yesterdayStart = new Date(todayStart);
    yesterdayStart.setDate(yesterdayStart.getDate() - 1);
    const trendStartMs = getRangeStartMs(timeRange, nowMs);
    const trendStatsRows: MonitoringEventRow[] = [];
    const topSummaryAccumulator = createMonitoringSummaryAccumulator();
    const todaySummaryAccumulator = createMonitoringSummaryAccumulator();
    const trendSummaryAccumulator = createMonitoringSummaryAccumulator();
    let todayCost = 0;
    let yesterdayCost = 0;

    allRows.forEach((row) => {
      if (!row.statsIncluded) return;
      addMonitoringSummaryRow(topSummaryAccumulator, row, summaryWindowStartMs, nowMs);
      if (row.timestampMs >= todayStart.getTime() && row.timestampMs < tomorrowStart.getTime()) {
        addMonitoringSummaryRow(todaySummaryAccumulator, row, summaryWindowStartMs, nowMs);
        todayCost += row.totalCost;
      } else if (row.timestampMs >= yesterdayStart.getTime() && row.timestampMs < todayStart.getTime()) {
        yesterdayCost += row.totalCost;
      }
      if (row.timestampMs >= trendStartMs && row.timestampMs <= nowMs) {
        trendStatsRows.push(row);
        addMonitoringSummaryRow(trendSummaryAccumulator, row, summaryWindowStartMs, nowMs);
      }
    });

    return {
      trendStatsRows,
      topSummary: finalizeMonitoringSummary(topSummaryAccumulator),
      todaySummary: finalizeMonitoringSummary(todaySummaryAccumulator),
      trendSummary: finalizeMonitoringSummary(trendSummaryAccumulator),
      todayCost,
      yesterdayCost,
    };
  }, [allRows, timeRange, usageAggregates?.snapshotAtMs]);
  const {
    trendStatsRows,
    topSummary,
    todaySummary,
    yesterdayCost,
  } = usageRowGroups;

  const clientUsageTrendAnalytics = useMemo(
    () => buildUsageTrendAnalytics(trendStatsRows, timeRange, usageTrendApiKey, t('monitoring.filter_all_api_keys')),
    [trendStatsRows, timeRange, usageTrendApiKey, t]
  );
  const serverUsageTrendAnalytics = useMemo(
    () => buildServerUsageTrendAnalytics(
      usageAggregates,
      usageAggregates?.scopeTimeRange ?? timeRange,
      modelPrices,
      clientUsageTrendAnalytics.apiKeyOptions,
      usageAggregates?.scopeApiKeyHash ?? usageTrendApiKey,
      t('monitoring.api_key_unattributed')
    ),
    [clientUsageTrendAnalytics.apiKeyOptions, modelPrices, t, timeRange, usageAggregates, usageTrendApiKey]
  );
  const aggregateTrendScopeMatches = Boolean(
    usageAggregates
      && usageAggregates.scopeTimeRange === timeRange
      && usageAggregates.scopeApiKeyHash === usageTrendApiKey
  );
  const usageTrendAnalytics = useMemo(() => {
    if (!serverUsageTrendAnalytics || (aggregatesError && !aggregateTrendScopeMatches)) {
      return clientUsageTrendAnalytics;
    }
    if (serverUsageTrendAnalytics.apiKeyRows.length > 0 || clientUsageTrendAnalytics.apiKeyRows.length === 0) {
      return serverUsageTrendAnalytics;
    }
    return {
      ...serverUsageTrendAnalytics,
      apiKeyRows: clientUsageTrendAnalytics.apiKeyRows,
    };
  }, [aggregateTrendScopeMatches, aggregatesError, clientUsageTrendAnalytics, serverUsageTrendAnalytics]);
  const usageTrendApiKeyOptions = usageTrendAnalytics.apiKeyOptions;
  const usageTrendPoints = usageTrendAnalytics.trendPoints;
  const tokenDistributionPoints = usageTrendAnalytics.tokenDistributionPoints;
  useEffect(() => {
    if (usageTrendApiKey !== 'all' && !usageTrendApiKeyOptions.some((option) => option.value === usageTrendApiKey)) {
      setUsageTrendApiKey('all');
    }
  }, [usageTrendApiKey, usageTrendApiKeyOptions]);

  const modelRankingRows = useMemo(
    () => [...usageTrendAnalytics.modelRows]
      .sort((left, right) => (
        getRankingMetricValue(right, modelRankingMetric) - getRankingMetricValue(left, modelRankingMetric)
        || right.totalTokens - left.totalTokens
        || right.totalCalls - left.totalCalls
      )),
    [modelRankingMetric, usageTrendAnalytics.modelRows]
  );
  const modelRankingMetricTotal = usageTrendAnalytics.scopedTotals[modelRankingMetric];
  const apiKeyRankingRows = useMemo(
    () => [...usageTrendAnalytics.apiKeyRows]
      .sort((left, right) => (
        getRankingMetricValue(right, apiKeyRankingMetric) - getRankingMetricValue(left, apiKeyRankingMetric)
        || right.totalCalls - left.totalCalls
        || right.totalCost - left.totalCost
      ))
      .slice(0, 8),
    [apiKeyRankingMetric, usageTrendAnalytics.apiKeyRows]
  );
  const apiKeyRankingMetricTotal = useMemo(
    () => usageTrendAnalytics.apiKeyRows.reduce(
      (total, row) => total + getRankingMetricValue(row, apiKeyRankingMetric),
      0
    ),
    [apiKeyRankingMetric, usageTrendAnalytics.apiKeyRows]
  );
  const accountStatsFilteredRows = useMemo(
    () => trendStatsRows.length > ACCOUNT_STATS_ANALYTICS_ROW_LIMIT
      ? trendStatsRows.slice(0, ACCOUNT_STATS_ANALYTICS_ROW_LIMIT)
      : trendStatsRows,
    [trendStatsRows]
  );
  const clientAccountStatsRows = useMemo(
    () => buildAccountRowsByAccount(accountStatsFilteredRows, true),
    [accountStatsFilteredRows]
  );
  const serverAccountStatsRows = useMemo(
    () => usageAggregates
      ? buildServerAccountRows(usageAggregates.accounts, allRows, authFilesByAuthIndex, modelPrices, t('monitoring.deleted_credential'))
      : null,
    [allRows, authFilesByAuthIndex, modelPrices, t, usageAggregates]
  );
  const accountStatsRows = useMemo(
    () => [...(
      aggregatesError && usageAggregates?.scopeTimeRange !== timeRange
        ? clientAccountStatsRows
        : serverAccountStatsRows ?? clientAccountStatsRows
    )]
      .sort((left, right) => (
        getAccountSortValue(right, accountStatsMetric) - getAccountSortValue(left, accountStatsMetric)
        || right.lastSeenAt - left.lastSeenAt
        || right.totalCalls - left.totalCalls
      )),
    [accountStatsMetric, aggregatesError, clientAccountStatsRows, serverAccountStatsRows, timeRange, usageAggregates?.scopeTimeRange]
  );
  const serverTopSummary = useMemo(
    () => usageAggregates ? buildAggregateSummary(usageAggregates.allSummary, modelPrices) : null,
    [modelPrices, usageAggregates]
  );
  const recentDailySummaries = useMemo(() => {
    if (!usageAggregates) return null;
    const grouped = new Map<string, UsageAggregateBucket[]>();
    usageAggregates.recentDailySummary.forEach((bucket) => {
      const dayKey = buildLocalDayKey(bucket.bucketStartMs);
      const items = grouped.get(dayKey) ?? [];
      items.push(bucket);
      grouped.set(dayKey, items);
    });
    const now = new Date();
    const todayKey = buildLocalDayKey(now.getTime());
    now.setDate(now.getDate() - 1);
    const yesterdayKey = buildLocalDayKey(now.getTime());
    return {
      today: buildAggregateSummary(grouped.get(todayKey) ?? [], modelPrices),
      yesterday: buildAggregateSummary(grouped.get(yesterdayKey) ?? [], modelPrices),
    };
  }, [modelPrices, usageAggregates]);
  const effectiveTopSummary = serverTopSummary ?? topSummary;
  const effectiveTodaySummary = recentDailySummaries?.today ?? todaySummary;
  const effectiveTodayCost = effectiveTodaySummary.totalCost;
  const effectiveYesterdayCost = recentDailySummaries?.yesterday.totalCost ?? yesterdayCost;
  const timeRangeLabel = useMemo(() => buildUsageTrendRangeLabel(timeRange, t), [timeRange, t]);
  const realtimeLogTotalCount = realtimeLogMatchedTotal;
  const realtimeLogTotalPages = realtimeLogTotalCount > 0 ? Math.ceil(realtimeLogTotalCount / REALTIME_LOG_PAGE_SIZE) : 0;
  const normalizedRealtimeLogPage = Math.min(Math.max(1, realtimeLogPage), Math.max(1, realtimeLogTotalPages));
  const realtimeLogPageRows = useMemo(
    () => buildRealtimeLogPageRows(scopedRows, 1, REALTIME_LOG_PAGE_SIZE).rows,
    [scopedRows]
  );
  const realtimeLogPagination = getClientPaginationRange(
    normalizedRealtimeLogPage,
    REALTIME_LOG_PAGE_SIZE,
    realtimeLogTotalCount,
    realtimeLogPageRows.length
  );
  const realtimeLogColumnDefinitions = useMemo<Record<RealtimeLogColumnKey, RealtimeLogColumnDefinition>>(() => ({
    type: {
      key: 'type',
      label: t('monitoring.column_type'),
      colClassName: styles.realtimeTypeCol,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.type,
      render: (row) => (
        <div className={styles.primaryCell}>
          <span>{row.provider}</span>
          <small>{row.account || row.authLabel || row.accountMasked || '-'}</small>
        </div>
      ),
    },
    model: {
      key: 'model',
      label: t('monitoring.column_model'),
      colClassName: styles.realtimeModelCol,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.model,
      render: (row) => (
        <div className={styles.primaryCell}>
          <span className={styles.monoCell}>{row.model}</span>
          <small className={styles.monoCell}>
            {row.modelAlias && row.modelAlias !== row.model ? row.modelAlias : buildRealtimeMetaText(row)}
          </small>
          {row.modelAlias && row.modelAlias !== row.model ? (
            <small className={styles.monoCell}>{buildRealtimeMetaText(row)}</small>
          ) : null}
        </div>
      ),
    },
    reasoningEffort: {
      key: 'reasoningEffort',
      label: t('monitoring.column_reasoning_effort'),
      colClassName: styles.realtimeReasoningCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => `${styles.realtimeCenterCell} ${styles.realtimeNowrapCell}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.reasoningEffort,
      render: (row) => {
        const reasoningEffort = row.reasoningEffort.trim();
        return reasoningEffort ? (
          <span className={`${styles.realtimeReasoningBadge} ${styles.monoCell}`} title={reasoningEffort}>
            <StatusBadge tone="good">{reasoningEffort}</StatusBadge>
          </span>
        ) : (
          <span className={styles.mutedText}>-</span>
        );
      },
    },
    stream: {
      key: 'stream',
      label: t('monitoring.column_stream'),
      colClassName: styles.realtimeStreamCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => `${styles.realtimeCenterCell} ${styles.realtimeNowrapCell}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.stream,
      render: (row) => (
        <span className={`${styles.realtimeReasoningBadge} ${row.stream ? '' : styles.realtimeNonStreamingBadge}`}>
          <StatusBadge tone="good">
            {t(row.stream ? 'monitoring.stream_mode_streaming' : 'monitoring.stream_mode_non_streaming')}
          </StatusBadge>
        </span>
      ),
    },
    apiKey: {
      key: 'apiKey',
      label: t('monitoring.api_key_label'),
      colClassName: styles.realtimeApiKeyCol,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.apiKey,
      render: (row) => <span className={styles.monoCell}>{row.clientApiKey.masked}</span>,
    },
    recent: {
      key: 'recent',
      label: t('monitoring.recent_status'),
      colClassName: styles.realtimeRecentCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => `${styles.realtimeCenterCell} ${styles.realtimeNowrapCell}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.recent,
      render: (row) => (
        <div className={styles.recentStatusCell}>
          <RecentPattern
            pattern={row.recentPattern}
            variant="plain"
            label={t('monitoring.recent_pattern_label', {
              total: row.recentPattern.length,
              success: row.recentSuccessCount,
              failure: row.recentFailureCount,
            })}
          />
        </div>
      ),
    },
    status: {
      key: 'status',
      label: t('monitoring.request_status'),
      colClassName: styles.realtimeStatusCol,
      headerClassName: styles.realtimeCenterHeader,
      cellClassName: () => styles.realtimeCenterCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.status,
      render: (row) => (
        <div className={styles.primaryCell}>
          {row.failed ? (
            <button
              type="button"
              className={styles.realtimeStatusErrorButton}
              onClick={() => setSelectedRealtimeErrorRow(row)}
              title={translateRealtimeErrorText('error_details_click_hint', t, i18n.language)}
              aria-label={translateRealtimeErrorText('error_details_click_hint', t, i18n.language)}
            >
              <StatusBadge tone="bad">{buildRealtimeStatusLabel(row, t('monitoring.result_failed'))}</StatusBadge>
            </button>
          ) : (
            <StatusBadge tone="good">{t('monitoring.result_success')}</StatusBadge>
          )}
        </div>
      ),
    },
    successRate: {
      key: 'successRate',
      label: t('monitoring.column_success_rate'),
      colClassName: styles.realtimeRateCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: (row) => `${styles.realtimeMetricCell} ${getSuccessRateClassName(row.successRate)}`,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.successRate,
      render: (row) => formatPercent(row.successRate),
    },
    calls: {
      key: 'calls',
      label: t('monitoring.total_calls'),
      colClassName: styles.realtimeCountCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.calls,
      render: (row) => formatCompactNumber(row.requestCount),
    },
    ttft: {
      key: 'ttft',
      label: t('monitoring.column_ttft'),
      colClassName: styles.realtimeTtftCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.ttft,
      render: (row) => (
        <span
          className={
            row.ttftMs !== null && row.ttftMs >= 15000
              ? styles.badText
              : row.ttftMs !== null && row.ttftMs >= 8000
                ? styles.warnText
                : undefined
          }
        >
          {formatDurationMs(row.ttftMs, { locale: i18n.language })}
        </span>
      ),
    },
    latency: {
      key: 'latency',
      label: t('monitoring.column_latency'),
      colClassName: styles.realtimeLatencyCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.latency,
      render: (row) => (
        <span
          className={
            row.latencyMs !== null && row.latencyMs >= 30000
              ? styles.badText
              : row.latencyMs !== null && row.latencyMs >= 15000
                ? styles.warnText
                : undefined
          }
        >
          {formatDurationMs(row.latencyMs, { locale: i18n.language })}
        </span>
      ),
    },
    tokens: {
      key: 'tokens',
      label: t('monitoring.realtime_tokens_column'),
      colClassName: styles.realtimeUsageCol,
      cellClassName: () => styles.realtimeTokensTableCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.tokens,
      render: (row) => (
        <div className={`${styles.primaryCell} ${styles.realtimeTokenCell}`}>
          <span>{t('monitoring.realtime_tokens_total')}: <strong>{formatTokenCount(row.totalTokens)}</strong></span>
          <small>
            {t('monitoring.realtime_tokens_input')}: {formatTokenCount(row.inputTokens)}
            {' | '}
            {t('monitoring.realtime_tokens_output')}: {formatTokenCount(row.outputTokens)}
          </small>
          {row.reasoningTokens > 0 ? (
            <small>{t('monitoring.realtime_tokens_reasoning')}: {formatTokenCount(row.reasoningTokens)}</small>
          ) : null}
        </div>
      ),
    },
    cacheRead: {
      key: 'cacheRead',
      label: t('monitoring.realtime_cache_read_column'),
      colClassName: styles.realtimeCacheReadCol,
      cellClassName: () => styles.realtimeCacheReadTableCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.cacheRead,
      render: (row) => {
        const hitRate = getCacheHitRate(row);
        return (
          <div className={styles.realtimeCacheReadCell}>
            <strong>{formatTokenCount(row.cachedTokens)}</strong>
            <small className={hitRate !== null && hitRate < 0.8 ? styles.realtimeCacheHitLow : undefined}>
              {hitRate === null ? '--' : formatPercent(hitRate)} {t('monitoring.realtime_cache_hit')}
            </small>
          </div>
        );
      },
    },
    cost: {
      key: 'cost',
      label: t('monitoring.this_call_cost'),
      colClassName: styles.realtimeCostCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeMetricCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.cost,
      render: (row) => <RealtimeCostCell row={row} hasPrices={hasPrices} t={t} />,
    },
    time: {
      key: 'time',
      label: t('monitoring.column_time'),
      colClassName: styles.realtimeTimeCol,
      cellClassName: () => styles.realtimeTimeCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.time,
      render: (row) => new Date(row.timestampMs).toLocaleString(i18n.language),
    },
  }), [hasPrices, i18n.language, t]);
  const visibleRealtimeLogColumns = useMemo(
    () => realtimeLogColumns
      .filter((column) => column.visible)
      .map((column) => {
        const definition = realtimeLogColumnDefinitions[column.key];
        const contentWidth = column.width ?? estimateRealtimeLogColumnWidth(
          column.key,
          definition.label,
          realtimeLogPageRows
        );
        return {
          ...definition,
          width: Math.max(contentWidth, estimateRealtimeLogHeaderWidth(column.key, definition.label)),
        };
      })
      .filter(Boolean),
    [realtimeLogColumnDefinitions, realtimeLogColumns, realtimeLogPageRows]
  );
  const realtimeLogTableMinWidth = useMemo(
    () => visibleRealtimeLogColumns.reduce((total, column) => total + column.width, 0),
    [visibleRealtimeLogColumns]
  );
  const realtimeLogVisibleColumnCount = Math.max(1, visibleRealtimeLogColumns.length);
  const realtimeLogVisiblePreferenceCount = realtimeLogColumns.filter((column) => column.visible).length;

  const accountQuotaTargetsByAccount = useMemo(
    () => {
      if (!usageAggregates || aggregatesError) {
        return buildAccountQuotaTargetsByAccount(accountStatsFilteredRows, authFilesByAuthIndex);
      }
      const sources = Array.from(new Set(usageAggregates.accounts.map((bucket) => bucket.authIndex).filter(Boolean)))
        .map((authIndex) => {
          const file = authFilesByAuthIndex.get(authIndex as string);
          const account = file
            ? [file.email, file.account, file.label, file.name]
                .map((value) => typeof value === 'string' ? value.trim() : '')
                .find(Boolean) || authIndex as string
            : authIndex as string;
          return {
            authIndex: authIndex as string,
            account,
            authLabel: file?.name || account,
          } satisfies AccountQuotaSourceRow;
        });
      return buildAccountQuotaTargetsByAccount(sources, authFilesByAuthIndex);
    },
    [accountStatsFilteredRows, aggregatesError, authFilesByAuthIndex, usageAggregates]
  );
  const accountQuotaEntriesByAccount = useMemo(
    () => buildAccountQuotaEntriesByAccount(accountQuotaTargetsByAccount, quotaStore, t),
    [accountQuotaTargetsByAccount, quotaStore, t]
  );
  const quotaTargetsByAccountForLoading = accountQuotaTargetsByAccount;

  const priceRuleTargets = useMemo<PriceRuleTarget[]>(() => {
    const targets = new Map<string, PriceRuleTarget>();
    observedPriceModels.forEach((item) => {
      const key = item.model;
      const current = targets.get(key);
      targets.set(key, {
        key,
        model: item.model,
        requests: (current?.requests ?? 0) + item.requests,
        lastSeenAtMs: Math.max(current?.lastSeenAtMs ?? 0, item.lastSeenAtMs),
        rule: current?.rule,
      });
    });
    priceRules.forEach((rule) => {
      const key = rule.model;
      const current = targets.get(key);
      targets.set(key, {
        key,
        model: rule.model,
        requests: current?.requests ?? 0,
        lastSeenAtMs: current?.lastSeenAtMs ?? 0,
        rule,
      });
    });
    return Array.from(targets.values()).sort((left, right) => {
      const configuredDelta = Number(Boolean(left.rule)) - Number(Boolean(right.rule));
      if (configuredDelta !== 0) return configuredDelta;
      return right.lastSeenAtMs - left.lastSeenAtMs || left.key.localeCompare(right.key);
    });
  }, [observedPriceModels, priceRules]);

  const selectedFiltersCount =
    [selectedProvider, selectedModel, selectedApiKey, selectedStatus].filter(
      (value) => value !== 'all'
    ).length + (deferredSearch.trim() ? 1 : 0);

  const usageMetricCards: UsageMetricCard[] = [
    {
      key: 'traffic',
      title: t('monitoring.traffic_title'),
      label: t('monitoring.today_requests'),
      value: formatCompactNumber(effectiveTodaySummary.totalCalls),
      accent: 'blue',
      footer: [
        { label: t('monitoring.total_requests_label'), value: formatCompactNumber(effectiveTopSummary.totalCalls) },
        { label: t('monitoring.total_success_rate'), value: formatPercent(effectiveTopSummary.successRate) },
      ],
    },
    {
      key: 'tokens',
      title: 'Token',
      label: t('monitoring.today_tokens'),
      value: formatCompactNumber(effectiveTodaySummary.totalTokens),
      accent: 'purple',
      footer: [
        { label: t('monitoring.total_tokens_label'), value: formatCompactNumber(effectiveTopSummary.totalTokens) },
        { label: t('monitoring.input_output_reasoning'), value: `${formatCompactNumber(effectiveTopSummary.inputTokens)} / ${formatCompactNumber(effectiveTopSummary.outputTokens)} / ${formatCompactNumber(effectiveTopSummary.reasoningTokens)}` },
      ],
    },
    {
      key: 'cache',
      title: t('monitoring.cache_title'),
      label: t('monitoring.today_cache_hit_rate'),
      value: formatPercent(effectiveTodaySummary.inputTokens > 0 ? effectiveTodaySummary.cachedTokens / effectiveTodaySummary.inputTokens : 0),
      accent: 'green',
      footer: [
        { label: t('monitoring.today_cached_tokens'), value: formatCompactNumber(effectiveTodaySummary.cachedTokens) },
        { label: t('monitoring.total_cache_hits'), value: `${formatCompactNumber(effectiveTopSummary.cachedTokens)} / ${formatPercent(effectiveTopSummary.inputTokens > 0 ? effectiveTopSummary.cachedTokens / effectiveTopSummary.inputTokens : 0)}` },
      ],
    },
    {
      key: 'billing',
      title: t('monitoring.billing_title'),
      label: t('monitoring.today_cost'),
      value: hasPrices ? formatUsd(effectiveTodayCost) : '--',
      accent: 'amber',
      footer: [
        { label: t('monitoring.vs_yesterday'), value: hasPrices ? formatDeltaPercent(effectiveTodayCost, effectiveYesterdayCost) : '--' },
        { label: t('monitoring.total_cost_label'), value: hasPrices ? formatUsd(effectiveTopSummary.totalCost) : '--' },
      ],
    },
  ];

  const clearFilters = useCallback(() => {
    setSearchInput('');
    setSelectedProvider('all');
    setSelectedModel('all');
    setSelectedApiKey('all');
    setSelectedStatus('all');
  }, []);

  const updateRealtimeLogColumns = useCallback((updater: (columns: RealtimeLogColumnPreference[]) => RealtimeLogColumnPreference[]) => {
    setRealtimeLogColumns((current) => {
      const next = normalizeRealtimeLogColumns(updater(current));
      saveRealtimeLogColumns(next);
      return next;
    });
  }, []);

  const toggleRealtimeLogColumn = useCallback((key: RealtimeLogColumnKey) => {
    updateRealtimeLogColumns((columns) => {
      const visibleCount = columns.filter((item) => item.visible).length;
      return columns.map((item) => {
        if (item.key !== key) return item;
        if (item.visible && visibleCount <= 1) return item;
        return { ...item, visible: !item.visible };
      });
    });
  }, [updateRealtimeLogColumns]);

  const reorderRealtimeLogColumn = useCallback((sourceKey: RealtimeLogColumnKey, targetKey: RealtimeLogColumnKey) => {
    if (sourceKey === targetKey) return;
    updateRealtimeLogColumns((columns) => {
      const sourceIndex = columns.findIndex((item) => item.key === sourceKey);
      const targetIndex = columns.findIndex((item) => item.key === targetKey);
      if (sourceIndex < 0 || targetIndex < 0) return columns;
      const next = [...columns];
      const [item] = next.splice(sourceIndex, 1);
      next.splice(targetIndex, 0, item);
      return next;
    });
  }, [updateRealtimeLogColumns]);

  const resizeRealtimeLogColumn = useCallback((key: RealtimeLogColumnKey, width: number) => {
    updateRealtimeLogColumns((columns) => columns.map((column) => (
      column.key === key ? { ...column, width: clampRealtimeLogColumnWidth(key, width) } : column
    )));
  }, [updateRealtimeLogColumns]);

  const handleRealtimeLogHeaderDragStart = useCallback((event: DragEvent<HTMLTableCellElement>, key: RealtimeLogColumnKey) => {
    setDraggedRealtimeLogColumnKey(key);
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', key);
  }, []);

  const handleRealtimeLogHeaderDragOver = useCallback((event: DragEvent<HTMLTableCellElement>) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
  }, []);

  const handleRealtimeLogHeaderDrop = useCallback((event: DragEvent<HTMLTableCellElement>, targetKey: RealtimeLogColumnKey) => {
    event.preventDefault();
    const sourceKey = draggedRealtimeLogColumnKey ?? event.dataTransfer.getData('text/plain');
    if (isRealtimeLogColumnKey(sourceKey)) {
      reorderRealtimeLogColumn(sourceKey, targetKey);
    }
    setDraggedRealtimeLogColumnKey(null);
  }, [draggedRealtimeLogColumnKey, reorderRealtimeLogColumn]);

  const handleRealtimeLogHeaderDragEnd = useCallback(() => {
    setDraggedRealtimeLogColumnKey(null);
  }, []);

  const startRealtimeLogColumnResize = useCallback((event: ReactMouseEvent<HTMLSpanElement>, key: RealtimeLogColumnKey) => {
    event.preventDefault();
    event.stopPropagation();

    const startX = event.clientX;
    const startWidth = visibleRealtimeLogColumns.find((column) => column.key === key)?.width ?? REALTIME_LOG_COLUMN_DEFAULT_WIDTHS[key];
    const handleMouseMove = (moveEvent: MouseEvent) => {
      resizeRealtimeLogColumn(key, startWidth + moveEvent.clientX - startX);
    };
    const handleMouseUp = () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
  }, [resizeRealtimeLogColumn, visibleRealtimeLogColumns]);

  const resetRealtimeLogColumns = useCallback(() => {
    updateRealtimeLogColumns(() => createDefaultRealtimeLogColumns());
  }, [updateRealtimeLogColumns]);

  useEffect(() => {
    if (!isRealtimeColumnsMenuOpen) return undefined;

    const handleDocumentMouseDown = (event: MouseEvent) => {
      if (event.target instanceof Node && realtimeColumnsMenuRef.current?.contains(event.target)) {
        return;
      }
      setIsRealtimeColumnsMenuOpen(false);
    };

    document.addEventListener('mousedown', handleDocumentMouseDown);
    return () => {
      document.removeEventListener('mousedown', handleDocumentMouseDown);
    };
  }, [isRealtimeColumnsMenuOpen]);

  const loadAccountQuota = useCallback(
    async (account: string, force: boolean = false) => {
      const currentState = accountQuotaStatesRef.current[account];
      const targets = quotaTargetsByAccountForLoading.get(account) ?? [];
      const targetKey = targets.map((target) => target.key).join('|');
      if (!force && currentState && currentState.status !== 'idle' && currentState.targetKey === targetKey) {
        return;
      }

      const requestId = (accountQuotaRequestIdsRef.current[account] ?? 0) + 1;
      accountQuotaRequestIdsRef.current[account] = requestId;

      setAccountQuotaStates((previous) => ({
        ...previous,
        [account]: {
          status: 'loading',
          targetKey,
          lastRefreshedAt: previous[account]?.lastRefreshedAt,
        },
      }));

      if (targets.length === 0) {
        if (accountQuotaRequestIdsRef.current[account] !== requestId) return;
        setAccountQuotaStates((previous) => ({
          ...previous,
          [account]: {
            status: 'success',
            targetKey,
            lastRefreshedAt: Date.now(),
          },
        }));
        return;
      }

      targets.forEach((target) => {
        setQuotaForConfig(target.config, (prev) => ({
          ...prev,
          [target.fileName]: target.config.buildLoadingState(),
        }));
      });

      const settled = await settleWithConcurrency(
        targets,
        ACCOUNT_QUOTA_REQUEST_CONCURRENCY,
        (target) => requestAccountQuota(target, t)
      );
      if (accountQuotaRequestIdsRef.current[account] !== requestId) return;

      settled.forEach((result, index) => {
        const target = targets[index];
        const quota = result.status === 'fulfilled'
          ? result.value
          : target.config.buildErrorState(
              result.reason instanceof Error ? result.reason.message : String(result.reason || t('common.unknown_error')),
              getStatusFromError(result.reason)
            ) as QuotaStatusState;

        setQuotaForConfig(target.config, (prev) => ({
          ...prev,
          [target.fileName]: quota,
        }));
      });

      const currentStore = useQuotaStore.getState();
      const entries = targets.map((target) => getQuotaForTarget(currentStore, target)).filter(Boolean) as QuotaStatusState[];
      const hasSuccess = entries.some((entry) => entry.status === 'success');
      const firstError = entries.find((entry) => entry.status === 'error')?.error;
      setAccountQuotaStates((previous) => ({
        ...previous,
        [account]: {
          status: hasSuccess ? 'success' : 'error',
          targetKey,
          error: hasSuccess ? '' : firstError || t('common.unknown_error'),
          lastRefreshedAt: Date.now(),
        },
      }));
    },
    [quotaTargetsByAccountForLoading, setQuotaForConfig, t]
  );

  const toggleAccountExpanded = useCallback((accountId: string, account: string) => {
    if (account && !expandedAccounts[accountId]) {
      void loadAccountQuota(account);
    }
    setExpandedAccounts((previous) => ({
      ...previous,
      [accountId]: !previous[accountId],
    }));
  }, [expandedAccounts, loadAccountQuota]);

  const refreshPriceManagement = useCallback(async () => {
    const [rulesPayload, syncState] = await Promise.all([loadModelPriceRules(), loadModelPriceSyncState()]);
    setPriceRules(rulesPayload.rules);
    setObservedPriceModels(rulesPayload.observedModels);
    setPriceSyncState(syncState);
    return rulesPayload;
  }, []);

  const selectPriceTarget = useCallback((model: string, rules = priceRules) => {
    setPriceModel(model);
    setPriceDraft(createPriceDraft(rules.find((rule) => rule.model === model)));
  }, [priceRules]);

  const openPriceManagement = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }
    setIsPriceModalOpen(true);
    setPriceManagementView('rules');
    setPriceRuleSearch('');
    setPriceSyncLockedOverrides([]);
    setIsPriceLoading(true);
    setIsMonitoringSettingsLoading(true);
    try {
      const [payload] = await Promise.all([refreshPriceManagement(), fetchMonitoringSettings()]);
      const selectedStillExists = payload.observedModels.some((item) => item.model === priceModel)
        || payload.rules.some((rule) => rule.model === priceModel);
      if (selectedStillExists) {
        selectPriceTarget(priceModel, payload.rules);
      } else {
        const nextTarget = payload.observedModels.find((item) => !payload.rules.some((rule) => rule.model === item.model))
          ?? payload.observedModels[0]
          ?? payload.rules[0];
        if (nextTarget) {
          selectPriceTarget(nextTarget.model, payload.rules);
        } else {
          setPriceModel('');
          setPriceDraft(createPriceDraft());
        }
      }
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setIsPriceLoading(false);
      setIsMonitoringSettingsLoading(false);
    }
  }, [connectionStatus, fetchMonitoringSettings, priceModel, refreshPriceManagement, selectPriceTarget, showNotification, t]);

  const handlePriceDraftChange = useCallback((field: Exclude<keyof PriceDraft, 'tiers'>, value: string) => {
    setPriceDraft((previous) => ({ ...previous, [field]: value }));
  }, []);

	const handlePriceTierChange = useCallback((index: number, field: keyof PriceTierDraft, value: string) => {
		setPriceDraft((previous) => ({
			...previous,
			tiers: previous.tiers.map((tier, tierIndex) => tierIndex === index ? { ...tier, [field]: value } : tier),
		}));
	}, []);

	const addPriceTier = useCallback(() => {
		setPriceDraft((previous) => ({
			...previous,
			tiers: [...previous.tiers, { contextSize: '', input: '', output: '', cacheRead: '', cacheWrite: '' }],
		}));
	}, []);

	const removePriceTier = useCallback((index: number) => {
		setPriceDraft((previous) => ({ ...previous, tiers: previous.tiers.filter((_, tierIndex) => tierIndex !== index) }));
	}, []);

	const resetPriceEditor = useCallback(() => {
		setPriceModel('');
		setPriceDraft(createPriceDraft());
	}, []);

	const handleSavePrice = useCallback(async () => {
		if (!priceModel) {
			return;
		}
		const rule: ModelPriceRule = {
			provider: '',
			model: priceModel,
			base: {
				input: parsePriceValue(priceDraft.input),
				output: parsePriceValue(priceDraft.output),
				cacheRead: parsePriceValue(priceDraft.cacheRead),
				cacheWrite: parsePriceValue(priceDraft.cacheWrite),
			},
			tiers: priceDraft.tiers
				.map((tier) => ({
					contextSize: parsePriceContextSize(tier.contextSize),
					input: parsePriceValue(tier.input),
					output: parsePriceValue(tier.output),
					cacheRead: parsePriceValue(tier.cacheRead),
					cacheWrite: parsePriceValue(tier.cacheWrite),
				}))
				.filter((tier) => tier.contextSize > 0),
		};
		setIsPriceSaving(true);
		try {
			await saveModelPriceRule(rule);
			await recalculateModelPriceHistory(false);
			await refreshPriceManagement();
			await refreshAll();
			showNotification(t('usage_stats.model_price_saved'), 'success');
		} catch (error) {
			showNotification(error instanceof Error ? error.message : String(error), 'error');
		} finally {
			setIsPriceSaving(false);
		}
	}, [priceDraft, priceModel, refreshAll, refreshPriceManagement, showNotification, t]);

	const handleDeletePrice = useCallback(
		async (model: string) => {
			try {
				await deleteModelPriceRule(model);
				const payload = await refreshPriceManagement();
				await refreshAll();
				if (priceModel === model) {
					const remainsObserved = payload.observedModels.some((item) => item.model === model);
					if (remainsObserved) {
						selectPriceTarget(model, payload.rules);
					} else {
						const nextTarget = payload.observedModels[0] ?? payload.rules[0];
						if (nextTarget) selectPriceTarget(nextTarget.model, payload.rules);
						else resetPriceEditor();
					}
				}
			} catch (error) {
				showNotification(error instanceof Error ? error.message : String(error), 'error');
			}
		},
		[priceModel, refreshAll, refreshPriceManagement, resetPriceEditor, selectPriceTarget, showNotification]
	);

	const handleSyncPrices = useCallback(async (dryRun = false) => {
		setIsPriceSyncing(true);
		setPriceSyncResult(null);
		setPriceSyncChangeFilter('all');
		if (dryRun) setPriceSyncLockedOverrides([]);
		try {
			const result = await syncModelPricesFromModelsDev(dryRun, dryRun ? [] : priceSyncLockedOverrides);
			setPriceSyncResult(result);
			if (!dryRun) setPriceSyncLockedOverrides([]);
			if (!dryRun) {
				const payload = await refreshPriceManagement();
				if (priceModel) selectPriceTarget(priceModel, payload.rules);
				await refreshAll();
			}
			showNotification(t(dryRun ? 'usage_stats.model_price_sync_preview_complete' : 'usage_stats.model_price_sync_complete', {
				added: result.added,
				updated: result.updated,
				overridden: result.overridden,
				locked: result.locked,
				unmatched: result.unmatched.length,
			}), 'success');
		} catch (error) {
			showNotification(error instanceof Error ? error.message : String(error), 'error');
		} finally {
			setIsPriceSyncing(false);
		}
	}, [priceModel, priceSyncLockedOverrides, refreshAll, refreshPriceManagement, selectPriceTarget, showNotification, t]);

  return (
    <div className={styles.page}>
      <section className={styles.masthead}>
        <div className={styles.mastheadGlow} aria-hidden="true" />

        <div className={styles.mastheadCopy}>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{t('monitoring.title')}</h1>
            <div className={styles.titleActions}>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={() => void handleExportUsage()}
              >
                {t('usage_stats.export')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={handleImportUsageClick}
                disabled={isImportingUsage}
              >
                {isImportingUsage ? t('common.loading') : t('usage_stats.import')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
				onClick={() => void openPriceManagement()}
              >
                {t('usage_stats.model_price_settings')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={() => void loadMonitoringSettings()}
                disabled={isMonitoringSettingsLoading}
                aria-busy={isMonitoringSettingsLoading}
              >
                {t('usage_stats.monitoring_settings')}
              </button>
              <input
                ref={importInputRef}
                type="file"
                accept=".jsonl,.ndjson,.json,application/x-ndjson,application/json"
                className={styles.hiddenFileInput}
                onChange={handleImportUsageFile}
              />
            </div>
          </div>
          <p className={styles.subtitle}>{t('monitoring.console_subtitle')}</p>

          <div className={styles.usageStatsHero}>
            <TopUsageStats cards={usageMetricCards} />
          </div>
        </div>
      </section>

      {!isUsageTrendHidden ? (
        <section className={styles.usageTrendSection}>
          <UsageTrendHeader
            range={timeRange}
            totalCalls={usageTrendAnalytics.scopedTotals.requests}
            apiKeyFilter={usageTrendApiKey}
            apiKeyOptions={usageTrendApiKeyOptions}
            onRangeChange={setTimeRange}
            onApiKeyFilterChange={setUsageTrendApiKey}
            onHide={() => setIsUsageTrendHidden(true)}
            t={t}
          />
          <div className={styles.usageTrendInsightsGrid}>
            <UsageTrendPanel
              points={usageTrendPoints}
              hasPrices={hasPrices}
              emptyText={t('monitoring.no_data')}
              t={t}
            />
            <ApiKeyRankingPanel
              title={t('monitoring.api_key_ranking_title')}
              subtitle={t('monitoring.api_key_ranking_desc')}
              rows={apiKeyRankingRows}
              metric={apiKeyRankingMetric}
              metricTotal={apiKeyRankingMetricTotal}
              onMetricChange={setApiKeyRankingMetric}
              emptyText={t('monitoring.no_data')}
              hasPrices={hasPrices}
              t={t}
            />
          </div>
          <div className={styles.rankingGrid}>
            <ModelStatsPanel
              title={t('monitoring.model_stats_title')}
              subtitle={t('monitoring.model_stats_desc')}
              rows={modelRankingRows}
              metric={modelRankingMetric}
              metricTotal={modelRankingMetricTotal}
              onMetricChange={setModelRankingMetric}
              emptyText={t('monitoring.no_data')}
              hasPrices={hasPrices}
              t={t}
            />
            <TokenDistributionPanel
              points={tokenDistributionPoints}
              emptyText={t('monitoring.no_data')}
              hasPrices={hasPrices}
              t={t}
            />
          </div>
        </section>
      ) : (
        <section className={styles.usageTrendCollapsed}>
          <div>
            <h2>{t('monitoring.usage_stats_title')}</h2>
            <p>{t('monitoring.analysis_hidden_desc')}</p>
          </div>
          <button type="button" className={styles.usageTrendHideButton} onClick={() => setIsUsageTrendHidden(false)}>
            {t('monitoring.show_analysis')}
          </button>
        </section>
      )}

      {!isAccountStatsHidden ? (
        <section className={styles.usageTrendSection}>
          <AccountStatsPanel
            rows={accountStatsRows}
            metric={accountStatsMetric}
            emptyText={t('monitoring.no_data')}
            hasPrices={hasPrices}
            locale={i18n.language}
            t={t}
            rangeLabel={timeRangeLabel}
            range={timeRange}
            onRangeChange={setTimeRange}
            onHide={() => setIsAccountStatsHidden(true)}
            expandedAccounts={expandedAccounts}
            accountQuotaStates={accountQuotaStates}
            accountQuotaEntriesByAccount={accountQuotaEntriesByAccount}
            onMetricChange={setAccountStatsMetric}
            onToggleAccount={toggleAccountExpanded}
            onRefreshQuota={(account) => void loadAccountQuota(account, true)}
          />
        </section>
      ) : (
        <section className={styles.usageTrendCollapsed}>
          <div>
            <h2>{t('monitoring.account_stats_title')}</h2>
            <p>{t('monitoring.account_stats_hidden_desc')}</p>
          </div>
          <button type="button" className={styles.usageTrendHideButton} onClick={() => setIsAccountStatsHidden(false)}>
            {t('monitoring.show_account_stats')}
          </button>
        </section>
      )}

      <section className={styles.usageTrendSection}>
        <div className={styles.usageTrendHeader}>
          <div className={styles.usageTrendCopy}>
            <h2>{t('monitoring.analysis_tab_logs')}</h2>
            <p>
              {selectedFiltersCount > 0
                ? t('monitoring.active_filters_hint', { count: selectedFiltersCount, rows: realtimeLogMatchedTotal })
                : t('monitoring.realtime_table_desc')}
            </p>
          </div>
          <div className={styles.usageTrendActions}>
            <div className={`${styles.rankingMetricSwitch} ${styles.timeRangeControl}`}>
              {TIME_RANGE_OPTIONS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  className={`${styles.rankingMetricButton} ${styles.timeRangeButton} ${timeRange === option.value ? styles.rankingMetricButtonActive : ''}`}
                  onClick={() => setTimeRange(option.value)}
                >
                  {t(option.labelKey)}
                </button>
              ))}
            </div>
          </div>
        </div>

        <Card className={styles.realtimePanel}>
        <div className={styles.filterGrid}>
          <Input
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            placeholder={t('monitoring.search_placeholder')}
            className={styles.toolbarHeaderSearchInput}
            rightElement={<IconSearch size={16} />}
            aria-label={t('monitoring.search_placeholder')}
          />
          <Select
            value={selectedApiKey}
            options={apiKeyOptions}
            onChange={setSelectedApiKey}
            ariaLabel={t('monitoring.filter_api_key')}
          />
          <Select
            value={selectedProvider}
            options={providerOptions}
            onChange={setSelectedProvider}
            ariaLabel={t('monitoring.filter_provider')}
          />
          <Select
            value={selectedModel}
            options={modelOptions}
            onChange={setSelectedModel}
            ariaLabel={t('monitoring.filter_model')}
          />
          <Select
            value={selectedStatus}
            options={statusOptions}
            onChange={(value) => setSelectedStatus(value as StatusFilter)}
            ariaLabel={t('monitoring.filter_status')}
          />
          <button type="button" className={styles.clearButton} onClick={clearFilters}>
            <IconSlidersHorizontal size={16} />
            <span>{t('monitoring.clear_filters')}</span>
          </button>
          <div className={styles.realtimeColumnsMenu} ref={realtimeColumnsMenuRef}>
            <button
              type="button"
              className={styles.clearButton}
              onClick={() => setIsRealtimeColumnsMenuOpen((open) => !open)}
              aria-expanded={isRealtimeColumnsMenuOpen}
            >
              <IconSlidersHorizontal size={16} />
              <span>{t('monitoring.realtime_columns_title')}</span>
            </button>
            {isRealtimeColumnsMenuOpen ? (
              <div className={styles.realtimeColumnsDropdown}>
                <div className={styles.realtimeColumnsDropdownHeader}>
                  <span>{t('monitoring.realtime_columns_hint')}</span>
                  <button type="button" className={styles.inlineActionButton} onClick={resetRealtimeLogColumns}>
                    {t('monitoring.realtime_columns_reset')}
                  </button>
                </div>
                <div className={styles.realtimeColumnsDropdownList}>
                  {realtimeLogColumns.map((column) => {
                    const definition = realtimeLogColumnDefinitions[column.key];
                    return (
                      <label key={column.key} className={styles.realtimeColumnToggle}>
                        <input
                          type="checkbox"
                          checked={column.visible}
                          disabled={column.visible && realtimeLogVisiblePreferenceCount <= 1}
                          onChange={() => toggleRealtimeLogColumn(column.key)}
                        />
                        <span>{definition.label}</span>
                      </label>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </div>
        </div>

        {combinedError ? <div className={styles.errorBox}>{combinedError}</div> : null}

        <div className={styles.realtimeLogStatusRow}>
          <div className={styles.inlineMetrics}>
            <span>{`${t('monitoring.log_rows')}: ${realtimeLogTotalCount}`}</span>
            <span>{`${t('monitoring.recent_failures')}: ${scopedFailureCount}`}</span>
            {realtimeLogMatchedTotal > 0 ? (
              <span>
                {t('monitoring.request_events_page_source_hint', {
                  from: realtimeLogPagination.from,
                  to: realtimeLogPagination.to,
                  total: realtimeLogMatchedTotal,
                  defaultValue: `Showing ${realtimeLogPagination.from}-${realtimeLogPagination.to} of ${realtimeLogMatchedTotal} matching events from a stable snapshot.`,
                })}
              </span>
            ) : null}
          </div>
          <label className={styles.realtimeFollowToggle} title={t('monitoring.request_events_live_follow_hint')}>
            <input
              type="checkbox"
              role="switch"
              checked={realtimeLogFollowEnabled}
              onChange={(event) => setRealtimeLogFollowEnabled(event.target.checked)}
            />
            <span className={styles.realtimeFollowTrack} aria-hidden="true"><span /></span>
            <span className={styles.realtimeFollowLabel}>{t('monitoring.request_events_live_follow')}</span>
          </label>
        </div>

        <div className={styles.realtimeTableShell}>
          {pendingRealtimeEventCount > 0 && realtimeLogAutoRefreshPaused ? (
            <div className={styles.realtimeUpdateBar} role="status" aria-live="polite">
              <div className={styles.realtimeUpdateCopy}>
                <strong>
                  {t('monitoring.request_events_new_available', {
                    count: pendingRealtimeEventCount,
                    defaultValue: `${pendingRealtimeEventCount} new events available`,
                  })}
                </strong>
                <span>{t('monitoring.request_events_paused_hint')}</span>
              </div>
              <button
                type="button"
                className={styles.inlineActionButton}
                onClick={() => void refreshRealtimeLogs('top')}
                disabled={realtimeLogLoading}
              >
                {t('monitoring.request_events_view_latest')}
              </button>
            </div>
          ) : null}

          <div
            ref={realtimeLogWrapperRef}
            className={`${styles.tableWrapper} ${styles.tableScrollWrapper} ${styles.realtimeTableWrapper}`}
            onScroll={handleRealtimeLogScroll}
            aria-busy={realtimeLogLoading}
          >
            <table
              className={`${styles.table} ${styles.realtimeTable}`}
              style={{ '--realtime-table-min-width': `${realtimeLogTableMinWidth}px` } as CSSProperties}
            >
              <colgroup>
                {visibleRealtimeLogColumns.map((column) => (
                  <col key={column.key} className={column.colClassName} style={{ width: `${column.width}px` }} />
                ))}
              </colgroup>
              <thead>
                <tr>
                  {visibleRealtimeLogColumns.map((column) => (
                    <th
                      key={column.key}
                      className={[
                        styles.realtimeDraggableHeader,
                        column.key === 'time' ? styles.realtimeFixedHeader : '',
                        column.headerClassName,
                        draggedRealtimeLogColumnKey === column.key ? styles.realtimeDraggableHeaderActive : '',
                      ].filter(Boolean).join(' ')}
                      draggable={column.key !== 'time'}
                      scope="col"
                      onDragStart={(event) => handleRealtimeLogHeaderDragStart(event, column.key)}
                      onDragOver={handleRealtimeLogHeaderDragOver}
                      onDrop={(event) => handleRealtimeLogHeaderDrop(event, column.key)}
                      onDragEnd={handleRealtimeLogHeaderDragEnd}
                    >
                      <span className={styles.realtimeHeaderContent}>{column.label}</span>
                      <span
                        className={styles.realtimeColumnResizeHandle}
                        role="separator"
                        aria-label={t('monitoring.realtime_column_resize', { column: column.label })}
                        onMouseDown={(event) => startRealtimeLogColumnResize(event, column.key)}
                      />
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {realtimeLogPageRows.map((row) => (
                  <tr
                    key={row.id}
                    data-realtime-row-id={row.id}
                    className={row.failed ? styles.logRowFailed : undefined}
                  >
                    {visibleRealtimeLogColumns.map((column) => (
                      <td key={column.key} className={column.cellClassName?.(row)}>
                        {column.render(row)}
                      </td>
                    ))}
                  </tr>
                ))}
                {realtimeLogPageRows.length === 0 ? (
                  <tr>
                    <td colSpan={realtimeLogVisibleColumnCount}>
                      <div className={styles.emptyTable}>
                        {monitoringLoading ? t('common.loading') : deferredSearch.trim() ? t('monitoring.no_filtered_data') : t('monitoring.no_data')}
                      </div>
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </div>
        {realtimeLogPagination.totalPages > 1 ? (
          <div className={quotaStyles.pagination}>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => void showPreviousRealtimeLogPage()}
              disabled={realtimeLogLoading || !realtimeLogPagination.hasPrevious}
              aria-label={t('monitoring.previous_page')}
            >
              {t('monitoring.previous_page')}
            </Button>
            <div className={quotaStyles.pageInfo}>
              {t('monitoring.pagination_info', {
                from: realtimeLogPagination.from,
                to: realtimeLogPagination.to,
                total: realtimeLogPagination.total,
                page: realtimeLogPagination.page,
                totalPages: realtimeLogPagination.totalPages,
                defaultValue: `${realtimeLogPagination.from}-${realtimeLogPagination.to} / ${realtimeLogPagination.total}`,
              })}
            </div>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => void showNextRealtimeLogPage()}
              disabled={realtimeLogLoading || !realtimeLogNextCursor || !realtimeLogPagination.hasNext}
              aria-label={t('monitoring.next_page')}
            >
              {t('monitoring.next_page')}
            </Button>
          </div>
        ) : null}
        </Card>
      </section>

      <Modal
        open={Boolean(selectedRealtimeErrorRow)}
        onClose={() => setSelectedRealtimeErrorRow(null)}
        title={translateRealtimeErrorText('error_details', t, i18n.language)}
        width={720}
        className={styles.monitorModal}
        footer={selectedRealtimeErrorRow ? (
          <div className={styles.monitorModalActions}>
            <Button variant="secondary" size="sm" onClick={() => handleCopyRealtimeDiagnostic(selectedRealtimeErrorRow)}>
              {translateRealtimeErrorText('copy_diagnostic', t, i18n.language)}
            </Button>
            <Button variant="primary" size="sm" onClick={() => setSelectedRealtimeErrorRow(null)}>
              {t('common.close')}
            </Button>
          </div>
        ) : null}
      >
        {selectedRealtimeErrorRow ? (
          <RealtimeErrorDetailsPanel row={selectedRealtimeErrorRow} t={t} language={i18n.language} />
        ) : null}
      </Modal>

      <MonitoringSettingsModal
        isMonitoringSettingsOpen={isMonitoringSettingsOpen}
        setIsMonitoringSettingsOpen={setIsMonitoringSettingsOpen}
        monitoringSettingsDraft={monitoringSettingsDraft}
        setMonitoringSettingsDraft={setMonitoringSettingsDraft}
        usageTotalRequests={Number(usage?.total_requests) || 0}
        isMonitoringStatisticsResetting={isMonitoringStatisticsResetting}
        isMonitoringSettingsSaving={isMonitoringSettingsSaving}
        handleMonitoringStatisticsReset={handleMonitoringStatisticsReset}
        handleSaveMonitoringSettings={handleSaveMonitoringSettings}
        t={t}
      />

      <ModelPriceManagerModal
        isPriceModalOpen={isPriceModalOpen}
        setIsPriceModalOpen={setIsPriceModalOpen}
        priceManagementView={priceManagementView}
        setPriceManagementView={setPriceManagementView}
        priceRuleTargets={priceRuleTargets}
        priceRuleSearch={priceRuleSearch}
        setPriceRuleSearch={setPriceRuleSearch}
        priceModel={priceModel}
        selectPriceTarget={selectPriceTarget}
        isPriceLoading={isPriceLoading}
        priceDraft={priceDraft}
        setPriceDraft={setPriceDraft}
        handlePriceDraftChange={handlePriceDraftChange}
        handlePriceTierChange={handlePriceTierChange}
        addPriceTier={addPriceTier}
        removePriceTier={removePriceTier}
        handleDeletePrice={handleDeletePrice}
        handleSavePrice={handleSavePrice}
        isPriceSaving={isPriceSaving}
        priceSyncState={priceSyncState}
        priceSyncResult={priceSyncResult}
        isPriceSyncing={isPriceSyncing}
        handleSyncPrices={handleSyncPrices}
        priceSyncLockedOverrides={priceSyncLockedOverrides}
        setPriceSyncLockedOverrides={setPriceSyncLockedOverrides}
        priceSyncChangeFilter={priceSyncChangeFilter}
        setPriceSyncChangeFilter={setPriceSyncChangeFilter}
        monitoringSettingsDraft={monitoringSettingsDraft}
        setMonitoringSettingsDraft={setMonitoringSettingsDraft}
        handleSaveMonitoringSettings={handleSaveMonitoringSettings}
        isMonitoringSettingsLoading={isMonitoringSettingsLoading}
        isMonitoringSettingsSaving={isMonitoringSettingsSaving}
        t={t}
      />
    </div>
  );
}
