const fs = require('fs');
const path = require('path');
const { chromium } = require('/root/.claude/gsd-user-files-backup/skills/gstack/node_modules/playwright');

const [outDir, password] = process.argv.slice(2);
const summary = { screenshots: [], console: [], pageErrors: [], requestFailures: [] };

async function screenshot(page, name) {
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

(async () => {
  const browser = await chromium.launch({
    headless: true,
    executablePath: '/usr/bin/google-chrome',
    args: ['--no-sandbox', '--disable-dev-shm-usage']
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  page.on('console', msg => {
    if (['error', 'warning'].includes(msg.type())) {
      summary.console.push({ type: msg.type(), text: msg.text(), url: page.url() });
    }
  });
  page.on('pageerror', err => summary.pageErrors.push({ message: err.message, url: page.url() }));
  page.on('requestfailed', req => summary.requestFailures.push({
    url: req.url(),
    method: req.method(),
    failure: req.failure()?.errorText
  }));

  await page.goto('http://127.0.0.1:18080/login', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.waitForTimeout(1000);
  const email = page.locator('input[type="email"], input[name="email"], input[placeholder*="email" i]').first();
  if (await email.count()) await email.fill('admin@sub2api.local');
  const pass = page.locator('input[type="password"], input[name="password"]').first();
  if (await pass.count()) await pass.fill(password);
  await page.locator('button').filter({ hasText: /login|登录|sign in/i }).first().click({ timeout: 5000 }).catch(() => {});
  await page.waitForTimeout(2500);

  for (const route of [
    ['http://127.0.0.1:18080/admin/accounts', 'sub2api-admin-accounts.png'],
    ['http://127.0.0.1:18080/admin/groups', 'sub2api-admin-groups.png'],
    ['http://127.0.0.1:18080/admin/usage', 'sub2api-admin-usage.png']
  ]) {
    await page.goto(route[0], { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => {});
    await page.waitForTimeout(2000);
    await screenshot(page, route[1]);
  }

  fs.writeFileSync(path.join(outDir, 'sub2api-admin-routes-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch(err => {
  summary.fatal = String(err && err.stack || err);
  fs.writeFileSync(path.join(outDir, 'sub2api-admin-routes-summary.json'), JSON.stringify(summary, null, 2));
  process.exit(1);
});
