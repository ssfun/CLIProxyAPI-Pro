import { describe, expect, test } from 'bun:test';
import { Transpiler } from 'bun';
import { apiKeyPolicyConflictKeyRef, parseKeyConcurrencyLimit } from '../src/pro/modules/apiKeyPolicy/apiKeyPolicy';
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

function harness(options: { refreshedLimit?: number; refreshFails?: boolean } = {}) {
  const request = deferred();
  const requestRevisionRef = { current: 1 };
  const keyActionSessionRef = { current: 1 };
  const keyActionBusyRef = { current: false };
  const workspaceSessionRef = { current: 1 };
  const dangerBusyRef = { current: false };
  let concurrencyDraft: { value: string; baseline: number } | null = null;
  let serverLimit = 0;
  let displayedLimit = 0;
  let serverDisabled = false;
  let displayedDisabled = false;
  let writes = 0;
  let loads = 0;
  let reveals = 0;
  let notifications = 0;
  const load = async () => { requestRevisionRef.current++; loads++; if (options.refreshFails) return; displayedDisabled = serverDisabled; displayedLimit = options.refreshedLimit ?? serverLimit; };
  const dependencies = {
    apiKeyPolicyApi: {
      setKeyConcurrency: async (_keyRef: string, limit: number) => { writes++; await request.promise; serverLimit = limit; },
      setKeyDisabled: async () => { writes++; await request.promise; serverDisabled = true; },
      readKey: async () => { await request.promise; return { key: 'demo-secret' }; },
    },
    requestRevisionRef, keyActionSessionRef, keyActionBusyRef, workspaceSessionRef, dangerBusyRef, savingRef: { current: false },
    setConcurrencyDraft: (value: typeof concurrencyDraft) => { concurrencyDraft = value; },
    setSnapshot: (update: (current: { bindings: { items: { keyRef: string; concurrencyLimit: number }[] } }) => { bindings: { items: { keyRef: string; concurrencyLimit: number }[] } }) => {
      displayedLimit = update({ bindings: { items: [{ keyRef: 'demo', concurrencyLimit: displayedLimit }] } }).bindings.items[0].concurrencyLimit;
    },
    loading: false, connectionStatus: 'connected', setKeyActionBusy: () => {},
    revealedKeys: {}, setRevealedKeys: () => { reveals++; },
    showNotification: () => { notifications++; }, t: (key: string) => key,
    takeoverStatus: { takeoverEnabled: true },
    apiKeyPolicyErrorCode: (error: Error) => error.message,
    apiKeyPolicyConflictKeyRef,
    errorMessage: (error: Error) => error.message,
    load,
  };
  const saveRevisionRef = { current: 0 };
  const draftRevisionRef = { current: 0 };
  const saveStart = source.indexOf('  const saveWorkspace = useCallback(');
  const saveEnd = source.indexOf('  const resetQuota', saveStart);
  const saveSource = new Transpiler({ loader: 'ts' }).transformSync(source.slice(saveStart, saveEnd));
  const save = async (limit: number) => {
    const deps = {
      ...dependencies,
      useCallback: (fn: unknown) => fn,
      workspaceTarget: { kind: 'create', binding: { keyRef: 'demo' } },
      workspaceBinding: { keyRef: 'demo' },
      draft: { displayName: 'Demo', profileEnabled: false },
      concurrencyDirty: true, concurrencyLimit: Number.isInteger(limit) && limit >= 0 && limit <= 1_000_000 ? limit : null,
      concurrencyExpectedLimit: 0,
      saveRevisionRef, draftRevisionRef,
      profileSignature: () => '', validateDraft: () => true,
      quotaSupported: false, quotaInputFromPolicy: () => null,
      setSaving: () => {}, setConflict: (value: boolean) => { if (value) notifications++; },
      setWorkspaceTarget: () => {}, setDraft: () => {},
      replacePolicyInSnapshot: () => {}, workspaceDraftFromTarget: () => ({}),
      closeWorkspace: () => {}, refreshQuotaAfterMutation: async () => {},
      apiKeyPolicyApi: { create: async (...args: unknown[]) => {
        writes++; await request.promise;
        serverLimit = (args[4] as {limit: number}).limit;
        return { id: 'policy', profiles: [] };
      } },
    };
    const fn = new Function(...Object.keys(deps), `${saveSource};return saveWorkspace;`)(...Object.values(deps));
    await fn();
  };
  const act = new Function(...Object.keys(dependencies), `${handlerSource};return keyAction;`)(...Object.values(dependencies)) as
    (binding: { keyRef: string; disabled: boolean }, action: 'toggle' | 'reveal' | 'concurrency', limit?: number) => Promise<void>;
  const derivedStart = source.indexOf('  const hasConcurrencyEdits =');
  const derivedSource = new Transpiler({ loader: 'ts' }).transformSync(source.slice(derivedStart, source.indexOf('\n  const quotaSupported', derivedStart)));
  const derive = new Function('concurrencyDraft', 'workspaceBinding', 'parseKeyConcurrencyLimit', 'concurrencySupported', 'dirty', `${derivedSource};return {concurrencyValue, concurrencyExpectedLimit, workspaceDirty};`);
  return { request, requestRevisionRef, keyActionSessionRef, keyActionBusyRef, workspaceSessionRef, dangerBusyRef, load,
    edit: (value: string, baseline: number) => { concurrencyDraft = { value, baseline }; },
    view: () => derive(concurrencyDraft, { concurrencyLimit: displayedLimit }, parseKeyConcurrencyLimit, true, false),
    act: (action: 'toggle' | 'reveal' | 'concurrency' = 'toggle', limit?: number) => action === 'concurrency' ? save(limit!) : act({ keyRef: 'demo', disabled: false }, action, limit),
    state: () => ({ serverLimit, displayedLimit, serverDisabled, displayedDisabled, writes, loads, reveals, notifications }),
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

describe('API key concurrency asynchronous mutations', () => {
  test('saves the requested limit and synchronizes after an intervening refresh', async () => {
    const h = harness(); const action = h.act('concurrency', 3);
    await h.load(); h.request.resolve(); await action;
    expect(h.state()).toMatchObject({ serverLimit: 3, displayedLimit: 3, writes: 1, loads: 2 });
  });
  test('clears the limit with zero and serializes writes with disable actions', async () => {
    const h = harness(); const action = h.act('concurrency', 0);
    await h.act('toggle'); h.request.resolve(); await action;
    expect(h.state()).toMatchObject({ serverLimit: 0, writes: 1, loads: 1 });
  });
  test('does not publish a limit into a new connection session', async () => {
    const h = harness(); const action = h.act('concurrency', 2);
    h.workspaceSessionRef.current++; h.request.resolve(); await action;
    expect(h.state()).toMatchObject({ loads: 0, notifications: 0 });
  });
  test('marks workspace conflicts after a refresh', async () => {
    const h = harness(); const action = h.act('concurrency', 2);
    await h.load(); h.request.reject(new Error('config_version_conflict')); await action;
    expect(h.state()).toMatchObject({ loads: 1, notifications: 1 });
  });
  test('rejects invalid numeric values before sending a write', async () => {
    for (const value of [-1, 1.5, NaN, Infinity, 1000001]) {
      const h = harness(); await h.act('concurrency', value);
      expect(h.state().writes).toBe(0);
    }
  });
});

describe('concurrency workspace boundaries and snapshot reconciliation', () => {
  test('ignores a successful response from a workspace that has been replaced', async () => {
    const h = harness(); const action = h.act('concurrency', 2);
    h.workspaceSessionRef.current += 2; // Close A, then open B in the same page session.
    h.edit('7', 0);
    h.request.resolve(); await action;
    expect(h.view()).toEqual({ concurrencyValue: '7', concurrencyExpectedLimit: 0, workspaceDirty: true });
    expect(h.state()).toMatchObject({ displayedLimit: 0, loads: 0, notifications: 0 });
    expect(h.keyActionBusyRef.current).toBe(false);
  });
  test('does not clear another workspace draft after an old request conflicts', async () => {
    const h = harness(); const action = h.act('concurrency', 2);
    h.workspaceSessionRef.current++; h.edit('7', 0);
    h.request.reject(new Error('config_version_conflict')); await action;
    expect(h.view().concurrencyValue).toBe('7');
    expect(h.state()).toMatchObject({ loads: 0, notifications: 0 });
  });
  test('uses the newer server value read immediately after a successful write', async () => {
    const h = harness({ refreshedLimit: 5 }); h.edit('2', 0);
    const action = h.act('concurrency', 2); h.request.resolve(); await action;
    expect(h.state().displayedLimit).toBe(5);
    expect(h.view()).toEqual({ concurrencyValue: '5', concurrencyExpectedLimit: 5, workspaceDirty: false });
  });
  test('preserves a successful write when the following refresh fails', async () => {
    const h = harness({ refreshFails: true }); h.edit('2', 0);
    const action = h.act('concurrency', 2); h.request.resolve(); await action;
    expect(h.view()).toEqual({ concurrencyValue: '2', concurrencyExpectedLimit: 2, workspaceDirty: false });
  });
  test('preserves unsaved edits and their conflict baseline across a snapshot refresh', async () => {
    const h = harness({ refreshedLimit: 5 }); h.edit('3', 0);
    await h.load();
    expect(h.view()).toEqual({ concurrencyValue: '3', concurrencyExpectedLimit: 0, workspaceDirty: true });
  });
  test('follows the server again after the user reverts an edit', async () => {
    const h = harness({ refreshedLimit: 5 }); h.edit('0', 0);
    await h.load();
    expect(h.view()).toEqual({ concurrencyValue: '5', concurrencyExpectedLimit: 5, workspaceDirty: false });
  });
  test('does not start a concurrency write while a destructive action is running', async () => {
    const h = harness(); h.dangerBusyRef.current = true;
    await h.act('concurrency', 2);
    expect(h.state().writes).toBe(0);
  });
});
