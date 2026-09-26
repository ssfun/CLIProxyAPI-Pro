import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { AccountInspectionPage } from '/src/pro/modules/inspection/AccountInspectionPage';
import {
  DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
  type AccountInspectionBackendResultItem,
  type AccountInspectionBackendResponse,
} from '/src/pro/modules/inspection/features/accountInspection';
import {
  accountInspectionApi,
  type AccountInspectionBatchOperation,
  type AccountInspectionBatchTarget,
} from '/src/pro/modules/inspection/api';
import { authFilesApi } from '/src/services/api/authFiles';
import { quotaPersistenceMiddleware } from '/src/pro/modules/quota';
import { useAuthStore, useNotificationStore } from '/src/stores';
import i18n from '/src/i18n';
import '/src/pro/registerLocales';
import '/src/styles/global.scss';

type ScenarioName = 'recheck-mixed' | 'action-effects' | 'recovery-edges' | 'execution-states';

const apiBase = 'http://review.invalid/v0/management';
const managementKey = 'inspection-receipt-fixture';
const now = 1_790_000_000_000;

const batchStorageKey = () => {
  let hash = 2166136261;
  for (const char of `${apiBase}\0${managementKey}`) {
    hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
  }
  return `account-inspection-batches:${(hash >>> 0).toString(16)}`;
};

const target = (
  key: string,
  action: AccountInspectionBatchTarget['action'] = 'keep',
  disabled = false,
): AccountInspectionBatchTarget => ({
  key,
  provider: 'codex',
  fileName: `${key}.json`,
  displayName: key,
  email: `${key}@fixture.invalid`,
  name: key,
  authIndex: `auth-${key}`,
  disabled,
  resultRef: `result-${key}`,
  suggested: false,
  action,
});

const result = (
  key: string,
  overrides: Partial<AccountInspectionBackendResultItem> = {},
): AccountInspectionBackendResultItem => ({
  key,
  fileName: `${key}.json`,
  displayName: key,
  email: `${key}@fixture.invalid`,
  name: key,
  authId: `auth-id-${key}`,
  authIndex: `auth-${key}`,
  provider: 'codex',
  disabled: false,
  registrationEpoch: 'fixture-epoch',
  resultRef: `result-${key}`,
  observedAt: now,
  runId: 'fixture-run',
  suggested: false,
  action: 'keep',
  actionReason: '',
  statusCode: 200,
  usedPercent: 12,
  isQuota: false,
  error: '',
  ...overrides,
});

const operation = (
  id: ScenarioName,
  kind: AccountInspectionBatchOperation['kind'],
  state: AccountInspectionBatchOperation['state'],
  items: AccountInspectionBatchOperation['items'],
): AccountInspectionBatchOperation => {
  const count = (status: AccountInspectionBatchOperation['items'][number]['status']) =>
    items.filter((item) => item.status === status).length;
  return {
    operationId: id,
    kind,
    state,
    createdAt: now - 5000,
    expiresAt: now + 3600000,
    items,
    summary: {
      total: items.length,
      ready: count('ready'),
      running: count('running'),
      succeeded: count('succeeded'),
      failed: count('failed'),
      stale: count('stale'),
      unsupported: count('unsupported'),
      interrupted: count('interrupted'),
    },
  };
};

