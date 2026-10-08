import { afterEach, beforeEach, expect, spyOn, test } from 'bun:test';
import { apiClient } from '../src/services/api/client';
import { useQuotaStore } from '../src/stores/useQuotaStore';
import { quotaPersistenceMiddleware as middleware } from '../src/pro/modules/quota/extensions/persistenceMiddleware';

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const waitForRetry = () => new Promise((resolve) => setTimeout(resolve, 1_080));
const billing = {
  mode: 'billing' as const, periodType: 'monthly' as const, usagePercent: null,
  productUsage: [], monthlyLimitCents: 0, usedCents: 0, includedUsedCents: null,
  onDemandCapCents: null, onDemandUsedCents: null, onDemandUsedPercent: null, usedPercent: null,
};
const state = (quotaIdentityFingerprint: string, cachedAt = 100) => ({
  status: 'success' as const, billing, quotaIdentityFingerprint, cachedAt,
});
let get: ReturnType<typeof spyOn>;
let put: ReturnType<typeof spyOn>;

beforeEach(async () => {
  middleware.stop();
  useQuotaStore.getState().clearQuotaCache();
  get = spyOn(apiClient, 'get').mockImplementation(async (_url, config) =>
    config?.params?.stats ? { generation: 1 } : { items: [] }
  );
  put = spyOn(apiClient, 'put').mockResolvedValue({});
  middleware.start();
  await middleware.ensureFresh();
});
afterEach(async () => {
  middleware.stop();
  await tick();
  get.mockRestore();
  put.mockRestore();
  useQuotaStore.getState().clearQuotaCache();
});
const staleError = () => Object.assign(new Error('quota identity mismatch'), { status: 409 });

test('xAI identity 409 removes stale observation and settles its queue without retry', async () => {
  put.mockRejectedValue(staleError());
  useQuotaStore.getState().setXaiQuota({ 'same.json': state('old-identity') });
  await tick();
  expect(useQuotaStore.getState().xaiQuota['same.json']).toBeUndefined();
  await waitForRetry();
  expect(put).toHaveBeenCalledTimes(1);
});

test('delayed old 409 cannot delete or delay a newly queued identity for the same file', async () => {
  let rejectOld!: (error: Error) => void;
  const pending = new Promise((_, reject) => { rejectOld = reject; });
  put.mockImplementationOnce(() => pending).mockResolvedValue({});
  useQuotaStore.getState().setXaiQuota({ 'same.json': state('old-identity') });
  await tick();
  const replacement = state('new-identity', 200);
  useQuotaStore.getState().setXaiQuota({ 'same.json': replacement });
  rejectOld(staleError());
  await tick();
  expect(useQuotaStore.getState().xaiQuota['same.json']).toEqual(replacement);
  expect(put).toHaveBeenCalledTimes(2);
  expect(put.mock.calls[1]?.[1]).toMatchObject({
    identityFingerprint: 'new-identity', data: { cachedAt: 200 },
  });
  await waitForRetry();
  expect(put).toHaveBeenCalledTimes(2);
});

test('delayed 409 also preserves a newer observation of the same identity', async () => {
  let rejectOld!: (error: Error) => void;
  const pending = new Promise((_, reject) => { rejectOld = reject; });
  put.mockImplementationOnce(() => pending).mockResolvedValue({});
  useQuotaStore.getState().setXaiQuota({ 'same.json': state('same-identity') });
  await tick();
  const replacement = state('same-identity', 200);
  useQuotaStore.getState().setXaiQuota({ 'same.json': replacement });
  rejectOld(staleError());
  await tick();
  expect(useQuotaStore.getState().xaiQuota['same.json']).toEqual(replacement);
  expect(put).toHaveBeenCalledTimes(2);
});

test('old-connection 409 cannot touch new session state or restart old queue', async () => {
  let rejectOld!: (error: Error) => void;
  const pending = new Promise((_, reject) => { rejectOld = reject; });
  put.mockImplementationOnce(() => pending).mockResolvedValue({});
  useQuotaStore.getState().setXaiQuota({ 'same.json': state('old-identity') });
  await tick();
  middleware.stop();
  useQuotaStore.getState().clearQuotaCache();
  middleware.start();
  await middleware.ensureFresh();
  const replacement = state('new-identity', 200);
  useQuotaStore.getState().setXaiQuota({ 'same.json': replacement });
  await tick();
  rejectOld(staleError());
  await tick();
  expect(useQuotaStore.getState().xaiQuota['same.json']).toEqual(replacement);
  expect(put).toHaveBeenCalledTimes(2);
});

test('xAI network failures remain retryable and preserve current observation', async () => {
  put.mockRejectedValueOnce(new Error('offline')).mockResolvedValue({});
  const observation = state('current-identity');
  useQuotaStore.getState().setXaiQuota({ 'same.json': observation });
  await tick();
  expect(useQuotaStore.getState().xaiQuota['same.json']).toEqual(observation);
  await waitForRetry();
  expect(put).toHaveBeenCalledTimes(2);
  expect(put.mock.calls[1]?.[1].identityFingerprint).toBe('current-identity');
});

test('other provider 409 retains the existing retry behavior', async () => {
  put.mockRejectedValueOnce(staleError()).mockResolvedValue({});
  useQuotaStore.getState().setCodexQuota({ 'same.json': { status: 'success', windows: [] } });
  await tick();
  expect(useQuotaStore.getState().codexQuota['same.json'].status).toBe('success');
  await waitForRetry();
  expect(put).toHaveBeenCalledTimes(2);
});
