// Public-page refresh queue. Changes received during a request are coalesced
// into a follow-up request; invalidation also fences responses that ignore abort.
export function createSelfUsageRefresh(
  run: (signal: AbortSignal, isCurrent: () => boolean) => Promise<void>,
  canRun: () => boolean = () => true
) {
  let disposed = false;
  let pending = false;
  let forced = false;
  let revision = 0;
  let active: { controller: AbortController; promise: Promise<void> } | null = null;

  const refresh = (force = false): Promise<void> => {
    if (disposed) return Promise.resolve();
    pending = true;
    forced ||= force;
    if (active) return active.promise;
    if (!forced && !canRun()) return Promise.resolve();
    const requestRevision = revision;
    const request = { controller: new AbortController(), promise: Promise.resolve() };
    const isCurrent = () =>
      !disposed && revision === requestRevision && !request.controller.signal.aborted;
    active = request;
    request.promise = (async () => {
      try {
        do {
          pending = false;
          forced = false;
          await run(request.controller.signal, isCurrent);
        } while (isCurrent() && pending && (forced || canRun()));
      } finally {
        if (active === request) active = null;
      }
    })();
    return request.promise;
  };

  const invalidate = () => {
    revision += 1;
    pending = false;
    forced = false;
    active?.controller.abort();
    active = null;
  };
  return {
    refresh,
    invalidate,
    dispose: () => {
      disposed = true;
      invalidate();
    },
  };
}

export function parseSelfUsageNotification(
  block: string
): { event: 'change' | 'reset'; generation?: number } | null {
  const lines = block.split('\n').map((line) => line.replace(/\r$/, ''));
  const event = lines
    .find((line) => line.startsWith('event:'))
    ?.slice(6)
    .trim();
  if (event !== 'change' && event !== 'reset') return null;
  const data = lines
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trim())
    .join('\n');
  const payload = JSON.parse(data) as { generation?: unknown };
  const generation =
    typeof payload.generation === 'number' &&
    Number.isSafeInteger(payload.generation) &&
    payload.generation > 0
      ? payload.generation
      : undefined;
  return { event, generation };
}
