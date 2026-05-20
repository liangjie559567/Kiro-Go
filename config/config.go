// Package config provides configuration management for Kiro API Proxy.
//
// This package handles persistent storage and retrieval of:
//   - Account credentials and authentication tokens
//   - Server settings (port, host, API keys)
//   - Usage statistics and metrics
//   - Thinking mode configuration for AI responses
//
// All configuration is stored in a JSON file with thread-safe access
// via read-write mutex protection.
package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// GenerateMachineId generates a UUID v4 format machine identifier.
// This ID is used to uniquely identify the proxy instance in Kiro API requests,
// helping with request tracking and rate limiting on the server side.
func GenerateMachineId() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	bytes[6] = (bytes[6] & 0x0f) | 0x40 // 版本 4
	bytes[8] = (bytes[8] & 0x3f) | 0x80 // 变体
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// Account represents a Kiro API account with authentication credentials and usage statistics.
type Account struct {
	// Basic identification
	ID       string `json:"id"`                 // Unique account identifier (UUID)
	Email    string `json:"email,omitempty"`    // User email address
	UserId   string `json:"userId,omitempty"`   // Kiro user ID
	Nickname string `json:"nickname,omitempty"` // Display name for admin panel

	// Authentication credentials
	AccessToken  string `json:"accessToken"`            // OAuth access token for API calls
	RefreshToken string `json:"refreshToken"`           // OAuth refresh token for token renewal
	ClientID     string `json:"clientId,omitempty"`     // OIDC client ID (for IdC auth)
	ClientSecret string `json:"clientSecret,omitempty"` // OIDC client secret (for IdC auth)
	AuthMethod   string `json:"authMethod"`             // Authentication method: "idc" (AWS IdC) or "social" (GitHub/Google)
	Provider     string `json:"provider,omitempty"`     // Identity provider name (e.g., "BuilderId", "GitHub")
	Region       string `json:"region"`                 // AWS region for OIDC endpoints
	StartUrl     string `json:"startUrl,omitempty"`     // AWS SSO start URL
	ExpiresAt    int64  `json:"expiresAt,omitempty"`    // Token expiration timestamp (Unix seconds)
	MachineId    string `json:"machineId,omitempty"`    // UUID machine identifier for request tracking
	ProfileArn   string `json:"profileArn,omitempty"`   // CodeWhisperer/Kiro profile ARN for generation requests

	// Per-account outbound proxy (falls back to global ProxyURL if empty)
	ProxyURL string `json:"proxyURL,omitempty"`

	// Priority weight for load balancing (higher = more requests)
	Weight int `json:"weight,omitempty"` // 0 or 1 = normal, 2+ = higher priority

	// Overage behavior after the main usage limit is reached.
	AllowOverage  bool `json:"allowOverage,omitempty"`  // Whether to keep using the account after UsageLimit is reached
	OverageWeight int  `json:"overageWeight,omitempty"` // 1-10, lower values reduce overage request frequency

	// Account status
	Enabled   bool   `json:"enabled"`             // Whether account is active in the pool
	BanStatus string `json:"banStatus,omitempty"` // Ban status: "ACTIVE", "BANNED", "SUSPENDED"
	BanReason string `json:"banReason,omitempty"` // Reason for ban/suspension
	BanTime   int64  `json:"banTime,omitempty"`   // Timestamp when ban was detected
	// Request-level health state
	LastFailureReason string `json:"lastFailureReason,omitempty"` // Last request failure classification
	LastFailureAt     int64  `json:"lastFailureAt,omitempty"`     // Last request failure timestamp
	CooldownUntil     int64  `json:"cooldownUntil,omitempty"`     // Request routing cooldown end timestamp
	FailureCount      int    `json:"failureCount,omitempty"`      // Consecutive request failure count

	// Subscription information
	SubscriptionType  string `json:"subscriptionType,omitempty"`  // Tier: FREE, PRO, PRO_PLUS, or POWER
	SubscriptionTitle string `json:"subscriptionTitle,omitempty"` // Human-readable subscription name
	DaysRemaining     int    `json:"daysRemaining,omitempty"`     // Days until subscription expires

	// Usage tracking
	UsageCurrent  float64 `json:"usageCurrent,omitempty"`  // Current period usage (credits)
	UsageLimit    float64 `json:"usageLimit,omitempty"`    // Maximum allowed usage per period
	UsagePercent  float64 `json:"usagePercent,omitempty"`  // Usage percentage (0.0-1.0)
	NextResetDate string  `json:"nextResetDate,omitempty"` // Date when usage resets (YYYY-MM-DD)
	LastRefresh   int64   `json:"lastRefresh,omitempty"`   // Last info refresh timestamp

	// Trial usage tracking
	TrialUsageCurrent float64 `json:"trialUsageCurrent,omitempty"` // Trial quota current usage
	TrialUsageLimit   float64 `json:"trialUsageLimit,omitempty"`   // Trial quota total limit
	TrialUsagePercent float64 `json:"trialUsagePercent,omitempty"` // Trial quota usage percentage (0.0-1.0)
	TrialStatus       string  `json:"trialStatus,omitempty"`       // Trial status: ACTIVE, EXPIRED, NONE
	TrialExpiresAt    int64   `json:"trialExpiresAt,omitempty"`    // Trial expiration timestamp (Unix seconds)

	// Runtime statistics (updated during operation)
	RequestCount int     `json:"requestCount,omitempty"` // Total requests processed
	ErrorCount   int     `json:"errorCount,omitempty"`   // Total errors encountered
	LastUsed     int64   `json:"lastUsed,omitempty"`     // Last request timestamp
	TotalTokens  int     `json:"totalTokens,omitempty"`  // Cumulative tokens processed
	TotalCredits float64 `json:"totalCredits,omitempty"` // Cumulative credits consumed
}

const (
	AutoRefreshScopeEnabled = "enabled"
	AutoRefreshScopeAll     = "all"

	AutoRefreshMinIntervalMinutes     = 5
	AutoRefreshMaxIntervalMinutes     = 1440
	AutoRefreshDefaultIntervalMinutes = 60
)

type AutoRefreshConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"intervalMinutes"`
	Scope           string `json:"scope"`
}

const (
	HealthCheckMinIntervalMinutes     = 5
	HealthCheckMaxIntervalMinutes     = 1440
	HealthCheckDefaultIntervalMinutes = 60
)

type HealthCheckConfig struct {
	Enabled              bool `json:"enabled"`
	IntervalMinutes      int  `json:"intervalMinutes"`
	AutoDisableUnhealthy bool `json:"autoDisableUnhealthy"`
}

type Opus47AdmissionConfig struct {
	MaxConcurrent int `json:"maxConcurrent"`
	MaxWaiting    int `json:"maxWaiting"`
}

type ModelAdmissionRule struct {
	MaxConcurrent int `json:"maxConcurrent"`
	MaxWaiting    int `json:"maxWaiting"`
}

type ModelAdmissionConfig struct {
	StreamBypass bool                          `json:"streamBypass,omitempty"`
	Default      ModelAdmissionRule            `json:"default,omitempty"`
	Models       map[string]ModelAdmissionRule `json:"models,omitempty"`
}

type StableDownstreamConfig struct {
	Enabled           bool     `json:"enabled"`
	Sub2APICompatible bool     `json:"sub2apiCompatible"`
	Models            []string `json:"models"`
}

type ContentContinuityConfig struct {
	Enabled                bool     `json:"enabled"`
	Models                 []string `json:"models"`
	MaxQueueWaitSeconds    int      `json:"maxQueueWaitSeconds"`
	MaxQueueDepth          int      `json:"maxQueueDepth"`
	MinContentTokens       int      `json:"minContentTokens"`
	StreamHeartbeatSeconds int      `json:"streamHeartbeatSeconds"`
}

type ClaudeCodeGovernorConfig struct {
	Enabled                         bool     `json:"enabled"`
	Models                          []string `json:"models"`
	InteractiveReservedPerSession   int      `json:"interactiveReservedPerSession"`
	SubagentMaxConcurrentPerSession int      `json:"subagentMaxConcurrentPerSession"`
	BackgroundMaxConcurrent         int      `json:"backgroundMaxConcurrent"`
	QueueMaxDepth                   int      `json:"queueMaxDepth"`
	InteractiveMaxWaitSeconds       int      `json:"interactiveMaxWaitSeconds"`
	SubagentMaxWaitSeconds          int      `json:"subagentMaxWaitSeconds"`
	BackgroundMaxWaitSeconds        int      `json:"backgroundMaxWaitSeconds"`
}

func (c StableDownstreamConfig) SupportsModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if !c.Enabled || model == "" {
		return false
	}
	for _, candidate := range c.Models {
		if strings.ToLower(strings.TrimSpace(candidate)) == model {
			return true
		}
	}
	return false
}

func (c ContentContinuityConfig) SupportsModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if !c.Enabled || model == "" {
		return false
	}
	for _, candidate := range c.Models {
		if strings.ToLower(strings.TrimSpace(candidate)) == model {
			return true
		}
	}
	return false
}

const (
	LoadBalanceStrategyHealth           = "health"
	LoadBalanceStrategyRoundRobin       = "round_robin"
	LoadBalanceStrategyLeastConnections = "least_connections"
)

type LoadBalanceConfig struct {
	Strategy string `json:"strategy"`
}

type ModelMappingRule struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name,omitempty"`
	Enabled      bool     `json:"enabled"`
	Type         string   `json:"type,omitempty"`
	SourceModel  string   `json:"sourceModel"`
	TargetModels []string `json:"targetModels"`
	Weights      []int    `json:"weights,omitempty"`
}

type ClientAccessConfig struct {
	ApiKey            string             `json:"apiKey,omitempty"`
	RequireApiKey     bool               `json:"requireApiKey"`
	ClientApiKeys     []string           `json:"clientApiKeys,omitempty"`
	ClientIPAllowlist []string           `json:"clientIPAllowlist,omitempty"`
	ModelMappings     []ModelMappingRule `json:"modelMappings,omitempty"`
}

func defaultLoadBalanceConfig() LoadBalanceConfig {
	return LoadBalanceConfig{Strategy: LoadBalanceStrategyHealth}
}

func normalizeLoadBalanceConfig(in LoadBalanceConfig) LoadBalanceConfig {
	if in.Strategy == "" {
		return defaultLoadBalanceConfig()
	}
	return in
}

func ValidateLoadBalanceConfig(in LoadBalanceConfig) error {
	switch in.Strategy {
	case LoadBalanceStrategyHealth, LoadBalanceStrategyRoundRobin, LoadBalanceStrategyLeastConnections:
		return nil
	default:
		return fmt.Errorf("strategy must be %q, %q, or %q", LoadBalanceStrategyHealth, LoadBalanceStrategyRoundRobin, LoadBalanceStrategyLeastConnections)
	}
}

func defaultOpus47AdmissionConfig() Opus47AdmissionConfig {
	return Opus47AdmissionConfig{
		MaxConcurrent: 2,
		MaxWaiting:    200,
	}
}

func defaultModelAdmissionConfig() ModelAdmissionConfig {
	opus := defaultOpus47AdmissionConfig()
	return ModelAdmissionConfig{
		StreamBypass: false,
		Models: map[string]ModelAdmissionRule{
			"claude-opus-4.7": {
				MaxConcurrent: opus.MaxConcurrent,
				MaxWaiting:    opus.MaxWaiting,
			},
		},
	}
}

func normalizeOpus47AdmissionConfig(in Opus47AdmissionConfig) Opus47AdmissionConfig {
	defaults := defaultOpus47AdmissionConfig()
	if in.MaxConcurrent == 0 {
		in.MaxConcurrent = defaults.MaxConcurrent
	}
	if in.MaxWaiting == 0 {
		in.MaxWaiting = defaults.MaxWaiting
	}
	return in
}

func ValidateOpus47AdmissionConfig(in Opus47AdmissionConfig) error {
	if in.MaxConcurrent <= 0 {
		return fmt.Errorf("maxConcurrent must be greater than 0")
	}
	if in.MaxWaiting < 0 {
		return fmt.Errorf("maxWaiting must be greater than or equal to 0")
	}
	return nil
}

func normalizeModelAdmissionRuleForUpdate(in ModelAdmissionRule) ModelAdmissionRule {
	return in
}

func normalizeModelAdmissionConfig(in ModelAdmissionConfig, legacy Opus47AdmissionConfig) ModelAdmissionConfig {
	out := ModelAdmissionConfig{
		StreamBypass: in.StreamBypass,
		Default:      normalizeModelAdmissionRuleForUpdate(in.Default),
		Models:       make(map[string]ModelAdmissionRule),
	}
	for model, rule := range in.Models {
		model = strings.ToLower(strings.TrimSpace(model))
		if model == "" {
			continue
		}
		out.Models[model] = normalizeModelAdmissionRuleForUpdate(rule)
	}
	if _, ok := out.Models["claude-opus-4.7"]; !ok {
		legacy = normalizeOpus47AdmissionConfig(legacy)
		out.Models["claude-opus-4.7"] = ModelAdmissionRule{
			MaxConcurrent: legacy.MaxConcurrent,
			MaxWaiting:    legacy.MaxWaiting,
		}
	}
	return out
}

