package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

var operatorFiles = []string{"invaris.json", "campaign.json", "scan.sh", "notes.txt", "lines.txt", "sample.env"}

// operatorProject copies the operator example into dir (a fresh directory
// under parent with the given name).
func operatorProject(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range operatorFiles {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "operators", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func v5Report(t *testing.T, dir string, args ...string) (evidence.Report, []byte) {
	t.Helper()
	code, out, stderr := runBinary(t, dir, nil, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d stderr=%q", args, code, stderr)
	}
	r, err := evidence.DecodeReportV5(out)
	if err != nil {
		t.Fatalf("invalid v5 report: %v\n%s", err, out)
	}
	return r, out
}

func TestV5ChallengeReportAndTamperRejection(t *testing.T) {
	dir := operatorProject(t, t.TempDir(), "p")
	r, _ := v5Report(t, dir, "challenge")
	if r.Decision != domain.Accept || len(r.MutationMetadata) != 3 || len(r.CommandDigests) != len(r.Observations) {
		t.Fatalf("report: %s %d %d", r.Decision, len(r.MutationMetadata), len(r.CommandDigests))
	}
	for _, m := range r.MutationMetadata {
		if m.Application != evidence.Applied || m.ReproductionID == nil || m.Expected.RuleID != m.Expected.PolicyID {
			t.Fatalf("metadata %+v", m)
		}
	}
	clone := func() evidence.Report {
		data, err := evidence.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		c, err := evidence.DecodeReportV5(data)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	other := strings.Repeat("0", 64)
	for name, tamper := range map[string]func(*evidence.Report){
		"before hash":          func(r *evidence.Report) { r.MutationMetadata[0].BeforeSHA256 = &other },
		"after hash":           func(r *evidence.Report) { r.MutationMetadata[1].AfterSHA256 = &other },
		"preimage":             func(r *evidence.Report) { r.MutationMetadata[0].PreimageSHA256 = other },
		"application":          func(r *evidence.Report) { r.MutationMetadata[0].Application = evidence.NotApplied },
		"unknown evidence ID":  func(r *evidence.Report) { r.MutationMetadata[0].EvidenceID = "nope" },
		"duplicate entry":      func(r *evidence.Report) { r.MutationMetadata[1] = r.MutationMetadata[0] },
		"reproduction ID":      func(r *evidence.Report) { s := "sha256:" + other; r.MutationMetadata[2].ReproductionID = &s },
		"reproduction dropped": func(r *evidence.Report) { r.MutationMetadata[2].ReproductionID = nil },
		"expected rule":        func(r *evidence.Report) { r.MutationMetadata[0].Expected.RuleID = "other" },
		"expected guard":       func(r *evidence.Report) { r.MutationMetadata[0].Expected.GuardID = "crlf" },
		"unknown operator":     func(r *evidence.Report) { r.MutationMetadata[0].Operator = "replace" },
		"fixture moved":        func(r *evidence.Report) { r.MutationMetadata[0].Fixture = "invaris-test-token-v1" },
		"challenged command differs": func(r *evidence.Report) {
			for i, o := range r.Observations {
				if o.Kind == "challenged" {
					r.CommandDigests[i].SHA256 = "sha256:" + other
					return
				}
			}
		},
		"digest missing":      func(r *evidence.Report) { r.CommandDigests = r.CommandDigests[1:] },
		"digest renamed":      func(r *evidence.Report) { r.CommandDigests[0].ObservationID = "x" },
		"argv leaked":         func(r *evidence.Report) { r.Observations[0].Args = []string{"scan.sh"} },
		"program path leaked": func(r *evidence.Report) { r.Observations[0].Program = "/home/user/bin/scan" },
		"output leaked":       func(r *evidence.Report) { r.Observations[0].Stdout = "{}" },
		"path leaked":         func(r *evidence.Report) { r.Observations[0].Executable.Path = "/bin/sh" },
		"root leaked":         func(r *evidence.Report) { r.Target = "/home/user/project" },
		"detail leaked":       func(r *evidence.Report) { r.Proofs[0].Detail = "guard reported the expected policy" },
		"empty engine":        func(r *evidence.Report) { e := ""; r.EngineVersion = &e },
		"forged conclusion":   func(r *evidence.Report) { r.Proofs[0].Mutation = nil },
		"weak builtin proof":  func(r *evidence.Report) { r.Proofs[0].EvidenceStrength = domain.StrengthWeak },
	} {
		t.Run(name, func(t *testing.T) {
			c := clone()
			tamper(&c)
			if err := evidence.Validate(c); err == nil {
				t.Fatal("tampered report accepted")
			}
			if _, err := evidence.Marshal(c); err == nil {
				t.Fatal("tampered report marshaled")
			}
		})
	}
	// Old reports never carry ProfileV5 fields.
	old := clone()
	old.SchemaVersion = evidence.SchemaVersion
	if evidence.Validate(old) == nil {
		t.Fatal("v1alpha2 report with v1alpha5 fields accepted")
	}
}

// v5 never stores the root path, arguments, output or file contents, in any
// output format or the report file; the reproduction ID depends only on the
// declared inputs and the edit.
func TestV5PrivacyAndReproductionIdentity(t *testing.T) {
	parent := t.TempDir()
	a := operatorProject(t, parent, "SENTINEL_HOME_alice")
	// A credential-valued argument and a guard that prints a sentinel.
	harness, _ := os.ReadFile(filepath.Join(a, "invaris.json"))
	h := strings.Replace(string(harness), `"args": ["scan.sh", "trailing-whitespace", "notes.txt"]`,
		`"args": ["-c", "echo SENTINEL_OUT >&2; exec sh scan.sh trailing-whitespace notes.txt", "--token=SENTINEL_CRED"]`, 1)
	if h == string(harness) {
		t.Fatal("fixture changed")
	}
	// An absolute program path under the private directory.
	wrapper := filepath.Join(a, "crlf-wrapper")
	mustWrite(t, wrapper, "#!/bin/sh\nexec sh scan.sh \"$@\"\n")
	os.Chmod(wrapper, 0o755)
	h = strings.Replace(h, `"program": "sh", "args": ["scan.sh", "line-endings", "lines.txt"]`,
		`"program": "`+wrapper+`", "args": ["line-endings", "lines.txt"]`, 1)
	if !strings.Contains(h, wrapper) {
		t.Fatal("fixture changed")
	}
	mustWrite(t, filepath.Join(a, "invaris.json"), h)
	report := filepath.Join(parent, "out.json")
	_, out := v5Report(t, a, "--report", report, "challenge")
	_, text, _ := runBinary(t, a, nil, "challenge", "--format", "text")
	saved, _ := os.ReadFile(report)
	for _, doc := range [][]byte{out, text, saved} {
		for _, s := range []string{"SENTINEL_HOME", "SENTINEL_CRED", "SENTINEL_OUT", parent, "scan.sh", "Release notes", "APP_MODE", "INVARIS_TEST_TOKEN"} {
			if bytes.Contains(doc, []byte(s)) {
				t.Fatalf("v5 output exposes %q", s)
			}
		}
	}

	ids := func(dir string) []string {
		r, _ := v5Report(t, dir, "challenge")
		var out []string
		for _, m := range r.MutationMetadata {
			out = append(out, *m.ReproductionID)
		}
		return out
	}
	b := operatorProject(t, parent, "elsewhere/bob")
	base := ids(b)
	if again := ids(operatorProject(t, t.TempDir(), "x")); strings.Join(again, ",") != strings.Join(base, ",") {
		t.Fatal("reproduction IDs depend on location or run")
	}
	// Changed source (another file), config or oracle change every identity.
	changed := operatorProject(t, parent, "changed-source")
	mustWrite(t, filepath.Join(changed, "extra.txt"), "x\n")
	cfg := operatorProject(t, parent, "changed-config")
	raw, _ := os.ReadFile(filepath.Join(cfg, "invaris.json"))
	mustWrite(t, filepath.Join(cfg, "invaris.json"), strings.Replace(string(raw), `"builtin-operators"`, `"renamed"`, 1))
	for _, dir := range []string{changed, cfg} {
		for i, id := range ids(dir) {
			if id == base[i] {
				t.Fatalf("%s: reproduction ID unchanged", dir)
			}
		}
	}
}

// The canonical vector is fixed; an independent construction of the same
// bytes must give the same identity.
func TestReproductionIDVector(t *testing.T) {
	h := strings.Repeat("a", 64)
	got := evidence.ReproductionID("sha256:"+h, h, "c", "g", "crlf-injection", "1", "", "dir/<f>&.txt", h, strings.Repeat("b", 64), "p", "p")
	literal := `["invaris.reproduction/v1","sha256:` + h + `","` + h + `","c","g","crlf-injection","1","","dir/<f>&.txt","` + h + `","` + strings.Repeat("b", 64) + `","p","p"]`
	sum := sha256.Sum256([]byte(literal))
	if got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("vector mismatch: %s", got)
	}
	d := evidence.CommandDigestOf("sh", nil, nil, 0)
	sum = sha256.Sum256([]byte(`["sh",[],[],30]`))
	if d != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("command digest vector mismatch: %s", d)
	}
}

func TestV5ProfileSelection(t *testing.T) {
	dir := setupCase(t, caseNamed(t, "v2-check"))
	for _, tc := range []struct {
		args   []string
		schema string
	}{
		{[]string{"check"}, "invaris.evidence/v1alpha2"},
		{[]string{"check", "--evidence-version", "v1alpha5"}, evidence.ProfileV5},
		{[]string{"run", "--evidence-version", "v1alpha5", "invaris.json"}, evidence.ProfileV5},
		{[]string{"challenge", "--format", "compact", "--evidence-version", "v1alpha5"}, ""},
	} {
		code, out, stderr := runBinary(t, dir, nil, tc.args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", tc.args, code, stderr)
		}
		if tc.schema == "" {
			continue
		}
		var v struct{ SchemaVersion string }
		json.Unmarshal(out, &v)
		if v.SchemaVersion != tc.schema {
			t.Fatalf("%v: schema %s", tc.args, v.SchemaVersion)
		}
	}
	mut := setupCase(t, caseNamed(t, "v3-mutate-killed"))
	code, out, _ := runBinary(t, mut, nil, "mutate", "--evidence-version", "v1alpha5", "killed.json")
	if code != 0 {
		t.Fatalf("mutate v5 exit %d", code)
	}
	r, err := evidence.DecodeReportV5(out)
	if err != nil || r.Experiment == nil || r.Experiment.Verdict != domain.MutationKilled || len(r.MutationMetadata) != 0 {
		t.Fatalf("mutate v5: %v", err)
	}
	// A v1alpha4 harness always produces v1alpha5; the flag is refused for other values.
	op := operatorProject(t, t.TempDir(), "p")
	if _, out := v5Report(t, op, "check"); !bytes.Contains(out, []byte(evidence.ProfileV5)) {
		t.Fatal("v1alpha4 input must produce v1alpha5")
	}
	for _, args := range [][]string{{"check", "--evidence-version", "v1alpha2"}, {"check", "--evidence-version", ""}, {"check", "--evidence-version", "v1alpha5", "--evidence-version", "v1alpha5"}, {"campaign", "--evidence-version", "v1alpha4"}} {
		if code, out, _ := runBinary(t, op, nil, args...); code != 2 || len(out) != 0 {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
}

func TestV5Campaign(t *testing.T) {
	dir := operatorProject(t, t.TempDir(), "p")
	code, out, stderr := runBinary(t, dir, nil, "campaign")
	if code != 0 {
		t.Fatalf("campaign exit %d: %s", code, stderr)
	}
	r, err := evidence.DecodeCampaignV5(out)
	if err != nil {
		t.Fatalf("invalid v5 campaign: %v", err)
	}
	if len(r.MutationMetadata) != 4 {
		t.Fatalf("one metadata entry per started attempt: %d", len(r.MutationMetadata))
	}
	// Both repeats of "spacing" ran the same declared inputs and edit.
	if r.MutationMetadata[0].EvidenceID != "spacing#1" || r.MutationMetadata[1].EvidenceID != "spacing#2" ||
		*r.MutationMetadata[0].ReproductionID != *r.MutationMetadata[1].ReproductionID {
		t.Fatalf("repeat identity: %+v", r.MutationMetadata[:2])
	}
	// Standalone and campaign proofs use the same oracle and identity.
	s, _ := v5Report(t, dir, "challenge")
	if *s.MutationMetadata[0].ReproductionID != *r.MutationMetadata[0].ReproductionID {
		t.Fatal("standalone and campaign reproduction IDs differ")
	}
	for name, tamper := range map[string]func(*evidence.CampaignReport){
		"metadata for missing attempt": func(r *evidence.CampaignReport) { r.MutationMetadata[0].EvidenceID = "spacing#9" },
		"digest dropped":               func(r *evidence.CampaignReport) { r.CommandDigests = r.CommandDigests[:len(r.CommandDigests)-1] },
		"attempt detail leaked":        func(r *evidence.CampaignReport) { r.Experiments[0].Attempts[0].Detail = "raw detail" },
		"attempt argv leaked":          func(r *evidence.CampaignReport) { r.Experiments[0].Attempts[0].Observations[0].Args = []string{"x"} },
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := evidence.DecodeCampaignV5(out)
			tamper(&c)
			if evidence.ValidateCampaign(c) == nil {
				t.Fatal("tampered campaign accepted")
			}
		})
	}
	// An old campaign that references no v1alpha4 harness stays v1alpha4.
	mut := setupCase(t, caseNamed(t, "campaign-mutation"))
	_, plain, _ := runBinary(t, mut, nil, "campaign", "campaign.json")
	if !bytes.Contains(plain, []byte(`"invaris.evidence/v1alpha4"`)) {
		t.Fatal("old campaign changed version")
	}
	plainCode, _, _ := runBinary(t, mut, nil, "campaign", "campaign.json")
	optedCode, opted, _ := runBinary(t, mut, nil, "campaign", "--evidence-version", "v1alpha5", "campaign.json")
	o, err := evidence.DecodeCampaignV5(opted)
	if err != nil {
		t.Fatalf("opted-in mutation campaign: %v", err)
	}
	// Behavioral mutations never appear in guardrail rows, and the profile
	// changes neither the decision, the exit code nor the score units.
	if len(o.Effectiveness.ByGuard)+len(o.Effectiveness.ByOperator) != 0 || optedCode != plainCode || o.MutationSummary.Score == nil || *o.MutationSummary.Score != 0.5 {
		t.Fatalf("opted-in campaign changed: exit %d vs %d, %+v", optedCode, plainCode, o.Effectiveness)
	}
}

func caseNamed(t *testing.T, name string) compatCase {
	t.Helper()
	for _, c := range compatCases() {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no compatibility case %q", name)
	return compatCase{}
}
