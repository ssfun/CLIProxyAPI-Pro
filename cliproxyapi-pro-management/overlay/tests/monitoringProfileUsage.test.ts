import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  buildProfileFilterOptions,
  resolveUsageProfileSnapshot,
} from '@/pro/modules/monitoring/features/profileUsage';
import {
  buildMonitoringUsageLocationState,
  readMonitoringUsageLocationState,
} from '@/pro/shared/monitoringNavigation';

const copy = {
  allProfiles: 'All profiles',
  deleted: (name: string) => `${name} (deleted)`,
};

describe('monitoring API key and Profile usage navigation', () => {
  test('keeps the API key hash in route state instead of the URL', () => {
    const state = buildMonitoringUsageLocationState({
      apiKeyHash: 'hash-1',
      apiKeyLabel: 'sk-****-01',
      profileId: 'profile-1',
      profileName: 'Current',
    });
    expect(readMonitoringUsageLocationState(state)).toEqual({
      apiKeyHash: 'hash-1',
      apiKeyLabel: 'sk-****-01',
      profileId: 'profile-1',
      profileName: 'Current',
    });
    expect(JSON.stringify(state)).not.toContain('api_key_policy_id');
  });

  test('filters a renamed Profile by stable ID while showing only its current name', () => {
    const options = buildProfileFilterOptions({
      observations: [
        { profileId: 'profile-1', profileName: 'Old', timestampMs: 100 },
        { profileId: 'profile-1', profileName: 'New', timestampMs: 200 },
      ],
      currentNames: new Map([['profile-1', 'Current']]),
      currentNamesLoaded: true,
      selectedProfileId: 'profile-1',
      selectedProfileName: '',
      copy,
    });
    expect(options).toEqual([
      { value: 'all', label: 'All profiles' },
      { value: 'profile-1', label: 'Current' },
    ]);
  });

  test('keeps historical snapshot names immutable and labels deleted Profiles', () => {
    expect(resolveUsageProfileSnapshot('Old name', 'profile-1', 'No profile')).toBe('Old name');
    expect(resolveUsageProfileSnapshot('', 'profile-1', 'No profile')).toBe('profile-1');
    expect(resolveUsageProfileSnapshot('', '', 'No profile')).toBe('No profile');
    expect(buildProfileFilterOptions({
      observations: [{ profileId: 'deleted-profile', profileName: 'Archived', timestampMs: 100 }],
      currentNames: new Map(),
      currentNamesLoaded: true,
      selectedProfileId: 'all',
      selectedProfileName: '',
      copy,
    })).toContainEqual({ value: 'deleted-profile', label: 'Archived (deleted)' });
  });

  test('removes policy and policy-mode filters and renders Profile under the API key', () => {
    const policyPage = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
    const policyClient = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts'), 'utf8');
    const monitoringPage = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/MonitoringCenterPage.tsx'), 'utf8');
    const apiKeyCell = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/features/components/MonitoringApiKeyCell.tsx'), 'utf8');
    const preferences = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/features/realtimeLogPreferences.ts'), 'utf8');
    const baseStyles = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/features/styles/_base.scss'), 'utf8');
    expect(policyPage).toContain("navigate('/monitoring#request-events'");
    expect(policyPage).toContain('apiKeyPolicyApi.usageTarget(binding.keyRef)');
    expect(policyClient).toContain("'/api-key-policy-usage-target'");
    expect(policyPage).toContain('apiKeyHash,');
    expect(policyPage).not.toContain('/monitoring?api_key_policy_id=');
    expect(monitoringPage).not.toContain('selectedAPIKeyPolicy');
    expect(monitoringPage).not.toContain('selectedPolicyMode');
    expect(monitoringPage).toContain('profileFilterObservations');
    expect(monitoringPage).toContain('...filteredRows');
    expect(monitoringPage).toContain('PROFILE_CATALOG_REFRESH_MS');
    expect(monitoringPage).toContain('apiKeyPolicyApi.profileCatalog()');
    expect(monitoringPage).not.toContain('apiKeyPolicyApi.bindings()');
    expect(monitoringPage).toContain('profileCatalogRequestRef.current');
    expect(monitoringPage).toContain('profileCatalogFetchedAtRef.current');
    expect(monitoringPage).toContain('profileCatalogGenerationRef.current === catalog.policyGeneration');
    expect(monitoringPage).toContain('<MonitoringApiKeyCell apiKey={row.clientApiKey} profileSnapshot={profileSnapshot} />');
    expect(apiKeyCell).toContain('styles.realtimeApiKeyCell');
    expect(monitoringPage).toContain("resolveUsageProfileSnapshot(row.profileName, row.profileId, '')");
    expect(apiKeyCell).toContain('{profileSnapshot ? <small title={profileSnapshot}>{profileSnapshot}</small> : null}');
    expect(monitoringPage).not.toContain("t('monitoring.api_key_profile'");
    expect(monitoringPage).toContain('profileId: selectedProfile');
    expect(preferences).toContain('apiKey: 168');
    expect(baseStyles).toContain('repeat(5, minmax(0, 1fr))');
    expect(baseStyles).toContain('.realtimeFilterSelectTrigger');
  });
});
