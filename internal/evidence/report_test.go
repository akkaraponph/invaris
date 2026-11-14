package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
)

func ptr[T any](v T) *T { return &v }

// accepted returns a fully populated ACCEPT report that passes Validate.
func accepted() Report {
	t0 := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	return Report{
		SchemaVersion: SchemaVersion, RunID: strings.Repeat("ab", 16), Action: "run", Target: "/work", Harness: "h",
		ConfigDigest: ptr("sha256:" + strings.Repeat("0", 64)), Environment: Environment{OS: "linux", Arch: "amd64"},
		StartedAt: t0, FinishedAt: t0.Add(time.Second), DurationMs: 1000,
		Compatibility: Compatibility{Status: "native", InputVersion: "invaris.dev/v1alpha2"},
		Policies:      []domain.Policy{{ID: "p1"}},
		Gate:          domain.Gate{RequiredGuards: []string{"g"}, RequiredProofs: []string{"c"}, Precedence: domain.FailureFirst},
		Steps:         []domain.Check{{ID: "s", Status: domain.Pass, ExitCode: ptr(0)}},
		Checks:        []domain.Check{{ID: "g", PolicyID: "p1", Status: domain.Pass, Inspected: ptr(3), ExitCode: ptr(0)}},
		Proofs: []domain.Proof{{ID: "c", PolicyID: "p1", GuardID: "g", Target: "src/a.js", FaultID: "fault-1", EvidenceStrength: domain.StrengthStrong, Status: domain.Proven,
			Mutation:   &domain.Mutation{Path: "src/a.js", BeforeSHA256: strings.Repeat("a", 64), AfterSHA256: strings.Repeat("b", 64)},
			Stages:     []domain.Stage{{Name: domain.StageCleanup, Status: domain.StageOK}},
			Baseline:   &domain.Check{ID: "c:baseline", Status: domain.Pass, Inspected: ptr(3)},
			Challenged: &domain.Check{ID: "c:challenged", Status: domain.Fail, Inspected: ptr(3), Findings: []domain.Finding{{PolicyID: "p1", RuleID: "p1", Path: "src/a.js"}}}}},
		TargetCount: ptr(4), Decision: domain.Accept,
		Artifacts: []Artifact{{Path: "out.log", SHA256: strings.Repeat("c", 64), Bytes: 5}},
	}
}

func TestEvidenceRejectsIncompleteSuccess(t *testing.T) {
	if err := Validate(accepted()); err != nil {
		t.Fatalf("complete accepted report invalid: %v", err)
	}
	for name, mutate := range map[string]func(*Report){
		"config digest removed":      func(r *Report) { r.ConfigDigest = nil },
		"malformed digest":           func(r *Report) { r.ConfigDigest = ptr("sha256:abc") },
		"baseline removed":           func(r *Report) { r.Proofs[0].Baseline = nil },
		"baseline failing":           func(r *Report) { r.Proofs[0].Baseline.Status = domain.Fail },
		"challenged removed":         func(r *Report) { r.Proofs[0].Challenged = nil },
		"target count removed":       func(r *Report) { r.TargetCount = nil },
		"target count zero":          func(r *Report) { r.TargetCount = ptr(0) },
		"version removed":            func(r *Report) { r.SchemaVersion = "" },
		"old version":                func(r *Report) { r.SchemaVersion = "invaris.evidence/v1alpha1" },
		"artifact bytes removed":     func(r *Report) { r.Artifacts[0].Bytes = 0 },
		"artifact hash removed":      func(r *Report) { r.Artifacts[0].SHA256 = "" },
		"run id removed":             func(r *Report) { r.RunID = "" },
		"inspected removed":          func(r *Report) { r.Checks[0].Inspected = nil },
		"inspected zero":             func(r *Report) { r.Checks[0].Inspected = ptr(0) },
		"required guard missing":     func(r *Report) { r.Checks = nil },
		"required proof missing":     func(r *Report) { r.Proofs = nil },
		"fail hidden as accept":      func(r *Report) { r.Checks[0].Status = domain.Fail },
		"unproven hidden":            func(r *Report) { r.Proofs[0].Status = domain.Unproven },
		"proven without mutation":    func(r *Report) { r.Proofs[0].Mutation = nil },
		"proven mutation other file": func(r *Report) { r.Proofs[0].Mutation.Path = "src/b.js" },
		"proven identical hashes":    func(r *Report) { r.Proofs[0].Mutation.AfterSHA256 = r.Proofs[0].Mutation.BeforeSHA256 },
		"proven malformed hash":      func(r *Report) { r.Proofs[0].Mutation.AfterSHA256 = "xyz" },
		"proven cleanup failed":      func(r *Report) { r.Proofs[0].Stages[0].Status = domain.StageFailed },
		"proven cleanup missing":     func(r *Report) { r.Proofs[0].Stages = nil },
		"weak proof without allow":   func(r *Report) { r.Proofs[0].EvidenceStrength = domain.StrengthWeak },
		"strong proof wrong path":    func(r *Report) { r.Proofs[0].Challenged.Findings[0].Path = "other.js" },
		"invalid status":             func(r *Report) { r.Checks[0].Status = "MAYBE" },
		"bad precedence":             func(r *Report) { r.Gate.Precedence = "" },
		"compat status removed":      func(r *Report) { r.Compatibility.Status = "" },
	} {
		r := accepted()
		mutate(&r)
		if err := Validate(r); err == nil {
			t.Errorf("%s: validated as accepted", name)
		}
		if _, err := Marshal(r); err == nil {
			t.Errorf("%s: marshalled", name)
		}
	}
}

