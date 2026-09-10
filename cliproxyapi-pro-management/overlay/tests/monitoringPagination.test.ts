import { describe, expect, test } from 'bun:test';
import {
  DEFAULT_PRO_PAGE_SIZE,
  PRO_PAGE_SIZE_OPTIONS,
  normalizeProPageSize,
  resolveProPaginationCopy,
} from '../src/pro/shared/pagination';

describe('monitoring pagination', () => {
  test('defaults to 20 rows and exposes only the supported sizes', () => {
    expect(DEFAULT_PRO_PAGE_SIZE).toBe(20);
    expect(PRO_PAGE_SIZE_OPTIONS).toEqual([20, 50, 100, 500]);
  });

  test('normalizes select values to a supported page size', () => {
    expect(normalizeProPageSize('50')).toBe(50);
    expect(normalizeProPageSize(100)).toBe(100);
    expect(normalizeProPageSize('500')).toBe(500);
    expect(normalizeProPageSize('25')).toBe(DEFAULT_PRO_PAGE_SIZE);
  });

  test('provides localized fallback copy when runtime locale keys are unavailable', () => {
    expect(resolveProPaginationCopy('zh-CN').pageSizeLabel).toBe('每页条数');
    expect(resolveProPaginationCopy('zh-CN').pageSizeValue(20)).toBe('20 条/页');
    expect(resolveProPaginationCopy('zh-CN').pageSizeValue(500)).toBe('500 条/页');
    expect(resolveProPaginationCopy('zh-TW').pageSizeValue(50)).toBe('50 筆/頁');
    expect(resolveProPaginationCopy('en-US').pageSizeValue(100)).toBe('100 / page');
  });

  test('keeps expanded inspection result pages inside a scrollable viewport', async () => {
    const styles = await Bun.file(
      new URL('../src/pro/modules/inspection/features/account-inspection-styles/_tables-dialogs.scss', import.meta.url)
    ).text();

    expect(styles).toContain('.resultsTableViewport');
    expect(styles).toContain('max-height: min(620px, 68vh)');
    expect(styles).toContain('overflow-y: auto');
    expect(styles).toContain('scrollbar-gutter: stable');
  });

  test('keeps page navigation and page-size controls on the same row', async () => {
    const baseStyles = await Bun.file(
      new URL('../src/pro/modules/monitoring/features/styles/_base.scss', import.meta.url)
    ).text();
    const responsiveStyles = await Bun.file(
      new URL('../src/pro/modules/monitoring/features/styles/_responsive.scss', import.meta.url)
    ).text();

    expect(baseStyles).toMatch(/\.paginationPageSizeControl\s*\{[^}]*grid-row: 1;/s);
    expect(baseStyles).toMatch(/\.paginationNavigation\s*\{[^}]*grid-row: 1;/s);
    expect(responsiveStyles).toContain('flex-wrap: nowrap');
    expect(responsiveStyles).not.toContain('grid-row: 2');
  });
});
