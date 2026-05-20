const fs = require('fs');
const path = require('path');

const outDir = path.resolve(__dirname, '..', 'api');
const url = process.env.SUB2API_URL || 'http://127.0.0.1:18080/v1/messages';
const apiKey = process.env.SUB2API_KEY;
const mode = process.env.LOAD_MODE || 'nonstream';
const rounds = Number(process.env.ROUNDS || 10);
const concurrency = Number(process.env.CONCURRENCY || 10);
const timeoutMs = Number(process.env.TIMEOUT_MS || 120000);

if (!apiKey) {
  throw new Error('SUB2API_KEY is required');
}

function contentText(json) {
  if (!json || !Array.isArray(json.content)) return '';
  return json.content.map((part) => part && (part.text || '')).join('');
}

async function runOne(round, slot) {
  const stream = mode === 'stream';
  const requestId = `uat-opus47-${mode}-r${round}-s${slot}-${Date.now()}`;
  const body = {
    model: 'claude-opus-4-7',
    max_tokens: 16,
    stream,
    messages: [
      {
        role: 'user',
        content: `Validation probe ${requestId}. Reply with exactly: ok`,
      },
    ],
  };
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error('timeout')), timeoutMs);
  const start = Date.now();
  try {
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'anthropic-version': '2023-06-01',
        authorization: `Bearer ${apiKey}`,
        'x-client-request-id': requestId,
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
    const text = await resp.text();
    const durationMs = Date.now() - start;
    let parsed = null;
    if (!stream) {
      try {
        parsed = JSON.parse(text);
      } catch {}
    }
    const output = stream ? text : contentText(parsed);
    const hasOk = /\bok\b/i.test(output);
    return {
      round,
      slot,
      requestId,
      status: resp.status,
      ok: resp.status === 200 && hasOk,
      hasOk,
      durationMs,
      retryAfter: resp.headers.get('retry-after') || '',
      kiroReason: resp.headers.get('x-kiro-go-error-reason') || '',
      bodyPrefix: text.slice(0, 500),
    };
  } catch (err) {
    return {
      round,
      slot,
      requestId,
      status: 0,
      ok: false,
      hasOk: false,
      durationMs: Date.now() - start,
      error: err && err.message ? err.message : String(err),
    };
  } finally {
    clearTimeout(timer);
  }
}

async function main() {
  const all = [];
  for (let round = 1; round <= rounds; round += 1) {
    const batch = [];
    for (let slot = 1; slot <= concurrency; slot += 1) {
      batch.push(runOne(round, slot));
    }
    const results = await Promise.all(batch);
    all.push(...results);
    const pass = results.filter((r) => r.ok).length;
    const max = Math.max(...results.map((r) => r.durationMs));
    console.log(`${mode} round=${round} pass=${pass}/${results.length} max_ms=${max}`);
  }
  const durations = all.map((r) => r.durationMs).sort((a, b) => a - b);
  const failures = all.filter((r) => !r.ok);
  const statusCounts = {};
  for (const row of all) statusCounts[row.status] = (statusCounts[row.status] || 0) + 1;
  const summary = {
    mode,
    url,
    rounds,
    concurrency,
    total: all.length,
    passed: all.length - failures.length,
    failed: failures.length,
    pass: failures.length === 0,
    statusCounts,
    maxLatencyMs: durations[durations.length - 1] || 0,
    p95LatencyMs: durations[Math.max(0, Math.ceil(durations.length * 0.95) - 1)] || 0,
    p50LatencyMs: durations[Math.max(0, Math.ceil(durations.length * 0.5) - 1)] || 0,
    firstFailure: failures[0] || null,
    startedAt: new Date(Date.now() - durations.reduce((a, b) => Math.max(a, b), 0)).toISOString(),
    completedAt: new Date().toISOString(),
  };
  fs.writeFileSync(path.join(outDir, `sub2api-opus47-${mode}-10x10-results.json`), JSON.stringify({ summary, results: all }, null, 2));
  fs.writeFileSync(path.join(outDir, `sub2api-opus47-${mode}-10x10-summary.json`), JSON.stringify(summary, null, 2));
  if (!summary.pass) {
    process.exitCode = 1;
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
