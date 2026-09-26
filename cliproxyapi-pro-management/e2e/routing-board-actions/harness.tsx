import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import '../../src/i18n';
import '../../src/pro/registerLocales';
import '../../src/styles/global.scss';
import { ConfirmationModal } from '../../src/components/common/ConfirmationModal';
import { authFilesApi } from '../../src/services/api/authFiles';
import { useAuthStore, useNotificationStore } from '../../src/stores';
import { RoutingPolicyPage } from '../../src/pro/modules/routing/RoutingPolicyPage';
import {
  routingPolicyApi,
  type SchedulingBoardAccount,
  type SchedulingBoardDetail,
  type SchedulingRecoveryRequest,
  type SchedulingRecoveryResult,
} from '../../src/pro/modules/routing/routingPolicy';

const initialDetails: SchedulingBoardDetail[] = [
  {
    source: 'inspection', scope: 'credential', kind: 'quota', resume: 'recheck-quota',
    reason: 'credential_quota', revision: '11', retryAt: Date.now() + 60_000,
  },
  {
    source: 'upstream', scope: 'model', model: 'gpt-5', kind: 'transient',
    resume: 'probe-request', reason: 'upstream_cooldown', revision: '22',
    retryAt: Date.now() + 120_000,
  },
];

const makeAccount = (
  name = 'A',
  details = initialDetails
): SchedulingBoardAccount => ({
  provider: 'codex', authId: `account-${name}`, authIndex: `index-${name}`, registrationEpoch: '7',
  fileName: `account-${name.toLowerCase()}.json`, scope: 'credential', kind: 'quota', bucket: 'overlap',
  sources: Array.from(new Set(details.map((detail) => detail.source))),
  resume: details.length > 1 ? 'multiple' : details[0]?.resume || 'manual',
  reason: details.map((detail) => detail.reason).join(', '),
  inspection: details.some((detail) => detail.source === 'inspection'),
  overlap: details.length > 1,
  details,
  models: details.map((detail) => detail.model).filter(Boolean) as string[],
  nextActionAt: details[0]?.retryAt,
  nextTransitionAt: details[1]?.retryAt,
});

let account: SchedulingBoardAccount | null = makeAccount();
const fillerAccounts = Array.from({ length: 17 }, (_, index) => makeAccount(
  String(index + 2),
  [{
    source: 'upstream', scope: 'credential', kind: 'transient', resume: 'probe-request',
    reason: 'upstream_cooldown', revision: String(100 + index), retryAt: Date.now() + 120_000,
  }]
));
let getCount = 0;
const checkRequests: SchedulingRecoveryRequest[] = [];
const releaseRequests: SchedulingRecoveryRequest[] = [];

const board = () => ({
  generatedAt: Date.now(),
  summary: {
    blocked: fillerAccounts.length + (account ? 1 : 0),
    quota: account ? 1 : 0,
    authTransient: fillerAccounts.length + (account ? 1 : 0),
    recheck: account ? 1 : 0,
    overlap: account?.overlap ? 1 : 0,
    excluded: 0,
  },
  accounts: account ? [account, ...fillerAccounts] : fillerAccounts,
});

authFilesApi.list = async () => ({ files: [] });
routingPolicyApi.get = async () => {
  getCount += 1;
  return board();
};
routingPolicyApi.check = async (request) => {
  checkRequests.push({ ...request });
  return {
    before: account ?? undefined,
    after: account ?? undefined,
    phases: [{ source: 'inspection', status: 'completed' }],
  };
};
routingPolicyApi.release = async (request) => {
  releaseRequests.push({ ...request });
  if (!account) return {};
  const before = account;
  const remaining = account.details.filter((detail) => !(
    detail.source === request.source
    && (detail.model || '') === (request.model || '')
    && detail.revision === request.revision
  ));
  account = remaining.length ? makeAccount('A', remaining) : null;
  return { before, after: account ?? undefined };
};

useAuthStore.setState({
  isAuthenticated: true,
  apiBase: 'http://fixture.invalid',
  managementKey: 'fixture',
  connectionStatus: 'connected',
});
useNotificationStore.setState({ notifications: [] });

Object.assign(window, {
  routingBoardActions: {
    requests: () => ({
      getCount,
      checks: checkRequests.map((request) => ({ ...request })),
      releases: releaseRequests.map((request) => ({ ...request })),
    }),
    reset: () => {
      account = makeAccount();
      checkRequests.splice(0);
      releaseRequests.splice(0);
      useNotificationStore.setState({ notifications: [] });
    },
  },
});

createRoot(document.getElementById('root')!).render(
  <MemoryRouter initialEntries={['/routing']}>
    <RoutingPolicyPage />
    <ConfirmationModal />
  </MemoryRouter>
);
