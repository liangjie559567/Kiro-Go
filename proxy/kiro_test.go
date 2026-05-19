package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"kiro-go/config"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalizeChunkBasicProgression(t *testing.T) {
	prev := ""

	if got := normalizeChunk("abc", &prev); got != "abc" {
		t.Fatalf("expected first chunk to pass through, got %q", got)
	}
	if got := normalizeChunk("abcde", &prev); got != "de" {
		t.Fatalf("expected appended delta, got %q", got)
	}
}

func TestNormalizeChunkPrefixRewindDoesNotReplay(t *testing.T) {
	prev := ""

	_ = normalizeChunk("abcde", &prev)
	if got := normalizeChunk("abc", &prev); got != "" {
		t.Fatalf("expected rewind chunk to be ignored, got %q", got)
	}
	if prev != "abcde" {
		t.Fatalf("expected previous snapshot to remain longest version, got %q", prev)
	}
	if got := normalizeChunk("abcdef", &prev); got != "f" {
		t.Fatalf("expected only unseen suffix after rewind, got %q", got)
	}
}

func TestNormalizeChunkOverlapDelta(t *testing.T) {
	prev := "hello world"

	if got := normalizeChunk("world!!!", &prev); got != "!!!" {
		t.Fatalf("expected overlap suffix delta, got %q", got)
	}
}

func TestBuildKiroTransportUsesExplicitProxyURL(t *testing.T) {
	transport := buildKiroTransport("http://proxy.local:8080")
	req := &http.Request{URL: mustParseURL(t, "https://q.us-east-1.amazonaws.com")}

	got, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("unexpected proxy error: %v", err)
	}
	assertProxyURL(t, got, "http://proxy.local:8080")
}

func TestBuildKiroTransportFallsBackToEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "http://env-proxy.local:2323")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	transport := buildKiroTransport("")
	req := &http.Request{URL: mustParseURL(t, "https://q.us-east-1.amazonaws.com")}

	got, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("unexpected proxy error: %v", err)
	}
	assertProxyURL(t, got, "http://env-proxy.local:2323")
}

