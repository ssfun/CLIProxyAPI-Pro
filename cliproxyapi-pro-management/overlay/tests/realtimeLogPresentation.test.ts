import { describe, expect, test } from 'bun:test';
import {
  buildRealtimeDiagnosticClipboardText,
  buildRealtimeLogPageRows,
  getClientPaginationRange,
  resolveRealtimeErrorCategoryKey,
} from '../src/features/monitoring/realtimeLogPresentation';
import type { MonitoringEventRow } from '../src/features/monitoring/hooks/useMonitoringData';

const event = (overrides: Partial<MonitoringEventRow> = {}): MonitoringEventRow => ({
  id: 'event-1',
  account: 'owner@example.com',
  provider: 'codex',
  model: 'gpt-test',
  modelAlias: '',
  channel: 'openai',
  failed: false,
  statsIncluded: true,
  statusCode: 200,
  errorCode: '',
  errorMessage: '',
  retryAfter: '',
  upstreamRequestId: '',
  clientIP: '',
  xForwardedFor: '',
  userAgent: '',
  endpointMethod: 'POST',
  endpointPath: '/v1/responses',
  ...overrides,
} as MonitoringEventRow);

describe('realtime log presentation', () => {
  test('classifies authentication and quota failures from status and message data', () => {
    expect(resolveRealtimeErrorCategoryKey(event({ failed: true, statusCode: 401 })))
      .toBe('monitoring.error_category_auth');
    expect(resolveRealtimeErrorCategoryKey(event({ failed: true, statusCode: null, errorMessage: 'quota exceeded' })))
      .toBe('monitoring.error_category_rate_limit');
  });

  test('enriches page rows with stream history and diagnostic fields', () => {
    const result = buildRealtimeLogPageRows([
      event({ id: 'newest', failed: true, statusCode: 429, errorMessage: 'quota exceeded' }),
      event({ id: 'oldest' }),
    ], 1, 10);

    expect(result.total).toBe(2);
    expect(result.rows[0].requestCount).toBe(2);
    expect(result.rows[0].recentPattern).toEqual([true, false]);
    expect(result.rows[0].diagnosticText).toContain('HTTP 429');
    expect(result.rows[0].errorSummary).toContain('quota exceeded');
  });

  test('reports stable client pagination bounds', () => {
    expect(getClientPaginationRange(2, 10, 25, 10)).toEqual({
      page: 2,
      total: 25,
      totalPages: 3,
      from: 11,
      to: 20,
      hasPrevious: true,
      hasNext: true,
    });
  });

  test('includes client request metadata in copied diagnostics', () => {
    const t = ((key: string, options?: { defaultValue?: string }) => options?.defaultValue ?? key) as never;
    const text = buildRealtimeDiagnosticClipboardText(event({
      clientIP: '192.0.2.10',
      xForwardedFor: '203.0.113.5, 198.51.100.8',
      userAgent: 'test-client/1.0',
    }), t, 'en');

    expect(text).toContain('Direct Client IP: 192.0.2.10');
    expect(text).toContain('Forwarded-For Chain: 203.0.113.5, 198.51.100.8');
    expect(text).toContain('User Agent: test-client/1.0');
  });

  test('keeps realtime badges and recent status bars styled', async () => {
    const styles = await Bun.file(
      new URL('../src/features/monitoring/styles/_realtime.scss', import.meta.url)
    ).text();

    [
      'statusBadge',
      'tonegood',
      'tonewarn',
      'tonebad',
      'patternBars',
      'patternBarsPlain',
      'patternBar',
      'patternBarPlain',
      'patternSuccess',
      'patternFailed',
    ].forEach((className) => expect(styles).toContain(`.${className}`));
  });
});
