package proxy

import (
	"encoding/json"
	"fmt"
	"kiro-go/pool"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxUpstreamErrorSummaryBytes = 500

type UpstreamError struct {
	StatusCode       int
	Source           string
	Name             string
	Code             string
	Reason           string
	Message          string
	Type             string
	AWSCode          string
	RequestID        string
	RetryAfter       time.Duration
	RetryAfterReset  time.Time
	RetryAfterSource string
	Summary          string
}

func newUpstreamHTTPError(status int, source string, headers http.Header, body string, now time.Time) *UpstreamError {
	err := &UpstreamError{
		StatusCode: status,
		Source:     strings.TrimSpace(source),
	}
	err.parseHeaders(headers, now)
	err.parseBody(body, now)
	if raw := strings.TrimSpace(body); raw != "" {
		err.Summary = redactedUpstreamErrorSummary(raw)
	} else {
		err.Summary = redactedUpstreamErrorSummary(firstNonEmpty(err.Message, err.Reason, err.Code, err.AWSCode))
	}
	return err
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Summary != "" {
		return fmt.Sprintf("HTTP %d from %s: %s", e.StatusCode, e.Source, e.Summary)
	}
	return fmt.Sprintf("HTTP %d from %s", e.StatusCode, e.Source)
}

func (e *UpstreamError) RateLimitResetAt() time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.RetryAfterReset
}

func (e *UpstreamError) FailureReason() pool.FailureReason {
	if e == nil {
		return pool.FailureReasonUnknown
	}
	haystack := strings.ToLower(strings.Join([]string{e.Name, e.Code, e.Reason, e.Message, e.Type, e.AWSCode, e.Summary}, " "))
	switch {
	case strings.Contains(haystack, "quota") || strings.Contains(haystack, "monthly"):
		return pool.FailureReasonQuotaExhausted
	case strings.Contains(haystack, "temporarily_suspended") || strings.Contains(haystack, "suspended") || strings.Contains(haystack, "banned"):
		return pool.FailureReasonSuspended
	case strings.Contains(haystack, "suspicious activity") && strings.Contains(haystack, "temporary limits"):
		return pool.FailureReasonTemporaryLimited
	case strings.Contains(haystack, "insufficient_model_capacity") || strings.Contains(haystack, "experiencing high traffic") || strings.Contains(haystack, "model capacity"):
		return pool.FailureReasonModelCapacity
	case e.StatusCode == http.StatusTooManyRequests || strings.Contains(haystack, "rate limit") || strings.Contains(haystack, "too many requests") || strings.Contains(haystack, "throttl"):
		return pool.FailureReasonRateLimited
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden || strings.Contains(haystack, "expired") || strings.Contains(haystack, "invalid token") || strings.Contains(haystack, "unauthorized") || strings.Contains(haystack, "forbidden"):
		return pool.FailureReasonAuthExpired
	case e.StatusCode >= 500 && e.StatusCode <= 599:
		return pool.FailureReasonUpstream5xx
	default:
		return pool.FailureReasonUnknown
	}
}

func (e *UpstreamError) parseHeaders(headers http.Header, now time.Time) {
	if headers == nil {
		return
	}
	for _, name := range []string{"x-amzn-requestid", "x-amzn-request-id", "x-request-id", "request-id"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			e.RequestID = value
			break
		}
	}
	if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
		if resetAt, ok := parseRateLimitTime(retryAfter, now); ok {
			e.RetryAfterReset = resetAt
			e.RetryAfter = nonNegativeDuration(resetAt.Sub(now))
			e.RetryAfterSource = "retry-after"
		}
	}
	if retryAfterMS := strings.TrimSpace(headers.Get("retry-after-ms")); retryAfterMS != "" {
		if ms, err := strconv.ParseFloat(retryAfterMS, 64); err == nil {
			if ms < 0 {
				ms = 0
			}
			d := time.Duration(math.Ceil(ms)) * time.Millisecond
			e.RetryAfter = d
			e.RetryAfterReset = now.Add(d)
			e.RetryAfterSource = "retry-after-ms"
		}
	}
}

func (e *UpstreamError) parseBody(body string, now time.Time) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return
	}
	if nested, ok := raw["error"].(map[string]any); ok {
		raw = mergeStringAny(raw, nested)
	}
	e.Name = firstNonEmpty(valueString(raw["name"]), valueString(raw["Name"]))
	e.Code = firstNonEmpty(valueString(raw["code"]), valueString(raw["Code"]))
	e.Reason = firstNonEmpty(valueString(raw["reason"]), valueString(raw["Reason"]))
	e.Message = firstNonEmpty(valueString(raw["message"]), valueString(raw["Message"]))
	e.Type = firstNonEmpty(valueString(raw["type"]), valueString(raw["Type"]))
	e.AWSCode = awsCodeFromBody(raw)
	if e.RequestID == "" {
		e.RequestID = firstNonEmpty(valueString(raw["requestId"]), valueString(raw["requestID"]), valueString(raw["RequestId"]), valueString(raw["RequestID"]))
	}
	if e.RetryAfterReset.IsZero() {
		if resetAt, ok := rateLimitResetAtFromBody(body, now); ok {
			e.RetryAfterReset = resetAt
			e.RetryAfter = nonNegativeDuration(resetAt.Sub(now))
			e.RetryAfterSource = "body"
		}
	}
	if ms, ok := numberLike(raw["retry-after-ms"]); ok {
		d := time.Duration(math.Ceil(ms)) * time.Millisecond
		e.RetryAfter = d
		e.RetryAfterReset = now.Add(d)
		e.RetryAfterSource = "body_retry_after_ms"
	} else if ms, ok := numberLike(raw["retry_after_ms"]); ok {
		d := time.Duration(math.Ceil(ms)) * time.Millisecond
		e.RetryAfter = d
		e.RetryAfterReset = now.Add(d)
		e.RetryAfterSource = "body_retry_after_ms"
	}
}

func mergeStringAny(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func valueString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func numberLike(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		if v < 0 {
			return 0, true
		}
		return v, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		if parsed < 0 {
			parsed = 0
		}
		return parsed, true
	default:
		return 0, false
	}
}

func awsCodeFromBody(raw map[string]any) string {
	for _, key := range []string{"__type", "code", "Code", "type", "Type"} {
		value := valueString(raw[key])
		if value == "" {
			continue
		}
		if idx := strings.LastIndex(value, "#"); idx >= 0 && idx+1 < len(value) {
			return strings.TrimSpace(value[idx+1:])
		}
		return value
	}
	return ""
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

var upstreamSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/\-=]+`),
	regexp.MustCompile(`(?i)("?(authorization|cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|secret)"?\s*[:=]\s*)"?[^,\s}"']+"?`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

func redactedUpstreamErrorSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	summary = upstreamSecretPatterns[0].ReplaceAllString(summary, "Bearer [redacted]")
	summary = upstreamSecretPatterns[1].ReplaceAllString(summary, "$1[redacted]")
	summary = upstreamSecretPatterns[2].ReplaceAllString(summary, "[redacted-aws-key]")
	if len(summary) > maxUpstreamErrorSummaryBytes {
		summary = summary[:maxUpstreamErrorSummaryBytes]
	}
	return summary
}
