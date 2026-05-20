package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kiro-go/auth"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestThinkingSourceReasoningFirst(t *testing.T) {
	var source thinkingStreamSource

	if !allowReasoningSource(&source) {
		t.Fatalf("expected reasoning source to be accepted first")
	}
	if source != thinkingSourceReasoningEvent {
		t.Fatalf("expected source to be reasoning, got %v", source)
	}
	if allowTagSource(&source) {
		t.Fatalf("expected tag source to be rejected after reasoning source selected")
	}
}

func TestStableDownstreamAppliesToOpus47Sub2APIClaudeRequests(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")

	if !stableDownstreamForRequest(r, "claude-opus-4.7", true) {
		t.Fatalf("expected stable downstream for sub2api Opus 4.7 Claude request")
	}
}

func TestStableDownstreamDoesNotApplyToNonOpusByDefault(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0")

	if stableDownstreamForRequest(r, "claude-sonnet-4.5", true) {
		t.Fatalf("did not expect stable downstream for sonnet by default")
	}
}

func TestStableDownstreamClaudeNoAccountsReturnsHTTP200(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
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
	if strings.Contains(w.Body.String(), "kiro_go_stable_fallback") {
		t.Fatalf("stable fallback must not leak internal fallback text into assistant content: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Opus 4.7") {
		t.Fatalf("stable fallback must include non-empty assistant content for Claude Code: %s", w.Body.String())
	}
}

func TestStableDownstreamClaudeStreamFallbackStartsHTTP200SSE(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
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
	if strings.Contains(w.Body.String(), "kiro_go_stable_fallback") {
		t.Fatalf("stable SSE fallback must not leak internal fallback text into assistant content: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "content_block_delta") || !strings.Contains(w.Body.String(), "Opus 4.7") {
		t.Fatalf("stable SSE fallback must include non-empty assistant text for Claude Code: %s", w.Body.String())
	}
}

func TestStableClaudeFallbackMarksContentFailure(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(httptest.NewRecorder(), req)

	h.sendStableClaudeFallback(loggedWriter, loggedReq, "claude-opus-4.7", "admission_pressure", errors.New("queue timeout"))
	h.finishRequestLog(ctx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.ContentSuccess {
		t.Fatalf("stable fallback must not be content success: %#v", entry)
	}
	if entry.ContentFailureReason != "admission_pressure" {
		t.Fatalf("ContentFailureReason = %q, want admission_pressure", entry.ContentFailureReason)
	}
	if !entry.StableFallbackFinal {
		t.Fatalf("expected StableFallbackFinal")
	}
}

func TestContentSuccessTokenCountTreatsStructuredOutputAsContent(t *testing.T) {
	if got := contentSuccessTokenCount(0, 1); got != 1 {
		t.Fatalf("structured output tokens = %d, want 1", got)
	}
	if got := contentSuccessTokenCount(0, 0, "  real content  "); got != 1 {
		t.Fatalf("text output tokens = %d, want 1", got)
	}
	if got := contentSuccessTokenCount(7, 0); got != 7 {
		t.Fatalf("estimated tokens = %d, want 7", got)
	}
	if got := contentSuccessTokenCount(0, 0, "   "); got != 0 {
		t.Fatalf("blank output tokens = %d, want 0", got)
	}
}

func TestStableDownstreamSuppressesOpus47RateLimitStatus(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
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
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	status, errType, stable := downstreamStatusForRetryExhaustion(r, "claude-opus-4.7", true, http.StatusTooManyRequests, "rate_limit_error")

	if stable {
		t.Fatalf("did not expect stable mode")
	}
	if status != http.StatusServiceUnavailable || errType != "overloaded_error" {
		t.Fatalf("status/type = %d/%s, want 503/overloaded_error", status, errType)
	}
}

func TestStableDownstreamOpenAINoAccountsReturnsHTTP200(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
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
	if strings.Contains(w.Body.String(), "kiro_go_stable_fallback") || strings.Contains(w.Body.String(), "Opus 4.7 is temporarily waiting") {
		t.Fatalf("stable fallback must not leak internal fallback text into assistant content: %s", w.Body.String())
	}
}

func TestStableDownstreamOpenAIResponsesNoAccountsReturnsHTTP200(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	r.Header.Set("User-Agent", "sub2api/1.0")

	h.sendStableOpenAIResponsesFallback(w, r, "claude-opus-4.7", "no_available_accounts", errors.New("No available accounts"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"object":"response"`) {
		t.Fatalf("expected OpenAI Responses object, got %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("stable fallback must not use OpenAI error envelope: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "kiro_go_stable_fallback") || strings.Contains(w.Body.String(), "Opus 4.7 is temporarily waiting") {
		t.Fatalf("stable fallback must not leak internal fallback text into response output: %s", w.Body.String())
	}
}

func TestThinkingSourceTagFirst(t *testing.T) {
	var source thinkingStreamSource

	if !allowTagSource(&source) {
		t.Fatalf("expected tag source to be accepted first")
	}
	if source != thinkingSourceTagBlock {
		t.Fatalf("expected source to be tag, got %v", source)
	}
	if allowReasoningSource(&source) {
		t.Fatalf("expected reasoning source to be rejected after tag source selected")
	}
}

func TestThinkingSourceSameSourceRemainsAllowed(t *testing.T) {
	var source thinkingStreamSource

	if !allowTagSource(&source) {
		t.Fatalf("expected initial tag source selection to succeed")
	}
	if !allowTagSource(&source) {
		t.Fatalf("expected repeated tag source selection to stay allowed")
	}

	source = thinkingSourceUnknown
	if !allowReasoningSource(&source) {
		t.Fatalf("expected initial reasoning source selection to succeed")
	}
	if !allowReasoningSource(&source) {
		t.Fatalf("expected repeated reasoning source selection to stay allowed")
	}
}

func TestValidateOpenAIRequestShapeRejectsAssistantPrefill(t *testing.T) {
	req := &OpenAIRequest{
		Messages: []OpenAIMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "prefill"},
		},
	}

	if msg := validateOpenAIRequestShape(req); msg == "" {
		t.Fatalf("expected assistant-prefill final message to be rejected")
	}
}

func TestValidateOpenAIRequestShapeAllowsToolResultFinalTurn(t *testing.T) {
	req := &OpenAIRequest{
		Messages: []OpenAIMessage{
			{Role: "user", Content: "find weather"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "get_weather", Arguments: "{}"},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		},
	}

	if msg := validateOpenAIRequestShape(req); msg != "" {
		t.Fatalf("expected tool-result final turn to be valid, got %q", msg)
	}
}

func TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount(t *testing.T) {
	authHTTPClientTestMu.Lock()
	t.Cleanup(authHTTPClientTestMu.Unlock)
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute).Unix()
	account := config.Account{
		ID:           "acct-refresh-coalesce",
		Email:        "refresh@example.com",
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AuthMethod:   "social",
		Region:       "us-east-1",
		Enabled:      true,
		ExpiresAt:    expiredAt,
		ProfileArn:   "arn:aws:codewhisperer:profile/old",
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	var authCalls int32
	authHttpClientStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&authCalls, 1)
			time.Sleep(25 * time.Millisecond)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access-token","refreshToken":"new-refresh-token","expiresIn":7200,"profileArn":"arn:aws:codewhisperer:profile/new"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { auth.InitHttpClient("") })

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			local := account
			if err := h.ensureValidToken(&local); err != nil {
				errs <- err
				return
			}
			if local.AccessToken != "new-access-token" || local.RefreshToken != "new-refresh-token" {
				errs <- fmt.Errorf("unexpected local token state: %#v", local)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&authCalls); got != 1 {
		t.Fatalf("expected one coalesced auth refresh, got %d", got)
	}
	latest := p.GetByID(account.ID)
	if latest == nil || latest.AccessToken != "new-access-token" || latest.RefreshToken != "new-refresh-token" {
		t.Fatalf("expected pool token to refresh once, got %#v", latest)
	}
	persisted := config.GetAccounts()[0]
	if persisted.AccessToken != "new-access-token" || persisted.RefreshToken != "new-refresh-token" {
		t.Fatalf("expected persisted token to refresh once, got %#v", persisted)
	}
}

func TestEnsureValidTokenRefreshesDifferentAccountsInParallel(t *testing.T) {
	authHTTPClientTestMu.Lock()
	t.Cleanup(authHTTPClientTestMu.Unlock)
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute).Unix()
	accounts := []config.Account{
		{ID: "acct-refresh-a", Email: "a@example.com", AccessToken: "old-a", RefreshToken: "refresh-a", AuthMethod: "social", Region: "us-east-1", Enabled: true, ExpiresAt: expiredAt},
		{ID: "acct-refresh-b", Email: "b@example.com", AccessToken: "old-b", RefreshToken: "refresh-b", AuthMethod: "social", Region: "us-east-1", Enabled: true, ExpiresAt: expiredAt},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}

	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	var running int32
	var maxRunning int32
	authHttpClientStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			now := atomic.AddInt32(&running, 1)
			for {
				seen := atomic.LoadInt32(&maxRunning)
				if now <= seen || atomic.CompareAndSwapInt32(&maxRunning, seen, now) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-token","refreshToken":"new-refresh","expiresIn":7200}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { auth.InitHttpClient("") })

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(accounts))
	for _, account := range accounts {
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := h.ensureValidToken(&account); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&maxRunning); got < 2 {
		t.Fatalf("expected different accounts to refresh in parallel, max concurrent refreshes = %d", got)
	}
}

func TestRequestStickyKeyRequiresClientIdentifier(t *testing.T) {
	claudeReq := &ClaudeRequest{
		Model:  "claude-sonnet-4.5",
		System: "system prompt",
		Messages: []ClaudeMessage{{
			Role:    "user",
			Content: "common prompt",
		}},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	if got := requestStickyKey(req, claudeReq); got != "" {
		t.Fatalf("expected no sticky key without client/session/request headers, got %q", got)
	}

	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	if got := requestStickyKey(req, claudeReq); got != "session-1" {
		t.Fatalf("expected Claude Code session sticky key, got %q", got)
	}
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-1")
	if got := requestStickyKey(req, claudeReq); got != "session-1/agent-1" {
		t.Fatalf("expected Claude Code session+agent sticky key, got %q", got)
	}
	req.Header.Del("X-Claude-Code-Session-Id")
	req.Header.Del("X-Claude-Code-Agent-Id")
	req.Header.Set("X-Request-Id", "req-1")
	if got := requestStickyKey(req, claudeReq); got != "req-1" {
		t.Fatalf("expected request id sticky key, got %q", got)
	}
}

func TestValidateApiKeyAcceptsSecondaryClientKeyAndRejectsDisabled(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateClientAccessConfig(config.ClientAccessConfig{
		RequireApiKey: true,
		ApiKey:        "sk-primary",
		ClientApiKeys: []string{"sk-secondary", "#disabled#sk-disabled"},
	}); err != nil {
		t.Fatalf("update client access: %v", err)
	}

	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer sk-secondary")
	if !h.validateApiKey(req) {
		t.Fatalf("expected secondary client key to authenticate")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer sk-disabled")
	if h.validateApiKey(req) {
		t.Fatalf("expected disabled client key to be rejected")
	}
}

func TestValidateClientAccessAllowsLocalSub2apiAndRejectsRemoteWhenAllowlistSet(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateClientAccessConfig(config.ClientAccessConfig{
		RequireApiKey:     false,
		ClientIPAllowlist: []string{"127.0.0.1"},
	}); err != nil {
		t.Fatalf("update client access: %v", err)
	}

	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.RemoteAddr = "127.0.0.1:41234"
	if !h.validateClientAccess(req) {
		t.Fatalf("expected localhost sub2api-style caller to be allowed")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.RemoteAddr = "203.0.113.10:41234"
	if h.validateClientAccess(req) {
		t.Fatalf("expected remote caller outside allowlist to be rejected")
	}
}

func TestAdminSettingsRoundTripClientAccessFields(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{}
	body := strings.NewReader(`{
		"apiKey":"sk-primary",
		"requireApiKey":true,
		"clientApiKeys":["sk-secondary"],
		"clientIPAllowlist":["127.0.0.1","10.0.0.0/8"],
		"modelMappings":[{"id":"m1","enabled":true,"type":"alias","sourceModel":"my-opus","targetModels":["claude-opus-4.7"]}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/settings", body)
	w := httptest.NewRecorder()
	h.apiUpdateSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected settings update 200, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil)
	w = httptest.NewRecorder()
	h.apiGetSettings(w, req)

	var got struct {
		ClientApiKeys     []string                  `json:"clientApiKeys"`
		ClientIPAllowlist []string                  `json:"clientIPAllowlist"`
		ModelMappings     []config.ModelMappingRule `json:"modelMappings"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if len(got.ClientApiKeys) != 1 || got.ClientApiKeys[0] != "sk-secondary" {
		t.Fatalf("unexpected client api keys: %#v", got.ClientApiKeys)
	}
	if len(got.ClientIPAllowlist) != 2 {
		t.Fatalf("unexpected allowlist: %#v", got.ClientIPAllowlist)
	}
	if len(got.ModelMappings) != 1 || got.ModelMappings[0].SourceModel != "my-opus" {
		t.Fatalf("unexpected model mappings: %#v", got.ModelMappings)
	}
}

func TestAdminSettingsPartialUpdatePreservesClientAccess(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateClientAccessConfig(config.ClientAccessConfig{
		ApiKey:            "sk-primary",
		RequireApiKey:     true,
		ClientApiKeys:     []string{"sk-secondary"},
		ClientIPAllowlist: []string{"127.0.0.1"},
		ModelMappings:     []config.ModelMappingRule{{Enabled: true, Type: "alias", SourceModel: "my-opus", TargetModels: []string{"claude-opus-4.7"}}},
	}); err != nil {
		t.Fatalf("seed client access: %v", err)
	}

	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/settings", strings.NewReader(`{"allowOverUsage":true}`))
	w := httptest.NewRecorder()
	h.apiUpdateSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected partial settings update 200, got %d: %s", w.Code, w.Body.String())
	}

	got := config.GetClientAccessConfig()
	if got.ApiKey != "sk-primary" || !got.RequireApiKey {
		t.Fatalf("expected auth settings to be preserved, got %#v", got)
	}
	if len(got.ClientApiKeys) != 1 || got.ClientApiKeys[0] != "sk-secondary" {
		t.Fatalf("expected client keys preserved, got %#v", got.ClientApiKeys)
	}
	if len(got.ClientIPAllowlist) != 1 || got.ClientIPAllowlist[0] != "127.0.0.1" {
		t.Fatalf("expected allowlist preserved, got %#v", got.ClientIPAllowlist)
	}
	if len(got.ModelMappings) != 1 || got.ModelMappings[0].SourceModel != "my-opus" {
		t.Fatalf("expected model mappings preserved, got %#v", got.ModelMappings)
	}
}

func TestHandleOpenAIResponsesReturnsResponsesObject(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	config.AddAccount(config.Account{
		ID:          "acct-resp",
		Enabled:     true,
		AccessToken: "token-resp",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "responses ok"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 7, "outputTokens": 3}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.6","input":"say ok","max_output_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["object"] != "response" || resp["output_text"] != "responses ok" {
		t.Fatalf("unexpected responses object: %#v", resp)
	}
	if _, ok := resp["choices"]; ok {
		t.Fatalf("responses endpoint should not return chat choices: %#v", resp)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if config.GetAccounts()[0].RequestCount > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected async account stats update to complete")
}

func TestBuildOpenAIResponsesObjectRepresentsFunctionCalls(t *testing.T) {
	resp := buildOpenAIResponsesObjectWithToolUses("resp_test", "claude-sonnet-4.5", "", []KiroToolUse{{
		ToolUseID: "toolu_1",
		Name:      "read_file",
		Input:     map[string]interface{}{"path": "/tmp/a.go"},
	}}, 10, 2, true)

	output, ok := resp["output"].([]map[string]interface{})
	if !ok || len(output) != 1 {
		t.Fatalf("expected one output item, got %#v", resp["output"])
	}
	item := output[0]
	if item["type"] != "function_call" || item["call_id"] != "toolu_1" || item["name"] != "read_file" || item["status"] != "completed" {
		t.Fatalf("unexpected function_call item: %#v", item)
	}
	if item["arguments"] != `{"path":"/tmp/a.go"}` {
		t.Fatalf("unexpected function_call arguments: %#v", item["arguments"])
	}
	if resp["output_text"] != "" {
		t.Fatalf("expected empty output_text for function call response, got %#v", resp["output_text"])
	}
}

func TestHandleOpenAIResponsesForwardsFunctionToolsToKiroPayload(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-resp-tools",
		Enabled:     true,
		AccessToken: "token-resp-tools",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var gotTools []KiroToolWrapper
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload KiroPayload
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode kiro payload: %v", err)
			}
			ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
			if ctx != nil {
				gotTools = ctx.Tools
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "tool-ready"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 7, "outputTokens": 3}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{
		"model":"claude-sonnet-4.5",
		"input":"read a file",
		"max_output_tokens":16,
		"tools":[{
			"type":"function",
			"name":"read_file",
			"description":"Read a project file",
			"parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
		}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	if len(gotTools) != 1 {
		t.Fatalf("expected one Kiro tool forwarded from Responses request, got %#v", gotTools)
	}
	if gotTools[0].ToolSpecification.Name != "read_file" {
		t.Fatalf("unexpected Kiro tool name: %#v", gotTools[0].ToolSpecification.Name)
	}
	if gotTools[0].ToolSpecification.Description != "Read a project file" {
		t.Fatalf("unexpected Kiro tool description: %#v", gotTools[0].ToolSpecification.Description)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleOpenAIResponsesConvertsFunctionCallOutputToToolResult(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-resp-tool-output",
		Enabled:     true,
		AccessToken: "token-resp-tool-output",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var gotContent string
	var gotToolResults []KiroToolResult
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload KiroPayload
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode kiro payload: %v", err)
			}
			current := payload.ConversationState.CurrentMessage.UserInputMessage
			gotContent = current.Content
			if current.UserInputMessageContext != nil {
				gotToolResults = current.UserInputMessageContext.ToolResults
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "continued"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 7, "outputTokens": 3}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{
		"model":"claude-sonnet-4.5",
		"input":[
			{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"/tmp/a.go\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"package main\n"}
		],
		"max_output_tokens":16
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(gotContent, toolResultsContinuationPrefix) || !strings.Contains(gotContent, "package main") {
		t.Fatalf("expected current content to include tool result continuation, got %q", gotContent)
	}
	if len(gotToolResults) != 1 {
		t.Fatalf("expected one Kiro tool result, got %#v", gotToolResults)
	}
	if gotToolResults[0].ToolUseID != "call_1" || gotToolResults[0].Content[0].Text != "package main\n" {
		t.Fatalf("unexpected Kiro tool result: %#v", gotToolResults[0])
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleOpenAIResponsesRestoresPreviousResponseSession(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-resp-session",
		Enabled:     true,
		AccessToken: "token-resp-session",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var responseID string
	var secondPayload KiroPayload
	var calls int
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			var payload KiroPayload
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode kiro payload: %v", err)
			}
			if calls == 2 {
				secondPayload = payload
			}
			messages := []testEventStreamMessage{
				{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 7, "outputTokens": 3}}},
			}
			if calls == 1 {
				messages = append([]testEventStreamMessage{
					{eventType: "toolUseEvent", payload: map[string]interface{}{"toolUseId": "call_1", "name": "read_file", "input": map[string]interface{}{"path": "/tmp/a.go"}}},
				}, messages...)
			} else {
				messages = append([]testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "continued"}},
				}, messages...)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(buildTestEventStream(t, messages))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	firstBody := strings.NewReader(`{
		"model":"claude-sonnet-4.5",
		"input":"read the file",
		"max_output_tokens":16,
		"tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}]
	}`)
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/responses", firstBody)
	firstW := httptest.NewRecorder()
	h.ServeHTTP(firstW, firstReq)
	if firstW.Code != http.StatusOK {
		t.Fatalf("expected first status 200, got %d body %s", firstW.Code, firstW.Body.String())
	}
	var firstResp map[string]interface{}
	if err := json.Unmarshal(firstW.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	responseID, _ = firstResp["id"].(string)
	if responseID == "" {
		t.Fatalf("expected response id, got %#v", firstResp)
	}
	session, ok := h.getOpenAIResponsesSession(responseID)
	if !ok {
		t.Fatalf("expected saved response session for %s", responseID)
	}
	if len(session.Messages) == 0 || len(session.Messages[len(session.Messages)-1].ToolCalls) == 0 {
		t.Fatalf("expected first response session to save assistant tool call, got %#v", session.Messages)
	}

	secondBody := strings.NewReader(`{
		"model":"claude-sonnet-4.5",
		"previous_response_id":"` + responseID + `",
		"input":[{"type":"function_call_output","call_id":"call_1","output":"package main\n"}],
		"max_output_tokens":16
	}`)
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/responses", secondBody)
	secondW := httptest.NewRecorder()
	h.ServeHTTP(secondW, secondReq)
	if secondW.Code != http.StatusOK {
		t.Fatalf("expected second status 200, got %d body %s", secondW.Code, secondW.Body.String())
	}
	if calls != 2 {
		t.Fatalf("expected two upstream calls, got %d", calls)
	}
	if len(secondPayload.ConversationState.History) == 0 {
		t.Fatalf("expected restored assistant tool call in history, got %#v", secondPayload.ConversationState.History)
	}
	var restored bool
	for _, msg := range secondPayload.ConversationState.History {
		if msg.AssistantResponseMessage == nil {
			continue
		}
		for _, toolUse := range msg.AssistantResponseMessage.ToolUses {
			if toolUse.ToolUseID == "call_1" && toolUse.Name == "read_file" {
				restored = true
			}
		}
	}
	if !restored {
		t.Fatalf("expected previous response tool call restored in history, got %#v", secondPayload.ConversationState.History)
	}
	waitForAccountRequestCount(t, 2)
}

func TestOpenAIResponsesSessionPrunesExpiredAndOldestEntries(t *testing.T) {
	h := &Handler{responses: make(map[string]responsesSession)}
	now := time.Now()
	h.responses["expired"] = responsesSession{UpdatedAt: now.Add(-2 * time.Hour)}
	for i := 0; i < 130; i++ {
		h.responses[fmt.Sprintf("resp_%03d", i)] = responsesSession{UpdatedAt: now.Add(time.Duration(i) * time.Second)}
	}

	h.responsesMu.Lock()
	h.pruneOpenAIResponsesSessionsLocked(now)
	h.responsesMu.Unlock()

	if _, ok := h.responses["expired"]; ok {
		t.Fatalf("expected expired response session to be pruned")
	}
	if len(h.responses) > maxOpenAIResponsesSessions {
		t.Fatalf("expected at most %d sessions, got %d", maxOpenAIResponsesSessions, len(h.responses))
	}
	if _, ok := h.responses["resp_000"]; ok {
		t.Fatalf("expected oldest session to be pruned")
	}
}

func TestSaveOpenAIResponsesSessionStoresPreviousResponseID(t *testing.T) {
	h := &Handler{responses: make(map[string]responsesSession)}
	req := &OpenAIRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []OpenAIMessage{{Role: "user", Content: "first"}},
	}

	h.saveOpenAIResponsesSession("resp_2", "resp_1", req, "ok", nil)

	session, ok := h.getOpenAIResponsesSession("resp_2")
	if !ok {
		t.Fatalf("expected saved response session")
	}
	if session.PreviousResponseID != "resp_1" {
		t.Fatalf("expected previous response id resp_1, got %q", session.PreviousResponseID)
	}
}

func TestRestoreOpenAIResponsesSessionRestoresPreviousResponseChain(t *testing.T) {
	h := &Handler{responses: make(map[string]responsesSession)}
	h.responses["resp_1"] = responsesSession{
		Messages:  []OpenAIMessage{{Role: "user", Content: "first"}, {Role: "assistant", Content: "first answer"}},
		UpdatedAt: time.Now(),
	}
	h.responses["resp_2"] = responsesSession{
		PreviousResponseID: "resp_1",
		Messages:           []OpenAIMessage{{Role: "user", Content: "second"}, {Role: "assistant", Content: "second answer"}},
		UpdatedAt:          time.Now(),
	}
	req := &OpenAIRequest{Messages: []OpenAIMessage{{Role: "user", Content: "third"}}}

	h.restoreOpenAIResponsesSession(map[string]interface{}{"previous_response_id": "resp_2"}, req)

	got := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		got = append(got, msg.Role+":"+extractOpenAIMessageText(msg.Content))
	}
	want := []string{"user:first", "assistant:first answer", "user:second", "assistant:second answer", "user:third"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected restored chain\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRestoreOpenAIResponsesSessionFiltersLatestToolCallsByCurrentOutputs(t *testing.T) {
	h := &Handler{responses: make(map[string]responsesSession)}
	assistant := OpenAIMessage{Role: "assistant"}
	tc1 := ToolCall{ID: "call_keep", Type: "function"}
	tc1.Function.Name = "read_file"
	tc1.Function.Arguments = `{"path":"a.go"}`
	tc2 := ToolCall{ID: "call_drop", Type: "function"}
	tc2.Function.Name = "bash"
	tc2.Function.Arguments = `{"command":"pwd"}`
	assistant.ToolCalls = []ToolCall{tc1, tc2}
	h.responses["resp_tools"] = responsesSession{
		Messages:  []OpenAIMessage{{Role: "user", Content: "use tools"}, assistant},
		UpdatedAt: time.Now(),
	}
	req := &OpenAIRequest{Messages: []OpenAIMessage{{Role: "tool", ToolCallID: "call_keep", Content: "package main"}}}

	h.restoreOpenAIResponsesSession(map[string]interface{}{"previous_response_id": "resp_tools"}, req)

	var restoredCalls []ToolCall
	for _, msg := range req.Messages {
		if msg.Role == "assistant" {
			restoredCalls = append(restoredCalls, msg.ToolCalls...)
		}
	}
	if len(restoredCalls) != 1 || restoredCalls[0].ID != "call_keep" {
		t.Fatalf("expected only matching tool call restored, got %#v", restoredCalls)
	}
}

func TestHandleClaudeMessagesRejectsTooLongRawToolName(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	body := `{
		"model":"claude-sonnet-4.5",
		"max_tokens":64,
		"tools":[{"name":"` + strings.Repeat("a", 65) + `","description":"too long","input_schema":{"type":"object"}}],
		"messages":[{"role":"user","content":"hello"}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.handleClaudeMessages(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Tool name") || !strings.Contains(rr.Body.String(), "64") {
		t.Fatalf("expected actionable tool-name error, got %s", rr.Body.String())
	}
}

func TestHandleOpenAIResponsesStreamRetriesBeforeFirstEvent(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	for i := 1; i <= 2; i++ {
		if err := config.AddAccount(config.Account{
			ID:          fmt.Sprintf("acct-resp-stream-%d", i),
			Enabled:     true,
			AccessToken: fmt.Sprintf("token-resp-stream-%d", i),
			ProfileArn:  "arn:aws:codewhisperer:profile/test",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		}); err != nil {
			t.Fatalf("add account %d: %v", i, err)
		}
	}
	p.Reload()

	var attempts int
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(`{"message":"try again","reason":"MODEL_RATE_LIMIT"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "stream recovered"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 5, "outputTokens": 2}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","input":"say ok","stream":true,"max_output_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected stream retry to succeed, got status %d body %s", w.Code, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("expected first upstream failure then retry success, got %d attempts", attempts)
	}
	bodyText := w.Body.String()
	if strings.Contains(bodyText, "response.failed") {
		t.Fatalf("expected retry before emitting failure event, got stream %s", bodyText)
	}
	if !strings.Contains(bodyText, "response.created") || !strings.Contains(bodyText, "stream recovered") || !strings.Contains(bodyText, "response.completed") {
		t.Fatalf("expected complete recovered Responses stream, got %s", bodyText)
	}
	waitForAccountRequestCount(t, 1)
	waitForAccountHealthWrite(t, "acct-resp-stream-1")
}

func TestHandleOpenAIResponsesNonRetryableUpstreamErrorPreservesStatusForSub2api(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-resp-quota",
		Enabled:     true,
		AccessToken: "token-resp-quota",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusPaymentRequired,
				Body:       io.NopCloser(strings.NewReader(`{"message":"quota exhausted"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","input":"say ok","max_output_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected sub2api-visible 402, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"billing_error"`) {
		t.Fatalf("expected billing_error body, got %s", w.Body.String())
	}
}

func TestHandleOpenAIChatPayloadGuardTruncatesLargeToolResultBeforeUpstream(t *testing.T) {
	dir, err := os.MkdirTemp("", "kiro-go-openai-chat-tool-trim-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-openai-guard",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var capturedPayload KiroPayload
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedPayload); err != nil {
				t.Fatalf("decode upstream payload: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 10, "outputTokens": 1}}}}))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{
		"model":"claude-sonnet-4.5",
		"messages":[
			{"role":"user","content":"run tool"},
			{"role":"assistant","tool_calls":[{"id":"call_now","type":"function","function":{"name":"read_file","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_now","content":"` + strings.Repeat("x", 1024*1024) + `"}
		],
		"max_tokens":16
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	ctx := capturedPayload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if ctx == nil || len(ctx.ToolResults) != 1 || len(ctx.ToolResults[0].Content) != 1 {
		t.Fatalf("expected captured tool_result, got %#v", ctx)
	}
	if got := ctx.ToolResults[0].Content[0].Text; len(got) >= 1024*1024 || !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncated upstream tool_result, len=%d text prefix=%q", len(got), got[:min(64, len(got))])
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	if logs[0].PayloadOriginalBytes == 0 || !logs[0].PayloadTrimmed {
		t.Fatalf("expected payload trim metadata, got %#v", logs[0])
	}
}

func TestHandleClaudeMessagesLogsKeptAndTrimmedTools(t *testing.T) {
	dir, err := os.MkdirTemp("", "kiro-tool-log-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-tool-log",
		Enabled:     true,
		AccessToken: "token-tool-log",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "ok"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 7, "outputTokens": 2}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	tools := []map[string]interface{}{}
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("mcp__fs__tool_%02d", i)
		if i == 22 {
			name = "task"
		}
		if i == 23 {
			name = "agent"
		}
		tools = append(tools, map[string]interface{}{
			"name":        name,
			"description": strings.Repeat("Large tool description. ", 80),
			"input_schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": strings.Repeat("path ", 80)},
				},
			},
		})
	}
	bodyBytes, err := json.Marshal(map[string]interface{}{
		"model":      "claude-opus-4-7",
		"max_tokens": 16,
		"tools":      tools,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "continue"},
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	entry := logs[0]
	if !containsString(entry.PayloadKeptTools, "agent") || !containsString(entry.PayloadKeptTools, "task") {
		t.Fatalf("expected kept tools to include agent/task, got %#v", entry.PayloadKeptTools)
	}
	if len(entry.PayloadTrimmedTools) == 0 {
		t.Fatalf("expected trimmed tool names in request log, got %#v", entry)
	}
}

func TestHandleOpenAIResponsesPayloadGuardRejectsBeforeAccountSelection(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-responses-guard",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","input":"` + strings.Repeat("x", 1024*1024) + `","max_output_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"invalid_request_error"`) || !strings.Contains(w.Body.String(), "payload remains too large") {
		t.Fatalf("expected OpenAI invalid_request_error payload guard response, got %s", w.Body.String())
	}
	if health := p.GetRuntimeHealth("acct-responses-guard"); health.ActiveConnections != 0 || health.RecentFailures != 0 || health.RecentSuccesses != 0 {
		t.Fatalf("expected no account health changes before selection, got %#v", health)
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	if logs[0].AccountID != "" {
		t.Fatalf("expected no selected account in request log, got %#v", logs[0])
	}
	if logs[0].PayloadOriginalBytes == 0 {
		t.Fatalf("expected payload byte metadata, got %#v", logs[0])
	}
}

func TestHandleOpenAIResponsesPayloadGuardRejectsAfterProfileArnFinalization(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	inputSize := maxKiroCurrentContentBytes - 128*1024
	profileArnPadding := defaultPayloadGuardOptions().HardLimitBytes - inputSize + 128*1024
	if err := config.AddAccount(config.Account{
		ID:          "acct-profile-guard",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/" + strings.Repeat("p", profileArnPadding),
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	input := strings.Repeat("x", inputSize)
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","input":"` + input + `","max_output_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"invalid_request_error"`) || !strings.Contains(w.Body.String(), "ProfileArn") {
		t.Fatalf("expected ProfileArn payload guard response, got %s", w.Body.String())
	}
	if health := p.GetRuntimeHealth("acct-profile-guard"); health.ActiveConnections != 0 || health.RecentFailures != 0 || health.RecentSuccesses != 0 {
		t.Fatalf("expected no account health changes after payload validation error, got %#v", health)
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	if logs[0].AccountID != "acct-profile-guard" {
		t.Fatalf("expected selected account metadata, got %#v", logs[0])
	}
	if logs[0].PayloadFinalBytes <= defaultPayloadGuardOptions().HardLimitBytes {
		t.Fatalf("expected logged final bytes to include oversized ProfileArn payload, got %#v", logs[0])
	}
}

func TestHandleOpenAIResponsesPayloadGuardRunsBeforeTokenRefreshFailure(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	inputSize := maxKiroCurrentContentBytes - 128*1024
	profileArnPadding := defaultPayloadGuardOptions().HardLimitBytes - inputSize + 128*1024
	if err := config.AddAccount(config.Account{
		ID:           "acct-profile-before-refresh",
		Enabled:      true,
		AccessToken:  "expired-token",
		RefreshToken: "bad-refresh-token",
		ProfileArn:   "arn:aws:codewhisperer:profile/" + strings.Repeat("p", profileArnPadding),
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	input := strings.Repeat("x", inputSize)
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","input":"` + input + `","max_output_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected payload guard status 400 before token refresh, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"invalid_request_error"`) || !strings.Contains(w.Body.String(), "ProfileArn") {
		t.Fatalf("expected ProfileArn payload guard response, got %s", w.Body.String())
	}
	if health := p.GetRuntimeHealth("acct-profile-before-refresh"); health.ActiveConnections != 0 || health.RecentFailures != 0 || health.RecentSuccesses != 0 {
		t.Fatalf("expected no account health changes before token refresh failure, got %#v", health)
	}
}

type testEventStreamMessage struct {
	eventType string
	payload   map[string]interface{}
}

func buildTestEventStream(t *testing.T, messages []testEventStreamMessage) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, msg := range messages {
		payload, err := json.Marshal(msg.payload)
		if err != nil {
			t.Fatalf("marshal event payload: %v", err)
		}
		headers := buildTestEventStreamHeader(":event-type", msg.eventType)
		totalLen := 12 + len(headers) + len(payload) + 4
		var prelude [12]byte
		binary.BigEndian.PutUint32(prelude[0:4], uint32(totalLen))
		binary.BigEndian.PutUint32(prelude[4:8], uint32(len(headers)))
		out.Write(prelude[:])
		out.Write(headers)
		out.Write(payload)
		out.Write([]byte{0, 0, 0, 0})
	}
	return out.Bytes()
}

func buildTestEventStreamHeader(name, value string) []byte {
	var out bytes.Buffer
	out.WriteByte(byte(len(name)))
	out.WriteString(name)
	out.WriteByte(7)
	out.WriteByte(byte(len(value) >> 8))
	out.WriteByte(byte(len(value)))
	out.WriteString(value)
	return out.Bytes()
}

func TestOpus47GateLimitsConcurrentWork(t *testing.T) {
	gate := newOpus47Gate(2, 10)
	var running int64
	var maxRunning int64
	started := make(chan struct{}, 5)
	release := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, err := gate.acquire(time.Second)
			if err != nil {
				t.Errorf("acquire gate: %v", err)
				return
			}
			defer done()

			now := atomic.AddInt64(&running, 1)
			for {
				seen := atomic.LoadInt64(&maxRunning)
				if now <= seen || atomic.CompareAndSwapInt64(&maxRunning, seen, now) {
					break
				}
			}
			started <- struct{}{}
			<-release
			atomic.AddInt64(&running, -1)
		}()
	}

	for i := 0; i < 2; i++ {
		<-started
	}
	select {
	case <-started:
		t.Fatalf("expected only two concurrent requests before release")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()

	if got := atomic.LoadInt64(&maxRunning); got > 2 {
		t.Fatalf("expected max concurrency <= 2, got %d", got)
	}
}

func TestValidateClaudeRequestShapeAllowsFinalAssistantTextPrefill(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{
			{Role: "user", Content: "Write one sentence."},
			{Role: "assistant", Content: "The answer is"},
		},
	}

	if msg := validateClaudeRequestShape(req); msg != "" {
		t.Fatalf("expected final assistant text prefill to be accepted, got %q", msg)
	}
}

func TestValidateClaudeRequestShapeRejectsFinalAssistantToolUsePrefill(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{
			{Role: "user", Content: "Use a tool."},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "bash",
					"input": map[string]interface{}{"command": "pwd"},
				},
			}},
		},
	}

	if msg := validateClaudeRequestShape(req); !strings.Contains(msg, "assistant-prefill tool_use") {
		t.Fatalf("expected tool-use prefill rejection, got %q", msg)
	}
}

func TestHandleClaudeNativeWebSearchUsesKiroMCP(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	config.AddAccount(config.Account{
		ID:          "acct-1",
		Enabled:     true,
		AccessToken: "token-1",
		ProfileArn:  "arn:aws:codewhisperer:profile/test-1",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	p.Reload()

	var requestedPath string
	var requestedAuth string
	var requestedMethod string
	var mcpBody map[string]interface{}
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedPath = req.URL.Path
			requestedAuth = req.Header.Get("Authorization")
			requestedMethod = req.Method
			if err := json.NewDecoder(req.Body).Decode(&mcpBody); err != nil {
				t.Fatalf("decode mcp body: %v", err)
			}
			resultText := `{"results":[{"title":"OpenAI News","url":"https://example.com/openai","snippet":"Latest OpenAI update"}],"totalResults":1,"query":"OpenAI latest news today"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"id":"web_search_tooluse_test",
					"jsonrpc":"2.0",
					"result":{"content":[{"type":"text","text":` + strconv.Quote(resultText) + `}],"isError":false}
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{
		"model":"claude-sonnet-4-6",
		"max_tokens":1024,
		"tools":[{"name":"web_search","type":"web_search_20250305","max_uses":2}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: OpenAI latest news today"}]}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected native web_search to succeed, got status %d body %s", w.Code, w.Body.String())
	}
	if requestedMethod != http.MethodPost {
		t.Fatalf("expected MCP POST, got %q", requestedMethod)
	}
	if requestedPath != "/mcp" {
		t.Fatalf("expected MCP path, got %q", requestedPath)
	}
	if requestedAuth != "Bearer token-1" {
		t.Fatalf("expected bearer token auth, got %q", requestedAuth)
	}
	if mcpBody["method"] != "tools/call" {
		t.Fatalf("expected tools/call MCP method, got %#v", mcpBody["method"])
	}
	params, _ := mcpBody["params"].(map[string]interface{})
	args, _ := params["arguments"].(map[string]interface{})
	if got := args["query"]; got != "OpenAI latest news today" {
		t.Fatalf("expected extracted query, got %#v", got)
	}

	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("expected server tool use, result, and text blocks, got %#v", resp.Content)
	}
	if resp.Content[0].Type != "server_tool_use" || resp.Content[0].Name != "web_search" {
		t.Fatalf("expected web_search server_tool_use block, got %#v", resp.Content[0])
	}
	if resp.Content[1].Type != "web_search_tool_result" {
		t.Fatalf("expected web_search_tool_result block, got %#v", resp.Content[1])
	}
	if resp.Content[2].Type != "text" || !strings.Contains(resp.Content[2].Text, "OpenAI News") {
		t.Fatalf("expected text summary with search result title, got %#v", resp.Content[2])
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if config.GetAccounts()[0].RequestCount > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected async account stats update to complete")
}

func TestHandleClaudeNativeWebSearchStreamEmitsServerToolBlocksAndUsage(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{}

	results := &kiroWebSearchResults{Results: []kiroWebSearchResult{{
		Title:   "Kiro docs",
		URL:     "https://kiro.dev/docs",
		Snippet: "Kiro documentation",
	}}}
	w := httptest.NewRecorder()
	h.sendClaudeNativeWebSearchStream(w, "claude-sonnet-4.5", "kiro docs", "web_search_tooluse_test", results, 123, 45)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	frames := parseSSEFrames(t, w.Body.String())
	if len(frames) < 8 {
		t.Fatalf("expected complete web_search SSE frame sequence, got %v body %q", frameEvents(frames), w.Body.String())
	}
	assertFrameEvent(t, frames, 0, "message_start")
	assertNestedNumber(t, frames[0], "message", "usage", "input_tokens", 123)
	assertFrameEvent(t, frames, 1, "content_block_start")
	assertNestedString(t, frames[1], "content_block", "type", "server_tool_use")
	assertNestedString(t, frames[1], "content_block", "name", "web_search")
	assertFrameEvent(t, frames, 3, "content_block_start")
	assertNestedString(t, frames[3], "content_block", "type", "web_search_tool_result")
	assertFrameEvent(t, frames, 5, "content_block_start")
	assertNestedString(t, frames[5], "content_block", "type", "text")
	assertFrameEvent(t, frames, len(frames)-2, "message_delta")
	assertObjectNumber(t, frames[len(frames)-2], "usage", "input_tokens", 123)
	assertObjectNumber(t, frames[len(frames)-2], "usage", "output_tokens", 45)
	assertFrameEvent(t, frames, len(frames)-1, "message_stop")
}

func TestHandleClaudeNativeWebSearchAccepts20260209ToolType(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	config.AddAccount(config.Account{
		ID:          "acct-websearch-20260209",
		Enabled:     true,
		AccessToken: "token-1",
		ProfileArn:  "arn:aws:codewhisperer:profile/test-1",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	p.Reload()

	var mcpCalled bool
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			mcpCalled = true
			resultText := `{"results":[{"title":"New Search","url":"https://example.com","snippet":"ok"}],"query":"latest docs"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"id":"web_search_tooluse_test",
					"jsonrpc":"2.0",
					"result":{"content":[{"type":"text","text":` + strconv.Quote(resultText) + `}],"isError":false}
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{
		"model":"claude-sonnet-4.5",
		"max_tokens":256,
		"tools":[{"name":"web_search","type":"web_search_20260209","max_uses":1}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":"latest docs"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected web_search_20260209 to succeed, got status %d body %s", w.Code, w.Body.String())
	}
	if !mcpCalled {
		t.Fatalf("expected Kiro MCP web search to be called")
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Content) < 2 || resp.Content[0].Type != "server_tool_use" || resp.Content[1].Type != "web_search_tool_result" {
		t.Fatalf("expected Anthropic web search tool blocks, got %#v", resp.Content)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if config.GetAccounts()[0].RequestCount > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected async account stats update to complete")
}

func TestHandleClaudeMessagesMaxTokensZeroReturnsCompatibleNoOutputResponse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{requestLogs: newRequestLogStore(5), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":0,"messages":[{"role":"user","content":"warm cache"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("expected max_tokens stop reason, got %#v", resp)
	}
	if len(resp.Content) != 0 || resp.Usage.OutputTokens != 0 {
		t.Fatalf("expected zero-output response, got %#v", resp)
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 || entries[0].MaxTokensZeroMode != "local_zero_output" {
		t.Fatalf("expected max_tokens=0 request log mode, got %#v", entries)
	}
}

func TestClaudeMaxTokensZeroRecordsCachePrewarmModeForCacheControl(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	body := strings.NewReader(`{
		"model":"claude-opus-4-7",
		"max_tokens":0,
		"system":[{"type":"text","text":"System prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages":[{"role":"user","content":"warm cache"}],
		"tools":[{"name":"read_file","description":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(w, req)
	h.handleClaudeMessages(loggedWriter, loggedReq)
	h.finishRequestLog(ctx, recorder)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one log")
	}
	if logs[0].MaxTokensZeroMode != "cache_prewarm" {
		t.Fatalf("max_tokens zero mode = %q, want cache_prewarm", logs[0].MaxTokensZeroMode)
	}
	if logs[0].CacheCreationInputTokens <= 0 {
		t.Fatalf("expected cache creation estimate, got %#v", logs[0])
	}
}

func TestClaudeMaxTokensZeroCachePrewarmByCacheControlLocation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "system block",
			body: `{
				"model":"claude-opus-4-7",
				"max_tokens":0,
				"system":[{"type":"text","text":"System prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],
				"messages":[{"role":"user","content":"warm cache"}]
			}`,
		},
		{
			name: "tool definition",
			body: `{
				"model":"claude-opus-4-7",
				"max_tokens":0,
				"messages":[{"role":"user","content":"warm cache"}],
				"tools":[{"name":"read_file","description":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]
			}`,
		},
		{
			name: "message content block",
			body: `{
				"model":"claude-opus-4-7",
				"max_tokens":0,
				"messages":[{"role":"user","content":[{"type":"text","text":"warm cache","cache_control":{"type":"ephemeral"}}]}]
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, resp := runClaudeMaxTokensZeroRequest(t, tt.body)
			if entry.MaxTokensZeroMode != "cache_prewarm" {
				t.Fatalf("max_tokens zero mode = %q, want cache_prewarm", entry.MaxTokensZeroMode)
			}
			if entry.CacheCreationInputTokens <= 0 || resp.Usage.CacheCreationInputTokens <= 0 {
				t.Fatalf("expected cache creation estimate, entry=%#v resp=%#v", entry, resp.Usage)
			}
		})
	}
}

func TestClaudeMaxTokensZeroIgnoresNestedCacheControlPayload(t *testing.T) {
	entry, resp := runClaudeMaxTokensZeroRequest(t, `{
		"model":"claude-opus-4-7",
		"max_tokens":0,
		"messages":[{"role":"user","content":[{
			"type":"tool_result",
			"tool_use_id":"toolu_nested",
			"content":{"text":"payload only","cache_control":{"type":"ephemeral"}}
		}]}]
	}`)
	if entry.MaxTokensZeroMode != "local_zero_output" {
		t.Fatalf("max_tokens zero mode = %q, want local_zero_output", entry.MaxTokensZeroMode)
	}
	if entry.CacheCreationInputTokens != 0 || resp.Usage.CacheCreationInputTokens != 0 {
		t.Fatalf("expected no cache creation estimate, entry=%#v resp=%#v", entry, resp.Usage)
	}
}

func runClaudeMaxTokensZeroRequest(t *testing.T, body string) (RequestLogEntry, ClaudeResponse) {
	t.Helper()
	h := &Handler{requestLogs: newRequestLogStore(10), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(w, req)
	h.handleClaudeMessages(loggedWriter, loggedReq)
	h.finishRequestLog(ctx, recorder)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one log, got %#v", logs)
	}
	return logs[0], resp
}

func TestHandleClaudeMessagesOmittedMaxTokensStillRoutesUpstream(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, requestLogs: newRequestLogStore(5), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-omitted-max-tokens",
		Enabled:     true,
		AccessToken: "token-omitted-max-tokens",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var upstreamCalled bool
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "routed upstream"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 5, "outputTokens": 2}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if !upstreamCalled {
		t.Fatalf("expected omitted max_tokens request to route upstream")
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StopReason == "max_tokens" || len(resp.Content) == 0 {
		t.Fatalf("expected upstream response, got %#v", resp)
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 || entries[0].MaxTokensZeroMode != "" {
		t.Fatalf("expected no max_tokens=0 request log mode, got %#v", entries)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeMessagesOmittedMaxTokensWithThinkingRoutesUpstream(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, requestLogs: newRequestLogStore(5), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-omitted-max-tokens-thinking",
		Enabled:     true,
		AccessToken: "token-omitted-max-tokens-thinking",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var upstreamCalled bool
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "reasoningContentEvent", payload: map[string]interface{}{"text": "thinking"}},
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": "routed upstream"}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 5, "outputTokens": 2}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if !upstreamCalled {
		t.Fatalf("expected omitted max_tokens thinking request to route upstream")
	}
	if strings.Contains(w.Body.String(), "max_tokens=0") {
		t.Fatalf("expected no max_tokens=0 validation error, got %s", w.Body.String())
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleCountTokensOmittedMaxTokensWithThinkingDoesNotRejectAsZero(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
	w := httptest.NewRecorder()

	h.handleCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "max_tokens=0") {
		t.Fatalf("expected no max_tokens=0 validation error, got %s", w.Body.String())
	}
}

func TestHandleCountTokensMarksEstimateInHeader(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
	w := httptest.NewRecorder()
	h.handleCountTokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Kiro-Go-Token-Count-Mode"); got != "estimated" {
		t.Fatalf("expected estimated token count header, got %q", got)
	}
}

func TestClaudeCountTokensRecordsEstimatedMode(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
	w := httptest.NewRecorder()
	ctx, req, recorder, _ := h.beginRequestLog(w, req)

	h.handleCountTokens(w, req)
	h.finishRequestLog(ctx, recorder)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	entries := h.ensureRequestLogStore().List(5)
	if len(entries) != 1 || entries[0].CountTokensMode != "estimated" {
		t.Fatalf("expected estimated count-token mode, got %#v", entries)
	}
}

func TestClaudeAssistantTextPrefillRecordsEmulatedMode(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-prefill-mode",
		Enabled:     true,
		AccessToken: "token-prefill-mode",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var upstreamCalled bool
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "assistantResponseEvent", payload: map[string]interface{}{"content": `{"ok":true}`}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 7, "outputTokens": 3}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":64,"messages":[{"role":"user","content":"Return JSON"},{"role":"assistant","content":"{\"ok\":"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !upstreamCalled {
		t.Fatalf("expected assistant prefill request to route upstream")
	}
	entries := h.requestLogs.List(5)
	if len(entries) != 1 || entries[0].AssistantPrefillMode != "emulated_text_prefill" {
		t.Fatalf("expected assistant prefill mode, got %#v", entries)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeNativeWebSearchUsesAccountRegionForMCP(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	config.AddAccount(config.Account{
		ID:          "acct-1",
		Enabled:     true,
		AccessToken: "token-1",
		ProfileArn:  "arn:aws:codewhisperer:ap-southeast-1:123456789012:profile/test-1",
		Region:      "us-east-1",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	p.Reload()

	var requestedHost string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedHost = req.URL.Host
			resultText := `{"results":[{"title":"Regional","url":"https://example.com","snippet":"ok"}],"query":"regional query"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"id":"web_search_tooluse_test",
					"jsonrpc":"2.0",
					"result":{"content":[{"type":"text","text":` + strconv.Quote(resultText) + `}],"isError":false}
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{
		"model":"claude-sonnet-4-6",
		"max_tokens":1024,
		"tools":[{"name":"web_search","type":"web_search_20250305"}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":"regional query"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected native web_search to succeed, got status %d body %s", w.Code, w.Body.String())
	}
	if requestedHost != "q.ap-southeast-1.amazonaws.com" {
		t.Fatalf("expected regional MCP q host, got %q", requestedHost)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeRetriesQuotaFailureOnNextAccount(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	config.AddAccount(config.Account{ID: "acct-2", Enabled: true, AccessToken: "token-2", ProfileArn: "arn:aws:codewhisperer:profile/test-2", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	p.Reload()

	var tokens []string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			tokens = append(tokens, req.Header.Get("Authorization"))
			status := http.StatusOK
			body := ""
			if len(tokens) <= 3 {
				status = http.StatusTooManyRequests
				body = `{"message":"quota exhausted"}`
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected retry to succeed, got status %d body %s", w.Code, w.Body.String())
	}
	if len(tokens) != 4 {
		t.Fatalf("expected three endpoint attempts on first account plus one retry, got %d: %#v", len(tokens), tokens)
	}
	if tokens[0] != "Bearer token-1" || tokens[3] != "Bearer token-2" {
		t.Fatalf("expected retry to switch from acct-1 to acct-2, got %#v", tokens)
	}
	if !p.IsCoolingDown("acct-1", time.Now()) {
		t.Fatalf("expected quota-failed account to enter cooldown")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if config.GetAccounts()[1].RequestCount > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected async account stats update to complete")
}

func TestHandleClaudeWaitsAndRetriesOpus47CapacityLimit(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = time.Second
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			status := http.StatusOK
			body := ""
			if attempts <= 6 {
				status = http.StatusTooManyRequests
				body = `{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 capacity contract must not return 429, body %s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected capacity retries to stop with 503, got status %d body %s", w.Code, w.Body.String())
	}
	if attempts != 4 {
		t.Fatalf("expected four real upstream attempts, got %d attempts", attempts)
	}
	if got := w.Header().Get("X-Kiro-Go-Error-Reason"); got != "attempt_budget_exhausted" {
		t.Fatalf("X-Kiro-Go-Error-Reason = %q, want attempt_budget_exhausted", got)
	}
	if got := w.Header().Get("X-Kiro-Go-Retryable"); got != "true" {
		t.Fatalf("X-Kiro-Go-Retryable = %q, want true", got)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
	if !strings.Contains(w.Body.String(), `"type":"overloaded_error"`) {
		t.Fatalf("expected Claude overloaded_error body, got %s", w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	if logs[0].Attempts > defaultOpus47MaxAttempts {
		t.Fatalf("request log attempts = %d, want <= %d", logs[0].Attempts, defaultOpus47MaxAttempts)
	}
	selected := 0
	for _, attempt := range logs[0].AttemptTrace {
		if attempt.Event == "selected" {
			selected++
		}
	}
	if selected > defaultOpus47MaxAttempts {
		t.Fatalf("request log selected attempts = %d, want <= %d; trace=%#v", selected, defaultOpus47MaxAttempts, logs[0].AttemptTrace)
	}
}

func TestStableAttemptBudgetExhaustionWaitsForClientInsteadOfFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	opusCapacityRetryBudget = time.Second
	sleepForOpusCapacityRetry = func(time.Duration) {}
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body).WithContext(ctx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(w, req)
	waited := time.Since(start)

	if waited < 900*time.Millisecond {
		t.Fatalf("stable attempt-budget wait lasted %s, want content continuity wait", waited)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("Claude stable wait must not emit fallback assistant content: %s", w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.StableDownstreamFallback || entry.StableFallbackReason != "" {
		t.Fatalf("did not expect attempt-budget stable fallback, got %#v", entry)
	}
	if !entry.QueuedForCapacity || entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected content continuity queue wait, got %#v", entry)
	}
	if attempts != defaultOpus47MaxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, defaultOpus47MaxAttempts)
	}
}

func TestStableClaudeAttemptBudgetDoesNotEmitFallbackWhenClientWaits(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	opusCapacityRetryBudget = time.Second
	sleepForOpusCapacityRetry = func(time.Duration) {}
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "Opus 4.7 upstream capacity is temporarily unavailable") {
		t.Fatalf("Claude stable request must not receive final assistant fallback text: %s", w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.StableDownstreamFallback {
		t.Fatalf("did not expect stable fallback to be emitted for Claude Code wait path: %#v", entry)
	}
	if !entry.QueuedForCapacity || entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected content continuity queue wait, got %#v", entry)
	}
}

func TestStableClaudeNoAccountsWaitsForClientInsteadOfFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusServiceUnavailable, time.Second)
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusServiceUnavailable, time.Second)
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(w, req)
	waited := time.Since(start)

	if waited < 900*time.Millisecond {
		t.Fatalf("stable no-accounts wait lasted %s, want content continuity wait", waited)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("Claude no-accounts stable wait must not emit fallback assistant content: %s", w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.StableDownstreamFallback || entry.StableFallbackReason != "" {
		t.Fatalf("did not expect no-accounts stable fallback, got %#v", entry)
	}
	if !entry.QueuedForCapacity || entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected content continuity queue wait, got %#v", entry)
	}
}

func TestStableClaudeNoAccountsStreamWaitsForClientInsteadOfFallbackSSE(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusServiceUnavailable, time.Second)
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusServiceUnavailable, time.Second)
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(w, req)
	waited := time.Since(start)

	if waited < 900*time.Millisecond {
		t.Fatalf("stable stream no-accounts wait lasted %s, want content continuity wait", waited)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: ping") {
		t.Fatalf("Claude stream no-accounts stable wait must emit heartbeat ping: %s", body)
	}
	if strings.Contains(body, "message_start") || strings.Contains(body, "message_stop") || strings.Contains(body, "content_block_delta") {
		t.Fatalf("Claude stream no-accounts stable wait must not emit assistant message frames: %s", body)
	}
	if strings.Contains(body, "Opus 4.7 upstream capacity") || strings.Contains(body, "kiro_go_stable_fallback") {
		t.Fatalf("Claude stream no-accounts stable wait must not emit fallback assistant text: %s", body)
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.StableDownstreamFallback || entry.StableFallbackReason != "" {
		t.Fatalf("did not expect no-accounts stable fallback, got %#v", entry)
	}
	if !entry.QueuedForCapacity || entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected content continuity queue wait, got %#v", entry)
	}
}

func TestStableClaudeStreamCapacityWaitSendsPingBeforeMessageStart(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	cfg.ContentContinuity.StreamHeartbeatSeconds = 1
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusServiceUnavailable, time.Second)
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusServiceUnavailable, time.Second)
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	h := &Handler{pool: &pool.AccountPool{}, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: ping") {
		t.Fatalf("stable stream wait must send heartbeat ping before capacity recovers, body=%q", body)
	}
	if strings.Contains(body, "message_start") || strings.Contains(body, "message_stop") || strings.Contains(body, "content_block_delta") {
		t.Fatalf("stable stream wait must not start or finish assistant message without real upstream content, body=%q", body)
	}
	if strings.Contains(body, "Opus 4.7 upstream capacity") || strings.Contains(body, "kiro_go_stable_fallback") {
		t.Fatalf("stable stream wait must not emit fallback assistant text, body=%q", body)
	}
}

func TestStableClaudeStreamUpstreamFirstTokenWaitSendsPing(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.StreamHeartbeatSeconds = 1
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	oldClient := kiroHttpStore.Load()
	t.Cleanup(func() {
		kiroHttpStore.Store(oldClient)
	})
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			reader, writer := io.Pipe()
			go func() {
				<-req.Context().Done()
				_ = writer.CloseWithError(req.Context().Err())
			}()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       reader,
				Header:     make(http.Header),
			}, nil
		}),
	})

	h := &Handler{pool: &pool.AccountPool{}, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()
	payload := &KiroPayload{}
	payload.ConversationState.CurrentMessage.UserInputMessage.ModelID = "claude-opus-4.7"
	payload.ConversationState.CurrentMessage.UserInputMessage.Content = "hello"
	account := &config.Account{ID: "acct-1", AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1"}

	done := make(chan struct{}, 1)
	go func() {
		_, _ = h.handleClaudeStreamAttempt(w, req, account, payload, "claude-opus-4.7", false, claudeThinkingResponseOptions{}, 1, promptCacheUsage{}, nil, nil, 0)
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("handleClaudeStreamAttempt did not return after request context cancellation")
	}

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: ping") {
		t.Fatalf("stable upstream first-token wait must send heartbeat ping: %s", body)
	}
	if strings.Contains(body, "message_start") || strings.Contains(body, "message_stop") || strings.Contains(body, "content_block_delta") {
		t.Fatalf("stable upstream first-token wait must not emit assistant frames before real upstream content: %s", body)
	}
	if strings.Contains(body, "Opus 4.7 upstream capacity") || strings.Contains(body, "kiro_go_stable_fallback") {
		t.Fatalf("stable upstream first-token wait must not emit fallback assistant text: %s", body)
	}
}

func TestHandleClaudeWaitsAndRetriesOpus47RateLimit(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = time.Second
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			status := http.StatusOK
			body := ""
			if attempts == 1 {
				status = http.StatusTooManyRequests
				body = `{"message":"Too many requests, please wait before trying again.","retry_after_seconds":0}`
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected rate-limit retry to recover, got status %d body %s", w.Code, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("expected one rate-limit retry then success, got %d attempts", attempts)
	}
	if len(sleeps) != 1 {
		t.Fatalf("expected one wait for rate-limit response, got %d", len(sleeps))
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeWaitsForShortPoolRateLimitCooldown(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()
	p.RecordFailureUntil("acct-1", pool.FailureReasonRateLimited, time.Now().Add(20*time.Millisecond))

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = time.Second
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
		time.Sleep(d)
	}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})

	body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected pool wait to recover, got status %d body %s", w.Code, w.Body.String())
	}
	if attempts != 1 {
		t.Fatalf("expected one upstream attempt after pool wait, got %d", attempts)
	}
	if len(sleeps) != 1 {
		t.Fatalf("expected one pool wait, got %d", len(sleeps))
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeDoesNotWaitForTemporaryLimitedPoolCooldown(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()
	p.RecordFailureUntil("acct-1", pool.FailureReasonTemporaryLimited, time.Now().Add(time.Minute))

	oldSleep := sleepForOpusCapacityRetry
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() {
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 temporary limit contract must not return 429, body %s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected temporary limit to return 503, got status %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Kiro-Go-Error-Reason"); got != "TEMPORARY_LIMITED" {
		t.Fatalf("expected TEMPORARY_LIMITED header, got %q", got)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
	if len(sleeps) != 0 {
		t.Fatalf("expected no wait for temporary-limited pool cooldown, got %d", len(sleeps))
	}
}

func TestHandleClaudeOpus47RateLimitTriesNextAccountBeforeWaiting(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	for i := 1; i <= 2; i++ {
		if err := config.AddAccount(config.Account{ID: fmt.Sprintf("acct-%d", i), Enabled: true, AccessToken: fmt.Sprintf("token-%d", i), ProfileArn: fmt.Sprintf("arn:aws:codewhisperer:profile/test-%d", i), ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()

	oldSleep := sleepForOpusCapacityRetry
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() {
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	var tokens []string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			tokens = append(tokens, req.Header.Get("Authorization"))
			status := http.StatusOK
			body := ""
			if len(tokens) == 1 {
				status = http.StatusTooManyRequests
				body = `{"message":"Too many requests, please wait before trying again.","retry_after_seconds":5}`
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected retry on next account to recover, got status %d body %s", w.Code, w.Body.String())
	}
	if len(sleeps) != 0 {
		t.Fatalf("expected next available account before waiting, got sleeps %#v", sleeps)
	}
	if len(tokens) != 2 || tokens[0] != "Bearer token-1" || tokens[1] != "Bearer token-2" {
		t.Fatalf("expected first account then second account, got %#v", tokens)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeOpus47StopsAtRequestAttemptBudget(t *testing.T) {
	h, upstreamHits := newOpus47RetryBudgetTestHandler(t, 8, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`))
	})

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = 25 * time.Second
	sleepForOpusCapacityRetry = func(time.Duration) {}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 pressure contract must not return 429, body=%s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", w.Code, w.Body.String())
	}
	if *upstreamHits != 4 {
		t.Fatalf("upstream hits = %d, want 4 attempt budget", *upstreamHits)
	}
	if got := w.Header().Get("X-Kiro-Go-Retryable"); got != "true" {
		t.Fatalf("X-Kiro-Go-Retryable = %q, want true", got)
	}
	if got := w.Header().Get("X-Kiro-Go-Error-Reason"); got != "attempt_budget_exhausted" {
		t.Fatalf("X-Kiro-Go-Error-Reason = %q, want attempt_budget_exhausted", got)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
}

func TestHandleClaudeOpus47RequestAttemptBudgetIgnoresPoolRecoveryWaits(t *testing.T) {
	h, upstreamHits := newOpus47RetryBudgetTestHandler(t, 1, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`))
	})

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = 25 * time.Second
	sleepForOpusCapacityRetry = func(d time.Duration) {
		h.pool.RecordSuccess("acct-1")
	}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 pressure contract must not return 429, body=%s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", w.Code, w.Body.String())
	}
	if *upstreamHits != 4 {
		t.Fatalf("upstream hits = %d, want 4 real upstream attempts despite pool waits", *upstreamHits)
	}
}

func TestHandleClaudeOpus47PressureHeadersSurviveRateLimitLastError(t *testing.T) {
	h, upstreamHits := newOpus47RetryBudgetTestHandler(t, 8, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"Too many requests, please wait before trying again.","retry_after_seconds":30}`))
	})

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = 25 * time.Second
	sleepForOpusCapacityRetry = func(time.Duration) {}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 pressure contract must not return 429, body=%s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", w.Code, w.Body.String())
	}
	if *upstreamHits != 4 {
		t.Fatalf("upstream hits = %d, want 4 attempt budget", *upstreamHits)
	}
	if got := w.Header().Get("X-Kiro-Go-Error-Reason"); got != "attempt_budget_exhausted" {
		t.Fatalf("X-Kiro-Go-Error-Reason = %q, want attempt_budget_exhausted", got)
	}
	if got := w.Header().Get("X-Kiro-Go-Retryable"); got != "true" {
		t.Fatalf("X-Kiro-Go-Retryable = %q, want true", got)
	}
	if got := w.Header().Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("Retry-After = %q, want positive value", got)
	}
}

func TestHandleClaudeRateLimitKeepsTryingUnusedAccounts(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	for i := 1; i <= 3; i++ {
		if err := config.AddAccount(config.Account{ID: fmt.Sprintf("acct-%d", i), Enabled: true, AccessToken: fmt.Sprintf("token-%d", i), ProfileArn: fmt.Sprintf("arn:aws:codewhisperer:profile/test-%d", i), ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()

	var tokens []string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			tokens = append(tokens, req.Header.Get("Authorization"))
			status := http.StatusOK
			body := ""
			if len(tokens) <= 2 {
				status = http.StatusTooManyRequests
				body = `{"message":"Too many requests, please wait before trying again.","reason":null}`
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected third account to recover after two 429s, got status %d body %s tokens %#v", w.Code, w.Body.String(), tokens)
	}
	if len(tokens) != 3 {
		t.Fatalf("expected three account attempts, got %d: %#v", len(tokens), tokens)
	}
	if tokens[0] != "Bearer token-1" || tokens[1] != "Bearer token-2" || tokens[2] != "Bearer token-3" {
		t.Fatalf("expected attempts to advance across unused accounts, got %#v", tokens)
	}
	waitForAccountRequestCount(t, 1)
}

func newOpus47RetryBudgetTestHandler(t *testing.T, accounts int, upstream http.HandlerFunc) (*Handler, *int) {
	t.Helper()
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	hits := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			hits++
			upstream(recorder, req)
			return recorder.Result(), nil
		}),
	})

	p := &pool.AccountPool{}
	for i := 0; i < accounts; i++ {
		id := fmt.Sprintf("acct-%d", i+1)
		if err := config.AddAccount(config.Account{
			ID:          id,
			Email:       id + "@example.test",
			Enabled:     true,
			AccessToken: "token-" + id,
			ProfileArn:  fmt.Sprintf("arn:aws:codewhisperer:profile/test-%d", i+1),
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
			Region:      "us-east-1",
		}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()
	for i := 0; i < accounts; i++ {
		p.SetModelList(fmt.Sprintf("acct-%d", i+1), []string{"claude-opus-4.7"})
	}

	h := &Handler{
		pool:        p,
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}
	return h, &hits
}

func TestHandleClaudeOpus47AdmissionGateLimitsUpstreamConcurrency(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	for i := 0; i < 5; i++ {
		if err := config.AddAccount(config.Account{ID: fmt.Sprintf("acct-%d", i), Enabled: true, AccessToken: fmt.Sprintf("token-%d", i), ProfileArn: "arn:aws:codewhisperer:profile/test", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()

	oldGate := opus47AdmissionGate
	opus47AdmissionGate = newOpus47Gate(2, 10)
	t.Cleanup(func() {
		opus47AdmissionGate = oldGate
		InitKiroHttpClient("")
	})

	var upstreamRunning int64
	var maxUpstreamRunning int64
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			now := atomic.AddInt64(&upstreamRunning, 1)
			for {
				seen := atomic.LoadInt64(&maxUpstreamRunning)
				if now <= seen || atomic.CompareAndSwapInt64(&maxUpstreamRunning, seen, now) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&upstreamRunning, -1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
			w := httptest.NewRecorder()
			h.handleClaudeMessagesInternal(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d body %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&maxUpstreamRunning); got > 2 {
		t.Fatalf("expected upstream concurrency <= 2, got %d", got)
	}
	waitForAccountRequestCount(t, 5)
}

func TestHandleClaudeGeneralModelAdmissionGateLimitsUpstreamConcurrency(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}
	if err := config.UpdateModelAdmissionConfig(config.ModelAdmissionConfig{
		Default: config.ModelAdmissionRule{MaxConcurrent: 2, MaxWaiting: 10},
	}); err != nil {
		t.Fatalf("update model admission config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	for i := 0; i < 5; i++ {
		if err := config.AddAccount(config.Account{ID: fmt.Sprintf("acct-general-%d", i), Enabled: true, AccessToken: fmt.Sprintf("token-general-%d", i), ProfileArn: "arn:aws:codewhisperer:profile/test", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()

	oldGate := modelAdmissionGate
	applyModelAdmissionConfig()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		InitKiroHttpClient("")
	})

	var upstreamRunning int64
	var maxUpstreamRunning int64
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			now := atomic.AddInt64(&upstreamRunning, 1)
			for {
				seen := atomic.LoadInt64(&maxUpstreamRunning)
				if now <= seen || atomic.CompareAndSwapInt64(&maxUpstreamRunning, seen, now) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&upstreamRunning, -1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
			w := httptest.NewRecorder()
			h.handleClaudeMessagesInternal(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d body %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&maxUpstreamRunning); got > 2 {
		t.Fatalf("expected upstream concurrency <= 2 for general model, got %d", got)
	}
	waitForAccountRequestCount(t, 5)
}

func TestModelAdmissionGateAdaptivePressureTemporarilyReducesConcurrency(t *testing.T) {
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Default: config.ModelAdmissionRule{MaxConcurrent: 3, MaxWaiting: 10},
	})
	gate.recordPressure("claude-sonnet-4.5", 503, 5*time.Second)
	gate.recordPressure("claude-sonnet-4.5", 503, 5*time.Second)

	release, gated, err := gate.acquire("claude-sonnet-4.5", time.Second)
	if err != nil || !gated {
		t.Fatalf("expected first adaptive acquire, gated=%v err=%v", gated, err)
	}
	defer release()

	_, gated, err = gate.acquire("claude-sonnet-4.5", 10*time.Millisecond)
	if !gated {
		t.Fatalf("expected adaptive gate to apply")
	}
	if err == nil {
		t.Fatalf("expected adaptive pressure to reduce concurrency to one slot")
	}
}

func TestModelAdmissionGatePressureHonorsRetryAfter(t *testing.T) {
	now := time.Unix(100, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Default: config.ModelAdmissionRule{MaxConcurrent: 3, MaxWaiting: 10},
	})
	gate.now = func() time.Time { return now }
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(2*time.Minute))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(2*time.Minute))

	now = now.Add(45 * time.Second)
	if !gate.hasPressure("claude-opus-4.7") {
		t.Fatalf("expected pressure to remain active for upstream retry-after window")
	}

	now = now.Add(2 * time.Minute)
	if gate.hasPressure("claude-opus-4.7") {
		t.Fatalf("expected pressure to expire after retry-after window")
	}
}

func TestModelAdmissionCircuitOpensAfterRepeatedCapacityPressure(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))

	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "open" {
		t.Fatalf("CircuitState = %q, want open; snap=%#v", snap.CircuitState, snap)
	}
	if snap.RetryAfterSeconds < 40 || snap.RetryAfterSeconds > 45 {
		t.Fatalf("RetryAfterSeconds = %d, want around 45", snap.RetryAfterSeconds)
	}
	if snap.EffectiveMaxConcurrent != 1 {
		t.Fatalf("EffectiveMaxConcurrent = %d, want 1", snap.EffectiveMaxConcurrent)
	}
	if !snap.Active {
		t.Fatalf("expected active pressure snapshot")
	}
}

func TestModelAdmissionCircuitHalfOpenAfterRetryAfter(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))

	now = now.Add(11 * time.Second)
	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "half_open" {
		t.Fatalf("CircuitState = %q, want half_open; snap=%#v", snap.CircuitState, snap)
	}
	if snap.RetryAfterSeconds != 0 {
		t.Fatalf("RetryAfterSeconds = %d, want 0", snap.RetryAfterSeconds)
	}
}

func TestModelAdmissionCircuitSuccessClosesAfterHalfOpen(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	now = now.Add(11 * time.Second)

	gate.recordSuccess("claude-opus-4.7", time.Second)
	gate.recordSuccess("claude-opus-4.7", time.Second)

	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "closed" {
		t.Fatalf("CircuitState = %q, want closed; snap=%#v", snap.CircuitState, snap)
	}
	if snap.Score != 0 {
		t.Fatalf("Score = %d, want 0", snap.Score)
	}
	if snap.EffectiveMaxConcurrent != 4 {
		t.Fatalf("EffectiveMaxConcurrent = %d, want restored max 4", snap.EffectiveMaxConcurrent)
	}
}

func TestModelAdmissionCircuitHalfOpenRequiresTwoSuccessesAtScoreFour(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	now = now.Add(11 * time.Second)

	gate.recordSuccess("claude-opus-4.7", time.Second)
	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "half_open" {
		t.Fatalf("CircuitState after first success = %q, want half_open; snap=%#v", snap.CircuitState, snap)
	}
	if snap.Score != 4 {
		t.Fatalf("Score after first success = %d, want 4", snap.Score)
	}

	gate.recordSuccess("claude-opus-4.7", time.Second)
	snap = gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "closed" {
		t.Fatalf("CircuitState after second success = %q, want closed; snap=%#v", snap.CircuitState, snap)
	}
}

func TestModelAdmissionCircuitKeepsActivePressureForRetainedRetryAt(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	retryAt := now.Add(2 * time.Minute)
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, retryAt)
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, retryAt)

	now = now.Add(45 * time.Second)
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, time.Time{})

	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "open" {
		t.Fatalf("CircuitState = %q, want open; snap=%#v", snap.CircuitState, snap)
	}
	if !snap.Active {
		t.Fatalf("expected active pressure while retained retryAt is in the future; snap=%#v", snap)
	}
	if !gate.hasPressure("claude-opus-4.7") {
		t.Fatalf("expected hasPressure while retained retryAt is in the future")
	}
	if snap.ExpiresAt.Before(retryAt) {
		t.Fatalf("ExpiresAt = %v, want not before retryAt %v", snap.ExpiresAt, retryAt)
	}
}

func TestModelAdmissionCircuitReopensAfterFailedHalfOpenProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	now = now.Add(11 * time.Second)

	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "half_open" {
		t.Fatalf("CircuitState before failed probe = %q, want half_open; snap=%#v", snap.CircuitState, snap)
	}

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, time.Time{})
	snap = gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "open" {
		t.Fatalf("CircuitState after failed probe = %q, want open; snap=%#v", snap.CircuitState, snap)
	}
	if snap.RetryAfterSeconds <= 0 {
		t.Fatalf("RetryAfterSeconds = %d, want > 0; snap=%#v", snap.RetryAfterSeconds, snap)
	}
	if !snap.Active {
		t.Fatalf("expected active pressure after failed half-open probe; snap=%#v", snap)
	}
	if !gate.hasPressure("claude-opus-4.7") {
		t.Fatalf("expected hasPressure after failed half-open probe")
	}
}

func TestAdminAdmissionPressureEndpointReportsActivePressure(t *testing.T) {
	oldGate := modelAdmissionGate
	now := time.Unix(100, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Default: config.ModelAdmissionRule{MaxConcurrent: 3, MaxWaiting: 10},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(2*time.Minute))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(2*time.Minute))
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
	})

	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/admission-pressure", nil)
	w := httptest.NewRecorder()
	h.apiGetAdmissionPressure(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Pressure []AdmissionPressureSnapshot `json:"pressure"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Pressure) != 1 {
		t.Fatalf("expected one pressure snapshot, got %#v", resp)
	}
	got := resp.Pressure[0]
	if got.Model != "claude-opus-4-7" || got.Score < 4 || !got.Active || !got.ReducedConcurrency {
		t.Fatalf("unexpected pressure snapshot: %#v", got)
	}
	if got.ExpiresAt.IsZero() || got.ExpiresInMs <= 0 {
		t.Fatalf("expected expiry metadata, got %#v", got)
	}
}

func TestAcquireAdmissionGatesStreamByDefault(t *testing.T) {
	oldGate := modelAdmissionGate
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 0},
		},
	})
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
	})

	held, gated, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("pre-acquire gate: gated=%v err=%v", gated, err)
	}
	defer held()

	h := &Handler{}
	w := httptest.NewRecorder()
	release, ok := h.acquireOpus47Admission(w, "claude-opus-4.7", true, true, time.Now().Add(10*time.Millisecond))
	if ok {
		release()
		t.Fatalf("expected stream to be gated by default")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for stream gate timeout, got %d body %s", w.Code, w.Body.String())
	}
}

func TestStableAdmissionPressureWaitsForClientInsteadOfFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	h := NewHandler()
	waitCtx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(waitCtx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(rr, req)

	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	release, _, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil {
		t.Fatalf("pre-acquire model gate: %v", err)
	}
	defer release()

	start := time.Now()
	releaseReq, ok := h.acquireOpus47AdmissionForRequest(loggedWriter, loggedReq, "claude-opus-4.7", false, true, time.Now().Add(30*time.Millisecond))
	if ok {
		releaseReq()
		t.Fatalf("expected admission wait to stop only after client cancellation")
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected stable continuity wait to ignore short request budget, waited %s", time.Since(start))
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("Claude admission pressure wait must not emit fallback assistant content: %s", rr.Body.String())
	}
	h.finishRequestLog(ctx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	entry := logs[0]
	if !entry.QueuedForCapacity {
		t.Fatalf("expected queuedForCapacity")
	}
	if entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected positive CapacityQueueWaitMs")
	}
	if entry.StableDownstreamFallback || entry.StableFallbackReason != "" {
		t.Fatalf("did not expect stable admission fallback, got %#v", entry)
	}
}

func TestStableAdmissionPressureStreamWaitSendsPingInsteadOfFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	cfg.ContentContinuity.StreamHeartbeatSeconds = 1
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	h := NewHandler()
	waitCtx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(waitCtx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(rr, req)

	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	release, _, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil {
		t.Fatalf("pre-acquire model gate: %v", err)
	}
	defer release()

	releaseReq, ok := h.acquireOpus47AdmissionForRequest(loggedWriter, loggedReq, "claude-opus-4.7", true, true, time.Now().Add(30*time.Millisecond))
	if ok {
		releaseReq()
		t.Fatalf("expected stream admission wait to stop only after client cancellation")
	}
	h.finishRequestLog(ctx, recorder)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: ping") {
		t.Fatalf("stable admission stream wait must send heartbeat ping: %s", body)
	}
	if strings.Contains(body, "message_start") || strings.Contains(body, "message_stop") || strings.Contains(body, "content_block_delta") {
		t.Fatalf("stable admission stream wait must not emit assistant frames: %s", body)
	}
	if strings.Contains(body, "Opus 4.7 upstream capacity") || strings.Contains(body, "kiro_go_stable_fallback") {
		t.Fatalf("stable admission stream wait must not emit fallback assistant text: %s", body)
	}
}

func TestStableAdmissionPressureResumesAfterCapacityBroadcast(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(rr, req)

	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	held, _, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil {
		t.Fatalf("pre-acquire model gate: %v", err)
	}

	done := make(chan struct {
		release func()
		ok      bool
	}, 1)
	go func() {
		releaseReq, ok := h.acquireOpus47AdmissionForRequest(loggedWriter, loggedReq, "claude-opus-4.7", false, true, time.Now().Add(200*time.Millisecond))
		done <- struct {
			release func()
			ok      bool
		}{release: releaseReq, ok: ok}
	}()

	time.Sleep(20 * time.Millisecond)
	held()
	contentContinuityGateGlobal.broadcast("claude-opus-4.7")

	select {
	case got := <-done:
		if !got.ok {
			t.Fatalf("expected admission to resume without stable fallback; body=%s", rr.Body.String())
		}
		got.release()
	case <-time.After(time.Second):
		t.Fatalf("admission did not resume after capacity broadcast")
	}
	h.finishRequestLog(ctx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	if logs[0].StableDownstreamFallback {
		t.Fatalf("did not expect stable fallback after capacity recovery: %#v", logs[0])
	}
	if !logs[0].QueuedForCapacity {
		t.Fatalf("expected queuedForCapacity after continuity wait")
	}
}

func TestHandlerClaudeSessionGovernorRunsBeforeModelAdmission(t *testing.T) {
	cfg := testClaudeCodeGovernorConfig()
	h := &Handler{
		governor:    newClaudeCodeConcurrencyGovernor(cfg),
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}

	first, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-2",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-3")
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, _ := h.beginRequestLog(rr, req)

	release, ok := h.acquireClaudeCodeSessionAdmissionForRequest(loggedReq, "claude-opus-4.7", true, true, time.Now().Add(20*time.Millisecond))
	if ok {
		release()
		t.Fatalf("expected session governor to reject queued subagent")
	}
	h.finishRequestLog(ctx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	if logs[0].GovernorDecision != "session_governor_rejected" {
		t.Fatalf("GovernorDecision = %q, want session_governor_rejected", logs[0].GovernorDecision)
	}
	if logs[0].GovernorWaitReason == "" {
		t.Fatalf("expected GovernorWaitReason to record rejection reason")
	}
}

func TestHandlerClaudeSessionGovernorRejectionWritesStableResponse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := testClaudeCodeGovernorConfig()
	cfg.QueueMaxDepth = -1
	h := &Handler{
		governor:    newClaudeCodeConcurrencyGovernor(cfg),
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}

	first, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-2",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	body := `{"model":"claude-opus-4.7","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"say ok"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-3")
	w := httptest.NewRecorder()

	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(w, req)
	h.handleClaudeMessages(loggedWriter, loggedReq)
	h.finishRequestLog(ctx, recorder)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 stable fallback; body=%s", w.Code, w.Body.String())
	}
	if strings.TrimSpace(w.Body.String()) == "" {
		t.Fatalf("expected non-empty response body")
	}
	if got := w.Header().Get("X-Kiro-Go-Stable-Fallback"); got != "true" {
		t.Fatalf("X-Kiro-Go-Stable-Fallback = %q, want true; body=%s", got, w.Body.String())
	}
	if got := w.Header().Get("X-Kiro-Go-Internal-Reason"); got != "admission_pressure" {
		t.Fatalf("X-Kiro-Go-Internal-Reason = %q, want admission_pressure", got)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response body is not valid JSON: %v body=%s", err, w.Body.String())
	}
	if resp["type"] != "message" || resp["role"] != "assistant" {
		t.Fatalf("expected Claude message fallback, got %#v", resp)
	}
	if strings.Contains(w.Body.String(), `"type":"error"`) {
		t.Fatalf("stable rejection fallback must not be an error envelope: %s", w.Body.String())
	}

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	if logs[0].GovernorDecision != "session_governor_rejected" {
		t.Fatalf("GovernorDecision = %q, want session_governor_rejected", logs[0].GovernorDecision)
	}
	if !logs[0].StableDownstreamFallback {
		t.Fatalf("expected stable fallback log entry: %#v", logs[0])
	}
}

