import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { hasKeyStateRestoreChanges, type PolicyBackupPreview, normalizeDataDomainInventory, normalizeDataManagementOverview } from '../src/pro/modules/dataManagement/dataManagement';

describe('data-management destructive-operation fencing', () => {
  test('executes cleanup against the previewed domains, cutoff, and record counts', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');

    expect(page).toContain('domains: cleanupPreview.domains.map((domain) => domain.id)');
    expect(page).toContain('beforeMs: cleanupPreview.cutoffMs');
    expect(page).toContain('expectedRecords: Object.fromEntries(cleanupPreview.domains.map((domain) => [domain.id, domain.records]))');
  });

  test('saves only visible settings sections with an optimistic snapshot', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');

    expect(page).toContain("sections.push('retention')");
    expect(page).toContain("sections.push('webdav')");
    expect(page).toContain('dataManagementApi.saveSettings(settings, expectedSettings, sections)');
    expect(page).not.toContain("sections.push('modelPriceSync')");
  });

  test('model-price settings save cannot overwrite data-management sections', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/MonitoringCenterPage.tsx'), 'utf8');

    expect(page).toContain("sections: ['modelPriceSync']");
    expect(page).toContain('expectedSettings,');
  });

  test('sends encrypted-export passphrases in a POST body', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');

    expect(page).toContain("apiClient.post<Blob>('/data/backups/export', { passphrase }");
    expect(page).not.toContain("'X-CLIProxy-Backup-Passphrase': passphrase");
  });

  test('normalizes nullable arrays returned by older data-management cores', () => {
    const domain = normalizeDataDomainInventory({
      id: 'routing-runtime',
      owner: 'scheduler',
      schemaVersion: 1,
      records: 1,
      updatedAtMs: 1,
      backupIncluded: true,
      restoreMode: 'replace',
      cleanupSupported: false,
      sensitivity: 'internal',
      secretClasses: null as unknown as string[],
      available: true,
    });
    expect(domain.secretClasses).toEqual([]);

    const overview = normalizeDataManagementOverview({
      service: 'ready',
      dbPath: '/tmp/usage.sqlite',
      dbSizeBytes: 1,
      walSizeBytes: 0,
      events: 0,
      deadLetters: 0,
      latestId: 0,
      latestTimestampMs: 0,
      generation: 1,
      resetAtMs: 0,
      webdavEnabled: false,
      webdavConfigured: false,
      domains: null as unknown as [],
      secretClasses: null as unknown as string[],
      updatedAtMs: 1,
    });
    expect(overview.domains).toEqual([]);
    expect(overview.secretClasses).toEqual([]);
  });

  test('keeps header action button content aligned', () => {
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');
    const styles = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.module.scss'), 'utf8');
    const header = page.slice(page.indexOf('<header className={styles.header}>'), page.indexOf('</header>'));

    expect(styles).toContain('.headerActions :global(.btn) > span');
    expect(styles).toContain('gap: 6px; white-space: nowrap;');
    expect(header).toContain("onClick={() => setActiveView('backups')}");
    expect(header).not.toContain('exportBackup()');
    expect(header).not.toContain('backupNow()');
  });

  test('restores a listed WebDAV backup through the data-management preview pipeline', () => {
    const api = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/dataManagement.ts'), 'utf8');
    const page = readFileSync(resolve(import.meta.dir, '../src/pro/modules/dataManagement/DataManagementPage.tsx'), 'utf8');

    expect(api).toContain("'/data/backups/webdav/preview'");
    expect(api).toContain("'/data/backups/webdav/restore'");
    expect(api).toContain('expectedSha256');
    expect(page).toContain('dataManagementApi.previewWebDAVRestore(backup.fileName)');
    expect(page).toContain('restorePreview?.backupSha256');
    expect(page).toContain('onClick={() => void previewWebDAVRestore(backup)}');
  });

  test('provides enabled and disabled common labels in every locale', () => {
    const locales = JSON.parse(readFileSync(resolve(import.meta.dir, '../src/pro/locales.generated.json'), 'utf8')) as Record<string, { common?: Record<string, string>; data_management?: Record<string, string> }>;

    for (const locale of ['en', 'ru', 'zh-CN', 'zh-TW']) {
      expect(locales[locale]?.common?.enabled).toBeTruthy();
      expect(locales[locale]?.common?.disabled).toBeTruthy();
      expect(locales[locale]?.data_management?.restore_from_webdav).toBeTruthy();
    }
  });
});

describe('disabled-key restore confirmation', () => {
  test('warns for changed key identities even when the totals match', () => {
    expect(hasKeyStateRestoreChanges({ currentDisabledKeys: 1, targetDisabledKeys: 1,
      addedDisabledKeys: 1, removedDisabledKeys: 1 } as PolicyBackupPreview)).toBe(true);
  });
  test('warns for an effective change caused only by takeover', () => {
    expect(hasKeyStateRestoreChanges({ newlyBlockedKeys: 1 } as PolicyBackupPreview)).toBe(true);
    expect(hasKeyStateRestoreChanges({ newlyAllowedKeys: 1 } as PolicyBackupPreview)).toBe(true);
  });
  test('retained settings and older Core responses do not fabricate changes', () => {
    expect(hasKeyStateRestoreChanges({ currentDisabledKeys: 2, targetDisabledKeys: 2 } as PolicyBackupPreview)).toBe(false);
    expect(hasKeyStateRestoreChanges({} as PolicyBackupPreview)).toBe(false);
    expect(hasKeyStateRestoreChanges(undefined)).toBe(false);
  });
});

test('restore confirmation includes saved and effective concurrency changes', () => {
  expect(hasKeyStateRestoreChanges({ changedConcurrencyKeys: 1 } as PolicyBackupPreview)).toBe(true);
  expect(hasKeyStateRestoreChanges({ effectiveConcurrencyChanges: 1 } as PolicyBackupPreview)).toBe(true);
  expect(hasKeyStateRestoreChanges({ currentConcurrencyKeys: 1, targetConcurrencyKeys: 1, changedConcurrencyKeys: 0, effectiveConcurrencyChanges: 0 } as PolicyBackupPreview)).toBe(false);
});
