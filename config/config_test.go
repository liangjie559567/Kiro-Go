package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultAutoRefreshConfig(t *testing.T) {
	got := defaultAutoRefreshConfig()
	if !got.Enabled {
		t.Fatalf("expected auto refresh enabled by default")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected default interval 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected default scope %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestNormalizeAutoRefreshConfigAppliesDefaults(t *testing.T) {
	got := normalizeAutoRefreshConfig(AutoRefreshConfig{})
	if !got.Enabled {
		t.Fatalf("expected zero config to default to enabled")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected scope default %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestNormalizePersistedAutoRefreshConfigPreservesExplicitDisabled(t *testing.T) {
	data := []byte(`{"autoRefresh":{"enabled":false}}`)
	got := normalizePersistedAutoRefreshConfig(data, AutoRefreshConfig{})
	if got.Enabled {
		t.Fatalf("expected explicit disabled config to be preserved")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected scope default %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestNormalizePersistedAutoRefreshConfigDefaultsAbsentEnabled(t *testing.T) {
	data := []byte(`{"autoRefresh":{"intervalMinutes":60,"scope":"enabled"}}`)
	got := normalizePersistedAutoRefreshConfig(data, AutoRefreshConfig{
		IntervalMinutes: 60,
		Scope:           AutoRefreshScopeEnabled,
	})
	if !got.Enabled {
		t.Fatalf("expected absent enabled field to default true")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected scope %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestNormalizeAutoRefreshConfigForUpdatePreservesSparseDisabled(t *testing.T) {
	got := normalizeAutoRefreshConfigForUpdate(AutoRefreshConfig{Enabled: false})
	if got.Enabled {
		t.Fatalf("expected sparse disabled update to stay disabled")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected scope default %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestValidateAutoRefreshConfig(t *testing.T) {
	valid := AutoRefreshConfig{Enabled: true, IntervalMinutes: 5, Scope: AutoRefreshScopeAll}
	if err := ValidateAutoRefreshConfig(valid); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	tooSmall := AutoRefreshConfig{Enabled: true, IntervalMinutes: 4, Scope: AutoRefreshScopeEnabled}
	if err := ValidateAutoRefreshConfig(tooSmall); err == nil {
		t.Fatalf("expected interval below minimum to fail")
	}

	tooLarge := AutoRefreshConfig{Enabled: true, IntervalMinutes: 1441, Scope: AutoRefreshScopeEnabled}
	if err := ValidateAutoRefreshConfig(tooLarge); err == nil {
		t.Fatalf("expected interval above maximum to fail")
	}

	badScope := AutoRefreshConfig{Enabled: true, IntervalMinutes: 60, Scope: "disabled"}
	if err := ValidateAutoRefreshConfig(badScope); err == nil {
		t.Fatalf("expected invalid scope to fail")
	}
}

func TestDefaultHealthCheckConfig(t *testing.T) {
	got := defaultHealthCheckConfig()
	if got.Enabled {
		t.Fatalf("expected health check disabled by default")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected default interval 60, got %d", got.IntervalMinutes)
	}
	if got.AutoDisableUnhealthy {
		t.Fatalf("expected auto-disable disabled by default")
	}
}

func TestNormalizePersistedHealthCheckConfigPreservesExplicitDisabled(t *testing.T) {
	data := []byte(`{"healthCheck":{"enabled":false,"autoDisableUnhealthy":true}}`)
	got := normalizePersistedHealthCheckConfig(data, HealthCheckConfig{
		AutoDisableUnhealthy: true,
	})
	if got.Enabled {
		t.Fatalf("expected explicit disabled config to be preserved")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if !got.AutoDisableUnhealthy {
		t.Fatalf("expected explicit auto-disable to be preserved")
	}
}

func TestNormalizePersistedHealthCheckConfigDefaultsAbsentEnabled(t *testing.T) {
	data := []byte(`{"healthCheck":{"intervalMinutes":60}}`)
	got := normalizePersistedHealthCheckConfig(data, HealthCheckConfig{IntervalMinutes: 60})
	if got.Enabled {
		t.Fatalf("expected absent enabled field to default false")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval 60, got %d", got.IntervalMinutes)
	}
	if got.AutoDisableUnhealthy {
		t.Fatalf("expected absent auto-disable to default false")
	}
}

func TestNormalizeHealthCheckConfigForUpdatePreservesSparseDisabled(t *testing.T) {
	got := normalizeHealthCheckConfigForUpdate(HealthCheckConfig{Enabled: false, AutoDisableUnhealthy: true})
	if got.Enabled {
		t.Fatalf("expected sparse disabled update to stay disabled")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if !got.AutoDisableUnhealthy {
		t.Fatalf("expected auto-disable to stay enabled")
	}
}

func TestValidateHealthCheckConfig(t *testing.T) {
	valid := HealthCheckConfig{Enabled: true, IntervalMinutes: 5, AutoDisableUnhealthy: true}
	if err := ValidateHealthCheckConfig(valid); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	tooSmall := HealthCheckConfig{Enabled: true, IntervalMinutes: 4}
	if err := ValidateHealthCheckConfig(tooSmall); err == nil {
		t.Fatalf("expected interval below minimum to fail")
	}

	tooLarge := HealthCheckConfig{Enabled: true, IntervalMinutes: 1441}
	if err := ValidateHealthCheckConfig(tooLarge); err == nil {
		t.Fatalf("expected interval above maximum to fail")
	}
}

func TestGetOpus47AdmissionConfigDefaultsAndPersistsValues(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	defaults := GetOpus47AdmissionConfig()
	if defaults.MaxConcurrent != 2 {
		t.Fatalf("expected default max concurrent 2, got %d", defaults.MaxConcurrent)
	}
	if defaults.MaxWaiting != 200 {
		t.Fatalf("expected default max waiting 200, got %d", defaults.MaxWaiting)
	}

	if err := UpdateOpus47AdmissionConfig(Opus47AdmissionConfig{MaxConcurrent: 10, MaxWaiting: 300}); err != nil {
		t.Fatalf("update opus admission config: %v", err)
	}
	got := GetOpus47AdmissionConfig()
	if got.MaxConcurrent != 10 || got.MaxWaiting != 300 {
		t.Fatalf("expected persisted opus admission config 10/300, got %#v", got)
	}
}

func TestValidateOpus47AdmissionConfig(t *testing.T) {
	if err := ValidateOpus47AdmissionConfig(Opus47AdmissionConfig{MaxConcurrent: 10, MaxWaiting: 0}); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if err := ValidateOpus47AdmissionConfig(Opus47AdmissionConfig{MaxConcurrent: 0, MaxWaiting: 0}); err == nil {
		t.Fatalf("expected zero max concurrent to fail")
	}
	if err := ValidateOpus47AdmissionConfig(Opus47AdmissionConfig{MaxConcurrent: 1, MaxWaiting: -1}); err == nil {
		t.Fatalf("expected negative max waiting to fail")
	}
}

func TestGetLoadBalanceConfigDefaultsAndPersistsStrategy(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	defaults := GetLoadBalanceConfig()
	if defaults.Strategy != LoadBalanceStrategyHealth {
		t.Fatalf("expected default strategy %q, got %q", LoadBalanceStrategyHealth, defaults.Strategy)
	}

	if err := UpdateLoadBalanceConfig(LoadBalanceConfig{Strategy: LoadBalanceStrategyLeastConnections}); err != nil {
		t.Fatalf("update load balance config: %v", err)
	}
	got := GetLoadBalanceConfig()
	if got.Strategy != LoadBalanceStrategyLeastConnections {
		t.Fatalf("expected persisted least_connections, got %#v", got)
	}
}

func TestValidateLoadBalanceConfig(t *testing.T) {
	for _, strategy := range []string{LoadBalanceStrategyHealth, LoadBalanceStrategyRoundRobin, LoadBalanceStrategyLeastConnections} {
		if err := ValidateLoadBalanceConfig(LoadBalanceConfig{Strategy: strategy}); err != nil {
			t.Fatalf("expected strategy %q to be valid: %v", strategy, err)
		}
	}
	if err := ValidateLoadBalanceConfig(LoadBalanceConfig{Strategy: "quota_magic"}); err == nil {
		t.Fatalf("expected invalid strategy to fail")
	}
}

func TestGetClientApiKeysMergesLegacyKeyAndSkipsDisabled(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfgLock.Lock()
	cfg.ApiKey = " sk-legacy "
	cfg.ClientApiKeys = []string{"sk-secondary", "#disabled#sk-disabled", "sk-secondary", " "}
	cfgLock.Unlock()

	got := GetClientApiKeys()
	want := []string{"sk-legacy", "sk-secondary"}
	if len(got) != len(want) {
		t.Fatalf("expected %d keys, got %#v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected key %d to be %q, got %#v", i, want[i], got)
		}
	}
}

func TestClientIPAllowlistDefaultsOpenAndMatchesCIDR(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if !IsClientIPAllowed("203.0.113.10:4321") {
		t.Fatalf("expected empty allowlist to preserve existing open behavior")
	}

	cfgLock.Lock()
	cfg.ClientIPAllowlist = []string{"127.0.0.1", "10.8.0.0/16"}
	cfgLock.Unlock()

	if !IsClientIPAllowed("10.8.1.2:1234") {
		t.Fatalf("expected CIDR allowlist to match client IP")
	}
	if IsClientIPAllowed("10.9.1.2:1234") {
		t.Fatalf("expected non-matching client IP to be rejected")
	}
}

func TestResolveModelMappingSupportsAliasAndWeightedRoundRobin(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfgLock.Lock()
	cfg.ModelMappings = []ModelMappingRule{
		{ID: "alias", Enabled: true, Type: "alias", SourceModel: "my-opus", TargetModels: []string{"claude-opus-4.7"}},
		{ID: "lb", Enabled: true, Type: "loadbalance", SourceModel: "my-balanced", TargetModels: []string{"a", "b"}, Weights: []int{1, 2}},
	}
	cfgLock.Unlock()

	if got := ResolveModelMapping("my-opus"); got != "claude-opus-4.7" {
		t.Fatalf("expected alias mapping, got %q", got)
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		seen[ResolveModelMapping("my-balanced")] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("expected weighted mapping to rotate through both targets, got %#v", seen)
	}
	if got := ResolveModelMapping("claude-sonnet-4.6"); got != "claude-sonnet-4.6" {
		t.Fatalf("expected unmapped model to stay unchanged, got %q", got)
	}
}

func TestResolveModelMappingWithoutInitializedConfigDoesNotPanic(t *testing.T) {
	oldCfg := cfg
	cfg = nil
	defer func() {
		cfg = oldCfg
	}()

	if got := ResolveModelMapping("claude-opus-4-7"); got != "claude-opus-4-7" {
		t.Fatalf("expected model to pass through without initialized config, got %q", got)
	}
}

func TestUpdateAndClearAccountHealth(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfgLock.Lock()
	cfg.Accounts = []Account{{ID: "acct-1"}}
	cfgLock.Unlock()

	if err := UpdateAccountHealth("acct-1", "quota_exhausted", 123, 456, 2); err != nil {
		t.Fatalf("update health: %v", err)
	}
	got := GetAccounts()[0]
	if got.LastFailureReason != "quota_exhausted" || got.LastFailureAt != 123 || got.CooldownUntil != 456 || got.FailureCount != 2 {
		t.Fatalf("unexpected health state: %#v", got)
	}

	if err := ClearAccountHealth("acct-1"); err != nil {
		t.Fatalf("clear health: %v", err)
	}
	got = GetAccounts()[0]
	if got.LastFailureReason != "" || got.LastFailureAt != 0 || got.CooldownUntil != 0 || got.FailureCount != 0 {
		t.Fatalf("expected cleared health state, got %#v", got)
	}
}
