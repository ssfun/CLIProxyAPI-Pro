import { describe, expect, test } from 'bun:test';
import { Transpiler } from 'bun';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { apiKeyPolicyConflictKeyRef, apiKeyPolicyErrorCode } from '../src/pro/modules/apiKeyPolicy/apiKeyPolicy';

// Run the actual save and reload callbacks together, with the HTTP conflict
// response and subsequent binding list controlled independently.
const source = readFileSync(resolve(import.meta.dir, '../src/pro/modules/apiKeyPolicy/APIKeyPolicyPage.tsx'), 'utf8');
const callback = (name: string, next: string) => {
  const start = source.indexOf('  const ' + name + ' =');
  return new Transpiler({ loader: 'ts' }).transformSync(source.slice(start, source.indexOf('  const ' + next, start)));
};
const saveSource = callback('saveWorkspace', 'resetQuota');
const reloadSource = callback('reloadWorkspace', 'validateDraft');
const conflictError = (keyRef?: unknown) => Object.assign(new Error('conflict'), {
  apiCode: 'config_version_conflict', data: { keyRef },
});

function harness() {
  const originalDraft = {
    displayName: 'My unsaved key', profileEnabled: true, profileId: '', isNewProfile: true,
    profile: { name: 'My profile', providers: [], models: [], mappings: [] },
    quota: { enabled: true, requests: 20, period: { type: 'all_time' } },
  };
  const oldBinding = { keyRef: 'old-ref', concurrencyLimit: 0, maskedKey: 'same-mask' };
  const otherBinding = { keyRef: 'unrelated-ref', concurrencyLimit: 99, maskedKey: 'same-mask' };
  let target = { kind: 'create', binding: oldBinding };
  let snapshot = { bindings: { items: [oldBinding, otherBinding] } };
  let draft = structuredClone(originalDraft);
  let concurrencyDraft: { value: string; baseline: number } | null = { value: '3', baseline: 0 };
  let bindings = { items: [{ ...oldBinding, keyRef: 'new-ref', concurrencyLimit: 2 }, otherBinding] };
  let error: Error | undefined = conflictError('new-ref');
  let responseGate: Promise<void> = Promise.resolve();
  const writes: unknown[][] = [];
  const saveRevisionRef = { current: 0 };
  const workspaceSessionRef = { current: 0 };
  const savingRef = { current: false };
  let closed = false;
  let conflict = false;
  let notifications = 0;
  let draftReplacements = 0;
  const context = () => ({
    useCallback: (fn: unknown) => fn,
    workspaceTarget: target, workspaceBinding: target.binding, snapshot, draft,
    saveRevisionRef, workspaceSessionRef, savingRef,
    keyActionBusyRef: { current: false }, dangerBusyRef: { current: false }, draftRevisionRef: { current: 0 },
    concurrencyDirty: true, concurrencyLimit: Number(concurrencyDraft?.value), concurrencyExpectedLimit: concurrencyDraft?.baseline,
    setSaving: () => {}, setConflict: (value: boolean) => { conflict = value; },
    setWorkspaceTarget: (value: typeof target) => { target = value; },
    setSnapshot: (update: (value: typeof snapshot) => typeof snapshot) => { snapshot = update(snapshot); },
    setConcurrencyDraft: (value: typeof concurrencyDraft | ((current: typeof concurrencyDraft) => typeof concurrencyDraft)) => {
      concurrencyDraft = typeof value === 'function' ? value(concurrencyDraft) : value;
    },
    setDraft: (value: typeof draft) => { draft = value; draftReplacements++; },
    apiKeyPolicyApi: {
      bindings: async () => { await responseGate; return bindings; },
      create: async (...args: unknown[]) => {
        writes.push(args);
        await responseGate;
        if (error) throw error;
        return { id: 'saved', state: 'configured', profiles: [{ id: 'profile', ...originalDraft.profile }] };
      },
    },
    apiKeyPolicyErrorCode, apiKeyPolicyConflictKeyRef,
    showNotification: () => { notifications++; }, t: (key: string) => key, errorMessage: String,
    closeWorkspace: () => { closed = true; }, load: async () => {},
    quotaSupported: true, validateDraft: () => true,
    quotaInputFromPolicy: () => null, profileSignature: JSON.stringify,
    replacePolicyInSnapshot: () => {}, refreshQuotaAfterMutation: async () => {},
    workspaceDraftFromTarget: () => originalDraft,
  });
  const run = async (body: string, name: string) => {
    const deps = context();
    const fn = new Function(...Object.keys(deps), body + ';return ' + name)(...Object.values(deps));
    await fn();
  };
  return {
    originalDraft, save: () => run(saveSource, 'saveWorkspace'), reload: () => run(reloadSource, 'reloadWorkspace'),
    setError: (value: typeof error) => { error = value; },
    setBindings: (value: typeof bindings) => { bindings = value; },
    pause: () => { let resume!: () => void; responseGate = new Promise<void>((resolve) => { resume = resolve; }); return resume; },
    replaceSession: () => { workspaceSessionRef.current++; saveRevisionRef.current++; },
    state: () => ({ target, snapshot, draft, concurrencyDraft, closed, conflict, notifications, draftReplacements, writes }),
  };
}

