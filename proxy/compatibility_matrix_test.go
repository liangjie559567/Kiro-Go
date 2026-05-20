package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeCodeCompatibilityMatrixIsCompleteAndHonest(t *testing.T) {
	path := filepath.Join("..", "docs", "claude-code-compatibility-matrix.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}

	var matrix struct {
		Rows []struct {
			ID                      string   `json:"id"`
			Feature                 string   `json:"feature"`
			ClaudeCodeCompatibility status   `json:"claude_code_compatibility"`
			OfficialAnthropicParity status   `json:"official_anthropic_parity"`
			Evidence                []string `json:"evidence"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode matrix: %v", err)
	}
	if len(matrix.Rows) == 0 {
		t.Fatalf("expected matrix rows")
	}

	covered := map[string]bool{}
	for _, row := range matrix.Rows {
		if row.ID == "" || row.Feature == "" {
			t.Fatalf("matrix row missing identity: %#v", row)
		}
		if len(row.Evidence) == 0 {
			t.Fatalf("matrix row %s has no evidence", row.ID)
		}
		req := requirementPrefix(row.ID)
		covered[req] = true

		mode := strings.ToLower(row.OfficialAnthropicParity.Mode)
		if row.OfficialAnthropicParity.Status == "PASS" && officialPassModeIsUnproven(mode) {
			t.Fatalf("row %s marks upstream-unproven mode %q as official PASS", row.ID, row.OfficialAnthropicParity.Mode)
		}
		if strings.Contains(mode, "estimated") && row.OfficialAnthropicParity.Status == "PASS" {
			t.Fatalf("row %s marks estimated behavior as official PASS", row.ID)
		}
		if strings.Contains(mode, "emulated") && row.OfficialAnthropicParity.Status == "PASS" {
			t.Fatalf("row %s marks emulated behavior as official PASS", row.ID)
		}
		if row.ClaudeCodeCompatibility.Status == "" || row.OfficialAnthropicParity.Status == "" {
			t.Fatalf("row %s has empty status: %#v", row.ID, row)
		}
	}

	for _, req := range []string{"CC-01", "CC-02", "CC-03", "CC-04", "CC-05", "CC-06", "CC-07"} {
		if !covered[req] {
			t.Fatalf("matrix missing requirement %s", req)
		}
	}
}

type status struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

func officialPassModeIsUnproven(mode string) bool {
	for _, marker := range []string{
		"estimated",
		"emulated",
		"local_",
		"upstream",
		"unproven",
		"uat_harness_ready",
		"live_evidence_required",
		"kiro_go_chunked_complete_input",
		"kiro_model_cache",
	} {
		if strings.Contains(mode, marker) {
			return true
		}
	}
	return false
}

func requirementPrefix(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) >= 2 && strings.EqualFold(parts[0], "CC") {
		return parts[0] + "-" + parts[1]
	}
	return id
}
