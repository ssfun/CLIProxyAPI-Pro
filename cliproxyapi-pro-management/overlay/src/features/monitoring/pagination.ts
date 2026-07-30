export const MONITORING_PAGE_SIZE_OPTIONS = [20, 50, 100] as const;

export type MonitoringPageSize = (typeof MONITORING_PAGE_SIZE_OPTIONS)[number];

export const DEFAULT_MONITORING_PAGE_SIZE: MonitoringPageSize = 20;

type MonitoringPaginationCopy = {
  pageSizeLabel: string;
  pageSizeValue: (pageSize: MonitoringPageSize) => string;
};

export const resolveMonitoringPaginationCopy = (language: string): MonitoringPaginationCopy => {
  const normalized = language.trim().toLowerCase();
  if (normalized.startsWith('zh-tw') || normalized.startsWith('zh-hk')) {
    return {
      pageSizeLabel: '每頁筆數',
      pageSizeValue: (pageSize) => `${pageSize} 筆/頁`,
    };
  }
  if (normalized.startsWith('zh')) {
    return {
      pageSizeLabel: '每页条数',
      pageSizeValue: (pageSize) => `${pageSize} 条/页`,
    };
  }
  if (normalized.startsWith('ru')) {
    return {
      pageSizeLabel: 'Строк на странице',
      pageSizeValue: (pageSize) => `${pageSize} / стр.`,
    };
  }
  return {
    pageSizeLabel: 'Rows per page',
    pageSizeValue: (pageSize) => `${pageSize} / page`,
  };
};

export const normalizeMonitoringPageSize = (value: string | number): MonitoringPageSize => {
  const parsed = typeof value === 'number' ? value : Number.parseInt(value, 10);
  return MONITORING_PAGE_SIZE_OPTIONS.find((pageSize) => pageSize === parsed)
    ?? DEFAULT_MONITORING_PAGE_SIZE;
};
