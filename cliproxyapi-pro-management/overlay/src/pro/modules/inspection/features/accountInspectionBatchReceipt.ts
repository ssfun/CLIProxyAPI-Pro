import type {
  AccountInspectionBackendResultItem,
  AccountInspectionResultItem,
} from './accountInspection';
import { resolveResultHealthStatus } from './accountInspectionPageModel';
import type {
  AccountInspectionBatchOperation,
  AccountInspectionBatchOutcome,
} from '../api';
import { isRecordValue, readBooleanValue, readStringValue } from '@/pro/shared/value';

export type AccountInspectionBatchReceiptState =
  | 'waiting'
  | 'healthy'
  | 'quotaExhausted'
  | 'reauthorizationRequired'
  | 'accountInvalid'
  | 'unknown'
  | 'disabled'
  | 'deleted'
  | 'enabled'
  | 'quotaProtected'
  | 'restrictionsCleared'
  | 'stillRestricted';

export type AccountInspectionBatchReceiptEntry = AccountInspectionBatchOperation['items'][number];

export type AccountInspectionBatchReceiptDetails = {
  source: 'status' | 'result' | 'after' | 'effect';
  result?: AccountInspectionBackendResultItem;
  after?: Record<string, unknown>;
  reason?: string;
  warning?: string;
};

export type AccountInspectionBatchReceiptItem = {
  entry: AccountInspectionBatchReceiptEntry;
  state: AccountInspectionBatchReceiptState;
  error?: string;
  details: AccountInspectionBatchReceiptDetails;
};

const RECEIPT_STATE_SORT_RANK: Record<AccountInspectionBatchReceiptState, number> = {
  quotaExhausted: 0,
  reauthorizationRequired: 0,
  accountInvalid: 0,
  unknown: 0,
  quotaProtected: 0,
  stillRestricted: 0,
  waiting: 1,
  disabled: 2,
  deleted: 2,
  enabled: 2,
  healthy: 2,
  restrictionsCleared: 2,
};

const readOutcomeRecord = (outcome: AccountInspectionBatchOutcome | undefined) => {
  if (!isRecordValue(outcome)) return null;
  let current: Record<string, unknown> = outcome;
  const visited = new Set<Record<string, unknown>>();
  while (isRecordValue(current.outcome) && !visited.has(current)) {
    visited.add(current);
    current = current.outcome;
  }
  return current;
};

const readOutcomeWarning = (outcome: AccountInspectionBatchOutcome | undefined) => {
  if (!isRecordValue(outcome)) return '';
  const warnings: string[] = [];
  let current: Record<string, unknown> = outcome;
  const visited = new Set<Record<string, unknown>>();
  while (!visited.has(current)) {
    visited.add(current);
    const warning = readStringValue(current.warning);
    if (warning && !warnings.includes(warning)) warnings.push(warning);
    if (!isRecordValue(current.outcome)) break;
    current = current.outcome;
  }
  return warnings.join(' · ');
};

const readOutcomeError = (
  entry: AccountInspectionBatchReceiptEntry,
  outcome: AccountInspectionBatchOutcome | undefined
) => {
  const errors = [readStringValue(entry.error)];
  if (isRecordValue(outcome)) {
    let current: Record<string, unknown> = outcome;
    const visited = new Set<Record<string, unknown>>();
    while (!visited.has(current)) {
      visited.add(current);
      errors.push(readStringValue(current.error));
      if (!isRecordValue(current.outcome)) break;
      current = current.outcome;
    }
  }
  return errors.filter((error, index, values) => Boolean(error) && values.indexOf(error) === index).join(' · ');
};

const readExactResult = (outcome: Record<string, unknown> | null) =>
  isRecordValue(outcome?.result)
    ? outcome.result as AccountInspectionBackendResultItem
    : null;

const readRecoveryAfter = (outcome: Record<string, unknown> | null) => {
  if (isRecordValue(outcome?.after)) return outcome.after;
  if (!Array.isArray(outcome?.receipts)) return null;
  const finalReceipt = outcome.receipts[outcome.receipts.length - 1];
  return isRecordValue(finalReceipt) && isRecordValue(finalReceipt.after)
    ? finalReceipt.after
    : null;
};

const hasInspectionResultEvidence = (result: AccountInspectionBackendResultItem) => {
  const record = result as unknown as Record<string, unknown>;
  return Boolean(
    readStringValue(record.key)
    || readStringValue(record.resultRef)
    || readStringValue(record.fileName)
    || readStringValue(record.provider)
  );
};

const readResultStatusCode = (result: AccountInspectionBackendResultItem) => {
  const record = result as unknown as Record<string, unknown>;
  const value = record.statusCode;
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && /^\d{3}$/.test(value.trim())) return Number(value.trim());
  const errors = [record.error, record.errorDetail, record.deepProbeError, record.tokenRefreshError]
    .map(readStringValue)
    .filter(Boolean)
    .join(' ');
  const match = errors.match(/\bHTTP\s+(\d{3})\b/i)
    ?? errors.match(/\bstatus(?:\s+code)?\s*[:=]?\s*(\d{3})\b/i);
  return match ? Number(match[1]) : null;
};

const readResultAction = (result: AccountInspectionBackendResultItem) => {
  const record = result as unknown as Record<string, unknown>;
  return readStringValue(record.action).toLowerCase();
};

const readResultError = (result: AccountInspectionBackendResultItem) => {
  const record = result as unknown as Record<string, unknown>;
  return [record.executeError, record.error, record.errorDetail, record.errorCode, record.deepProbeError, record.tokenRefreshError]
    .map(readStringValue)
    .filter((error, index, values) => Boolean(error) && values.indexOf(error) === index)
    .join(' · ');
};

