// Run with the documented stdin workflow:
// (printf '%s\n' "globalThis.inspectionReceiptConfig={spaceId:3,url:'http://127.0.0.1:5189/review.html',artifact:'/tmp/inspection-receipt-results.json'};"; \
//   cat scripts/validation/inspection_receipt_smoke.mjs) | ego-browser nodejs
//
// The fixture must serve fixtures/inspection-receipt/main.tsx as /review.tsx
// and fixtures/inspection-receipt/index.html as /review.html in a generated
// Management checkout. Reuse one TaskSpace; this runner intentionally does not
// finish it because the main validation flow may inspect the final page.

const fs = await import('node:fs/promises');
const path = await import('node:path');

const suppliedConfig = globalThis.inspectionReceiptConfig || {};
const config = {
  spaceId: Number(suppliedConfig.spaceId ?? 3),
  url: suppliedConfig.url || 'http://127.0.0.1:5189/review.html',
  artifact: suppliedConfig.artifact || '/tmp/inspection-receipt-results.json',
  captureScreenshots: suppliedConfig.captureScreenshots !== false,
};
if (!Number.isInteger(config.spaceId) || config.spaceId <= 0) {
  throw new Error(`Invalid inspection receipt TaskSpace id: ${String(suppliedConfig.spaceId ?? '')}`);
}

const artifactBase = config.artifact.endsWith('.json')
  ? config.artifact.slice(0, -'.json'.length)
  : config.artifact;
const screenshots = {
  desktop: {
    path: `${artifactBase}.desktop.png`,
    status: config.captureScreenshots ? 'pending' : 'not-captured',
  },
  mobile: {
    path: `${artifactBase}.mobile.png`,
    status: config.captureScreenshots ? 'pending' : 'not-captured',
  },
};
const screenshotErrors = [];
await fs.mkdir(path.dirname(config.artifact), { recursive: true });

const task = await taskSpace(config.spaceId);
const page = task.page('p1');
const assertions = [];
const observations = {};

function record(name, passed, evidence) {
  assertions.push({ name, passed, evidence });
}

function countStates(rows) {
  const counts = {};
  for (const row of rows) counts[row.state] = (counts[row.state] || 0) + 1;
  return counts;
}

function sameCounts(actual, expected) {
  const keys = new Set([...Object.keys(actual), ...Object.keys(expected)]);
  return [...keys].every((key) => (actual[key] || 0) === (expected[key] || 0));
}

async function setViewport(width, height, deviceScaleFactor = 1) {
  await page.cdp('Emulation.setDeviceMetricsOverride', {
    width,
    height,
    deviceScaleFactor,
    mobile: width <= 520,
    screenWidth: width,
    screenHeight: height,
  });
}

async function inspectScenario(name) {
  await page.selectOption('#inspection-receipt-scenario', name);
  await page.waitForFunction(
    (scenario) => document.documentElement.dataset.inspectionReceiptScenario === scenario,
    name,
  );
  await page.waitForSelector('[data-testid="inspection-batch-receipt"]');
  await page.waitForFunction(
    (scenario) => window.inspectionReceiptFixture?.scenario === scenario
      && document.querySelector('[data-testid="inspection-batch-receipt"]'),
    name,
  );
  const detailsOpen = await page.evaluate(() => {
    const details = document.querySelector('[data-testid="inspection-batch-receipt"] details');
    return details instanceof HTMLDetailsElement && details.open;
  });
  if (!detailsOpen) {
    await page.click('[data-testid="inspection-batch-receipt"] summary');
  }
  await page.waitForSelector('[data-testid="inspection-batch-receipt"] [data-receipt-state]');
  const observed = await page.evaluate(() => {
    const receipt = document.querySelector('[data-testid="inspection-batch-receipt"]');
    if (!(receipt instanceof HTMLElement)) throw new Error('receipt missing');
    const rows = [...receipt.querySelectorAll('[data-receipt-state]')].map((node) => ({
      state: node.getAttribute('data-receipt-state') || '',
      account: node.querySelector(':scope > strong')?.textContent?.trim() || '',
      text: node.textContent?.replace(/\s+/g, ' ').trim() || '',
    }));
    return {
      ariaLabel: receipt.getAttribute('aria-label') || '',
      text: receipt.textContent?.replace(/\s+/g, ' ').trim() || '',
      summaryText: [...receipt.querySelectorAll('[role="status"] span')]
        .map((node) => node.textContent?.replace(/\s+/g, ' ').trim() || ''),
      rows,
      top: receipt.getBoundingClientRect().top,
      width: receipt.getBoundingClientRect().width,
      viewport: { width: window.innerWidth, height: window.innerHeight },
    };
  });
  observations[name] = observed;
  record(`${name}: user-facing title`, observed.ariaLabel === '操作后账号状态', observed.ariaLabel);
  record(`${name}: all account details rendered`, observed.rows.length > 0, observed.rows);
  return observed;
}

