# Claude Code High-Concurrency Correctness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Claude Code calls through sub2api and Kiro-Go correct, observable, and recoverable when Kiro upstream returns high-concurrency 429s.

**Architecture:** Keep Kiro-Go as the upstream account-pool owner and sub2api as the downstream scheduler/protocol gateway. Kiro-Go must expose layered readiness and cooldown state; sub2api must recognize Kiro-Go `TEMPORARY_LIMITED` as a pool cooldown signal and avoid same-account retry amplification.

**Tech Stack:** Go, net/http, gin, PostgreSQL via sub2api repositories, Playwright for UAT screenshots, curl/jq/psql for API and DB evidence.

---

## Current Baseline

The Kiro-Go worktree already has uncommitted changes in:

- `pool/account.go`
- `pool/account_test.go`
- `proxy/handler.go`
- `proxy/handler_test.go`

Those changes already add parts of risk-group cooldown and retry handling. Do not revert them. Read diffs before editing the same files, and preserve the existing tests.

The main missing functional gap is in `/www/sub2api`: Kiro-Go `TEMPORARY_LIMITED` 429s are treated like generic pool-mode retryable 429s, causing same-account retries against `kiro_claude_01`.

## File Map

Kiro-Go:

- Modify `pool/account.go`: preserve current risk-group cooldown behavior; add helper accessors only if needed for readiness.
- Modify `proxy/handler.go`: extend readiness/admin responses and background test skip behavior only if not already present.
- Modify `proxy/request_log.go`: include layered status fields in request log metadata if readiness cannot explain failures.
- Test `pool/account_test.go`: risk-group escalation, single temporary limit does not cool shared profile, expired cooldown clears.
- Test `proxy/handler_test.go`: Claude 429 envelope, `Retry-After`, no empty SSE, admin test skips cooldown.

sub2api:

- Modify `/www/sub2api/backend/internal/service/gateway_service.go`: add Kiro-Go temporary-limit classifier and use it in Anthropic API-key passthrough failover paths.
- Modify `/www/sub2api/backend/internal/service/temp_unsched.go`: no required change unless a shared helper struct is needed.
- Modify `/www/sub2api/backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`: add passthrough tests for Kiro-Go `TEMPORARY_LIMITED`.
- Modify `/www/sub2api/backend/internal/handler/failover_loop.go`: only if service-layer classification cannot prevent same-account retry. Preferred: avoid changing generic failover loop.
- Test `/www/sub2api/backend/internal/handler/failover_loop_test.go`: only if failover loop changes.

UAT:

- Create `docs/superpowers/uat/claude-code-high-concurrency-correctness-<timestamp>/`.
- Create Playwright script under that UAT directory.
- Create `UAT-RESULT.md`.

---

### Task 1: Verify Kiro-Go Baseline Tests

