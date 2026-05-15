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