func TestEvidenceFailureReportsRemainSerializable(t *testing.T) {
	r := accepted()
	r.ConfigDigest, r.TargetCount, r.Artifacts = nil, nil, nil
	r.Proofs = nil
	r.Gate.RequiredProofs = nil
	r.Checks[0].Status, r.Checks[0].Findings = domain.Fail, []domain.Finding{{PolicyID: "p1", RuleID: "p1", Path: "a"}}
	r.Decision = domain.Reject
	data, err := Marshal(r)
	if err != nil {
		t.Fatalf("incomplete failure report not serializable: %v", err)
	}
	for _, want := range []string{`"configDigest": null`, `"targetCount": null`, `"gitRevision": null`, `"artifacts": []`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("missing explicit %s in %s", want, data)
		}
	}
	// A recorded decision that disagrees with the results is invalid.
	r.Decision = domain.RefuseDecision
	if Validate(r) == nil {
		t.Fatal("REFUSE recorded for a result set that rejects")
	}
}

func TestEvidenceMarshalDeterministic(t *testing.T) {
	a, err := Marshal(accepted())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, _ := Marshal(accepted())
		if !bytes.Equal(a, b) {
			t.Fatal("same report marshalled to different bytes")
		}
	}
	// nil and empty lists serialize identically.
	r1, r2 := accepted(), accepted()
	r1.Diagnostics, r2.Diagnostics = nil, []domain.Diagnostic{}
	b1, _ := Marshal(r1)
	b2, _ := Marshal(r2)
	if !bytes.Equal(b1, b2) {
		t.Fatal("nil and empty slices differ")
	}
	r3 := accepted()
	r3.RunID = strings.Repeat("cd", 16)
	if b3, _ := Marshal(r3); bytes.Equal(a, b3) {
		t.Fatal("different run IDs produced equal bytes")
	}
	if !bytes.HasSuffix(a, []byte("}\n")) || !strings.Contains(string(a), `"schemaVersion": "invaris.evidence/v1alpha2"`) {
		t.Fatalf("unexpected framing: %s", a)
	}
}

func TestRedact(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		secrets        []string
	}{
		{"literal", "token=hunter2!", "token=" + Marker + "!", []string{"hunter2"}},
		{"repeated", "a hunter2 b hunter2", "a " + Marker + " b " + Marker, []string{"hunter2"}},
		{"longest first", "abcdef", Marker, []string{"abc", "abcdef"}},
		{"overlap merged", "xabcdy", "x" + Marker + "y", []string{"abc", "bcd"}},
		{"matches found out of text order", "ab xyz ab", Marker + " " + Marker + " " + Marker, []string{"xyz", "ab"}},
		{"empty secret ignored", "abc", "abc", []string{"", ""}},
		{"no secrets", "abc", "abc", nil},
		{"not re-matched in marker", "aREDb", "a" + Marker + "b", []string{"RED"}},
	} {
		if got := string(Redact([]byte(tc.in), tc.secrets)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := string(RedactTruncated([]byte("out hunt"), []string{"hunter2"})); got != "out "+Marker {
		t.Errorf("truncated prefix leaked: %q", got)
	}
	if got := string(Redact([]byte("out hunt"), []string{"hunter2"})); got != "out hunt" {
		t.Errorf("non-truncated partial changed: %q", got)
	}
	in := []byte("secret")
	Redact(in, []string{"secret"})
	if string(in) != "secret" {
		t.Fatal("Redact modified its input")
	}
}

func TestRedactReportCoversEveryString(t *testing.T) {
	r := accepted()
	const s = "TOPSECRET-VALUE"
	r.Harness = "h " + s
	r.Steps[0].Diagnostics = []domain.Diagnostic{{Code: "C", Message: s}}
	r.Checks[0].Findings = []domain.Finding{{Message: s}}
	r.Diagnostics = []domain.Diagnostic{{Message: s}}
	r.Observations = []Observation{{ID: "o", Args: []string{"--token", s}, Program: "p", Stdout: "out " + s, Stderr: s, Truncated: true}}
	r.Observations[0].Stdout += " TOPSECRET-VA"
	RedactReport(&r, []string{s})
	r.Decision = domain.Reject
	r.Checks[0].Status, r.Checks[0].Findings[0].PolicyID = domain.Fail, "p1"
	data, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TOPSECRET") {
		t.Fatalf("secret survived serialization:\n%s", data)
	}
	if !strings.Contains(string(data), Marker) {
		t.Fatal("no redaction marker")
	}
}

func TestPersistArtifact(t *testing.T) {
	dir := t.TempDir()
	art, err := PersistRedacted(dir, "log.txt", []byte("a hunter2 b"), []string{"hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "log.txt"))
	sum := sha256.Sum256(disk)
	if string(disk) != "a "+Marker+" b" || art.SHA256 != hex.EncodeToString(sum[:]) || art.Bytes != int64(len(disk)) || art.Path != "log.txt" {
		t.Fatalf("artifact does not describe persisted bytes: %+v %q", art, disk)
	}
	if _, err := PersistArtifact(dir, "log.txt", []byte("other")); err == nil {
		t.Fatal("overwrote existing artifact")
	}
	if again, _ := os.ReadFile(filepath.Join(dir, "log.txt")); !bytes.Equal(again, disk) {
		t.Fatal("existing artifact changed")
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "../x"} {
		if _, err := PersistArtifact(dir, name, []byte("x")); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	if _, err := PersistArtifact(filepath.Join(dir, "missing"), "x", []byte("x")); err == nil {
		t.Fatal("persisted into missing directory")
	}
}
