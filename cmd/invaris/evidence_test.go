package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decoded is an independent view of the emitted JSON, not the producer's types.
type decoded struct {
	SchemaVersion string `json:"schemaVersion"`
	Decision      string `json:"decision"`
	ConfigDigest  *string
	Compatibility struct {
		Status string `json:"status"`
	} `json:"compatibility"`
	Checks []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"checks"`
}

func TestCLIEmitsV2EvidenceAndMapsDecisions(t *testing.T) {
	head := `"version":"invaris.dev/v1alpha2","name":"cli","trustMode":"trusted-local","networkPolicy":"unrestricted",`
	two := `"guards":[{"id":"fails","program":"sh","args":["-c","exit 1"]},{"id":"refuses","program":"invaris-missing-executable-4242"}]`
	for _, tc := range []struct {
		name, config string
		code         int
		decision     string
	}{
		{"accept", head + `"guards":[{"id":"ok","program":"true"}]`, 0, "ACCEPT"},
		{"failure-first", head + two + `,"gate":{"requiredGuards":["fails","refuses"],"precedence":"failure-first"}`, 1, "REJECT"},
		{"refusal-first", head + two + `,"gate":{"requiredGuards":["fails","refuses"],"precedence":"refusal-first"}`, 2, "REFUSE"},
		{"v1alpha1 input", `"version":"invaris.dev/v1alpha1","name":"legacy","guards":[{"id":"ok","program":"true"}]`, 0, "ACCEPT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "invaris.json")
			mustWrite(t, path, "{"+tc.config+"}")
			report := filepath.Join(dir, "out", "report.json")
			if err := os.Mkdir(filepath.Dir(report), 0o700); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := invoke(t, dir, "--report", report, "check", path)
			if code != tc.code {
				t.Fatalf("code=%d stderr=%q stdout=%s", code, stderr, stdout)
			}
			var got decoded
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatal(err)
			}
			if got.SchemaVersion != "invaris.evidence/v1alpha2" || got.Decision != tc.decision {
				t.Fatalf("unexpected evidence: %s", stdout)
			}
			if tc.name == "v1alpha1 input" && got.Compatibility.Status != "legacy" {
				t.Fatalf("legacy input not flagged: %s", stdout)
			}
			if tc.name == "failure-first" || tc.name == "refusal-first" {
				seen := map[string]string{}
				for _, c := range got.Checks {
					seen[c.ID] = c.Status
				}
				if seen["fails"] != "FAIL" || seen["refuses"] != "REFUSE" {
					t.Fatalf("both outcomes must remain visible: %v", seen)
				}
			}
			if disk, _ := os.ReadFile(report); string(disk) != string(stdout) {
				t.Fatal("report file differs from stdout")
			}
			if !strings.HasSuffix(string(stdout), "}\n") {
				t.Fatal("missing trailing newline")
			}
		})
	}
}
