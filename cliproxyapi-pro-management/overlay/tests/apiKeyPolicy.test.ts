import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  APIKeyPolicyCapabilityError,
  buildAPIKeyQuotaTimezoneOptions,
  buildAPIKeyPolicyWorkspaceUpdate,
  cloneProfileInput,
  apiKeyPolicyErrorTranslationKey,
  formatAPIKeyPolicyTimestamp,
  resolveMappingTargetModels,
  resolveModelsForProviders,
  supportsAPIKeyProfileEnforcementToggle,
  supportsAPIKeyQuota,
  supportsAPIKeyQuotaOverview,
  supportsAPIKeyQuotaTimezone,
  supportsAPIKeyPolicyUsageTarget,
  supportsOptionalAPIKeyProfile,
  updateProfileProviders,
  validateAPIKeyPolicyCapabilities,
  validateProfileInput,
  type APIKeyPolicyCatalog,
  type APIKeyProfileInput,
} from '@/pro/modules/apiKeyPolicy';

const catalog: APIKeyPolicyCatalog = {
  providers: ['openai', 'claude', 'home'],
  models: ['gpt-5', 'claude-sonnet-4'],
  modelProviders: {
    'gpt-5': ['openai', 'home'],
    'claude-sonnet-4': ['claude'],
  },
};

const validProfile = (): APIKeyProfileInput => ({
  name: 'Production',
  providers: ['openai'],
  models: ['gpt-5'],
  mappings: [{ source: 'smart', target: 'gpt-5' }],
});

describe('usage policy backup preview contract', () => {
  test('previews replacement, orphaned state, and config key boundary before importing', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');
    const service = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/dataManagement.ts'), 'utf8');
    expect(service).toContain("'/data/backups/preview'");
    expect(service).toContain('orphanedPolicies: number');
		expect(service).toContain('currentTakeoverEnabled: boolean');
		expect(service).toContain('targetTakeoverEnabled: boolean');
    expect(page).toContain('policy_restore_replace');
    expect(page).toContain('policy_restore_preserve');
    expect(page).toContain('policy_takeover_change');
    expect(page).toContain('restore_no_api_keys');
    expect(page).toContain('await dataManagementApi.restore(');
  });

  test('lists WebDAV backups and uses the data-management preview and restore endpoints', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');
    const service = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/dataManagement.ts'), 'utf8');
    expect(service).toContain("'/data/backups'");
    expect(service).toContain("'/data/backups/preview'");
    expect(service).toContain("'/data/backups/restore'");
    expect(page).toContain('history.backups.slice(0, 10)');
    expect(page).toContain('restorePreview?.domains.map');
    expect(page).toContain('restoreFileName');
  });
});

