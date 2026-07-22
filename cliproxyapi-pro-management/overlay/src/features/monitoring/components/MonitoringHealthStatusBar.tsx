import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { TFunction } from 'i18next';
import type { AccountStatusBlockDetail, AccountStatusData } from '../accountHealth';
import styles from '../monitoring.module.scss';



const ACCOUNT_STATUS_COLOR_STOPS = [
  { r: 239, g: 68, b: 68 },
  { r: 250, g: 204, b: 21 },
  { r: 34, g: 197, b: 94 },
] as const;

const getAccountStatusColor = (rate: number) => {
  const value = Math.max(0, Math.min(1, rate));
  const segment = value < 0.5 ? 0 : 1;
  const localValue = segment === 0 ? value * 2 : (value - 0.5) * 2;
  const from = ACCOUNT_STATUS_COLOR_STOPS[segment];
  const to = ACCOUNT_STATUS_COLOR_STOPS[segment + 1];
  const r = Math.round(from.r + (to.r - from.r) * localValue);
  const g = Math.round(from.g + (to.g - from.g) * localValue);
  const b = Math.round(from.b + (to.b - from.b) * localValue);
  return `rgb(${r}, ${g}, ${b})`;
};

const formatStatusRate = (rate: number) => {
  const rounded = rate.toFixed(1);
  return `${rounded.endsWith('.0') ? rounded.slice(0, -2) : rounded}%`;
};

const formatStatusWindowLabel = (startTime: number, endTime: number, locale: string) => {
  const start = new Date(startTime);
  const end = new Date(endTime);
  const sameDay = start.toDateString() === end.toDateString();
  const dateOptions: Intl.DateTimeFormatOptions = { month: 'numeric', day: 'numeric' };
  const timeOptions: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' };
  const startDateLabel = start.toLocaleDateString(locale, dateOptions);
  const endDateLabel = end.toLocaleDateString(locale, dateOptions);
  const startTimeLabel = start.toLocaleTimeString(locale, timeOptions);
  const endTimeLabel = end.toLocaleTimeString(locale, timeOptions);

  return sameDay
    ? `${startDateLabel} ${startTimeLabel} - ${endTimeLabel}`
    : `${startDateLabel} ${startTimeLabel} - ${endDateLabel} ${endTimeLabel}`;
};

const buildAccountStatusBlockAriaLabel = ({
  detail,
  timeRangeLabel,
  successRateValue,
  copy,
}: {
  detail: AccountStatusBlockDetail;
  timeRangeLabel: string;
  successRateValue: string;
  copy: {
    successLabel: string;
    failureLabel: string;
    noRequestsLabel: string;
    successRateLabel: string;
  };
}) => {
  const total = detail.success + detail.failure;
  if (total === 0) {
    return `${timeRangeLabel}，${copy.noRequestsLabel}`;
  }
  return `${timeRangeLabel}，${copy.successLabel} ${detail.success}，${copy.failureLabel} ${detail.failure}，${copy.successRateLabel} ${successRateValue}`;
};

const getNextAccountStatusBlockIndex = (currentIndex: number, key: string, count: number) => {
  if (count <= 0) return null;
  if (key === 'ArrowRight' || key === 'ArrowDown') return Math.min(currentIndex + 1, count - 1);
  if (key === 'ArrowLeft' || key === 'ArrowUp') return Math.max(currentIndex - 1, 0);
  if (key === 'Home') return 0;
  if (key === 'End') return count - 1;
  return null;
};

