import { useQuotaSummaries } from './useQuotaSummaries';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  IconAlertTriangle,
	IconCheckCircle2,
	IconInfo,
  IconKey,
  IconPlus,
	IconShield,
  IconTrash2,
} from '@/components/ui/icons';
import { useAuthStore, useNotificationStore } from '@/stores';
import { ProFeatureHeader } from '@/pro/shared/ProFeatureHeader';
import { ProTaskDialog, ProWorkspaceSheet } from '@/pro/shared/ProSurface';
import { buildMonitoringUsageLocationState } from '@/pro/shared/monitoringNavigation';
import {
  apiKeyPolicyApi,
  apiKeyPolicyErrorCode,
  apiKeyPolicyErrorTranslationKey,
  buildAPIKeyQuotaTimezoneOptions,
  cloneProfileInput,
  formatAPIKeyPolicyTimestamp,
  isAPIKeyPolicyUnsupported,
  resolveMappingTargetModels,
  resolveModelsForProviders,
  supportsAPIKeyProfileEnforcementToggle,
  supportsAPIKeyQuota,
  supportsAPIKeyQuotaOverview,
  supportsAPIKeyQuotaTimezone,
  supportsOptionalAPIKeyProfile,
  supportsAPIKeyPolicyUsageTarget,
  updateProfileProviders,
  validateProfileInput,
  type APIKeyPolicy,
  type APIKeyPolicyBinding,
  type APIKeyPolicyDeletePreview,
  type APIKeyPolicySnapshot,
  type APIKeyPolicyStatus,
  type APIKeyQuotaInput,
  type APIKeyQuotaPeriod,
  type APIKeyQuotaSummary,
  type APIKeyProfileInput,
} from './apiKeyPolicy';
import styles from './APIKeyPolicyPage.module.scss';

type BindingFilter = 'all' | 'unconfigured' | 'configured' | 'orphaned';
type PageView = 'policies' | 'quotas';
type QuotaFilter = 'all' | 'attention' | 'exhausted' | 'blocked' | 'inactive' | 'disabled';
type QuotaVisualState = 'inactive' | 'disabled' | 'available' | 'warning' | 'exhausted' | 'blocked' | 'unknown';
type CapabilityState = 'checking' | 'ready' | 'unsupported' | 'error';
type WorkspaceTarget =
  | { kind: 'create'; binding: APIKeyPolicyBinding }
  | { kind: 'policy'; policy: APIKeyPolicy; readOnly: boolean };

interface WorkspaceDraft {
  displayName: string;
  profileEnabled: boolean;
  profileId: string;
  profile: APIKeyProfileInput;
  isNewProfile: boolean;
  quota: APIKeyQuotaInput | null;
}

const emptyProfile = (): APIKeyProfileInput => ({
  name: '',
  providers: [],
  models: [],
  mappings: [],
});

const defaultQuotaPeriod = (): APIKeyQuotaPeriod => ({ type: 'all_time' });

const quotaPeriodSignature = (period: APIKeyQuotaPeriod | undefined): string => {
  const normalized = period ?? defaultQuotaPeriod();
  return JSON.stringify(normalized.type === 'calendar_duration'
    ? { ...normalized, timezone: normalized.timezone?.trim() || 'UTC' }
    : normalized);
};

const invalidPositiveInteger = (value: number | undefined): boolean =>
  value !== undefined && (!Number.isSafeInteger(value) || value <= 0);

const validIanaTimezone = (value: string | undefined): boolean => {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value?.trim() || 'UTC' }).format(0);
    return true;
  } catch {
    return false;
  }
};

const formatQuotaCost = (value: number): string => value.toFixed(6).replace(/\.?0+$/, '');

const quotaRatio = (used: number, limit: number | undefined): number | null =>
  limit === undefined || limit <= 0 ? null : Math.max(0, used / limit);

const quotaMaximumRatio = (summary: APIKeyQuotaSummary | undefined): number => {
  const quota = summary?.quota;
  if (!quota?.enabled) return 0;
  return Math.max(
    quotaRatio(quota.usage.requestsUsed, quota.requests) ?? 0,
    quotaRatio(quota.usage.totalTokensUsed, quota.totalTokens) ?? 0,
    quotaRatio(quota.usage.costUsed, quota.cost) ?? 0,
  );
};

const quotaVisualState = (
  summary: APIKeyQuotaSummary | undefined,
  takeoverActive: boolean,
  quotaConfigured = false,
): QuotaVisualState => {
  if (quotaConfigured && !takeoverActive) return 'inactive';
  // Missing summaries mean that no successful snapshot exists for this policy;
  // do not present that failure as a real disabled quota with zero usage.
  if (!summary) return quotaConfigured ? 'unknown' : 'disabled';
  if (!summary.quota?.enabled || summary.admissionState === 'disabled') return 'disabled';
  if (summary.admissionState === 'blocked') return 'blocked';
  if (summary.admissionState === 'exhausted') return 'exhausted';
  return quotaMaximumRatio(summary) >= 0.8 ? 'warning' : 'available';
};

const formatQuotaNumber = (value: number): string => new Intl.NumberFormat(undefined, {
  notation: Math.abs(value) >= 10000 ? 'compact' : 'standard',
  maximumFractionDigits: 1,
}).format(value);

const updateQuotaLimit = (
  quota: APIKeyQuotaInput | null,
  field: 'requests' | 'totalTokens' | 'cost',
  value: number | undefined,
): APIKeyQuotaInput => {
  const next: APIKeyQuotaInput = quota
    ? { ...quota }
    : { enabled: true, period: defaultQuotaPeriod() };
  if (value === undefined) delete next[field];
  else next[field] = value;
  return next;
};

const quotaInputFromPolicy = (policy: APIKeyPolicy): APIKeyQuotaInput | null => policy.quota ? {
  enabled: policy.quota.enabled,
  ...(policy.quota.requests !== undefined ? { requests: policy.quota.requests } : {}),
  ...(policy.quota.totalTokens !== undefined ? { totalTokens: policy.quota.totalTokens } : {}),
  ...(policy.quota.cost !== undefined ? { cost: policy.quota.cost } : {}),
  period: policy.quota.period ?? defaultQuotaPeriod(),
} : null;

const workspaceDraftFromTarget = (
  target: WorkspaceTarget,
  profileId?: string,
  profileRequired = false,
): WorkspaceDraft => {
  if (target.kind === 'create') {
    return {
      displayName: '', profileEnabled: profileRequired, profileId: '',
      profile: emptyProfile(), isNewProfile: profileRequired, quota: null,
    };
  }
  const selected =
    target.policy.profiles.find((profile) => profile.id === profileId) ??
    target.policy.profiles.find((profile) => profile.id === target.policy.activeProfileId) ??
    target.policy.profiles[0];
  return {
    displayName: target.policy.displayName,
    profileEnabled: target.policy.profileEnabled,
    profileId: selected?.id ?? '',
    profile: selected ? cloneProfileInput(selected) : emptyProfile(),
    isNewProfile: false,
    quota: quotaInputFromPolicy(target.policy),
  };
};

const profileSignature = (profile: APIKeyProfileInput): string =>
  JSON.stringify({
    name: profile.name.trim(),
    providers: [...profile.providers].sort(),
    models: [...profile.models].sort(),
    mappings: profile.mappings.map(({ source, target }) => ({
      source: source.trim(),
      target: target.trim(),
    })),
  });

const workspaceIsDirty = (
  target: WorkspaceTarget | null,
  draft: WorkspaceDraft | null,
): boolean => {
  if (!target || !draft) return false;
  if (target.kind === 'create') {
    return Boolean(
      draft.displayName.trim() ||
        (draft.profileEnabled && (
          draft.profile.name.trim() ||
          draft.profile.providers.length ||
          draft.profile.models.length ||
          draft.profile.mappings.length
        )) || draft.quota !== null,
    );
  }
  if (draft.profileEnabled && draft.isNewProfile) return true;
  const persisted = target.policy.profiles.find((profile) => profile.id === draft.profileId);
  return (
    draft.displayName !== target.policy.displayName ||
    draft.profileEnabled !== target.policy.profileEnabled ||
    (draft.profileEnabled && (!persisted ||
      profileSignature(draft.profile) !== profileSignature(persisted)))
    || JSON.stringify(draft.quota) !== JSON.stringify(quotaInputFromPolicy(target.policy))
  );
};

function ChoiceList({
  title,
  values,
  selected,
  onChange,
  disabled,
  emptyLabel,
  allLabel,
  emptySelectionHint,
  unavailableLabel,
  removeUnavailableLabel,
}: {
  title: string;
  values: string[];
  selected: string[];
  onChange: (values: string[]) => void;
  disabled: boolean;
  emptyLabel: string;
  allLabel: string;
  emptySelectionHint: string;
  unavailableLabel: string;
  removeUnavailableLabel: (value: string) => string;
}) {
  const [search, setSearch] = useState('');
  const query = search.trim().toLowerCase();
  const filtered = values.filter((value) => !query || value.toLowerCase().includes(query));
  const unavailableSelected = selected.filter((value) => !values.includes(value));
  return (
    <section className={styles.choiceSection}>
      <div className={styles.choiceHeader}>
        <strong>{title}</strong>
        <span>{selected.length > 0 ? selected.length : allLabel}</span>
      </div>
      <p className={styles.choiceHint}>{emptySelectionHint}</p>
      {unavailableSelected.length ? (
        <div className={styles.unavailableSelections} role="note">
          <span>{unavailableLabel}</span>
          <div>
            {unavailableSelected.map((value) => (
              <button
                key={value}
                type="button"
                disabled={disabled}
                aria-label={removeUnavailableLabel(value)}
                title={removeUnavailableLabel(value)}
                onClick={() => onChange(selected.filter((item) => item !== value))}
              >
                <span>{value}</span>
                <span aria-hidden="true">×</span>
              </button>
            ))}
          </div>
        </div>
      ) : null}
      <input
        className="input"
        value={search}
        onChange={(event) => setSearch(event.target.value)}
        placeholder={emptyLabel}
        disabled={disabled}
        aria-label={emptyLabel}
      />
      <div className={styles.choiceList}>
        {filtered.length ? (
          filtered.map((value) => (
            <label className={styles.choiceItem} key={value}>
              <input
                type="checkbox"
                checked={selected.includes(value)}
                disabled={disabled}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? [...selected, value]
                      : selected.filter((item) => item !== value),
                  )
                }
              />
              <span>{value}</span>
            </label>
          ))
        ) : (
          <span className={styles.choiceEmpty}>{emptyLabel}</span>
        )}
      </div>
    </section>
  );
}

