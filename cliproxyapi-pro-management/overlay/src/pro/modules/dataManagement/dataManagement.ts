import { apiClient } from '@/services/api/client';
import type { DataManagementSettings } from './dataManagementSettings';

export type DataSensitivity = 'internal' | 'sensitive' | 'secret';

export interface DataOperation {
  id: number;
  kind: string;
  status: 'running' | 'success' | 'failed';
  target: string;
  fileName: string;
  startedAtMs: number;
  finishedAtMs: number;
  sizeBytes: number;
  affectedRecords: number;
  secretClasses: string[];
  message: string;
  metadata: Record<string, unknown>;
}

export interface DataDomainInventory {
  id: string;
  owner: string;
  schemaVersion: number;
  records: number;
  updatedAtMs: number;
  backupIncluded: boolean;
  restoreMode: string;
  cleanupSupported: boolean;
  sensitivity: DataSensitivity;
  secretClasses: string[];
  available: boolean;
  error?: string;
}

export interface DataManagementOverview {
  service: string;
  dbPath: string;
  dbSizeBytes: number;
  walSizeBytes: number;
  events: number;
  deadLetters: number;
  latestId: number;
  latestTimestampMs: number;
  generation: number;
  resetAtMs: number;
  webdavEnabled: boolean;
  webdavConfigured: boolean;
  lastBackup?: DataOperation;
  domains: DataDomainInventory[];
  secretClasses: string[];
  updatedAtMs: number;
}

export interface WebDAVBackup {
  fileName: string;
  sizeBytes: number;
  lastModified?: string;
  lastModifiedMs?: number;
}

export interface DataBackupHistory {
  backups: WebDAVBackup[];
  operations: DataOperation[];
}

export interface DataRestoreDomainPreview {
  id: string;
  owner: string;
  currentRecords: number;
  backupRecords: number;
  action: string;
  sensitivity: DataSensitivity;
  secretClasses: string[];
  available: boolean;
  error?: string;
}

export interface PolicyBackupPreview {
  currentDisabledKeys?: number;
  targetDisabledKeys?: number;
  addedDisabledKeys?: number;
  removedDisabledKeys?: number;
  newlyBlockedKeys?: number;
  newlyAllowedKeys?: number;

  hasPolicies: boolean;
  replacePolicies: number;
  preservePolicies: number;
  replaceProfiles: number;
  preserveProfiles: number;
  targetPolicies: number;
  targetProfiles: number;
  associatedPolicies: number;
  orphanedPolicies: number;
  currentTakeoverEnabled: boolean;
  targetTakeoverEnabled: boolean;
}

export interface DataRestorePreview {
  backupSha256: string;
  legacyBackup: boolean;
  integrityProtected: boolean;
  encrypted: boolean;
  restoresAPIKeys: false;
  domains: DataRestoreDomainPreview[];
  secretClasses: string[];
  policyBackup?: PolicyBackupPreview;
}

export interface DataCleanupRequest {
  domains: string[];
  retentionDays: number;
  beforeMs?: number;
  expectedRecords?: Record<string, number>;
}

export interface DataCleanupPreview {
  domains: Array<{ id: string; records: number; cutoffMs: number; reclaimable: boolean }>;
  totalRecords: number;
  cutoffMs: number;
}

export interface WebDAVConnectionTestResult {
  connected: boolean;
  writable: boolean;
  deletable: boolean;
  latencyMs: number;
}

export interface DataStatisticsResetResult {
  deletedEvents: number;
  deletedAuthRuntimeStats: number;
  generation: number;
  resetAtMs: number;
}

const normalizeStringArray = (value: unknown): string[] => (
  Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
);

const normalizeOperation = (operation: DataOperation): DataOperation => ({
  ...operation,
  secretClasses: normalizeStringArray(operation.secretClasses),
  metadata: operation.metadata && typeof operation.metadata === 'object' ? operation.metadata : {},
});

export const normalizeDataDomainInventory = (domain: DataDomainInventory): DataDomainInventory => ({
  ...domain,
  secretClasses: normalizeStringArray(domain.secretClasses),
});

export const normalizeDataManagementOverview = (overview: DataManagementOverview): DataManagementOverview => ({
  ...overview,
  domains: Array.isArray(overview.domains) ? overview.domains.map(normalizeDataDomainInventory) : [],
  secretClasses: normalizeStringArray(overview.secretClasses),
  lastBackup: overview.lastBackup ? normalizeOperation(overview.lastBackup) : undefined,
});

