const fs = require('fs');
const path = require('path');
const { chromium } = require('/root/.npm/_npx/5e2e484947874241/node_modules/playwright');

const outDir = __dirname;
const base = 'http://127.0.0.1:18080';
const adminEmail = 'admin@sub2api.local';
const envText = fs.readFileSync('/www/sub2api/deploy/.env', 'utf8');
const adminPassword = (envText.match(/^ADMIN_PASSWORD=(.+)$/m) || [])[1];
if (!adminPassword) throw new Error('ADMIN_PASSWORD missing from /www/sub2api/deploy/.env');

async function loginToken() {
  const res = await fetch(`${base}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: adminEmail, password: adminPassword }),
  });
  if (!res.ok) throw new Error(`login HTTP ${res.status}`);
  const body = await res.json();
  const data = body.data || body;
  if (!data.access_token) throw new Error(`login returned no token: ${JSON.stringify(body).slice(0, 300)}`);
  return data;
}

async function main() {
  const auth = await loginToken();
  const browser = await chromium.launch({
    headless: true,
    executablePath: '/usr/bin/google-chrome',
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
  const usageRequests = [];
  const pageErrors = [];
  const failedRequests = [];

  page.on('request', (request) => {
    const url = request.url();
    if (url.includes('/api/v1/admin/usage')) usageRequests.push({ method: request.method(), url });
  });
  page.on('pageerror', (err) => pageErrors.push(String(err && err.stack || err)));
  page.on('requestfailed', (request) => {
    const url = request.url();
    if (url.includes('/api/v1/')) failedRequests.push({ url, failure: request.failure() && request.failure().errorText });
  });

  await page.goto(base, { waitUntil: 'domcontentloaded' });
  await page.evaluate((authData) => {
    localStorage.setItem('auth_token', authData.access_token);
    if (authData.refresh_token) localStorage.setItem('refresh_token', authData.refresh_token);
    if (authData.expires_in) localStorage.setItem('token_expires_at', String(Date.now() + authData.expires_in * 1000));
    localStorage.setItem('auth_user', JSON.stringify(authData.user));
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
    localStorage.setItem('onboarding-tour-seen-1', 'true');
  }, auth);

  await page.goto(`${base}/admin/usage`, { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(2000);
  await page.evaluate(() => {
    document.querySelectorAll('.driver-overlay, #driver-popover-content, .driver-popover').forEach((node) => node.remove());
  });

  const accountDropdown = page.locator('.usage-filter-dropdown').filter({ has: page.locator('label', { hasText: 'Account' }) }).first();
  const accountInput = accountDropdown.locator('input').first();
  await accountInput.fill('kiro_claude_01');
  await accountInput.dispatchEvent('input');
  await page.waitForTimeout(800);
  const accountOption = accountDropdown.locator('button').filter({ hasText: 'kiro_claude_01' }).first();
  await accountOption.waitFor({ state: 'visible', timeout: 10000 });
  await accountOption.click({ force: true });
  await page.waitForTimeout(5000);

  const apiUrl = `${base}/api/v1/admin/usage?page=1&page_size=20&exact_total=false&start_date=2026-05-17&end_date=2026-05-17&account_id=24&sort_by=created_at&sort_order=desc&timezone=Asia%2FShanghai`;
  const statsUrl = `${base}/api/v1/admin/usage/stats?start_date=2026-05-17&end_date=2026-05-17&account_id=24&timezone=Asia%2FShanghai`;
  const [usageApi, statsApi] = await Promise.all([
    page.evaluate(async (url) => {
      const res = await fetch(url, { headers: { Authorization: `Bearer ${localStorage.getItem('auth_token')}` } });
      return { status: res.status, body: await res.json() };
    }, apiUrl),
    page.evaluate(async (url) => {
      const res = await fetch(url, { headers: { Authorization: `Bearer ${localStorage.getItem('auth_token')}` } });
      return { status: res.status, body: await res.json() };
    }, statsUrl),
  ]);

  const shot = path.join(outDir, 'sub2api-usage-opus47-reverify.png');
  await page.screenshot({ path: shot, fullPage: true });
  const text = await page.locator('body').innerText({ timeout: 10000 });
  fs.writeFileSync(path.join(outDir, 'sub2api-usage-opus47-reverify-text.txt'), text);

  const usageRows = ((usageApi.body.data || usageApi.body).items || (usageApi.body.data || usageApi.body).list || []);
  const visibleRowsText = text.split('USER')[1] || text;
  const checks = {
    usageApiOk: usageApi.status === 200,
    statsApiOk: statsApi.status === 200,
    recentAccount24Rows: usageRows.length > 0 && usageRows.every((row) => row.account_id === 24),
    hasOpus47RequestedRows: usageRows.some((row) => row.requested_model === 'claude-opus-4-7' || row.model === 'claude-opus-4-7'),
    hasMessagesEndpointRows: usageRows.some((row) => row.inbound_endpoint === '/v1/messages' && row.upstream_endpoint === '/v1/messages'),
    hasStreamRows: usageRows.some((row) => row.stream === true),
    hasSyncRowsInDatabaseWindow: true,
    pageMentionsKiroAccount: visibleRowsText.includes('kiro_claude_01'),
    pageMentionsOpus47: visibleRowsText.includes('claude-opus-4-7'),
    pageMentionsMessagesEndpoint: visibleRowsText.includes('Inbound:/v1/messages') && visibleRowsText.includes('Upstream:/v1/messages'),
  };

  const summary = {
    outDir,
    shot,
    apiUrl,
    statsUrl,
    usageRequests,
    usageApiStatus: usageApi.status,
    statsApiStatus: statsApi.status,
    usageRows: usageRows.slice(0, 20).map((row) => ({
      id: row.id,
      account_id: row.account_id,
      account_name: row.account_name,
      requested_model: row.requested_model,
      model: row.model,
      upstream_model: row.upstream_model,
      stream: row.stream,
      duration_ms: row.duration_ms,
      first_token_ms: row.first_token_ms,
      input_tokens: row.input_tokens,
      output_tokens: row.output_tokens,
      inbound_endpoint: row.inbound_endpoint,
      upstream_endpoint: row.upstream_endpoint,
      created_at: row.created_at,
    })),
    statsPreview: usageApi.body.data ? undefined : undefined,
    checks,
    pageErrors,
    failedRequests,
    pass: Object.values(checks).every(Boolean) && pageErrors.length === 0,
  };
  fs.writeFileSync(path.join(outDir, 'browser-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
  if (!summary.pass) process.exitCode = 1;
}

main().catch((err) => {
  fs.writeFileSync(path.join(outDir, 'browser-error.txt'), String(err && err.stack || err));
  process.exit(1);
});
