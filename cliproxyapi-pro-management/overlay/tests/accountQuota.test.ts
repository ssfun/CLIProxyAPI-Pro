import { describe, expect, test } from 'bun:test';
import {
  buildAccountQuotaTargetsByAccount,
  getAccountQuotaConfig,
} from '../src/features/monitoring/accountQuota';
import type { MonitoringEventRow } from '../src/features/monitoring/hooks/useMonitoringData';
import type { AuthFileItem } from '../src/types';

const authFile = (provider: string, name: string): AuthFileItem => ({
  provider,
  name,
} as AuthFileItem);

describe('monitoring account quota boundaries', () => {
  test('supports only account providers rendered by the monitoring quota panel', () => {
    expect(getAccountQuotaConfig(authFile('codex', 'codex.json'))?.type).toBe('codex');
    expect(getAccountQuotaConfig(authFile('xai', 'xai.json'))?.type).toBe('xai');
    expect(getAccountQuotaConfig(authFile('gemini-cli', 'gemini.json'))).toBeUndefined();
  });

  test('groups and de-duplicates quota targets by account and credential identity', () => {
    const source = {
      authIndex: 'auth-1',
      account: 'owner@example.com',
      authLabel: 'Primary',
    } as MonitoringEventRow;
    const grouped = buildAccountQuotaTargetsByAccount(
      [source, source],
      new Map([['auth-1', authFile('codex', 'codex.json')]])
    );

    expect(grouped.get('owner@example.com')).toHaveLength(1);
    expect(grouped.get('owner@example.com')?.[0]).toMatchObject({
      authIndex: 'auth-1',
      authLabel: 'Primary',
      fileName: 'codex.json',
    });
  });
});