func TestOpenAIResponsesStableSessionGovernorRejectionReturnsResponsesFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := testClaudeCodeGovernorConfig()
	cfg.QueueMaxDepth = -1
	h := &Handler{
		governor:    newClaudeCodeConcurrencyGovernor(cfg),
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
		responses:   make(map[string]responsesSession),
	}

	first, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-2",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	body := `{"model":"claude-opus-4.7","stream":false,"max_output_tokens":16,"input":"say ok"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sub2api/1.0")
	req.Header.Set("X-Sub2API-Request", "uat")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-3")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 stable fallback; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Kiro-Go-Stable-Fallback"); got != "true" {
		t.Fatalf("X-Kiro-Go-Stable-Fallback = %q, want true; body=%s", got, w.Body.String())
	}
	if got := w.Header().Get("X-Kiro-Go-Internal-Reason"); got != "session_governor_rejected" {
		t.Fatalf("X-Kiro-Go-Internal-Reason = %q, want session_governor_rejected", got)
	}
	if !strings.Contains(w.Body.String(), `"object":"response"`) {
		t.Fatalf("expected OpenAI Responses fallback object, got %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "chat.completion") {
		t.Fatalf("responses governor fallback must not return chat.completion shape: %s", w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response body is not valid JSON: %v body=%s", err, w.Body.String())
	}
	if resp["object"] != "response" {
		t.Fatalf("object = %#v, want response; body=%s", resp["object"], w.Body.String())
	}

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	if logs[0].GovernorDecision != "session_governor_rejected" {
		t.Fatalf("GovernorDecision = %q, want session_governor_rejected", logs[0].GovernorDecision)
	}
	if !logs[0].StableDownstreamFallback {
		t.Fatalf("expected stable fallback log entry: %#v", logs[0])
	}
}

func TestHandlerClaudeSessionGovernorAllowsInteractiveWhenSubagentsFull(t *testing.T) {
	cfg := testClaudeCodeGovernorConfig()
	h := &Handler{
		governor:    newClaudeCodeConcurrencyGovernor(cfg),
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}

	first, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:         "claude-opus-4.7",
		SessionID:     "session-1",
		ParentAgentID: "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, _ := h.beginRequestLog(rr, req)

	release, ok := h.acquireClaudeCodeSessionAdmissionForRequest(loggedReq, "claude-opus-4.7", true, true, time.Now().Add(20*time.Millisecond))
	if !ok {
		t.Fatalf("expected interactive request to acquire when subagents are full")
	}
	release()
	h.finishRequestLog(ctx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	if logs[0].GovernorDecision != "session_governor_admitted_interactive" {
		t.Fatalf("GovernorDecision = %q, want session_governor_admitted_interactive", logs[0].GovernorDecision)
	}
}

func TestAcquireAdmissionCanBypassStreamWhenConfigured(t *testing.T) {
	oldGate := modelAdmissionGate
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		StreamBypass: true,
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 2, MaxWaiting: 0},
		},
	})
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
	})

	h := &Handler{}
	w := httptest.NewRecorder()
	release, ok := h.acquireOpus47Admission(w, "claude-opus-4.7", true, true, time.Now().Add(10*time.Millisecond))
	if !ok {
		t.Fatalf("expected stream without pressure to bypass admission when configured, got status %d body %s", w.Code, w.Body.String())
	}
	release()

	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, time.Second)
	held, gated, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("pre-acquire pressured gate: gated=%v err=%v", gated, err)
	}
	defer held()

	w = httptest.NewRecorder()
	release, ok = h.acquireOpus47Admission(w, "claude-opus-4.7", true, true, time.Now().Add(10*time.Millisecond))
	if ok {
		release()
		t.Fatalf("expected pressured stream to be gated and time out")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for pressured stream gate timeout, got %d body %s", w.Code, w.Body.String())
	}
}

