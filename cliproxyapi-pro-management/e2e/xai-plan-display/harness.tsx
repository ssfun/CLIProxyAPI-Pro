import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { apiCallApi, type ApiCallResult } from '../../src/services/api';
import { useQuotaStore } from '../../src/stores';
import { useQuotaActions } from '../../src/features/quota/hooks/useQuotaActions';
import { QUOTA_ADAPTERS } from '../../src/features/quota/providers';
import { bindQuotaClasses } from '../../src/features/quota/types';
import { ProXaiQuotaBody } from '../../src/pro/modules/quota/extensions/ProXaiQuotaBody';
import type { XaiBillingSummary } from '../../src/types';
import styles from '../../src/features/quota/components/QuotaBody.module.scss';
import i18n from '../../src/i18n';
import '../../src/pro/registerLocales';
import '../../src/styles/global.scss';

const classes = bindQuotaClasses(styles, 'xai-plan-e2e');
const file = { name: 'xai-fixture.json', type: 'xai', auth_index: 'fixture', plan_type: 'x-premium-plus' };
const base: XaiBillingSummary = {
  mode: 'billing', periodType: 'weekly', usagePercent: 1, productUsage: [],
  monthlyLimitCents: 0, usedCents: 0, includedUsedCents: 0,
  onDemandCapCents: 0, onDemandUsedCents: 0, onDemandUsedPercent: 0, usedPercent: 1,
  planType: 'x-premium-plus', periodEnd: '2030-10-11T01:08:00Z',
};
const requests: string[] = [];
let subscriptionFailure = false;
let releaseSubscription: (() => void) | undefined;
let subscriptionGate = Promise.resolve();
const response = (body: unknown, statusCode = 200): ApiCallResult => ({
  statusCode, hasStatusCode: true, header: {}, body, bodyText: JSON.stringify(body),
});
apiCallApi.request = async ({ url }) => {
  requests.push(url);
  if (url.includes('/billing?')) return response({ config: {
    currentPeriod: { type: 'weekly', end: base.periodEnd },
    creditUsagePercent: 1, productUsage: [],
  } });
  if (url.endsWith('/billing')) return response({ config: {
    monthlyLimit: { val: 0 }, used: { val: 0 }, onDemandCap: { val: 0 },
  } });
  if (url.includes('/user?') || url.endsWith('/settings')) {
    await subscriptionGate;
    if (subscriptionFailure) return response({ error: 'fixture unavailable' }, 503);
    return response({ subscriptionTier: 'X_PREMIUM_PLUS', subscription_tier_display: 'X Premium+' });
  }
  throw new Error(`Unexpected fixture request: ${url}`);
};
const seed = (billing: XaiBillingSummary) => useQuotaStore.getState().setXaiQuota({
  [file.name]: { status: 'success', billing },
});
seed(base);
const cases: Record<string, XaiBillingSummary> = {
  inspection: base,
  empty: { ...base, planLabel: '' },
  free: { ...base, planType: 'free', planLabel: 'Free' },
  'free-observed': { ...base, planType: 'free', planLabel: 'Free', freeQuota: {
    model: 'grok-4.5', usedTokens: 25, limitTokens: 100,
  } },
  supergrok: { ...base, planType: 'supergrok', monthlyLimitCents: 15000 },
  heavy: { ...base, planType: 'supergrok-heavy', monthlyLimitCents: 150000 },
  mismatch: { ...base, planType: 'x-premium-plus', monthlyLimitCents: 15000 },
  official: { ...base, mode: 'paid-health', planType: 'paid', planLabel: 'Official API' },
};
for (const planType of ['x-basic', 'x-premium', 'x-premium-plus', 'supergrok-lite', 'supergrok', 'supergrok-heavy', 'paid-unknown'] as const) {
  cases[`enriched-${planType}`] = { ...base, planType, planLabel: `Native ${planType}` };
}

export function App() {
  const quota = useQuotaStore((state) => state.xaiQuota[file.name]);
  const { refreshQuota } = useQuotaActions(false);
  const [scenario, setScenario] = useState('inspection');
  return <main style={{ maxWidth: 760, margin: '40px auto', padding: 24 }}>
    <h1>xAI 套餐显示回归</h1>
    <label>场景 <select id="scenario" value={scenario} onChange={(event) => {
      setScenario(event.target.value); seed(cases[event.target.value]);
    }}>{Object.keys(cases).map((key) => <option key={key}>{key}</option>)}</select></label>
    <button id="refresh" onClick={() => {
      subscriptionGate = new Promise<void>((resolve) => { releaseSubscription = resolve; });
      void refreshQuota(file, QUOTA_ADAPTERS.xai);
    }}>刷新额度</button>
    <button id="release" onClick={() => { subscriptionFailure = false; releaseSubscription?.(); }}>完成订阅查询</button>
    <button id="fail" onClick={() => { subscriptionFailure = true; releaseSubscription?.(); }}>订阅查询失败</button>
    <button id="clear" onClick={() => useQuotaStore.getState().clearQuotaCache()}>清空会话配额</button>
    <article id="card" data-status={quota?.status ?? 'empty'} style={{ padding: 24, marginTop: 24, border: '1px solid #aaa', borderRadius: 16 }}>
      <h2>{file.name}</h2>
      {quota?.status === 'success' && <ProXaiQuotaBody quota={quota} classes={classes} />}
    </article>
  </main>;
}
Object.assign(window, { xaiPlanE2E: {
  requests, planSelector: `.${classes.codexPlanLabel}`,
  billing: () => useQuotaStore.getState().xaiQuota[file.name]?.billing,
} });
void i18n.changeLanguage('zh-CN').then(() => createRoot(document.getElementById('root')!).render(<App />));
