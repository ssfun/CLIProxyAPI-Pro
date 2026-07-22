import { describe, expect, test } from 'bun:test';
import { findMonitoringAuthIndexes } from '../src/features/monitoring/monitoringAuthSearch';

describe('monitoring auth metadata search', () => {
  test('finds auth indexes from the complete auth-file metadata without a recent event row', () => {
    const authFiles = [
      {
        name: 'codex-old.json',
        provider: 'codex',
        authIndex: 'auth-old',
        id_token: { email: 'older@example.com' },
      },
      {
        name: 'codex-new.json',
        provider: 'codex',
        authIndex: 'auth-new',
        email: 'newer@example.com',
      },
    ];

    expect(findMonitoringAuthIndexes(authFiles, [], 'older@example.com')).toBe('auth-old');
  });

  test('unions sanitized row labels, de-duplicates indexes, and ignores unrelated secret fields', () => {
    const authFiles = [
      { name: 'one.json', authIndex: 'auth-b', label: 'Shared account', refresh_token: 'must-not-match' },
      { name: 'two.json', authIndex: 'auth-a', account: 'Shared account' },
    ];
    const rows = [{
      authIndex: 'auth-b',
      account: 'Shared account',
      accountMasked: 'Sh***',
      authLabel: 'Shared account',
      source: 'one.json',
      sourceMasked: 'one.json',
    }];

    expect(findMonitoringAuthIndexes(authFiles, rows, 'shared')).toBe('auth-a,auth-b');
    expect(findMonitoringAuthIndexes(authFiles, rows, 'must-not-match')).toBe('');
  });
});
