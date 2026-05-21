package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"kiro-go/logger"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultKiroCLIDiagnosticTimeout = 5 * time.Second
	kiroCLIDiagnosticOutputLimit    = 16 * 1024
	maxKiroCLIDiagnosticAudit       = 20
)

type kiroCLICommandRunner interface {
	Run(ctx context.Context, path string, args []string, env []string, limit int) kiroCLIDiagnosticCommandResult
}

var (
	kiroCLIRunnerMu sync.RWMutex
	kiroCLIRunner   kiroCLICommandRunner = osExecKiroCLICommandRunner{}
)

type kiroCLIDiagnosticCommandResult struct {
	Command     string        `json:"command"`
	Args        []string      `json:"args,omitempty"`
	Status      string        `json:"status"`
	Reason      string        `json:"reason,omitempty"`
	ExitStatus  int           `json:"exitStatus"`
	DurationMs  int64         `json:"durationMs"`
	Output      string        `json:"output,omitempty"`
	ErrorOutput string        `json:"errorOutput,omitempty"`
	Redacted    bool          `json:"redacted"`
	StartedAt   time.Time     `json:"startedAt"`
	FinishedAt  time.Time     `json:"finishedAt"`
	Timeout     time.Duration `json:"-"`
}

type kiroCLIDiagnosticAuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Action     string    `json:"action"`
	Command    string    `json:"command"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Redacted   bool      `json:"redacted"`
	DurationMs int64     `json:"durationMs"`
}

type kiroCLICommandSpec struct {
	Name string
	Args []string
}

type osExecKiroCLICommandRunner struct{}

func (osExecKiroCLICommandRunner) Run(ctx context.Context, path string, args []string, env []string, limit int) kiroCLIDiagnosticCommandResult {
	started := time.Now()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	var stdout, stderr limitedBuffer
	stdout.limit = limit
	stderr.limit = limit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	finished := time.Now()
	result := kiroCLIDiagnosticCommandResult{
		Status:      "ok",
		ExitStatus:  0,
		DurationMs:  finished.Sub(started).Milliseconds(),
		Output:      stdout.String(),
		ErrorOutput: stderr.String(),
		StartedAt:   started,
		FinishedAt:  finished,
	}
	if ctx.Err() != nil {
		result.Status = "timeout"
		result.Reason = "command_timeout"
		result.ExitStatus = -1
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Reason = "command_failed"
		result.ExitStatus = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitStatus = exitErr.ExitCode()
		}
	}
	return result
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func resolveKiroCLIPath() (string, bool, string) {
	if path := strings.TrimSpace(os.Getenv("KIRO_CLI_PATH")); path != "" {
		if executableFile(path) {
			return path, true, "env"
		}
		return path, false, "env_not_executable"
	}
	for _, name := range []string{"kiro-cli", "kiro"} {
		if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
			return path, true, name
		}
	}
	return "kiro-cli", false, "not_found"
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func kiroCLIHomeSummary() map[string]interface{} {
	home := strings.TrimSpace(os.Getenv("KIRO_CLI_HOME"))
	out := map[string]interface{}{
		"configured": home != "",
		"present":    false,
	}
	if home == "" {
		return out
	}
	if info, err := os.Stat(home); err == nil && info.IsDir() {
		out["present"] = true
	}
	return out
}

func allowedKiroCLICommand(name string) (kiroCLICommandSpec, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(name, "_", " ")))
	switch normalized {
	case "version", "--version":
		return kiroCLICommandSpec{Name: "version", Args: []string{"--version"}}, true
	case "whoami":
		return kiroCLICommandSpec{Name: "whoami", Args: []string{"whoami"}}, true
	case "doctor":
		return kiroCLICommandSpec{Name: "doctor", Args: []string{"doctor"}}, true
	case "diagnostic", "diagnostics":
		return kiroCLICommandSpec{Name: "diagnostic", Args: []string{"diagnostic"}}, true
	case "chat --list-models", "chat list models", "chat-list-models":
		return kiroCLICommandSpec{Name: "chat --list-models", Args: []string{"chat", "--list-models"}}, true
	case "settings list", "settings-list":
		return kiroCLICommandSpec{Name: "settings list", Args: []string{"settings", "list"}}, true
	default:
		return kiroCLICommandSpec{}, false
	}
}

func runKiroCLIDiagnosticCommand(ctx context.Context, path string, spec kiroCLICommandSpec) kiroCLIDiagnosticCommandResult {
	kiroCLIRunnerMu.RLock()
	runner := kiroCLIRunner
	kiroCLIRunnerMu.RUnlock()
	env := safeKiroCLIEnv()
	started := time.Now()
	result := runner.Run(ctx, path, spec.Args, env, kiroCLIDiagnosticOutputLimit)
	if result.StartedAt.IsZero() {
		result.StartedAt = started
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now()
	}
	result.Command = spec.Name
	result.Args = append([]string(nil), spec.Args...)
	result.Output = redactCLIDiagnosticOutput(result.Output)
	result.ErrorOutput = redactCLIDiagnosticOutput(result.ErrorOutput)
	result.Redacted = true
	if result.DurationMs == 0 {
		result.DurationMs = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	}
	return result
}

func safeKiroCLIEnv() []string {
	env := []string{}
	for _, item := range os.Environ() {
		key := item
		if idx := strings.Index(item, "="); idx >= 0 {
			key = item[:idx]
		}
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "key") || strings.Contains(lower, "cookie") {
			continue
		}
		env = append(env, item)
	}
	if home := strings.TrimSpace(os.Getenv("KIRO_CLI_HOME")); home != "" {
		env = append(env, "KIRO_CLI_HOME="+home)
	}
	return env
}

var cliDiagnosticSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/\-=]+`),
	regexp.MustCompile(`(?i)("?(authorization|cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|secret)"?\s*[:=]\s*)"?[^,\s}"']+"?`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

func redactCLIDiagnosticOutput(s string) string {
	s = strings.TrimSpace(s)
	s = cliDiagnosticSecretPatterns[0].ReplaceAllString(s, "Bearer [redacted]")
	s = cliDiagnosticSecretPatterns[1].ReplaceAllString(s, "$1[redacted]")
	s = cliDiagnosticSecretPatterns[2].ReplaceAllString(s, "[redacted-aws-key]")
	if len(s) > kiroCLIDiagnosticOutputLimit {
		s = s[:kiroCLIDiagnosticOutputLimit]
	}
	return s
}

func opus47CLIModelState(result *kiroCLIDiagnosticCommandResult) map[string]interface{} {
	state := "unknown"
	source := ""
	evidence := ""
	if result != nil {
		source = result.Command
		evidence = result.Output
		if result.Status != "ok" {
			state = "unknown"
		} else if strings.Contains(strings.ToLower(result.Output), "claude-opus-4.7") || strings.Contains(strings.ToLower(result.Output), "claude-opus-4-7") {
			state = "present"
		} else if strings.TrimSpace(result.Output) != "" {
			state = "unavailable"
		}
	}
	if len(evidence) > 300 {
		evidence = evidence[:300]
	}
	return map[string]interface{}{
		"state":     state,
		"source":    source,
		"timestamp": time.Now(),
		"evidence":  evidence,
	}
}

func commandRouterState(latest *kiroCLIDiagnosticCommandResult) string {
	if latest == nil || latest.Command != "settings list" || latest.Status != "ok" {
		return "unknown"
	}
	text := strings.ToLower(latest.Output)
	if strings.Contains(text, "router") && (strings.Contains(text, "enabled") || strings.Contains(text, "on")) {
		return "enabled"
	}
	if strings.Contains(text, "router") && (strings.Contains(text, "disabled") || strings.Contains(text, "off")) {
		return "disabled"
	}
	return "unknown"
}

func (h *Handler) appendKiroCLIDiagnosticAudit(entry kiroCLIDiagnosticAuditEntry) {
	if h == nil {
		return
	}
	h.cliDiagnosticsMu.Lock()
	defer h.cliDiagnosticsMu.Unlock()
	h.cliDiagnostics = append([]kiroCLIDiagnosticAuditEntry{entry}, h.cliDiagnostics...)
	if len(h.cliDiagnostics) > maxKiroCLIDiagnosticAudit {
		h.cliDiagnostics = h.cliDiagnostics[:maxKiroCLIDiagnosticAudit]
	}
}

func (h *Handler) latestKiroCLIDiagnosticAudit() []kiroCLIDiagnosticAuditEntry {
	if h == nil {
		return nil
	}
	h.cliDiagnosticsMu.Lock()
	defer h.cliDiagnosticsMu.Unlock()
	return append([]kiroCLIDiagnosticAuditEntry(nil), h.cliDiagnostics...)
}

func (h *Handler) apiGetKiroCLIDiagnostics(w http.ResponseWriter, r *http.Request) {
	path, available, pathSource := resolveKiroCLIPath()
	displayPath := redactLocalDiagnosticPath(path)
	resp := map[string]interface{}{
		"cliPath":          displayPath,
		"cliPathSource":    pathSource,
		"available":        available,
		"home":             kiroCLIHomeSummary(),
		"commandRouter":    "unknown",
		"allowlisted":      []string{"version", "whoami", "doctor", "diagnostic", "chat --list-models", "settings list"},
		"opus47ModelState": opus47CLIModelState(nil),
		"latest":           nil,
		"audit":            h.latestKiroCLIDiagnosticAudit(),
		"readOnly":         true,
	}
	if available {
		ctx, cancel := context.WithTimeout(r.Context(), defaultKiroCLIDiagnosticTimeout)
		defer cancel()
		spec, _ := allowedKiroCLICommand("version")
		result := runKiroCLIDiagnosticCommand(ctx, path, spec)
		resp["version"] = result.Output
		resp["latest"] = result
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) apiRunKiroCLIDiagnostic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	spec, ok := allowedKiroCLICommand(req.Command)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Command is not allowlisted", "readOnly": true})
		return
	}
	path, available, pathSource := resolveKiroCLIPath()
	displayPath := redactLocalDiagnosticPath(path)
	if !available {
		result := kiroCLIDiagnosticCommandResult{
			Command:    spec.Name,
			Args:       spec.Args,
			Status:     "unavailable",
			Reason:     pathSource,
			ExitStatus: -1,
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
			Redacted:   true,
		}
		h.appendKiroCLIDiagnosticAudit(kiroCLIDiagnosticAuditEntry{Timestamp: time.Now(), Action: "kiro_cli_diagnostic", Command: spec.Name, Status: result.Status, Reason: result.Reason, Redacted: true})
		json.NewEncoder(w).Encode(map[string]interface{}{"result": result, "opus47ModelState": opus47CLIModelState(&result), "readOnly": true, "cliPath": displayPath})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultKiroCLIDiagnosticTimeout)
	defer cancel()
	result := runKiroCLIDiagnosticCommand(ctx, path, spec)
	audit := kiroCLIDiagnosticAuditEntry{Timestamp: time.Now(), Action: "kiro_cli_diagnostic", Command: spec.Name, Status: result.Status, Reason: result.Reason, Redacted: result.Redacted, DurationMs: result.DurationMs}
	h.appendKiroCLIDiagnosticAudit(audit)
	logger.Infof("[KiroCLI] diagnostic command=%s status=%s reason=%s duration_ms=%d redacted=%t", spec.Name, result.Status, result.Reason, result.DurationMs, result.Redacted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result":           result,
		"commandRouter":    commandRouterState(&result),
		"opus47ModelState": opus47CLIModelState(&result),
		"readOnly":         true,
		"audit":            h.latestKiroCLIDiagnosticAudit(),
		"cliPath":          displayPath,
	})
}

func redactLocalDiagnosticPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return path
}
