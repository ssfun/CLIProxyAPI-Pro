import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { ProPagination } from '@/pro/shared/ProPagination';
import { type AccountInspectionBackendResultItem, type AccountInspectionResultItem } from './features/accountInspection';
import { accountInspectionApi, type AccountInspectionHistoryResponse, type AccountInspectionOperationsResponse } from './api';
import styles from './features/accountInspection.module.scss';

type HistoryFilter = 'all' | 'parent' | 'run';
const RECORD_PAGE_SIZE = 20;
const KNOWN_OPERATION_ACTIONS = new Set([
  'inspect', 'quota_protection', 'quota_recovery', 'admin_disable', 'admin_enable',
  'delete', 'recovery_check', 'unknown',
]);

type RestrictionEvidence = { source: string; model?: string; revision?: string; active: boolean };

function RestrictionSnapshot({ restriction }: { restriction: RestrictionEvidence }) {
  const { t } = useTranslation();
  return <p className={styles.recordEvidence}>{t('monitoring.account_inspection_operation_restriction_scope', {
    source: restriction.source,
    model: restriction.model || '-',
    revision: restriction.revision || '-',
    state: t(restriction.active
      ? 'monitoring.account_inspection_operation_restriction_active'
      : 'monitoring.account_inspection_operation_restriction_cleared'),
  })}</p>;
}

const formatRecordTime = (timestamp: number | undefined, language: string, missing: string) =>
  timestamp && Number.isFinite(timestamp) ? new Date(timestamp).toLocaleString(language) : missing;

function EvidenceSnapshot({ item }: { item: Partial<AccountInspectionBackendResultItem> }) {
  const { t, i18n } = useTranslation();
  return (
    <div className={styles.recordEvidence}>
      <span>{t('monitoring.account_inspection_observed_at')}: {formatRecordTime(item.observedAt, i18n.language, t('monitoring.account_inspection_observation_not_recorded'))}</span>
      <span>{t('monitoring.account_inspection_next_action')}: {item.action ? t(`monitoring.account_inspection_action_${item.action}`) : '-'}</span>
      <span>{t('monitoring.account_inspection_http_status')}: {item.statusCode ?? '-'}</span>
      <span>{t('monitoring.account_inspection_error_code')}: {item.errorCode || '-'}</span>
      <span>{t('monitoring.account_inspection_used_percent')}: {item.usedPercent !== null && item.usedPercent !== undefined ? `${item.usedPercent}%` : '-'}</span>
      <span>{t('monitoring.account_inspection_enabled_status')}: {item.disabled === true ? t('monitoring.account_inspection_action_disable') : item.disabled === false ? t('monitoring.account_inspection_action_enable') : '-'}</span>
      {item.actionReason ? <span>{t('monitoring.account_inspection_reason')}: {item.actionReason}</span> : null}
      {item.resultRef ? <span>{t('monitoring.account_inspection_result_ref')}: {item.resultRef}</span> : null}
      {item.registrationEpoch ? <span>{t('monitoring.account_inspection_registration_epoch')}: {item.registrationEpoch}</span> : null}
      {item.runId ? <span>{t('monitoring.account_inspection_run_id')}: {item.runId}</span> : null}
      {item.parentResultRef ? <span>{t('monitoring.account_inspection_parent_result_ref')}: {item.parentResultRef}</span> : null}
    </div>
  );
}

