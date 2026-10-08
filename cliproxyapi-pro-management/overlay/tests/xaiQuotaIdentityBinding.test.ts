import { afterEach, beforeEach, expect, spyOn, test } from 'bun:test';
import type { TFunction } from 'i18next';
import { PRO_XAI_CONFIG } from '../src/pro/modules/quota/extensions/xaiQuotaAdapter';
import { XAI_CONFIG } from '../src/features/quota/providers/xai/data';
import { apiCallApi } from '../src/services/api/apiCall';
import { apiClient } from '../src/services/api/client';
import { useQuotaStore } from '../src/stores/useQuotaStore';
import { quotaPersistenceMiddleware as middleware } from '../src/pro/modules/quota/extensions/persistenceMiddleware';
import { sqliteQuotaCache, type QuotaCacheEntry } from '../src/pro/modules/quota/extensions/sqliteQuotaCache';
import { XAI_FREE_QUOTA_PROBE_URL } from '../src/pro/modules/quota/extensions/xaiQuota';
import { XAI_BILLING_MONTHLY_URL, XAI_BILLING_WEEKLY_URL } from '../src/utils/quota';

const t = ((key: string) => key) as TFunction;
const oldIdentity = 'xai:v2:old-subject';
const newIdentity = 'xai:v2:new-subject';
const billing = {
  mode: 'billing' as const, periodType: 'monthly' as const, usagePercent: null,
  productUsage: [], monthlyLimitCents: null, usedCents: null, includedUsedCents: null,
  onDemandCapCents: null, onDemandUsedCents: null, onDemandUsedPercent: null, usedPercent: null,
};
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const boundState = (identity = oldIdentity) => ({
  status: 'success' as const, billing: { ...billing, planType: 'x-premium' as const },
  quotaIdentityFingerprint: identity, cachedAt: 100,
});
const fixtureEntry = (identity: string | undefined): QuotaCacheEntry => ({
  id: 'xai:same.json', provider: 'xai', fileName: 'same.json', data: boundState('incorrect-body-binding'),
  cachedAt: 100, observedAt: 100, accessedAt: 100, storedAt: 100, version: 1, revision: 1,
  identityFingerprint: identity,
});
let items: QuotaCacheEntry[];
let get: ReturnType<typeof spyOn>;
let put: ReturnType<typeof spyOn>;
let probe: ReturnType<typeof spyOn> | undefined;
let upstream: ReturnType<typeof spyOn> | undefined;

beforeEach(() => {
  middleware.stop();
  useQuotaStore.getState().clearQuotaCache();
  items = [];
  get = spyOn(apiClient, 'get').mockImplementation(async (_url, config) =>
    config?.params?.stats ? { generation: 1 } : { items }
  );
  put = spyOn(apiClient, 'put').mockResolvedValue({});
});
afterEach(async () => {
  middleware.stop();
  await tick();
  get.mockRestore();
  put.mockRestore();
  probe?.mockRestore();
  upstream?.mockRestore();
  probe = undefined;
  upstream = undefined;
  useQuotaStore.getState().clearQuotaCache();
});
async function start() {
  middleware.start();
  await middleware.ensureFresh();
}
function mockFreeQuota(wait?: Promise<void>) {
  probe = spyOn(apiCallApi, 'request').mockImplementation(async (request) => {
    await wait;
    const body = request.url === XAI_BILLING_MONTHLY_URL
      ? { config: { monthlyLimit: { val: 0 }, used: { val: 0 } } }
      : { config: { currentPeriod: { type: 'weekly' } } };
    const header = request.url === XAI_FREE_QUOTA_PROBE_URL
      ? { 'x-ratelimit-limit-tokens': ['100'], 'x-ratelimit-remaining-tokens': ['70'] }
      : {};
    return { statusCode: 200, hasStatusCode: true, body, bodyText: JSON.stringify(body), header };
  });
}
function identityOf(value: unknown) {
  return (value as { quotaIdentityFingerprint?: string }).quotaIdentityFingerprint;
}

test('actual manual fetch -> success state -> store -> PUT preserves observed identity', async () => {
  mockFreeQuota();
  await start();
  const result = await PRO_XAI_CONFIG.fetchQuota({ name: 'same.json', auth_index: 'xai:index', quota_identity_fingerprint: oldIdentity }, t);
  const state = { ...PRO_XAI_CONFIG.buildSuccessState(result), cachedAt: 100 };
  useQuotaStore.getState().setXaiQuota({ 'same.json': state });
  await tick();
  expect(identityOf(state)).toBe(oldIdentity);
  expect(put.mock.calls[0]?.[1]).toMatchObject({
    provider: 'xai', fileName: 'same.json', identityFingerprint: oldIdentity,
    data: { quotaIdentityFingerprint: oldIdentity, billing: { freeQuota: { remainingTokens: 70 } } },
  });
  expect(probe?.mock.calls.some(([request]) => request.url === XAI_BILLING_WEEKLY_URL)).toBe(true);
});