func ValidateModelAdmissionConfig(in ModelAdmissionConfig) error {
	if in.Default != (ModelAdmissionRule{}) {
		if err := ValidateModelAdmissionRule(in.Default); err != nil {
			return fmt.Errorf("default: %w", err)
		}
	}
	for model, rule := range in.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			return fmt.Errorf("model key must not be empty")
		}
		if err := ValidateModelAdmissionRule(rule); err != nil {
			return fmt.Errorf("%s: %w", model, err)
		}
	}
	return nil
}

func ValidateModelAdmissionRule(in ModelAdmissionRule) error {
	if in.MaxConcurrent <= 0 {
		return fmt.Errorf("maxConcurrent must be greater than 0")
	}
	if in.MaxWaiting < 0 {
		return fmt.Errorf("maxWaiting must be greater than or equal to 0")
	}
	return nil
}

// PromptFilterRule defines a single custom prompt sanitization rule.
// Type can be: "regex" (regexp find/replace within prompt) or
// "lines-containing" (remove lines containing the match substring).
type PromptFilterRule struct {
	ID      string `json:"id"`                // Unique rule identifier
	Name    string `json:"name"`              // Human-readable rule name
	Type    string `json:"type"`              // "regex" or "lines-containing"
	Match   string `json:"match"`             // Pattern to match (regex pattern or substring)
	Replace string `json:"replace,omitempty"` // Replacement string (only for regex; empty = delete match)
	Enabled bool   `json:"enabled"`           // Whether this rule is active
}

// Config represents the global application configuration.
type Config struct {
	// Server settings
	Password           string                   `json:"password"`         // Admin panel password
	Port               int                      `json:"port"`             // HTTP server port (default: 8080)
	Host               string                   `json:"host"`             // HTTP server bind address (default: 0.0.0.0)
	ApiKey             string                   `json:"apiKey,omitempty"` // API key for client authentication
	RequireApiKey      bool                     `json:"requireApiKey"`    // Whether to enforce API key validation
	ClientApiKeys      []string                 `json:"clientApiKeys,omitempty"`
	ClientIPAllowlist  []string                 `json:"clientIPAllowlist,omitempty"`
	ModelMappings      []ModelMappingRule       `json:"modelMappings,omitempty"`
	KiroVersion        string                   `json:"kiroVersion,omitempty"`
	SystemVersion      string                   `json:"systemVersion,omitempty"`
	NodeVersion        string                   `json:"nodeVersion,omitempty"`
	Accounts           []Account                `json:"accounts"` // Registered Kiro accounts
	AutoRefresh        AutoRefreshConfig        `json:"autoRefresh"`
	HealthCheck        HealthCheckConfig        `json:"healthCheck"`
	Opus47Admission    Opus47AdmissionConfig    `json:"opus47Admission,omitempty"`
	ModelAdmission     ModelAdmissionConfig     `json:"modelAdmission,omitempty"`
	StableDownstream   StableDownstreamConfig   `json:"stableDownstream"`
	ContentContinuity  ContentContinuityConfig  `json:"contentContinuity"`
	ClaudeCodeGovernor ClaudeCodeGovernorConfig `json:"claudeCodeGovernor,omitempty"`
	LoadBalance        LoadBalanceConfig        `json:"loadBalance,omitempty"`

	// Thinking mode configuration for extended reasoning output
	ThinkingSuffix       string `json:"thinkingSuffix,omitempty"`       // Model suffix to trigger thinking mode (default: "-thinking")
	OpenAIThinkingFormat string `json:"openaiThinkingFormat,omitempty"` // OpenAI output format: "reasoning_content", "thinking", or "think"
	ClaudeThinkingFormat string `json:"claudeThinkingFormat,omitempty"` // Claude output format: "reasoning_content", "thinking", or "think"

	// Endpoint configuration: "auto", "kiro", "codewhisperer", or "amazonq"
	PreferredEndpoint string `json:"preferredEndpoint,omitempty"`

	// EndpointFallback controls whether to try other endpoints when the preferred one fails.
	// Defaults to true. Set to false to only use the preferred endpoint.
	EndpointFallback *bool `json:"endpointFallback,omitempty"`

	// AllowOverUsage allows accounts to continue serving requests even when their
	// usage quota has been exhausted. When enabled, the pool will not skip accounts
	// solely because usageCurrent >= usageLimit.
	AllowOverUsage bool `json:"allowOverUsage,omitempty"`

	// Proxy configuration: optional outbound proxy for Kiro API requests
	// Format: "socks5://host:port", "socks5://user:pass@host:port",
	//         "http://host:port",  "http://user:pass@host:port"
	// Leave empty to connect directly.
	ProxyURL string `json:"proxyURL,omitempty"`

	// SanitizeClaudeCodePrompt is kept for backward-compatible JSON loading only.
	// Migrated to FilterClaudeCode on first load. Do not use directly.
	SanitizeClaudeCodePrompt bool `json:"sanitizeClaudeCodePrompt,omitempty"`

	// FilterClaudeCode detects the Claude Code CLI built-in system prompt and replaces it
	// with a compact backend-only prompt, reducing token usage significantly.
	FilterClaudeCode bool `json:"filterClaudeCode,omitempty"`

	// FilterEnvNoise strips environment metadata lines from system prompts:
	// git status, recent commits, environment sections, fast_mode_info tags, etc.
	FilterEnvNoise bool `json:"filterEnvNoise,omitempty"`

	// FilterStripBoundaries removes --- SYSTEM PROMPT --- / --- END SYSTEM PROMPT --- markers.
	FilterStripBoundaries bool `json:"filterStripBoundaries,omitempty"`

	// PromptFilterRules is a list of user-defined prompt sanitization rules (regex or line-filter).
	PromptFilterRules []PromptFilterRule `json:"promptFilterRules,omitempty"`

	// LogLevel controls verbosity of application logs.
	// Accepted values: "debug", "info", "warn", "error". Defaults to "info".
	// Can be overridden by the LOG_LEVEL environment variable.
	LogLevel string `json:"logLevel,omitempty"`

	// Global statistics (persisted across restarts)
	TotalRequests   int     `json:"totalRequests,omitempty"`   // Total API requests received
	SuccessRequests int     `json:"successRequests,omitempty"` // Successful requests count
	FailedRequests  int     `json:"failedRequests,omitempty"`  // Failed requests count
	TotalTokens     int     `json:"totalTokens,omitempty"`     // Total tokens processed
	TotalCredits    float64 `json:"totalCredits,omitempty"`    // Total credits consumed
}

