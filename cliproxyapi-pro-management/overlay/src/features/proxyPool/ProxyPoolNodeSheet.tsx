import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Sheet } from '@/components/ui/Sheet';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconEye, IconEyeOff, IconRefreshCw } from '@/components/ui/icons';
import type {
  ProxyPoolNodeConfig,
  ProxyPoolNodeStatus,
  ProxyPoolProbeResult,
  ProxyPoolStrategy,
} from '@/services/api/proxyPool';
import {
  formatProxyPoolSuccessRate,
  formatProxyPoolTime,
  maskProxyCredentials,
  proxyPoolStateLabel,
} from './proxyPoolUi';
import styles from './ProxyPool.module.scss';

interface ProxyPoolNodeSheetProps {
  open: boolean;
  node: ProxyPoolNodeConfig | null;
  strategy: ProxyPoolStrategy;
  runtime?: ProxyPoolNodeStatus;
  probe?: ProxyPoolProbeResult;
  language: string;
  testing: boolean;
  recovering: boolean;
  onClose: () => void;
  onApply: (node: ProxyPoolNodeConfig) => void;
  onTest: (node: ProxyPoolNodeConfig) => void;
  onRecover: (nodeId: string) => void;
}

export function ProxyPoolNodeSheet({
  open,
  node,
  strategy,
  runtime,
  probe,
  language,
  testing,
  recovering,
  onClose,
  onApply,
  onTest,
  onRecover,
}: ProxyPoolNodeSheetProps) {
  const { t } = useTranslation();
  const [value, setValue] = useState<ProxyPoolNodeConfig | null>(node);
  const [showCredentials, setShowCredentials] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  useEffect(() => {
    setValue(node);
    setShowCredentials(false);
    setAdvancedOpen(false);
  }, [node, open]);

  if (!value) return null;
  const state = runtime?.state ?? (value.enabled ? 'unknown' : 'disabled');
  const displayUrl = showCredentials ? value.url : maskProxyCredentials(value.url);
  const hasCredentials = displayUrl !== value.url;

  return (
    <Sheet
      open={open}
      onClose={onClose}
      size="lg"
      eyebrow={t('proxy_pool.node_details', { defaultValue: 'Node details' })}
      title={
        value.label || value.id || t('proxy_pool.unnamed_node', { defaultValue: 'Unnamed node' })
      }
      description={t('proxy_pool.node_sheet_hint', {
        defaultValue:
          'Edit configuration and inspect the latest runtime result without leaving the node list.',
      })}
      footer={
        <div className={styles.sheetFooter}>
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            onClick={() => {
              onApply(value);
              onClose();
            }}
          >
            {t('proxy_pool.apply_node_changes', { defaultValue: 'Apply changes' })}
          </Button>
        </div>
      }
    >
      <div className={styles.sheetStatusLine}>
        <span className={`${styles.stateBadge} ${styles[`state_${state}`]}`}>
          {t(`proxy_pool.state_${state}`, { defaultValue: proxyPoolStateLabel(state) })}
        </span>
        <span>
          {runtime?.latencyMs
            ? `${runtime.latencyMs} ms`
            : t('proxy_pool.not_tested', { defaultValue: 'Not tested' })}
        </span>
        <span>
          {probe?.location || runtime?.location || probe?.exitIp || runtime?.exitIp || '-'}
        </span>
      </div>

      <div className={styles.sheetForm}>
        <Input
          label={t('proxy_pool.node_label', { defaultValue: 'Display name' })}
          value={value.label}
          onChange={(event) =>
            setValue((current) => (current ? { ...current, label: event.target.value } : current))
          }
          placeholder={t('proxy_pool.node_label_placeholder', {
            defaultValue: 'e.g. Tokyo primary',
          })}
        />
        <div className={styles.urlField}>
          <Input
            label={t('proxy_pool.proxy_url', { defaultValue: 'Proxy URL' })}
            value={displayUrl}
            readOnly={!showCredentials && hasCredentials}
            onChange={(event) =>
              setValue((current) => (current ? { ...current, url: event.target.value } : current))
            }
            className={styles.monospaceInput}
            placeholder="socks5://user:pass@host:1080"
            rightElement={
              hasCredentials ? (
                <button
                  type="button"
                  className={styles.iconButton}
                  onClick={() => setShowCredentials((current) => !current)}
                  aria-label={
                    showCredentials
                      ? t('proxy_pool.hide_credentials', { defaultValue: 'Hide proxy credentials' })
                      : t('proxy_pool.show_credentials', { defaultValue: 'Show proxy credentials' })
                  }
                >
                  {showCredentials ? <IconEyeOff size={16} /> : <IconEye size={16} />}
                </button>
              ) : undefined
            }
          />
          {!showCredentials && hasCredentials && (
            <button
              type="button"
              className={styles.textButton}
              onClick={() => setShowCredentials(true)}
            >
              {t('proxy_pool.reveal_to_edit', { defaultValue: 'Reveal credentials to edit' })}
            </button>
          )}
        </div>
        <ToggleSwitch
          checked={value.enabled}
          onChange={(enabled) =>
            setValue((current) => (current ? { ...current, enabled } : current))
          }
          label={t('proxy_pool.node_enabled', {
            defaultValue: 'Use this node for new connections',
          })}
        />
        {strategy === 'weighted' && (
          <Input
            type="number"
            min={1}
            label={t('proxy_pool.weight', { defaultValue: 'Weight' })}
            value={value.weight}
            onChange={(event) =>
              setValue((current) =>
                current
                  ? { ...current, weight: Math.max(1, Number(event.target.value) || 1) }
                  : current
              )
            }
          />
        )}

        <div className={styles.sheetActions}>
          <Button
            variant="secondary"
            onClick={() => onTest(value)}
            loading={testing}
            disabled={!value.url.trim()}
          >
            <IconRefreshCw size={16} />
            {t('proxy_pool.test_node', { defaultValue: 'Test node' })}
          </Button>
          {runtime?.state === 'isolated' && (
            <Button variant="ghost" onClick={() => onRecover(value.id)} loading={recovering}>
              {t('proxy_pool.recover', { defaultValue: 'Clear isolation' })}
            </Button>
          )}
        </div>

        {(probe || runtime) && (
          <section className={styles.testResult} aria-live="polite">
            <div className={styles.sectionHeading}>
              <div>
                <h3>{t('proxy_pool.latest_result', { defaultValue: 'Latest result' })}</h3>
                <p>{formatProxyPoolTime(probe?.checkedAt || runtime?.lastCheck || '', language)}</p>
              </div>
              <span
                className={
                  (probe?.success ?? runtime?.state === 'healthy')
                    ? styles.resultSuccess
                    : styles.resultFailure
                }
              >
                {(probe?.success ?? runtime?.state === 'healthy')
                  ? t('proxy_pool.test_passed', { defaultValue: 'Passed' })
                  : t('proxy_pool.test_failed', { defaultValue: 'Failed' })}
              </span>
            </div>
            <dl className={styles.detailGrid}>
              <div>
                <dt>{t('proxy_pool.latency', { defaultValue: 'Latency' })}</dt>
                <dd>
                  {probe?.latencyMs || runtime?.latencyMs
                    ? `${probe?.latencyMs || runtime?.latencyMs} ms`
                    : '-'}
                </dd>
              </div>
              <div>
                <dt>{t('proxy_pool.exit_ip', { defaultValue: 'Exit IP' })}</dt>
                <dd>{probe?.exitIp || runtime?.exitIp || '-'}</dd>
              </div>
              <div>
                <dt>{t('proxy_pool.location', { defaultValue: 'Location' })}</dt>
                <dd>{probe?.location || runtime?.location || '-'}</dd>
              </div>
              <div>
                <dt>{t('proxy_pool.success_rate', { defaultValue: 'Success rate' })}</dt>
                <dd>
                  {formatProxyPoolSuccessRate(
                    runtime?.successConnects ?? 0,
                    runtime?.totalConnects ?? 0
                  )}
                </dd>
              </div>
            </dl>
            {(probe?.error || runtime?.lastError) && (
              <pre className={styles.rawError}>{probe?.error || runtime?.lastError}</pre>
            )}
          </section>
        )}

        <details
          className={styles.advancedDetails}
          open={advancedOpen}
          onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
        >
          <summary>
            {t('proxy_pool.advanced_node_fields', { defaultValue: 'Advanced node fields' })}
          </summary>
          <div className={styles.advancedDetailsBody}>
            <Input
              label={t('proxy_pool.node_id', { defaultValue: 'Node ID' })}
              value={value.id}
              onChange={(event) =>
                setValue((current) => (current ? { ...current, id: event.target.value } : current))
              }
            />
            <Input
              type="number"
              label={t('proxy_pool.order', { defaultValue: 'Raw order' })}
              value={value.order}
              onChange={(event) =>
                setValue((current) =>
                  current ? { ...current, order: Number(event.target.value) || 0 } : current
                )
              }
            />
          </div>
        </details>
      </div>
    </Sheet>
  );
}