func TestBuildKiroTransportConcurrencyAndTimeouts(t *testing.T) {
	transport := buildKiroTransport("")

	if transport.MaxIdleConns != 200 {
		t.Fatalf("expected global idle pool of 200, got %d", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 50 {
		t.Fatalf("expected per-host idle pool of 50, got %d", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxConnsPerHost != 100 {
		t.Fatalf("expected per-host connection cap of 100, got %d", transport.MaxConnsPerHost)
	}
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("expected response header timeout of 60s, got %s", transport.ResponseHeaderTimeout)
	}
	if transport.ExpectContinueTimeout != time.Second {
		t.Fatalf("expected expect-continue timeout of 1s, got %s", transport.ExpectContinueTimeout)
	}
	if transport.DialContext == nil {
		t.Fatalf("expected explicit dialer with timeout and keepalive")
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatalf("expected HTTP/2 attempts to remain enabled without explicit proxy")
	}
}

func TestInitKiroHttpClientKeepsShortRestTimeout(t *testing.T) {
	InitKiroHttpClient("")
	t.Cleanup(func() { InitKiroHttpClient("") })

	streamClient := kiroHttpStore.Load()
	restClient := kiroRestHttpStore.Load()

	if streamClient.Timeout != 5*time.Minute {
		t.Fatalf("expected streaming timeout to be 5m, got %s", streamClient.Timeout)
	}
	if restClient.Timeout != 30*time.Second {
		t.Fatalf("expected REST timeout to stay 30s, got %s", restClient.Timeout)
	}
}

func TestResolveKiroRegionPrefersProfileArnThenAccountRegion(t *testing.T) {
	if got := resolveKiroRegion(
		"arn:aws:codewhisperer:eu-central-1:123456789012:profile/test",
		"us-west-2",
	); got != "eu-central-1" {
		t.Fatalf("expected profile ARN region to win, got %q", got)
	}
	if got := resolveKiroRegion("", "ap-southeast-1"); got != "ap-southeast-1" {
		t.Fatalf("expected account region fallback, got %q", got)
	}
	if got := resolveKiroRegion("arn:aws:s3:us-west-2:123456789012:bucket/test", "not-a-region"); got != defaultKiroRegion {
		t.Fatalf("expected default region for unsupported inputs, got %q", got)
	}
}

func TestCallKiroAPIUsesAccountRegionForStreamingEndpoint(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	var requestedHost string
	var requestedRequestHost string
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedHost = req.URL.Host
			requestedRequestHost = req.Host
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.6",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)

	err := CallKiroAPI(&config.Account{
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:eu-central-1:123456789012:profile/test",
		Region:      "us-east-1",
	}, payload, &KiroStreamCallback{})
	if err == nil {
		t.Fatalf("expected mocked 429 error")
	}
	if requestedHost != "q.eu-central-1.amazonaws.com" {
		t.Fatalf("expected regional q host, got %q", requestedHost)
	}
	if requestedRequestHost != "q.eu-central-1.amazonaws.com" {
		t.Fatalf("expected request Host header to match regional q host, got %q", requestedRequestHost)
	}
}

func TestCallKiroAPIDoesNotMutateFinalizedProfileArn(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.6",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)
	payload.ProfileArnFinalized = true

	var capturedProfileArn interface{}
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			capturedProfileArn = body["profileArn"]
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 1, "outputTokens": 1}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	err := CallKiroAPI(&config.Account{
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/should-not-be-added",
	}, payload, &KiroStreamCallback{})
	if err != nil {
		t.Fatalf("call kiro api: %v", err)
	}
	if payload.ProfileArn != "" {
		t.Fatalf("expected finalized empty profile ARN not to be mutated, got %q", payload.ProfileArn)
	}
	if capturedProfileArn != nil {
		t.Fatalf("expected serialized request to omit profileArn, got %#v", capturedProfileArn)
	}
}

func TestCallKiroAPIRetainsTooManyRequestsBody(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"model claude-opus-4.7 is throttled","reason":"MODEL_RATE_LIMIT"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-opus-4-7",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)

	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{})
	if err == nil {
		t.Fatalf("expected 429 error")
	}
	if !strings.Contains(err.Error(), "MODEL_RATE_LIMIT") || !strings.Contains(err.Error(), "claude-opus-4.7 is throttled") {
		t.Fatalf("expected 429 body to be preserved, got %q", err.Error())
	}
}

func TestCallKiroAPIDoesNotFallbackEndpointsForOpus47RateLimit(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("auto"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(true); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"Too many requests, please wait before trying again.","retry_after_seconds":5}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-opus-4.7",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)

	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{})
	if err == nil {
		t.Fatalf("expected 429 error")
	}
	if attempts != 1 {
		t.Fatalf("expected opus 4.7 429 to stop endpoint fallback after one attempt, got %d", attempts)
	}
}

func TestCallKiroAPIDoesNotFallbackEndpointsForMalformedRequest(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("auto"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(true); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	attempts := 0
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"message":"Improperly formed request.","reason":null}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-opus-4.7",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)

	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{})
	if err == nil {
		t.Fatalf("expected malformed request error")
	}
	if attempts != 1 {
		t.Fatalf("expected malformed request to stop endpoint fallback after one attempt, got %d", attempts)
	}
	if !strings.Contains(err.Error(), "Improperly formed request") {
		t.Fatalf("expected upstream 400 body to be preserved, got %q", err.Error())
	}
}

func TestCallKiroAPIRetriesMalformedWithConservativePayload(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	attempts := 0
	var retrySummary kiroPayloadSummary
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"message":"Improperly formed request.","reason":null}`)),
					Header:     make(http.Header),
				}, nil
			}
			var sent KiroPayload
			if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
				t.Fatalf("decode retry request: %v", err)
			}
			retrySummary = summarizeKiroPayload(&sent)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 1, "outputTokens": 1}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := malformedRiskToolHistoryPayload(24, 16)
	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{})
	if err != nil {
		t.Fatalf("expected malformed retry to recover, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected one conservative retry, got %d attempts", attempts)
	}
	if retrySummary.HistoryToolUses > conservativeKiroHistoryToolUses || retrySummary.CurrentTools > conservativeKiroTools {
		t.Fatalf("expected conservative retry payload, got %#v", retrySummary)
	}
}

func TestCallKiroAPIReturnsRateLimitResetFromRetryAfter(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Retry-After", "2")
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited"}`)),
				Header:     header,
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)

	start := time.Now()
	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{})
	if err == nil {
		t.Fatalf("expected 429 error")
	}
	rlErr, ok := err.(interface{ RateLimitResetAt() time.Time })
	if !ok {
		t.Fatalf("expected rate limit reset error, got %T", err)
	}
	resetAt := rlErr.RateLimitResetAt()
	if resetAt.Before(start.Add(1500*time.Millisecond)) || resetAt.After(start.Add(3*time.Second)) {
		t.Fatalf("expected reset near 2s from start, got %s", resetAt.Sub(start))
	}
}

