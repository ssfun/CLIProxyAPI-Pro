import type { TFunction } from 'i18next';
import type { MonitoringEventRow } from './hooks/useMonitoringData';

type ModelAuditRow = Pick<MonitoringEventRow, 'model' | 'modelAlias' | 'requestedModel' | 'effectiveModel' | 'upstreamModel' | 'responseModel' | 'modelMatchStatus'>;

export const resolveModelAudit = (row: ModelAuditRow) => {
  const requested = row.requestedModel?.trim() || row.modelAlias?.trim() || row.model;
  const sent = row.upstreamModel?.trim() || '';
  const response = row.responseModel?.trim() || '';
  const status = sent && response && ['match', 'variant', 'mismatch'].includes(row.modelMatchStatus || '')
    ? row.modelMatchStatus!
    : 'unknown';
  return {
    requested,
    sent,
    response,
    status,
    showSent: Boolean(sent && sent !== requested),
    showResponse: Boolean(response && response !== sent),
    // Legacy logs have no confirmed outbound model. Preserve their existing model information.
    legacyModel: !sent && row.model !== requested ? row.model : '',
  };
};

export const modelAuditItems = (row: ModelAuditRow, t: TFunction) => {
  const audit = resolveModelAudit(row);
  const missing = t('monitoring.model_not_recorded');
  return [
    { label: t('monitoring.model_requested'), value: audit.requested || missing },
    ...(row.effectiveModel ? [{ label: t('monitoring.model_effective'), value: row.effectiveModel }] : []),
    { label: t('monitoring.model_upstream'), value: audit.sent || missing },
    { label: t('monitoring.model_response'), value: audit.response || missing },
    { label: t('monitoring.model_audit_status'), value: t(`monitoring.model_status_${audit.status}`) },
  ];
};