await setViewport(1440, 1000);
await page.goto(config.url);
await page.waitForSelector('#inspection-receipt-scenario');
await page.waitForSelector('[data-testid="inspection-batch-receipt"]');

const recheck = await inspectScenario('recheck-mixed');
const recheckCounts = countStates(recheck.rows);
record('recheck: account health categories replace execution conclusion', sameCounts(recheckCounts, {
  healthy: 1,
  quotaExhausted: 1,
  reauthorizationRequired: 1,
  accountInvalid: 1,
  unknown: 1,
}), { counts: recheckCounts, summary: recheck.summaryText });
record('recheck: abnormal accounts appear before healthy',
  recheck.rows.at(-1)?.state === 'healthy'
    && recheck.rows.slice(0, -1).every((row) => row.state !== 'healthy'),
  recheck.rows,
);
record('recheck: execution success is secondary evidence',
  recheck.text.includes('执行情况：完成 5')
    && recheck.text.indexOf('操作后账号状态') < recheck.text.indexOf('执行情况：完成 5'),
  recheck.text,
);
const reauthorization = recheck.rows.find((row) => row.account === 'reauthorization-required');
record('recheck: result error detail remains visible',
  reauthorization?.text.includes('fixture authorization detail') === true
    && reauthorization.text.includes('inspection_http_error'),
  reauthorization,
);

const actions = await inspectScenario('action-effects');
const actionCounts = countStates(actions.rows);
record('actions: post-action administrative states are explicit', sameCounts(actionCounts, {
  enabled: 1,
  disabled: 1,
  deleted: 1,
  quotaProtected: 1,
}), { counts: actionCounts, rows: actions.rows, summary: actions.summaryText });

const recovery = await inspectScenario('recovery-edges');
const recoveryCounts = countStates(recovery.rows);
record('recovery: cleared, restricted, and unknown outcomes stay distinct', sameCounts(recoveryCounts, {
  restrictionsCleared: 1,
  stillRestricted: 1,
  unknown: 3,
}), { counts: recoveryCounts, rows: recovery.rows, summary: recovery.summaryText });
const nestedWarning = recovery.rows.find((row) => row.account === 'nested-warning');
record('recovery: nested warning remains visible',
  nestedWarning?.text.includes('nested fixture warning: follow-up required') === true,
  nestedWarning,
);
const stillRestricted = recovery.rows.find((row) => row.account === 'still-restricted');
record('recovery: failure and final restriction reason remain visible',
  stillRestricted?.text.includes('fixture recovery check failed') === true
    && stillRestricted.text.includes('fixture quota restriction'),
  stillRestricted,
);
record('recovery: abnormal rows precede cleared state',
  recovery.rows.at(-1)?.state === 'restrictionsCleared'
    && recovery.rows.slice(0, -1).every((row) => row.state !== 'restrictionsCleared'),
  recovery.rows,
);

const execution = await inspectScenario('execution-states');
const executionCounts = countStates(execution.rows);
record('execution states: running stays waiting and skipped/interrupted stay unknown', sameCounts(executionCounts, {
  waiting: 1,
  unknown: 3,
}), { counts: executionCounts, rows: execution.rows });
record('execution states: transport counters remain available',
  execution.text.includes('待处理 1')
    && execution.text.includes('跳过 2')
    && execution.text.includes('中断 1'),
  execution.text,
);

