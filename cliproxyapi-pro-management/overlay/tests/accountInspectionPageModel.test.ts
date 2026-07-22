import { describe, expect, test } from 'bun:test';
import {
  buildInspectionResultsViewState,
  getPaginationRange,
  toSettingsDraft,
} from '../src/features/monitoring/accountInspectionPageModel';
import {
  DEFAULT_ACCOUNT_INSPECTION_SETTINGS,
  type AccountInspectionResultItem,
} from '../src/features/monitoring/accountInspection';

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
  test('classifies result rows and pending actions in one pass', () => {
    const view = buildInspectionResultsViewState([
      result(),
      result({ key: 'auth-2', fileName: 'invalid.json', statusCode: 401, action: 'delete' }),
      result({ key: 'auth-3', fileName: 'quota.json', isQuota: true, action: 'disable' }),
    ]);

    expect(view.healthCounts.total).toBe(3);
    expect(view.healthCounts.healthy).toBe(1);
    expect(view.healthCounts.authInvalid).toBe(1);
    expect(view.healthCounts.quotaExhausted).toBe(1);
    expect(view.filterRowCounts.pending).toBe(2);
    expect(view.actionableActionCounts).toMatchObject({ delete: 1, disable: 1 });
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
      targetType: DEFAULT_ACCOUNT_INSPECTION_SETTINGS.targetType,
    });
  });
});