**Files:**
- Read: `pool/account.go`
- Read: `proxy/handler.go`
- Test: `pool/account_test.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Run focused Kiro-Go tests before edits**

Run:

```bash
cd /www/Kiro-Go
go test ./pool ./proxy -run 'TemporaryLimit|RiskGroup|RetryAfter|NoAvailableAccounts|Opus47|Stream.*Malformed|CapacityLimit' -count=1
```

Expected:

- PASS means current uncommitted baseline is coherent.
- FAIL means inspect the failing test before any new code. Do not skip failures; the existing dirty changes are part of the working baseline.

- [ ] **Step 2: Run full Kiro-Go package tests for touched packages**

Run:

```bash
cd /www/Kiro-Go
go test ./pool ./proxy -count=1
```

Expected:

- PASS or specific pre-existing failures documented in the plan execution notes.

- [ ] **Step 3: Commit only if baseline tests pass and current dirty edits are intentional**

Do not commit user changes blindly. If the current dirty Kiro-Go changes are not yours, leave them uncommitted and continue with additive edits only.

---

### Task 2: Add sub2api Kiro-Go Temporary-Limit Classifier

**Files:**
- Modify: `/www/sub2api/backend/internal/service/gateway_service.go`
- Test: `/www/sub2api/backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`

- [ ] **Step 1: Add failing tests for classifier behavior**

Append these tests to `/www/sub2api/backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`:

```go
func TestGatewayService_AnthropicAPIKeyPassthrough_KiroTemporaryLimitedNotSameAccountRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-opus-4-7","stream":true,"messages":[{"role":"user","content":"hello"}],"max_tokens":16}`)
	parsed := &ParsedRequest{
		Body:   body,
		Model:  "claude-opus-4-7",
		Stream: true,
	}

	upstreamBody := `{"type":"error","error":{"type":"rate_limit_error","message":"No available accounts for claude-opus-4.7: upstream temporary limits are cooling down (TEMPORARY_LIMITED)"}}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"Content-Type":              []string{"application/json"},
				"Retry-After":               []string{"61"},
				"X-Kiro-Go-Error-Reason":    []string{"TEMPORARY_LIMITED"},
				"X-Request-Id":              []string{"req-kiro-temp-limit"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}

	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				MaxLineSize:              defaultMaxLineSize,
				LogUpstreamErrorBody:     true,
				LogUpstreamErrorBodyMaxBytes: 2048,
			},
		},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{},
	}

	account := newAnthropicAPIKeyAccountForTest()
	account.ID = 2401
	account.Name = "kiro_claude_01"

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount, "Kiro-Go TEMPORARY_LIMITED must not trigger same-account retry")
	require.Contains(t, string(failoverErr.ResponseBody), "TEMPORARY_LIMITED")
}

func TestGatewayService_KiroTemporaryLimitedClassifier(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		body   string
		want   bool
	}{
		{
			name:   "explicit response header",
			header: http.Header{"X-Kiro-Go-Error-Reason": []string{"TEMPORARY_LIMITED"}},
			body:   `{"type":"error","error":{"message":"anything"}}`,
			want:   true,
		},
		{
			name:   "pool message",
			header: http.Header{},
			body:   `{"type":"error","error":{"message":"No available accounts for claude-opus-4.7: upstream temporary limits are cooling down (TEMPORARY_LIMITED)"}}`,
			want:   true,
		},
		{
			name:   "official suspicious activity body",
			header: http.Header{},
			body:   `{"error":{"message":"HTTP 429 from Kiro IDE: {\"message\":\"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.\",\"reason\":null}","type":"rate_limit_error"}}`,
			want:   true,
		},
		{
			name:   "generic 429 is not kiro temporary limit",
			header: http.Header{},
			body:   `{"type":"error","error":{"message":"Upstream rate limit exceeded, please retry later"}}`,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isKiroGoTemporaryLimitedResponse(tt.header, []byte(tt.body)))
		})
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
cd /www/sub2api/backend
go test ./internal/service -run 'KiroTemporaryLimited|AnthropicAPIKeyPassthrough_KiroTemporaryLimited' -count=1
```

Expected:

- FAIL with `undefined: isKiroGoTemporaryLimitedResponse`, or fail because `RetryableOnSameAccount` is still true.

- [ ] **Step 3: Implement classifier**

In `/www/sub2api/backend/internal/service/gateway_service.go`, near `extractUpstreamErrorMessage`, add:

```go
func isKiroGoTemporaryLimitedResponse(headers http.Header, body []byte) bool {
	if headers != nil {
		if strings.EqualFold(strings.TrimSpace(headers.Get("X-Kiro-Go-Error-Reason")), "TEMPORARY_LIMITED") {
			return true
		}
	}

	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "temporary_limited") &&
		strings.Contains(lower, "upstream temporary limits are cooling down") {
		return true
	}
	return strings.Contains(lower, "due to suspicious activity") &&
		strings.Contains(lower, "temporary limits") &&
		strings.Contains(lower, "kiro")
}
```

Use existing imports. `gateway_service.go` already imports `net/http` and `strings`.

- [ ] **Step 4: Use classifier in Anthropic API-key passthrough failover returns**

In `/www/sub2api/backend/internal/service/gateway_service.go`, update the two Anthropic API-key passthrough `UpstreamFailoverError` returns around the `retry_exhausted_failover` and direct `failover` branches.

Change:

```go
RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
```

to:

```go
RetryableOnSameAccount: account.IsPoolMode() &&
	isPoolModeRetryableStatus(resp.StatusCode) &&
	!isKiroGoTemporaryLimitedResponse(resp.Header, respBody),
```

