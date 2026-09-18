import { describe, expect, test } from 'bun:test';
import { createLatestRequestGate } from '../src/pro/modules/routing/latestRequestGate';

describe('latest request gate', () => {
  test('a newer request aborts and fences the older response', () => {
    const gate = createLatestRequestGate();
    const first = gate.begin();
    const second = gate.begin();
    expect(first.signal.aborted).toBe(true);
    expect(first.isCurrent()).toBe(false);
    expect(second.isCurrent()).toBe(true);
    first.finish();
    expect(second.isCurrent()).toBe(true);
  });

  test('connection invalidation fences a request even if abort is ignored', () => {
    const gate = createLatestRequestGate();
    const request = gate.begin();
    gate.invalidate();
    expect(request.signal.aborted).toBe(true);
    expect(request.isCurrent()).toBe(false);
  });
});
