import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconAlertTriangle } from '@/components/ui/icons';
import type { ProxyPoolConfig } from '@/services/api/proxyPool';
import {
  proxyPoolDurationValue,
  serializeProxyPoolDuration,
  type ProxyPoolDurationUnit,
} from './proxyPoolUi';
import styles from './ProxyPool.module.scss';

interface DurationInputProps {
  label: string;
  value: string;
  unit: ProxyPoolDurationUnit;
  unitLabel: string;
  fallback: number;
  disabled?: boolean;
  onChange: (value: string) => void;
}

const formatDurationNumber = (value: number): string => String(Math.round(value * 1000) / 1000);

function DurationInput({
  label,
  value,
  unit,
  unitLabel,
  fallback,
  disabled = false,
  onChange,
}: DurationInputProps) {
  const inputId = useId();
  const numericValue = proxyPoolDurationValue(value, unit) ?? fallback;
  const [text, setText] = useState(() => formatDurationNumber(numericValue));

  useEffect(() => {
    setText(formatDurationNumber(numericValue));
  }, [numericValue]);

  const commit = () => {
    const next = Number(text);
    if (!Number.isFinite(next) || next <= 0) {
      setText(formatDurationNumber(numericValue));
      return;
    }
    const normalized = Math.round(next * 1000) / 1000;
    setText(formatDurationNumber(normalized));
    if (Math.abs(normalized - numericValue) < 0.000001) return;
    onChange(serializeProxyPoolDuration(normalized, unit));
  };

  return (
    <div className="form-group">
      <label htmlFor={inputId}>{label}</label>
      <div className={styles.durationControl}>
        <input
          id={inputId}
          className="input"
          type="number"
          min="0.001"
          step="0.1"
          inputMode="decimal"
          value={text}
          disabled={disabled}
          onChange={(event) => setText(event.target.value)}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.key === 'Enter') event.currentTarget.blur();
          }}
        />
        <span aria-hidden="true">{unitLabel}</span>
      </div>
    </div>
  );
}

interface ProxyPoolSettingsProps {
  draft: ProxyPoolConfig;
  onChange: (config: ProxyPoolConfig) => void;
  onEnableFailOpen: () => void;
}

