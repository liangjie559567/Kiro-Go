const { chromium } = require('/www/cg-sto/node_modules/playwright');
const fs = require('fs');
const path = require('path');

const out = __dirname;
const kiroPassword = process.env.KIRO_ADMIN_PASSWORD;
const subEmail = process.env.SUB2API_ADMIN_EMAIL || 'admin@sub2api.local';
const subPassword = process.env.SUB2API_ADMIN_PASSWORD;

const summary = { screenshots: [], console: [], pageErrors: [], requestFailures: [], assertions: [] };

function file(name) {
  return path.join(out, name);
}

async function screenshot(page, name, assertionName, expectedPatterns = []) {
  await page.waitForTimeout(500);
  await page.screenshot({ path: file(name), fullPage: true });
  const text = await page.locator('body').innerText({ timeout: 3000 }).catch(() => '');
  const item = {
    name,
    url: page.url(),
    title: await page.title().catch(() => ''),
    bytes: fs.statSync(file(name)).size,
    text: text.slice(0, 6000),
  };
  summary.screenshots.push(item);
  if (assertionName) {
    const matched = expectedPatterns.map((pattern) => new RegExp(pattern, 'i').test(text));
    summary.assertions.push({
      name: assertionName,
      passed: matched.every(Boolean),
      expectedPatterns,
      matched,
    });
  }
}

function track(page) {
  page.on('console', (msg) => {
    if (['error', 'warning'].includes(msg.type())) {
      summary.console.push({ type: msg.type(), text: msg.text(), url: page.url() });
    }
  });
  page.on('pageerror', (err) => summary.pageErrors.push({ message: err.message, url: page.url() }));
  page.on('requestfailed', (req) => {
    const url = req.url();
    if (url.startsWith('http://127.0.0.1:8080') || url.startsWith('http://127.0.0.1:18080')) {
      summary.requestFailures.push({ url, method: req.method(), failure: req.failure()?.errorText });
    }
  });
}

(async () => {
  const browser = await chromium.launch({
    executablePath: '/usr/bin/google-chrome',
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });

  const kiro = await context.newPage();
  track(kiro);
  await kiro.goto('http://127.0.0.1:8080/admin', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await kiro.locator('#pwdField').fill(kiroPassword || '', { timeout: 5000 });
  await kiro.locator('button').filter({ hasText: /登录|Login/i }).first().click({ timeout: 5000 });
  await kiro.waitForTimeout(1500);
  await screenshot(kiro, 'kiro-admin-dashboard.png', 'kiro-dashboard-visible', ['Kiro-Go|Dashboard|仪表', '账户|Accounts|Requests|请求']);
  await kiro.getByText('API', { exact: true }).first().click({ timeout: 5000 }).catch(() => {});
  await kiro.waitForTimeout(1500);
  await screenshot(kiro, 'kiro-admin-api-readiness.png', 'kiro-api-readiness-visible', ['Claude Code|API', 'messages|tool|count|ready|兼容']);
  await kiro.getByText(/Request Logs|请求日志|日志/i).first().click({ timeout: 5000 }).catch(() => {});
  await kiro.waitForTimeout(1500);
  await screenshot(kiro, 'kiro-admin-request-logs.png', 'kiro-request-log-shows-429', ['429|rate_limit|Too Many|失败|error']);

  const sub = await context.newPage();
  track(sub);
  await sub.goto('http://127.0.0.1:18080/login', { waitUntil: 'domcontentloaded', timeout: 15000 });
  const email = sub.locator('input[type="email"], input[name="email"], input[placeholder*="email" i], input[placeholder*="邮箱" i]').first();
  if (await email.count()) await email.fill(subEmail);
  const pass = sub.locator('input[type="password"], input[name="password"]').first();
  if (await pass.count()) await pass.fill(subPassword || '');
  await sub.locator('button').filter({ hasText: /login|登录|sign in/i }).first().click({ timeout: 5000 }).catch(() => {});
  await sub.waitForTimeout(2500);

  for (const [url, name, assertion, patterns] of [
    ['http://127.0.0.1:18080/admin/dashboard', 'sub2api-dashboard.png', 'sub2api-dashboard-visible', ['Dashboard|仪表|Usage|用量|请求']],
    ['http://127.0.0.1:18080/admin/accounts', 'sub2api-accounts.png', 'sub2api-accounts-visible', ['kiro_claude_01|openai|anthropic|账号|Accounts']],
    ['http://127.0.0.1:18080/admin/groups', 'sub2api-groups.png', 'sub2api-groups-visible', ['claude|openai|Groups|分组']],
    ['http://127.0.0.1:18080/admin/usage', 'sub2api-usage.png', 'sub2api-usage-visible', ['gpt-5.5|Usage|用量|Tokens|令牌']],
  ]) {
    await sub.goto(url, { waitUntil: 'domcontentloaded', timeout: 15000 });
    await sub.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {});
    await screenshot(sub, name, assertion, patterns);
  }

  fs.writeFileSync(file('playwright-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch((err) => {
  summary.fatal = String(err && err.stack || err);
  fs.writeFileSync(file('playwright-summary.json'), JSON.stringify(summary, null, 2));
  process.exit(1);
});