func TestAcquireAdmissionFastRejectsOpenOpusCircuit(t *testing.T) {
	oldGate := modelAdmissionGate
	now := time.Unix(1000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
	})

	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	p := pool.GetPool()
	for i := 1; i <= defaultOpus47MaxAttempts; i++ {
		id := fmt.Sprintf("acct-%d", i)
		if err := config.AddAccount(config.Account{ID: id, Enabled: true, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()
	for i := 1; i <= defaultOpus47MaxAttempts; i++ {
		id := fmt.Sprintf("acct-%d", i)
		p.SetModelList(id, []string{"claude-opus-4.7"})
		p.RecordFailureUntil(id, pool.FailureReasonTemporaryLimited, time.Now().Add(time.Minute))
	}
	h := &Handler{pool: p}
	w := httptest.NewRecorder()
	release, ok := h.acquireOpus47Admission(w, "claude-opus-4.7", true, true, time.Now().Add(time.Second))

	if ok {
		release()
		t.Fatalf("expected open circuit to reject before upstream attempt")
	}
	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 open circuit contract must not return 429, body %s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for open circuit, got %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
	if got := w.Header().Get("X-Kiro-Go-Circuit-State"); got != "open" {
		t.Fatalf("expected open circuit header, got %q", got)
	}
	if !strings.Contains(w.Body.String(), "circuit is open") {
		t.Fatalf("expected circuit-open message, got %s", w.Body.String())
	}
}

func TestStableOpenCircuitWaitsForClientInsteadOfFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	now := time.Unix(1000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	p := pool.GetPool()
	for i := 1; i <= defaultOpus47MaxAttempts; i++ {
		id := fmt.Sprintf("stable-acct-%d", i)
		if err := config.AddAccount(config.Account{ID: id, Enabled: true, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()
	for i := 1; i <= defaultOpus47MaxAttempts; i++ {
		id := fmt.Sprintf("stable-acct-%d", i)
		p.SetModelList(id, []string{"claude-opus-4.7"})
		p.RecordFailureUntil(id, pool.FailureReasonTemporaryLimited, time.Now().Add(time.Minute))
	}

	h := &Handler{pool: p, requestLogs: newRequestLogStore(defaultRequestLogCapacity)}
	waitCtx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(waitCtx)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(rr, req)

	start := time.Now()
	release, ok := h.acquireOpus47AdmissionForRequest(loggedWriter, loggedReq, "claude-opus-4.7", false, true, time.Now().Add(30*time.Millisecond))
	if ok {
		release()
		t.Fatalf("expected open-circuit wait to stop only after client cancellation")
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected stable continuity wait to ignore short request budget, waited %s", time.Since(start))
	}
	h.finishRequestLog(ctx, recorder)

	if rr.Body.Len() != 0 {
		t.Fatalf("Claude open-circuit wait must not emit fallback assistant content: %s", rr.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	entry := logs[0]
	if !entry.QueuedForCapacity || entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected queued capacity wait, got %#v", entry)
	}
	if entry.StableDownstreamFallback || entry.StableFallbackReason != "" {
		t.Fatalf("did not expect stable admission fallback, got %#v", entry)
	}
}

func TestAdminClaudeCodeCompatibilityEndpointReturnsGatewaySettings(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateSettings("admin-key", true, "secret"); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if err := config.UpdateModelAdmissionConfig(config.ModelAdmissionConfig{
		Default: config.ModelAdmissionRule{MaxConcurrent: 8, MaxWaiting: 120},
		Models: map[string]config.ModelAdmissionRule{
			"claude-sonnet-4.5": {MaxConcurrent: 4, MaxWaiting: 30},
		},
	}); err != nil {
		t.Fatalf("update model admission config: %v", err)
	}
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/compat", nil)
	req.Host = "0.0.0.0:8080"
	req.Header.Set("X-Admin-Password", "secret")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["baseUrl"] != "http://127.0.0.1:8080" {
		t.Fatalf("expected local base URL, got %#v", resp["baseUrl"])
	}
	env, ok := resp["environment"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected environment map, got %#v", resp["environment"])
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8080" {
		t.Fatalf("expected ANTHROPIC_BASE_URL, got %#v", env)
	}
	if env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] != "1" || env["CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING"] != "1" {
		t.Fatalf("expected Claude Code gateway flags, got %#v", env)
	}
	caps, ok := resp["capabilities"].(map[string]interface{})
	if !ok || caps["anthropicMessages"] != true || caps["toolUse"] != true || caps["streaming"] != true {
		t.Fatalf("expected compatibility capabilities, got %#v", resp["capabilities"])
	}
	for _, capName := range []string{"fineGrainedToolStreaming", "toolSearch", "promptCacheControl", "webSearch20260209", "adaptiveModelAdmission"} {
		if caps[capName] != true {
			t.Fatalf("expected capability %s, got %#v", capName, caps)
		}
	}
	admission, ok := resp["modelAdmission"].(map[string]interface{})
	if !ok || admission["default"] == nil || admission["models"] == nil {
		t.Fatalf("expected model admission config, got %#v", resp["modelAdmission"])
	}
	notes, ok := resp["sub2apiNotes"].([]interface{})
	if !ok || len(notes) == 0 {
		t.Fatalf("expected sub2api notes, got %#v", resp["sub2apiNotes"])
	}
}

func TestAdminClaudeCodeModelReadinessReturnsCapabilityMatrix(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("secret")
	h := &Handler{
		cachedModels: []ModelInfo{
			{ModelId: "claude-sonnet-4.5", InputTypes: []string{"text", "image"}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4-5", nil)
	rr := httptest.NewRecorder()

	h.apiGetClaudeCodeModelReadiness(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body["requestedModel"] != "claude-sonnet-4-5" || body["mappedModel"] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected model mapping: %#v", body)
	}
	if body["listedByGateway"] != true {
		t.Fatalf("expected listedByGateway=true: %#v", body)
	}
	summary, ok := body["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected layered readiness summary, got %#v", body)
	}
	if summary["modelListed"] != true {
		t.Fatalf("expected summary modelListed=true, got %#v", summary)
	}
	caps, ok := body["capabilities"].(map[string]interface{})
	if !ok || caps["vision"] != true || caps["toolUse"] != true {
		t.Fatalf("expected capabilities, got %#v", body["capabilities"])
	}

	routeReq := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4-5", nil)
	routeReq.Header.Set("X-Admin-Password", "secret")
	routeRR := httptest.NewRecorder()
	h.handleAdminAPI(routeRR, routeReq)
	if routeRR.Code != http.StatusOK {
		t.Fatalf("expected route 200, got %d body=%s", routeRR.Code, routeRR.Body.String())
	}
}

func TestAdminClaudeCodeModelReadinessNormalizesVersionedHaiku45(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{
		cachedModels: []ModelInfo{
			{ModelId: "claude-haiku-4.5"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-haiku-4-5-20251001", nil)
	rr := httptest.NewRecorder()

	h.apiGetClaudeCodeModelReadiness(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body["mappedModel"] != "claude-haiku-4.5" {
		t.Fatalf("expected versioned Haiku 4.5 to normalize to Kiro model, got %#v", body)
	}
	if body["listedByGateway"] != true {
		t.Fatalf("expected normalized Haiku 4.5 to be listed, got %#v", body)
	}
}

func TestClaudeCodeModelReadinessIncludesAdmissionPressure(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	originalGate := modelAdmissionGate
	t.Cleanup(func() { modelAdmissionGate = originalGate })
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 10},
		},
	})
	h := &Handler{
		pool:        &pool.AccountPool{},
		startTime:   time.Now().Unix(),
		requestLogs: newRequestLogStore(5),
	}
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()

	h.apiGetClaudeCodeModelReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pressure, ok := body["admissionPressure"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected admissionPressure object, got %#v", body)
	}
	if pressure["active"] != true {
		t.Fatalf("expected active pressure, got %#v", pressure)
	}
}

func TestClaudeCodeModelReadinessIncludesAccountReasons(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "disabled-account", Email: "ab@example.com", Enabled: false}); err != nil {
		t.Fatalf("add disabled account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprint(resp["routingReason"])); got != "no enabled accounts" {
		t.Fatalf("expected disabled-only routing reason, got %q in %#v", got, resp)
	}
	accounts, ok := resp["accounts"].([]interface{})
	if !ok || len(accounts) != 1 {
		t.Fatalf("expected routingReason, got %#v", resp)
	}
	account, ok := accounts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected account readiness row, got %#v", accounts[0])
	}
	if account["id"] != "disabled-account" || account["enabled"] != false || account["healthy"] != false || account["listsModel"] != true || account["schedulable"] != false {
		t.Fatalf("expected disabled non-schedulable account row, got %#v", account)
	}
	if account["reason"] != "disabled account" {
		t.Fatalf("expected disabled reason, got %#v", account)
	}
	if got := fmt.Sprint(account["email"]); got == "ab@example.com" || !strings.HasSuffix(got, "@example.com") {
		t.Fatalf("expected masked email preserving domain, got %q", got)
	}
}

func TestMaskReadinessEmailMasksShortLocalParts(t *testing.T) {
	for _, email := range []string{"a@example.com", "ab@example.com"} {
		got := maskReadinessEmail(email)
		if got == email {
			t.Fatalf("expected %q to be masked, got %q", email, got)
		}
		if !strings.HasSuffix(got, "@example.com") {
			t.Fatalf("expected domain to be preserved, got %q", got)
		}
		local := strings.TrimSuffix(got, "@example.com")
		if strings.Contains(local, strings.Split(email, "@")[0]) {
			t.Fatalf("expected local part to be masked, got %q for %q", got, email)
		}
	}
}

func TestClaudeCodeModelReadinessAllowsTransientFailureWithoutCooldown(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:                "transient-account",
		Email:             "transient@example.com",
		Enabled:           true,
		AccessToken:       "token",
		ProfileArn:        "arn:aws:codewhisperer:profile/test",
		ExpiresAt:         time.Now().Add(time.Hour).Unix(),
		LastFailureReason: "transient_network",
		FailureCount:      1,
	}); err != nil {
		t.Fatalf("add transient account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "schedulable accounts available" {
		t.Fatalf("expected schedulable routing reason, got %#v", resp)
	}
	accounts := resp["accounts"].([]interface{})
	account := accounts[0].(map[string]interface{})
	if account["schedulable"] != true || account["reason"] != "schedulable" {
		t.Fatalf("expected transient non-cooldown account to be schedulable, got %#v", account)
	}
}

func TestClaudeCodeModelReadinessAllowsExpiredCooldownState(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:                "expired-cooldown-account",
		Email:             "expired@example.com",
		Enabled:           true,
		AccessToken:       "token",
		ProfileArn:        "arn:aws:codewhisperer:profile/test",
		ExpiresAt:         time.Now().Add(time.Hour).Unix(),
		LastFailureReason: "rate_limited",
		FailureCount:      2,
		CooldownUntil:     time.Now().Add(-time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("add expired cooldown account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "schedulable accounts available" {
		t.Fatalf("expected schedulable routing reason, got %#v", resp)
	}
	accounts := resp["accounts"].([]interface{})
	account := accounts[0].(map[string]interface{})
	if account["schedulable"] != true || account["reason"] != "schedulable" {
		t.Fatalf("expected expired cooldown account to be schedulable, got %#v", account)
	}
}

func TestClaudeCodeModelReadinessReportsAccountsEvaluatedWhenEnabledButBlocked(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:            "cooldown-account",
		Email:         "cooldown@example.com",
		Enabled:       true,
		AccessToken:   "token",
		ProfileArn:    "arn:aws:codewhisperer:profile/test",
		ExpiresAt:     time.Now().Add(time.Hour).Unix(),
		CooldownUntil: time.Now().Add(time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("add cooldown account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "accounts evaluated" {
		t.Fatalf("expected accounts evaluated routing reason, got %#v", resp)
	}
	accounts := resp["accounts"].([]interface{})
	account := accounts[0].(map[string]interface{})
	if account["schedulable"] != false || account["reason"] != "unhealthy account" {
		t.Fatalf("expected cooldown account to be blocked, got %#v", account)
	}
}

func TestClaudeCodeModelReadinessKeepsSharedProfileSchedulableAfterSingleTemporaryLimit(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{
			ID:          "limited-account",
			Email:       "limited@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "shared-account",
			Email:       "shared@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.RecordFailureUntil("limited-account", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "schedulable accounts available" {
		t.Fatalf("expected schedulable shared-profile account after one temporary limit, got %#v", resp)
	}
	rows := resp["accounts"].([]interface{})
	for _, raw := range rows {
		row := raw.(map[string]interface{})
		if row["id"] == "limited-account" && (row["reason"] != "temporary limited account cooling down" || row["cooldownSource"] != "account") {
			t.Fatalf("expected limited account to report account cooldown source, got %#v", row)
		}
		if row["id"] == "shared-account" && (row["schedulable"] != true || row["reason"] != "schedulable") {
			t.Fatalf("expected shared account to remain schedulable, got %#v", row)
		}
	}
}

func TestClaudeCodeModelReadinessKeepsSharedProfileSchedulableAfterMultipleTemporaryLimits(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{
			ID:          "limited-account-1",
			Email:       "limited1@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "limited-account-2",
			Email:       "limited2@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "shared-account",
			Email:       "shared@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.RecordFailureUntil("limited-account-1", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	p.RecordFailureUntil("limited-account-2", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "schedulable accounts available" {
		t.Fatalf("expected untouched shared-profile account to remain routable, got %#v", resp)
	}
	rows := resp["accounts"].([]interface{})
	for _, raw := range rows {
		row := raw.(map[string]interface{})
		if strings.HasPrefix(row["id"].(string), "limited-account-") && row["schedulable"] != false {
			t.Fatalf("expected explicitly limited account to be non-schedulable, got %#v", row)
		}
		if row["id"] == "shared-account" && (row["schedulable"] != true || row["reason"] != "schedulable" || row["cooldownSource"] != "account") {
			t.Fatalf("expected untouched shared account to remain schedulable, got %#v", row)
		}
	}
}

func TestClaudeCodeModelReadinessBlocksUsageLimitWithoutOverage(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:           "usage-limit-account",
		Email:        "usage@example.com",
		Enabled:      true,
		AccessToken:  "token",
		ProfileArn:   "arn:aws:codewhisperer:profile/test",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		UsageCurrent: 10,
		UsageLimit:   10,
	}); err != nil {
		t.Fatalf("add usage account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "accounts evaluated" {
		t.Fatalf("expected accounts evaluated routing reason, got %#v", resp)
	}
	accounts := resp["accounts"].([]interface{})
	account := accounts[0].(map[string]interface{})
	if account["schedulable"] != false || account["reason"] != "usage limit reached" {
		t.Fatalf("expected usage-limited account to be blocked, got %#v", account)
	}
}

func TestClaudeCodeModelReadinessBlocksUsageLimitWhenOnlyGlobalOverUsageAllowed(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateAllowOverUsage(true); err != nil {
		t.Fatalf("update allow over usage: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:           "global-overuse-account",
		Email:        "global@example.com",
		Enabled:      true,
		AccessToken:  "token",
		ProfileArn:   "arn:aws:codewhisperer:profile/test",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		UsageCurrent: 10,
		UsageLimit:   10,
	}); err != nil {
		t.Fatalf("add usage account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["routingReason"] != "accounts evaluated" {
		t.Fatalf("expected accounts evaluated routing reason, got %#v", resp)
	}
	accounts := resp["accounts"].([]interface{})
	account := accounts[0].(map[string]interface{})
	if account["schedulable"] != false || account["reason"] != "usage limit reached" {
		t.Fatalf("expected globally allowed but non-overage account to be blocked after reload, got %#v", account)
	}
}

func TestAdminClaudeCodeReadinessRouteReportsRecentToolEvidence(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("secret")
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                   time.Now(),
		Endpoint:                    "/v1/messages",
		Model:                       "claude-sonnet-4.5",
		ClaudeCodeSessionID:         "session-1",
		AnthropicBetas:              []string{"tool-search-2025-10-19"},
		ToolReferenceCount:          1,
		PayloadTrimmed:              true,
		PayloadCurrentMessageShape:  "text+tool_result",
		PayloadContextReminderKinds: []string{"system", "language"},
		PayloadKeptTools:            []string{"bash"},
		PayloadTrimmedTools:         []string{"mcp__browser__screenshot"},
		PayloadDeferredTools:        []string{"mcp__browser__snapshot"},
		PayloadMaterializedToolRefs: []string{"mcp__fs__read_file"},
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	req.Header.Set("X-Admin-Password", "secret")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	for _, key := range []string{"recentClaudeCode", "recentToolReferences", "recentMCPTools", "recentToolTrimming"} {
		if resp[key] != true {
			t.Fatalf("expected %s=true, got %#v", key, resp)
		}
	}
	if resp["recentToolResultTurns"] != true {
		t.Fatalf("expected recentToolResultTurns=true, got %#v", resp)
	}
	reminders, ok := resp["recentContextReminders"].([]interface{})
	if !ok || len(reminders) != 2 || reminders[0] != "language" || reminders[1] != "system" {
		t.Fatalf("expected sorted recent context reminders, got %#v", resp["recentContextReminders"])
	}
	examples, ok := resp["examples"].([]interface{})
	if !ok || len(examples) == 0 {
		t.Fatalf("expected readiness examples, got %#v", resp["examples"])
	}
	first, ok := examples[0].(map[string]interface{})
	if !ok || first["currentMessageShape"] != "text+tool_result" {
		t.Fatalf("expected readiness example to include current message shape, got %#v", first)
	}
}

func TestClaudeCodeReadinessIncludesNewParitySignals(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                           time.Now().UTC(),
		ClaudeCodeSessionID:                 "session_1",
		ClaudeCodeAgentID:                   "agent_1",
		ClaudeCodeParentAgentID:             "parent_1",
		PayloadToolResultImages:             1,
		PayloadOrphanedToolResultsConverted: 1,
		PayloadUnsupportedContentBlocks:     []string{"document"},
		FineGrainedToolStreamingRequested:   true,
		FineGrainedToolStreamingMode:        "requested_partial",
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	rr := httptest.NewRecorder()

	h.apiGetClaudeCodeReadiness(rr, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, key := range []string{"recentParentAgents", "recentToolResultImages", "recentOrphanedToolResults", "recentUnsupportedBlocks", "recentFineGrainedToolStreaming"} {
		if body[key] != true {
			t.Fatalf("expected %s=true, got %#v", key, body)
		}
	}
}

func TestClaudeCodeReadinessReportsPartialCapabilities(t *testing.T) {
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		RequestID:                           "req-partial",
		Endpoint:                            "/v1/messages",
		Model:                               "claude-sonnet-4.5",
		StatusCode:                          200,
		Outcome:                             "success",
		FineGrainedToolStreamingRequested:   true,
		FineGrainedToolStreamingMode:        "requested_partial",
		MaxTokensZeroMode:                   "local_zero_output",
		SuppressedToolUseCount:              1,
		SuppressedToolUseNames:              []string{"request_user_input"},
		PayloadUnsupportedContentBlocks:     []string{"document"},
		PayloadUnknownOfficialFields:        []string{"container"},
		PayloadRelocatedToolDescriptions:    2,
		PayloadOrphanedToolResultsConverted: 1,
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeReadiness(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	capabilities, ok := resp["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", resp)
	}
	for _, key := range []string{"fineGrainedToolStreaming", "maxTokensZero", "countTokens", "assistantPrefill"} {
		if _, ok := capabilities[key]; !ok {
			t.Fatalf("expected capability %s in %#v", key, capabilities)
		}
	}
}

func TestClaudeCodeReadinessReportsLayeredPartialCapabilities(t *testing.T) {
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(10)}
	now := time.Now().UTC()
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:       now,
		RequestID:       "req-count",
		Endpoint:        "/v1/messages/count_tokens",
		Model:           "claude-sonnet-4.5",
		StatusCode:      200,
		Outcome:         "success",
		CountTokensMode: "estimated",
	})
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:         now,
		RequestID:         "req-zero",
		Endpoint:          "/v1/messages",
		Model:             "claude-sonnet-4.5",
		StatusCode:        200,
		Outcome:           "success",
		MaxTokensZeroMode: "local_zero_output",
	})
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                         now,
		RequestID:                         "req-fg",
		Endpoint:                          "/v1/messages",
		Model:                             "claude-sonnet-4.5",
		StatusCode:                        200,
		Outcome:                           "success",
		FineGrainedToolStreamingRequested: true,
		FineGrainedToolStreamingMode:      "kiro_go_chunked_complete_input",
	})
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:            now,
		RequestID:            "req-prefill",
		Endpoint:             "/v1/messages",
		Model:                "claude-opus-4.7",
		StatusCode:           200,
		Outcome:              "success",
		AssistantPrefillMode: "emulated_text_prefill",
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	w := httptest.NewRecorder()

	h.apiGetClaudeCodeReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	capabilities := resp["capabilities"].(map[string]interface{})
	for _, name := range []string{"countTokens", "maxTokensZero", "fineGrainedToolStreaming", "assistantPrefill"} {
		capability := capabilities[name].(map[string]interface{})
		if capability["status"] != "PARTIAL" {
			t.Fatalf("expected %s top-level PARTIAL, got %#v", name, capability)
		}
		if _, ok := capability["claudeCodeCompatibility"].(map[string]interface{}); !ok {
			t.Fatalf("expected %s claudeCodeCompatibility object, got %#v", name, capability)
		}
		if _, ok := capability["officialAnthropicParity"].(map[string]interface{}); !ok {
			t.Fatalf("expected %s officialAnthropicParity object, got %#v", name, capability)
		}
		if _, ok := capability["evidence"].(map[string]interface{}); !ok {
			t.Fatalf("expected %s evidence object, got %#v", name, capability)
		}
	}
	count := capabilities["countTokens"].(map[string]interface{})
	countCompat := count["claudeCodeCompatibility"].(map[string]interface{})
	if countCompat["status"] != "PASS" || countCompat["mode"] != "estimated" {
		t.Fatalf("expected countTokens compatibility PASS estimated, got %#v", countCompat)
	}
	prefill := capabilities["assistantPrefill"].(map[string]interface{})
	prefillCompat := prefill["claudeCodeCompatibility"].(map[string]interface{})
	if prefillCompat["status"] != "EMULATED_PASS" || prefillCompat["mode"] != "emulated_text_prefill" {
		t.Fatalf("expected assistant prefill emulated compatibility, got %#v", prefillCompat)
	}
	prefillOfficial := prefill["officialAnthropicParity"].(map[string]interface{})
	if prefillOfficial["status"] != "UNSUPPORTED_BY_MODEL" {
		t.Fatalf("expected opus 4.7 prefill unsupported by model, got %#v", prefillOfficial)
	}
}

