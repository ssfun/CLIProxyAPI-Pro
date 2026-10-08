import { describe, expect, test } from 'bun:test';
import type { TFunction } from 'i18next';
import type { AuthFileItem } from '@/types';
import { resolveAuthProvider } from '@/utils/quota';
import {
  ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS,
  saveAccountInspectionConfigurableSettings,
} from '../src/pro/modules/inspection/features/accountInspection';
import {
  buildAuthFileAccountStats,
  isInspectableAccountInspectionAuthFile,
} from '../src/pro/modules/inspection/features/accountInspectionPageModel';
import { resolveAccountPlanLabel, type AccountPlanQuotaStore } from '../src/pro/modules/quota/accountPlan';
import {
  normalizePersistedQuotaState,
  selectPreferredQuotaCacheEntries,
} from '../src/pro/modules/quota/extensions/normalizedQuotaSnapshot';
import type { QuotaCacheEntry } from '../src/pro/modules/quota/extensions/sqliteQuotaCache';

const quotaStore = (): AccountPlanQuotaStore => ({
  antigravityQuota: {}, claudeQuota: {}, codexQuota: {}, devinQuota: {},
  geminiCliQuota: {}, kimiQuota: {}, metaQuota: {}, xaiQuota: {},
});
const t = ((key: string) => key) as TFunction;
const devin = (remainingPercent: number | null = 5) => ({
  status: 'success' as const,
  windows: [{ id: 'daily' as const, remainingPercent, resetAtMs: null, periodHours: 24 }],
  observedAtMs: 100, plan: 'pro', planStartMs: null, planEndMs: null,
});
const meta = (usedPercent: number | null = 95) => ({
  status: 'success' as const,
  data: { planName: 'Muse Pro', isSubscriptionActive: true, windows: [{ id: 'weekly' as const, usedPercent }] },
});

describe('inspection/manual provider UI coverage', () => {
  test('includes credential providers while excluding API-key-only records', () => {
    for (const provider of ['devin', 'meta']) {
      expect(ACCOUNT_INSPECTION_SUPPORTED_PROVIDERS).toContain(provider);
      expect(isInspectableAccountInspectionAuthFile({ name: `${provider}.json`, provider })).toBe(true);
      expect(isInspectableAccountInspectionAuthFile({ name: `${provider}.json`, provider, api_key: 'key' })).toBe(false);
      expect(saveAccountInspectionConfigurableSettings({ targetType: provider }).targetType).toBe(provider);
    }
  });

  test('keeps Devin OAuth listing inspectable before a file path exists', () => {
    // Real buildAuthFileEntryLocked exports AccountInfo as account_type and
    // omits the api_key/session_token attributes of CreateAuthRecord.
    const listed: AuthFileItem = {
      id: 'devin-user', name: 'devin-user.json', provider: 'devin', type: 'devin',
      label: 'Devin (username)', account_type: 'oauth', source: 'memory',
      disabled: false, status: 'active',
    };
    expect(isInspectableAccountInspectionAuthFile(listed)).toBe(true);
    expect(isInspectableAccountInspectionAuthFile({ ...listed, api_key: 'session-key' })).toBe(true);
    expect(isInspectableAccountInspectionAuthFile({ ...listed, api_key: 'config-key', source: 'config:devin' })).toBe(false);
    expect(isInspectableAccountInspectionAuthFile({ ...listed, account_type: 'api_key', api_key: 'key' })).toBe(false);
    expect(buildAuthFileAccountStats([listed, { ...listed, name: 'compat.json', api_key: 'session-key' }], quotaStore(), 90, 'max-used').total).toBe(2);
  });

  test('normalizes Kimi international aliases for selection and cached plan display', () => {
    const store = quotaStore();
    store.kimiQuota['kimi.json'] = { status: 'success', rows: [], planType: 'plan_pro' };
    for (const provider of ['kimi', 'kimi-ai', 'kimi.ai']) {
      const authFile: AuthFileItem = { name: 'kimi.json', provider };
      expect(resolveAuthProvider(authFile)).toBe('kimi');
      expect(isInspectableAccountInspectionAuthFile(authFile)).toBe(true);
      expect(saveAccountInspectionConfigurableSettings({ targetType: provider }).targetType).toBe('kimi');
      expect(resolveAccountPlanLabel({ authFile, provider, quotaStore: store, t })).toBe('Pro');
    }
  });

  test('reads manual Devin and Meta cache shapes for availability and plan labels', () => {
    const store = quotaStore();
    store.devinQuota['devin.json'] = devin();
    store.metaQuota['meta.json'] = meta();
    const files = ['devin', 'meta'].map((provider) => ({ name: `${provider}.json`, provider }));
    const stats = buildAuthFileAccountStats(files, store, 90, 'max-used');
    expect(stats).toMatchObject({ total: 2, providerCount: 2, quotaLow: 2, highAvailable: 0 });
    expect(stats.providers.map((item) => item.provider).sort()).toEqual(['devin', 'meta']);
    expect(resolveAccountPlanLabel({ authFile: files[0], quotaStore: store, t })).toBe('Pro');
    expect(resolveAccountPlanLabel({ authFile: files[1], quotaStore: store, t })).toBe('Muse Pro');
    store.devinQuota['devin.json'] = devin(30);
    store.metaQuota['meta.json'] = meta(20);
    expect(buildAuthFileAccountStats(files, store, 90, 'max-used')).toMatchObject({ quotaLow: 0, highAvailable: 2 });
    store.devinQuota['devin.json'] = { ...devin(0), status: 'error' };
    store.metaQuota['meta.json'] = { ...meta(100), status: 'loading' };
    expect(buildAuthFileAccountStats(files, store, 90, 'max-used').quotaLow).toBe(0);
    store.devinQuota['devin.json'] = devin(null);
    store.metaQuota['meta.json'] = meta(null);
    expect(buildAuthFileAccountStats(files, store, 90, 'max-used').quotaLow).toBe(0);
  });

  test('counts Gemini fractional remaining quota against the configured threshold', () => {
    const store = quotaStore();
    store.geminiCliQuota['gemini.json'] = { status: 'success', buckets: [{ id: 'pro', label: 'Pro', remainingFraction: 0.05, remainingAmount: null, tokenType: null, modelIds: [] }] };
    expect(buildAuthFileAccountStats([{ name: 'gemini.json', provider: 'gemini-cli' }], store, 90, 'max-used').quotaLow).toBe(1);
  });

  test('hydrates the freshest compatible manual/inspection Devin and Meta snapshots', () => {
    for (const [provider, data] of [['devin', devin()], ['meta', meta()]] as const) {
      const entries = [
        { provider, fileName: 'account.json', plugin: 'quota', cachedAt: 1, observedAt: 1, data },
        { provider, fileName: 'account.json', plugin: 'inspection', cachedAt: 2, observedAt: 2, data },
        { provider, fileName: 'account.json', plugin: 'other', cachedAt: 3, observedAt: 3, data: { status: 'success', token: 'private' } },
      ] as unknown as QuotaCacheEntry[];
      const chosen = selectPreferredQuotaCacheEntries(provider, entries).get('account.json');
      expect(chosen?.cachedAt).toBe(2);
      expect(normalizePersistedQuotaState(provider, chosen?.data, 2)).toEqual(data);
    }
  });
});
