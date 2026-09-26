const task = await taskSpace(config.spaceId ?? 'routing-board-actions');
const page = task.page('p1');
const evidence = {};
const ensure = (value, label) => { if (!value) throw new Error(`${label}: ${JSON.stringify(evidence)}`); };
const readPage = () => page.evaluate(() => ({
  body: document.body.textContent ?? '',
  dialogs: Array.from(document.querySelectorAll('[role="dialog"]')).map((item) => item.textContent ?? ''),
  actionGroups: Array.from(document.querySelectorAll('[data-routing-row-actions]')).map((item) =>
    Array.from(item.querySelectorAll('button')).map((button) => button.textContent?.trim() ?? '')
  ),
  scrollRegions: Array.from(document.querySelectorAll('[data-routing-scroll-region]')).map((region) => {
    const target = region.getAttribute('data-routing-scroll-region') === 'table'
      ? region.querySelector(':scope > div > div')
      : region;
    const style = getComputedStyle(region);
    return {
      kind: region.getAttribute('data-routing-scroll-region'),
      visible: style.display !== 'none' && region.getBoundingClientRect().height > 0,
      clientHeight: target?.clientHeight ?? 0,
      scrollHeight: target?.scrollHeight ?? 0,
    };
  }),
  requests: window.routingBoardActions.requests(),
}));

try {
  await page.goto(config.url);
  await page.waitForFunction(() => document.body.textContent?.includes('account-a.json'));
  evidence.initial = await readPage();
  const hasInlineRecheck = evidence.initial.body.includes('重检');
  const hasInlineResume = evidence.initial.body.includes('恢复');
  ensure(hasInlineRecheck === !config.expectBefore, 'inline recheck action');
  ensure(hasInlineResume === !config.expectBefore, 'inline resume action');
  if (!config.expectBefore) {
    ensure(evidence.initial.actionGroups.every((items) => JSON.stringify(items) === JSON.stringify(['详情', '重检', '恢复'])), 'row action order');
    const desktopRegion = evidence.initial.scrollRegions.find((item) => item.kind === 'table' && item.visible);
    ensure(desktopRegion && desktopRegion.clientHeight <= 560 && desktopRegion.scrollHeight > desktopRegion.clientHeight, 'desktop constrained scroll region');
  }

  const fs = await import('node:fs/promises');
  await fs.mkdir(config.artifactDir, { recursive: true });
  const desktopScreenshot = `${config.artifactDir}/desktop.png`;
  await page.screenshot({ path: desktopScreenshot, fullPage: true });

  await page.cdp('Emulation.setDeviceMetricsOverride', {
    width: 390,
    height: 844,
    deviceScaleFactor: 1,
    mobile: false,
  });
  await page.waitForFunction(() => document.body.textContent?.includes('account-a.json'));
  if (!config.expectBefore) {
    evidence.mobileLayout = await readPage();
    const mobileRegion = evidence.mobileLayout.scrollRegions.find((item) => item.kind === 'cards' && item.visible);
    ensure(mobileRegion && mobileRegion.clientHeight <= 560 && mobileRegion.scrollHeight > mobileRegion.clientHeight, 'mobile constrained scroll region');
  }
  const mobileScreenshot = `${config.artifactDir}/mobile.png`;
  await page.screenshot({ path: mobileScreenshot, fullPage: true });
  await page.cdp('Emulation.setDeviceMetricsOverride', {
    width: 1440,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  });

  await page.click('loc=css:[data-routing-auth-id="account-A"] [data-routing-action="details"]');
  await page.waitForFunction(() => document.querySelector('[role="dialog"]'));
  evidence.detail = await readPage();
  const detailHasRecoveryModule = evidence.detail.dialogs.some((text) => text.includes('调度恢复'));
  ensure(detailHasRecoveryModule === config.expectBefore, 'details recovery module visibility');
  if (!config.expectBefore) {
    ensure(!evidence.detail.dialogs.some((text) => text.includes('重检') || text.includes('恢复调用')), 'details contain inline actions');
  }
  await page.keyboard.press('Escape');
  await page.waitForFunction(() => !document.querySelector('[role="dialog"]'));

  if (!config.expectBefore) {
    await page.click('loc=css:[data-routing-auth-id="account-A"] [data-routing-action="recheck"]');
    await page.waitForFunction(() => window.routingBoardActions.requests().checks.length === 1);
    evidence.recheck = await readPage();
    ensure(evidence.recheck.requests.checks[0].authId === 'account-A', 'recheck authId');
    ensure(evidence.recheck.requests.checks[0].authIndex === 'index-A', 'recheck authIndex');
    ensure(evidence.recheck.requests.checks[0].registrationEpoch === '7', 'recheck epoch');
    ensure(!('source' in evidence.recheck.requests.checks[0]), 'recheck unexpectedly narrowed scope');

    await page.click('loc=css:[data-routing-auth-id="account-A"] [data-routing-action="resume"]');
    await page.waitForFunction(() => document.querySelector('[role="dialog"]')?.textContent?.includes('确认恢复'));
    evidence.confirmation = await readPage();
    ensure(evidence.confirmation.dialogs.some((text) => text.includes('2')), 'release confirmation count');
    await page.click('loc=role:button[name="确认恢复"]');
    await page.waitForFunction(() => window.routingBoardActions.requests().releases.length === 2);
    await page.waitForFunction(() => !document.body.textContent?.includes('account-a.json'));
    evidence.release = await readPage();
    const compact = (request) => ({
      authId: request.authId,
      authIndex: request.authIndex,
      registrationEpoch: request.registrationEpoch,
      source: request.source,
      model: request.model || '',
      revision: request.revision,
    });
    ensure(JSON.stringify(evidence.release.requests.releases.map(compact)) === JSON.stringify([
      { authId: 'account-A', authIndex: 'index-A', registrationEpoch: '7', source: 'inspection', model: '', revision: '11' },
      { authId: 'account-A', authIndex: 'index-A', registrationEpoch: '7', source: 'upstream', model: 'gpt-5', revision: '22' },
    ]), 'release request scopes');
  }

  const result = {
    spaceId: task.spaceId,
    expectBefore: config.expectBefore,
    hasInlineRecheck,
    hasInlineResume,
    detailHasRecoveryModule,
    desktopScreenshot,
    mobileScreenshot,
    evidence,
  };
  await fs.writeFile(`${config.artifactDir}/result.json`, JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ artifact: `${config.artifactDir}/result.json`, ...result }));
  if (!config.keepOpen) await task.finish({ keep: [] });
} catch (error) {
  console.error(`Ego task space ${task.spaceId} retained for inspection`);
  throw error;
}
