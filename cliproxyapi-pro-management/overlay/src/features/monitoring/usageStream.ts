export type UsageStreamPayload = {
  generation?: number;
  details_limited?: boolean;
  [key: string]: unknown;
};

export type UsageStreamMessage = {
  event: 'usage' | 'ready' | 'reset';
  payload: UsageStreamPayload;
};

type UsageStreamHandlers = {
  currentGeneration: () => number;
  setGeneration: (generation: number) => void;
  applyUsage: (payload: UsageStreamPayload) => void;
  reloadUsage: () => void;
  loadIncremental: () => void;
};

const toFiniteNumber = (value: unknown) =>
  Number.isFinite(Number(value)) ? Number(value) : 0;

export const readUsageSseMessage = (block: string): { event: string; data: string } | null => {
  if (!block.trim()) return null;
  let event = 'message';
  const dataLines: string[] = [];
  block.replace(/\r\n?/g, '\n').split('\n').forEach((line) => {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
  });
  return dataLines.length > 0 ? { event, data: dataLines.join('\n') } : null;
};

export const parseUsageSseMessage = (block: string): UsageStreamMessage | null => {
  const message = readUsageSseMessage(block);
  if (!message || !['usage', 'ready', 'reset'].includes(message.event)) return null;
  return {
    event: message.event as UsageStreamMessage['event'],
    payload: JSON.parse(message.data) as UsageStreamPayload,
  };
};

// Returns false when the current stream must end. Reset and generation changes are
// hard dataset boundaries, so later frames from the same connection must not merge.
export const processUsageSseBlocks = (
  blocks: string[],
  handlers: UsageStreamHandlers
): boolean => {
  for (const block of blocks) {
    try {
      const message = parseUsageSseMessage(block);
      if (!message) continue;
      const nextGeneration = toFiniteNumber(message.payload.generation);
      const currentGeneration = handlers.currentGeneration();
      const generationChanged = nextGeneration > 0
        && currentGeneration > 0
        && nextGeneration !== currentGeneration;
      if (message.event === 'reset' || generationChanged) {
        handlers.reloadUsage();
        return false;
      }
      if (nextGeneration > 0) handlers.setGeneration(nextGeneration);
      if (message.event === 'usage') {
        handlers.applyUsage(message.payload);
        if (message.payload.details_limited) handlers.loadIncremental();
      }
    } catch {
      handlers.loadIncremental();
    }
  }
  return true;
};
