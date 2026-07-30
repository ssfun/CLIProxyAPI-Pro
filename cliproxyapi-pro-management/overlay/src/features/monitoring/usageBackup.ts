export const hasUsageBackupManifest = (content: string): boolean => {
  const firstRecord = content
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find(Boolean);
  if (!firstRecord) return false;
  try {
    const parsed = JSON.parse(firstRecord) as { record_type?: unknown };
    return parsed.record_type === 'backup_manifest';
  } catch {
    return false;
  }
};