// AccountInfo contains account metadata retrieved from Kiro API.
// Used for updating subscription and usage information.
type AccountInfo struct {
	Email             string
	UserId            string
	SubscriptionType  string
	SubscriptionTitle string
	DaysRemaining     int
	UsageCurrent      float64
	UsageLimit        float64
	UsagePercent      float64
	NextResetDate     string
	LastRefresh       int64
	TrialUsageCurrent float64
	TrialUsageLimit   float64
	TrialUsagePercent float64
	TrialStatus       string
	TrialExpiresAt    int64
}

// Version current version
const Version = "1.0.8"

var (
	cfg     *Config
	cfgLock sync.RWMutex
	cfgPath string
)

func defaultAutoRefreshConfig() AutoRefreshConfig {
	return AutoRefreshConfig{
		Enabled:         true,
		IntervalMinutes: AutoRefreshDefaultIntervalMinutes,
		Scope:           AutoRefreshScopeEnabled,
	}
}

func defaultStableDownstreamConfig() StableDownstreamConfig {
	return StableDownstreamConfig{
		Enabled:           true,
		Sub2APICompatible: true,
		Models:            []string{"claude-opus-4.7"},
	}
}

func defaultContentContinuityConfig() ContentContinuityConfig {
	return ContentContinuityConfig{
		Enabled:                true,
		Models:                 []string{"claude-opus-4.7"},
		MaxQueueWaitSeconds:    120,
		MaxQueueDepth:          300,
		MinContentTokens:       1,
		StreamHeartbeatSeconds: 10,
	}
}

func normalizeContentContinuityConfig(in ContentContinuityConfig) ContentContinuityConfig {
	defaults := defaultContentContinuityConfig()
	if len(in.Models) == 0 {
		in.Models = defaults.Models
	}
	if in.MaxQueueWaitSeconds <= 0 {
		in.MaxQueueWaitSeconds = defaults.MaxQueueWaitSeconds
	}
	if in.MaxQueueDepth <= 0 {
		in.MaxQueueDepth = defaults.MaxQueueDepth
	}
	if in.MinContentTokens <= 0 {
		in.MinContentTokens = defaults.MinContentTokens
	}
	if in.StreamHeartbeatSeconds <= 0 {
		in.StreamHeartbeatSeconds = defaults.StreamHeartbeatSeconds
	}
	return in
}

func defaultClaudeCodeGovernorConfig() ClaudeCodeGovernorConfig {
	return ClaudeCodeGovernorConfig{
		Enabled:                         false,
		Models:                          []string{"claude-opus-4.7"},
		InteractiveReservedPerSession:   1,
		SubagentMaxConcurrentPerSession: 2,
		BackgroundMaxConcurrent:         1,
		QueueMaxDepth:                   300,
		InteractiveMaxWaitSeconds:       120,
		SubagentMaxWaitSeconds:          90,
		BackgroundMaxWaitSeconds:        15,
	}
}

func normalizeClaudeCodeGovernorConfig(in ClaudeCodeGovernorConfig) ClaudeCodeGovernorConfig {
	defaults := defaultClaudeCodeGovernorConfig()
	if len(in.Models) == 0 {
		in.Models = defaults.Models
	}
	if in.InteractiveReservedPerSession == 0 {
		in.InteractiveReservedPerSession = defaults.InteractiveReservedPerSession
	}
	if in.SubagentMaxConcurrentPerSession == 0 {
		in.SubagentMaxConcurrentPerSession = defaults.SubagentMaxConcurrentPerSession
	}
	if in.BackgroundMaxConcurrent == 0 {
		in.BackgroundMaxConcurrent = defaults.BackgroundMaxConcurrent
	}
	if in.QueueMaxDepth == 0 {
		in.QueueMaxDepth = defaults.QueueMaxDepth
	}
	if in.InteractiveMaxWaitSeconds == 0 {
		in.InteractiveMaxWaitSeconds = defaults.InteractiveMaxWaitSeconds
	}
	if in.SubagentMaxWaitSeconds == 0 {
		in.SubagentMaxWaitSeconds = defaults.SubagentMaxWaitSeconds
	}
	if in.BackgroundMaxWaitSeconds == 0 {
		in.BackgroundMaxWaitSeconds = defaults.BackgroundMaxWaitSeconds
	}
	return in
}

func ValidateClaudeCodeGovernorConfig(in ClaudeCodeGovernorConfig) error {
	if in.InteractiveReservedPerSession < 0 {
		return fmt.Errorf("interactiveReservedPerSession must be greater than or equal to 0")
	}
	if in.SubagentMaxConcurrentPerSession < 0 {
		return fmt.Errorf("subagentMaxConcurrentPerSession must be greater than or equal to 0")
	}
	if in.BackgroundMaxConcurrent < 0 {
		return fmt.Errorf("backgroundMaxConcurrent must be greater than or equal to 0")
	}
	if in.QueueMaxDepth < 0 {
		return fmt.Errorf("queueMaxDepth must be greater than or equal to 0")
	}
	if in.InteractiveMaxWaitSeconds < 0 {
		return fmt.Errorf("interactiveMaxWaitSeconds must be greater than or equal to 0")
	}
	if in.SubagentMaxWaitSeconds < 0 {
		return fmt.Errorf("subagentMaxWaitSeconds must be greater than or equal to 0")
	}
	if in.BackgroundMaxWaitSeconds < 0 {
		return fmt.Errorf("backgroundMaxWaitSeconds must be greater than or equal to 0")
	}
	for _, model := range in.Models {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("model must not be empty")
		}
	}
	return nil
}

type persistedContentContinuityConfig struct {
	Enabled                *bool    `json:"enabled"`
	Models                 []string `json:"models"`
	MaxQueueWaitSeconds    int      `json:"maxQueueWaitSeconds"`
	MaxQueueDepth          int      `json:"maxQueueDepth"`
	MinContentTokens       int      `json:"minContentTokens"`
	StreamHeartbeatSeconds int      `json:"streamHeartbeatSeconds"`
}

