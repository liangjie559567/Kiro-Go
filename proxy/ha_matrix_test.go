package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKiroHAMatrixIsCompleteAndHonest(t *testing.T) {
	path := filepath.Join("..", "docs", "kiro-ha-compatibility-matrix.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read HA matrix: %v", err)
	}

	var matrix struct {
		Rows []struct {
			ID        string   `json:"id"`
			Feature   string   `json:"feature"`
			Automated status   `json:"automated"`
			LiveUAT   status   `json:"live_uat"`
			Evidence  []string `json:"evidence"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode HA matrix: %v", err)
	}
	if len(matrix.Rows) == 0 {
		t.Fatalf("expected HA matrix rows")
	}

	covered := map[string]bool{}
	for _, row := range matrix.Rows {
		if row.ID == "" || row.Feature == "" {
			t.Fatalf("HA matrix row missing identity: %#v", row)
		}
		if len(row.Evidence) == 0 {
			t.Fatalf("HA matrix row %s has no evidence", row.ID)
		}
		covered[row.ID] = true
		if row.Automated.Status == "" || row.LiveUAT.Status == "" {
			t.Fatalf("HA matrix row %s has empty status: %#v", row.ID, row)
		}
		if row.LiveUAT.Status == "PASS" && strings.Contains(strings.ToLower(row.LiveUAT.Mode), "pre_current_change") {
			t.Fatalf("HA matrix row %s marks historical live UAT as current PASS", row.ID)
		}
		if row.ID == "HA-07" && row.Automated.Status == "PASS" {
			t.Fatalf("HA-07 must not be automated PASS without latest-code live UAT")
		}
	}

	for _, req := range []string{"HA-01", "HA-02", "HA-03", "HA-04", "HA-05", "HA-06", "HA-07"} {
		if !covered[req] {
			t.Fatalf("HA matrix missing requirement %s", req)
		}
	}
}
