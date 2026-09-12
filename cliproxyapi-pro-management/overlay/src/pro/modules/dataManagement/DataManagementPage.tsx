import { hasKeyStateRestoreChanges } from './dataManagement';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import {
  IconAlertTriangle,
  IconCheck,
  IconCheckCircle2,
  IconDownload,
  IconEye,
  IconEyeOff,
  IconInfo,
  IconRefreshCw,
  IconScrollText,
  IconSettings,
  IconShield,
  IconTrash2,
  IconX,
} from '@/components/ui/icons';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import { IconChartColumnIncreasing } from '@/pro/icons';
import { ProFeatureTabs } from '@/pro/shared/ProFeatureTabs';
import { ProTaskDialog, ProWorkspaceSheet } from '@/pro/shared/ProSurface';
import configStyles from '@/pro/shared/FloatingActionBar.module.scss';
import { useAuthStore, useNotificationStore } from '@/stores';
import { apiClient } from '@/services/api/client';
import {
  dataManagementApi,
  type DataBackupHistory,
  type DataCleanupPreview,
  type DataDomainInventory,
  type DataManagementOverview,
  type DataOperation,
  type DataRestorePreview,
  type WebDAVBackup,
} from './dataManagement';
import {
  buildDataManagementSettingsFromDraft,
  createDataManagementSettingsDraft,
  type DataManagementSettingsDraft,
} from './dataManagementSettings';
import styles from './DataManagementPage.module.scss';
import { hasDataBackupManifest } from './backup';

type DataManagementView = 'overview' | 'backups' | 'retention' | 'domains' | 'operations';

const formatCount = (value: number) => new Intl.NumberFormat().format(Math.max(0, Number(value) || 0));

const formatBytes = (value: number) => {
  const bytes = Math.max(0, Number(value) || 0);
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let current = bytes / 1024;
  let unit = units[0];
  for (let index = 1; index < units.length && current >= 1024; index += 1) {
    current /= 1024;
    unit = units[index];
  }
  return `${current >= 10 ? current.toFixed(1) : current.toFixed(2)} ${unit}`;
};

const formatDateTime = (value?: number) => {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString();
};

const isEncryptedBackup = async (file: File) => {
  if (/\.encrypted\.json$/i.test(file.name)) return true;
  const head = await file.slice(0, 512).text();
  return head.includes('"format":"cliproxy-pro-encrypted-backup"') || head.includes('"format": "cliproxy-pro-encrypted-backup"');
};

const downloadBlob = (blob: Blob, fileName: string) => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
};

const operationTone = (operation: DataOperation) => {
  if (operation.status === 'success') return styles.toneGood;
  if (operation.status === 'failed') return styles.toneDanger;
  return styles.toneWarning;
};

