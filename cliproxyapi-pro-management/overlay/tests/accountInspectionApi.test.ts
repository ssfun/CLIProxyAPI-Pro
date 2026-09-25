import { afterEach, describe, expect, test } from 'bun:test';
import { apiClient } from '../src/services/api/client';
import {
  ACCOUNT_INSPECTION_BULK_RECHECK_TIMEOUT_MS,
  accountInspectionApi,
} from '../src/pro/modules/inspection/api';

type AxiosAdapter = (config: unknown) => Promise<unknown>;

const internalClient = apiClient as unknown as {
  instance: { defaults: { adapter: AxiosAdapter | AxiosAdapter[] | undefined } };
};
const originalAdapter = internalClient.instance.defaults.adapter;

afterEach(() => {
  internalClient.instance.defaults.adapter = originalAdapter;
  apiClient.setConfig({ apiBase: '', managementKey: '' });
});

describe('account inspection API', () => {
  test('starts a batch atomically with an idempotency identity', async () => {
    let capturedUrl = '';
    let capturedBody: unknown;
    internalClient.instance.defaults.adapter = (async (config: unknown) => {
      const request = config as { url?: string; data?: string };
      capturedUrl = request.url ?? '';
      capturedBody = request.data ? JSON.parse(request.data) : undefined;
      return {
        data: { operationId: 'batch-1', kind: 'inspect', state: 'running', items: [], summary: {} },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    }) as AxiosAdapter;

    await accountInspectionApi.startBatch('inspect', {
      type: 'selected',
      items: [],
    }, 'request-1');

    expect(capturedUrl).toBe('/account-inspection/batches');
    expect(capturedBody).toEqual({
      kind: 'inspect',
      scope: { type: 'selected', items: [] },
      clientRequestId: 'request-1',
    });
  });

  test('retries failed batch items through the atomic retry endpoint', async () => {
    let capturedUrl = '';
    let capturedBody: unknown;
    internalClient.instance.defaults.adapter = (async (config: unknown) => {
      const request = config as { url?: string; data?: string };
      capturedUrl = request.url ?? '';
      capturedBody = request.data ? JSON.parse(request.data) : undefined;
      return {
        data: { operationId: 'batch-2', kind: 'action', state: 'running', items: [], summary: {} },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    }) as AxiosAdapter;

    await accountInspectionApi.retryExecuteBatch('batch/1');

    expect(capturedUrl).toBe('/account-inspection/batches/batch%2F1/retry-execute');
    expect(capturedBody).toEqual({});
  });

  test('gives bulk rechecks enough time to reach the backend run deadline', async () => {
    let capturedTimeout: number | undefined;
    internalClient.instance.defaults.adapter = (async (config: unknown) => {
      capturedTimeout = (config as { timeout?: number }).timeout;
      return {
        data: { outcomes: [], schedule: {}, status: {} },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    }) as AxiosAdapter;

    await accountInspectionApi.inspectMany([]);
    expect(capturedTimeout).toBe(ACCOUNT_INSPECTION_BULK_RECHECK_TIMEOUT_MS);
    expect(capturedTimeout).toBeGreaterThan(30 * 60 * 1000);
  });

  test('passes caller cancellation through detail status requests', async () => {
    let capturedSignal: AbortSignal | undefined;
    let adapterReady = false;
    internalClient.instance.defaults.adapter = ((config: unknown) => new Promise((resolve) => {
      capturedSignal = (config as { signal?: AbortSignal }).signal;
      capturedSignal?.addEventListener('abort', () => resolve({
        data: { schedule: {}, status: {} },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }), { once: true });
      adapterReady = true;
    })) as AxiosAdapter;

    const controller = new AbortController();
    const pending = accountInspectionApi.getStatus(true, controller.signal);
    while (!adapterReady) await Promise.resolve();
    controller.abort();
    await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' });
    expect(capturedSignal?.aborted).toBe(true);
  });
});
