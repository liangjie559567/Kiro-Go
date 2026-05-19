// Package proxy is the core proxy layer for the Kiro API.
// It handles streaming API calls to the Kiro backend and parses AWS Event Stream responses.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kiro-go/config"
	"kiro-go/logger"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const defaultRateLimitFallbackSeconds = 5
const defaultKiroRegion = "us-east-1"

type rateLimitError struct {
	endpoint string
	body     string
	resetAt  time.Time
}

func (e *rateLimitError) Error() string {
	if e.body != "" {
		return fmt.Sprintf("HTTP 429 from %s: %s", e.endpoint, e.body)
	}
	return fmt.Sprintf("HTTP 429 from %s", e.endpoint)
}

func (e *rateLimitError) RateLimitResetAt() time.Time {
	return e.resetAt
}

// Endpoint configuration (auto-fallback on quota exhaustion).
type kiroEndpoint struct {
	URL       string
	Origin    string
	AmzTarget string
	Name      string
	Service   string
	Path      string
}

var kiroEndpoints = []kiroEndpoint{
	{
		URL:       "https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		Origin:    "AI_EDITOR",
		AmzTarget: "",
		Name:      "Kiro IDE",
		Service:   "q",
		Path:      "/generateAssistantResponse",
	},
	{
		URL:       "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		Origin:    "AI_EDITOR",
		AmzTarget: "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
		Name:      "CodeWhisperer",
		Service:   "codewhisperer",
		Path:      "/generateAssistantResponse",
	},
	{
		URL:       "https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		Origin:    "AI_EDITOR",
		AmzTarget: "AmazonQDeveloperStreamingService.SendMessage",
		Name:      "AmazonQ",
		Service:   "q",
		Path:      "/generateAssistantResponse",
	},
}

// Global HTTP clients, swappable at runtime to apply proxy reconfiguration without restart.
var kiroHttpStore atomic.Pointer[http.Client]
var kiroRestHttpStore atomic.Pointer[http.Client]

// proxyClientCache caches http.Client instances keyed by proxy URL for per-account proxy support.
var proxyClientCache sync.Map

func init() {
	InitKiroHttpClient("")
}

// GetClientForProxy returns an http.Client configured for the given proxy URL.
// If proxyURL is empty, returns the global kiro HTTP client.
func GetClientForProxy(proxyURL string) *http.Client {
	if proxyURL == "" {
		return kiroHttpStore.Load()
	}
	if cached, ok := proxyClientCache.Load(proxyURL); ok {
		return cached.(*http.Client)
	}
	client := &http.Client{
		Timeout:   5 * time.Minute,
		Transport: buildKiroTransport(proxyURL),
	}
	proxyClientCache.Store(proxyURL, client)
	return client
}

// GetRestClientForProxy returns a rest http.Client (30s timeout) for the given proxy URL.
// If proxyURL is empty, returns the global kiro REST HTTP client.
func GetRestClientForProxy(proxyURL string) *http.Client {
	if proxyURL == "" {
		return kiroRestHttpStore.Load()
	}
	cacheKey := "rest:" + proxyURL
	if cached, ok := proxyClientCache.Load(cacheKey); ok {
		return cached.(*http.Client)
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: buildKiroTransport(proxyURL),
	}
	proxyClientCache.Store(cacheKey, client)
	return client
}

// ResolveAccountProxyURL returns the effective proxy URL for an account.
// Falls back to global config.GetProxyURL() if the account has no per-account proxy.
func ResolveAccountProxyURL(account *config.Account) string {
	if account != nil && account.ProxyURL != "" {
		return account.ProxyURL
	}
	return config.GetProxyURL()
}

// buildKiroTransport constructs an HTTP Transport with optional outbound proxy support.
func buildKiroTransport(proxyURL string) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	t := &http.Transport{
		Proxy:                 freshProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:     true,
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			t.Proxy = http.ProxyURL(u)
			// Proxied connections cannot negotiate HTTP/2.
			t.ForceAttemptHTTP2 = false
		}
	}
	return t
}

func freshProxyFromEnvironment(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	if proxyBypassedByEnvironment(req.URL) {
		return nil, nil
	}
	switch req.URL.Scheme {
	case "https":
		return parseEnvironmentProxyURL(envAny("HTTPS_PROXY", "https_proxy"))
	case "http":
		proxy := envAny("HTTP_PROXY", "http_proxy")
		if proxy != "" && os.Getenv("REQUEST_METHOD") != "" {
			return nil, errors.New("refusing to use HTTP_PROXY value in CGI environment")
		}
		return parseEnvironmentProxyURL(proxy)
	default:
		return nil, nil
	}
}

