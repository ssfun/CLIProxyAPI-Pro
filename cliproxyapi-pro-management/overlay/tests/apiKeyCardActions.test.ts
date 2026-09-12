import { describe, expect, test } from 'bun:test';
import { Transpiler } from 'bun';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// Execute the actual page handler with controlled request completion order.
const source = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
const start = source.indexOf('  const keyAction = async');
const handlerSource = new Transpiler({ loader: 'ts' }).transformSync(source.slice(start, source.indexOf('\n  const {', start)));
const deferred = () => {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};

function harness() {
  const request = deferred();
  const requestRevisionRef = { current: 1 };
  const keyActionSessionRef = { current: 1 };
  const keyActionBusyRef = { current: false };
  let serverDisabled = false;
  let displayedDisabled = false;
  let writes = 0;
  let loads = 0;
  let reveals = 0;
  let notifications = 0;
  const load = async () => { requestRevisionRef.current++; loads++; displayedDisabled = serverDisabled; };
  const dependencies = {
    apiKeyPolicyApi: {
      setKeyDisabled: async () => { writes++; await request.promise; serverDisabled = true; },
      readKey: async () => { await request.promise; return { key: 'demo-secret' }; },
    },
    requestRevisionRef, keyActionSessionRef, keyActionBusyRef,
    loading: false, connectionStatus: 'connected', setKeyActionBusy: () => {},
    revealedKeys: {}, setRevealedKeys: () => { reveals++; },
    showNotification: () => { notifications++; }, t: (key: string) => key,
    takeoverStatus: { takeoverEnabled: true },
    apiKeyPolicyErrorCode: (error: Error) => error.message,
    errorMessage: (error: Error) => error.message,
    load,
  };
  const act = new Function(...Object.keys(dependencies), `${handlerSource};return keyAction;`)(...Object.values(dependencies)) as
    (binding: { keyRef: string; disabled: boolean }, action: 'toggle' | 'reveal') => Promise<void>;
  return { request, requestRevisionRef, keyActionSessionRef, keyActionBusyRef, load,
    act: (action: 'toggle' | 'reveal' = 'toggle') => act({ keyRef: 'demo', disabled: false }, action),
    state: () => ({ serverDisabled, displayedDisabled, writes, loads, reveals, notifications }),
  };
}

describe('API key card asynchronous mutations', () => {
  test('resynchronizes after an older refresh finishes before the write', async () => {
    const h = harness();
    const action = h.act();
    await h.load();
    h.request.resolve();
    await action;
    expect(h.state()).toMatchObject({ serverDisabled: true, displayedDisabled: true, loads: 2 });
  });
  test('does not publish a completed write into another page session', async () => {
    const h = harness(); const action = h.act();
    h.keyActionSessionRef.current++;
    h.request.resolve(); await action;
    expect(h.state()).toMatchObject({ loads: 0, notifications: 0 });
    expect(h.keyActionBusyRef.current).toBe(true); // The old finally must not release another session's action.
  });
  test('keeps refresh invalidation for revealed secrets', async () => {
    const h = harness(); const action = h.act('reveal');
    await h.load(); h.request.resolve(); await action;
    expect(h.state().reveals).toBe(0);
  });
  test('reloads conflicts even when a refresh advanced the read revision', async () => {
    const h = harness(); const action = h.act();
    await h.load(); h.request.reject(new Error('config_version_conflict')); await action;
    expect(h.state()).toMatchObject({ loads: 2, notifications: 1 });
  });
  test('serializes repeated toggle clicks', async () => {
    const h = harness(); const action = h.act();
    await h.act(); h.request.resolve(); await action;
    expect(h.state().writes).toBe(1);
    expect(h.keyActionBusyRef.current).toBe(false);
  });
});
