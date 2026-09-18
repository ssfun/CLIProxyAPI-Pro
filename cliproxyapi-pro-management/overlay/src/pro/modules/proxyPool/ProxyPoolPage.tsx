import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconInfo, IconNetwork, IconSettings } from '@/components/ui/icons';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import { ProxyPoolDiagnostics } from '@/pro/modules/proxyPool/features/ProxyPoolDiagnostics';
import { ProxyPoolHeader } from '@/pro/modules/proxyPool/features/ProxyPoolHeader';
import { ProxyPoolImportModal } from '@/pro/modules/proxyPool/features/ProxyPoolImportModal';
import { ProxyPoolNodeManager } from '@/pro/modules/proxyPool/features/ProxyPoolNodeManager';
import { ProxyPoolNodeSheet } from '@/pro/modules/proxyPool/features/ProxyPoolNodeSheet';
import { ProxyPoolSaveBar } from '@/pro/modules/proxyPool/features/ProxyPoolSaveBar';
import { ProxyPoolSettings } from '@/pro/modules/proxyPool/features/ProxyPoolSettings';
import { ProxyPoolStatusOverview } from '@/pro/modules/proxyPool/features/ProxyPoolStatusOverview';
import { ProxyPoolTakeoverDialog } from '@/pro/modules/proxyPool/features/ProxyPoolTakeoverDialog';
import {
  createProxyPoolNode,
  proxyNodeKey,
  type ProxyPoolStatusFilter,
  type ProxyPoolView,
} from '@/pro/modules/proxyPool/features/proxyPoolUi';
import {
  defaultProxyPoolConfig,
  normalizeProxyPoolInteger,
  PROXY_POOL_TEST_CONCURRENCY_LIMITS,
  proxyPoolApi,
  type ProxyPoolConfig,
  type ProxyPoolNodeConfig,
  type ProxyPoolProbeResult,
  type ProxyPoolSnapshot,
} from '@/pro/modules/proxyPool/proxyPool';
import { useProSurfaceState } from '@/pro/shared/useProSurfaceState';
import { ProFeatureTabs } from '@/pro/shared/ProFeatureTabs';
import { useAuthStore, useNotificationStore } from '@/stores';
import styles from '@/pro/modules/proxyPool/features/ProxyPool.module.scss';

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : String(error || 'Unknown error');

interface ValidationError {
  key: string;
  defaultValue: string;
  values?: Record<string, string>;
}

const parseLoopbackListener = (value: string): { host: string; port: string } | null => {
  const listen = value.trim();
  const ipv6Match = listen.match(/^\[::1\]:(\d{1,5})$/);
  if (ipv6Match) {
    const port = Number(ipv6Match[1]);
    return port >= 1 && port <= 65535 ? { host: '::1', port: String(port) } : null;
  }
  const separator = listen.lastIndexOf(':');
  if (separator <= 0) return null;
  const host = listen.slice(0, separator);
  const portText = listen.slice(separator + 1);
  const octets = host.split('.');
  const port = Number(portText);
  if (
    octets.length !== 4 ||
    octets[0] !== '127' ||
    octets.some((octet) => !/^\d{1,3}$/.test(octet) || Number(octet) > 255) ||
    !/^\d{1,5}$/.test(portText) ||
    port < 1 ||
    port > 65535
  )
    return null;
  return { host, port: String(port) };
};

const isLoopbackHostname = (value: string): boolean => {
  const host = value.trim().replace(/^\[|\]$/g, '').toLowerCase();
  if (host === 'localhost' || host === '::1') return true;
  const octets = host.split('.');
  return (
    octets.length === 4 &&
    octets[0] === '127' &&
    octets.every((octet) => /^\d{1,3}$/.test(octet) && Number(octet) <= 255)
  );
};

