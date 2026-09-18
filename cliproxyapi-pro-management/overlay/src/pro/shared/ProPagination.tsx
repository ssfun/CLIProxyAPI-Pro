import { type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import {
  DEFAULT_PRO_PAGE_SIZE,
  PRO_PAGE_SIZE_OPTIONS,
  normalizeProPageSize,
  resolveProPaginationCopy,
  type ProPageSize,
} from './pagination';
import styles from './ProPagination.module.scss';

export interface ProPaginationProps {
  page: number;
  pageSize: ProPageSize;
  total: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: ProPageSize) => void;
  pageSizeOptions?: ReadonlyArray<ProPageSize>;
  className?: string;
  idPrefix?: string;
  renderInfo?: (params: { page: number; totalPages: number; total: number; from: number; to: number }) => ReactNode;
}

export function ProPagination({
  page,
  pageSize = DEFAULT_PRO_PAGE_SIZE,
  total,
  onPageChange,
  onPageSizeChange,
  pageSizeOptions = PRO_PAGE_SIZE_OPTIONS,
  className = '',
  idPrefix = 'pro-pagination',
  renderInfo,
}: ProPaginationProps) {
  const { t, i18n } = useTranslation();
  const paginationCopy = resolveProPaginationCopy(i18n.resolvedLanguage ?? i18n.language);
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const from = total > 0 ? (page - 1) * pageSize + 1 : 0;
  const to = Math.min(total, page * pageSize);

  const infoContent = renderInfo
    ? renderInfo({ page, totalPages, total, from, to })
    : t('monitoring.pagination_info', {
        from,
        to,
        total,
        page,
        totalPages,
        current: page,
        count: total,
        defaultValue: `${page} / ${totalPages} (${total})`,
      });

  return (
    <div className={`${styles.paginationBar} ${className}`.trim()}>
      <div className={styles.pageSizeControl}>
        <span id={`${idPrefix}-size-label`}>{paginationCopy.pageSizeLabel}</span>
        <Select
          id={`${idPrefix}-size`}
          value={String(pageSize)}
          options={pageSizeOptions.map((size) => ({
            value: String(size),
            label: paginationCopy.pageSizeValue(size),
          }))}
          onChange={(value) => {
            const nextSize = normalizeProPageSize(value);
            onPageSizeChange(nextSize);
          }}
          ariaLabelledBy={`${idPrefix}-size-label`}
          triggerClassName={styles.pageSizeSelectTrigger}
          size="sm"
        />
      </div>

      <div className={styles.navigation}>
        <span className={styles.pageInfo}>{infoContent}</span>
        <Button
          size="sm"
          variant="secondary"
          onClick={() => onPageChange(Math.max(1, page - 1))}
          disabled={page <= 1}
          aria-label={t('common.previous_page', { defaultValue: 'Previous page' })}
        >
          &lt;
        </Button>
        <Button
          size="sm"
          variant="secondary"
          onClick={() => onPageChange(Math.min(totalPages, page + 1))}
          disabled={page >= totalPages}
          aria-label={t('common.next_page', { defaultValue: 'Next page' })}
        >
          &gt;
        </Button>
      </div>
    </div>
  );
}
