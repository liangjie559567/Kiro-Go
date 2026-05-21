import cp from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const project = '/root/gsd-workspaces/gsd-boundary-workflow-20260522';
const outDir = path.join(project, 'uat-claude-api-path', new Date().toISOString().replace(/[-:.TZ]/g, '').slice(0, 14));
fs.mkdirSync(outDir, { recursive: true });
const sub2apiBase = 'http://127.0.0.1:18080';
const kiroBase = 'http://127.0.0.1:8080';
const model = 'claude-opus-4-7';
const agents = Number(process.env.CLAUDE_API_AGENTS || 4);
const runId = `claude-api-${Date.now()}`;
const startedAt = new Date().toISOString();

function redact(s) {
  return String(s || '')
    .replace(/sk-[A-Za-z0-9_-]+/g, 'sk-<redacted>')
    .replace(/(Authorization|ANTHROPIC_AUTH_TOKEN|Bearer)([:= ]+)[^\s"]+/gi, '$1$2<redacted>');
}
function writeJSON(name, value) { fs.writeFileSync(path.join(outDir, name), JSON.stringify(value, null, 2)); }
function pg(sql) {
  const r = cp.spawnSync('docker', ['exec', '-i', 'sub2api-postgres', 'sh', '-lc', 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atq'], { input: sql, encoding: 'utf8', maxBuffer: 20 * 1024 * 1024 });
  if (r.status !== 0) throw new Error(redact(r.stderr || r.stdout));
  return String(r.stdout || '').trim();
}
function pgJSON(sql, fallback) {
  const text = pg(sql);
  if (!text) return fallback;
  return JSON.parse(text);
}
async function readiness(label) {
  const password = JSON.parse(fs.readFileSync('/www/Kiro-Go/data/config.json', 'utf8')).password || '';
  const res = await fetch(`${kiroBase}/admin/api/fleet/readiness?model=${model}`, { headers: { 'X-Admin-Password': password } });
  const body = await res.json();
  const summary = {
    label,
    status: body.status,
    circuitState: body.circuitState,
    reasonCodes: body.reasonCodes,
    safeConcurrency: body.safeConcurrency,
    locallySchedulableAccounts: body.locallySchedulableAccounts,
    coolingDownAccounts: body.coolingDownAccounts,
    temporaryLimitedAccounts: body.temporaryLimitedAccounts,
    admissionPressureScore: body.admissionPressureScore,
    lastPressureReason: body.lastPressureReason,
    retryAfterSeconds: body.retryAfterSeconds,
    recommendedAction: body.recommendedAction,
  };
  writeJSON(`readiness-${label}.json`, summary);
  return summary;
}
function runClaude(index, apiKey) {
  return new Promise((resolve) => {
    const expected = `OK${index}`;
    const prompt = [
      `Return only this exact token, with no punctuation, no markdown, and no other text: ${expected}`,
    ].join(' ');
    const started = Date.now();
    const child = cp.spawn('claude', ['-p', prompt, '--model', model, '--output-format', 'json', '--max-budget-usd', '2'], {
      cwd: project,
      env: {
        ...process.env,
        ANTHROPIC_BASE_URL: sub2apiBase,
        ANTHROPIC_AUTH_TOKEN: apiKey,
        ANTHROPIC_MODEL: model,
        CLAUDE_CODE_MAX_RETRIES: '1',
        CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
        [`CLAUDE_API_PATH_AGENT_${index}`]: runId,
      },
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', c => stdout += c);
    child.stderr.on('data', c => stderr += c);
    child.on('close', code => {
      fs.writeFileSync(path.join(outDir, `claude-${index}.stdout.json`), redact(stdout));
      fs.writeFileSync(path.join(outDir, `claude-${index}.stderr.log`), redact(stderr));
      let parsed = null;
      try { parsed = JSON.parse(stdout); } catch {}
      const result = parsed?.result || stdout;
      const apiOk = code === 0 && (!parsed || parsed.is_error !== true);
      const markerOk = String(result).trim() === expected;
      resolve({
        index,
        exitCode: code,
        durationMs: Date.now() - started,
        parsed: Boolean(parsed),
        isError: parsed ? parsed.is_error === true : code !== 0,
        apiErrorStatus: parsed?.api_error_status ?? null,
        stopReason: parsed?.stop_reason ?? null,
        apiOk,
        markerOk,
        ok: apiOk,
        expected,
        resultHead: String(result).slice(0, 300),
        overloaded: /overloaded_error|529|capacity|temporarily unavailable|cooling_down/i.test(stdout + stderr),
        stderrHead: redact(stderr).slice(0, 500),
      });
    });
  });
}
async function main() {
  const apiKey = pg("select key from api_keys where id=2 and status='active'");
  if (!apiKey) throw new Error('missing active sub2api api key id=2');
  const pre = await readiness('before');
  const health = {
    kiro: await (await fetch(`${kiroBase}/health`)).json(),
    sub2api: await (await fetch(`${sub2apiBase}/health`)).json(),
  };
  writeJSON('health-before.json', health);

  const results = await Promise.all(Array.from({ length: agents }, (_, i) => runClaude(i + 1, apiKey)));
  writeJSON('claude-results.json', results);
  await new Promise(resolve => setTimeout(resolve, 1500));
  const post = await readiness('after');

  const db = pgJSON(`select json_build_object(
    'usage', coalesce((select json_agg(row_to_json(t)) from (
      select id, created_at, api_key_id, model, requested_model, upstream_model, inbound_endpoint, upstream_endpoint, stream, duration_ms, user_agent
      from usage_logs
      where created_at >= ('${startedAt}'::timestamptz - interval '2 minutes') and api_key_id=2
      order by created_at desc limit 100
    ) t), '[]'::json),
    'errors', coalesce((select json_agg(row_to_json(t)) from (
      select id, created_at, api_key_id, model, requested_model, upstream_model, request_path, inbound_endpoint, upstream_endpoint, stream, status_code, upstream_status_code, error_type, provider_error_type, retry_after_seconds, left(coalesce(error_message,''), 300) as error_message
      from ops_error_logs
      where created_at >= ('${startedAt}'::timestamptz - interval '2 minutes') and api_key_id=2
      order by created_at desc limit 100
    ) t), '[]'::json)
  )`, { usage: [], errors: [] });
  writeJSON('db-evidence.json', db);
  const usage = db.usage || [];
  const errors = db.errors || [];
  const summary = {
    runId,
    startedAt,
    outDir,
    preReadiness: pre,
    postReadiness: post,
    agents,
    claudeApiOk: results.filter(r => r.apiOk).length,
    claudeApiFailed: results.filter(r => !r.apiOk).length,
    markerOk: results.filter(r => r.markerOk).length,
    overloaded: results.filter(r => r.overloaded).length,
    usageRows: usage.length,
    errorRows: errors.length,
    gatewayPass: results.every(r => r.apiOk) && usage.length >= results.length && results.every(r => !r.overloaded),
    readinessPass: post.status === 'healthy' || (post.status === 'degraded' && post.safeConcurrency > 0),
  };
  summary.pass = summary.gatewayPass && summary.readinessPass;
  writeJSON('summary.json', summary);
  console.log(JSON.stringify(summary, null, 2));
  process.exit(summary.pass ? 0 : 1);
}

main().catch(err => {
  writeJSON('fatal-error.json', { message: err.message, stack: err.stack });
  console.error(err);
  process.exit(1);
});