func envAny(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func parseEnvironmentProxyURL(proxy string) (*url.URL, error) {
	if proxy == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil || !supportedProxyScheme(proxyURL.Scheme) {
		if fallbackURL, fallbackErr := url.Parse("http://" + proxy); fallbackErr == nil {
			return fallbackURL, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address %q: %w", proxy, err)
	}
	if !supportedProxyScheme(proxyURL.Scheme) {
		return nil, fmt.Errorf("invalid proxy scheme %q", proxyURL.Scheme)
	}
	return proxyURL, nil
}

func supportedProxyScheme(scheme string) bool {
	return scheme == "http" || scheme == "https" || scheme == "socks5"
}

func proxyBypassedByEnvironment(reqURL *url.URL) bool {
	noProxy := envAny("NO_PROXY", "no_proxy")
	if strings.TrimSpace(noProxy) == "" {
		return false
	}
	host := reqURL.Hostname()
	if host == "" {
		return false
	}
	if host == "localhost" || isLoopbackHost(host) {
		return true
	}
	for _, rawRule := range strings.Split(noProxy, ",") {
		rule := strings.ToLower(strings.TrimSpace(rawRule))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if strings.Contains(rule, ":") {
			ruleHost, rulePort, err := net.SplitHostPort(rule)
			if err == nil && rulePort != reqURL.Port() {
				continue
			}
			if err == nil {
				rule = ruleHost
			}
		}
		hostLower := strings.ToLower(host)
		if strings.HasPrefix(rule, ".") {
			if strings.HasSuffix(hostLower, rule) {
				return true
			}
			continue
		}
		if hostLower == rule || strings.HasSuffix(hostLower, "."+rule) {
			return true
		}
	}
	return false
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// InitKiroHttpClient initializes (or reinitializes) the HTTP clients used for Kiro API requests.
func InitKiroHttpClient(proxyURL string) {
	client := &http.Client{
		Timeout:   5 * time.Minute,
		Transport: buildKiroTransport(proxyURL),
	}
	kiroHttpStore.Store(client)

	restClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: buildKiroTransport(proxyURL),
	}
	kiroRestHttpStore.Store(restClient)
}

func wrapKiroToolUseCallback(payload *KiroPayload, callback *KiroStreamCallback) *KiroStreamCallback {
	if callback == nil || payload == nil || (callback.OnToolUse == nil && callback.OnValidatedToolUse == nil && callback.OnSuppressedToolUse == nil) {
		return callback
	}
	if len(payload.ToolNameMap) == 0 && len(payload.ToolSchemas) == 0 {
		return callback
	}
	originalOnToolUse := callback.OnToolUse
	originalOnSuppressedToolUse := callback.OnSuppressedToolUse
	nameMap := payload.ToolNameMap
	schemas := payload.ToolSchemas
	wrapped := *callback
	wrapped.OnToolUse = func(tu KiroToolUse) {
		if original, ok := nameMap[tu.Name]; ok {
			tu.Name = original
		}
		tu.Input = repairToolUseInputForClientSchema(tu.Name, tu.Input, schemas[tu.Name])
		if !toolUseInputSatisfiesSchema(tu, schemas) {
			reason := "input does not satisfy client tool schema"
			logger.Warnf("[ToolUse] Dropping invalid tool_use id=%s name=%s: %s", tu.ToolUseID, tu.Name, reason)
			if originalOnSuppressedToolUse != nil {
				originalOnSuppressedToolUse(tu, reason)
			}
			return
		}
		if originalOnToolUse != nil {
			originalOnToolUse(tu)
		}
	}
	if callback.OnValidatedToolUse != nil {
		originalOnValidatedToolUse := callback.OnValidatedToolUse
		wrapped.OnValidatedToolUse = func(tu KiroToolUse) bool {
			if original, ok := nameMap[tu.Name]; ok {
				tu.Name = original
			}
			tu.Input = repairToolUseInputForClientSchema(tu.Name, tu.Input, schemas[tu.Name])
			if !toolUseInputSatisfiesSchema(tu, schemas) {
				reason := "input does not satisfy client tool schema"
				logger.Warnf("[ToolUse] Dropping invalid tool_use id=%s name=%s: %s", tu.ToolUseID, tu.Name, reason)
				if originalOnSuppressedToolUse != nil {
					originalOnSuppressedToolUse(tu, reason)
				}
				return false
			}
			return originalOnValidatedToolUse(tu)
		}
	}
	return &wrapped
}

func repairToolUseInputForClientSchema(name string, input map[string]interface{}, summary toolSchemaSummary) map[string]interface{} {
	if input == nil {
		input = map[string]interface{}{}
	}
	repaired := make(map[string]interface{}, len(input)+1)
	for key, value := range input {
		repaired[key] = value
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "bash":
		repairBashInput(repaired)
	case "edit":
		repairEditInput(repaired)
	case "multiedit":
		repairMultiEditInput(repaired)
	case "glob":
		repairGlobInput(repaired)
	case "grep":
		repairGrepInput(repaired)
	case "ls":
		repairLSInput(repaired)
	case "write":
		repairWriteInput(repaired)
	case "read":
		aliasToCanonical(repaired, "path", "file_path")
		aliasToCanonical(repaired, "file", "file_path")
		coerceIntegerField(repaired, "offset")
		coerceIntegerField(repaired, "limit")
	case "taskcreate":
		repairTaskCreateInput(repaired)
	case "taskupdate":
		repairTaskUpdateInput(repaired)
	case "taskoutput":
		repairTaskOutputInput(repaired)
	case "todowrite":
		repairTodoWriteInput(repaired)
	case "askuserquestion", "request_user_input":
		repairAskUserQuestionInput(repaired)
	}
	removeUnknownFieldsWhenDisallowed(repaired, summary.Schema)
	return repaired
}

func repairBashInput(input map[string]interface{}) {
	aliasToCanonical(input, "cmd", "command")
	aliasToCanonical(input, "shell_command", "command")
	aliasToCanonical(input, "script", "command")
	aliasToCanonical(input, "summary", "description")
	coerceStringField(input, "command")
	coerceStringField(input, "description")
	coerceIntegerField(input, "timeout")
}

func repairEditInput(input map[string]interface{}) {
	aliasToCanonical(input, "path", "file_path")
	aliasToCanonical(input, "file", "file_path")
	aliasToCanonical(input, "old", "old_string")
	aliasToCanonical(input, "oldText", "old_string")
	aliasToCanonical(input, "old_text", "old_string")
	aliasToCanonical(input, "new", "new_string")
	aliasToCanonical(input, "newText", "new_string")
	aliasToCanonical(input, "new_text", "new_string")
	aliasToCanonical(input, "replaceAll", "replace_all")
	coerceStringField(input, "file_path")
	coerceStringField(input, "old_string")
	coerceStringField(input, "new_string")
	coerceBoolField(input, "replace_all")
}

func repairMultiEditInput(input map[string]interface{}) {
	aliasToCanonical(input, "path", "file_path")
	aliasToCanonical(input, "file", "file_path")
	coerceStringField(input, "file_path")
	edits, ok := input["edits"].([]interface{})
	if !ok {
		return
	}
	for _, raw := range edits {
		edit, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		aliasToCanonical(edit, "old", "old_string")
		aliasToCanonical(edit, "oldText", "old_string")
		aliasToCanonical(edit, "old_text", "old_string")
		aliasToCanonical(edit, "new", "new_string")
		aliasToCanonical(edit, "newText", "new_string")
		aliasToCanonical(edit, "new_text", "new_string")
		aliasToCanonical(edit, "replaceAll", "replace_all")
		coerceStringField(edit, "old_string")
		coerceStringField(edit, "new_string")
		coerceBoolField(edit, "replace_all")
	}
}

func repairGlobInput(input map[string]interface{}) {
	aliasToCanonical(input, "query", "pattern")
	aliasToCanonical(input, "glob", "pattern")
	aliasToCanonical(input, "directory", "path")
	aliasToCanonical(input, "dir", "path")
	coerceStringField(input, "pattern")
	coerceStringField(input, "path")
}

func repairGrepInput(input map[string]interface{}) {
	aliasToCanonical(input, "query", "pattern")
	aliasToCanonical(input, "regex", "pattern")
	aliasToCanonical(input, "regexp", "pattern")
	aliasToCanonical(input, "directory", "path")
	aliasToCanonical(input, "dir", "path")
	aliasToCanonical(input, "include", "glob")
	coerceStringField(input, "pattern")
	coerceStringField(input, "path")
	coerceStringField(input, "glob")
}

func repairLSInput(input map[string]interface{}) {
	aliasToCanonical(input, "dir", "path")
	aliasToCanonical(input, "directory", "path")
	if _, ok := input["path"]; !ok {
		input["path"] = "."
	}
	coerceStringField(input, "path")
}

func repairWriteInput(input map[string]interface{}) {
	aliasToCanonical(input, "path", "file_path")
	aliasToCanonical(input, "file", "file_path")
	aliasToCanonical(input, "text", "content")
	coerceStringField(input, "file_path")
	coerceStringField(input, "content")
}

func repairTaskCreateInput(input map[string]interface{}) {
	mergeFirstTaskCreateCandidate(input)
	aliasToCanonical(input, "active_form", "activeForm")
	aliasToCanonical(input, "active_forming", "activeForm")
	aliasToCanonical(input, "active", "activeForm")
	if _, ok := input["subject"]; !ok {
		if value, exists := firstPresent(input, "content", "title", "task", "todo"); exists {
			input["subject"] = value
		}
	}
	if _, ok := input["description"]; !ok {
		if value, exists := firstPresent(input, "descriptionText", "details", "content", "activeForm", "subject", "title"); exists {
			input["description"] = value
		}
	}
	coerceStringField(input, "subject")
	coerceStringField(input, "description")
	coerceStringField(input, "activeForm")
	if _, ok := input["description"]; !ok {
		if activeForm, ok := input["activeForm"].(string); ok && strings.TrimSpace(activeForm) != "" {
			input["description"] = activeForm
		} else if subject, ok := input["subject"].(string); ok && strings.TrimSpace(subject) != "" {
			input["description"] = subject
		}
	}
}

func mergeFirstTaskCreateCandidate(input map[string]interface{}) {
	if input == nil {
		return
	}
	var candidate map[string]interface{}
	if tasks, ok := input["tasks"].([]interface{}); ok && len(tasks) > 0 {
		candidate, _ = tasks[0].(map[string]interface{})
	}
	if candidate == nil {
		if task, ok := input["task"].(map[string]interface{}); ok {
			candidate = task
		}
	}
	if candidate == nil {
		if todo, ok := input["todo"].(map[string]interface{}); ok {
			candidate = todo
		}
	}
	if candidate == nil {
		return
	}
	for _, pair := range [][2]string{
		{"subject", "subject"},
		{"title", "subject"},
		{"content", "subject"},
		{"description", "description"},
		{"descriptionText", "description"},
		{"details", "description"},
		{"activeForm", "activeForm"},
		{"active_form", "activeForm"},
		{"active_forming", "activeForm"},
		{"active", "activeForm"},
	} {
		if _, exists := input[pair[1]]; exists {
			continue
		}
		if value, ok := candidate[pair[0]]; ok && value != nil {
			input[pair[1]] = value
		}
	}
	delete(input, "tasks")
	delete(input, "task")
	delete(input, "todo")
}

func repairTaskUpdateInput(input map[string]interface{}) {
	if _, ok := input["taskId"]; !ok {
		if task, ok := input["task"].(map[string]interface{}); ok {
			if value, exists := firstPresent(task, "taskId", "task_id", "id"); exists {
				input["taskId"] = value
			}
		}
	}
	aliasToCanonical(input, "task_id", "taskId")
	aliasToCanonical(input, "taskID", "taskId")
	aliasToCanonical(input, "id", "taskId")
	aliasToCanonical(input, "active_form", "activeForm")
	aliasToCanonical(input, "active_forming", "activeForm")
	aliasToCanonical(input, "active", "activeForm")
	aliasToCanonical(input, "content", "subject")
	aliasToCanonical(input, "title", "subject")
	aliasToCanonical(input, "details", "description")
	coerceStringField(input, "taskId")
	coerceStringField(input, "subject")
	coerceStringField(input, "description")
	coerceStringField(input, "activeForm")
	normalizeTaskStatus(input)
	delete(input, "task")
}

func repairTaskOutputInput(input map[string]interface{}) {
	if _, ok := input["taskId"]; !ok {
		if task, ok := input["task"].(map[string]interface{}); ok {
			if value, exists := firstPresent(task, "taskId", "task_id", "id"); exists {
				input["taskId"] = value
			}
		}
	}
	aliasToCanonical(input, "task_id", "taskId")
	aliasToCanonical(input, "taskID", "taskId")
	aliasToCanonical(input, "id", "taskId")
	coerceStringField(input, "taskId")
	coerceBoolField(input, "block")
	coerceIntegerField(input, "timeout")
	delete(input, "task")
}

func repairTodoWriteInput(input map[string]interface{}) {
	if _, ok := input["todos"]; !ok {
		aliasToCanonical(input, "tasks", "todos")
		aliasToCanonical(input, "items", "todos")
	}
	todos, ok := input["todos"].([]interface{})
	if !ok {
		if todo, ok := input["todo"].(map[string]interface{}); ok {
			todos = []interface{}{todo}
			input["todos"] = todos
		}
	}
	for _, raw := range todos {
		todo, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		aliasToCanonical(todo, "active_form", "activeForm")
		aliasToCanonical(todo, "active_forming", "activeForm")
		aliasToCanonical(todo, "active", "activeForm")
		aliasToCanonical(todo, "subject", "content")
		aliasToCanonical(todo, "title", "content")
		if _, ok := todo["activeForm"]; !ok {
			if value, exists := firstPresent(todo, "content", "description"); exists {
				todo["activeForm"] = value
			}
		}
		coerceStringField(todo, "content")
		coerceStringField(todo, "activeForm")
		normalizeTaskStatus(todo)
	}
}

func repairAskUserQuestionInput(input map[string]interface{}) {
	if _, ok := input["questions"]; !ok {
		aliasToCanonical(input, "question", "questions")
	}
	if raw, ok := input["questions"].(string); ok {
		if parsed, ok := parseJSONArrayOrObjectString(raw); ok {
			input["questions"] = parsed
		}
	}
	questions, ok := input["questions"].([]interface{})
	if !ok {
		if question, ok := input["questions"].(map[string]interface{}); ok {
			questions = []interface{}{question}
			input["questions"] = questions
		} else {
			return
		}
	}
	for _, rawQuestion := range questions {
		question, ok := rawQuestion.(map[string]interface{})
		if !ok {
			continue
		}
		coerceStringField(question, "header")
		coerceBoolField(question, "multiSelect")
		if options, ok := question["options"].([]interface{}); ok {
			for _, rawOption := range options {
				option, ok := rawOption.(map[string]interface{})
				if !ok {
					continue
				}
				coerceStringField(option, "label")
				coerceStringField(option, "description")
			}
		}
	}
}

func parseJSONArrayOrObjectString(raw string) (interface{}, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false
	}
	switch value := parsed.(type) {
	case []interface{}:
		return value, true
	case map[string]interface{}:
		return []interface{}{value}, true
	default:
		return nil, false
	}
}

func normalizeTaskStatus(input map[string]interface{}) {
	if status, ok := input["status"].(map[string]interface{}); ok {
		if value, exists := firstPresent(status, "status", "state", "value", "name"); exists {
			input["status"] = value
		}
	}
	coerceStringField(input, "status")
	if status, ok := input["status"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "done", "complete", "completed":
			input["status"] = "completed"
		case "doing", "in-progress", "in_progress", "in progress", "active", "working", "running":
			input["status"] = "in_progress"
		case "todo", "pending", "open", "not_started", "not started":
			input["status"] = "pending"
		}
	}
}

