# sub2api Opus 4.7 Stable 200 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Guarantee that the sub2api-facing Opus 4.7 generation path never receives Kiro-Go HTTP `429`, `502`, or `503`, while Kiro-Go internally absorbs upstream Kiro pressure through account failover, cooldowns, queues, and health governance.

**Architecture:** Add a narrow downstream compatibility layer for generation requests that can identify sub2api/Claude Code traffic, keep retryable Opus 4.7 failures inside Kiro-Go, and emit stable Anthropic/OpenAI-compatible `200` responses only at the downstream boundary. Preserve existing upstream error classification, account cooldowns, model breakers, and Opus admission gate as internal control signals.

**Tech Stack:** Go standard library, existing Kiro-Go `proxy`, `pool`, and `config` packages, current Admin UI/request-log infrastructure, Node/Playwright UAT scripts under `docs/superpowers/uat`.

---

## Context From Research

- `kiro-gateway` uses request-level `tried_accounts`, account failover for `402/403/429`, sticky success routing, exponential cooldown, and half-open recovery. Kiro-Go should borrow the account-health pattern, not its exact error surface.
- `KiroSwitchManager` publicly documents automatic account switching, stream heartbeat, quota/ban visibility, and message truncation protection. Kiro-Go should adopt the health-signal and stream-keepalive ideas without silently changing requested models.
- LiteLLM Router, Envoy, and Nginx all separate retry/fallback/circuit-breaking from downstream response semantics. Retries must have budgets; upstreams should be ejected temporarily; streaming failover is only safe before downstream bytes are sent.
- Local code analysis found Kiro-Go does not directly emit `502` on the generation path. sub2api-visible `502` is likely produced by sub2api or a reverse proxy after Kiro-Go returns `503`, closes early, or sends an invalid/empty response.

## Non-Negotiable Contract

- For sub2api-facing Opus 4.7 generation requests, Kiro-Go must not write HTTP `429`, `502`, or `503`.
- Upstream Kiro `429`, suspicious temporary limits, model capacity pressure, account cooldown, and admission pressure are internal scheduling events.
- When Kiro-Go cannot get a real Opus 4.7 upstream success within budget, it must still return a syntactically valid downstream `200` Anthropic/OpenAI-compatible response that tells Claude Code the service is internally waiting/degraded, rather than causing sub2api to mark Kiro-Go as a failed upstream.
- Non-generation admin/maintenance endpoints may keep their existing `429` behavior.
- Non-Opus models may keep standard rate-limit semantics unless the same sub2api stable mode is explicitly enabled for them later.

## File Structure

- Modify `config/config.go`: add explicit stable downstream compatibility config with safe defaults.
- Modify `proxy/handler.go`: add sub2api/stable-mode detection, stable error writers, and replace Opus 4.7 downstream `429/503` leaks on generation paths.
- Modify `proxy/claude_sse_writer.go`: ensure stable stream fallback can emit valid Anthropic SSE with HTTP `200` and no empty/invalid body.
- Modify `proxy/request_log.go`: record stable-mode fallback reason, suppressed downstream status, account switches, queue waits, and final downstream status.
- Modify `pool/account.go`: keep existing cooldown/breaker behavior, adding only helper observability if tests require it.
- Modify `proxy/handler_test.go`: add contract tests that simulate upstream 429/503/no-account/admission pressure and assert downstream status `200`.
- Modify `proxy/request_log_test.go`: cover stable-mode log fields.
- Add `docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js`: real local sub2api/Kiro-Go UAT proving downstream status-code contract.
- Add `docs/superpowers/uat/sub2api-opus47-stable-200/README.md`: describe required env vars and pass/fail evidence.
- Update `docs/kiro-ha-compatibility-matrix.md`: add the stable downstream status-code contract and UAT evidence requirement.

---

### Task 1: Add Stable Downstream Mode Configuration

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add tests near existing config default tests:

```go
func TestStableDownstreamDefaultsEnableSub2APIOpus47(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.StableDownstream.Enabled {
		t.Fatalf("StableDownstream.Enabled = false, want true")
	}
	if !cfg.StableDownstream.Sub2APICompatible {
		t.Fatalf("StableDownstream.Sub2APICompatible = false, want true")
	}
	if len(cfg.StableDownstream.Models) != 1 || cfg.StableDownstream.Models[0] != "claude-opus-4.7" {
		t.Fatalf("StableDownstream.Models = %#v, want claude-opus-4.7 only", cfg.StableDownstream.Models)
	}
}

func TestStableDownstreamSupportsOpus47OnlyByDefault(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.StableDownstream.SupportsModel("claude-opus-4.7") {
		t.Fatalf("expected stable downstream to support claude-opus-4.7")
	}
	if cfg.StableDownstream.SupportsModel("claude-sonnet-4.5") {
		t.Fatalf("did not expect stable downstream to support claude-sonnet-4.5 by default")
	}
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./config -run 'TestStableDownstream' -count=1 -v
```