const validateProxyPoolConfig = (config: ProxyPoolConfig): ValidationError | null => {
  const listener = parseLoopbackListener(config.listen);
  if (!listener)
    return {
      key: 'proxy_pool.validation_listener',
      defaultValue: 'Listener must be a numeric loopback address with a port from 1 to 65535',
    };
  const ids = new Set<string>();
  const urls = new Set<string>();
  for (const node of config.nodes) {
    const id = node.id.trim();
    const url = node.url.trim();
    if (!id || !url)
      return {
        key: 'proxy_pool.validation_required',
        defaultValue: 'Every node requires an ID and proxy URL',
      };
    if (ids.has(id))
      return {
        key: 'proxy_pool.validation_duplicate_id',
        defaultValue: 'Duplicate node ID: {{value}}',
        values: { value: id },
      };
    if (urls.has(url))
      return {
        key: 'proxy_pool.validation_duplicate_url',
        defaultValue: 'Duplicate proxy URL: {{value}}',
        values: { value: url },
      };
    ids.add(id);
    urls.add(url);
    if (!/^(https?|socks5h?):\/\//i.test(url))
      return {
        key: 'proxy_pool.validation_unsupported_url',
        defaultValue: 'Unsupported proxy URL: {{value}}',
        values: { value: url },
      };
    try {
      const parsed = new URL(url);
      if (!parsed.hostname) throw new Error('missing host');
      const normalizedHost = parsed.hostname.replace(/^\[|\]$/g, '');
      const normalizedPort =
        parsed.port ||
        (parsed.protocol === 'http:' ? '80' : parsed.protocol === 'https:' ? '443' : '');
      if (
        normalizedPort === listener.port &&
        (normalizedHost === listener.host || isLoopbackHostname(normalizedHost))
      )
        return {
          key: 'proxy_pool.validation_recursive_url',
          defaultValue: 'A proxy node cannot point back to the internal listener: {{value}}',
          values: { value: url },
        };
    } catch {
      return {
        key: 'proxy_pool.validation_invalid_url',
        defaultValue: 'Invalid proxy URL: {{value}}',
        values: { value: url },
      };
    }
  }
  return null;
};

export function ProxyPoolPage() {
  const { t, i18n } = useTranslation();
  const pageTransitionLayer = usePageTransitionLayer();
  const isCurrentLayer = pageTransitionLayer ? pageTransitionLayer.isCurrentLayer : true;
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const [snapshot, setSnapshot] = useState<ProxyPoolSnapshot | null>(null);
  const [draft, setDraft] = useState<ProxyPoolConfig>(defaultProxyPoolConfig);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testConcurrency, setTestConcurrency] = useState<number>(
    PROXY_POOL_TEST_CONCURRENCY_LIMITS.default
  );
  const [testingNode, setTestingNode] = useState('');
  const [recoveringNode, setRecoveringNode] = useState('');
  const [dirty, setDirty] = useState(false);
  const dirtyRef = useRef(false);
  const draftRevisionRef = useRef(0);
  const loadSequenceRef = useRef(0);
  const appliedLoadSequenceRef = useRef(0);
  const pollInFlightRef = useRef(false);
  const mountedRef = useRef(true);
  const automaticLocationAttemptsRef = useRef(new Set<string>());
  const [loadError, setLoadError] = useState('');
  const [probeResults, setProbeResults] = useState<Record<string, ProxyPoolProbeResult>>({});
  const [activeView, setActiveView] = useState<ProxyPoolView>('nodes');
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<ProxyPoolStatusFilter>('all');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [pendingNode, setPendingNode] = useState<ProxyPoolNodeConfig | null>(null);
  const { activeSurface, openSurface, closeSurface } = useProSurfaceState<'node' | 'import' | 'takeover'>();
  const importOpen = activeSurface === 'import';
  const takeoverOpen = activeSurface === 'takeover';
  const unsavedChangesDialog = useMemo(
    () => ({
      title: t('common.unsaved_changes_title'),
      message: t('common.unsaved_changes_message'),
      confirmText: t('common.confirm'),
      cancelText: t('common.cancel'),
    }),
    [t]
  );

  useUnsavedChangesGuard({
    enabled: isCurrentLayer,
    shouldBlock: dirty,
    dialog: unsavedChangesDialog,
  });

  const setImportOpen = useCallback((open: boolean) => {
    if (open) openSurface('import');
    else if (activeSurface === 'import') closeSurface();
  }, [activeSurface, closeSurface, openSurface]);
  const setTakeoverOpen = useCallback((open: boolean) => {
    if (open) openSurface('takeover');
    else if (activeSurface === 'takeover') closeSurface();
  }, [activeSurface, closeSurface, openSurface]);

  const load = useCallback(
    async (silent = false, replaceDraftRevision?: number): Promise<boolean> => {
      const loadSequence = ++loadSequenceRef.current;
      if (connectionStatus !== 'connected') {
        appliedLoadSequenceRef.current = loadSequence;
        setLoading(false);
        return false;
      }
      if (!silent) setLoading(true);
      try {
        const next = await proxyPoolApi.load();
        if (loadSequence < appliedLoadSequenceRef.current) return false;
        appliedLoadSequenceRef.current = loadSequence;
        setSnapshot(next);
        const canReplaceSavedDraft =
          replaceDraftRevision !== undefined &&
          draftRevisionRef.current === replaceDraftRevision;
        if (!dirtyRef.current || canReplaceSavedDraft) setDraft(next.config);
        if (canReplaceSavedDraft) {
          setDirty(false);
          dirtyRef.current = false;
        }
        setLoadError('');
        return canReplaceSavedDraft;
      } catch (error) {
        if (loadSequence < appliedLoadSequenceRef.current) return false;
        appliedLoadSequenceRef.current = loadSequence;
        setLoadError(errorMessage(error));
        return false;
      } finally {
        if (loadSequence === loadSequenceRef.current) setLoading(false);
      }
    },
    [connectionStatus]
  );

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  useEffect(() => {
    if (connectionStatus !== 'connected') return;
    const timer = window.setInterval(() => {
      if (pollInFlightRef.current) return;
      pollInFlightRef.current = true;
      void load(true).finally(() => {
        pollInFlightRef.current = false;
      });
    }, 10_000);
    return () => window.clearInterval(timer);
  }, [connectionStatus, load]);

  const statusByID = useMemo(
    () => new Map((snapshot?.status?.nodes ?? []).map((node) => [node.id, node])),
    [snapshot?.status?.nodes]
  );
  useEffect(() => {
    if (connectionStatus !== 'connected' || !snapshot || dirtyRef.current) return;
    const runtimeByID = new Map((snapshot.status?.nodes ?? []).map((node) => [node.id, node]));
    const candidates = snapshot.config.nodes
      .map((node, index) => ({
        node,
        key: proxyNodeKey(node, index),
        attemptKey: `${node.id}\n${node.url}\n${snapshot.config.healthCheck.testUrl}`,
      }))
      .filter(({ node, attemptKey }) => {
        if (!node.enabled || !node.url.trim() || runtimeByID.get(node.id)?.location) return false;
        if (automaticLocationAttemptsRef.current.has(attemptKey)) return false;
        automaticLocationAttemptsRef.current.add(attemptKey);
        return true;
      });
    if (candidates.length === 0) return;

    void (async () => {
      for (let offset = 0; offset < candidates.length; offset += testConcurrency) {
        const results = await Promise.all(
          candidates.slice(offset, offset + testConcurrency).map(async ({ node, key }) => {
            try {
              const result = await proxyPoolApi.testNode(
                node.id,
                node.url,
                snapshot.config.healthCheck.testUrl
              );
              return { key, result };
            } catch {
              return null;
            }
          })
        );
        if (!mountedRef.current) return;
        setProbeResults((current) => {
          const next = { ...current };
          results.forEach((item) => {
            if (item?.result.success) next[item.key] = item.result;
          });
          return next;
        });
      }
    })();
  }, [connectionStatus, snapshot, testConcurrency]);
  const editingNode =
    pendingNode ?? (editingIndex === null ? null : (draft.nodes[editingIndex] ?? null));
  const editingNodeIndex = pendingNode ? draft.nodes.length : editingIndex;
  const editingKey =
    editingNode && editingNodeIndex !== null
      ? proxyNodeKey(editingNode, editingNodeIndex)
      : '';

  const closeNodeSheet = () => {
    if (activeSurface === 'node') closeSurface();
  };

  const clearClosedNodeSheet = () => {
    setEditingIndex(null);
    setPendingNode(null);
  };

  const editNode = (index: number) => {
    setPendingNode(null);
    setEditingIndex(index);
    openSurface('node');
  };

  const beginAddNode = () => {
    setEditingIndex(null);
    setPendingNode(createProxyPoolNode(draft.nodes.length));
    openSurface('node');
  };

  const updateDraft = useCallback(
    (next: ProxyPoolConfig | ((current: ProxyPoolConfig) => ProxyPoolConfig)) => {
      setDraft((current) => (typeof next === 'function' ? next(current) : next));
      draftRevisionRef.current += 1;
      setDirty(true);
      dirtyRef.current = true;
    },
    []
  );

  const notifyValidation = (validation: ValidationError): void => {
    showNotification(
      t(validation.key, { defaultValue: validation.defaultValue, ...validation.values }),
      'error'
    );
  };

  const save = async (): Promise<boolean> => {
    const configToSave = draft;
    const savedRevision = draftRevisionRef.current;
    const validation = validateProxyPoolConfig(configToSave);
    if (validation) {
      notifyValidation(validation);
      return false;
    }
    setSaving(true);
    try {
      await proxyPoolApi.save(configToSave);
      if (draftRevisionRef.current === savedRevision) {
        setDirty(false);
        dirtyRef.current = false;
      }
      showNotification(
        t('proxy_pool.save_success', { defaultValue: 'Proxy pool saved' }),
        'success'
      );
      await load(true, savedRevision);
      return true;
    } catch (error) {
      showNotification(
        `${t('proxy_pool.save_failed', { defaultValue: 'Save failed' })}: ${errorMessage(error)}`,
        'error'
      );
      return false;
    } finally {
      setSaving(false);
    }
  };

  const confirmTakeover = async () => {
    if (!snapshot) return;
    const activating = !snapshot.takeoverActive;
    const configToApply = draft;
    const savedRevision = draftRevisionRef.current;
    const validation = validateProxyPoolConfig(configToApply);
    if (activating && validation) {
      notifyValidation(validation);
      setTakeoverOpen(false);
      return;
    }
    setSaving(true);
    try {
      if (activating) {
        const activationDraft = { ...configToApply, enabled: true, takeoverEnabled: true };
        await proxyPoolApi.activate(activationDraft);
      } else {
        await proxyPoolApi.deactivate(configToApply);
      }
      if (draftRevisionRef.current === savedRevision) {
        setDirty(false);
        dirtyRef.current = false;
      }
      setTakeoverOpen(false);
      const replacedDraft = await load(true, savedRevision);
      if (!replacedDraft) {
        setDraft((current) => ({ ...current, takeoverEnabled: activating }));
      }
      showNotification(
        activating
          ? t('proxy_pool.takeover_enabled', { defaultValue: 'Global proxy takeover enabled' })
          : t('proxy_pool.takeover_disabled', { defaultValue: 'Global proxy takeover disabled' }),
        'success'
      );
    } catch (error) {
      showNotification(errorMessage(error), 'error');
    } finally {
      setSaving(false);
    }
  };

  const runNodeTest = async (
    node: ProxyPoolNodeConfig,
    index: number,
    announce = true
  ): Promise<ProxyPoolProbeResult | null> => {
    const key = proxyNodeKey(node, index);
    setTestingNode(key);
    try {
      const result = await proxyPoolApi.testNode(key, node.url, draft.healthCheck.testUrl);
      setProbeResults((current) => ({ ...current, [key]: result }));
      if (announce)
        showNotification(
          result.success
            ? `${result.exitIp || key} · ${result.latencyMs} ms`
            : result.error || t('proxy_pool.test_failed', { defaultValue: 'Proxy test failed' }),
          result.success ? 'success' : 'error'
        );
      return result;
    } catch (error) {
      if (announce) showNotification(errorMessage(error), 'error');
      return null;
    } finally {
      setTestingNode('');
    }
  };

  const runTests = async (items: Array<{ node: ProxyPoolNodeConfig; index: number }>) => {
    setTesting(true);
    try {
      const results: ProxyPoolProbeResult[] = [];
      for (let offset = 0; offset < items.length; offset += testConcurrency) {
        const batch = await Promise.all(
          items.slice(offset, offset + testConcurrency).map(({ node, index }) => runNodeTest(node, index, false))
        );
        results.push(...batch.filter((result): result is ProxyPoolProbeResult => result !== null));
      }
      if (items.length === 0)
        showNotification(
          t('proxy_pool.no_nodes_to_test', { defaultValue: 'No enabled proxy nodes to test' }),
          'warning'
        );
      else {
        const success = results.filter((result) => result.success).length;
        showNotification(
          t('proxy_pool.test_summary', {
            defaultValue: '{{success}}/{{total}} nodes passed',
            success,
            total: items.length,
          }),
          success === items.length ? 'success' : 'warning'
        );
      }
      await load(true);
    } finally {
      setTesting(false);
    }
  };

  const testAll = () =>
    runTests(
      draft.nodes
        .map((node, index) => ({ node, index }))
        .filter(({ node }) => node.enabled && node.url.trim())
    );
  const testSelected = () =>
    runTests(
      draft.nodes
        .map((node, index) => ({ node, index }))
        .filter(({ node, index }) => selected.has(proxyNodeKey(node, index)) && node.url.trim())
    );

  const moveNode = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= draft.nodes.length) return;
    updateDraft((current) => {
      const nodes = [...current.nodes];
      [nodes[index], nodes[target]] = [nodes[target], nodes[index]];
      return {
        ...current,
        nodes: nodes.map((node, nodeIndex) => ({ ...node, order: (nodeIndex + 1) * 10 })),
      };
    });
  };

  const enableSelected = (enabled: boolean) => {
    updateDraft((current) => ({
      ...current,
      nodes: current.nodes.map((node, index) =>
        selected.has(proxyNodeKey(node, index)) ? { ...node, enabled } : node
      ),
    }));
  };
  const deleteSelected = () =>
    showConfirmation({
      title: t('proxy_pool.delete_selected_title', { defaultValue: 'Delete selected nodes?' }),
      message: t('proxy_pool.delete_selected_message', {
        defaultValue: '{{count}} selected nodes will be removed from the draft configuration.',
        count: selected.size,
      }),
      confirmText: t('common.delete'),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: () => {
        updateDraft((current) => ({
          ...current,
          nodes: current.nodes
            .filter((node, index) => !selected.has(proxyNodeKey(node, index)))
            .map((node, index) => ({ ...node, order: (index + 1) * 10 })),
        }));
        setSelected(new Set());
      },
    });

  const resetStats = () =>
    showConfirmation({
      title: t('proxy_pool.reset_stats_title', { defaultValue: 'Reset runtime statistics?' }),
      message: t('proxy_pool.reset_stats_message', {
        defaultValue:
          'Connection counters, latency samples, and recent probe results will be cleared.',
      }),
      confirmText: t('proxy_pool.reset_stats', { defaultValue: 'Reset statistics' }),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: async () => {
        try {
          await proxyPoolApi.resetStats();
          automaticLocationAttemptsRef.current.clear();
          setProbeResults({});
          await load(true);
          showNotification(
            t('proxy_pool.stats_reset', { defaultValue: 'Runtime stats reset' }),
            'success'
          );
        } catch (error) {
          showNotification(errorMessage(error), 'error');
        }
      },
    });

  const recoverNode = async (nodeId: string) => {
    setRecoveringNode(nodeId);
    try {
      await proxyPoolApi.recoverNode(nodeId);
      await load(true);
      showNotification(
        t('proxy_pool.recover_success', { defaultValue: 'Node isolation cleared' }),
        'success'
      );
    } catch (error) {
      showNotification(errorMessage(error), 'error');
    } finally {
      setRecoveringNode('');
    }
  };

  const copyDiagnostics = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      showNotification(
        t('proxy_pool.diagnostics_copied', { defaultValue: 'Diagnostics copied' }),
        'success'
      );
    } catch (error) {
      showNotification(errorMessage(error), 'error');
    }
  };

  const discard = () => {
    if (!snapshot) return;
    setDraft(snapshot.config);
    draftRevisionRef.current += 1;
    setDirty(false);
    dirtyRef.current = false;
    setSelected(new Set());
    setProbeResults({});
    showNotification(
      t('proxy_pool.changes_discarded', { defaultValue: 'Unsaved changes discarded' }),
      'success'
    );
  };

  return (
    <div className={`${styles.page} ${dirty ? styles.pageWithSave : ''}`}>
      <ProxyPoolHeader
        snapshot={snapshot}
        loading={loading}
        busy={saving}
        onRefresh={() => void load()}
        onTakeover={() => setTakeoverOpen(true)}
      />
      {loadError && <div className={styles.errorBanner}>{loadError}</div>}
      {!snapshot ? (
        <div className={styles.noticeCard}>
          <strong>
            {loading
              ? t('proxy_pool.loading', { defaultValue: 'Loading proxy pool...' })
              : t('proxy_pool.load_unavailable', {
                  defaultValue: 'Proxy pool data is unavailable',
                })}
          </strong>
          <p>
            {loading
              ? t('proxy_pool.loading_hint', {
                  defaultValue: 'Reading proxy pool configuration and runtime status.',
                })
              : t('proxy_pool.load_unavailable_hint', {
                  defaultValue: 'Fix the connection error above, then refresh.',
                })}
          </p>
        </div>
      ) : (
          <>
            <ProxyPoolStatusOverview snapshot={snapshot} draft={draft} />
            <ProFeatureTabs
              ariaLabel={t('proxy_pool.views', { defaultValue: 'Proxy pool views' })}
              activeKey={activeView}
              onChange={(key) => setActiveView(key as ProxyPoolView)}
              items={[
                {
                  key: 'nodes',
                  icon: <IconNetwork size={17} />,
                  label: t('proxy_pool.node_management', { defaultValue: 'Node management' }),
                },
                {
                  key: 'diagnostics',
                  icon: <IconInfo size={17} />,
                  label: t('proxy_pool.diagnostics', { defaultValue: 'Runtime diagnostics' }),
                },
                {
                  key: 'settings',
                  icon: <IconSettings size={17} />,
                  label: t('proxy_pool.advanced_settings', { defaultValue: 'Advanced settings' }),
                },
              ]}
            />

            {activeView === 'nodes' && (
              <>
                <section className={styles.quickSettings}>
                  <div className={styles.quickSettingField}>
                    <span className={styles.quickSettingLabel}>
                      {t('proxy_pool.strategy', { defaultValue: 'Selection strategy' })}
                    </span>
                    <Select
                      size="sm"
                      triggerClassName={styles.quickSelectTrigger}
                      value={draft.strategy}
                      onChange={(strategy) =>
                        updateDraft({ ...draft, strategy: strategy as ProxyPoolConfig['strategy'] })
                      }
                      options={[
                        {
                          value: 'round-robin',
                          label: t('proxy_pool.strategy_round_robin', {
                            defaultValue: 'Round robin',
                          }),
                        },
                        {
                          value: 'weighted',
                          label: t('proxy_pool.strategy_weighted', { defaultValue: 'Weighted' }),
                        },
                        {
                          value: 'least-connections',
                          label: t('proxy_pool.strategy_least_connections', {
                            defaultValue: 'Least connections',
                          }),
                        },
                      ]}
                    />
                  </div>
                  <div className={styles.quickSettingField}>
                    <span className={styles.quickSettingLabel}>
                      {t('proxy_pool.background_health_checks', {
                        defaultValue: 'Background health checks',
                      })}
                    </span>
                    <div className={styles.quickToggleControl}>
                      <ToggleSwitch
                        checked={draft.healthCheck.enabled}
                        onChange={(enabled) =>
                          updateDraft({ ...draft, healthCheck: { ...draft.healthCheck, enabled } })
                        }
                        ariaLabel={t('proxy_pool.background_health_checks', {
                          defaultValue: 'Background health checks',
                        })}
                      />
                      <span>
                        {draft.healthCheck.enabled
                          ? t('proxy_pool.enabled', { defaultValue: 'Enabled' })
                          : t('proxy_pool.disabled', { defaultValue: 'Disabled' })}
                      </span>
                    </div>
                  </div>
                  <button
                    type="button"
                    className={styles.settingsLink}
                    onClick={() => setActiveView('settings')}
                  >
                    {t('proxy_pool.open_advanced_settings', {
                      defaultValue: 'Open advanced settings',
                    })}
                  </button>
                </section>
                <ProxyPoolNodeManager
                  nodes={draft.nodes}
                  statusByID={statusByID}
                  probeResults={probeResults}
                  query={query}
                  statusFilter={statusFilter}
                  selected={selected}
                  language={i18n.language}
                  testing={testing}
                  onQueryChange={setQuery}
                  onStatusFilterChange={setStatusFilter}
                  onSelectionChange={setSelected}
                  onEdit={editNode}
                  onMove={moveNode}
                  onAdd={beginAddNode}
                  onImport={() => setImportOpen(true)}
                  onBulkEnable={enableSelected}
                  onBulkTest={() => void testSelected()}
                  onBulkDelete={deleteSelected}
                />
              </>
            )}
            {activeView === 'diagnostics' && (
              <ProxyPoolDiagnostics
                snapshot={snapshot}
                draft={draft}
                language={i18n.language}
                testing={testing}
                testConcurrency={testConcurrency}
                onTestConcurrencyChange={(value) => setTestConcurrency(normalizeProxyPoolInteger(
                  value,
                  PROXY_POOL_TEST_CONCURRENCY_LIMITS.min,
                  PROXY_POOL_TEST_CONCURRENCY_LIMITS.max
                ))}
                onTestAll={() => void testAll()}
                onResetStats={resetStats}
                onCopy={(value) => void copyDiagnostics(value)}
              />
            )}
            {activeView === 'settings' && (
              <ProxyPoolSettings
                draft={draft}
                onChange={updateDraft}
                onEnableFailOpen={() =>
                  showConfirmation({
                    title: t('proxy_pool.enable_fail_open_title', {
                      defaultValue: 'Enable direct fallback?',
                    }),
                    message: t('proxy_pool.enable_fail_open_message', {
                      defaultValue:
                        'When every proxy is unavailable, requests may connect directly and expose the server IP.',
                    }),
                    confirmText: t('proxy_pool.enable_fail_open', {
                      defaultValue: 'Enable direct fallback',
                    }),
                    cancelText: t('common.cancel'),
                    variant: 'danger',
                    onConfirm: () => updateDraft({ ...draft, failOpen: true }),
                  })
                }
              />
            )}

            <ProxyPoolNodeSheet
              open={activeSurface === 'node' && editingNode !== null}
              node={editingNode}
              strategy={draft.strategy}
              runtime={!pendingNode && editingNode ? statusByID.get(editingNode.id) : undefined}
              probe={probeResults[editingKey]}
              language={i18n.language}
              testing={testingNode === editingKey}
              recovering={recoveringNode === editingNode?.id}
              onClose={closeNodeSheet}
              onAfterClose={clearClosedNodeSheet}
              onApply={(node) => {
                if (pendingNode) {
                  updateDraft((current) => ({ ...current, nodes: [...current.nodes, node] }));
                } else if (editingIndex !== null) {
                  updateDraft((current) => ({
                    ...current,
                    nodes: current.nodes.map((item, index) =>
                      index === editingIndex ? node : item
                    ),
                  }));
                }
              }}
              onTest={(node) => {
                if (editingNodeIndex !== null) void runNodeTest(node, editingNodeIndex);
              }}
              onRecover={(nodeId) => void recoverNode(nodeId)}
            />
            <ProxyPoolImportModal
              open={importOpen}
              existing={draft.nodes}
              onClose={() => setImportOpen(false)}
              onImport={(nodes) =>
                updateDraft((current) => ({ ...current, nodes: [...current.nodes, ...nodes] }))
              }
            />
            <ProxyPoolTakeoverDialog
              open={takeoverOpen}
              snapshot={snapshot}
              draft={draft}
              busy={saving}
              onClose={() => setTakeoverOpen(false)}
              onConfirm={() => void confirmTakeover()}
            />
            <ProxyPoolSaveBar
              visible={isCurrentLayer && dirty}
              saving={saving}
              onDiscard={discard}
              onSave={() => void save()}
            />
          </>
      )}
    </div>
  );
}