func TestAdminClaudeCodeReadinessReportsDeferredMCPToolReferences(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("secret")
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:            time.Now(),
		Endpoint:             "/v1/messages",
		Model:                "claude-sonnet-4.5",
		ClaudeCodeSessionID:  "session-1",
		ToolReferenceCount:   1,
		PayloadDeferredTools: []string{"mcp__browser__screenshot"},
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	req.Header.Set("X-Admin-Password", "secret")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if resp["recentMCPTools"] != true {
		t.Fatalf("expected deferred MCP tool reference to set recentMCPTools=true, got %#v", resp)
	}
}

func TestApplyOpus47AdmissionConfigUsesConfiguredGate(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateOpus47AdmissionConfig(config.Opus47AdmissionConfig{MaxConcurrent: 10, MaxWaiting: 20}); err != nil {
		t.Fatalf("update opus admission config: %v", err)
	}

	oldGate := opus47AdmissionGate
	t.Cleanup(func() {
		opus47AdmissionGate = oldGate
	})

	applyOpus47AdmissionConfig()

	releases := make([]func(), 0, 10)
	for i := 0; i < 10; i++ {
		release, err := opus47AdmissionGate.acquire(time.Millisecond)
		if err != nil {
			t.Fatalf("expected configured gate to allow 10 concurrent holders, acquire %d failed: %v", i+1, err)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
}

func TestHandleClaudeOpus47AdmissionGateSharesRetryBudget(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	oldGate := opus47AdmissionGate
	oldModelGate := modelAdmissionGate
	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opus47AdmissionGate = newOpus47Gate(1, 10)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 10},
		},
	})
	opusCapacityRetryBudget = 200 * time.Millisecond
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() {
		opus47AdmissionGate = oldGate
		modelAdmissionGate = oldModelGate
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	held, _, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil {
		t.Fatalf("pre-acquire gate: %v", err)
	}

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			status := http.StatusOK
			body := ""
			if attempts == 1 {
				status = http.StatusTooManyRequests
				body = `{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		body := strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
		w := httptest.NewRecorder()
		h.handleClaudeMessagesInternal(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d body %s", w.Code, w.Body.String())
		}
	}()

	time.Sleep(90 * time.Millisecond)
	held()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("handler did not complete")
	}
	if len(sleeps) != 1 {
		t.Fatalf("expected one capacity retry sleep, got %d", len(sleeps))
	}
	if sleeps[0] > 150*time.Millisecond {
		t.Fatalf("expected gate wait to consume retry budget, got retry sleep %s", sleeps[0])
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeStreamOpus47CapacityLimitReturnsExplicitError(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{ID: "acct-1", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:aws:codewhisperer:profile/test-1", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = time.Millisecond
	sleepForOpusCapacityRetry = func(d time.Duration) {}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	body := strings.NewReader(`{"model":"claude-opus-4.7","stream":true,"max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 capacity contract must not return 429, body %q", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected explicit 503 after capacity budget, got status %d body %q", w.Code, w.Body.String())
	}
	if strings.TrimSpace(w.Body.String()) == "" {
		t.Fatalf("expected non-empty error body")
	}
	if !strings.Contains(w.Body.String(), "INSUFFICIENT_MODEL_CAPACITY") {
		t.Fatalf("expected upstream capacity reason in body, got %q", w.Body.String())
	}
}

func TestClaudeUpstreamErrorsMapToAnthropicErrorTypes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{
			name:       "rate limit",
			err:        &rateLimitError{endpoint: "Kiro IDE", body: `{"message":"rate limited"}`, resetAt: time.Now().Add(time.Second)},
			wantStatus: http.StatusTooManyRequests,
			wantType:   "rate_limit_error",
		},
		{
			name:       "upstream capacity",
			err:        errors.New(`HTTP 429 from Kiro IDE: {"reason":"INSUFFICIENT_MODEL_CAPACITY"}`),
			wantStatus: http.StatusTooManyRequests,
			wantType:   "rate_limit_error",
		},
		{
			name:       "temporary account limit",
			err:        errors.New(`HTTP 429 from AmazonQ: {"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`),
			wantStatus: http.StatusTooManyRequests,
			wantType:   "rate_limit_error",
		},
		{
			name:       "upstream unavailable",
			err:        errors.New("HTTP 503 from Kiro IDE"),
			wantStatus: http.StatusServiceUnavailable,
			wantType:   "overloaded_error",
		},
		{
			name:       "http2 reset",
			err:        errors.New("stream error: stream ID 397; INTERNAL_ERROR; received from peer"),
			wantStatus: http.StatusServiceUnavailable,
			wantType:   "overloaded_error",
		},
		{
			name:       "malformed upstream request",
			err:        errors.New(`HTTP 400 from AmazonQ: {"message":"Improperly formed request.","reason":null}`),
			wantStatus: http.StatusBadRequest,
			wantType:   "invalid_request_error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotType := claudeUpstreamErrorStatusAndType(tc.err)
			if gotStatus != tc.wantStatus || gotType != tc.wantType {
				t.Fatalf("expected %d/%s, got %d/%s", tc.wantStatus, tc.wantType, gotStatus, gotType)
			}
		})
	}
}

