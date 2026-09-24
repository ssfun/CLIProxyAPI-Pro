import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../../src/i18n';
import '../../src/pro/registerLocales';
import '../../src/styles/global.scss';
import { SchedulingRecoveryDialog } from '../../src/pro/modules/inspection/SchedulingRecoveryDialog';
import { routingPolicyApi, type SchedulingBoardAccount, type SchedulingRecoveryRequest, type SchedulingRecoveryResult } from '../../src/pro/modules/routing/routingPolicy';

type AccountName = 'A' | 'B';
const accounts = Object.fromEntries((['A', 'B'] as const).map((name) => [name, {
  provider: 'codex', authId: name, authIndex: `index-${name}`, registrationEpoch: '1',
  fileName: name, scope: 'credential', kind: 'quota', bucket: 'quota',
  sources: ['inspection'], resume: 'recheck-quota', reason: `reason-${name}`,
  inspection: true, overlap: false,
  details: [{ source: 'inspection', scope: 'credential', kind: 'quota',
    resume: 'recheck-quota', reason: `reason-${name}`, revision: '1' }],
}])) as Record<AccountName, SchedulingBoardAccount>;

const pending: Array<{ request: SchedulingRecoveryRequest; resolve: (result: SchedulingRecoveryResult) => void }> = [];
const notifications: string[] = [];
const checkRequests: string[] = [];
let settledChecks = 0;
let holdNextBoard = false;
let releaseBoard: (() => void) | null = null;
const board = {
  generatedAt: Date.now(),
  summary: { blocked: 2, quota: 2, authTransient: 0, recheck: 2, overlap: 0, excluded: 0 },
  accounts: Object.values(accounts),
};

routingPolicyApi.get = () => {
  if (!holdNextBoard) return Promise.resolve(board);
  holdNextBoard = false;
  return new Promise((resolve) => { releaseBoard = () => resolve(board); });
};
routingPolicyApi.check = (request) => {
  checkRequests.push(request.authId);
  return new Promise((resolve) => pending.push({ request, resolve }));
};

function Harness() {
  const [name, setName] = useState<AccountName | null>(null);
  Object.assign(window, {
    recoveryRace: {
      open: (next: AccountName) => setName(next),
      close: () => setName(null),
      pending: () => pending.map(({ request }) => request.authId),
      checkRequests: () => [...checkRequests],
      settledChecks: () => settledChecks,
      notifications: () => [...notifications],
      holdNextBoard: () => { holdNextBoard = true; },
      boardIsHeld: () => releaseBoard !== null,
      releaseBoard: () => {
        const release = releaseBoard;
        releaseBoard = null;
        release?.();
      },
      resolveOldest: () => {
        const next = pending.shift();
        if (!next) throw new Error('No pending check');
        next.resolve({ after: accounts[next.request.authId as AccountName], phases: [{ source: 'inspection', status: 'completed' }] });
        setTimeout(() => { settledChecks += 1; }, 0);
      },
    },
  });
  return (
    <>
      <button id="open-a" onClick={() => setName('A')}>Open A</button>
      <button id="open-b" onClick={() => setName('B')}>Open B</button>
      <SchedulingRecoveryDialog
        open={name !== null}
        authId={name ?? ''}
        authIndex={name ? accounts[name].authIndex : ''}
        accountName={name ?? ''}
        onClose={() => setName(null)}
        onResult={(result) => { notifications.push(result?.after?.authId ?? 'empty'); }}
      />
    </>
  );
}

createRoot(document.getElementById('root')!).render(<Harness />);