Do this only in the Anthropic API-key passthrough function, not globally for OpenAI/Gemini paths.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd /www/sub2api/backend
go test ./internal/service -run 'KiroTemporaryLimited|AnthropicAPIKeyPassthrough_KiroTemporaryLimited|AnthropicAPIKeyPassthrough' -count=1
```

Expected:

- PASS.

- [ ] **Step 6: Commit sub2api classifier**

Run:

```bash
cd /www/sub2api
git status --short
git add backend/internal/service/gateway_service.go backend/internal/service/gateway_anthropic_apikey_passthrough_test.go
git commit -m "fix: avoid retrying kiro temporary limits"
```

Expected:

- Commit includes only the two sub2api files.

---

### Task 3: Temp-Unschedule Kiro-Go Temporary Limits in sub2api

**Files:**
- Modify: `/www/sub2api/backend/internal/service/gateway_service.go`
- Test: `/www/sub2api/backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`

- [ ] **Step 1: Add repository test stub**

In `/www/sub2api/backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`, add a small repo stub near other test helpers:

```go
type kiroTempLimitAccountRepo struct {
	AccountRepository
	lastID     int64
	lastUntil  time.Time
	lastReason string
	calls      int
}

func (r *kiroTempLimitAccountRepo) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.calls++
	r.lastID = id
	r.lastUntil = until
	r.lastReason = reason
	return nil
}
```

- [ ] **Step 2: Add failing test for temp-unschedule side effect**

Append:

```go
func TestGatewayService_AnthropicAPIKeyPassthrough_KiroTemporaryLimitedTempUnschedulesAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-opus-4-7","stream":false,"messages":[{"role":"user","content":"hello"}],"max_tokens":16}`)
	parsed := &ParsedRequest{Body: body, Model: "claude-opus-4-7"}

	upstreamBody := `{"type":"error","error":{"type":"rate_limit_error","message":"No available accounts for claude-opus-4.7: upstream temporary limits are cooling down (TEMPORARY_LIMITED)"}}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"Retry-After":            []string{"61"},
				"X-Kiro-Go-Error-Reason": []string{"TEMPORARY_LIMITED"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}
	repo := &kiroTempLimitAccountRepo{}
	svc := &GatewayService{
		cfg:              &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		accountRepo:      repo,
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{},
	}
	account := newAnthropicAPIKeyAccountForTest()
	account.ID = 2402

	_, err := svc.Forward(context.Background(), c, account, parsed)

	require.Error(t, err)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, int64(2402), repo.lastID)
	require.True(t, repo.lastUntil.After(time.Now().Add(55*time.Second)), "Retry-After should drive cooldown")
	require.Contains(t, repo.lastReason, "TEMPORARY_LIMITED")
	require.Contains(t, repo.lastReason, "claude-opus-4.7")
}
```

- [ ] **Step 3: Run test and verify failure**

Run:

```bash
cd /www/sub2api/backend
go test ./internal/service -run 'KiroTemporaryLimitedTempUnschedules' -count=1
```

Expected:

- FAIL because no temp-unschedule side effect exists.

- [ ] **Step 4: Implement cooldown helper**

In `/www/sub2api/backend/internal/service/gateway_service.go`, add helpers near the classifier:

```go
const kiroTemporaryLimitFallbackCooldown = time.Minute

func kiroTemporaryLimitRetryAt(headers http.Header, now time.Time) time.Time {
	if headers != nil {
		if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
				return now.Add(time.Duration(seconds) * time.Second)
			}
			if parsed, err := http.ParseTime(retryAfter); err == nil && parsed.After(now) {
				return parsed
			}
		}
	}
	return now.Add(kiroTemporaryLimitFallbackCooldown)
}

func (s *GatewayService) tempUnscheduleKiroTemporaryLimit(ctx context.Context, account *Account, headers http.Header, body []byte) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	now := time.Now()
	until := kiroTemporaryLimitRetryAt(headers, now)
	message := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if message == "" {
		message = "Kiro-Go upstream temporary limits are cooling down (TEMPORARY_LIMITED)"
	}
	state := &TempUnschedState{
		UntilUnix:        until.Unix(),
		TriggeredAtUnix:  now.Unix(),
		StatusCode:       http.StatusTooManyRequests,
		MatchedKeyword:   "TEMPORARY_LIMITED",
		RuleIndex:        -1,
		ErrorMessage:     truncateTempUnschedMessage([]byte(message), tempUnschedMessageMaxBytes),
	}
	reasonBytes, _ := json.Marshal(state)
	reason := string(reasonBytes)
	if reason == "" {
		reason = message
	}
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		slog.Warn("kiro_temp_limit_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return
	}
	if s.rateLimitService != nil && s.rateLimitService.tempUnschedCache != nil {
		_ = s.rateLimitService.tempUnschedCache.SetTempUnsched(ctx, account.ID, state)
	}
}
```