await setViewport(390, 844, 2);
await page.selectOption('#inspection-receipt-scenario', 'recovery-edges');
await page.waitForFunction(() => document.documentElement.dataset.inspectionReceiptScenario === 'recovery-edges');
await page.waitForSelector('[data-testid="inspection-batch-receipt"]');
await page.evaluate(() => {
  const details = document.querySelector('[data-testid="inspection-batch-receipt"] details');
  if (details instanceof HTMLDetailsElement) details.open = true;
  document.querySelector('[data-testid="inspection-batch-receipt"]')?.scrollIntoView({ block: 'start' });
});
const mobileLayout = await page.evaluate(() => {
  const receipt = document.querySelector('[data-testid="inspection-batch-receipt"]');
  const toolbar = document.querySelector('.inspection-receipt-fixture-toolbar');
  const bodyWidth = document.documentElement.scrollWidth;
  return {
    viewportWidth: window.innerWidth,
    bodyWidth,
    receiptWidth: receipt?.getBoundingClientRect().width || 0,
    toolbarWidth: toolbar?.getBoundingClientRect().width || 0,
    titleVisible: receipt?.textContent?.includes('操作后账号状态') || false,
  };
});
record('mobile: receipt and fixture selector fit the viewport',
  mobileLayout.viewportWidth === 390
    && mobileLayout.bodyWidth <= 390
    && mobileLayout.receiptWidth > 0
    && mobileLayout.receiptWidth <= 390
    && mobileLayout.toolbarWidth <= 390
    && mobileLayout.titleVisible,
  mobileLayout,
);
const artifact = {
  taskSpaceId: task.spaceId,
  page: page.label,
  url: config.url,
  screenshots,
  screenshotErrors,
  assertions,
  observations,
};
// Persist semantic evidence before optional image capture. A browser-level
// Page.captureScreenshot timeout must not erase the repeatable DOM results.
await fs.writeFile(config.artifact, `${JSON.stringify(artifact, null, 2)}\n`);

async function capture(name, scenario, width, height, deviceScaleFactor, outputPath) {
  try {
    await setViewport(width, height, deviceScaleFactor);
    await page.selectOption('#inspection-receipt-scenario', scenario);
    await page.waitForFunction(
      (expected) => document.documentElement.dataset.inspectionReceiptScenario === expected,
      scenario,
    );
    await page.waitForSelector('[data-testid="inspection-batch-receipt"]');
    await page.evaluate(() => {
      const details = document.querySelector('[data-testid="inspection-batch-receipt"] details');
      if (details instanceof HTMLDetailsElement) details.open = true;
      document.querySelector('[data-testid="inspection-batch-receipt"]')?.scrollIntoView({ block: 'start' });
    });
    await page.screenshot({ path: outputPath });
    screenshots[name].status = 'captured';
  } catch (error) {
    screenshots[name].status = 'failed';
    screenshotErrors.push({
      name,
      path: outputPath,
      error: error instanceof Error ? error.message : String(error),
    });
  }
}

if (config.captureScreenshots) {
  await capture('desktop', 'recheck-mixed', 1440, 1000, 1, screenshots.desktop.path);
  await capture('mobile', 'recovery-edges', 390, 844, 2, screenshots.mobile.path);
}
await fs.writeFile(config.artifact, `${JSON.stringify(artifact, null, 2)}\n`);
const failedAssertions = assertions.filter((assertion) => !assertion.passed);
console.log(JSON.stringify({
  taskSpaceId: task.spaceId,
  assertionCount: assertions.length,
  passed: failedAssertions.length === 0,
  failedAssertions: failedAssertions.map(({ name, evidence }) => ({ name, evidence })),
  screenshots,
  screenshotErrors,
  artifact: config.artifact,
}, null, 2));
if (failedAssertions.length > 0) {
  throw new Error('Account inspection receipt browser regression');
}
