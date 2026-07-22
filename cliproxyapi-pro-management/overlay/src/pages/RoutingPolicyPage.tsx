import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { createPortal } from 'react-dom';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconCheck, IconRefreshCw, IconShield } from '@/components/ui/icons';
import { useActionBarHeightVar } from '@/hooks/useActionBarHeightVar';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import {
  ROUTING_POLICY_PROVIDERS,
  routingPolicyApi,
  type RoutingPolicyGlobalSettings,
  type RoutingPolicyProvider,
  type RoutingPolicyResponse,
  type RoutingProtectedAccount,
  type RoutingProtectionEvent,
  type RoutingProtectionProviderPolicy,
  type RoutingRequestProtectionConfig,
} from '@/services/api/routingPolicy';
import { useAuthStore, useNotificationStore } from '@/stores';
import configActionStyles from './ConfigPage.module.scss';
import styles from './RoutingPolicyPage.module.scss';

type RoutingPolicyView = 'global' | 'providers' | 'runtime';
type RoutingRuntimeDetail =
  | { kind: 'active'; item: RoutingProtectedAccount }
  | { kind: 'event'; item: RoutingProtectionEvent };

const VIEW_KEYS: RoutingPolicyView[] = ['global', 'providers', 'runtime'];

const parseStatusCodes = (value: string): number[] => {
  const result = new Set<number>();
  for (const token of value.split(/[\s,;]+/)) {
    if (!token) continue;
    const status = Number(token);
    if (Number.isInteger(status) && status >= 100 && status <= 599) {
      result.add(status);
    }
  }
  return [...result].sort((left, right) => left - right);
};

const formatTimestamp = (value: number, locale: string, emptyText: string): string => {
  if (!value) return emptyText;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return emptyText;
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
};

const toNumber = (value: string): number => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
};

const runtimeStatusToneClass = (statusCode: number): string => {
  if (statusCode >= 400) return styles.runtimeStatusBad;
  if (statusCode >= 300) return styles.runtimeStatusWarn;
  if (statusCode >= 200) return styles.runtimeStatusGood;
  return styles.runtimeStatusNeutral;
};

function RoutingRuntimeDetailPanel({
  detail,
  t,
  language,
}: {
  detail: RoutingRuntimeDetail;
  t: ReturnType<typeof useTranslation>['t'];
  language: string;
}) {
  const item = detail.item;
  const accountName = item.fileName || item.authIndex || item.authId || '-';
  const action = detail.kind === 'active' ? 'disabled' : detail.item.action;
  const detailItems = [
    { label: t('routing_policy.runtime.provider'), value: item.provider || '-' },
    { label: t('routing_policy.runtime.account'), value: accountName },
    { label: t('routing_policy.runtime.auth_index'), value: item.authIndex || '-' },
    { label: t('routing_policy.runtime.auth_id'), value: item.authId || '-' },
    {
      label: t('routing_policy.runtime.status_code'),
      value: item.statusCode ? String(item.statusCode) : '-',
    },
    {
      label:
        detail.kind === 'active'
          ? t('routing_policy.runtime.triggered_at')
          : t('routing_policy.runtime.time'),
      value: formatTimestamp(item.triggeredAt, language, '-'),
    },
    {
      label: t('routing_policy.runtime.release_at'),
      value: formatTimestamp(
        item.releaseAt,
        language,
        detail.kind === 'active' ? t('routing_policy.runtime.manual') : '-'
      ),
    },
    {
      label: t('routing_policy.runtime.action'),
      value: t(`routing_policy.actions.${action}`, { defaultValue: action }),
    },
    ...(detail.kind === 'event'
      ? [
          {
            label: t('routing_policy.runtime.mode'),
            value: t(`routing_policy.mode_${detail.item.mode}`, {
              defaultValue: detail.item.mode,
            }),
          },
          {
            label: t('routing_policy.runtime.confirmation'),
            value: detail.item.required
              ? `${detail.item.count}/${detail.item.required}`
              : '-',
          },
        ]
      : []),
  ];

  return (
    <div className={styles.runtimeDetailPanel}>
      <div className={styles.runtimeDetailOverview}>
        <div className={styles.runtimeDetailOverviewTop}>
          <span
            className={`${styles.runtimeStatusBadge} ${runtimeStatusToneClass(item.statusCode)}`}
          >
            {item.statusCode || '-'}
          </span>
          <span>{t(`routing_policy.actions.${action}`, { defaultValue: action })}</span>
        </div>
        <strong>{accountName}</strong>
      </div>
      <div className={styles.runtimeDetailGrid}>
        {detailItems.map((entry) => (
          <div key={entry.label} className={styles.runtimeDetailItem}>
            <span>{entry.label}</span>
            <strong>{entry.value}</strong>
          </div>
        ))}
      </div>
      <div className={styles.runtimeReasonBlock}>
        <span>{t('routing_policy.runtime.reason_details')}</span>
        <pre className={styles.runtimeReasonMessage}>{item.reason || '-'}</pre>
      </div>
    </div>
  );
}

