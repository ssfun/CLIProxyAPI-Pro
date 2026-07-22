import type { ReactNode } from 'react';
import type { TFunction } from 'i18next';
import type { MonitoringStatusTone } from '../hooks/useMonitoringData';
import {
  buildRealtimeStatusLabel,
  compactRealtimeErrorMessage,
  translateRealtimeErrorCategory,
  translateRealtimeErrorText,
  type RealtimeLogRow,
} from '../realtimeLogPresentation';
import { maskSensitiveText } from '@/utils/format';
import styles from '../monitoring.module.scss';

export function StatusBadge({ tone, children }: { tone: MonitoringStatusTone; children: ReactNode }) {
  return <span className={`${styles.statusBadge} ${styles[`tone${tone}`]}`}>{children}</span>;
}

export function RealtimeErrorDetailsPanel({
  row,
  t,
  language,
}: {
  row: RealtimeLogRow;
  t: TFunction;
  language?: string;
}) {
  const categoryText = translateRealtimeErrorCategory(row.errorCategoryKey, t, language);
  const statusText = buildRealtimeStatusLabel(row, t('monitoring.result_failed'));
  const summaryText = row.errorMessage
    ? compactRealtimeErrorMessage(row.errorMessage, 220)
    : row.errorSummary || row.diagnosticText || categoryText;
  const detailItems = [
    { label: translateRealtimeErrorText('http_status', t, language), value: row.statusCode !== null ? String(row.statusCode) : '-' },
    { label: translateRealtimeErrorText('error_code', t, language), value: row.errorCode || '-' },
    { label: translateRealtimeErrorText('upstream_request_id', t, language), value: row.upstreamRequestId || '-' },
    { label: translateRealtimeErrorText('retry_after', t, language), value: row.retryAfter || '-' },
  ].filter((item) => item.value !== '-');

  return (
    <div className={styles.realtimeErrorDetailsPanel}>
      <div className={styles.realtimeErrorOverview}>
        <div className={styles.realtimeErrorOverviewTop}>
          <StatusBadge tone="bad">{statusText}</StatusBadge>
          <span>{categoryText}</span>
        </div>
        <strong>{summaryText}</strong>
      </div>
      {row.errorMessage ? (
        <div className={styles.realtimeErrorMessageBlock}>
          <span>{translateRealtimeErrorText('error_message', t, language)}</span>
          <pre className={styles.realtimeErrorMessage}>{compactRealtimeErrorMessage(row.errorMessage, 1200)}</pre>
        </div>
      ) : null}
      {detailItems.length > 0 ? (
        <div className={styles.realtimeErrorDetailsGrid}>
          {detailItems.map((item) => (
            <div key={item.label} className={styles.realtimeErrorDetailItem}>
              <span>{item.label}</span>
              <strong>{maskSensitiveText(item.value)}</strong>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function RecentPattern({
  pattern,
  variant = 'default',
  label,
}: {
  pattern: boolean[];
  variant?: 'default' | 'plain';
  label?: string;
}) {
  const normalized = pattern.length > 0 ? pattern : Array.from({ length: 10 }, () => true);
  const successCount = normalized.filter(Boolean).length;
  const failureCount = normalized.length - successCount;
  const ariaLabel = label ?? `Recent ${normalized.length} requests: ${successCount} succeeded, ${failureCount} failed`;
  const containerClassName = [
    styles.patternBars,
    variant === 'plain' ? styles.patternBarsPlain : '',
  ]
    .filter(Boolean)
    .join(' ');
  const barClassName = [styles.patternBar, variant === 'plain' ? styles.patternBarPlain : '']
    .filter(Boolean)
    .join(' ');

  return (
    <div className={containerClassName} role="img" aria-label={ariaLabel}>
      {normalized.map((item, index) => (
        <span
          key={index}
          className={`${barClassName} ${item ? styles.patternSuccess : styles.patternFailed}`}
          aria-hidden="true"
        />
      ))}
    </div>
  );
}
