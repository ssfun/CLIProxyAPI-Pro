import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { CodexQuotaBody } from '../../src/features/quota/providers/codex/CodexQuotaBody';
import { bindQuotaClasses } from '../../src/features/quota/types';
import type { CodexQuotaState } from '../../src/types';
import styles from '../../src/features/quota/components/QuotaBody.module.scss';
import compactStyles from '../../src/features/authFiles/components/AuthFileQuota.module.scss';
import i18n from '../../src/i18n';
import '../../src/styles/global.scss';

const classes = bindQuotaClasses(styles, 'quota');
const compact = bindQuotaClasses(compactStyles, 'auth-file');
const credit = { id: 'retained', status: 'available', grantedAt: '2030-01-01T00:00:00Z', expiresAt: '2030-10-23T04:46:00Z' };
const base: CodexQuotaState = {
  status: 'success', windows: [{ id: 'weekly', label: '周限额', usedPercent: 0, resetLabel: '-' }],
  planType: 'pro', rateLimitResetCreditsAvailableCount: 1,
  rateLimitResetCredits: [credit], rateLimitResetCreditsError: 'HTTP 503',
};
const scenarios = {
  retained: base,
  failed: { ...base, rateLimitResetCredits: [] },
  recovered: { ...base, rateLimitResetCreditsError: '', rateLimitResetCredits: [{ ...credit, id: 'fresh', expiresAt: '2030-10-30T04:46:00Z' }] },
  empty: { ...base, rateLimitResetCredits: [], rateLimitResetCreditsError: '', rateLimitResetCreditsAvailableCount: 0 },
};
export function App() {
  const [scenario, setScenario] = useState<keyof typeof scenarios>('retained');
  return <main style={{ maxWidth: 760, margin: '24px auto', padding: 16 }}>
    <h1>Codex 配额详情回退</h1>
    <select id="scenario" value={scenario} onChange={(event) => setScenario(event.target.value as keyof typeof scenarios)}>
      {Object.keys(scenarios).map((key) => <option key={key}>{key}</option>)}
    </select>
    {[['quota', classes], ['auth-file', compact]] .map(([name, map]) => <section key={String(name)} data-host={name} style={{ padding: 16, marginTop: 24, border: '1px solid #aaa' }}>
      <h2>{String(name)}</h2>
      <CodexQuotaBody quota={scenarios[scenario]} classes={map as typeof classes} />
    </section>)}
  </main>;
}
Object.assign(window, { codexDetailE2E: { quota: classes, 'auth-file': compact } });
void i18n.changeLanguage('zh-CN').then(() => createRoot(document.getElementById('root')!).render(<App />));
