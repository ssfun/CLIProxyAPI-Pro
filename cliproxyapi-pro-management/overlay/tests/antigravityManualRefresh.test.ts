import { afterEach, beforeEach, describe, expect, spyOn, test } from 'bun:test';
import type { TFunction } from 'i18next';
import { ANTIGRAVITY_CONFIG } from '../src/features/quota/providers/antigravity/data';
import { antigravitySubscriptionApi, apiCallApi } from '../src/services/api';
import { useQuotaStore, captureQuotaCacheGeneration, commitIfQuotaCacheCurrent } from '../src/stores/useQuotaStore';
import { ANTIGRAVITY_QUOTA_URLS, getStatusFromError } from '../src/utils/quota';
import { getQuotaCacheKey } from '../src/utils/quota/identity';

const file = { name: 'ag.json', auth_index: 'ag-index', project_id: 'project' };
const key = getQuotaCacheKey(file);
const t = ((key: string) => key) as TFunction;
const oldQuota = {
  status: 'success' as const,
  groups: [{ id: 'gemini', label: 'Gemini Models', buckets: [{ id: '5h', label: '5h', remainingFraction: 0.42 }] }],
  subscription: { plan: 'pro' as const, tierName: 'Pro', tierId: 'g1-pro-tier' },
  serverTimeOffsetMs: 1234,
  cachedAt: 123456,
};
const valid = { groups: [{ displayName: 'Gemini Models', buckets: [{ remainingFraction: 0 }] }] };
let responses: Array<{ statusCode: number; body: unknown }>;
let request: ReturnType<typeof spyOn>;
let subscription: ReturnType<typeof spyOn>;

beforeEach(() => {
  useQuotaStore.getState().clearQuotaCache();
  useQuotaStore.getState().setAntigravityQuota({ [key]: structuredClone(oldQuota) });
  responses = [];
  request = spyOn(apiCallApi, 'request').mockImplementation(async () => {
    const response = responses.shift();
    if (!response) throw new Error('fixture response exhausted');
    return { ...response, hasStatusCode: true, header: {}, bodyText: JSON.stringify(response.body) };
  });
  subscription = spyOn(antigravitySubscriptionApi, 'get').mockResolvedValue(oldQuota.subscription);
});
afterEach(() => {
  request.mockRestore();
  subscription.mockRestore();
  useQuotaStore.getState().clearQuotaCache();
});

