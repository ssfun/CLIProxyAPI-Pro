type AuthFileSearchEntry = {
  name?: unknown;
  authIndex?: unknown;
  [key: string]: unknown;
};

export type MonitoringAuthSearchRow = {
  authIndex: string;
  account: string;
  accountMasked: string;
  authLabel: string;
  source: string;
  sourceMasked: string;
};

const isRecordValue = (value: unknown): value is Record<string, unknown> => (
  Boolean(value) && typeof value === 'object' && !Array.isArray(value)
);

const readStringValue = (value: unknown) => (
  typeof value === 'string' || typeof value === 'number' ? String(value).trim() : ''
);

const normalizeAuthIndex = (value: unknown) => readStringValue(value);

const readNestedString = (value: unknown, path: string[]) => {
  let current = value;
  for (const key of path) {
    if (!isRecordValue(current)) return '';
    current = current[key];
  }
  return readStringValue(current);
};

const buildAuthFileSearchText = (entry: AuthFileSearchEntry, authIndex: string) => [
  authIndex,
  readStringValue(entry.name),
  readStringValue(entry.provider),
  readStringValue(entry.type),
  readStringValue(entry.label),
  readStringValue(entry.email),
  readStringValue(entry.account),
  readStringValue(entry.source),
  readStringValue(entry['file_name'] ?? entry.fileName),
  readNestedString(entry, ['id_token', 'email']),
  readNestedString(entry, ['id_token', 'account']),
  readNestedString(entry, ['id_token', 'preferred_username']),
  readNestedString(entry, ['id_token', 'sub']),
]
  .filter(Boolean)
  .join('\n')
  .toLowerCase();

const buildRowSearchText = (row: MonitoringAuthSearchRow) => [
  row.authIndex,
  row.account,
  row.accountMasked,
  row.authLabel,
  row.source,
  row.sourceMasked,
]
  .filter(Boolean)
  .join('\n')
  .toLowerCase();

export const findMonitoringAuthIndexes = (
  authFiles: readonly AuthFileSearchEntry[],
  rows: readonly MonitoringAuthSearchRow[],
  query: string,
  limit = 100
) => {
  const normalizedQuery = query.trim().toLowerCase();
  if (!normalizedQuery || limit <= 0) return '';

  const matches = new Set<string>();
  authFiles.forEach((entry) => {
    const authIndex = normalizeAuthIndex(entry['auth_index'] ?? entry.authIndex);
    if (!authIndex || !buildAuthFileSearchText(entry, authIndex).includes(normalizedQuery)) return;
    matches.add(authIndex);
  });
  rows.forEach((row) => {
    if (!row.authIndex || row.authIndex === '-' || !buildRowSearchText(row).includes(normalizedQuery)) return;
    matches.add(row.authIndex);
  });

  return Array.from(matches).sort().slice(0, limit).join(',');
};
