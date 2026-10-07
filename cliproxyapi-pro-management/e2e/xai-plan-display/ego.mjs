const task = await taskSpace(config.spaceId);
const page = task.page('p1');
const fs = await import('node:fs/promises');
await fs.mkdir(config.artifactDir, { recursive: true });
const checks = [];
const read = () => page.evaluate(() => ({
  text: document.querySelector('#card').textContent,
  plans: [...document.querySelector('#card').querySelectorAll(window.xaiPlanE2E.planSelector)]
    .filter((node) => node.textContent === '套餐').length,
  billing: window.xaiPlanE2E.billing(),
}));
const check = (name, passed, evidence) => {
  checks.push({ name, passed, evidence });
  if (!passed) throw new Error(name);
};
try {
  await page.cdp('Emulation.setDeviceMetricsOverride', {
    width: 1200, height: 900, deviceScaleFactor: 1, mobile: false,
  });
  await page.goto(config.url);
  await page.waitForSelector('#card[data-status="success"]');
  let state = await read();
  check('inspection fallback', state.plans === 1, state);
  await page.click('#refresh');
  await page.waitForFunction(() => window.xaiPlanE2E.requests.filter((url) => /\/user\?|\/settings$/.test(url)).length === 2);
  state = await read();
  check('pending enrichment fallback', state.plans === 1 && !state.billing.planLabel, state);
  await page.click('#release');
  await page.waitForFunction(() => Boolean(window.xaiPlanE2E.billing()?.planLabel));
  state = await read();
  check(config.before ? 'reproduced duplicate' : 'enriched plan rendered once', state.plans === (config.before ? 2 : 1), state);
  if (!config.before) {
    await page.click('#refresh');
    await page.waitForFunction(() => window.xaiPlanE2E.requests.filter((url) => /\/user\?|\/settings$/.test(url)).length === 4);
    await page.click('#fail');
    // A browser task turn lets both rejected enrichment requests settle.
    await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 30)));
    state = await read();
    check('subscription failure preserves fallback', state.plans === 1 && state.billing.planType === 'x-premium-plus', state);
    await page.click('#refresh');
    await page.waitForFunction(() => window.xaiPlanE2E.requests.filter((url) => /\/user\?|\/settings$/.test(url)).length === 6);
    await page.click('#release');
    await page.waitForFunction(() => Boolean(window.xaiPlanE2E.billing()?.planLabel));
    state = await read();
    check('subscription recovery', state.plans === 1, state);
    const scenarios = await page.evaluate(() => [...document.querySelector('#scenario').options].map((option) => option.value));
    for (const scenario of scenarios) {
      await page.selectOption('#scenario', scenario);
      state = await read();
      check(`${scenario}: single plan`, state.plans === 1, state);
      if (scenario.startsWith('free')) {
        check(`${scenario}: token row retained`, state.text.includes('grok-4.5'), state.text);
        if (scenario === 'free') check('Free pending observation', state.text.includes('待探测'), state.text);
        else check('Free observed quota', state.text.includes('75%'), state.text);
      }
    }
    await page.selectOption('#scenario', 'inspection');
    await page.click('#refresh');
    await page.waitForFunction(() => window.xaiPlanE2E.requests.filter((url) => /\/user\?|\/settings$/.test(url)).length === 8);
    await page.click('#clear');
    await page.click('#release');
    await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 30)));
    state = await read();
    check('late enrichment cannot restore cleared account', !state.billing && state.plans === 0, state);
    await page.selectOption('#scenario', 'enriched-x-premium-plus');
  }
  const receipt = { passed: true, before: config.before, checks,
    requests: await page.evaluate(() => window.xaiPlanE2E.requests), screenshot: null, screenshotError: null };
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify(receipt, null, 2));
  try {
    await page.screenshot({ path: `${config.artifactDir}/xai-plan.png`, fullPage: true });
    receipt.screenshot = `${config.artifactDir}/xai-plan.png`;
  } catch (error) { receipt.screenshotError = String(error); }
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify(receipt, null, 2));
  console.log(JSON.stringify({ passed: true, checks: checks.length, artifact: `${config.artifactDir}/result.json` }));
} catch (error) {
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify({ passed: false, checks, error: String(error) }, null, 2));
  throw error;
}
