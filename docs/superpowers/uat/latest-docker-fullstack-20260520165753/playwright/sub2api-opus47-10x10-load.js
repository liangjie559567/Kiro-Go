#!/usr/bin/env node
'use strict';

const cp = require('child_process');
const fs = require('fs');
const path = require('path');

const outDir = path.resolve(__dirname, '..', 'api');
const url = process.env.SUB2API_URL || 'http://127.0.0.1:18080/v1/messages';
const mode = process.env.LOAD_MODE || 'nonstream';
const rounds = Number(process.env.ROUNDS || 10);
const concurrency = Number(process.env.CONCURRENCY || 10);
const timeoutMs = Number(process.env.TIMEOUT_MS || 150000);

function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

function sub2apiKey() {
  if (process.env.SUB2API_KEY) return process.env.SUB2API_KEY;
  return sh("docker exec sub2api sh -lc 'PGPASSWORD=\"$DATABASE_PASSWORD\" psql -h \"$DATABASE_HOST\" -U \"$DATABASE_USER\" -d \"$DATABASE_DBNAME\" -Atc \"select key from api_keys where lower(status) = '\\''active'\\'' order by id limit 1\"'");
}

function textFromMessage(json) {
  if (!json || !Array.isArray(json.content)) return '';
  return json.content.map((part) => part && part.text ? part.text : '').join('');
}

function textFromSse(text) {
  const chunks = [];
  for (const line of text.split(/\r?\n/)) {
    if (!line.startsWith('data: ')) continue;
    const payload = line.slice(6).trim();
    if (!payload || payload === '[DONE]') continue;
    try {
      const event = JSON.parse(payload);
      if (event.type === 'content_block_delta' && event.delta && event.delta.text) {
        chunks.push(event.delta.text);
      }
    } catch (_) {
      // Ignore non-JSON SSE lines.
    }
  }
  return chunks.join('');
}

async function runOne(apiKey, round, slot) {
  const stream = mode === 'stream';
  const requestId = `latest-uat-opus47-${mode}-r${round}-s${slot}-${Date.now()}`;
  const expected = `LATEST_UAT_${mode.toUpperCase()}_${round}_${slot}`;
  const body = {
    model: 'claude-opus-4-7',
    max_tokens: 80,
    stream,
    messages: [
      {
        role: 'user',
        content: `Return one compact JSON object only. The object must have exactly one field named uat with this exact string value: ${expected}`,
      },
    ],
  };
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error('timeout')), timeoutMs);
  const started = Date.now();
  try {
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'anthropic-version': '2023-06-01',
        authorization: `Bearer ${apiKey}`,
        'x-claude-code-session-id': requestId,
        'x-client-request-id': requestId,
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
    const raw = await resp.text();
    const durationMs = Date.now() - started;
    let text = '';
    if (stream) {
      text = textFromSse(raw);
    } else {
      try {
        text = textFromMessage(JSON.parse(raw));
      } catch (_) {
        text = raw;
      }
    }
    let parsed = '';
    const match = text.match(/\{[^{}]*"uat"[^{}]*\}/);
    if (match) {
      try {
        parsed = JSON.parse(match[0]).uat || '';
      } catch (_) {
        parsed = '';
      }
    }
    const ok = resp.status === 200 && parsed === expected;
    return {
      round,
      slot,
      requestId,
      status: resp.status,
      ok,
      durationMs,
      expected,
      parsed,
      retryAfter: resp.headers.get('retry-after') || '',
      kiroReason: resp.headers.get('x-kiro-go-error-reason') || '',
      bodyPrefix: ok ? undefined : raw.slice(0, 500),
    };
  } catch (error) {
    return {
      round,
      slot,
      requestId,
      status: 0,
      ok: false,
      durationMs: Date.now() - started,
      expected,
      parsed: '',
      error: error && error.message ? error.message : String(error),
    };
  } finally {
    clearTimeout(timer);
  }
}

async function main() {
  fs.mkdirSync(outDir, { recursive: true });
  const apiKey = sub2apiKey();
  if (!apiKey) throw new Error('No enabled sub2api API key found.');

  const all = [];
  for (let round = 1; round <= rounds; round += 1) {
    const batch = [];
    for (let slot = 1; slot <= concurrency; slot += 1) {
      batch.push(runOne(apiKey, round, slot));
    }
    const results = await Promise.all(batch);
    all.push(...results);
    const pass = results.filter((row) => row.ok).length;
    const max = Math.max(...results.map((row) => row.durationMs));
    console.log(`${mode} round=${round} pass=${pass}/${results.length} max_ms=${max}`);
  }

  const durations = all.map((row) => row.durationMs).sort((a, b) => a - b);
  const failures = all.filter((row) => !row.ok);
  const statusCounts = {};
  for (const row of all) statusCounts[row.status] = (statusCounts[row.status] || 0) + 1;
  const summary = {
    mode,
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
    completedAt: new Date().toISOString(),
  };
  fs.writeFileSync(path.join(outDir, `sub2api-opus47-${mode}-10x10-results.json`), JSON.stringify({ summary, results: all }, null, 2) + '\n');
  fs.writeFileSync(path.join(outDir, `sub2api-opus47-${mode}-10x10-summary.json`), JSON.stringify(summary, null, 2) + '\n');
  if (!summary.pass) process.exitCode = 1;
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});
