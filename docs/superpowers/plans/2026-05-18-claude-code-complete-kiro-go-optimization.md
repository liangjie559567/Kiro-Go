# Claude Code Complete Kiro-Go Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完整优化 Kiro-Go 的 Claude Code 使用闭环，同时保证本地 `/www/sub2api` 重建后继续通过真实 `sub2api -> Kiro-Go -> Kiro` 链路可调用。

**Architecture:** 本阶段只修改 `/www/Kiro-Go`，不修改 `/www/sub2api` 源码。实现集中在四条低风险链路：Claude Code 接入文档、OpenAI Responses 链式上下文恢复、工具/MCP 诊断可观测性、真实下游 UAT 验收。已有未提交改动已经包含部分 payload guard、request log 和 Responses 基础实现，执行时必须先读取当前文件，基于现状补齐而不是覆盖。

**Tech Stack:** Go 1.21、标准库 `testing`、Kiro-Go proxy/admin 静态 HTML、Docker Compose、本地 sub2api 验收。

---

## 文件结构与职责

- `README.md` / `README_CN.md`：新增 Claude Code 接入、MCP Tool Search、sub2api 下游兼容说明。
- `proxy/handler.go`：补齐 Responses session 链式恢复、tool call 过滤、session TTL/容量 pruning、Claude Code readiness 诊断 API。
- `proxy/handler_test.go`：覆盖 Responses 多跳链式恢复、当前 tool output 过滤、tools/tool_choice 继承、session pruning、readiness API。
- `proxy/request_log.go`：如果当前实现缺少 session restore/readiness 聚合字段，在这里补齐最小字段或统计函数。
- `proxy/request_log_test.go`：覆盖新增 request log/session restore/readiness 元数据。
- `web/index.html`：在已有设置/API/日志区域增加轻量 Claude Code readiness 展示；不要做大 UI 重构。
- `docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md`：记录测试、Docker 重建、sub2api 真实 stream/non-stream smoke 结果。
- `docs/superpowers/uat/claude-code-complete-sub2api-smoke.sh`：可选，封装不打印 API key 的本地 smoke。

执行前必须查看 `git diff -- <file>`，避免覆盖已有未提交改动。

---

### Task 1: 文档化 Claude Code 完整接入

**Files:**
- Modify: `README.md`
- Modify: `README_CN.md`

- [ ] **Step 1: 写入 README.md 的 Claude Code 章节**

在 `README.md` 的 `## Usage` 示例之后、`## Thinking Mode` 之前插入：

````markdown
## Claude Code

Kiro-Go can be used as an Anthropic-compatible backend for Claude Code.

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=any
export ANTHROPIC_MODEL=claude-sonnet-4.5
export ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4.5
export ENABLE_TOOL_SEARCH=true
```

Notes:

- Claude Code remains the MCP host. Kiro-Go receives the `tools`, `tool_use`, `tool_result`, and `tool_reference` request shapes emitted by Claude Code.
- Set `ENABLE_TOOL_SEARCH=true` when using MCP Tool Search with a non-Anthropic `ANTHROPIC_BASE_URL`.
- Kiro-Go does not start or manage local MCP servers. Configure MCP in Claude Code as usual.
- Use the admin request logs to inspect model, account, first-token latency, attempts, payload trimming, and tool-reference metadata.
````

- [ ] **Step 2: 写入 README_CN.md 的 Claude Code 章节**

在 `README_CN.md` 的 `## 使用方法` 示例之后、`## 思考模式` 之前插入：

````markdown
## Claude Code

