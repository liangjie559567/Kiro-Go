package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestValidateClaudeRequestShapeRejectsAssistantPrefill(t *testing.T) {
	req := &ClaudeRequest{
		Messages: []ClaudeMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "prefill"},
		},
	}

	if msg := validateClaudeRequestShape(req); msg == "" {
		t.Fatalf("expected assistant-prefill final message to be rejected")
	}
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

func TestCheckAccountHealthValidatesTokenBeforeModelLoad(t *testing.T) {
	h := &Handler{}
	account := &config.Account{ID: "account-1", Email: "test@example.com"}

	var calls []string
	ensureValidTokenForHealthCheck = func(h *Handler, account *config.Account) error {
		calls = append(calls, "ensure")
		return nil
	}
	listAvailableModelsForHealthCheck = func(account *config.Account) ([]ModelInfo, error) {
		calls = append(calls, "list")
		return nil, nil
	}
	t.Cleanup(func() {
		ensureValidTokenForHealthCheck = defaultEnsureValidTokenForHealthCheck
		listAvailableModelsForHealthCheck = ListAvailableModels
	})

	if err := h.checkAccountHealth(account); err != nil {
		t.Fatalf("expected health check to pass, got %v", err)
	}
	if got := strings.Join(calls, ","); got != "ensure,list" {
		t.Fatalf("expected ensure before list, got %q", got)
	}

	calls = nil
	ensureErr := errors.New("refresh failed")
	ensureValidTokenForHealthCheck = func(h *Handler, account *config.Account) error {
		calls = append(calls, "ensure")
		return ensureErr
	}
	listAvailableModelsForHealthCheck = func(account *config.Account) ([]ModelInfo, error) {
		calls = append(calls, "list")
		return nil, nil
	}

	if err := h.checkAccountHealth(account); !errors.Is(err, ensureErr) {
		t.Fatalf("expected ensure error, got %v", err)
	}
	if got := strings.Join(calls, ","); got != "ensure" {
		t.Fatalf("expected list to be skipped after ensure error, got %q", got)
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
	if supportsImage, ok := models[0]["supports_image"].(bool); !ok || !supportsImage {
		t.Fatalf("expected image capability to be preserved, got %#v", models[0]["supports_image"])
	}
}