func firstPresent(input map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		value, ok := input[key]
		if !ok || value == nil {
			continue
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		return value, true
	}
	return nil, false
}

func aliasToCanonical(input map[string]interface{}, alias, canonical string) {
	if input == nil || alias == canonical {
		return
	}
	if _, hasCanonical := input[canonical]; hasCanonical {
		delete(input, alias)
		return
	}
	if value, ok := input[alias]; ok {
		input[canonical] = value
		delete(input, alias)
	}
}

func coerceIntegerField(input map[string]interface{}, field string) {
	value, ok := input[field]
	if !ok || value == nil {
		return
	}
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			delete(input, field)
			return
		}
		if n, err := strconv.Atoi(trimmed); err == nil {
			input[field] = n
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			input[field] = int(n)
		}
	case float64:
		if v == float64(int64(v)) {
			input[field] = int(v)
		}
	}
}

func coerceStringField(input map[string]interface{}, field string) {
	value, ok := input[field]
	if !ok || value == nil {
		return
	}
	switch v := value.(type) {
	case string:
		input[field] = strings.TrimSpace(v)
	case json.Number:
		input[field] = v.String()
	case int, int64, int32, float64, float32:
		input[field] = strings.TrimSpace(fmt.Sprint(v))
	}
}

func coerceBoolField(input map[string]interface{}, field string) {
	value, ok := input[field]
	if !ok || value == nil {
		return
	}
	switch v := value.(type) {
	case bool:
		return
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1", "on":
			input[field] = true
		case "false", "no", "0", "off", "":
			input[field] = false
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			input[field] = n != 0
		}
	case int:
		input[field] = v != 0
	case int64:
		input[field] = v != 0
	case float64:
		input[field] = v != 0
	}
}