test('delayed old-identity fetch never relabels the result after auth-file identity changes', async () => {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => { release = resolve; });
  mockFreeQuota(wait);
  await start();
  const file = { name: 'same.json', auth_index: 'xai:index', quota_identity_fingerprint: oldIdentity };
  const pending = PRO_XAI_CONFIG.fetchQuota(file, t);
  file.quota_identity_fingerprint = newIdentity;
  release();
  const result = await pending;
  const state = { ...PRO_XAI_CONFIG.buildSuccessState(result), cachedAt: 100 };
  let accepted = false;
  put.mockImplementation(async (_url, body) => {
    accepted = body.identityFingerprint === newIdentity;
    if (!accepted) throw Object.assign(new Error('stale observation identity'), { status: 409 });
    return {};
  });
  useQuotaStore.getState().setXaiQuota({ 'same.json': state });
  await tick();
  expect(identityOf(state)).toBe(oldIdentity);
  expect(put.mock.calls[0]?.[1].identityFingerprint).toBe(oldIdentity);
  expect(accepted).toBe(false);
});

test('replacement identity does not borrow previous plan or free quota from the same filename', async () => {
  useQuotaStore.getState().setXaiQuota({ 'same.json': { ...boundState(), billing: { ...billing, planType: 'x-premium', freeQuota: { observedAt: 100, remainingTokens: 1 } } } });
  upstream = spyOn(XAI_CONFIG, 'fetchQuota').mockResolvedValue({ ...billing });
  const result = await PRO_XAI_CONFIG.fetchQuota({ name: 'same.json', auth_index: 'xai:index', quota_identity_fingerprint: newIdentity }, t);
  expect(result.planType).toBeUndefined();
  expect(result.freeQuota).toBeUndefined();
  expect(identityOf(PRO_XAI_CONFIG.buildSuccessState(result))).toBe(newIdentity);
});

test('same stable identity still retains previous runtime observations', async () => {
  useQuotaStore.getState().setXaiQuota({ 'same.json': boundState() });
  upstream = spyOn(XAI_CONFIG, 'fetchQuota').mockResolvedValue({ ...billing });
  const result = await PRO_XAI_CONFIG.fetchQuota({ name: 'same.json', auth_index: 'rotated:index', quota_identity_fingerprint: oldIdentity }, t);
  expect(result.planType).toBe('x-premium');
  expect(identityOf(PRO_XAI_CONFIG.buildSuccessState(result))).toBe(oldIdentity);
});

test('a missing auth-file binding cannot reuse or persist prior xAI quota', async () => {
  await start();
  useQuotaStore.getState().setXaiQuota({ 'same.json': boundState() });
  await tick();
  put.mockClear();
  upstream = spyOn(XAI_CONFIG, 'fetchQuota').mockResolvedValue({ ...billing });
  const result = await PRO_XAI_CONFIG.fetchQuota({ name: 'same.json', auth_index: 'xai:index' }, t);
  expect(result.planType).toBeUndefined();
  useQuotaStore.getState().setXaiQuota({ 'same.json': { ...PRO_XAI_CONFIG.buildSuccessState(result), cachedAt: 101 } });
  await tick();
  expect(put).not.toHaveBeenCalled();
});

test('hydrate takes the quota-cache row binding and preserves it during later writes', async () => {
  items = [fixtureEntry(oldIdentity)];
  await start();
  const state = useQuotaStore.getState().xaiQuota['same.json'];
  expect(identityOf(state)).toBe(oldIdentity);
  expect(put).not.toHaveBeenCalled();
  useQuotaStore.getState().setXaiQuota({ 'same.json': { ...state, cachedAt: 101 } });
  await tick();
  expect(put.mock.calls[0]?.[1]).toMatchObject({ identityFingerprint: oldIdentity, data: { quotaIdentityFingerprint: oldIdentity } });
});

test('legacy xAI cache rows without a row identity are not hydrated as trusted quota', async () => {
  items = [fixtureEntry(undefined)];
  await start();
  expect(useQuotaStore.getState().xaiQuota['same.json']).toBeUndefined();
  expect(put).not.toHaveBeenCalled();
});

test('direct SQLite writes bind the observation without consulting the current auth identity', async () => {
  await sqliteQuotaCache.set('xai', 'same.json', boundState(), 100);
  expect(put.mock.calls[0]?.[1].identityFingerprint).toBe(oldIdentity);
});

test('unbound direct xAI writes fail locally instead of retrying an invalid observation', async () => {
  const result = await sqliteQuotaCache.set('xai', 'same.json', { status: 'success', billing }, 100);
  expect(result).toBe(false);
  expect(put).not.toHaveBeenCalled();
});

test('other provider persistence retains its existing payload', async () => {
  await sqliteQuotaCache.set('codex', 'same.json', { status: 'success', windows: [] }, 100);
  expect(put.mock.calls[0]?.[1].identityFingerprint).toBeUndefined();
});
