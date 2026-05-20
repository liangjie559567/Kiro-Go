const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

async function main() {
  const outDir = __dirname;
  const kiroConfig = JSON.parse(fs.readFileSync('/www/Kiro-Go/data/config.json', 'utf8'));
  const sub2apiConfig = fs.readFileSync('/www/sub2api/deploy/data/config.yaml', 'utf8');
  const sub2apiEnv = fs.existsSync('/www/sub2api/deploy/.env')
    ? fs.readFileSync('/www/sub2api/deploy/.env', 'utf8')
    : '';
  const adminPassword =
    (sub2apiConfig.match(/admin_password:\s*(\S+)/) || [])[1] ||
    (sub2apiEnv.match(/^ADMIN_PASSWORD=(.+)$/m) || [])[1] ||
    process.env.SUB2API_ADMIN_PASSWORD;
  if (!adminPassword) {
    throw new Error('sub2api admin password not found');
  }

  const browser = await chromium.launch({
    executablePath: '/usr/bin/google-chrome',
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });

  const result = {
    checks: {},
    screenshots: [],
    api: {},
  };

  await page.goto('http://127.0.0.1:8080/admin', { waitUntil: 'networkidle' });
  await page.fill('#pwdField', kiroConfig.password);
  await page.click('button[data-i18n="login.submit"]');
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 10000 });
  const kiroDashboard = path.join(outDir, 'playwright-kiro-dashboard.png');
  await page.screenshot({ path: kiroDashboard, fullPage: true });
  result.screenshots.push(kiroDashboard);

  await page.click('[data-tab="accounts"]');
  await page.waitForSelector('#accountsList', { timeout: 10000 });
  await page.waitForFunction(() => document.querySelector('#accountsList')?.innerText.includes('@'), null, { timeout: 10000 });
  const kiroAccounts = path.join(outDir, 'playwright-kiro-accounts.png');
  await page.screenshot({ path: kiroAccounts, fullPage: true });
  result.screenshots.push(kiroAccounts);

  await page.click('[data-tab="api"]');
  await page.waitForSelector('#tabApi:not(.hidden)', { timeout: 10000 });
  await page.evaluate(() => {
    if (typeof loadClaudeCodeModelReadiness === 'function') {
      return loadClaudeCodeModelReadiness();
    }
    return null;
  });
  await page.waitForTimeout(1000);
  const kiroApi = path.join(outDir, 'playwright-kiro-api-readiness.png');
  await page.screenshot({ path: kiroApi, fullPage: true });
  result.screenshots.push(kiroApi);

  const readiness = await page.evaluate(async (password) => {
    const resp = await fetch('/admin/api/claude-code/model-readiness?model=claude-opus-4-7', {
      headers: { 'X-Admin-Password': password },
    });
    return resp.json();
  }, kiroConfig.password);
  result.api.kiroReadiness = readiness.summary;
  result.checks.kiroDashboardVisible = await page.locator('#mainPage:not(.hidden)').count() === 1;
  result.checks.kiroAccountsRows = await page.evaluate(() => {
    const text = document.querySelector('#accountsList')?.innerText || '';
    return (text.match(/@/g) || []).length;
  });
  result.checks.kiroReadiness = {
    routingReason: readiness.routingReason,
    accountsEvaluated: readiness.summary?.accountsEvaluated,
    locallySchedulable: readiness.summary?.locallySchedulable,
    riskGroupCoolingDown: readiness.summary?.riskGroupCoolingDown,
    nonSchedulable: readiness.accounts.filter((a) => !a.schedulable).length,
  };

  await page.goto('http://127.0.0.1:18080/login', { waitUntil: 'networkidle' });
  await page.getByLabel(/邮箱|Email/i).fill('admin@sub2api.local');
  await page.getByLabel(/密码|Password/i).fill(adminPassword);
  await page.getByRole('button', { name: /登录|Sign in|Login/i }).click();
  await page.waitForURL(/\/(dashboard|admin)/, { timeout: 15000 });
  await page.locator('button').filter({ hasText: '×' }).first().click({ timeout: 3000 }).catch(() => {});
  await page.locator('button').filter({ hasText: /Skip|跳过/i }).first().click({ timeout: 3000 }).catch(() => {});

  await page.goto('http://127.0.0.1:18080/admin/accounts', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  await page.locator('input[placeholder*="Search"], input[placeholder*="搜索"]').first().fill('kiro').catch(() => {});
  await page.waitForTimeout(1500);
  const subAccounts = path.join(outDir, 'playwright-sub2api-accounts.png');
  await page.screenshot({ path: subAccounts, fullPage: true });
  result.screenshots.push(subAccounts);
  const accountsText = await page.locator('body').innerText();
  result.checks.sub2apiAccountsPage = {
    hasKiroClaude01: accountsText.includes('kiro_claude_01'),
    hasActive: /active|启用|正常|活跃/i.test(accountsText),
  };

  await page.goto('http://127.0.0.1:18080/admin/usage?model=claude-opus-4-7', { waitUntil: 'networkidle' });
  await page.locator('button').filter({ hasText: '×' }).first().click({ timeout: 3000 }).catch(() => {});
  await page.locator('button').filter({ hasText: /Skip|跳过/i }).first().click({ timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(1500);
  const subUsage = path.join(outDir, 'playwright-sub2api-usage.png');
  await page.screenshot({ path: subUsage, fullPage: true });
  result.screenshots.push(subUsage);
  const usageText = await page.locator('body').innerText();
  result.checks.sub2apiUsagePage = {
    hasOpusModel: usageText.includes('claude-opus-4-7'),
    hasMessagesEndpoint: usageText.includes('/v1/messages'),
  };

  const subApi = await page.evaluate(async () => {
    const token = localStorage.getItem('auth_token');
    const headers = token ? { Authorization: `Bearer ${token}` } : {};
    const [accountsResp, usageResp, statsResp] = await Promise.all([
      fetch('/api/v1/admin/accounts?page=1&page_size=20&search=kiro', { headers }),
      fetch('/api/v1/admin/usage?page=1&page_size=20&model=claude-opus-4-7', { headers }),
      fetch('/api/v1/admin/usage/stats?model=claude-opus-4-7', { headers }),
    ]);
    return {
      accounts: await accountsResp.json(),
      usage: await usageResp.json(),
      stats: await statsResp.json(),
    };
  });
  result.api.sub2api = {
    accountsCount: subApi.accounts?.data?.items?.length || 0,
    usageCount: subApi.usage?.data?.items?.length || 0,
    totalRequests: subApi.stats?.data?.total_requests || 0,
  };
  result.checks.sub2apiApi = {
    accountsCount: result.api.sub2api.accountsCount,
    usageCount: result.api.sub2api.usageCount,
    totalRequests: result.api.sub2api.totalRequests,
  };

  result.pass =
    result.checks.kiroDashboardVisible &&
    result.checks.kiroAccountsRows >= 21 &&
    result.checks.kiroReadiness.routingReason === 'schedulable accounts available' &&
    result.checks.kiroReadiness.accountsEvaluated === 21 &&
    result.checks.kiroReadiness.locallySchedulable > 0 &&
    result.checks.kiroReadiness.riskGroupCoolingDown === 0 &&
    result.checks.sub2apiAccountsPage.hasKiroClaude01 &&
    result.checks.sub2apiUsagePage.hasOpusModel &&
    result.checks.sub2apiApi.accountsCount >= 1 &&
    result.checks.sub2apiApi.usageCount >= 1 &&
    result.checks.sub2apiApi.totalRequests >= 200;

  fs.writeFileSync(path.join(outDir, 'playwright-summary.json'), JSON.stringify(result, null, 2));
  await browser.close();
  if (!result.pass) {
    throw new Error(`Playwright UAT failed: ${JSON.stringify(result.checks)}`);
  }
  console.log(JSON.stringify(result, null, 2));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
