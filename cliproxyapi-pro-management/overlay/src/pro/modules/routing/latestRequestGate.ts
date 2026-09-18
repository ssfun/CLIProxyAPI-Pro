export interface LatestRequestToken {
  signal: AbortSignal;
  isCurrent: () => boolean;
  finish: () => void;
}

export function createLatestRequestGate() {
  let generation = 0;
  let sequence = 0;
  let active: { generation: number; sequence: number; controller: AbortController } | null = null;

  const invalidate = () => {
    generation += 1;
    active?.controller.abort();
    active = null;
  };

  const begin = (): LatestRequestToken => {
    active?.controller.abort();
    const request = {
      generation,
      sequence: ++sequence,
      controller: new AbortController(),
    };
    active = request;
    const isCurrent = () =>
      active === request &&
      request.generation === generation &&
      request.sequence === sequence &&
      !request.controller.signal.aborted;
    return {
      signal: request.controller.signal,
      isCurrent,
      finish: () => {
        if (active === request) active = null;
      },
    };
  };

  return { begin, invalidate };
}
