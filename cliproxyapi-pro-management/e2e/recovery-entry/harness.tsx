import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import i18n from '../../src/i18n';
import '../../src/pro/registerLocales';
import '../../src/styles/global.scss';
import { useAuthStore } from '../../src/stores';
import { authFilesApi } from '../../src/services/api/authFiles';
import { AccountInspectionPage } from '../../src/pro/modules/inspection/AccountInspectionPage';
import { accountInspectionApi } from '../../src/pro/modules/inspection/api';
import { DEFAULT_ACCOUNT_INSPECTION_SETTINGS, createIdleAccountInspectionProgressSnapshot } from '../../src/pro/modules/inspection/features/accountInspection';
import { routingPolicyApi, type SchedulingBoardAccount, type SchedulingRecoveryRequest } from '../../src/pro/modules/routing/routingPolicy';

await i18n.changeLanguage('en');
useAuthStore.setState({ connectionStatus: 'connected', apiBase: '', managementKey: '' });
authFilesApi.list = async () => ({ files: [] });
accountInspectionApi.getStatus = async () => ({
  schedule: { enabled: false, intervalMinutes: 30, nextRunAt: 0, settings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS },
  status: { state: 'idle', runSettings: DEFAULT_ACCOUNT_INSPECTION_SETTINGS, lastStartedAt: 0, lastFinishedAt: 0, lastError: '', summary: createIdleAccountInspectionProgressSnapshot().summary, logs: [], results: [] },
});
const accounts: SchedulingBoardAccount[] = [
  { provider: 'openai-compat', authId: 'sk-SECRET-API-KEY', authIndex: 'api-index', registrationEpoch: '5', fileName: 'sk-SECRET-API-KEY', scope: 'model', kind: 'transient', bucket: 'authTransient', sources: ['upstream'], resume: 'auto-expire', reason: 'upstream-only', inspection: false, overlap: false,
    details: [{ source: 'upstream', scope: 'model', model: 'model-only', kind: 'transient', resume: 'auto-expire', reason: 'upstream-only', retryAt: 1893456000000, revision: 'u1' }] },
  { provider: 'codex', authId: 'overlap', authIndex: 'overlap-index', registrationEpoch: '8', fileName: 'overlap.json', scope: 'credential', kind: 'quota', bucket: 'overlap', sources: ['inspection', 'upstream'], resume: 'multiple', reason: 'both-sources', inspection: true, overlap: true,
    details: [{ source: 'inspection', scope: 'credential', kind: 'quota', resume: 'recheck-quota', reason: 'inspection-remains', revision: 'i2' }, { source: 'upstream', scope: 'model', model: 'cooling-model', kind: 'quota', resume: 'auto-expire', reason: 'upstream-remove', retryAt: 1893456000000, revision: 'u2' }] },
];
let gets = 0;
let fail = false;
let empty = false;
let hold = false;
let pending: (() => void) | null = null;
const releases: SchedulingRecoveryRequest[] = [];
routingPolicyApi.get = async () => {
  gets += 1;
  if (fail) throw new Error('Controlled refresh failure');
  const snapshot = structuredClone({ generatedAt: Date.now(), summary: { blocked: accounts.length, quota: 1, authTransient: 1, recheck: 1, overlap: 1, excluded: 0 }, accounts: empty ? [] : accounts });
  if (hold) {
    hold = false;
    await new Promise<void>((resolve) => { pending = resolve; });
  }
  return snapshot;
};
routingPolicyApi.release = async (request) => {
  const account = accounts.find((item) => item.authId === request.authId && item.authIndex === request.authIndex && item.registrationEpoch === request.registrationEpoch);
  const detail = account?.details.find((item) => item.source === request.source && (item.model || '') === request.model && item.revision === request.revision);
  if (!account || !detail) throw new Error('stale target');
  releases.push(request);
  account.details = account.details.filter((item) => item !== detail);
  return { after: structuredClone(account) };
};
Object.assign(window, { recoveryEntry: { hold: () => { hold = true; }, pending: () => pending !== null, resolve: () => { pending?.(); pending = null; }, switchConnection: () => useAuthStore.setState({ apiBase: 'http://127.0.0.1:9' }), gets: () => gets, releases: () => releases, fail: (value: boolean) => { fail = value; }, empty: (value: boolean) => { empty = value; } } });
createRoot(document.getElementById('root')!).render(<MemoryRouter><AccountInspectionPage /></MemoryRouter>);
