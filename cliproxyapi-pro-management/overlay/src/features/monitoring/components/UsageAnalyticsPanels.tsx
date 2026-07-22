import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import type { TFunction } from 'i18next';
import { Card } from '@/components/ui/Card';
import { Select } from '@/components/ui/Select';
import type { MonitoringAccountRow, MonitoringTimeRange } from '../hooks/useMonitoringData';
import {
  formatPercent,
  formatRankingMetricValue,
  getChartAxisLabels,
  getProgressWidth,
  getRankingMetricLabel,
  getRankingMetricValue,
  getRankingSummaryLabel,
  type RankingMetric,
  type TokenDistributionPoint,
  type TrendPoint,
} from '../monitoringAnalytics';
import { RANKING_METRIC_OPTIONS, TIME_RANGE_OPTIONS } from '../monitoringOptions';
import { formatCompactNumber, formatUsd } from '@/utils/usage';
import styles from '@/pages/MonitoringCenterPage.module.scss';

export type UsageMetricCard = {
  key: string;
  title: string;
  label: string;
  value: ReactNode;
  accent: 'blue' | 'purple' | 'green' | 'amber';
  footer: Array<{ label: string; value: ReactNode }>;
};

const DONUT_COLORS = ['#2563eb', '#22c55e', '#f97316', '#8b5cf6', '#06b6d4', '#ec4899', '#14b8a6', '#eab308'];

const getDonutColor = (index: number) => DONUT_COLORS[index % DONUT_COLORS.length];

const RankingMetricSwitch = ({
  value,
  onChange,
  disabledCost,
  t,
}: {
  value: RankingMetric;
  onChange: (value: RankingMetric) => void;
  disabledCost: boolean;
  t: TFunction;
}) => (
  <div className={styles.rankingMetricSwitch} role="group" aria-label={t('monitoring.ranking_metric_aria')}>
    {RANKING_METRIC_OPTIONS.map((option) => (
      <button
        key={option.value}
        type="button"
        className={`${styles.rankingMetricButton} ${value === option.value ? styles.rankingMetricButtonActive : ''}`}
        onClick={() => onChange(option.value)}
        disabled={option.value === 'cost' && disabledCost}
      >
        {t(option.labelKey)}
      </button>
    ))}
  </div>
);

export function UsageTrendHeader({
  range,
  totalCalls,
  apiKeyFilter,
  apiKeyOptions,
  onRangeChange,
  onApiKeyFilterChange,
  onHide,
  t,
}: {
  range: MonitoringTimeRange;
  totalCalls: number;
  apiKeyFilter: string;
  apiKeyOptions: Array<{ value: string; label: string }>;
  onRangeChange: (range: MonitoringTimeRange) => void;
  onApiKeyFilterChange: (value: string) => void;
  onHide: () => void;
  t: TFunction;
}) {
  return (
    <div className={styles.usageTrendHeader}>
      <div className={styles.usageTrendCopy}>
        <h2>{t('monitoring.usage_stats_title')}</h2>
        <p>{t('monitoring.usage_stats_desc', { value: formatCompactNumber(totalCalls) })}</p>
      </div>
      <button type="button" className={`${styles.rankingMetricButton} ${styles.usageTrendHideButton} ${styles.mobileHeaderHideButton}`} onClick={onHide}>
        {t('monitoring.hide_analysis')}
      </button>
      <div className={styles.usageTrendActions}>
        <div className={`${styles.rankingMetricSwitch} ${styles.timeRangeControl}`}>
          {TIME_RANGE_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={`${styles.rankingMetricButton} ${styles.timeRangeButton} ${range === option.value ? styles.rankingMetricButtonActive : ''}`}
              onClick={() => onRangeChange(option.value)}
            >
              {t(option.labelKey)}
            </button>
          ))}
        </div>
        <Select
          className={styles.usageTrendApiKeySelect}
          value={apiKeyFilter}
          options={apiKeyOptions}
          onChange={onApiKeyFilterChange}
          ariaLabel={t('monitoring.filter_usage_trend_api_key')}
          fullWidth={false}
        />
        <button type="button" className={`${styles.rankingMetricButton} ${styles.usageTrendHideButton}`} onClick={onHide}>
          {t('monitoring.hide_analysis')}
        </button>
      </div>
    </div>
  );
}

