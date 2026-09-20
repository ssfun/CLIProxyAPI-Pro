import { describe, expect, test } from 'bun:test';
import { clampRealtimeLogColumnWidth } from '../src/pro/modules/monitoring/features/realtimeLogPreferences';
import { allocateRealtimeColumnWidths, retainRealtimeColumnSample, type RealtimeColumnSize } from '../src/pro/modules/monitoring/features/realtimeColumnLayout';

const columns: RealtimeColumnSize[] = [
  { key: 'type', preferred: 240, header: 80 },
  { key: 'model', preferred: 300, header: 60 },
  { key: 'apiKey', preferred: 180, header: 90 },
  { key: 'time', preferred: 180, header: 60 },
  { key: 'tokens', preferred: 210, header: 80 },
];
const sum = (widths: { width: number }[]) => widths.reduce((total, item) => total + item.width, 0);
const width = (widths: { key: string; width: number }[], key: string) => widths.find((item) => item.key === key)!.width;

describe('realtime column allocation', () => {
  test('gives spare room to the model without stretching short metrics', () => {
    const result = allocateRealtimeColumnWidths(columns, 1250);
    expect(sum(result)).toBe(1250);
    expect(width(result, 'model') - 300).toBeGreaterThan(width(result, 'type') - 240);
    expect(width(result, 'tokens')).toBe(210);
    expect(width(result, 'apiKey')).toBe(180);
  });
  test('shrinks identity columns before model and keeps readable overflow', () => {
    const result = allocateRealtimeColumnWidths(columns, 950);
    expect(sum(result)).toBe(950);
    expect(width(result, 'model')).toBe(300);
    expect(width(result, 'tokens')).toBe(210);
    const narrow = allocateRealtimeColumnWidths(columns, 390);
    expect(sum(narrow)).toBeGreaterThan(390);
    expect(width(narrow, 'model')).toBe(220);
    expect(width(narrow, 'tokens')).toBe(210);
  });
  test('manual columns are never stretched or squeezed and hidden columns release space', () => {
    const manual = columns.map((column) => ({ ...column, manual: 200 }));
    for (const available of [390, 2000]) expect(allocateRealtimeColumnWidths(manual, available).map((item) => item.width)).toEqual([200, 200, 200, 200, 200]);
    const full = allocateRealtimeColumnWidths(columns, 1150);
    const reduced = allocateRealtimeColumnWidths(columns.filter((item) => item.key !== 'apiKey'), 1150);
    expect(width(reduced, 'model')).toBeGreaterThan(width(full, 'model'));
    expect(reduced.some((item) => item.key === 'apiKey')).toBe(false);
  });
  test('dragging a wide automatic model column does not snap back to the old cap', () => {
    expect(clampRealtimeLogColumnWidth('model', 500)).toBe(500);
    expect(clampRealtimeLogColumnWidth('model', 999)).toBe(520);
  });
  test('localized headers set the lower bound; empty and unmeasured containers are safe', () => {
    expect(allocateRealtimeColumnWidths([{ key: 'model', preferred: 200, header: 270 }], 100)[0].width).toBe(270);
    expect(allocateRealtimeColumnWidths([], 1000)).toEqual([]);
    expect(sum(allocateRealtimeColumnWidths(columns, 0))).toBeGreaterThan(0);
  });
  test('live refreshes retain the sample; initial data and explicit layout epochs recalculate', () => {
    const empty = retainRealtimeColumnSample(null, 'en:0', false, () => 100);
    const first = retainRealtimeColumnSample(empty, 'en:0', true, () => 200);
    const live = retainRealtimeColumnSample(first, 'en:0', true, () => { throw new Error('must not remeasure live rows'); });
    expect(live).toBe(first);
    expect(retainRealtimeColumnSample(live, 'zh-CN:0', true, () => 250).value).toBe(250);
    expect(retainRealtimeColumnSample(live, 'en:1', true, () => 300).value).toBe(300);
  });
});
