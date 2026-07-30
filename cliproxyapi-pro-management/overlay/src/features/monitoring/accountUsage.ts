import type { AuthFileItem } from '@/types';

const EMAIL_PATTERN = /^[^@\s]+@[^@\s]+$/;

const readText = (value: unknown): string => typeof value === 'string' ? value.trim() : '';

export function resolveAccountUsageLabel(file: AuthFileItem | null, authIndex: string | null): string {
  const idToken = file?.id_token && typeof file.id_token === 'object'
    ? file.id_token as Record<string, unknown>
    : null;
  const authSuffix = authIndex?.includes(':') ? authIndex.split(':').slice(1).join(':') : authIndex;
  const email = [
    file?.email,
    idToken?.email,
    file?.account,
    idToken?.preferred_username,
    file?.label,
    authSuffix,
  ]
    .map(readText)
    .find((value) => EMAIL_PATTERN.test(value));

  return email || readText(file?.name) || authIndex || '-';
}

export function buildAccountUsageLogPath(authIndex: string, fromMs: number, toMs: number): string {
  const params = new URLSearchParams({
    auth_index: authIndex,
    from_ms: String(Math.max(0, Math.round(fromMs))),
    to_ms: String(Math.max(0, Math.round(toMs))),
  });
  return `/monitoring?${params.toString()}#request-events`;
}

export function ratio(numerator: number, denominator: number): number {
  if (!Number.isFinite(numerator) || !Number.isFinite(denominator) || denominator <= 0) return 0;
  return Math.min(Math.max(numerator / denominator, 0), 1);
}