func TestCallKiroAPIReturnsRateLimitResetFromBody(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	resetAt := time.Now().Add(4 * time.Second).UTC().Truncate(time.Second)
	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","rate_limit_reset_at":"` + resetAt.Format(time.RFC3339) + `"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
	}, false)

	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{})
	if err == nil {
		t.Fatalf("expected 429 error")
	}
	rlErr, ok := err.(interface{ RateLimitResetAt() time.Time })
	if !ok {
		t.Fatalf("expected rate limit reset error, got %T", err)
	}
	if !rlErr.RateLimitResetAt().Equal(resetAt) {
		t.Fatalf("expected reset %s, got %s", resetAt, rlErr.RateLimitResetAt())
	}
}

func TestHandleToolUseEventReplacesEmptyObjectPrefixWithFullJSON(t *testing.T) {
	var got []KiroToolUse
	callback := &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	}

	current := handleToolUseEvent(map[string]interface{}{
		"toolUseId": "toolu_1",
		"name":      "read_file",
		"input":     "{}",
	}, nil, callback)
	current = handleToolUseEvent(map[string]interface{}{
		"toolUseId": "toolu_1",
		"name":      "read_file",
		"input":     `{"path":"/tmp/a.go"}`,
		"stop":      true,
	}, current, callback)

	if current != nil {
		t.Fatalf("expected completed tool use to clear current state")
	}
	if len(got) != 1 {
		t.Fatalf("expected one tool use, got %#v", got)
	}
	if got[0].Input["path"] != "/tmp/a.go" {
		t.Fatalf("expected full JSON input without empty-object prefix, got %#v", got[0].Input)
	}
}

func TestHandleToolUseEventUsesLastObjectFromConcatenatedInput(t *testing.T) {
	var got []KiroToolUse
	callback := &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	}

	handleToolUseEvent(map[string]interface{}{
		"toolUseId": "toolu_1",
		"name":      "get_project_status",
		"input":     `{}{"scope":"full"}`,
		"stop":      true,
	}, nil, callback)

	if len(got) != 1 {
		t.Fatalf("expected one tool use, got %#v", got)
	}
	if got[0].Input["scope"] != "full" {
		t.Fatalf("expected last JSON object input, got %#v", got[0].Input)
	}
}

func TestCallKiroAPIFlushesUnstoppedToolUseAtStreamEnd(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "toolUseEvent", payload: map[string]interface{}{"toolUseId": "toolu_unstopped", "name": "read_file", "input": map[string]interface{}{"path": "/tmp/a.go"}}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 4, "outputTokens": 2}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "read file"}},
	}, false)

	var got []KiroToolUse
	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})
	if err != nil {
		t.Fatalf("CallKiroAPI: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected unstopped tool use to be flushed, got %#v", got)
	}
	if got[0].ToolUseID != "toolu_unstopped" || got[0].Name != "read_file" || got[0].Input["path"] != "/tmp/a.go" {
		t.Fatalf("unexpected flushed tool use: %#v", got[0])
	}
}

