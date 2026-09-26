// Run with: ego-browser nodejs < scripts/validation/api_key_policy_page_smoke.mjs
// Serve fixtures/api_key_policy_page.tsx as /review.tsx in a generated Management
// checkout; /review.html must mount #root and load /review.tsx as a module.
// Prepend globalThis.akpReviewConfig = {spaceId, url, artifact} to this script.
// Reuse one existing TaskSpace. Fixtures contain no real keys.
const fs = await import('node:fs/promises');
const config = globalThis.akpReviewConfig;
const task = await taskSpace(config.spaceId);
const page = task.page('p1');
const results = [];
const copySelector = 'button[aria-label="api_key_policy.copy_key"]';
const copied = () => page.evaluate(() => !!document.querySelector('button[class*="copiedSuccess"]'));
async function setup(mode) {
  await page.goto(config.url);
  await page.waitForSelector(copySelector);
  await page.evaluate(mode => { window.review.mode = mode; }, mode);
}
async function record(name, passed, evidence) {
  results.push({ name, passed, evidence });
}
for (const mode of ['read-failed', 'clipboard-failed', 'success']) {
  await setup(mode);
  await page.click(copySelector);
  await page.waitForFunction(() => window.review.notifications.length > 0);
  const shown = await copied();
  await record(mode, shown === (mode === 'success'), {
    copied: shown, notifications: await page.evaluate(() => window.review.notifications),
  });
  if (mode === 'success') {
    await page.waitForFunction(() => !document.querySelector('button[class*="copiedSuccess"]'));
    await record('success feedback expires', true);
    await page.click(copySelector);
    await page.waitForFunction(() => !!document.querySelector('button[class*="copiedSuccess"]'));
    await page.click('button[aria-label="Refresh"]');
    await record('refresh clears feedback', !(await copied()));
    await page.click(copySelector);
    await page.waitForFunction(() => !!document.querySelector('button[class*="copiedSuccess"]'));
    await page.evaluate(() => { window.review.mode = 'clipboard-failed'; });
    await page.click(copySelector);
    await page.waitForFunction(() => window.review.notifications.at(-1) === 'api_key_policy.key_copy_failed');
    await record('failed retry clears previous success', !(await copied()));
  }
}
await setup('pending');
await page.click(copySelector);
await page.waitForFunction(() => window.review.release !== null);
await page.evaluate(() => window.review.disconnect());
await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
await page.evaluate(() => window.review.release());
// A frame checkpoint lets resolved promises and React updates finish, not a timed sleep.
await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
const notifications = await page.evaluate(() => window.review.notifications);
await record('stale clipboard completion is silent', notifications.length === 0, { notifications });
await fs.writeFile(config.artifact, JSON.stringify({results}, null, 2) + '\n');
console.log(JSON.stringify({results}, null, 2));
if (results.some(result => !result.passed)) throw new Error('API key policy page regression');