function PolicyBadge({ state, children }: { state: string; children: ReactNode }) {
  return <span className={`${styles.badge} ${styles[`badge_${state}`] ?? ''}`}>{children}</span>;
}

function QuotaMetric({
  label,
  used,
  limit,
  cost = false,
}: {
  label: string;
  used?: number;
  limit?: number;
  cost?: boolean;
}) {
  const { t } = useTranslation();
  const unavailable = used === undefined;
  const ratio = unavailable ? null : quotaRatio(used, limit);
  const format = cost ? (value: number) => `$${formatQuotaCost(value)}` : formatQuotaNumber;
  const detail = unavailable
    ? t('api_key_policy.quota_overview.snapshot_unavailable')
    : limit === undefined
      ? t('api_key_policy.quota_overview.unlimited')
      : t('api_key_policy.quota_overview.remaining', { value: format(Math.max(limit - used, 0)) });
  return (
    <div className={styles.quotaMetric} role="group" aria-label={label}>
      <div><span>{label}</span><strong>{unavailable ? '—' : `${format(used)} / ${limit === undefined ? '∞' : format(limit)}`}</strong></div>
      <div className={styles.quotaProgress} aria-hidden="true">
        {!unavailable ? <span style={{ width: `${Math.min((ratio ?? 0) * 100, 100)}%` }} /> : null}
      </div>
      <small>{detail}</small>
    </div>
  );
}

