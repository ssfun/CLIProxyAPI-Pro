import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useLanguageStore } from '@/stores';
import { LANGUAGE_ORDER, LANGUAGE_LABEL_KEYS } from '@/utils/constants';
import styles from './SelfUsagePage.module.scss';
import { formatTokenCount as formatTokens, getCacheHitRate } from './selfUsagePresentation';
import { startPolling } from '@/pro/shared/polling';
import { createSelfUsageRefresh, parseSelfUsageNotification } from './selfUsageRefresh';

type Totals = {
  totalRequests: number;
  successCount: number;
  failureCount: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  estimatedCost: number;
};
type Stats = {
  generation?: number;
  total: Totals;
  trend: (Totals & { bucketStartMs: number })[];
  snapshotAtMs: number;
};
type Event = {
  id: number;
  timestampMs: number;
  model: string;
  reasoningEffort?: string;
  reasoningTokens?: number;
  cachedTokens?: number;
  cacheReadTokens?: number;
  cacheInputTokens?: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  latencyMs?: number;
  ttftMs?: number;
  statusCode?: number;
  estimatedCost?: number;
  failed: boolean;
};
type Events = {
  items: Event[];
  hasMore: boolean;
  snapshotAtMs: number;
  generation?: number;
};
type Quota = {
  enforced: boolean;
  state: string;
  nextRecoverAtMs?: number;
  quota?: {
    enabled: boolean;
    requests?: number;
    totalTokens?: number;
    cost?: number;
    period: { type: string; value?: number; unit?: string; timezone?: string };
    usage: {
      requestsUsed: number;
      totalTokensUsed: number;
      costUsed: number;
      requestsRemaining?: number;
      totalTokensRemaining?: number;
      costRemaining?: number;
      windowEndsAtMs?: number;
    };
  };
};
class QueryError extends Error {
  constructor(
    public status: number,
    public generation?: number
  ) {
    super(
      status === 401
        ? 'self_usage.error_invalid_key'
        : status === 404
          ? 'self_usage.error_update_core'
          : status === 429
            ? 'self_usage.error_busy'
            : 'self_usage.error_unavailable'
    );
  }
}

async function request<T>(key: string, path: string, signal: AbortSignal): Promise<T> {
  const response = await fetch(`./v0/self/${path}`, {
    headers: { Authorization: `Bearer ${key}` },
    signal,
    cache: 'no-store',
    credentials: 'omit',
    redirect: 'error',
  });
  if (!response.ok) {
    const detail = response.status === 409 ? await response.json().catch(() => null) : null;
    throw new QueryError(response.status, detail?.generation);
  }
  return response.json() as Promise<T>;
}