// Same adapter -> generation fence -> updater sequence used by both quota hooks
// and AuthFileQuotaSection; the store and provider are the real implementations.
async function refresh(afterLoading?: () => void) {
  const generation = captureQuotaCacheGeneration(file.name);
  const setQuota = useQuotaStore.getState().setAntigravityQuota;
  setQuota((previous) => ({ ...previous, [key]: ANTIGRAVITY_CONFIG.buildLoadingState() }));
  afterLoading?.();
  try {
    const data = await ANTIGRAVITY_CONFIG.fetchQuota(file, t);
    commitIfQuotaCacheCurrent(generation, () => {
      setQuota((previous) => ({ ...previous, [key]: ANTIGRAVITY_CONFIG.buildSuccessState(data) }));
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : 'unknown';
    commitIfQuotaCacheCurrent(generation, () => {
      setQuota((previous) => ({ ...previous, [key]: ANTIGRAVITY_CONFIG.buildErrorState(message, getStatusFromError(error)) }));
    });
  }
  return useQuotaStore.getState().antigravityQuota[key];
}
const repeat = (body: unknown, statusCode = 200) => {
  responses = ANTIGRAVITY_QUOTA_URLS.map(() => ({ statusCode, body }));
};
function expectOldData(state: ReturnType<typeof refresh> extends Promise<infer T> ? T : never) {
  expect(state.groups).toEqual(oldQuota.groups);
  expect(state.subscription).toEqual(oldQuota.subscription);
  expect(state.serverTimeOffsetMs).toBe(oldQuota.serverTimeOffsetMs);
  expect((state as typeof oldQuota).cachedAt).toBe(oldQuota.cachedAt);
}

describe('Antigravity manual refresh through provider and real quota store', () => {
  for (const [name, body] of [
    ['missing groups', {}],
    ['unreadable JSON', 'not-json'],
    ['null body', null],
    ['array body', []],
    ['nonarray groups', { groups: {} }],
    ['null group', { groups: [null] }],
    ['missing buckets', { groups: [{ displayName: 'Gemini Models' }] }],
    ['unreadable fraction', { groups: [{ buckets: [{ remainingFraction: 'unreadable' }] }] }],
    ['partially malformed buckets', { groups: [{ buckets: [{ remainingFraction: 0.5 }, { remainingFraction: 'unreadable' }] }] }],
    ['error disguised as empty quota', { error: { message: 'upstream failed' }, groups: [] }],
  ] as const) {
    test(`${name} in HTTP 200 reports failure and preserves cached quota`, async () => {
      repeat(body);
      const state = await refresh(() => {
        const loading = useQuotaStore.getState().antigravityQuota[key];
        expect(loading.status).toBe('loading');
        expectOldData(loading);
      });
      expect(state.status).toBe('error');
      expect(state.error).toBeTruthy();
      expectOldData(state);
      expect(request).toHaveBeenCalledTimes(ANTIGRAVITY_QUOTA_URLS.length);
    });
  }
  test('explicit empty groups is valid and replaces previously populated quota', async () => {
    repeat({ groups: [] });
    const state = await refresh();
    expect(state.status).toBe('success');
    expect(state.groups).toEqual([]);
    expect(request).toHaveBeenCalledTimes(1);
  });
  test('explicit empty buckets is valid empty quota', async () => {
    repeat({ groups: [{ displayName: 'Gemini Models', buckets: [] }] });
    const state = await refresh();
    expect(state.status).toBe('success');
    expect(state.groups).toEqual([]);
    expect(request).toHaveBeenCalledTimes(1);
  });
  test('fallback endpoint recovers after malformed 200', async () => {
    responses = [{ statusCode: 200, body: {} }, { statusCode: 200, body: valid }];
    const state = await refresh();
    expect(state.status).toBe('success');
    expect(state.groups[0].buckets[0].remainingFraction).toBe(0);
    expect(request).toHaveBeenCalledTimes(2);
  });
  test('HTTP errors and repeated failures retain quota until successful recovery', async () => {
    repeat({}, 403);
    let state = await refresh();
    expect(state.status).toBe('error');
    expect(state.errorStatus).toBe(403);
    expectOldData(state);
    repeat({});
    state = await refresh();
    expect(state.status).toBe('error');
    expectOldData(state);
    repeat(valid);
    state = await refresh();
    expect(state.status).toBe('success');
    expect(state.error).toBeUndefined();
    expect(state.groups[0].buckets[0].remainingFraction).toBe(0);
  });
  test('first failed refresh does not invent cached quota', async () => {
    useQuotaStore.getState().clearQuotaCache();
    repeat({});
    const state = await refresh();
    expect(state.status).toBe('error');
    expect(state.groups).toEqual([]);
    expect(state.subscription).toBeNull();
  });
  test('credential invalidation does not resurrect quota from a stale failed refresh', async () => {
    repeat({});
    await refresh(() => useQuotaStore.getState().clearQuotaCache([file.name]));
    expect(useQuotaStore.getState().antigravityQuota[key]).toBeUndefined();
  });
  test('explicit cache deletion and other providers retain their existing semantics', () => {
    useQuotaStore.getState().setAntigravityQuota({});
    expect(useQuotaStore.getState().antigravityQuota).toEqual({});
    useQuotaStore.getState().setCodexQuota({ other: { status: 'success', windows: [], planType: 'pro' } });
    useQuotaStore.getState().setCodexQuota({ other: { status: 'error', windows: [], error: 'failed' } });
    expect(useQuotaStore.getState().codexQuota.other.planType).toBeUndefined();
  });
});
