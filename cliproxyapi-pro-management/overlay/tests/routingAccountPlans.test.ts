import { describe, expect, test } from 'bun:test';
import type { TFunction } from 'i18next';
import type { AuthFileItem } from '@/types';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import type { AccountPlanQuotaStore } from '../src/pro/modules/quota/accountPlan';
import { resolveSchedulingBoardPlans } from '../src/pro/modules/routing/routingAccountPlans';
import type { SchedulingBoardAccount } from '../src/pro/modules/routing/routingPolicy';

const t = ((key: string) => key) as TFunction;
const emptyQuota = (): AccountPlanQuotaStore => ({
  antigravityQuota: {},
  claudeQuota: {},
  codexQuota: {},
  geminiCliQuota: {},
  kimiQuota: {},
  xaiQuota: {},
});
const row = (changes: Partial<SchedulingBoardAccount> = {}) =>
  ({
    authId: 'a',
    authIndex: 'idx-a',
    fileName: 'a.json',
    provider: 'xai',
    ...changes,
  }) as SchedulingBoardAccount;

describe('scheduling board account plans', () => {
  test('uses shared provider-specific plan formatting for auth metadata', () => {
    const got = resolveSchedulingBoardPlans(
      [row()],
      [{ name: 'a.json', authIndex: 'idx-a', provider: 'xai', plan_type: 'supergrok-heavy' }],
      emptyQuota(),
      t
    );
    expect(got.get('a')).toBe('SuperGrok Heavy');
  });
  test('runtime index disambiguates identical names and quota cache keys', () => {
    const files: AuthFileItem[] = [
      {
        name: 'shared.json',
        authIndex: 'idx-a',
        provider: 'codex',
        runtimeOnly: true,
        plan_type: 'free',
      },
      {
        name: 'shared.json',
        authIndex: 'idx-b',
        provider: 'codex',
        runtimeOnly: true,
        plan_type: 'free',
      },
    ];
    const quota = emptyQuota();
    quota.codexQuota[getQuotaCacheKey(files[1])] = {
      status: 'success',
      planType: 'plus',
      windows: [],
    };
    const got = resolveSchedulingBoardPlans(
      [row({ fileName: 'shared.json', provider: 'codex', authIndex: 'idx-b' })],
      files,
      quota,
      t
    );
    expect(got.get('a')).toBe('Plus');
  });
  test('never borrows a plan from another account or provider with the same filename', () => {
    const got = resolveSchedulingBoardPlans(
      [row()],
      [{ name: 'a.json', authIndex: 'idx-other', provider: 'xai', plan_type: 'free' }],
      emptyQuota(),
      t
    );
    expect(got.has('a')).toBe(false);
    expect(
      resolveSchedulingBoardPlans(
        [row()],
        [{ name: 'a.json', authIndex: 'idx-a', provider: 'codex', plan_type: 'plus' }],
        emptyQuota(),
        t
      ).has('a')
    ).toBe(false);
  });
  test('supports unique legacy filename fallback but rejects ambiguous matches', () => {
    const file: AuthFileItem = { name: 'a.json', provider: 'xai', plan_type: 'free' };
    expect(resolveSchedulingBoardPlans([row()], [file], emptyQuota(), t).get('a')).toBe('Free');
    expect(
      resolveSchedulingBoardPlans([row()], [file, { ...file }], emptyQuota(), t).has('a')
    ).toBe(false);
  });
  test('does not infer free when account plan metadata is absent', () => {
    expect(
      resolveSchedulingBoardPlans(
        [row()],
        [{ name: 'a.json', authIndex: 'idx-a', provider: 'xai' }],
        emptyQuota(),
        t
      ).get('a')
    ).toBe('');
  });
});
