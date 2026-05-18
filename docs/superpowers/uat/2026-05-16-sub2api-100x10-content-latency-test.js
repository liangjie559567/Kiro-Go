const fs = require('fs');
const path = require('path');

const base = 'http://127.0.0.1:18080';
const mode = process.argv[2] || 'sync';
const total = Number(process.argv[3] || 100);
const concurrency = Number(process.argv[4] || 10);
const model = process.argv[5] || 'claude-sonnet-4.5';
const runId = process.argv[6] || `content-latency-${mode}-${Date.now()}`;
const taskMode = process.argv[7] || 'marker';
const outDir = '/www/Kiro-Go/docs/superpowers/uat/sub2api-100x10-2026-05-16';
const apiKey = fs.readFileSync('/tmp/sub2api_claude_key', 'utf8').trim();
const warningRe = /伪造|system prompt|Kiro 的方式|--- SYSTEM PROMPT ---|x-anthropic-billing-header|Claude Code/i;

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
  let eventCount = 0;
  let firstByteMs = null;
  for (const line of raw.split(/\r?\n/)) {
    if (!line.startsWith('data: ')) continue;
    eventCount++;
    const data = line.slice(6).trim();
    if (!data || data === '[DONE]') continue;
    try {
      const obj = JSON.parse(data);
      if (obj.type === 'message_start') hasMessageStart = true;
      if (obj.type === 'message_stop') hasMessageStop = true;
      if (obj.type === 'content_block_delta' && obj.delta && typeof obj.delta.text === 'string') {
        chunks.push(obj.delta.text);
      }
      if (obj.type === 'error') chunks.push(JSON.stringify(obj.error || obj));
    } catch {}
  }
  return {text: chunks.join(''), hasMessageStart, hasMessageStop, eventCount, firstByteMs};
}

async function one(index, jsonl) {
  const marker = `${runId}-${String(index).padStart(3, '0')}`;
  const expected = taskMode === 'math' ? String(index + 1000) : marker;
  const started = Date.now();
  const request = {
    model,
    max_tokens: 48,
    stream: mode === 'stream',
    messages: [{
      role: 'user',
      content: taskMode === 'math'
        ? `Compute ${index} + 1000. Reply with only the decimal integer result and no other text.`
        : `Return exactly this marker and nothing else: ${marker}`,
    }],
  };
  const result = {runId, mode, taskMode, index, marker, expected, startedAt: new Date(started).toISOString()};
  try {
    const res = await fetch(`${base}/v1/messages`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${apiKey}`,
        'Content-Type': 'application/json',
        'anthropic-version': '2023-06-01',
        'X-Claude-Code-Session-Id': runId,
        'X-Claude-Code-Agent-Id': `agent-${mode}`,
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
      result.text = parsed.text;
      result.hasMessageStart = parsed.hasMessageStart;
      result.hasMessageStop = parsed.hasMessageStop;
      result.eventCount = parsed.eventCount;
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
    result.correct = res.ok && result.text.trim() === expected;
    result.containsMarker = res.ok && result.text.includes(expected);
    result.warning = warningRe.test(result.text || '');
    if (!result.correct || result.warning || !res.ok) {
      result.rawPreview = raw.slice(0, 1000);
      result.textPreview = (result.text || '').slice(0, 1000);
    }
  } catch (err) {
    result.status = 0;
    result.ok = false;
    result.correct = false;
    result.containsMarker = false;
    result.warning = false;
    result.durationMs = Date.now() - started;
    result.error = String(err && err.stack || err);
  }
  fs.appendFileSync(jsonl, JSON.stringify(result) + '\n');
  console.log(JSON.stringify({
    mode,
    taskMode,
    index,
    status: result.status,
    durationMs: result.durationMs,
    correct: result.correct,
    containsMarker: result.containsMarker,
    warning: result.warning,
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
  const failures = results.filter((r) => !r.ok || !r.correct || r.warning || (mode === 'stream' && !r.hasMessageStop));
  const summary = {
    runId,
    base,
    mode,
    taskMode,
    model,
    total,
    concurrency,
    success: results.filter((r) => r.ok).length,
    correct: results.filter((r) => r.correct).length,
    containsMarker: results.filter((r) => r.containsMarker).length,
    warnings: results.filter((r) => r.warning).length,
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
      expected: r.expected,
      status: r.status,
      durationMs: r.durationMs,
      correct: r.correct,
      containsMarker: r.containsMarker,
      warning: r.warning,
      hasMessageStop: r.hasMessageStop,
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
