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
    responseTone: !response ? '' : status === 'mismatch' || status === 'variant'
      ? status : response !== requested ? 'different' : '',
    unknownReason: !sent && !response ? 'missing_both' : !sent ? 'missing_sent' : !response ? 'missing_response' : 'unclassified',
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
    { key: 'requested', label: t('monitoring.model_requested'), value: audit.requested || missing },
    ...(row.effectiveModel ? [{ key: 'effective', label: t('monitoring.model_effective'), value: row.effectiveModel }] : []),
    { key: 'sent', label: t('monitoring.model_upstream'), value: audit.sent || missing },
    { key: 'response', label: t('monitoring.model_response'), value: audit.response || missing },
    { key: 'status', label: t('monitoring.model_audit_status'), value: audit.status === 'unknown'
      ? t(`monitoring.model_unknown_${audit.unknownReason}`) : t(`monitoring.model_status_${audit.status}`) },
  ];
};
