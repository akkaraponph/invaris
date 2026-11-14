package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIExitContract(t *testing.T) {
	const legacy = `"version":"invaris.dev/v1alpha1","name":"exit-contract",`
	const modern = `"version":"invaris.dev/v1alpha2","name":"exit-contract","trustMode":"trusted-local","networkPolicy":"unrestricted",`
	const structured = `"guards":[{"id":"rule","policyId":"no-forbidden-marker","program":"sh","args":["guard.sh"]}]`
	const publishCollision = `"guards":[{"id":"rule","program":"sh","args":["-c","printf existing-report > raced.json"]}]`
	const alwaysPasses = `"guards":[{"id":"rule","program":"sh","args":["-c","exit 0"]}]`
	const challenge = `,"challenges":[{"id":"fault","guard":"rule","file":"source.txt","before":"SAFE","after":"FORBIDDEN","expectedOutput":"FORBIDDEN"}]`
	const malformed = `"guards":[{"id":"rule","policyId":"no-forbidden-marker","program":"sh","args":["-c","echo not-json; exit 0"]}]`
	const mixed = `"guards":[{"id":"fails","program":"sh","args":["-c","exit 1"]},{"id":"refuses","program":"./missing-guard"}]`
	const requiredProvider = `"adapters":[{"id":"missing","program":"./missing-provider"}],"requiredAdapters":["missing"],"guards":[{"id":"rule","adapterId":"missing","policyId":"no-forbidden-marker","scope":["source.txt"]}]`
	for _, tc := range []struct {
		name, action, config, source, decision string
		exit                                   int
		checks                                 map[string]string
		proof, stderr                          string
		destination                            string
		preexisting                            bool
	}{
		{name: "clean", action: "check", config: legacy + structured, source: "SAFE", exit: 0, decision: "ACCEPT", checks: map[string]string{"rule": "PASS"}},
		{name: "matching rule violation", action: "check", config: legacy + structured, source: "FORBIDDEN", exit: 1, decision: "REJECT", checks: map[string]string{"rule": "FAIL"}},
		{name: "surviving mutant", action: "challenge", config: legacy + alwaysPasses + challenge, source: "SAFE", exit: 1, decision: "REJECT", proof: "UNPROVEN"},
		{name: "malformed evidence", action: "check", config: legacy + malformed, exit: 2, decision: "REFUSE", checks: map[string]string{"rule": "REFUSE"}},
		{name: "missing evidence", action: "check", config: legacy + strings.Replace(malformed, "echo not-json; exit 0", "exit 0", 1), exit: 2, decision: "REFUSE", checks: map[string]string{"rule": "REFUSE"}},
		{name: "required provider unavailable", action: "check", config: modern + requiredProvider, exit: 2, decision: "REFUSE", checks: map[string]string{"rule": "NOT_RUN"}},
		{name: "failure first", action: "check", config: modern + mixed + `,"gate":{"requiredGuards":["fails","refuses"],"precedence":"failure-first"}`, exit: 1, decision: "REJECT", checks: map[string]string{"fails": "FAIL", "refuses": "REFUSE"}},
		{name: "refusal first", action: "check", config: modern + mixed + `,"gate":{"requiredGuards":["fails","refuses"],"precedence":"refusal-first"}`, exit: 2, decision: "REFUSE", checks: map[string]string{"fails": "FAIL", "refuses": "REFUSE"}},
		{name: "report destination occupied", action: "check", config: legacy + alwaysPasses, exit: 2, stderr: "prepare report:", destination: "occupied.json", preexisting: true},
		{name: "report publication collision", action: "check", config: legacy + publishCollision, exit: 2, stderr: "save report:", destination: "raced.json"},
		{name: "invalid harness", action: "check", config: `"version":"unsupported"`, exit: 2, stderr: "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "invaris.json"), "{"+tc.config+"}")
			mustWrite(t, filepath.Join(dir, "source.txt"), tc.source)
			guard, err := os.ReadFile("../../examples/structured/guard.sh")
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(dir, "guard.sh"), string(guard))
			args := []string{tc.action}
			destination := filepath.Join(dir, tc.destination)
			if tc.destination != "" {
				if tc.preexisting {
					mustWrite(t, destination, "existing-report")
				}
				args = append([]string{"--report", destination}, args...)
			}
			cmd := exec.Command(cliBinary, args...)
			cmd.Dir = dir
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			exit := 0
			if err != nil {
				var status *exec.ExitError
				if !errors.As(err, &status) {
					t.Fatal(err)
				}
				exit = status.ExitCode()
			}
			if exit != tc.exit {
				t.Fatalf("exit=%d, want %d; stderr=%q stdout=%s", exit, tc.exit, stderr.String(), stdout)
			}
			if tc.stderr != "" {
				if len(stdout) != 0 || !strings.Contains(stderr.String(), tc.stderr) {
					t.Fatalf("expected only stderr containing %q, got stdout=%q stderr=%q", tc.stderr, stdout, stderr.String())
				}
				if tc.destination != "" {
					data, err := os.ReadFile(destination)
					if err != nil || string(data) != "existing-report" {
						t.Fatalf("existing destination changed: %q, %v", data, err)
					}
				}
				return
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected CLI stderr: %s", &stderr)
			}
			var report struct {
				decoded
				Proofs []struct{ ID, Status string }
			}
			if err := json.Unmarshal(stdout, &report); err != nil {
				t.Fatal(err)
			}
			if report.SchemaVersion != "invaris.evidence/v1alpha2" || report.Decision != tc.decision {
				t.Fatalf("wrong schema or decision: %s", stdout)
			}
			for id, want := range tc.checks {
				found := false
				for _, check := range report.Checks {
					if check.ID == id && check.Status == want {
						found = true
					}
				}
				if !found {
					t.Errorf("missing check %q with status %s: %+v", id, want, report.Checks)
				}
			}
			if tc.proof != "" && (len(report.Proofs) != 1 || report.Proofs[0].ID != "fault" || report.Proofs[0].Status != tc.proof) {
				t.Errorf("unexpected proofs: %+v", report.Proofs)
			}
		})
	}
}
