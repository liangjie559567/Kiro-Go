const fs = require('fs');
const path = require('path');
const { chromium } = require('/root/.claude/gsd-user-files-backup/skills/gstack/node_modules/playwright');
const [outDir, kiroPassword, sub2Password] = process.argv.slice(2);
const summary = { screenshots: [], console: [], pageErrors: [], requestFailures: [] };
async function shot(page, name) {
  const file = path.join(outDir, name);
  await page.screenshot({ path: file, fullPage: true });
  const text = await page.locator('body').innerText().catch(() => '');
  summary.screenshots.push({ name, bytes: fs.statSync(file).size, url: page.url(), title: await page.title().catch(() => ''), text: text.slice(0, 5000) });
}
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  context.on('page', page => {
    page.on('console', msg => { if (['error','warning'].includes(msg.type())) summary.console.push({ type: msg.type(), text: msg.text(), url: page.url() }); });
    page.on('pageerror', err => summary.pageErrors.push({ message: err.message, url: page.url() }));
    page.on('requestfailed', req => summary.requestFailures.push({ url: req.url(), method: req.method(), failure: req.failure()?.errorText }));
  });
  const kiro = await context.newPage();
  await kiro.goto('http://127.0.0.1:8080/admin', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await kiro.locator('#pwdField').fill(kiroPassword, { timeout: 5000 });
  await kiro.locator('button').filter({ hasText: /登录|Login/i }).first().click({ timeout: 5000 });
  await kiro.waitForTimeout(1500);
  await shot(kiro, 'kiro-admin-dashboard.png');
  await kiro.getByText('API', { exact: true }).first().click({ timeout: 5000 }).catch(() => {});
  await kiro.waitForTimeout(1500);
  await shot(kiro, 'kiro-admin-api-readiness.png');
  await kiro.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await kiro.waitForTimeout(800);
  await shot(kiro, 'kiro-admin-bottom-request-logs.png');

  const sub = await context.newPage();
  await sub.goto('http://127.0.0.1:18080/login', { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(async () => {
    await sub.goto('http://127.0.0.1:18080', { waitUntil: 'domcontentloaded', timeout: 15000 });
  });
  await sub.waitForTimeout(1000);
  const email = sub.locator('input[type="email"], input[name="email"], input[placeholder*="email" i]').first();
  if (await email.count()) await email.fill('admin@sub2api.local', { timeout: 5000 }).catch(() => {});
  const pass = sub.locator('input[type="password"], input[name="password"]').first();
  if (await pass.count()) await pass.fill(sub2Password, { timeout: 5000 }).catch(() => {});
  await sub.locator('button').filter({ hasText: /login|登录|sign in/i }).first().click({ timeout: 5000 }).catch(() => {});
  await sub.waitForTimeout(2500);
  await shot(sub, 'sub2api-dashboard.png');
  await sub.goto('http://127.0.0.1:18080/accounts', { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => {});
  await sub.waitForTimeout(1200); await shot(sub, 'sub2api-accounts.png');
  await sub.goto('http://127.0.0.1:18080/usage', { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => {});
  await sub.waitForTimeout(1200); await shot(sub, 'sub2api-usage.png');
  await sub.goto('http://127.0.0.1:18080/groups', { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => {});
  await sub.waitForTimeout(1200); await shot(sub, 'sub2api-groups.png');
  fs.writeFileSync(path.join(outDir, 'playwright-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch(err => { summary.fatal = String(err && err.stack || err); fs.writeFileSync(path.join(outDir, 'playwright-summary.json'), JSON.stringify(summary, null, 2)); process.exit(1); });
