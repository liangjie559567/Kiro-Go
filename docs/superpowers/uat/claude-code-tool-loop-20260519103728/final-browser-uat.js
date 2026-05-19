const fs = require('fs');
const path = require('path');
const { chromium } = require('/root/.claude/gsd-user-files-backup/skills/gstack/node_modules/playwright');

const [outDir, kiroPassword, sub2Password] = process.argv.slice(2);
const summary = { screenshots: [], console: [], pageErrors: [], requestFailures: [] };

async function screenshot(page, name) {
  await page.keyboard.press('Escape').catch(() => {});
  await page.locator('button').filter({ hasText: /^×$|^Close$|^Skip$/i }).first().click({ timeout: 500 }).catch(() => {});
  await page.locator('button[aria-label="Close"], button[aria-label="close"]').first().click({ timeout: 500 }).catch(() => {});
  await page.waitForTimeout(300);
  const file = path.join(outDir, name);
  await page.screenshot({ path: file, fullPage: true });
  const text = await page.locator('body').innerText().catch(() => '');
  summary.screenshots.push({
    name,
    bytes: fs.statSync(file).size,
    url: page.url(),
    title: await page.title().catch(() => ''),
    text: text.slice(0, 7000)
  });
}

function trackPage(page) {
  page.on('console', msg => {
    if (!['error', 'warning'].includes(msg.type())) return;
    const text = msg.text();
    if (text.includes('m.stripe.com')) return;
    if (text.includes('Failed to load Stripe.js')) return;
    if (text.includes('ERR_BLOCKED_BY_CLIENT.Inspector')) return;
    summary.console.push({ type: msg.type(), text, url: page.url() });
  });
  page.on('pageerror', err => summary.pageErrors.push({ message: err.message, url: page.url() }));
  page.on('requestfailed', req => {
    const url = req.url();
    if (!url.startsWith('http://127.0.0.1:8080') && !url.startsWith('http://127.0.0.1:18080')) return;
    summary.requestFailures.push({ url, method: req.method(), failure: req.failure()?.errorText });
  });
}

(async () => {
  const browser = await chromium.launch({
    headless: true,
    executablePath: '/usr/bin/google-chrome',
    args: ['--no-sandbox', '--disable-dev-shm-usage']
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.route(url => {
    const host = new URL(url).hostname;
    return !['127.0.0.1', 'localhost'].includes(host);
  }, route => route.abort('blockedbyclient'));

  const kiro = await context.newPage();
  trackPage(kiro);
  await kiro.goto('http://127.0.0.1:8080/admin', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await kiro.locator('#pwdField').fill(kiroPassword, { timeout: 5000 });
  await kiro.locator('button').filter({ hasText: /登录|Login/i }).first().click({ timeout: 5000 });
  await kiro.waitForTimeout(1500);
  await screenshot(kiro, 'kiro-admin-dashboard-final.png');
  await kiro.getByText('API', { exact: true }).first().click({ timeout: 5000 }).catch(() => {});
  await kiro.waitForTimeout(1500);
  await screenshot(kiro, 'kiro-admin-api-readiness-final.png');

  const sub = await context.newPage();
  trackPage(sub);
  await sub.goto('http://127.0.0.1:18080/login', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await sub.waitForTimeout(1000);
  const email = sub.locator('input[type="email"], input[name="email"], input[placeholder*="email" i]').first();
  if (await email.count()) await email.fill('admin@sub2api.local');
  const pass = sub.locator('input[type="password"], input[name="password"]').first();
  if (await pass.count()) await pass.fill(sub2Password);
  await sub.locator('button').filter({ hasText: /login|登录|sign in/i }).first().click({ timeout: 5000 }).catch(() => {});
  await sub.waitForTimeout(2500);

  for (const [url, name] of [
    ['http://127.0.0.1:18080/admin/accounts', 'sub2api-admin-accounts-final.png'],
    ['http://127.0.0.1:18080/admin/groups', 'sub2api-admin-groups-final.png'],
    ['http://127.0.0.1:18080/admin/usage', 'sub2api-admin-usage-final.png']
  ]) {
    await sub.goto(url, { waitUntil: 'domcontentloaded', timeout: 15000 });
    await sub.waitForTimeout(2000);
    await screenshot(sub, name);
  }

  fs.writeFileSync(path.join(outDir, 'final-browser-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch(err => {
  summary.fatal = String(err && err.stack || err);
  fs.writeFileSync(path.join(outDir, 'final-browser-summary.json'), JSON.stringify(summary, null, 2));
  process.exit(1);
});
