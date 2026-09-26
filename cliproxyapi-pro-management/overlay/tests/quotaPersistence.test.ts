import { afterEach, beforeEach, expect, spyOn, test } from 'bun:test';
import { apiClient } from '../src/services/api/client';
import { useQuotaStore } from '../src/stores/useQuotaStore';
import { quotaPersistenceMiddleware as middleware } from '../src/pro/modules/quota/extensions/persistenceMiddleware';

const quota = (cachedAt: number, label = 'old') => ({
  status: 'success' as const,
  windows: [],
  cachedAt,
  planType: label,
});
const entry = (cachedAt: number, label = 'old') => ({
  id: 'codex:a.json',
  provider: 'codex',
  fileName: 'a.json',
  data: quota(cachedAt, label),
  cachedAt,
  observedAt: cachedAt,
});
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
let generation = 10;
let items = [entry(100)];
let read: () => Promise<{ items: typeof items }>;
let get: ReturnType<typeof spyOn>;
let put: ReturnType<typeof spyOn>;

beforeEach(() => {
  middleware.stop();
  useQuotaStore.getState().clearQuotaCache();
  generation += 10;
  items = [entry(100)];
  read = async () => ({ items });
  get = spyOn(apiClient, 'get').mockImplementation(async (_url, config) =>
    config?.params?.stats ? { generation } : read()
  );
  put = spyOn(apiClient, 'put').mockResolvedValue({});
  middleware.markStale();
});
afterEach(async () => {
  middleware.stop();
  await tick();
  get.mockRestore();
  put.mockRestore();
});

async function start() {
  middleware.start();
  await middleware.ensureFresh();
}

test('failed reads preserve hydrated state and retry the same generation', async () => {
  await start();
  generation++;
  read = async () => {
    throw new Error('offline');
  };
  await middleware.ensureFresh();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(100));
  items = [entry(200)];
  read = async () => ({ items });
  await middleware.ensureFresh();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(200));
});

test('live refresh during hydration is kept and written', async () => {
  await start();
  generation++;
  const pending = deferred<{ items: typeof items }>();
  read = () => pending.promise;
  const refresh = middleware.ensureFresh();
  await tick();
  useQuotaStore.getState().setCodexQuota({ 'a.json': quota(300, 'live') });
  pending.resolve({ items: [entry(200)] });
  await refresh;
  await tick();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(300, 'live'));
  expect(put.mock.calls.some(([, body]) => body.data.cachedAt === 300)).toBe(true);
});

test('older cache cannot overwrite a newer local success', async () => {
  useQuotaStore.getState().setCodexQuota({ 'a.json': quota(300) });
  await start();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(300));
});

test.each(['file', 'session', 'stop'] as const)(
  '%s invalidation fences pending hydration',
  async (kind) => {
    await start();
    generation++;
    const pending = deferred<{ items: typeof items }>();
    read = () => pending.promise;
    const refresh = middleware.ensureFresh();
    await tick();
    if (kind === 'stop') middleware.stop();
    useQuotaStore.getState().clearQuotaCache(kind === 'file' ? ['a.json'] : undefined);
    pending.resolve({ items: [entry(200)] });
    await refresh;
    expect(useQuotaStore.getState().codexQuota['a.json']).toBeUndefined();
  }
);

test('markStale during a read survives its completion', async () => {
  await start();
  generation++;
  const pending = deferred<{ items: typeof items }>();
  read = () => pending.promise;
  const refresh = middleware.ensureFresh();
  await tick();
  middleware.markStale();
  pending.resolve({ items: [entry(200)] });
  await refresh;
  read = async () => ({ items: [entry(300)] });
  await middleware.ensureFresh();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(300));
});

test('restart accepts a lower backend generation', async () => {
  await start();
  middleware.stop();
  generation = 1;
  items = [entry(200)];
  await start();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(200));
});

test('same timestamp with changed payload is persisted', async () => {
  await start();
  useQuotaStore.getState().setCodexQuota({ 'a.json': quota(100, 'updated') });
  await tick();
  expect(put.mock.calls.some(([, body]) => body.data.planType === 'updated')).toBe(true);
});

test('backend deletion removes hydrated state without mirroring it', async () => {
  await start();
  expect(put).not.toHaveBeenCalled();
  items = [];
  generation++;
  await middleware.ensureFresh();
  expect(useQuotaStore.getState().codexQuota['a.json']).toBeUndefined();
  expect(put).not.toHaveBeenCalled();
});

test('backend deletion preserves locally refreshed state', async () => {
  await start();
  useQuotaStore.getState().setCodexQuota({ 'a.json': quota(300) });
  await tick();
  items = [];
  generation++;
  await middleware.ensureFresh();
  expect(useQuotaStore.getState().codexQuota['a.json']).toEqual(quota(300));
});
