import { describe, expect, test } from 'bun:test';
import {
  buildMonitoringSettingsFromDraft,
  createMonitoringSettingsDraft,
} from '../src/features/monitoring/monitoringSettings';

describe('monitoring settings form model', () => {
  test('uses stable defaults for a new settings form', () => {
    expect(createMonitoringSettingsDraft()).toMatchObject({
      retentionDays: '0',
      webdavEnabled: false,
      webdavIntervalMinutes: '1440',
      modelPriceSyncEnabled: false,
      modelPriceSyncIntervalMinutes: '1440',
    });
  });

  test('normalizes numeric fields and trims connection identity fields', () => {
    const settings = buildMonitoringSettingsFromDraft({
      retentionDays: '-1',
      webdavEnabled: true,
      webdavIntervalMinutes: '0',
      webdavRetentionDays: '7',
      webdavUrl: ' https://example.com/dav ',
      webdavUsername: ' owner ',
      webdavPassword: ' preserve spaces ',
      modelPriceSyncEnabled: true,
      modelPriceSyncIntervalMinutes: '60',
    });

    expect(settings.retentionDays).toBe(0);
    expect(settings.webdav.intervalMinutes).toBe(1440);
    expect(settings.webdav.url).toBe('https://example.com/dav');
    expect(settings.webdav.username).toBe('owner');
    expect(settings.webdav.password).toBe(' preserve spaces ');
    expect(settings.modelPriceSync.intervalMinutes).toBe(60);
  });
});