`gateway_service.go` already imports `encoding/json`, `log/slog`, `net/http`, `strconv`, `strings`, and `time`.

- [ ] **Step 5: Call helper before returning failover**

In both Anthropic API-key passthrough failover branches from Task 2, compute:

```go
kiroTemporaryLimited := isKiroGoTemporaryLimitedResponse(resp.Header, respBody)
if kiroTemporaryLimited {
	s.tempUnscheduleKiroTemporaryLimit(ctx, account, resp.Header, respBody)
}
```

Then use:

```go
RetryableOnSameAccount: account.IsPoolMode() &&
	isPoolModeRetryableStatus(resp.StatusCode) &&
	!kiroTemporaryLimited,
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
cd /www/sub2api/backend
go test ./internal/service -run 'KiroTemporaryLimited|AnthropicAPIKeyPassthrough_KiroTemporaryLimited' -count=1
```

Expected:

- PASS.

- [ ] **Step 7: Commit temp-unschedule behavior**

Run:

```bash
cd /www/sub2api
git add backend/internal/service/gateway_service.go backend/internal/service/gateway_anthropic_apikey_passthrough_test.go
git commit -m "fix: cool down kiro temporary limited accounts"
```

Expected:

- Commit includes only sub2api service and tests.

---

### Task 4: Kiro-Go Readiness and Background Cooldown Review

**Files:**
- Modify if needed: `proxy/handler.go`
- Modify if needed: `pool/account.go`
- Test: `proxy/handler_test.go`
- Test: `pool/account_test.go`

- [ ] **Step 1: Inspect existing readiness response**

Run:

```bash
cd /www/Kiro-Go
rg -n "model-readiness|readiness|cooldownSource|risk_group|admissionPressure|listedByGateway|routingReason" proxy pool -S
```

Expected:

- Identify the admin model-readiness handler and JSON fields.

- [ ] **Step 2: Add or confirm readiness fields**

The model-readiness API must expose these JSON fields:

```json
{
  "requestedModel": "claude-opus-4-7",
  "mappedModel": "claude-opus-4.7",
  "listedByGateway": true,
  "routingReason": "schedulable accounts available",
  "admissionPressure": {},
  "summary": {
    "modelListed": true,
    "accountsEvaluated": 21,
    "locallySchedulable": 21,
    "riskGroupCoolingDown": 0,
    "generationBlocked": 0
  }
}
```

If equivalent fields already exist under different names, do not rename existing public fields. Add `summary` as a backward-compatible extension.

- [ ] **Step 3: Add failing readiness test if summary is missing**

In `proxy/handler_test.go`, add a test that calls the model-readiness admin handler and checks:

```go
if _, ok := resp["summary"].(map[string]interface{}); !ok {
	t.Fatalf("expected layered readiness summary, got %#v", resp)
}
```

Use the existing admin/readiness test helpers in `proxy/handler_test.go` rather than creating new router scaffolding.

- [ ] **Step 4: Implement minimal readiness summary**

Add summary construction in the existing readiness handler. Use existing pool methods:

- `ModelBlockState(model, time.Now())`
- account list already used by the handler
- admission pressure already used by the handler

Do not run real upstream probes in this handler.

- [ ] **Step 5: Verify Kiro-Go focused tests**

Run:

```bash
cd /www/Kiro-Go
go test ./pool ./proxy -run 'Readiness|TemporaryLimit|RiskGroup|NoAvailableAccounts|RetryAfter' -count=1
```

Expected:

- PASS.

- [ ] **Step 6: Commit Kiro-Go readiness work only if you changed files**

Run:

