package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

const (
	cliTests   = `if [ "$(cat src)" = new ]; then printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"ASSERTION_FAILED"}]}'; exit 1; fi; printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"PASS"}]}'`
	cliPass    = `printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"PASS"}]}'`
	cliValid   = `printf '%s\n' '{"version":"invaris.validity-result/v1alpha1","status":"VALID"}'`
	cliGuard   = `if [ "$(cat src)" = new ]; then printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"p","path":"src"}]}'; exit 1; fi; printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}'`
	cliBroken  = `exit 3`
	cliSurvive = "survive"
)

func cliJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cliMutation(t *testing.T, id, tests string) string {
	cmd := func(name, script string) map[string]any {
		return map[string]any{"id": name, "program": "sh", "args": []string{"-c", script}, "timeoutSeconds": 10}
	}
	return cliJSON(t, map[string]any{"version": "invaris.dev/v1alpha3", "name": id, "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"writablePaths": []string{"src"}, "experiment": map[string]any{"id": id,
			"mutation":   map[string]any{"id": "chg", "operator": "replace", "path": "src", "before": "old", "after": "new"},
			"validation": cmd("valid", cliValid), "test": cmd("test", tests), "testIds": []string{"case"}}})
}

func cliGuardHarness(t *testing.T, guard string) string {
	return cliJSON(t, map[string]any{"version": "invaris.dev/v1alpha2", "name": "g", "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"writablePaths": []string{"src"},
		"guards":        []any{map[string]any{"id": "g", "program": "sh", "args": []string{"-c", guard}, "policyId": "p", "timeoutSeconds": 10}},
		"challenges":    []any{map[string]any{"id": "c1", "guard": "g", "file": "src", "before": "old", "after": "new"}}})
}

// cliCampaign writes a project whose campaign runs a killed mutant, a second
// mutant (killed, or surviving with cliSurvive) and a guardrail (proven, or
// inconclusive with cliBroken).
func cliCampaign(t *testing.T, second, guard string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src"), "old")
	mustWrite(t, filepath.Join(dir, "killed.json"), cliMutation(t, "m1", cliTests))
	tests := cliTests
	if second == cliSurvive {
		tests = cliPass
	}
	mustWrite(t, filepath.Join(dir, "second.json"), cliMutation(t, "m2", tests))
	mustWrite(t, filepath.Join(dir, "guard.json"), cliGuardHarness(t, guard))
	mustWrite(t, filepath.Join(dir, "campaign.json"), cliJSON(t, map[string]any{
		"version": "invaris.campaign/v1alpha1", "name": "cli", "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"experiments": []any{
			map[string]any{"id": "killed", "kind": "mutation", "harness": "killed.json", "experimentId": "m1", "repeat": 1},
			map[string]any{"id": "second", "kind": "mutation", "harness": "second.json", "experimentId": "m2", "repeat": 1},
			map[string]any{"id": "guard", "kind": "guardrail", "harness": "guard.json", "challengeId": "c1", "repeat": 1},
		},
		"acceptance": map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": true, "requireNoInconclusive": true, "requireNoMixed": true},
	}))
	return dir
}

func decodeCampaign(t *testing.T, raw []byte) evidence.CampaignReport {
	t.Helper()
	var r evidence.CampaignReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v: %s", err, raw)
	}
	if err := evidence.ValidateCampaign(r); err != nil {
		t.Fatalf("stdout report invalid: %v", err)
	}
	return r
}

func TestCampaignCLIDecisions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		second, guard string
		code          int
		decision      domain.Decision
	}{
		{"accept", "", cliGuard, 0, domain.Accept},
		{"reject", cliSurvive, cliGuard, 1, domain.Reject},
		{"refuse", cliSurvive, cliBroken, 2, domain.RefuseDecision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := cliCampaign(t, tc.second, tc.guard)
			code, out, stderr := invoke(t, dir, "--report", "report.json", "campaign", "campaign.json")
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, stderr)
			}
			saved, err := os.ReadFile(filepath.Join(dir, "report.json"))
			if err != nil || !bytes.Equal(saved, out) {
				t.Fatalf("report file differs from stdout: %v", err)
			}
			if r := decodeCampaign(t, out); r.Decision != tc.decision || r.SchemaVersion != "invaris.evidence/v1alpha4" {
				t.Fatalf("decision %s", r.Decision)
			}
		})
	}
}

