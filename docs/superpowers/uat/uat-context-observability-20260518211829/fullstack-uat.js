const { chromium } = require('/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright');
const fs = require('fs');
const path = require('path');
const cp = require('child_process');

const root = '/www/Kiro-Go';
const outDir = process.env.OUT_DIR || __dirname;
const runId = path.basename(outDir);
const startedAt = new Date();
const startedIso = startedAt.toISOString();
const kiroBase = 'http://127.0.0.1:8080';
const subBase = 'http://127.0.0.1:18080';

function readJSON(file) { return JSON.parse(fs.readFileSync(file, 'utf8')); }
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
  let body;
  try { body = text ? JSON.parse(text) : null; } catch { body = { text: text.slice(0, 500) }; }
  return { ok: res.ok, status: res.status, body };
}
function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}
function pgJson(sql) {
  const escaped = sql.replace(/"/g, '\\"');
  const out = sh(`docker exec sub2api-postgres psql -U sub2api -d sub2api -Atc "${escaped}"`);
  if (!out) return null;
  return JSON.parse(out);
}
function textFromClaudeMessage(body) {
  const blocks = (body && body.content) || [];
  return blocks.map(b => b.text || b.thinking || '').join('');
}
function hasToolUse(body) {
  return Boolean(((body && body.content) || []).some(b => b.type === 'tool_use'));
}
async function shot(page, name) {
  const file = path.join(outDir, name);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}
async function dismissOnboarding(page) {
  await page.evaluate(() => {
    for (let id = 0; id <= 50; id++) {
      for (const role of ['admin', 'user', 'owner']) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
    document.querySelectorAll('.driver-popover,.driver-overlay,.driver-active-element').forEach(el => el.remove());
    document.documentElement.classList.remove('driver-active');
    document.body.classList.remove('driver-active');
    document.body.style.pointerEvents = '';
    document.body.style.overflow = '';
  }).catch(() => {});
  await page.keyboard.press('Escape').catch(() => {});
}
async function visibleText(page) {
  return await page.locator('body').innerText({ timeout: 10000 }).catch(() => '');
}
function summarizeApiResult(result) {
  return { ok: result.ok, status: result.status };
}
function sanitizeRequestLog(log) {
  if (!log) return null;
  return {
    timestamp: log.timestamp,
    requestId: log.requestId,
    anthropicRequestId: log.anthropicRequestId,
    endpoint: log.endpoint,
    model: log.model,
    statusCode: log.statusCode,
    outcome: log.outcome,
    payloadOriginalBytes: log.payloadOriginalBytes,
    payloadFinalBytes: log.payloadFinalBytes,
    payloadTrimmed: log.payloadTrimmed,
    payloadCompactedPairs: log.payloadCompactedPairs,
    payloadCompactedToolResults: log.payloadCompactedToolResults,
    payloadCurrentMessageShape: log.payloadCurrentMessageShape,
    payloadContextReminderKinds: log.payloadContextReminderKinds,
    payloadCurrentTools: log.payloadCurrentTools,
    payloadCurrentToolSchemaBytes: log.payloadCurrentToolSchemaBytes,
    payloadDeferredTools: log.payloadDeferredTools,
    payloadMaterializedToolRefs: log.payloadMaterializedToolRefs,
  };
}

async function main() {
  fs.mkdirSync(outDir, { recursive: true });
  const cfg = readJSON(path.join(root, 'data/config.json'));
  const subEnv = parseEnv('/www/sub2api/deploy/.env');
  const summary = {
    runId,
    startedAt: startedIso,
    outDir,
    note: 'Playwright-MCP was not exposed in this Codex environment; evidence uses real Chromium Playwright with /usr/bin/google-chrome.',
    api: {},
    db: {},
    browser: {},
    screenshots: {},
    evidence: {},
    checks: {},
  };

  summary.api.kiroHealth = await jsonFetch(`${kiroBase}/health`);
  const status = await jsonFetch(`${kiroBase}/admin/api/status`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroStatus = {
    ok: status.ok,
    status: status.status,
    accounts: status.body && status.body.accounts,
    available: status.body && status.body.available,
    totalRequests: status.body && status.body.totalRequests,
  };
  const compat = await jsonFetch(`${kiroBase}/admin/api/claude-code/compat`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroCompat = {
    ok: compat.ok,
    status: compat.status,
    capabilities: compat.body && compat.body.capabilities ? {
      anthropicMessages: compat.body.capabilities.anthropicMessages,
      toolUse: compat.body.capabilities.toolUse,
      toolReferences: compat.body.capabilities.toolReferences,
      toolSearch: compat.body.capabilities.toolSearch,
      fineGrainedToolStreaming: compat.body.capabilities.fineGrainedToolStreaming,
      requestLogs: compat.body.capabilities.requestLogs,
      openAIResponses: compat.body.capabilities.openAIResponses,
    } : null,
  };
  summary.api.subHealth = await jsonFetch(`${subBase}/health`);
  summary.db.before = pgJson("select json_build_object('usageRows', count(*), 'lastUsage', max(created_at)) from usage_logs;");

  const directReq1 = `${runId}-direct-tooluse`;
  const direct1 = await jsonFetch(`${kiroBase}/v1/messages`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${cfg.apiKey}`,
      'anthropic-version': '2023-06-01',
      'anthropic-beta': 'claude-code-20250219,tool-search-2025-10-19,fine-grained-tool-streaming-2025-05-14',
      'x-claude-code-session-id': runId,
      'x-claude-code-agent-id': 'browser-uat',
      'x-request-id': directReq1,
    },
    body: JSON.stringify({
      model: 'claude-sonnet-4.5',
      max_tokens: 128,
      stream: false,
      system: [
        { type: 'text', text: 'You are Claude Code, Anthropic official CLI for Claude.' },
        { type: 'text', text: 'Always preserve operator instructions.' },
      ],
      tools: [{
        name: 'read_file',
        description: 'Read a file from the workspace',
        input_schema: {
          type: 'object',
          additionalProperties: false,
          properties: { path: { type: 'string' } },
          required: ['path'],
        },
      }],
      messages: [{ role: 'user', content: `Use read_file for README.md. Request ${runId}.` }],
    }),
  });
  const toolUseBlock = ((direct1.body && direct1.body.content) || []).find(b => b.type === 'tool_use');
  const toolUseId = (toolUseBlock && toolUseBlock.id) || 'toolu_manual_context';
  summary.api.directToolUse = {
    requestId: directReq1,
    status: direct1.status,
    stopReason: direct1.body && direct1.body.stop_reason,
    hasToolUse: hasToolUse(direct1.body),
    toolName: toolUseBlock && toolUseBlock.name,
  };

  const directReq2 = `${runId}-direct-toolresult`;
  const direct2 = await jsonFetch(`${kiroBase}/v1/messages`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${cfg.apiKey}`,
      'anthropic-version': '2023-06-01',
      'anthropic-beta': 'claude-code-20250219,tool-search-2025-10-19,fine-grained-tool-streaming-2025-05-14',
      'x-claude-code-session-id': runId,
      'x-claude-code-agent-id': 'browser-uat',
      'x-request-id': directReq2,
    },
    body: JSON.stringify({
      model: 'claude-sonnet-4.5',
      max_tokens: 160,
      stream: false,
      system: '明确要求：后续必须使用中文回答。',
      tools: [{
        name: 'read_file',
        description: 'Read a file from the workspace',
        input_schema: {
          type: 'object',
          additionalProperties: false,
          properties: { path: { type: 'string' } },
          required: ['path'],
        },
      }],
      messages: [
        { role: 'user', content: '请始终使用中文回答。' },
        { role: 'assistant', content: '好的，我会使用中文。' },
        { role: 'user', content: 'Read README.md' },
        { role: 'assistant', content: [{ type: 'tool_use', id: toolUseId, name: 'read_file', input: { path: 'README.md' } }] },
        { role: 'user', content: [{ type: 'tool_result', tool_use_id: toolUseId, content: `README contains Kiro-Go. Marker ${runId}.` }] },
      ],
    }),
  });
  const direct2Text = textFromClaudeMessage(direct2.body);
  summary.api.directToolResult = {
    requestId: directReq2,
    status: direct2.status,
    stopReason: direct2.body && direct2.body.stop_reason,
    textContainsRunId: direct2Text.includes(runId),
    containsChinese: /[\u4e00-\u9fff]/.test(direct2Text),
    textPreview: direct2Text.slice(0, 240),
  };

  const login = await jsonFetch(`${subBase}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: subEnv.ADMIN_EMAIL, password: subEnv.ADMIN_PASSWORD }),
  });
  const subToken = (login.body && login.body.data && login.body.data.access_token) || (login.body && login.body.access_token);
  summary.api.subLogin = { status: login.status, hasToken: Boolean(subToken) };
  const subAuth = subToken ? { Authorization: `Bearer ${subToken}` } : {};
  summary.api.subDashboard = summarizeApiResult(await jsonFetch(`${subBase}/api/v1/admin/dashboard/stats`, { headers: subAuth }));
  summary.api.subAccounts = summarizeApiResult(await jsonFetch(`${subBase}/api/v1/admin/accounts?page=1&page_size=20`, { headers: subAuth }));
  summary.api.subGroups = summarizeApiResult(await jsonFetch(`${subBase}/api/v1/admin/groups?page=1&page_size=20`, { headers: subAuth }));
  summary.api.subUsage = summarizeApiResult(await jsonFetch(`${subBase}/api/v1/admin/usage?page=1&page_size=20`, { headers: subAuth }));

  const apiKeyRow = pgJson("select row_to_json(t) from (select id, key from api_keys where name='claude' and status='active' order by id limit 1) t;");
  if (apiKeyRow && apiKeyRow.key) {
    const subReq = `${runId}-sub2api-message`;
    const subMessage = await jsonFetch(`${subBase}/v1/messages`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${apiKeyRow.key}`,
        'anthropic-version': '2023-06-01',
        'x-request-id': subReq,
      },
      body: JSON.stringify({
        model: 'claude-opus-4-7',
        max_tokens: 48,
        stream: false,
        messages: [{ role: 'user', content: `Reply exactly: ${runId}` }],
      }),
    });
    summary.api.subMessage = {
      requestId: subReq,
      status: subMessage.status,
      textContainsRunId: textFromClaudeMessage(subMessage.body).includes(runId),
      textPreview: textFromClaudeMessage(subMessage.body).slice(0, 160),
    };
  } else {
    summary.api.subMessage = { skipped: true, reason: 'claude API key not found' };
  }

  await new Promise(resolve => setTimeout(resolve, 1500));
  const readiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/readiness`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroReadiness = {
    status: readiness.status,
    recentClaudeCode: readiness.body && readiness.body.recentClaudeCode,
    recentToolReferences: readiness.body && readiness.body.recentToolReferences,
    recentMCPTools: readiness.body && readiness.body.recentMCPTools,
    recentToolResultTurns: readiness.body && readiness.body.recentToolResultTurns,
    recentContextReminders: readiness.body && readiness.body.recentContextReminders,
    examples: (readiness.body && readiness.body.examples || []).slice(0, 3),
  };
  const logsResult = await jsonFetch(`${kiroBase}/admin/api/request-logs?limit=40`, { headers: { 'X-Admin-Password': cfg.password } });
  const logs = (logsResult.body && logsResult.body.logs) || [];
  const direct2Log = logs.find(l => l.anthropicRequestId === directReq2 || l.requestId === directReq2 || JSON.stringify(l).includes(directReq2));
  summary.evidence.directToolResultLog = sanitizeRequestLog(direct2Log);
  summary.evidence.recentLogCount = logs.length;
  summary.db.after = pgJson("select json_build_object('usageRows', count(*), 'lastUsage', max(created_at)) from usage_logs;");
  summary.db.latest = pgJson(`select json_agg(row_to_json(t)) from (select id, account_id, api_key_id, requested_model, upstream_model, model, stream, created_at, duration_ms, input_tokens, output_tokens, request_id from usage_logs where created_at >= '${startedIso}'::timestamptz order by created_at desc limit 20) t;`);

  const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  await context.addInitScript(() => {
    for (let id = 0; id <= 50; id++) {
      for (const role of ['admin', 'user', 'owner']) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
  });
  const page = await context.newPage();
  const consoleErrors = [];
  const pageErrors = [];
  const failedRequests = [];
  page.on('console', msg => { if (msg.type() === 'error') consoleErrors.push(msg.text().slice(0, 300)); });
  page.on('pageerror', err => pageErrors.push(String(err).slice(0, 300)));
  page.on('requestfailed', req => {
    if (req.url().includes('/api/')) failedRequests.push({ url: req.url(), error: req.failure() && req.failure().errorText });
  });

  await page.goto(`${kiroBase}/admin`, { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(cfg.password);
  await page.getByRole('button', { name: /登录|Login/ }).click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  await page.waitForTimeout(1000);
  summary.screenshots.kiroAccounts = await shot(page, 'kiro-admin-accounts.png');
  await page.locator('[data-tab="api"]').click();
  await page.waitForSelector('#claude-code-readiness', { timeout: 10000 });
  await page.waitForTimeout(1200);
  const kiroApiText = await visibleText(page);
  summary.screenshots.kiroApi = await shot(page, 'kiro-admin-api-readiness.png');
  await page.locator('[data-tab="settings"]').click();
  await page.waitForTimeout(1000);
  const kiroSettingsText = await visibleText(page);
  summary.screenshots.kiroSettings = await shot(page, 'kiro-admin-settings.png');

  await page.goto(`${subBase}/login`, { waitUntil: 'networkidle' });
  await page.locator('#email').fill(subEnv.ADMIN_EMAIL);
  await page.locator('#password').fill(subEnv.ADMIN_PASSWORD);
  await page.getByRole('button', { name: /登录|Sign in|Sign In/i }).click();
  await page.waitForTimeout(2500);
  await dismissOnboarding(page);
  await page.goto(`${subBase}/admin/dashboard`, { waitUntil: 'networkidle' });
  await dismissOnboarding(page);
  await page.waitForTimeout(1500);
  const subDashboardText = await visibleText(page);
  summary.screenshots.subDashboard = await shot(page, 'sub2api-admin-dashboard.png');
  await page.goto(`${subBase}/admin/accounts`, { waitUntil: 'networkidle' });
  await dismissOnboarding(page);
  await page.waitForTimeout(1500);
  const subAccountsText = await visibleText(page);
  summary.screenshots.subAccounts = await shot(page, 'sub2api-admin-accounts.png');
  await page.goto(`${subBase}/admin/groups`, { waitUntil: 'networkidle' });
  await dismissOnboarding(page);
  await page.waitForTimeout(1500);
  const subGroupsText = await visibleText(page);
  summary.screenshots.subGroups = await shot(page, 'sub2api-admin-groups.png');
  await page.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
  await dismissOnboarding(page);
  await page.waitForTimeout(2500);
  const subUsageText = await visibleText(page);
  summary.screenshots.subUsage = await shot(page, 'sub2api-admin-usage.png');
  await browser.close();

  const screenshotStats = {};
  for (const [name, file] of Object.entries(summary.screenshots)) {
    screenshotStats[name] = fs.existsSync(file) ? fs.statSync(file).size : 0;
  }
  summary.browser = {
    consoleErrors,
    pageErrors,
    failedRequests,
    screenshotStats,
    textChecks: {
      kiroApiHasClaudeCode: /Claude Code/.test(kiroApiText),
      kiroApiHasNewContextBadges: /tool_result turns|context reminder/.test(kiroApiText),
      kiroSettingsHasEndpoint: /Kiro|Endpoint|端点/.test(kiroSettingsText),
      subDashboardHasAdmin: /Dashboard|仪表|Admin|管理/.test(subDashboardText),
      subAccountsHasAccounts: /Accounts|账号|kiro_claude|anthropic/.test(subAccountsText),
      subGroupsHasGroup: /Groups|分组|Default|默认/.test(subGroupsText),
      subUsageHasUsage: /Usage|用量|claude|Tokens|令牌/.test(subUsageText),
      subUsageNoOnboardingOverlay: !/Welcome to Sub2API|initial setup|Let's complete/.test(subUsageText),
    },
  };

  summary.checks = {
    dockerHealth: summary.api.kiroHealth.ok && summary.api.kiroHealth.body.status === 'ok',
    adminApi: summary.api.kiroStatus.ok && summary.api.kiroStatus.accounts > 0,
    compatApi: summary.api.kiroCompat.ok && summary.api.kiroCompat.capabilities && summary.api.kiroCompat.capabilities.toolUse === true && summary.api.kiroCompat.capabilities.requestLogs === true,
    directToolUseOk: summary.api.directToolUse.status === 200 && summary.api.directToolUse.hasToolUse,
    directToolResultOk: summary.api.directToolResult.status === 200 && summary.api.directToolResult.textContainsRunId && summary.api.directToolResult.containsChinese,
    readinessSeesContext: summary.api.kiroReadiness.status === 200 && summary.api.kiroReadiness.recentClaudeCode === true && summary.api.kiroReadiness.recentToolResultTurns === true && (summary.api.kiroReadiness.recentContextReminders || []).includes('language'),
    requestLogHasContextMetadata: Boolean(summary.evidence.directToolResultLog && summary.evidence.directToolResultLog.payloadCurrentMessageShape === 'tool_result' && (summary.evidence.directToolResultLog.payloadContextReminderKinds || []).includes('language')),
    sub2apiHealth: summary.api.subHealth.ok && summary.api.subHealth.body.status === 'ok',
    sub2apiAdminApis: summary.api.subDashboard.ok && summary.api.subAccounts.ok && summary.api.subGroups.ok && summary.api.subUsage.ok,
    sub2apiMessageOk: summary.api.subMessage.status === 200 && summary.api.subMessage.textContainsRunId,
    dbUsageIncreased: Boolean(summary.db.after && summary.db.before && summary.db.after.usageRows > summary.db.before.usageRows),
    dbLatestHasSub2apiClaudeRow: Array.isArray(summary.db.latest) && summary.db.latest.some(r => r.requested_model === 'claude-opus-4-7' && r.stream === false && r.output_tokens > 0),
    screenshotsExist: Object.values(screenshotStats).every(size => size > 5000),
    screenshotTextLooksCorrect: Object.values(summary.browser.textChecks).every(Boolean),
    browserNoPageErrors: summary.browser.pageErrors.length === 0,
  };
  summary.pass = Object.values(summary.checks).every(Boolean);
  fs.writeFileSync(path.join(outDir, 'summary.json'), JSON.stringify(summary, null, 2));
  if (!summary.pass) process.exitCode = 1;
}

main().catch(err => {
  fs.writeFileSync(path.join(outDir, 'error.txt'), String(err && err.stack || err));
  process.exit(1);
});