Expected: FAIL because `StableDownstream` config does not exist.

- [ ] **Step 3: Implement config**

Add to `config.Config`:

```go
StableDownstream StableDownstreamConfig `json:"stableDownstream"`
```

Add:

```go
type StableDownstreamConfig struct {
	Enabled           bool     `json:"enabled"`
	Sub2APICompatible bool     `json:"sub2apiCompatible"`
	Models            []string `json:"models"`
}

func (c StableDownstreamConfig) SupportsModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if !c.Enabled || model == "" {
		return false
	}
	for _, candidate := range c.Models {
		if strings.ToLower(strings.TrimSpace(candidate)) == model {
			return true
		}
	}
	return false
}
```

In defaults:

```go
StableDownstream: StableDownstreamConfig{
	Enabled:           true,
	Sub2APICompatible: true,
	Models:            []string{"claude-opus-4.7"},
},
```

- [ ] **Step 4: Verify**

Run:

```bash
go test ./config -run 'TestStableDownstream' -count=1 -v
```

Expected: PASS.

---

### Task 2: Detect sub2api Stable Opus Generation Requests

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing detection tests**

Add:

```go
func TestStableDownstreamAppliesToOpus47Sub2APIClaudeRequests(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	if !stableDownstreamForRequest(r, "claude-opus-4.7", true) {
		t.Fatalf("expected stable downstream for sub2api Opus 4.7 Claude request")
	}
}

func TestStableDownstreamDoesNotApplyToNonOpusByDefault(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0")
	if stableDownstreamForRequest(r, "claude-sonnet-4.5", true) {
		t.Fatalf("did not expect stable downstream for sonnet by default")
	}
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamApplies|TestStableDownstreamDoesNotApply' -count=1 -v
```

Expected: FAIL because `stableDownstreamForRequest` does not exist.

- [ ] **Step 3: Implement detection**

Add in `proxy/handler.go` near request helpers:

```go
func stableDownstreamForRequest(r *http.Request, model string, generation bool) bool {
	if !generation || !isOpus47Model(model) {
		return false
	}
	cfg := config.Get()
	if cfg == nil || !cfg.StableDownstream.SupportsModel(model) || !cfg.StableDownstream.Sub2APICompatible {
		return false
	}
	if r == nil {
		return true
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	return strings.Contains(ua, "sub2api") ||
		strings.Contains(ua, "claude-cli") ||
		strings.TrimSpace(r.Header.Get("X-Sub2API-Request")) != ""
}
```