func TestHandleClaudeStreamUpstreamMalformedBeforeFirstEventReturnsJSONError(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-stream-malformed",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"message":"Improperly formed request.","reason":null}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	w := httptest.NewRecorder()
	body := `{"model":"claude-opus-4-7","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	r.Header.Set("content-type", "application/json")

	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 JSON error before stream starts, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "event:") || strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected non-SSE JSON error before first stream event, content-type=%q body=%s", w.Header().Get("Content-Type"), w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_request_error") || !strings.Contains(w.Body.String(), "Improperly formed request") {
		t.Fatalf("expected Anthropic invalid_request_error, body=%s", w.Body.String())
	}
}

func TestOpenAIUpstreamErrorsPreserveRetryableStatusAndType(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{
			name:       "rate limit",
			err:        &rateLimitError{endpoint: "Kiro IDE", body: `{"message":"rate limited"}`, resetAt: time.Now().Add(time.Second)},
			wantStatus: http.StatusTooManyRequests,
			wantType:   "rate_limit_error",
		},
		{
			name:       "temporary account limit",
			err:        &rateLimitError{endpoint: "AmazonQ", body: `{"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`, resetAt: time.Now().Add(time.Second)},
			wantStatus: http.StatusTooManyRequests,
			wantType:   "rate_limit_error",
		},
		{
			name:       "upstream unavailable",
			err:        errors.New("HTTP 503 from Kiro IDE"),
			wantStatus: http.StatusServiceUnavailable,
			wantType:   "server_error",
		},
		{
			name:       "auth expired",
			err:        errors.New("HTTP 401 from Kiro IDE: expired token"),
			wantStatus: http.StatusUnauthorized,
			wantType:   "authentication_error",
		},
		{
			name:       "quota exhausted",
			err:        errors.New("quota exhausted on Kiro IDE"),
			wantStatus: http.StatusPaymentRequired,
			wantType:   "billing_error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotType := openAIUpstreamErrorStatusAndType(tc.err)
			if gotStatus != tc.wantStatus || gotType != tc.wantType {
				t.Fatalf("expected %d/%s, got %d/%s", tc.wantStatus, tc.wantType, gotStatus, gotType)
			}
		})
	}
}

