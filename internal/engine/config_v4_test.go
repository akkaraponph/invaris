package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/mutation"
	"github.com/akkaraponph/invaris/internal/workspace"
)

const scanScript = `set -eu
policy=$1 file=$2 mode=${3:-normal}
cr=$(printf '\r')
case "$policy" in
  ws) grep -q '[[:blank:]]$' "$file" && found=1 || found=0 ;;
  crlf) grep -q "$cr" "$file" && found=1 || found=0 ;;
  secret) grep -q INVARIS_TEST_TOKEN_DO_NOT_USE_00000000 "$file" && found=1 || found=0 ;;
esac
if [ "$mode" = noop ]; then found=0; fi
if [ "$found" = 1 ]; then
  path=$file; [ "$mode" = wrongpath ] && path=other.txt
  extra=""; [ "$mode" = twice ] && extra=",{\"policyId\":\"$policy\",\"path\":\"$file\"}"
  printf '{"version":"invaris.inspection/v1alpha1","policyId":"%s","inspected":1,"findings":[{"policyId":"%s","path":"%s"}%s]}\n' "$policy" "$policy" "$path" "$extra"
  exit 1
fi
printf '{"version":"invaris.inspection/v1alpha1","policyId":"%s","inspected":1,"findings":[]}\n' "$policy"
`

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// v4Project writes a project with one built-in challenge and returns its
// root and harness bytes. op selects the operator; mode the scanner's lie.
func v4Project(t *testing.T, op, mode, content string, edit func(map[string]any)) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scan.sh"), []byte(scanScript), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := map[string]string{mutation.TrailingWhitespace: "ws", mutation.CRLFInjection: "crlf", mutation.SecretLeakInjection: "secret"}[op]
	args := []string{"scan.sh", policy, "target.txt"}
	if mode != "" {
		args = append(args, mode)
	}
	m := map[string]any{"operator": op, "version": "1", "preimageSha256": sha([]byte(content))}
	if op == mutation.SecretLeakInjection {
		m["fixture"] = mutation.SecretFixture
	}
	h := map[string]any{"version": versionV4, "name": "v4", "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"writablePaths": []string{"target.txt"}, "policies": []any{map[string]any{"id": policy}},
		"guards":     []any{map[string]any{"id": "g", "policyId": policy, "program": "sh", "args": args, "minInspected": 1, "timeoutSeconds": 10}},
		"challenges": []any{map[string]any{"id": "c", "guard": "g", "file": "target.txt", "mutation": m}}}
	if edit != nil {
		edit(h)
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "invaris.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, raw
}

func TestV4DecodesBuiltinAndLiteralChallenges(t *testing.T) {
	spec, err := Load("../../examples/operators/invaris.json")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Version != versionV4 || len(spec.Challenges) != 3 {
		t.Fatalf("spec %+v", spec)
	}
	for _, c := range spec.Challenges {
		if c.Builtin == nil || c.Before != "" || c.After != "" {
			t.Fatalf("challenge %+v", c)
		}
	}
	if spec.Challenges[2].Builtin.Fixture != mutation.SecretFixture {
		t.Fatal("fixture lost")
	}
	for action, want := range map[string]bool{"run": true, "check": true, "challenge": true, "mutate": false, "campaign": false} {
		if spec.SupportsAction(action) != want {
			t.Errorf("SupportsAction(%s) != %v", action, want)
		}
	}
	// A literal challenge stays legal but is strong-by-default: weak marker
	// evidence is not permitted without an explicit gate.
	raw := `{"version":"invaris.dev/v1alpha4","name":"lit","trustMode":"trusted-local","networkPolicy":"unrestricted","writablePaths":["a"],` +
		`"guards":[{"id":"g","program":"true"}],"challenges":[{"id":"c","guard":"g","file":"a","before":"x","after":"y","expectedOutput":"y"}]}`
	lit, err := Decode([]byte(raw))
	if err != nil || lit.Challenges[0].Builtin != nil || lit.Challenges[0].Before != "x" {
		t.Fatalf("literal v4: %+v %v", lit, err)
	}
	if effectiveGate(lit, "challenge").AllowWeakEvidence || effectiveGate(spec, "check").AllowWeakEvidence {
		t.Fatal("v1alpha4 must not permit weak evidence by default")
	}
}

