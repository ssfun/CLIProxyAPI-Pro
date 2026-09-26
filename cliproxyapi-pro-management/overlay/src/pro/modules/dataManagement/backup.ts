export const hasDataBackupManifest = (content: string): boolean => {
  const firstRecord = content.trimStart().split(/\r?\n/, 1)[0];
  if (!firstRecord) return false;
  try {
    const parsed = JSON.parse(firstRecord) as { record_type?: unknown } | null;
    return parsed?.record_type === 'backup_manifest';
  } catch {
    return false;
  }
};

export const isEncryptedDataBackup = (content: string): boolean => {
  try {
    const parsed = JSON.parse(content) as { format?: unknown } | null;
    return parsed?.format === 'cliproxy-pro-encrypted-backup';
  } catch {
    return false;
  }
};
