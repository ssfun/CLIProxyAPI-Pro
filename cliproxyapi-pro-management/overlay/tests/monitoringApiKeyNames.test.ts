import { describe, expect, test } from 'bun:test';
import { buildConfiguredApiKeyMap, buildMonitoringApiKeyNames, formatMonitoringApiKeyLabel } from '@/pro/modules/monitoring/features/apiKeyIdentity';

describe('monitoring API key names', () => {
  test('associates colliding masks and duplicate names by full hash', () => {
    const keys = buildConfiguredApiKeyMap(['sk-first-01', 'sk-second-01']).keys;
    expect(keys[0].masked).toBe(keys[1].masked);
    const names = buildMonitoringApiKeyNames([
      { apiKeyHash: keys[0].hash, displayName: 'Production' },
      { apiKeyHash: keys[1].hash, displayName: 'Testing' },
      { apiKeyHash: 'historical-hash', displayName: 'Production' },
    ]);
    expect(formatMonitoringApiKeyLabel({ ...keys[0], name: names.get(keys[0].hash) })).toBe(`Production (${keys[0].masked})`);
    expect(formatMonitoringApiKeyLabel({ ...keys[1], name: names.get(keys[1].hash) })).toBe(`Testing (${keys[1].masked})`);
    expect(names.get('historical-hash')).toBe('Production');
    expect(names.size).toBe(3);
  });
  test('falls back for older Core, empty names, and removed policies', () => {
    expect(buildMonitoringApiKeyNames().size).toBe(0);
    expect(buildMonitoringApiKeyNames([{ apiKeyHash: 'hash', displayName: '  ' }]).size).toBe(0);
    expect(formatMonitoringApiKeyLabel({ masked: 'sk******01', name: '  ' })).toBe('sk******01');
    expect(formatMonitoringApiKeyLabel({ masked: 'Unattributed' })).toBe('Unattributed');
    const before = buildMonitoringApiKeyNames([{ apiKeyHash: 'hash', displayName: 'Old' }]);
    const after = buildMonitoringApiKeyNames([{ apiKeyHash: 'hash', displayName: ' New ' }]);
    expect(before.get('hash')).toBe('Old');
    expect(after.get('hash')).toBe('New');
    expect(buildMonitoringApiKeyNames([]).has('hash')).toBe(false);
  });
});
