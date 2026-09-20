import type { TFunction } from 'i18next';
import type { AuthFileItem } from '@/types';
import { resolveAuthProvider } from '@/utils/quota';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import {
  resolveAccountPlanLabel,
  type AccountPlanQuotaStore,
} from '@/pro/modules/quota';
import type { SchedulingBoardAccount } from './routingPolicy';

const normalize = (value: unknown) => String(value ?? '').trim();
const providerKey = (value: string) => value.trim().toLowerCase().replace(/_/g, '-');

// Prefer stable runtime identity. Never attach a same-name account's plan when
// both sides supply different indices, or when the fallback match is ambiguous.
export function resolveSchedulingBoardPlans(
  accounts: SchedulingBoardAccount[],
  files: AuthFileItem[],
  quotaStore: AccountPlanQuotaStore,
  t: TFunction
): Map<string, string> {
  const byIndex = new Map<string, AuthFileItem[]>();
  const byName = new Map<string, AuthFileItem[]>();
  for (const file of files) {
    const index = normalize(file.authIndex);
    if (index) byIndex.set(index, [...(byIndex.get(index) ?? []), file]);
    byName.set(file.name, [...(byName.get(file.name) ?? []), file]);
  }
  const plans = new Map<string, string>();
  for (const account of accounts) {
    const index = normalize(account.authIndex);
    const indexed = index ? byIndex.get(index) : undefined;
    const candidates =
      indexed ??
      (byName.get(account.fileName) ?? []).filter((file) => !index || !normalize(file.authIndex));
    const matches = candidates.filter(
      (file) => providerKey(resolveAuthProvider(file)) === providerKey(account.provider)
    );
    if (matches.length !== 1) continue;
    const file = matches[0];
    plans.set(
      account.authId,
      resolveAccountPlanLabel({
        authFile: file,
        fileName: getQuotaCacheKey(file),
        provider: account.provider,
        quotaStore,
        t,
        emptyLabel: '',
      })
    );
  }
  return plans;
}
