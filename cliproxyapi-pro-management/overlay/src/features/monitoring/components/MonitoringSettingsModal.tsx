import type { Dispatch, SetStateAction } from 'react';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { IconTrash2 } from '@/components/ui/icons';
import { formatCompactNumber } from '@/utils/usage';
import type { MonitoringSettingsDraft } from '../monitoringSettings';
import styles from '../monitoring.module.scss';

export function MonitoringSettingsModal({
  isMonitoringSettingsOpen,
  setIsMonitoringSettingsOpen,
  monitoringSettingsDraft,
  setMonitoringSettingsDraft,
  usageTotalRequests,
  isMonitoringStatisticsResetting,
  isMonitoringSettingsSaving,
  handleMonitoringStatisticsReset,
  handleSaveMonitoringSettings,
  t,
}: {
  isMonitoringSettingsOpen: boolean;
  setIsMonitoringSettingsOpen: Dispatch<SetStateAction<boolean>>;
  monitoringSettingsDraft: MonitoringSettingsDraft;
  setMonitoringSettingsDraft: Dispatch<SetStateAction<MonitoringSettingsDraft>>;
  usageTotalRequests: number;
  isMonitoringStatisticsResetting: boolean;
  isMonitoringSettingsSaving: boolean;
  handleMonitoringStatisticsReset: () => void;
  handleSaveMonitoringSettings: () => void | Promise<void>;
  t: TFunction;
}) {
  return (
      <Modal
        open={isMonitoringSettingsOpen}
        onClose={() => {
          if (!isMonitoringStatisticsResetting) setIsMonitoringSettingsOpen(false);
        }}
        title={t('usage_stats.monitoring_settings')}
        width={760}
        className={styles.monitorModal}
      >
        <div className={styles.monitoringSettingsEditor}>
          <div className={styles.settingsSectionCard}>
            <div className={styles.settingsSectionHeader}>
              <strong>{t('usage_stats.monitoring_settings_retention_title')}</strong>
              <span>{t('usage_stats.monitoring_settings_retention_desc')}</span>
            </div>
            <label className={styles.settingsField}>
              <span>{t('usage_stats.monitoring_settings_retention_days')}</span>
              <Input
                type="number"
                min="0"
                step="1"
                value={monitoringSettingsDraft.retentionDays}
                onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, retentionDays: event.target.value }))}
                placeholder="0"
              />
              <small>{t('usage_stats.monitoring_settings_retention_hint')}</small>
              <div className={styles.settingsScheduleNote}>{t('usage_stats.monitoring_settings_retention_schedule')}</div>
            </label>
          </div>

          <div className={styles.settingsSectionCard}>
            <div className={styles.settingsSectionHeader}>
              <strong>{t('usage_stats.monitoring_settings_webdav_title')}</strong>
              <span>{t('usage_stats.monitoring_settings_webdav_desc')}</span>
            </div>
            <label className={styles.settingsCheckboxField}>
              <input
                type="checkbox"
                checked={monitoringSettingsDraft.webdavEnabled}
                onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavEnabled: event.target.checked }))}
              />
              <span>{t('usage_stats.monitoring_settings_webdav_enabled')}</span>
            </label>
            <div className={styles.settingsGrid}>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_interval')}</span>
                <Input
                  type="number"
                  min="1"
                  step="1"
                  value={monitoringSettingsDraft.webdavIntervalMinutes}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavIntervalMinutes: event.target.value }))}
                  placeholder="1440"
                />
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_retention_days')}</span>
                <Input
                  type="number"
                  min="0"
                  step="1"
                  value={monitoringSettingsDraft.webdavRetentionDays}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavRetentionDays: event.target.value }))}
                  placeholder="0"
                />
                <small>{t('usage_stats.monitoring_settings_webdav_retention_hint')}</small>
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_url')}</span>
                <Input
                  value={monitoringSettingsDraft.webdavUrl}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavUrl: event.target.value }))}
                  placeholder="https://example.com/dav/path"
                />
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_username')}</span>
                <Input
                  value={monitoringSettingsDraft.webdavUsername}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavUsername: event.target.value }))}
                  autoComplete="username"
                />
              </label>
              <label className={styles.settingsField}>
                <span>{t('usage_stats.monitoring_settings_webdav_password')}</span>
                <Input
                  type="password"
                  value={monitoringSettingsDraft.webdavPassword}
                  onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, webdavPassword: event.target.value }))}
                  autoComplete="current-password"
                />
              </label>
            </div>
            <small className={styles.settingsHint}>{t('usage_stats.monitoring_settings_webdav_hint')}</small>
          </div>

          <div className={`${styles.settingsSectionCard} ${styles.settingsDangerSection}`}>
            <div className={styles.settingsSectionHeader}>
              <strong>{t('usage_stats.monitoring_settings_data_title')}</strong>
              <span>{t('usage_stats.monitoring_settings_data_desc')}</span>
            </div>
            <div className={styles.settingsDangerAction}>
              <div>
                <span>{t('usage_stats.monitoring_settings_data_count')}</span>
                <strong>{formatCompactNumber(usageTotalRequests)}</strong>
              </div>
              <Button
                variant="danger"
                size="sm"
                className={styles.resetStatisticsButton}
                onClick={handleMonitoringStatisticsReset}
                disabled={isMonitoringStatisticsResetting || isMonitoringSettingsSaving}
              >
                <IconTrash2 size={15} />
                {isMonitoringStatisticsResetting
                  ? t('usage_stats.monitoring_settings_resetting')
                  : t('usage_stats.monitoring_settings_reset_button')}
              </Button>
            </div>
          </div>

          <div className={styles.priceActionsBar}>
            <Button variant="secondary" size="sm" onClick={() => setIsMonitoringSettingsOpen(false)} disabled={isMonitoringStatisticsResetting}>
              {t('common.cancel')}
            </Button>
            <Button variant="primary" size="sm" onClick={() => void handleSaveMonitoringSettings()} disabled={isMonitoringSettingsSaving || isMonitoringStatisticsResetting}>
              {isMonitoringSettingsSaving ? t('common.loading') : t('common.save')}
            </Button>
          </div>
        </div>
      </Modal>
  );
}
