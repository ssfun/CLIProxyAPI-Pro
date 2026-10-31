// Run with: ego-browser nodejs < scripts/validation/e2e/management-login/check.mjs
// Set spaceId to resume an existing task; URL must serve a production build.
const fs = await import('node:fs/promises');
const assert = (await import('node:assert/strict')).default;
const url = 'http://127.0.0.1:4179/';
const output = '/tmp/management-login-e2e';
const spaceId = null;
await fs.mkdir(output, { recursive: true });
const task = await taskSpace(spaceId ?? 'Management login regression');
const page = task.page('p1');
const checks = [];
try {
  await page.cdp('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await page.goto(`${url}#/login`);
  await page.waitForSelector('a[href="#/usage"]');
  assert.equal(await page.evaluate(() => document.querySelectorAll('a[href="#/usage"]').length), 1);
  // Preserve upstream empty-key validation and verify the public link still works afterward.
  await page.click('button[type="submit"]');
  await page.waitForSelector('[role="alert"]');
  checks.push('anonymous login has one usage link and retains empty-key validation');
  await fs.writeFile(`${output}/login-desktop.txt`, await page.snapshot());
  await page.click('a[href="#/usage"]');
  await page.waitForSelector('#query-key');
  assert.match(await page.url(), /#\/usage$/);
  await page.reload();
  await page.waitForSelector('#query-key');
  assert.match(await page.url(), /#\/usage$/);
  checks.push('usage link and reload remain public without management credentials');
  await fs.writeFile(`${output}/usage.txt`, await page.snapshot());
  await page.cdp('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await page.goto(`${url}#/login`);
  await page.waitForSelector('a[href="#/usage"]');
  await page.click('a[href="#/usage"]');
  await page.waitForSelector('#query-key');
  assert.match(await page.url(), /#\/usage$/);
  checks.push('mobile login usage link is actionable');
  await page.goto(`${url}#/login`);
  await page.waitForSelector('a[href="#/usage"]');
  await fs.writeFile(`${output}/login-mobile.txt`, await page.snapshot());
  await fs.writeFile(`${output}/receipt.json`, JSON.stringify({ passed: true, url, checks }, null, 2));
  console.log({ output, checks });
  await task.finish({ keep: [] });
} catch (error) {
  await fs.writeFile(`${output}/receipt.json`, JSON.stringify({ passed: false, url, checks, error: String(error), spaceId: task.spaceId }, null, 2));
  throw error;
}