describe('API Key Policy profile drafts', () => {
  test('requires the explicit minimum Core capability contract', () => {
    expect(validateAPIKeyPolicyCapabilities({
		apiVersion: 2,
		features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'policy_delete_preview', 'orphaned_purge_guard', 'takeover_control'],
		}).apiVersion).toBe(2);
    expect(() => validateAPIKeyPolicyCapabilities({
      apiVersion: 1,
      features: ['policy_crud', 'profile_crud', 'optimistic_concurrency'],
    })).toThrow(APIKeyPolicyCapabilityError);
  });

  test('rejects Core that omits only policy backup and restore support', () => {
    expect(() => validateAPIKeyPolicyCapabilities({
      apiVersion: 1,
		features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_delete_preview', 'orphaned_purge_guard', 'takeover_control', 'usage_key_target'],
    })).toThrow(APIKeyPolicyCapabilityError);
  });

  test('rejects Core that cannot provide a server-derived delete preview', () => {
    expect(() => validateAPIKeyPolicyCapabilities({
      apiVersion: 1,
		features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'orphaned_purge_guard', 'takeover_control', 'usage_key_target'],
    })).toThrow(APIKeyPolicyCapabilityError);
  });

  test('rejects Core that cannot atomically guard orphaned-policy purge', () => {
    expect(() => validateAPIKeyPolicyCapabilities({
      apiVersion: 1,
		features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'policy_delete_preview', 'takeover_control', 'usage_key_target'],
    })).toThrow(APIKeyPolicyCapabilityError);
  });

  test('keeps usage navigation optional for older compatible Core versions', () => {
    const legacyCapabilities = validateAPIKeyPolicyCapabilities({
      apiVersion: 2,
      features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'policy_delete_preview', 'orphaned_purge_guard', 'takeover_control'],
    });
    expect(supportsAPIKeyPolicyUsageTarget(legacyCapabilities)).toBe(false);
    expect(supportsAPIKeyPolicyUsageTarget({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'usage_key_target'],
    })).toBe(true);
  });

  test('gates quota independently without raising the minimum compatible Core contract', () => {
    const legacyCapabilities = validateAPIKeyPolicyCapabilities({
      apiVersion: 2,
      features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'policy_delete_preview', 'orphaned_purge_guard', 'takeover_control'],
    });
    expect(supportsAPIKeyQuota(legacyCapabilities)).toBe(false);
    expect(supportsAPIKeyQuota({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'key_quota_requests_tokens'],
    })).toBe(false);
    expect(supportsAPIKeyQuota({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'key_quota_requests_tokens', 'key_quota_explicit_reset'],
    })).toBe(false);
    expect(supportsAPIKeyQuota({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'key_quota_requests_tokens', 'key_quota_explicit_reset', 'key_quota_cost_period'],
    })).toBe(true);
    expect(supportsAPIKeyQuotaTimezone(legacyCapabilities)).toBe(false);
    expect(supportsAPIKeyQuotaTimezone({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'key_quota_calendar_timezone'],
    })).toBe(true);
  });

  test('gates optional Profile creation without breaking older compatible Core versions', () => {
    const legacyCapabilities = validateAPIKeyPolicyCapabilities({
      apiVersion: 2,
      features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'policy_delete_preview', 'orphaned_purge_guard', 'takeover_control'],
    });
    expect(supportsOptionalAPIKeyProfile(legacyCapabilities)).toBe(false);
    expect(supportsAPIKeyProfileEnforcementToggle(legacyCapabilities)).toBe(false);
    expect(supportsOptionalAPIKeyProfile({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'optional_profile'],
    })).toBe(true);
    expect(supportsAPIKeyProfileEnforcementToggle({
      ...legacyCapabilities,
      features: [...legacyCapabilities.features, 'profile_enforcement_toggle'],
    })).toBe(true);
  });

  test('localizes API errors and keeps orphan purge bound to version and config generation', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
    expect(apiKeyPolicyErrorTranslationKey({ apiCode: 'api_key_policy_orphaned' })).toBe('api_key_policy.error.api_key_policy_orphaned');
    expect(page).toContain('apiKeyPolicyApi.purgeOrphaned(');
    expect(page).toContain('snapshot.bindings.configGeneration');
    expect(client).toContain('data: { version, configGeneration }');
    expect(client).toContain("'orphaned_purge_guard'");
    expect(client).toContain("'usage_key_target'");
    expect(client).toContain("'/api-key-policy-usage-target'");
    expect(page).toContain('usageTargetSupported ?');
  });

  test('negotiates and renders the lightweight API Key quota overview', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const styles = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.module.scss'), 'utf8');
    const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
    const legacyCapabilities = validateAPIKeyPolicyCapabilities({
      apiVersion: 1,
      features: ['policy_crud', 'profile_crud', 'optimistic_concurrency', 'atomic_workspace_save', 'policy_backup_restore', 'policy_delete_preview', 'orphaned_purge_guard', 'takeover_control'],
    });
    expect(supportsAPIKeyQuotaOverview({ ...legacyCapabilities, features: [...legacyCapabilities.features, 'key_quota_overview'] })).toBe(true);
    expect(client).toContain("'/api-key-policy-quota-summaries'");
    expect(page).toContain("type PageView = 'policies' | 'quotas'");
    expect(page).toContain("type QuotaVisualState = 'inactive' | 'disabled' | 'available' | 'warning' | 'exhausted' | 'blocked' | 'unknown'");
    expect(page).toContain("const quotaWorkspaceOpen = workspaceTarget?.kind === 'policy' && Boolean(workspaceTarget.policy.quota);");
    expect(page).toContain('const currentQuotaSummary = currentPolicy ? quotaSummaryByPolicy.get(currentPolicy.id) : undefined;');
    expect(page).toContain('currentQuotaSummary && currentQuotaSummary.policyVersion === currentPolicy?.version');
    expect(page).toContain('currentQuota.usage.requestsUsed');
    expect(page).not.toContain('currentPolicy.quota.usage.requestsUsed');
    expect(page).toContain("setPageView('policies');");
    expect(page).toContain('quotaVisualState(summary, takeoverActive, policy.quota?.enabled === true)');
    expect(page).toContain('apiKeyPolicyApi.resetQuota(policy.id, policy.version)');
    expect(page).toContain('await Promise.all([load(), refreshQuotaAfterMutation()]);');
    expect(page).toContain('onRefresh={() => void refreshPage()}');
    expect(page).toContain("quotaFilter === 'inactive'");
    expect(page).toContain("'blocked', 'inactive', 'disabled'");
    expect(page).toContain("quota_overview.inactive");
    expect(page).toContain("if (quotaConfigured && !takeoverActive) return 'inactive';");
    expect(page.indexOf("if (quotaConfigured && !takeoverActive) return 'inactive';"))
      .toBeLessThan(page.indexOf("if (!summary) return quotaConfigured ? 'unknown' : 'disabled';"));
    expect(page).toContain('key={binding.keyRef}');
    expect(page).toContain("t(`api_key_policy.quota_block.${summary.blockedReason}`)");
    expect(page).toContain('className={styles.quotaList} role="list"');
    expect(page).toContain('className={styles.quotaListItem} role="listitem"');
    expect(page).toContain('className={styles.quotaMetrics}');
    expect(page).not.toContain('className={styles.quotaTableHead}');
    expect(page).toContain("t('api_key_policy.quota_overview.remaining'");
    expect(styles).toContain('grid-template-areas: "key period state actions" "metrics metrics metrics metrics";');
    expect(styles).toContain('grid-template-areas: "key" "state" "period" "metrics" "actions";');
  });

  test('has complete distinct translations for every supported language', async () => {
    const locales = await import('../src/pro/apiKeyPolicyLocales');
    const bundles = locales.apiKeyPolicyLocales as Record<string, { api_key_policy: Record<string, unknown> }>;
    expect(Object.keys(bundles).sort()).toEqual(['en', 'ru', 'zh-CN', 'zh-TW']);
    const flatten = (value: unknown, prefix = ''): Record<string, string> => {
      if (!value || typeof value !== 'object') return {};
      return Object.entries(value as Record<string, unknown>).reduce<Record<string, string>>((out, [key, child]) => {
        const path = prefix ? `${prefix}.${key}` : key;
        if (typeof child === 'string') out[path] = child;
        else Object.assign(out, flatten(child, path));
        return out;
      }, {});
    };
    const english = flatten(bundles.en);
    for (const language of ['ru', 'zh-CN', 'zh-TW']) {
      const translated = flatten(bundles[language]);
      expect(Object.keys(translated).sort()).toEqual(Object.keys(english).sort());
      for (const key of Object.keys(english)) expect(translated[key]).not.toBe(english[key]);
    }
    expect(bundles['zh-CN'].api_key_policy.delete_policy_preview).not.toContain('unrestricted passthrough');
  });

  test('uses one atomic workspace request and synchronous duplicate-save guard', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const styles = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.module.scss'), 'utf8');
    const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
    expect(page).toContain('apiKeyPolicyApi.updateWorkspace(');
    expect(page).not.toContain('policy = await apiKeyPolicyApi.rename');
    expect(page).toContain('savingRef.current = true');
    expect(page).toContain('if (!workspaceTarget || !draft || savingRef.current) return;');
    expect(page).toContain('if (!validateDraft(changedProfile)) return;');
    expect(page).toContain('draft.profileEnabled ? draft.profile : undefined');
    expect(page).toContain("supportsOptionalAPIKeyProfile(snapshot.capabilities)");
    expect(page).toContain("t('api_key_policy.no_profile_behavior')");
    expect(client).toContain('...(initialProfile ? { initialProfile } : {})');
    expect(client).toContain('confirmNoProfile: NO_PROFILE_CONFIRMATION');
    expect(client).toContain("'/api-key-policy-capabilities'");
    expect(client).toContain('buildAPIKeyPolicyWorkspaceUpdate(');
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false)).toEqual({
      displayName: 'Renamed',
      version: 2,
      clientFeatures: ['provider_model_linkage', 'key_quota_cost_period'],
    });
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', validProfile(), false)).toEqual({
      displayName: 'Renamed',
      version: 2,
      clientFeatures: ['provider_model_linkage', 'key_quota_cost_period'],
      profileId: 'profile-1',
      profile: validProfile(),
      createProfile: false,
    });
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false, undefined)).not.toHaveProperty('quota');
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false, null)).toMatchObject({ quota: null });
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false, undefined, true, 'profile-1')).toEqual({
      displayName: 'Renamed',
      version: 2,
      clientFeatures: ['provider_model_linkage', 'key_quota_cost_period', 'profile_enforcement_toggle'],
      profileEnabled: true,
      activeProfileId: 'profile-1',
    });
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false, undefined, false)).toEqual({
      displayName: 'Renamed',
      version: 2,
      clientFeatures: ['provider_model_linkage', 'key_quota_cost_period', 'profile_enforcement_toggle'],
      profileEnabled: false,
    });
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false, {
      enabled: true,
      requests: 100,
      period: { type: 'calendar_duration', unit: 'day', timezone: 'Asia/Shanghai' },
    })).toMatchObject({
      clientFeatures: ['provider_model_linkage', 'key_quota_cost_period', 'key_quota_calendar_timezone'],
      quota: { period: { timezone: 'Asia/Shanghai' } },
    });
    expect(buildAPIKeyPolicyWorkspaceUpdate('Renamed', 2, 'profile-1', undefined, false, {
      enabled: true,
      requests: 100,
      period: { type: 'calendar_duration', unit: 'day', timezone: '' },
    })).toMatchObject({
      clientFeatures: ['provider_model_linkage', 'key_quota_cost_period', 'key_quota_calendar_timezone'],
    });
    const timestamp = Date.UTC(2026, 7, 19, 12, 0, 0);
    expect(formatAPIKeyPolicyTimestamp(timestamp, 'en-US', 'Local'))
      .toBe(formatAPIKeyPolicyTimestamp(timestamp, 'en-US', 'UTC'));
    expect(buildAPIKeyQuotaTimezoneOptions('Asia/Shanghai')[0]).toEqual({ value: 'UTC', label: 'UTC' });
    expect(buildAPIKeyQuotaTimezoneOptions().some((option) => option.value === 'Pacific/Auckland')).toBe(true);
    expect(buildAPIKeyQuotaTimezoneOptions('Custom/Legacy')[0]).toEqual({ value: 'Custom/Legacy', label: 'Custom/Legacy' });
    expect(buildAPIKeyQuotaTimezoneOptions('Pacific/Auckland').filter((option) => option.value === 'Pacific/Auckland')).toHaveLength(1);
    expect(page).toContain('quotaSupported ? draft.quota : undefined');
    expect(page).toContain('profileEnabled: target.policy.profileEnabled');
    expect(page).toContain('draft.profileEnabled !== target.policy.profileEnabled');
    expect(page).toContain("supportsAPIKeyProfileEnforcementToggle(snapshot.capabilities)");
    expect(page).not.toContain("window.confirm(t('api_key_policy.profile_disable_confirm'))");
    expect(page).toContain('profileEnabledChanged ? draft.profileEnabled : undefined');
    expect(page).toContain('profileEnabledChanged && draft.profileEnabled ? draft.profileId : undefined');
    expect(page).toContain('currentPolicy.profiles.length > 0 && !profileEnforcementToggleSupported');
    expect(page).toContain("window.confirm(t('api_key_policy.quota_period_change_confirm'))");
    expect(page).toContain('{quotaSupported ? <section className={styles.quotaSection}>');
    expect(page.indexOf('className={styles.quotaSection}')).toBeLessThan(page.indexOf('className={styles.profileRail}'));
    expect(page).toContain('{draft.quota?.enabled ? <>');
    expect(page).toContain("placeholder={t('api_key_policy.quota_cost_example')}");
    expect(page).toContain("(['all_time', 'past_duration', 'calendar_duration'] as const)");
    expect(page).toContain('supportsAPIKeyQuotaTimezone(snapshot.capabilities)');
    expect(page).toContain('options={buildAPIKeyQuotaTimezoneOptions(draft.quota.period.timezone)}');
    expect(client).toContain("supportedValuesOf.call(Intl, 'timeZone')");
    expect(client).toContain("profileEnabled: typeof policy.profileEnabled === 'boolean'");
    expect(page).not.toContain('list="api-key-quota-timezones"');
    expect(page).not.toContain("placeholder={t('api_key_policy.quota_timezone_example')}");
    expect(page).toContain("!validIanaTimezone(draft.quota.period.timezone)");
    expect(page).toContain("timezone: normalized.timezone?.trim() || 'UTC'");
    expect(page).toContain("timezone: quota.period.timezone ?? 'UTC'");
    expect(page).toContain("quota.period.timezone ?? 'UTC' : undefined");
    expect(styles).toContain('.quotaPeriodGrid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); align-items: start;');
    expect(styles).toContain('.quotaPeriodGrid :global(.form-group) { margin-bottom: 0; }');
    expect(styles).toContain('.quotaSelectTrigger { height: 46px;');
    expect(page.match(/triggerClassName=\{styles\.quotaSelectTrigger\}/g)).toHaveLength(4);
    expect(page).toContain('const quotaRevisionRef = useRef(0);');
    expect(page).toContain('const quotaBusyRef = useRef(false);');
    expect(page).toContain('if (revision !== quotaRevisionRef.current) return;');
    expect(page).toContain('current.policy.id !== policyId');
    expect(page).toContain('submittedDraftRevision === draftRevisionRef.current');
  });

	test('uses the standard 720px Workspace Sheet with fixed save and cancel actions', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const styles = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.module.scss'), 'utf8');
		expect(page).toContain('className={styles.policySheet}');
		expect(page).toContain('footer={');
		expect(page).toContain('disabled={!dirty || saving}');
		expect(page).not.toContain('workspaceActionBar');
		expect(styles).toContain('width: min(720px, 100vw) !important;');
  });

	test('prioritizes mappings and omits the internal policy version from the workspace', () => {
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		expect(page).not.toContain('styles.versionBox');
		expect(page).not.toContain("t('api_key_policy.version')");
		expect(page.indexOf('className={styles.mappingSection}')).toBeLessThan(page.indexOf('className={styles.policyGrid}'));
	});

	test('limits mapping targets to currently available catalog models without hiding saved unavailable selections', () => {
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		expect(resolveMappingTargetModels([], ['gpt-5', 'claude-sonnet-4'])).toEqual([
			'gpt-5',
			'claude-sonnet-4',
		]);
		expect(resolveMappingTargetModels(
			['retired-model', 'gpt-5'],
			['gpt-5', 'new-model'],
		)).toEqual(['gpt-5']);
		expect(page).toContain('const unavailableSelected = selected.filter((value) => !values.includes(value));');
		expect(page).toContain('selected.length > 0 ? selected.length : allLabel');
		expect(page).toContain('onClick={() => onChange(selected.filter((item) => item !== value))}');
		expect(page).toContain("? [...selected, value]");
		expect(page).toContain(': selected.filter((item) => item !== value)');
		expect(page).toContain("t('api_key_policy.unavailable_selections')");
	});

	test('links effective models and mapping targets to the selected providers', () => {
		expect(resolveModelsForProviders([], catalog)).toEqual(catalog.models);
		expect(resolveModelsForProviders(['openai'], catalog)).toEqual(['gpt-5']);
		expect(resolveModelsForProviders(['claude'], catalog)).toEqual(['claude-sonnet-4']);
		expect(resolveModelsForProviders(['home', 'claude'], catalog)).toEqual(catalog.models);
		expect(validateProfileInput({ ...validProfile(), providers: ['claude'] }, catalog)).toBe('models');
		expect(validateProfileInput({
			...validProfile(),
			providers: ['claude'],
			models: [],
			mappings: [{ source: 'smart', target: 'gpt-5' }],
		}, catalog)).toBe('mappings');
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		expect(page).toContain('values={availableModels}');
		const changed = updateProfileProviders(validProfile(), ['claude']);
		expect(changed.providers).toEqual(['claude']);
		expect(changed.models).toEqual(['gpt-5']);
		expect(changed.mappings).toEqual([{ source: 'smart', target: 'gpt-5' }]);
		expect(validateProfileInput(changed, catalog)).toBe('models');
		expect(page).toContain('profile: updateProfileProviders(current.profile, providers)');
		expect(page).not.toContain('const models = current.profile.models.filter((model) => providerModels.includes(model))');
		expect(page).not.toContain('mappings: current.profile.mappings.filter((mapping) => mappingTargets.includes(mapping.target))');
	});

	test('negotiates provider-model write validation without breaking older Core or Management clients', () => {
		const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
		expect(client).toContain("const API_KEY_POLICY_WRITE_FEATURES = ['provider_model_linkage', 'key_quota_cost_period'] as const;");
		expect(client).toContain('clientFeatures: [...API_KEY_POLICY_WRITE_FEATURES]');
	});

	test('validates the live catalog only when the saved profile changes', () => {
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		const validate = page.slice(page.indexOf('const validateDraft'), page.indexOf('const saveWorkspace'));
		const save = page.slice(page.indexOf('const saveWorkspace'), page.indexOf('const activateProfile'));
		expect(validate).toContain('if (validateProfile && draft.profileEnabled)');
		expect(validate).toContain('validateProfileInput(draft.profile, snapshot.catalog)');
		expect(save).toContain("const persisted = workspaceTarget.kind === 'policy'");
		expect(save).toContain('const changedProfile = draft.profileEnabled && (');
		expect(save).toContain('profileSignature(persisted) !== profileSignature(draft.profile)');
		expect(save).toContain('if (!validateDraft(changedProfile)) return;');
	});

	test('matches the account-policy status overview structure and responsive grid', () => {
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		const styles = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.module.scss'), 'utf8');
		expect(page).not.toContain('<div><small>{t(\'api_key_policy.');
		expect(page).toContain("<small>{t('api_key_policy.runtime')}</small>");
		expect(styles).toContain('@media (max-width: 980px)');
		expect(styles).toContain('.overviewItem:nth-child(-n + 2) { border-bottom: 1px solid var(--border-color); }');
	});

	test('loads and updates the explicit takeover contract', () => {
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
		expect(client).toContain("'takeover_control'");
		expect(client).toContain("'/api-key-policy-status'");
		expect(client).toContain("'/api-key-policy-takeover'");
		expect(client).toContain('policyGeneration: status.policyGeneration');
		expect(client).toContain('configuredGeneration: status.configuredGeneration');
		expect(page).toContain('const [takeoverStatus, setTakeoverStatus]');
		expect(page).toContain('active={takeoverActive}');
		expect(page).toContain('!takeoverActive && !takeoverScopeReady');
		expect(page).toContain('apiKeyPolicyApi.setTakeover(enabled, takeoverStatus)');
		expect(page).toContain("apiKeyPolicyErrorCode(error) === 'api_key_policy_state_changed'");
		expect(page).toContain("navigate('/config')");
	});

  test('keeps the draft on 409 and does not replace it when server state is reloaded for manual merge', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    expect(page).toContain("apiKeyPolicyErrorCode(error) === 'config_version_conflict'");
    expect(page).toContain('setConflict(true);');
    const reload = page.slice(page.indexOf('const reloadWorkspace'), page.indexOf('const validateDraft'));
    expect(reload).toContain('setWorkspaceTarget(target);');
    expect(reload).not.toContain('setDraft(');
  });

  test('invalidates delayed activate and danger responses and resets synchronous busy guards', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const activate = page.slice(page.indexOf('const activateProfile'), page.indexOf('const runDangerAction'));
    expect(activate).toContain('const revision = ++saveRevisionRef.current;');
    expect(activate).toContain('if (revision !== saveRevisionRef.current) return;');
    const danger = page.slice(page.indexOf('const runDangerAction'), page.indexOf('const visibleItems'));
    expect(danger).toContain('if (!dangerPolicy || !dangerKind || dangerBusyRef.current) return;');
    expect(danger).toContain('const revision = ++dangerRevisionRef.current;');
    expect(danger).toContain('if (revision !== dangerRevisionRef.current) return;');
    expect(danger).toContain("dangerPolicy.activeProfileId === draft?.profileId");
    expect(danger).toContain('!optionalProfileSupported');
    expect(page).toContain('setSaving(false);');
    expect(page).toContain('optionalProfileSupported && removingOnlyActiveProfile');
    expect(page).toContain(
      'currentPolicy && draft?.profileId && draft.profileId === currentPolicy.activeProfileId',
    );
  });

  test('reload invalidates an in-flight save without replacing edits made after submit', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const reload = page.slice(page.indexOf('const reloadWorkspace'), page.indexOf('const validateDraft'));
    const save = page.slice(page.indexOf('const saveWorkspace'), page.indexOf('const activateProfile'));
    expect(reload).toContain('const revision = ++saveRevisionRef.current;');
    expect(reload).toContain('savingRef.current = true;');
    expect(save).toContain('const submittedDraftRevision = draftRevisionRef.current;');
    expect(save).toContain('if (revision !== saveRevisionRef.current) return;');
    expect(save).toContain('if (submittedDraftRevision === draftRevisionRef.current)');
  });

  test('closes a conflicting danger dialog and leaves the action reopenable', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const danger = page.slice(page.indexOf('const runDangerAction'), page.indexOf('const visibleItems'));
    expect(danger).toContain("apiKeyPolicyErrorCode(error) === 'config_version_conflict'");
    expect(danger).toContain('setDangerPolicy(null);');
    expect(danger).toContain('setDangerKind(null);');
    expect(danger).toContain('dangerBusyRef.current = false;');
    expect(page).toContain('onClick={() => void openPolicyDeletePreview(currentPolicy)}');
    expect(page).toContain('apiKeyPolicyApi.deletePreview(policy.id)');
    expect(page).toContain('preview.version !== policy.version');
  });

  test('restarts orphan pagination on config-generation drift and never merges mixed pages', () => {
    const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
    expect(client).toContain('page.configGeneration !== first.configGeneration');
    expect(client).toContain("(error as ApiError).apiCode === 'api_key_policy_config_changed'");
    expect(client).toContain('for (let attempt = 0; attempt < 2; attempt += 1)');
  });

  test('keeps aggregate fallback scoped and preserves stale data only within one connection', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/MonitoringCenterPage.tsx'), 'utf8');
    const hook = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/features/hooks/useUsageAggregates.ts'), 'utf8');
    const analytics = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/features/hooks/useMonitoringAnalytics.ts'), 'utf8');
    expect(analytics).toContain('usageAggregates.scopeTimeRangeKey === timeRangeKey');
    expect(analytics).toContain('usageAggregates.scopeApiKeyHash === usageTrendApiKey');
    expect(page).not.toContain('scopeAPIKeyPolicyId');
    expect(page).not.toContain('scopePolicyMode');
    expect(page).toContain('if (!serverUsageTrendAnalytics || !aggregateTrendScopeMatches)');
    expect(hook).toContain('const connectionChanged = activeConnectionKeyRef.current !== connectionKey;');
    expect(hook).toContain('if (connectionChanged || datasetChanged) {');
    expect(hook).toContain('setData(null);');
    expect(hook).toContain('data?.scopeConnectionKey === connectionKey ? data : null');
    expect(page).toContain('refreshMeta(false)');
  });

  test('accepts only server-catalog providers, models, and allowed mapping targets', () => {
    expect(validateProfileInput(validProfile(), catalog)).toBeNull();
    expect(validateProfileInput({ ...validProfile(), providers: ['unknown'] }, catalog)).toBe('providers');
    expect(validateProfileInput({ ...validProfile(), models: ['unknown'] }, catalog)).toBe('models');
    expect(validateProfileInput({ ...validProfile(), mappings: [{ source: 'smart', target: 'claude-sonnet-4' }] }, catalog)).toBe('mappings');
		expect(validateProfileInput({
			...validProfile(),
			providers: [],
			models: [],
			mappings: [{ source: 'smart', target: 'claude-sonnet-4' }],
		}, catalog)).toBeNull();
		expect(validateProfileInput({
			...validProfile(),
			providers: [],
			models: [],
			mappings: [{ source: 'smart', target: 'unknown' }],
		}, catalog)).toBe('mappings');
  });

	test('documents empty provider and model selections as allow-all and keeps catalog mapping targets', () => {
		const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
		expect(page).toContain("all_providers_when_empty");
		expect(page).toContain("all_models_when_empty");
		expect(page).toContain("models.length === 0 ? current.profile.mappings");
		expect(page).toContain("const availableModels = resolveModelsForProviders(");
	});

  test('rejects duplicate or partial mapping sources', () => {
    expect(validateProfileInput({
      ...validProfile(),
      mappings: [
        { source: 'smart', target: 'gpt-5' },
        { source: 'smart', target: 'gpt-5' },
      ],
    }, catalog)).toBe('mappings');
    expect(validateProfileInput({ ...validProfile(), mappings: [{ source: '', target: 'gpt-5' }] }, catalog)).toBe('mappings');
  });

  test('clones mappings and selection arrays so server state cannot overwrite a draft', () => {
    const original = validProfile();
    const draft = cloneProfileInput(original);
    draft.providers.push('home');
    draft.models.push('claude-sonnet-4');
    draft.mappings[0].target = 'claude-sonnet-4';
    expect(original).toEqual(validProfile());
  });

  test('normalizes legacy null mapping collections at the API boundary', () => {
    const legacy = { ...validProfile(), mappings: null } as unknown as APIKeyProfileInput;
    expect(cloneProfileInput(legacy).mappings).toEqual([]);
    const client = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
    expect(client).toContain('mappings: (profile.mappings ?? []).map');
    expect(client).toContain('policy: normalizePolicy(binding.policy)');
  });
});
