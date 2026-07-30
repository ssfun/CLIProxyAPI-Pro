import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconAlertTriangle, IconCheckCircle2, IconRefreshCw } from '@/components/ui/icons';
import type { ProxyPoolConfig, ProxyPoolSnapshot } from '@/services/api/proxyPool';
import { formatProxyPoolTime, maskProxyCredentials } from './proxyPoolUi';
import styles from './ProxyPool.module.scss';

interface ProxyPoolDiagnosticsProps {
  snapshot: ProxyPoolSnapshot;
  draft: ProxyPoolConfig;
  language: string;
  testing: boolean;
  onTestAll: () => void;
  onResetStats: () => void;
  onCopy: (value: string) => void;
}

export function ProxyPoolDiagnostics({
  snapshot,
  draft,
  language,
  testing,
  onTestAll,
  onResetStats,
  onCopy,
}: ProxyPoolDiagnosticsProps) {
  const { t } = useTranslation();
  const status = snapshot.status;
  const endpoint = status?.proxyUrl || `socks5://${draft.listen}`;
  const diagnosticText = useMemo(
    () =>
      JSON.stringify(
        {
          plugin: {
            discovered: snapshot.pluginDiscovered,
            enabled: snapshot.pluginEnabled,
            registered: snapshot.pluginRegistered,
          },
          takeoverActive: snapshot.takeoverActive,
          globalProxyUrl: maskProxyCredentials(snapshot.globalProxyUrl),
          endpoint,
          bypassCredentials: snapshot.bypassCredentials.map((item) => ({
            ...item,
            proxyUrl: maskProxyCredentials(item.proxyUrl),
          })),
          status,
        },
        null,
        2
      ),
    [endpoint, snapshot, status]
  );

  const checks = [
    {
      label: t('proxy_pool.plugin_discovered', { defaultValue: 'Bundled plugin discovered' }),
      good: snapshot.pluginDiscovered,
    },
    {
      label: t('proxy_pool.plugin_registered', { defaultValue: 'Plugin registered' }),
      good: snapshot.pluginRegistered,
    },
    {
      label: t('proxy_pool.listener_ready', { defaultValue: 'Internal listener ready' }),
      good: status?.ready === true,
    },
    {
      label: t('proxy_pool.global_proxy_consistent', {
        defaultValue: 'Global proxy matches listener',
      }),
      good: !snapshot.takeoverActive || snapshot.globalProxyUrl === endpoint,
    },
  ];

  return (
    <section className={styles.diagnosticsPanel}>
      <div className={styles.panelHeading}>
        <div>
          <h2>{t('proxy_pool.diagnostics', { defaultValue: 'Runtime diagnostics' })}</h2>
          <p>
            {t('proxy_pool.diagnostics_hint', {
              defaultValue:
                'Live plugin state and troubleshooting details. Runtime values refresh every 10 seconds.',
            })}
          </p>
        </div>
        <div className={styles.panelActions}>
          <Button variant="ghost" size="sm" onClick={() => onCopy(diagnosticText)}>
            {t('proxy_pool.copy_diagnostics', { defaultValue: 'Copy diagnostics' })}
          </Button>
          <Button variant="secondary" size="sm" onClick={onTestAll} loading={testing}>
            <IconRefreshCw size={16} />
            {t('proxy_pool.test_all', { defaultValue: 'Test all nodes' })}
          </Button>
        </div>
      </div>

      <div className={styles.diagnosticChecks}>
        {checks.map((check) => (
          <div key={check.label}>
            <span className={check.good ? styles.checkGood : styles.checkBad}>
              {check.good ? <IconCheckCircle2 size={18} /> : <IconAlertTriangle size={18} />}
            </span>
            <span>{check.label}</span>
            <strong>
              {check.good
                ? t('proxy_pool.pass', { defaultValue: 'Pass' })
                : t('proxy_pool.needs_attention', { defaultValue: 'Needs attention' })}
            </strong>
          </div>
        ))}
      </div>

      <div className={styles.diagnosticGrid}>
        <section>
          <h3>{t('proxy_pool.runtime_state', { defaultValue: 'Runtime state' })}</h3>
          <dl>
            <div>
              <dt>{t('proxy_pool.generation', { defaultValue: 'Configuration generation' })}</dt>
              <dd>{status?.generation ?? '-'}</dd>
            </div>
            <div>
              <dt>{t('proxy_pool.last_applied', { defaultValue: 'Last applied' })}</dt>
              <dd>{formatProxyPoolTime(status?.lastAppliedAt || '', language)}</dd>
            </div>
            <div>
              <dt>{t('proxy_pool.last_health_cycle', { defaultValue: 'Last health cycle' })}</dt>
              <dd>{formatProxyPoolTime(status?.lastHealthAt || '', language)}</dd>
            </div>
            <div>
              <dt>{t('proxy_pool.started_at', { defaultValue: 'Plugin started' })}</dt>
              <dd>{formatProxyPoolTime(status?.startedAt || '', language)}</dd>
            </div>
          </dl>
        </section>
        <section>
          <h3>{t('proxy_pool.network_path', { defaultValue: 'Network path' })}</h3>
          <dl>
            <div>
              <dt>{t('proxy_pool.listener', { defaultValue: 'Listener' })}</dt>
              <dd>
                <code>{endpoint}</code>
              </dd>
            </div>
            <div>
              <dt>{t('proxy_pool.global_proxy', { defaultValue: 'Global proxy' })}</dt>
              <dd>
                <code>{maskProxyCredentials(snapshot.globalProxyUrl) || '-'}</code>
              </dd>
            </div>
            <div>
              <dt>{t('proxy_pool.strategy', { defaultValue: 'Strategy' })}</dt>
              <dd>{status?.strategy || draft.strategy}</dd>
            </div>
            <div>
              <dt>{t('proxy_pool.active_tunnels', { defaultValue: 'Active tunnels' })}</dt>
              <dd>{status?.activeTunnels ?? 0}</dd>
            </div>
          </dl>
        </section>
      </div>

      {snapshot.bypassCredentials.length > 0 && (
        <details className={styles.bypassPanel}>
          <summary>
            {t('proxy_pool.bypass_warning', {
              defaultValue: '{{count}} credentials bypass this pool',
              count: snapshot.bypassCredentials.length,
            })}
          </summary>
          <div>
            {snapshot.bypassCredentials.map((item) => (
              <div key={`${item.name}-${item.proxyUrl}`}>
                <strong>{item.name}</strong>
                <span>{item.provider || '-'}</span>
                <code>{maskProxyCredentials(item.proxyUrl)}</code>
              </div>
            ))}
          </div>
        </details>
      )}

      {status?.lastError && (
        <section className={styles.runtimeError}>
          <h3>{t('proxy_pool.runtime_error', { defaultValue: 'Last runtime error' })}</h3>
          <pre>{status.lastError}</pre>
        </section>
      )}

      <div className={styles.diagnosticFooter}>
        <Button variant="ghost" size="sm" onClick={onResetStats}>
          {t('proxy_pool.reset_stats', { defaultValue: 'Reset runtime statistics' })}
        </Button>
      </div>
    </section>
  );
}