func normalizePersistedContentContinuityConfig(data []byte, in ContentContinuityConfig) ContentContinuityConfig {
	defaults := defaultContentContinuityConfig()
	var raw struct {
		ContentContinuity *persistedContentContinuityConfig `json:"contentContinuity"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.ContentContinuity == nil {
		return defaults
	}

	normalized := normalizeContentContinuityConfig(in)
	if raw.ContentContinuity.Enabled == nil {
		normalized.Enabled = defaults.Enabled
	} else {
		normalized.Enabled = *raw.ContentContinuity.Enabled
	}
	return normalized
}

type persistedStableDownstreamConfig struct {
	Enabled           *bool    `json:"enabled"`
	Sub2APICompatible *bool    `json:"sub2apiCompatible"`
	Models            []string `json:"models"`
}

func normalizePersistedStableDownstreamConfig(data []byte, in StableDownstreamConfig) StableDownstreamConfig {
	defaults := defaultStableDownstreamConfig()
	var raw struct {
		StableDownstream *persistedStableDownstreamConfig `json:"stableDownstream"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.StableDownstream == nil {
		return defaults
	}

	normalized := in
	if raw.StableDownstream.Enabled == nil {
		normalized.Enabled = defaults.Enabled
	} else {
		normalized.Enabled = *raw.StableDownstream.Enabled
	}
	if raw.StableDownstream.Sub2APICompatible == nil {
		normalized.Sub2APICompatible = defaults.Sub2APICompatible
	} else {
		normalized.Sub2APICompatible = *raw.StableDownstream.Sub2APICompatible
	}
	if raw.StableDownstream.Models == nil {
		normalized.Models = defaults.Models
	}
	return normalized
}

func defaultConfig() Config {
	return Config{
		Password:           "changeme",
		Port:               8080,
		Host:               "0.0.0.0",
		RequireApiKey:      false,
		Accounts:           []Account{},
		AutoRefresh:        defaultAutoRefreshConfig(),
		HealthCheck:        defaultHealthCheckConfig(),
		Opus47Admission:    defaultOpus47AdmissionConfig(),
		ModelAdmission:     defaultModelAdmissionConfig(),
		StableDownstream:   defaultStableDownstreamConfig(),
		ContentContinuity:  defaultContentContinuityConfig(),
		ClaudeCodeGovernor: defaultClaudeCodeGovernorConfig(),
		LoadBalance:        defaultLoadBalanceConfig(),
	}
}

func normalizeAutoRefreshConfig(in AutoRefreshConfig) AutoRefreshConfig {
	defaults := defaultAutoRefreshConfig()
	if in == (AutoRefreshConfig{}) {
		return defaults
	}
	return normalizeAutoRefreshConfigForUpdate(in)
}

func normalizeAutoRefreshConfigForUpdate(in AutoRefreshConfig) AutoRefreshConfig {
	defaults := defaultAutoRefreshConfig()
	if in.IntervalMinutes == 0 {
		in.IntervalMinutes = defaults.IntervalMinutes
	}
	if in.Scope == "" {
		in.Scope = defaults.Scope
	}
	return in
}

func ValidateAutoRefreshConfig(in AutoRefreshConfig) error {
	if in.IntervalMinutes < AutoRefreshMinIntervalMinutes || in.IntervalMinutes > AutoRefreshMaxIntervalMinutes {
		return fmt.Errorf("intervalMinutes must be between %d and %d", AutoRefreshMinIntervalMinutes, AutoRefreshMaxIntervalMinutes)
	}
	if in.Scope != AutoRefreshScopeEnabled && in.Scope != AutoRefreshScopeAll {
		return fmt.Errorf("scope must be %q or %q", AutoRefreshScopeEnabled, AutoRefreshScopeAll)
	}
	return nil
}

type persistedAutoRefreshConfig struct {
	Enabled         *bool  `json:"enabled"`
	IntervalMinutes int    `json:"intervalMinutes"`
	Scope           string `json:"scope"`
}

func normalizePersistedAutoRefreshConfig(data []byte, in AutoRefreshConfig) AutoRefreshConfig {
	var raw struct {
		AutoRefresh *persistedAutoRefreshConfig `json:"autoRefresh"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.AutoRefresh == nil {
		return normalizeAutoRefreshConfig(in)
	}

	normalized := normalizeAutoRefreshConfigForUpdate(in)
	if raw.AutoRefresh.Enabled == nil {
		normalized.Enabled = defaultAutoRefreshConfig().Enabled
		return normalized
	}
	normalized.Enabled = *raw.AutoRefresh.Enabled
	return normalized
}

func defaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Enabled:              false,
		IntervalMinutes:      HealthCheckDefaultIntervalMinutes,
		AutoDisableUnhealthy: false,
	}
}

func normalizeHealthCheckConfig(in HealthCheckConfig) HealthCheckConfig {
	defaults := defaultHealthCheckConfig()
	if in == (HealthCheckConfig{}) {
		return defaults
	}
	return normalizeHealthCheckConfigForUpdate(in)
}

func normalizeHealthCheckConfigForUpdate(in HealthCheckConfig) HealthCheckConfig {
	defaults := defaultHealthCheckConfig()
	if in.IntervalMinutes == 0 {
		in.IntervalMinutes = defaults.IntervalMinutes
	}
	return in
}

func ValidateHealthCheckConfig(in HealthCheckConfig) error {
	if in.IntervalMinutes < HealthCheckMinIntervalMinutes || in.IntervalMinutes > HealthCheckMaxIntervalMinutes {
		return fmt.Errorf("intervalMinutes must be between %d and %d", HealthCheckMinIntervalMinutes, HealthCheckMaxIntervalMinutes)
	}
	return nil
}

type persistedHealthCheckConfig struct {
	Enabled              *bool `json:"enabled"`
	IntervalMinutes      int   `json:"intervalMinutes"`
	AutoDisableUnhealthy *bool `json:"autoDisableUnhealthy"`
}

func normalizePersistedHealthCheckConfig(data []byte, in HealthCheckConfig) HealthCheckConfig {
	var raw struct {
		HealthCheck *persistedHealthCheckConfig `json:"healthCheck"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.HealthCheck == nil {
		return normalizeHealthCheckConfig(in)
	}

	normalized := normalizeHealthCheckConfigForUpdate(in)
	if raw.HealthCheck.Enabled == nil {
		normalized.Enabled = defaultHealthCheckConfig().Enabled
	} else {
		normalized.Enabled = *raw.HealthCheck.Enabled
	}
	if raw.HealthCheck.AutoDisableUnhealthy == nil {
		normalized.AutoDisableUnhealthy = defaultHealthCheckConfig().AutoDisableUnhealthy
	} else {
		normalized.AutoDisableUnhealthy = *raw.HealthCheck.AutoDisableUnhealthy
	}
	return normalized
}

// Init initializes the configuration system with the specified file path.
// If the file doesn't exist, a default configuration is created.
func Init(path string) error {
	cfgPath = path
	return Load()
}

func Load() error {
	cfgLock.Lock()
	defer cfgLock.Unlock()

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create default configuration.
			// Binds to 0.0.0.0 by default for Docker/container compatibility.
			defaults := defaultConfig()
			cfg = &defaults
			return Save()
		}
		return err
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	c.AutoRefresh = normalizePersistedAutoRefreshConfig(data, c.AutoRefresh)
	c.HealthCheck = normalizePersistedHealthCheckConfig(data, c.HealthCheck)
	c.Opus47Admission = normalizeOpus47AdmissionConfig(c.Opus47Admission)
	c.ModelAdmission = normalizeModelAdmissionConfig(c.ModelAdmission, c.Opus47Admission)
	c.StableDownstream = normalizePersistedStableDownstreamConfig(data, c.StableDownstream)
	c.ContentContinuity = normalizePersistedContentContinuityConfig(data, c.ContentContinuity)
	c.ClaudeCodeGovernor = normalizeClaudeCodeGovernorConfig(c.ClaudeCodeGovernor)
	c.LoadBalance = normalizeLoadBalanceConfig(c.LoadBalance)
	cfg = &c
	return nil
}

// Save persists the current configuration to the JSON file.
// Uses indented formatting for human readability.
func Save() error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, data, 0600)
}

