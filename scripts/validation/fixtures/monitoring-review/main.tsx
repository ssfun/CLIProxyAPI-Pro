import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { useUsageData, type UsagePayload } from '../src/pro/modules/monitoring/features/hooks/useUsageData';
import { useRealtimeLogData } from '../src/pro/modules/monitoring/features/hooks/useRealtimeLogData';
import { apiClient } from '../src/services/api/client';
import { useAuthStore } from '../src/stores/useAuthStore';

let price = 1, snapshotCalls = 0, failSnapshot = false, failPage = false;
let heldPrice: (() => void) | null = null, holdPrice = false;
let latestId = 0;
const pageCalls: string[] = [];
const pause = (ms = 20) => new Promise(resolve => setTimeout(resolve, ms));
const wait = async (predicate: () => boolean) => {
  for (let n = 0; n < 150; n++) { if (predicate()) return; await pause(); }
  throw new Error('condition timed out');
};
apiClient.get = async (url: string) => {
  if (url === '/usage') {
    snapshotCalls++;
    await pause();
    if (failSnapshot) throw new Error('snapshot unavailable');
    return { latest_id: 0, generation: 1, apis: {} } as never;
  }
  if (url === '/usage/model-prices') {
    const captured = price;
    if (holdPrice) {
      holdPrice = false;
      await new Promise<void>(resolve => { heldPrice = resolve; });
    }
    return { prices: { demo: { prompt: captured, completion: 0, cache: 0 } } } as never;
  }
  throw new Error(`Unexpected API: ${url}`);
};
// Keep SSE open until the production hook aborts it; no real network requests.
window.fetch = async (_input, init) => new Response(new ReadableStream({
  start(controller) { init?.signal?.addEventListener('abort', () => controller.close(), { once: true }); },
}), { headers: { 'Content-Type': 'text/event-stream' } });
useAuthStore.setState({ apiBase: 'http://fixture-a', managementKey: 'fixture', connectionStatus: 'connected' });
const filters = () => ({});
const generationChange = () => {};
const loadPage = async ({ cursor = '' }: { cursor?: string }) => {
  pageCalls.push(cursor);
  if (failPage) throw new Error('page unavailable');
  const page = cursor === 'c2' ? 2 : cursor === 'c3' ? 3 : 1;
  return { usage: { apis: {} }, matchedTotal: 300, pageCursor: page === 1 ? 'c1' : cursor,
    nextCursor: page === 1 ? 'c2' : page === 2 ? 'c3' : '', hasMore: page < 3, snapshotMaxId: latestId };
};
let current: ReturnType<typeof useUsageData>;
let logs: ReturnType<typeof useRealtimeLogData>;
let update: (n: number) => void;
let setFollow: (value: boolean) => void;
let setDetails: (value: boolean) => void;
function App() {
  current = useUsageData();
  const [usage, setUsage] = useState<UsagePayload | null>(null);
  const [id, setId] = useState(0);
  const [follow, followSetter] = useState(true);
  const [details, detailsSetter] = useState(false);
  const [report, setReport] = useState<unknown>(null);
  update = setId; setFollow = followSetter; setDetails = detailsSetter;
  logs = useRealtimeLogData({ connectionStatus: 'connected', latestId: id, generation: 1,
    usage, setUsage, loadEventPage: loadPage, buildFilters: filters, followEnabled: follow,
    detailsOpen: details, onGenerationChange: generationChange });
  return <main><h1>Monitoring regression</h1><button onClick={async () => setReport(await run())}>Run</button>
    <p>Refreshing: {String(current.refreshing)}; price: {current.modelPrices.demo?.prompt}; page: {logs.page}</p>
    <pre id="report">{JSON.stringify(report, null, 2)}</pre></main>;
}
async function run() {
  const results: { name: string; pass: boolean; error?: string }[] = [];
  const check = async (name: string, body: () => Promise<void>) => {
    try { await body(); results.push({ name, pass: true }); }
    catch (e) { results.push({ name, pass: false, error: String(e) }); }
  };
  await wait(() => current.modelPricesReady && !current.loading && pageCalls.length > 0);
  await check('manual refresh releases spinner and publishes price', async () => {
    price = 2; await current.refreshUsage();
    await wait(() => !current.refreshing && current.modelPrices.demo?.prompt === 2);
    const before = snapshotCalls; await current.refreshUsage();
    if (snapshotCalls !== before + 1) throw new Error('second refresh suppressed');
  });
  await check('failed refresh releases spinner and allows retry', async () => {
    failSnapshot = true; await current.refreshUsage(); await pause();
    if (current.refreshing) throw new Error('stuck refreshing');
    failSnapshot = false; const before = snapshotCalls; await current.refreshUsage();
    if (snapshotCalls !== before + 1) throw new Error('retry suppressed');
  });
  failSnapshot = false;
  await check('connection switch rejects stale price', async () => {
    holdPrice = true; price = 3; const refreshing = current.refreshUsage();
    await wait(() => heldPrice !== null);
    price = 4; useAuthStore.setState({ apiBase: 'http://fixture-b' });
    await wait(() => current.modelPrices.demo?.prompt === 4);
    heldPrice!(); await refreshing; await pause();
    if (current.modelPrices.demo?.prompt !== 4 || current.refreshing) throw new Error('stale refresh published');
  });
  // Unblock even on the old implementation so later scenarios remain independent.
  heldPrice?.(); holdPrice = false;
  await check('first event follows empty snapshot', async () => {
    const before = pageCalls.length; latestId = 1; update(1);
    await wait(() => pageCalls.length > before && logs.snapshotMaxId === 1);
  });
  await check('failed refresh preserves previous-page cursor', async () => {
    await logs.showNextPage(); await pause(); await logs.showNextPage(); await pause();
    if (logs.page !== 3) throw new Error('did not reach page 3');
    failPage = true; await logs.refresh(); await pause(); failPage = false;
    await logs.showPreviousPage(); await pause();
    if (pageCalls.at(-1) !== 'c2') throw new Error(`wrong cursor: ${pageCalls.at(-1)}`);
  });
  await check('follow pauses for page, details and disabled preference', async () => {
    if (!logs.autoRefreshPaused) throw new Error('page 2 did not pause');
    setFollow(false); await logs.refresh(); await pause();
    if (!logs.autoRefreshPaused) throw new Error('disabled follow did not pause');
    setFollow(true); setDetails(true); await pause();
    if (!logs.autoRefreshPaused) throw new Error('details did not pause');
  });
  return { results, snapshotCalls, pageCalls };
}
createRoot(document.getElementById('root')!).render(<App />);
