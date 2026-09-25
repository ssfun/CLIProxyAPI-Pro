import { describe, expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  buildAuthFileAccountStats,
  buildActionPreview,
  buildHealthStatusLabel,
  buildInspectionResultsViewState,
  createInspectionBackendState,
  formatAccountInspectionDuration,
  getPaginationRange,
  inspectionBackendReducer,
  isAuthFileAccountInvalid,
  isAuthFileRequestError,
  isResultAccountInvalid,
  isResultRequestError,
  isSchedulingRecoveryAction,
  isXaiQuotaLow,
  InspectionErrorDetailsPanel,
  partitionInspectionActionTargets,
  resolveAccountInspectionAccountLabel,
  resolveAccountInspectionPlanLabel,
  resolveAssetInspectionHealthCounts,
  resolveResultHealthStatus,
  toSettingsDraft,
} from '../src/pro/modules/inspection/features/accountInspectionPageModel';
import type { TFunction } from 'i18next';
import {
  buildAccountInspectionBackendViewState,
  DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
  hasAccountInspectionAutoExecutePolicies,
  isAccountInspectionBackendResponse,
  saveAccountInspectionConfigurableSettings,
  type AccountInspectionResultItem,
} from '../src/pro/modules/inspection/features/accountInspection';

const result = (overrides: Partial<AccountInspectionResultItem> = {}): AccountInspectionResultItem => ({
  key: 'auth-1',
  fileName: 'account.json',
  displayAccount: 'owner@example.com',
  authIndex: 'auth-1',
  accountId: null,
  provider: 'codex',
  disabled: false,
  status: 'active',
  state: 'active',
  raw: { name: 'account.json', provider: 'codex' },
  action: 'keep',
  actionReason: '',
  statusCode: 200,
  usedPercent: 20,
  isQuota: false,
  error: '',
  ...overrides,
});