Kiro-Go 可以作为 Claude Code 的 Anthropic 兼容后端使用。

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=any
export ANTHROPIC_MODEL=claude-sonnet-4.5
export ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4.5
export ENABLE_TOOL_SEARCH=true
```

说明：

- Claude Code 仍然是 MCP host。Kiro-Go 接收 Claude Code 发出的 `tools`、`tool_use`、`tool_result` 和 `tool_reference` 请求形态。
- 使用非 Anthropic 官方 `ANTHROPIC_BASE_URL` 时，如果需要 MCP Tool Search，请设置 `ENABLE_TOOL_SEARCH=true`。
- Kiro-Go 不启动也不管理本地 MCP server。MCP 仍按 Claude Code 的方式配置。
- 可在管理面板请求日志中查看模型、账号、首 token 延迟、重试次数、payload 裁剪和 tool_reference 元数据。
````

- [ ] **Step 3: 检查 Markdown 代码围栏**

Run: `rg -n "Claude Code|ENABLE_TOOL_SEARCH|ANTHROPIC_BASE_URL" README.md README_CN.md`

Expected: 两个 README 都包含 `Claude Code`、`ENABLE_TOOL_SEARCH=true`、`ANTHROPIC_BASE_URL=http://127.0.0.1:8080`。

- [ ] **Step 4: 提交文档**

```bash
git add README.md README_CN.md
git commit -m "docs: add claude code setup guide"
```

---

### Task 2: Responses session 增加链式状态字段和 pruning

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: 写失败测试 - session 保存 previous_response_id 并按 TTL/capacity pruning**

在 `proxy/handler_test.go` 的 Responses tests 附近添加：

```go
func TestOpenAIResponsesSessionPrunesExpiredAndOldestEntries(t *testing.T) {
	h := &Handler{responses: make(map[string]responsesSession)}
	now := time.Now()
	h.responses["expired"] = responsesSession{UpdatedAt: now.Add(-2 * time.Hour)}
	for i := 0; i < 130; i++ {
		h.responses[fmt.Sprintf("resp_%03d", i)] = responsesSession{UpdatedAt: now.Add(time.Duration(i) * time.Second)}
	}

	h.pruneOpenAIResponsesSessionsLocked(now)

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
```

- [ ] **Step 2: 运行失败测试**

Run:

```bash
go test ./proxy -run 'TestOpenAIResponsesSessionPrunesExpiredAndOldestEntries|TestSaveOpenAIResponsesSessionStoresPreviousResponseID' -count=1 -v
```

Expected: FAIL，提示 `PreviousResponseID`、`maxOpenAIResponsesSessions` 或新函数签名不存在。

- [ ] **Step 3: 最小实现 session 字段、常量、pruning 函数**

在 `proxy/handler.go` 的 `responsesSession` 附近改成：

```go
const (
	maxOpenAIResponsesSessions = 128
	openAIResponsesSessionTTL = time.Hour
)

type responsesSession struct {
	PreviousResponseID string
	Messages           []OpenAIMessage
	Tools              []OpenAITool
	ToolChoice         interface{}
	UpdatedAt          time.Time
}
```

把 `saveOpenAIResponsesSession` 签名改成：

```go
func (h *Handler) saveOpenAIResponsesSession(id, previousResponseID string, req *OpenAIRequest, content string, toolUses []KiroToolUse) {
```

并在 session 构造里加入：

```go
PreviousResponseID: strings.TrimSpace(previousResponseID),
```

把保存前 pruning 改为：

```go
h.pruneOpenAIResponsesSessionsLocked(time.Now())
h.responses[id] = session
h.pruneOpenAIResponsesSessionsLocked(time.Now())
```

新增函数：

```go
func (h *Handler) pruneOpenAIResponsesSessionsLocked(now time.Time) {
	if h.responses == nil {
		return
	}
	for id, session := range h.responses {
		if now.Sub(session.UpdatedAt) > openAIResponsesSessionTTL {
			delete(h.responses, id)
		}
	}
	for len(h.responses) > maxOpenAIResponsesSessions {
		h.trimOpenAIResponsesSessionsLocked()
	}
}
```

把现有调用点改为：

```go
previousID, _ := payload["previous_response_id"].(string)
h.saveOpenAIResponsesSession(responseID, previousID, req, finalContent, toolUses)
```

如果调用点没有 `payload`，先在 `handleOpenAIResponsesWithAccountRetry`、`handleOpenAIResponsesStreamAttempt`、`handleOpenAIResponsesNonStreamAttempt` 参数中透传 `previousResponseID string`。

