const fs = require('fs');
const path = require('path');

const base = process.env.SUB2API_BASE || 'http://127.0.0.1:18080';
const model = process.env.SUB2API_MODEL || 'claude-sonnet-4.5';
const keyPath = process.env.SUB2API_KEY_FILE || '/tmp/sub2api_claude_key';
const outDir = process.env.SUB2API_SMOKE_OUT || '/www/Kiro-Go/docs/superpowers/uat/sub2api-smoke';
const runId = process.env.SUB2API_SMOKE_RUN_ID || `sub2api-smoke-${Date.now()}`;
const apiKey = fs.readFileSync(keyPath, 'utf8').trim();

fs.mkdirSync(outDir, {recursive: true});

function requestHeaders(endpoint) {
  return {
    Authorization: `Bearer ${apiKey}`,
    'Content-Type': 'application/json',
    'anthropic-version': '2023-06-01',
    'x-request-id': `${runId}-${endpoint.replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '')}`,
    'x-claude-code-session-id': runId,
  };
}

function extractClaudeText(body) {
  if (!body || typeof body !== 'object' || !Array.isArray(body.content)) return '';
  return body.content.map((block) => {
    if (!block || typeof block !== 'object') return '';
    if (block.type === 'text' && typeof block.text === 'string') return block.text;
    if (typeof block.text === 'string') return block.text;
    return '';
  }).join('');
}

function parseSSE(raw) {
  const events = [];
  const text = [];
  let currentEvent = '';
  let dataLines = [];

  function flush() {
    if (!currentEvent && dataLines.length === 0) return;
    const data = dataLines.join('\n').trim();
    const event = currentEvent || '';
    if (event) events.push(event);
    currentEvent = '';
    dataLines = [];

    if (!data || data === '[DONE]') return;
    try {
      const parsed = JSON.parse(data);
      if (parsed && parsed.type && !event) events.push(parsed.type);
      if (parsed.type === 'content_block_delta' && parsed.delta && typeof parsed.delta.text === 'string') {
        text.push(parsed.delta.text);
      }
      if (parsed.type === 'error') {
        text.push(JSON.stringify(parsed.error || parsed));
      }
    } catch (err) {
      events.push('parse_error');
    }
  }

  for (const line of raw.split(/\r?\n/)) {
    if (line === '') {
      flush();
      continue;
    }
    if (line.startsWith(':')) continue;
    if (line.startsWith('event:')) {
      currentEvent = line.slice(6).trim();
      continue;
    }
    if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trimStart());
    }
  }
  flush();

  return {
    events,
    text: text.join(''),
    hasMessageStart: events.includes('message_start'),
    hasMessageStop: events.includes('message_stop'),
  };
}

async function requestJSON(endpoint, payload) {
  const started = Date.now();
  const res = await fetch(`${base}${endpoint}`, {
    method: 'POST',
    headers: requestHeaders(endpoint),
    body: JSON.stringify(payload),
  });
  const raw = await res.text();
  return {
    status: res.status,
    ok: res.ok,
    durationMs: Date.now() - started,
    raw,
  };
}

function parseJSON(raw) {
  try {
    return JSON.parse(raw);
  } catch (err) {
    return null;
  }
}

async function main() {
  const marker = `${runId}-marker`;
  const messages = [{role: 'user', content: `Return exactly this marker and nothing else: ${marker}`}];
  const results = {
    runId,
    base,
    model,
    startedAt: new Date().toISOString(),
  };

  const modelsStarted = Date.now();
  const modelsRes = await fetch(`${base}/v1/models`, {
    method: 'GET',
    headers: requestHeaders('/v1/models'),
  });
  const modelsRaw = await modelsRes.text();
  const modelsBody = parseJSON(modelsRaw);
  results.models = {
    status: modelsRes.status,
    ok: modelsRes.ok,
    durationMs: Date.now() - modelsStarted,
    modelCount: modelsBody && Array.isArray(modelsBody.data) ? modelsBody.data.length : undefined,
    rawPreview: modelsRes.ok ? undefined : modelsRaw.slice(0, 1000),
  };

  const count = await requestJSON('/v1/messages/count_tokens', {
    model,
    messages,
  });
  const countBody = parseJSON(count.raw);
  results.countTokens = {
    status: count.status,
    ok: count.ok,
    durationMs: count.durationMs,
    body: countBody,
    positiveInputTokens: Boolean(countBody && Number(countBody.input_tokens) > 0),
    rawPreview: count.ok ? undefined : count.raw.slice(0, 1000),
  };

  const sync = await requestJSON('/v1/messages', {
    model,
    max_tokens: 64,
    stream: false,
    messages,
  });
  const syncBody = parseJSON(sync.raw);
  const syncText = extractClaudeText(syncBody).trim();
  results.sync = {
    status: sync.status,
    ok: sync.ok,
    durationMs: sync.durationMs,
    text: syncText,
    correct: sync.ok && syncText === marker,
    stopReason: syncBody && syncBody.stop_reason,
    usage: syncBody && syncBody.usage,
    rawPreview: sync.ok && syncText === marker ? undefined : sync.raw.slice(0, 1000),
  };

  const stream = await requestJSON('/v1/messages', {
    model,
    max_tokens: 64,
    stream: true,
    messages,
  });
  const parsedStream = parseSSE(stream.raw);
  const streamText = parsedStream.text.trim();
  results.stream = {
    status: stream.status,
    ok: stream.ok,
    durationMs: stream.durationMs,
    text: streamText,
    correct: stream.ok && streamText === marker,
    hasMessageStart: parsedStream.hasMessageStart,
    hasMessageStop: parsedStream.hasMessageStop,
    eventCount: parsedStream.events.length,
    events: parsedStream.events,
    rawPreview: stream.ok && streamText === marker && parsedStream.hasMessageStop ? undefined : stream.raw.slice(0, 1000),
  };

  results.finishedAt = new Date().toISOString();
  results.passed = Boolean(
    results.models.ok &&
    results.countTokens.ok &&
    results.countTokens.positiveInputTokens &&
    results.sync.correct &&
    results.stream.correct &&
    results.stream.hasMessageStop
  );

  const out = path.join(outDir, `${runId}.json`);
  fs.writeFileSync(out, JSON.stringify(results, null, 2));
  console.log(JSON.stringify({out, passed: results.passed, results}, null, 2));

  if (!results.passed) process.exit(1);
}

main().catch((err) => {
  console.error(err && err.stack || err);
  process.exit(1);
});