func removeUnknownFieldsWhenDisallowed(input map[string]interface{}, schema map[string]interface{}) {
	if input == nil || schema == nil {
		return
	}
	additional, hasAdditional := schema["additionalProperties"].(bool)
	if !hasAdditional || additional {
		return
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return
	}
	for key := range input {
		if _, ok := props[key]; !ok {
			delete(input, key)
		}
	}
}

func toolUseInputSatisfiesSchema(tu KiroToolUse, schemas map[string]toolSchemaSummary) bool {
	if len(schemas) == 0 {
		return true
	}
	schema, ok := schemas[tu.Name]
	if !ok {
		return true
	}
	if schema.Schema != nil {
		return validateToolInputAgainstSchema(tu.Input, schema.Schema)
	}
	if len(schema.Required) == 0 {
		return true
	}
	for _, field := range schema.Required {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		value, exists := tu.Input[field]
		if !exists || value == nil {
			return false
		}
		if s, ok := value.(string); ok && s == "" {
			return false
		}
	}
	return true
}

func validateToolInputAgainstSchema(input map[string]interface{}, schema map[string]interface{}) bool {
	if input == nil {
		input = map[string]interface{}{}
	}
	return validateJSONSchemaValue(input, schema)
}

func validateJSONSchemaValue(value interface{}, schema map[string]interface{}) bool {
	if schema == nil {
		return true
	}
	if enumValues, ok := schema["enum"].([]interface{}); ok && len(enumValues) > 0 && !jsonSchemaEnumContains(enumValues, value) {
		return false
	}
	if anyOf, ok := schema["anyOf"].([]interface{}); ok && len(anyOf) > 0 {
		for _, item := range anyOf {
			if sub, ok := schemaMap(item); ok && validateJSONSchemaValue(value, sub) {
				return true
			}
		}
		return false
	}
	if oneOf, ok := schema["oneOf"].([]interface{}); ok && len(oneOf) > 0 {
		matches := 0
		for _, item := range oneOf {
			if sub, ok := schemaMap(item); ok && validateJSONSchemaValue(value, sub) {
				matches++
			}
		}
		return matches == 1
	}
	if allOf, ok := schema["allOf"].([]interface{}); ok && len(allOf) > 0 {
		for _, item := range allOf {
			if sub, ok := schemaMap(item); ok && !validateJSONSchemaValue(value, sub) {
				return false
			}
		}
	}

	types := schemaTypes(schema["type"])
	if len(types) > 0 && !jsonValueMatchesAnyType(value, types) {
		return false
	}
	if len(types) == 0 {
		if _, ok := schema["properties"]; ok && !jsonValueMatchesType(value, "object") {
			return false
		}
		if _, ok := schema["items"]; ok && !jsonValueMatchesType(value, "array") {
			return false
		}
	}

	if propsRaw, ok := schema["properties"].(map[string]interface{}); ok {
		obj, ok := value.(map[string]interface{})
		if !ok {
			return false
		}
		for _, field := range schemaRequiredFields(schema) {
			fieldValue, exists := obj[field]
			if !exists || fieldValue == nil {
				return false
			}
			if s, ok := fieldValue.(string); ok && s == "" {
				return false
			}
		}
		for key, fieldValue := range obj {
			if propSchema, ok := schemaMap(propsRaw[key]); ok {
				if !validateJSONSchemaValue(fieldValue, propSchema) {
					return false
				}
				continue
			}
			if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
				return false
			}
			if additionalSchema, ok := schemaMap(schema["additionalProperties"]); ok && !validateJSONSchemaValue(fieldValue, additionalSchema) {
				return false
			}
		}
	}

	if itemsSchema, ok := schemaMap(schema["items"]); ok {
		arr, ok := value.([]interface{})
		if !ok {
			return false
		}
		if !arrayLengthSatisfiesSchema(arr, schema) {
			return false
		}
		for _, item := range arr {
			if !validateJSONSchemaValue(item, itemsSchema) {
				return false
			}
		}
	} else if arr, ok := value.([]interface{}); ok {
		if !arrayLengthSatisfiesSchema(arr, schema) {
			return false
		}
	}

	if text, ok := value.(string); ok {
		if !stringLengthSatisfiesSchema(text, schema) || !stringPatternSatisfiesSchema(text, schema) {
			return false
		}
	}

	if !numericValueSatisfiesSchema(value, schema) {
		return false
	}

	return true
}

func arrayLengthSatisfiesSchema(arr []interface{}, schema map[string]interface{}) bool {
	if minItems, ok := schemaNumber(schema["minItems"]); ok && float64(len(arr)) < minItems {
		return false
	}
	if maxItems, ok := schemaNumber(schema["maxItems"]); ok && float64(len(arr)) > maxItems {
		return false
	}
	return true
}

func stringLengthSatisfiesSchema(text string, schema map[string]interface{}) bool {
	length := float64(len([]rune(text)))
	if minLength, ok := schemaNumber(schema["minLength"]); ok && length < minLength {
		return false
	}
	if maxLength, ok := schemaNumber(schema["maxLength"]); ok && length > maxLength {
		return false
	}
	return true
}

func stringPatternSatisfiesSchema(text string, schema map[string]interface{}) bool {
	pattern, ok := schema["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return true
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return true
	}
	return re.MatchString(text)
}

func numericValueSatisfiesSchema(value interface{}, schema map[string]interface{}) bool {
	number, ok := jsonNumberAsFloat(value)
	if !ok {
		return true
	}
	if minimum, ok := schemaNumber(schema["minimum"]); ok && number < minimum {
		return false
	}
	if exclusiveMinimum, ok := schemaNumber(schema["exclusiveMinimum"]); ok && number <= exclusiveMinimum {
		return false
	}
	if maximum, ok := schemaNumber(schema["maximum"]); ok && number > maximum {
		return false
	}
	if exclusiveMaximum, ok := schemaNumber(schema["exclusiveMaximum"]); ok && number >= exclusiveMaximum {
		return false
	}
	return true
}

func schemaNumber(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func jsonNumberAsFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func schemaMap(value interface{}) (map[string]interface{}, bool) {
	switch v := value.(type) {
	case map[string]interface{}:
		return v, true
	default:
		return nil, false
	}
}

func schemaTypes(value interface{}) []string {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{strings.TrimSpace(v)}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return append([]string(nil), v...)
	default:
		return nil
	}
}

