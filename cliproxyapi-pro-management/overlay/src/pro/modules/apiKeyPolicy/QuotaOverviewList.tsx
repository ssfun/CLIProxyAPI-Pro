import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Collapsible } from '@/components/ui/Collapsible';
import {
  formatAPIKeyPolicyTimestamp,
  type APIKeyPolicy,
  type APIKeyPolicyBinding,
  type APIKeyQuotaSummary,
} from './apiKeyPolicy';
import styles from './QuotaOverviewList.module.scss';

interface QuotaRow {
  binding: APIKeyPolicyBinding;
  policy: APIKeyPolicy;
  summary?: APIKeyQuotaSummary;
  visualState: string;
}

interface Props {
  rows: QuotaRow[];
  revealDisabled: boolean;
  busy: boolean;
  onEdit: (policy: APIKeyPolicy) => void;
  onReset: (policy: APIKeyPolicy) => void;
  onUsage?: (binding: APIKeyPolicyBinding) => void;
}

export function QuotaOverviewList({
  rows,
  revealDisabled,
  busy,
  onEdit,
  onReset,
  onUsage,
}: Props) {
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage ?? i18n.language;
  const copy = (key: string) => t(`api_key_policy.quota_overview.${key}`);
  const number = (value: number) =>
    new Intl.NumberFormat(language, {
      notation: Math.abs(value) >= 10000 ? 'compact' : 'standard',
      maximumFractionDigits: 1,
    }).format(value);
  const money = (value: number) =>
    value > 0 && value < 0.01 ? '< $0.01' : `$${value.toFixed(2)}`;
  const active = rows.filter((row) => row.visualState !== 'disabled');
  const disabled = rows.filter((row) => row.visualState === 'disabled');
  const ratio = (used: number, limit: number) =>
    limit > 0 ? Math.max(0, used / limit) : 0;

  const disabledRows = (
    <div className={styles.disabledList} role="list">
      {disabled.map(({ policy, binding }) => (
        <div className={styles.disabledRow} role="listitem" key={policy.id}>
          <div className={styles.identity}>
            <strong>{policy.displayName}</strong>
            <code>{binding.maskedKey}</code>
          </div>
          <span className={styles.muted}>
            {t('api_key_policy.quota_state.disabled')}
          </span>
          {onUsage ? (
            <Button variant="ghost" size="sm" onClick={() => onUsage(binding)}>
              {t('api_key_policy.view_usage')}
            </Button>
          ) : null}
          <Button variant="secondary" size="sm" onClick={() => onEdit(policy)}>
            {copy('configure')}
          </Button>
        </div>
      ))}
    </div>
  );

  return (
    <div className={styles.list}>
      {active.length > 0 ? (
        <div className={styles.grid} role="list" aria-label={copy('title')}>
          {active.map(({ binding, policy, summary, visualState }) => {
            const quota = summary?.quota;
            const period = quota?.period ?? policy.quota?.period;
            const timezone =
              period?.type === 'calendar_duration'
                ? (period.timezone ?? 'UTC')
                : undefined;
            const timestamp = (value: number) =>
              formatAPIKeyPolicyTimestamp(value, language, timezone);
            const metrics = quota
              ? [
                  {
                    key: 'cost',
                    used: quota.usage.costUsed,
                    limit: quota.cost,
                    format: money,
                  },
                  {
                    key: 'requests',
                    used: quota.usage.requestsUsed,
                    limit: quota.requests,
                    format: number,
                  },
                  {
                    key: 'tokens',
                    used: quota.usage.totalTokensUsed,
                    limit: quota.totalTokens,
                    format: number,
                  },
                ]
              : [];
            const limited = metrics
              .flatMap((metric) =>
                metric.limit === undefined
                  ? []
                  : [{ ...metric, limit: metric.limit }],
              )
              .sort((a, b) => ratio(b.used, b.limit) - ratio(a.used, a.limit));
            const unlimited = metrics.filter(
              (metric) => metric.limit === undefined,
            );
            return (
              <article
                role="listitem"
                key={policy.id}
                aria-label={policy.displayName}
              >
                <Card className={styles.card}>
                  <header className={styles.header}>
                    <div className={styles.identity}>
                      <h3>{policy.displayName}</h3>
                      <code>{binding.maskedKey}</code>
                    </div>
                    <span
                      className={`${styles.state} ${styles[visualState] ?? ''}`}
                    >
                      {t(`api_key_policy.quota_state.${visualState}`)}
                    </span>
                  </header>
                  {summary?.blockedReason ? (
                    <p className={styles.blockedReason}>
                      {t(`api_key_policy.quota_block.${summary.blockedReason}`)}
                    </p>
                  ) : null}
                  <div className={styles.limits}>
                    {!quota ? (
                      <p className={styles.muted}>
                        {copy('snapshot_unavailable')}
                      </p>
                    ) : limited.length === 0 ? (
                      <p className={styles.noLimit}>{copy('unlimited')}</p>
                    ) : (
                      limited.map(({ key, used, limit, format }, index) => {
                        const fraction = ratio(used, limit);
                        return (
                          <div
                            className={`${styles.metric} ${index === 0 ? styles.primary : ''}`}
                            key={key}
                          >
                            <div className={styles.metricTitle}>
                              <span>{copy(key)}</span>
                              <strong>
                                {t('api_key_policy.quota_overview.remaining', {
                                  value: format(Math.max(limit - used, 0)),
                                })}
                              </strong>
                            </div>
                            <div
                              className={`${styles.progress} ${fraction >= 1 ? styles.exhausted : fraction >= 0.8 ? styles.warning : ''}`}
                              role="progressbar"
                              aria-label={copy(key)}
                              aria-valuemin={0}
                              aria-valuemax={100}
                              aria-valuenow={Math.min(fraction * 100, 100)}
                            >
                              <span
                                style={{
                                  width: `${Math.min(fraction * 100, 100)}%`,
                                }}
                              />
                            </div>
                            <div className={styles.metricCaption}>
                              <span>
                                {t('api_key_policy.quota_overview.used_limit', {
                                  used: format(used),
                                  limit: format(limit),
                                })}
                              </span>
                              <span>{number(fraction * 100)}%</span>
                            </div>
                          </div>
                        );
                      })
                    )}
                  </div>
                  {unlimited.length > 0 ? (
                    <div className={styles.unlimitedUsage}>
                      <div>
                        {unlimited.map(({ key, used, format }) => (
                          <span key={key}>
                            {copy(key)} <strong>{format(used)}</strong>
                          </span>
                        ))}
                      </div>
                      <small>
                        {unlimited.map(({ key }) => copy(key)).join(' / ')} ·{' '}
                        {copy('unlimited')}
                      </small>
                    </div>
                  ) : null}
                  <div className={styles.metadata}>
                    {period ? (
                      <div className={styles.period}>
                        <span>
                          {period.type === 'past_duration'
                            ? t(
                                'api_key_policy.quota_overview.rolling_period',
                                {
                                  value: period.value,
                                  unit: t(
                                    `api_key_policy.quota_unit.${period.unit}`,
                                  ),
                                },
                              )
                            : period.type === 'calendar_duration'
                              ? t(
                                  `api_key_policy.quota_calendar_unit.${period.unit}`,
                                )
                              : t('api_key_policy.quota_period.all_time')}
                        </span>
                        {period.type === 'all_time' ? (
                          <span>{copy('no_auto_reset')}</span>
                        ) : (
                          <div />
                        )}
                        {period.type === 'calendar_duration' &&
                        quota?.usage.windowEndsAtMs ? (
                          <span>
                            {t('api_key_policy.quota_overview.resets_at', {
                              time: timestamp(quota.usage.windowEndsAtMs),
                            })}
                          </span>
                        ) : (
                          <div />
                        )}
                        {timezone ? <span>{timezone}</span> : null}
                        {summary?.nextRecoverAtMs ? (
                          <span>
                            {t('api_key_policy.quota_overview.recovers_at', {
                              time: timestamp(summary.nextRecoverAtMs),
                            })}
                          </span>
                        ) : (
                          <div />
                        )}
                      </div>
                    ) : (
                      <div />
                    )}
                    {quota ? (
                      <Collapsible
                        className={styles.details}
                        label={copy('usage_details')}
                      >
                        <dl className={styles.exactUsage}>
                          {metrics.map(({ key, used, limit }) => (
                            <div key={key}>
                              <dt>{copy(key)}</dt>
                              <dd>
                                {key === 'cost'
                                  ? `$${used.toFixed(6)}`
                                  : new Intl.NumberFormat(language).format(
                                      used,
                                    )}{' '}
                                /{' '}
                                {limit === undefined
                                  ? copy('unlimited')
                                  : key === 'cost'
                                    ? `$${limit}`
                                    : new Intl.NumberFormat(language).format(
                                        limit,
                                      )}
                              </dd>
                            </div>
                          ))}
                        </dl>
                      </Collapsible>
                    ) : (
                      <div />
                    )}
                  </div>
                  <footer className={styles.actions}>
                    {onUsage ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => onUsage(binding)}
                      >
                        {t('api_key_policy.view_usage')}
                      </Button>
                    ) : null}
                    <div className={styles.editActions}>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => onEdit(policy)}
                      >
                        {copy('edit')}
                      </Button>
                      {quota?.enabled ? (
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => onReset(policy)}
                          disabled={busy}
                        >
                          {t('api_key_policy.quota_reset')}
                        </Button>
                      ) : null}
                    </div>
                  </footer>
                </Card>
              </article>
            );
          })}
        </div>
      ) : null}
      {disabled.length > 0 ? (
        revealDisabled ? (
          disabledRows
        ) : (
          <Collapsible
            label={t('api_key_policy.quota_overview.disabled_group', {
              count: disabled.length,
            })}
            flush
          >
            {disabledRows}
          </Collapsible>
        )
      ) : null}
    </div>
  );
}
