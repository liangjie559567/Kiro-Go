package proxy

import (
	"kiro-go/pool"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUpstreamErrorParsesStructuredEvidenceAndRetryAfterMS(t *testing.T) {
	now := time.Unix(1000, 0)
	headers := make(http.Header)
	headers.Set("retry-after-ms", "1250")
	headers.Set("x-amzn-requestid", "req-123")
	body := `{"error":{"type":"rate_limit_error","code":"MODEL_RATE_LIMIT","message":"model throttled","reason":"MODEL_RATE_LIMIT"}}`

	err := newUpstreamHTTPError(http.StatusTooManyRequests, "Kiro IDE", headers, body, now)

	if err.StatusCode != http.StatusTooManyRequests || err.Source != "Kiro IDE" {
		t.Fatalf("unexpected status/source: %#v", err)
	}
	if err.Type != "rate_limit_error" || err.Code != "MODEL_RATE_LIMIT" || err.Reason != "MODEL_RATE_LIMIT" || err.Message != "model throttled" {
		t.Fatalf("did not parse JSON error fields: %#v", err)
	}
	if err.RequestID != "req-123" {
		t.Fatalf("request id = %q", err.RequestID)
	}
	if got := err.RetryAfterReset.Sub(now); got != 1250*time.Millisecond {
		t.Fatalf("retry-after-ms parsed as %s", got)
	}
	if got := err.FailureReason(); got != pool.FailureReasonRateLimited {
		t.Fatalf("reason = %v want %v", got, pool.FailureReasonRateLimited)
	}
}

func TestUpstreamErrorReasonMappingKeepsAccountAndModelLimitsDistinct(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   pool.FailureReason
	}{
		{
			name:   "temporary account limit",
			status: http.StatusTooManyRequests,
			body:   `{"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`,
			want:   pool.FailureReasonTemporaryLimited,
		},
		{
			name:   "model capacity",
			status: http.StatusTooManyRequests,
			body:   `{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`,
			want:   pool.FailureReasonModelCapacity,
		},
		{
			name:   "generic rate limit",
			status: http.StatusTooManyRequests,
			body:   `{"message":"rate limited","reason":"RATE_LIMIT"}`,
			want:   pool.FailureReasonRateLimited,
		},
		{
			name:   "quota",
			status: http.StatusForbidden,
			body:   `{"message":"monthly quota exhausted","code":"QUOTA_EXHAUSTED"}`,
			want:   pool.FailureReasonQuotaExhausted,
		},
		{
			name:   "auth",
			status: http.StatusUnauthorized,
			body:   `{"message":"expired token","code":"UNAUTHORIZED"}`,
			want:   pool.FailureReasonAuthExpired,
		},
		{
			name:   "suspended",
			status: http.StatusForbidden,
			body:   `{"message":"account banned","code":"TEMPORARILY_SUSPENDED"}`,
			want:   pool.FailureReasonSuspended,
		},
		{
			name:   "upstream 5xx",
			status: http.StatusServiceUnavailable,
			body:   `{"message":"service unavailable"}`,
			want:   pool.FailureReasonUpstream5xx,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := newUpstreamHTTPError(tc.status, "Kiro IDE", nil, tc.body, time.Now())
			if got := err.FailureReason(); got != tc.want {
				t.Fatalf("got %v want %v; err=%#v", got, tc.want, err)
			}
		})
	}
}

func TestUpstreamErrorRedactsSecretSummary(t *testing.T) {
	awsKey := "AKIA" + "1234567890ABCDEF"
	err := newUpstreamHTTPError(http.StatusForbidden, "Kiro IDE", nil, `{"message":"Authorization=Bearer sk-secret api_key=abc123 `+awsKey+`"}`, time.Now())

	for _, forbidden := range []string{"sk-secret", "abc123", awsKey} {
		if strings.Contains(err.Summary, forbidden) {
			t.Fatalf("summary leaked %q: %q", forbidden, err.Summary)
		}
	}
	if !strings.Contains(err.Summary, "[redacted]") {
		t.Fatalf("expected redaction marker in %q", err.Summary)
	}
}
