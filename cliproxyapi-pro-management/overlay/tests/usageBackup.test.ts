import { describe, expect, test } from 'bun:test';
import { hasUsageBackupManifest } from '../src/features/monitoring/usageBackup';

describe('usage backup compatibility gate', () => {
  test('accepts only a first non-empty manifest record as verified backup', () => {
    expect(
      hasUsageBackupManifest(
        '\n{"record_type":"backup_manifest","version":1}\n{"event_hash":"event"}\n'
      )
    ).toBe(true);
    expect(
      hasUsageBackupManifest(
        '{"event_hash":"legacy"}\n{"record_type":"backup_manifest","version":1}\n'
      )
    ).toBe(false);
  });

  test('treats empty, malformed, and non-manifest JSONL as legacy input', () => {
    expect(hasUsageBackupManifest('')).toBe(false);
    expect(hasUsageBackupManifest('{invalid')).toBe(false);
    expect(hasUsageBackupManifest('{"record_type":"usage_event"}')).toBe(false);
  });
});