export function SelfUsagePage() {
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage ?? i18n.language;
  const setLanguage = useLanguageStore((state) => state.setLanguage);
  const number = (value: number) =>
    new Intl.NumberFormat(language, { maximumFractionDigits: 2 }).format(value);
  const money = (value: number) =>
    new Intl.NumberFormat(language, {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: 4,
      maximumFractionDigits: 4,
    }).format(value);
  const date = (value: number) => new Date(value).toLocaleString(language);
  const formatTokenCount = (value: number) => formatTokens(value, language);
  const periodUnit = (unit?: string) =>
    ['minute', 'hour', 'day', 'week', 'month'].includes(unit ?? '')
      ? t(`self_usage.unit_${unit}`)
      : (unit ?? '');
  const [draft, setDraft] = useState('');
  const [key, setKey] = useState('');
  const [days, setDays] = useState(7);
  const [stats, setStats] = useState<Stats | null>(null);
  const [quota, setQuota] = useState<Quota | null>(null);
  const [events, setEvents] = useState<Events | null>(null);
  const [error, setError] = useState('');
  const [quotaError, setQuotaError] = useState('');
  const [live, setLive] = useState(false);
  const [history, setHistory] = useState(false);
  const [loading, setLoading] = useState(false);
  const refresh = useRef<() => void>(() => {});
  const pageController = useRef<AbortController | null>(null);
  const revision = useRef(0);
  const historyView = useRef(false);
  const acceptGeneration = useRef<(next?: number, fromStream?: boolean) => boolean>(() => true);

  useEffect(() => {
    const previous = document.title;
    document.title = `${t('self_usage.title')} · CLIProxyAPI Pro`;
    return () => {
      document.title = previous;
    };
  }, [t]);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    let streamController: AbortController | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let generation: number | undefined;
    let foreground = true;
    let loaded = false;
    historyView.current = false;
    refresh.current = () => {};
    acceptGeneration.current = () => true;
    revision.current += 1;
    pageController.current?.abort();
    setStats(null);
    setQuota(null);
    setEvents(null);
    if (key) setError('');
    setQuotaError('');
    setLive(false);
    setHistory(false);
    if (!key) {
      setLoading(false);
      return () => controller.abort();
    }
    const handleError = (cause: unknown) => {
      if (signal.aborted) return;
      if (cause instanceof QueryError && cause.status === 401) {
        setKey('');
        setError(cause.message);
        return;
      }
      setError(cause instanceof Error ? cause.message : 'self_usage.error_unavailable');
    };
    const queue = createSelfUsageRefresh(
      async (requestSignal, isCurrent) => {
        if (foreground || !loaded) setLoading(true);
        foreground = false;
        try {
          const [nextStats, nextEvents, nextQuota] = await Promise.allSettled([
            request<Stats>(key, `stats?days=${days}`, requestSignal),
            historyView.current
              ? Promise.resolve(null)
              : request<Events>(key, `events?days=${days}`, requestSignal),
            request<Quota>(key, 'quota', requestSignal),
          ]);
          if (signal.aborted || !isCurrent()) return;
          // Check every usage response before committing any part of the batch.
          for (const result of [nextStats, nextEvents]) {
            const nextGeneration =
              result.status === 'fulfilled'
                ? result.value?.generation
                : result.reason instanceof QueryError
                  ? result.reason.generation
                  : undefined;
            if (!acceptGeneration.current(nextGeneration)) return;
            if (
              result.status === 'rejected' &&
              result.reason instanceof QueryError &&
              result.reason.status === 409
            ) {
              void queue.refresh();
              return;
            }
          }
          setError('');
          if (nextStats.status === 'fulfilled') setStats(nextStats.value);
          else handleError(nextStats.reason);
          if (nextEvents.status === 'fulfilled') {
            if (nextEvents.value && !historyView.current) setEvents(nextEvents.value);
          } else handleError(nextEvents.reason);
          if (nextQuota.status === 'fulfilled') {
            setQuota(nextQuota.value);
            setQuotaError('');
          } else {
            setQuotaError('self_usage.quota_unavailable');
            if (nextQuota.reason instanceof QueryError && nextQuota.reason.status === 401)
              handleError(nextQuota.reason);
          }
        } finally {
          if (!signal.aborted && isCurrent()) {
            loaded = true;
            setLoading(false);
          }
        }
      },
      () => !document.hidden
    );

    const resetView = (nextGeneration?: number) => {
      if (nextGeneration !== undefined) generation = nextGeneration;
      queue.invalidate();
      revision.current += 1;
      pageController.current?.abort();
      historyView.current = false;
      setHistory(false);
      setStats(null);
      setEvents(null);
      setQuota(null);
      setError('');
      setQuotaError('');
      loaded = false;
      foreground = true;
      void queue.refresh(true);
    };
    acceptGeneration.current = (next, fromStream = false) => {
      if (!Number.isSafeInteger(next) || (next ?? 0) <= 0) return true;
      if (generation === undefined) {
        generation = next;
        return true;
      }
      if (next === generation) return true;
      // A delayed HTTP reply cannot roll back the generation established by SSE.
      if (fromStream || next! > generation) resetView(next);
      return false;
    };
    refresh.current = () => {
      queue.invalidate();
      revision.current += 1;
      pageController.current?.abort();
      historyView.current = false;
      setHistory(false);
      foreground = true;
      void queue.refresh(true);
    };
    const connect = async () => {
      if (signal.aborted || document.hidden) return;
      streamController?.abort();
      const current = new AbortController();
      streamController = current;
      try {
        const response = await fetch('./v0/self/stream', {
          headers: { Authorization: `Bearer ${key}` },
          signal: current.signal,
          cache: 'no-store',
          credentials: 'omit',
          redirect: 'error',
        });
        if (!response.ok) throw new QueryError(response.status);
        if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream'))
          throw new Error('self_usage.error_stream_unavailable');
        if (signal.aborted || current.signal.aborted) return;
        setLive(true);
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let pending = '';
        try {
          while (!signal.aborted && !current.signal.aborted) {
            const chunk = await reader.read();
            if (chunk.done) break;
            pending += decoder.decode(chunk.value, { stream: true });
            let end = pending.indexOf('\n\n');
            while (end >= 0) {
              const block = pending.slice(0, end);
              pending = pending.slice(end + 2);
              const notification = parseSelfUsageNotification(block);
              if (notification?.event === 'reset') {
                resetView(notification.generation);
              } else if (notification && acceptGeneration.current(notification.generation, true)) {
                void queue.refresh();
              }
              end = pending.indexOf('\n\n');
            }
            if (pending.length > 65536) throw new Error('self_usage.error_stream_response');
          }
        } finally {
          await reader.cancel().catch(() => {});
          reader.releaseLock();
        }
      } catch (cause) {
        if (!current.signal.aborted) handleError(cause);
      } finally {
        if (!signal.aborted && streamController === current) {
          setLive(false);
          if (!document.hidden)
            reconnectTimer = setTimeout(() => {
              void connect();
            }, 5000);
        }
      }
    };
    const visibility = () => {
      clearTimeout(reconnectTimer);
      if (document.hidden) {
        streamController?.abort();
        setLive(false);
      } else {
        void queue.refresh();
        void connect();
      }
    };
    document.addEventListener('visibilitychange', visibility);
    // Quota windows can recover without a new usage event.
    const stopPolling = startPolling(() => queue.refresh(), 30000);
    void queue.refresh();
    void connect();
    return () => {
      refresh.current = () => {};
      acceptGeneration.current = () => true;
      queue.dispose();
      controller.abort();
      streamController?.abort();
      pageController.current?.abort();
      clearTimeout(reconnectTimer);
      stopPolling();
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [key, days]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (draft.trim()) {
      setError('');
      setKey(draft.trim());
      setDraft('');
    }
  };
  const older = async () => {
    const last = events?.items[events.items.length - 1];
    if (!last || loading) return;
    historyView.current = true;
    setHistory(true);
    setLoading(true);
    const current = ++revision.current;
    const controller = new AbortController();
    pageController.current?.abort();
    pageController.current = controller;
    try {
      const page = await request<Events>(
        key,
        `events?days=${days}&before_ms=${last.timestampMs}&before_id=${last.id}`,
        controller.signal
      );
      if (
        !controller.signal.aborted &&
        revision.current === current &&
        acceptGeneration.current(page.generation)
      ) {
        setEvents(page);
        setError('');
      }
    } catch (cause) {
      if (!controller.signal.aborted && revision.current === current) {
        if (cause instanceof QueryError && cause.status === 409) {
          if (acceptGeneration.current(cause.generation)) refresh.current();
          return;
        }
        if (cause instanceof QueryError && cause.status === 401) setKey('');
        setError(cause instanceof Error ? cause.message : 'self_usage.error_unavailable');
      }
    } finally {
      if (!controller.signal.aborted && revision.current === current) setLoading(false);
    }
  };
  const budget = quota?.quota;
  const active = quota?.enforced && budget?.enabled;
  const metrics = budget
    ? [
        {
          label: t('self_usage.requests'),
          used: budget.usage.requestsUsed,
          limit: budget.requests,
          remaining: budget.usage.requestsRemaining,
          format: number,
        },
        {
          label: 'Token',
          used: budget.usage.totalTokensUsed,
          limit: budget.totalTokens,
          remaining: budget.usage.totalTokensRemaining,
          format: number,
        },
        {
          label: t('self_usage.cost_limit'),
          used: budget.usage.costUsed,
          limit: budget.cost,
          remaining: budget.usage.costRemaining,
          format: money,
        },
      ]
    : [];
  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <div>
          <span className={styles.eyebrow}>CLIProxyAPI Pro</span>
          <h1>{t('self_usage.title')}</h1>
          <p>{t('self_usage.subtitle')}</p>
        </div>
        <div className={styles.headerActions}>
          <select
            aria-label={t('language.switch')}
            value={language}
            onChange={(event) => setLanguage(event.target.value)}
          >
            {LANGUAGE_ORDER.map((value) => (
              <option key={value} value={value}>
                {t(LANGUAGE_LABEL_KEYS[value])}
              </option>
            ))}
          </select>
          {key && (
            <button
              onClick={() => {
                setKey('');
                setDraft('');
                setError('');
              }}
            >
              {t('self_usage.sign_out')}
            </button>
          )}
        </div>
      </header>
      {!key ? (
        <section className={styles.login}>
          <h2>{t('self_usage.enter_key')}</h2>
          <p>{t('self_usage.key_hint')}</p>
          <form onSubmit={submit}>
            <label htmlFor="query-key">API key</label>
            <input
              id="query-key"
              type="password"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder={t('self_usage.key_placeholder')}
              autoComplete="off"
              spellCheck={false}
              required
            />
            <button type="submit" disabled={!draft.trim()}>
              {t('self_usage.query')}
            </button>
          </form>
          <small>{t('self_usage.key_privacy')}</small>
        </section>
      ) : (
        <>
          <div className={styles.toolbar}>
            <span className={live ? styles.connected : styles.muted}>
              {live ? t('self_usage.live_connected') : t('self_usage.live_disconnected')}
              {history ? t('self_usage.history') : ''}
            </span>
            <div>
              <label htmlFor="query-days">{t('self_usage.range')}</label>
              <select
                id="query-days"
                value={days}
                onChange={(e) => setDays(Number(e.target.value))}
              >
                <option value={1}>{t('self_usage.range_day')}</option>
                <option value={7}>{t('self_usage.range_week')}</option>
                <option value={30}>{t('self_usage.range_month')}</option>
              </select>
              <button disabled={loading} onClick={() => refresh.current()}>
                {loading
                  ? t('self_usage.updating')
                  : history
                    ? t('self_usage.latest')
                    : t('self_usage.refresh')}
              </button>
            </div>
          </div>
          <section className={styles.section}>
            <div className={styles.title}>
              <h2>{t('self_usage.quota_title')}</h2>
              <span>
                {(quotaError ? t(quotaError) : '') ||
                  (!quota
                    ? t('self_usage.loading')
                    : !active
                      ? t('self_usage.quota_unlimited')
                      : quota.state === 'exhausted'
                        ? t('self_usage.quota_exhausted')
                        : quota.state === 'blocked'
                          ? t('self_usage.quota_blocked')
                          : t('self_usage.quota_available'))}
              </span>
            </div>
            {active && budget ? (
              <>
                <div className={styles.cards}>
                  {metrics.map((metric) => (
                    <article key={metric.label}>
                      <span>{metric.label}</span>
                      <strong>
                        {metric.remaining == null
                          ? t('self_usage.unlimited')
                          : metric.format(metric.remaining)}
                      </strong>
                      <small>
                        {t('self_usage.remaining_used', {
                          value: metric.format(metric.used),
                        })}
                        {metric.limit != null ? ` / ${metric.format(metric.limit)}` : ''}
                      </small>
                      {metric.limit != null && (
                        <progress
                          aria-label={t('self_usage.usage_ratio', {
                            metric: metric.label,
                          })}
                          value={metric.used}
                          max={Math.max(metric.limit, metric.used, 1)}
                        />
                      )}
                    </article>
                  ))}
                </div>
                <p className={styles.note}>
                  {budget.period.type === 'all_time'
                    ? t('self_usage.period_all_time')
                    : budget.period.type === 'past_duration'
                      ? t('self_usage.period_rolling', {
                          value: budget.period.value,
                          unit: periodUnit(budget.period.unit),
                        })
                      : `${t('self_usage.period_calendar', { value: budget.period.value, unit: periodUnit(budget.period.unit) })}${budget.period.timezone ? ` · ${budget.period.timezone}` : ''}`}
                  {quota?.nextRecoverAtMs
                    ? ` · ${t('self_usage.recovers_at', { time: date(quota.nextRecoverAtMs) })}`
                    : budget.period.type === 'calendar_duration' && budget.usage.windowEndsAtMs
                      ? ` · ${t('self_usage.period_ends', { time: date(budget.usage.windowEndsAtMs) })}`
                      : ''}
                </p>
              </>
            ) : (
              <p className={styles.empty}>
                {quotaError
                  ? t('self_usage.quota_retry')
                  : quota
                    ? t('self_usage.quota_none')
                    : t('self_usage.quota_loading')}
              </p>
            )}
          </section>
          <section className={styles.section}>
            <div className={styles.title}>
              <h2>{t('self_usage.stats_title')}</h2>
              <small>
                {stats
                  ? t('self_usage.updated_at', {
                      time: date(stats.snapshotAtMs),
                    })
                  : t('self_usage.loading')}
              </small>
            </div>
            <div className={styles.cards}>
              {[
                [
                  t('self_usage.requests'),
                  stats ? number(stats.total.totalRequests) : '—',
                  stats
                    ? t('self_usage.request_counts', {
                        success: number(stats.total.successCount),
                        failed: number(stats.total.failureCount),
                      })
                    : '',
                ],
                [
                  t('self_usage.total_tokens'),
                  stats ? number(stats.total.totalTokens) : '—',
                  stats
                    ? t('self_usage.token_counts', {
                        input: number(stats.total.inputTokens),
                        output: number(stats.total.outputTokens),
                      })
                    : '',
                ],
                [
                  t('self_usage.cost'),
                  stats ? money(stats.total.estimatedCost) : '—',
                  t('self_usage.cost_hint'),
                ],
              ].map(([label, value, detail]) => (
                <article key={label}>
                  <span>{label}</span>
                  <strong>{value}</strong>
                  <small>{detail}</small>
                </article>
              ))}
            </div>
            <p className={styles.note}>{t('self_usage.stats_note')}</p>
          </section>
          <section className={styles.section}>
            <div className={styles.title}>
              <h2>{t('self_usage.logs_title')}</h2>
              <small>
                {events
                  ? t('self_usage.updated_at', {
                      time: date(events.snapshotAtMs),
                    })
                  : t('self_usage.loading')}
              </small>
            </div>
            <div className={styles.table}>
              <table>
                <thead>
                  <tr>
                    <th>{t('self_usage.column_time')}</th>
                    <th>{t('self_usage.column_model')}</th>
                    <th>{t('self_usage.column_effort')}</th>
                    <th>{t('self_usage.column_status')}</th>
                    <th>{t('self_usage.column_tokens')}</th>
                    <th>{t('self_usage.column_cache')}</th>
                    <th>{t('self_usage.column_latency')}</th>
                    <th>{t('self_usage.cost')}</th>
                  </tr>
                </thead>
                <tbody>
                  {events?.items.map((item) => {
                    const effort = item.reasoningEffort?.trim();
                    const cachedTokens = Math.max(
                      item.cachedTokens ?? 0,
                      item.cacheReadTokens ?? 0
                    );
                    const hitRate = getCacheHitRate({
                      cachedTokens,
                      cacheInputTokens: item.cacheInputTokens ?? 0,
                    });
                    return (
                      <tr key={item.id}>
                        <td>{date(item.timestampMs)}</td>
                        <td>{item.model}</td>
                        <td>
                          {effort ? (
                            <span className={styles.effort}>{effort}</span>
                          ) : (
                            <span className={styles.muted}>—</span>
                          )}
                        </td>
                        <td>
                          <span className={item.failed ? styles.failed : styles.connected}>
                            {item.failed ? t('self_usage.failed') : t('self_usage.success')}
                            {item.statusCode ? ` ${item.statusCode}` : ''}
                          </span>
                        </td>
                        <td>
                          <div className={styles.tokenUsage}>
                            <strong>{formatTokenCount(item.totalTokens)}</strong>
                            <small>
                              {t('self_usage.token_counts', {
                                input: formatTokenCount(item.inputTokens),
                                output: formatTokenCount(item.outputTokens),
                              })}
                            </small>
                            {(item.reasoningTokens ?? 0) > 0 && (
                              <small>
                                {t('self_usage.reasoning_tokens', {
                                  value: formatTokenCount(item.reasoningTokens ?? 0),
                                })}
                              </small>
                            )}
                          </div>
                        </td>
                        <td>
                          <div className={styles.cacheUsage}>
                            <strong>{formatTokenCount(cachedTokens)}</strong>
                            <small>
                              {t('self_usage.cache_hit', {
                                value:
                                  hitRate === null
                                    ? '--'
                                    : new Intl.NumberFormat(language, {
                                        style: 'percent',
                                        minimumFractionDigits: 1,
                                        maximumFractionDigits: 1,
                                      }).format(hitRate),
                              })}
                            </small>
                          </div>
                        </td>
                        <td>
                          {item.latencyMs == null ? '—' : `${number(item.latencyMs)} ms`} /{' '}
                          {item.ttftMs == null ? '—' : `${number(item.ttftMs)} ms`}
                        </td>
                        <td>{item.estimatedCost == null ? '—' : money(item.estimatedCost)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {!events?.items.length && (
              <p className={styles.empty}>
                {events ? t('self_usage.logs_empty') : t('self_usage.logs_loading')}
              </p>
            )}
            <div className={styles.pager}>
              <small>{t('self_usage.logs_note')}</small>
              {events?.hasMore && (
                <button disabled={loading} onClick={() => void older()}>
                  {t('self_usage.older')}
                </button>
              )}
            </div>
          </section>
        </>
      )}
      {error && (
        <p role="alert" className={styles.error}>
          {t(error, { defaultValue: error })}
        </p>
      )}
      <footer className={styles.footer}>CLIProxyAPI Pro · {t('self_usage.read_only')}</footer>
    </main>
  );
}
