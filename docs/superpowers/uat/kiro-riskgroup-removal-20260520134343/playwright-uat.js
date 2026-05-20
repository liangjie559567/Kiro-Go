const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

async function main() {
  const outDir = __dirname;
  const password = fs.readFileSync('/www/Kiro-Go/data/config.json', 'utf8');
  const adminPassword = JSON.parse(password).password;
  const browser = await chromium.launch({
    executablePath: '/usr/bin/google-chrome',
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const result = {
    url: 'http://127.0.0.1:8080/admin',
    checks: {},
    screenshots: [],
  };

  await page.goto(result.url, { waitUntil: 'networkidle' });
  await page.fill('#pwdField', adminPassword);
  await page.click('button[data-i18n="login.submit"]');
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 10000 });

  const dashboardPath = path.join(outDir, 'kiro-dashboard.png');
  await page.screenshot({ path: dashboardPath, fullPage: true });
  result.screenshots.push(dashboardPath);
  result.checks.dashboardVisible = await page.locator('#mainPage:not(.hidden)').count() === 1;

  await page.click('[data-tab="accounts"]');
  await page.waitForSelector('#accountsList', { timeout: 10000 });
  await page.waitForFunction(() => document.querySelector('#accountsList')?.innerText.includes('@'), null, { timeout: 10000 });
  const accountsPath = path.join(outDir, 'kiro-accounts-page.png');
  await page.screenshot({ path: accountsPath, fullPage: true });
  result.screenshots.push(accountsPath);
  result.checks.accountsRows = await page.evaluate(() => {
    const text = document.querySelector('#accountsList')?.innerText || '';
    return (text.match(/@/g) || []).length;
  });
  result.checks.accountsTextHasRiskGroup = (await page.locator('body').innerText()).includes('风险组');

  await page.click('[data-tab="api"]');
  await page.waitForSelector('#tabApi:not(.hidden)', { timeout: 10000 });
  await page.evaluate(() => {
    if (typeof loadClaudeCodeModelReadiness === 'function') {
      return loadClaudeCodeModelReadiness();
    }
  });
  await page.waitForTimeout(1000);
  const apiPath = path.join(outDir, 'kiro-api-readiness-page.png');
  await page.screenshot({ path: apiPath, fullPage: true });
  result.screenshots.push(apiPath);

  const readiness = await page.evaluate(async (adminPassword) => {
    const resp = await fetch('/admin/api/claude-code/model-readiness?model=claude-opus-4-7', {
      headers: { 'X-Admin-Password': adminPassword },
    });
    return await resp.json();
  }, adminPassword);
  result.checks.readiness = {
    routingReason: readiness.routingReason,
    accountsEvaluated: readiness.summary && readiness.summary.accountsEvaluated,
    locallySchedulable: readiness.summary && readiness.summary.locallySchedulable,
    riskGroupCoolingDown: readiness.summary && readiness.summary.riskGroupCoolingDown,
    generationBlocked: readiness.summary && readiness.summary.generationBlocked,
    nonSchedulable: readiness.accounts.filter((a) => !a.schedulable).length,
    riskGroupRows: readiness.accounts.filter((a) => a.cooldownSource === 'risk_group').length,
  };

  result.pass =
    result.checks.dashboardVisible &&
    result.checks.accountsRows >= 21 &&
    result.checks.readiness.routingReason === 'schedulable accounts available' &&
    result.checks.readiness.accountsEvaluated === 21 &&
    result.checks.readiness.locallySchedulable === 21 &&
    result.checks.readiness.riskGroupCoolingDown === 0 &&
    result.checks.readiness.riskGroupRows === 0;

  fs.writeFileSync(path.join(outDir, 'playwright-summary.json'), JSON.stringify(result, null, 2));
  await browser.close();
  if (!result.pass) {
    throw new Error(`UAT failed: ${JSON.stringify(result.checks)}`);
  }
  console.log(JSON.stringify(result, null, 2));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