// SetPassword updates the admin password.
// Primarily used for environment variable override in containerized deployments.
func SetPassword(password string) {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.Password = password
}

func Get() *Config {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg
}

func GetPassword() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg.Password
}

func GetPort() int {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg.Port == 0 {
		return 8080
	}
	return cfg.Port
}

func GetHost() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg.Host == "" {
		return "127.0.0.1"
	}
	return cfg.Host
}

func GetAccounts() []Account {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	accounts := make([]Account, len(cfg.Accounts))
	copy(accounts, cfg.Accounts)
	return accounts
}

func GetAutoRefreshConfig() AutoRefreshConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return normalizeAutoRefreshConfig(cfg.AutoRefresh)
}

func UpdateAutoRefreshConfig(autoRefresh AutoRefreshConfig) error {
	normalized := normalizeAutoRefreshConfigForUpdate(autoRefresh)
	if err := ValidateAutoRefreshConfig(normalized); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.AutoRefresh = normalized
	return Save()
}

func GetHealthCheckConfig() HealthCheckConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return normalizeHealthCheckConfig(cfg.HealthCheck)
}

func GetOpus47AdmissionConfig() Opus47AdmissionConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return defaultOpus47AdmissionConfig()
	}
	return normalizeOpus47AdmissionConfig(cfg.Opus47Admission)
}

func GetModelAdmissionConfig() ModelAdmissionConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return defaultModelAdmissionConfig()
	}
	return normalizeModelAdmissionConfig(cfg.ModelAdmission, cfg.Opus47Admission)
}

func GetClaudeCodeGovernorConfig() ClaudeCodeGovernorConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return defaultClaudeCodeGovernorConfig()
	}
	return normalizeClaudeCodeGovernorConfig(cfg.ClaudeCodeGovernor)
}

func UpdateOpus47AdmissionConfig(admission Opus47AdmissionConfig) error {
	normalized := normalizeOpus47AdmissionConfig(admission)
	if err := ValidateOpus47AdmissionConfig(normalized); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.Opus47Admission = normalized
	cfg.ModelAdmission = normalizeModelAdmissionConfig(cfg.ModelAdmission, normalized)
	cfg.ModelAdmission.Models["claude-opus-4.7"] = ModelAdmissionRule{
		MaxConcurrent: normalized.MaxConcurrent,
		MaxWaiting:    normalized.MaxWaiting,
	}
	return Save()
}

func UpdateModelAdmissionConfig(admission ModelAdmissionConfig) error {
	if err := ValidateModelAdmissionConfig(admission); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.ModelAdmission = normalizeModelAdmissionConfig(admission, cfg.Opus47Admission)
	return Save()
}

func GetLoadBalanceConfig() LoadBalanceConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return defaultLoadBalanceConfig()
	}
	return normalizeLoadBalanceConfig(cfg.LoadBalance)
}

func UpdateLoadBalanceConfig(loadBalance LoadBalanceConfig) error {
	normalized := normalizeLoadBalanceConfig(loadBalance)
	if err := ValidateLoadBalanceConfig(normalized); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.LoadBalance = normalized
	return Save()
}

func UpdateHealthCheckConfig(healthCheck HealthCheckConfig) error {
	normalized := normalizeHealthCheckConfigForUpdate(healthCheck)
	if err := ValidateHealthCheckConfig(normalized); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.HealthCheck = normalized
	return Save()
}

func GetEnabledAccounts() []Account {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	var accounts []Account
	for _, a := range cfg.Accounts {
		if a.Enabled {
			accounts = append(accounts, a)
		}
	}
	return accounts
}

func AddAccount(account Account) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.Accounts = append(cfg.Accounts, account)
	return Save()
}

func UpdateAccount(id string, account Account) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			cfg.Accounts[i] = account
			return Save()
		}
	}
	return nil
}

// DisableAccountOverage turns off AllowOverage for a specific account.
func DisableAccountOverage(id string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			cfg.Accounts[i].AllowOverage = false
			return Save()
		}
	}
	return nil
}

func UpdateAccountProfileArn(id, profileArn string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			cfg.Accounts[i].ProfileArn = profileArn
			return Save()
		}
	}
	return nil
}

func UpdateAccountHealth(id string, lastFailureReason string, lastFailureAt, cooldownUntil int64, failureCount int) error {
	if cfg == nil {
		return nil
	}
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			cfg.Accounts[i].LastFailureReason = lastFailureReason
			cfg.Accounts[i].LastFailureAt = lastFailureAt
			cfg.Accounts[i].CooldownUntil = cooldownUntil
			cfg.Accounts[i].FailureCount = failureCount
			return Save()
		}
	}
	return nil
}

func ClearAccountHealth(id string) error {
	return UpdateAccountHealth(id, "", 0, 0, 0)
}

func DeleteAccount(id string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			cfg.Accounts = append(cfg.Accounts[:i], cfg.Accounts[i+1:]...)
			return Save()
		}
	}
	return nil
}

func UpdateAccountToken(id, accessToken, refreshToken string, expiresAt int64) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			cfg.Accounts[i].AccessToken = accessToken
			if refreshToken != "" {
				cfg.Accounts[i].RefreshToken = refreshToken
			}
			cfg.Accounts[i].ExpiresAt = expiresAt
			return Save()
		}
	}
	return nil
}

func GetApiKey() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg.ApiKey
}

func GetClientApiKeys() []string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return effectiveClientApiKeysLocked()
}

func effectiveClientApiKeysLocked() []string {
	keys := make([]string, 0, 1+len(cfg.ClientApiKeys))
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || strings.HasPrefix(key, "#disabled#") {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	add(cfg.ApiKey)
	for _, key := range cfg.ClientApiKeys {
		add(key)
	}
	return keys
}

func UpdateClientAccessConfig(in ClientAccessConfig) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.ApiKey = strings.TrimSpace(in.ApiKey)
	cfg.RequireApiKey = in.RequireApiKey
	cfg.ClientApiKeys = normalizeStringList(in.ClientApiKeys)
	cfg.ClientIPAllowlist = normalizeStringList(in.ClientIPAllowlist)
	cfg.ModelMappings = normalizeModelMappings(in.ModelMappings)
	return Save()
}

