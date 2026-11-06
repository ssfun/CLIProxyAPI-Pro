const task = await taskSpace(config.taskSpaceId ?? 'recovery-entry');
const page = task.page('p1');
const evidence = {};
const ensure = (value, label) => { if (!value) throw new Error(label); };
const dialogBody = () => page.evaluate(() => document.querySelector('[role="dialog"] .modal-body')?.textContent ?? '');
const closeDialog = async () => {
  await page.click('[role="dialog"] .modal-close-floating');
};
try {
  await page.goto(config.url);
  await page.waitForFunction(() => [...document.querySelectorAll('button')].some((button) => button.textContent.trim() === 'Restrictions and recovery'));
  ensure(await page.evaluate(() => document.body.textContent.includes('Use Inspection Control to generate account health')), 'real page has zero history');
  ensure(await page.evaluate(() => window.recoveryEntry.gets() === 0), 'board must load on demand');
  await page.click('loc=role:button[name="Restrictions and recovery"]');
  await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('api-index'));
  evidence.zeroHistoryPicker = await dialogBody();
  ensure(!evidence.zeroHistoryPicker.includes('SECRET'), 'picker leaked secret');
  await page.fill('input[aria-label="Search provider or account index"]', 'openai-compat');
  await page.click('loc=role:button[name="openai-compat · #api-index"]');
  await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('upstream-only'));
  evidence.apiKeyUpstreamOnly = await dialogBody();
  ensure(!evidence.apiKeyUpstreamOnly.includes('SECRET'), 'dialog leaked secret');
  await closeDialog();
  await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('overlap-index'));
  await page.click('loc=role:button[name="codex · #overlap-index"]');
  await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('inspection-remains'));
  await page.click('[role="dialog"] details summary');
  await page.evaluate(() => {
    const buttons = [...document.querySelectorAll('[role="dialog"] details button')];
    buttons.at(-1).click();
  });
  await page.evaluate(() => {
    const buttons = [...document.querySelectorAll('[role="dialog"] details button')];
    buttons.find((button) => button.textContent.includes('Confirm')).click();
  });
  await page.waitForFunction(() => window.recoveryEntry.releases().length === 1);
  await page.waitForFunction(() => !document.querySelector('[role="dialog"] .modal-body')?.textContent.includes('upstream-remove'));
  evidence.release = await page.evaluate(() => window.recoveryEntry.releases()[0]);
  ensure(evidence.release.authId === 'overlap' && evidence.release.authIndex === 'overlap-index' && evidence.release.registrationEpoch === '8' && evidence.release.source === 'upstream' && evidence.release.model === 'cooling-model' && evidence.release.revision === 'u2', 'targeted identity/revision');
  ensure((await dialogBody()).includes('inspection-remains'), 'other source removed');
  await closeDialog();
  await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('overlap-index'));
  await page.evaluate(() => window.recoveryEntry.fail(true));
  await page.click('loc=role:button[name="Refresh"]');
  await page.waitForFunction(() => document.querySelector('[role="alert"]')?.textContent.includes('Controlled refresh failure'));
  evidence.refreshError = await dialogBody();
  ensure(!evidence.refreshError.includes('overlap-index'), 'stale rows after refresh error');
  await page.evaluate(() => { window.recoveryEntry.fail(false); window.recoveryEntry.empty(true); });
  await page.click('loc=role:button[name="Refresh"]');
  await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('No matching restricted accounts'));
  evidence.empty = await dialogBody();
  // An old board request must not survive close/reopen or a connection change.
  for (const changeConnection of [false, true]) {
    await page.evaluate(() => { window.recoveryEntry.empty(false); window.recoveryEntry.hold(); });
    await page.click('loc=role:button[name="Refresh"]');
    await page.waitForFunction(() => window.recoveryEntry.pending());
    if (changeConnection) await page.evaluate(() => window.recoveryEntry.switchConnection());
    else await closeDialog();
    await page.waitForFunction(() => !document.querySelector('[role="dialog"]'));
    await page.evaluate(() => window.recoveryEntry.empty(true));
    await page.click('loc=role:button[name="Restrictions and recovery"]');
    await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent.includes('No matching restricted accounts'));
    await page.evaluate(() => window.recoveryEntry.resolve());
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    ensure(!(await dialogBody()).includes('api-index'), 'stale board crossed picker session');
  }
  evidence.sessionIsolation = true;
  const fs = await import('node:fs/promises');
  await fs.mkdir(config.artifactDir, { recursive: true });
  await page.screenshot({ path: `${config.artifactDir}/entry-empty.png` });
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify({ passed: true, evidence }, null, 2));
  console.log(JSON.stringify({ passed: true, artifact: `${config.artifactDir}/result.json` }));
  if (!config.keepTaskSpace) await task.finish({ keep: [] });
} catch (error) { console.error(`Ego task space ${task.spaceId} retained`); throw error; }
