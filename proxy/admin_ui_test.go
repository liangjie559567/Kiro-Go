package proxy

import (
	"os"
	"strings"
	"testing"
)

func TestAdminUIIncludesKiroCLIDiagnosticsCard(t *testing.T) {
	body, err := os.ReadFile("../web/index.html")
	if err != nil {
		t.Fatalf("read admin UI: %v", err)
	}
	html := string(body)
	for _, want := range []string{
		`id="kiro-cli-diagnostics"`,
		"loadKiroCLIDiagnostics()",
		"/admin/api/kiro-cli/diagnostics",
		"escapeHtml(data.cliPath",
		"escapeHtml(latestText)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("admin UI missing %q", want)
		}
	}
}