func GetClientAccessConfig() ClientAccessConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return ClientAccessConfig{
		ApiKey:            cfg.ApiKey,
		RequireApiKey:     cfg.RequireApiKey,
		ClientApiKeys:     append([]string(nil), cfg.ClientApiKeys...),
		ClientIPAllowlist: append([]string(nil), cfg.ClientIPAllowlist...),
		ModelMappings:     append([]ModelMappingRule(nil), cfg.ModelMappings...),
	}
}

func IsApiKeyRequired() bool {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg.RequireApiKey
}

func UpdateSettings(apiKey string, requireApiKey bool, password string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.ApiKey = strings.TrimSpace(apiKey)
	cfg.RequireApiKey = requireApiKey
	if password != "" {
		cfg.Password = password
	}
	return Save()
}

func IsClientIPAllowed(remoteAddr string) bool {
	cfgLock.RLock()
	allowlist := append([]string(nil), cfg.ClientIPAllowlist...)
	cfgLock.RUnlock()
	if len(allowlist) == 0 {
		return true
	}
	ipText := strings.TrimSpace(remoteAddr)
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		ipText = host
	}
	ip := net.ParseIP(strings.Trim(ipText, "[]"))
	if ip == nil {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if allowedIP := net.ParseIP(strings.Trim(entry, "[]")); allowedIP != nil {
			if allowedIP.Equal(ip) {
				return true
			}
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

var modelMappingCursor uint64

func ResolveModelMapping(requestedModel string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return requestedModel
	}
	cfgLock.RLock()
	if cfg == nil {
		cfgLock.RUnlock()
		return requestedModel
	}
	rules := append([]ModelMappingRule(nil), cfg.ModelMappings...)
	cfgLock.RUnlock()
	for _, rule := range rules {
		if !rule.Enabled || strings.TrimSpace(rule.SourceModel) != requestedModel || len(rule.TargetModels) == 0 {
			continue
		}
		ruleType := strings.ToLower(strings.TrimSpace(rule.Type))
		if ruleType == "" || ruleType == "alias" || ruleType == "replace" {
			return strings.TrimSpace(rule.TargetModels[0])
		}
		if ruleType == "loadbalance" {
			return chooseWeightedModel(rule.TargetModels, rule.Weights)
		}
	}
	return requestedModel
}

func chooseWeightedModel(targets []string, weights []int) string {
	normalizedTargets := normalizeStringList(targets)
	if len(normalizedTargets) == 0 {
		return ""
	}
	if len(weights) != len(targets) {
		idx := atomic.AddUint64(&modelMappingCursor, 1) - 1
		return normalizedTargets[int(idx%uint64(len(normalizedTargets)))]
	}
	total := 0
	for _, weight := range weights {
		if weight > 0 {
			total += weight
		}
	}
	if total <= 0 {
		return normalizedTargets[0]
	}
	tick := int((atomic.AddUint64(&modelMappingCursor, 1) - 1) % uint64(total))
	cumulative := 0
	for i, target := range targets {
		weight := weights[i]
		if weight <= 0 {
			continue
		}
		cumulative += weight
		if tick < cumulative {
			return strings.TrimSpace(target)
		}
	}
	return normalizedTargets[0]
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		duplicate := false
		for _, existing := range out {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, value)
		}
	}
	return out
}

func normalizeModelMappings(rules []ModelMappingRule) []ModelMappingRule {
	out := make([]ModelMappingRule, 0, len(rules))
	for _, rule := range rules {
		rule.SourceModel = strings.TrimSpace(rule.SourceModel)
		rule.TargetModels = normalizeStringList(rule.TargetModels)
		rule.Type = strings.ToLower(strings.TrimSpace(rule.Type))
		if rule.SourceModel == "" || len(rule.TargetModels) == 0 {
			continue
		}
		out = append(out, rule)
	}
	return out
}

func UpdateStats(totalReq, successReq, failedReq, totalTokens int, totalCredits float64) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.TotalRequests = totalReq
	cfg.SuccessRequests = successReq
	cfg.FailedRequests = failedReq
	cfg.TotalTokens = totalTokens
	cfg.TotalCredits = totalCredits
	return Save()
}

func GetStats() (int, int, int, int, float64) {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg.TotalRequests, cfg.SuccessRequests, cfg.FailedRequests, cfg.TotalTokens, cfg.TotalCredits
}

func UpdateAccountStats(id string, requestCount, errorCount, totalTokens int, totalCredits float64, lastUsed int64) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			if requestCount < a.RequestCount {
				return nil
			}
			cfg.Accounts[i].RequestCount = requestCount
			cfg.Accounts[i].ErrorCount = errorCount
			cfg.Accounts[i].TotalTokens = totalTokens
			cfg.Accounts[i].TotalCredits = totalCredits
			cfg.Accounts[i].LastUsed = lastUsed
			return Save()
		}
	}
	return nil
}

// UpdateAccountInfo updates an account's subscription and usage information.
// Called after refreshing account data from Kiro API.
func UpdateAccountInfo(id string, info AccountInfo) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i, a := range cfg.Accounts {
		if a.ID == id {
			if info.Email != "" {
				cfg.Accounts[i].Email = info.Email
			}
			if info.UserId != "" {
				cfg.Accounts[i].UserId = info.UserId
			}
			cfg.Accounts[i].SubscriptionType = info.SubscriptionType
			cfg.Accounts[i].SubscriptionTitle = info.SubscriptionTitle
			cfg.Accounts[i].DaysRemaining = info.DaysRemaining
			cfg.Accounts[i].UsageCurrent = info.UsageCurrent
			cfg.Accounts[i].UsageLimit = info.UsageLimit
			cfg.Accounts[i].UsagePercent = info.UsagePercent
			cfg.Accounts[i].NextResetDate = info.NextResetDate
			cfg.Accounts[i].LastRefresh = info.LastRefresh
			cfg.Accounts[i].TrialUsageCurrent = info.TrialUsageCurrent
			cfg.Accounts[i].TrialUsageLimit = info.TrialUsageLimit
			cfg.Accounts[i].TrialUsagePercent = info.TrialUsagePercent
			cfg.Accounts[i].TrialStatus = info.TrialStatus
			cfg.Accounts[i].TrialExpiresAt = info.TrialExpiresAt
			return Save()
		}
	}
	return nil
}