func TestCallKiroAPIFlushesUnstoppedValidatedToolUseAtStreamEnd(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
		t.Fatalf("update preferred endpoint: %v", err)
	}
	if err := config.UpdateEndpointFallback(false); err != nil {
		t.Fatalf("update endpoint fallback: %v", err)
	}

	kiroHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(buildTestEventStream(t, []testEventStreamMessage{
					{eventType: "toolUseEvent", payload: map[string]interface{}{"toolUseId": "toolu_unstopped", "name": "read_file", "input": map[string]interface{}{"path": "/tmp/a.go"}}},
					{eventType: "metadataEvent", payload: map[string]interface{}{"usage": map[string]interface{}{"inputTokens": 4, "outputTokens": 2}}},
				}))),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 16,
		Messages:  []ClaudeMessage{{Role: "user", Content: "read file"}},
	}, false)

	var got []KiroToolUse
	err := CallKiroAPI(&config.Account{AccessToken: "token", ProfileArn: "arn:aws:codewhisperer:profile/test"}, payload, &KiroStreamCallback{
		OnValidatedToolUse: func(tu KiroToolUse) bool {
			got = append(got, tu)
			return true
		},
	})
	if err != nil {
		t.Fatalf("CallKiroAPI: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected unstopped validated tool use to be flushed, got %#v", got)
	}
	if got[0].ToolUseID != "toolu_unstopped" || got[0].Name != "read_file" || got[0].Input["path"] != "/tmp/a.go" {
		t.Fatalf("unexpected flushed validated tool use: %#v", got[0])
	}
}

func TestWrapToolUseDropsInvalidRequiredArguments(t *testing.T) {
	payload := &KiroPayload{
		ToolNameMap: map[string]string{"readFile": "read_file"},
		ToolSchemas: map[string]toolSchemaSummary{
			"read_file": {Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
				"required":   []interface{}{"path"},
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_1",
		Name:      "readFile",
		Input:     map[string]interface{}{},
	})

	if len(got) != 0 {
		t.Fatalf("expected invalid tool_use to be dropped before client emission, got %#v", got)
	}
}

func TestWrapToolUseDropsWrongRequiredArgumentType(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"write_file": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string"},
					"content": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"path", "content"},
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_1",
		Name:      "write_file",
		Input:     map[string]interface{}{"path": []interface{}{"README.md"}, "content": "ok"},
	})

	if len(got) != 0 {
		t.Fatalf("expected wrong required argument type to be dropped, got %#v", got)
	}
}

func TestWrapToolUseDropsInvalidEnumArgument(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"update_todos": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{"type": "string", "enum": []interface{}{"pending", "in_progress", "completed"}},
				},
				"required": []interface{}{"status"},
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_1",
		Name:      "update_todos",
		Input:     map[string]interface{}{"status": "done"},
	})

	if len(got) != 0 {
		t.Fatalf("expected invalid enum argument to be dropped, got %#v", got)
	}
}

func TestWrapToolUseDropsArrayAboveMaxItems(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"request_user_input": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"questions": map[string]interface{}{
						"type":     "array",
						"maxItems": float64(3),
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"header": map[string]interface{}{"type": "string"},
							},
						},
					},
				},
				"required": []interface{}{"questions"},
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_user_input",
		Name:      "request_user_input",
		Input: map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{"header": "A"},
				map[string]interface{}{"header": "B"},
				map[string]interface{}{"header": "C"},
				map[string]interface{}{"header": "D"},
			},
		},
	})

	if len(got) != 0 {
		t.Fatalf("expected tool_use exceeding maxItems to be dropped, got %#v", got)
	}
}

func TestWrapToolUseRepairsClaudeCodeReadAliases(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"Read": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string"},
					"offset":    map[string]interface{}{"type": []interface{}{"integer", "null"}},
					"limit":     map[string]interface{}{"type": []interface{}{"integer", "null"}},
				},
				"required":             []interface{}{"file_path"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_read",
		Name:      "Read",
		Input:     map[string]interface{}{"path": "/www/automation/autonomous.md", "offset": "25", "limit": "80"},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired Read tool_use to pass, got %#v", got)
	}
	if got[0].Input["file_path"] != "/www/automation/autonomous.md" {
		t.Fatalf("expected path alias repaired to file_path, got %#v", got[0].Input)
	}
	if got[0].Input["offset"] != 25 || got[0].Input["limit"] != 80 {
		t.Fatalf("expected offset/limit coerced to integers, got %#v", got[0].Input)
	}
	if _, ok := got[0].Input["path"]; ok {
		t.Fatalf("expected path alias removed after repair, got %#v", got[0].Input)
	}
}

