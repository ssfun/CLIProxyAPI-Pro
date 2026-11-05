import { afterEach, describe, expect, test } from 'bun:test';
import { apiClient } from '../src/services/api/client';

type CapturedConfig = { headers: Record<string, unknown>; signal?: AbortSignal };
type CapturedResponse = { config: CapturedConfig; headers: Record<string, string>; data: unknown };
type InterceptorAccess = {
  request: { handlers: Array<{ fulfilled: (config: CapturedConfig) => CapturedConfig }> };
  response: { handlers: Array<{
    fulfilled: (response: CapturedResponse) => unknown;
    rejected: (error: { isAxiosError: boolean; config: CapturedConfig; response: { status: number } }) => Promise<never>;
  }> };
};

type AxiosAdapter = (config: unknown) => Promise<unknown>;

const internalClient = apiClient as unknown as {
  instance: { defaults: { adapter: AxiosAdapter | AxiosAdapter[] | undefined } };
};

const originalAdapter = internalClient.instance.defaults.adapter;

afterEach(() => {
  internalClient.instance.defaults.adapter = originalAdapter;
  apiClient.setConfig({ apiBase: '', managementKey: '' });
});

describe('API client connection isolation', () => {
  test('preserves native revision semantics for unchanged and ABA connections', () => {
    apiClient.setConfig({ apiBase: 'https://a.example.com', managementKey: 'key' });
    const revision = apiClient.getConnectionRevision();
    apiClient.setConfig({ apiBase: 'https://a.example.com', managementKey: 'key', timeout: 123 });
    expect(apiClient.getConnectionRevision()).toBe(revision);
    apiClient.setConfig({ apiBase: 'https://b.example.com', managementKey: 'key' });
    apiClient.setConfig({ apiBase: 'https://a.example.com', managementKey: 'key' });
    expect(apiClient.getConnectionRevision()).toBe(revision + 2);
  });

  test('key-only changes advance the same native revision and abort requests', async () => {
    let requestSignal: AbortSignal | undefined;
    let complete: (() => void) | undefined;
    internalClient.instance.defaults.adapter = ((config: unknown) => new Promise((resolve) => {
      requestSignal = (config as CapturedConfig).signal;
      complete = () => resolve({ data: {}, status: 200, statusText: 'OK', headers: {}, config });
    })) as AxiosAdapter;
    apiClient.setConfig({ apiBase: 'https://same.example.com', managementKey: 'old' });
    const revision = apiClient.getConnectionRevision();
    const pending = apiClient.get('/usage');
    while (!complete) await Promise.resolve();
    apiClient.setConfig({ apiBase: 'https://same.example.com', managementKey: 'new' });
    expect(apiClient.getConnectionRevision()).toBe(revision + 1);
    expect(requestSignal?.aborted).toBe(true);
    complete();
    await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' });
  });

  test('response guards fence stale success and 401 even without adapter cancellation', async () => {
    const handlers = (internalClient.instance as unknown as { interceptors: InterceptorAccess }).interceptors;
    apiClient.setConfig({ apiBase: 'https://a.example.com', managementKey: 'key' });
    const config = handlers.request.handlers[0].fulfilled({ headers: {} });
    apiClient.setConfig({ apiBase: 'https://b.example.com', managementKey: 'key' });
    apiClient.setConfig({ apiBase: 'https://a.example.com', managementKey: 'key' });
    expect(() => handlers.response.handlers[0].fulfilled({
      config, headers: { 'x-cpa-version': 'stale' }, data: {},
    })).toThrow('Connection changed');
    await expect(handlers.response.handlers[0].rejected({
      isAxiosError: true, config, response: { status: 401 },
    })).rejects.toMatchObject({ code: 'ERR_CANCELED' });
  });

  test('physically aborts an in-flight request when the connection changes', async () => {
    let listenerReady = false;
    let physicallyAborted = false;
    internalClient.instance.defaults.adapter = ((config: unknown) =>
      new Promise((resolve) => {
        const signal = (config as { signal?: AbortSignal }).signal;
        signal?.addEventListener(
          'abort',
          () => {
            physicallyAborted = true;
            resolve({ data: {}, status: 200, statusText: 'OK', headers: {}, config });
          },
          { once: true }
        );
        listenerReady = true;
      })) as AxiosAdapter;

    apiClient.setConfig({ apiBase: 'https://old.example.com', managementKey: 'old-key' });
    const pending = apiClient.get('/usage');
    while (!listenerReady) await Promise.resolve();

    apiClient.setConfig({ apiBase: 'https://new.example.com', managementKey: 'new-key' });

    await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' });
    expect(physicallyAborted).toBe(true);
  });

  test('preserves caller cancellation when signals are combined', async () => {
    let listenerReady = false;
    let callerAbortReachedAdapter = false;
    internalClient.instance.defaults.adapter = ((config: unknown) =>
      new Promise((resolve) => {
        const signal = (config as { signal?: AbortSignal }).signal;
        signal?.addEventListener(
          'abort',
          () => {
            callerAbortReachedAdapter = true;
            resolve({ data: {}, status: 200, statusText: 'OK', headers: {}, config });
          },
          { once: true }
        );
        listenerReady = true;
      })) as AxiosAdapter;

    apiClient.setConfig({ apiBase: 'https://same.example.com', managementKey: 'same-key' });
    const callerController = new AbortController();
    const pending = apiClient.get('/usage', { signal: callerController.signal });
    while (!listenerReady) await Promise.resolve();

    callerController.abort();

    await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' });
    expect(callerAbortReachedAdapter).toBe(true);
  });

  test('rejects a response from the previous server generation', async () => {
    let resolveResponse: ((value: unknown) => void) | null = null;
    let requestConfig: unknown;
    internalClient.instance.defaults.adapter = ((config: unknown) => {
      requestConfig = config;
      return new Promise((resolve) => {
        resolveResponse = resolve;
      });
    }) as AxiosAdapter;

    apiClient.setConfig({ apiBase: 'https://old.example.com', managementKey: 'old-key' });
    const pending = apiClient.get('/usage');
    while (!resolveResponse) await Promise.resolve();

    apiClient.setConfig({ apiBase: 'https://new.example.com', managementKey: 'new-key' });
    resolveResponse({
      data: { total_requests: 1 },
      status: 200,
      statusText: 'OK',
      headers: {},
      config: requestConfig,
    });

    await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' });
  });

  test('turns a stale unauthorized response into cancellation', async () => {
    let rejectResponse: ((reason: unknown) => void) | null = null;
    let requestConfig: unknown;
    internalClient.instance.defaults.adapter = ((config: unknown) => {
      requestConfig = config;
      return new Promise((_, reject) => {
        rejectResponse = reject;
      });
    }) as AxiosAdapter;

    apiClient.setConfig({ apiBase: 'https://old.example.com', managementKey: 'old-key' });
    const pending = apiClient.get('/usage');
    while (!rejectResponse) await Promise.resolve();

    apiClient.setConfig({ apiBase: 'https://new.example.com', managementKey: 'new-key' });
    rejectResponse({
      isAxiosError: true,
      message: 'Request failed with status code 401',
      config: requestConfig,
      response: { status: 401, data: { error: 'unauthorized' }, config: requestConfig },
    });

    await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' });
  });
});