func TestClaudeRateLimitErrorBuildsRetryAfterHeader(t *testing.T) {
	resetAt := time.Now().Add(2500 * time.Millisecond)
	headers := claudeErrorHeadersForUpstreamError(&rateLimitError{
		endpoint: "Kiro IDE",
		body:     `{"message":"rate limited"}`,
		resetAt:  resetAt,
	})

	if got := headers.Get("Retry-After"); got != "2" && got != "3" {
		t.Fatalf("expected Retry-After around reset time, got %q", got)
	}
}

func TestClaudeRealUpstreamRateLimitStillReturns429(t *testing.T) {
	err := &rateLimitError{
		endpoint: "Kiro IDE",
		body:     `{"message":"rate limited"}`,
		resetAt:  time.Now().Add(2500 * time.Millisecond),
	}

	status, errType := claudeUpstreamErrorStatusAndType(err)
	if status != http.StatusTooManyRequests || errType != "rate_limit_error" {
		t.Fatalf("expected real upstream rate limit to remain 429/rate_limit_error, got %d/%s", status, errType)
	}
	headers := claudeErrorHeadersForUpstreamError(err)
	if got := headers.Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After for real upstream 429")
	}
	if got := headers.Get("X-Kiro-Go-Error-Reason"); got != "" {
		t.Fatalf("expected no pool-only error reason for real upstream 429, got %q", got)
	}
}

func TestOpus47PressureErrorsNeverReturn429(t *testing.T) {
	h := &Handler{}
	rateErr := &rateLimitError{
		endpoint: "Kiro IDE",
		body:     `{"message":"rate limited"}`,
		resetAt:  time.Now().Add(2500 * time.Millisecond),
	}

	claudeW := httptest.NewRecorder()
	h.sendClaudeOpusPressureError(claudeW, "claude-opus-4.7", rateErr, "attempt_budget_exhausted")
	if claudeW.Code == http.StatusTooManyRequests {
		t.Fatalf("Claude Opus pressure error must not return 429: %s", claudeW.Body.String())
	}
	if claudeW.Code != http.StatusServiceUnavailable {
		t.Fatalf("Claude Opus pressure status = %d body=%s, want 503", claudeW.Code, claudeW.Body.String())
	}
	if got := claudeW.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
	if got := claudeW.Header().Get("X-Kiro-Go-Retryable"); got != "true" {
		t.Fatalf("expected retryable header, got %q", got)
	}

	openAIW := httptest.NewRecorder()
	h.sendOpenAIOpusPressureError(openAIW, "claude-opus-4.7", rateErr, "attempt_budget_exhausted")
	if openAIW.Code == http.StatusTooManyRequests {
		t.Fatalf("OpenAI Opus pressure error must not return 429: %s", openAIW.Body.String())
	}
	if openAIW.Code != http.StatusServiceUnavailable {
		t.Fatalf("OpenAI Opus pressure status = %d body=%s, want 503", openAIW.Code, openAIW.Body.String())
	}
	if got := openAIW.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
	if got := openAIW.Header().Get("X-Kiro-Go-Retryable"); got != "true" {
		t.Fatalf("expected retryable header, got %q", got)
	}
}

func TestClaudeNonRateLimitErrorDoesNotBuildRetryAfterHeader(t *testing.T) {
	headers := claudeErrorHeadersForUpstreamError(errors.New("HTTP 503 from Kiro IDE"))

	if got := headers.Get("Retry-After"); got != "" {
		t.Fatalf("expected no Retry-After for unrelated error, got %q", got)
	}
}

func TestClaudeStreamErrorEventUsesMappedAnthropicErrorType(t *testing.T) {
	w := httptest.NewRecorder()
	flusher, ok := interface{}(w).(http.Flusher)
	if !ok {
		t.Fatalf("recorder should support flushing")
	}

	h := &Handler{}
	h.sendClaudeStreamError(w, flusher, errors.New("stream error: stream ID 397; INTERNAL_ERROR; received from peer"))

	body := w.Body.String()
	if !strings.Contains(body, `"type":"overloaded_error"`) {
		t.Fatalf("expected mapped overloaded_error in stream error, got %q", body)
	}
	if strings.Contains(body, `"type":"api_error"`) {
		t.Fatalf("expected stream error not to fall back to api_error, got %q", body)
	}
}

func TestHandleClaudeStreamToolUseStartsWithMessageStart(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-tool-stream",
		Enabled:     true,
		AccessToken: "token-tool-stream",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_test_1",
							"name":      "get_weather",
							"input":     map[string]interface{}{"city": "Shanghai"},
							"stop":      true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 11, "outputTokens": 7}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":true,"max_tokens":64,"tools":[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"tool_choice":{"type":"tool","name":"get_weather"},"messages":[{"role":"user","content":"Use the weather tool for Shanghai"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	events := collectSSEEvents(w.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected multiple SSE events, got %v body %q", events, w.Body.String())
	}
	if events[0] != "message_start" {
		t.Fatalf("expected message_start to be first SSE event for tool-only stream, got %v body %q", events, w.Body.String())
	}
	if events[1] != "content_block_start" {
		t.Fatalf("expected tool content block after message_start, got %v body %q", events, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"tool_use"`) {
		t.Fatalf("expected tool_use content block, got %q", w.Body.String())
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeStreamMultipleToolUsesEmitsIndexedInputJSONDeltas(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-two-tool-stream",
		Enabled:     true,
		AccessToken: "token-two-tool-stream",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_read_1",
							"name":      "read_file",
							"input":     map[string]interface{}{"path": "/tmp/a.go", "encoding": "utf-8"},
							"stop":      true,
						},
					},
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_write_2",
							"name":      "write_file",
							"input":     map[string]interface{}{"path": "/tmp/b.go", "content": "package main\n"},
							"stop":      true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":true,"max_tokens":64,"tools":[{"name":"read_file","description":"Read file","input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}},{"name":"write_file","description":"Write file","input_schema":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}}],"messages":[{"role":"user","content":"Read then write files"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	frames := parseSSEFrames(t, w.Body.String())
	assertFrameEvent(t, frames, 0, "message_start")

	next := assertToolUseBlock(t, frames, 1, 0, "toolu_read_1", "read_file", map[string]interface{}{
		"path":     "/tmp/a.go",
		"encoding": "utf-8",
	})
	next = assertToolUseBlock(t, frames, next, 1, "toolu_write_2", "write_file", map[string]interface{}{
		"path":    "/tmp/b.go",
		"content": "package main\n",
	})

	assertFrameEvent(t, frames, next, "message_delta")
	assertNestedString(t, frames[next], "delta", "stop_reason", "tool_use")
	next++
	assertFrameEvent(t, frames, next, "message_stop")
	next++
	if next != len(frames) {
		t.Fatalf("unexpected frames after message_stop: events=%v", frameEvents(frames[next:]))
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeStreamToolReferenceRestoresOriginalToolName(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-toolref-stream",
		Enabled:     true,
		AccessToken: "token-toolref-stream",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	var kiroToolName string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload KiroPayload
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode kiro payload: %v", err)
			}
			ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
			if ctx == nil || len(ctx.Tools) != 1 {
				t.Fatalf("expected one tool in Kiro payload, got %#v", ctx)
			}
			kiroToolName = ctx.Tools[0].ToolSpecification.Name
			if kiroToolName == "mcp__filesystem__read_file" {
				t.Fatalf("expected Kiro payload to use sanitized name")
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_ref_1",
							"name":      kiroToolName,
							"input":     map[string]interface{}{"path": "README.md"},
							"stop":      true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 11, "outputTokens": 7}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":true,"max_tokens":64,"tool_reference":[{"type":"tool_reference","id":"toolref_1","name":"mcp__filesystem__read_file","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}],"messages":[{"role":"user","content":"read README"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `"name":"mcp__filesystem__read_file"`) {
		t.Fatalf("expected streamed tool_use to restore original name, got %q", respBody)
	}
	if strings.Contains(respBody, `"name":"`+kiroToolName+`"`) {
		t.Fatalf("expected streamed tool_use not to expose sanitized name %q, got %q", kiroToolName, respBody)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeStreamInvalidToolUseFallsBackToEndTurn(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(10)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-invalid-tool-stream",
		Enabled:     true,
		AccessToken: "token-invalid-tool-stream",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_input",
							"name":      "request_user_input",
							"input": map[string]interface{}{
								"questions": []interface{}{
									map[string]interface{}{"header": "A"},
									map[string]interface{}{"header": "B"},
									map[string]interface{}{"header": "C"},
									map[string]interface{}{"header": "D"},
								},
							},
							"stop": true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":true,"max_tokens":64,"tools":[{"name":"request_user_input","description":"Ask user","input_schema":{"type":"object","properties":{"questions":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"header":{"type":"string"}}}}},"required":["questions"]}}],"messages":[{"role":"user","content":"Ask the user to choose."}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if strings.Contains(respBody, `"type":"tool_use"`) {
		t.Fatalf("expected invalid tool_use to be suppressed, got %q", respBody)
	}
	if strings.Contains(respBody, `"stop_reason":"tool_use"`) {
		t.Fatalf("expected end_turn stop when no valid tool_use is emitted, got %q", respBody)
	}
	if !strings.Contains(respBody, `"stop_reason":"end_turn"`) {
		t.Fatalf("expected end_turn stop, got %q", respBody)
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %#v", entries)
	}
	entry := entries[0]
	if entry.SuppressedToolUseCount != 1 {
		t.Fatalf("expected one suppressed tool use, got %#v", entry)
	}
	if strings.Join(entry.SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed tool name, got %#v", entry)
	}
	if !strings.Contains(strings.Join(entry.SuppressedToolUseReasons, ","), "schema") {
		t.Fatalf("expected suppressed tool reason, got %#v", entry)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleClaudeNonStreamInvalidToolUseIsSuppressedAndLogged(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(10)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-invalid-tool-nonstream",
		Enabled:     true,
		AccessToken: "token-invalid-tool-nonstream",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_input",
							"name":      "request_user_input",
							"input": map[string]interface{}{
								"questions": []interface{}{
									map[string]interface{}{"header": "A"},
									map[string]interface{}{"header": "B"},
									map[string]interface{}{"header": "C"},
									map[string]interface{}{"header": "D"},
								},
							},
							"stop": true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":false,"max_tokens":64,"tools":[{"name":"request_user_input","description":"Ask user","input_schema":{"type":"object","properties":{"questions":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"header":{"type":"string"}}}}},"required":["questions"]}}],"messages":[{"role":"user","content":"Ask the user to choose."}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, block := range resp.Content {
		if block.Type == "tool_use" {
			t.Fatalf("expected invalid tool_use to be suppressed, got %#v", resp.Content)
		}
	}
	if resp.StopReason == "tool_use" {
		t.Fatalf("expected non-tool stop when invalid tool_use is suppressed, got %#v", resp)
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %#v", entries)
	}
	entry := entries[0]
	if entry.SuppressedToolUseCount != 1 {
		t.Fatalf("expected one suppressed tool use, got %#v", entry)
	}
	if strings.Join(entry.SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed tool name, got %#v", entry)
	}
	if !strings.Contains(strings.Join(entry.SuppressedToolUseReasons, ","), "schema") {
		t.Fatalf("expected suppressed tool reason, got %#v", entry)
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleOpenAIChatStreamLogsSuppressedToolUse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(10)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-openai-chat-suppressed-tool",
		Enabled:     true,
		AccessToken: "token-openai-chat-suppressed-tool",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_input",
							"name":      "request_user_input",
							"input": map[string]interface{}{
								"questions": []interface{}{
									map[string]interface{}{"header": "A"},
									map[string]interface{}{"header": "B"},
									map[string]interface{}{"header": "C"},
									map[string]interface{}{"header": "D"},
								},
							},
							"stop": true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":true,"max_tokens":64,"tools":[{"type":"function","function":{"name":"request_user_input","description":"Ask user","parameters":{"type":"object","properties":{"questions":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"header":{"type":"string"}}}}},"required":["questions"]}}}],"messages":[{"role":"user","content":"Ask the user to choose."}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"tool_calls"`) {
		t.Fatalf("expected invalid tool call to be suppressed, got %q", w.Body.String())
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %#v", entries)
	}
	if entries[0].SuppressedToolUseCount != 1 || strings.Join(entries[0].SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed request_user_input metadata, got %#v", entries[0])
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleOpenAIChatNonStreamLogsSuppressedToolUse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(10)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-openai-chat-nonstream-suppressed-tool",
		Enabled:     true,
		AccessToken: "token-openai-chat-nonstream-suppressed-tool",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_input",
							"name":      "request_user_input",
							"input": map[string]interface{}{
								"questions": []interface{}{
									map[string]interface{}{"header": "A"},
									map[string]interface{}{"header": "B"},
									map[string]interface{}{"header": "C"},
									map[string]interface{}{"header": "D"},
								},
							},
							"stop": true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":false,"max_tokens":64,"tools":[{"type":"function","function":{"name":"request_user_input","description":"Ask user","parameters":{"type":"object","properties":{"questions":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"header":{"type":"string"}}}}},"required":["questions"]}}}],"messages":[{"role":"user","content":"Ask the user to choose."}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"tool_calls"`) {
		t.Fatalf("expected invalid tool call to be suppressed, got %q", w.Body.String())
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %#v", entries)
	}
	if entries[0].SuppressedToolUseCount != 1 || strings.Join(entries[0].SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed request_user_input metadata, got %#v", entries[0])
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleOpenAIResponsesStreamLogsSuppressedToolUse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(10)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-openai-responses-suppressed-tool",
		Enabled:     true,
		AccessToken: "token-openai-responses-suppressed-tool",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_input",
							"name":      "request_user_input",
							"input": map[string]interface{}{
								"questions": []interface{}{
									map[string]interface{}{"header": "A"},
									map[string]interface{}{"header": "B"},
									map[string]interface{}{"header": "C"},
									map[string]interface{}{"header": "D"},
								},
							},
							"stop": true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":true,"max_output_tokens":64,"tools":[{"type":"function","name":"request_user_input","description":"Ask user","parameters":{"type":"object","properties":{"questions":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"header":{"type":"string"}}}}},"required":["questions"]}}],"input":"Ask the user to choose."}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"function_call"`) {
		t.Fatalf("expected invalid function call to be suppressed, got %q", w.Body.String())
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %#v", entries)
	}
	if entries[0].SuppressedToolUseCount != 1 || strings.Join(entries[0].SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed request_user_input metadata, got %#v", entries[0])
	}
	waitForAccountRequestCount(t, 1)
}

func TestHandleOpenAIResponsesNonStreamLogsSuppressedToolUse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(10)}
	if err := config.AddAccount(config.Account{
		ID:          "acct-openai-responses-nonstream-suppressed-tool",
		Enabled:     true,
		AccessToken: "token-openai-responses-nonstream-suppressed-tool",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p.Reload()

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{
						eventType: "toolUseEvent",
						payload: map[string]interface{}{
							"toolUseId": "toolu_input",
							"name":      "request_user_input",
							"input": map[string]interface{}{
								"questions": []interface{}{
									map[string]interface{}{"header": "A"},
									map[string]interface{}{"header": "B"},
									map[string]interface{}{"header": "C"},
									map[string]interface{}{"header": "D"},
								},
							},
							"stop": true,
						},
					},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 25, "outputTokens": 9}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	body := strings.NewReader(`{"model":"claude-sonnet-4.5","stream":false,"max_output_tokens":64,"tools":[{"type":"function","name":"request_user_input","description":"Ask user","parameters":{"type":"object","properties":{"questions":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"header":{"type":"string"}}}}},"required":["questions"]}}],"input":"Ask the user to choose."}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"function_call"`) {
		t.Fatalf("expected invalid function call to be suppressed, got %q", w.Body.String())
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %#v", entries)
	}
	if entries[0].SuppressedToolUseCount != 1 || strings.Join(entries[0].SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed request_user_input metadata, got %#v", entries[0])
	}
	waitForAccountRequestCount(t, 1)
}

func assertToolUseBlock(t *testing.T, frames []sseFrame, start, wantIndex int, wantID, wantName string, wantInput map[string]interface{}) int {
	t.Helper()
	assertFrameEvent(t, frames, start, "content_block_start")
	assertFrameIndex(t, frames[start], wantIndex)
	assertNestedString(t, frames[start], "content_block", "type", "tool_use")
	assertNestedString(t, frames[start], "content_block", "id", wantID)
	assertNestedString(t, frames[start], "content_block", "name", wantName)

	var inputJSON strings.Builder
	next := start + 1
	for ; next < len(frames); next++ {
		if frames[next].event == "content_block_stop" {
			break
		}
		assertFrameEvent(t, frames, next, "content_block_delta")
		assertFrameIndex(t, frames[next], wantIndex)
		assertNestedString(t, frames[next], "delta", "type", "input_json_delta")
		inputJSON.WriteString(requireNestedString(t, frames[next], "delta", "partial_json"))
	}
	if inputJSON.Len() == 0 {
		t.Fatalf("expected tool input_json_delta for tool index %d, got events=%v", wantIndex, frameEvents(frames[start:]))
	}
	var gotInput map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON.String()), &gotInput); err != nil {
		t.Fatalf("invalid reconstructed tool input JSON %q: %v", inputJSON.String(), err)
	}
	if !reflect.DeepEqual(gotInput, wantInput) {
		t.Fatalf("tool index %d reconstructed input = %#v, want %#v", wantIndex, gotInput, wantInput)
	}
	assertFrameEvent(t, frames, next, "content_block_stop")
	assertFrameIndex(t, frames[next], wantIndex)
	return next + 1
}

func assertObjectNumber(t *testing.T, frame sseFrame, objectKey, fieldKey string, want float64) {
	t.Helper()
	obj, ok := frame.data[objectKey].(map[string]interface{})
	if !ok {
		t.Fatalf("frame %q %s missing or non-object: %#v", frame.event, objectKey, frame.data[objectKey])
	}
	got, ok := obj[fieldKey].(float64)
	if !ok {
		t.Fatalf("frame %q %s.%s missing or non-numeric: %#v", frame.event, objectKey, fieldKey, obj[fieldKey])
	}
	if got != want {
		t.Fatalf("frame %q %s.%s = %v, want %v", frame.event, objectKey, fieldKey, got, want)
	}
}

func assertNestedNumber(t *testing.T, frame sseFrame, objectKey, nestedKey, fieldKey string, want float64) {
	t.Helper()
	obj, ok := frame.data[objectKey].(map[string]interface{})
	if !ok {
		t.Fatalf("frame %q %s missing or non-object: %#v", frame.event, objectKey, frame.data[objectKey])
	}
	nested, ok := obj[nestedKey].(map[string]interface{})
	if !ok {
		t.Fatalf("frame %q %s.%s missing or non-object: %#v", frame.event, objectKey, nestedKey, obj[nestedKey])
	}
	got, ok := nested[fieldKey].(float64)
	if !ok {
		t.Fatalf("frame %q %s.%s.%s missing or non-numeric: %#v", frame.event, objectKey, nestedKey, fieldKey, nested[fieldKey])
	}
	if got != want {
		t.Fatalf("frame %q %s.%s.%s = %v, want %v", frame.event, objectKey, nestedKey, fieldKey, got, want)
	}
}

func collectSSEEvents(body string) []string {
	var events []string
	for _, line := range strings.Split(body, "\n") {
		if event, ok := strings.CutPrefix(line, "event: "); ok {
			events = append(events, event)
		}
	}
	return events
}