export function RoutingPolicyPage() {
  const { t, i18n } = useTranslation();
  const pageTransitionLayer = usePageTransitionLayer();
  const isCurrentLayer = pageTransitionLayer ? pageTransitionLayer.isCurrentLayer : true;
  const isMobile = useMediaQuery('(max-width: 768px)');
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const [activeView, setActiveView] = useState<RoutingPolicyView>('global');
  const [data, setData] = useState<RoutingPolicyResponse | null>(null);
  const [globalSettings, setGlobalSettings] = useState<RoutingPolicyGlobalSettings | null>(null);
  const [requestProtection, setRequestProtection] =
    useState<RoutingRequestProtectionConfig | null>(null);
  const [statusCodeInputs, setStatusCodeInputs] = useState<Record<RoutingPolicyProvider, string>>(
    () =>
      Object.fromEntries(
        ROUTING_POLICY_PROVIDERS.map((provider) => [provider, ''])
      ) as Record<RoutingPolicyProvider, string>
  );
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [releasing, setReleasing] = useState<string | null>(null);
  const [selectedRuntimeDetail, setSelectedRuntimeDetail] =
    useState<RoutingRuntimeDetail | null>(null);
  const [error, setError] = useState('');
  const [dirty, setDirty] = useState(false);
  const floatingActionsRef = useRef<HTMLDivElement>(null);
  const runtimeRequestIdRef = useRef(0);

  const disabled = connectionStatus !== 'connected';
  const shouldRenderFloatingActions = isCurrentLayer;
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

  useActionBarHeightVar(
    floatingActionsRef,
    '--routing-policy-action-bar-height',
    shouldRenderFloatingActions
  );

  const applyConfigResponse = useCallback((response: RoutingPolicyResponse) => {
    setData(response);
    setGlobalSettings(response.global);
    setRequestProtection(response.requestProtection);
    setStatusCodeInputs(
      Object.fromEntries(
        ROUTING_POLICY_PROVIDERS.map((provider) => [
          provider,
          response.requestProtection.providers[provider]?.statusCodes?.join(', ') ?? '',
        ])
      ) as Record<RoutingPolicyProvider, string>
    );
    setDirty(false);
  }, []);

  const loadPolicy = useCallback(async () => {
    const requestId = runtimeRequestIdRef.current + 1;
    runtimeRequestIdRef.current = requestId;
    if (connectionStatus !== 'connected') {
      setData(null);
      setGlobalSettings(null);
      setRequestProtection(null);
      setError('');
      setLoading(false);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const response = await routingPolicyApi.get();
      if (runtimeRequestIdRef.current !== requestId) return;
      applyConfigResponse(response);
    } catch (error: unknown) {
      if (runtimeRequestIdRef.current !== requestId) return;
      const message = error instanceof Error ? error.message : t('routing_policy.load_failed');
      setError(message);
      showNotification(`${t('routing_policy.load_failed')}: ${message}`, 'error');
    } finally {
      if (runtimeRequestIdRef.current === requestId) setLoading(false);
    }
  }, [applyConfigResponse, connectionStatus, showNotification, t]);

  useEffect(() => {
    void loadPolicy();
  }, [loadPolicy]);

  useEffect(() => {
    if (activeView !== 'runtime' || connectionStatus !== 'connected' || saving || releasing) return;
    let inFlight = false;
    let cancelled = false;
    const timer = window.setInterval(() => {
      if (inFlight) return;
      inFlight = true;
      const requestId = runtimeRequestIdRef.current + 1;
      runtimeRequestIdRef.current = requestId;
      void routingPolicyApi.get()
        .then((response) => {
          if (cancelled || runtimeRequestIdRef.current !== requestId) return;
          setData((current) =>
            current
              ? { ...current, active: response.active, recentEvents: response.recentEvents }
              : response
          );
        })
        .catch(() => undefined)
        .finally(() => { inFlight = false; });
    }, 15000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [activeView, connectionStatus, releasing, saving]);

  const setGlobal = useCallback(
    <Key extends keyof RoutingPolicyGlobalSettings>(
      key: Key,
      value: RoutingPolicyGlobalSettings[Key]
    ) => {
      setGlobalSettings((current) => (current ? { ...current, [key]: value } : current));
      setDirty(true);
    },
    []
  );

  const setProtection = useCallback(
    <Key extends keyof Omit<RoutingRequestProtectionConfig, 'providers'>>(
      key: Key,
      value: RoutingRequestProtectionConfig[Key]
    ) => {
      setRequestProtection((current) => (current ? { ...current, [key]: value } : current));
      setDirty(true);
    },
    []
  );

  const setProviderPolicy = useCallback(
    <Key extends keyof RoutingProtectionProviderPolicy>(
      provider: RoutingPolicyProvider,
      key: Key,
      value: RoutingProtectionProviderPolicy[Key]
    ) => {
      setRequestProtection((current) => {
        if (!current) return current;
        return {
          ...current,
          providers: {
            ...current.providers,
            [provider]: { ...current.providers[provider], [key]: value },
          },
        };
      });
      setDirty(true);
    },
    []
  );

  const handleSave = async () => {
    if (!globalSettings || !requestProtection) return;
    const providers = { ...requestProtection.providers };
    for (const provider of ROUTING_POLICY_PROVIDERS) {
      const statusCodes = parseStatusCodes(statusCodeInputs[provider]);
      if (statusCodes.length === 0) {
        showNotification(
          t('routing_policy.status_codes_required', {
            provider: t(`routing_policy.providers.${provider}`),
          }),
          'error'
        );
        setActiveView('providers');
        return;
      }
      providers[provider] = { ...providers[provider], statusCodes };
    }

    setSaving(true);
    const requestId = runtimeRequestIdRef.current + 1;
    runtimeRequestIdRef.current = requestId;
    try {
      const response = await routingPolicyApi.update({
        global: globalSettings,
        requestProtection: { ...requestProtection, providers },
      });
      if (runtimeRequestIdRef.current !== requestId) return;
      applyConfigResponse(response);
      showNotification(t('routing_policy.save_success'), 'success');
    } catch (error: unknown) {
      if (runtimeRequestIdRef.current !== requestId) return;
      const message = error instanceof Error ? error.message : t('routing_policy.save_failed');
      showNotification(`${t('routing_policy.save_failed')}: ${message}`, 'error');
    } finally {
      setSaving(false);
    }
  };

  const refreshRuntime = useCallback(async () => {
    const requestId = runtimeRequestIdRef.current + 1;
    runtimeRequestIdRef.current = requestId;
    try {
      const response = await routingPolicyApi.get();
      if (runtimeRequestIdRef.current !== requestId) return;
      setData((current) =>
        current ? { ...current, active: response.active, recentEvents: response.recentEvents } : response
      );
    } catch (error: unknown) {
      if (runtimeRequestIdRef.current !== requestId) return;
      const message = error instanceof Error ? error.message : t('routing_policy.load_failed');
      showNotification(`${t('routing_policy.load_failed')}: ${message}`, 'error');
    }
  }, [showNotification, t]);

  const handleRefresh = () => {
    if (!dirty) {
      void loadPolicy();
      return;
    }
    showConfirmation({
      title: t('common.unsaved_changes_title'),
      message: t('config_management.reload_confirm_message'),
      confirmText: t('config_management.reload'),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: loadPolicy,
    });
  };

  const releaseAccount = (authIndex: string, fileName: string) => {
    showConfirmation({
      title: t('routing_policy.release_title'),
      message: t('routing_policy.release_message', { account: fileName || authIndex }),
      confirmText: t('routing_policy.release'),
      cancelText: t('common.cancel'),
      onConfirm: async () => {
        setReleasing(authIndex);
        const requestId = runtimeRequestIdRef.current + 1;
        runtimeRequestIdRef.current = requestId;
        try {
          const response = await routingPolicyApi.release(authIndex);
          if (runtimeRequestIdRef.current !== requestId) return;
          setData((current) =>
            current
              ? { ...current, active: response.active, recentEvents: response.recentEvents }
              : response
          );
          showNotification(t('routing_policy.release_success'), 'success');
        } catch (error: unknown) {
          if (runtimeRequestIdRef.current !== requestId) return;
          const message = error instanceof Error ? error.message : t('routing_policy.release_failed');
          showNotification(`${t('routing_policy.release_failed')}: ${message}`, 'error');
        } finally {
          setReleasing((current) => current === authIndex ? null : current);
        }
      },
    });
  };

  const modeLabel = useMemo(() => {
    if (!requestProtection?.enabled) return t('routing_policy.mode_disabled');
    return requestProtection.mode === 'enforce'
      ? t('routing_policy.mode_enforce')
      : t('routing_policy.mode_observe');
  }, [requestProtection, t]);

  const getStatusText = () => {
    if (disabled) return t('config_management.status_disconnected');
    if (loading) return t('config_management.status_loading');
    if (error) return t('config_management.status_load_failed');
    if (saving) return t('config_management.status_saving');
    if (dirty) return t('config_management.status_dirty');
    return t('config_management.status_loaded');
  };

  const getFloatingStatusText = () => {
    if (!isMobile) return getStatusText();
    if (disabled)
      return t('config_management.status_disconnected_short', { defaultValue: 'Disconnected' });
    if (loading) return t('config_management.status_loading_short', { defaultValue: 'Loading' });
    if (error) return t('config_management.status_load_failed_short', { defaultValue: 'Failed' });
    if (saving) return t('config_management.status_saving_short', { defaultValue: 'Saving' });
    if (dirty) return t('config_management.status_dirty_short', { defaultValue: 'Unsaved' });
    return t('config_management.status_loaded_short', { defaultValue: 'Loaded' });
  };

  const getStatusClass = () => {
    if (error) return configActionStyles.error;
    if (dirty) return configActionStyles.modified;
    if (!loading && !saving) return configActionStyles.saved;
    return '';
  };

  const floatingActions = (
    <div className={configActionStyles.floatingActionContainer} ref={floatingActionsRef}>
      <div className={configActionStyles.floatingActionList}>
        <div
          className={`${configActionStyles.floatingStatus} ${
            isMobile ? configActionStyles.floatingStatusCompact : ''
          } ${getStatusClass()}`}
        >
          {getFloatingStatusText()}
        </div>
        <button
          type="button"
          className={configActionStyles.floatingActionButton}
          onClick={handleRefresh}
          disabled={loading || saving}
          title={t('config_management.reload')}
          aria-label={t('config_management.reload')}
        >
          <IconRefreshCw size={16} />
        </button>
        <button
          type="button"
          className={configActionStyles.floatingActionButton}
          onClick={() => void handleSave()}
          disabled={disabled || loading || saving || !dirty}
          title={t('config_management.save')}
          aria-label={t('config_management.save')}
        >
          <IconCheck size={16} />
          {dirty && <span className={configActionStyles.dirtyDot} aria-hidden="true" />}
        </button>
      </div>
    </div>
  );

  if (loading && !globalSettings) {
    return (
      <div className={styles.container}>
        <div className={styles.loading}>{t('common.loading')}</div>
        {shouldRenderFloatingActions && typeof document !== 'undefined'
          ? createPortal(floatingActions, document.body)
          : null}
      </div>
    );
  }

  if (!globalSettings || !requestProtection) {
    return (
      <div className={styles.container}>
        <div className={styles.emptyState}>
          <p>{t('routing_policy.load_failed')}</p>
        </div>
        {shouldRenderFloatingActions && typeof document !== 'undefined'
          ? createPortal(floatingActions, document.body)
          : null}
      </div>
    );
  }

  return (
    <div className={styles.container}>
      <header className={styles.pageHeader}>
        <div className={styles.headerCopy}>
          <div className={styles.titleRow}>
            <h1 className={styles.pageTitle}>{t('routing_policy.title')}</h1>
            <span
              className={`${styles.modeBadge} ${
                requestProtection.enabled && requestProtection.mode === 'enforce'
                  ? styles.modeEnforce
                  : ''
              }`}
            >
              <IconShield size={14} />
              {modeLabel}
            </span>
          </div>
          <p className={styles.subtitle}>{t('routing_policy.subtitle')}</p>
        </div>
      </header>

      <nav className={styles.viewTabs} aria-label={t('routing_policy.title')}>
        {VIEW_KEYS.map((view) => (
          <button
            key={view}
            type="button"
            className={`${styles.viewTab} ${activeView === view ? styles.viewTabActive : ''}`}
            onClick={() => setActiveView(view)}
          >
            {t(`routing_policy.views.${view}`)}
            {view === 'runtime' && data?.active?.length ? (
              <span className={styles.tabCount}>{data.active.length}</span>
            ) : null}
          </button>
        ))}
      </nav>

      {activeView === 'global' && (
        <div className={styles.sectionStack}>
          <section className={styles.panel}>
            <div className={styles.sectionHeader}>
              <h2>{t('routing_policy.global.routing_title')}</h2>
            </div>
            <div className={styles.fieldGrid}>
              <div className={styles.fieldBlock}>
                <label>{t('routing_policy.global.strategy')}</label>
                <Select
                  value={globalSettings.strategy}
                  options={[
                    { value: 'round-robin', label: t('routing_policy.global.round_robin') },
                    { value: 'fill-first', label: t('routing_policy.global.fill_first') },
                  ]}
                  onChange={(value) =>
                    setGlobal('strategy', value as RoutingPolicyGlobalSettings['strategy'])
                  }
                  disabled={disabled}
                />
              </div>
              <div className={styles.fieldBlock}>
                <label>{t('routing_policy.global.session_affinity_ttl')}</label>
                <Input
                  value={globalSettings.sessionAffinityTTL}
                  onChange={(event) => setGlobal('sessionAffinityTTL', event.target.value)}
                  disabled={disabled || !globalSettings.sessionAffinity}
                  placeholder="1h"
                />
              </div>
              <div className={styles.toggleField}>
                <div>
                  <strong>{t('routing_policy.global.session_affinity')}</strong>
                  <span>{t('routing_policy.global.session_affinity_hint')}</span>
                </div>
                <ToggleSwitch
                  checked={globalSettings.sessionAffinity}
                  onChange={(value) => setGlobal('sessionAffinity', value)}
                  disabled={disabled}
                  ariaLabel={t('routing_policy.global.session_affinity')}
                />
              </div>
              <div className={styles.toggleField}>
                <div>
                  <strong>{t('routing_policy.global.codex_identity_confuse')}</strong>
                  <span>{t('routing_policy.global.codex_identity_confuse_hint')}</span>
                </div>
                <ToggleSwitch
                  checked={globalSettings.codexIdentityConfuse}
                  onChange={(value) => setGlobal('codexIdentityConfuse', value)}
                  disabled={disabled}
                  ariaLabel={t('routing_policy.global.codex_identity_confuse')}
                />
              </div>
            </div>
          </section>

          <section className={styles.panel}>
            <div className={styles.sectionHeader}>
              <h2>{t('routing_policy.global.retry_title')}</h2>
            </div>
            <div className={styles.fieldGridThree}>
              <Input
                label={t('routing_policy.global.request_retry')}
                type="number"
                min={0}
                max={10}
                value={globalSettings.requestRetry}
                onChange={(event) => setGlobal('requestRetry', toNumber(event.target.value))}
                disabled={disabled}
              />
              <Input
                label={t('routing_policy.global.max_retry_credentials')}
                type="number"
                min={0}
                max={100}
                value={globalSettings.maxRetryCredentials}
                onChange={(event) => setGlobal('maxRetryCredentials', toNumber(event.target.value))}
                disabled={disabled}
              />
              <Input
                label={t('routing_policy.global.max_retry_interval')}
                type="number"
                min={0}
                max={3600}
                value={globalSettings.maxRetryInterval}
                onChange={(event) => setGlobal('maxRetryInterval', toNumber(event.target.value))}
                disabled={disabled}
              />
            </div>
          </section>

          <section className={styles.panel}>
            <div className={styles.sectionHeader}>
              <h2>{t('routing_policy.global.cooldown_title')}</h2>
            </div>
            <div className={styles.toggleGrid}>
              <div className={styles.toggleField}>
                <div>
                  <strong>{t('routing_policy.global.cooling_enabled')}</strong>
                  <span>{t('routing_policy.global.cooling_enabled_hint')}</span>
                </div>
                <ToggleSwitch
                  checked={globalSettings.coolingEnabled}
                  onChange={(value) => setGlobal('coolingEnabled', value)}
                  disabled={disabled}
                  ariaLabel={t('routing_policy.global.cooling_enabled')}
                />
              </div>
              <div className={styles.toggleField}>
                <div>
                  <strong>{t('routing_policy.global.save_cooldown_status')}</strong>
                  <span>{t('routing_policy.global.save_cooldown_status_hint')}</span>
                </div>
                <ToggleSwitch
                  checked={globalSettings.saveCooldownStatus}
                  onChange={(value) => setGlobal('saveCooldownStatus', value)}
                  disabled={disabled}
                  ariaLabel={t('routing_policy.global.save_cooldown_status')}
                />
              </div>
            </div>
            <div className={styles.singleField}>
              <Input
                label={t('routing_policy.global.transient_cooldown')}
                hint={t('routing_policy.global.transient_cooldown_hint')}
                type="number"
                min={-1}
                max={86400}
                value={globalSettings.transientErrorCooldownSeconds}
                onChange={(event) =>
                  setGlobal('transientErrorCooldownSeconds', toNumber(event.target.value))
                }
                disabled={disabled}
              />
            </div>
          </section>

          <section className={styles.panel}>
            <div className={styles.sectionHeader}>
              <h2>{t('routing_policy.global.quota_title')}</h2>
            </div>
            <div className={styles.toggleGrid}>
              {(
                [
                  ['quotaSwitchProject', 'quota_switch_project'],
                  ['quotaSwitchPreviewModel', 'quota_switch_preview_model'],
                  ['quotaAntigravityCredits', 'quota_antigravity_credits'],
                ] as const
              ).map(([key, label]) => (
                <div className={styles.toggleField} key={key}>
                  <div>
                    <strong>{t(`routing_policy.global.${label}`)}</strong>
                    <span>{t(`routing_policy.global.${label}_hint`)}</span>
                  </div>
                  <ToggleSwitch
                    checked={globalSettings[key]}
                    onChange={(value) => setGlobal(key, value)}
                    disabled={disabled}
                    ariaLabel={t(`routing_policy.global.${label}`)}
                  />
                </div>
              ))}
            </div>
          </section>
        </div>
      )}

      {activeView === 'providers' && (
        <div className={styles.sectionStack}>
          <section className={styles.protectionBar}>
            <div className={styles.protectionMaster}>
              <div>
                <h2>{t('routing_policy.protection.title')}</h2>
                <p>{t('routing_policy.protection.master_hint')}</p>
              </div>
              <ToggleSwitch
                checked={requestProtection.enabled}
                onChange={(value) => setProtection('enabled', value)}
                disabled={disabled}
                ariaLabel={t('routing_policy.protection.enabled')}
              />
            </div>
            <div className={styles.modeSelect}>
              <label>{t('routing_policy.protection.mode')}</label>
              <Select
                value={requestProtection.mode}
                options={[
                  { value: 'observe', label: t('routing_policy.mode_observe') },
                  { value: 'enforce', label: t('routing_policy.mode_enforce') },
                ]}
                onChange={(value) =>
                  setProtection('mode', value as RoutingRequestProtectionConfig['mode'])
                }
                disabled={disabled || !requestProtection.enabled}
              />
            </div>
          </section>

          <div className={styles.providerGrid}>
            {data?.availableProviders.map((provider) => {
              const policy = requestProtection.providers[provider];
              return (
                <section className={styles.providerCard} key={provider}>
                  <div className={styles.providerHeader}>
                    <div>
                      <h2>{t(`routing_policy.providers.${provider}`)}</h2>
                      <span>{provider}</span>
                    </div>
                    <ToggleSwitch
                      checked={policy.enabled}
                      onChange={(value) => setProviderPolicy(provider, 'enabled', value)}
                      disabled={disabled || !requestProtection.enabled}
                      ariaLabel={t('routing_policy.protection.provider_enabled', {
                        provider: t(`routing_policy.providers.${provider}`),
                      })}
                    />
                  </div>
                  <div className={styles.providerFields}>
                    <Input
                      label={t('routing_policy.protection.status_codes')}
                      hint={t('routing_policy.protection.status_codes_hint')}
                      value={statusCodeInputs[provider]}
                      onChange={(event) => {
                        setStatusCodeInputs((current) => ({
                          ...current,
                          [provider]: event.target.value,
                        }));
                        setDirty(true);
                      }}
                      disabled={disabled || !policy.enabled}
                      placeholder="429, 401, 403"
                    />
                    <div className={styles.compactFields}>
                      <Input
                        label={t('routing_policy.protection.confirmations')}
                        type="number"
                        min={1}
                        max={5}
                        value={policy.confirmations}
                        onChange={(event) =>
                          setProviderPolicy(provider, 'confirmations', toNumber(event.target.value))
                        }
                        disabled={disabled || !policy.enabled}
                      />
                      <Input
                        label={t('routing_policy.protection.confirmation_window')}
                        type="number"
                        min={1}
                        max={86400}
                        value={policy.confirmationWindowSeconds}
                        onChange={(event) =>
                          setProviderPolicy(
                            provider,
                            'confirmationWindowSeconds',
                            toNumber(event.target.value)
                          )
                        }
                        disabled={disabled || !policy.enabled}
                      />
                      <Input
                        label={t('routing_policy.protection.fallback_minutes')}
                        type="number"
                        min={0}
                        max={10080}
                        value={policy.fallbackDisableMinutes}
                        onChange={(event) =>
                          setProviderPolicy(
                            provider,
                            'fallbackDisableMinutes',
                            toNumber(event.target.value)
                          )
                        }
                        disabled={disabled || !policy.enabled || !policy.autoEnable}
                      />
                    </div>
                    <div className={styles.providerToggles}>
                      <div className={styles.toggleField}>
                        <div>
                          <strong>{t('routing_policy.protection.quota_evidence')}</strong>
                          <span>{t('routing_policy.protection.quota_evidence_hint')}</span>
                        </div>
                        <ToggleSwitch
                          checked={policy.requireQuotaEvidence}
                          onChange={(value) =>
                            setProviderPolicy(provider, 'requireQuotaEvidence', value)
                          }
                          disabled={disabled || !policy.enabled}
                          ariaLabel={t('routing_policy.protection.quota_evidence')}
                        />
                      </div>
                      <div className={styles.toggleField}>
                        <div>
                          <strong>{t('routing_policy.protection.auto_enable')}</strong>
                          <span>{t('routing_policy.protection.auto_enable_hint')}</span>
                        </div>
                        <ToggleSwitch
                          checked={policy.autoEnable}
                          onChange={(value) => setProviderPolicy(provider, 'autoEnable', value)}
                          disabled={disabled || !policy.enabled}
                          ariaLabel={t('routing_policy.protection.auto_enable')}
                        />
                      </div>
                    </div>
                  </div>
                </section>
              );
            })}
          </div>
          {!data?.availableProviders.length && (
            <div className={styles.emptyState}>
              <p>{t('routing_policy.protection.no_providers')}</p>
            </div>
          )}
        </div>
      )}

      {activeView === 'runtime' && (
        <div className={styles.sectionStack}>
          <section className={styles.panel}>
            <div className={styles.sectionHeaderWithAction}>
              <div>
                <h2>{t('routing_policy.runtime.active_title')}</h2>
                <p>{t('routing_policy.runtime.active_count', { count: data?.active?.length ?? 0 })}</p>
              </div>
              <Button variant="secondary" size="sm" onClick={() => void refreshRuntime()}>
                <IconRefreshCw size={15} /> {t('common.refresh')}
              </Button>
            </div>
            {data?.active?.length ? (
              <div className={`${styles.tableScroller} ${styles.runtimeTableScroller}`}>
                <table className={`${styles.table} ${styles.runtimeTable}`}>
                  <colgroup>
                    <col className={styles.runtimeProviderColumn} />
                    <col className={styles.runtimeAccountColumn} />
                    <col className={styles.runtimeStatusColumn} />
                    <col className={styles.runtimeTimeColumn} />
                    <col className={styles.runtimeReleaseColumn} />
                    <col className={styles.runtimeActionsColumn} />
                  </colgroup>
                  <thead>
                    <tr>
                      <th>{t('routing_policy.runtime.provider')}</th>
                      <th>{t('routing_policy.runtime.account')}</th>
                      <th>{t('routing_policy.runtime.status_code')}</th>
                      <th>{t('routing_policy.runtime.triggered_at')}</th>
                      <th>{t('routing_policy.runtime.release_at')}</th>
                      <th className={styles.runtimeActionHeader}>{t('routing_policy.runtime.actions')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.active.map((account) => (
                      <tr key={account.authIndex}>
                        <td><span className={styles.providerTag}>{account.provider}</span></td>
                        <td>
                          <strong className={styles.accountName} title={account.fileName || account.authIndex}>
                            {account.fileName || account.authIndex}
                          </strong>
                          <span className={styles.accountIndex}>{account.authIndex}</span>
                        </td>
                        <td>
                          <button
                            type="button"
                            className={styles.runtimeStatusButton}
                            onClick={() => setSelectedRuntimeDetail({ kind: 'active', item: account })}
                            title={t('routing_policy.runtime.details_click_hint')}
                            aria-label={t('routing_policy.runtime.details_click_hint')}
                          >
                            <span
                              className={`${styles.runtimeStatusBadge} ${runtimeStatusToneClass(account.statusCode)}`}
                            >
                              {account.statusCode || '-'}
                            </span>
                          </button>
                        </td>
                        <td className={styles.runtimeTimeCell}>{formatTimestamp(account.triggeredAt, i18n.language, '-')}</td>
                        <td className={styles.runtimeTimeCell}>{formatTimestamp(account.releaseAt, i18n.language, t('routing_policy.runtime.manual'))}</td>
                        <td className={styles.runtimeActionCell}>
                          <Button
                            variant="secondary"
                            size="sm"
                            className={styles.runtimeReleaseButton}
                            loading={releasing === account.authIndex}
                            onClick={() => releaseAccount(account.authIndex, account.fileName)}
                          >
                            {t('routing_policy.release')}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className={styles.inlineEmpty}>{t('routing_policy.runtime.no_active')}</div>
            )}
          </section>

          <section className={styles.panel}>
            <div className={styles.sectionHeaderWithAction}>
              <div>
                <h2>{t('routing_policy.runtime.events_title')}</h2>
                <p>{t('routing_policy.runtime.events_count', { count: data?.recentEvents?.length ?? 0 })}</p>
              </div>
            </div>
            {data?.recentEvents?.length ? (
              <div className={`${styles.tableScroller} ${styles.runtimeTableScroller}`}>
                <table className={`${styles.table} ${styles.runtimeTable}`}>
                  <colgroup>
                    <col className={styles.runtimeTimeColumn} />
                    <col className={styles.runtimeProviderColumn} />
                    <col className={styles.runtimeEventAccountColumn} />
                    <col className={styles.runtimeStatusColumn} />
                    <col className={styles.runtimeEventActionColumn} />
                    <col className={styles.runtimeConfirmationColumn} />
                  </colgroup>
                  <thead>
                    <tr>
                      <th>{t('routing_policy.runtime.time')}</th>
                      <th>{t('routing_policy.runtime.provider')}</th>
                      <th>{t('routing_policy.runtime.account')}</th>
                      <th>{t('routing_policy.runtime.status_code')}</th>
                      <th>{t('routing_policy.runtime.action')}</th>
                      <th>{t('routing_policy.runtime.confirmation')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.recentEvents.map((event) => (
                      <tr key={event.id}>
                        <td className={styles.runtimeTimeCell}>{formatTimestamp(event.triggeredAt, i18n.language, '-')}</td>
                        <td><span className={styles.providerTag}>{event.provider}</span></td>
                        <td>
                          <strong
                            className={styles.accountName}
                            title={event.fileName || event.authIndex || event.authId || '-'}
                          >
                            {event.fileName || event.authIndex || event.authId || '-'}
                          </strong>
                          {event.fileName && event.authIndex ? (
                            <span className={styles.accountIndex}>{event.authIndex}</span>
                          ) : null}
                        </td>
                        <td>
                          <button
                            type="button"
                            className={styles.runtimeStatusButton}
                            onClick={() => setSelectedRuntimeDetail({ kind: 'event', item: event })}
                            title={t('routing_policy.runtime.details_click_hint')}
                            aria-label={t('routing_policy.runtime.details_click_hint')}
                          >
                            <span
                              className={`${styles.runtimeStatusBadge} ${runtimeStatusToneClass(event.statusCode)}`}
                            >
                              {event.statusCode || '-'}
                            </span>
                          </button>
                        </td>
                        <td>
                          <span className={`${styles.actionTag} ${styles[`action_${event.action}`] ?? ''}`}>
                            {t(`routing_policy.actions.${event.action}`, { defaultValue: event.action })}
                          </span>
                        </td>
                        <td>{event.required ? `${event.count}/${event.required}` : '-'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className={styles.inlineEmpty}>{t('routing_policy.runtime.no_events')}</div>
            )}
          </section>
        </div>
      )}
      <Modal
        open={Boolean(selectedRuntimeDetail)}
        onClose={() => setSelectedRuntimeDetail(null)}
        title={t('routing_policy.runtime.details_title')}
        width={720}
        className={styles.runtimeDetailModal}
        footer={(
          <div className={styles.runtimeDetailModalActions}>
            <Button variant="primary" size="sm" onClick={() => setSelectedRuntimeDetail(null)}>
              {t('common.close')}
            </Button>
          </div>
        )}
      >
        {selectedRuntimeDetail ? (
          <RoutingRuntimeDetailPanel
            detail={selectedRuntimeDetail}
            t={t}
            language={i18n.language}
          />
        ) : null}
      </Modal>
      {shouldRenderFloatingActions && typeof document !== 'undefined'
        ? createPortal(floatingActions, document.body)
        : null}
    </div>
  );
}
