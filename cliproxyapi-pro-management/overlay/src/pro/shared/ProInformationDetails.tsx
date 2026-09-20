import type { ReactNode } from 'react';
import styles from './ProInformationDetails.module.scss';

export type ProInformationDetailsTone = 'neutral' | 'good' | 'warning' | 'danger';

export interface ProInformationDetailItem {
  label: ReactNode;
  value: ReactNode;
  mono?: boolean;
  wide?: boolean;
}

export interface ProInformationDetailGroup {
  title: ReactNode;
  items: ProInformationDetailItem[];
}

interface ProInformationDetailsProps {
  className?: string;
  tone?: ProInformationDetailsTone;
  layout?: 'default' | 'compact';
  status: ReactNode;
  context?: ReactNode;
  summary: ReactNode;
  groups: ProInformationDetailGroup[];
  detailLabel?: ReactNode;
  detail?: ReactNode;
}

export function ProInformationDetails({
  className,
  tone = 'neutral',
  layout = 'default',
  status,
  context,
  summary,
  groups,
  detailLabel,
  detail,
}: ProInformationDetailsProps) {
  const visibleGroups = groups.filter((group) => group.items.length > 0);
  const rootClassName = [styles.details, styles[tone], layout === 'compact' ? styles.compact : '', className].filter(Boolean).join(' ');

  return (
    <div className={rootClassName}>
      <section className={styles.summary}>
        <div className={styles.summaryMeta}>
          <div className={styles.status}>{status}</div>
          {context ? <span className={styles.context}>{context}</span> : null}
        </div>
        <strong className={styles.summaryText}>{summary}</strong>
      </section>

      {visibleGroups.length > 0 ? (
        <div className={styles.groupGrid}>
          {visibleGroups.map((group, groupIndex) => (
            <section key={groupIndex} className={styles.group}>
              <h3>{group.title}</h3>
              <dl>
                {group.items.map((item, itemIndex) => (
                  <div key={itemIndex} className={[styles.item, item.wide ? styles.itemWide : ''].filter(Boolean).join(' ')}>
                    <dt>{item.label}</dt>
                    <dd className={item.mono === false ? undefined : styles.monoValue}>{item.value}</dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      ) : null}

      {detail ? (
        <section className={styles.detailBlock}>
          {detailLabel ? <h3>{detailLabel}</h3> : null}
          <div className={styles.detailContent}>{detail}</div>
        </section>
      ) : null}
    </div>
  );
}
