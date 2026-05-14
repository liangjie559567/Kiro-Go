package config

import "testing"

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