describe('account inspection page model', () => {
  test('retires legacy request-error policy from imported and locally saved settings', () => {
    // Failure matrix: an old delete value alone must not enable automation or survive
    // save/restore; explicit account-invalid and quota policies must still count.
    const legacy = { ...DEFAULT_ACCOUNT_INSPECTION_SETTINGS, autoExecuteRequestErrorAction: 'delete' };
    const settings = saveAccountInspectionConfigurableSettings(legacy);
    expect(settings).not.toHaveProperty('autoExecuteRequestErrorAction');
    expect(hasAccountInspectionAutoExecutePolicies(settings)).toBe(false);
    expect(hasAccountInspectionAutoExecutePolicies({ ...settings, autoExecuteAccountInvalidAction: 'disable' })).toBe(true);
    expect(hasAccountInspectionAutoExecutePolicies({ ...settings, autoExecuteQuotaLimitDisable: true })).toBe(true);
  });

  test('formats the completed inspection duration from backend run timestamps', () => {
    const labels: Record<string, string> = {
      'monitoring.account_inspection_duration_day': '天',
      'monitoring.account_inspection_duration_hour': '小时',
      'monitoring.account_inspection_duration_minute': '分',
      'monitoring.account_inspection_duration_second': '秒',
    };
    const t = ((key: string, options?: { count?: number }) => `${options?.count ?? ''}${labels[key] ?? key}`) as TFunction;
    const startedAt = Date.UTC(2026, 7, 12, 9, 50, 50);

    expect(formatAccountInspectionDuration(startedAt, startedAt + 4_332_000, t)).toBe('1小时12分12秒');
    expect(formatAccountInspectionDuration(startedAt, startedAt + 12_000, t)).toBe('12秒');
    expect(formatAccountInspectionDuration(startedAt, startedAt, t)).toBe('0秒');
    expect(formatAccountInspectionDuration(startedAt, startedAt - 1, t)).toBeNull();
  });

  test('ignores incomplete backend snapshots instead of crashing the page reducer', () => {
    const state = createInspectionBackendState(DEFAULT_ACCOUNT_INSPECTION_SETTINGS);
    const incompleteResponses = [
      undefined,
      {},
      { status: { summary: {} } },
      { schedule: {}, status: { summary: {} } },
      { schedule: { settings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS }, status: {} },
    ];

    incompleteResponses.forEach((response) => {
      expect(isAccountInspectionBackendResponse(response)).toBe(false);
      expect(inspectionBackendReducer(state, {
        type: 'backendResponseReceived',
        response: response as never,
      })).toBe(state);
    });

    expect(isAccountInspectionBackendResponse({
      schedule: { settings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS },
      status: { summary: {} },
    })).toBe(true);
  });

  test('does not mark an unchanged settings dialog as dirty', () => {
	let state = createInspectionBackendState(DEFAULT_ACCOUNT_INSPECTION_SETTINGS);
	state = inspectionBackendReducer(state, { type: 'discardSettingsDraft' });
	expect(state.settingsDirty).toBe(false);
	state = inspectionBackendReducer(state, { type: 'updateSettingsDraft', values: { sampleSize: '10' } });
	expect(state.settingsDirty).toBe(true);
	state = inspectionBackendReducer(state, { type: 'discardSettingsDraft' });
	expect(state.settingsDirty).toBe(false);
  });

  test('keeps historical run settings and terminal states separate from the current schedule', () => {
    const runSettings = { ...DEFAULT_ACCOUNT_INSPECTION_SETTINGS, usedPercentThreshold: 80, sampleSize: 12 };
    const response = {
      schedule: { enabled: true, intervalMinutes: 360, nextRunAt: 99, settings: { ...DEFAULT_ACCOUNT_INSPECTION_SETTINGS, usedPercentThreshold: 95 } },
      status: {
        state: 'partial' as const,
        runSettings,
        lastStartedAt: 10,
        lastFinishedAt: 20,
        lastError: 'deadline exceeded',
        summary: { totalFiles: 20, probeSetCount: 20, sampledCount: 12, disabledCount: 0, enabledCount: 12, deleteCount: 0, disableCount: 0, enableCount: 0, keepCount: 12, errorCount: 1 },
        logs: [],
        results: [],
      },
    };

    const view = buildAccountInspectionBackendViewState(response);
    expect(view.settings.usedPercentThreshold).toBe(95);
    expect(view.result?.settings).toMatchObject({ usedPercentThreshold: 80, sampleSize: 12 });
    expect(view.result?.state).toBe('partial');
    expect(view.runStatus).toBe('partial');
    expect(view.lastError).toBe('deadline exceeded');
  });

  test('tracks settings as dirty until saved or explicitly discarded', () => {
    let state = createInspectionBackendState(DEFAULT_ACCOUNT_INSPECTION_SETTINGS);
    state = inspectionBackendReducer(state, { type: 'updateSettingsDraft', values: { sampleSize: '10' } });
    expect(state.settingsDirty).toBe(true);
    expect(state.settingsDraft.sampleSize).toBe('10');
    state = inspectionBackendReducer(state, { type: 'discardSettingsDraft' });
    expect(state.settingsDirty).toBe(false);
    expect(state.settingsDraft.sampleSize).toBe('0');
  });

  test('classifies result rows and pending actions in one pass', () => {
    const view = buildInspectionResultsViewState([
      result(),
      result({ key: 'auth-2', fileName: 'invalid.json', statusCode: 401, errorCode: 'inspection_http_error', action: 'delete' }),
      result({ key: 'auth-3', fileName: 'quota.json', isQuota: true, action: 'disable' }),
    ]);

    expect(view.healthCounts.total).toBe(3);
    expect(view.healthCounts.healthy).toBe(1);
    expect(view.healthCounts.authInvalid).toBe(1);
    expect(view.healthCounts.quotaExhausted).toBe(1);
    expect(view.filterRowCounts.all).toBe(3);
    expect(view.filterRowCounts.accountIssues).toBe(1);
    expect(view.filterRowCounts.quotaChanges).toBe(1);
    expect(view.filterRowCounts.pending).toBe(2);
    expect(view.actionableActionCounts).toMatchObject({ delete: 1, disable: 1 });
  });

  test('distinguishes 401 authorization expiry from 403 authentication failure in result labels', () => {
    // Failure matrix: both statuses remain aggregated as account-invalid, while the
    // result field must explain the distinct remediation signal to operators.
    const labels: Record<string, string> = {
      'monitoring.account_inspection_account_invalid': '账号失效',
      'monitoring.account_inspection_result_authorization_expired': '授权过期',
      'monitoring.account_inspection_result_authentication_invalid': '认证失效',
    };
    const t = ((key: string) => labels[key] ?? key) as TFunction;
    const unauthorized = result({ statusCode: 401, errorCode: 'inspection_http_error' });
    const forbidden = result({ statusCode: 403, errorCode: 'inspection_http_error' });

    expect(resolveResultHealthStatus(unauthorized)).toBe('authInvalid');
    expect(resolveResultHealthStatus(forbidden)).toBe('authInvalid');
    expect(buildHealthStatusLabel(unauthorized, 'authInvalid', t)).toBe('授权过期 · 401');
    expect(buildHealthStatusLabel(forbidden, 'authInvalid', t)).toBe('认证失效 · 403');
  });

  test('merges incomplete results into inspection errors', () => {
    const incomplete = result({
      key: 'incomplete',
      statusCode: null,
      errorCode: 'inspection_incomplete',
      error: 'inspection did not finish',
    });
    const view = buildInspectionResultsViewState([incomplete]);

    expect(resolveResultHealthStatus(incomplete)).toBe('inspectionError');
    expect(view.healthCounts).toMatchObject({ total: 1, inspectionError: 1, unknown: 0 });
    expect(view.filterRowCounts).toMatchObject({ accountIssues: 1, requestError: 1 });
    expect(view.filterRows.accountIssues.map(({ item }) => item.key)).toEqual(['incomplete']);
    expect(view.filterRows.requestError.map(({ item }) => item.key)).toEqual(['incomplete']);
  });

  test('organizes inspection details around the current observation and collapses diagnostics', () => {
    const labels: Record<string, string> = {
      'monitoring.account_inspection_account_details_title': '账号信息',
      'monitoring.account_inspection_observation_details_title': '本次观察',
      'monitoring.account_inspection_diagnostics_title': '诊断信息',
      'monitoring.account_inspection_technical_details_title': '技术信息',
      'monitoring.account_inspection_raw_error_response': '原始错误响应',
    };
    const t = ((key: string) => labels[key] ?? key) as TFunction;
    const html = renderToStaticMarkup(InspectionErrorDetailsPanel({
      item: result({
        statusCode: 401,
        errorCode: 'inspection_http_error',
        error: 'Invalid credentials',
        errorDetail: '{"error":"Invalid credentials"}',
        runId: 'run-1',
        resultRef: 'result-1',
        registrationEpoch: 'epoch-1',
      }),
      t,
    }));

    expect(html).toContain('账号信息');
    expect(html).toContain('本次观察');
    expect(html).toContain('<details');
    expect(html).toContain('诊断信息');
    expect(html).toContain('技术信息');
    expect(html).toContain('原始错误响应');
    expect(html).not.toContain('<details open=""');
    expect(html.indexOf('账号信息')).toBeLessThan(html.indexOf('本次观察'));
    expect(html.indexOf('本次观察')).toBeLessThan(html.indexOf('诊断信息'));
  });

  test('routes only live quota enable actions through scheduling recovery', () => {
    const quotaRecovery = result({ key: 'quota-recovery', action: 'enable', quotaCooling: true });
    const manualEnable = result({ key: 'manual-enable', action: 'enable', quotaCooling: true, disabled: true });
    const disable = result({ key: 'disable', action: 'disable', quotaCooling: true });

    expect(isSchedulingRecoveryAction(quotaRecovery)).toBe(true);
    expect(isSchedulingRecoveryAction(manualEnable)).toBe(false);
    expect(isSchedulingRecoveryAction(disable)).toBe(false);
    expect(partitionInspectionActionTargets([quotaRecovery, manualEnable, disable])).toEqual({
      recovery: [quotaRecovery],
      executable: [manualEnable, disable],
    });
  });

  test('uses the auth file name when the result email is unavailable', () => {
    expect(resolveAccountInspectionAccountLabel(result({ email: 'owner@example.com' }))).toBe('owner@example.com');
    expect(resolveAccountInspectionAccountLabel(result({
      email: undefined,
      displayAccount: '-',
      name: 'Codex Account',
      fileName: 'codex-account.json',
    }))).toBe('codex-account.json');
  });

  test('builds a complete scrollable action preview across every action type', () => {
    const t = ((key: string) => ({
      'monitoring.account_inspection_action_delete': '删除',
      'monitoring.account_inspection_action_disable': '禁用',
      'monitoring.account_inspection_action_enable': '启用',
    }[key] ?? key)) as TFunction;
    const actions = ['delete', 'disable', 'enable', 'delete', 'disable', 'enable'] as const;
    const preview = buildActionPreview(actions.map((action, index) => result({
      key: `preview-${index}`,
      fileName: `account-${index}.json`,
      action,
      actionReason: `reason-${index}`,
    })), t);

    expect(preview).toHaveLength(6);
    expect(preview.map((item) => item.action)).toEqual(['删除', '禁用', '启用', '删除', '禁用', '启用']);
    expect(preview[0]).toMatchObject({ account: 'account-0.json', reason: 'reason-0', dangerous: true });
    expect(preview[2]).toMatchObject({ account: 'account-2.json', dangerous: false });
    expect(preview[5]).toMatchObject({ account: 'account-5.json', reason: 'reason-5', dangerous: false });
  });

  test('uses semantic evidence consistently across all providers', () => {
    const providers = ['antigravity', 'claude', 'codex', 'gemini-cli', 'kimi', 'xai'];
    providers.forEach((provider) => {
      expect(resolveResultHealthStatus(result({
        provider,
        statusCode: 401,
        errorCode: 'inspection_http_error',
        action: provider === 'codex' ? 'delete' : 'disable',
      }))).toBe('authInvalid');
      expect(resolveResultHealthStatus(result({
        provider,
        statusCode: provider === 'codex' || provider === 'xai' ? 402 : 200,
        isQuota: true,
        action: 'disable',
      }))).toBe('quotaExhausted');
    });

    const requestErrors: Partial<AccountInspectionResultItem>[] = [
      { provider: 'antigravity', statusCode: 400, errorCode: 'antigravity_deep_probe_error', deepProbeStatus: 'transient_error' },
      { provider: 'claude', statusCode: 502, errorCode: 'inspection_probe_error' },
      { provider: 'codex', statusCode: null, errorCode: 'missing_auth_index' },
      { provider: 'gemini-cli', statusCode: 503, errorCode: 'inspection_probe_error' },
      { provider: 'kimi', statusCode: null, errorCode: 'token_refresh_error', tokenRefreshStatus: 'failed' },
      { provider: 'xai', statusCode: 400, errorCode: 'xai_deep_probe_error', deepProbeStatus: 'transient_error' },
    ];
    requestErrors.forEach((requestError, index) => {
      const item = result({
        key: `request-${index}`,
        action: 'delete',
        error: 'probe failed',
        ...requestError,
      });
      expect(isResultAccountInvalid(item)).toBe(false);
      expect(isResultRequestError(item)).toBe(true);
      expect(resolveResultHealthStatus(item)).toBe('inspectionError');
    });
  });

  test('does not infer account health from suggested actions', () => {
    expect(resolveResultHealthStatus(result({ action: 'delete' }))).toBe('healthy');
    expect(resolveResultHealthStatus(result({ action: 'disable' }))).toBe('healthy');
  });

  test('aligns auth-file and inspection-result error code semantics', () => {
    const invalidFile = {
      name: 'invalid.json',
      type: 'claude',
      last_error: { code: 'inspection_http_error', http_status: 401 },
    };
    expect(isAuthFileAccountInvalid(invalidFile)).toBe(true);
    expect(isAuthFileRequestError(invalidFile)).toBe(false);

    const requestErrorFile = {
      name: 'request-error.json',
      type: 'xai',
      last_error: { code: 'xai_deep_probe_error', http_status: 400 },
    };
    expect(isAuthFileAccountInvalid(requestErrorFile)).toBe(false);
    expect(isAuthFileRequestError(requestErrorFile)).toBe(true);

    expect(isResultAccountInvalid(result({ statusCode: 401, errorCode: 'inspection_http_error' }))).toBe(true);
    expect(isResultRequestError(result({ statusCode: 400, errorCode: 'xai_deep_probe_error', error: 'probe failed' }))).toBe(true);

    expect(isAuthFileRequestError({
      name: 'quota-disabled.json',
      type: 'xai',
      disabled: true,
      unavailable: true,
      status: 'error',
      status_message: 'disabled by scheduled account inspection',
    })).toBe(false);
  });

  test('uses the auth-files page semantics for all, enabled, disabled, and problem stats', () => {
    const files = [
      { name: 'healthy.json', type: 'codex', status_message: 'healthy' },
      {
        name: 'disabled-with-error.json',
        type: 'codex',
        disabled: true,
        unavailable: true,
        status: 'error',
        last_error: { message: 'credential failed' },
      },
      { name: 'status-disabled.json', type: 'codex', status: 'disabled' },
      { name: 'unavailable.json', type: 'claude', unavailable: true },
      { name: 'error-status.json', type: 'claude', status: 'error' },
      { name: 'healthy-message.json', type: 'claude', status_message: 'available' },
      { name: 'warning-message.json', type: 'claude', last_error: { message: 'refresh failed' } },
    ];
    const stats = buildAuthFileAccountStats(
      files,
      {
        antigravityQuota: {},
        claudeQuota: {},
        codexQuota: {},
        geminiCliQuota: {},
        kimiQuota: {},
        xaiQuota: {},
      },
      90,
      'claude-gpt'
    );

    expect(stats).toMatchObject({ total: 7, enabled: 6, disabled: 1, problem: 3 });
    expect(stats.providers).toEqual([
      expect.objectContaining({ provider: 'claude', total: 4, enabled: 4, disabled: 0, problem: 3 }),
      expect.objectContaining({ provider: 'codex', total: 3, enabled: 2, disabled: 1, problem: 0 }),
    ]);
  });

  test('uses complete inspection health counts for aggregate and provider asset cards', () => {
    const allCounts = {
      total: 10,
      healthy: 2,
      disabled: 0,
      authInvalid: 3,
      quotaExhausted: 4,
      inspectionError: 1,
      recoverable: 0,
    };
    const xaiCounts = { ...allCounts, total: 6, authInvalid: 2, quotaExhausted: 3 };
    const providerCounts = { xai: xaiCounts };

    expect(resolveAssetInspectionHealthCounts(allCounts, providerCounts, 'all', 10)).toBe(allCounts);
    expect(resolveAssetInspectionHealthCounts(allCounts, providerCounts, 'xai', 6)).toBe(xaiCounts);
    expect(resolveAssetInspectionHealthCounts(allCounts, providerCounts, 'xai', 7)).toBeNull();
  });

  test('keeps pagination and settings draft conversion deterministic', () => {
    expect(getPaginationRange({
      page: 2,
      pageSize: 100,
      total: 250,
      totalPages: 3,
      hasMore: true,
    }, 100)).toMatchObject({ from: 101, to: 200, hasPrevious: true, hasNext: true });

    expect(toSettingsDraft(DEFAULT_ACCOUNT_INSPECTION_SETTINGS)).toMatchObject({
      workers: String(DEFAULT_ACCOUNT_INSPECTION_SETTINGS.workers),
      providerWorkers: String(DEFAULT_ACCOUNT_INSPECTION_SETTINGS.providerWorkers),
      autoExecuteConfirmations: String(DEFAULT_ACCOUNT_INSPECTION_SETTINGS.autoExecuteConfirmations),
      targetType: DEFAULT_ACCOUNT_INSPECTION_SETTINGS.targetType,
    });
  });

  test('normalizes total and per-provider worker limits independently', () => {
    expect(saveAccountInspectionConfigurableSettings({
      workers: 99,
      providerWorkers: 99,
    })).toMatchObject({
      workers: 8,
      providerWorkers: 4,
    });
    expect(saveAccountInspectionConfigurableSettings({
      workers: 2,
      providerWorkers: 4,
    })).toMatchObject({
      workers: 2,
      providerWorkers: 4,
    });
  });

  test('keeps quota thresholds in the backend 0-100 percentage unit', () => {
    expect(saveAccountInspectionConfigurableSettings({
      usedPercentThreshold: 0.5,
    }).usedPercentThreshold).toBe(0.5);
    expect(saveAccountInspectionConfigurableSettings({
      usedPercentThreshold: 1,
    }).usedPercentThreshold).toBe(1);
  });

  test('uses free-token exhaustion only for free xAI plans', () => {
    expect(isXaiQuotaLow({
      status: 'success',
      billing: { planType: 'free', freeQuota: { exhausted: true } },
    }, 90)).toBe(true);
    expect(isXaiQuotaLow({
      status: 'success',
      billing: {
        planType: 'paid',
        usagePercent: 10,
        freeQuota: { exhausted: true },
      },
    }, 90)).toBe(false);
  });

  test('resolves account plans from quota state with auth-file fallback', () => {
    const t = ((key: string) => ({
      'codex_quota.plan_pro': '专业版',
      'xai_quota.plan_paid_unknown': 'Paid (unknown tier)',
      'xai_quota.plan_x_premium': 'X Premium',
    }[key] ?? key)) as TFunction;
    const quotaStore = {
      antigravityQuota: {},
      claudeQuota: {},
      codexQuota: { 'account.json': { status: 'success', planType: 'pro' } },
      geminiCliQuota: {},
      kimiQuota: {},
      xaiQuota: {},
    } as Parameters<typeof resolveAccountInspectionPlanLabel>[2];

    expect(resolveAccountInspectionPlanLabel(result(), undefined, quotaStore, t)).toBe('专业版');
    expect(resolveAccountInspectionPlanLabel(
      result({ provider: 'gemini-cli' }),
      { name: 'account.json', type: 'gemini-cli', tier_id: 'standard-tier' },
      quotaStore,
      t
    )).toBe('Standard Tier');
    expect(resolveAccountInspectionPlanLabel(
      result({ provider: 'xai' }),
      undefined,
      {
        ...quotaStore,
        xaiQuota: {
          'account.json': { status: 'success', billing: { monthlyLimitCents: 20_000 } },
        },
      } as Parameters<typeof resolveAccountInspectionPlanLabel>[2],
      t
    )).toBe('Paid (unknown tier)');
    expect(resolveAccountInspectionPlanLabel(
      result({ provider: 'xai' }),
      undefined,
      {
        ...quotaStore,
        xaiQuota: {
          'account.json': {
            status: 'success',
            billing: { monthlyLimitCents: 0, planType: 'x-premium' },
          },
        },
      } as Parameters<typeof resolveAccountInspectionPlanLabel>[2],
      t
    )).toBe('X Premium');
  });
});
