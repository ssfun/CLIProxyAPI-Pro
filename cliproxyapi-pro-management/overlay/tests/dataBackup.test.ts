import { expect, test } from 'bun:test';
import { hasDataBackupManifest, isEncryptedDataBackup } from '../src/pro/modules/dataManagement/backup';

test('backup classification tolerates arbitrary JSON whitespace and invalid records', () => {
  expect(isEncryptedDataBackup(JSON.stringify({ format: 'cliproxy-pro-encrypted-backup', ciphertext: 'fixture' }, null, '\t'))).toBe(true);
  for (const text of ['', 'null', '{invalid', '{"record_type":"usage_event"}']) {
    expect(hasDataBackupManifest(text)).toBe(false);
    expect(isEncryptedDataBackup(text)).toBe(false);
  }
  expect(hasDataBackupManifest('\n {"record_type":"backup_manifest"}\n{"record_type":"usage_event"}')).toBe(true);
  expect(hasDataBackupManifest('{"record_type":"usage_event"}\n{"record_type":"backup_manifest"}')).toBe(false);
});