describe('first workspace save conflict recovery', () => {
  test('rotates only the matching reference, preserves all drafts, reloads and retries atomically', async () => {
    const h = harness();
    await h.save();
    expect(h.state()).toMatchObject({
      target: { binding: { keyRef: 'new-ref' } }, conflict: true, closed: false, draftReplacements: 0,
      concurrencyDraft: { value: '3', baseline: 0 },
    });
    expect(h.state().snapshot.bindings.items.map((item) => item.keyRef)).toEqual(['new-ref', 'unrelated-ref']);
    await h.reload();
    expect(h.state()).toMatchObject({
      draft: h.originalDraft, concurrencyDraft: { value: '3', baseline: 2 },
      conflict: false, closed: false, draftReplacements: 0,
    });
    h.setError(undefined);
    await h.save();
    expect(h.state().writes[1]).toEqual([
      'new-ref', h.originalDraft.displayName, h.originalDraft.profile, h.originalDraft.quota,
      { limit: 3, expectedLimit: 2 },
    ]);
  });

  test('keeps drafts when an older Core omits the replacement reference', async () => {
    const h = harness(); h.setError(conflictError());
    await h.save(); await h.reload();
    expect(h.state()).toMatchObject({ draft: h.originalDraft, closed: false, conflict: true, draftReplacements: 0 });
  });

  test('does not identify a missing key by a shared mask or erase its draft', async () => {
    const h = harness(); await h.save();
    h.setBindings({ items: [{ keyRef: 'unrelated-ref', maskedKey: 'same-mask', concurrencyLimit: 99 }] });
    await h.reload();
    expect(h.state()).toMatchObject({
      target: { binding: { keyRef: 'new-ref' } }, draft: h.originalDraft,
      concurrencyDraft: { value: '3', baseline: 0 }, closed: false, conflict: true,
    });
  });

  test('stale-reference errors do not close a new workspace with unsaved edits', async () => {
    const h = harness(); h.setError(Object.assign(new Error('stale'), { apiCode: 'api_key_reference_stale' }));
    await h.save();
    expect(h.state()).toMatchObject({ draft: h.originalDraft, closed: false, draftReplacements: 0 });
  });

  test('late conflict responses cannot rotate the reference in another workspace', async () => {
    const h = harness(); const resume = h.pause(); const saving = h.save();
    h.replaceSession(); resume(); await saving;
    expect(h.state()).toMatchObject({ target: { binding: { keyRef: 'old-ref' } }, notifications: 0 });
  });

  test('late binding reloads cannot change another workspace', async () => {
    const h = harness(); await h.save();
    const resume = h.pause(); const loading = h.reload();
    h.replaceSession(); resume(); await loading;
    expect(h.state().concurrencyDraft).toEqual({ value: '3', baseline: 0 });
  });
});

test('conflict reference parsing ignores unrelated and malformed error data', () => {
  for (const keyRef of [undefined, null, '', '  ', 5, {}]) {
    expect(apiKeyPolicyConflictKeyRef(conflictError(keyRef))).toBeUndefined();
  }
  expect(apiKeyPolicyConflictKeyRef({ apiCode: 'other_error', data: { keyRef: 'new-ref' } })).toBeUndefined();
  expect(apiKeyPolicyConflictKeyRef(conflictError('new-ref'))).toBe('new-ref');
});
