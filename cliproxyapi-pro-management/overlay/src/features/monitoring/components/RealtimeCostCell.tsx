import {
  useCallback,
  useId,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import { createPortal } from 'react-dom';
import type { TFunction } from 'i18next';
import { IconInfo } from '@/components/ui/icons';
import type { RealtimeLogRow } from '../realtimeLogPresentation';
import { formatCompactNumber, formatUsdPrecise } from '@/utils/usage';
import styles from '@/pages/MonitoringCenterPage.module.scss';

type RealtimeCostTooltipPosition = {
  top: number;
  left: number;
  arrowTop: number;
  placement: 'left' | 'right';
};

const REALTIME_COST_TOOLTIP_WIDTH = 336;
const REALTIME_COST_TOOLTIP_MARGIN = 12;

const formatCostTierLabel = (value: string) => value
  .trim()
  .replace(/[-_]+/g, ' ')
  .replace(/\b\w/g, (character) => character.toUpperCase());

const calculateMillionTokenRate = (cost: number, tokens: number): number | null => (
  tokens > 0 ? (cost / tokens) * 1_000_000 : null
);

const formatMillionTokenRate = (rate: number | null) => rate === null
  ? '--'
  : `$${rate.toLocaleString(undefined, { minimumFractionDigits: 4, maximumFractionDigits: 4 })} / 1M Token`;

export function RealtimeCostCell({ row, hasPrices, t }: {
  row: RealtimeLogRow;
  hasPrices: boolean;
  t: TFunction;
}) {
  const cellRef = useRef<HTMLSpanElement>(null);
  const tooltipId = useId();
  const [tooltipPosition, setTooltipPosition] = useState<RealtimeCostTooltipPosition | null>(null);
  const breakdown = row.costBreakdown;

  const showTooltip = useCallback((element: HTMLElement | null) => {
    if (!element || typeof window === 'undefined') return;
    const rect = element.getBoundingClientRect();
    const detailRowCount = breakdown
      ? 6 + [breakdown.cacheReadTokens, breakdown.cacheWriteTokens, breakdown.reasoningTokens].filter((tokens) => tokens > 0).length
      : 1;
    const estimatedHeight = Math.min(420, 70 + detailRowCount * 31);
    const placement = rect.left >= REALTIME_COST_TOOLTIP_WIDTH + REALTIME_COST_TOOLTIP_MARGIN * 2 ? 'left' : 'right';
    const unclampedLeft = placement === 'left'
      ? rect.left - REALTIME_COST_TOOLTIP_WIDTH - REALTIME_COST_TOOLTIP_MARGIN
      : rect.right + REALTIME_COST_TOOLTIP_MARGIN;
    const left = Math.min(
      Math.max(REALTIME_COST_TOOLTIP_MARGIN, unclampedLeft),
      Math.max(REALTIME_COST_TOOLTIP_MARGIN, window.innerWidth - REALTIME_COST_TOOLTIP_WIDTH - REALTIME_COST_TOOLTIP_MARGIN)
    );
    const centerY = rect.top + rect.height / 2;
    const top = Math.min(
      Math.max(REALTIME_COST_TOOLTIP_MARGIN, centerY - estimatedHeight / 2),
      Math.max(REALTIME_COST_TOOLTIP_MARGIN, window.innerHeight - estimatedHeight - REALTIME_COST_TOOLTIP_MARGIN)
    );
    setTooltipPosition({
      top,
      left,
      placement,
      arrowTop: Math.min(Math.max(22, centerY - top), estimatedHeight - 22),
    });
  }, [breakdown]);

  const hideTooltip = useCallback(() => setTooltipPosition(null), []);

  if (!hasPrices && !breakdown) return <span>--</span>;

  const conditionalCosts = breakdown ? [
    { key: 'cache-read', tokens: breakdown.cacheReadTokens, label: t('monitoring.cost_detail_cache_read'), cost: breakdown.cacheReadCost },
    { key: 'cache-write', tokens: breakdown.cacheWriteTokens, label: t('monitoring.cost_detail_cache_write'), cost: breakdown.cacheWriteCost },
    { key: 'reasoning', tokens: breakdown.reasoningTokens, label: t('monitoring.cost_detail_reasoning'), cost: breakdown.reasoningCost },
  ].filter((item) => item.tokens > 0 || item.cost > 0) : [];
  const actualTier = breakdown?.serviceTier || row.serviceTier;
  const actualTierLabel = actualTier
    ? formatCostTierLabel(actualTier)
    : t('monitoring.cost_detail_standard');
  const billingMode = breakdown?.serviceTier
    ? t('monitoring.cost_detail_service_tier_mode')
    : breakdown && breakdown.contextTierSize > 0
      ? t('monitoring.cost_detail_context_mode', { size: formatCompactNumber(breakdown.contextTierSize) })
      : t('monitoring.cost_detail_standard');

  return (
    <span
      ref={cellRef}
      className={styles.realtimeCostCell}
      onMouseEnter={() => showTooltip(cellRef.current)}
      onMouseLeave={hideTooltip}
    >
      <span className={styles.realtimeCostValue}>{formatUsdPrecise(row.totalCost)}</span>
      <button
        type="button"
        className={styles.realtimeCostInfoButton}
        aria-label={t('monitoring.cost_detail_open')}
        aria-describedby={tooltipPosition ? tooltipId : undefined}
        onFocus={(event) => showTooltip(event.currentTarget)}
        onBlur={hideTooltip}
      >
        <IconInfo size={16} />
      </button>
      {tooltipPosition && typeof document !== 'undefined' ? createPortal(
        <div
          id={tooltipId}
          role="tooltip"
          className={styles.realtimeCostTooltip}
          data-placement={tooltipPosition.placement}
          style={{
            top: tooltipPosition.top,
            left: tooltipPosition.left,
            '--realtime-cost-arrow-top': `${tooltipPosition.arrowTop}px`,
          } as CSSProperties}
        >
          <strong className={styles.realtimeCostTooltipTitle}>{t('monitoring.cost_detail_title')}</strong>
          {breakdown ? (
            <div className={styles.realtimeCostTooltipRows}>
              <div><span>{t('monitoring.cost_detail_input')}</span><strong>{formatUsdPrecise(breakdown.inputCost)}</strong></div>
              <div><span>{t('monitoring.cost_detail_output')}</span><strong>{formatUsdPrecise(breakdown.outputCost)}</strong></div>
              {conditionalCosts.map((item) => (
                <div key={item.key}><span>{item.label}</span><strong>{formatUsdPrecise(item.cost)}</strong></div>
              ))}
              <div className={styles.realtimeCostTooltipDivider} aria-hidden="true" />
              <div><span>{t('monitoring.cost_detail_input_rate')}</span><strong className={styles.realtimeCostRateInput}>{formatMillionTokenRate(calculateMillionTokenRate(breakdown.inputCost, breakdown.inputTokens))}</strong></div>
              <div><span>{t('monitoring.cost_detail_output_rate')}</span><strong className={styles.realtimeCostRateOutput}>{formatMillionTokenRate(calculateMillionTokenRate(breakdown.outputCost, breakdown.outputTokens))}</strong></div>
              <div><span>{t('monitoring.cost_detail_actual_tier')}</span><strong>{actualTierLabel}</strong></div>
              <div><span>{t('monitoring.cost_detail_billing_mode')}</span><strong>{billingMode}</strong></div>
            </div>
          ) : (
            <p className={styles.realtimeCostTooltipEmpty}>{t('monitoring.cost_detail_unavailable')}</p>
          )}
        </div>,
        document.body
      ) : null}
    </span>
  );
}
