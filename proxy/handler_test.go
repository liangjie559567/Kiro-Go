package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestHandleClaudeNativeWebSearchUsesKiroMCP(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
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

	h.handleClaudeMessagesInternal(w, req)

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

func TestHandleClaudeNativeWebSearchUsesAccountRegionForMCP(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	p := &pool.AccountPool{}
	h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
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

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected capacity retries to recover, got status %d body %s", w.Code, w.Body.String())
	}
	if attempts != 7 {
		t.Fatalf("expected six capacity retries then success, got %d attempts", attempts)
	}
	if len(sleeps) != 6 {
		t.Fatalf("expected one wait per capacity response, got %d waits", len(sleeps))
	}
	if !strings.Contains(w.Body.String(), `"model":"claude-opus-4.7"`) {
		t.Fatalf("expected response to preserve requested opus model, got %s", w.Body.String())
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
	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opus47AdmissionGate = newOpus47Gate(1, 10)
	opusCapacityRetryBudget = 200 * time.Millisecond
	var sleeps []time.Duration
	sleepForOpusCapacityRetry = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() {
		opus47AdmissionGate = oldGate
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
		InitKiroHttpClient("")
	})

	held, err := opus47AdmissionGate.acquire(time.Second)
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

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected explicit 429 after capacity budget, got status %d body %q", w.Code, w.Body.String())
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