export function TopUsageStats({ cards }: { cards: UsageMetricCard[] }) {
  return (
    <section className={styles.usageStatsGrid} aria-label="Usage statistics">
      {cards.map((card) => (
        <Card key={card.key} className={`${styles.usageStatsCard} ${card.key === 'tokens' ? styles.usageStatsCardTokens : ''}`}>
          <div className={styles.usageStatsCardHeader}>
            <span className={`${styles.usageStatsIcon} ${styles[`usageStatsIcon${card.accent}`]}`} aria-hidden="true" />
            <strong>{card.title}</strong>
          </div>
          <div className={styles.usageStatsBody}>
            <span>{card.label}</span>
            <strong>{card.value}</strong>
          </div>
          <div className={styles.usageStatsFooter}>
            {card.footer.map((item) => (
              <div key={item.label}>
                <span>{item.label}</span>
                <strong>{item.value}</strong>
              </div>
            ))}
          </div>
        </Card>
      ))}
    </section>
  );
}

export function UsageTrendPanel({
  points,
  hasPrices,
  emptyText,
  t,
}: {
  points: TrendPoint[];
  hasPrices: boolean;
  emptyText: string;
  t: TFunction;
}) {
  const chartPoints = points.slice(-30);
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const chartViewBoxHeight = 310;
  const [chartViewBoxWidth, setChartViewBoxWidth] = useState(700);
  const rightLabelX = chartViewBoxWidth - 8;
  const plot = {
    left: 36,
    top: 38,
    right: rightLabelX - 144,
    costAxis: rightLabelX - 88,
    tokenAxis: rightLabelX - 42,
    tokenLabel: rightLabelX,
    bottom: 278,
  };
  const requestMax = Math.max(...chartPoints.map((point) => point.requests), 0);
  const tokenMax = Math.max(...chartPoints.map((point) => point.tokens), 0);
  const costMax = Math.max(...chartPoints.map((point) => point.cost), 0);
  const requestAxisMax = Math.max(10, Math.ceil(requestMax * 1.1));
  const tokenAxisMax = Math.max(1000, Math.ceil(tokenMax * 1.1));
  const costAxisMax = Math.max(0.1, costMax * 1.1);
  const series = [
    {
      key: 'tokens',
      label: t('monitoring.ranking_metric_tokens'),
      color: '#7c3aed',
      axis: 'tokens',
      getValue: (point: TrendPoint) => point.tokens,
      format: (value: number) => formatCompactNumber(value),
    },
    {
      key: 'requests',
      label: t('monitoring.ranking_metric_requests'),
      color: '#2563eb',
      axis: 'requests',
      getValue: (point: TrendPoint) => point.requests,
      format: (value: number) => formatCompactNumber(value),
    },
    {
      key: 'cost',
      label: t('monitoring.ranking_metric_cost'),
      color: '#047857',
      axis: 'cost',
      getValue: (point: TrendPoint) => point.cost,
      format: (value: number) => (hasPrices ? formatUsd(value) : '--'),
    },
  ] as const;
  const visibleSeries = hasPrices ? series : series.filter((item) => item.key !== 'cost');
  const totals = chartPoints.reduce(
    (sum, point) => ({
      requests: sum.requests + point.requests,
      failures: sum.failures + point.failures,
      tokens: sum.tokens + point.tokens,
      cost: sum.cost + point.cost,
    }),
    { requests: 0, failures: 0, tokens: 0, cost: 0 }
  );
  const peakTokenPoint = chartPoints.reduce<TrendPoint | null>(
    (peak, point) => (!peak || point.tokens > peak.tokens ? point : peak),
    null
  );
  const summaryItems = [
    { key: 'requests', label: t('monitoring.total_requests_label'), value: formatCompactNumber(totals.requests), color: '#2563eb' },
    { key: 'tokens', label: t('monitoring.total_tokens_label'), value: formatCompactNumber(totals.tokens), color: '#7c3aed' },
    ...(hasPrices ? [{ key: 'cost', label: t('monitoring.total_cost_label'), value: formatUsd(totals.cost), color: '#059669' }] : []),
    { key: 'peak', label: t('monitoring.peak_period'), value: peakTokenPoint?.label ?? '--', color: '#f97316' },
  ];
  const trendMinutes = Math.max(chartPoints.length * 60, 1);
  const headerStats = [
    { key: 'rpm', label: 'RPM', value: (totals.requests / trendMinutes).toFixed(2) },
    { key: 'tpm', label: 'TPM', value: formatCompactNumber(totals.tokens / trendMinutes) },
    { key: 'errorRate', label: t('monitoring.error_rate'), value: formatPercent(totals.requests > 0 ? totals.failures / totals.requests : 0) },
  ];
  const axisTicks = [0, 0.25, 0.5, 0.75, 1];
  const getAxisMax = (axis: typeof series[number]['axis']) => {
    if (axis === 'tokens') return tokenAxisMax;
    if (axis === 'cost') return costAxisMax;
    return requestAxisMax;
  };
  const getX = (index: number) => chartPoints.length <= 1
    ? (plot.left + plot.right) / 2
    : plot.left + (index / (chartPoints.length - 1)) * (plot.right - plot.left);
  const getY = (value: number, axis: typeof series[number]['axis']) => {
    const max = getAxisMax(axis);
    return plot.bottom - Math.max(Math.min(value / max, 1), 0) * (plot.bottom - plot.top);
  };
  const buildPath = (item: typeof series[number]) => {
    const coords = chartPoints.map((point, index) => ({
      x: getX(index),
      y: getY(item.getValue(point), item.axis),
    }));
    if (coords.length === 0) return '';
    if (coords.length === 1) return `M ${coords[0].x} ${coords[0].y}`;
    return coords.slice(1).reduce((path, point, index) => {
      const previous = coords[index];
      const midX = (previous.x + point.x) / 2;
      return `${path} C ${midX} ${previous.y}, ${midX} ${point.y}, ${point.x} ${point.y}`;
    }, `M ${coords[0].x} ${coords[0].y}`);
  };
  const buildAreaPath = (item: typeof series[number]) => {
    const path = buildPath(item);
    return path ? `${path} L ${getX(chartPoints.length - 1)} ${plot.bottom} L ${getX(0)} ${plot.bottom} Z` : '';
  };
  const labels = getChartAxisLabels(chartPoints);
  const hoveredPoint = hoveredIndex === null ? null : chartPoints[hoveredIndex];
  const hoveredX = hoveredIndex === null ? 0 : getX(hoveredIndex);
  const tooltipX = Math.min(Math.max(hoveredX - 84, plot.left + 8), plot.right - 168);
  const formatCostAxisValue = (value: number) => `$${formatCompactNumber(value)}`;

  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;

    const updateViewBoxWidth = () => {
      const rect = svg.getBoundingClientRect();
      const nextWidth = Math.max(700, Math.round((rect.width / Math.max(rect.height, 1)) * chartViewBoxHeight));
      setChartViewBoxWidth((current) => current === nextWidth ? current : nextWidth);
    };

    updateViewBoxWidth();
    const observer = new ResizeObserver(updateViewBoxWidth);
    observer.observe(svg);
    return () => observer.disconnect();
  }, [chartViewBoxHeight]);

  return (
    <Card className={`${styles.usageTrendChartCard} ${styles.usageTrendLineCard}`}>
      <div className={styles.trendCardHeader}>
        <div>
          <h3>{t('monitoring.usage_trend_chart_title')}</h3>
          <p>{t('monitoring.usage_trend_chart_desc')}</p>
        </div>
        <div className={styles.trendHeaderStats}>
          {headerStats.map((item) => (
            <div key={item.key}>
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
          ))}
        </div>
      </div>
      {chartPoints.length > 0 ? (
        <div className={styles.professionalChartShell}>
          <div className={styles.trendSummaryStrip}>
            {summaryItems.map((item) => (
              <div key={item.key} className={styles.trendSummaryItem} style={{ '--series-color': item.color } as CSSProperties}>
                <span>{item.label}</span>
                <strong>{item.value}</strong>
              </div>
            ))}
          </div>
          <svg ref={svgRef} className={styles.usageTrendSvg} viewBox={`0 0 ${chartViewBoxWidth} ${chartViewBoxHeight}`} role="img" aria-label={t('monitoring.usage_trend_chart_aria')}>
            <defs>
              <linearGradient id="usageTrendTokensFill" x1="0" x2="0" y1="0" y2="1">
                <stop offset="5%" stopColor="#7c3aed" stopOpacity="0.24" />
                <stop offset="95%" stopColor="#7c3aed" stopOpacity="0" />
              </linearGradient>
              <linearGradient id="usageTrendRequestsFill" x1="0" x2="0" y1="0" y2="1">
                <stop offset="5%" stopColor="#2563eb" stopOpacity="0.16" />
                <stop offset="95%" stopColor="#2563eb" stopOpacity="0" />
              </linearGradient>
              <linearGradient id="usageTrendCostFill" x1="0" x2="0" y1="0" y2="1">
                <stop offset="5%" stopColor="#047857" stopOpacity="0.24" />
                <stop offset="95%" stopColor="#047857" stopOpacity="0" />
              </linearGradient>
            </defs>
            {axisTicks.map((tick) => {
              const y = plot.bottom - tick * (plot.bottom - plot.top);
              return (
                <g key={tick}>
                  {tick > 0 ? <line className={styles.chartGridLine} x1={plot.left} x2={plot.costAxis} y1={y} y2={y} /> : null}
                  <text className={`${styles.chartAxisLabel} ${styles.chartAxisLabelRequests}`} x={plot.left - 12} y={y + 4}>
                    {formatCompactNumber(requestAxisMax * tick)}
                  </text>
                  {hasPrices ? (
                    <text className={`${styles.chartAxisLabel} ${styles.chartAxisLabelCost}`} x={plot.costAxis - 12} y={y + 4}>
                      {formatCostAxisValue(costAxisMax * tick)}
                    </text>
                  ) : null}
                  <text className={`${styles.chartAxisLabel} ${styles.chartAxisLabelTokens}`} x={plot.tokenLabel} y={y + 4}>
                    {formatCompactNumber(tokenAxisMax * tick)}
                  </text>
                </g>
              );
            })}
            <line className={styles.chartAxisBase} x1={plot.left} x2={plot.costAxis} y1={plot.bottom} y2={plot.bottom} />
            <line className={styles.chartYAxisRequests} x1={plot.left} x2={plot.left} y1={plot.top} y2={plot.bottom} />
            {hasPrices ? <line className={styles.chartYAxisCost} x1={plot.costAxis} x2={plot.costAxis} y1={plot.top} y2={plot.bottom} /> : null}
            <line className={styles.chartYAxisTokens} x1={plot.tokenAxis} x2={plot.tokenAxis} y1={plot.top} y2={plot.bottom} />
            {visibleSeries.map((item) => {
              const path = buildPath(item);
              const area = buildAreaPath(item);
              return (
                <g key={item.key}>
                  {area ? <path className={styles.trendAreaFill} d={area} fill={`url(#usageTrend${item.key[0].toUpperCase()}${item.key.slice(1)}Fill)`} /> : null}
                  <path className={styles.trendSeriesLine} d={path} stroke={item.color} />
                </g>
              );
            })}
            {labels.map((item) => (
              <g key={item.key}>
                <line className={styles.chartXAxisTick} x1={getX(item.index)} x2={getX(item.index)} y1={plot.bottom} y2={plot.bottom + 7} />
                <text className={styles.chartXAxisLabel} x={getX(item.index)} y="300">{item.label}</text>
              </g>
            ))}
            {chartPoints.map((point, index) => {
              const x = getX(index);
              const isHovered = hoveredIndex === index;
              return (
                <g
                  key={point.key}
                  className={styles.trendHoverTarget}
                  onMouseEnter={() => setHoveredIndex(index)}
                  onMouseLeave={() => setHoveredIndex(null)}
                  onFocus={() => setHoveredIndex(index)}
                  onBlur={() => setHoveredIndex(null)}
                  tabIndex={0}
                >
                  <rect x={Math.max(plot.left, x - 14)} y={plot.top - 10} width="28" height={plot.bottom - plot.top + 26} fill="transparent" />
                  {isHovered ? <line className={styles.trendHoverGuide} x1={x} x2={x} y1={plot.top} y2={plot.bottom} /> : null}
                  {isHovered ? visibleSeries.map((item) => (
                    <circle
                      key={item.key}
                      className={styles.trendSeriesDot}
                      cx={x}
                      cy={getY(item.getValue(point), item.axis)}
                      r={4.5}
                      stroke={item.color}
                    />
                  )) : null}
                </g>
              );
            })}
            {hoveredPoint ? (
              <g className={styles.trendTooltipLayer}>
                <rect x={tooltipX} y="82" width="168" height={hasPrices ? 118 : 92} rx="12" />
                <text className={styles.trendTooltipTitle} x={tooltipX + 16} y="108">{hoveredPoint.label}</text>
                {visibleSeries.map((item, index) => (
                  <text key={item.key} className={styles.trendTooltipMetric} x={tooltipX + 16} y={136 + index * 25} fill={item.color}>
                    {`${item.label}：${item.format(item.getValue(hoveredPoint))}`}
                  </text>
                ))}
              </g>
            ) : null}
          </svg>
          <div className={styles.trendChartLegend}>
            {visibleSeries.map((item) => (
              <span key={item.key} style={{ '--series-color': item.color } as CSSProperties}>
                {item.label}
              </span>
            ))}
          </div>
        </div>
      ) : (
        <div className={styles.emptyBlockSmall}>{emptyText}</div>
      )}
    </Card>
  );
}