```bash
cd /www/Kiro-Go
git status --short
git add pool/account.go pool/account_test.go proxy/handler.go proxy/handler_test.go proxy/request_log.go
git commit -m "fix: expose layered kiro readiness"
```

Expected:

- Commit only files actually changed by this task. If there were pre-existing dirty edits from the user, do not include unrelated hunks.

---

### Task 5: Integration Verification

**Files:**
- Create: `docs/superpowers/uat/claude-code-high-concurrency-correctness-<timestamp>/UAT-RESULT.md`
- Create: `docs/superpowers/uat/claude-code-high-concurrency-correctness-<timestamp>/playwright/fullstack-uat.js`
- Create: API and DB evidence files in the UAT directory.

- [ ] **Step 1: Restart/reload services if needed**

Run only after code builds:

```bash
cd /www/Kiro-Go
go test ./pool ./proxy -count=1

cd /www/sub2api/backend
go test ./internal/service ./internal/handler -run 'KiroTemporaryLimited|FailoverError|AnthropicAPIKeyPassthrough' -count=1
```

Expected:

- PASS.

If services are containerized and require rebuild/restart, use the repo's existing deployment commands. Do not run destructive Docker volume commands.

- [ ] **Step 2: Create UAT directory**

Run:

```bash
cd /www/Kiro-Go
uat="docs/superpowers/uat/claude-code-high-concurrency-correctness-$(date +%Y%m%d%H%M%S)"
mkdir -p "$uat/api" "$uat/db" "$uat/screenshots" "$uat/playwright"
printf '%s\n' "$uat" > /tmp/kiro-current-uat-dir
```

Expected:

- New UAT path is recorded in `/tmp/kiro-current-uat-dir`.

- [ ] **Step 3: Capture API evidence**

Run minimal probes:

```bash
cd /www/Kiro-Go
uat=$(cat /tmp/kiro-current-uat-dir)
admin=$(jq -r '.password' /www/Kiro-Go/data/config.json)
curl -sS -H "X-Admin-Password: $admin" \
  'http://127.0.0.1:8080/admin/api/claude-code/model-readiness?model=claude-opus-4-7' \
  | jq . > "$uat/api/kiro-readiness-opus47.json"

body='{"model":"claude-opus-4-7","max_tokens":16,"stream":false,"messages":[{"role":"user","content":"UAT probe. Reply exactly ok."}]}'
kiro_key=$(jq -r '.apiKey' /www/Kiro-Go/data/config.json)
curl -sS -D "$uat/api/kiro-direct.headers" -o "$uat/api/kiro-direct.body" \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -H "Authorization: Bearer $kiro_key" \
  --data "$body" \
  http://127.0.0.1:8080/v1/messages
```

Expected:

- Readiness JSON saved.
- Direct Kiro response saved, success or valid Anthropic error.

- [ ] **Step 4: Capture sub2api Claude key evidence**

Run:

```bash
cd /www/Kiro-Go
uat=$(cat /tmp/kiro-current-uat-dir)
pass=$(awk '/^database:/{f=1} f && /^[[:space:]]*password:/{print $2; exit}' /www/sub2api/deploy/data/config.yaml)
claude_key=$(docker exec -e PGPASSWORD="$pass" sub2api psql -h postgres -U sub2api -d sub2api -Atc "select key from api_keys where id=2")
body='{"model":"claude-opus-4-7","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"UAT stream probe. Reply exactly ok."}]}'
curl -sS -D "$uat/api/sub2api-claude-stream.headers" -o "$uat/api/sub2api-claude-stream.body" \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -H "Authorization: Bearer $claude_key" \
  --data "$body" \
  http://127.0.0.1:18080/v1/messages
```

Expected:

- If 200: body contains Anthropic SSE events and no malformed partial stream.
- If 429: body is JSON error or a valid pre-start error path with `rate_limit_error`; headers include `Retry-After` when known.

- [ ] **Step 5: Capture DB evidence**

Run:

