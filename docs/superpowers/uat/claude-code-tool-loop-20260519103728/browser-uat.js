const fs = require('fs');
const path = require('path');
const { chromium } = require('/root/.claude/gsd-user-files-backup/skills/gstack/node_modules/playwright');

const outDir = process.argv[2];
const kiroPassword = process.argv[3];
const sub2Password = process.argv[4];
const summary = { pages: [], console: [], pageErrors: [], requestFailures: [], screenshots: [] };
function saveJSON(name, data) { fs.writeFileSync(path.join(outDir, name), JSON.stringify(data, null, 2)); }
async function shot(page, name) {
  const file = path.join(outDir, name);
  await page.screenshot({ path: file, fullPage: true });
  const stat = fs.statSync(file);
  const text = await page.locator('body').innerText().catch(() => '');
  summary.screenshots.push({ name, bytes: stat.size, title: await page.title().catch(() => ''), text: text.slice(0, 4000) });
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
  await kiro.goto('http://127.0.0.1:8080/admin', { waitUntil: 'domcontentloaded' });
  await kiro.locator('#pwdField').fill(kiroPassword);
  await kiro.locator('button').filter({ hasText: /登录|Login/i }).first().click();
  await kiro.waitForLoadState('networkidle').catch(() => {});
  await kiro.waitForTimeout(1200);
  await shot(kiro, 'kiro-admin-dashboard.png');
  const apiTab = kiro.getByText('API', { exact: true }).first();
  if (await apiTab.count()) await apiTab.click();
  await kiro.waitForTimeout(1500);
  await shot(kiro, 'kiro-admin-api-readiness.png');
  const logsText = kiro.getByText(/请求日志|Request Logs|request logs/i).first();
  if (await logsText.count()) await logsText.click().catch(() => {});
  await kiro.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await kiro.waitForTimeout(1200);
  await shot(kiro, 'kiro-admin-bottom-request-logs.png');

  const sub = await context.newPage();
  await sub.goto('http://127.0.0.1:18080/login', { waitUntil: 'domcontentloaded' }).catch(async () => {
    await sub.goto('http://127.0.0.1:18080', { waitUntil: 'domcontentloaded' });
  });
  await sub.waitForTimeout(1000);
  const email = sub.locator('input[type="email"], input[name="email"], input[placeholder*="email" i]').first();
  if (await email.count()) await email.fill('admin@sub2api.local');
  const pass = sub.locator('input[type="password"], input[name="password"]').first();
  if (await pass.count()) await pass.fill(sub2Password);
  const loginBtn = sub.locator('button').filter({ hasText: /login|登录|sign in/i }).first();
  if (await loginBtn.count()) await loginBtn.click();
  await sub.waitForLoadState('networkidle').catch(() => {});
  await sub.waitForTimeout(1800);
  await shot(sub, 'sub2api-dashboard.png');
  for (const [label, file] of [['Accounts','sub2api-accounts.png'], ['Usage','sub2api-usage.png'], ['Groups','sub2api-groups.png']]) {
    const item = sub.getByText(new RegExp(label + '|账号|账户|用量|使用|分组|组', 'i')).first();
    if (await item.count()) await item.click().catch(() => {});
    await sub.waitForTimeout(1500);
    await shot(sub, file);
  }
  summary.pages.push({ kiroURL: kiro.url(), sub2apiURL: sub.url() });
  saveJSON('playwright-summary.json', summary);
  await browser.close();
})().catch(err => { summary.fatal = String(err && err.stack || err); saveJSON('playwright-summary.json', summary); process.exit(1); });
