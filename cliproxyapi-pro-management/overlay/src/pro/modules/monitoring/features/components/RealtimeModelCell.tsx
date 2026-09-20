import type { TFunction } from 'i18next';
import { modelAuditItems, resolveModelAudit } from '../modelAudit';
import { buildRealtimeMetaText, type RealtimeLogRow } from '../realtimeLogPresentation';
import styles from '../monitoring.module.scss';

export function RealtimeModelCell({ row, t, onDetails }: { row: RealtimeLogRow; t: TFunction; onDetails: () => void }) {
  const audit = resolveModelAudit(row);
  const title = modelAuditItems(row, t).map(({ label, value }) => `${label}: ${value}`).join('\n');
  const flagged = audit.status === 'mismatch' || audit.status === 'variant';
  return (
    <div className={`${styles.primaryCell} ${styles.realtimeModelCell}`} title={title}>
      <button type="button" className={`${styles.monoCell} ${styles.modelAuditDetailsButton}`} onClick={onDetails} aria-label={t('monitoring.model_audit_title')}>{audit.requested}</button>
      {audit.showSent && <small className={styles.monoCell}>↳ {t('monitoring.model_upstream')}: {audit.sent}</small>}
      {audit.legacyModel && <small className={styles.monoCell}>{audit.legacyModel}</small>}
      {audit.showResponse && (
        <div className={audit.responseTone ? styles[`modelAudit${audit.responseTone}`] : undefined}>
          <small className={styles.monoCell}>↳ {t('monitoring.model_response')}: {audit.response}</small>
          {flagged && <span className={styles.modelAuditBadge}>{t(`monitoring.model_status_${audit.status}`)}</span>}
        </div>
      )}
      {buildRealtimeMetaText(row) && <small className={styles.monoCell}>{buildRealtimeMetaText(row)}</small>}
    </div>
  );
}
