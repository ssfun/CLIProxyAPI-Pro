const task = await taskSpace(config.spaceId);
const page = task.page('p1');
const fs = await import('node:fs/promises');
await fs.mkdir(config.artifactDir, { recursive: true });
const checks = [];
try {
  await page.cdp('Emulation.setDeviceMetricsOverride', { width: 1200, height: 1000, deviceScaleFactor: 1, mobile: false });
  await page.goto(config.url);
  await page.waitForSelector('[data-host="quota"]');
  for (const width of config.before ? [1200] : [1200, 390]) {
    await page.cdp('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: width < 500 });
    for (const scenario of config.before ? ['retained'] : ['retained', 'failed', 'recovered', 'empty']) {
      await page.selectOption('#scenario', scenario);
      const hosts = await page.evaluate(() => [...document.querySelectorAll('[data-host]')].map((host) => {
        const name = host.getAttribute('data-host');
        const classes = window.codexDetailE2E[name];
        return { name, text: host.textContent,
          errors: host.querySelectorAll(`.${classes.codexResetCreditsError}`).length,
          rows: host.querySelectorAll(`.${classes.codexResetCreditRow}`).length,
        };
      }));
      for (const host of hosts) {
        const expectedRows = ['retained', 'recovered'].includes(scenario) ? 1 : 0;
        const expectedErrors = config.before ? 0 : ['retained', 'failed'].includes(scenario) ? 1 : 0;
        const passed = host.rows === expectedRows && host.errors === expectedErrors
          && (expectedErrors === 0 || host.text.includes('HTTP 503'));
        checks.push({ width, scenario, ...host, passed });
        if (!passed) throw new Error(`incorrect details/error display: ${JSON.stringify(host)}`);
      }
    }
  }
  await page.selectOption('#scenario', 'retained');
  const receipt = { passed: true, before: config.before, checks, screenshotError: null };
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify(receipt, null, 2));
  await fs.writeFile(`${config.artifactDir}/page.txt`, await page.snapshot());
  try { await page.screenshot({ path: `${config.artifactDir}/page.png`, fullPage: true }); }
  catch (error) { receipt.screenshotError = String(error); }
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify(receipt, null, 2));
  console.log(JSON.stringify({ passed: true, checks: checks.length, artifact: config.artifactDir }));
} catch (error) {
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify({ passed: false, checks, error: String(error) }, null, 2));
  throw error;
}
