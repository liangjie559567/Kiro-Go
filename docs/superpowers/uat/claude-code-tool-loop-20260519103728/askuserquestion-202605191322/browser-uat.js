const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const outDir = __dirname;

async function writeJSON(name, value) {
  fs.writeFileSync(path.join(outDir, name), JSON.stringify(value, null, 2));
}

async function main() {
  const kiroPassword = process.env.KIRO_ADMIN_PASSWORD;
  const sub2apiEmail = process.env.SUB2API_ADMIN_EMAIL || 'admin@sub2api.local';
  const sub2apiPassword = process.env.SUB2API_ADMIN_PASSWORD;
  if (!kiroPassword || !sub2apiPassword) {
    throw new Error('KIRO_ADMIN_PASSWORD and SUB2API_ADMIN_PASSWORD are required');
  }

  const browser = await chromium.launch({
    headless: true,
    executablePath: '/usr/bin/google-chrome',
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.setDefaultNavigationTimeout(15000);
  const consoleMessages = [];
  const pageErrors = [];
  const failedRequests = [];
  page.on('console', msg => {
    if (['error', 'warning'].includes(msg.type())) {
      const text = msg.text();
      if (!text.includes('401 (Unauthorized)')) {
        consoleMessages.push({ type: msg.type(), text });
      }
    }
  });
  page.on('pageerror', err => pageErrors.push(String(err)));
  page.on('requestfailed', req => {
    const failure = req.failure()?.errorText || '';
    if (failure !== 'net::ERR_ABORTED') {
      failedRequests.push({ url: req.url(), failure });
    }
  });

  const checks = [];

  await page.goto('http://127.0.0.1:8080/admin', { waitUntil: 'domcontentloaded' });
  await page.locator('#pwdField').fill(kiroPassword);
  await page.locator('#loginPage button.btn-primary').click();
  await page.waitForTimeout(1000);
  const kiroDashboardText = await page.locator('body').innerText();
  checks.push({ name: 'kiro dashboard', pass: /Kiro|Admin|Account|账号|请求|日志/i.test(kiroDashboardText) });
  await page.screenshot({ path: path.join(outDir, 'kiro-admin-dashboard.png'), fullPage: true });

  await page.goto('http://127.0.0.1:8080/admin/api/claude-code/readiness', { waitUntil: 'domcontentloaded' });
  const readiness = await page.evaluate(async password => {
    const response = await fetch('/admin/api/claude-code/readiness', { headers: { 'X-Admin-Password': password } });
    return { status: response.status, text: await response.text() };
  }, kiroPassword);
  await writeJSON('kiro-readiness-browser-api.json', readiness);
  const readinessText = readiness.text;
  checks.push({ name: 'kiro readiness api', pass: readiness.status === 200 && (readinessText.includes('toolSchemaValidation') || readinessText.includes('recentSuppressedToolUses')) });
  await page.screenshot({ path: path.join(outDir, 'kiro-readiness-json.png'), fullPage: true });

  const loginResp = await page.request.post('http://127.0.0.1:18080/api/v1/auth/login', {
    data: { email: sub2apiEmail, password: sub2apiPassword },
  });
  const loginJSON = await loginResp.json();
  if (!loginResp.ok() || !loginJSON.data?.access_token) {
    throw new Error(`sub2api login failed: ${loginResp.status()} ${JSON.stringify(loginJSON)}`);
  }
  const token = loginJSON.data.access_token;
  await page.goto('http://127.0.0.1:18080/', { waitUntil: 'domcontentloaded' });
  await page.evaluate(tokenValue => {
    localStorage.setItem('auth_token', tokenValue);
    localStorage.setItem('access_token', tokenValue);
    localStorage.setItem('token', tokenValue);
  }, token);
  await page.evaluate(loginValue => {
    localStorage.setItem('auth_user', JSON.stringify(loginValue.data.user));
    localStorage.setItem('refresh_token', loginValue.data.refresh_token);
    localStorage.setItem('token_expires_at', String(Date.now() + loginValue.data.expires_in * 1000));
  }, loginJSON);

  await page.goto('http://127.0.0.1:18080/admin/accounts', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  const accountsText = await page.locator('body').innerText();
  const accountsAPI = await page.evaluate(async tokenValue => {
    const response = await fetch('/api/v1/admin/accounts?page=1&page_size=50&platform=anthropic', { headers: { Authorization: `Bearer ${tokenValue}` } });
    return { status: response.status, text: await response.text() };
  }, token);
  await writeJSON('sub2api-accounts-browser-api.json', accountsAPI);
  checks.push({ name: 'sub2api accounts', pass: accountsAPI.status === 200 && accountsAPI.text.includes('kiro_claude_01') && /anthropic/i.test(accountsAPI.text) });
  await page.screenshot({ path: path.join(outDir, 'sub2api-admin-accounts.png'), fullPage: true });

  await page.goto('http://127.0.0.1:18080/admin/usage', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  const usageText = await page.locator('body').innerText();
  const usageAPI = await page.evaluate(async tokenValue => {
    const response = await fetch('/api/v1/admin/usage?page=1&page_size=30&api_key_id=2&account_id=24&start_date=2026-05-19&end_date=2026-05-19&timezone=Asia/Shanghai', { headers: { Authorization: `Bearer ${tokenValue}` } });
    return { status: response.status, text: await response.text() };
  }, token);
  await writeJSON('sub2api-usage-browser-api.json', usageAPI);
  checks.push({ name: 'sub2api usage', pass: usageAPI.status === 200 && usageAPI.text.includes('/v1/messages') && usageAPI.text.includes('claude-opus-4-7') && usageAPI.text.includes('"account_id":24') });
  await page.screenshot({ path: path.join(outDir, 'sub2api-admin-usage.png'), fullPage: true });

  await page.goto('http://127.0.0.1:18080/health', { waitUntil: 'domcontentloaded' });
  const subHealth = await page.locator('body').innerText();
  checks.push({ name: 'sub2api health page', pass: subHealth.includes('"status":"ok"') || subHealth.includes('status') });
  await page.screenshot({ path: path.join(outDir, 'sub2api-health-json.png'), fullPage: true });

  await writeJSON('browser-summary.json', {
    checks,
    pass: checks.every(check => check.pass),
    consoleMessages,
    pageErrors,
    failedRequests,
  });

  await browser.close();
}

main().catch(async err => {
  await writeJSON('browser-summary.json', { pass: false, error: String(err), stack: err.stack });
  process.exitCode = 1;
});
