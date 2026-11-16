package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/explain"
)

func explainFixture(name string) string {
	return filepath.Join("..", "..", "internal", "explain", "testdata", name)
}

func runExplain(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runCLI(t.Context(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestExplainTextNamesMissedAndNext(t *testing.T) {
	code, out, errText := runExplain(t, "explain", explainFixture("guardrail-unproven.json"))
	if code != 0 || errText != "" {
		t.Fatalf("exit %d stderr %q", code, errText)
	}
	g, _ := explain.GuidanceFor("guard_passed")
	for _, want := range []string{
		"Invaris explain: challenge ",
		"-> missed",
		"reason: guard_passed (result)",
		"meaning: " + g.Meaning,
		"next: " + escapeLine(g.Next),
		"Limits\n",
		"- unauthenticated: ",
		"Explained offline from the report; nothing was run.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output lacks %q:\n%s", want, out)
		}
	}
}

func TestExplainTextOmitsNullReasonAndNext(t *testing.T) {
	_, out, _ := runExplain(t, "explain", "--format", "text", explainFixture("structured-check.json"))
	if strings.Contains(out, "reason:") || strings.Contains(out, "next:") {
		t.Fatalf("null reason/next must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "-> passed") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestExplainJSON(t *testing.T) {
	code, out, errText := runExplain(t, "explain", "--format", "json", explainFixture("campaign.json"))
	if code != 0 || errText != "" {
		t.Fatalf("exit %d stderr %q", code, errText)
	}
	var e explain.Explanation
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	if e.Version != "invaris.explanation/v1alpha1" || len(e.Items) != 6 || !strings.HasSuffix(out, "}\n") {
		t.Fatalf("explanation = %+v", e)
	}
}

func TestExplainExitsTwo(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := explainFixture("mutation-killed.json")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"explain", "--nope", good}, "unknown flag: --nope"},
		{"bad format", []string{"explain", "--format", "compact", good}, "unsupported format: compact"},
		{"format missing value", []string{"explain", "--format"}, "--format requires a value"},
		{"format repeated", []string{"explain", "--format", "text", "--format", "json", good}, "--format is repeated"},
		{"no file", []string{"explain"}, "missing report file"},
		{"two files", []string{"explain", good, good}, "unexpected argument"},
		{"flag after file", []string{"explain", good, "--format", "json"}, "flags must come before the report file"},
		{"dash is a path", []string{"explain", "-"}, "read report:"},
		{"missing file", []string{"explain", filepath.Join(dir, "absent.json")}, "read report:"},
		{"not a report", []string{"explain", write("bad.json", "nope")}, "report is not valid evidence: "},
		{"unsupported", []string{"explain", write("v9.json", `{"schemaVersion":"invaris.evidence/v9"}`)}, "unsupported report version: invaris.evidence/v9"},
		{"report flag", []string{"--report", filepath.Join(dir, "r.json"), "explain", good}, "--report is not valid for explain: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errText := runExplain(t, tc.args...)
			if code != 2 || out != "" || !strings.Contains(errText, tc.want) {
				t.Fatalf("exit %d stdout %q stderr %q, want exit 2 with %q", code, out, errText, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "r.json")); err == nil {
		t.Fatal("--report must not create a file")
	}
}

func TestExplainTamperedReportExitsTwo(t *testing.T) {
	data, err := os.ReadFile(explainFixture("guardrail-unproven.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "t.json")
	if err := os.WriteFile(p, bytes.Replace(data, []byte(`"decision": "REJECT"`), []byte(`"decision": "ACCEPT"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errText := runExplain(t, "explain", p)
	if code != 2 || out != "" || !strings.HasPrefix(errText, "report is not valid evidence: ") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errText)
	}
}

func TestExplainOversizeReportExitsTwo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(p, bytes.Repeat([]byte(" "), explain.MaxReportBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errText := runExplain(t, "explain", p)
	if code != 2 || !strings.Contains(errText, "report exceeds 16 MiB") {
		t.Fatalf("exit %d stderr %q", code, errText)
	}
}

func TestExplainUsageListsExplain(t *testing.T) {
	_, _, errText := runExplain(t)
	if !strings.Contains(errText, "invaris explain [--format text|json] REPORT") {
		t.Fatalf("usage lacks explain:\n%s", errText)
	}
}

func TestExplanationTextEscapesUserValues(t *testing.T) {
	reason, category, next := "odd\x1b[31m", "cat\negory", "n\x1b[0m\r\nx"
	e := explain.Explanation{
		Version: explain.ExplanationVersion,
		Report:  explain.ReportInfo{SchemaVersion: "invaris.evidence/v1alpha2", Action: "challenge", RunID: "r", Decision: "REJECT", Subject: "a b\"c\x1b[2J"},
		Items: []explain.Item{{ID: "id\nx y", Kind: "proof", Outcome: "UNPROVEN", Class: "missed",
			Reason: &reason, Category: &category, Meaning: "m\x1b[1m\nline", Next: &next}},
		Limits: []explain.Limit{},
	}
	out := explanationText(e)
	if strings.ContainsAny(strings.ReplaceAll(out, "\n", ""), "\x1b\r") {
		t.Fatalf("control characters leaked:\n%q", out)
	}
	if got := strings.Count(out, "\n"); got != 9 {
		t.Fatalf("a value broke the line structure (%d lines):\n%s", got, out)
	}
	for _, want := range []string{`"a b\"c\x1b[2J"`, `"id\nx y"`, `meaning: m\x1b[1m\nline`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

// A next step longer than the free-text width limit must still reach the
// reader whole; it names the action to take.
func TestExplainTextKeepsLongNextWhole(t *testing.T) {
	g, ok := explain.GuidanceFor("workspace_prepare_failed")
	if !ok || len(g.Next) <= messageLimit {
		t.Fatalf("test needs a next step longer than %d bytes", messageLimit)
	}
	e := explain.Explanation{
		Version: explain.ExplanationVersion,
		Report:  explain.ReportInfo{SchemaVersion: "invaris.evidence/v1alpha5", Action: "challenge", RunID: "r", Decision: "REFUSE", Subject: "h"},
		Items: []explain.Item{{ID: "c", Kind: "proof", Outcome: "INCONCLUSIVE", Class: "inconclusive",
			Meaning: g.Meaning, Next: &g.Next}},
		Limits: []explain.Limit{},
	}
	out := explanationText(e)
	if !strings.Contains(out, "next: "+g.Next+"\n") || strings.Contains(out, "(truncated)") {
		t.Fatalf("next step was shortened:\n%s", out)
	}
}

func TestEscapeLineAndMessageAgree(t *testing.T) {
	for _, s := range []string{`a\b`, "tab\there", "ctl\x1b[0m", "multi\nline", "plain"} {
		if got, want := escapeLine(s), message(s); got != want {
			t.Errorf("escapeLine(%q) = %q, message = %q", s, got, want)
		}
	}
	if got := escapeLine(`a\b`); got != `a\\b` {
		t.Errorf("backslash must be doubled, got %q", got)
	}
}
