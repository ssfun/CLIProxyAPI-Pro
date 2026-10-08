import { normalizeNumberValue } from '@/utils/quota';
import { isRecordValue, readBooleanValue } from '@/pro/shared/value';

const isQuotaLowWindow = (window: unknown, usedPercentThreshold: number): boolean => {
  if (!isRecordValue(window)) return false;
  if (readBooleanValue(window.limitReached ?? window.limit_reached)) return true;
  if (window.allowed !== undefined && !readBooleanValue(window.allowed, true)) return true;
  const threshold = Number.isFinite(usedPercentThreshold) ? usedPercentThreshold : 100;
  const usedPercent = normalizeNumberValue(window.usedPercent ?? window.used_percent);
  if (usedPercent !== null && usedPercent >= threshold) return true;
  const remainingFraction = normalizeNumberValue(
    window.remainingFraction ?? window.remaining_fraction
  );
  if (remainingFraction !== null && remainingFraction * 100 <= 100 - threshold) return true;
  const remainingPercent = normalizeNumberValue(window.remainingPercent ?? window.remaining_percent);
  if (remainingPercent !== null && remainingPercent <= 100 - threshold) return true;
  const remainingAmount = normalizeNumberValue(
    window.remainingAmount ?? window.remaining_amount ?? window.remaining
  );
  if (remainingAmount !== null && remainingAmount <= 0) return true;
  const limit = normalizeNumberValue(window.limit);
  const used = normalizeNumberValue(window.used);
  return limit !== null && limit > 0 && used !== null && used >= limit;
};

export const isQuotaLowState = (quota: unknown, usedPercentThreshold = 100): boolean => {
  if (!isRecordValue(quota) || quota.status !== 'success') return false;
  return ['windows', 'groups', 'buckets', 'rows'].some((key) => {
    const value = quota[key];
    return Array.isArray(value) && value.some((window) =>
      isQuotaLowWindow(window, usedPercentThreshold)
    );
  });
};