const normalizeRestorePreview = (preview: DataRestorePreview): DataRestorePreview => ({
  ...preview,
  domains: Array.isArray(preview.domains)
    ? preview.domains.map((domain) => ({ ...domain, secretClasses: normalizeStringArray(domain.secretClasses) }))
    : [],
  secretClasses: normalizeStringArray(preview.secretClasses),
});

export const dataManagementApi = {
  async overview(): Promise<DataManagementOverview> {
    return normalizeDataManagementOverview(await apiClient.get<DataManagementOverview>('/data/overview'));
  },
  async backups(): Promise<DataBackupHistory> {
    const history = await apiClient.get<DataBackupHistory>('/data/backups');
    return {
      backups: Array.isArray(history.backups) ? history.backups : [],
      operations: Array.isArray(history.operations) ? history.operations.map(normalizeOperation) : [],
    };
  },
  async operations(): Promise<{ operations: DataOperation[] }> {
    const result = await apiClient.get<{ operations: DataOperation[] }>('/data/operations');
    return { operations: Array.isArray(result.operations) ? result.operations.map(normalizeOperation) : [] };
  },
  settings(): Promise<{ settings: DataManagementSettings }> {
    return apiClient.get<{ settings: DataManagementSettings }>('/data/settings');
  },
  saveSettings(
    settings: DataManagementSettings,
    expectedSettings: DataManagementSettings,
    sections: Array<'retention' | 'webdav'>
  ): Promise<{ settings: DataManagementSettings }> {
    return apiClient.put<{ settings: DataManagementSettings }>('/data/settings', { settings, expectedSettings, sections });
  },
  backupNow(): Promise<{ backup: WebDAVBackup }> {
    return apiClient.post<{ backup: WebDAVBackup }>('/data/backups/now', {});
  },
  testWebDAV(): Promise<WebDAVConnectionTestResult> {
    return apiClient.post<WebDAVConnectionTestResult>('/data/backups/test', {});
  },
  async previewRestore(data: ArrayBuffer, passphrase: string, allowLegacy: boolean): Promise<DataRestorePreview> {
    const preview = await apiClient.post<DataRestorePreview>('/data/backups/preview', data, {
      headers: {
        'Content-Type': 'application/octet-stream',
        ...(passphrase ? { 'X-CLIProxy-Backup-Passphrase': passphrase } : {}),
      },
      params: allowLegacy ? { allow_legacy: 1 } : undefined,
    });
    return normalizeRestorePreview(preview);
  },
  async previewWebDAVRestore(fileName: string): Promise<DataRestorePreview> {
    const preview = await apiClient.post<DataRestorePreview>('/data/backups/webdav/preview', { fileName });
    return normalizeRestorePreview(preview);
  },
  restore(data: ArrayBuffer, passphrase: string, allowLegacy: boolean): Promise<Record<string, unknown>> {
    return apiClient.post<Record<string, unknown>>('/data/backups/restore', data, {
      headers: {
        'Content-Type': 'application/octet-stream',
        ...(passphrase ? { 'X-CLIProxy-Backup-Passphrase': passphrase } : {}),
      },
      params: allowLegacy ? { allow_legacy: 1 } : undefined,
    });
  },
  restoreWebDAV(fileName: string, allowLegacy: boolean, expectedSha256: string): Promise<Record<string, unknown>> {
    return apiClient.post<Record<string, unknown>>('/data/backups/webdav/restore', { fileName, allowLegacy, expectedSha256 });
  },
  previewCleanup(request: DataCleanupRequest): Promise<DataCleanupPreview> {
    return apiClient.post<DataCleanupPreview>('/data/maintenance/preview', request);
  },
  executeCleanup(request: DataCleanupRequest): Promise<DataCleanupPreview> {
    return apiClient.post<DataCleanupPreview>('/data/maintenance/execute', request);
  },
  resetStatistics(): Promise<DataStatisticsResetResult> {
    return apiClient.post<DataStatisticsResetResult>('/data/statistics/reset', { confirm: true });
  },
};

// Older Core builds omit these counts; do not fabricate a state change.
export const hasKeyStateRestoreChanges = (preview?: PolicyBackupPreview): boolean =>
  Boolean(preview && [preview.addedDisabledKeys, preview.removedDisabledKeys,
    preview.newlyBlockedKeys, preview.newlyAllowedKeys].some((count) => (count ?? 0) > 0));
