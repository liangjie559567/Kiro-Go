package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeKiroCLIRunner struct {
	result kiroCLIDiagnosticCommandResult
	wait   bool
	calls  []fakeKiroCLICall
}

type fakeKiroCLICall struct {
	Path string
	Args []string
	Env  []string
}

func (f *fakeKiroCLIRunner) Run(ctx context.Context, path string, args []string, env []string, limit int) kiroCLIDiagnosticCommandResult {
	f.calls = append(f.calls, fakeKiroCLICall{Path: path, Args: append([]string(nil), args...), Env: append([]string(nil), env...)})
	if f.wait {
		<-ctx.Done()
		return kiroCLIDiagnosticCommandResult{Status: "timeout", Reason: "command_timeout", ExitStatus: -1, StartedAt: time.Now(), FinishedAt: time.Now(), Redacted: true}
	}
	return f.result
}

func setFakeKiroCLIRunner(t *testing.T, runner kiroCLICommandRunner) {
	t.Helper()
	kiroCLIRunnerMu.Lock()
	old := kiroCLIRunner
	kiroCLIRunner = runner
	kiroCLIRunnerMu.Unlock()
	t.Cleanup(func() {
		kiroCLIRunnerMu.Lock()
		kiroCLIRunner = old
		kiroCLIRunnerMu.Unlock()
	})
}

func makeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kiro-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}

func TestAllowedKiroCLICommandRejectsUnsafeActions(t *testing.T) {
	for _, command := range []string{"login", "logout", "update", "settings set", "token refresh", "credential import", "router set"} {
		if _, ok := allowedKiroCLICommand(command); ok {
			t.Fatalf("command %q should not be allowlisted", command)
		}
	}
	for _, command := range []string{"version", "whoami", "doctor", "diagnostic", "chat --list-models", "settings list"} {
		if _, ok := allowedKiroCLICommand(command); !ok {
			t.Fatalf("command %q should be allowlisted", command)
		}
	}
}

func TestRedactCLIDiagnosticOutput(t *testing.T) {
	awsKey := "AKIA" + "1234567890ABCDEF"
	got := redactCLIDiagnosticOutput("Authorization: Bearer sk-secret\napi_key=abc123\n" + awsKey)
	for _, forbidden := range []string{"sk-secret", "abc123", awsKey} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted output leaked %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected redaction marker in %q", got)
	}
}

func TestKiroCLIDiagnosticsRouteReportsMissingCLI(t *testing.T) {
	t.Setenv("KIRO_CLI_PATH", filepath.Join(t.TempDir(), "missing-kiro-cli"))
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/kiro-cli/diagnostics", nil)
	w := httptest.NewRecorder()

	h.apiGetKiroCLIDiagnostics(w, req)

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["available"] != false || body["readOnly"] != true {
		t.Fatalf("unexpected diagnostics body: %#v", body)
	}
}

func TestKiroCLIDiagnosticRunRedactsOutputAndAudits(t *testing.T) {
	path := makeExecutable(t)
	t.Setenv("KIRO_CLI_PATH", path)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "do-not-pass")
	runner := &fakeKiroCLIRunner{result: kiroCLIDiagnosticCommandResult{
		Status:     "ok",
		ExitStatus: 0,
		Output:     `{"models":["claude-opus-4.7"],"access_token":"REDACTED"}`,
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
	}}
	setFakeKiroCLIRunner(t, runner)
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/kiro-cli/diagnostics", strings.NewReader(`{"command":"chat --list-models"}`))
	w := httptest.NewRecorder()

	h.apiRunKiroCLIDiagnostic(w, req)

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	result := body["result"].(map[string]interface{})
	if result["status"] != "ok" || result["redacted"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(result["output"].(string), "sk-secret") {
		t.Fatalf("output was not redacted: %#v", result)
	}
	modelState := body["opus47ModelState"].(map[string]interface{})
	if modelState["state"] != "present" {
		t.Fatalf("expected opus 4.7 present, got %#v", modelState)
	}
	audit := body["audit"].([]interface{})
	if len(audit) != 1 || audit[0].(map[string]interface{})["command"] != "chat --list-models" {
		t.Fatalf("expected audit entry, got %#v", audit)
	}
	if len(runner.calls) != 1 || runner.calls[0].Path != path {
		t.Fatalf("runner calls = %#v", runner.calls)
	}
	for _, env := range runner.calls[0].Env {
		if strings.Contains(env, "AWS_SECRET_ACCESS_KEY") {
			t.Fatalf("secret env was passed to CLI: %q", env)
		}
	}
}

func TestKiroCLIDiagnosticRunReportsTimeout(t *testing.T) {
	path := makeExecutable(t)
	t.Setenv("KIRO_CLI_PATH", path)
	runner := &fakeKiroCLIRunner{wait: true}
	setFakeKiroCLIRunner(t, runner)
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/kiro-cli/diagnostics", strings.NewReader(`{"command":"doctor"}`))
	w := httptest.NewRecorder()

	h.apiRunKiroCLIDiagnostic(w, req)

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	result := body["result"].(map[string]interface{})
	if result["status"] != "timeout" || result["reason"] != "command_timeout" {
		t.Fatalf("expected timeout result, got %#v", result)
	}
}

func TestKiroCLIDiagnosticRunReportsNonZeroExit(t *testing.T) {
	path := makeExecutable(t)
	t.Setenv("KIRO_CLI_PATH", path)
	runner := &fakeKiroCLIRunner{result: kiroCLIDiagnosticCommandResult{
		Status:      "failed",
		Reason:      "command_failed",
		ExitStatus:  2,
		ErrorOutput: "doctor failed",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
	}}
	setFakeKiroCLIRunner(t, runner)
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/kiro-cli/diagnostics", strings.NewReader(`{"command":"doctor"}`))
	w := httptest.NewRecorder()

	h.apiRunKiroCLIDiagnostic(w, req)

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	result := body["result"].(map[string]interface{})
	if result["status"] != "failed" || result["reason"] != "command_failed" || result["exitStatus"] != float64(2) {
		t.Fatalf("expected failed result, got %#v", result)
	}
}

func TestKiroCLIDiagnosticRunRejectsUnsafeCommand(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/kiro-cli/diagnostics", strings.NewReader(`{"command":"login"}`))
	w := httptest.NewRecorder()

	h.apiRunKiroCLIDiagnostic(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", w.Code)
	}
}
