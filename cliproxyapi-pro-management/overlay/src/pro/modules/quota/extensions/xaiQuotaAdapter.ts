import type { TFunction } from 'i18next';
import type { AuthFileItem, XaiBillingSummary, XaiQuotaState } from '@/types';
import { apiCallApi, getApiCallErrorMessage } from '@/services/api/apiCall';
import { useQuotaStore } from '@/stores';
import {
  XAI_PAID_HEALTH_MODEL,
  XAI_REQUEST_HEADERS,
  createStatusError,
  isXaiUsingOfficialAPI,
} from '@/utils/quota';
import { XAI_CONFIG, requestXaiPaidHealth } from '@/features/quota/providers/xai/data';
import type { QuotaProviderData } from '@/features/quota/providers/types';
import { normalizeAuthIndex } from '@/utils/authIndex';
import {
  XAI_FREE_QUOTA_PROBE_URL,
  mergeXaiBillingRuntimeState,
  normalizeXaiPlanType,
  parseXaiFreeQuotaProbe,
  resolveXaiPlanType,
  isXaiMonthlyBillingKnown,
} from './xaiQuota';

const REQUEST_TIMEOUT_MS = 15_000;
type XaiQuotaObservation = XaiBillingSummary & { quotaIdentityFingerprint?: string };

async function requestXaiFreeQuota(authIndex: string, t: TFunction) {
  const result = await apiCallApi.request(
    {
      authIndex,
      method: 'POST',
      url: XAI_FREE_QUOTA_PROBE_URL,
      header: {
        ...XAI_REQUEST_HEADERS,
        accept: 'text/event-stream',
        'Content-Type': 'application/json',
      },
      data: JSON.stringify({
        model: XAI_PAID_HEALTH_MODEL,
        input: [{ role: 'user', content: [{ type: 'input_text', text: 'ping' }] }],
        instructions: 'You are a helpful assistant. Reply briefly.',
        max_output_tokens: 1,
        stream: true,
        store: false,
      }),
      useExecutor: true,
    },
    { timeout: REQUEST_TIMEOUT_MS }
  );
  const quota = parseXaiFreeQuotaProbe(result, XAI_PAID_HEALTH_MODEL);
  if (quota) return quota;
  if (result.statusCode < 200 || result.statusCode >= 300) {
    throw createStatusError(getApiCallErrorMessage(result), result.statusCode);
  }
  throw new Error(t('xai_quota.empty_data'));
}

async function fetchProXaiQuota(file: AuthFileItem, t: TFunction): Promise<XaiQuotaObservation> {
  // Bind before any request: auth-file data may change while requests are pending.
  const quotaIdentityFingerprint = typeof file.quota_identity_fingerprint === 'string'
    ? file.quota_identity_fingerprint.trim() || undefined
    : undefined;
  const cached = useQuotaStore.getState().xaiQuota[file.name];
  const previous = quotaIdentityFingerprint && cached?.quotaIdentityFingerprint === quotaIdentityFingerprint
    ? cached.billing
    : undefined;
  const observed = (billing: XaiBillingSummary): XaiQuotaObservation => ({
    ...billing,
    quotaIdentityFingerprint,
  });
  const authIndex = normalizeAuthIndex(file.auth_index ?? file.authIndex);
  if (authIndex && isXaiUsingOfficialAPI(file)) {
    const billing = await requestXaiPaidHealth(authIndex);
    return observed(mergeXaiBillingRuntimeState({ ...billing, planType: 'paid' }, previous));
  }

  const billing = await XAI_CONFIG.fetchQuota(file, t);
  if (billing.mode === 'paid-health') {
    return observed(mergeXaiBillingRuntimeState({ ...billing, planType: 'paid' }, previous));
  }

  const planType =
    normalizeXaiPlanType(file.plan_type ?? file.planType) ??
    resolveXaiPlanType(billing.monthlyLimitCents, isXaiMonthlyBillingKnown(billing));
  const merged = mergeXaiBillingRuntimeState({ ...billing, planType }, previous);
  if (planType !== 'free') return observed(merged);

  if (!authIndex) return observed(merged);
  const freeQuota = await requestXaiFreeQuota(authIndex, t);
  return observed({ ...merged, freeQuota });
}

export const PRO_XAI_CONFIG: QuotaProviderData<XaiQuotaState, XaiQuotaObservation> = {
  ...XAI_CONFIG,
  fetchQuota: fetchProXaiQuota,
  buildSuccessState: (observation) => {
    const { quotaIdentityFingerprint, ...billing } = observation;
    return { status: 'success', billing, quotaIdentityFingerprint };
  },
};