func TestWrapToolUseRepairsClaudeCodeTaskUpdateAliases(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"TaskUpdate": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"taskId": map[string]interface{}{"type": "string"},
					"status": map[string]interface{}{"type": "string", "enum": []interface{}{"pending", "in_progress", "completed", "deleted"}},
				},
				"required":             []interface{}{"taskId"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_task",
		Name:      "TaskUpdate",
		Input:     map[string]interface{}{"task_id": 123, "status": "done"},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired TaskUpdate tool_use to pass, got %#v", got)
	}
	if got[0].Input["taskId"] != "123" || got[0].Input["status"] != "completed" {
		t.Fatalf("expected task_id/status aliases repaired, got %#v", got[0].Input)
	}
	if _, ok := got[0].Input["task_id"]; ok {
		t.Fatalf("expected task_id alias removed after repair, got %#v", got[0].Input)
	}
}

func TestWrapToolUseRepairsClaudeCodeTaskUpdateNestedTaskAndStatusAliases(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"TaskUpdate": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"taskId":      map[string]interface{}{"type": "string"},
					"status":      map[string]interface{}{"type": "string", "enum": []interface{}{"pending", "in_progress", "completed", "deleted"}},
					"subject":     map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"activeForm":  map[string]interface{}{"type": "string"},
				},
				"required":             []interface{}{"taskId"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_task",
		Name:      "TaskUpdate",
		Input: map[string]interface{}{
			"task":        map[string]interface{}{"id": json.Number("42")},
			"status":      "in progress",
			"content":     "Run regression tests",
			"active_form": "Running regression tests",
		},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired TaskUpdate tool_use to pass, got %#v", got)
	}
	if got[0].Input["taskId"] != "42" || got[0].Input["status"] != "in_progress" {
		t.Fatalf("expected nested task id and status aliases repaired, got %#v", got[0].Input)
	}
	if got[0].Input["subject"] != "Run regression tests" || got[0].Input["activeForm"] != "Running regression tests" {
		t.Fatalf("expected subject/activeForm aliases repaired, got %#v", got[0].Input)
	}
	if _, ok := got[0].Input["task"]; ok {
		t.Fatalf("expected task object removed after repair, got %#v", got[0].Input)
	}
	if _, ok := got[0].Input["content"]; ok {
		t.Fatalf("expected content alias removed after repair, got %#v", got[0].Input)
	}
}

func TestWrapToolUseRepairsClaudeCodeTaskCreateAliases(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"TaskCreate": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject":     map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"activeForm":  map[string]interface{}{"type": "string"},
				},
				"required":             []interface{}{"subject", "description"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_task_create",
		Name:      "TaskCreate",
		Input: map[string]interface{}{
			"content":     "Write SUMMARY and commit",
			"active_form": "Writing SUMMARY and committing",
		},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired TaskCreate tool_use to pass, got %#v", got)
	}
	if got[0].Input["subject"] != "Write SUMMARY and commit" || got[0].Input["description"] != "Write SUMMARY and commit" {
		t.Fatalf("expected content alias to populate subject and description, got %#v", got[0].Input)
	}
	if got[0].Input["activeForm"] != "Writing SUMMARY and committing" {
		t.Fatalf("expected activeForm alias repaired, got %#v", got[0].Input)
	}
}

func TestWrapToolUseRepairsClaudeCodeTaskCreateTasksArray(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"TaskCreate": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject":     map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"activeForm":  map[string]interface{}{"type": "string"},
				},
				"required":             []interface{}{"subject", "description"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_task_create",
		Name:      "TaskCreate",
		Input: map[string]interface{}{
			"tasks": []interface{}{
				map[string]interface{}{
					"subject":     "Phase 46: discuss/smart-discuss",
					"description": "Collect context before planning",
					"activeForm":  "Running Phase 46 discuss",
				},
				map[string]interface{}{
					"subject":     "Phase 47: execute",
					"description": "Execute next phase",
					"activeForm":  "Running Phase 47",
				},
			},
		},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired TaskCreate tasks array to pass, got %#v", got)
	}
	if got[0].Input["subject"] != "Phase 46: discuss/smart-discuss" || got[0].Input["description"] != "Collect context before planning" {
		t.Fatalf("expected first task promoted to TaskCreate fields, got %#v", got[0].Input)
	}
	if got[0].Input["activeForm"] != "Running Phase 46 discuss" {
		t.Fatalf("expected first task activeForm promoted, got %#v", got[0].Input)
	}
	if _, ok := got[0].Input["tasks"]; ok {
		t.Fatalf("expected tasks array removed after repair, got %#v", got[0].Input)
	}
}

