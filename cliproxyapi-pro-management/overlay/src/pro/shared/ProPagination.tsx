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
  disabled?: boolean;
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
  disabled = false,
}: ProPaginationProps) {
  const { t, i18n } = useTranslation();
  const paginationCopy = resolveProPaginationCopy(i18n.resolvedLanguage ?? i18n.language);
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const normalizedPage = Math.min(Math.max(1, page), totalPages);
  const from = total > 0 ? (normalizedPage - 1) * pageSize + 1 : 0;
  const to = Math.min(total, normalizedPage * pageSize);
  const hasPrevious = normalizedPage > 1;
  const hasNext = normalizedPage < totalPages;

  if (total <= 0) return null;

  return (
    <div className={`${styles.paginationBar} ${className}`.trim()}>
      <div className={styles.paginationPageSizeControl}>
        <span id={`${idPrefix}-page-size-label`}>
          {t('monitoring.pagination_page_size', { defaultValue: paginationCopy.pageSizeLabel })}
        </span>
        <Select
          id={`${idPrefix}-page-size`}
          value={String(pageSize)}
          options={pageSizeOptions.map((size) => ({
            value: String(size),
            label: t('monitoring.pagination_page_size_value', {
              count: size,
              defaultValue: paginationCopy.pageSizeValue(size),
            }),
          }))}
          onChange={(value) => {
            const nextPageSize = normalizeProPageSize(value);
            if (nextPageSize === pageSize) return;
            onPageSizeChange(nextPageSize);
          }}
          ariaLabelledBy={`${idPrefix}-page-size-label`}
          className={styles.paginationPageSizeSelect}
          disabled={disabled}
        />
      </div>

      {totalPages > 1 ? (
        <div className={styles.paginationNavigation}>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => onPageChange(Math.max(1, normalizedPage - 1))}
            disabled={disabled || !hasPrevious}
            aria-label={t('monitoring.previous_page')}
          >
            {t('monitoring.previous_page')}
          </Button>
          <div className={styles.pageInfo}>
            {t('monitoring.pagination_info', {
              from,
              to,
              total,
              page: normalizedPage,
              totalPages,
              defaultValue: `${from}-${to} / ${total}`,
            })}
          </div>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => onPageChange(Math.min(totalPages, normalizedPage + 1))}
            disabled={disabled || !hasNext}
            aria-label={t('monitoring.next_page')}
          >
            {t('monitoring.next_page')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
