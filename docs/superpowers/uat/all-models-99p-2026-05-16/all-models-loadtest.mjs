import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createWriteStream } from 'node:fs';
import { setTimeout as sleep } from 'node:timers/promises';

const OUT_DIR = new URL('./', import.meta.url).pathname;
const SUB2API_URL = process.env.SUB2API_URL || 'http://127.0.0.1:18080';
const KIRO_URL = process.env.KIRO_URL || 'http://127.0.0.1:8080';
const SUB2API_KEY_FILE = process.env.SUB2API_KEY_FILE || '/tmp/sub2api_claude_key.txt';
const RUN_ID = process.env.RUN_ID || new Date().toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
const REQUESTS = Number(process.env.REQUESTS || 100);
const CONCURRENCY = Number(process.env.CONCURRENCY || 10);
const MODE = process.argv[2] || 'precheck';

function percentile(values, p) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const index = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[index];
}

function mean(values) {
  if (!values.length) return null;
  return Math.round(values.reduce((a, b) => a + b, 0) / values.length);
}

function modelToken(model) {
  return model.toUpperCase().replace(/[^A-Z0-9]+/g, '').slice(0, 24) || 'MODEL';
}

function modelBase(model) {
  return [...model].reduce((sum, char) => sum + char.charCodeAt(0), 0) % 500;
}

async function loadKey() {
  return (await readFile(SUB2API_KEY_FILE, 'utf8')).trim();
}

async function loadModels(key) {
  const headers = { authorization: `Bearer ${key}`, 'x-api-key': key };
  let models = [];
  try {
    const res = await fetch(`${KIRO_URL}/v1/models`, { headers });
    const json = await res.json();
    models = (json.data || []).map((m) => m.id).filter(Boolean);
  } catch {
    const res = await fetch(`${SUB2API_URL}/v1/models`, { headers });
    const json = await res.json();
    models = (json.data || []).map((m) => m.id).filter(Boolean);
  }
  const withLegacy = [...models, 'claude-opus-4-7'];
  return [...new Set(withLegacy)].sort();
}

function requestBody(model, stream, expected, left, right) {
  const body = {
    model,
    max_tokens: 64,
    stream,
    messages: [
      {
        role: 'user',
        content: `Compute ${left} + ${right}. Reply with only the integer result, no explanation.`,
      },
    ],
  };
  if (model.endsWith('-thinking')) {
    body.thinking = { type: 'enabled', budget_tokens: 1024 };
    body.max_tokens = 1200;
  }
  return body;
}

function extractTextFromMessage(json) {
  if (!json || !Array.isArray(json.content)) return '';
  return json.content
    .filter((block) => block && block.type === 'text')
    .map((block) => block.text || '')
    .join('');
}

function parseSseText(raw) {
  const events = [];
  let text = '';
  let thinking = '';
  for (const line of raw.split(/\r?\n/)) {
    if (!line.startsWith('data:')) continue;
    const data = line.slice(5).trimStart();
    if (!data || data === '[DONE]') continue;
    try {
      const obj = JSON.parse(data);
      events.push(obj.type || 'unknown');
      if (obj.type === 'content_block_delta' && obj.delta) {
        if (typeof obj.delta.text === 'string') text += obj.delta.text;
        if (typeof obj.delta.thinking === 'string') thinking += obj.delta.thinking;
      }
      if (obj.type === 'content_block_start' && obj.content_block?.type === 'text') {
        text += obj.content_block.text || '';
      }
    } catch {
      events.push('parse_error');
    }
  }
  return { events, text, thinking };
}

function classify({ status, json, raw, text, expected, stream, error }) {
  const normalized = text.trim().replace(/[,.，。]+$/g, '').trim();
  const hasMarker = normalized === expected || new RegExp(`(^|[^0-9-])${expected}([^0-9]|$)`).test(text);
  const shapeOk = stream
    ? raw.includes('message_start') && raw.includes('message_stop')
    : json?.type === 'message' && Array.isArray(json.content);
  return {
    ok: status === 200 && !error && shapeOk && hasMarker,
    has_marker: hasMarker,
    shape_ok: shapeOk,
  };
}

async function callOne({ key, model, stream, index }) {
  const left = 200 + modelBase(model) + index;
  const right = stream ? 37 : 19;
  const marker = String(left + right);
  const started = Date.now();
  let firstByteMs = null;
  let status = 0;
  let raw = '';
  let json = null;
  let text = '';
  let thinking = '';
  let events = [];
  let error = null;
  try {
    const res = await fetch(`${SUB2API_URL}/v1/messages`, {
      method: 'POST',
      headers: {
        authorization: `Bearer ${key}`,
        'x-api-key': key,
        'anthropic-version': '2023-06-01',
        'content-type': 'application/json',
      },
      body: JSON.stringify(requestBody(model, stream, marker, left, right)),
    });
    status = res.status;
    if (stream) {
      const reader = res.body.getReader();
      const chunks = [];
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        if (firstByteMs === null) firstByteMs = Date.now() - started;
        chunks.push(Buffer.from(value));
      }
      raw = Buffer.concat(chunks).toString('utf8');
      const parsed = parseSseText(raw);
      events = parsed.events;
      text = parsed.text;
      thinking = parsed.thinking;
    } else {
      raw = await res.text();
      try {
        json = JSON.parse(raw);
        text = extractTextFromMessage(json);
        if (json?.error) error = json.error;
      } catch (e) {
        error = { message: e.message, type: 'json_parse_error' };
      }
    }
  } catch (e) {
    error = { message: e.message, type: e.name || 'fetch_error' };
  }
  const latencyMs = Date.now() - started;
  const verdict = classify({ status, json, raw, text, expected: marker, stream, error });
  return {
    run_id: RUN_ID,
    model,
    mode: stream ? 'stream' : 'nonstream',
    index,
    status,
    latency_ms: latencyMs,
    first_byte_ms: firstByteMs,
    events_count: events.length,
    ok: verdict.ok,
    has_marker: verdict.has_marker,
    shape_ok: verdict.shape_ok,
    marker,
    expected: marker,
    text,
    thinking_chars: thinking.length,
    error,
  };
}