func TestCampaignCLIDefaultFile(t *testing.T) {
	dir := cliCampaign(t, "", cliGuard)
	if code, out, stderr := invoke(t, dir, "campaign"); code != 0 || decodeCampaign(t, out).Campaign != "cli" {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestCampaignCLIOnly(t *testing.T) {
	// The guard is broken, but it is outside the selected scope.
	dir := cliCampaign(t, "", cliBroken)
	code, out, stderr := invoke(t, dir, "campaign", "--only", "killed", "--only", "second", "campaign.json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	r := decodeCampaign(t, out)
	g := r.Experiments[2]
	if g.Selection != domain.FilteredOut || len(g.Attempts) != 0 || r.Selection.Mode != "filtered" || r.GuardrailSummary.Filtered != 1 ||
		r.AcceptanceEvaluation.Requirements[1].Status != domain.RequirementNotApplicable {
		t.Fatalf("%+v %+v %+v", g, r.Selection, r.AcceptanceEvaluation)
	}
}

func TestCampaignCLIText(t *testing.T) {
	dir := cliCampaign(t, cliSurvive, cliBroken)
	code, out, stderr := invoke(t, dir, "--report", "report.json", "campaign", "--format", "text", "campaign.json")
	text := string(out)
	if code != 2 || strings.Contains(text, "{") {
		t.Fatalf("exit %d: %s %s", code, text, stderr)
	}
	for _, want := range []string{"Invaris campaign: cli", "Score:         50.0% (1 of 2 eligible; 0 excluded)", "Inconclusive:  1",
		"Acceptance: REFUSE", "requireNoSurvivors: VIOLATED (second)", "requireNoInconclusive: UNSUPPORTED (guard)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	saved, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	decodeCampaign(t, saved)
	code, out, _ = invoke(t, dir, "campaign", "--format", "text", "--only", "killed", "campaign.json")
	if code != 0 || !strings.Contains(string(out), "Scope: filtered (--only killed)") || !strings.Contains(string(out), "FILTERED_OUT") {
		t.Fatalf("filtered text, exit %d:\n%s", code, out)
	}
}

// Invalid invocations run nothing and print nothing on stdout.
func TestCampaignCLIRefusesBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		edit func(dir string)
	}{
		{"unknown only", []string{"campaign", "--only", "missing", "campaign.json"}, nil},
		{"repeated only", []string{"campaign", "--only", "killed", "--only", "killed", "campaign.json"}, nil},
		{"only without value", []string{"campaign", "campaign.json", "--only"}, nil},
		{"bad format", []string{"campaign", "--format", "xml", "campaign.json"}, nil},
		{"repeated format", []string{"campaign", "--format", "json", "--format", "text", "campaign.json"}, nil},
		{"unknown flag", []string{"campaign", "--parallel", "campaign.json"}, nil},
		{"two files", []string{"campaign", "campaign.json", "campaign.json"}, nil},
		{"missing file", []string{"campaign", "absent.json"}, nil},
		{"invalid campaign", []string{"campaign", "campaign.json"}, func(dir string) {
			mustWrite(t, filepath.Join(dir, "campaign.json"), `{"version":"invaris.campaign/v1alpha1"}`)
		}},
		{"occupied report", []string{"--report", "campaign.json", "campaign", "campaign.json"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "ran")
			mustWrite(t, filepath.Join(dir, "src"), "old")
			mustWrite(t, filepath.Join(dir, "killed.json"), cliMutation(t, "m1", `touch `+sentinel+`; `+cliTests))
			mustWrite(t, filepath.Join(dir, "campaign.json"), cliJSON(t, map[string]any{
				"version": "invaris.campaign/v1alpha1", "name": "cli", "trustMode": "trusted-local", "networkPolicy": "unrestricted",
				"experiments": []any{map[string]any{"id": "killed", "kind": "mutation", "harness": "killed.json", "experimentId": "m1", "repeat": 1}},
				"acceptance":  map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true},
			}))
			if tc.edit != nil {
				tc.edit(dir)
			}
			code, out, stderr := invoke(t, dir, tc.args...)
			if code != 2 || len(out) != 0 || strings.TrimSpace(stderr) == "" {
				t.Fatalf("exit %d stdout %q stderr %q", code, out, stderr)
			}
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("an experiment ran")
			}
		})
	}
}

// A publication failure after the run leaves nothing on stdout, in either format.
func TestCampaignCLIPublicationFailure(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			dir := cliCampaign(t, "", cliGuard)
			withFS(t, func(f *reportFS) {
				link := f.Link
				f.Link = func(oldname, newname string) error {
					if strings.HasSuffix(newname, ".link") {
						return link(oldname, newname) // the preflight probe succeeds
					}
					return errInjected
				}
			})
			code, out, stderr := invoke(t, dir, "--report", "report.json", "campaign", "--format", format, "campaign.json")
			if code != 2 || len(out) != 0 || !strings.Contains(stderr, "save report") {
				t.Fatalf("exit %d stdout %q stderr %q", code, out, stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
				t.Fatal("report published")
			}
		})
	}
}
