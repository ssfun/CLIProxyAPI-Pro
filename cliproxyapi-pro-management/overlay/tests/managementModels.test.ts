import { describe, expect, test } from 'bun:test';
import { createManagementModelsStore } from '../src/pro/system/useManagementModelsStore';
import type { ModelInfo } from '../src/utils/models';

const deferred = () => {
  let resolve!: (models: ModelInfo[]) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<ModelInfo[]>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};

describe('management model catalog', () => {
  test('shares pending requests and cache, but refresh fetches a new catalog', async () => {
    let calls = 0;
    const store = createManagementModelsStore(async () => [{ name: `model-${++calls}` }]);
    const first = store.getState().fetchModels('server');
    const second = store.getState().fetchModels('server');
    expect(await first).toEqual(await second);
    await store.getState().fetchModels('server');
    expect(calls).toBe(1);
    await store.getState().fetchModels('server', true);
    expect(store.getState().models).toEqual([{ name: 'model-2' }]);
  });

  test('old connection responses cannot replace the current catalog', async () => {
    const old = deferred();
    let calls = 0;
    const store = createManagementModelsStore(() =>
      ++calls === 1 ? old.promise : Promise.resolve([{ name: 'current' }])
    );
    const first = store.getState().fetchModels('old-server');
    store.getState().clearCache();
    await store.getState().fetchModels('new-server');
    old.resolve([{ name: 'old' }]);
    await first;
    expect(store.getState().models).toEqual([{ name: 'current' }]);
  });

  test('a failed refresh invalidates cached results and allows retry', async () => {
    let calls = 0;
    const store = createManagementModelsStore(async () => {
      if (++calls === 2) throw new Error('unavailable');
      return [{ name: `model-${calls}` }];
    });
    await store.getState().fetchModels('server');
    await expect(store.getState().fetchModels('server', true)).rejects.toThrow('unavailable');
    expect(store.getState().models).toEqual([]);
    await store.getState().fetchModels('server');
    expect(store.getState().models).toEqual([{ name: 'model-3' }]);
  });

  test('a superseded failure cannot clear a successful forced refresh', async () => {
    const old = deferred();
    let calls = 0;
    const store = createManagementModelsStore(() =>
      ++calls === 1 ? old.promise : Promise.resolve([{ name: 'fresh' }])
    );
    const first = store.getState().fetchModels('server');
    await store.getState().fetchModels('server', true);
    old.reject(new Error('old failure'));
    await expect(first).rejects.toThrow('old failure');
    expect(store.getState().models).toEqual([{ name: 'fresh' }]);
    expect(store.getState().error).toBeNull();
  });
});
