import { describe, expect, test } from 'bun:test';
import {
  accountInspectionWebSocketProtocol,
  buildAccountInspectionLogsWebSocketUrl,
  nextAccountInspectionReconnectDelay,
} from '../src/services/api/accountInspection';

describe('account inspection transport contract', () => {
  test('builds a secure management websocket URL without query credentials', () => {
    const url = buildAccountInspectionLogsWebSocketUrl('https://example.com/v0/management', true);
    expect(url).toBe('wss://example.com/v0/management/account-inspection/logs?details=1');
    expect(url).not.toContain('secret');
    expect(accountInspectionWebSocketProtocol('a key/with symbols')).toBe(
      'cpa-management.a%20key%2Fwith%20symbols'
    );
  });

  test('caps reconnect backoff at thirty seconds', () => {
    expect(nextAccountInspectionReconnectDelay(0)).toBe(2000);
    expect(nextAccountInspectionReconnectDelay(8000)).toBe(16000);
    expect(nextAccountInspectionReconnectDelay(20000)).toBe(30000);
    expect(nextAccountInspectionReconnectDelay(30000)).toBe(30000);
  });
});