const hasHealthyInspectionEvidence = (result: AccountInspectionBackendResultItem) => {
  const record = result as unknown as Record<string, unknown>;
  const statusCode = readResultStatusCode(result);
  const usedPercent = record.usedPercent;
  const hasSuccessfulObservation = statusCode !== null
    ? statusCode >= 200 && statusCode < 300
    : typeof usedPercent === 'number' && Number.isFinite(usedPercent);
  return readResultAction(result) === 'keep'
    && !readResultError(result)
    && hasSuccessfulObservation;
};

const classifyInspectionResult = (
  result: AccountInspectionBackendResultItem
): AccountInspectionBatchReceiptState => {
  if (!hasInspectionResultEvidence(result)) return 'unknown';

  const record = result as unknown as Record<string, unknown>;
  const executedEffect = readStringValue(record.executedEffect).toLowerCase();
  const executedAction = readStringValue(record.executedAction).toLowerCase();
  if (executedEffect === 'delete' || executedAction === 'delete') return 'deleted';
  if (
    executedEffect === 'quota_protection'
    || readBooleanValue(record.quotaCooling)
  ) return 'quotaProtected';
  if (
    executedEffect === 'admin_disable'
    || executedAction === 'disable'
    || readBooleanValue(record.disabled)
  ) return 'disabled';
  if (executedEffect === 'admin_enable' || executedAction === 'enable') return 'enabled';

  const health = resolveResultHealthStatus(result as unknown as AccountInspectionResultItem);
  if (health === 'quotaExhausted') return 'quotaExhausted';
  if (health === 'authInvalid') {
    return readResultStatusCode(result) === 401 ? 'reauthorizationRequired' : 'accountInvalid';
  }
  if (health === 'inspectionError') return 'unknown';
  if (health === 'disabled') return 'disabled';
  if (health === 'recoverable') return 'unknown';
  if (readResultAction(result) && readResultAction(result) !== 'keep') return 'unknown';
  return health === 'healthy' && hasHealthyInspectionEvidence(result) ? 'healthy' : 'unknown';
};

const classifyRecoveryAfter = (after: Record<string, unknown>): AccountInspectionBatchReceiptState => {
  // Core returns an empty routing-board account when no restriction remains.
  // This confirms scheduling state only, not the account's health.
  if (Object.keys(after).length === 0) return 'restrictionsCleared';
  if (!Object.prototype.hasOwnProperty.call(after, 'authId')) return 'unknown';
  if (typeof after.authId !== 'string') return 'unknown';
  const authId = readStringValue(after.authId);
  return authId ? 'stillRestricted' : 'restrictionsCleared';
};

const classifySucceededEntry = (
  operation: AccountInspectionBatchOperation,
  entry: AccountInspectionBatchReceiptEntry
): Pick<AccountInspectionBatchReceiptItem, 'state' | 'details'> => {
  if (operation.kind === 'recover' || entry.effect === 'recovery_check' || entry.effect === 'quota_recovery') {
    return { state: 'unknown', details: { source: 'after' } };
  }

  const stateByEffect: Partial<Record<AccountInspectionBatchReceiptEntry['effect'], AccountInspectionBatchReceiptState>> = {
    delete: 'deleted',
    quota_protection: 'quotaProtected',
    admin_disable: 'disabled',
    admin_enable: 'enabled',
  };
  return {
    state: stateByEffect[entry.effect] ?? 'unknown',
    details: { source: 'effect' },
  };
};

const classifyEntry = (
  operation: AccountInspectionBatchOperation,
  entry: AccountInspectionBatchReceiptEntry
): AccountInspectionBatchReceiptItem => {
  const outcome = readOutcomeRecord(entry.outcome);
  const error = readOutcomeError(entry, entry.outcome);
  const warning = readOutcomeWarning(entry.outcome);

  if (entry.status === 'ready' || entry.status === 'running') {
    return {
      entry,
      state: 'waiting',
      ...(error ? { error } : {}),
      details: { source: 'status', ...(warning ? { warning } : {}) },
    };
  }

  const result = readExactResult(outcome);
  if (result) {
    const resultError = [error, readResultError(result)]
      .filter((value, index, values) => Boolean(value) && values.indexOf(value) === index)
      .join(' · ');
    return {
      entry,
      state: classifyInspectionResult(result),
      ...(resultError ? { error: resultError } : {}),
      details: { source: 'result', result, ...(warning ? { warning } : {}) },
    };
  }
  const after = readRecoveryAfter(outcome);
  if (after) {
    const state = classifyRecoveryAfter(after);
    const reason = readStringValue(after.reason);
    const recoveryError = error || (state === 'stillRestricted' ? reason : '');
    return {
      entry,
      state,
      ...(recoveryError ? { error: recoveryError } : {}),
      details: {
        source: 'after',
        after,
        ...(reason ? { reason } : {}),
        ...(warning ? { warning } : {}),
      },
    };
  }

  if (entry.status !== 'succeeded') {
    return {
      entry,
      state: 'unknown',
      ...(error ? { error } : {}),
      details: { source: 'status', ...(warning ? { warning } : {}) },
    };
  }

  const classified = classifySucceededEntry(operation, entry);
  return {
    entry,
    ...classified,
    ...(error ? { error } : {}),
    details: {
      ...classified.details,
      ...(warning ? { warning } : {}),
    },
  };
};

export const getBatchReceiptItems = (
  operation: AccountInspectionBatchOperation
): AccountInspectionBatchReceiptItem[] => operation.items
  .map((entry, index) => ({ item: classifyEntry(operation, entry), index }))
  .sort((left, right) =>
    RECEIPT_STATE_SORT_RANK[left.item.state] - RECEIPT_STATE_SORT_RANK[right.item.state]
    || left.index - right.index
  )
  .map(({ item }) => item);