export function APIKeyPolicyPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const [snapshot, setSnapshot] = useState<APIKeyPolicySnapshot | null>(null);
	const [takeoverStatus, setTakeoverStatus] = useState<APIKeyPolicyStatus | null>(null);
  const [capability, setCapability] = useState<CapabilityState>('checking');
  const [loadError, setLoadError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState<BindingFilter>('all');
  const [pageView, setPageView] = useState<PageView>('policies');
  const [quotaFilter, setQuotaFilter] = useState<QuotaFilter>('all');
  const [search, setSearch] = useState('');
  const [workspaceTarget, setWorkspaceTarget] = useState<WorkspaceTarget | null>(null);
  const [draft, setDraft] = useState<WorkspaceDraft | null>(null);
  const [saving, setSaving] = useState(false);
	const [takeoverOpen, setTakeoverOpen] = useState(false);
	const [takeoverBusy, setTakeoverBusy] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [dangerPolicy, setDangerPolicy] = useState<APIKeyPolicy | null>(null);
  const [dangerKind, setDangerKind] = useState<'policy' | 'profile' | 'orphaned' | null>(null);
  const [dangerBusy, setDangerBusy] = useState(false);
  const [deletePreview, setDeletePreview] = useState<APIKeyPolicyDeletePreview | null>(null);
  const [quotaBusy, setQuotaBusy] = useState(false);
  const requestRevisionRef = useRef(0);
  const saveRevisionRef = useRef(0);
  const draftRevisionRef = useRef(0);
  const savingRef = useRef(false);
  const dangerRevisionRef = useRef(0);
  const dangerBusyRef = useRef(false);
  const quotaRevisionRef = useRef(0);
  const quotaBusyRef = useRef(false);
  const dirty = workspaceIsDirty(workspaceTarget, draft);
  const quotaSupported = Boolean(snapshot && supportsAPIKeyQuota(snapshot.capabilities));
  const quotaOverviewSupported = Boolean(snapshot && supportsAPIKeyQuotaOverview(snapshot.capabilities));
  const quotaTimezoneSupported = Boolean(snapshot && supportsAPIKeyQuotaTimezone(snapshot.capabilities));
  const optionalProfileSupported = Boolean(
    snapshot && supportsOptionalAPIKeyProfile(snapshot.capabilities),
  );
  const profileEnforcementToggleSupported = Boolean(
    snapshot && supportsAPIKeyProfileEnforcementToggle(snapshot.capabilities),
  );
  const quotaWorkspaceOpen = workspaceTarget?.kind === 'policy' && Boolean(workspaceTarget.policy.quota);

  const errorMessage = useCallback((error: unknown): string => {
    const key = apiKeyPolicyErrorTranslationKey(error);
    if (key && i18n.exists(key)) return t(key);
    return error instanceof Error ? error.message : String(error ?? '');
  }, [i18n, t]);

  const openPolicyDeletePreview = useCallback(async (policy: APIKeyPolicy) => {
    if (dangerBusyRef.current) return;
    const revision = ++dangerRevisionRef.current;
    dangerBusyRef.current = true;
    setDangerBusy(true);
    setDeletePreview(null);
    try {
      const preview = await apiKeyPolicyApi.deletePreview(policy.id);
      if (revision !== dangerRevisionRef.current) return;
      if (preview.policyId !== policy.id || preview.version !== policy.version) {
        throw new Error(t('api_key_policy.delete_preview_stale'));
      }
      setDeletePreview(preview);
      setDangerPolicy(policy);
      setDangerKind('policy');
    } catch (error) {
      if (revision !== dangerRevisionRef.current) return;
      showNotification(errorMessage(error), 'error');
    } finally {
      if (revision === dangerRevisionRef.current) {
        dangerBusyRef.current = false;
        setDangerBusy(false);
      }
    }
  }, [errorMessage, showNotification, t]);

  const load = useCallback(async () => {
    if (connectionStatus !== 'connected') {
      setLoading(false);
      setCapability('checking');
		setTakeoverStatus(null);
      return;
    }
    const revision = ++requestRevisionRef.current;
    setLoading(true);
    setLoadError(null);
    try {
		const [statusResult, snapshotResult] = await Promise.allSettled([
			apiKeyPolicyApi.status(),
			apiKeyPolicyApi.snapshot(),
		]);
      if (revision !== requestRevisionRef.current) return;
		if (statusResult.status === 'fulfilled') {
			setTakeoverStatus(statusResult.value);
		}
		if (snapshotResult.status === 'rejected') throw snapshotResult.reason;
		if (statusResult.status === 'rejected') throw statusResult.reason;
		setSnapshot(snapshotResult.value);
      setCapability('ready');
    } catch (error) {
      if (revision !== requestRevisionRef.current) return;
      if (isAPIKeyPolicyUnsupported(error)) {
        setCapability('unsupported');
        setSnapshot(null);
      } else {
        setCapability('error');
        setLoadError(error);
      }
    } finally {
      if (revision === requestRevisionRef.current) setLoading(false);
    }
  }, [connectionStatus]);

  useEffect(() => {
    void load();
    return () => {
      requestRevisionRef.current += 1;
      saveRevisionRef.current += 1;
      dangerRevisionRef.current += 1;
      quotaRevisionRef.current += 1;
      savingRef.current = false;
      dangerBusyRef.current = false;
      quotaBusyRef.current = false;
    };
  }, [load]);

  const {
    quotaSummaries, quotaSnapshotAt, quotaLoading, quotaError,
    loadQuotaSummaries, refreshQuotaAfterMutation,
  } = useQuotaSummaries(
    quotaOverviewSupported && connectionStatus === 'connected',
    pageView === 'quotas' || quotaWorkspaceOpen,
    errorMessage,
  );

  const refreshPage = useCallback(async () => {
    await Promise.all([load(), loadQuotaSummaries()]);
  }, [load, loadQuotaSummaries]);

  useEffect(() => {
    if (!quotaOverviewSupported || connectionStatus !== 'connected') setPageView('policies');
  }, [connectionStatus, quotaOverviewSupported]);

  const openWorkspace = useCallback(
    (target: WorkspaceTarget, profileId?: string) => {
      if (!snapshot) return;
      quotaRevisionRef.current += 1;
      quotaBusyRef.current = false;
      setQuotaBusy(false);
      setWorkspaceTarget(target);
      draftRevisionRef.current += 1;
      setDraft(workspaceDraftFromTarget(
        target,
        profileId,
        target.kind === 'create' && !supportsOptionalAPIKeyProfile(snapshot.capabilities),
      ));
      setConflict(false);
    },
    [snapshot],
  );

  const openUsage = useCallback(async (
    binding: APIKeyPolicyBinding,
    profile?: { id: string; name: string },
  ) => {
    try {
      const { apiKeyHash } = await apiKeyPolicyApi.usageTarget(binding.keyRef);
      navigate('/monitoring#request-events', {
        state: buildMonitoringUsageLocationState({
          apiKeyHash,
          apiKeyLabel: binding.maskedKey,
          ...(profile ? { profileId: profile.id, profileName: profile.name } : {}),
        }),
      });
    } catch (error) {
      if (apiKeyPolicyErrorCode(error) === 'api_key_reference_stale') {
        await load();
      }
      showNotification(errorMessage(error), 'error');
    }
  }, [errorMessage, load, navigate, showNotification]);

  const closeWorkspace = useCallback(() => {
    saveRevisionRef.current += 1;
    draftRevisionRef.current += 1;
    quotaRevisionRef.current += 1;
    savingRef.current = false;
    dangerRevisionRef.current += 1;
    dangerBusyRef.current = false;
    quotaBusyRef.current = false;
    setSaving(false);
    setDangerBusy(false);
    setQuotaBusy(false);
    setDeletePreview(null);
    setWorkspaceTarget(null);
    setDraft(null);
    setConflict(false);
  }, []);

	const requestWorkspaceClose = useCallback(async () => {
		if (dirty && !window.confirm(t('api_key_policy.discard_confirm'))) return;
		closeWorkspace();
	}, [closeWorkspace, dirty, t]);

	const toggleTakeover = useCallback(async () => {
		if (!takeoverStatus || takeoverBusy) return;
		const enabled = !takeoverStatus.takeoverEnabled;
		setTakeoverBusy(true);
		try {
			const status = await apiKeyPolicyApi.setTakeover(enabled, takeoverStatus);
			setTakeoverStatus(status);
			setTakeoverOpen(false);
			showNotification(
				status.takeoverEnabled
					? t('api_key_policy.takeover_started')
					: t('api_key_policy.takeover_stopped'),
				'success',
			);
		} catch (error) {
			if (apiKeyPolicyErrorCode(error) === 'api_key_policy_state_changed') {
				setTakeoverOpen(false);
				await load();
			}
			showNotification(errorMessage(error), 'error');
		} finally {
			setTakeoverBusy(false);
		}
	}, [errorMessage, load, showNotification, t, takeoverBusy, takeoverStatus]);

  const updateDraft = useCallback((updater: (current: WorkspaceDraft) => WorkspaceDraft) => {
    draftRevisionRef.current += 1;
    setDraft((current) => current ? updater(current) : current);
    setConflict(false);
  }, []);

  const replacePolicyInSnapshot = useCallback((policy: APIKeyPolicy) => {
    setSnapshot((current) => {
      if (!current) return current;
      return {
        ...current,
        bindings: {
          ...current.bindings,
          items: current.bindings.items.map((binding) =>
            binding.policy?.id === policy.id
              ? { ...binding, state: policy.state, policy }
              : binding,
          ),
          orphaned: current.bindings.orphaned.map((item) => (item.id === policy.id ? policy : item)),
        },
      };
    });
  }, []);

  const reloadWorkspace = useCallback(async () => {
    if (!workspaceTarget || workspaceTarget.kind !== 'policy' || !snapshot) return;
    const revision = ++saveRevisionRef.current;
    savingRef.current = true;
    setSaving(true);
    try {
      const policy = await apiKeyPolicyApi.get(workspaceTarget.policy.id);
      if (revision !== saveRevisionRef.current) return;
      const target = { kind: 'policy' as const, policy, readOnly: policy.state === 'orphaned' };
      setWorkspaceTarget(target);
      replacePolicyInSnapshot(policy);
      setConflict(false);
    } catch (error) {
      showNotification(errorMessage(error), 'error');
    } finally {
      if (revision === saveRevisionRef.current) {
        savingRef.current = false;
        setSaving(false);
      }
    }
  }, [errorMessage, replacePolicyInSnapshot, showNotification, snapshot, workspaceTarget]);

  const validateDraft = useCallback((validateProfile: boolean): boolean => {
    if (!snapshot || !draft) return false;
    if (!draft.displayName.trim()) {
      showNotification(t('api_key_policy.validation.display_name'), 'warning');
      return false;
    }
    if (validateProfile && draft.profileEnabled) {
      const problem = validateProfileInput(draft.profile, snapshot.catalog);
      if (problem) {
        showNotification(t(`api_key_policy.validation.${problem}`), 'warning');
        return false;
      }
    }
		if (quotaSupported && draft.quota?.enabled && draft.quota.requests === undefined && draft.quota.totalTokens === undefined && draft.quota.cost === undefined) {
			showNotification(t('api_key_policy.validation.quota'), 'warning');
			return false;
		}
		if (quotaSupported && (invalidPositiveInteger(draft.quota?.requests) ||
			invalidPositiveInteger(draft.quota?.totalTokens) ||
			(draft.quota?.cost !== undefined && (!Number.isFinite(draft.quota.cost) || draft.quota.cost <= 0)) ||
			(draft.quota?.period.type === 'past_duration' && invalidPositiveInteger(draft.quota.period.value)))) {
			showNotification(t('api_key_policy.validation.quota'), 'warning');
			return false;
		}
		if (quotaSupported && quotaTimezoneSupported && draft.quota?.period.type === 'calendar_duration' && !validIanaTimezone(draft.quota.period.timezone)) {
			showNotification(t('api_key_policy.validation.quota_timezone'), 'warning');
			return false;
		}
    return true;
  }, [draft, quotaSupported, quotaTimezoneSupported, showNotification, snapshot, t]);

  const saveWorkspace = useCallback(async () => {
    if (!workspaceTarget || !draft || savingRef.current) return;
    const persisted = workspaceTarget.kind === 'policy'
      ? workspaceTarget.policy.profiles.find((item) => item.id === draft.profileId)
      : undefined;
    const profileEnabledChanged = workspaceTarget.kind === 'policy' &&
      draft.profileEnabled !== workspaceTarget.policy.profileEnabled;
    const changedProfile = draft.profileEnabled && (
      workspaceTarget.kind === 'create' || draft.isNewProfile || !persisted ||
      profileSignature(persisted) !== profileSignature(draft.profile)
    );
    if (!validateDraft(changedProfile)) return;
    const persistedQuota = workspaceTarget.kind === 'policy' ? quotaInputFromPolicy(workspaceTarget.policy) : null;
    const periodChanged = Boolean(
      persistedQuota && draft.quota &&
      quotaPeriodSignature(persistedQuota.period) !== quotaPeriodSignature(draft.quota.period),
    );
    if (periodChanged && !window.confirm(t('api_key_policy.quota_period_change_confirm'))) return;
    const revision = ++saveRevisionRef.current;
    const submittedDraftRevision = draftRevisionRef.current;
    savingRef.current = true;
    setSaving(true);
    setConflict(false);
    try {
      let policy: APIKeyPolicy;
      if (workspaceTarget.kind === 'create') {
        policy = await apiKeyPolicyApi.create(
          workspaceTarget.binding.keyRef,
          draft.displayName.trim(),
          draft.profileEnabled ? draft.profile : undefined,
          quotaSupported ? draft.quota : undefined,
        );
      } else {
        policy = await apiKeyPolicyApi.updateWorkspace(
          workspaceTarget.policy.id,
          draft.displayName.trim(),
          workspaceTarget.policy.version,
          draft.profileId,
          draft.profileEnabled && changedProfile ? draft.profile : undefined,
          draft.isNewProfile,
          quotaSupported ? draft.quota : undefined,
          profileEnabledChanged ? draft.profileEnabled : undefined,
          profileEnabledChanged && draft.profileEnabled ? draft.profileId : undefined,
        );
      }
      if (revision !== saveRevisionRef.current) return;
      replacePolicyInSnapshot(policy);
      const target = { kind: 'policy' as const, policy, readOnly: false };
      setWorkspaceTarget(target);
      const priorProfileIDs = workspaceTarget.kind === 'policy'
        ? new Set(workspaceTarget.policy.profiles.map((item) => item.id))
        : new Set<string>();
      const savedProfile = draft.profileEnabled && draft.isNewProfile
        ? policy.profiles.find((item) => !priorProfileIDs.has(item.id))
        : policy.profiles.find((item) => item.id === draft.profileId);
      if (submittedDraftRevision === draftRevisionRef.current) {
        draftRevisionRef.current += 1;
        setDraft(workspaceDraftFromTarget(target, savedProfile?.id));
      }
      showNotification(t('api_key_policy.saved'), 'success');
      await Promise.all([load(), refreshQuotaAfterMutation()]);
    } catch (error) {
      if (revision !== saveRevisionRef.current) return;
      if (apiKeyPolicyErrorCode(error) === 'config_version_conflict') {
        setConflict(true);
      } else if (apiKeyPolicyErrorCode(error) === 'api_key_reference_stale') {
        showNotification(t('api_key_policy.key_ref_stale'), 'warning');
        closeWorkspace();
        await load();
      } else {
        showNotification(errorMessage(error), 'error');
      }
    } finally {
      if (revision === saveRevisionRef.current) {
        savingRef.current = false;
        setSaving(false);
      }
    }
  }, [closeWorkspace, draft, errorMessage, load, quotaSupported, refreshQuotaAfterMutation, replacePolicyInSnapshot, showNotification, t, validateDraft, workspaceTarget]);

  const resetQuota = useCallback(async () => {
    if (!quotaSupported || !workspaceTarget || workspaceTarget.kind !== 'policy' || quotaBusyRef.current || !workspaceTarget.policy.quota) return;
    if (!window.confirm(t('api_key_policy.quota_reset_confirm'))) return;
    const revision = ++quotaRevisionRef.current;
    const policyId = workspaceTarget.policy.id;
    const selectedProfileId = draft?.profileId;
    const submittedDraftRevision = draftRevisionRef.current;
    quotaBusyRef.current = true;
    setQuotaBusy(true);
    try {
      const policy = await apiKeyPolicyApi.resetQuota(policyId, workspaceTarget.policy.version);
      if (revision !== quotaRevisionRef.current) return;
      replacePolicyInSnapshot(policy);
      setWorkspaceTarget((current) => {
        if (!current || current.kind !== 'policy' || current.policy.id !== policyId) return current;
        return { ...current, policy };
      });
      if (submittedDraftRevision === draftRevisionRef.current) {
        const target = { kind: 'policy' as const, policy, readOnly: false };
        draftRevisionRef.current += 1;
        setDraft(workspaceDraftFromTarget(target, selectedProfileId));
      }
      await refreshQuotaAfterMutation();
      showNotification(t('api_key_policy.quota_reset_done'), 'success');
    } catch (error) {
      if (revision !== quotaRevisionRef.current) return;
      showNotification(errorMessage(error), 'error');
    } finally {
      if (revision === quotaRevisionRef.current) {
        quotaBusyRef.current = false;
        setQuotaBusy(false);
      }
    }
  }, [draft?.profileId, errorMessage, quotaSupported, refreshQuotaAfterMutation, replacePolicyInSnapshot, showNotification, t, workspaceTarget]);

  const resetQuotaFromOverview = useCallback(async (policy: APIKeyPolicy) => {
    if (!quotaSupported || quotaBusyRef.current || !policy.quota) return;
    if (!window.confirm(t('api_key_policy.quota_reset_confirm'))) return;
    const revision = ++quotaRevisionRef.current;
    quotaBusyRef.current = true;
    setQuotaBusy(true);
    try {
      const updated = await apiKeyPolicyApi.resetQuota(policy.id, policy.version);
      if (revision !== quotaRevisionRef.current) return;
      replacePolicyInSnapshot(updated);
      await refreshQuotaAfterMutation();
      if (revision !== quotaRevisionRef.current) return;
      showNotification(t('api_key_policy.quota_reset_done'), 'success');
    } catch (error) {
      if (revision !== quotaRevisionRef.current) return;
      showNotification(errorMessage(error), 'error');
    } finally {
      if (revision === quotaRevisionRef.current) {
        quotaBusyRef.current = false;
        setQuotaBusy(false);
      }
    }
  }, [errorMessage, quotaSupported, refreshQuotaAfterMutation, replacePolicyInSnapshot, showNotification, t]);

  const activateProfile = useCallback(async () => {
    if (!workspaceTarget || workspaceTarget.kind !== 'policy' || !draft || dirty || savingRef.current) return;
    const revision = ++saveRevisionRef.current;
    const submittedDraftRevision = draftRevisionRef.current;
    savingRef.current = true;
    setSaving(true);
    try {
      const policy = await apiKeyPolicyApi.activate(
        workspaceTarget.policy.id,
        draft.profileId,
        workspaceTarget.policy.version,
      );
      if (revision !== saveRevisionRef.current) return;
      replacePolicyInSnapshot(policy);
      const target = { kind: 'policy' as const, policy, readOnly: false };
      setWorkspaceTarget(target);
      if (submittedDraftRevision === draftRevisionRef.current) {
        draftRevisionRef.current += 1;
        setDraft(workspaceDraftFromTarget(target, draft.profileId));
      }
      showNotification(t('api_key_policy.activated'), 'success');
    } catch (error) {
      if (revision !== saveRevisionRef.current) return;
      if (apiKeyPolicyErrorCode(error) === 'config_version_conflict') setConflict(true);
      else showNotification(errorMessage(error), 'error');
    } finally {
      if (revision === saveRevisionRef.current) {
        savingRef.current = false;
        setSaving(false);
      }
    }
  }, [dirty, draft, errorMessage, replacePolicyInSnapshot, showNotification, t, workspaceTarget]);

  const runDangerAction = useCallback(async () => {
    if (!dangerPolicy || !dangerKind || dangerBusyRef.current) return;
    if (
      dangerKind === 'profile' &&
      dangerPolicy.profiles.length === 1 &&
      dangerPolicy.activeProfileId === draft?.profileId &&
      !optionalProfileSupported
    ) return;
    if (dangerKind === 'policy' && (
      !deletePreview || deletePreview.policyId !== dangerPolicy.id || deletePreview.version !== dangerPolicy.version
    )) return;
    const revision = ++dangerRevisionRef.current;
    dangerBusyRef.current = true;
    setDangerBusy(true);
    try {
      if (dangerKind === 'policy') {
        await apiKeyPolicyApi.deletePolicy(dangerPolicy.id, dangerPolicy.version);
      } else if (dangerKind === 'orphaned') {
        if (!snapshot) return;
        await apiKeyPolicyApi.purgeOrphaned(
          dangerPolicy.id,
          dangerPolicy.version,
          snapshot.bindings.configGeneration,
        );
      } else if (workspaceTarget?.kind === 'policy' && draft) {
        const removingOnlyActiveProfile = dangerPolicy.profiles.length === 1 &&
          dangerPolicy.activeProfileId === draft.profileId;
        await apiKeyPolicyApi.deleteProfile(
          dangerPolicy.id,
          draft.profileId,
          dangerPolicy.version,
          removingOnlyActiveProfile,
        );
      }
      if (revision !== dangerRevisionRef.current) return;
      setDangerPolicy(null);
      setDangerKind(null);
      setDeletePreview(null);
      closeWorkspace();
      await load();
      showNotification(t('api_key_policy.deleted'), 'success');
    } catch (error) {
      if (revision !== dangerRevisionRef.current) return;
      if (apiKeyPolicyErrorCode(error) === 'config_version_conflict') {
        setConflict(true);
        setDangerPolicy(null);
        setDangerKind(null);
        setDeletePreview(null);
      }
      showNotification(errorMessage(error), 'error');
    } finally {
      if (revision === dangerRevisionRef.current) {
        dangerBusyRef.current = false;
        setDangerBusy(false);
      }
    }
  }, [closeWorkspace, dangerKind, dangerPolicy, deletePreview, draft, errorMessage, load, optionalProfileSupported, showNotification, snapshot, t, workspaceTarget]);

  const visibleItems = useMemo(() => {
    const query = search.trim().toLowerCase();
    const current = snapshot?.bindings.items ?? [];
    const associated = current.filter((binding) => {
      if (filter !== 'all' && filter !== binding.state) return false;
      const text = `${binding.maskedKey} ${binding.policy?.displayName ?? ''} ${binding.policy?.profiles.map((profile) => profile.name).join(' ') ?? ''}`.toLowerCase();
      return !query || text.includes(query);
    });
    const orphaned = (snapshot?.bindings.orphaned ?? []).filter((policy) => {
      if (filter !== 'all' && filter !== 'orphaned') return false;
      const text = `${policy.displayName} ${policy.profiles.map((profile) => profile.name).join(' ')}`.toLowerCase();
      return !query || text.includes(query);
    });
    return { associated, orphaned };
  }, [filter, search, snapshot]);

  const statusCounts = useMemo(() => ({
    all: (snapshot?.bindings.items.length ?? 0) + (snapshot?.bindings.orphaned.length ?? 0),
    unconfigured: snapshot?.bindings.items.filter((item) => item.state === 'unconfigured').length ?? 0,
    configured: snapshot?.bindings.items.filter((item) => item.state === 'configured').length ?? 0,
    orphaned: snapshot?.bindings.orphaned.length ?? 0,
  }), [snapshot]);
	const takeoverActive = takeoverStatus?.takeoverEnabled === true;

  const quotaSummaryByPolicy = useMemo(
    () => new Map(quotaSummaries.map((item) => [item.policyId, item])),
    [quotaSummaries],
  );

  const quotaRows = useMemo(() => {
    const query = search.trim().toLowerCase();
    const rows = (snapshot?.bindings.items ?? []).flatMap((binding) => {
      const policy = binding.policy;
      if (!policy) return [];
      const summary = quotaSummaryByPolicy.get(policy.id);
      const visualState = quotaVisualState(summary, takeoverActive, policy.quota?.enabled === true);
      if (quotaFilter === 'attention' && !['unknown', 'warning', 'exhausted', 'blocked'].includes(visualState)) return [];
      if (quotaFilter === 'exhausted' && visualState !== 'exhausted') return [];
      if (quotaFilter === 'blocked' && visualState !== 'blocked') return [];
      if (quotaFilter === 'inactive' && visualState !== 'inactive') return [];
      if (quotaFilter === 'disabled' && visualState !== 'disabled') return [];
      const text = `${policy.displayName} ${binding.maskedKey}`.toLowerCase();
      if (query && !text.includes(query)) return [];
      return [{ binding, policy, summary, visualState }];
    });
    const rank = { unknown: 0, blocked: 1, exhausted: 2, warning: 3, available: 4, inactive: 5, disabled: 6 } as const;
    return rows.sort((left, right) => rank[left.visualState] - rank[right.visualState]
      || quotaMaximumRatio(right.summary) - quotaMaximumRatio(left.summary)
      || left.policy.displayName.localeCompare(right.policy.displayName));
  }, [quotaFilter, quotaSummaryByPolicy, search, snapshot, takeoverActive]);

  const quotaCounts = useMemo(() => {
    const counts = { enabled: 0, available: 0, warning: 0, exhausted: 0, blocked: 0, unknown: 0, inactive: 0 };
    (snapshot?.bindings.items ?? []).forEach((binding) => {
      if (!binding.policy) return;
      const summary = quotaSummaryByPolicy.get(binding.policy.id);
      if (!binding.policy.quota?.enabled) return;
      counts.enabled += 1;
      const state = quotaVisualState(summary, takeoverActive, binding.policy.quota?.enabled === true);
      if (state in counts) counts[state as keyof typeof counts] += 1;
    });
    return counts;
  }, [quotaSummaryByPolicy, snapshot, takeoverActive]);
		const takeoverScopeReady = Boolean(
		takeoverStatus?.healthy && snapshot && capability === 'ready',
	);
	const takeoverActionDisabled = !takeoverStatus || (
		!takeoverActive && !takeoverScopeReady
	);

  const currentPolicy = workspaceTarget?.kind === 'policy' ? workspaceTarget.policy : null;
  const currentQuotaSummary = currentPolicy ? quotaSummaryByPolicy.get(currentPolicy.id) : undefined;
  const currentQuota = currentQuotaSummary && currentQuotaSummary.policyVersion === currentPolicy?.version
    ? currentQuotaSummary.quota
    : currentPolicy?.quota;
  const currentBinding = currentPolicy
    ? snapshot?.bindings.items.find((binding) => binding.policy?.id === currentPolicy.id)
    : undefined;
  const readOnly = workspaceTarget?.kind === 'policy' && workspaceTarget.readOnly;
  const selectedProfile = currentPolicy?.profiles.find((profile) => profile.id === draft?.profileId);
  const active = Boolean(
    currentPolicy && draft?.profileId && draft.profileId === currentPolicy.activeProfileId,
  );
  const removingOnlyActiveProfile = Boolean(
    currentPolicy && currentPolicy.profiles.length === 1 && active,
  );
  const availableModels = resolveModelsForProviders(
    draft?.profile.providers ?? [],
    snapshot?.catalog ?? { providers: [], models: [] },
  );
  const mappingTargetModels = resolveMappingTargetModels(
    draft?.profile.models ?? [],
    availableModels,
  );
  const usageTargetSupported = Boolean(
    snapshot && supportsAPIKeyPolicyUsageTarget(snapshot.capabilities),
  );

  useEffect(() => {
    if (!snapshot || workspaceTarget || capability !== 'ready') return;
    const params = new URLSearchParams(location.search);
    const policyId = params.get('policy_id')?.trim();
    const profileId = params.get('profile_id')?.trim();
    if (!policyId) return;
    const policy = [
      ...snapshot.bindings.items.flatMap((item) => (item.policy ? [item.policy] : [])),
      ...snapshot.bindings.orphaned,
    ].find((item) => item.id === policyId);
    if (policy) openWorkspace({ kind: 'policy', policy, readOnly: policy.state === 'orphaned' }, profileId);
  }, [capability, location.search, openWorkspace, snapshot, workspaceTarget]);

  return (
    <div className={styles.page}>
		<ProFeatureHeader
			title={t('api_key_policy.title')}
			subtitle={t('api_key_policy.subtitle')}
			icon={<IconKey size={20} />}
			active={takeoverActive}
			loading={loading}
			actionBusy={takeoverBusy}
			actionDisabled={takeoverActionDisabled}
			onRefresh={() => void refreshPage()}
			onToggle={() => setTakeoverOpen(true)}
		/>

      {capability === 'unsupported' ? (
        <section className={styles.capabilityNotice}>
          <IconAlertTriangle size={22} />
          <div><strong>{t('api_key_policy.unsupported_title')}</strong><p>{t('api_key_policy.unsupported_body')}</p></div>
        </section>
      ) : capability === 'error' ? (
        <section className={styles.capabilityNotice}>
          <IconAlertTriangle size={22} />
          <div><strong>{t('api_key_policy.unavailable_title')}</strong><p>{errorMessage(loadError)}</p></div>
        </section>
      ) : (
        <>
			<section className={styles.statusOverview} aria-label={t('api_key_policy.overview')}>
				<div className={styles.overviewItem}>
					<span className={takeoverActive ? styles.overviewGood : styles.overviewMuted}>
						{takeoverActive ? <IconCheckCircle2 size={18} /> : <IconAlertTriangle size={18} />}
					</span>
					<small>{t('api_key_policy.runtime')}</small>
					<strong>{takeoverActive ? t('api_key_policy.running') : t('api_key_policy.stopped')}</strong>
				</div>
				<div className={styles.overviewItem}>
					<span className={styles.overviewAccent}><IconKey size={18} /></span>
					<small>{t('api_key_policy.upstream_keys')}</small>
					<strong>{snapshot?.bindings.items.length ?? 0}</strong>
				</div>
				<div className={styles.overviewItem}>
					<span className={styles.overviewGood}><IconShield size={18} /></span>
					<small>{t('api_key_policy.configured_policies')}</small>
					<strong>{statusCounts.configured}</strong>
				</div>
				<div className={styles.overviewItem}>
					<span className={statusCounts.orphaned ? styles.overviewBad : styles.overviewMuted}><IconAlertTriangle size={18} /></span>
					<small>{t('api_key_policy.orphaned_policies')}</small>
					<strong>{statusCounts.orphaned}</strong>
				</div>
				</section>

          {quotaOverviewSupported ? (
            <div className={styles.viewTabs} role="tablist" aria-label={t('api_key_policy.view_label')}>
              <button type="button" role="tab" aria-selected={pageView === 'policies'} className={pageView === 'policies' ? styles.viewTabActive : ''} onClick={() => setPageView('policies')}>{t('api_key_policy.view.policies')}</button>
              <button type="button" role="tab" aria-selected={pageView === 'quotas'} className={pageView === 'quotas' ? styles.viewTabActive : ''} onClick={() => setPageView('quotas')}>{t('api_key_policy.view.quotas')}</button>
            </div>
          ) : null}

          {pageView === 'policies' ? <>
          <div className={styles.toolbar}>
            <input className="input" value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('api_key_policy.search')} />
            <Select
              value={filter}
              onChange={(value) => setFilter(value as BindingFilter)}
              options={(['all', 'unconfigured', 'configured', 'orphaned'] as const).map((value) => ({ value, label: t(`api_key_policy.filter.${value}`) }))}
              ariaLabel={t('api_key_policy.filter_label')}
            />
				<Button variant="secondary" size="sm" onClick={() => navigate('/config')}>
					{t('api_key_policy.manage_upstream_keys')}
				</Button>
          </div>

          {loading && !snapshot ? <div className={styles.empty}>{t('common.loading')}</div> : null}

          <section className={styles.cards} aria-busy={loading}>
            {visibleItems.associated.map((binding) => {
              const policy = binding.policy;
              const activeProfile = policy?.profiles.find((profile) => profile.id === policy.activeProfileId);
              return (
                <article className={styles.card} key={binding.keyRef}>
                  <div className={styles.cardTop}>
                    <div className={styles.cardIdentity}>
                      <span><IconKey size={18} /></span>
                      <div><strong>{policy?.displayName || binding.maskedKey}</strong><code>{binding.maskedKey}</code></div>
                    </div>
                    <PolicyBadge state={binding.state}>{t(`api_key_policy.state.${binding.state}`)}</PolicyBadge>
                  </div>
                  <p className={styles.cardSummary}>
                    {policy
							? activeProfile
							  ? t(takeoverActive ? 'api_key_policy.configured_summary' : 'api_key_policy.configured_inactive_summary', { profile: activeProfile.name, count: policy.profiles.length })
							  : t(takeoverActive ? 'api_key_policy.configured_no_profile_summary' : 'api_key_policy.configured_no_profile_inactive_summary')
                      : t('api_key_policy.passthrough_summary')}
                  </p>
                  {binding.weakKey ? <div className={styles.weakKey}><IconAlertTriangle size={15} /> {t('api_key_policy.weak_key')}</div> : null}
                  {policy && quotaOverviewSupported ? (() => {
                    const summary = quotaSummaryByPolicy.get(policy.id);
                    const visualState = quotaVisualState(summary, takeoverActive, policy.quota?.enabled === true);
                    const percent = Math.min(Math.round(quotaMaximumRatio(summary) * 100), 100);
                    return <div className={`${styles.cardQuota} ${styles[`cardQuota_${visualState}`] ?? ''}`}><span>{t(`api_key_policy.quota_state.${visualState}`)}</span><strong>{summary?.quota?.enabled ? `${percent}%` : '-'}</strong></div>;
                  })() : null}
                  <div className={styles.cardMeta}>
                    <span>{t('api_key_policy.active_profile')}: <strong>{activeProfile?.name ?? t('api_key_policy.profile_not_configured')}</strong></span>
                    <span>{t('api_key_policy.updated')}: {policy ? formatAPIKeyPolicyTimestamp(policy.updatedAtMs, i18n.resolvedLanguage ?? i18n.language) : '-'}</span>
                  </div>
                  <div className={styles.cardActions}>
                    {policy ? (
                      <>
                        <Button variant="secondary" size="sm" onClick={() => openWorkspace({ kind: 'policy', policy, readOnly: false })}>{t('api_key_policy.open_workspace')}</Button>
                        {usageTargetSupported ? <Button variant="ghost" size="sm" onClick={() => void openUsage(binding)}>{t('api_key_policy.view_usage')}</Button> : null}
                      </>
                    ) : (
                      <Button size="sm" onClick={() => openWorkspace({ kind: 'create', binding })}>{t('api_key_policy.configure')}</Button>
                    )}
                  </div>
                </article>
              );
            })}
          </section>

          {visibleItems.orphaned.length ? (
            <section className={styles.orphanedSection}>
              <div className={styles.sectionHeading}><div><h2>{t('api_key_policy.orphaned_title')}</h2><p>{t('api_key_policy.orphaned_body')}</p></div><PolicyBadge state="orphaned">{visibleItems.orphaned.length}</PolicyBadge></div>
              <div className={styles.cards}>
                {visibleItems.orphaned.map((policy) => (
                  <article className={`${styles.card} ${styles.orphanedCard}`} key={policy.id}>
                    <div className={styles.cardTop}><div className={styles.cardIdentity}><span><IconAlertTriangle size={18} /></span><div><strong>{policy.displayName}</strong><code>{policy.id}</code></div></div><PolicyBadge state="orphaned">{t('api_key_policy.state.orphaned')}</PolicyBadge></div>
                    <p className={styles.cardSummary}>{t('api_key_policy.orphaned_summary', { count: policy.profiles.length })}</p>
                    <div className={styles.cardActions}>
                      <Button variant="secondary" size="sm" onClick={() => openWorkspace({ kind: 'policy', policy, readOnly: true })}>{t('api_key_policy.inspect')}</Button>
                      <Button variant="danger" size="sm" onClick={() => { setDangerPolicy(policy); setDangerKind('orphaned'); }}>{t('api_key_policy.purge')}</Button>
                    </div>
                  </article>
                ))}
              </div>
            </section>
          ) : null}

          {!loading && snapshot && visibleItems.associated.length === 0 && visibleItems.orphaned.length === 0 ? <div className={styles.empty}>{t('api_key_policy.empty')}</div> : null}
          </> : (
            <section className={styles.quotaOverview}>
              <div className={styles.quotaStats}>
                <div>
                  <small>{t('api_key_policy.quota_overview.enabled')}</small><strong>{quotaCounts.enabled}</strong>
                </div>
                <div>
                  <small>{t('api_key_policy.quota_overview.available')}</small><strong>{quotaCounts.available}</strong>
                </div>
                <div>
                  <small>{t('api_key_policy.quota_overview.attention')}</small><strong>{quotaCounts.warning}</strong>
                </div>
                <div>
                  <small>{t('api_key_policy.quota_overview.unavailable')}</small><strong>{quotaCounts.exhausted + quotaCounts.blocked + quotaCounts.unknown}</strong>
                </div>
                <div>
                  <small>{t('api_key_policy.quota_overview.inactive')}</small><strong>{quotaCounts.inactive}</strong>
                </div>
              </div>
              <div className={styles.quotaToolbar}>
                <input className="input" value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('api_key_policy.quota_overview.search')} />
                <Select value={quotaFilter} onChange={(value) => setQuotaFilter(value as QuotaFilter)} options={(['all', 'attention', 'exhausted', 'blocked', 'inactive', 'disabled'] as const).map((value) => ({ value, label: t(`api_key_policy.quota_filter.${value}`) }))} ariaLabel={t('api_key_policy.quota_overview.filter')} />
                <Button variant="secondary" size="sm" onClick={() => void loadQuotaSummaries()} loading={quotaLoading}>{t('common.refresh')}</Button>
              </div>
              {quotaError ? <div className={styles.quotaStale} role="alert"><IconAlertTriangle size={15} /><span>{t('api_key_policy.quota_overview.stale')}: {quotaError}</span></div> : null}
              <div className={styles.quotaList} role="list" aria-label={t('api_key_policy.quota_overview.title')}>
                {quotaRows.map(({ binding, policy, summary, visualState }) => {
                  const quota = summary?.quota;
                  const periodLabel = !summary
                    ? t('api_key_policy.quota_overview.snapshot_unavailable')
                    : quota?.period.type === 'past_duration'
                      ? t('api_key_policy.quota_overview.rolling_period', { value: quota.period.value, unit: t(`api_key_policy.quota_unit.${quota.period.unit}`) })
                      : quota?.period.type === 'calendar_duration'
                        ? t('api_key_policy.quota_overview.calendar_period', {
                          unit: t(`api_key_policy.quota_calendar_unit.${quota.period.unit}`),
                          timezone: quota.period.timezone ?? 'UTC',
                        })
                        : t('api_key_policy.quota_period.all_time');
                  return (
                    <article className={styles.quotaListItem} role="listitem" key={policy.id}>
                      <div className={styles.quotaKeyCell}><strong title={policy.displayName}>{policy.displayName}</strong><code title={binding.maskedKey}>{binding.maskedKey}</code></div>
                      <div className={styles.quotaPeriodCell}><strong>{periodLabel}</strong>{summary?.nextRecoverAtMs ? <small>{t('api_key_policy.quota_overview.recovers_at', { time: formatAPIKeyPolicyTimestamp(summary.nextRecoverAtMs, i18n.resolvedLanguage ?? i18n.language, quota?.period.type === 'calendar_duration' ? quota.period.timezone ?? 'UTC' : undefined) })}</small> : <small>{!summary ? t('api_key_policy.quota_overview.snapshot_unavailable') : quota?.period.type === 'all_time' ? t('api_key_policy.quota_overview.manual_reset') : t('api_key_policy.quota_overview.active_window')}</small>}</div>
                      <div className={styles.quotaStateCell}><PolicyBadge state={`quota_${visualState}`}>{t(`api_key_policy.quota_state.${visualState}`)}</PolicyBadge>{summary?.blockedReason ? <small>{t(`api_key_policy.quota_block.${summary.blockedReason}`)}</small> : null}</div>
                      <div className={styles.quotaRowActions}><Button variant="secondary" size="sm" onClick={() => openWorkspace({ kind: 'policy', policy, readOnly: false })}>{t('api_key_policy.quota_overview.edit')}</Button>{quota?.enabled ? <Button variant="danger" size="sm" onClick={() => void resetQuotaFromOverview(policy)} disabled={quotaBusy}>{t('api_key_policy.quota_reset')}</Button> : null}{usageTargetSupported ? <Button variant="ghost" size="sm" onClick={() => void openUsage(binding)}>{t('api_key_policy.view_usage')}</Button> : null}</div>
                      <div className={styles.quotaMetrics}>
                        <QuotaMetric label={t('api_key_policy.quota_requests')} used={quota ? quota.usage.requestsUsed : undefined} limit={quota?.requests} />
                        <QuotaMetric label={t('api_key_policy.quota_tokens')} used={quota ? quota.usage.totalTokensUsed : undefined} limit={quota?.totalTokens} />
                        <QuotaMetric label={t('api_key_policy.quota_cost')} used={quota ? quota.usage.costUsed : undefined} limit={quota?.cost} cost />
                      </div>
                    </article>
                  );
                })}
                {!quotaLoading && quotaRows.length === 0 ? <div className={styles.empty}>{t('api_key_policy.quota_overview.empty')}</div> : null}
              </div>
              {quotaSnapshotAt > 0 ? <p className={styles.quotaSnapshot}>{t('api_key_policy.quota_overview.updated_at', { time: formatAPIKeyPolicyTimestamp(quotaSnapshotAt, i18n.resolvedLanguage ?? i18n.language) })}</p> : null}
            </section>
          )}
        </>
      )}

      <ProWorkspaceSheet
        open={Boolean(workspaceTarget && draft)}
		onClose={closeWorkspace}
        confirmClose={() => !dirty || window.confirm(t('api_key_policy.discard_confirm'))}
        closeDisabled={saving}
		size="lg"
		className={styles.policySheet}
        eyebrow={workspaceTarget?.kind === 'create' ? t('api_key_policy.create_eyebrow') : t('api_key_policy.workspace_eyebrow')}
        title={workspaceTarget?.kind === 'create' ? t('api_key_policy.create_title') : currentPolicy?.displayName}
        description={readOnly ? t('api_key_policy.orphaned_read_only') : t('api_key_policy.workspace_description')}
		footer={
			<div className={styles.sheetFooter}>
				<span>{dirty ? t('config_management.status_dirty_short') : t('api_key_policy.workspace_saved')}</span>
				<div>
					<Button variant="secondary" onClick={() => void requestWorkspaceClose()} disabled={saving}>
						{readOnly ? t('common.close') : t('common.cancel')}
					</Button>
					{!readOnly ? <Button onClick={() => void saveWorkspace()} loading={saving} disabled={!dirty || saving}>{t('common.save')}</Button> : null}
				</div>
			</div>
		}
      >
        {draft && snapshot ? (
          <div className={styles.workspace}>
            {conflict ? (
              <div className={styles.conflict} role="alert">
                <div><strong>{t('api_key_policy.conflict_title')}</strong><p>{t('api_key_policy.conflict_body')}</p></div>
                <Button variant="secondary" size="sm" onClick={() => void reloadWorkspace()} disabled={saving}>{t('api_key_policy.reload_server')}</Button>
              </div>
            ) : null}

            <Input
              label={t('api_key_policy.display_name')}
              value={draft.displayName}
              onChange={(event) => updateDraft((current) => ({ ...current, displayName: event.target.value }))}
              disabled={readOnly || saving}
              hint={t('api_key_policy.display_name_hint')}
            />

            {quotaSupported ? <section className={styles.quotaSection}>
              <div className={styles.quotaHeader}>
                <div><h3>{t('api_key_policy.quota_title')}</h3><p>{t('api_key_policy.quota_hint')}</p></div>
                <label className={styles.quotaToggle}>
                  <input
                    type="checkbox"
                    checked={draft.quota?.enabled === true}
                    disabled={readOnly || saving}
                    onChange={(event) => updateDraft((current) => ({
                      ...current,
                      quota: event.target.checked
                        ? { ...(current.quota ?? {}), enabled: true, period: current.quota?.period ?? defaultQuotaPeriod() }
                        : currentPolicy?.quota ? { ...(current.quota ?? quotaInputFromPolicy(currentPolicy)!), enabled: false } : null,
                    }))}
                  />
                  <span>{t('api_key_policy.quota_enabled')}</span>
                </label>
              </div>

              {draft.quota?.enabled ? <>
                <div className={styles.quotaGrid}>
                  <Input
                    label={t('api_key_policy.quota_requests')}
                    type="number"
                    min={1}
                    step={1}
                    placeholder={t('api_key_policy.quota_requests_example')}
                    value={draft.quota.requests ?? ''}
                    disabled={readOnly || saving}
                    onChange={(event) => updateDraft((current) => ({ ...current, quota: updateQuotaLimit(current.quota, 'requests', event.target.value ? Number(event.target.value) : undefined) }))}
                  />
                  <Input
                    label={t('api_key_policy.quota_tokens')}
                    type="number"
                    min={1}
                    step={1}
                    placeholder={t('api_key_policy.quota_tokens_example')}
                    value={draft.quota.totalTokens ?? ''}
                    disabled={readOnly || saving}
                    onChange={(event) => updateDraft((current) => ({ ...current, quota: updateQuotaLimit(current.quota, 'totalTokens', event.target.value ? Number(event.target.value) : undefined) }))}
                  />
                  <Input
                    label={t('api_key_policy.quota_cost')}
                    type="number"
                    min={0.000001}
                    step={0.000001}
                    placeholder={t('api_key_policy.quota_cost_example')}
                    value={draft.quota.cost ?? ''}
                    disabled={readOnly || saving}
                    onChange={(event) => updateDraft((current) => ({ ...current, quota: updateQuotaLimit(current.quota, 'cost', event.target.value ? Number(event.target.value) : undefined) }))}
                  />
                </div>

                <div className={styles.quotaPeriodGrid}>
                  <label className={styles.quotaSelectField}>
                    <span>{t('api_key_policy.quota_period_label')}</span>
                    <Select
                      value={draft.quota.period.type}
                      triggerClassName={styles.quotaSelectTrigger}
                      options={(['all_time', 'past_duration', 'calendar_duration'] as const).map((value) => ({ value, label: t(`api_key_policy.quota_period.${value}`) }))}
                      onChange={(value) => updateDraft((current) => ({
                        ...current,
                        quota: current.quota ? {
                          ...current.quota,
                          period: value === 'past_duration'
                            ? (current.quota.period.type === 'past_duration' ? current.quota.period : { type: 'past_duration', value: 1, unit: 'day' })
                            : value === 'calendar_duration'
                              ? (current.quota.period.type === 'calendar_duration' ? current.quota.period : {
                                type: 'calendar_duration', unit: 'day',
                                ...(quotaTimezoneSupported ? { timezone: 'UTC' } : {}),
                              })
                              : { type: 'all_time' },
                        } : current.quota,
                      }))}
                      disabled={Boolean(readOnly || saving)}
                      ariaLabel={t('api_key_policy.quota_period_label')}
                    />
                  </label>
                  {draft.quota.period.type === 'past_duration' ? <>
                    <Input
                      label={t('api_key_policy.quota_period_value')}
                      type="number"
                      min={1}
                      step={1}
                      placeholder={t('api_key_policy.quota_period_value_example')}
                      value={draft.quota.period.value || ''}
                      disabled={readOnly || saving}
                      onChange={(event) => updateDraft((current) => current.quota?.period.type === 'past_duration' ? ({ ...current, quota: { ...current.quota, period: { ...current.quota.period, value: Number(event.target.value) } } }) : current)}
                    />
                    <label className={styles.quotaSelectField}>
                      <span>{t('api_key_policy.quota_period_unit')}</span>
                      <Select
                        value={draft.quota.period.unit}
                        triggerClassName={styles.quotaSelectTrigger}
                        options={(['minute', 'hour', 'day'] as const).map((value) => ({ value, label: t(`api_key_policy.quota_unit.${value}`) }))}
                        onChange={(unit) => updateDraft((current) => current.quota?.period.type === 'past_duration' ? ({ ...current, quota: { ...current.quota, period: { ...current.quota.period, unit: unit as 'minute' | 'hour' | 'day' } } }) : current)}
                        disabled={Boolean(readOnly || saving)}
                        ariaLabel={t('api_key_policy.quota_period_unit')}
                      />
                    </label>
                  </> : null}
                  {draft.quota.period.type === 'calendar_duration' ? <>
                    <label className={styles.quotaSelectField}>
                      <span>{t('api_key_policy.quota_period_unit')}</span>
                      <Select
                        value={draft.quota.period.unit}
                        triggerClassName={styles.quotaSelectTrigger}
                        options={(['day', 'month'] as const).map((value) => ({ value, label: t(`api_key_policy.quota_calendar_unit.${value}`) }))}
                        onChange={(unit) => updateDraft((current) => current.quota?.period.type === 'calendar_duration' ? ({ ...current, quota: { ...current.quota, period: { ...current.quota.period, unit: unit as 'day' | 'month' } } }) : current)}
                        disabled={Boolean(readOnly || saving)}
                        ariaLabel={t('api_key_policy.quota_period_unit')}
                      />
                    </label>
                    {quotaTimezoneSupported ? <>
                      <label className={styles.quotaSelectField}>
                        <span>{t('api_key_policy.quota_timezone')}</span>
                        <Select
                          value={draft.quota.period.timezone ?? 'UTC'}
                          triggerClassName={styles.quotaSelectTrigger}
                          options={buildAPIKeyQuotaTimezoneOptions(draft.quota.period.timezone)}
                          onChange={(timezone) => updateDraft((current) => current.quota?.period.type === 'calendar_duration' ? ({ ...current, quota: { ...current.quota, period: { ...current.quota.period, timezone } } }) : current)}
                          disabled={Boolean(readOnly || saving)}
                          ariaLabel={t('api_key_policy.quota_timezone')}
                        />
                      </label>
                    </> : null}
                  </> : null}
                </div>
                <p className={styles.quotaPeriodHint}>{t(quotaTimezoneSupported ? 'api_key_policy.quota_period_hint' : 'api_key_policy.quota_period_hint_legacy')}</p>

                {currentQuota ? (
                  <div className={styles.quotaUsage}>
                    <span>{t('api_key_policy.quota_requests_usage', { used: currentQuota.usage.requestsUsed, limit: currentQuota.requests ?? '∞' })}</span>
                    <span>{t('api_key_policy.quota_tokens_usage', { used: currentQuota.usage.totalTokensUsed, limit: currentQuota.totalTokens ?? '∞' })}</span>
                    <span>{t('api_key_policy.quota_cost_usage', { used: formatQuotaCost(currentQuota.usage.costUsed), limit: currentQuota.cost === undefined ? '∞' : formatQuotaCost(currentQuota.cost) })}</span>
                    {!readOnly ? <Button variant="danger" size="sm" onClick={() => void resetQuota()} loading={quotaBusy} disabled={saving || dirty}>{t('api_key_policy.quota_reset')}</Button> : null}
                  </div>
                ) : null}
              </> : null}
            </section> : null}

            <section className={styles.profileToggleSection}>
              <div>
                <h3>{t('api_key_policy.profile_configuration')}</h3>
                <p>{t('api_key_policy.profile_optional_hint')}</p>
              </div>
              <label className={styles.quotaToggle}>
                <input
                  type="checkbox"
                  checked={draft.profileEnabled}
                  disabled={Boolean(
                    readOnly || saving || !optionalProfileSupported ||
                    (currentPolicy && currentPolicy.profiles.length > 0 && !profileEnforcementToggleSupported)
                  )}
                  onChange={(event) => {
                    const enabled = event.target.checked;
                    updateDraft((current) => {
                      if (!enabled) return { ...current, profileEnabled: false };
                      if (workspaceTarget?.kind === 'policy' && workspaceTarget.policy.profiles.length > 0) {
                        return { ...current, profileEnabled: true };
                      }
                      return {
                        ...current,
                        profileEnabled: true,
                        profileId: '',
                        profile: emptyProfile(),
                        isNewProfile: workspaceTarget?.kind === 'policy',
                      };
                    });
                  }}
                />
                <span>{t('api_key_policy.profile_enabled')}</span>
              </label>
            </section>

            {!draft.profileEnabled ? (
              <div className={styles.profileDisabledHint} role="note">
                <IconInfo size={18} />
                <span>{t('api_key_policy.no_profile_behavior')}</span>
              </div>
            ) : <>
            {currentPolicy && currentPolicy.profiles.length > 0 ? (
              <div className={styles.profileRail}>
                {currentPolicy.profiles.map((profile) => (
                  <button
                    key={profile.id}
                    className={draft.profileId === profile.id && !draft.isNewProfile ? styles.profileActive : ''}
                    onClick={() => updateDraft(() => workspaceDraftFromTarget(workspaceTarget!, profile.id))}
                    disabled={saving || dirty}
                  >
                    <span>{profile.name}</span>
                    {profile.id === currentPolicy.activeProfileId ? <small>{t('api_key_policy.active')}</small> : null}
                  </button>
                ))}
                {!readOnly ? <button className={draft.isNewProfile ? styles.profileActive : ''} onClick={() => updateDraft((current) => ({ ...current, profileEnabled: true, profileId: '', profile: emptyProfile(), isNewProfile: true }))} disabled={saving || dirty}><IconPlus size={14} /> {t('api_key_policy.new_profile')}</button> : null}
              </div>
            ) : null}

            <div className={styles.profileHeading}>
              <div><h3>{draft.isNewProfile ? t('api_key_policy.new_profile') : draft.profile.name || t('api_key_policy.initial_profile')}</h3><p>{active
                ? t('api_key_policy.active_profile_hint')
                : currentPolicy?.profiles.length === 0
                  ? t('api_key_policy.first_profile_hint')
                  : t('api_key_policy.inactive_profile_hint')}</p></div>
              {currentPolicy && !readOnly ? (
                <div className={styles.profileActions}>
                  {!active && !draft.isNewProfile ? <Button size="sm" onClick={() => void activateProfile()} disabled={dirty || saving}>{t('api_key_policy.set_active')}</Button> : null}
                  {(!active || (optionalProfileSupported && removingOnlyActiveProfile)) && !draft.isNewProfile ? <Button variant="danger" size="sm" onClick={() => { setDangerPolicy(currentPolicy); setDangerKind('profile'); }} disabled={saving}><IconTrash2 size={14} /> {t('common.delete')}</Button> : null}
                </div>
              ) : null}
            </div>

            <Input
              label={t('api_key_policy.profile_name')}
              value={draft.profile.name}
              onChange={(event) => updateDraft((current) => ({ ...current, profile: { ...current.profile, name: event.target.value } }))}
              disabled={readOnly || saving}
            />

            <section className={styles.mappingSection}>
              <div className={styles.mappingHeader}><div><h3>{t('api_key_policy.mappings')}</h3><p>{t('api_key_policy.mappings_hint')}</p></div>{!readOnly ? <Button variant="secondary" size="sm" onClick={() => updateDraft((current) => ({ ...current, profile: { ...current.profile, mappings: [...current.profile.mappings, { source: '', target: mappingTargetModels[0] ?? '' }] } }))} disabled={saving || mappingTargetModels.length === 0}><IconPlus size={14} /> {t('common.add')}</Button> : null}</div>
              {draft.profile.mappings.length ? draft.profile.mappings.map((mapping, index) => (
                <div className={styles.mappingRow} key={`${index}-${mapping.source}`}>
                  <input className="input" value={mapping.source} placeholder={t('api_key_policy.source_model')} disabled={readOnly || saving} onChange={(event) => updateDraft((current) => { const mappings = current.profile.mappings.map((item, itemIndex) => itemIndex === index ? { ...item, source: event.target.value } : item); return { ...current, profile: { ...current.profile, mappings } }; })} />
                  <span>→</span>
                  <Select value={mapping.target} options={mappingTargetModels.map((model) => ({ value: model, label: model }))} onChange={(target) => updateDraft((current) => { const mappings = current.profile.mappings.map((item, itemIndex) => itemIndex === index ? { ...item, target } : item); return { ...current, profile: { ...current.profile, mappings } }; })} disabled={Boolean(readOnly || saving)} ariaLabel={t('api_key_policy.target_model')} />
                  {!readOnly ? <button className={styles.iconButton} onClick={() => updateDraft((current) => ({ ...current, profile: { ...current.profile, mappings: current.profile.mappings.filter((_, itemIndex) => itemIndex !== index) } }))} disabled={saving} aria-label={t('common.delete')}><IconTrash2 size={15} /></button> : null}
                </div>
              )) : <div className={styles.mappingEmpty}>{t('api_key_policy.no_mappings')}</div>}
            </section>

            <div className={styles.policyGrid}>
              <ChoiceList title={t('api_key_policy.allowed_providers')} values={snapshot.catalog.providers} selected={draft.profile.providers} onChange={(providers) => updateDraft((current) => ({ ...current, profile: updateProfileProviders(current.profile, providers) }))} disabled={Boolean(readOnly || saving)} emptyLabel={t('api_key_policy.search_providers')} allLabel={t('api_key_policy.all')} emptySelectionHint={t('api_key_policy.all_providers_when_empty')} unavailableLabel={t('api_key_policy.unavailable_selections')} removeUnavailableLabel={(value) => t('api_key_policy.remove_unavailable_selection', { value })} />
              <ChoiceList title={t('api_key_policy.allowed_models')} values={availableModels} selected={draft.profile.models} onChange={(models) => updateDraft((current) => ({ ...current, profile: { ...current.profile, models, mappings: models.length === 0 ? current.profile.mappings : current.profile.mappings.filter((mapping) => models.includes(mapping.target)) } }))} disabled={Boolean(readOnly || saving)} emptyLabel={t('api_key_policy.search_models')} allLabel={t('api_key_policy.all')} emptySelectionHint={t('api_key_policy.all_models_when_empty')} unavailableLabel={t('api_key_policy.unavailable_selections')} removeUnavailableLabel={(value) => t('api_key_policy.remove_unavailable_selection', { value })} />
            </div>
            </>}

            {currentPolicy ? (
              <div className={styles.workspaceFooterActions}>
                {usageTargetSupported ? <Button variant="ghost" size="sm" disabled={!currentBinding || !selectedProfile} onClick={() => { if (currentBinding && selectedProfile) void openUsage(currentBinding, selectedProfile); }}>{t('api_key_policy.view_profile_usage')}</Button> : null}
                {!readOnly ? <Button variant="danger" size="sm" onClick={() => void openPolicyDeletePreview(currentPolicy)} disabled={saving || dangerBusy}><IconAlertTriangle size={14} /> {t('api_key_policy.delete_policy')}</Button> : null}
              </div>
            ) : null}
          </div>
        ) : null}
      </ProWorkspaceSheet>

		<ProTaskDialog
			open={takeoverOpen}
			onClose={() => setTakeoverOpen(false)}
			closeDisabled={takeoverBusy}
			title={takeoverActive ? t('api_key_policy.stop_takeover_title') : t('api_key_policy.start_takeover_title')}
			footer={<><Button variant="secondary" onClick={() => setTakeoverOpen(false)} disabled={takeoverBusy}>{t('common.cancel')}</Button><Button variant={takeoverActive ? 'danger' : 'primary'} onClick={() => void toggleTakeover()} loading={takeoverBusy}>{takeoverActive ? t('pro_feature_header.stop_takeover') : t('pro_feature_header.start_takeover')}</Button></>}
		>
			<div className={styles.takeoverBody}>
				<span><IconInfo size={22} /></span>
				<div>
					<p>{takeoverActive ? t('api_key_policy.stop_takeover_body') : t('api_key_policy.start_takeover_body')}</p>
					<ul>
						{takeoverScopeReady ? (
							<>
								<li>{t('api_key_policy.takeover_configured_count', { count: statusCounts.configured })}</li>
								<li>{t('api_key_policy.takeover_passthrough_count', { count: statusCounts.unconfigured })}</li>
							</>
						) : <li>{t('api_key_policy.takeover_scope_unavailable')}</li>}
						<li>{t('api_key_policy.takeover_new_requests_only')}</li>
					</ul>
				</div>
			</div>
		</ProTaskDialog>

      <ProTaskDialog
        open={Boolean(dangerPolicy && dangerKind)}
        onClose={() => { if (!dangerBusy) { setDangerPolicy(null); setDangerKind(null); setDeletePreview(null); } }}
        closeDisabled={dangerBusy}
        title={dangerKind === 'policy'
          ? t('api_key_policy.delete_policy_title')
          : dangerKind === 'orphaned'
            ? t('api_key_policy.purge_title')
            : t(removingOnlyActiveProfile ? 'api_key_policy.delete_last_profile_title' : 'api_key_policy.delete_profile_title')}
        footer={<><Button variant="secondary" onClick={() => { setDangerPolicy(null); setDangerKind(null); setDeletePreview(null); }} disabled={dangerBusy}>{t('common.cancel')}</Button><Button variant="danger" onClick={() => void runDangerAction()} loading={dangerBusy} disabled={dangerKind === 'policy' && !deletePreview}>{dangerKind === 'policy' ? t('api_key_policy.confirm_passthrough') : t('common.delete')}</Button></>}
      >
        <div className={styles.dangerBody}>
          <span><IconAlertTriangle size={24} /></span>
          <div>
            <strong>{dangerPolicy?.displayName}</strong>
            <p>{dangerKind === 'policy'
              ? t('api_key_policy.delete_policy_preview')
              : dangerKind === 'orphaned'
                ? t('api_key_policy.purge_preview')
                : t(removingOnlyActiveProfile ? 'api_key_policy.delete_last_profile_preview' : 'api_key_policy.delete_profile_preview', { profile: selectedProfile?.name ?? '' })}</p>
            {dangerKind === 'policy' && deletePreview ? <ul>
              {deletePreview.activeProfile ? <>
                <li>{deletePreview.activeProfile.models.length ? t('api_key_policy.permission_change_models_count', { count: deletePreview.activeProfile.models.length }) : t('api_key_policy.permission_change_models_all')}</li>
                <li>{deletePreview.activeProfile.providers.length ? t('api_key_policy.permission_change_providers_count', { count: deletePreview.activeProfile.providers.length }) : t('api_key_policy.permission_change_providers_all')}</li>
              </> : <li>{t('api_key_policy.no_profile_policy_delete_effect')}</li>}
              {dangerPolicy?.quota ? <li>{t('api_key_policy.policy_delete_quota_effect')}</li> : null}
              <li>{t('api_key_policy.permission_change_new_requests')}</li>
            </ul> : null}
          </div>
        </div>
      </ProTaskDialog>

    </div>
  );
}