func TestWrapToolUseRepairsClaudeCodeTaskCreateTasksArrayWithoutDescription(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"TaskCreate": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject":     map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"activeForm":  map[string]interface{}{"type": "string"},
				},
				"required":             []interface{}{"subject", "description"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_task_create",
		Name:      "TaskCreate",
		Input: map[string]interface{}{
			"tasks": []interface{}{
				map[string]interface{}{
					"subject":    "Phase 46: discuss/smart-discuss",
					"activeForm": "Phase 46 discuss",
				},
			},
		},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired TaskCreate without description to pass, got %#v", got)
	}
	if got[0].Input["description"] != "Phase 46 discuss" {
		t.Fatalf("expected missing description to fall back to activeForm, got %#v", got[0].Input)
	}
}

func TestWrapToolUseRepairsClaudeCodeTodoWriteAliases(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"TodoWrite": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"todos": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"content":    map[string]interface{}{"type": "string"},
								"status":     map[string]interface{}{"type": "string", "enum": []interface{}{"pending", "in_progress", "completed"}},
								"activeForm": map[string]interface{}{"type": "string"},
							},
							"required":             []interface{}{"content", "status", "activeForm"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []interface{}{"todos"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_todo",
		Name:      "TodoWrite",
		Input: map[string]interface{}{
			"tasks": []interface{}{
				map[string]interface{}{
					"subject":     "Run tests",
					"status":      "doing",
					"active_form": "Running tests",
				},
			},
		},
	})

	if len(got) != 1 {
		t.Fatalf("expected repaired TodoWrite tool_use to pass, got %#v", got)
	}
	todos, ok := got[0].Input["todos"].([]interface{})
	if !ok || len(todos) != 1 {
		t.Fatalf("expected todos array, got %#v", got[0].Input)
	}
	todo, ok := todos[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected todo object, got %#v", todos[0])
	}
	if todo["content"] != "Run tests" || todo["status"] != "in_progress" || todo["activeForm"] != "Running tests" {
		t.Fatalf("expected todo aliases repaired, got %#v", todo)
	}
}

