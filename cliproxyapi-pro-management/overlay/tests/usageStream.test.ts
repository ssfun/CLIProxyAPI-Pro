import { describe, expect, test } from 'bun:test';
import {
  parseUsageSseMessage,
  processUsageSseBlocks,
} from '../src/features/monitoring/usageStream';

describe('usage SSE contract', () => {
  test('parses CRLF frames and multi-line data', () => {
    const message = parseUsageSseMessage(
      'event: usage\r\ndata: {"generation":1,\r\ndata: "details_limited":true}\r\n'
    );
    expect(message).toEqual({
      event: 'usage',
      payload: { generation: 1, details_limited: true },
    });
  });

  test('treats reset as a hard boundary for the current connection', () => {
    const applied: number[] = [];
    let reloads = 0;
    let generation = 1;
    const keepStream = processUsageSseBlocks([
      'event: usage\ndata: {"generation":1,"latest_id":2}',
      'event: reset\ndata: {"generation":2}',
      'event: usage\ndata: {"generation":2,"latest_id":3}',
    ], {
      currentGeneration: () => generation,
      setGeneration: (value) => { generation = value; },
      applyUsage: (payload) => { applied.push(Number(payload.latest_id)); },
      reloadUsage: () => { reloads += 1; },
      loadIncremental: () => undefined,
    });

    expect(keepStream).toBe(false);
    expect(reloads).toBe(1);
    expect(applied).toEqual([2]);
  });

  test('ends the stream when the dataset generation changes without a reset event', () => {
    let reloads = 0;
    const keepStream = processUsageSseBlocks([
      'event: ready\ndata: {"generation":4}',
    ], {
      currentGeneration: () => 3,
      setGeneration: () => undefined,
      applyUsage: () => undefined,
      reloadUsage: () => { reloads += 1; },
      loadIncremental: () => undefined,
    });
    expect(keepStream).toBe(false);
    expect(reloads).toBe(1);
  });
});
