import type { TFunction } from 'i18next';
import { isRecordValue } from '@/pro/shared/value';

const messageKeys: Record<string, string> = {
  'batch account identity changed or unavailable': 'identity_changed',
  'batch suggestion changed or already processed': 'suggestion_changed',
  'batch account state changed during execution': 'state_changed',
  'batch account already absent': 'absent',
  'account is disabled; scheduling recovery unavailable': 'disabled',
  'batch has no executable targets': 'no_targets',
  'account has no active recoverable restriction': 'no_restriction',
  'account inspection result is stale or no longer available': 'stale',
  'result reference is stale': 'stale',
  'restored account inspection snapshot is read-only; run a new inspection first': 'read_only',
  'duplicate target': 'duplicate',
  'unsupported action': 'unsupported',
  'provider does not support inspection': 'unsupported',
  'provider does not support this action': 'unsupported',
  'operation not found': 'not_found',
  'operation must complete before retry': 'not_completed',
  'operation has no failed items': 'nothing_to_retry',
  'preflight expired; prepare a new operation': 'expired',
  'retry preflight expired; prepare a new retry': 'expired',
  'interrupted operation cannot be replayed; inspect current state and prepare explicitly': 'interrupted',
  'interrupted operation cannot be replayed; inspect current state and retry explicitly': 'interrupted',
  'check completed; account remains restricted': 'still_restricted',
  'check completed; suggested restriction remains active': 'still_restricted',
  'batch receipt capacity reached': 'capacity',
  'operation receipts could not be loaded': 'load_failed',
  'failed to persist execution intent': 'save_failed',
  'failed to persist operation': 'save_failed',
  'batch receipt persistence unavailable': 'save_failed',
  'account inspection scheduler unavailable': 'unavailable',
  'scheduler unavailable': 'unavailable',
  'clientRequestId was already used for a different batch request': 'request_conflict',
  'inspection returned no receipt': 'missing_receipt',
  'action returned no receipt': 'missing_receipt',
};

const readText = (value: unknown) => typeof value === 'string' ? value.trim() : '';
const translate = (key: string, t: TFunction) => t(`monitoring.account_inspection_batch_error_${key}`);

export const formatInspectionBatchMessage = (message: string, t: TFunction): string => {
  // Receipt errors may combine multiple independent diagnostics. Translate only
  // known application messages; keep provider diagnostics intact.
  return message.split(' · ').map((part) => {
    const key = messageKeys[part.trim()];
    return key ? translate(key, t) : part;
  }).join(' · ');
};

export const buildInspectionBatchError = (error: unknown, t: TFunction) => {
  if (!error) return null;
  const record = isRecordValue(error) ? error : null;
  const details = isRecordValue(record?.details) ? record.details : null;
  const message = readText(details?.error) || readText(record?.message) || readText(error);
  const noTargets = message === 'batch has no executable targets';
  const rawItems = noTargets && Array.isArray(details?.items) ? details.items : [];
  const items = rawItems.map((raw) => {
    const entry = isRecordValue(raw) ? raw : null;
    const item = isRecordValue(entry?.item) ? entry.item : null;
    const reason = readText(entry?.error);
    let reasonKey = messageKeys[reason];
    // Older Core uses one reason for disabled accounts and absent restrictions.
    // The server-bound item carries the disabled flag; never consult live rows.
    if (reasonKey === 'no_restriction' && item?.disabled === true) reasonKey = 'disabled';
    if (!reason && entry?.status === 'stale') reasonKey = 'stale';
    return {
      name: readText(item?.displayName) || readText(item?.fileName) || readText(entry?.key) || t('monitoring.account_label'),
      reasonKey,
      reason: reasonKey ? translate(reasonKey, t) : reason || t('common.unknown_error'),
    };
  });
  let key = noTargets ? 'no_targets' : '';
  let tone: 'info' | 'warning' | 'error' = noTargets ? 'warning' : 'error';
  if (items.length && items.every((item) => item.reasonKey === 'no_restriction')) {
    key = 'no_recovery_needed';
    tone = 'info';
  } else if (items.length && items.every((item) => item.reasonKey === 'stale')) {
    key = 'all_stale';
  } else if (items.length && items.every((item) => item.reasonKey === 'disabled')) {
    key = 'all_disabled';
  }
  return {
    message: key ? translate(key, t) : formatInspectionBatchMessage(message || t('common.unknown_error'), t),
    tone,
    items,
  };
};