export function TokenDistributionPanel({
  points,
  emptyText,
  hasPrices,
  t,
}: {
  points: TokenDistributionPoint[];
  emptyText: string;
  hasPrices: boolean;
  t: TFunction;
}) {
  const totals = points.reduce(
    (sum, point) => ({
      requests: sum.requests + point.requests,
      totalTokens: sum.totalTokens + point.totalTokens,
      inputTokens: sum.inputTokens + point.inputTokens,
      outputTokens: sum.outputTokens + point.outputTokens,
      reasoningTokens: sum.reasoningTokens + point.reasoningTokens,
      cachedTokens: sum.cachedTokens + point.cachedTokens,
      totalCost: sum.totalCost + point.totalCost,
    }),
    { requests: 0, totalTokens: 0, inputTokens: 0, outputTokens: 0, reasoningTokens: 0, cachedTokens: 0, totalCost: 0 }
  );
  const tokenMinutes = Math.max(points.length * 60, 1);
  const rpm = totals.requests / tokenMinutes;
  const tpm = totals.totalTokens / tokenMinutes;
  const rows = [
    { key: 'rpm', label: 'RPM', value: rpm, displayValue: rpm.toFixed(2), base: 0, accent: 'Purple', showShare: false },
    { key: 'tpm', label: 'TPM', value: tpm, displayValue: formatCompactNumber(tpm), base: 0, accent: 'Blue', showShare: false },
    { key: 'requests', label: t('monitoring.request_count'), value: totals.requests, displayValue: formatCompactNumber(totals.requests), base: 0, accent: 'Cyan', showShare: false },
    { key: 'total', label: t('monitoring.total_tokens_label'), value: totals.totalTokens, displayValue: formatCompactNumber(totals.totalTokens), base: 0, accent: 'Green', showShare: false },
    { key: 'input', label: t('monitoring.token_metric_input'), value: totals.inputTokens, displayValue: formatCompactNumber(totals.inputTokens), base: totals.totalTokens, accent: 'Amber', showShare: true },
    { key: 'output', label: t('monitoring.token_metric_output'), value: totals.outputTokens, displayValue: formatCompactNumber(totals.outputTokens), base: totals.totalTokens, accent: 'Rose', showShare: true },
    { key: 'reasoning', label: t('monitoring.token_metric_reasoning'), value: totals.reasoningTokens, displayValue: formatCompactNumber(totals.reasoningTokens), base: totals.totalTokens, accent: 'Indigo', showShare: true },
    { key: 'cached', label: t('monitoring.token_metric_cached'), value: totals.cachedTokens, displayValue: formatCompactNumber(totals.cachedTokens), base: totals.totalTokens, accent: 'Slate', showShare: true },
  ];
  const hasData = rows.some((row) => row.value > 0);

  return (
    <Card className={`${styles.usageTrendChartCard} ${styles.tokenDistributionCard}`}>
      <div className={`${styles.trendCardHeader} ${styles.tokenDistributionHeader}`}>
        <div>
          <h3>{t('monitoring.token_stats_title')}</h3>
          <p>{t('monitoring.token_stats_desc')}</p>
        </div>
        <div className={styles.tokenCostBadge}>
          <span>{t('monitoring.token_cost')}</span>
          <strong>{hasPrices ? formatUsd(totals.totalCost) : '--'}</strong>
        </div>
      </div>
      {hasData ? (
        <div className={styles.tokenStatCardList}>
          {rows.map((row) => {
            const share = row.base > 0 ? row.value / row.base : 0;
            return (
              <div key={row.key} className={`${styles.tokenStatCard} ${styles[`tokenStatCard${row.accent}`]}`}>
                <div className={styles.tokenStatCardHeader}>
                  <span>{row.label}</span>
                  {row.showShare ? <strong>{formatPercent(share)}</strong> : null}
                </div>
                <div className={styles.tokenStatCardValue}>{row.displayValue}</div>
                {row.showShare ? (
                  <div className={styles.tokenStatProgressTrack} aria-hidden="true">
                    <span style={{ '--token-stat-width': `${Math.min(Math.max(share * 100, row.value > 0 ? 1 : 0), 100)}%` } as CSSProperties} />
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : (
        <div className={styles.emptyBlockSmall}>{emptyText}</div>
      )}
    </Card>
  );
}

export function ModelStatsPanel({
  title,
  subtitle,
  rows,
  metric,
  metricTotal,
  onMetricChange,
  emptyText,
  hasPrices,
  t,
}: {
  title: string;
  subtitle: string;
  rows: MonitoringAccountRow[];
  metric: RankingMetric;
  metricTotal: number;
  onMetricChange: (metric: RankingMetric) => void;
  emptyText: string;
  hasPrices: boolean;
  t: TFunction;
}) {
  const shareBase = metricTotal > 0 ? metricTotal : rows.reduce((sum, row) => sum + getRankingMetricValue(row, metric), 0);
  const shareModeLabel = t('monitoring.share_by_metric', { metric: getRankingMetricLabel(metric, t) });
  const totalShareValue = formatRankingMetricValue(shareBase, metric, hasPrices);
  const legendRows = rows.slice(0, 5);
  const donutTooltipRows = rows
    .map((row) => {
      const value = getRankingMetricValue(row, metric);
      return {
        row,
        value,
        share: shareBase > 0 ? value / shareBase : 0,
      };
    })
    .filter((item) => item.value > 0);
  const donutStops = legendRows.reduce(
    (state, row, index) => {
      const value = getRankingMetricValue(row, metric);
      const share = shareBase > 0 ? (value / shareBase) * 100 : 0;
      const end = state.offset + share;
      state.parts.push(`${getDonutColor(index)} ${state.offset}% ${end}%`);
      state.offset = end;
      return state;
    },
    { offset: 0, parts: [] as string[] }
  );
  const donutBackground = donutStops.parts.length > 0
    ? `conic-gradient(${donutStops.parts.join(', ')}, color-mix(in srgb, var(--monitor-line) 58%, transparent) ${donutStops.offset}% 100%)`
    : 'color-mix(in srgb, var(--monitor-line) 58%, transparent)';

  return (
    <Card className={styles.modelStatsCard}>
      <div className={styles.rankingHeader}>
        <div>
          <h3>{title}</h3>
          <p>{subtitle}</p>
        </div>
        <RankingMetricSwitch value={metric} onChange={onMetricChange} disabledCost={!hasPrices} t={t} />
      </div>
      {rows.length > 0 ? (
        <div className={styles.modelStatsLayout}>
          <div className={styles.modelStatsList}>
            {rows.map((row) => {
              const rowValue = getRankingMetricValue(row, metric);
              const value = shareBase > 0 ? rowValue / shareBase : 0;
              return (
                <div key={row.id} className={styles.modelStatsRow}>
                  <div className={styles.modelStatsMain}>
                    <div className={styles.modelStatsTitleLine}>
                      <strong>{row.account}</strong>
                      <span>{formatPercent(value)}</span>
                    </div>
                    <div className={styles.modelStatsMetaLine}>
                      <span>{`${formatCompactNumber(row.totalCalls)} ${t('monitoring.ranking_metric_requests')}`}</span>
                      <span>{`${formatCompactNumber(row.totalTokens)} ${t('monitoring.ranking_metric_tokens')}`}</span>
                      <span>{`${formatCompactNumber(row.failureCalls)} ${t('monitoring.errors_label')}`}</span>
                      <span>{hasPrices ? formatUsd(row.totalCost) : '--'}</span>
                    </div>
                    <span
                      className={styles.modelStatsBar}
                      style={{ '--ranking-width': getProgressWidth(value) } as CSSProperties}
                      aria-hidden="true"
                    />
                  </div>
                </div>
              );
            })}
          </div>
          <aside className={styles.modelSharePanel}>
            <div className={styles.modelShareHeader}>
              <strong>{t('monitoring.model_share_title')}</strong>
              <span>{shareModeLabel}</span>
            </div>
            <div className={styles.donutChart} style={{ '--donut-bg': donutBackground } as CSSProperties}>
              <div className={styles.donutCenter}>
                <span>{getRankingSummaryLabel(metric, t)}</span>
                <strong>{totalShareValue}</strong>
              </div>
              <div className={styles.donutTooltip}>
                <strong>{t('monitoring.share_tooltip_title', { metric: shareModeLabel })}</strong>
                <div>
                  {donutTooltipRows.map((item, index) => (
                    <span key={item.row.id}>
                      <i style={{ background: getDonutColor(index) }} aria-hidden="true" />
                      <em>{item.row.account}</em>
                      <b>{formatPercent(item.share)}</b>
                    </span>
                  ))}
                </div>
              </div>
            </div>
            <div className={styles.modelLegend}>
              {legendRows.map((row, index) => {
                const rowValue = getRankingMetricValue(row, metric);
                const value = shareBase > 0 ? rowValue / shareBase : 0;
                return (
                  <div key={row.id} className={styles.modelLegendItem} style={{ '--legend-color': getDonutColor(index) } as CSSProperties}>
                    <span className={styles.modelLegendDot} />
                    <span>{row.account}</span>
                    <strong>{formatPercent(value)}</strong>
                  </div>
                );
              })}
            </div>
          </aside>
        </div>
      ) : (
        <div className={styles.emptyBlockSmall}>{emptyText}</div>
      )}
    </Card>
  );
}

export function ApiKeyRankingPanel({
  title,
  subtitle,
  rows,
  metric,
  metricTotal,
  onMetricChange,
  emptyText,
  hasPrices,
  t,
}: {
  title: string;
  subtitle: string;
  rows: MonitoringAccountRow[];
  metric: RankingMetric;
  metricTotal: number;
  onMetricChange: (metric: RankingMetric) => void;
  emptyText: string;
  hasPrices: boolean;
  t: TFunction;
}) {
  const shareBase = metricTotal > 0 ? metricTotal : rows.reduce((sum, row) => sum + getRankingMetricValue(row, metric), 0);
  const summaryLabel = getRankingSummaryLabel(metric, t);

  return (
    <Card className={styles.apiKeyRankingCard}>
      <div className={styles.rankingHeader}>
        <div>
          <h3>{title}</h3>
          <p>{subtitle}</p>
        </div>
        <RankingMetricSwitch value={metric} onChange={onMetricChange} disabledCost={!hasPrices} t={t} />
      </div>
      <div className={styles.apiKeyRankingList}>
        {rows.length > 0 ? (
          <div className={styles.apiKeyRankingSummary}>
            <span>{summaryLabel}</span>
            <strong>{formatRankingMetricValue(shareBase, metric, hasPrices)}</strong>
            <small>{t('monitoring.api_keys_count', { count: rows.length })}</small>
          </div>
        ) : null}
        {rows.length > 0 ? (
          <div className={styles.apiKeyRankingScroll}>
            {rows.map((row, index) => {
              const rowValue = getRankingMetricValue(row, metric);
              const share = shareBase > 0 ? rowValue / shareBase : 0;
              return (
                <div key={row.id} className={styles.apiKeyRankingRow}>
                  <div className={styles.apiKeyRankingTopLine}>
                    <div className={styles.apiKeyRankingName}>
                      <span className={styles.rankingIndex}>{index + 1}</span>
                      <strong>{row.account}</strong>
                    </div>
                    <span>{formatPercent(share)}</span>
                  </div>
                  <div className={styles.apiKeyRankingMetaLine}>
                    <span>{`${formatCompactNumber(row.totalCalls)} ${t('monitoring.ranking_metric_requests')}`}</span>
                    <span>{`${formatCompactNumber(row.totalTokens)} Token`}</span>
                    <span>{`${formatCompactNumber(row.failureCalls)} ${t('monitoring.errors_label')}`}</span>
                    <span>{hasPrices ? formatUsd(row.totalCost) : '--'}</span>
                  </div>
                  <span
                    className={styles.apiKeyRankingBar}
                    style={{ '--ranking-width': getProgressWidth(share) } as CSSProperties}
                    aria-hidden="true"
                  />
                </div>
              );
            })}
          </div>
        ) : (
          <div className={styles.emptyBlockSmall}>{emptyText}</div>
        )}
      </div>
    </Card>
  );
}
