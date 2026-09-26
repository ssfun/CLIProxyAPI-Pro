export interface LatestRequestToken {
  signal: AbortSignal;
  isCurrent: () => boolean;
  finish: () => void;
}

export function createLatestRequestGate() {
  let active: AbortController | null = null;

  const invalidate = () => {
    active?.abort();
    active = null;
  };

  const begin = (): LatestRequestToken => {
    active?.abort();
    const request = new AbortController();
    active = request;
    const isCurrent = () => active === request && !request.signal.aborted;
    return {
      signal: request.signal,
      isCurrent,
      finish: () => {
        if (active === request) active = null;
      },
    };
  };

  return { begin, invalidate };
}