func TestWrapToolUseRepairsClaudeCodeFilesystemAndShellAliases(t *testing.T) {
	payload := &KiroPayload{
		ToolSchemas: map[string]toolSchemaSummary{
			"Bash": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command":     map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"timeout":     map[string]interface{}{"type": []interface{}{"integer", "null"}},
				},
				"required":             []interface{}{"command"},
				"additionalProperties": false,
			}},
			"Edit": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path":   map[string]interface{}{"type": "string"},
					"old_string":  map[string]interface{}{"type": "string"},
					"new_string":  map[string]interface{}{"type": "string"},
					"replace_all": map[string]interface{}{"type": []interface{}{"boolean", "null"}},
				},
				"required":             []interface{}{"file_path", "old_string", "new_string"},
				"additionalProperties": false,
			}},
			"Glob": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string"},
					"path":    map[string]interface{}{"type": []interface{}{"string", "null"}},
				},
				"required":             []interface{}{"pattern"},
				"additionalProperties": false,
			}},
			"Grep": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string"},
					"path":    map[string]interface{}{"type": []interface{}{"string", "null"}},
					"glob":    map[string]interface{}{"type": []interface{}{"string", "null"}},
				},
				"required":             []interface{}{"pattern"},
				"additionalProperties": false,
			}},
			"LS": {Schema: map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
				"required":             []interface{}{"path"},
				"additionalProperties": false,
			}},
			"Write": {Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string"},
					"content":   map[string]interface{}{"type": "string"},
				},
				"required":             []interface{}{"file_path", "content"},
				"additionalProperties": false,
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_bash", Name: "Bash", Input: map[string]interface{}{"cmd": "go test ./...", "timeout": "120"}})
	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_edit", Name: "Edit", Input: map[string]interface{}{"path": "/tmp/a.go", "old": "a", "new": "b", "replaceAll": "true"}})
	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_glob", Name: "Glob", Input: map[string]interface{}{"query": "*.go", "directory": "/www/Kiro-Go"}})
	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_grep", Name: "Grep", Input: map[string]interface{}{"regex": "func main", "directory": "/www/Kiro-Go", "include": "*.go"}})
	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_ls", Name: "LS", Input: map[string]interface{}{"dir": "/www/Kiro-Go"}})
	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_ls_empty", Name: "LS", Input: map[string]interface{}{}})
	callback.OnToolUse(KiroToolUse{ToolUseID: "toolu_write", Name: "Write", Input: map[string]interface{}{"path": "/tmp/out.txt", "text": "hello"}})

	if len(got) != 7 {
		t.Fatalf("expected all repaired Claude Code tool uses to pass, got %#v", got)
	}
	assertToolInputValue(t, got, "toolu_bash", "command", "go test ./...")
	assertToolInputValue(t, got, "toolu_bash", "timeout", 120)
	assertToolInputValue(t, got, "toolu_edit", "file_path", "/tmp/a.go")
	assertToolInputValue(t, got, "toolu_edit", "old_string", "a")
	assertToolInputValue(t, got, "toolu_edit", "new_string", "b")
	assertToolInputValue(t, got, "toolu_edit", "replace_all", true)
	assertToolInputValue(t, got, "toolu_glob", "pattern", "*.go")
	assertToolInputValue(t, got, "toolu_glob", "path", "/www/Kiro-Go")
	assertToolInputValue(t, got, "toolu_grep", "pattern", "func main")
	assertToolInputValue(t, got, "toolu_grep", "path", "/www/Kiro-Go")
	assertToolInputValue(t, got, "toolu_grep", "glob", "*.go")
	assertToolInputValue(t, got, "toolu_ls", "path", "/www/Kiro-Go")
	assertToolInputValue(t, got, "toolu_ls_empty", "path", ".")
	assertToolInputValue(t, got, "toolu_write", "file_path", "/tmp/out.txt")
	assertToolInputValue(t, got, "toolu_write", "content", "hello")
}

func TestWrapToolUseAllowsValidRequiredArgumentsAndRestoresName(t *testing.T) {
	payload := &KiroPayload{
		ToolNameMap: map[string]string{"readFile": "read_file"},
		ToolSchemas: map[string]toolSchemaSummary{
			"read_file": {Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
				"required":   []interface{}{"path"},
			}},
		},
	}
	var got []KiroToolUse
	callback := wrapKiroToolUseCallback(payload, &KiroStreamCallback{
		OnToolUse: func(tu KiroToolUse) {
			got = append(got, tu)
		},
	})

	callback.OnToolUse(KiroToolUse{
		ToolUseID: "toolu_1",
		Name:      "readFile",
		Input:     map[string]interface{}{"path": "README.md"},
	})

	if len(got) != 1 {
		t.Fatalf("expected valid tool_use to pass, got %#v", got)
	}
	if got[0].Name != "read_file" || got[0].Input["path"] != "README.md" {
		t.Fatalf("expected original name and input preserved, got %#v", got[0])
	}
}

func assertToolInputValue(t *testing.T, uses []KiroToolUse, id, key string, want interface{}) {
	t.Helper()
	for _, use := range uses {
		if use.ToolUseID != id {
			continue
		}
		if got := use.Input[key]; got != want {
			t.Fatalf("tool %s input[%s] = %#v, want %#v; full input=%#v", id, key, got, want, use.Input)
		}
		return
	}
	t.Fatalf("tool use %s not found in %#v", id, uses)
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invalid test URL: %v", err)
	}
	return parsed
}

func assertProxyURL(t *testing.T, got *url.URL, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("expected proxy URL %q, got nil", want)
	}
	if got.String() != want {
		t.Fatalf("expected proxy URL %q, got %q", want, got.String())
	}
}
