package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

const v2Head = `"version":"invaris.dev/v1alpha2","name":"v2","trustMode":"trusted-local","networkPolicy":"unrestricted"`

func mustSpec(t *testing.T, body string) Specification {
	t.Helper()
	s, err := decodeSpecification([]byte("{" + body + "}"))
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return s
}

func marshaled(t *testing.T, r evidence.Report) string {
	t.Helper()
	data, err := evidence.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestV1ConfigsEmitV2Evidence(t *testing.T) {
	for _, dir := range []string{"../../examples", "../../examples/structured"} {
		spec, err := Load(filepath.Join(dir, "invaris.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"check", "challenge"} {
			r := RunEvidence(context.Background(), spec, dir, action)
			if r.SchemaVersion != "invaris.evidence/v1alpha2" || r.Decision != domain.Accept || r.Compatibility.Status != "legacy" || len(r.Compatibility.Warnings) == 0 {
				t.Fatalf("%s %s: unexpected report: decision=%s compat=%+v diag=%+v", dir, action, r.Decision, r.Compatibility, r.Diagnostics)
			}
			if err := evidence.Validate(r); err != nil {
				t.Fatalf("%s %s: %v", dir, action, err)
			}
			if r.ConfigDigest == nil || r.TargetCount == nil || *r.TargetCount <= 0 {
				t.Fatalf("incomplete accepted evidence: %+v", r)
			}
		}
	}
}

func TestV2ConfigContract(t *testing.T) {
	good := v2Head + `,"guards":[{"id":"g","program":"true"}]`
	if s := mustSpec(t, good); s.WritablePaths == nil || len(s.WritablePaths) != 0 || s.TrustMode != "trusted-local" {
		t.Fatalf("v2 must default to no mutation: %+v", s)
	}
	for name, body := range map[string]string{
		"missing trustMode":        `"version":"invaris.dev/v1alpha2","name":"x","networkPolicy":"unrestricted","guards":[{"id":"g","program":"true"}]`,
		"unsupported trust":        strings.Replace(good, "trusted-local", "sandboxed", 1),
		"missing network policy":   `"version":"invaris.dev/v1alpha2","name":"x","trustMode":"trusted-local","guards":[{"id":"g","program":"true"}]`,
		"network restricted":       strings.Replace(good, "unrestricted", "none", 1),
		"duplicate key":            good + `,"name":"again"`,
		"unknown field":            good + `,"extra":1`,
		"gate unknown guard":       good + `,"gate":{"requiredGuards":["nope"],"precedence":"failure-first"}`,
		"gate unknown proof":       good + `,"gate":{"requiredProofs":["nope"],"precedence":"failure-first"}`,
		"gate bad precedence":      good + `,"gate":{"precedence":"whatever"}`,
		"duplicate policy":         good + `,"policies":[{"id":"a"},{"id":"a"}]`,
		"bad redactEnv":            good + `,"redactEnv":["A=B"]`,
		"duplicate redactEnv":      good + `,"redactEnv":["A","A"]`,
		"mixed guard forms":        v2Head + `,"guards":[{"id":"g","program":"true","adapterId":"a"}]`,
		"command with scope":       v2Head + `,"guards":[{"id":"g","program":"true","scope":["src"]}]`,
		"null options":             v2Head + `,"adapters":[{"id":"a","program":"p","args":[]}],"guards":[{"id":"g","adapterId":"a","policyId":"p","scope":["src"],"options":null}]`,
		"duplicate options key":    v2Head + `,"adapters":[{"id":"a","program":"p","args":[]}],"guards":[{"id":"g","adapterId":"a","policyId":"p","scope":["src"],"options":{"k":1,"k":2}}]`,
		"provider guard declared":  v2Head + `,"adapters":[{"id":"a","program":"p","args":[]}],"guards":[{"id":"g","adapterId":"a","policyId":"p","scope":["src"],"options":{"k":1}}]`,
		"required adapter":         good + `,"requiredAdapters":["a"]`,
		"policy list of strings":   good + `,"policies":["a"]`,
		"v1 policy object":         `"version":"invaris.dev/v1alpha1","name":"x","policies":[{"id":"a"}],"guards":[{"id":"g","program":"true"}]`,
		"unsupported next version": strings.Replace(good, "v1alpha2", "v1alpha3", 1),
	} {
		if _, err := decodeSpecification([]byte("{" + body + "}")); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A refused provider guard never executes anything.
	root := t.TempDir()
	spec := Specification{Version: versionV2, Name: "p", TrustMode: "trusted-local", NetworkPolicy: "unrestricted",
		Steps:          []Command{{ID: "s", Program: "touch", Args: []string{"sentinel"}}},
		ProviderGuards: []ProviderGuard{{ID: "pg", AdapterID: "a", PolicyID: "p", Scope: []string{"src"}}}}
	if r := RunEvidence(context.Background(), spec, root, "run"); r.Decision != domain.RefuseDecision {
		t.Fatalf("provider guard ran: %s", r.Decision)
	}
	if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
		t.Fatal("step executed despite refused provider guard")
	}
}

func TestV2GatePrecedenceModes(t *testing.T) {
	// One guard rejects (exit 1), another refuses (missing executable).
	body := func(precedence string) string {
		return v2Head + `,"guards":[{"id":"fails","program":"sh","args":["-c","exit 1"]},{"id":"refuses","program":"invaris-missing-executable-4242"}],` +
			`"gate":{"requiredGuards":["fails","refuses"],"precedence":"` + precedence + `"}`
	}
	for precedence, want := range map[string]domain.Decision{domain.FailureFirst: domain.Reject, domain.RefusalFirst: domain.RefuseDecision} {
		r := RunEvidence(context.Background(), mustSpec(t, body(precedence)), t.TempDir(), "check")
		if r.Decision != want {
			t.Errorf("%s: got %s, want %s", precedence, r.Decision, want)
		}
		statuses := map[string]domain.CheckStatus{}
		for _, c := range r.Checks {
			statuses[c.ID] = c.Status
		}
		if statuses["fails"] != domain.Fail || statuses["refuses"] != domain.Refuse {
			t.Errorf("%s: individual results not retained: %+v", precedence, statuses)
		}
		if err := evidence.Validate(r); err != nil {
			t.Errorf("%s: %v", precedence, err)
		}
	}
	// Default precedence is failure-first.
	r := RunEvidence(context.Background(), mustSpec(t, v2Head+`,"guards":[{"id":"fails","program":"sh","args":["-c","exit 1"]},{"id":"refuses","program":"invaris-missing-executable-4242"}],"gate":{"requiredGuards":["fails","refuses"]}`), t.TempDir(), "check")
	if r.Decision != domain.Reject || r.Gate.Precedence != domain.FailureFirst {
		t.Fatalf("default precedence: %s %q", r.Decision, r.Gate.Precedence)
	}
}

func TestV2WeakProofHandling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("SAFE"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := func(gate string) string {
		return v2Head + `,"writablePaths":["source.txt"],"guards":[{"id":"g","program":"sh","args":["-c","if grep -q BAD source.txt; then echo BAD; exit 1; fi"]}],` +
			`"challenges":[{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}],"gate":` + gate
	}
	denied := RunEvidence(context.Background(), mustSpec(t, body(`{"requiredProofs":["c"],"precedence":"failure-first"}`)), dir, "challenge")
	if denied.Decision != domain.Reject || denied.Proofs[0].Status != domain.Proven || denied.Proofs[0].EvidenceStrength != domain.StrengthWeak {
		t.Fatalf("weak proof must reject without allowWeakEvidence: %s %+v", denied.Decision, denied.Proofs)
	}
	// With no configured gate, v1alpha2 requires strong evidence by default.
	noGate := strings.Replace(body(`{}`), `,"gate":{}`, "", 1)
	if r := RunEvidence(context.Background(), mustSpec(t, noGate), dir, "challenge"); r.Decision != domain.Reject || r.Gate.AllowWeakEvidence {
		t.Fatalf("default v1alpha2 gate accepted weak proof: %s %+v", r.Decision, r.Gate)
	}
	allowed := RunEvidence(context.Background(), mustSpec(t, body(`{"requiredProofs":["c"],"allowWeakEvidence":true}`)), dir, "challenge")
	if allowed.Decision != domain.Accept {
		t.Fatalf("permitted weak proof: %s %+v", allowed.Decision, allowed.Diagnostics)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "source.txt")); string(b) != "SAFE" {
		t.Fatal("source changed")
	}
	// Structured (strong) evidence is accepted without the allowance.
	sdir := "../../examples/structured"
	spec, _ := Load(filepath.Join(sdir, "invaris.json"))
	spec.Gate = &domain.Gate{RequiredProofs: []string{"detect-marker"}, Precedence: domain.FailureFirst}
	if r := RunEvidence(context.Background(), spec, sdir, "challenge"); r.Decision != domain.Accept || r.Proofs[0].EvidenceStrength != domain.StrengthStrong {
		t.Fatalf("strong proof: %s %+v", r.Decision, r.Proofs)
	}
}

func TestMissingRequiredResultsCannotAccept(t *testing.T) {
	// A gate entry the invoked action cannot produce is not applied to it: a
	// proof-only gate leaves `check` with nothing required, which is refused as
	// empty work, and a guard-only gate does the same for `challenge`.
	cfg := func(gate string) string {
		return v2Head + `,"guards":[{"id":"g","program":"true"}],"challenges":[{"id":"c","guard":"g","file":"f","before":"a","after":"b","expectedOutput":"b"}],"writablePaths":["f"],"gate":` + gate
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ gate, action string }{
		"proof-only gate, check":     {`{"requiredProofs":["c"]}`, "check"},
		"guard-only gate, challenge": {`{"requiredGuards":["g"]}`, "challenge"},
		"empty gate, check":          {`{}`, "check"},
	} {
		if r := RunEvidence(context.Background(), mustSpec(t, cfg(tc.gate)), dir, tc.action); r.Decision != domain.RefuseDecision {
			t.Errorf("%s: %s", name, r.Decision)
		}
	}
	both := `{"requiredGuards":["g"],"requiredProofs":["c"],"allowWeakEvidence":true}`
	for _, action := range []string{"check", "challenge"} {
		r := RunEvidence(context.Background(), mustSpec(t, cfg(both)), dir, action)
		if action == "check" && (r.Decision != domain.Accept || len(r.Gate.RequiredProofs) != 0) {
			t.Errorf("check with combined gate: %s gate=%+v", r.Decision, r.Gate)
		}
		if action == "challenge" && r.Decision != domain.Reject { // the guard never fails: UNPROVEN
			t.Errorf("challenge with combined gate: %s", r.Decision)
		}
	}
	// failFast leaves the required guard unrun.
	r := RunEvidence(context.Background(), mustSpec(t, v2Head+`,"steps":[{"id":"s","program":"false"}],"guards":[{"id":"g","program":"true"}],"alwaysRunGuards":false`), t.TempDir(), "run")
	if r.Decision == domain.Accept {
		t.Fatalf("unrun guard accepted: %+v", r.Checks)
	}
	// No work at all, and invalid configuration, never accept.
	for _, spec := range []Specification{{Version: versionV2, Name: "empty", TrustMode: "trusted-local", NetworkPolicy: "unrestricted"}, {Version: "bogus", Name: "x"}} {
		if r := RunEvidence(context.Background(), spec, t.TempDir(), "check"); r.Decision != domain.RefuseDecision || evidence.Validate(r) != nil {
			t.Fatalf("%q: %s %v", spec.Version, r.Decision, evidence.Validate(r))
		}
	}
}

func TestIncompleteEvidenceIsDowngraded(t *testing.T) {
	// A guard that passes with zero inspected targets would otherwise accept.
	spec := mustSpec(t, v2Head+`,"policies":[{"id":"p"}],"guards":[{"id":"g","program":"true","policyId":"p"}]`)
	legacy := Report{Harness: spec.Name, Action: "check", Status: "PASS", StartedAt: testTime, FinishedAt: testTime,
		Results: []Result{{ID: "g", Status: "PASS", Inspection: &Inspection{Version: "invaris.inspection/v1alpha1", PolicyID: "p", Inspected: 0, Findings: []Finding{}}}}}
	r := buildEvidence(context.Background(), spec, t.TempDir(), "check", legacy)
	if r.Decision != domain.RefuseDecision {
		t.Fatalf("zero-target pass accepted: %s", r.Decision)
	}
	found := false
	for _, d := range r.Diagnostics {
		found = found || d.Code == "EVIDENCE_INCOMPLETE"
	}
	if !found || evidence.Validate(r) != nil {
		t.Fatalf("downgrade not recorded or inconsistent: %+v / %v", r.Diagnostics, evidence.Validate(r))
	}
}

func TestEvidenceRedactsConfiguredSecrets(t *testing.T) {
	const secret = "s3cr3t-token-VALUE"
	t.Setenv("INVARIS_TEST_SECRET", secret)
	t.Setenv("INVARIS_TEST_PUBLIC", "public-value")
	script := `printf '%s\n' "$INVARIS_TEST_SECRET"; printf '%s\n' "$INVARIS_TEST_SECRET" >&2; echo "$INVARIS_TEST_PUBLIC"; exit 1`
	body := v2Head + `,"redactEnv":["INVARIS_TEST_SECRET","INVARIS_TEST_UNSET"],"steps":[{"id":"s","program":"sh","args":["-c",` + jsonString(script) + `,"--token=` + secret + `"],"envAllowlist":["INVARIS_TEST_SECRET","INVARIS_TEST_PUBLIC"]}],"guards":[{"id":"g","program":"true"}]`
	r := RunEvidence(context.Background(), mustSpec(t, body), t.TempDir(), "run")
	out := marshaled(t, r)
	if strings.Contains(out, secret) {
		t.Fatalf("secret in serialized evidence:\n%s", out)
	}
	if !strings.Contains(out, evidence.Marker) || !strings.Contains(out, "public-value") {
		t.Fatalf("expected marker and unredacted public value:\n%s", out)
	}
	if r.Decision != domain.Reject {
		t.Fatalf("failing step: %s", r.Decision)
	}
	// Redaction happens before persistence of any artifact as well.
	art, err := evidence.PersistRedacted(t.TempDir(), "raw.log", []byte("x "+secret), secretsFor(mustSpec(t, body)))
	if err != nil || art.Bytes != int64(len("x "+evidence.Marker)) {
		t.Fatalf("artifact not redacted: %+v %v", art, err)
	}
	// The harness cannot cause redactEnv values to be serialized.
	if strings.Contains(out, "INVARIS_TEST_UNSET\":") || strings.Contains(out, "redactEnv") {
		t.Fatal("redactEnv leaked into evidence")
	}
}

func TestExcerptsBoundedUnlessSensitiveStorage(t *testing.T) {
	big := v2Head + `,"guards":[{"id":"g","program":"sh","args":["-c","head -c 20000 /dev/zero | tr '\\0' x"]}]`
	r := RunEvidence(context.Background(), mustSpec(t, big), t.TempDir(), "check")
	if o := r.Observations[0]; len(o.Stdout) != excerptLimit || !o.Truncated {
		t.Fatalf("default excerpt: %d truncated=%v", len(o.Stdout), o.Truncated)
	}
	r = RunEvidence(context.Background(), mustSpec(t, big+`,"storeSensitiveOutput":true`), t.TempDir(), "check")
	if o := r.Observations[0]; len(o.Stdout) != 20000 || o.Truncated {
		t.Fatalf("sensitive excerpt: %d truncated=%v", len(o.Stdout), o.Truncated)
	}
}

func TestEvidenceDeterministicForSameResults(t *testing.T) {
	spec, _ := Load("../../examples/invaris.json")
	r := RunEvidence(context.Background(), spec, "../../examples", "check")
	if marshaled(t, r) != marshaled(t, r) {
		t.Fatal("same report marshalled differently")
	}
	other := RunEvidence(context.Background(), spec, "../../examples", "check")
	if other.RunID == r.RunID || len(r.RunID) != 32 {
		t.Fatalf("run IDs must be unique 128-bit hex: %q %q", r.RunID, other.RunID)
	}
}

var testTime = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