async function runQueue(tasks, limit, onResult) {
  let next = 0;
  const workers = Array.from({ length: limit }, async () => {
    while (next < tasks.length) {
      const task = tasks[next++];
      const result = await task();
      await onResult(result);
      await sleep(25);
    }
  });
  await Promise.all(workers);
}

function summarize(results) {
  const groups = new Map();
  for (const row of results) {
    const key = `${row.model}::${row.mode}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(row);
  }
  const rows = [];
  for (const [key, values] of groups) {
    const [model, mode] = key.split('::');
    const latencies = values.filter((v) => v.status === 200).map((v) => v.latency_ms);
    const firstBytes = values.filter((v) => v.first_byte_ms !== null).map((v) => v.first_byte_ms);
    const ok = values.filter((v) => v.ok).length;
    const http2xx = values.filter((v) => v.status >= 200 && v.status < 300).length;
    rows.push({
      model,
      mode,
      total: values.length,
      ok,
      ok_rate: ok / values.length,
      http_2xx: http2xx,
      http_2xx_rate: http2xx / values.length,
      latency_ms_avg: mean(latencies),
      latency_ms_p50: percentile(latencies, 50),
      latency_ms_p95: percentile(latencies, 95),
      latency_ms_p99: percentile(latencies, 99),
      first_byte_ms_avg: mean(firstBytes),
      first_byte_ms_p95: percentile(firstBytes, 95),
      failures: values.filter((v) => !v.ok).slice(0, 5).map((v) => ({
        index: v.index,
        status: v.status,
        has_marker: v.has_marker,
        shape_ok: v.shape_ok,
        text: v.text.slice(0, 240),
        error: v.error,
      })),
    });
  }
  rows.sort((a, b) => a.model.localeCompare(b.model) || a.mode.localeCompare(b.mode));
  return rows;
}

async function main() {
  await mkdir(OUT_DIR, { recursive: true });
  const key = await loadKey();
  const models = await loadModels(key);
  const iterations = MODE === 'precheck' ? 1 : REQUESTS;
  const tasks = [];
  for (const model of models) {
    for (const stream of [false, true]) {
      for (let i = 1; i <= iterations; i += 1) {
        tasks.push(() => callOne({ key, model, stream, index: i }));
      }
    }
  }

  const results = [];
  const jsonlPath = `${OUT_DIR}/${MODE}-${RUN_ID}.jsonl`;
  const jsonl = createWriteStream(jsonlPath, { flags: 'w' });
  let completed = 0;
  await runQueue(tasks, Math.min(CONCURRENCY, tasks.length), async (result) => {
    results.push(result);
    jsonl.write(`${JSON.stringify(result)}\n`);
    completed += 1;
    if (completed % 20 === 0 || completed === tasks.length) {
      console.log(`${MODE} progress ${completed}/${tasks.length}`);
    }
  });
  await new Promise((resolve) => jsonl.end(resolve));

  const summary = {
    run_id: RUN_ID,
    mode: MODE,
    created_at: new Date().toISOString(),
    endpoint: `${SUB2API_URL}/v1/messages`,
    concurrency: Math.min(CONCURRENCY, tasks.length),
    requests_per_model_mode: iterations,
    models,
    total_requests: results.length,
    total_ok: results.filter((r) => r.ok).length,
    total_ok_rate: results.filter((r) => r.ok).length / results.length,
    by_model_mode: summarize(results),
    artifacts: { jsonl: jsonlPath },
  };
  await writeFile(`${OUT_DIR}/${MODE}-summary-${RUN_ID}.json`, `${JSON.stringify(summary, null, 2)}\n`);
  if (MODE === 'precheck') {
    await writeFile(`${OUT_DIR}/precheck-matrix-latest.json`, `${JSON.stringify(summary, null, 2)}\n`);
    const callable = summary.by_model_mode
      .filter((r) => r.ok_rate === 1)
      .reduce((acc, row) => {
        acc[row.model] ||= {};
        acc[row.model][row.mode] = true;
        return acc;
      }, {});
    const both = Object.entries(callable).filter(([, modes]) => modes.nonstream && modes.stream).map(([model]) => model).sort();
    await writeFile(`${OUT_DIR}/callable-models-latest.json`, `${JSON.stringify({ run_id: RUN_ID, callable_both_modes: both, callable }, null, 2)}\n`);
  }
  console.log(JSON.stringify({
    run_id: summary.run_id,
    mode: summary.mode,
    total_requests: summary.total_requests,
    total_ok: summary.total_ok,
    total_ok_rate: summary.total_ok_rate,
    summary_file: `${OUT_DIR}/${MODE}-summary-${RUN_ID}.json`,
    jsonl: jsonlPath,
  }, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