// Run separately with AbortSignal.any disabled to exercise older-browser behavior.
describe('fallback request signal lifetime', () => {
  const fallbackTest = typeof AbortSignal.any === 'function' ? test.skip : test;
  for (const outcome of ['success', 'axios failure', 'plain failure', 'transform failure', 'stale pro', 'caller abort', 'connection abort', 'already aborted'] as const) {
    fallbackTest(`removes both source listeners after ${outcome}`, async () => {
      apiClient.setConfig({ apiBase: 'https://cleanup.example.com', managementKey: 'key' });
      const caller = new AbortController();
      const connection = (apiClient as unknown as { connectionAbortController: AbortController }).connectionAbortController;
      const counts = [caller.signal, connection.signal].map((signal) => {
        const listeners = new Set<EventListenerOrEventListenerObject>();
        const add = signal.addEventListener.bind(signal);
        const remove = signal.removeEventListener.bind(signal);
        signal.addEventListener = ((type, listener, options) => {
          if (type === 'abort' && listener) listeners.add(listener);
          add(type, listener, options);
        }) as typeof signal.addEventListener;
        signal.removeEventListener = ((type, listener, options) => {
          if (type === 'abort' && listener) listeners.delete(listener);
          remove(type, listener, options);
        }) as typeof signal.removeEventListener;
        return listeners;
      });
      if (outcome === 'already aborted') caller.abort();
      internalClient.instance.defaults.adapter = (async (config: unknown) => {
        if (outcome === 'axios failure') throw { isAxiosError: true, config, message: 'failed' };
        if (outcome === 'plain failure') throw new Error('plain failed');
        if (outcome === 'caller abort') caller.abort();
        if (outcome === 'connection abort') apiClient.setConfig({ apiBase: 'https://next.example.com', managementKey: 'key' });
        if (outcome.endsWith('abort')) expect(counts.map((listeners) => listeners.size)).toEqual([0, 0]);
        return { config, data: {}, status: 200, statusText: 'OK', headers: {} };
      }) as AxiosAdapter;
      for (let attempt = 0; attempt < (outcome === 'success' ? 20 : 1); attempt++) {
        const config = {
          signal: caller.signal,
          ...(outcome === 'transform failure' ? { transformRequest: [() => { throw new Error('transform failed'); }] } : {}),
          ...(outcome === 'stale pro' ? { managementApiNamespace: 'pro', managementConnectionRevision: -1 } : {}),
        };
        const pending = apiClient.get('/usage', config);
        if (outcome === 'success') await pending;
        else await expect(pending).rejects.toBeDefined();
        expect(counts.map((listeners) => listeners.size)).toEqual([0, 0]);
      }
    });
  }
});
