// run.sh prefixes this script with `const config = {...}`.
const task = await taskSpace(config.taskSpaceId ?? 'recovery-dialog-race');
const page = task.page('p1');
const evidence = {};
const readDialog = () => page.evaluate(() => ({
  title: document.querySelector('[role="dialog"] .modal-title')?.textContent ?? '',
  body: document.querySelector('[role="dialog"] .modal-body')?.textContent ?? '',
  notifications: window.recoveryRace.notifications(),
  checks: window.recoveryRace.checkRequests(),
}));
const settle = async (count) => {
  await page.waitForFunction((expected) => window.recoveryRace.settledChecks() >= expected, count);
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
};
const switchTo = async (name) => {
  await page.evaluate(() => window.recoveryRace.close());
  await page.waitForFunction(() => !document.querySelector('[role="dialog"]'));
  await page.evaluate((target) => window.recoveryRace.open(target), name);
};
const ensure = (value, label) => { if (!value) throw new Error(`${label}: ${JSON.stringify(evidence)}`); };

try {
  await page.goto(config.url);
  console.log('stage: open A and start check');
  await page.click('#open-a');
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-A'));
  await page.click('loc=role:button[name="恢复调度"]');
  await page.waitForFunction(() => window.recoveryRace.pending().length === 1);
  await switchTo('B');
  console.log('stage: B opened');
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-B'));
  await page.evaluate(() => window.recoveryRace.resolveOldest());
  await settle(1);
  console.log('stage: old A settled in B');
  evidence.crossAccount = await readDialog();
  const oldAReachedB = evidence.crossAccount.title.includes('B') && evidence.crossAccount.body.includes('reason-A');
  ensure(oldAReachedB === config.expectBug, 'A result crossed into B dialog');
  ensure(evidence.crossAccount.notifications.length === (config.expectBug ? 1 : 0), 'stale parent notification');

  // The next real button click must use B's identity; before the fix it targets A.
  console.log('stage: B follow-up check');
  await page.click('loc=role:button[name="恢复调度"]');
  await page.waitForFunction(() => window.recoveryRace.pending().length === 1);
  evidence.followUp = await readDialog();
  ensure(evidence.followUp.checks.at(-1) === (config.expectBug ? 'A' : 'B'), 'follow-up check target');
  await page.evaluate(() => window.recoveryRace.resolveOldest());
  await settle(2);
  console.log('stage: B check settled');

  await switchTo('A');
  console.log('stage: same A reopen');
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-A'));
  await page.click('loc=role:button[name="恢复调度"]');
  await page.waitForFunction(() => window.recoveryRace.pending().length === 1);
  await switchTo('A');
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-A'));
  const priorNotifications = (await readDialog()).notifications.length;
  await page.evaluate(() => window.recoveryRace.resolveOldest());
  await settle(3);
  console.log('stage: old A settled after reopen');
  evidence.reopened = await readDialog();
  const oldOutcomeReachedNewA = evidence.reopened.body.includes('已完成');
  ensure(oldOutcomeReachedNewA === config.expectBug, 'old A outcome reached reopened A');
  ensure(evidence.reopened.notifications.length === priorNotifications + (config.expectBug ? 1 : 0), 'same-account stale parent notification');

  await switchTo('A');
  console.log('stage: close without reopen');
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-A'));
  await page.click('loc=role:button[name="恢复调度"]');
  await page.waitForFunction(() => window.recoveryRace.pending().length === 1);
  const beforeCloseNotifications = (await readDialog()).notifications.length;
  await page.evaluate(() => window.recoveryRace.close());
  await page.waitForFunction(() => !document.querySelector('[role="dialog"]'));
  await page.evaluate(() => window.recoveryRace.resolveOldest());
  await settle(4);
  evidence.closed = await page.evaluate(() => ({
    openDialogs: document.querySelectorAll('[role="dialog"]').length,
    notifications: window.recoveryRace.notifications(),
  }));
  ensure(evidence.closed.openDialogs === 0, 'closed dialog remained active');
  ensure(evidence.closed.notifications.length === beforeCloseNotifications + (config.expectBug ? 1 : 0), 'closed-session parent notification');

  await page.evaluate(() => window.recoveryRace.open('A'));
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-A'));
  await page.evaluate(() => { window.recoveryRace.holdNextBoard(); window.recoveryRace.open('B'); });
  await page.waitForFunction(() => window.recoveryRace.boardIsHeld());
  evidence.pendingBoard = await readDialog();
  ensure(evidence.pendingBoard.title.includes('B') && !evidence.pendingBoard.body.includes('reason-A'), 'A account shown under B title while B loads');
  await page.evaluate(() => window.recoveryRace.releaseBoard());
  await page.waitForFunction(() => document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('reason-B'));

  if (!config.expectBug) {
    evidence.restrictionDetails = await readDialog();
    const body = evidence.restrictionDetails.body;
    ensure(body.includes('upstream-model-reason') && body.includes('model-cooldown') && body.includes('HTTP 429') && body.includes('2030'), 'complete upstream restriction detail');
    ensure(body.includes('reason-B') && body.includes('全部模型'), 'credential-wide inspection restriction');
    await page.evaluate(() => { document.querySelector('[role="dialog"] details').open = true; });
    await page.evaluate(() => {
      const rows = [...document.querySelectorAll('[role="dialog"] details button')].filter((button) => button.textContent.trim() === '强制解除');
      rows.at(-1).click();
    });
    await page.evaluate(() => {
      const button = [...document.querySelectorAll('[role="dialog"] details button')].find((item) => item.textContent.trim() === '确认强制解除');
      button.click();
    });
    await page.waitForFunction(() => window.recoveryRace.releaseRequests().length === 1);
    await page.waitForFunction(() => !document.querySelector('[role="dialog"] .modal-body')?.textContent?.includes('upstream-model-reason'));
    evidence.targetedRelease = await page.evaluate(() => window.recoveryRace.releaseRequests()[0]);
    const request = evidence.targetedRelease;
    ensure(request.authId === 'B' && request.authIndex === 'index-B' && request.registrationEpoch === '1' && request.source === 'upstream' && request.model === 'model-cooldown' && request.revision === 'upstream-2', 'targeted release identity and revision');
    ensure((await readDialog()).body.includes('reason-B'), 'release preserved inspection restriction');
  }

  const fs = await import('node:fs/promises');
  await fs.mkdir(config.artifactDir, { recursive: true });
  const screenshot = `${config.artifactDir}/final-dialog.png`;
  await page.screenshot({ path: screenshot });
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify({ expectBug: config.expectBug, oldAReachedB, oldOutcomeReachedNewA, screenshot, evidence }, null, 2));
  console.log(JSON.stringify({ artifact: `${config.artifactDir}/result.json`, screenshot, oldAReachedB, oldOutcomeReachedNewA, evidence }));
  if (!config.keepTaskSpace) await task.finish({ keep: [] });
} catch (error) {
  console.error(`Ego task space ${task.spaceId} retained for inspection`);
  throw error;
}
