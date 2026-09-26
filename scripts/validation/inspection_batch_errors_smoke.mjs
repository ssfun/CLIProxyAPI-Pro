// Run through ego-browser nodejs, with globalThis.inspectionBatchErrorConfig
// specifying { spaceId, url, artifact }. Uses the inspection-receipt fixture.
const config = globalThis.inspectionBatchErrorConfig;
if (!config?.spaceId || !config?.url || !config?.artifact) throw new Error('Missing browser fixture configuration');
const task = await taskSpace(config.spaceId);
const page = task.page('p1');
const assertions = [];
const fs = await import('node:fs/promises');
const check = (name, passed, evidence) => assertions.push({ name, passed, evidence });
await page.cdp('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1200, deviceScaleFactor: 1, mobile: false });
await page.goto(config.url);
await page.waitForSelector('#inspection-batch-error-mode');
console.log(await page.snapshot({ scope: 'full_page' }));

async function readError() {
  await page.waitForSelector('[data-testid="inspection-batch-error"]');
  const details = '[data-testid="inspection-batch-error"] details';
  const closed = await page.evaluate(() => {
    const element = document.querySelector('[data-testid="inspection-batch-error"] details');
    return element && !element.open;
  });
  if (closed) await page.click(`${details} summary`);
  return page.evaluate(() => {
    const panel = document.querySelector('[data-testid="inspection-batch-error"]');
    return {
      text: panel.textContent,
      role: panel.querySelector('[role="alert"], [role="status"]')?.getAttribute('role'),
      items: panel.querySelectorAll('[role="listitem"]').length,
      requests: window.inspectionReceiptFixture.requestCount,
    };
  });
}
async function recover(mode) {
  await page.selectOption('#inspection-batch-error-mode', mode);
  const checkbox = 'input[aria-label="选择账号 page-health-snapshot@fixture.invalid"]';
  if (!(await page.evaluate((selector) => document.querySelector(selector)?.checked, checkbox))) {
    await page.evaluate((selector) => document.querySelector(selector)?.scrollIntoView({ block: 'center' }), checkbox);
    await page.click(checkbox);
  }
  await page.click('loc=role:button[name="恢复调度"]');
  return readError();
}
const cases = [
  ['no-restriction', '无需操作', 'status', 1],
  ['disabled', '所选账号均已禁用', 'alert', 1],
  ['stale', '请重新巡检后再试', 'alert', 1],
  ['mixed', '本次没有可执行的账号', 'alert', 3],
  ['unknown', 'fixture upstream diagnostic 503', 'alert', 0],
  ['malformed', '本次没有可执行的账号', 'alert', 2],
  ['unstructured', '本次没有可执行的账号', 'alert', 0],
];
for (const [mode, expected, role, items] of cases) {
  const observed = await recover(mode);
  check(mode, observed.text.includes(expected) && observed.role === role && observed.items === items, observed);
  check(`${mode}: no raw internal error`, !observed.text.includes('batch has no executable targets'), observed.text);
}
await recover('no-restriction');
const beforeLanguageSwitch = await readError();
for (const [language, expected] of [['en', 'No action is needed'], ['ru', 'Действия не требуются'], ['zh-TW', '無需操作'], ['zh-CN', '无需操作']]) {
  await page.selectOption('#inspection-fixture-language', language);
  await page.waitForFunction((text) => document.querySelector('[data-testid="inspection-batch-error"]')?.textContent.includes(text), expected);
  const observed = await readError();
  check(`language ${language}`, observed.text.includes(expected) && observed.requests === beforeLanguageSwitch.requests, observed);
}
// A successful start must clear the previous rejection and show its own receipt.
await page.selectOption('#inspection-batch-error-mode', 'none');
await page.click('loc=role:button[name="恢复调度"]');
await page.waitForFunction(() => !document.querySelector('[data-testid="inspection-batch-error"]') && document.querySelector('[data-testid="inspection-batch-receipt"]'));
check('success clears rejection', true, await page.evaluate(() => document.querySelector('[data-testid="inspection-batch-receipt"]')?.textContent));
// Seed a completed batch with a failed recovery and exercise the actual retry button.
await page.selectOption('#inspection-receipt-scenario', 'recovery-edges');
await page.selectOption('#inspection-batch-error-mode', 'expired');
await page.click('loc=role:button[name="重试 1 个失败或过期项"]');
const retried = await readError();
check('retry uses localized error', retried.text.includes('操作预检已过期') && !retried.text.includes('preflight'), retried);
const receiptText = await page.evaluate(() => document.querySelector('[data-testid="inspection-batch-receipt"]')?.textContent);
check('receipt internal reason localized', receiptText.includes('检查已完成，账号仍有限制') && receiptText.includes('fixture quota restriction'), receiptText);
const artifact = { url: config.url, assertions, passed: assertions.every((entry) => entry.passed) };
await fs.writeFile(config.artifact, `${JSON.stringify(artifact, null, 2)}\n`);
console.log(JSON.stringify({ artifact: config.artifact, passed: artifact.passed, assertions: assertions.length, failures: assertions.filter((entry) => !entry.passed) }));
if (!artifact.passed) throw new Error('Batch error localization browser validation failed');
