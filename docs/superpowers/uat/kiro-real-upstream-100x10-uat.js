const fs = require('fs');
const path = require('path');

const base = process.argv[2] || 'http://127.0.0.1:8080';
const keyFile = process.argv[3] || '/tmp/kiro_go_api_key';
const mode = process.argv[4] || 'sync';
const total = Number(process.argv[5] || 100);
const concurrency = Number(process.argv[6] || 10);
const model = process.argv[7] || 'claude-sonnet-4.5';
const runId = process.argv[8] || `kiro-real-${mode}-${Date.now()}`;
const outDir = process.argv[9] || '/www/Kiro-Go/docs/superpowers/uat/kiro-real-upstream-20260519';
const apiKey = fs.readFileSync(keyFile, 'utf8').trim();

fs.mkdirSync(outDir, {recursive: true});

function percentile(values, p) {
  if (!values.length) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const idx = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.max(0, Math.min(sorted.length - 1, idx))];
}

function extractClaudeText(body) {
  if (!body || typeof body !== 'object' || !Array.isArray(body.content)) return '';
  return body.content.map((block) => {
    if (!block) return '';
    if (block.type === 'text' && typeof block.text === 'string') return block.text;
    if (typeof block.text === 'string') return block.text;
    return '';
  }).join('');
}

function parseSSE(raw) {
  const chunks = [];
  let hasMessageStart = false;
  let hasMessageStop = false;
  let hasError = false;
  let eventCount = 0;
  for (const line of raw.split(/\r?\n/)) {
    if (!line.startsWith('data: ')) continue;
    eventCount++;
    const data = line.slice(6).trim();
    if (!data || data === '[DONE]') continue;
    try {
      const obj = JSON.parse(data);
      if (obj.type === 'message_start') hasMessageStart = true;
      if (obj.type === 'message_stop') hasMessageStop = true;
      if (obj.type === 'error') {
        hasError = true;
        chunks.push(JSON.stringify(obj.error || obj));
      }
      if (obj.type === 'content_block_delta' && obj.delta && typeof obj.delta.text === 'string') {
        chunks.push(obj.delta.text);
      }
    } catch {}
  }
  return {text: chunks.join(''), hasMessageStart, hasMessageStop, hasError, eventCount};
}

async function one(index, jsonl) {
  const marker = `${runId}-${String(index).padStart(3, '0')}`;
  const started = Date.now();
  const request = {
    model,
    max_tokens: 64,
    stream: mode === 'stream',
    messages: [{
      role: 'user',
      content: `Return exactly this marker and nothing else: ${marker}`,
    }],
  };
  const result = {runId, base, mode, index, marker, startedAt: new Date(started).toISOString()};
  try {
    const res = await fetch(`${base}/v1/messages`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${apiKey}`,
        'Content-Type': 'application/json',
        'anthropic-version': '2023-06-01',
        'X-Claude-Code-Session-Id': runId,
        'X-Claude-Code-Agent-Id': `uat-${mode}`,
      },
      body: JSON.stringify(request),
    });
    const raw = await res.text();
    result.status = res.status;
    result.ok = res.ok;
    result.durationMs = Date.now() - started;
    result.responseBytes = raw.length;
    if (mode === 'stream') {
      const parsed = parseSSE(raw);
      Object.assign(result, parsed);
    } else {
      try {
        const body = JSON.parse(raw);
        result.text = extractClaudeText(body);
        result.stopReason = body.stop_reason;
        result.usage = body.usage;
      } catch {
        result.text = raw;
      }
    }
    result.exact = res.ok && (result.text || '').trim() === marker;
    result.containsMarker = res.ok && (result.text || '').includes(marker);
    result.correct = result.exact;
    if (!result.correct || !res.ok || result.hasError || (mode === 'stream' && !result.hasMessageStop)) {
      result.textPreview = (result.text || '').slice(0, 1000);
      result.rawPreview = raw.slice(0, 1000);
    }
  } catch (err) {
    result.status = 0;
    result.ok = false;
    result.correct = false;
    result.containsMarker = false;
    result.durationMs = Date.now() - started;
    result.error = String(err && err.stack || err);
  }
  fs.appendFileSync(jsonl, JSON.stringify(result) + '\n');
  console.log(JSON.stringify({
    mode,
    index,
    status: result.status,
    durationMs: result.durationMs,
    correct: result.correct,
    containsMarker: result.containsMarker,
    hasMessageStop: result.hasMessageStop,
    error: result.error,
  }));
  return result;
}

async function main() {
  const jsonl = path.join(outDir, `${runId}.jsonl`);
  const summaryFile = path.join(outDir, `${runId}-summary.json`);
  fs.writeFileSync(jsonl, '');
  const results = new Array(total);
  let next = 1;
  async function worker() {
    for (;;) {
      const index = next++;
      if (index > total) return;
      results[index - 1] = await one(index, jsonl);
    }
  }
  await Promise.all(Array.from({length: concurrency}, worker));
  const durations = results.map((r) => r.durationMs).filter(Number.isFinite);
  const failures = results.filter((r) => !r.ok || !r.correct || r.hasError || (mode === 'stream' && !r.hasMessageStop));
  const summary = {
    runId,
    base,
    mode,
    model,
    total,
    concurrency,
    httpSuccess: results.filter((r) => r.ok).length,
    exactCorrect: results.filter((r) => r.correct).length,
    containsMarker: results.filter((r) => r.containsMarker).length,
    failed: failures.length,
    statusCounts: results.reduce((acc, r) => {
      acc[r.status] = (acc[r.status] || 0) + 1;
      return acc;
    }, {}),
    latencyMs: {
      min: Math.min(...durations),
      max: Math.max(...durations),
      avg: Math.round(durations.reduce((a, b) => a + b, 0) / Math.max(1, durations.length)),
      p50: percentile(durations, 50),
      p90: percentile(durations, 90),
      p95: percentile(durations, 95),
      p99: percentile(durations, 99),
    },
    failures: failures.map((r) => ({
      index: r.index,
      marker: r.marker,
      status: r.status,
      durationMs: r.durationMs,
      correct: r.correct,
      containsMarker: r.containsMarker,
      hasMessageStop: r.hasMessageStop,
      hasError: r.hasError,
      error: r.error,
      textPreview: r.textPreview,
      rawPreview: r.rawPreview,
    })),
    jsonl,
  };
  fs.writeFileSync(summaryFile, JSON.stringify(summary, null, 2));
  console.error(JSON.stringify({summaryFile, summary}, null, 2));
  if (summary.failed > 0) process.exitCode = 1;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