export function DataManagementPage() {
  const { t } = useTranslation();
  const pageTransitionLayer = usePageTransitionLayer();
  const isCurrentLayer = pageTransitionLayer ? pageTransitionLayer.isCurrentLayer : true;
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const [activeView, setActiveView] = useState<DataManagementView>('overview');
  const [overview, setOverview] = useState<DataManagementOverview | null>(null);
  const [history, setHistory] = useState<DataBackupHistory>({ backups: [], operations: [] });
  const [operations, setOperations] = useState<DataOperation[]>([]);
  const [settingsDraft, setSettingsDraft] = useState<DataManagementSettingsDraft>(() => createDataManagementSettingsDraft());
  const [savedSettingsDraft, setSavedSettingsDraft] = useState<DataManagementSettingsDraft>(() => createDataManagementSettingsDraft());
  const [loading, setLoading] = useState(true);
  const [backupHistoryLoading, setBackupHistoryLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [backupBusy, setBackupBusy] = useState(false);
  const [testingWebDAV, setTestingWebDAV] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [showWebDAVPassword, setShowWebDAVPassword] = useState(false);
  const [cleanupDomains, setCleanupDomains] = useState<string[]>(['usage-events']);
  const [cleanupRetentionDays, setCleanupRetentionDays] = useState('30');
  const [cleanupPreview, setCleanupPreview] = useState<DataCleanupPreview | null>(null);
  const [cleanupBusy, setCleanupBusy] = useState(false);
  const [encryptionDialogOpen, setEncryptionDialogOpen] = useState(false);
  const [encryptionPassphrase, setEncryptionPassphrase] = useState('');
  const [encryptionPassphraseConfirm, setEncryptionPassphraseConfirm] = useState('');
  const [restorePassphraseDialogOpen, setRestorePassphraseDialogOpen] = useState(false);
  const [restorePassphrase, setRestorePassphrase] = useState('');
  const [restoreFileName, setRestoreFileName] = useState('');
  const [restoreBuffer, setRestoreBuffer] = useState<ArrayBuffer | null>(null);
  const [restoreWebDAVFileName, setRestoreWebDAVFileName] = useState('');
  const [restoreEncrypted, setRestoreEncrypted] = useState(false);
  const [restoreAllowLegacy, setRestoreAllowLegacy] = useState(false);
  const [restorePreview, setRestorePreview] = useState<DataRestorePreview | null>(null);
  const [restorePreviewOpen, setRestorePreviewOpen] = useState(false);
  const [restoreBusy, setRestoreBusy] = useState(false);
  const restoreInputRef = useRef<HTMLInputElement | null>(null);
  const loadSequenceRef = useRef(0);
  const settingsDraftRevisionRef = useRef(0);
  const restoreFileSequenceRef = useRef(0);
  const restorePreviewSequenceRef = useRef(0);

  const updateSettingsDraft = useCallback((updater: (current: DataManagementSettingsDraft) => DataManagementSettingsDraft) => {
    settingsDraftRevisionRef.current += 1;
    setSettingsDraft(updater);
  }, []);

  const dirty = useMemo(
    () => JSON.stringify(settingsDraft) !== JSON.stringify(savedSettingsDraft),
    [savedSettingsDraft, settingsDraft]
  );

  useUnsavedChangesGuard({
    enabled: isCurrentLayer,
    shouldBlock: dirty,
    dialog: {
      title: t('common.unsaved_changes_title'),
      message: t('common.unsaved_changes_message'),
      confirmText: t('common.confirm'),
      cancelText: t('common.cancel'),
    },
  });

  const loadCore = useCallback(async (silent = false) => {
    const sequence = ++loadSequenceRef.current;
    const draftRevision = settingsDraftRevisionRef.current;
    if (connectionStatus !== 'connected') {
      setLoading(false);
      return;
    }
    if (!silent) setLoading(true);
    try {
      const [nextOverview, settingsResponse, operationsResponse] = await Promise.all([
        dataManagementApi.overview(),
        dataManagementApi.settings(),
        dataManagementApi.operations(),
      ]);
      if (sequence !== loadSequenceRef.current) return;
      setOverview(nextOverview);
      setOperations(operationsResponse.operations ?? []);
      if (!dirty && draftRevision === settingsDraftRevisionRef.current) {
        const nextDraft = createDataManagementSettingsDraft(settingsResponse.settings);
        setSettingsDraft(nextDraft);
        setSavedSettingsDraft(nextDraft);
      }
      setLoadError('');
    } catch (error) {
      if (sequence !== loadSequenceRef.current) return;
      setLoadError(error instanceof Error ? error.message : String(error));
    } finally {
      if (sequence === loadSequenceRef.current) setLoading(false);
    }
  }, [connectionStatus, dirty]);

  const loadBackupHistory = useCallback(async () => {
    if (connectionStatus !== 'connected') return;
    setBackupHistoryLoading(true);
    try {
      const next = await dataManagementApi.backups();
      setHistory({ backups: next.backups ?? [], operations: next.operations ?? [] });
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setBackupHistoryLoading(false);
    }
  }, [connectionStatus, showNotification]);

  useEffect(() => {
    void loadCore();
  }, [loadCore]);

  useEffect(() => {
    if (activeView === 'backups') void loadBackupHistory();
  }, [activeView, loadBackupHistory]);

  const saveSettings = useCallback(async () => {
    const settings = buildDataManagementSettingsFromDraft(settingsDraft);
    const expectedSettings = buildDataManagementSettingsFromDraft(savedSettingsDraft);
    const sections: Array<'retention' | 'webdav'> = [];
    if (settings.retentionDays !== expectedSettings.retentionDays) sections.push('retention');
    if (JSON.stringify(settings.webdav) !== JSON.stringify(expectedSettings.webdav)) sections.push('webdav');
    if (!sections.length) return;
    const submittedRevision = settingsDraftRevisionRef.current;
    if (sections.includes('webdav') && settings.webdav.enabled && !settings.webdav.url) {
      showNotification(t('data_management.webdav_url_required', { defaultValue: 'WebDAV URL is required when backup is enabled' }), 'warning');
      return;
    }
    setSaving(true);
    try {
      const response = await dataManagementApi.saveSettings(settings, expectedSettings, sections);
      const nextDraft = createDataManagementSettingsDraft(response.settings);
      setSavedSettingsDraft(nextDraft);
      if (submittedRevision === settingsDraftRevisionRef.current) {
        setSettingsDraft(nextDraft);
      }
      showNotification(t('data_management.settings_saved', { defaultValue: 'Data management settings saved' }), 'success');
      await loadCore(true);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setSaving(false);
    }
  }, [loadCore, savedSettingsDraft, settingsDraft, showNotification, t]);

  const exportBackup = useCallback(async (passphrase = '') => {
    if (connectionStatus !== 'connected') return;
    setBackupBusy(true);
    try {
      const encrypted = Boolean(passphrase);
      const responseData = encrypted
        ? await apiClient.post<Blob>('/data/backups/export', { passphrase }, { responseType: 'blob' })
        : (await apiClient.getRaw('/data/backups/export', { responseType: 'blob' })).data;
      const blob = responseData instanceof Blob
        ? responseData
        : new Blob([responseData], { type: encrypted ? 'application/json' : 'application/x-ndjson' });
      const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
      downloadBlob(blob, encrypted
        ? `cliproxy-pro-backup-${timestamp}.encrypted.json`
        : `cliproxy-pro-backup-${timestamp}.jsonl`);
      showNotification(t('data_management.export_success', { defaultValue: 'Pro backup downloaded' }), 'success');
      await loadCore(true);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setBackupBusy(false);
    }
  }, [connectionStatus, loadCore, showNotification, t]);

  const confirmEncryptedExport = useCallback(async () => {
    if (encryptionPassphrase.length < 8) {
      showNotification(t('data_management.passphrase_too_short', { defaultValue: 'Use a passphrase with at least 8 characters' }), 'warning');
      return;
    }
    if (encryptionPassphrase !== encryptionPassphraseConfirm) {
      showNotification(t('data_management.passphrase_mismatch', { defaultValue: 'Passphrases do not match' }), 'warning');
      return;
    }
    setEncryptionDialogOpen(false);
    await exportBackup(encryptionPassphrase);
    setEncryptionPassphrase('');
    setEncryptionPassphraseConfirm('');
  }, [encryptionPassphrase, encryptionPassphraseConfirm, exportBackup, showNotification, t]);

  const backupNow = useCallback(async () => {
    if (!overview?.webdavConfigured) {
      showNotification(t('data_management.webdav_configure_first', { defaultValue: 'Configure and save WebDAV before creating a remote backup' }), 'warning');
      setActiveView('backups');
      return;
    }
    setBackupBusy(true);
    try {
      const result = await dataManagementApi.backupNow();
      showNotification(t('data_management.backup_now_success', {
        defaultValue: 'Backup {{name}} uploaded',
        name: result.backup.fileName,
      }), 'success');
      await Promise.all([loadCore(true), loadBackupHistory()]);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setBackupBusy(false);
    }
  }, [loadBackupHistory, loadCore, overview?.webdavConfigured, showNotification, t]);

  const testWebDAV = useCallback(async () => {
    if (dirty) {
      showNotification(t('data_management.save_before_test', { defaultValue: 'Save the WebDAV settings before testing the connection' }), 'warning');
      return;
    }
    setTestingWebDAV(true);
    try {
      const result = await dataManagementApi.testWebDAV();
      showNotification(t('data_management.webdav_test_success', {
        defaultValue: 'WebDAV write and cleanup test passed in {{latency}} ms',
        latency: result.latencyMs,
      }), 'success');
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setTestingWebDAV(false);
    }
  }, [dirty, showNotification, t]);

  const previewRestoreBuffer = useCallback(async (buffer: ArrayBuffer, passphrase: string, allowLegacy: boolean) => {
    const sequence = ++restorePreviewSequenceRef.current;
    setRestoreBusy(true);
    try {
      const preview = await dataManagementApi.previewRestore(buffer, passphrase, allowLegacy);
      if (sequence !== restorePreviewSequenceRef.current) return;
      setRestorePreview(preview);
      setRestorePreviewOpen(true);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      if (sequence === restorePreviewSequenceRef.current) setRestoreBusy(false);
    }
  }, [showNotification]);

  const handleRestoreFile = useCallback(async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) return;
    const fileSequence = ++restoreFileSequenceRef.current;
    restorePreviewSequenceRef.current += 1;
    setRestoreBusy(false);
    const [buffer, encrypted] = await Promise.all([file.arrayBuffer(), isEncryptedBackup(file)]);
    if (fileSequence !== restoreFileSequenceRef.current) return;
    setRestoreFileName(file.name);
    setRestoreBuffer(buffer);
    setRestoreWebDAVFileName('');
    setRestoreEncrypted(encrypted);
    if (encrypted) {
      setRestorePassphrase('');
      setRestorePassphraseDialogOpen(true);
      return;
    }
    const allowLegacy = !hasDataBackupManifest(await file.text());
    setRestoreAllowLegacy(allowLegacy);
    await previewRestoreBuffer(buffer, '', allowLegacy);
  }, [previewRestoreBuffer]);

  const previewWebDAVRestore = useCallback(async (backup: WebDAVBackup) => {
    restoreFileSequenceRef.current += 1;
    const sequence = ++restorePreviewSequenceRef.current;
    setRestoreBusy(true);
    setRestoreBuffer(null);
    setRestoreWebDAVFileName(backup.fileName);
    setRestoreFileName(backup.fileName);
    setRestoreEncrypted(false);
    setRestorePassphrase('');
    try {
      const preview = await dataManagementApi.previewWebDAVRestore(backup.fileName);
      if (sequence !== restorePreviewSequenceRef.current) return;
      setRestoreAllowLegacy(preview.legacyBackup);
      setRestorePreview(preview);
      setRestorePreviewOpen(true);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      if (sequence === restorePreviewSequenceRef.current) setRestoreBusy(false);
    }
  }, [showNotification]);

  const previewEncryptedRestore = useCallback(async () => {
    if (!restoreBuffer) return;
    setRestorePassphraseDialogOpen(false);
    setRestoreAllowLegacy(false);
    await previewRestoreBuffer(restoreBuffer, restorePassphrase, false);
  }, [previewRestoreBuffer, restoreBuffer, restorePassphrase]);

  const executeRestore = useCallback(async () => {
    if (!restoreBuffer && !restoreWebDAVFileName) return;
    setRestoreBusy(true);
    try {
      if (restoreWebDAVFileName) {
        await dataManagementApi.restoreWebDAV(
          restoreWebDAVFileName,
          restoreAllowLegacy,
          restorePreview?.backupSha256 ?? ''
        );
      } else if (restoreBuffer) {
        await dataManagementApi.restore(restoreBuffer, restoreEncrypted ? restorePassphrase : '', restoreAllowLegacy);
      }
      setRestorePreviewOpen(false);
      setRestorePreview(null);
      setRestoreBuffer(null);
      setRestoreWebDAVFileName('');
      setRestoreFileName('');
      setRestorePassphrase('');
      showNotification(t('data_management.restore_success', { defaultValue: 'Pro backup restored' }), 'success');
      await Promise.all([loadCore(true), loadBackupHistory()]);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setRestoreBusy(false);
    }
  }, [loadBackupHistory, loadCore, restoreAllowLegacy, restoreBuffer, restoreEncrypted, restorePassphrase, restorePreview, restoreWebDAVFileName, showNotification, t]);

  const previewCleanup = useCallback(async () => {
    setCleanupBusy(true);
    try {
      const preview = await dataManagementApi.previewCleanup({
        domains: cleanupDomains,
        retentionDays: Math.max(1, Number(cleanupRetentionDays) || 30),
      });
      setCleanupPreview(preview);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setCleanupBusy(false);
    }
  }, [cleanupDomains, cleanupRetentionDays, showNotification]);

  const executeCleanup = useCallback(() => {
    if (!cleanupPreview) return;
    showConfirmation({
      title: t('data_management.cleanup_confirm_title', { defaultValue: 'Delete the previewed data?' }),
      message: t('data_management.cleanup_confirm_message', {
        defaultValue: 'This permanently deletes {{count}} records older than the selected cutoff. This action cannot be undone.',
        count: formatCount(cleanupPreview.totalRecords),
      }),
      confirmText: t('data_management.cleanup_execute', { defaultValue: 'Delete data' }),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: async () => {
        setCleanupBusy(true);
        try {
          const result = await dataManagementApi.executeCleanup({
            domains: cleanupPreview.domains.map((domain) => domain.id),
            retentionDays: 0,
            beforeMs: cleanupPreview.cutoffMs,
            expectedRecords: Object.fromEntries(cleanupPreview.domains.map((domain) => [domain.id, domain.records])),
          });
          setCleanupPreview(null);
          showNotification(t('data_management.cleanup_success', {
            defaultValue: 'Deleted {{count}} records',
            count: formatCount(result.totalRecords),
          }), 'success');
          await loadCore(true);
        } catch (error) {
          showNotification(error instanceof Error ? error.message : String(error), 'error');
        } finally {
          setCleanupBusy(false);
        }
      },
    });
  }, [cleanupPreview, loadCore, showConfirmation, showNotification, t]);

  const resetStatistics = useCallback(() => {
    showConfirmation({
      title: t('data_management.reset_confirm_title', { defaultValue: 'Reset all request statistics?' }),
      message: t('data_management.reset_confirm_message', {
        defaultValue: 'This permanently deletes {{count}} request events and account runtime counters. Settings, prices, routing cursors and backups are preserved.',
        count: formatCount(overview?.events ?? 0),
      }),
      confirmText: t('data_management.reset_action', { defaultValue: 'Reset statistics' }),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: async () => {
        setCleanupBusy(true);
        try {
          const result = await dataManagementApi.resetStatistics();
          showNotification(t('data_management.reset_success', {
            defaultValue: 'Deleted {{events}} request events and {{accounts}} account counters',
            events: formatCount(result.deletedEvents),
            accounts: formatCount(result.deletedAuthRuntimeStats),
          }), 'success');
          await loadCore(true);
        } catch (error) {
          showNotification(error instanceof Error ? error.message : String(error), 'error');
        } finally {
          setCleanupBusy(false);
        }
      },
    });
  }, [loadCore, overview?.events, showConfirmation, showNotification, t]);

  const toggleCleanupDomain = (id: string) => {
    setCleanupPreview(null);
    setCleanupDomains((current) => current.includes(id)
      ? current.filter((item) => item !== id)
      : [...current, id]);
  };

  const tabs = [
    { key: 'overview', label: t('data_management.tab_overview', { defaultValue: 'Overview' }), icon: <IconChartColumnIncreasing size={16} /> },
    { key: 'backups', label: t('data_management.tab_backups', { defaultValue: 'Backup & restore' }), icon: <IconRefreshCw size={16} /> },
    { key: 'retention', label: t('data_management.tab_retention', { defaultValue: 'Retention & cleanup' }), icon: <IconTrash2 size={16} /> },
    { key: 'domains', label: t('data_management.tab_domains', { defaultValue: 'Data domains' }), icon: <IconScrollText size={16} />, badge: overview?.domains.length },
    { key: 'operations', label: t('data_management.tab_operations', { defaultValue: 'Operations' }), icon: <IconSettings size={16} />, badge: operations.length || undefined },
  ];

  const cleanupCandidates = overview?.domains.filter((domain) => domain.cleanupSupported) ?? [];
  const lastBackupProtected = Boolean(overview?.lastBackup?.metadata?.encrypted);

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div className={styles.headerIdentity}>
          <span className={styles.headerIcon}><IconShield size={24} /></span>
          <div>
            <div className={styles.headerTitleLine}>
              <h1>{t('data_management.title', { defaultValue: 'Data Management' })}</h1>
              <span className={`${styles.serviceState} ${loadError ? styles.serviceStateError : ''}`}>
                <span aria-hidden="true" />
                {loadError
                  ? t('data_management.state_error', { defaultValue: 'Needs attention' })
                  : t('data_management.state_ready', { defaultValue: 'Ready' })}
              </span>
            </div>
            <p>{t('data_management.subtitle', { defaultValue: 'Manage Pro storage, backup and restore, retention policies, and maintenance operations.' })}</p>
          </div>
        </div>
        <div className={styles.headerActions}>
          <Button variant="ghost" size="sm" onClick={() => void loadCore()} disabled={loading}>
            <IconRefreshCw size={16} />
            {t('common.refresh')}
          </Button>
          <Button variant="primary" size="sm" onClick={() => setActiveView('backups')} disabled={loading}>
            <IconShield size={16} />
            {t('data_management.tab_backups', { defaultValue: 'Backup & restore' })}
          </Button>
        </div>
      </header>

      <ProFeatureTabs
        items={tabs}
        activeKey={activeView}
        ariaLabel={t('data_management.tabs_aria', { defaultValue: 'Data management views' })}
        onChange={(key) => setActiveView(key as DataManagementView)}
      />

      {loading ? (
        <div className={styles.loadingState}><LoadingSpinner size={22} />{t('common.loading')}</div>
      ) : loadError ? (
        <div className={styles.errorState}>
          <IconAlertTriangle size={22} />
          <div><strong>{t('data_management.load_failed', { defaultValue: 'Data management could not be loaded' })}</strong><span>{loadError}</span></div>
          <Button variant="secondary" size="sm" onClick={() => void loadCore()}>{t('common.retry')}</Button>
        </div>
      ) : null}

      {!loading && !loadError && activeView === 'overview' ? (
        <main className={styles.content}>
          <section className={styles.statusGrid}>
            <article>
              <span className={styles.metricIcon}><IconScrollText size={18} /></span>
              <small>{t('data_management.local_storage', { defaultValue: 'Local storage' })}</small>
              <strong>{formatBytes((overview?.dbSizeBytes ?? 0) + (overview?.walSizeBytes ?? 0))}</strong>
              <p>{overview?.dbPath || '—'}</p>
            </article>
            <article>
              <span className={styles.metricIcon}><IconChartColumnIncreasing size={18} /></span>
              <small>{t('data_management.request_data', { defaultValue: 'Request data' })}</small>
              <strong>{formatCount(overview?.events ?? 0)}</strong>
              <p>{t('data_management.latest_write', { defaultValue: 'Latest write: {{value}}', value: formatDateTime(overview?.latestTimestampMs) })}</p>
            </article>
            <article>
              <span className={styles.metricIcon}><IconRefreshCw size={18} /></span>
              <small>{t('data_management.backup_protection', { defaultValue: 'Backup protection' })}</small>
              <strong>{overview?.webdavEnabled ? t('common.enabled') : t('common.disabled')}</strong>
              <p>{t('data_management.last_backup', { defaultValue: 'Last backup: {{value}}', value: formatDateTime(overview?.lastBackup?.finishedAtMs) })}</p>
            </article>
            <article>
              <span className={styles.metricIcon}><IconShield size={18} /></span>
              <small>{t('data_management.data_health', { defaultValue: 'Data health' })}</small>
              <strong>{overview?.deadLetters ? t('data_management.attention_count', { defaultValue: '{{count}} issues', count: overview.deadLetters }) : t('data_management.healthy', { defaultValue: 'Healthy' })}</strong>
              <p>{t('data_management.generation', { defaultValue: 'Dataset generation {{value}}', value: overview?.generation ?? 0 })}</p>
            </article>
          </section>

          <section className={styles.overviewColumns}>
            <article className={styles.panel}>
              <div className={styles.panelHeading}>
                <div><h2>{t('data_management.protection_summary', { defaultValue: 'Protection summary' })}</h2><p>{t('data_management.protection_summary_desc', { defaultValue: 'Backup coverage and sensitive-data handling for this Pro instance.' })}</p></div>
                {overview?.lastBackup?.status === 'success' ? <span className={styles.goodBadge}><IconCheckCircle2 size={14} />{t('common.success')}</span> : null}
              </div>
              <div className={styles.summaryRows}>
                <div><span>{t('data_management.covered_domains', { defaultValue: 'Backup-covered domains' })}</span><strong>{overview?.domains.filter((domain) => domain.backupIncluded).length ?? 0}</strong></div>
                <div><span>{t('data_management.secret_classes', { defaultValue: 'Sensitive classes' })}</span><strong>{overview?.secretClasses.length ?? 0}</strong></div>
                <div><span>{t('data_management.backup_encryption', { defaultValue: 'Last backup encryption' })}</span><strong>{lastBackupProtected ? t('data_management.encrypted', { defaultValue: 'Encrypted' }) : t('data_management.plaintext', { defaultValue: 'Not encrypted' })}</strong></div>
                <div><span>{t('data_management.webdav_status', { defaultValue: 'WebDAV target' })}</span><strong>{overview?.webdavConfigured ? t('data_management.configured', { defaultValue: 'Configured' }) : t('data_management.not_configured', { defaultValue: 'Not configured' })}</strong></div>
              </div>
              {!lastBackupProtected && overview?.lastBackup ? (
                <div className={styles.securityNotice}><IconAlertTriangle size={17} /><span>{t('data_management.plaintext_warning', { defaultValue: 'The current full backup contains sensitive Pro configuration. Use encrypted export when the file leaves a trusted storage boundary.' })}</span></div>
              ) : null}
            </article>

            <article className={styles.panel}>
              <div className={styles.panelHeading}><div><h2>{t('data_management.domain_snapshot', { defaultValue: 'Data-domain snapshot' })}</h2><p>{t('data_management.domain_snapshot_desc', { defaultValue: 'The largest and most recently updated Pro-owned datasets.' })}</p></div></div>
              <div className={styles.domainSnapshot}>
                {(overview?.domains ?? []).slice().sort((left, right) => right.records - left.records).slice(0, 6).map((domain) => (
                  <div key={domain.id}>
                    <span><strong>{t(`data_management.domain_${domain.id}`, { defaultValue: domain.id })}</strong><small>{domain.owner}</small></span>
                    <b>{formatCount(domain.records)}</b>
                  </div>
                ))}
              </div>
            </article>
          </section>
        </main>
      ) : null}

      {!loading && !loadError && activeView === 'backups' ? (
        <main className={styles.content}>
          <section className={styles.actionGrid}>
            <button type="button" onClick={() => void exportBackup()} disabled={backupBusy}>
              <span><IconDownload size={20} /></span><div><strong>{t('data_management.standard_backup', { defaultValue: 'Standard backup' })}</strong><small>{t('data_management.standard_backup_desc', { defaultValue: 'Download a manifest-protected full Pro backup.' })}</small></div>
            </button>
            <button type="button" onClick={() => setEncryptionDialogOpen(true)} disabled={backupBusy}>
              <span><IconShield size={20} /></span><div><strong>{t('data_management.encrypted_backup', { defaultValue: 'Encrypted backup' })}</strong><small>{t('data_management.encrypted_backup_desc', { defaultValue: 'Protect the complete backup with an AES-256-GCM passphrase.' })}</small></div>
            </button>
            <button type="button" onClick={() => restoreInputRef.current?.click()} disabled={restoreBusy}>
              <span><IconRefreshCw size={20} /></span><div><strong>{t('data_management.restore_backup', { defaultValue: 'Restore backup' })}</strong><small>{t('data_management.restore_backup_desc', { defaultValue: 'Preview every data domain before any write occurs.' })}</small></div>
            </button>
            <button type="button" onClick={() => void backupNow()} disabled={backupBusy}>
              <span><IconCheckCircle2 size={20} /></span><div><strong>{t('data_management.backup_now', { defaultValue: 'Back up now' })}</strong><small>{t('data_management.backup_now_desc', { defaultValue: 'Create an immediate backup on the configured WebDAV target.' })}</small></div>
            </button>
          </section>
          <input ref={restoreInputRef} type="file" accept=".jsonl,.ndjson,.json" hidden onChange={handleRestoreFile} />

          <section className={styles.twoColumns}>
            <article className={styles.panel}>
              <div className={styles.panelHeading}>
                <div><h2>{t('data_management.webdav_title', { defaultValue: 'WebDAV backup target' })}</h2><p>{t('data_management.webdav_desc', { defaultValue: 'Schedule off-site backups and verify the target can create and remove files.' })}</p></div>
                <label className={styles.switchField}><input type="checkbox" checked={settingsDraft.webdavEnabled} onChange={(event) => updateSettingsDraft((current) => ({ ...current, webdavEnabled: event.target.checked }))} /><span>{settingsDraft.webdavEnabled ? t('common.enabled') : t('common.disabled')}</span></label>
              </div>
              <div className={styles.settingsGrid}>
                <label className={styles.fieldWide}><span>{t('data_management.webdav_url', { defaultValue: 'Directory URL' })}</span><Input value={settingsDraft.webdavUrl} onChange={(event) => updateSettingsDraft((current) => ({ ...current, webdavUrl: event.target.value }))} placeholder="https://example.com/dav/pro-backups" /></label>
                <label><span>{t('data_management.webdav_username', { defaultValue: 'Username' })}</span><Input value={settingsDraft.webdavUsername} onChange={(event) => updateSettingsDraft((current) => ({ ...current, webdavUsername: event.target.value }))} autoComplete="username" /></label>
                <label><span>{t('data_management.webdav_password', { defaultValue: 'Password' })}</span><div className={styles.passwordField}><Input type={showWebDAVPassword ? 'text' : 'password'} value={settingsDraft.webdavPassword} onChange={(event) => updateSettingsDraft((current) => ({ ...current, webdavPassword: event.target.value }))} autoComplete="current-password" /><button type="button" onClick={() => setShowWebDAVPassword((value) => !value)} aria-label={showWebDAVPassword ? t('common.hide') : t('common.show')}>{showWebDAVPassword ? <IconEyeOff size={16} /> : <IconEye size={16} />}</button></div></label>
                <label><span>{t('data_management.backup_interval', { defaultValue: 'Backup interval (minutes)' })}</span><Input type="number" min="1" value={settingsDraft.webdavIntervalMinutes} onChange={(event) => updateSettingsDraft((current) => ({ ...current, webdavIntervalMinutes: event.target.value }))} /></label>
                <label><span>{t('data_management.backup_retention', { defaultValue: 'Remote retention (days)' })}</span><Input type="number" min="0" value={settingsDraft.webdavRetentionDays} onChange={(event) => updateSettingsDraft((current) => ({ ...current, webdavRetentionDays: event.target.value }))} /></label>
              </div>
              <div className={styles.panelActions}><Button variant="secondary" size="sm" onClick={() => void testWebDAV()} loading={testingWebDAV} disabled={!overview?.webdavConfigured || dirty}>{t('data_management.test_connection', { defaultValue: 'Test connection' })}</Button></div>
            </article>

            <article className={styles.panel}>
              <div className={styles.panelHeading}><div><h2>{t('data_management.backup_history', { defaultValue: 'Backup history' })}</h2><p>{t('data_management.backup_history_desc', { defaultValue: 'Backups currently visible on the configured WebDAV target.' })}</p></div><Button variant="ghost" size="sm" onClick={() => void loadBackupHistory()} disabled={backupHistoryLoading}><IconRefreshCw size={15} /></Button></div>
              {backupHistoryLoading ? <div className={styles.inlineLoading}><LoadingSpinner size={16} />{t('common.loading')}</div> : history.backups.length ? (
                <div className={styles.backupList}>{history.backups.slice(0, 10).map((backup) => <div key={backup.fileName}><span><strong>{backup.fileName}</strong><small>{formatDateTime(backup.lastModifiedMs)}</small></span><span className={styles.backupListActions}><b>{formatBytes(backup.sizeBytes)}</b><Button variant="secondary" size="sm" onClick={() => void previewWebDAVRestore(backup)} disabled={restoreBusy}><IconRefreshCw size={14} />{t('data_management.restore_from_webdav', { defaultValue: 'Restore' })}</Button></span></div>)}</div>
              ) : <div className={styles.emptyState}><IconInfo size={18} />{t('data_management.no_backups', { defaultValue: 'No remote backups found.' })}</div>}
            </article>
          </section>
        </main>
      ) : null}

      {!loading && !loadError && activeView === 'retention' ? (
        <main className={styles.content}>
          <section className={styles.twoColumns}>
            <article className={styles.panel}>
              <div className={styles.panelHeading}><div><h2>{t('data_management.automatic_retention', { defaultValue: 'Automatic request retention' })}</h2><p>{t('data_management.automatic_retention_desc', { defaultValue: 'The scheduled cleanup runs daily using server-local time.' })}</p></div></div>
              <label className={styles.retentionField}><span>{t('data_management.retention_days', { defaultValue: 'Keep request events for' })}</span><div><Input type="number" min="0" value={settingsDraft.retentionDays} onChange={(event) => updateSettingsDraft((current) => ({ ...current, retentionDays: event.target.value }))} /><b>{t('data_management.days', { defaultValue: 'days' })}</b></div><small>{t('data_management.retention_zero', { defaultValue: 'Use 0 to disable automatic request cleanup.' })}</small></label>
            </article>
            <article className={styles.panel}>
              <div className={styles.panelHeading}><div><h2>{t('data_management.cleanup_preview', { defaultValue: 'Cleanup preview' })}</h2><p>{t('data_management.cleanup_preview_desc', { defaultValue: 'Choose supported data domains and preview the exact record count before deletion.' })}</p></div></div>
              <div className={styles.cleanupDomains}>{cleanupCandidates.map((domain) => <label key={domain.id}><input type="checkbox" checked={cleanupDomains.includes(domain.id)} onChange={() => toggleCleanupDomain(domain.id)} /><span>{t(`data_management.domain_${domain.id}`, { defaultValue: domain.id })}</span><b>{formatCount(domain.records)}</b></label>)}</div>
              <label className={styles.retentionField}><span>{t('data_management.delete_older_than', { defaultValue: 'Delete records older than' })}</span><div><Input type="number" min="1" value={cleanupRetentionDays} onChange={(event) => { setCleanupRetentionDays(event.target.value); setCleanupPreview(null); }} /><b>{t('data_management.days', { defaultValue: 'days' })}</b></div></label>
              <div className={styles.panelActions}><Button variant="secondary" size="sm" onClick={() => void previewCleanup()} loading={cleanupBusy} disabled={!cleanupDomains.length}>{t('data_management.preview_cleanup', { defaultValue: 'Preview cleanup' })}</Button>{cleanupPreview ? <Button variant="danger" size="sm" onClick={executeCleanup} disabled={cleanupBusy || cleanupPreview.totalRecords === 0}>{t('data_management.delete_records', { defaultValue: 'Delete {{count}} records', count: formatCount(cleanupPreview.totalRecords) })}</Button> : null}</div>
            </article>
          </section>
          <section className={`${styles.panel} ${styles.dangerPanel}`}>
            <div className={styles.panelHeading}><div><h2>{t('data_management.danger_zone', { defaultValue: 'Danger zone' })}</h2><p>{t('data_management.danger_zone_desc', { defaultValue: 'Reset request events, derived statistics, and account scheduling counters while preserving settings, prices, routing cursors, and backups.' })}</p></div><strong>{formatCount(overview?.events ?? 0)} {t('data_management.events', { defaultValue: 'events' })}</strong></div>
            <Button variant="danger" size="sm" onClick={resetStatistics} disabled={cleanupBusy}><IconTrash2 size={15} />{t('data_management.reset_action', { defaultValue: 'Reset statistics' })}</Button>
          </section>
        </main>
      ) : null}

      {!loading && !loadError && activeView === 'domains' ? (
        <main className={styles.content}>
          <section className={styles.panel}>
            <div className={styles.panelHeading}><div><h2>{t('data_management.domain_inventory', { defaultValue: 'Pro data inventory' })}</h2><p>{t('data_management.domain_inventory_desc', { defaultValue: 'Ownership, backup behavior, sensitivity, and cleanup support for every registered data domain.' })}</p></div></div>
            <div className={styles.domainTableWrapper}><table className={styles.domainTable}><thead><tr><th>{t('data_management.domain', { defaultValue: 'Data domain' })}</th><th>{t('data_management.owner', { defaultValue: 'Owner' })}</th><th>{t('data_management.records', { defaultValue: 'Records' })}</th><th>{t('data_management.backup', { defaultValue: 'Backup' })}</th><th>{t('data_management.restore_behavior', { defaultValue: 'Restore behavior' })}</th><th>{t('data_management.sensitivity', { defaultValue: 'Sensitivity' })}</th><th>{t('data_management.updated', { defaultValue: 'Updated' })}</th></tr></thead><tbody>{(overview?.domains ?? []).map((domain: DataDomainInventory) => <tr key={domain.id}><td><strong>{t(`data_management.domain_${domain.id}`, { defaultValue: domain.id })}</strong><small>v{domain.schemaVersion}</small></td><td>{domain.owner}</td><td>{domain.available ? formatCount(domain.records) : '—'}</td><td><span className={domain.backupIncluded ? styles.goodBadge : styles.neutralBadge}>{domain.backupIncluded ? <IconCheck size={13} /> : <IconX size={13} />}{domain.backupIncluded ? t('common.yes') : t('common.no')}</span></td><td>{t(`data_management.restore_${domain.restoreMode}`, { defaultValue: domain.restoreMode })}</td><td><span className={`${styles.sensitivityBadge} ${styles[`sensitivity_${domain.sensitivity}`]}`}>{t(`data_management.sensitivity_${domain.sensitivity}`, { defaultValue: domain.sensitivity })}</span>{(domain.secretClasses ?? []).length ? <small className={styles.secretList}>{(domain.secretClasses ?? []).join(', ')}</small> : null}</td><td>{formatDateTime(domain.updatedAtMs)}</td></tr>)}</tbody></table></div>
          </section>
          <div className={styles.boundaryNotice}><IconShield size={18} /><div><strong>{t('data_management.external_boundary_title', { defaultValue: 'Upstream credential boundary' })}</strong><span>{t('data_management.external_boundary_desc', { defaultValue: 'config.yaml API keys and source auth files are not owned or restored by Pro data management.' })}</span></div></div>
        </main>
      ) : null}

      {!loading && !loadError && activeView === 'operations' ? (
        <main className={styles.content}>
          <section className={styles.panel}>
            <div className={styles.panelHeading}><div><h2>{t('data_management.operation_history', { defaultValue: 'Operation history' })}</h2><p>{t('data_management.operation_history_desc', { defaultValue: 'Persistent backup, restore, cleanup, and export results survive service restarts.' })}</p></div></div>
            {operations.length ? <div className={styles.operationList}>{operations.map((operation) => <article key={operation.id}><span className={`${styles.operationIcon} ${operationTone(operation)}`}>{operation.status === 'success' ? <IconCheckCircle2 size={17} /> : operation.status === 'failed' ? <IconAlertTriangle size={17} /> : <LoadingSpinner size={16} />}</span><div><strong>{t(`data_management.operation_${operation.kind}`, { defaultValue: operation.kind })}</strong><small>{operation.fileName || operation.target || '—'}</small>{operation.message ? <p>{operation.message}</p> : null}</div><span><b>{formatDateTime(operation.finishedAtMs || operation.startedAtMs)}</b><small>{operation.sizeBytes ? formatBytes(operation.sizeBytes) : operation.affectedRecords ? t('data_management.affected_records', { defaultValue: '{{count}} records', count: formatCount(operation.affectedRecords) }) : '—'}</small></span></article>)}</div> : <div className={styles.emptyState}><IconInfo size={18} />{t('data_management.no_operations', { defaultValue: 'No data-management operations have been recorded yet.' })}</div>}
          </section>
        </main>
      ) : null}

      {dirty && isCurrentLayer ? (
        <div className={configStyles.floatingActionContainer}>
          <div className={configStyles.floatingActionList}>
            <span className={`${configStyles.floatingStatus} ${configStyles.modified}`}>{t('common.unsaved_changes_title')}</span>
            <button type="button" className={configStyles.floatingActionButton} onClick={() => updateSettingsDraft(() => savedSettingsDraft)} disabled={saving} aria-label={t('common.reset')}><IconRefreshCw size={17} /></button>
            <button type="button" className={configStyles.floatingActionButton} onClick={() => void saveSettings()} disabled={saving} aria-label={t('common.save')}>{saving ? <LoadingSpinner size={16} /> : <IconCheck size={17} />} {!saving ? <span className={configStyles.dirtyDot} aria-hidden="true" /> : null}</button>
          </div>
        </div>
      ) : null}

      <ProTaskDialog open={encryptionDialogOpen} title={t('data_management.encrypted_backup', { defaultValue: 'Encrypted backup' })} onClose={() => setEncryptionDialogOpen(false)} footer={<><Button variant="secondary" onClick={() => setEncryptionDialogOpen(false)}>{t('common.cancel')}</Button><Button variant="primary" onClick={() => void confirmEncryptedExport()} disabled={backupBusy}>{t('data_management.download_encrypted', { defaultValue: 'Download encrypted backup' })}</Button></>}>
        <div className={styles.dialogBody}><div className={styles.securityNotice}><IconShield size={17} /><span>{t('data_management.encryption_notice', { defaultValue: 'The passphrase is never stored. Losing it makes the backup unrecoverable.' })}</span></div><label><span>{t('data_management.passphrase', { defaultValue: 'Passphrase' })}</span><Input type="password" value={encryptionPassphrase} onChange={(event) => setEncryptionPassphrase(event.target.value)} autoComplete="new-password" /></label><label><span>{t('data_management.passphrase_confirm', { defaultValue: 'Confirm passphrase' })}</span><Input type="password" value={encryptionPassphraseConfirm} onChange={(event) => setEncryptionPassphraseConfirm(event.target.value)} autoComplete="new-password" /></label></div>
      </ProTaskDialog>

      <ProTaskDialog open={restorePassphraseDialogOpen} title={t('data_management.unlock_backup', { defaultValue: 'Unlock encrypted backup' })} onClose={() => setRestorePassphraseDialogOpen(false)} footer={<><Button variant="secondary" onClick={() => setRestorePassphraseDialogOpen(false)}>{t('common.cancel')}</Button><Button variant="primary" onClick={() => void previewEncryptedRestore()} disabled={!restorePassphrase || restoreBusy}>{restoreBusy ? t('common.loading') : t('data_management.unlock_and_preview', { defaultValue: 'Unlock and preview' })}</Button></>}>
        <div className={styles.dialogBody}><p>{restoreFileName}</p><label><span>{t('data_management.passphrase', { defaultValue: 'Passphrase' })}</span><Input type="password" value={restorePassphrase} onChange={(event) => setRestorePassphrase(event.target.value)} autoComplete="current-password" /></label></div>
      </ProTaskDialog>

      <ProWorkspaceSheet open={restorePreviewOpen} onClose={() => !restoreBusy && setRestorePreviewOpen(false)} title={t('data_management.restore_preview_title', { defaultValue: 'Review backup restore' })} description={restoreFileName} closeDisabled={restoreBusy} footer={<div className={styles.sheetFooter}><Button variant="secondary" onClick={() => setRestorePreviewOpen(false)} disabled={restoreBusy}>{t('common.cancel')}</Button><Button variant={restorePreview?.legacyBackup || hasKeyStateRestoreChanges(restorePreview?.policyBackup) ? 'danger' : 'primary'} onClick={() => void executeRestore()} loading={restoreBusy}>{hasKeyStateRestoreChanges(restorePreview?.policyBackup) ? t('data_management.restore_confirm_key_states') : t('data_management.restore_confirm', { defaultValue: 'Restore backup' })}</Button></div>}>
        <div className={styles.restorePreview}>
          <div className={restorePreview?.integrityProtected ? styles.integrityGood : styles.integrityWarning}>{restorePreview?.integrityProtected ? <IconCheckCircle2 size={17} /> : <IconAlertTriangle size={17} />}<span>{restorePreview?.integrityProtected ? t('data_management.integrity_verified', { defaultValue: 'Backup manifest and content hash verified' }) : t('data_management.legacy_warning', { defaultValue: 'Legacy backup without an integrity manifest. Continue only if the source is trusted.' })}</span></div>
          {restorePreview?.encrypted ? <div className={styles.integrityGood}><IconShield size={17} /><span>{t('data_management.encrypted_verified', { defaultValue: 'AES-256-GCM authentication succeeded' })}</span></div> : null}
          {restorePreview?.policyBackup ? (
            <div className={styles.boundaryNotice}>
              <IconShield size={17} />
              <div>
                <strong>{t('data_management.policy_restore_title', { defaultValue: 'API Key policy restore' })}</strong>
                <span>{restorePreview.policyBackup.hasPolicies
                  ? t('data_management.policy_restore_replace', {
                    defaultValue: 'Replace with {{policies}} policies and {{profiles}} profiles; {{associated}} associated and {{orphaned}} orphaned. API keys remain in config.yaml.',
                    policies: formatCount(restorePreview.policyBackup.targetPolicies),
                    profiles: formatCount(restorePreview.policyBackup.targetProfiles),
                    associated: formatCount(restorePreview.policyBackup.associatedPolicies),
                    orphaned: formatCount(restorePreview.policyBackup.orphanedPolicies),
                  })
                  : t('data_management.policy_restore_preserve', {
                    defaultValue: 'Preserve {{policies}} current policies and {{profiles}} profiles. API keys remain in config.yaml.',
                    policies: formatCount(restorePreview.policyBackup.preservePolicies),
                    profiles: formatCount(restorePreview.policyBackup.preserveProfiles),
                  })}</span>
                {typeof restorePreview.policyBackup.currentDisabledKeys === 'number' && typeof restorePreview.policyBackup.targetDisabledKeys === 'number' ? (
                  <div role="note">
                    <p>{t('data_management.restore_disabled_key_settings', {
                      current: formatCount(restorePreview.policyBackup.currentDisabledKeys),
                      target: formatCount(restorePreview.policyBackup.targetDisabledKeys),
                      added: formatCount(restorePreview.policyBackup.addedDisabledKeys ?? 0),
                      removed: formatCount(restorePreview.policyBackup.removedDisabledKeys ?? 0),
                    })}</p>
                    <p>{t('data_management.restore_disabled_key_effects', {
                      blocked: formatCount(restorePreview.policyBackup.newlyBlockedKeys ?? 0),
                      allowed: formatCount(restorePreview.policyBackup.newlyAllowedKeys ?? 0),
                    })}</p>
                  </div>
                ) : null}
                {typeof restorePreview.policyBackup.currentConcurrencyKeys === 'number' ? (
                  <p>{t('data_management.restore_concurrency_limits', {
                    current: formatCount(restorePreview.policyBackup.currentConcurrencyKeys),
                    target: formatCount(restorePreview.policyBackup.targetConcurrencyKeys ?? 0),
                    changed: formatCount(restorePreview.policyBackup.changedConcurrencyKeys ?? 0),
                    effective: formatCount(restorePreview.policyBackup.effectiveConcurrencyChanges ?? 0),
                  })}</p>
                ) : null}
                {restorePreview.policyBackup.currentTakeoverEnabled !== restorePreview.policyBackup.targetTakeoverEnabled ? (
                  <small>{t('data_management.policy_takeover_change', {
                    defaultValue: 'Takeover changes from {{current}} to {{target}}.',
                    current: restorePreview.policyBackup.currentTakeoverEnabled ? t('common.enabled') : t('common.disabled'),
                    target: restorePreview.policyBackup.targetTakeoverEnabled ? t('common.enabled') : t('common.disabled'),
                  })}</small>
                ) : null}
              </div>
            </div>
          ) : null}
          <div className={styles.restoreDomainList}>{restorePreview?.domains.map((domain) => <div key={domain.id}><span><strong>{t(`data_management.domain_${domain.id}`, { defaultValue: domain.id })}</strong><small>{domain.owner}</small></span><span><small>{t('data_management.current_to_backup', { defaultValue: '{{current}} current → {{backup}} backup', current: formatCount(domain.currentRecords), backup: formatCount(domain.backupRecords) })}</small><b>{t(`data_management.restore_${domain.action}`, { defaultValue: domain.action })}</b></span></div>)}</div>
          <div className={styles.boundaryNotice}><IconInfo size={17} /><span>{t('data_management.restore_no_api_keys', { defaultValue: 'config.yaml API keys are not included and will be preserved.' })}</span></div>
        </div>
      </ProWorkspaceSheet>
    </div>
  );
}
