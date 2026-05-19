const { chromium } = require('/www/cg-sto/node_modules/playwright');
const fs = require('fs');
const path = require('path');
const out = process.env.UAT_DIR;
const kiroPassword = process.env.KIRO_ADMIN_PASSWORD || '';
const subEmail = process.env.SUB2API_ADMIN_EMAIL || '';
const subPassword = process.env.SUB2API_ADMIN_PASSWORD || '';
async function capture(page, name, summary, extra = {}) {
  await page.screenshot({ path: path.join(out, name), fullPage: true, timeout: 10000 });
  const text = await page.locator('body').innerText({ timeout: 2500 }).catch(() => '');
  summary.screenshots.push({ name, url: page.url(), title: await page.title().catch(() => ''), text: text.slice(0, 3000), ...extra });
}
async function main() {
  const summary = { startedAt: new Date().toISOString(), screenshots: [], console: [], pageErrors: [], failedRequests: [], notes: [] };
  const browser = await chromium.launch({ executablePath: '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on('console', msg => { if (['error', 'warning'].includes(msg.type())) summary.console.push({ type: msg.type(), text: msg.text(), url: page.url() }); });
  page.on('pageerror', err => summary.pageErrors.push({ message: err.message, stack: err.stack, url: page.url() }));
  page.on('requestfailed', req => summary.failedRequests.push({ url: req.url(), method: req.method(), failure: req.failure()?.errorText }));

  await page.goto('http://localhost:8080/admin', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.locator('#pwdField').fill(kiroPassword, { timeout: 5000 }).catch(() => {});
  await page.locator('button').filter({ hasText: /登录|Login/i }).first().click({ timeout: 5000 }).catch(async () => page.keyboard.press('Enter'));
  await page.waitForTimeout(2500);
  await capture(page, 'kiro-admin-dashboard.png', summary, { area: 'kiro-dashboard' });
  for (const [name, pattern] of [
    ['kiro-admin-claude-readiness.png', /API|接口|Claude Code/i],
    ['kiro-admin-model-readiness.png', /模型|Model|Claude Code/i],
    ['kiro-admin-request-logs.png', /日志|Logs|请求日志|Request/i],
  ]) {
    await page.getByText(pattern).first().click({ timeout: 3500 }).catch(() => {});
    await page.waitForTimeout(2000);
    await capture(page, name, summary, { area: name.replace('.png', '') });
  }

  const loginResp = await page.request.post('http://localhost:18080/api/v1/auth/login', { data: { email: subEmail, password: subPassword } });
  const wrapper = await loginResp.json().catch(() => ({}));
  const auth = wrapper.data || wrapper;
  if (!loginResp.ok() || !auth.access_token) {
    summary.notes.push('sub2api API login failed or no access_token: status=' + loginResp.status());
    summary.notes.push(JSON.stringify(wrapper).slice(0, 500));
  }
  await page.goto('http://localhost:18080/login', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.evaluate((auth) => {
    localStorage.setItem('auth_token', auth.access_token || '');
    if (auth.refresh_token) localStorage.setItem('refresh_token', auth.refresh_token);
    if (auth.expires_in) localStorage.setItem('token_expires_at', String(Date.now() + auth.expires_in * 1000));
    if (auth.user) localStorage.setItem('auth_user', JSON.stringify(auth.user));
  }, auth);
  for (const [name, url] of [
    ['sub2api-dashboard.png', 'http://localhost:18080/admin/dashboard'],
    ['sub2api-accounts.png', 'http://localhost:18080/admin/accounts'],
    ['sub2api-usage.png', 'http://localhost:18080/admin/usage'],
    ['sub2api-groups-or-channels.png', 'http://localhost:18080/admin/groups'],
  ]) {
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.waitForTimeout(2800);
    await capture(page, name, summary, { area: name.replace('.png', '') });
  }
  summary.endedAt = new Date().toISOString();
  fs.writeFileSync(path.join(out, 'playwright-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
}
main().catch(err => { fs.writeFileSync(path.join(out || '.', 'playwright-error.txt'), err.stack || String(err)); process.exit(1); });
