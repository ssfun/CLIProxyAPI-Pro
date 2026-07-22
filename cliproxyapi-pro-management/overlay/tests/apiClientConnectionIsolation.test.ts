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