export function MonitoringHealthStatusBar({
  statusData,
  locale,
  t,
  showRate = true,
}: {
  statusData: AccountStatusData;
  locale: string;
  t: TFunction;
  showRate?: boolean;
}) {
  const [activeTooltip, setActiveTooltip] = useState<number | null>(null);
  const [focusIndex, setFocusIndex] = useState(() => (statusData.blockDetails.length > 0 ? 0 : -1));
  const blocksRef = useRef<HTMLDivElement | null>(null);
  const blockButtonRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const tooltipIdPrefix = useId();
  const blockCount = statusData.blockDetails.length;
  const resolvedFocusIndex = blockCount === 0 ? -1 : focusIndex >= 0 && focusIndex < blockCount ? focusIndex : 0;
  const resolvedActiveTooltip = activeTooltip !== null && activeTooltip >= 0 && activeTooltip < blockCount ? activeTooltip : null;
  const hasData = statusData.totalSuccess + statusData.totalFailure > 0;
  const rateClassName = !hasData
    ? ''
    : statusData.successRate >= 90
      ? styles.monitoringStatusRateHigh
      : statusData.successRate >= 50
        ? styles.monitoringStatusRateMedium
        : styles.monitoringStatusRateLow;

  useEffect(() => {
    if (resolvedActiveTooltip === null) return;
    const handlePointerDown = (event: PointerEvent) => {
      if (blocksRef.current && !blocksRef.current.contains(event.target as Node)) {
        setActiveTooltip(null);
      }
    };
    document.addEventListener('pointerdown', handlePointerDown);
    return () => document.removeEventListener('pointerdown', handlePointerDown);
  }, [resolvedActiveTooltip]);

  const handlePointerEnter = useCallback((event: React.PointerEvent, index: number) => {
    if (event.pointerType === 'mouse') {
      setActiveTooltip(index);
    }
  }, []);

  const handlePointerLeave = useCallback((event: React.PointerEvent) => {
    if (event.pointerType === 'mouse' && (!blocksRef.current || !blocksRef.current.contains(document.activeElement))) {
      setActiveTooltip(null);
    }
  }, []);

  const handlePointerDown = useCallback((event: React.PointerEvent, index: number) => {
    if (event.pointerType === 'touch') {
      event.preventDefault();
      setFocusIndex(index);
      setActiveTooltip((previous) => (previous === index ? null : index));
    }
  }, []);

  const focusBlock = useCallback((index: number) => {
    blockButtonRefs.current[index]?.focus();
    setFocusIndex(index);
    setActiveTooltip(index);
  }, []);

  const handleFocus = useCallback((index: number) => {
    setFocusIndex(index);
    setActiveTooltip(index);
  }, []);

  const handleBlur = useCallback((event: React.FocusEvent<HTMLButtonElement>) => {
    if (blocksRef.current?.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setActiveTooltip(null);
  }, []);

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLButtonElement>, index: number) => {
      if (event.key === 'Escape') {
        setActiveTooltip(null);
        return;
      }

      const nextIndex = getNextAccountStatusBlockIndex(index, event.key, blockCount);
      if (nextIndex === null) return;

      event.preventDefault();
      focusBlock(nextIndex);
    },
    [blockCount, focusBlock]
  );

  const getTooltipPositionClassName = (index: number, total: number) => {
    if (index <= 2) return styles.monitoringStatusTooltipLeft;
    if (index >= total - 3) return styles.monitoringStatusTooltipRight;
    return '';
  };

  const renderTooltip = (detail: AccountStatusBlockDetail, index: number, tooltipId: string) => {
    const total = detail.success + detail.failure;
    const timeRange = formatStatusWindowLabel(detail.startTime, detail.endTime, locale);

    return (
      <div
        id={tooltipId}
        role="tooltip"
        className={[
          styles.monitoringStatusTooltip,
          getTooltipPositionClassName(index, statusData.blockDetails.length),
        ]
          .filter(Boolean)
          .join(' ')}
      >
        <span className={styles.monitoringTooltipTime}>{timeRange}</span>
        {total > 0 ? (
          <span className={styles.monitoringTooltipStats}>
            <span className={styles.monitoringTooltipSuccess}>{t('status_bar.success_short')} {detail.success}</span>
            <span className={styles.monitoringTooltipFailure}>{t('status_bar.failure_short')} {detail.failure}</span>
            <span className={styles.monitoringTooltipRate}>({(detail.rate * 100).toFixed(1)}%)</span>
          </span>
        ) : (
          <span className={styles.monitoringTooltipStats}>{t('status_bar.no_requests')}</span>
        )}
      </div>
    );
  };

  return (
    <div className={styles.monitoringStatusBar}>
      <div
        className={styles.monitoringStatusBlocks}
        ref={blocksRef}
        role="group"
        aria-label={t('monitoring.account_health_status')}
      >
        {statusData.blockDetails.map((detail, index) => {
          const isIdle = detail.rate === -1;
          const isActive = resolvedActiveTooltip === index;
          const timeRangeLabel = formatStatusWindowLabel(detail.startTime, detail.endTime, locale);
          const tooltipId = `${tooltipIdPrefix}-monitoring-status-tooltip-${index}`;
          const ariaLabel = buildAccountStatusBlockAriaLabel({
            detail,
            timeRangeLabel,
            successRateValue: formatStatusRate(Math.max(0, detail.rate * 100)),
            copy: {
              successLabel: t('stats.success'),
              failureLabel: t('stats.failure'),
              noRequestsLabel: t('status_bar.no_requests'),
              successRateLabel: t('monitoring.success_rate'),
            },
          });

          return (
            <div
              key={`${detail.startTime}-${detail.endTime}`}
              className={[styles.monitoringStatusBlockWrapper, isActive ? styles.monitoringStatusBlockActive : '']
                .filter(Boolean)
                .join(' ')}
            >
              <button
                ref={(node) => {
                  blockButtonRefs.current[index] = node;
                }}
                type="button"
                className={styles.monitoringStatusBlockButton}
                tabIndex={resolvedFocusIndex === index ? 0 : -1}
                aria-label={ariaLabel}
                aria-describedby={isActive ? tooltipId : undefined}
                onFocus={() => handleFocus(index)}
                onBlur={handleBlur}
                onKeyDown={(event) => handleKeyDown(event, index)}
                onPointerEnter={(event) => handlePointerEnter(event, index)}
                onPointerLeave={handlePointerLeave}
                onPointerDown={(event) => handlePointerDown(event, index)}
              >
                <div
                  aria-hidden="true"
                  className={[styles.monitoringStatusBlock, isIdle ? styles.monitoringStatusBlockIdle : '']
                    .filter(Boolean)
                    .join(' ')}
                  style={isIdle ? undefined : { backgroundColor: getAccountStatusColor(detail.rate) }}
                />
              </button>
              {isActive ? renderTooltip(detail, index, tooltipId) : null}
            </div>
          );
        })}
      </div>
      {showRate ? (
        <span
          className={[styles.monitoringStatusRate, rateClassName, !hasData ? styles.monitoringStatusRatePlaceholder : '']
            .filter(Boolean)
            .join(' ')}
        >
          {hasData ? formatStatusRate(statusData.successRate) : '--'}
        </span>
      ) : null}
    </div>
  );
}
