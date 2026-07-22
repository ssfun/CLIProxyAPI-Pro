export type MonitoringSettings = {
  retentionDays: number;
  webdav: {
    enabled: boolean;
    intervalMinutes: number;
    retentionDays: number;
    url: string;
    username: string;
    password: string;
  };
  modelPriceSync: {
    enabled: boolean;
    intervalMinutes: number;
  };
};

export type MonitoringSettingsDraft = {
  retentionDays: string;
  webdavEnabled: boolean;
  webdavIntervalMinutes: string;
  webdavRetentionDays: string;
  webdavUrl: string;
  webdavUsername: string;
  webdavPassword: string;
  modelPriceSyncEnabled: boolean;
  modelPriceSyncIntervalMinutes: string;
};

export const createMonitoringSettingsDraft = (
  settings?: MonitoringSettings
): MonitoringSettingsDraft => ({
  retentionDays: String(settings?.retentionDays ?? 0),
  webdavEnabled: settings?.webdav.enabled ?? false,
  webdavIntervalMinutes: String(settings?.webdav.intervalMinutes ?? 1440),
  webdavRetentionDays: String(settings?.webdav.retentionDays ?? 0),
  webdavUrl: settings?.webdav.url ?? '',
  webdavUsername: settings?.webdav.username ?? '',
  webdavPassword: settings?.webdav.password ?? '',
  modelPriceSyncEnabled: settings?.modelPriceSync?.enabled ?? false,
  modelPriceSyncIntervalMinutes: String(settings?.modelPriceSync?.intervalMinutes ?? 1440),
});

const parseNonNegativeInteger = (value: string) => {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

const parsePositiveInteger = (value: string, fallback: number) => {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
};

export const buildMonitoringSettingsFromDraft = (
  draft: MonitoringSettingsDraft
): MonitoringSettings => ({
  retentionDays: parseNonNegativeInteger(draft.retentionDays),
  webdav: {
    enabled: draft.webdavEnabled,
    intervalMinutes: parsePositiveInteger(draft.webdavIntervalMinutes, 1440),
    retentionDays: parseNonNegativeInteger(draft.webdavRetentionDays),
    url: draft.webdavUrl.trim(),
    username: draft.webdavUsername.trim(),
    password: draft.webdavPassword,
  },
  modelPriceSync: {
    enabled: draft.modelPriceSyncEnabled,
    intervalMinutes: parsePositiveInteger(draft.modelPriceSyncIntervalMinutes, 1440),
  },
});