If tests cannot use global config safely, add a small test helper that resets config to defaults before assertions.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamApplies|TestStableDownstreamDoesNotApply' -count=1 -v
```

Expected: PASS.

---

### Task 3: Replace Opus 4.7 Downstream 429/503 With Stable 200 Fallbacks

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/claude_sse_writer.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing Claude non-stream contract tests**

Add tests that call `sendNoAvailableAccountsError` and pressure helpers through a request marked as sub2api/stable:

```go
func TestStableDownstreamClaudeNoAccountsReturnsHTTP200(t *testing.T) {
	h := NewHandler()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")

	h.sendStableClaudeFallback(w, r, "claude-opus-4.7", "no_available_accounts", errors.New("No available accounts"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"type":"error"`) {
		t.Fatalf("stable fallback must be a message response, not an HTTP error envelope: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "kiro_go_stable_fallback") {
		t.Fatalf("expected stable fallback marker in body: %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Write failing Claude stream contract test**

Add:

```go
func TestStableDownstreamClaudeStreamFallbackStartsHTTP200SSE(t *testing.T) {
	h := NewHandler()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")

	h.sendStableClaudeStreamFallback(w, r, "claude-opus-4.7", "admission_pressure", errors.New("circuit open"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	for _, forbidden := range []string{"HTTP 429", "HTTP 502", "HTTP 503"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("stable SSE leaked forbidden status marker %q: %s", forbidden, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("expected complete Anthropic SSE fallback, got: %s", w.Body.String())
	}
}
```

- [ ] **Step 3: Run failing tests**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamClaude.*Fallback' -count=1 -v
```

Expected: FAIL because stable fallback writers do not exist.

- [ ] **Step 4: Implement stable fallback writers**

Add in `proxy/handler.go`:

```go
func stableFallbackText(reason string, err error) string {
	msg := "kiro_go_stable_fallback: Opus 4.7 is temporarily waiting for healthy upstream capacity."
	if reason != "" {
		msg += " reason=" + reason + "."
	}
	if err != nil {
		msg += " upstream_status=internal_retryable."
	}
	return msg
}

func (h *Handler) sendStableClaudeFallback(w http.ResponseWriter, r *http.Request, model, reason string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Kiro-Go-Stable-Fallback", "true")
	w.Header().Set("X-Kiro-Go-Internal-Reason", reason)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            "msg_" + uuid.New().String(),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       []map[string]string{{"type": "text", "text": stableFallbackText(reason, err)}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]int{"input_tokens": 0, "output_tokens": 0},
	})
}

func (h *Handler) sendStableClaudeStreamFallback(w http.ResponseWriter, r *http.Request, model, reason string, err error) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Kiro-Go-Stable-Fallback", "true")
	w.Header().Set("X-Kiro-Go-Internal-Reason", reason)
	w.WriteHeader(http.StatusOK)
	sse := newClaudeSSEWriter(w, "msg_"+uuid.New().String(), model, buildClaudeUsageMap(0, 0, promptCacheUsage{}, false), 1024)
	sse.Start()
	sse.TextDelta(stableFallbackText(reason, err))
	sse.Stop(0, 0, "end_turn")
	if flusher != nil {
		flusher.Flush()
	}
}
```

If `claudeSSEWriter.Stop` signature differs, use the existing stop method exactly as implemented in `proxy/claude_sse_writer.go`.

- [ ] **Step 5: Wire stable fallback at final downstream error points**

In `handleClaudeWithAccountRetry`, before each final call to `sendClaudeOpusPressureError`, `sendNoAvailableAccountsError`, and final stream pre-start upstream error, check:

```go
if stableDownstreamForRequest(r, model, true) {
	if stream {
		h.sendStableClaudeStreamFallback(w, r, model, "attempt_budget_exhausted", lastErr)
	} else {
		h.sendStableClaudeFallback(w, r, model, "attempt_budget_exhausted", lastErr)
	}
	return
}
```

Use reason values matching the site: `attempt_budget_exhausted`, `capacity_recovery_timeout`, `no_available_accounts`, `upstream_retryable_exhausted`, `token_refresh_failed`, `admission_pressure`.

- [ ] **Step 6: Verify**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamClaude.*Fallback' -count=1 -v
```

Expected: PASS.

---

### Task 4: Stop Opus 4.7 429-to-503 Leaks on Stable Requests

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing status-mapping tests**

Add:

```go
func TestStableDownstreamSuppressesOpus47RateLimitStatus(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0")
	status, errType, stable := downstreamStatusForRetryExhaustion(r, "claude-opus-4.7", true, http.StatusTooManyRequests, "rate_limit_error")
	if !stable {
		t.Fatalf("expected stable mode")
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if errType != "stable_fallback" {
		t.Fatalf("errType = %q, want stable_fallback", errType)
	}
}

func TestNonStableOpus47RateLimitKeepsExisting503Mapping(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	status, errType, stable := downstreamStatusForRetryExhaustion(r, "claude-opus-4.7", true, http.StatusTooManyRequests, "rate_limit_error")
	if stable {
		t.Fatalf("did not expect stable mode")
	}
	if status != http.StatusServiceUnavailable || errType != "overloaded_error" {
		t.Fatalf("status/type = %d/%s, want 503/overloaded_error", status, errType)
	}
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamSuppresses|TestNonStableOpus47RateLimit' -count=1 -v
```

Expected: FAIL because helper does not exist.

- [ ] **Step 3: Implement helper and replace direct mapping call sites**

Add:

```go
func downstreamStatusForRetryExhaustion(r *http.Request, model string, claudeFormat bool, status int, errType string) (int, string, bool) {
	if stableDownstreamForRequest(r, model, true) && (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) {
		return http.StatusOK, "stable_fallback", true
	}
	status, errType = downstreamOpus47ErrorStatusAndType(model, claudeFormat, status, errType)
	return status, errType, false
}
```

Update final error sites in Claude/OpenAI generation loops to use this helper. If `stable == true`, call the relevant stable fallback writer instead of `sendClaudeUpstreamError`, `sendClaudeOpusPressureError`, `sendOpenAIError`, or `sendOpenAIOpusPressureError`.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamSuppresses|TestNonStableOpus47RateLimit' -count=1 -v
```

Expected: PASS.

---

### Task 5: Add OpenAI-Compatible Stable 200 Fallbacks

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing OpenAI fallback tests**

Add:

```go
func TestStableDownstreamOpenAINoAccountsReturnsHTTP200(t *testing.T) {
	h := NewHandler()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("User-Agent", "sub2api/1.0")

	h.sendStableOpenAIFallback(w, r, "claude-opus-4.7", "no_available_accounts", errors.New("No available accounts"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"choices"`) {
		t.Fatalf("expected OpenAI choices response, got %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("stable fallback must not use OpenAI error envelope: %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Implement OpenAI fallback writer**

Add:

```go
func (h *Handler) sendStableOpenAIFallback(w http.ResponseWriter, r *http.Request, model, reason string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Kiro-Go-Stable-Fallback", "true")
	w.Header().Set("X-Kiro-Go-Internal-Reason", reason)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      "chatcmpl-" + uuid.New().String(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": stableFallbackText(reason, err)},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}
```

Wire it in `handleOpenAIWithAccountRetry` and `handleOpenAIResponsesWithAccountRetry` final retry-exhaustion/no-account paths for stable Opus 4.7 requests.

- [ ] **Step 3: Verify**

Run:

```bash
go test ./proxy -run 'TestStableDownstreamOpenAI' -count=1 -v
```

Expected: PASS.

---

### Task 6: Request Logs Prove Suppressed Downstream Failure Status

**Files:**
- Modify: `proxy/request_log.go`
- Modify: `proxy/handler.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Write failing log tests**

Add:

```go
func TestRequestLogRecordsStableDownstreamFallback(t *testing.T) {
	entry := RequestLogEntry{}
	markRequestLogStableFallback(&entry, "no_available_accounts", http.StatusServiceUnavailable)
	if !entry.StableDownstreamFallback {
		t.Fatalf("expected StableDownstreamFallback true")
	}
	if entry.StableFallbackReason != "no_available_accounts" {
		t.Fatalf("reason = %q", entry.StableFallbackReason)
	}
	if entry.SuppressedDownstreamStatus != http.StatusServiceUnavailable {
		t.Fatalf("suppressed status = %d", entry.SuppressedDownstreamStatus)
	}
}
```

- [ ] **Step 2: Implement log fields**

Add to `RequestLogEntry`:

```go
StableDownstreamFallback bool   `json:"stableDownstreamFallback,omitempty"`
StableFallbackReason    string `json:"stableFallbackReason,omitempty"`
SuppressedDownstreamStatus int  `json:"suppressedDownstreamStatus,omitempty"`
```

Add helper:

```go
func markRequestLogStableFallback(entry *RequestLogEntry, reason string, suppressedStatus int) {
	if entry == nil {
		return
	}
	entry.StableDownstreamFallback = true
	entry.StableFallbackReason = reason
	entry.SuppressedDownstreamStatus = suppressedStatus
}
```

Add request-context wrapper equivalent if existing request logs are only mutated through `*http.Request` context:

```go
func updateRequestLogStableFallback(r *http.Request, reason string, suppressedStatus int) {
	ctx := requestLogContextFromRequest(r)
	if ctx == nil {
		return
	}
	markRequestLogStableFallback(&ctx.entry, reason, suppressedStatus)
}
```

Use the actual existing request-log context helper names in `proxy/request_log.go`.

- [ ] **Step 3: Call logging helper from stable fallback writers**

Before writing the stable fallback response:

```go
updateRequestLogStableFallback(r, reason, http.StatusServiceUnavailable)
```

For suppressed 429-specific paths, pass `http.StatusTooManyRequests`.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./proxy -run 'TestRequestLogRecordsStableDownstreamFallback' -count=1 -v
```

Expected: PASS.

---

### Task 7: Add Real sub2api Stable 200 UAT Harness

**Files:**
- Add: `docs/superpowers/uat/sub2api-opus47-stable-200/README.md`
- Add: `docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js`
- Modify: `docs/kiro-ha-compatibility-matrix.md`

- [ ] **Step 1: Create UAT README**

Create:

```markdown
# sub2api Opus 4.7 Stable 200 UAT

This UAT verifies the downstream contract:

- sub2api receives no Kiro-Go generation response with HTTP 429, 502, or 503.
- Opus 4.7 stream and non-stream calls remain syntactically valid.
- Kiro-Go request logs record any internally suppressed retryable failure.

Required environment:

- `SUB2API_BASE_URL`, default `http://127.0.0.1:18080`
- `SUB2API_API_KEY`
- `MODEL`, default `claude-opus-4.7`
- `ROUNDS`, default `10`
- `CONCURRENCY`, default `10`

Pass criteria:

- Every HTTP response status is `200`.
- Every response body is valid JSON or valid Anthropic SSE for the chosen endpoint.
- No response body contains gateway-level `HTTP 429`, `HTTP 502`, or `HTTP 503`.
- Kiro-Go Admin request logs show stable fallback metadata if upstream capacity was exhausted.
```

- [ ] **Step 2: Create Node UAT script**

Create `run-stable-200-uat.js`:

```js
const baseURL = process.env.SUB2API_BASE_URL || "http://127.0.0.1:18080";
const apiKey = process.env.SUB2API_API_KEY;
const model = process.env.MODEL || "claude-opus-4.7";
const rounds = Number(process.env.ROUNDS || "10");
const concurrency = Number(process.env.CONCURRENCY || "10");

if (!apiKey) {
  console.error("SUB2API_API_KEY is required");
  process.exit(2);
}

async function callOnce(i, stream) {
  const res = await fetch(`${baseURL}/v1/messages`, {
    method: "POST",
    headers: {
      "Authorization": `Bearer ${apiKey}`,
      "Content-Type": "application/json",
      "User-Agent": "sub2api-stable-200-uat/1.0 claude-cli/2.1",
      "X-Sub2API-Request": "uat"
    },
    body: JSON.stringify({
      model,
      max_tokens: 64,
      stream,
      messages: [{ role: "user", content: `stable 200 uat request ${i}; reply with ok` }]
    })
  });
  const text = await res.text();
  const forbidden = [429, 502, 503].includes(res.status) || /HTTP 429|HTTP 502|HTTP 503/.test(text);
  return { index: i, stream, status: res.status, ok: res.status === 200 && !forbidden, forbidden, sample: text.slice(0, 240) };
}

async function main() {
  const jobs = [];
  for (let i = 0; i < rounds * concurrency; i++) {
    jobs.push(callOnce(i, i % 2 === 0));
  }
  const results = await Promise.all(jobs);
  const failed = results.filter(r => !r.ok);
  console.log(JSON.stringify({
    total: results.length,
    passed: results.length - failed.length,
    failed: failed.length,
    forbidden_statuses: results.filter(r => [429, 502, 503].includes(r.status)).length,
    failures: failed.slice(0, 10)
  }, null, 2));
  process.exit(failed.length === 0 ? 0 : 1);
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
```

- [ ] **Step 3: Document matrix**

In `docs/kiro-ha-compatibility-matrix.md`, add a row:

```markdown
| HA-08 | sub2api Opus 4.7 stable downstream status | `StableDownstream` generation contract | HUMAN_NEEDED (`latest_code_live_uat`) | REQUIRED | `docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js` |
```

- [ ] **Step 4: Verify script syntax**

Run:

```bash
node --check docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Expected: PASS.

---

### Task 8: Regression Suite And Acceptance Gate

**Files:**
- Modify as needed from prior tasks.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
go test ./config ./pool ./proxy -run 'StableDownstream|Opus47|ModelAdmission|NoAvailable|RequestLogRecordsStable' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run UAT syntax check**

Run:

```bash
node --check docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Expected: PASS.

- [ ] **Step 4: Run live UAT when local sub2api and Kiro-Go are available**

Run:

```bash
SUB2API_BASE_URL=http://127.0.0.1:18080 \
SUB2API_API_KEY="$SUB2API_API_KEY" \
ROUNDS=10 \
CONCURRENCY=10 \
node docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Expected JSON:

```json
{
  "total": 100,
  "passed": 100,
  "failed": 0,
  "forbidden_statuses": 0
}
```

- [ ] **Step 5: Final manual acceptance checklist**

Confirm:

- No sub2api-facing Opus 4.7 generation response uses HTTP `429`, `502`, or `503`.
- Kiro-Go request logs still record real internal upstream reasons.
- Admin and maintenance endpoints keep useful non-200 status codes.
- Stream fallback emits complete Anthropic SSE with `message_start`, text delta, `message_delta`, and `message_stop`.
- Non-stable/non-Opus behavior remains unchanged.

---

## Risk Notes

- This plan guarantees the downstream HTTP contract, not true upstream Opus 4.7 mathematical availability. Real availability still depends on account count, quota, Kiro capacity, network, and upstream policy.
- Returning stable `200` fallback text is safer for sub2api health than returning `503`, but it may cause Claude Code to treat the turn as completed. If product behavior requires indefinite waiting instead, replace the fallback writer with a bounded stream heartbeat queue before sending final text.
- If multiple Kiro-Go instances share the same accounts, local-only cooldowns are not enough. Add shared state before scaling horizontally.