func jsonValueMatchesAnyType(value interface{}, types []string) bool {
	for _, typ := range types {
		if jsonValueMatchesType(value, typ) {
			return true
		}
	}
	return false
}

func jsonValueMatchesType(value interface{}, typ string) bool {
	switch typ {
	case "null":
		return value == nil
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		switch value.(type) {
		case float64, float32, int, int64, int32, json.Number:
			return true
		default:
			return false
		}
	case "integer":
		switch v := value.(type) {
		case int, int64, int32:
			return true
		case float64:
			return v == float64(int64(v))
		case json.Number:
			_, err := v.Int64()
			return err == nil
		default:
			return false
		}
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	default:
		return true
	}
}

func jsonSchemaEnumContains(enumValues []interface{}, value interface{}) bool {
	for _, item := range enumValues {
		if reflect.DeepEqual(item, value) {
			return true
		}
		itemJSON, itemErr := json.Marshal(item)
		valueJSON, valueErr := json.Marshal(value)
		if itemErr == nil && valueErr == nil && bytes.Equal(itemJSON, valueJSON) {
			return true
		}
	}
	return false
}

var supportedKiroRegions = map[string]struct{}{
	"af-south-1":     {},
	"ap-east-1":      {},
	"ap-northeast-1": {},
	"ap-northeast-2": {},
	"ap-northeast-3": {},
	"ap-south-1":     {},
	"ap-south-2":     {},
	"ap-southeast-1": {},
	"ap-southeast-2": {},
	"ap-southeast-3": {},
	"ap-southeast-4": {},
	"ap-southeast-5": {},
	"ap-southeast-7": {},
	"ca-central-1":   {},
	"ca-west-1":      {},
	"cn-north-1":     {},
	"cn-northwest-1": {},
	"eu-central-1":   {},
	"eu-central-2":   {},
	"eu-north-1":     {},
	"eu-south-1":     {},
	"eu-south-2":     {},
	"eu-west-1":      {},
	"eu-west-2":      {},
	"eu-west-3":      {},
	"il-central-1":   {},
	"me-central-1":   {},
	"me-south-1":     {},
	"mx-central-1":   {},
	"sa-east-1":      {},
	"us-east-1":      {},
	"us-east-2":      {},
	"us-gov-east-1":  {},
	"us-gov-west-1":  {},
	"us-west-1":      {},
	"us-west-2":      {},
}

func isSupportedKiroRegion(region string) bool {
	_, ok := supportedKiroRegions[strings.TrimSpace(region)]
	return ok
}

func parseRegionFromProfileArn(profileArn string) string {
	parts := strings.Split(strings.TrimSpace(profileArn), ":")
	if len(parts) < 4 {
		return ""
	}
	if parts[0] != "arn" || parts[2] != "codewhisperer" {
		return ""
	}
	region := strings.TrimSpace(parts[3])
	if !isSupportedKiroRegion(region) {
		return ""
	}
	return region
}

func resolveKiroRegion(profileArn, accountRegion string) string {
	if region := parseRegionFromProfileArn(profileArn); region != "" {
		return region
	}
	if region := strings.TrimSpace(accountRegion); isSupportedKiroRegion(region) {
		return region
	}
	return defaultKiroRegion
}

func resolveAccountKiroRegion(account *config.Account) string {
	if account == nil {
		return defaultKiroRegion
	}
	return resolveKiroRegion(account.ProfileArn, account.Region)
}

func (ep kiroEndpoint) withRegion(region string) kiroEndpoint {
	if region == "" {
		region = defaultKiroRegion
	}
	service := ep.Service
	if service == "" {
		if parsed, err := url.Parse(ep.URL); err == nil {
			service = strings.Split(parsed.Host, ".")[0]
		}
	}
	path := ep.Path
	if path == "" {
		if parsed, err := url.Parse(ep.URL); err == nil {
			path = parsed.Path
		}
	}
	ep.URL = fmt.Sprintf("https://%s.%s.amazonaws.com%s", service, region, path)
	return ep
}

// ==================== Request Structs ====================

// KiroPayload is the top-level request body sent to the Kiro API.
type KiroPayload struct {
	ConversationState struct {
		AgentContinuationId string `json:"agentContinuationId,omitempty"`
		AgentTaskType       string `json:"agentTaskType,omitempty"`
		ChatTriggerType     string `json:"chatTriggerType"`
		ConversationID      string `json:"conversationId"`
		CurrentMessage      struct {
			UserInputMessage KiroUserInputMessage `json:"userInputMessage"`
		} `json:"currentMessage"`
		History []KiroHistoryMessage `json:"history,omitempty"`
	} `json:"conversationState"`
	ProfileArn      string           `json:"profileArn,omitempty"`
	InferenceConfig *InferenceConfig `json:"inferenceConfig,omitempty"`

	// ToolNameMap maps sanitized tool names (sent to Kiro) back to the
	// original names supplied by the client. Used to restore original names
	// in tool_use responses so the client can match them to its tool registry.
	// Not serialized to the Kiro API request body.
	ToolNameMap map[string]string `json:"-"`

	// ProfileArnFinalized indicates guarded handler paths have already resolved
	// or intentionally left ProfileArn empty, so CallKiroAPI must not mutate it.
	ProfileArnFinalized bool `json:"-"`

	// Tool reference metadata is kept out of the upstream request body and used
	// only for request logging/diagnostics.
	DeferredToolReferenceNames     []string `json:"-"`
	MaterializedToolReferenceNames []string `json:"-"`

	// ToolSchemas keeps a minimal, non-serialized copy of client tool
	// requirements so upstream tool_use events can be checked before they are
	// emitted back to strict clients such as Claude Code.
	ToolSchemas map[string]toolSchemaSummary `json:"-"`

	// Context continuity metadata is not sent upstream. It is only recorded in
	// local request logs/readiness diagnostics to explain Claude Code turns.
	CurrentMessageShape          string   `json:"-"`
	ContextReminderKinds         []string `json:"-"`
	OrphanedToolResultsConverted int      `json:"-"`
	ToolResultImages             int      `json:"-"`
	RelocatedToolDescriptions    int      `json:"-"`
	UnsupportedContentBlocks     []string `json:"-"`
}

type toolSchemaSummary struct {
	Required []string
	Schema   map[string]interface{}
}

type KiroUserInputMessage struct {
	Content                 string                   `json:"content"`
	ModelID                 string                   `json:"modelId,omitempty"`
	Origin                  string                   `json:"origin"`
	Images                  []KiroImage              `json:"images,omitempty"`
	UserInputMessageContext *UserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

type UserInputMessageContext struct {
	Tools       []KiroToolWrapper `json:"tools,omitempty"`
	ToolResults []KiroToolResult  `json:"toolResults,omitempty"`
}

type KiroToolWrapper struct {
	ToolSpecification struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		InputSchema InputSchema `json:"inputSchema"`
	} `json:"toolSpecification"`
}

type InputSchema struct {
	JSON interface{} `json:"json"`
}

