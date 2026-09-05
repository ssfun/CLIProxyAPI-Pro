import { useMonitoringAnalytics } from './features/hooks/useMonitoringAnalytics';
import { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  IconSearch,
  IconSlidersHorizontal,
} from '@/components/ui/icons';
import {
  buildLocalDayKey,
  useMonitoringEventRows,
  type MonitoringEventRow,
} from '@/pro/modules/monitoring/features/hooks/useMonitoringData';
import { useRealtimeLogData } from '@/pro/modules/monitoring/features/hooks/useRealtimeLogData';
import { useUsageData, type UsageEventPageFilters, type UsagePayload } from '@/pro/modules/monitoring/features/hooks/useUsageData';
import { useUsageAggregates, type UsageAggregateBucket } from '@/pro/modules/monitoring/features/hooks/useUsageAggregates';
import { findMonitoringAuthIndexes } from '@/pro/modules/monitoring/features/monitoringAuthSearch';
import {
  buildProfileFilterOptions,
  resolveUsageProfileSnapshot,
} from '@/pro/modules/monitoring/features/profileUsage';
import { ModelPriceManagerModal } from '@/pro/modules/monitoring/features/components/ModelPriceManagerModal';
import {
  RealtimeRequestDetailsPanel,
  RecentPattern,
  StatusBadge,
} from '@/pro/modules/monitoring/features/components/RealtimeLogDetails';
import {
  RealtimeCostCell,
} from '@/pro/modules/monitoring/features/components/RealtimeCostCell';
import {
  ApiKeyRankingPanel,
  ModelStatsPanel,
  TokenDistributionPanel,
  TopUsageStats,
  UsageTrendHeader,
  UsageTrendPanel,
  type UsageMetricCard,
} from '@/pro/modules/monitoring/features/components/UsageAnalyticsPanels';
import { buildAggregateSummary } from '@/pro/modules/monitoring/features/monitoringAggregates';
import {
  resolveAccountPlanLabel,
  type AccountPlanQuotaStore,
} from '@/pro/modules/quota';
import {
  formatPercent,
  getRankingMetricValue,
  hasCompleteUsageAnalyticsSource,
  type RankingMetric,
} from '@/pro/modules/monitoring/features/monitoringAnalytics';
import {
  DEFAULT_TIME_RANGE,
  TimeRangeSelector,
  createCustomTimeRange,
  getTimeRangeKey,
  resolveTimeRange,
  type TimeRangeSelection,
} from '@/pro/modules/monitoring/features/timeRange';
import {
  buildMonitoringSettingsFromDraft,
  createMonitoringSettingsDraft,
  type MonitoringSettings,
  type MonitoringSettingsDraft,
} from '@/pro/modules/monitoring/features/monitoringSettings';
import {
  buildModelPriceRule,
  createSpeedDraft,
  createServiceTierDraft,
  createPriceDraft,
  formatDeltaPercent,
  validatePriceDraft,
  type PriceDraft,
  type PriceManagementView,
  type PriceRateDraft,
  type PriceRuleTarget,
  type ServiceTierDraft,
  type SpeedDraft,
  type PriceSyncChangeFilter,
  type PriceTierDraft,
} from '@/pro/modules/monitoring/features/modelPricePresentation';
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
} from '@/pro/modules/monitoring/features/realtimeLogPreferences';
import {
  buildRealtimeDiagnosticClipboardText,
  buildRealtimeLogPageRows,
  buildRealtimeMetaText,
  buildRealtimeStatusLabel,
  getClientPaginationRange,
  translateRealtimeErrorText,
  type RealtimeLogRow,
} from '@/pro/modules/monitoring/features/realtimeLogPresentation';
import { apiKeyPolicyApi } from '@/pro/modules/apiKeyPolicy';
import { readMonitoringUsageLocationState } from '@/pro/shared/monitoringNavigation';
import {
  DEFAULT_PRO_PAGE_SIZE,
  PRO_PAGE_SIZE_OPTIONS,
  normalizeProPageSize,
  resolveProPaginationCopy,
} from '@/pro/shared/pagination';
import { ProDetailDialog } from '@/pro/shared/ProSurface';
import { useProSurfaceState } from '@/pro/shared/useProSurfaceState';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { apiClient } from '@/services/api/client';
import { useAuthStore, useConfigStore, useNotificationStore, useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { maskSensitiveText } from '@/utils/format';
import {
  deleteModelPriceRule,
  formatCompactNumber,
  formatDurationMs,
  formatUsd,
  formatUsdPrecise,
  loadModelPriceRules,
  loadModelPriceSyncState,
  recalculateModelPriceHistory,
  saveModelPriceRule,
  syncModelPricesFromModelsDev,
  type ModelPriceRule,
  type ModelPriceSyncResult,
  type ModelPriceSyncState,
  type ObservedModelPriceTarget,
  normalizeAuthIndex,
} from '@/pro/modules/monitoring/features/usage';
import quotaStyles from '@/features/quota/QuotaPage.module.scss';
import styles from '@/pro/modules/monitoring/features/monitoring.module.scss';

type StatusFilter = 'all' | 'success' | 'failed';
type LinkedRequestLogScope = { authIndex: string; fromMs: number; toMs: number };
const PROFILE_CATALOG_REFRESH_MS = 30_000;

type RealtimeLogDisplayRow = RealtimeLogRow & { accountPlan: string };

type RealtimeLogColumnDefinition = {
  key: RealtimeLogColumnKey;
  label: string;
  colClassName: string;
  headerClassName?: string;
  cellClassName?: (row: RealtimeLogDisplayRow) => string | undefined;
  render: (row: RealtimeLogDisplayRow) => ReactNode;
  width: number;
};
const formatTokenCount = (value: number) => Math.max(0, Math.round(Number(value) || 0)).toLocaleString();

const getCacheHitRate = (row: Pick<MonitoringEventRow, 'cacheInputTokens' | 'cachedTokens'>): number | null => (
  row.cacheInputTokens > 0 ? Math.min(Math.max(row.cachedTokens / row.cacheInputTokens, 0), 1) : null
);

const getSuccessRateClassName = (rate: number) => (
  rate >= 0.95 ? styles.goodText : rate >= 0.85 ? styles.warnText : styles.badText
);

const getRealtimeLogColumnContentTexts = (key: RealtimeLogColumnKey, row: RealtimeLogDisplayRow) => {
  switch (key) {
    case 'type':
      return [
        row.accountPlan === '-' ? row.provider : `${row.provider} · ${row.accountPlan}`,
        row.account || row.authLabel || row.accountMasked || '-',
      ];
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
    case 'latency':
      return [
        `First ${formatDurationMs(row.ttftMs)}`,
        `Total ${formatDurationMs(row.latencyMs)}`,
      ];
    case 'tokens':
      return [
        formatTokenCount(row.totalTokens),
        `I ${formatTokenCount(row.inputTokens)} O ${formatTokenCount(row.outputTokens)}`,
        row.reasoningTokens > 0 ? `R ${formatTokenCount(row.reasoningTokens)}` : '',
      ];
    case 'cacheRead':
      return [
        formatTokenCount(row.cachedTokens),
        row.cacheInputTokens > 0 ? formatPercent(Math.min(row.cachedTokens / row.cacheInputTokens, 1)) : '--',
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
  rows: RealtimeLogDisplayRow[]
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

export function MonitoringCenterPage() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const config = useConfigStore((state) => state.config);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const claudeQuota = useQuotaStore((state) => state.claudeQuota);
  const codexQuota = useQuotaStore((state) => state.codexQuota);
  const geminiCliQuota = useQuotaStore((state) => state.geminiCliQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);
  const [timeRange, setTimeRange] = useState<TimeRangeSelection>(DEFAULT_TIME_RANGE);
  const [searchInput, setSearchInput] = useState('');
  const [selectedProvider, setSelectedProvider] = useState('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [selectedApiKey, setSelectedApiKey] = useState('all');
  const [selectedApiKeyFallbackLabel, setSelectedApiKeyFallbackLabel] = useState('');
  const [selectedProfile, setSelectedProfile] = useState('all');
  const [selectedProfileFallbackName, setSelectedProfileFallbackName] = useState('');
  const [currentProfileCatalog, setCurrentProfileCatalog] = useState<{
    loaded: boolean;
    names: Map<string, string>;
  }>({ loaded: false, names: new Map() });
  const [selectedStatus, setSelectedStatus] = useState<StatusFilter>('all');
  const [linkedRequestLogScope, setLinkedRequestLogScope] = useState<LinkedRequestLogScope | null>(null);
  const [selectedRealtimeErrorRow, setSelectedRealtimeErrorRowState] = useState<RealtimeLogRow | null>(null);
  const { activeSurface, openSurface, closeSurface } = useProSurfaceState<'realtime-detail' | 'price-management'>();
  const setSelectedRealtimeErrorRow = useCallback((row: RealtimeLogRow | null) => {
    if (row) {
      setSelectedRealtimeErrorRowState(row);
      openSurface('realtime-detail');
    } else if (activeSurface === 'realtime-detail') {
      closeSurface();
    }
  }, [activeSurface, closeSurface, openSurface]);
  const isPriceModalOpen = activeSurface === 'price-management';
  const setIsPriceModalOpen = useCallback((open: boolean) => {
    if (open) openSurface('price-management');
    else if (activeSurface === 'price-management') closeSurface();
  }, [activeSurface, closeSurface, openSurface]);
  const [isMonitoringSettingsLoading, setIsMonitoringSettingsLoading] = useState(false);
  const [isMonitoringSettingsSaving, setIsMonitoringSettingsSaving] = useState(false);
  const [monitoringSettingsDraft, setMonitoringSettingsDraft] = useState<MonitoringSettingsDraft>(() => createMonitoringSettingsDraft());
  const [savedMonitoringSettingsDraft, setSavedMonitoringSettingsDraft] = useState<MonitoringSettingsDraft>(() => createMonitoringSettingsDraft());
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
  const priceManagementRequestRef = useRef<Promise<void> | null>(null);
  const profileCatalogRequestRef = useRef<Promise<void> | null>(null);
  const profileCatalogFetchedAtRef = useRef(0);
  const profileCatalogGenerationRef = useRef<number | null>(null);
  const [isUsageTrendHidden, setIsUsageTrendHidden] = useState(false);
  const [modelRankingMetric, setModelRankingMetric] = useState<RankingMetric>('requests');
  const [apiKeyRankingMetric, setApiKeyRankingMetric] = useState<RankingMetric>('requests');
  const [usageTrendApiKey, setUsageTrendApiKey] = useState('all');
  const [realtimeLogUsage, setRealtimeLogUsage] = useState<UsagePayload | null>(null);
  const [realtimeLogPageSize, setRealtimeLogPageSize] = useState(DEFAULT_PRO_PAGE_SIZE);
  const [realtimeLogColumns, setRealtimeLogColumns] = useState<RealtimeLogColumnPreference[]>(loadRealtimeLogColumns);
  const [realtimeLogFollowEnabled, setRealtimeLogFollowEnabled] = useState(loadRealtimeLogFollowEnabled);
  const [draggedRealtimeLogColumnKey, setDraggedRealtimeLogColumnKey] = useState<RealtimeLogColumnKey | null>(null);
  const [isRealtimeColumnsMenuOpen, setIsRealtimeColumnsMenuOpen] = useState(false);
  const realtimeColumnsMenuRef = useRef<HTMLDivElement | null>(null);
  const deferredSearchInput = useDeferredValue(searchInput);
  const [deferredSearch, setDeferredSearch] = useState(searchInput);
  const paginationCopy = resolveProPaginationCopy(i18n.resolvedLanguage ?? i18n.language);
  const timeRangeKey = getTimeRangeKey(timeRange);
  const handleTimeRangeChange = useCallback((nextRange: TimeRangeSelection) => {
    setLinkedRequestLogScope(null);
    setTimeRange(nextRange);
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const authIndex = params.get('auth_index')?.trim() ?? '';
    const linkedUsage = readMonitoringUsageLocationState(location.state);
    const profileId = linkedUsage?.profileId ?? params.get('profile_id')?.trim() ?? '';
    const fromMs = Number(params.get('from_ms'));
    const toMs = Number(params.get('to_ms'));
    if (linkedUsage || profileId) {
      setSelectedApiKey(linkedUsage?.apiKeyHash || 'all');
      setSelectedApiKeyFallbackLabel(linkedUsage?.apiKeyLabel || '');
      setSelectedProfile(profileId || 'all');
      setSelectedProfileFallbackName(linkedUsage?.profileName || '');
      setLinkedRequestLogScope(null);
      window.requestAnimationFrame(() => {
        document.getElementById('request-events')?.scrollIntoView({ block: 'start' });
      });
      return;
    }
    if (!authIndex || !Number.isFinite(fromMs) || !Number.isFinite(toMs) || fromMs < 0 || toMs <= 0 || fromMs > toMs) {
      setLinkedRequestLogScope(null);
      return;
    }
    setLinkedRequestLogScope({ authIndex, fromMs, toMs });
    const linkedTimeRange = createCustomTimeRange(fromMs, toMs);
    if (linkedTimeRange) setTimeRange(linkedTimeRange);
    setSearchInput(authIndex);
    window.requestAnimationFrame(() => {
      document.getElementById('request-events')?.scrollIntoView({ block: 'start' });
    });
  }, [location.search, location.state]);

  useEffect(() => {
    let cancelled = false;
    if (connectionStatus !== 'connected') {
      profileCatalogRequestRef.current = null;
      profileCatalogFetchedAtRef.current = 0;
      profileCatalogGenerationRef.current = null;
      setCurrentProfileCatalog({ loaded: false, names: new Map() });
      return () => {
        cancelled = true;
      };
    }
    const refreshProfileCatalog = (force = false): Promise<void> => {
      const fetchedAt = profileCatalogFetchedAtRef.current;
      if (!force && fetchedAt > 0 && Date.now() - fetchedAt < PROFILE_CATALOG_REFRESH_MS) {
        return Promise.resolve();
      }
      if (profileCatalogRequestRef.current) return profileCatalogRequestRef.current;
      const request = apiKeyPolicyApi.profileCatalog()
        .then((catalog) => {
          if (cancelled) return;
          profileCatalogFetchedAtRef.current = Date.now();
          if (profileCatalogGenerationRef.current === catalog.policyGeneration) return;
          profileCatalogGenerationRef.current = catalog.policyGeneration;
          setCurrentProfileCatalog({
            loaded: true,
            names: new Map(catalog.items.map((profile) => [profile.id, profile.name])),
          });
        })
        .catch(() => {
          if (!cancelled) {
            setCurrentProfileCatalog((current) => current.loaded
              ? current
              : { loaded: false, names: new Map() });
          }
        });
      profileCatalogRequestRef.current = request;
      void request.then(() => {
        if (profileCatalogRequestRef.current === request) {
          profileCatalogRequestRef.current = null;
        }
      });
      return request;
    };
    const refreshWhenVisible = () => {
      if (document.visibilityState === 'visible') void refreshProfileCatalog();
    };
    void refreshProfileCatalog(true);
    const interval = window.setInterval(refreshWhenVisible, PROFILE_CATALOG_REFRESH_MS);
    const refreshWhenFocused = () => void refreshProfileCatalog();
    window.addEventListener('focus', refreshWhenFocused);
    document.addEventListener('visibilitychange', refreshWhenVisible);
    return () => {
      cancelled = true;
      window.clearInterval(interval);
      window.removeEventListener('focus', refreshWhenFocused);
      document.removeEventListener('visibilitychange', refreshWhenVisible);
    };
  }, [connectionStatus]);

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
  } = useMonitoringEventRows({
    usage: deferredUsage,
    logUsage: realtimeLogUsage,
    config,
    modelPrices,
    deletedCredentialLabel: t('monitoring.deleted_credential'),
    unattributedApiKeyLabel: t('monitoring.api_key_unattributed'),
  });

  const {
    data: usageAggregates,
    loading: usageAggregatesLoading,
    refreshing: usageAggregatesRefreshing,
    error: usageAggregatesError,
    refresh: refreshAggregates,
  } = useUsageAggregates({
    latestId,
    generation: Number(usage?.generation) || 0,
    timeRange,
    apiKeyHash: usageTrendApiKey,
    enabled: connectionStatus === 'connected',
  });

  const searchMatchedAuthIndexFilter = useMemo(() => {
    return findMonitoringAuthIndexes(authFiles, allRows, deferredSearch);
  }, [allRows, authFiles, deferredSearch]);

  const buildRealtimeLogFilters = useCallback((): UsageEventPageFilters => {
    const nowMs = Date.now();
    const resolvedRange = resolveTimeRange(timeRange, nowMs);
    const fromMs = linkedRequestLogScope?.fromMs ?? resolvedRange.fromMs;
    return {
      fromMs: Number.isFinite(fromMs) && fromMs > 0 ? fromMs : undefined,
      toMs: linkedRequestLogScope?.toMs ?? resolvedRange.toMs,
      provider: selectedProvider === 'all' ? undefined : selectedProvider,
      model: selectedModel === 'all' ? undefined : selectedModel,
      authIndex: linkedRequestLogScope?.authIndex,
      searchAuthIndexes: linkedRequestLogScope ? undefined : (searchMatchedAuthIndexFilter || undefined),
      apiKeyHash: selectedApiKey === 'all' ? undefined : selectedApiKey,
      profileId: selectedProfile === 'all' ? undefined : selectedProfile,
      status: selectedStatus,
      search: linkedRequestLogScope ? undefined : deferredSearch,
      limit: realtimeLogPageSize,
    };
  }, [deferredSearch, linkedRequestLogScope, realtimeLogPageSize, searchMatchedAuthIndexFilter, selectedApiKey, selectedModel, selectedProfile, selectedProvider, selectedStatus, timeRange]);

  const handleRealtimeLogGenerationChange = useCallback(() => {
    setSelectedRealtimeErrorRow(null);
    void refreshAggregates();
  }, [refreshAggregates, setSelectedRealtimeErrorRow]);

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
    const nextDraft = createMonitoringSettingsDraft(response.settings);
    setMonitoringSettingsDraft(nextDraft);
    setSavedMonitoringSettingsDraft(nextDraft);
    return response.settings;
  }, []);

  const handleSaveMonitoringSettings = useCallback(async () => {
    const settings = buildMonitoringSettingsFromDraft(monitoringSettingsDraft);
    const expectedSettings = buildMonitoringSettingsFromDraft(savedMonitoringSettingsDraft);
    setIsMonitoringSettingsSaving(true);
    try {
      const response = await apiClient.put<{ settings: MonitoringSettings }>('/usage/settings', {
        settings,
        expectedSettings,
        sections: ['modelPriceSync'],
      });
      const nextDraft = createMonitoringSettingsDraft(response.settings);
      setMonitoringSettingsDraft(nextDraft);
      setSavedMonitoringSettingsDraft(nextDraft);
      showNotification(t('usage_stats.monitoring_settings_saved'), 'success');
      await refreshAll();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error || t('common.unknown_error')), 'error');
    } finally {
      setIsMonitoringSettingsSaving(false);
    }
  }, [monitoringSettingsDraft, refreshAll, savedMonitoringSettingsDraft, showNotification, t]);

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
    if (selectedApiKey !== 'all') {
      if (selectedApiKeyFallbackLabel) {
        apiKeys.set(selectedApiKey, selectedApiKeyFallbackLabel);
      } else if (!apiKeys.has(selectedApiKey)) {
        apiKeys.set(selectedApiKey, maskSensitiveText(selectedApiKey));
      }
    }

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
  }, [allRows, selectedApiKey, selectedApiKeyFallbackLabel, t, usageAggregates]);
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

  const profileFilterObservations = useMemo(
    () => [...allRows, ...filteredRows],
    [allRows, filteredRows],
  );
  const profileFilterOptions = useMemo(() => buildProfileFilterOptions({
    observations: profileFilterObservations,
    currentNames: currentProfileCatalog.names,
    currentNamesLoaded: currentProfileCatalog.loaded,
    selectedProfileId: selectedProfile,
    selectedProfileName: selectedProfileFallbackName,
    copy: {
      allProfiles: t('monitoring.filter_all_profiles'),
      deleted: (name) => t('monitoring.profile_filter_deleted', { name }),
    },
  }), [currentProfileCatalog, profileFilterObservations, selectedProfile, selectedProfileFallbackName, t]);

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

  const scopedRowsState = useMemo(() => ({
    rows: requestLogRows,
    failureCount: requestLogRows.filter((row) => row.failed).length,
  }), [requestLogRows]);
  const scopedRows = scopedRowsState.rows;
  const scopedFailureCount = scopedRowsState.failureCount;

  const { aggregateTrendScopeMatches, topSummary, todaySummary, yesterdayCost, clientUsageTrendAnalytics, serverUsageTrendAnalytics } = useMonitoringAnalytics({
    allRows, usageAggregates, timeRange, timeRangeKey, usageTrendApiKey, modelPrices, apiKeyOptions,
    allKeysLabel: t('monitoring.filter_all_api_keys'), unattributedLabel: t('monitoring.api_key_unattributed'),
  });
  const usageTrendDataReady = hasCompleteUsageAnalyticsSource(
    aggregateTrendScopeMatches,
    Boolean(usage),
    usage?.details_limited === true
  );
  const currentUsageTrendAnalytics = useMemo(() => {
    if (!serverUsageTrendAnalytics || !aggregateTrendScopeMatches) {
      return clientUsageTrendAnalytics;
    }
    if (serverUsageTrendAnalytics.apiKeyRows.length > 0 || clientUsageTrendAnalytics.apiKeyRows.length === 0) {
      return serverUsageTrendAnalytics;
    }
    return {
      ...serverUsageTrendAnalytics,
      apiKeyRows: clientUsageTrendAnalytics.apiKeyRows,
    };
  }, [aggregateTrendScopeMatches, clientUsageTrendAnalytics, serverUsageTrendAnalytics]);
  const staleUsageTrendAnalytics = !aggregateTrendScopeMatches
    ? serverUsageTrendAnalytics
    : null;
  const usageTrendAnalytics = usageTrendDataReady
    ? currentUsageTrendAnalytics
    : staleUsageTrendAnalytics ?? currentUsageTrendAnalytics;
  const usageTrendHasDisplayData = usageTrendDataReady || Boolean(staleUsageTrendAnalytics);
  const usageTrendDataRefreshing = usageTrendHasDisplayData
    && (usageAggregatesRefreshing || !usageTrendDataReady);
  const usageTrendDataStale = usageTrendHasDisplayData && !usageTrendDataReady;
  const usageTrendStatusText = usageAggregatesError
    || (!usageTrendDataReady ? t('common.loading') : undefined);
  const usageTrendApiKeyOptions = usageTrendAnalytics.apiKeyOptions;
  const usageTrendPoints = usageTrendAnalytics.trendPoints;
  const tokenDistributionPoints = usageTrendAnalytics.tokenDistributionPoints;
  useEffect(() => {
    if (!usageTrendDataReady) return;
    if (usageTrendApiKey !== 'all' && !usageTrendApiKeyOptions.some((option) => option.value === usageTrendApiKey)) {
      setUsageTrendApiKey('all');
    }
  }, [usageTrendApiKey, usageTrendApiKeyOptions, usageTrendDataReady]);

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
  const realtimeLogTotalCount = realtimeLogMatchedTotal;
  const realtimeLogTotalPages = realtimeLogTotalCount > 0 ? Math.ceil(realtimeLogTotalCount / realtimeLogPageSize) : 0;
  const normalizedRealtimeLogPage = Math.min(Math.max(1, realtimeLogPage), Math.max(1, realtimeLogTotalPages));
  const authFileByAuthIndex = useMemo(() => {
    const filesByAuthIndex = new Map<string, AuthFileItem>();
    authFiles.forEach((file) => {
      const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
      if (authIndex) filesByAuthIndex.set(authIndex, file);
    });
    return filesByAuthIndex;
  }, [authFiles]);
  const accountPlanQuotaStore = useMemo<AccountPlanQuotaStore>(() => ({
    antigravityQuota,
    claudeQuota,
    codexQuota,
    geminiCliQuota,
    kimiQuota,
    xaiQuota,
  }), [antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, kimiQuota, xaiQuota]);
  const realtimeLogPageRows = useMemo(
    () => buildRealtimeLogPageRows(scopedRows, 1, realtimeLogPageSize).rows.map((row) => {
      const authFile = authFileByAuthIndex.get(row.authIndex);
      return {
        ...row,
        accountPlan: resolveAccountPlanLabel({
          authFile,
          provider: row.provider,
          fallbackPlan: row.planType,
          quotaStore: accountPlanQuotaStore,
          t,
          emptyLabel: '-',
        }),
      };
    }),
    [accountPlanQuotaStore, authFileByAuthIndex, realtimeLogPageSize, scopedRows, t]
  );
  const realtimeLogPagination = getClientPaginationRange(
    normalizedRealtimeLogPage,
    realtimeLogPageSize,
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
          <span
            className={styles.realtimeAccountTypeLine}
            title={row.accountPlan === '-' ? row.provider : `${row.provider} · ${row.accountPlan}`}
          >
            <strong>{row.provider}{row.accountPlan === '-' ? '' : ' · '}</strong>
            {row.accountPlan === '-' ? null : row.accountPlan}
          </span>
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
      render: (row) => {
        const profileSnapshot = resolveUsageProfileSnapshot(row.profileName, row.profileId, '');
        return (
          <div className={`${styles.primaryCell} ${styles.realtimeApiKeyCell}`}>
            <span className={styles.monoCell} title={row.clientApiKey.masked}>{row.clientApiKey.masked}</span>
            {profileSnapshot ? <small title={profileSnapshot}>{profileSnapshot}</small> : null}
          </div>
        );
      },
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
          <button
            type="button"
            className={styles.realtimeStatusDetailsButton}
            onClick={() => setSelectedRealtimeErrorRow(row)}
            title={translateRealtimeErrorText('request_details_click_hint', t, i18n.language)}
            aria-label={translateRealtimeErrorText('request_details_click_hint', t, i18n.language)}
          >
            {row.failed ? (
              <StatusBadge tone="bad">{buildRealtimeStatusLabel(row, t('monitoring.result_failed'))}</StatusBadge>
            ) : (
              <StatusBadge tone="good">{t('monitoring.result_success')}</StatusBadge>
            )}
          </button>
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
    latency: {
      key: 'latency',
      label: t('monitoring.column_latency'),
      colClassName: styles.realtimeLatencyCol,
      headerClassName: styles.realtimeMetricHeader,
      cellClassName: () => styles.realtimeDurationTableCell,
      width: REALTIME_LOG_COLUMN_DEFAULT_WIDTHS.latency,
      render: (row) => (
        <div className={styles.realtimeDurationCell}>
          <span>
            <small>{t('monitoring.realtime_duration_ttft')}</small>
            <strong className={
              row.ttftMs !== null && row.ttftMs >= 15000
                ? styles.badText
                : row.ttftMs !== null && row.ttftMs >= 8000
                  ? styles.warnText
                  : undefined
            }>
              {formatDurationMs(row.ttftMs, { locale: i18n.language })}
            </strong>
          </span>
          <span>
            <small>{t('monitoring.realtime_duration_total')}</small>
            <small className={
              row.latencyMs !== null && row.latencyMs >= 30000
                ? styles.badText
                : row.latencyMs !== null && row.latencyMs >= 15000
                  ? styles.warnText
                  : undefined
            }>
              {formatDurationMs(row.latencyMs, { locale: i18n.language })}
            </small>
          </span>
        </div>
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
  }), [hasPrices, i18n.language, setSelectedRealtimeErrorRow, t]);
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
    [selectedProvider, selectedModel, selectedApiKey, selectedStatus, selectedProfile].filter(
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
      value: formatPercent(effectiveTodaySummary.cacheInputTokens > 0 ? effectiveTodaySummary.cachedTokens / effectiveTodaySummary.cacheInputTokens : 0),
      accent: 'green',
      footer: [
        { label: t('monitoring.today_cached_tokens'), value: formatCompactNumber(effectiveTodaySummary.cachedTokens) },
        { label: t('monitoring.total_cache_hits'), value: `${formatCompactNumber(effectiveTopSummary.cachedTokens)} / ${formatPercent(effectiveTopSummary.cacheInputTokens > 0 ? effectiveTopSummary.cachedTokens / effectiveTopSummary.cacheInputTokens : 0)}` },
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
    setLinkedRequestLogScope(null);
    setSearchInput('');
    setSelectedProvider('all');
    setSelectedModel('all');
    setSelectedApiKey('all');
    setSelectedApiKeyFallbackLabel('');
    setSelectedStatus('all');
    setSelectedProfile('all');
    setSelectedProfileFallbackName('');
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

  const openPriceManagement = useCallback(() => {
    if (connectionStatus !== 'connected') {
      showNotification(t('notification.connection_required'), 'warning');
      return Promise.resolve();
    }
    setIsPriceModalOpen(true);
    if (priceManagementRequestRef.current) return priceManagementRequestRef.current;
    setPriceManagementView('rules');
    setPriceRuleSearch('');
    setPriceSyncLockedOverrides([]);
    setIsPriceLoading(true);
    setIsMonitoringSettingsLoading(true);
    const request = Promise.all([refreshPriceManagement(), fetchMonitoringSettings()])
      .then(([payload]) => {
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
      })
      .catch((error) => {
        showNotification(error instanceof Error ? error.message : String(error), 'error');
      })
      .finally(() => {
        priceManagementRequestRef.current = null;
        setIsPriceLoading(false);
        setIsMonitoringSettingsLoading(false);
      });
    priceManagementRequestRef.current = request;
    return request;
  }, [connectionStatus, fetchMonitoringSettings, priceModel, refreshPriceManagement, selectPriceTarget, setIsPriceModalOpen, showNotification, t]);

  const handlePriceDraftChange = useCallback((field: keyof PriceRateDraft, value: string) => {
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
			tiers: [...previous.tiers, { contextSize: '', input: '', output: '', cacheRead: '', cacheWrite: '', reasoning: '' }],
		}));
	}, []);

	const removePriceTier = useCallback((index: number) => {
		setPriceDraft((previous) => ({ ...previous, tiers: previous.tiers.filter((_, tierIndex) => tierIndex !== index) }));
	}, []);

	const handleServiceTierChange = useCallback((index: number, field: keyof ServiceTierDraft, value: string) => {
		setPriceDraft((previous) => ({
			...previous,
			serviceTiers: previous.serviceTiers.map((tier, tierIndex) => tierIndex === index ? { ...tier, [field]: value } : tier),
		}));
	}, []);

	const addServiceTier = useCallback(() => {
		setPriceDraft((previous) => ({
			...previous,
			serviceTiers: [...previous.serviceTiers, createServiceTierDraft()],
		}));
	}, []);

	const removeServiceTier = useCallback((index: number) => {
		setPriceDraft((previous) => ({
			...previous,
			serviceTiers: previous.serviceTiers.filter((_, tierIndex) => tierIndex !== index),
		}));
	}, []);

	const handleSpeedChange = useCallback((index: number, field: keyof SpeedDraft, value: string) => {
		setPriceDraft((previous) => ({
			...previous,
			speeds: previous.speeds.map((speed, speedIndex) => speedIndex === index ? { ...speed, [field]: value } : speed),
		}));
	}, []);

	const addSpeed = useCallback(() => {
		setPriceDraft((previous) => ({
			...previous,
			speeds: [...previous.speeds, createSpeedDraft()],
		}));
	}, []);

	const removeSpeed = useCallback((index: number) => {
		setPriceDraft((previous) => ({
			...previous,
			speeds: previous.speeds.filter((_, speedIndex) => speedIndex !== index),
		}));
	}, []);

	const resetPriceEditor = useCallback(() => {
		setPriceModel('');
		setPriceDraft(createPriceDraft());
	}, []);

	const handleSavePrice = useCallback(async () => {
		if (!priceModel) {
			return;
		}
		const validationError = validatePriceDraft(priceDraft);
		if (validationError) {
			showNotification(t(`usage_stats.model_price_validation_${validationError}`), 'warning');
			return;
		}
		const rule = buildModelPriceRule(priceModel, priceDraft);
		setIsPriceSaving(true);
		try {
			const savedRule = await saveModelPriceRule(rule);
			setPriceDraft(createPriceDraft(savedRule));
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
                onClick={() => void openPriceManagement()}
                disabled={isPriceLoading}
                aria-busy={isPriceLoading}
              >
                {t('usage_stats.model_price_settings')}
              </button>
              <button
                type="button"
                className={`${styles.quickLinkButton} ${styles.mastheadActionButton}`}
                onClick={() => navigate('/data-management')}
              >
                {t('nav.data_management', { defaultValue: 'Data Management' })}
              </button>
            </div>
          </div>
          <p className={styles.subtitle}>{t('monitoring.console_subtitle')}</p>

          <div className={styles.usageStatsHero}>
            <TopUsageStats cards={usageMetricCards} />
            {usageAggregates?.summarySnapshotAtMs ? (
              <small>{t('monitoring.summary_updated', { time: new Date(usageAggregates.summarySnapshotAtMs).toLocaleTimeString() })}</small>
            ) : null}
          </div>
        </div>
      </section>

      {!isUsageTrendHidden ? (
        <section className={styles.usageTrendSection}>
          <UsageTrendHeader
            range={timeRange}
            totalCalls={usageTrendAnalytics.scopedTotals.requests}
            statusText={usageTrendStatusText}
            apiKeyFilter={usageTrendApiKey}
            apiKeyOptions={usageTrendApiKeyOptions}
            onRangeChange={handleTimeRangeChange}
            onApiKeyFilterChange={setUsageTrendApiKey}
            onHide={() => setIsUsageTrendHidden(true)}
            t={t}
          />
          {usageTrendHasDisplayData ? (
            <>
              <div
                className={`${styles.usageTrendData} ${usageTrendDataStale ? styles.usageTrendDataStale : ''}`.trim()}
                aria-busy={usageTrendDataRefreshing}
              >
                <div className={styles.usageTrendInsightsGrid}>
                  <UsageTrendPanel
                    points={usageTrendPoints}
                    durationMinutes={usageTrendAnalytics.durationMinutes}
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
                    durationMinutes={usageTrendAnalytics.durationMinutes}
                    emptyText={t('monitoring.no_data')}
                    hasPrices={hasPrices}
                    t={t}
                  />
                </div>
              </div>
              {usageAggregatesError ? (
                <div className={`${styles.errorBox} ${styles.usageTrendState}`} role="alert">
                  <span>{usageAggregatesError}</span>
                  <Button variant="secondary" size="sm" onClick={() => void refreshAggregates()}>
                    {t('common.retry')}
                  </Button>
                </div>
              ) : null}
            </>
          ) : (
            <div
              className={`${usageAggregatesError ? styles.errorBox : styles.callout} ${styles.usageTrendState}`}
              role={usageAggregatesError ? 'alert' : 'status'}
              aria-busy={usageAggregatesLoading || usageAggregatesRefreshing}
            >
              <span>{usageTrendStatusText}</span>
              {usageAggregatesError ? (
                <Button variant="secondary" size="sm" onClick={() => void refreshAggregates()}>
                  {t('common.retry')}
                </Button>
              ) : null}
            </div>
          )}
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
            <TimeRangeSelector value={timeRange} onChange={handleTimeRangeChange} panelAlign="end" />
          </div>
        </div>

        <Card className={styles.realtimePanel}>
        <div id="request-events" className={styles.filterGrid}>
          <Input
            value={searchInput}
            onChange={(event) => {
              setLinkedRequestLogScope(null);
              setSearchInput(event.target.value);
            }}
            placeholder={t('monitoring.search_placeholder')}
            className={styles.toolbarHeaderSearchInput}
            rightElement={<IconSearch size={16} />}
            aria-label={t('monitoring.search_placeholder')}
          />
          <Select
            value={selectedApiKey}
            options={apiKeyOptions}
            onChange={(value) => {
              setSelectedApiKey(value);
              setSelectedApiKeyFallbackLabel('');
            }}
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
            triggerClassName={styles.realtimeFilterSelectTrigger}
            dropdownClassName={styles.realtimeFilterSelectDropdown}
          />
          <Select
            value={selectedStatus}
            options={statusOptions}
            onChange={(value) => setSelectedStatus(value as StatusFilter)}
            ariaLabel={t('monitoring.filter_status')}
          />
          <Select
            value={selectedProfile}
            options={profileFilterOptions}
            onChange={(value) => {
              setSelectedProfile(value);
              setSelectedProfileFallbackName('');
            }}
            ariaLabel={t('monitoring.filter_profile')}
            triggerClassName={styles.realtimeFilterSelectTrigger}
            dropdownClassName={styles.realtimeFilterSelectDropdown}
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
        {realtimeLogPagination.total > 0 ? (
          <div className={styles.paginationBar}>
            <div className={styles.paginationPageSizeControl}>
              <span id="realtime-log-page-size-label">
                {t('monitoring.pagination_page_size', { defaultValue: paginationCopy.pageSizeLabel })}
              </span>
              <Select
                id="realtime-log-page-size"
                value={String(realtimeLogPageSize)}
                options={PRO_PAGE_SIZE_OPTIONS.map((pageSize) => ({
                  value: String(pageSize),
                  label: t('monitoring.pagination_page_size_value', {
                    count: pageSize,
                    defaultValue: paginationCopy.pageSizeValue(pageSize),
                  }),
                }))}
                onChange={(value) => {
                  const nextPageSize = normalizeProPageSize(value);
                  if (nextPageSize === realtimeLogPageSize) return;
                  resetRealtimeLogs();
                  setRealtimeLogPageSize(nextPageSize);
                }}
                ariaLabelledBy="realtime-log-page-size-label"
                className={styles.paginationPageSizeSelect}
              />
            </div>
            {realtimeLogPagination.totalPages > 1 ? (
              <div className={styles.paginationNavigation}>
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
          </div>
        ) : null}
        </Card>
      </section>

      <ProDetailDialog
        open={activeSurface === 'realtime-detail' && Boolean(selectedRealtimeErrorRow)}
        onClose={() => setSelectedRealtimeErrorRow(null)}
        onAfterClose={() => setSelectedRealtimeErrorRowState(null)}
        title={translateRealtimeErrorText('request_details', t, i18n.language)}
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
          <RealtimeRequestDetailsPanel row={selectedRealtimeErrorRow} t={t} language={i18n.language} />
        ) : null}
      </ProDetailDialog>

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
        handleServiceTierChange={handleServiceTierChange}
        addServiceTier={addServiceTier}
        removeServiceTier={removeServiceTier}
        handleSpeedChange={handleSpeedChange}
        addSpeed={addSpeed}
        removeSpeed={removeSpeed}
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
        savedMonitoringSettingsDraft={savedMonitoringSettingsDraft}
        setMonitoringSettingsDraft={setMonitoringSettingsDraft}
        handleSaveMonitoringSettings={handleSaveMonitoringSettings}
        isMonitoringSettingsLoading={isMonitoringSettingsLoading}
        isMonitoringSettingsSaving={isMonitoringSettingsSaving}
        t={t}
      />
    </div>
  );
}