export function ProxyPoolSettings({ draft, onChange, onEnableFailOpen }: ProxyPoolSettingsProps) {
  const { t } = useTranslation();
  const patch = (value: Partial<ProxyPoolConfig>) => onChange({ ...draft, ...value });
  const patchHealth = (value: Partial<ProxyPoolConfig['healthCheck']>) =>
    onChange({ ...draft, healthCheck: { ...draft.healthCheck, ...value } });

  return (
    <section className={styles.settingsPanel}>
      <div className={styles.panelHeading}>
        <div>
          <h2>{t('proxy_pool.advanced_settings', { defaultValue: 'Advanced settings' })}</h2>
          <p>
            {t('proxy_pool.advanced_settings_hint', {
              defaultValue:
                'Defaults are suitable for most installations. Changes apply after saving.',
            })}
          </p>
        </div>
      </div>

      <div className={styles.settingsSection}>
        <div className={styles.settingsSectionHeading}>
          <h3>{t('proxy_pool.routing_behavior', { defaultValue: 'Routing behavior' })}</h3>
        </div>
        <div className={styles.settingsGrid}>
          <div className="form-group">
            <label>{t('proxy_pool.strategy', { defaultValue: 'Selection strategy' })}</label>
            <Select
              value={draft.strategy}
              onChange={(strategy) => patch({ strategy: strategy as ProxyPoolConfig['strategy'] })}
              options={[
                {
                  value: 'round-robin',
                  label: t('proxy_pool.strategy_round_robin', { defaultValue: 'Round robin' }),
                },
                {
                  value: 'weighted',
                  label: t('proxy_pool.strategy_weighted', { defaultValue: 'Weighted' }),
                },
                {
                  value: 'least-connections',
                  label: t('proxy_pool.strategy_least_connections', {
                    defaultValue: 'Least connections',
                  }),
                },
              ]}
            />
          </div>
          <Input
            label={t('proxy_pool.listen_address', { defaultValue: 'Listen address' })}
            value={draft.listen}
            onChange={(event) => patch({ listen: event.target.value })}
            placeholder="127.0.0.1:8318"
          />
          <DurationInput
            label={t('proxy_pool.dial_timeout', { defaultValue: 'Dial timeout' })}
            value={draft.dialTimeout}
            unit="s"
            unitLabel={t('proxy_pool.unit_seconds', { defaultValue: 'seconds' })}
            fallback={8}
            onChange={(dialTimeout) => patch({ dialTimeout })}
          />
          <Input
            type="number"
            min={1}
            label={t('proxy_pool.failover_attempts', { defaultValue: 'Max failover attempts' })}
            value={draft.maxFailoverAttempts}
            onChange={(event) =>
              patch({ maxFailoverAttempts: Math.max(1, Number(event.target.value) || 1) })
            }
          />
        </div>
      </div>

      <div className={styles.settingsSection}>
        <div className={styles.settingsSectionHeading}>
          <div>
            <h3>{t('proxy_pool.health_checks', { defaultValue: 'Health checks' })}</h3>
            <p>
              {t('proxy_pool.health_checks_hint', {
                defaultValue: 'Unhealthy nodes are temporarily isolated from new connections.',
              })}
            </p>
          </div>
          <ToggleSwitch
            checked={draft.healthCheck.enabled}
            onChange={(enabled) => patchHealth({ enabled })}
            ariaLabel={t('proxy_pool.health_checks', { defaultValue: 'Health checks' })}
          />
        </div>
        <div className={styles.settingsGrid}>
          <DurationInput
            label={t('proxy_pool.health_interval', { defaultValue: 'Check interval' })}
            value={draft.healthCheck.interval}
            unit="s"
            unitLabel={t('proxy_pool.unit_seconds', { defaultValue: 'seconds' })}
            fallback={30}
            onChange={(interval) => patchHealth({ interval })}
            disabled={!draft.healthCheck.enabled}
          />
          <DurationInput
            label={t('proxy_pool.health_timeout', { defaultValue: 'Check timeout' })}
            value={draft.healthCheck.timeout}
            unit="s"
            unitLabel={t('proxy_pool.unit_seconds', { defaultValue: 'seconds' })}
            fallback={8}
            onChange={(timeout) => patchHealth({ timeout })}
            disabled={!draft.healthCheck.enabled}
          />
          <Input
            type="number"
            min={1}
            label={t('proxy_pool.isolation_threshold', {
              defaultValue: 'Failures before isolation',
            })}
            value={draft.healthCheck.isolationThreshold}
            onChange={(event) =>
              patchHealth({ isolationThreshold: Math.max(1, Number(event.target.value) || 1) })
            }
            disabled={!draft.healthCheck.enabled}
          />
          <DurationInput
            label={t('proxy_pool.isolation_duration', { defaultValue: 'Isolation duration' })}
            value={draft.healthCheck.isolationDuration}
            unit="m"
            unitLabel={t('proxy_pool.unit_minutes', { defaultValue: 'minutes' })}
            fallback={5}
            onChange={(isolationDuration) => patchHealth({ isolationDuration })}
            disabled={!draft.healthCheck.enabled}
          />
          <Input
            label={t('proxy_pool.probe_target', { defaultValue: 'TCP probe target' })}
            value={draft.healthCheck.probeAddress}
            onChange={(event) => patchHealth({ probeAddress: event.target.value })}
            placeholder="www.gstatic.com:443"
            disabled={!draft.healthCheck.enabled}
          />
          <Input
            label={t('proxy_pool.test_url', { defaultValue: 'Exit-IP test URL' })}
            value={draft.healthCheck.testUrl}
            onChange={(event) => patchHealth({ testUrl: event.target.value })}
            placeholder="https://ipwho.is/"
          />
        </div>
      </div>

      <div className={styles.dangerZone}>
        <div>
          <IconAlertTriangle size={20} />
          <div>
            <h3>{t('proxy_pool.direct_fallback', { defaultValue: 'Direct fallback' })}</h3>
            <p>
              {t('proxy_pool.direct_fallback_hint', {
                defaultValue:
                  'Allows traffic to bypass the pool when every proxy is unavailable. This can expose the server IP.',
              })}
            </p>
          </div>
        </div>
        {draft.failOpen ? (
          <Button variant="danger" size="sm" onClick={() => patch({ failOpen: false })}>
            {t('proxy_pool.disable_fail_open', { defaultValue: 'Disable direct fallback' })}
          </Button>
        ) : (
          <Button variant="secondary" size="sm" onClick={onEnableFailOpen}>
            {t('proxy_pool.enable_fail_open', { defaultValue: 'Enable with confirmation' })}
          </Button>
        )}
      </div>
    </section>
  );
}