func TestHandleClaudeStreamOpus47CapacityLimitNeverReturnsEmptyBodyUnderConcurrency(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	for i := 0; i < 10; i++ {
		if err := config.AddAccount(config.Account{ID: fmt.Sprintf("acct-%d", i), Enabled: true, AccessToken: fmt.Sprintf("token-%d", i), ProfileArn: "arn:aws:codewhisperer:profile/test", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()

	oldGate := opus47AdmissionGate
	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opus47AdmissionGate = newOpus47Gate(100, 100)
	opusCapacityRetryBudget = time.Millisecond
	sleepForOpusCapacityRetry = func(d time.Duration) {}
	t.Cleanup(func() {
		opus47AdmissionGate = oldGate
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY","retry_after_seconds":0}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	var wg sync.WaitGroup
	errCh := make(chan string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := strings.NewReader(`{"model":"claude-opus-4.7","stream":true,"max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
			w := httptest.NewRecorder()
			h.handleClaudeMessagesInternal(w, req)
			respBody := w.Body.String()
			if w.Code == http.StatusOK && strings.TrimSpace(respBody) == "" {
				errCh <- "got HTTP 200 with empty body"
				return
			}
			if strings.TrimSpace(respBody) == "" {
				errCh <- fmt.Sprintf("got status %d with empty body", w.Code)
				return
			}
			if w.Code == http.StatusOK && !strings.Contains(respBody, "event: error") {
				errCh <- fmt.Sprintf("got HTTP 200 without SSE error body: %q", respBody)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var failures []string
	for msg := range errCh {
		failures = append(failures, msg)
	}
	if len(failures) > 0 {
		t.Fatalf("expected no empty streams, got %d failures, first: %s", len(failures), failures[0])
	}
}

func waitForAccountRequestCount(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		total := 0
		for _, account := range config.GetAccounts() {
			total += account.RequestCount
		}
		if total >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected async account stats request count >= %d", want)
}

func waitForAccountHealthWrite(t *testing.T, accountID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, account := range config.GetAccounts() {
			if account.ID == accountID && account.FailureCount > 0 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected account health write for %s", accountID)
}

func TestAdminAccountsExposeHealthCooldownFields(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{pool: &pool.AccountPool{}}
	account := config.Account{
		ID:                "acct-1",
		Email:             "user@example.com",
		Enabled:           true,
		LastFailureReason: "quota_exhausted",
		LastFailureAt:     123,
		CooldownUntil:     456,
		FailureCount:      2,
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	h.pool.Reload()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/accounts", nil)
	h.apiGetAccounts(w, req)

	var got []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode accounts response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one account, got %d", len(got))
	}
	if got[0]["lastFailureReason"] != "quota_exhausted" {
		t.Fatalf("expected lastFailureReason, got %#v", got[0]["lastFailureReason"])
	}
	if got[0]["lastFailureAt"] != float64(123) || got[0]["cooldownUntil"] != float64(456) || got[0]["failureCount"] != float64(2) {
		t.Fatalf("expected health fields in accounts response, got %#v", got[0])
	}
	health, ok := got[0]["runtimeHealth"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected runtimeHealth object, got %#v", got[0]["runtimeHealth"])
	}
	if health["score"] != float64(100) {
		t.Fatalf("expected default runtime health score 100, got %#v", health)
	}
}

func TestAdminAccountFullExposesHealthCooldownFields(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{pool: &pool.AccountPool{}}
	account := config.Account{
		ID:                "acct-1",
		Email:             "user@example.com",
		Enabled:           true,
		LastFailureReason: "rate_limited",
		LastFailureAt:     234,
		CooldownUntil:     567,
		FailureCount:      3,
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	h.pool.Reload()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/accounts/acct-1/full", nil)
	h.apiGetAccountFull(w, req, "acct-1")

	var got map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode account response: %v", err)
	}
	if got["lastFailureReason"] != "rate_limited" {
		t.Fatalf("expected lastFailureReason, got %#v", got["lastFailureReason"])
	}
	if got["lastFailureAt"] != float64(234) || got["cooldownUntil"] != float64(567) || got["failureCount"] != float64(3) {
		t.Fatalf("expected health fields in account detail response, got %#v", got)
	}
	health, ok := got["runtimeHealth"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected runtimeHealth object, got %#v", got["runtimeHealth"])
	}
	if health["activeConnections"] != float64(0) {
		t.Fatalf("expected runtime active connections 0, got %#v", health)
	}
}

func TestAdminAccountsExposeRiskGroupAndCooldownState(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	now := time.Now()
	accounts := []config.Account{
		{
			ID:                "acct-1",
			Email:             "one@example.com",
			Enabled:           true,
			UserId:            "d-shared.user-one",
			ProfileArn:        "arn:aws:codewhisperer:us-east-1:123:profile/shared",
			LastFailureReason: "temporary_limited",
			CooldownUntil:     now.Add(time.Minute).Unix(),
			FailureCount:      1,
		},
		{
			ID:         "acct-2",
			Email:      "two@example.com",
			Enabled:    true,
			UserId:     "d-shared.user-two",
			ProfileArn: "arn:aws:codewhisperer:us-east-1:123:profile/shared",
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/accounts", nil)

	h.apiGetAccounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two accounts, got %#v", rows)
	}
	for _, row := range rows {
		if row["riskGroupKey"] != "profile:arn:aws:codewhisperer:us-east-1:123:profile/shared" {
			t.Fatalf("expected profile risk group key, got %#v", row)
		}
		if row["riskGroupSize"] != float64(2) {
			t.Fatalf("expected shared risk group size 2, got %#v", row)
		}
	}
	if rows[0]["coolingDown"] != true {
		t.Fatalf("expected first account coolingDown=true, got %#v", rows[0])
	}
	if rows[1]["coolingDown"] != false || rows[1]["cooldownSource"] != "account" {
		t.Fatalf("expected second account to remain available after one account cooldown, got %#v", rows[1])
	}
	if remaining, ok := rows[0]["cooldownRemainingSeconds"].(float64); !ok || remaining < 50 || remaining > 65 {
		t.Fatalf("expected cooldown remaining around 60s, got %#v", rows[0])
	}
}

func TestAdminAccountsDoNotExposeEscalatedRiskGroupCooldownState(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{
			ID:         "acct-1",
			Email:      "one@example.com",
			Enabled:    true,
			ProfileArn: "arn:aws:codewhisperer:us-east-1:123:profile/shared",
		},
		{
			ID:         "acct-2",
			Email:      "two@example.com",
			Enabled:    true,
			ProfileArn: "arn:aws:codewhisperer:us-east-1:123:profile/shared",
		},
		{
			ID:         "acct-3",
			Email:      "three@example.com",
			Enabled:    true,
			ProfileArn: "arn:aws:codewhisperer:us-east-1:123:profile/shared",
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.RecordFailureUntil("acct-1", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	p.RecordFailureUntil("acct-2", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	h := &Handler{pool: p}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/accounts", nil)

	h.apiGetAccounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	for _, row := range rows {
		if row["id"] == "acct-3" && row["coolingDown"] != false {
			t.Fatalf("did not expect untouched shared account to inherit cooldown, got %#v", row)
		}
		if row["cooldownSource"] == "risk_group" {
			t.Fatalf("did not expect risk-group cooldown source after temporary limits, got %#v", row)
		}
	}
}

func TestRecordAccountFailureUsesRateLimitResetTime(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{ID: "acct-1", Email: "acct1@example.com", Enabled: true}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}
	resetAt := time.Now().Add(2 * time.Second).Truncate(time.Second)

	h.recordAccountFailure(account.ID, &rateLimitError{endpoint: "Kiro IDE", body: `{"message":"rate limited"}`, resetAt: resetAt})

	got := config.GetAccounts()[0]
	if got.LastFailureReason != "rate_limited" {
		t.Fatalf("expected rate_limited, got %q", got.LastFailureReason)
	}
	if got.CooldownUntil != resetAt.Unix() {
		t.Fatalf("expected cooldown until %d, got %d", resetAt.Unix(), got.CooldownUntil)
	}
	if got.CooldownUntil-time.Now().Unix() > 5 {
		t.Fatalf("expected short precise cooldown, got until %d", got.CooldownUntil)
	}
}

func TestRecordAccountFailureModelCapacityDoesNotMarkAccountFailed(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{ID: "acct-capacity", Email: "capacity@example.com", Enabled: true}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	reason := h.recordAccountFailure(account.ID, &rateLimitError{
		endpoint: "Kiro IDE",
		body:     `{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`,
		resetAt:  time.Now().Add(5 * time.Second),
	})

	if reason != pool.FailureReasonModelCapacity {
		t.Fatalf("expected model_capacity reason, got %q", reason)
	}
	got := config.GetAccounts()[0]
	if got.LastFailureReason != "" || got.FailureCount != 0 || got.CooldownUntil != 0 {
		t.Fatalf("expected account health to remain clean for model capacity, got %#v", got)
	}
	health := p.GetRuntimeHealth(account.ID)
	if health.RecentFailures != 0 || health.Score != 100 {
		t.Fatalf("expected runtime health to remain clean for model capacity, got %#v", health)
	}
}

func TestShouldWaitAndRetryOpus47DoesNotRetrySuspiciousTemporaryLimit(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	err := &rateLimitError{
		endpoint: "Kiro IDE",
		body:     `{"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`,
		resetAt:  time.Now().Add(5 * time.Second),
	}

	if shouldWaitAndRetryOpus47(err, "claude-opus-4.7") {
		t.Fatalf("expected suspicious temporary limit to stop Opus 4.7 retry loop")
	}
}

func TestAPITestAccountTreatsModelCapacityAsBusyNotFailed(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}
	account := config.Account{
		ID:          "acct-test-capacity",
		Email:       "capacity@example.com",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-test-capacity/test", strings.NewReader(`{"model":"claude-opus-4.7"}`))
	w := httptest.NewRecorder()

	h.apiTestAccount(w, req, account.ID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected capacity busy to use HTTP 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["success"] != true || resp["status"] != "capacity_busy" || resp["reason"] != string(pool.FailureReasonModelCapacity) {
		t.Fatalf("expected capacity_busy success response, got %#v", resp)
	}
	got := config.GetAccounts()[0]
	if got.LastFailureReason != "" || got.FailureCount != 0 || got.CooldownUntil != 0 {
		t.Fatalf("expected account health to remain clean for model capacity, got %#v", got)
	}
}

func TestAPITestAccountRecordsSuspiciousTemporaryLimitCooldown(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}
	account := config.Account{
		ID:          "acct-test-temp-limit",
		Email:       "temp-limit@example.com",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}
	errBody := `{"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(errBody)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-test-temp-limit/test", strings.NewReader(`{"model":"claude-sonnet-4"}`))
	w := httptest.NewRecorder()

	h.apiTestAccount(w, req, account.ID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected temporary limit test to use HTTP 200 UI response, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["success"] != true || resp["status"] != "temporary_limited" || resp["reason"] != string(pool.FailureReasonTemporaryLimited) {
		t.Fatalf("expected temporary_limited success response for UI, got %#v", resp)
	}
	if _, ok := resp["retry_after_seconds"].(float64); !ok {
		t.Fatalf("expected retry_after_seconds in response, got %#v", resp)
	}
	got := config.GetAccounts()[0]
	if got.LastFailureReason != "temporary_limited" || got.CooldownUntil <= time.Now().Unix() {
		t.Fatalf("expected account cooldown to be persisted, got %#v", got)
	}
}

func TestAPITestAccountSkipsUpstreamWhenTemporaryLimitCoolingDown(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:          "acct-test-temp-cooling",
		Email:       "temp-cooling@example.com",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.RecordFailureUntil(account.ID, pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	h := &Handler{pool: p}
	upstreamCalled := false
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-test-temp-cooling/test", strings.NewReader(`{"model":"claude-sonnet-4"}`))
	w := httptest.NewRecorder()

	h.apiTestAccount(w, req, account.ID)

	if upstreamCalled {
		t.Fatalf("expected test endpoint to skip upstream while account is cooling down")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected cooldown UI response to use HTTP 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["success"] != true || resp["status"] != "temporary_limited" {
		t.Fatalf("expected temporary_limited cooldown response, got %#v", resp)
	}
	retryAfter, ok := resp["retry_after_seconds"].(float64)
	if !ok || retryAfter < 25 || retryAfter > 65 {
		t.Fatalf("expected retry_after_seconds from existing cooldown, got %#v", resp)
	}
}

func TestAPITestAccountAllowsSharedProfileUpstreamAfterSingleTemporaryLimit(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{
			ID:          "acct-limited",
			Email:       "limited@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "acct-shared",
			Email:       "shared@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.RecordFailureUntil("acct-limited", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	h := &Handler{pool: p}
	upstreamCalled := false
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-shared/test", strings.NewReader(`{"model":"claude-sonnet-4"}`))
	w := httptest.NewRecorder()

	h.apiTestAccount(w, req, "acct-shared")

	if !upstreamCalled {
		t.Fatalf("expected shared-profile account test to call upstream after one account temporary limit")
	}
}

func TestAPITestAccountAllowsSharedProfileUpstreamAfterMultipleTemporaryLimits(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{
			ID:          "acct-limited-1",
			Email:       "limited1@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "acct-limited-2",
			Email:       "limited2@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "acct-shared",
			Email:       "shared@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/shared",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.RecordFailureUntil("acct-limited-1", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	p.RecordFailureUntil("acct-limited-2", pool.FailureReasonTemporaryLimited, time.Now().Add(30*time.Second))
	h := &Handler{pool: p}
	upstreamCalled := false
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-shared/test", strings.NewReader(`{"model":"claude-sonnet-4"}`))
	w := httptest.NewRecorder()

	h.apiTestAccount(w, req, "acct-shared")

	if !upstreamCalled {
		t.Fatalf("expected untouched shared-profile account test to call upstream")
	}
}

func TestAPITestAccountThrottlesBackToBackGenerationTests(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:          "acct-test-throttle",
		Email:       "test-throttle@example.com",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}
	originalSpacing := adminAccountTestMinSpacing
	adminAccountTestMinSpacing = 3 * time.Second
	t.Cleanup(func() { adminAccountTestMinSpacing = originalSpacing })

	upstreamCalls := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	firstReq := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-test-throttle/test", strings.NewReader(`{"model":"claude-sonnet-4"}`))
	firstW := httptest.NewRecorder()
	h.apiTestAccount(firstW, firstReq, account.ID)

	secondReq := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/acct-test-throttle/test", strings.NewReader(`{"model":"claude-sonnet-4"}`))
	secondW := httptest.NewRecorder()
	h.apiTestAccount(secondW, secondReq, account.ID)

	if upstreamCalls != 1 {
		t.Fatalf("expected second back-to-back admin test to skip upstream, got %d upstream calls", upstreamCalls)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(secondW.Body).Decode(&resp); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if resp["success"] != true || resp["status"] != "test_throttled" {
		t.Fatalf("expected test_throttled response, got %#v", resp)
	}
	if retryAfter, ok := resp["retry_after_seconds"].(float64); !ok || retryAfter < 1 {
		t.Fatalf("expected retry_after_seconds, got %#v", resp)
	}
}

func TestRecordAccountFailureSuspiciousTemporaryLimitUsesAdaptiveCooldown(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{ID: "acct-1", Email: "acct1@example.com", Enabled: true}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}
	errBody := `{"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`

	reason := h.recordAccountFailure(account.ID, &rateLimitError{endpoint: "AmazonQ", body: errBody, resetAt: time.Now().Add(5 * time.Second)})

	if reason != pool.FailureReasonTemporaryLimited {
		t.Fatalf("expected temporary_limited reason, got %q", reason)
	}
	got := config.GetAccounts()[0]
	if got.LastFailureReason != "temporary_limited" {
		t.Fatalf("expected temporary_limited persisted reason, got %q", got.LastFailureReason)
	}
	remaining := got.CooldownUntil - time.Now().Unix()
	if remaining < 2 || remaining > 5 {
		t.Fatalf("expected single-account adaptive cooldown around 3s, got %ds", remaining)
	}
	if !shouldRetryAccount(reason, 0) {
		t.Fatalf("expected temporary_limited to try another account for the current request")
	}
	status, errType := claudeUpstreamErrorStatusAndType(&rateLimitError{endpoint: "AmazonQ", body: errBody, resetAt: time.Now().Add(5 * time.Second)})
	if status != http.StatusTooManyRequests || errType != "rate_limit_error" {
		t.Fatalf("expected Claude 429 rate_limit_error, got %d %s", status, errType)
	}
	headers := claudeErrorHeadersForUpstreamError(&rateLimitError{endpoint: "AmazonQ", body: errBody, resetAt: time.Now().Add(5 * time.Second)})
	retryAfter, err := strconv.Atoi(headers.Get("Retry-After"))
	if err != nil {
		t.Fatalf("expected Retry-After header, got %q", headers.Get("Retry-After"))
	}
	if got := headers.Get("X-Kiro-Go-Error-Reason"); got != "TEMPORARY_LIMITED" {
		t.Fatalf("expected TEMPORARY_LIMITED reason header, got %q", got)
	}
	if retryAfter < 55 || retryAfter > 65 {
		t.Fatalf("expected public Retry-After around 60s for upstream suspicious temporary limit, got %d", retryAfter)
	}
}

func TestHandleClaudeTemporaryLimitFallsThroughToNextAccount(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}
	if err := config.UpdateLoadBalanceConfig(config.LoadBalanceConfig{Strategy: config.LoadBalanceStrategyRoundRobin}); err != nil {
		t.Fatalf("update load balance strategy: %v", err)
	}
	accounts := []config.Account{
		{ID: "acct-1", Email: "one@example.com", Enabled: true, AccessToken: "token-1", ProfileArn: "arn:shared", ExpiresAt: time.Now().Add(time.Hour).Unix()},
		{ID: "acct-2", Email: "two@example.com", Enabled: true, AccessToken: "token-2", ProfileArn: "arn:shared", ExpiresAt: time.Now().Add(time.Hour).Unix()},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}

	p := &pool.AccountPool{}
	p.Reload()
	p.SetStrategy(pool.StrategyRoundRobin)
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
	errBody := `{"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`
	var seen []string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			authHeader := req.Header.Get("Authorization")
			seen = append(seen, authHeader)
			if strings.Contains(authHeader, "token-1") {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(errBody)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected second account to satisfy request, got %d body=%s", w.Code, w.Body.String())
	}
	if len(seen) != 2 {
		t.Fatalf("expected first temporary-limited account and second fallback account, got %#v", seen)
	}
	rows := config.GetAccounts()
	byID := map[string]config.Account{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if byID["acct-1"].LastFailureReason != string(pool.FailureReasonTemporaryLimited) || byID["acct-1"].CooldownUntil <= time.Now().Unix() {
		t.Fatalf("expected acct-1 temporary-limited cooldown, got %#v", byID["acct-1"])
	}
	if byID["acct-2"].LastFailureReason != "" || byID["acct-2"].CooldownUntil != 0 {
		t.Fatalf("did not expect acct-2 to inherit cooldown, got %#v", byID["acct-2"])
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	trace := logs[0].AttemptTrace
	if len(trace) < 4 {
		t.Fatalf("expected selection/failure/success trace entries, got %#v", trace)
	}
	if trace[0].Event != "selected" || trace[0].AccountID != "acct-1" || trace[0].Attempt != 1 {
		t.Fatalf("expected first trace entry to select acct-1, got %#v", trace[0])
	}
	if trace[1].Event != "failure" || trace[1].AccountID != "acct-1" || trace[1].Reason != string(pool.FailureReasonTemporaryLimited) {
		t.Fatalf("expected second trace entry to record acct-1 temporary limit, got %#v", trace[1])
	}
	if trace[2].Event != "selected" || trace[2].AccountID != "acct-2" || trace[2].Attempt != 2 {
		t.Fatalf("expected third trace entry to select acct-2, got %#v", trace[2])
	}
	if trace[3].Event != "success" || trace[3].AccountID != "acct-2" || trace[3].Attempt != 2 {
		t.Fatalf("expected fourth trace entry to record acct-2 success, got %#v", trace[3])
	}
	waitForAccountRequestCount(t, 1)
}

func TestSendNoAvailableAccountsMapsTemporaryLimitedPoolToClaudeRetryableRateLimit(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:          "acct-temp-limited",
		Email:       "acct@example.com",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList(account.ID, []string{"claude-opus-4.7"})
	p.RecordFailureUntil(account.ID, pool.FailureReasonTemporaryLimited, time.Now().Add(5*time.Second))
	h := &Handler{pool: p}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.sendNoAvailableAccountsError(w, r, "claude-opus-4.7", nil, true)

	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("sub2api Opus 4.7 pool temporary limit contract must not return 429, body %s", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected retryable unavailable status, got %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header")
	}
	var resp struct {
		Type  string            `json:"type"`
		Error map[string]string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error["type"] != "overloaded_error" {
		t.Fatalf("expected overloaded_error for downstream retry/failover, got %#v", resp)
	}
	if !strings.Contains(resp.Error["message"], "TEMPORARY_LIMITED") {
		t.Fatalf("expected temporary limit reason in message, got %#v", resp)
	}
}

func TestSendNoAvailableAccountsDoesNotReportTemporaryLimitWhileAccountsRemainSchedulable(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{
			ID:          "acct-temp-limited",
			Email:       "limited@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/test",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
		{
			ID:          "acct-healthy",
			Email:       "healthy@example.com",
			Enabled:     true,
			AccessToken: "token",
			ProfileArn:  "arn:aws:codewhisperer:profile/other",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList("acct-temp-limited", []string{"claude-opus-4.7"})
	p.SetModelList("acct-healthy", []string{"claude-opus-4.7"})
	p.RecordFailureUntil("acct-temp-limited", pool.FailureReasonTemporaryLimited, time.Now().Add(5*time.Second))
	h := &Handler{pool: p}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.sendNoAvailableAccountsError(w, r, "claude-opus-4.7", nil, true)

	if w.Header().Get("X-Kiro-Go-Error-Reason") == "TEMPORARY_LIMITED" {
		t.Fatalf("did not expect pool temporary limit while a matching account remains schedulable")
	}
	if strings.Contains(w.Body.String(), "TEMPORARY_LIMITED") {
		t.Fatalf("did not expect temporary limit message while a matching account remains schedulable: %s", w.Body.String())
	}
}

func TestResolveClaudeThinkingModeHonorsRequestThinking(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		thinking     *ClaudeThinkingConfig
		wantModel    string
		wantThinking bool
	}{
		{
			name:         "adaptive request enables thinking",
			model:        "claude-sonnet-4.6",
			thinking:     &ClaudeThinkingConfig{Type: "adaptive"},
			wantModel:    "claude-sonnet-4.6",
			wantThinking: true,
		},
		{
			name:         "enabled request enables thinking",
			model:        "claude-opus-4.5",
			thinking:     &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
			wantModel:    "claude-opus-4.5",
			wantThinking: true,
		},
		{
			name:         "disabled request keeps thinking off",
			model:        "claude-opus-4.7",
			thinking:     &ClaudeThinkingConfig{Type: "disabled"},
			wantModel:    "claude-opus-4.7",
			wantThinking: false,
		},
		{
			name:         "suffix remains supported when thinking is disabled",
			model:        "claude-sonnet-4.5-thinking",
			thinking:     &ClaudeThinkingConfig{Type: "disabled"},
			wantModel:    "claude-sonnet-4.5",
			wantThinking: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotModel, gotThinking := resolveClaudeThinkingMode(tc.model, tc.thinking, "-thinking")
			if gotModel != tc.wantModel {
				t.Fatalf("expected model %q, got %q", tc.wantModel, gotModel)
			}
			if gotThinking != tc.wantThinking {
				t.Fatalf("expected thinking=%v, got %v", tc.wantThinking, gotThinking)
			}
		})
	}
}

func TestNormalizeOpus47ClaudeRequestUsesAdaptiveThinkingAndDropsSampling(t *testing.T) {
	req := ClaudeRequest{
		Model:       "claude-opus-4-7",
		MaxTokens:   64,
		Temperature: 0.7,
		TopP:        0.9,
		Thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048, Display: "summarized"},
		Messages:    []ClaudeMessage{{Role: "user", Content: "hello"}},
	}
	meta := normalizeOpus47ClaudeRequest(&req, true)
	if req.Model != "claude-opus-4.7" {
		t.Fatalf("model = %q, want claude-opus-4.7", req.Model)
	}
	if req.Temperature != 0 || req.TopP != 0 {
		t.Fatalf("expected sampling params dropped, got temperature=%v top_p=%v", req.Temperature, req.TopP)
	}
	if req.Thinking == nil || req.Thinking.Type != "adaptive" || req.Thinking.BudgetTokens != 0 {
		t.Fatalf("expected adaptive thinking without budget, got %#v", req.Thinking)
	}
	if !meta.Opus47 || !meta.ThinkingNormalized || !meta.SamplingDropped {
		t.Fatalf("expected metadata flags, got %#v", meta)
	}
}

func TestNormalizeOpus47ClaudeRequestLeavesNonOpusRequestUnchanged(t *testing.T) {
	for _, model := range []string{
		"claude-sonnet-5-latest",
		"claude-sonnet-4-5",
		"claude-3-sonnet",
	} {
		t.Run(model, func(t *testing.T) {
			req := ClaudeRequest{
				Model:       model,
				MaxTokens:   64,
				Temperature: 0.7,
				TopP:        0.9,
				Thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048, Display: "summarized"},
				Messages:    []ClaudeMessage{{Role: "user", Content: "hello"}},
			}
			meta := normalizeOpus47ClaudeRequest(&req, true)
			if req.Model != model {
				t.Fatalf("expected model unchanged, got %q", req.Model)
			}
			if req.Temperature != 0.7 || req.TopP != 0.9 {
				t.Fatalf("expected sampling params unchanged, got temperature=%v top_p=%v", req.Temperature, req.TopP)
			}
			if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != 2048 || req.Thinking.Display != "summarized" {
				t.Fatalf("expected thinking unchanged, got %#v", req.Thinking)
			}
			if meta.Opus47 || meta.ThinkingNormalized || meta.SamplingDropped {
				t.Fatalf("expected empty metadata, got %#v", meta)
			}
		})
	}
}

func TestCloneClaudeRequestForThinkingInjectsPromptWithoutMutatingOriginal(t *testing.T) {
	req := &ClaudeRequest{
		Model:  "claude-sonnet-4.6",
		System: "Follow the user instructions.",
	}

	cloned := cloneClaudeRequestForThinking(req, true)
	blocks, ok := cloned.System.([]interface{})
	if !ok {
		t.Fatalf("expected cloned system prompt to be structured blocks, got %T", cloned.System)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 system blocks after prepend, got %d", len(blocks))
	}
	gotPrompt := extractSystemPrompt(cloned.System)
	expected := ThinkingModePrompt + "\n\nFollow the user instructions."
	if gotPrompt != expected {
		t.Fatalf("expected injected system prompt %q, got %q", expected, gotPrompt)
	}
	if original, ok := req.System.(string); !ok || original != "Follow the user instructions." {
		t.Fatalf("expected original request system prompt to stay unchanged, got %#v", req.System)
	}
}

func TestCloneClaudeRequestForThinkingPreservesStructuredSystemBlocks(t *testing.T) {
	req := &ClaudeRequest{
		Model: "claude-sonnet-4.6",
		System: []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "cached system",
				"cache_control": map[string]interface{}{
					"type": "ephemeral",
					"ttl":  "5m",
				},
			},
		},
	}

	cloned := cloneClaudeRequestForThinking(req, true)
	blocks, ok := cloned.System.([]interface{})
	if !ok {
		t.Fatalf("expected structured system blocks, got %T", cloned.System)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 system blocks after prepend, got %d", len(blocks))
	}
	first, ok := blocks[0].(map[string]interface{})
	if !ok || first["text"] != ThinkingModePrompt+"\n" {
		t.Fatalf("expected first block to be thinking prompt, got %#v", blocks[0])
	}
	second, ok := blocks[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected original system block to remain a map, got %T", blocks[1])
	}
	cacheControl, ok := second["cache_control"].(map[string]interface{})
	if !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("expected original cache_control to be preserved, got %#v", second["cache_control"])
	}
}

func TestThinkingPromptAffectsClaudeTokenEstimate(t *testing.T) {
	req := &ClaudeRequest{
		Model:    "claude-sonnet-4.6",
		Messages: []ClaudeMessage{{Role: "user", Content: "hello"}},
	}

	baseTokens := estimateClaudeRequestInputTokens(req)
	thinkingTokens := estimateClaudeRequestInputTokens(cloneClaudeRequestForThinking(req, true))

	if thinkingTokens <= baseTokens {
		t.Fatalf("expected thinking tokens (%d) to exceed base tokens (%d)", thinkingTokens, baseTokens)
	}
}

func TestAdminHealthCheckConfigRejectsInvalidInterval(t *testing.T) {
	dir := t.TempDir()
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("test-password")

	h := &Handler{
		pool:               pool.GetPool(),
		healthCheckUpdated: make(chan struct{}, 1),
	}

	body := strings.NewReader(`{"enabled":true,"intervalMinutes":4,"autoDisableUnhealthy":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/health-check", body)
	req.Header.Set("X-Admin-Password", "test-password")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body %s", w.Code, w.Body.String())
	}
}

func TestAdminHealthCheckConfigUpdateAndGet(t *testing.T) {
	dir := t.TempDir()
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("test-password")

	h := &Handler{
		pool:               pool.GetPool(),
		healthCheckUpdated: make(chan struct{}, 1),
	}

	body := strings.NewReader(`{"enabled":true,"intervalMinutes":15,"autoDisableUnhealthy":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/health-check", body)
	req.Header.Set("X-Admin-Password", "test-password")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/api/health-check", nil)
	getReq.Header.Set("X-Admin-Password", "test-password")
	getW := httptest.NewRecorder()

	h.handleAdminAPI(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected get status 200, got %d body %s", getW.Code, getW.Body.String())
	}
	if !strings.Contains(getW.Body.String(), `"intervalMinutes":15`) {
		t.Fatalf("expected saved interval in response, got %s", getW.Body.String())
	}
	if !strings.Contains(getW.Body.String(), `"autoDisableUnhealthy":true`) {
		t.Fatalf("expected saved auto-disable in response, got %s", getW.Body.String())
	}
}

func TestAdminPageDisablesCaching(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()

	h.serveAdminPage(w, req)

	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("expected admin page to disable caching, got %q", got)
	}
}

func TestFaviconServed(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected favicon status 200, got %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "image/svg+xml") {
		t.Fatalf("expected svg favicon content type, got %q", got)
	}
}

func TestDashboardRefreshRefreshesVisibleRuntimeData(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "web", "index.html"))
	if err != nil {
		t.Fatalf("read admin page: %v", err)
	}

	html := string(body)
	expectations := map[string]string{
		"active tab state":               "let currentTab = 'accounts';",
		"visible refresh coordinator":    "async function refreshVisibleData()",
		"settings runtime refresh":       "loadRequestLogs()",
		"auto refresh status only":       "loadAutoRefreshConfig({ updateFields: false })",
		"health check status only":       "loadHealthCheckConfig({ updateFields: false })",
		"detail modal state":             "let currentDetailAccountId = '';",
		"detail draft preservation":      "getDetailDraftValues()",
		"detail modal refresh":           "refreshOpenAccountDetail()",
		"overlap guard":                  "refreshInFlight",
		"switch tab immediate refresh":   "refreshVisibleData();",
		"visible page immediate refresh": "document.addEventListener('visibilitychange'",
	}
	for name, needle := range expectations {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected admin page refresh behavior %q to contain %q", name, needle)
		}
	}
}

func TestAdminAutoRefreshConfigRunsImmediatelyWhenEnabled(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("test-password")

	h := &Handler{
		pool:               &pool.AccountPool{},
		autoRefreshUpdated: make(chan struct{}, 1),
	}

	body := strings.NewReader(`{"enabled":true,"intervalMinutes":5,"scope":"all"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/auto-refresh", body)
	req.Header.Set("X-Admin-Password", "test-password")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	status := waitForAutoRefreshFinished(t, h)
	if status.LastStartedAt == 0 || status.LastFinishedAt == 0 {
		t.Fatalf("expected enabled auto refresh save to run immediately, got %#v", status)
	}
}

func TestAdminHealthCheckConfigRunsImmediatelyWhenEnabled(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("test-password")

	h := &Handler{
		pool:               &pool.AccountPool{},
		healthCheckUpdated: make(chan struct{}, 1),
	}

	body := strings.NewReader(`{"enabled":true,"intervalMinutes":5,"autoDisableUnhealthy":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/health-check", body)
	req.Header.Set("X-Admin-Password", "test-password")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	status := waitForHealthCheckFinished(t, h)
	if status.LastStartedAt == 0 || status.LastFinishedAt == 0 {
		t.Fatalf("expected enabled health check save to run immediately, got %#v", status)
	}
}

func waitForAutoRefreshFinished(t *testing.T, h *Handler) autoRefreshStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var status autoRefreshStatus
	for time.Now().Before(deadline) {
		status = h.getAutoRefreshStatus()
		if status.LastStartedAt != 0 && status.LastFinishedAt != 0 && !status.Running {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	return status
}

func waitForHealthCheckFinished(t *testing.T, h *Handler) healthCheckStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var status healthCheckStatus
	for time.Now().Before(deadline) {
		status = h.getHealthCheckStatus()
		if status.LastStartedAt != 0 && status.LastFinishedAt != 0 && !status.Running {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	return status
}

func TestCheckAccountHealthValidatesTokenBeforeProbe(t *testing.T) {
	h := &Handler{}
	account := &config.Account{ID: "account-1", Email: "test@example.com"}

	var calls []string
	ensureValidTokenForHealthCheck = func(h *Handler, account *config.Account) error {
		calls = append(calls, "ensure")
		return nil
	}
	listAvailableModelsForHealthCheck = func(account *config.Account) ([]ModelInfo, error) {
		calls = append(calls, "models")
		return []ModelInfo{{ModelId: "claude-sonnet-4.5"}}, nil
	}
	t.Cleanup(func() {
		ensureValidTokenForHealthCheck = defaultEnsureValidTokenForHealthCheck
		listAvailableModelsForHealthCheck = ListAvailableModels
	})

	if err := h.checkAccountHealth(account); err != nil {
		t.Fatalf("expected health check to pass, got %v", err)
	}
	if got := strings.Join(calls, ","); got != "ensure,models" {
		t.Fatalf("expected ensure before model list, got %q", got)
	}

	calls = nil
	ensureErr := errors.New("refresh failed")
	ensureValidTokenForHealthCheck = func(h *Handler, account *config.Account) error {
		calls = append(calls, "ensure")
		return ensureErr
	}
	listAvailableModelsForHealthCheck = func(account *config.Account) ([]ModelInfo, error) {
		calls = append(calls, "models")
		return []ModelInfo{{ModelId: "claude-sonnet-4.5"}}, nil
	}

	if err := h.checkAccountHealth(account); !errors.Is(err, ensureErr) {
		t.Fatalf("expected ensure error, got %v", err)
	}
	if got := strings.Join(calls, ","); got != "ensure" {
		t.Fatalf("expected model list to be skipped after ensure error, got %q", got)
	}
}

func TestValidateClaudeThinkingConfig(t *testing.T) {
	tests := []struct {
		name        string
		thinking    *ClaudeThinkingConfig
		maxTokens   int
		expectError bool
	}{
		{
			name:        "adaptive is valid",
			thinking:    &ClaudeThinkingConfig{Type: "adaptive"},
			maxTokens:   4096,
			expectError: false,
		},
		{
			name:        "enabled requires budget",
			thinking:    &ClaudeThinkingConfig{Type: "enabled"},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "enabled requires at least 1024 budget tokens",
			thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 512},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "enabled rejects max tokens zero",
			thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
			maxTokens:   0,
			expectError: true,
		},
		{
			name:        "enabled budget must stay below max tokens",
			thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 4096},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "disabled rejects display",
			thinking:    &ClaudeThinkingConfig{Type: "disabled", Display: "summarized"},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "missing type is rejected",
			thinking:    &ClaudeThinkingConfig{},
			maxTokens:   4096,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := validateClaudeThinkingConfig(tc.thinking, tc.maxTokens)
			if tc.expectError && errMsg == "" {
				t.Fatalf("expected validation error")
			}
			if !tc.expectError && errMsg != "" {
				t.Fatalf("expected thinking config to be valid, got %q", errMsg)
			}
		})
	}
}

func TestResolveClaudeThinkingResponseOptions(t *testing.T) {
	tests := []struct {
		name       string
		thinking   *ClaudeThinkingConfig
		defaultFmt string
		wantFmt    string
		wantOmit   bool
	}{
		{
			name:       "default config is preserved when display unset",
			thinking:   &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
			defaultFmt: "think",
			wantFmt:    "think",
			wantOmit:   false,
		},
		{
			name:       "summarized forces official thinking blocks",
			thinking:   &ClaudeThinkingConfig{Type: "adaptive", Display: "summarized"},
			defaultFmt: "reasoning_content",
			wantFmt:    "thinking",
			wantOmit:   false,
		},
		{
			name:       "omitted forces official thinking blocks and hides content",
			thinking:   &ClaudeThinkingConfig{Type: "adaptive", Display: "omitted"},
			defaultFmt: "think",
			wantFmt:    "thinking",
			wantOmit:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := resolveClaudeThinkingResponseOptions(tc.thinking, tc.defaultFmt)
			if opts.Format != tc.wantFmt {
				t.Fatalf("expected format %q, got %q", tc.wantFmt, opts.Format)
			}
			if opts.OmitDisplay != tc.wantOmit {
				t.Fatalf("expected omitDisplay=%v, got %v", tc.wantOmit, opts.OmitDisplay)
			}
		})
	}
}

func TestMergeUniqueModelsPreservesUnionAcrossAccounts(t *testing.T) {
	base := []ModelInfo{
		{ModelId: "claude-sonnet-4.5", InputTypes: []string{"TEXT"}},
	}
	incoming := []ModelInfo{
		{ModelId: "claude-sonnet-4.5", InputTypes: []string{"image"}},
		{ModelId: "claude-opus-4-7", InputTypes: []string{"text"}},
	}

	merged := mergeUniqueModels(base, incoming)
	if len(merged) != 2 {
		t.Fatalf("expected 2 unique models, got %d", len(merged))
	}
	if !modelSupportsImage(merged[0].InputTypes) {
		t.Fatalf("expected merged input types to preserve image capability, got %#v", merged[0].InputTypes)
	}
	if merged[1].ModelId != "claude-opus-4-7" {
		t.Fatalf("expected second model to be claude-opus-4-7, got %q", merged[1].ModelId)
	}
}

func TestRefreshModelsCacheSkipsDuringOpusQuietMode(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "acct-1", Email: "one@example.com", Enabled: true, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	oldGate := modelAdmissionGate
	now := time.Unix(2000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(time.Minute))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(time.Minute))
	oldList := listAvailableModelsForCache
	var calls int
	listAvailableModelsForCache = func(account *config.Account) ([]ModelInfo, error) {
		calls++
		return []ModelInfo{{ModelId: "claude-opus-4.7"}}, nil
	}
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		listAvailableModelsForCache = oldList
	})

	h := &Handler{pool: pool.GetPool()}
	h.pool.Reload()
	h.refreshModelsCache()

	if calls != 0 {
		t.Fatalf("expected quiet-mode refresh to skip upstream calls, got %d", calls)
	}
}

