const { chromium } = require('/www/cg-sto/node_modules/playwright');
const fs = require('fs');
const path = require('path');
const out = process.env.UAT_DIR;
const email = process.env.SUB2API_ADMIN_EMAIL;
const password = process.env.SUB2API_ADMIN_PASSWORD;

async function dismiss(page) {
  for (const re of [/Skip/i, /跳过/i, /ESC/i]) await page.getByText(re).first().click({ timeout: 1000 }).catch(() => {});
  await page.keyboard.press('Escape').catch(() => {});
}
async function capture(page, name, summary) {
  await dismiss(page);
  await page.waitForTimeout(800);
  await page.screenshot({ path: path.join(out, name), fullPage: true });
  const text = await page.locator('body').innerText({ timeout: 3000 }).catch(() => '');
  summary.screenshots.push({ name, url: page.url(), title: await page.title().catch(() => ''), text: text.slice(0, 2500) });
}
(async () => {
  const summary = { startedAt: new Date().toISOString(), screenshots: [], errors: [], failedRequests: [] };
  const browser = await chromium.launch({ executablePath: '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 } });
  await context.addInitScript(() => {
    for (let id = 0; id <= 50; id++) for (const role of ['admin','user','owner']) localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
  });
  const page = await context.newPage();
  page.on('pageerror', e => summary.errors.push(String(e).slice(0, 500)));
  page.on('requestfailed', r => summary.failedRequests.push({ url: r.url(), failure: r.failure()?.errorText }));
  await page.goto('http://127.0.0.1:18080/login', { waitUntil: 'networkidle', timeout: 20000 });
  await page.locator('#email').fill(email);
  await page.locator('#password').fill(password);
  await page.getByRole('button', { name: /Sign In|登录|Login/i }).click();
  await page.waitForTimeout(3000);
  for (const [name, url] of [
    ['sub2api-admin-dashboard-final.png', 'http://127.0.0.1:18080/admin/dashboard'],
    ['sub2api-admin-accounts-final.png', 'http://127.0.0.1:18080/admin/accounts'],
    ['sub2api-admin-usage-final.png', 'http://127.0.0.1:18080/admin/usage'],
    ['sub2api-admin-groups-final.png', 'http://127.0.0.1:18080/admin/groups'],
  ]) {
    await page.goto(url, { waitUntil: 'networkidle', timeout: 25000 });
    await capture(page, name, summary);
  }
  summary.endedAt = new Date().toISOString();
  fs.writeFileSync(path.join(out, 'sub2api-playwright-summary-final.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch(err => { fs.writeFileSync(path.join(out || '.', 'sub2api-playwright-error-final.txt'), err.stack || String(err)); process.exit(1); });