export function InspectionRecordsPanel({ item }: { item: AccountInspectionResultItem }) {
  const { t, i18n } = useTranslation();
  const [historyFilter, setHistoryFilter] = useState<HistoryFilter>('all');
  const [historyPage, setHistoryPage] = useState(1);
  const [operationPage, setOperationPage] = useState(1);
  const [history, setHistory] = useState<AccountInspectionHistoryResponse | null>(null);
  const [operations, setOperations] = useState<AccountInspectionOperationsResponse | null>(null);
  const [historyLoading, setHistoryLoading] = useState(true);
  const [operationsLoading, setOperationsLoading] = useState(true);
  const [historyError, setHistoryError] = useState('');
  const [operationsError, setOperationsError] = useState('');
  const [historyRetry, setHistoryRetry] = useState(0);
  const [operationsRetry, setOperationsRetry] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setHistoryLoading(true);
    setHistoryError('');
    setHistory(null);
    void accountInspectionApi.getHistory({
      key: item.key,
      page: historyPage,
      pageSize: RECORD_PAGE_SIZE,
      ...(historyFilter === 'parent' && item.parentResultRef ? { resultRef: item.parentResultRef } : {}),
      ...(historyFilter === 'run' && item.runId ? { runId: item.runId } : {}),
    }, controller.signal).then((response) => {
      if (!controller.signal.aborted) setHistory(response);
    }).catch((error) => {
      if (!controller.signal.aborted) setHistoryError(error instanceof Error ? error.message : String(error));
    }).finally(() => {
      if (!controller.signal.aborted) setHistoryLoading(false);
    });
    return () => controller.abort();
  }, [historyFilter, historyPage, historyRetry, item.key, item.parentResultRef, item.runId]);

  useEffect(() => {
    const controller = new AbortController();
    setOperationsLoading(true);
    setOperationsError('');
    setOperations(null);
    void accountInspectionApi.getOperations({ key: item.key, page: operationPage, pageSize: RECORD_PAGE_SIZE }, controller.signal)
      .then((response) => { if (!controller.signal.aborted) setOperations(response); })
      .catch((error) => { if (!controller.signal.aborted) setOperationsError(error instanceof Error ? error.message : String(error)); })
      .finally(() => { if (!controller.signal.aborted) setOperationsLoading(false); });
    return () => controller.abort();
  }, [item.key, operationPage, operationsRetry]);

  return (
    <div className={styles.inspectionRecords}>
      <section className={styles.recordSection} aria-label={t('monitoring.account_inspection_history_title')}>
        <div className={styles.recordSectionHeader}>
          <h3>{t('monitoring.account_inspection_history_title')}</h3>
          <div className={styles.recordFilters}>
            <Button size="sm" variant={historyFilter === 'all' ? 'primary' : 'secondary'} onClick={() => { setHistoryFilter('all'); setHistoryPage(1); }}>
              {t('monitoring.account_inspection_history_all')}
            </Button>
            {item.parentResultRef ? <Button size="sm" variant={historyFilter === 'parent' ? 'primary' : 'secondary'} onClick={() => { setHistoryFilter('parent'); setHistoryPage(1); }}>
              {t('monitoring.account_inspection_history_parent')}
            </Button> : null}
            {item.runId ? <Button size="sm" variant={historyFilter === 'run' ? 'primary' : 'secondary'} onClick={() => { setHistoryFilter('run'); setHistoryPage(1); }}>
              {t('monitoring.account_inspection_history_run')}
            </Button> : null}
          </div>
        </div>
        <p className={styles.recordRetention}>{t('monitoring.account_inspection_history_retention', { count: history?.retention.maxResults ?? 1000 })}</p>
        {historyError ? <div role="alert" className={styles.recordError}>{historyError} <Button size="sm" variant="secondary" onClick={() => setHistoryRetry((value) => value + 1)}>{t('monitoring.account_inspection_retry_load')}</Button></div> : null}
        {historyLoading ? <p>{t('common.loading')}</p> : history?.items.length ? (
          <div className={styles.recordList}>
            {history.items.map((entry, index) => {
              return <details key={entry.resultRef || `${entry.key}:${entry.observedAt}:${index}`} className={styles.recordItem}>
                <summary>
                  <strong>{formatRecordTime(entry.observedAt, i18n.language, t('monitoring.account_inspection_observation_not_recorded'))}</strong>
                  <span>{entry.parentResultRef
                    ? t('monitoring.account_inspection_observation_recheck')
                    : entry.runId ? t('monitoring.account_inspection_observation_full_run') : t('monitoring.account_inspection_observation_not_recorded')}</span>
                  <span>{entry.action ? t(`monitoring.account_inspection_action_${entry.action}`) : t('monitoring.account_inspection_record_evidence')}</span>
                </summary>
                <EvidenceSnapshot item={entry} />
              </details>;
            })}
          </div>
        ) : !historyError ? <p>{t('monitoring.account_inspection_history_empty')}</p> : null}
        {history && history.pageInfo.total > RECORD_PAGE_SIZE ? <ProPagination
          page={historyPage}
          pageSize={RECORD_PAGE_SIZE}
          pageSizeOptions={[RECORD_PAGE_SIZE]}
          total={history.pageInfo.total}
          onPageChange={setHistoryPage}
          onPageSizeChange={() => undefined}
          idPrefix="account-inspection-history"
          disabled={historyLoading}
        /> : null}
      </section>

      <section className={styles.recordSection} aria-label={t('monitoring.account_inspection_operations_title')}>
        <div className={styles.recordSectionHeader}><h3>{t('monitoring.account_inspection_operations_title')}</h3></div>
        <p className={styles.recordRetention}>{t('monitoring.account_inspection_operations_retention', { count: operations?.retention.maxOperations ?? 256 })}</p>
        {operationsError ? <div role="alert" className={styles.recordError}>{operationsError} <Button size="sm" variant="secondary" onClick={() => setOperationsRetry((value) => value + 1)}>{t('monitoring.account_inspection_retry_load')}</Button></div> : null}
        {operationsLoading ? <p>{t('common.loading')}</p> : operations?.items.length ? (
          <div className={styles.recordList}>
            {operations.items.map((record) => <details key={record.operationId} className={styles.recordItem}>
              <summary>
                <strong>{record.effect === 'token_refresh' || record.action === 'token_refresh' || record.action === 'refresh'
                  ? t('monitoring.account_inspection_operation_refresh')
                  : record.effect === 'manual_release_inspection' || record.effect === 'manual_release_upstream'
                    ? t('routing_policy.recovery.manual_title')
                  : record.effect === 'recover' || record.action === 'recover'
                    ? t('monitoring.account_inspection_batch_group_recovery_check')
                    : KNOWN_OPERATION_ACTIONS.has(record.effect || record.action)
                      ? t(`monitoring.account_inspection_batch_group_${record.effect || record.action}`)
                      : t(`monitoring.account_inspection_operation_source_${record.source}`)}</strong>
                <span>{t(`monitoring.account_inspection_operation_source_${record.source}`)}</span>
                <span>{t(`monitoring.account_inspection_operation_status_${record.status}`)}</span>
                <time>{formatRecordTime(record.startedAt, i18n.language, t('monitoring.account_inspection_observation_not_recorded'))}</time>
              </summary>
              <div className={styles.recordOperationDetail}>
                {record.finishedAt ? <p>{t('monitoring.account_inspection_operation_finished_at')}: {formatRecordTime(record.finishedAt, i18n.language, '-')}</p> : null}
                {record.batchOperationId ? <p>{t('monitoring.account_inspection_operation_batch_id')}: {record.batchOperationId}</p> : null}
                {record.error ? <p className={styles.inspectionStatusError}>{record.error}</p> : null}
                <strong>{t('monitoring.account_inspection_operation_before')}</strong>
                {record.restrictionBefore ? <RestrictionSnapshot restriction={record.restrictionBefore} /> : null}
                {record.before?.resultRef || record.before?.key
                  ? <EvidenceSnapshot item={record.before} /> : <p>{t('monitoring.account_inspection_record_evidence_missing')}</p>}
                <strong>{t('monitoring.account_inspection_operation_after')}</strong>
                {record.restrictionAfter ? <RestrictionSnapshot restriction={record.restrictionAfter} /> : null}
                {record.after?.resultRef || record.after?.key
                  ? <EvidenceSnapshot item={record.after} /> : <p>{t('monitoring.account_inspection_operation_after_missing')}</p>}
              </div>
            </details>)}
          </div>
        ) : !operationsError ? <p>{t('monitoring.account_inspection_operations_empty')}</p> : null}
        {operations && operations.pageInfo.total > RECORD_PAGE_SIZE ? <ProPagination
          page={operationPage}
          pageSize={RECORD_PAGE_SIZE}
          pageSizeOptions={[RECORD_PAGE_SIZE]}
          total={operations.pageInfo.total}
          onPageChange={setOperationPage}
          onPageSizeChange={() => undefined}
          idPrefix="account-inspection-operations"
          disabled={operationsLoading}
        /> : null}
      </section>
    </div>
  );
}
