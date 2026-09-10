export const PRO_PAGE_SIZE_OPTIONS = [20, 50, 100, 500] as const;

export type ProPageSize = (typeof PRO_PAGE_SIZE_OPTIONS)[number];

export const DEFAULT_PRO_PAGE_SIZE: ProPageSize = 20;

type ProPaginationCopy = {
  pageSizeLabel: string;
  pageSizeValue: (pageSize: ProPageSize) => string;
};

export const resolveProPaginationCopy = (language: string): ProPaginationCopy => {
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

export const normalizeProPageSize = (value: string | number): ProPageSize => {
  const parsed = typeof value === 'number' ? value : Number.parseInt(value, 10);
  return PRO_PAGE_SIZE_OPTIONS.find((pageSize) => pageSize === parsed)
    ?? DEFAULT_PRO_PAGE_SIZE;
};
