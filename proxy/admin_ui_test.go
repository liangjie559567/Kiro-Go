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
	app, err := os.ReadFile("../web/app.js")
	if err != nil {
		t.Fatalf("read admin JS: %v", err)
	}
	assets := string(body) + "\n" + string(app)
	for _, want := range []string{
		`id="kiro-cli-diagnostics"`,
		"loadKiroCLIDiagnostics()",
		"/admin/api/kiro-cli/diagnostics",
		"escapeHtml(data.cliPath",
		"escapeHtml(latestText)",
	} {
		if !strings.Contains(assets, want) {
			t.Fatalf("admin UI missing %q", want)
		}
	}
}
