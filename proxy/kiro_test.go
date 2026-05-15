package proxy

import (
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
	t.Setenv("HTTPS_PROXY", "http://env-proxy.local:2323")
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
