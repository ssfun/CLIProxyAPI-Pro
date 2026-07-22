import { afterEach, describe, expect, test } from 'bun:test';
import { apiClient } from '../src/services/api/client';

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