// GetFilterClaudeCode returns whether Claude Code system prompt detection is enabled.
// Also checks the legacy SanitizeClaudeCodePrompt flag for backward compatibility.
func GetFilterClaudeCode() bool {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return false
	}
	return cfg.FilterClaudeCode || cfg.SanitizeClaudeCodePrompt
}

// GetFilterEnvNoise returns whether environment noise line stripping is enabled.
func GetFilterEnvNoise() bool {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return false
	}
	return cfg.FilterEnvNoise
}

// GetFilterStripBoundaries returns whether boundary marker stripping is enabled.
func GetFilterStripBoundaries() bool {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return false
	}
	return cfg.FilterStripBoundaries
}

// PromptFilterConfig holds all prompt filter settings for API responses.
type PromptFilterConfig struct {
	FilterClaudeCode      bool               `json:"filterClaudeCode"`
	FilterEnvNoise        bool               `json:"filterEnvNoise"`
	FilterStripBoundaries bool               `json:"filterStripBoundaries"`
	Rules                 []PromptFilterRule `json:"rules"`
}

// GetPromptFilterConfig returns all prompt filter settings.
func GetPromptFilterConfig() PromptFilterConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return PromptFilterConfig{Rules: []PromptFilterRule{}}
	}
	rules := make([]PromptFilterRule, len(cfg.PromptFilterRules))
	copy(rules, cfg.PromptFilterRules)
	return PromptFilterConfig{
		FilterClaudeCode:      cfg.FilterClaudeCode || cfg.SanitizeClaudeCodePrompt,
		FilterEnvNoise:        cfg.FilterEnvNoise,
		FilterStripBoundaries: cfg.FilterStripBoundaries,
		Rules:                 rules,
	}
}

// UpdatePromptFilterConfig saves all prompt filter settings atomically.
func UpdatePromptFilterConfig(filterClaudeCode, filterEnvNoise, filterStripBoundaries bool, rules []PromptFilterRule) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.FilterClaudeCode = filterClaudeCode
	cfg.FilterEnvNoise = filterEnvNoise
	cfg.FilterStripBoundaries = filterStripBoundaries
	// Clear legacy flag to avoid double-applying after first save
	cfg.SanitizeClaudeCodePrompt = false
	if rules != nil {
		cfg.PromptFilterRules = rules
	}
	return Save()
}

// GetPromptFilterRules returns the current prompt filter rules.
func GetPromptFilterRules() []PromptFilterRule {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return nil
	}
	rules := make([]PromptFilterRule, len(cfg.PromptFilterRules))
	copy(rules, cfg.PromptFilterRules)
	return rules
}

// ThinkingConfig holds settings for AI thinking/reasoning mode.
// When enabled, models output their reasoning process alongside the response.
type ThinkingConfig struct {
	Suffix       string `json:"suffix"`       // Model name suffix that triggers thinking mode
	OpenAIFormat string `json:"openaiFormat"` // Output format for OpenAI-compatible responses
	ClaudeFormat string `json:"claudeFormat"` // Output format for Claude-compatible responses
}

// GetThinkingConfig 获取 thinking 配置
func GetThinkingConfig() ThinkingConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()

	suffix := cfg.ThinkingSuffix
	if suffix == "" {
		suffix = "-thinking"
	}
	openaiFormat := cfg.OpenAIThinkingFormat
	if openaiFormat == "" {
		openaiFormat = "reasoning_content"
	}
	claudeFormat := cfg.ClaudeThinkingFormat
	if claudeFormat == "" {
		claudeFormat = "thinking"
	}

	return ThinkingConfig{
		Suffix:       suffix,
		OpenAIFormat: openaiFormat,
		ClaudeFormat: claudeFormat,
	}
}

// UpdateThinkingConfig 更新 thinking 配置
func UpdateThinkingConfig(suffix, openaiFormat, claudeFormat string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.ThinkingSuffix = suffix
	cfg.OpenAIThinkingFormat = openaiFormat
	cfg.ClaudeThinkingFormat = claudeFormat
	return Save()
}

// GetPreferredEndpoint 获取首选端点配置
func GetPreferredEndpoint() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg.PreferredEndpoint == "" {
		return "auto"
	}
	return cfg.PreferredEndpoint
}

// UpdatePreferredEndpoint 更新首选端点配置
func UpdatePreferredEndpoint(endpoint string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.PreferredEndpoint = endpoint
	return Save()
}

// GetEndpointFallback returns whether endpoint fallback is enabled. Defaults to true.
func GetEndpointFallback() bool {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg.EndpointFallback == nil {
		return true
	}
	return *cfg.EndpointFallback
}

// UpdateEndpointFallback sets the endpoint fallback switch and persists the change.
func UpdateEndpointFallback(enabled bool) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.EndpointFallback = &enabled
	return Save()
}

// GetProxyURL 获取出站代理地址
func GetProxyURL() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg.ProxyURL
}

// UpdateProxySettings 更新出站代理配置
func UpdateProxySettings(proxyURL string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.ProxyURL = proxyURL
	return Save()
}

// GetAllowOverUsage returns whether over-usage is allowed when account quota is exhausted.
func GetAllowOverUsage() bool {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return false
	}
	return cfg.AllowOverUsage
}

// UpdateAllowOverUsage sets the over-usage setting and persists the change.
func UpdateAllowOverUsage(allow bool) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.AllowOverUsage = allow
	return Save()
}

// GetLogLevel returns the configured log level (debug/info/warn/error). Defaults to "info".
func GetLogLevel() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil || cfg.LogLevel == "" {
		return "info"
	}
	return cfg.LogLevel
}

// UpdateLogLevel updates the log level setting and persists the change.
func UpdateLogLevel(level string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.LogLevel = level
	return Save()
}

type KiroClientConfig struct {
	KiroVersion   string
	SystemVersion string
	NodeVersion   string
}

func GetKiroClientConfig() KiroClientConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()

	kiroVersion := "0.11.107"
	if cfg != nil && cfg.KiroVersion != "" {
		kiroVersion = cfg.KiroVersion
	}

	systemVersion := ""
	if cfg != nil {
		systemVersion = cfg.SystemVersion
	}
	if systemVersion == "" {
		systemVersion = defaultSystemVersion()
	}

	nodeVersion := "22.22.0"
	if cfg != nil && cfg.NodeVersion != "" {
		nodeVersion = cfg.NodeVersion
	}

	return KiroClientConfig{
		KiroVersion:   kiroVersion,
		SystemVersion: systemVersion,
		NodeVersion:   nodeVersion,
	}
}

func defaultSystemVersion() string {
	switch runtime.GOOS {
	case "windows":
		return "win32#10.0.22631"
	case "darwin":
		return "darwin#24.6.0"
	default:
		return "linux#6.6.87"
	}
}
