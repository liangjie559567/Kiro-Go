const { chromium } = require('/www/cg-sto/node_modules/playwright');
const fs = require('fs');
const path = require('path');

const out = process.env.UAT_DIR;
const kiroPassword = process.env.KIRO_ADMIN_PASSWORD;
const subEmail = process.env.SUB2API_ADMIN_EMAIL;
const subPassword = process.env.SUB2API_ADMIN_PASSWORD;

function shotPath(name) { return path.join(out, name); }
function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

async function capture(page, name, summary, extra = {}) {
  await page.screenshot({ path: shotPath(name), fullPage: true });
  const title = await page.title().catch(() => '');
  const url = page.url();
  const text = (await page.locator('body').innerText({ timeout: 3000 }).catch(() => '')).slice(0, 3000);
  summary.screenshots.push({ name, path: name, title, url, text, ...extra });
}

async function main() {
  const summary = { startedAt: new Date().toISOString(), screenshots: [], console: [], pageErrors: [], failedRequests: [], notes: [] };
  const browser = await chromium.launch({ executablePath: '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on('console', msg => {
    if (['error', 'warning'].includes(msg.type())) summary.console.push({ type: msg.type(), text: msg.text(), url: page.url() });
  });
  page.on('pageerror', err => summary.pageErrors.push({ message: err.message, stack: err.stack, url: page.url() }));
  page.on('requestfailed', req => summary.failedRequests.push({ url: req.url(), method: req.method(), failure: req.failure()?.errorText }));

  // Kiro-Go admin
  await page.goto('http://localhost:8080/admin', { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle', { timeout: 10000 }).catch(() => {});
  const kiroPwd = page.locator('#pwdField');
  if (await kiroPwd.count()) {
    await kiroPwd.fill(kiroPassword || '');
    await page.locator('button').filter({ hasText: /登录|Login/i }).first().click().catch(async () => { await page.keyboard.press('Enter'); });
    await page.waitForTimeout(1200);
  }
  await capture(page, 'kiro-admin-dashboard.png', summary, { area: 'kiro-dashboard' });

  // API/readiness tab is in the same static app; click or use exposed UI text.
  const apiTab = page.getByText(/Claude Code|API|接口/i).first();
  await apiTab.click().catch(() => {});
  await page.waitForTimeout(1500);
  await capture(page, 'kiro-admin-claude-readiness.png', summary, { area: 'kiro-readiness' });

  // Ensure readiness APIs have been called; if tab click did not navigate, still capture current UI after direct route-independent app state.
  await page.evaluate(async (pwd) => {
    await fetch('/admin/api/claude-code/readiness', { headers: { 'X-Admin-Password': pwd || '' } }).catch(() => null);
    await fetch('/admin/api/claude-code/model-readiness?model=claude-opus-4-7', { headers: { 'X-Admin-Password': pwd || '' } }).catch(() => null);
  }, kiroPassword || '');
  await page.waitForTimeout(500);
  await capture(page, 'kiro-admin-model-readiness.png', summary, { area: 'kiro-model-readiness' });

  const logsText = page.getByText(/Request Logs|请求日志|日志/i).first();
  await logsText.click().catch(() => {});
  await page.waitForTimeout(1200);
  await capture(page, 'kiro-admin-request-logs.png', summary, { area: 'kiro-request-logs' });

  // sub2api admin login
  await page.goto('http://localhost:18080/login', { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle', { timeout: 10000 }).catch(() => {});
  const emailCandidates = ['input[type="email"]', 'input[name="email"]', 'input[placeholder*="email" i]', 'input[placeholder*="邮箱" i]'];
  let emailFilled = false;
  for (const sel of emailCandidates) {
    const loc = page.locator(sel).first();
    if (await loc.count()) { await loc.fill(subEmail || ''); emailFilled = true; break; }
  }
  const passLoc = page.locator('input[type="password"]').first();
  if (await passLoc.count()) await passLoc.fill(subPassword || '');
  if (emailFilled) {
    await page.locator('button').filter({ hasText: /登录|Login|Sign in/i }).first().click().catch(async () => { await page.keyboard.press('Enter'); });
    await page.waitForTimeout(2500);
  } else {
    summary.notes.push('sub2api login fields were not detected');
  }

  const subPages = [
    ['sub2api-dashboard.png', 'http://localhost:18080/admin/dashboard'],
    ['sub2api-accounts.png', 'http://localhost:18080/admin/accounts'],
    ['sub2api-usage.png', 'http://localhost:18080/admin/usage'],
    ['sub2api-groups-or-channels.png', 'http://localhost:18080/admin/groups'],
  ];
  for (const [name, url] of subPages) {
    await page.goto(url, { waitUntil: 'domcontentloaded' });
    await page.waitForLoadState('networkidle', { timeout: 12000 }).catch(() => {});
    await sleep(1200);
    await capture(page, name, summary, { area: name.replace('.png', '') });
  }

  summary.endedAt = new Date().toISOString();
  fs.writeFileSync(path.join(out, 'playwright-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
}

main().catch(err => {
  fs.writeFileSync(path.join(out || '.', 'playwright-error.txt'), err.stack || String(err));
  process.exit(1);
});