type KiroToolResult struct {
	ToolUseID string              `json:"toolUseId"`
	Content   []KiroResultContent `json:"content"`
	Status    string              `json:"status"`
}

type KiroResultContent struct {
	Text string `json:"text"`
}

type KiroImage struct {
	Format string `json:"format"`
	Source struct {
		Bytes string `json:"bytes"`
	} `json:"source"`
}

type KiroHistoryMessage struct {
	UserInputMessage         *KiroUserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *KiroAssistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

type KiroAssistantResponseMessage struct {
	Content  string        `json:"content"`
	ToolUses []KiroToolUse `json:"toolUses,omitempty"`
}

type KiroToolUse struct {
	ToolUseID string                 `json:"toolUseId"`
	Name      string                 `json:"name"`
	Input     map[string]interface{} `json:"input"`
}

type InferenceConfig struct {
	MaxTokens   int     `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
}

// ==================== Stream Callbacks ====================

// KiroStreamCallback stream response callbacks
type KiroStreamCallback struct {
	OnText              func(text string, isThinking bool)
	OnToolUse           func(toolUse KiroToolUse)
	OnValidatedToolUse  func(toolUse KiroToolUse) bool
	OnSuppressedToolUse func(toolUse KiroToolUse, reason string)
	OnComplete          func(inputTokens, outputTokens int)
	OnError             func(err error)
	OnCredits           func(credits float64)
	OnContextUsage      func(percentage float64)
}

// ==================== API Call ====================

// getSortedEndpoints returns endpoints ordered by user preference, with optional fallback.
func getSortedEndpoints(preferred string) []kiroEndpoint {
	fallback := config.GetEndpointFallback()

	var primary int
	switch preferred {
	case "kiro":
		primary = 0
	case "codewhisperer":
		primary = 1
	case "amazonq":
		primary = 2
	default:
		// "auto": Kiro first, then fallback to others
		return []kiroEndpoint{kiroEndpoints[0], kiroEndpoints[1], kiroEndpoints[2]}
	}

	if !fallback {
		// No fallback: only use the selected endpoint
		return []kiroEndpoint{kiroEndpoints[primary]}
	}

	// With fallback: selected first, then others in order
	result := []kiroEndpoint{kiroEndpoints[primary]}
	for i, ep := range kiroEndpoints {
		if i != primary {
			result = append(result, ep)
		}
	}
	return result
}

func getSortedEndpointsForRegion(preferred, region string) []kiroEndpoint {
	endpoints := getSortedEndpoints(preferred)
	for i := range endpoints {
		endpoints[i] = endpoints[i].withRegion(region)
	}
	return endpoints
}

// CallKiroAPI calls the Kiro streaming API, trying each configured endpoint with automatic fallback.
func CallKiroAPI(account *config.Account, payload *KiroPayload, callback *KiroStreamCallback) error {
	if _, err := json.Marshal(payload); err != nil {
		return err
	}
	opus47Payload := payload != nil && isOpus47Model(payload.ConversationState.CurrentMessage.UserInputMessage.ModelID)

	// Debug: dump full payload for troubleshooting upstream rejections
	if payloadJSON, err := json.Marshal(payload); err == nil {
		logger.Debugf("[KiroAPI] Request payload: %s", string(payloadJSON))
	}

	callback = wrapKiroToolUseCallback(payload, callback)

	if payload != nil && strings.TrimSpace(payload.ProfileArn) == "" && !payload.ProfileArnFinalized {
		finalizeKiroPayloadProfileArn(payload, account)
	}

	// Build endpoint list ordered by configuration.
	endpoints := getSortedEndpointsForRegion(config.GetPreferredEndpoint(), resolveAccountKiroRegion(account))

	var lastErr error
	for _, ep := range endpoints {
		attemptPayload := payload
		malformedRetried := false
	retryEndpoint:
		// Update the origin field for the selected endpoint.
		attemptPayload.ConversationState.CurrentMessage.UserInputMessage.Origin = ep.Origin

		reqBody, _ := json.Marshal(attemptPayload)
		req, err := http.NewRequest("POST", ep.URL, bytes.NewReader(reqBody))
		if err != nil {
			lastErr = err
			continue
		}

		host := ""
		if parsedURL, parseErr := url.Parse(ep.URL); parseErr == nil {
			host = parsedURL.Host
		}
		headerValues := buildStreamingHeaderValues(account, host)

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "*/*")
		if ep.AmzTarget != "" {
			req.Header.Set("X-Amz-Target", ep.AmzTarget)
		}
		applyKiroBaseHeaders(req, account, headerValues)
		req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
		req.Header.Set("x-amzn-codewhisperer-optout", "true")
		req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
		req.Header.Set("Amz-Sdk-Invocation-Id", uuid.New().String())

		resp, err := GetClientForProxy(ResolveAccountProxyURL(account)).Do(req)
		if err != nil {
			lastErr = err
			logger.Warnf("[KiroAPI] Endpoint %s failed: %v", ep.Name, err)
			continue
		}

		if resp.StatusCode == 429 {
			errBody := readResponseBody(resp)
			resetAt := rateLimitResetAt(resp.Header, errBody, time.Now())
			resp.Body.Close()
			if errBody != "" {
				if opus47Payload {
					logger.Warnf("[KiroAPI] Endpoint %s returned Opus 4.7 429: %s", ep.Name, errBody)
				} else {
					logger.Warnf("[KiroAPI] Endpoint %s returned 429: %s, trying next...", ep.Name, errBody)
				}
			} else {
				if opus47Payload {
					logger.Warnf("[KiroAPI] Endpoint %s returned Opus 4.7 429", ep.Name)
				} else {
					logger.Warnf("[KiroAPI] Endpoint %s returned 429, trying next...", ep.Name)
				}
			}
			lastErr = &rateLimitError{endpoint: ep.Name, body: errBody, resetAt: resetAt}
			if opus47Payload {
				return lastErr
			}
			continue
		}

		if resp.StatusCode != 200 {
			errBody := readResponseBody(resp)
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, ep.Name, errBody)
			if resp.StatusCode == 400 && isKiroMalformedRequestBody(errBody) && !malformedRetried {
				retryPayload := cloneKiroPayload(attemptPayload)
				if retryPayload != nil {
					guardResult, guardErr := prepareGuardedKiroPayload(retryPayload, conservativePayloadGuardOptions())
					if guardErr == nil {
						if guardResult.Trimmed || guardResult.FinalBytes < guardResult.OriginalBytes {
							malformedRetried = true
							attemptPayload = retryPayload
							logger.Warnf("[KiroAPI] Endpoint %s rejected payload; retrying once with conservative guard: err=%v before=%+v after=%+v guard=%+v", ep.Name, lastErr, summarizeKiroPayload(payload), summarizeKiroPayload(retryPayload), guardResult)
							goto retryEndpoint
						}
						logger.Warnf("[KiroAPI] Endpoint %s malformed payload not retried because conservative guard made no structural change: err=%v summary=%+v", ep.Name, lastErr, summarizeKiroPayload(attemptPayload))
					}
					logger.Warnf("[KiroAPI] Endpoint %s conservative retry guard failed: err=%v summary=%+v", ep.Name, guardErr, summarizeKiroPayload(retryPayload))
				}
			}
			// Authentication errors and payment errors are not retried across endpoints.
			if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 402 {
				if resp.StatusCode == 400 {
					logger.Warnf("[KiroAPI] Endpoint %s rejected malformed payload: err=%v summary=%+v", ep.Name, lastErr, summarizeKiroPayload(attemptPayload))
				}
				return lastErr
			}
			logger.Warnf("[KiroAPI] Endpoint %s error: %v", ep.Name, lastErr)
			continue
		}

		err = parseEventStream(resp.Body, callback)
		resp.Body.Close()
		return err
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all endpoints failed")
}

func isKiroMalformedRequestBody(body string) bool {
	body = strings.ToLower(strings.TrimSpace(body))
	return strings.Contains(body, "improperly formed request") || strings.Contains(body, "malformed")
}

func readResponseBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return strings.TrimSpace(string(body))
}

func rateLimitResetAt(headers http.Header, body string, now time.Time) time.Time {
	if resetAt, ok := rateLimitResetAtFromHeaders(headers, now); ok {
		return resetAt
	}
	if resetAt, ok := rateLimitResetAtFromBody(body, now); ok {
		return resetAt
	}
	return now.Add(defaultRateLimitFallbackSeconds * time.Second)
}

func rateLimitResetAtFromHeaders(headers http.Header, now time.Time) (time.Time, bool) {
	if headers == nil {
		return time.Time{}, false
	}
	if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
		if resetAt, ok := parseRateLimitTime(retryAfter, now); ok {
			return resetAt, true
		}
	}
	for _, name := range []string{
		"x-ratelimit-reset-requests",
		"x-ratelimit-reset-tokens",
		"anthropic-ratelimit-unified-reset",
	} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			if resetAt, ok := parseRateLimitTime(value, now); ok {
				return resetAt, true
			}
		}
	}
	return time.Time{}, false
}

func rateLimitResetAtFromBody(body string, now time.Time) (time.Time, bool) {
	if strings.TrimSpace(body) == "" {
		return time.Time{}, false
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return time.Time{}, false
	}
	for _, key := range []string{"rate_limit_reset_at", "reset_at"} {
		if value, ok := stringValue(raw[key]); ok {
			if resetAt, ok := parseRateLimitTime(value, now); ok {
				return resetAt, true
			}
		}
	}
	for _, key := range []string{"reset_after_seconds", "retry_after_seconds", "retry_after"} {
		if seconds, ok := secondsValue(raw[key]); ok {
			return now.Add(time.Duration(seconds) * time.Second), true
		}
	}
	return time.Time{}, false
}

func parseRateLimitTime(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds > 1000000000 {
			return time.Unix(int64(seconds), 0), true
		}
		if seconds < 0 {
			seconds = 0
		}
		return now.Add(time.Duration(seconds * float64(time.Second))), true
	}
	if resetAt, err := time.Parse(time.RFC3339, value); err == nil {
		return resetAt, true
	}
	if resetAt, err := http.ParseTime(value); err == nil {
		return resetAt, true
	}
	return time.Time{}, false
}

func stringValue(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, v != ""
	default:
		return "", false
	}
}

func secondsValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case string:
		seconds, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return seconds, err == nil
	default:
		return 0, false
	}
}

// ==================== Event Stream Parsing ====================

// parseEventStream decodes an AWS binary Event Stream response body.
func parseEventStream(body io.Reader, callback *KiroStreamCallback) error {
	// Read directly without bufio to avoid buffering latency in streaming responses.
	var inputTokens, outputTokens int
	var totalCredits float64
	var currentToolUse *toolUseState
	var lastAssistantContent string
	var lastReasoningContent string

	for {
		// Prelude: 12 bytes (total_len + headers_len + crc)
		prelude := make([]byte, 12)
		_, err := io.ReadFull(body, prelude)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		totalLength := int(prelude[0])<<24 | int(prelude[1])<<16 | int(prelude[2])<<8 | int(prelude[3])
		headersLength := int(prelude[4])<<24 | int(prelude[5])<<16 | int(prelude[6])<<8 | int(prelude[7])

		if totalLength < 16 {
			continue
		}

		// Read the remaining message bytes.
		remaining := totalLength - 12
		msgBuf := make([]byte, remaining)
		_, err = io.ReadFull(body, msgBuf)
		if err != nil {
			return err
		}

		if headersLength > len(msgBuf)-4 {
			continue
		}

		eventType := extractEventType(msgBuf[0:headersLength])
		payloadBytes := msgBuf[headersLength : len(msgBuf)-4]
		if len(payloadBytes) == 0 {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal(payloadBytes, &event); err != nil {
			continue
		}

		inputTokens, outputTokens = updateTokensFromEvent(event, inputTokens, outputTokens)

		// Dispatch by event type.
		switch eventType {
		case "assistantResponseEvent":
			if content, ok := event["content"].(string); ok && content != "" {
				normalized := normalizeChunk(content, &lastAssistantContent)
				if normalized != "" && callback.OnText != nil {
					callback.OnText(normalized, false)
				}
			}
		case "reasoningContentEvent":
			if text, ok := event["text"].(string); ok && text != "" {
				normalized := normalizeChunk(text, &lastReasoningContent)
				if normalized != "" && callback.OnText != nil {
					callback.OnText(normalized, true)
				}
			}
		case "toolUseEvent":
			currentToolUse = handleToolUseEvent(event, currentToolUse, callback)
		case "meteringEvent":
			if usage, ok := event["usage"].(float64); ok {
				totalCredits += usage
			}
		case "contextUsageEvent":
			if pct, ok := event["contextUsagePercentage"].(float64); ok {
				if callback.OnContextUsage != nil {
					callback.OnContextUsage(pct)
				}
			}
		}
	}

	if currentToolUse != nil && (callback.OnToolUse != nil || callback.OnValidatedToolUse != nil) {
		finishToolUse(currentToolUse, callback)
	}

	if callback.OnCredits != nil && totalCredits > 0 {
		callback.OnCredits(totalCredits)
	}

	if callback.OnComplete != nil {
		callback.OnComplete(inputTokens, outputTokens)
	}
	return nil
}

func updateTokensFromEvent(event map[string]interface{}, currentInputTokens, currentOutputTokens int) (int, int) {
	candidates := []map[string]interface{}{event}
	collectUsageMaps(event, &candidates)

	inputTokens := currentInputTokens
	outputTokens := currentOutputTokens

	for _, usage := range candidates {
		if usage == nil {
			continue
		}

		if v, ok := readTokenNumber(usage,
			"outputTokens", "completionTokens", "totalOutputTokens",
			"output_tokens", "completion_tokens", "total_output_tokens",
		); ok {
			outputTokens = v
		}

		if v, ok := readTokenNumber(usage,
			"inputTokens", "promptTokens", "totalInputTokens",
			"input_tokens", "prompt_tokens", "total_input_tokens",
		); ok {
			inputTokens = v
			continue
		}

		uncached, _ := readTokenNumber(usage, "uncachedInputTokens", "uncached_input_tokens")
		cacheRead, _ := readTokenNumber(usage, "cacheReadInputTokens", "cache_read_input_tokens")
		cacheWrite, _ := readTokenNumber(usage, "cacheWriteInputTokens", "cache_write_input_tokens", "cacheCreationInputTokens", "cache_creation_input_tokens")
		if uncached+cacheRead+cacheWrite > 0 {
			inputTokens = uncached + cacheRead + cacheWrite
			continue
		}

		total, ok := readTokenNumber(usage, "totalTokens", "total_tokens")
		if ok && total > 0 {
			candidateOutput := outputTokens
			if v, vok := readTokenNumber(usage,
				"outputTokens", "completionTokens", "totalOutputTokens",
				"output_tokens", "completion_tokens", "total_output_tokens",
			); vok {
				candidateOutput = v
			}
			if total-candidateOutput > 0 {
				inputTokens = total - candidateOutput
			}
		}
	}

	return inputTokens, outputTokens
}

// getContextWindowSize returns the context window size (in tokens) for a model.
func getContextWindowSize(model string) int {
	m := strings.ToLower(model)
	// sonnet-4.6, opus-4.6, opus-4.7 all have 1M context windows
	if strings.Contains(m, "4.6") || strings.Contains(m, "4-6") ||
		strings.Contains(m, "4.7") || strings.Contains(m, "4-7") {
		return 1_000_000
	}
	return 200_000
}

func collectUsageMaps(v interface{}, out *[]map[string]interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, child := range t {
			lk := strings.ToLower(k)
			if lk == "usage" || lk == "tokenusage" || lk == "token_usage" {
				if m, ok := child.(map[string]interface{}); ok {
					*out = append(*out, m)
				}
			}
			collectUsageMaps(child, out)
		}
	case []interface{}:
		for _, child := range t {
			collectUsageMaps(child, out)
		}
	}
}

func normalizeChunk(chunk string, previous *string) string {
	if chunk == "" {
		return ""
	}

	prev := *previous
	if prev == "" {
		*previous = chunk
		return chunk
	}

	if chunk == prev {
		return ""
	}

	if strings.HasPrefix(chunk, prev) {
		delta := chunk[len(prev):]
		*previous = chunk
		return delta
	}

	if strings.HasPrefix(prev, chunk) {
		return ""
	}

	maxOverlap := 0
	maxLen := len(prev)
	if len(chunk) < maxLen {
		maxLen = len(chunk)
	}
	for i := maxLen; i > 0; i-- {
		if strings.HasSuffix(prev, chunk[:i]) {
			maxOverlap = i
			break
		}
	}

	*previous = chunk
	if maxOverlap > 0 {
		return chunk[maxOverlap:]
	}

	return chunk
}

func readTokenNumber(m map[string]interface{}, keys ...string) (int, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int(n), true
		case int:
			return n, true
		case int64:
			return int(n), true
		case json.Number:
			if parsed, err := n.Int64(); err == nil {
				return int(parsed), true
			}
		case string:
			if parsed, err := strconv.Atoi(n); err == nil {
				return parsed, true
			}
			if parsed, err := strconv.ParseFloat(n, 64); err == nil {
				return int(parsed), true
			}
		}
	}
	return 0, false
}

// ==================== Tool Use Handling ====================

type toolUseState struct {
	ToolUseID   string
	Name        string
	InputBuffer strings.Builder
}

func handleToolUseEvent(event map[string]interface{}, current *toolUseState, callback *KiroStreamCallback) *toolUseState {
	toolUseID, _ := event["toolUseId"].(string)
	name, _ := event["name"].(string)
	isStop, _ := event["stop"].(bool)

	if toolUseID != "" && name != "" {
		if current == nil {
			current = &toolUseState{ToolUseID: toolUseID, Name: name}
		} else if current.ToolUseID != toolUseID {
			finishToolUse(current, callback)
			current = &toolUseState{ToolUseID: toolUseID, Name: name}
		}
	}

	if current != nil {
		if input, ok := event["input"].(string); ok {
			writeToolUseInputChunk(current, input)
		} else if inputObj, ok := event["input"].(map[string]interface{}); ok {
			data, _ := json.Marshal(inputObj)
			current.InputBuffer.Reset()
			current.InputBuffer.Write(data)
		}
	}

	if isStop && current != nil {
		finishToolUse(current, callback)
		return nil
	}

	return current
}

func writeToolUseInputChunk(current *toolUseState, input string) {
	if current == nil || input == "" {
		return
	}
	trimmed := strings.TrimSpace(input)
	if current.InputBuffer.Len() == 0 {
		current.InputBuffer.WriteString(input)
		return
	}
	existing := strings.TrimSpace(current.InputBuffer.String())
	if existing == "{}" && strings.HasPrefix(trimmed, "{") {
		current.InputBuffer.Reset()
		current.InputBuffer.WriteString(input)
		return
	}
	if json.Valid([]byte(input)) && strings.HasPrefix(trimmed, "{") {
		current.InputBuffer.Reset()
		current.InputBuffer.WriteString(input)
		return
	}
	current.InputBuffer.WriteString(input)
}

func finishToolUse(state *toolUseState, callback *KiroStreamCallback) {
	var input map[string]interface{}
	if state.InputBuffer.Len() > 0 {
		input = parseToolUseInputBuffer(state.InputBuffer.String())
	}
	if input == nil {
		input = make(map[string]interface{})
	}
	toolUse := KiroToolUse{
		ToolUseID: state.ToolUseID,
		Name:      state.Name,
		Input:     input,
	}
	if callback.OnValidatedToolUse != nil {
		callback.OnValidatedToolUse(toolUse)
		return
	}
	callback.OnToolUse(toolUse)
}

func parseToolUseInputBuffer(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &input); err == nil {
		return input
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	for {
		var next map[string]interface{}
		if err := decoder.Decode(&next); err != nil {
			break
		}
		if len(next) > 0 {
			input = next
		}
	}
	return input
}

// extractEventType extracts the event type string from AWS Event Stream message headers.
func extractEventType(headers []byte) string {
	offset := 0
	for offset < len(headers) {
		if offset >= len(headers) {
			break
		}
		nameLen := int(headers[offset])
		offset++
		if offset+nameLen > len(headers) {
			break
		}
		name := string(headers[offset : offset+nameLen])
		offset += nameLen
		if offset >= len(headers) {
			break
		}
		valueType := headers[offset]
		offset++

		if valueType == 7 { // String
			if offset+2 > len(headers) {
				break
			}
			valueLen := int(headers[offset])<<8 | int(headers[offset+1])
			offset += 2
			if offset+valueLen > len(headers) {
				break
			}
			value := string(headers[offset : offset+valueLen])
			offset += valueLen
			if name == ":event-type" {
				return value
			}
			continue
		}

		// Skip other value types by their fixed byte widths.
		skipSizes := map[byte]int{0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}
		if valueType == 6 {
			if offset+2 > len(headers) {
				break
			}
			l := int(headers[offset])<<8 | int(headers[offset+1])
			offset += 2 + l
		} else if skip, ok := skipSizes[valueType]; ok {
			offset += skip
		} else {
			break
		}
	}
	return ""
}
