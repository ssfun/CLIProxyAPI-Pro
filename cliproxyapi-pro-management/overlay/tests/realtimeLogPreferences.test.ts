import { describe, expect, test } from 'bun:test';
import {
  clampRealtimeLogColumnWidth,
  createDefaultRealtimeLogColumns,
  normalizeRealtimeLogColumns,
} from '../src/features/monitoring/realtimeLogPreferences';

describe('realtime log column preferences', () => {
  test('migrates legacy usage and inserts new columns beside the model column', () => {
    const columns = normalizeRealtimeLogColumns([
      { key: 'model', visible: true },
      { key: 'usage', visible: false },
      { key: 'time', visible: true },
    ]);

    expect(columns.slice(0, 4).map(({ key }) => key)).toEqual([
      'model',
      'reasoningEffort',
      'stream',
      'tokens',
    ]);
    expect(columns.find(({ key }) => key === 'tokens')?.visible).toBe(false);
    expect(columns.find(({ key }) => key === 'cacheRead')?.visible).toBe(false);
    expect(columns.at(-1)?.key).toBe('time');
  });

  test('clamps persisted widths and restores defaults when every column is hidden', () => {
    expect(clampRealtimeLogColumnWidth('type', 999)).toBe(240);
    expect(clampRealtimeLogColumnWidth('model', 1)).toBe(132);

    const normalized = normalizeRealtimeLogColumns(
      createDefaultRealtimeLogColumns().map((column) => ({ ...column, visible: false }))
    );
    expect(normalized.every(({ visible }) => visible)).toBe(true);
  });
});
