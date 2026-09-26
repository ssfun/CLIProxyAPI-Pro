// Serve fixtures/data_management_page.tsx as /review.tsx in a generated Management checkout.
// Run via ego-browser nodejs, prepending globalThis.dataReviewConfig = {spaceId, url, artifact}.
const fs = await import('node:fs/promises');
const config = globalThis.dataReviewConfig;
const task = await taskSpace(config.spaceId);
const page = task.page('p1');
const results = [];
const frame = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function setup() {
  await page.goto(config.url);
  await page.waitForSelector('button[role=tab]');
  await page.click('loc=role:tab[name="Backup & restore"]');
  await page.waitForSelector('input[type=file]', { state: 'attached' });
}
async function select(name, content, fail = false) {
  await page.evaluate(({name, content, fail}) => {
    const input = document.querySelector('input[type=file]');
    const file = new File([content], name);
    if (fail) file.arrayBuffer = async () => { throw new Error('fixture read failure'); };
    const transfer = new DataTransfer(); transfer.items.add(file);
    input.files = transfer.files;
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }, {name, content, fail});
  await frame();
}
await setup();
await select('A.jsonl', 'A');
await page.waitForFunction(() => !!window.review.pending.A);
await select('B.jsonl', 'B');
await page.waitForFunction(() => document.body.innerText.includes('B.jsonl'));
await page.evaluate(() => window.review.pending.A.reject(new Error('stale A error')));
await frame();
const stale = await page.evaluate(() => window.review.notifications);
results.push({name:'stale preview failure is silent', passed:stale.length === 0, evidence:stale});
await setup();
await select('broken.jsonl', '', true);
const readErrors = await page.evaluate(() => window.review.notifications);
results.push({name:'file read error is reported', passed:readErrors.includes('fixture read failure'), evidence:readErrors});
await setup();
await select('renamed.json', JSON.stringify({format:'cliproxy-pro-encrypted-backup', ciphertext:'fixture'}, null, '\t'));
const encrypted = await page.evaluate(() => ({text:document.body.innerText, previews:window.review.previews}));
results.push({name:'pretty printed encrypted backup asks for passphrase', passed:encrypted.text.includes('Unlock encrypted backup') && encrypted.previews.length === 0, evidence:encrypted.previews});
await setup();
await page.evaluate(() => {
  const input = document.querySelector('input[type=file]');
  const file = new File(['A'], 'A.jsonl');
  file.arrayBuffer = () => new Promise(resolve => { window.review.releaseRead = () => resolve(new TextEncoder().encode('A').buffer); });
  const transfer = new DataTransfer(); transfer.items.add(file); input.files = transfer.files;
  input.dispatchEvent(new Event('change', {bubbles:true}));
});
await select('B.jsonl', 'B');
await page.evaluate(() => window.review.releaseRead());
await frame();
const reads = await page.evaluate(() => window.review.previews);
results.push({name:'late file read cannot replace newer selection', passed:JSON.stringify(reads) === '["B"]', evidence:reads});
await page.screenshot({path:config.artifact + '.png'});
await fs.writeFile(config.artifact, JSON.stringify({results}, null, 2) + '\n');
console.log(JSON.stringify({results}, null, 2));
if (results.some(result => !result.passed)) throw new Error('Data management page regression');
