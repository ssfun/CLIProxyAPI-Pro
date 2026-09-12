import { useEffect, useRef, useState, type FormEvent } from 'react';
import styles from './SelfUsagePage.module.scss';
import { formatTokenCount, getCacheHitRate } from './selfUsagePresentation';
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
type Events = { items: Event[]; hasMore: boolean; snapshotAtMs: number; generation?: number };
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
const number = (value: number) =>
  new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value);
const money = (value: number) => `$${value.toFixed(4)}`;
const date = (value: number) => new Date(value).toLocaleString();
const periodUnit = (unit?: string) =>
  (
    ({ minute: '分钟', hour: '小时', day: '天', week: '周', month: '月' }) as Record<string, string>
  )[unit ?? ''] ??
  unit ??
  '';

class QueryError extends Error {
  constructor(
    public status: number,
    public generation?: number
  ) {
    super(
      status === 401
        ? 'API key 无效或已被移除，请重新输入。'
        : status === 404
          ? '当前服务尚未提供公开查询接口，请更新 Core。'
          : status === 429
            ? '查询繁忙，请稍后重试。'
            : '暂时无法读取数据，请稍后重试。'
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
    document.title = '密钥用量查询 · CLIProxyAPI Pro';
    return () => {
      document.title = previous;
    };
  }, []);

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
      setError(cause instanceof Error ? cause.message : '查询失败');
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
            setQuotaError('配额暂时不可用');
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
          throw new Error('实时连接不可用');
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
            if (pending.length > 65536) throw new Error('实时连接响应异常');
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
        setError(cause instanceof Error ? cause.message : '查询失败');
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
          label: '请求次数',
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
          label: '费用额度',
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
          <h1>密钥用量查询</h1>
          <p>查询自己的配额、使用统计与实时请求记录</p>
        </div>
        {key && (
          <button
            onClick={() => {
              setKey('');
              setDraft('');
              setError('');
            }}
          >
            退出查询
          </button>
        )}
      </header>
      {!key ? (
        <section className={styles.login}>
          <h2>输入 API key</h2>
          <p>仅显示此密钥的数据，无需管理权限。</p>
          <form onSubmit={submit}>
            <label htmlFor="query-key">API key</label>
            <input
              id="query-key"
              type="password"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder="请输入你的 API key"
              autoComplete="off"
              spellCheck={false}
              required
            />
            <button type="submit" disabled={!draft.trim()}>
              查询用量 →
            </button>
          </form>
          <small>密钥仅用于当前查询，不保存在浏览器中。</small>
        </section>
      ) : (
        <>
          <div className={styles.toolbar}>
            <span className={live ? styles.connected : styles.muted}>
              {live ? '● 实时已连接' : '○ 实时未连接'}
              {history ? ' · 正在查看历史页' : ''}
            </span>
            <div>
              <label htmlFor="query-days">统计范围</label>
              <select
                id="query-days"
                value={days}
                onChange={(e) => setDays(Number(e.target.value))}
              >
                <option value={1}>最近 24 小时</option>
                <option value={7}>最近 7 天</option>
                <option value={30}>最近 30 天</option>
              </select>
              <button disabled={loading} onClick={() => refresh.current()}>
                {loading ? '更新中…' : history ? '返回最新' : '刷新'}
              </button>
            </div>
          </div>
          <section className={styles.section}>
            <div className={styles.title}>
              <h2>配额概览</h2>
              <span>
                {quotaError ||
                  (!quota
                    ? '加载中…'
                    : !active
                      ? '未启用额度限制'
                      : quota.state === 'exhausted'
                        ? '额度已耗尽'
                        : quota.state === 'blocked'
                          ? '结算暂不可用'
                          : '额度可用')}
              </span>
            </div>
            {active && budget ? (
              <>
                <div className={styles.cards}>
                  {metrics.map((metric) => (
                    <article key={metric.label}>
                      <span>{metric.label}</span>
                      <strong>
                        {metric.remaining == null ? '未设置上限' : metric.format(metric.remaining)}
                      </strong>
                      <small>
                        剩余 · 已用 {metric.format(metric.used)}
                        {metric.limit != null ? ` / ${metric.format(metric.limit)}` : ''}
                      </small>
                      {metric.limit != null && (
                        <progress
                          aria-label={`${metric.label}使用比例`}
                          value={metric.used}
                          max={Math.max(metric.limit, metric.used, 1)}
                        />
                      )}
                    </article>
                  ))}
                </div>
                <p className={styles.note}>
                  {budget.period.type === 'all_time'
                    ? '累计配额'
                    : budget.period.type === 'past_duration'
                      ? `滚动窗口：${budget.period.value} ${periodUnit(budget.period.unit)}`
                      : `日历周期：${budget.period.value} ${periodUnit(budget.period.unit)}${budget.period.timezone ? ` · ${budget.period.timezone}` : ''}`}
                  {quota?.nextRecoverAtMs
                    ? ` · 预计恢复 ${date(quota.nextRecoverAtMs)}`
                    : budget.period.type === 'calendar_duration' && budget.usage.windowEndsAtMs
                      ? ` · 周期结束 ${date(budget.usage.windowEndsAtMs)}`
                      : ''}
                </p>
              </>
            ) : (
              <p className={styles.empty}>
                {quotaError
                  ? '请稍后刷新配额。'
                  : quota
                    ? '该密钥当前没有生效的额度限制。'
                    : '正在读取配额…'}
              </p>
            )}
          </section>
          <section className={styles.section}>
            <div className={styles.title}>
              <h2>使用统计</h2>
              <small>{stats ? `更新于 ${date(stats.snapshotAtMs)}` : '加载中…'}</small>
            </div>
            <div className={styles.cards}>
              {[
                [
                  '请求次数',
                  stats ? number(stats.total.totalRequests) : '—',
                  stats
                    ? `成功 ${number(stats.total.successCount)} · 失败 ${number(stats.total.failureCount)}`
                    : '',
                ],
                [
                  '总 Token',
                  stats ? number(stats.total.totalTokens) : '—',
                  stats
                    ? `输入 ${number(stats.total.inputTokens)} · 输出 ${number(stats.total.outputTokens)}`
                    : '',
                ],
                [
                  '估算费用',
                  stats ? money(stats.total.estimatedCost) : '—',
                  '按已保存的模型定价计算',
                ],
              ].map(([label, value, detail]) => (
                <article key={label}>
                  <span>{label}</span>
                  <strong>{value}</strong>
                  <small>{detail}</small>
                </article>
              ))}
            </div>
            <p className={styles.note}>
              统计基于所选时间范围内保留的请求记录，可能与配额结算累计值不同。费用为估算值。
            </p>
          </section>
          <section className={styles.section}>
            <div className={styles.title}>
              <h2>请求日志</h2>
              <small>{events ? `更新于 ${date(events.snapshotAtMs)}` : '加载中…'}</small>
            </div>
            <div className={styles.table}>
              <table>
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>模型</th>
                    <th>推理强度</th>
                    <th>状态</th>
                    <th>词元</th>
                    <th>读缓存</th>
                    <th>耗时 / 首字</th>
                    <th>估算费用</th>
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
                            {item.failed ? '失败' : '成功'}
                            {item.statusCode ? ` ${item.statusCode}` : ''}
                          </span>
                        </td>
                        <td>
                          <div className={styles.tokenUsage}>
                            <strong>{formatTokenCount(item.totalTokens)}</strong>
                            <small>
                              输入 {formatTokenCount(item.inputTokens)} · 输出{' '}
                              {formatTokenCount(item.outputTokens)}
                            </small>
                            {(item.reasoningTokens ?? 0) > 0 && (
                              <small>推理 {formatTokenCount(item.reasoningTokens ?? 0)}</small>
                            )}
                          </div>
                        </td>
                        <td>
                          <div className={styles.cacheUsage}>
                            <strong>{formatTokenCount(cachedTokens)}</strong>
                            <small>
                              {hitRate === null ? '--' : `${(hitRate * 100).toFixed(1)}%`} 命中
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
                {events ? '所选时间范围内暂无请求记录' : '正在读取请求记录…'}
              </p>
            )}
            <div className={styles.pager}>
              <small>请求完成并记录用量后显示，每页最多 50 条。</small>
              {events?.hasMore && (
                <button disabled={loading} onClick={() => void older()}>
                  更早记录 →
                </button>
              )}
            </div>
          </section>
        </>
      )}
      {error && (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      )}
      <footer className={styles.footer}>CLIProxyAPI Pro · 只读查询</footer>
    </main>
  );
}
