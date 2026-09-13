import { afterEach, describe, expect, test } from 'bun:test';
import {
  captureQuotaCacheGeneration,
  commitIfQuotaCacheCurrent,
  useQuotaStore,
} from '../src/stores/useQuotaStore';

afterEach(() => useQuotaStore.getState().clearQuotaCache());

describe('Gemini CLI quota invalidation', () => {
  test('invalidates only the changed file and rejects its pending response', () => {
    const quota = { status: 'success' as const, buckets: [] };
    useQuotaStore.getState().setGeminiCliQuota({ 'a.json': quota, 'b.json': quota });
    const changed = captureQuotaCacheGeneration('a.json');
    const unrelated = captureQuotaCacheGeneration('b.json');

    useQuotaStore.getState().clearQuotaCache(['a.json']);

    expect(useQuotaStore.getState().geminiCliQuota).toEqual({ 'b.json': quota });
    expect(useQuotaStore.getState().geminiCliQuota['b.json']).toBe(quota);
    expect(commitIfQuotaCacheCurrent(changed, () => {
      useQuotaStore.getState().setGeminiCliQuota((prev) => ({ ...prev, 'a.json': quota }));
    })).toBe(false);
    expect(commitIfQuotaCacheCurrent(unrelated, () => {})).toBe(true);
    expect(useQuotaStore.getState().geminiCliQuota['a.json']).toBeUndefined();
  });

  test('empty invalidation is a no-op; clearing the session removes Gemini quotas', () => {
    useQuotaStore.getState().setGeminiCliQuota({ 'a.json': { status: 'loading', buckets: [] } });
    const request = captureQuotaCacheGeneration('a.json');
    const state = useQuotaStore.getState();
    state.clearQuotaCache([]);
    expect(useQuotaStore.getState()).toBe(state);
    state.clearQuotaCache();
    expect(useQuotaStore.getState().geminiCliQuota).toEqual({});
    expect(commitIfQuotaCacheCurrent(request, () => {})).toBe(false);
  });
});