```bash
cd /www/Kiro-Go
uat=$(cat /tmp/kiro-current-uat-dir)
pass=$(awk '/^database:/{f=1} f && /^[[:space:]]*password:/{print $2; exit}' /www/sub2api/deploy/data/config.yaml)
docker exec -e PGPASSWORD="$pass" sub2api psql -h postgres -U sub2api -d sub2api -c "
select a.id,a.name,a.platform,a.type,a.status,a.schedulable,a.concurrency,
       a.rate_limit_reset_at,a.temp_unschedulable_until,a.temp_unschedulable_reason,ag.group_id
from accounts a
left join account_groups ag on ag.account_id=a.id
where a.deleted_at is null
order by ag.group_id,a.id;" > "$uat/db/accounts.txt"

docker exec -e PGPASSWORD="$pass" sub2api psql -h postgres -U sub2api -d sub2api -c "
select id,created_at,request_id,api_key_id,account_id,group_id,platform,model,requested_model,
       stream,status_code,upstream_status_code,left(error_message,240) as error_message
from ops_error_logs
where created_at > now() - interval '2 hours'
  and (account_id=24 or model ilike '%opus%' or requested_model ilike '%opus%' or error_message ilike '%TEMPORARY%')
order by created_at desc limit 50;" > "$uat/db/ops_error_logs.txt"

docker exec -e PGPASSWORD="$pass" sub2api psql -h postgres -U sub2api -d sub2api -c "
select id,created_at,request_id,api_key_id,account_id,group_id,platform,model,requested_model,
       stream,status_code,input_tokens,output_tokens
from usage_logs
where created_at > now() - interval '2 hours'
  and (account_id=24 or model ilike '%opus%' or requested_model ilike '%opus%')
order by created_at desc limit 50;" > "$uat/db/usage_logs.txt"
```

Expected:

- DB files show group/account topology and request outcomes.

- [ ] **Step 6: Playwright screenshots**

Reuse the login/screenshot patterns from:

`docs/superpowers/uat/kiro-429-realness-20260520110750/playwright/kiro-429-fullstack-uat.js`

Create `playwright/fullstack-uat.js` that:

- Opens Kiro-Go admin.
- Captures readiness page or admin JSON rendered page.
- Opens sub2api admin.
- Captures accounts/groups/log pages.
- Saves screenshots under `screenshots/`.
- Writes extracted visible text to `api/playwright-fullstack-summary.json`.

Run:

```bash
cd /www/Kiro-Go
uat=$(cat /tmp/kiro-current-uat-dir)
node "$uat/playwright/fullstack-uat.js"
```

Expected:

- Screenshots are non-empty.
- Summary JSON states whether visible UI agrees with API/DB evidence.

- [ ] **Step 7: Write UAT result**

Create `UAT-RESULT.md` with:

```markdown
# UAT Result: Claude Code High-Concurrency Correctness

Date: 2026-05-20
Verdict: PASS or FAIL

## API Evidence

Summarize direct Kiro-Go and sub2api responses with status codes and key body fields.

## DB Evidence

Summarize accounts, usage_logs, and ops_error_logs.

## Screenshot Analysis

For each screenshot, state what visible fact it proves and whether it matches API/DB evidence.

## Pass Criteria

- [ ] No empty HTTP responses.
- [ ] No pre-generation failure is emitted as broken SSE.
- [ ] Kiro-Go TEMPORARY_LIMITED does not trigger same-account retry amplification.
- [ ] 429 error envelope is Anthropic-compatible.
- [ ] API, DB, logs, and screenshots agree.
```

Mark PASS only if every checkbox is true.

- [ ] **Step 8: Commit UAT**

Run:

```bash
cd /www/Kiro-Go
uat=$(cat /tmp/kiro-current-uat-dir)
git add "$uat"
git commit -m "test: add claude code concurrency uat evidence"
```

Expected:

- Commit contains only UAT evidence.

---

## Self-Review

Spec coverage:

- Kiro-Go layered readiness: Task 4.
- Kiro-Go temporary-limit dampening: Task 1 verifies existing baseline; Task 4 fills readiness gaps.
- sub2api Kiro-Go 429 classification: Task 2.
- sub2api temp-unschedule: Task 3.
- Protocol correctness and no broken SSE: Task 5 API checks.
- Frontend/API/DB evidence: Task 5.

Known risk:

- If Kiro official is actively limiting the pool during UAT, the correct result may be valid 429 rather than 200. PASS requires correct classification and evidence agreement, not guaranteed upstream success.

