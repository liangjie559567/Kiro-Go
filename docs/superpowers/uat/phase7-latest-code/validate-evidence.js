#!/usr/bin/env node

const fs = require("fs");
const path = require("path");

const manifestPath = process.argv[2];
if (!manifestPath) {
  console.error("usage: node validate-evidence.js <evidence-manifest.json>");
  process.exit(2);
}

const root = path.dirname(manifestPath);
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const failures = [];

function requireField(obj, field) {
  if (!Object.prototype.hasOwnProperty.call(obj, field)) {
    failures.push(`missing field: ${field}`);
  }
}

function requireArtifact(name) {
  const value = manifest.artifacts && manifest.artifacts[name];
  if (!value) {
    failures.push(`missing artifact reference: ${name}`);
    return;
  }
  const values = Array.isArray(value) ? value : [value];
  for (const item of values) {
    const target = path.resolve(root, item);
    if (!fs.existsSync(target)) {
      failures.push(`artifact not found: ${name} -> ${item}`);
    }
  }
}

for (const field of ["contractVersion", "verdict", "readiness", "nonStream", "stream", "blockedCapacity", "artifacts", "redaction"]) {
  requireField(manifest, field);
}

if (manifest.contractVersion !== "phase7-evidence.1") {
  failures.push(`contractVersion must be phase7-evidence.1, got ${manifest.contractVersion}`);
}

const readiness = manifest.readiness || {};
const viable = (readiness.status === "healthy" || readiness.status === "degraded") && Number(readiness.safeConcurrency) > 0;
const blocked = readiness.status === "blocked" || Number(readiness.safeConcurrency) === 0;

const nonStream = manifest.nonStream || {};
const stream = manifest.stream || {};
const blockedCapacity = manifest.blockedCapacity || {};

if (manifest.verdict === "PASS") {
  if (!viable) failures.push("PASS requires healthy/degraded readiness with safeConcurrency > 0");
  for (const [name, run] of [["nonStream", nonStream], ["stream", stream]]) {
    if (run.attempted !== 100) failures.push(`${name} attempted must be 100`);
    if (run.httpSuccesses !== 100) failures.push(`${name} httpSuccesses must be 100`);
    if (run.realContentSuccesses !== 100) failures.push(`${name} realContentSuccesses must be 100`);
    if (run.stableFallbackSuccesses !== 0) failures.push(`${name} stableFallbackSuccesses must be 0`);
  }
  if (Number(stream.replayAfterContentViolations || 0) !== 0) {
    failures.push("stream replayAfterContentViolations must be 0");
  }
}

if (blocked && manifest.verdict === "PASS") {
  failures.push("blocked readiness cannot produce full generation PASS");
}

if (manifest.verdict === "BLOCKED_CAPACITY_PASS") {
  if (!blocked) failures.push("BLOCKED_CAPACITY_PASS requires blocked readiness or safeConcurrency=0");
  if (!blockedCapacity.verified) failures.push("blockedCapacity.verified must be true");
  if (!blockedCapacity.retryAfterPresent) failures.push("blockedCapacity.retryAfterPresent must be true");
  if (!blockedCapacity.noPermanentAccountPoisoning) failures.push("blockedCapacity.noPermanentAccountPoisoning must be true");
}

if (!manifest.redaction || manifest.redaction.checked !== true || Number(manifest.redaction.leaksDetected) !== 0) {
  failures.push("redaction.checked must be true and leaksDetected must be 0");
}

for (const artifact of [
  "fleetReadiness",
  "acceptanceEvidence",
  "requestLogs",
  "sub2apiReadinessDecisions",
  "sub2apiUsageLogs",
  "sub2apiOpsErrors",
  "sub2apiAccountSchedulingState",
  "consoleSummary",
  "redactionReport"
]) {
  requireArtifact(artifact);
}

if (failures.length > 0) {
  console.error("Phase 7 evidence validation failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("Phase 7 evidence validation passed");