func TestV4RejectsInvalidDeclarations(t *testing.T) {
	_, base := v4Project(t, mutation.TrailingWhitespace, "", "a\n", nil)
	h := string(base)
	mut := func(old, new string) string {
		if !strings.Contains(h, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(h, old, new, 1)
	}
	m := `"mutation":{"operator":"trailing-whitespace","preimageSha256":"` + sha([]byte("a\n")) + `","version":"1"}`
	if !strings.Contains(h, m) {
		t.Fatalf("unexpected fixture encoding: %s", h)
	}
	for name, raw := range map[string]string{
		"mutation with before":            mut(m, m+`,"before":"a"`),
		"mutation with empty after":       mut(m, m+`,"after":""`),
		"mutation with expectedOutput":    mut(m, m+`,"expectedOutput":""`),
		"null mutation":                   mut(m, `"mutation":null`),
		"empty mutation":                  mut(m, `"mutation":{}`),
		"unknown operator":                mut("trailing-whitespace\",\"preimage", "replace\",\"preimage"),
		"unknown version":                 mut(`"version":"1"}`, `"version":"2"}`),
		"missing version":                 mut(`,"version":"1"}`, `}`),
		"uppercase preimage":              mut(sha([]byte("a\n")), strings.ToUpper(sha([]byte("a\n")))),
		"short preimage":                  mut(sha([]byte("a\n")), "abc"),
		"fixture on whitespace":           mut(`"version":"1"}`, `"version":"1","fixture":""}`),
		"unknown option":                  mut(`"version":"1"}`, `"version":"1","seed":1}`),
		"duplicate key":                   mut(`"version":"1"}`, `"version":"1","version":"1"}`),
		"marker guard":                    mut(`"policyId":"ws",`, ``),
		"target not writable":             mut(`"writablePaths":["target.txt"]`, `"writablePaths":[]`),
		"writablePaths omitted":           mut(`,"writablePaths":["target.txt"]`, ``),
		"null challenge element":          mut(`"challenges":[`, `"challenges":[null,`),
		"trust omitted":                   mut(`"trustMode":"trusted-local",`, ``),
		"mutation in a v2 harness":        mut(versionV4, versionV2),
		"literal without before or after": mut(m, `"expectedOutput":"x"`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(raw)); err == nil {
				t.Fatalf("accepted:\n%s", raw)
			}
		})
	}
	secret := func(fixture string) string {
		_, raw := v4Project(t, mutation.SecretLeakInjection, "", "a\n", func(h map[string]any) {
			c := h["challenges"].([]any)[0].(map[string]any)
			mm := c["mutation"].(map[string]any)
			if fixture == "-" {
				delete(mm, "fixture")
			} else {
				mm["fixture"] = fixture
			}
		})
		return string(raw)
	}
	for _, f := range []string{"-", "", "real-token"} {
		if _, err := Decode([]byte(secret(f))); err == nil {
			t.Errorf("secret fixture %q accepted", f)
		}
	}
	if _, err := Decode([]byte(secret(mutation.SecretFixture))); err != nil {
		t.Fatalf("valid secret declaration refused: %v", err)
	}
	// Only the v1alpha4 decoder may set Builtin.
	spec, _ := Decode([]byte(string(base)))
	spec.Version = versionV2
	if spec.Validate() == nil {
		t.Fatal("built-in challenge accepted in v1alpha2")
	}
}

func TestBuiltinProofOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, op, mode, content string
		preimage                string
		status                  domain.ProofStatus
		reason, application     string
	}{
		{"whitespace detected", mutation.TrailingWhitespace, "", "a\nb\n", "", domain.Proven, domain.ReasonGuardDetected, evidence.Applied},
		{"crlf detected", mutation.CRLFInjection, "", "a\nb\n", "", domain.Proven, domain.ReasonGuardDetected, evidence.Applied},
		{"secret detected", mutation.SecretLeakInjection, "", "K=v\n", "", domain.Proven, domain.ReasonGuardDetected, evidence.Applied},
		{"no-op guard survives", mutation.TrailingWhitespace, "noop", "a\n", "", domain.Unproven, domain.ReasonGuardPassed, evidence.Applied},
		{"wrong path", mutation.CRLFInjection, "wrongpath", "a\n", "", domain.Inconclusive, domain.ReasonFindingMismatch, evidence.Applied},
		{"two findings", mutation.SecretLeakInjection, "twice", "K=v\n", "", domain.Inconclusive, domain.ReasonFindingMismatch, evidence.Applied},
		{"preimage mismatch", mutation.TrailingWhitespace, "", "a\n", sha([]byte("other\n")), domain.Inconclusive, domain.ReasonMutationRejected, evidence.Rejected},
		{"ineligible CRLF file", mutation.TrailingWhitespace, "", "a\r\n", "", domain.Inconclusive, domain.ReasonMutationRejected, evidence.Rejected},
		{"unhealthy baseline", mutation.SecretLeakInjection, "", "K=" + mutation.SecretToken + "\n", "", domain.Inconclusive, domain.ReasonUnhealthyBaseline, evidence.NotApplied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := v4Project(t, tc.op, tc.mode, tc.content, func(h map[string]any) {
				if tc.preimage != "" {
					h["challenges"].([]any)[0].(map[string]any)["mutation"].(map[string]any)["preimageSha256"] = tc.preimage
				}
			})
			before, _ := workspace.Snapshot(root)
			spec, err := Load(filepath.Join(root, "invaris.json"))
			if err != nil {
				t.Fatal(err)
			}
			r := RunEvidenceV5(context.Background(), spec, root, "challenge")
			if err := evidence.Validate(r); err != nil {
				t.Fatalf("invalid v5 report: %v", err)
			}
			p := r.Proofs[0]
			if p.Status != tc.status || p.Reason != tc.reason || p.EvidenceStrength != domain.StrengthStrong {
				t.Fatalf("proof %s/%s/%s", p.Status, p.Reason, p.EvidenceStrength)
			}
			if len(r.MutationMetadata) != 1 || r.MutationMetadata[0].Application != tc.application {
				t.Fatalf("metadata %+v", r.MutationMetadata)
			}
			md := r.MutationMetadata[0]
			if (md.ReproductionID != nil) != (tc.application == evidence.Applied) || (p.Mutation != nil) != (tc.application == evidence.Applied) {
				t.Fatalf("applied edit and identity disagree: %+v %+v", md, p.Mutation)
			}
			if tc.application == evidence.Applied && *md.BeforeSHA256 != sha([]byte(tc.content)) {
				t.Fatal("before hash is not the preimage")
			}
			after, _ := workspace.Snapshot(root)
			if d := workspace.Diff(before, after); len(d) != 0 {
				t.Fatalf("source changed: %v", d)
			}
		})
	}
}

func TestBuiltinCleanupFailureCannotCertify(t *testing.T) {
	root, _ := v4Project(t, mutation.TrailingWhitespace, "", "a\n", nil)
	spec, err := Load(filepath.Join(root, "invaris.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := spec.Challenges[0]
	g := spec.Guards[0]
	evaluate := func(ctx context.Context, workRoot string) domain.Check {
		return toCheck(checkGuard(ctx, workRoot, g), g)
	}
	failing := func(w *workspace.Workspace) error { w.Close(); return errors.New("injected cleanup failure") }
	p := prove(context.Background(), root, c, expectation{Policy: g.PolicyID, Rule: g.PolicyID}, evaluate, workspace.Prepare, failing)
	if p.Status != domain.Inconclusive || p.Reason != domain.ReasonCleanupFailed || p.Mutation == nil {
		t.Fatalf("proof %s/%s mutation=%v", p.Status, p.Reason, p.Mutation)
	}
}

func TestEngineVersionPrefersLinkedRelease(t *testing.T) {
	defer func(v string) { releaseVersion = v }(releaseVersion)
	releaseVersion = ""
	dev := engineVersion()
	releaseVersion = "v0.3.0"
	if v := engineVersion(); v == nil || *v != "v0.3.0" {
		t.Fatalf("linked release ignored: %v", v)
	}
	releaseVersion = ""
	if again := engineVersion(); (again == nil) != (dev == nil) || again != nil && *again == "v0.3.0" {
		t.Fatal("release version leaked into an unlinked build")
	}
}