const operations: Record<ScenarioName, AccountInspectionBatchOperation> = {
  'recheck-mixed': operation('recheck-mixed', 'inspect', 'completed', [
    {
      key: 'healthy', status: 'succeeded', effect: 'inspect', item: target('healthy'),
      outcome: { success: true, result: result('healthy') },
    },
    {
      key: 'quota-exhausted', status: 'succeeded', effect: 'inspect', item: target('quota-exhausted'),
      outcome: { success: true, result: result('quota-exhausted', { isQuota: true, usedPercent: 100 }) },
    },
    {
      key: 'reauthorization-required', status: 'succeeded', effect: 'inspect', item: target('reauthorization-required'),
      outcome: { success: true, result: result('reauthorization-required', {
        statusCode: 401,
        errorCode: 'inspection_http_error',
        error: 'HTTP 401: refresh token expired',
        errorDetail: 'fixture authorization detail',
      }) },
    },
    {
      key: 'account-invalid', status: 'succeeded', effect: 'inspect', item: target('account-invalid'),
      outcome: { success: true, result: result('account-invalid', {
        statusCode: 403,
        errorCode: 'inspection_http_error',
        error: 'HTTP 403: credential rejected',
      }) },
    },
    {
      key: 'unknown', status: 'succeeded', effect: 'inspect', item: target('unknown'),
      outcome: { success: true, result: result('unknown', {
        statusCode: 429,
        errorCode: 'inspection_rate_limited',
        error: 'HTTP 429: transient inspection rate limit',
      }) },
    },
  ]),
  'action-effects': operation('action-effects', 'action', 'completed', [
    { key: 'enabled', status: 'succeeded', effect: 'admin_enable', item: target('enabled', 'enable', true), outcome: { success: true } },
    { key: 'disabled', status: 'succeeded', effect: 'admin_disable', item: target('disabled', 'disable'), outcome: { success: true } },
    { key: 'deleted', status: 'succeeded', effect: 'delete', item: target('deleted', 'delete'), outcome: { success: true } },
    { key: 'quota-protected', status: 'succeeded', effect: 'quota_protection', item: target('quota-protected', 'disable'), outcome: { success: true } },
  ]),
  'recovery-edges': operation('recovery-edges', 'recover', 'completed', [
    {
      key: 'restrictions-cleared', status: 'succeeded', effect: 'recovery_check', item: target('restrictions-cleared', 'enable', true),
      outcome: { success: true, after: {} },
    },
    {
      key: 'still-restricted', status: 'failed', effect: 'recovery_check', item: target('still-restricted', 'enable', true),
      error: 'check completed; account remains restricted',
      outcome: { success: false, receipts: [{ before: { authId: 'restriction-old' }, after: { authId: 'restriction-current', reason: 'fixture quota restriction' }, steps: ['probe'] }] },
    },
    {
      key: 'missing-outcome', status: 'succeeded', effect: 'recovery_check', item: target('missing-outcome', 'enable', true),
    },
    {
      key: 'sparse-result', status: 'succeeded', effect: 'inspect', item: target('sparse-result'),
      outcome: { success: true, result: { key: 'sparse-result' } as AccountInspectionBackendResultItem },
    },
    {
      key: 'nested-warning', status: 'succeeded', effect: 'inspect', item: target('nested-warning'),
      outcome: { outcome: { success: true, result: {} as AccountInspectionBackendResultItem, warning: 'nested fixture warning: follow-up required' } },
    },
  ]),
  'execution-states': operation('execution-states', 'action', 'interrupted', [
    { key: 'running', status: 'running', effect: 'admin_enable', item: target('running', 'enable', true) },
    { key: 'stale-skipped', status: 'stale', effect: 'admin_disable', item: target('stale-skipped', 'disable'), error: 'result reference is stale' },
    { key: 'unsupported-skipped', status: 'unsupported', effect: 'unknown', item: target('unsupported-skipped'), error: 'provider does not support this action' },
    { key: 'interrupted', status: 'interrupted', effect: 'delete', item: target('interrupted', 'delete'), error: 'worker stopped before a receipt was recorded' },
  ]),
};

const scenarioLabels: Record<ScenarioName, string> = {
  'recheck-mixed': '重检：执行成功但账号健康状态混合',
  'action-effects': '动作：启用、禁用、删除、额度保护',
  'recovery-edges': '恢复：空 after、仍受限、缺失结果、嵌套警告',
  'execution-states': '执行态：处理中、跳过、中断',
};

const backendResponse: AccountInspectionBackendResponse = {
  schedule: {
    enabled: false,
    intervalMinutes: 360,
    nextRunAt: 0,
    settings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
  },
  status: {
    state: 'completed',
    runSettings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
    lastStartedAt: now - 60_000,
    lastFinishedAt: now - 55_000,
    lastError: '',
    summary: {
      totalFiles: 1,
      probeSetCount: 1,
      sampledCount: 1,
      disabledCount: 0,
      enabledCount: 1,
      deleteCount: 0,
      disableCount: 0,
      enableCount: 0,
      keepCount: 1,
      errorCount: 0,
      usedPercentThreshold: 100,
      sampled: false,
      plannedActionPreview: [],
    },
    healthCounts: { total: 1, healthy: 1, disabled: 0, authInvalid: 0, quotaExhausted: 0, inspectionError: 0, recoverable: 0, unknown: 0 },
    resultsPage: { page: 1, pageSize: 50, total: 1, totalPages: 1, hasMore: false },
    logsPage: { page: 1, pageSize: 100, total: 0, totalPages: 0, hasMore: false },
    logs: [],
    results: [result('page-health-snapshot')],
  },
};

const fixtureState = {
  scenario: 'recheck-mixed' as ScenarioName,
  storageKey: batchStorageKey(),
  errorMode: 'none',
  requestCount: 0,
  get operation() { return operations[this.scenario]; },
};

const seedScenario = (scenario: ScenarioName) => {
  fixtureState.scenario = scenario;
  window.sessionStorage.setItem(fixtureState.storageKey, JSON.stringify(operations[scenario].operationId));
  document.documentElement.dataset.inspectionReceiptScenario = scenario;
};

class FixtureWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSING = 2;
  readonly CLOSED = 3;
  readyState = FixtureWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  close() { this.readyState = FixtureWebSocket.CLOSED; }
  send() {}
  addEventListener() {}
  removeEventListener() {}
  dispatchEvent() { return true; }
}

Object.assign(window, {
  inspectionReceiptFixture: fixtureState,
  WebSocket: FixtureWebSocket,
});

accountInspectionApi.getStatus = async () => structuredClone(backendResponse);
accountInspectionApi.getBatch = async (operationId: string) => {
  const found = Object.values(operations).find((candidate) => candidate.operationId === operationId);
  if (!found) throw new Error(`Unknown inspection receipt fixture operation: ${operationId}`);
  return structuredClone(found);
};
const rejectBatch = () => {
  fixtureState.requestCount += 1;
  const mode = fixtureState.errorMode;
  if (mode === 'none') return;
  const entry = (name: string, reason: string, disabled = false, status = 'unsupported') => ({
    key: name, status, effect: 'unknown', item: target(name, 'enable', disabled), error: reason,
  });
  const missing = entry('no-restriction', 'account has no active recoverable restriction');
  const disabled = entry('disabled-account', 'account has no active recoverable restriction', true);
  const stale = entry('stale-account', 'account inspection result is stale or no longer available', false, 'stale');
  const items = mode === 'no-restriction' ? [missing]
    : mode === 'disabled' ? [disabled]
      : mode === 'stale' ? [stale] : [missing, disabled, stale];
  const message = mode === 'unknown' ? 'fixture upstream diagnostic 503'
    : mode === 'expired' ? 'retry preflight expired; prepare a new retry'
      : 'batch has no executable targets';
  const error = Object.assign(new Error(message), {
    status: 409,
    details: mode === 'malformed' ? { error: message, items: [null, {}] }
      : mode === 'unstructured' ? undefined : { error: message, items },
  });
  throw error;
};
accountInspectionApi.startBatch = async () => {
  rejectBatch();
  return structuredClone(operations['action-effects']);
};
accountInspectionApi.retryExecuteBatch = async (operationId: string) => {
  rejectBatch();
  return accountInspectionApi.getBatch(operationId);
};
authFilesApi.list = async () => ({ files: [] });
quotaPersistenceMiddleware.markStale = () => {};
quotaPersistenceMiddleware.ensureFresh = async () => {};

useNotificationStore.setState({
  showNotification: () => {},
  showConfirmation: ({ onConfirm }: { onConfirm?: () => void | Promise<void> }) => void onConfirm?.(),
});
useAuthStore.setState({ connectionStatus: 'connected', apiBase, managementKey });

const queryScenario = new URLSearchParams(window.location.search).get('scenario') as ScenarioName | null;
const initialScenario = queryScenario && queryScenario in operations ? queryScenario : 'recheck-mixed';
seedScenario(initialScenario);
await i18n.changeLanguage('zh-CN');

function FixtureApp() {
  const [scenario, setScenario] = useState<ScenarioName>(initialScenario);
  const selectScenario = (nextScenario: ScenarioName) => {
    seedScenario(nextScenario);
    setScenario(nextScenario);
  };
  return (
    <>
      <label className="inspection-receipt-fixture-toolbar">
        <strong>批量回执场景</strong>
        <select
          id="inspection-receipt-scenario"
          aria-label="批量回执场景"
          value={scenario}
          onChange={(event) => selectScenario(event.currentTarget.value as ScenarioName)}
        >
          {(Object.keys(scenarioLabels) as ScenarioName[]).map((name) => (
            <option key={name} value={name}>{scenarioLabels[name]}</option>
          ))}
        </select>
        <select aria-label="批量错误场景" id="inspection-batch-error-mode" defaultValue="none"
          onChange={(event) => { fixtureState.errorMode = event.currentTarget.value; }}>
          {['none', 'no-restriction', 'disabled', 'stale', 'mixed', 'unknown', 'expired', 'malformed', 'unstructured'].map((mode) => (
            <option key={mode} value={mode}>{mode}</option>
          ))}
        </select>
        <select aria-label="测试语言" id="inspection-fixture-language" defaultValue="zh-CN"
          onChange={(event) => void i18n.changeLanguage(event.currentTarget.value)}>
          {['zh-CN', 'zh-TW', 'en', 'ru'].map((language) => <option key={language}>{language}</option>)}
        </select>
      </label>
      <MemoryRouter key={scenario}>
        <AccountInspectionPage />
      </MemoryRouter>
    </>
  );
}

createRoot(document.getElementById('root')!).render(<FixtureApp />);