func TestRefreshModelsCacheStopsAfterConsecutivePressureFailures(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	for i := 1; i <= 3; i++ {
		if err := config.AddAccount(config.Account{
			ID:          fmt.Sprintf("acct-%d", i),
			Email:       fmt.Sprintf("account-%d@example.com", i),
			Enabled:     true,
			AccessToken: "token",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}

	oldGate := modelAdmissionGate
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{})
	oldList := listAvailableModelsForCache
	var calls []string
	listAvailableModelsForCache = func(account *config.Account) ([]ModelInfo, error) {
		calls = append(calls, account.ID)
		return nil, errors.New(`HTTP 429: {"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`)
	}
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		listAvailableModelsForCache = oldList
	})

	h := &Handler{pool: pool.GetPool()}
	h.pool.Reload()
	h.refreshModelsCache()

	if len(calls) != modelCachePressureFailureLimit {
		t.Fatalf("expected refresh to stop after %d pressure failures, got calls %#v", modelCachePressureFailureLimit, calls)
	}
	state := h.pool.CooldownState("acct-1", time.Now())
	if !state.CoolingDown || state.Reason != pool.FailureReasonTemporaryLimited {
		t.Fatalf("expected first failed account to enter temporary-limit cooldown, got %#v", state)
	}
}

func TestAPIRefreshAllAccountsModelsHonorsOpusQuietMode(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "acct-1", Email: "one@example.com", Enabled: true, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	oldGate := modelAdmissionGate
	now := time.Unix(2000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(time.Minute))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(time.Minute))
	oldList := listAvailableModelsForCache
	listAvailableModelsForCache = func(account *config.Account) ([]ModelInfo, error) {
		t.Fatalf("list models should not be called in quiet mode")
		return nil, nil
	}
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		listAvailableModelsForCache = oldList
	})

	h := &Handler{pool: pool.GetPool(), cachedModels: []ModelInfo{{ModelId: "claude-opus-4.7"}}}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/models/refresh", nil)
	w := httptest.NewRecorder()

	h.apiRefreshAllAccountsModels(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected quiet-mode refresh to return 429, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "opus_4_7_quiet_mode") {
		t.Fatalf("expected quiet-mode reason, got %s", w.Body.String())
	}
}

func TestAPIGetAccountModelsSkipsUpstreamForCoolingAccount(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:                "acct-cooling",
		Email:             "cooling@example.com",
		Enabled:           true,
		AccessToken:       "token",
		ExpiresAt:         time.Now().Add(time.Hour).Unix(),
		LastFailureReason: string(pool.FailureReasonTemporaryLimited),
		CooldownUntil:     time.Now().Add(time.Minute).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	oldGate := modelAdmissionGate
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{})
	oldList := listAvailableModelsForCache
	listAvailableModelsForCache = func(account *config.Account) ([]ModelInfo, error) {
		t.Fatalf("list models should not be called for cooling account")
		return nil, nil
	}
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		listAvailableModelsForCache = oldList
	})

	p := pool.GetPool()
	p.Reload()
	p.SetModelList(account.ID, []string{"claude-opus-4.7"})
	h := &Handler{pool: p}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/accounts/acct-cooling/models", nil)
	w := httptest.NewRecorder()

	h.apiGetAccountModels(w, req, account.ID)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected cooling account model probe to return 429, got %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "claude-opus-4.7") {
		t.Fatalf("expected cached models in response, got %s", w.Body.String())
	}
}

func TestBuildAnthropicModelsResponseGeneratesThinkingVariants(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{
		ModelId:    "claude-sonnet-4.5",
		InputTypes: []string{"text", "image"},
	}}, "-thinking")

	if len(models) != 2 {
		t.Fatalf("expected base model and thinking variant, got %d", len(models))
	}
	if models[0]["id"] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected base model id: %#v", models[0]["id"])
	}
	if models[1]["id"] != "claude-sonnet-4.5-thinking" {
		t.Fatalf("unexpected thinking model id: %#v", models[1]["id"])
	}
	for _, field := range []string{"type", "object", "display_name", "created_at", "owned_by"} {
		if _, ok := models[0][field]; !ok {
			t.Fatalf("expected Anthropic model field %q in %#v", field, models[0])
		}
	}
	for _, field := range []string{"supports_image", "input_modalities", "modalities", "capabilities", "info"} {
		if _, ok := models[0][field]; ok {
			t.Fatalf("expected public Anthropic model to exclude %q: %#v", field, models[0])
		}
	}
}

func TestAnthropicModelsResponseIncludesAliasesWithoutExtraFields(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{
		ModelId:    "claude-sonnet-4.5",
		InputTypes: []string{"text", "image"},
	}}, "-thinking")
	models = append(models, buildModelInfo("auto", "kiro-proxy", true))

	seenAuto := false
	for _, model := range models {
		if model["id"] == "auto" {
			seenAuto = true
		}
		for _, field := range []string{"id", "type", "object", "display_name", "created_at", "owned_by"} {
			if _, ok := model[field]; !ok {
				t.Fatalf("model %v missing Anthropic field %q", model["id"], field)
			}
		}
		for _, field := range []string{"supports_image", "input_modalities", "modalities", "capabilities", "info"} {
			if _, ok := model[field]; ok {
				t.Fatalf("model %v contains non-Anthropic field %q", model["id"], field)
			}
		}
	}
	if !seenAuto {
		t.Fatalf("expected alias model auto in response: %#v", models)
	}
}

func TestAnthropicModelsIncludesOfficialAndKiroOpus47NamesWithoutDuplicateIDs(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{ModelId: "claude-opus-4.7", ModelName: "Claude Opus", InputTypes: []string{"text"}}}, "-thinking")
	ids := map[string]int{}
	for _, model := range models {
		id, _ := model["id"].(string)
		ids[id]++
	}
	if ids["claude-opus-4.7"] != 1 {
		t.Fatalf("expected one kiro opus id, got ids=%#v", ids)
	}
	if ids["claude-opus-4-7"] != 1 {
		t.Fatalf("expected one official opus id, got ids=%#v", ids)
	}
	for id, count := range ids {
		if count != 1 {
			t.Fatalf("duplicate model id %q count=%d", id, count)
		}
	}
}

func TestClaudeErrorSetsRequestIDAndRetryAfter(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	w.Header().Set("request-id", "req_existing")
	w.Header().Set("x-request-id", "req_existing")

	h.sendClaudeErrorWithHeaders(w, http.StatusTooManyRequests, "rate_limit_error", "retry later", http.Header{
		"Retry-After": []string{"7"},
	})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", w.Code)
	}
	if got := w.Header().Get("request-id"); got != "req_existing" {
		t.Fatalf("request-id = %q", got)
	}
	if got := w.Header().Get("x-request-id"); got != "req_existing" {
		t.Fatalf("x-request-id = %q", got)
	}
	if got := w.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q", got)
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Type != "error" || body.Error.Type != "rate_limit_error" || body.Error.Message != "retry later" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}

func TestClaudeCodeToolReferenceFixtureParses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "claude_code_tool_reference_message.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("anthropic-beta", "tool-search-2025-10-19")

	env, err := parseAnthropicEnvelope(req, body)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if !env.HasBetaPrefix("tool-search") {
		t.Fatalf("expected tool-search beta")
	}
	if len(env.Request.ToolReferences) != 1 {
		t.Fatalf("expected one tool_reference, got %#v", env.Request.ToolReferences)
	}
	ref := env.Request.ToolReferences[0]
	if ref.Type != "tool_reference" || ref.Name == "" {
		t.Fatalf("unexpected tool_reference: %#v", ref)
	}
}
