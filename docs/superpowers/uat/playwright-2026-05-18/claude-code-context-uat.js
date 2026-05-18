const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const childProcess = require('child_process');

const root = path.resolve(__dirname, '../../../..');
const outDir = __dirname;

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function parseEnv(file) {
  const env = {};
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || !trimmed.includes('=')) continue;
    const idx = trimmed.indexOf('=');
    env[trimmed.slice(0, idx)] = trimmed.slice(idx + 1);
  }
  return env;
}

async function jsonFetch(url, options = {}) {
  const res = await fetch(url, options);
  const text = await res.text();
  let body = null;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = { parseError: true, text: text.slice(0, 200) };
  }
  return { ok: res.ok, status: res.status, body };
}

function anthropicHeaders(extra = {}) {
  return {
    'Content-Type': 'application/json',
    'anthropic-version': '2023-06-01',
    'anthropic-beta': 'claude-code-20250219,tool-search-2025-10-19',
    'x-claude-code-session-id': `playwright-uat-${Date.now()}`,
    'x-claude-code-agent-id': 'playwright-uat',
    ...extra,
  };
}

async function screenshot(page, name) {
  const file = path.join(outDir, name);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

async function hasSub2apiOnboarding(page) {
  return await page.evaluate(() => Boolean(
    Array.from(document.querySelectorAll('.driver-popover, .driver-overlay')).some(el => {
      const style = window.getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
    }) ||
    document.body.innerText.includes('Welcome to Sub2API') ||
    document.body.innerText.includes("Let's complete the initial setup")
  )).catch(() => false);
}

async function dismissSub2apiOnboarding(page) {
  await page.evaluate(() => {
    const roles = ['admin', 'user', 'owner'];
    for (let id = 0; id <= 20; id += 1) {
      for (const role of roles) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    document.querySelectorAll('.driver-popover, .driver-overlay, .driver-active-element').forEach(el => el.remove());
    document.documentElement.classList.remove('driver-active');
    document.body.classList.remove('driver-active');
    document.body.style.pointerEvents = '';
    document.body.style.overflow = '';
  }).catch(() => {});
  for (const name of [/Skip/i, /Exit/i, /Close/i, /跳过/, /退出/, /关闭/]) {
    const button = page.getByRole('button', { name }).first();
    if (await button.isVisible().catch(() => false)) {
      await button.click({ force: true }).catch(() => {});
      await page.waitForTimeout(300);
    }
  }
  for (const selector of [
    'button:has-text("Skip")',
    'button:has-text("Exit")',
    'button:has-text("×")',
    'button[aria-label="Close"]',
    '.driver-popover-close-btn',
    '.driver-popover-done-btn',
  ]) {
    const target = page.locator(selector).first();
    if (await target.isVisible().catch(() => false)) {
      await target.click({ force: true }).catch(() => {});
      await page.waitForTimeout(300);
    }
  }
  if (await hasSub2apiOnboarding(page)) {
    await page.keyboard.press('Escape').catch(() => {});
    await page.waitForTimeout(300);
  }
  await page.evaluate(() => {
    document.querySelectorAll('.driver-popover, .driver-overlay, .driver-active-element').forEach(el => el.remove());
    document.documentElement.classList.remove('driver-active');
    document.body.classList.remove('driver-active');
    document.body.style.pointerEvents = '';
    document.body.style.overflow = '';
  }).catch(() => {});
}

function sh(cmd) {
  return childProcess.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

async function main() {
  fs.mkdirSync(outDir, { recursive: true });
  const kiroConfig = readJSON(path.join(root, 'data/config.json'));
  const sub2apiEnv = parseEnv('/www/sub2api/deploy/.env');
  const now = new Date().toISOString();

  const api = {
    timestamp: now,
    kiro: {},
    sub2api: {},
    database: {},
  };

  api.kiro.health = await jsonFetch('http://127.0.0.1:8080/health');
  api.kiro.models = await jsonFetch('http://127.0.0.1:8080/v1/models');
  api.kiro.adminStatus = await jsonFetch('http://127.0.0.1:8080/admin/api/status', {
    headers: { 'X-Admin-Password': kiroConfig.password },
  });
  api.kiro.readiness = await jsonFetch('http://127.0.0.1:8080/admin/api/claude-code/readiness', {
    headers: { 'X-Admin-Password': kiroConfig.password },
  });
  api.kiro.requestStats = await jsonFetch('http://127.0.0.1:8080/admin/api/request-stats', {
    headers: { 'X-Admin-Password': kiroConfig.password },
  });
  api.kiro.claudeCodeToolReferenceSmoke = await jsonFetch('http://127.0.0.1:8080/v1/messages', {
    method: 'POST',
    headers: anthropicHeaders({
      Authorization: `Bearer ${kiroConfig.apiKey || 'playwright-uat'}`,
      'x-request-id': `playwright-uat-tool-reference-${Date.now()}`,
    }),
    body: JSON.stringify({
      model: 'claude-sonnet-4.5',
      max_tokens: 16,
      stream: false,
      messages: [
        {
          role: 'user',
          content: 'Reply exactly: OK',
        },
      ],
      tool_reference: [
        {
          type: 'tool_reference',
          name: 'mcp__browser__screenshot',
          title: 'Screenshot',
          description: 'Capture a browser screenshot for Playwright UAT evidence.',
          input_schema: {
            type: 'object',
            properties: { url: { type: 'string' } },
            required: ['url'],
          },
        },
      ],
    }),
  });
  api.kiro.readinessAfterClaudeCodeSmoke = await jsonFetch('http://127.0.0.1:8080/admin/api/claude-code/readiness', {
    headers: { 'X-Admin-Password': kiroConfig.password },
  });

  api.sub2api.health = await jsonFetch('http://127.0.0.1:18080/health');
  api.sub2api.login = await jsonFetch('http://127.0.0.1:18080/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      email: sub2apiEnv.ADMIN_EMAIL,
      password: sub2apiEnv.ADMIN_PASSWORD,
    }),
  });

  const subToken = api.sub2api.login.body?.data?.access_token || api.sub2api.login.body?.access_token || '';
  const subAuth = subToken ? { Authorization: `Bearer ${subToken}` } : {};
  api.sub2api.dashboard = await jsonFetch('http://127.0.0.1:18080/api/v1/admin/dashboard/stats', { headers: subAuth });
  api.sub2api.accounts = await jsonFetch('http://127.0.0.1:18080/api/v1/admin/accounts?page=1&page_size=20', { headers: subAuth });
  api.sub2api.groups = await jsonFetch('http://127.0.0.1:18080/api/v1/admin/groups?page=1&page_size=20', { headers: subAuth });
  api.sub2api.usage = await jsonFetch('http://127.0.0.1:18080/api/v1/admin/usage?page=1&page_size=20', { headers: subAuth });

  const dbLines = sh(`docker exec sub2api-postgres psql -U sub2api -d sub2api -Atc "select 'users='||count(*) from users union all select 'groups='||count(*) from groups union all select 'accounts='||count(*) from accounts union all select 'api_keys='||count(*) from api_keys;"`).split('\n');
  for (const line of dbLines) {
    const [key, value] = line.split('=');
    api.database[key] = Number(value);
  }

  const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome' });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.addInitScript(() => {
    const roles = ['admin', 'user', 'owner'];
    for (let id = 0; id <= 20; id += 1) {
      for (const role of roles) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
  });
  const page = await context.newPage();
  const consoleMessages = [];
  const pageErrors = [];
  page.on('console', msg => {
    if (['error', 'warning'].includes(msg.type())) {
      consoleMessages.push({ type: msg.type(), text: msg.text().slice(0, 300) });
    }
  });
  page.on('pageerror', err => pageErrors.push(String(err).slice(0, 300)));

  const screenshots = {};

  await page.goto('http://127.0.0.1:8080/admin', { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(kiroConfig.password);
  await page.getByRole('button', { name: /登录|Login/ }).click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 10000 });
  await page.waitForSelector('#accountsList', { timeout: 10000 });
  screenshots.kiroAccounts = await screenshot(page, 'kiro-admin-accounts.png');
  await page.locator('[data-tab="api"]').click();
  await page.waitForSelector('#claude-code-readiness', { timeout: 10000 });
  await page.waitForTimeout(800);
  screenshots.kiroApiReadiness = await screenshot(page, 'kiro-admin-api-readiness.png');
  await page.locator('[data-tab="settings"]').click();
  await page.waitForFunction(() => !document.querySelector('#tabSettings')?.classList.contains('hidden'), null, { timeout: 10000 });
  screenshots.kiroSettings = await screenshot(page, 'kiro-admin-settings.png');

  await page.goto('http://127.0.0.1:18080/login', { waitUntil: 'networkidle' });
  await page.locator('#email').fill(sub2apiEnv.ADMIN_EMAIL);
  await page.locator('#password').fill(sub2apiEnv.ADMIN_PASSWORD);
  await page.getByRole('button', { name: /登录|Sign in|Sign In/i }).click();
  await page.waitForURL(/\/admin\/dashboard|\/dashboard/, { timeout: 15000 });
  await dismissSub2apiOnboarding(page);
  await page.goto('http://127.0.0.1:18080/admin/dashboard', { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(1500);
  await dismissSub2apiOnboarding(page);
  const sub2apiDashboardOnboardingVisible = await hasSub2apiOnboarding(page);
  screenshots.sub2apiDashboard = await screenshot(page, 'sub2api-admin-dashboard.png');
  await page.goto('http://127.0.0.1:18080/admin/accounts', { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(2000);
  await dismissSub2apiOnboarding(page);
  const sub2apiAccountsOnboardingVisible = await hasSub2apiOnboarding(page);
  screenshots.sub2apiAccounts = await screenshot(page, 'sub2api-admin-accounts.png');
  await page.goto('http://127.0.0.1:18080/admin/groups', { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(1500);
  await dismissSub2apiOnboarding(page);
  const sub2apiGroupsOnboardingVisible = await hasSub2apiOnboarding(page);
  screenshots.sub2apiGroups = await screenshot(page, 'sub2api-admin-groups.png');
  await page.goto('http://127.0.0.1:18080/admin/usage', { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(2000);
  await dismissSub2apiOnboarding(page);
  const sub2apiUsageOnboardingVisible = await hasSub2apiOnboarding(page);
  screenshots.sub2apiUsage = await screenshot(page, 'sub2api-admin-usage.png');

  const pageState = await page.evaluate(() => ({
    title: document.title,
    url: location.href,
    bodyTextSample: document.body.innerText.slice(0, 2000),
    hasVisibleTable: !!document.querySelector('table'),
    hasCards: document.querySelectorAll('.card, [class*="card"]').length,
  }));

  await browser.close();

  const summary = {
    timestamp: now,
    tool: 'Playwright CLI using real Chromium; Playwright-MCP tool endpoint was not exposed in this Codex runtime',
    screenshots,
    checks: {
      kiroHealthOk: api.kiro.health.ok && api.kiro.health.body?.status === 'ok',
      kiroModelsCount: api.kiro.models.body?.data?.length || 0,
      kiroAdminOk: api.kiro.adminStatus.ok,
      kiroReadinessOk: api.kiro.readiness.ok,
      kiroClaudeCodeToolReferenceSmokeOk: api.kiro.claudeCodeToolReferenceSmoke.ok,
      kiroReadinessSawClaudeCode: api.kiro.readinessAfterClaudeCodeSmoke.body?.recentClaudeCode === true,
      kiroReadinessSawToolReferences: api.kiro.readinessAfterClaudeCodeSmoke.body?.recentToolReferences === true,
      kiroReadinessSawMCPTools: api.kiro.readinessAfterClaudeCodeSmoke.body?.recentMCPTools === true,
      sub2apiHealthOk: api.sub2api.health.ok && api.sub2api.health.body?.status === 'ok',
      sub2apiLoginOk: api.sub2api.login.ok && !!subToken,
      sub2apiDashboardOk: api.sub2api.dashboard.ok,
      sub2apiAccountsApiOk: api.sub2api.accounts.ok,
      sub2apiGroupsApiOk: api.sub2api.groups.ok,
      sub2apiUsageApiOk: api.sub2api.usage.ok,
      dbUsers: api.database.users,
      dbGroups: api.database.groups,
      dbAccounts: api.database.accounts,
      dbApiKeys: api.database.api_keys,
      browserConsoleWarningsOrErrors: consoleMessages.length,
      browserPageErrors: pageErrors.length,
      finalPageHasContent: pageState.bodyTextSample.length > 100,
      screenshotsUnobstructed: !sub2apiDashboardOnboardingVisible && !sub2apiAccountsOnboardingVisible && !sub2apiGroupsOnboardingVisible && !sub2apiUsageOnboardingVisible,
    },
    apiEvidence: {
      kiroHealth: api.kiro.health.body,
      kiroModelsCount: api.kiro.models.body?.data?.length || 0,
      kiroModelSample: (api.kiro.models.body?.data || []).slice(0, 8).map(m => m.id),
      kiroAdminStatus: api.kiro.adminStatus.body ? {
        accounts: api.kiro.adminStatus.body.accounts,
        totalRequests: api.kiro.adminStatus.body.totalRequests,
        successRequests: api.kiro.adminStatus.body.successRequests,
        failedRequests: api.kiro.adminStatus.body.failedRequests,
        version: api.kiro.adminStatus.body.version,
      } : null,
      kiroReadiness: api.kiro.readiness.body,
      kiroReadinessAfterClaudeCodeSmoke: api.kiro.readinessAfterClaudeCodeSmoke.body,
      kiroClaudeCodeToolReferenceSmoke: {
        status: api.kiro.claudeCodeToolReferenceSmoke.status,
        ok: api.kiro.claudeCodeToolReferenceSmoke.ok,
        stopReason: api.kiro.claudeCodeToolReferenceSmoke.body?.stop_reason,
        text: (api.kiro.claudeCodeToolReferenceSmoke.body?.content || []).map(block => block.text || '').join('').slice(0, 80),
        errorType: api.kiro.claudeCodeToolReferenceSmoke.body?.error?.type,
      },
      sub2apiHealth: api.sub2api.health.body,
      sub2apiDashboardKeys: api.sub2api.dashboard.body?.data ? Object.keys(api.sub2api.dashboard.body.data).slice(0, 20) : [],
      sub2apiAccountsTotal: api.sub2api.accounts.body?.data?.total ?? api.sub2api.accounts.body?.total ?? null,
      sub2apiGroupsTotal: api.sub2api.groups.body?.data?.total ?? api.sub2api.groups.body?.total ?? null,
      sub2apiUsageTotal: api.sub2api.usage.body?.data?.total ?? api.sub2api.usage.body?.total ?? null,
      database: api.database,
    },
    browserEvidence: {
      pageState,
      consoleMessages,
      pageErrors,
      onboardingVisible: {
        dashboard: sub2apiDashboardOnboardingVisible,
        accounts: sub2apiAccountsOnboardingVisible,
        groups: sub2apiGroupsOnboardingVisible,
        usage: sub2apiUsageOnboardingVisible,
      },
    },
  };

  const summaryPath = path.join(outDir, 'summary.json');
  fs.writeFileSync(summaryPath, JSON.stringify(summary, null, 2));
  console.log(JSON.stringify({
    summaryPath,
    checks: summary.checks,
    screenshots,
  }, null, 2));

  const failed = Object.entries(summary.checks).filter(([key, value]) => {
    if (key === 'browserConsoleWarningsOrErrors' || key === 'browserPageErrors') return value !== 0;
    if (key.startsWith('db')) return !(Number(value) > 0);
    if (key === 'kiroModelsCount') return !(Number(value) > 0);
    return value !== true;
  });
  if (failed.length) {
    console.error('Failed checks:', JSON.stringify(failed));
    process.exit(1);
  }
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
