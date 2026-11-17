package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findingHarness writes a v1alpha1 structured-guard harness whose guard
// prints one finding with the given extra JSON fields (", ..." or "").
func findingHarness(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	out := `{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":2,"findings":[{"policyId":"p","path":"src.txt"` + extra + `}]}`
	guard := "printf '%s\\n' '" + out + "'; exit 1\n"
	harness := `{"version":"invaris.dev/v1alpha1","name":"f","guards":[{"id":"g","policyId":"p","minInspected":1,"program":"sh","args":["guard.sh"]}]}`
	for name, body := range map[string]string{"guard.sh": guard, "invaris.json": harness, "src.txt": "x\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "invaris.json")
}

func checkReport(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runCLI(t.Context(), append([]string{"check"}, args...), &out, &errb)
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("code %d: stdout not JSON: %v\n%s\n%s", code, err, out.String(), errb.String())
	}
	return code, doc
}

func firstFinding(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	checks, _ := doc["checks"].([]any)
	for _, c := range checks {
		if fs, _ := c.(map[string]any)["findings"].([]any); len(fs) > 0 {
			return fs[0].(map[string]any)
		}
	}
	t.Fatalf("no finding in %v", doc)
	return nil
}

func TestInspectionFindingFields(t *testing.T) {
	h := findingHarness(t, `,"ruleId":"route-missing","message":"GET /admin has no guard","line":12`)
	code, doc := checkReport(t, h)
	f := firstFinding(t, doc)
	if code != 1 || f["ruleId"] != "route-missing" || f["message"] != "GET /admin has no guard" || f["line"] != float64(12) {
		t.Fatalf("code %d finding %v", code, f)
	}
	_, v5 := checkReport(t, "--evidence-version", "v1alpha5", h)
	f = firstFinding(t, v5)
	if f["message"] != "details withheld" || f["line"] != float64(12) {
		t.Fatalf("v5 finding %v", f)
	}
	// A report carrying the new fields still passes explain's validation.
	dir := t.TempDir()
	report := filepath.Join(dir, "r.json")
	var out, errb bytes.Buffer
	runCLI(t.Context(), []string{"--report", report, "check", h}, &out, &errb)
	out.Reset()
	if code := runCLI(t.Context(), []string{"explain", report}, &out, &errb); code != 0 {
		t.Fatalf("explain refused the report: %s", errb.String())
	}
}

func TestInspectionFindingValidation(t *testing.T) {
	for name, extra := range map[string]string{
		"line zero":     `,"line":0`,
		"negative line": `,"line":-1`,
		"fraction line": `,"line":1.5`,
		"empty rule":    `,"ruleId":""`,
		"null message":  `,"message":null`,
		"unknown field": `,"column":3`,
		"long rule":     `,"ruleId":"` + strings.Repeat("r", 129) + `"`,
	} {
		code, doc := checkReport(t, findingHarness(t, extra))
		if code != 2 || doc["decision"] != "REFUSE" {
			t.Errorf("%s: code %d decision %v", name, code, doc["decision"])
		}
	}
}

func TestInspectionFindingOmittedFields(t *testing.T) {
	_, doc := checkReport(t, findingHarness(t, ""))
	f := firstFinding(t, doc)
	if _, ok := f["line"]; ok || f["ruleId"] != "p" || f["message"] != "" {
		t.Fatalf("finding %v", f)
	}
}

// A structured guard that names its own ruleId still proves a challenge.
func TestChallengeWithGuardRuleID(t *testing.T) {
	dir := t.TempDir()
	guard := `if grep -q FORBIDDEN source.txt; then printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"p","path":"source.txt","ruleId":"marker-found","line":1}]}'; exit 1; fi
printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}'
`
	harness := `{"version":"invaris.dev/v1alpha1","name":"r","guards":[{"id":"g","policyId":"p","program":"sh","args":["guard.sh"]}],"challenges":[{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"FORBIDDEN"}]}`
	for name, body := range map[string]string{"guard.sh": guard, "invaris.json": harness, "source.txt": "SAFE\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	code := runCLI(t.Context(), []string{"challenge", filepath.Join(dir, "invaris.json")}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), `"status": "PROVEN"`) {
		t.Fatalf("code %d\n%s\n%s", code, out.String(), errb.String())
	}
}
