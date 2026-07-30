import { describe, expect, test } from 'bun:test';
import {
  DEFAULT_MONITORING_PAGE_SIZE,
  MONITORING_PAGE_SIZE_OPTIONS,
  normalizeMonitoringPageSize,
  resolveMonitoringPaginationCopy,
} from '../src/features/monitoring/pagination';

describe('monitoring pagination', () => {
  test('defaults to 20 rows and exposes only the supported sizes', () => {
    expect(DEFAULT_MONITORING_PAGE_SIZE).toBe(20);
    expect(MONITORING_PAGE_SIZE_OPTIONS).toEqual([20, 50, 100]);
  });

  test('normalizes select values to a supported page size', () => {
    expect(normalizeMonitoringPageSize('50')).toBe(50);
    expect(normalizeMonitoringPageSize(100)).toBe(100);
    expect(normalizeMonitoringPageSize('25')).toBe(DEFAULT_MONITORING_PAGE_SIZE);
  });

  test('provides localized fallback copy when runtime locale keys are unavailable', () => {
    expect(resolveMonitoringPaginationCopy('zh-CN').pageSizeLabel).toBe('每页条数');
    expect(resolveMonitoringPaginationCopy('zh-CN').pageSizeValue(20)).toBe('20 条/页');
    expect(resolveMonitoringPaginationCopy('zh-TW').pageSizeValue(50)).toBe('50 筆/頁');
    expect(resolveMonitoringPaginationCopy('en-US').pageSizeValue(100)).toBe('100 / page');
  });

  test('keeps expanded inspection result pages inside a scrollable viewport', async () => {
    const styles = await Bun.file(
      new URL('../src/features/monitoring/account-inspection-styles/_tables-dialogs.scss', import.meta.url)
    ).text();

    expect(styles).toContain('.resultsTableViewport');
    expect(styles).toContain('max-height: min(620px, 68vh)');
    expect(styles).toContain('overflow-y: auto');
    expect(styles).toContain('scrollbar-gutter: stable');
  });

  test('keeps page navigation and page-size controls on the same row', async () => {
    const baseStyles = await Bun.file(
      new URL('../src/features/monitoring/styles/_base.scss', import.meta.url)
    ).text();
    const responsiveStyles = await Bun.file(
      new URL('../src/features/monitoring/styles/_responsive.scss', import.meta.url)
    ).text();

    expect(baseStyles).toMatch(/\.paginationPageSizeControl\s*\{[^}]*grid-row: 1;/s);
    expect(baseStyles).toMatch(/\.paginationNavigation\s*\{[^}]*grid-row: 1;/s);
    expect(responsiveStyles).toContain('flex-wrap: nowrap');
    expect(responsiveStyles).not.toContain('grid-row: 2');
  });
});