- [ ] **Step 4: 运行测试确认通过**

Run:

```bash
go test ./proxy -run 'TestOpenAIResponsesSessionPrunesExpiredAndOldestEntries|TestSaveOpenAIResponsesSessionStoresPreviousResponseID' -count=1 -v
```

Expected: PASS。

- [ ] **Step 5: 提交 session 状态基础**

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: track responses session chains"
```

---

### Task 3: Responses previous_response_id 链式恢复与 tool_call 过滤

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: 写失败测试 - 多跳链式恢复**

在 `proxy/handler_test.go` 添加：

```go
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
```

- [ ] **Step 2: 写失败测试 - 当前 tool output 只恢复匹配的 tool_call**

```go
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
```

- [ ] **Step 3: 运行失败测试**

Run:

```bash
go test ./proxy -run 'TestRestoreOpenAIResponsesSessionRestoresPreviousResponseChain|TestRestoreOpenAIResponsesSessionFiltersLatestToolCallsByCurrentOutputs' -count=1 -v
```

Expected: FAIL，当前只恢复单层且不过滤 tool calls。

- [ ] **Step 4: 实现链式恢复 helper**

在 `proxy/handler.go` 增加：

```go
func (h *Handler) collectOpenAIResponsesSessionChain(previousID string) []responsesSession {
	previousID = strings.TrimSpace(previousID)
	if h == nil || previousID == "" {
		return nil
	}
	var reversed []responsesSession
	seen := map[string]bool{}
	for previousID != "" && !seen[previousID] {
		seen[previousID] = true
		session, ok := h.getOpenAIResponsesSession(previousID)
		if !ok {
			break
		}
		reversed = append(reversed, session)
		previousID = session.PreviousResponseID
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

func currentOpenAIToolOutputIDs(messages []OpenAIMessage) map[string]bool {
	ids := map[string]bool{}
	for _, msg := range messages {
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolCallID) != "" {
			ids[strings.TrimSpace(msg.ToolCallID)] = true
		}
	}
	return ids
}

func filterLatestAssistantToolCalls(messages []OpenAIMessage, keep map[string]bool) []OpenAIMessage {
	if len(keep) == 0 {
		return messages
	}
	out := append([]OpenAIMessage(nil), messages...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != "assistant" || len(out[i].ToolCalls) == 0 {
			continue
		}
		filtered := out[i].ToolCalls[:0]
		for _, tc := range out[i].ToolCalls {
			if keep[strings.TrimSpace(tc.ID)] {
				filtered = append(filtered, tc)
			}
		}
		if len(filtered) > 0 {
			out[i].ToolCalls = filtered
		}
		return out
	}
	return out
}
```

把 `restoreOpenAIResponsesSession` 替换为链式逻辑：

```go
func (h *Handler) restoreOpenAIResponsesSession(payload map[string]interface{}, req *OpenAIRequest) {
	if h == nil || req == nil || payload == nil {
		return
	}
	previousID, _ := payload["previous_response_id"].(string)
	chain := h.collectOpenAIResponsesSessionChain(previousID)
	if len(chain) == 0 {
		return
	}
	currentMessages := append([]OpenAIMessage(nil), req.Messages...)
	currentToolIDs := currentOpenAIToolOutputIDs(currentMessages)
	restored := make([]OpenAIMessage, 0)
	for i, session := range chain {
		messages := append([]OpenAIMessage(nil), session.Messages...)
		if i == len(chain)-1 {
			messages = filterLatestAssistantToolCalls(messages, currentToolIDs)
		}
		restored = append(restored, messages...)
	}
	req.Messages = append(restored, currentMessages...)

	for i := len(chain) - 1; i >= 0; i-- {
		session := chain[i]
		if len(req.Tools) == 0 && len(session.Tools) > 0 {
			req.Tools = append([]OpenAITool(nil), session.Tools...)
		}
		if req.ToolChoice == nil && session.ToolChoice != nil {
			req.ToolChoice = session.ToolChoice
		}
		if len(req.Tools) > 0 && req.ToolChoice != nil {
			break
		}
	}
}
```

- [ ] **Step 5: 运行 Responses restore 测试**

Run:

```bash
go test ./proxy -run 'TestRestoreOpenAIResponsesSession|TestHandleOpenAIResponsesRestoresPreviousResponseSession' -count=1 -v
```

Expected: PASS。

- [ ] **Step 6: 提交链式恢复**

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: restore responses session chains"
```

---

### Task 4: Responses session 恢复日志与 readiness 统计 API

**Files:**
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: 写失败测试 - request log 捕获 Responses session 恢复元数据**

在 `proxy/request_log_test.go` 添加：

```go
func TestRequestLogCapturesResponsesSessionRestoreMetadata(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	ctx, loggedReq, recorder, _ := h.beginRequestLog(httptest.NewRecorder(), req)

	updateRequestLogResponsesSession(loggedReq, "resp_prev", 3, 2, true)
	recorder.WriteHeader(http.StatusOK)
	h.finishRequestLog(ctx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.ResponsesPreviousID != "resp_prev" || entry.ResponsesRestoredSessions != 3 || entry.ResponsesRestoredToolCalls != 2 || !entry.ResponsesInheritedTools {
		t.Fatalf("expected responses session metadata, got %#v", entry)
	}
}
```

- [ ] **Step 2: 实现 request log 字段与 update 函数**

在 `RequestLogEntry` 增加：

```go
ResponsesPreviousID        string `json:"responsesPreviousId,omitempty"`
ResponsesRestoredSessions  int    `json:"responsesRestoredSessions,omitempty"`
ResponsesRestoredToolCalls int    `json:"responsesRestoredToolCalls,omitempty"`
ResponsesInheritedTools    bool   `json:"responsesInheritedTools,omitempty"`
```

新增：

```go
func updateRequestLogResponsesSession(r *http.Request, previousID string, restoredSessions, restoredToolCalls int, inheritedTools bool) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.ResponsesPreviousID = strings.TrimSpace(previousID)
	ctx.entry.ResponsesRestoredSessions = restoredSessions
	ctx.entry.ResponsesRestoredToolCalls = restoredToolCalls
	ctx.entry.ResponsesInheritedTools = inheritedTools
}
```

- [ ] **Step 3: 在 restoreOpenAIResponsesSession 调用日志更新**

在 `restoreOpenAIResponsesSession` 中计算：

```go
restoredToolCalls := 0
for _, session := range chain {
	for _, msg := range session.Messages {
		restoredToolCalls += len(msg.ToolCalls)
	}
}
inheritedTools := false
```

当继承 tools 或 tool_choice 时设置 `inheritedTools = true`。函数末尾调用：

```go
updateRequestLogResponsesSession(request, previousID, len(chain), restoredToolCalls, inheritedTools)
```

如果当前函数没有 `*http.Request` 参数，将签名改为：

```go
func (h *Handler) restoreOpenAIResponsesSession(r *http.Request, payload map[string]interface{}, req *OpenAIRequest)
```

并更新调用点。

- [ ] **Step 4: 写失败测试 - readiness API 聚合 Claude Code/MCP 证据**

在 `proxy/handler_test.go` 添加：

```go
func TestClaudeCodeReadinessAPIReportsRecentToolEvidence(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                   time.Now(),
		Endpoint:                    "/v1/messages",
		ClaudeCodeSessionID:         "sess_1",
		AnthropicBetas:              []string{"tool-search-2025-10-19"},
		ToolReferenceCount:          2,
		PayloadCurrentTools:         12,
		PayloadKeptTools:            []string{"bash", "read"},
		PayloadTrimmedTools:         []string{"mcp__browser__screenshot"},
		PayloadMaterializedToolRefs: []string{"mcp__fs__read_file"},
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if resp["recentClaudeCode"].(bool) != true || resp["recentToolReferences"].(bool) != true || resp["recentToolTrimming"].(bool) != true {
		t.Fatalf("unexpected readiness response: %#v", resp)
	}
}
```

- [ ] **Step 5: 实现 readiness API**

在 `proxy/handler.go` admin route 注册区增加：

```go
case path == "/admin/api/claude-code/readiness":
	h.apiGetClaudeCodeReadiness(w, r)
```

新增 handler：

```go
func (h *Handler) apiGetClaudeCodeReadiness(w http.ResponseWriter, r *http.Request) {
	logs := h.ensureRequestLogStore().List(maxRequestLogLimit)
	cutoff := time.Now().Add(-30 * time.Minute)
	resp := map[string]interface{}{
		"recentClaudeCode":      false,
		"recentToolReferences":  false,
		"recentMCPTools":        false,
		"recentToolTrimming":    false,
		"recentResponsesRestore": false,
		"lastSeen":              "",
		"examples":              []map[string]interface{}{},
	}
	examples := make([]map[string]interface{}, 0, 5)
	for _, entry := range logs {
		if entry.Timestamp.Before(cutoff) {
			continue
		}
		if entry.ClaudeCodeSessionID != "" || strings.Contains(strings.ToLower(strings.Join(entry.AnthropicBetas, ",")), "tool") {
			resp["recentClaudeCode"] = true
			if resp["lastSeen"] == "" {
				resp["lastSeen"] = entry.Timestamp.Format(time.RFC3339)
			}
		}
		if entry.ToolReferenceCount > 0 || len(entry.PayloadMaterializedToolRefs) > 0 || len(entry.PayloadDeferredTools) > 0 {
			resp["recentToolReferences"] = true
		}
		if containsMCPToolName(entry.PayloadKeptTools) || containsMCPToolName(entry.PayloadTrimmedTools) || containsMCPToolName(entry.PayloadMaterializedToolRefs) {
			resp["recentMCPTools"] = true
		}
		if entry.PayloadTrimmed || len(entry.PayloadTrimmedTools) > 0 {
			resp["recentToolTrimming"] = true
		}
		if entry.ResponsesRestoredSessions > 0 {
			resp["recentResponsesRestore"] = true
		}
		if len(examples) < 5 {
			examples = append(examples, map[string]interface{}{
				"timestamp":      entry.Timestamp,
				"endpoint":       entry.Endpoint,
				"model":          entry.Model,
				"toolReferences": entry.ToolReferenceCount,
				"tools":          entry.PayloadCurrentTools,
				"trimmedTools":   entry.PayloadTrimmedTools,
			})
		}
	}
	resp["examples"] = examples
	json.NewEncoder(w).Encode(resp)
}

func containsMCPToolName(names []string) bool {
	for _, name := range names {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "mcp__") {
			return true
		}
	}
	return false
}
```

- [ ] **Step 6: 运行 request log/readiness 测试**

Run:

```bash
go test ./proxy -run 'TestRequestLogCapturesResponsesSessionRestoreMetadata|TestClaudeCodeReadinessAPIReportsRecentToolEvidence' -count=1 -v
```

Expected: PASS。

- [ ] **Step 7: 提交日志与 readiness API**

```bash
git add proxy/handler.go proxy/handler_test.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: expose claude code readiness evidence"
```

---

### Task 5: 管理页展示 Claude Code readiness 和工具诊断

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: 定位已有请求日志/设置 UI**

Run:

```bash
rg -n "requestLogs|payloadKeptTools|settings.filterClaudeCode|admin/api/request" web/index.html
```

Expected: 找到已有日志表和设置翻译区域。

- [ ] **Step 2: 增加最小 readiness 数据加载函数**

在现有 admin JS API 函数附近新增：

```javascript
async function loadClaudeCodeReadiness() {
    try {
        const resp = await fetch('/admin/api/claude-code/readiness');
        if (!resp.ok) return null;
        return await resp.json();
    } catch (err) {
        return null;
    }
}
```

- [ ] **Step 3: 在设置或 API 标签页中渲染 readiness 小区块**

在已有 API/日志区块附近加入渲染函数：

```javascript
function renderClaudeCodeReadiness(data) {
    if (!data) {
        return '<div class="muted">Claude Code readiness: unavailable</div>';
    }
    const flag = (ok, text) => '<span class="' + (ok ? 'status-ok' : 'status-muted') + '">' + escapeHtml(text) + '</span>';
    return '<div class="settings-card">' +
        '<h3>Claude Code</h3>' +
        '<div class="hint">Use ANTHROPIC_BASE_URL with Kiro-Go. Set ENABLE_TOOL_SEARCH=true for MCP Tool Search.</div>' +
        '<div class="inline-flags">' +
        flag(!!data.recentClaudeCode, 'client') +
        flag(!!data.recentToolReferences, 'tool_reference') +
        flag(!!data.recentMCPTools, 'mcp tools') +
        flag(!!data.recentToolTrimming, 'tool trimming') +
        flag(!!data.recentResponsesRestore, 'responses restore') +
        '</div>' +
        '<pre>export ANTHROPIC_BASE_URL=' + escapeHtml(location.origin) + '\\nexport ANTHROPIC_AUTH_TOKEN=any\\nexport ENABLE_TOOL_SEARCH=true</pre>' +
        '</div>';
}
```

如果现有 CSS 没有 `settings-card`、`inline-flags`、`status-ok`、`status-muted`，使用已有卡片/徽标 class；不要引入大样式系统。

- [ ] **Step 4: 将 readiness 加入页面刷新流程**

在现有加载设置或日志的函数中加入：

```javascript
const readiness = await loadClaudeCodeReadiness();
const el = document.getElementById('claude-code-readiness');
if (el) el.innerHTML = renderClaudeCodeReadiness(readiness);
```

并在 HTML 中放置：

```html
<div id="claude-code-readiness"></div>
```

- [ ] **Step 5: 静态检查**

Run:

```bash
rg -n "claude-code-readiness|loadClaudeCodeReadiness|renderClaudeCodeReadiness|ENABLE_TOOL_SEARCH" web/index.html
```

Expected: 四个关键字符串都存在。

- [ ] **Step 6: 提交管理页诊断**

```bash
git add web/index.html
git commit -m "feat: show claude code readiness in admin"
```

---

### Task 6: 全量 Go 回归与直接 Kiro-Go smoke

**Files:**
- Create: `docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md`

- [ ] **Step 1: 运行全量 Go 测试**

Run:

```bash
go test ./...
```

Expected: PASS。

- [ ] **Step 2: 创建 UAT 记录文件**

新增 `docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md`：

```markdown
# Claude Code Complete Kiro-Go Optimization UAT

Date: 2026-05-18

## Scope

- Kiro-Go source changes only.
- `/www/sub2api` is rebuild and real-call verification target only.
- Secrets and API keys are not printed.

## Go Tests

- Command: `go test ./...`
- Result:
- Notes:

## Kiro-Go Local Verification

- Build/restart command:
- Health URL: `http://127.0.0.1:8080/health`
- Health result:
- `/v1/models` result:
- Direct `/v1/messages` smoke:

## sub2api Downstream Verification

- Rebuild/restart command:
- Health URL: `http://127.0.0.1:18080/health`
- Health result:
- Non-stream `/v1/messages` result:
- Stream `/v1/messages` result:

## Request Log Evidence

- Kiro-Go request IDs:
- Models:
- Accounts:
- Attempts:
- First-token timings:
- Payload/tool trimming:
- Responses restore/readiness:

## Failure Classification

- sub2api layer:
- Kiro-Go protocol/payload layer:
- Kiro-Go account/token layer:
- Kiro upstream capacity/network layer:
```

- [ ] **Step 3: 测 Kiro-Go health 和 models**

如果服务已运行：

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:8080/v1/models | head -c 1000
```

Expected: `/health` 返回 OK JSON；`/v1/models` 返回模型列表 JSON。

如果服务未运行，本步骤记录为 pending，留到 Docker rebuild 后完成。

- [ ] **Step 4: 更新 UAT 记录并提交**

```bash
git add docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md
git commit -m "test: record claude code optimization uat"
```

---

### Task 7: Docker rebuild、sub2api rebuild 和真实下游 smoke

**Files:**
- Modify: `docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md`
- Optional Create: `docs/superpowers/uat/claude-code-complete-sub2api-smoke.sh`

- [ ] **Step 1: 确认 sub2api 源码不被本阶段修改**

Run:

```bash
git -C /www/sub2api status --short
```

Expected: 可以有既存未提交改动，但本阶段不得新增或修改 `/www/sub2api` 文件。记录当前输出到 UAT。

- [ ] **Step 2: rebuild/restart Kiro-Go**

根据本地实际 compose 文件执行其一：

```bash
docker compose up -d --build kiro-go
```

或：

```bash
docker compose up -d --build
```

Expected: Kiro-Go container 启动成功。

- [ ] **Step 3: Kiro-Go health**

Run:

```bash
curl -sS http://127.0.0.1:8080/health
```

Expected: 返回健康 JSON。

- [ ] **Step 4: rebuild/restart sub2api**

Run:

```bash
cd /www/sub2api/deploy
docker compose -f docker-compose.current.yml up -d --build
```

Expected: sub2api container 启动成功。不要修改 sub2api 源码。

- [ ] **Step 5: sub2api health**

Run:

```bash
curl -sS http://127.0.0.1:18080/health
```

Expected: 返回健康 JSON。

- [ ] **Step 6: 准备不打印密钥的 smoke 命令**

如果已有环境变量可用，使用：

```bash
SUB2API_KEY="${SUB2API_KEY:?set SUB2API_KEY without printing it}"
```

非流式 smoke：

```bash
curl -sS http://127.0.0.1:18080/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: ${SUB2API_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4.5","max_tokens":64,"messages":[{"role":"user","content":"Return exactly: kiro-go-sub2api-nonstream-ok"}]}' \
  | tee /tmp/kiro-go-sub2api-nonstream.json
```

Expected: 响应包含 `kiro-go-sub2api-nonstream-ok` 或可解释的上游容量错误。

- [ ] **Step 7: 流式 smoke**

```bash
curl -N -sS http://127.0.0.1:18080/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: ${SUB2API_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4.5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Return exactly: kiro-go-sub2api-stream-ok"}]}' \
  | tee /tmp/kiro-go-sub2api-stream.sse
```

Expected: SSE 中包含 `message_start`、`content_block_delta` 或兼容事件，并包含目标文本，或返回可分类错误。

- [ ] **Step 8: 拉取 Kiro-Go 请求日志证据**

Run:

```bash
curl -sS http://127.0.0.1:8080/admin/api/request-logs | head -c 4000
curl -sS http://127.0.0.1:8080/admin/api/claude-code/readiness | head -c 2000
```

Expected: request logs 中能看到 smoke 请求；readiness API 返回 JSON。

- [ ] **Step 9: 更新 UAT 文件**

把每一步命令、状态、错误分类写入 `docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md`。不要写入 API key。

- [ ] **Step 10: 提交 UAT 结果**

```bash
git add docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md
git commit -m "test: verify sub2api downstream after kiro-go optimization"
```

---

## 自审清单

- Spec 覆盖：
  - Claude Code 接入文档：Task 1。
  - Responses 链式上下文恢复：Task 2、Task 3。
  - 工具/MCP 诊断：Task 4、Task 5。
  - sub2api 真实下游验收：Task 6、Task 7。
  - 不修改 sub2api 源码：Task 7 Step 1 明确检查。
- 占位扫描：本计划没有未定项；每个代码任务包含具体测试或实现片段。
- 类型一致性：
  - `responsesSession.PreviousResponseID` 在 Task 2 定义，Task 3 使用。
  - `updateRequestLogResponsesSession` 在 Task 4 定义并在 restore 流程调用。
  - `apiGetClaudeCodeReadiness` 和 `containsMCPToolName` 在 Task 4 定义，Task 5 只消费 API。
