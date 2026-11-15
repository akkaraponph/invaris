package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

const v1Head = `{"version":"invaris.dev/v1alpha1","name":"legacy",`

func migrate(t *testing.T, raw string) MigrationResult {
	t.Helper()
	r, err := MigrateConfig([]byte(raw), "", "")
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, raw)
	}
	return r
}

func wireFields(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMigrateTransformsOnlyTheV2Contract(t *testing.T) {
	src := v1Head + `"failFast":false,"policies":["p"],"requiredAdapters":[],` +
		`"steps":[{"id":"s","program":"sh","args":["-c","echo  'two  spaces'",""],"envAllowlist":["PATH"],"timeoutSeconds":7}],` +
		`"guards":[{"id":"g","program":"sh","args":["guard.sh"],"policyId":"p","minInspected":2},{"id":"m","program":"true"}],` +
		`"challenges":[{"id":"c1","guard":"g","file":"b.txt","before":"x","after":"y"},{"id":"c2","guard":"m","file":"a.txt","before":"x","after":"y","expectedOutput":"Y"},{"id":"c3","guard":"g","file":"b.txt","before":"q","after":"r"}]}`
	r := migrate(t, src)
	if !r.Changed || r.SourceVersion != versionV1 || r.TargetVersion != versionV2 || !bytes.HasSuffix(r.Bytes, []byte("}\n")) {
		t.Fatalf("result %+v", r)
	}
	in, out := wireFields(t, []byte(src)), wireFields(t, r.Bytes)
	for _, k := range []string{"steps", "guards", "challenges", "requiredAdapters", "failFast", "name"} {
		if compact(t, in[k]) != compact(t, out[k]) {
			t.Errorf("%s changed:\n%s\n%s", k, in[k], out[k])
		}
	}
	want := map[string]string{
		"version":       `"invaris.dev/v1alpha2"`,
		"trustMode":     `"trusted-local"`,
		"networkPolicy": `"unrestricted"`,
		"writablePaths": `["b.txt","a.txt"]`, // first occurrence order, no duplicates
		"policies":      `[{"id":"p","allowEmpty":false}]`,
		"gate":          `{"requiredGuards":["g","m"],"requiredProofs":["c1","c2","c3"],"precedence":"failure-first","allowWeakEvidence":true}`,
	}
	for k, v := range want {
		if got := compact(t, out[k]); got != v {
			t.Errorf("%s = %s, want %s", k, got, v)
		}
	}
	if _, ok := out["alwaysRunGuards"]; ok {
		t.Error("omitted alwaysRunGuards must stay omitted")
	}
	again, err := MigrateConfig([]byte(src), "v1alpha1", "v1alpha2")
	if err != nil || !bytes.Equal(again.Bytes, r.Bytes) {
		t.Fatal("migration is not deterministic")
	}
	if next := migrate(t, string(r.Bytes)); next.Changed || !bytes.Equal(next.Bytes, r.Bytes) {
		t.Fatal("migrating the result again must be a byte-preserving no-op")
	}
}

func TestMigratePreservesAbsenceAndEmptiness(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		present    map[string]string // field -> compact value; "" means absent
	}{
		{"omitted policies and lists", `"guards":[{"id":"g","program":"true"}]}`,
			map[string]string{"policies": "", "steps": "", "challenges": "", "writablePaths": "[]"}},
		{"explicit empty lists", `"policies":[],"steps":[],"guards":[],"challenges":[],"writablePaths":[]}`,
			map[string]string{"policies": "[]", "steps": "[]", "guards": "[]", "challenges": "[]", "writablePaths": "[]",
				"gate": `{"requiredGuards":[],"requiredProofs":[],"precedence":"failure-first","allowWeakEvidence":true}`}},
		{"explicit writable list kept", `"writablePaths":["a.txt","z.txt"],"guards":[{"id":"g","program":"true"}],"challenges":[{"id":"c","guard":"g","file":"a.txt","before":"x","after":"y","expectedOutput":"y"}]}`,
			map[string]string{"writablePaths": `["a.txt","z.txt"]`}},
		{"blank network policy", `"networkPolicy":"","guards":[{"id":"g","program":"true"}]}`,
			map[string]string{"networkPolicy": `"unrestricted"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := wireFields(t, migrate(t, v1Head+tc.body).Bytes)
			for k, v := range tc.present {
				raw, ok := out[k]
				switch {
				case v == "" && ok:
					t.Errorf("%s must be omitted, got %s", k, raw)
				case v != "" && (!ok || compact(t, raw) != v):
					t.Errorf("%s = %s, want %s", k, raw, v)
				}
			}
		})
	}
}

func TestMigrateRefusals(t *testing.T) {
	big := v1Head + `"guards":[{"id":"g","program":"true","args":[` + strings.Repeat(`"a",`, 200_000) + `"a"]}]}`
	for _, tc := range []struct{ name, raw, from, to, msg string }{
		{"v3 mutation harness", mustRead(t, "../../examples/mutation/killed.json"), "", "", "different contract"},
		{"unknown version", `{"version":"invaris.dev/v9","name":"x"}`, "", "", "unsupported harness version"},
		{"missing version", `{"name":"x"}`, "", "", "unsupported harness version"},
		{"unknown field", v1Head + `"guards":[],"comment":"keep me"}`, "", "", "unknown"},
		{"duplicate field", v1Head + `"guards":[],"guards":[]}`, "", "", "duplicate"},
		{"case mismatch", v1Head + `"Guards":[]}`, "", "", "unknown"},
		{"null field", v1Head + `"guards":null}`, "", "", "null"},
		{"from mismatch", v1Head + `"guards":[]}`, "v1alpha2", "", "does not match"},
		{"unknown from", v1Head + `"guards":[]}`, "v0", "", "unsupported source"},
		{"target v3", v1Head + `"guards":[]}`, "", "v1alpha3", "only migration target"},
		{"target v4", v1Head + `"guards":[]}`, "", "v1alpha4", "only migration target"},
		{"result over 1 MiB", big, "", "", "exceeds 1 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MigrateConfig([]byte(tc.raw), tc.from, tc.to)
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err=%v, want %q", err, tc.msg)
			}
		})
	}
	v2 := mustRead(t, "../../examples/v1alpha2/invaris.json")
	r, err := MigrateConfig([]byte(v2), "v1alpha2", "")
	if err != nil || r.Changed || string(r.Bytes) != v2 {
		t.Fatalf("already-current harness must be returned byte for byte: %+v %v", r.Changed, err)
	}
}

// sameSemantics must refuse a destination that would change behavior.
func TestSameSemanticsDetectsDrift(t *testing.T) {
	src, err := Decode([]byte(mustRead(t, "../../examples/invaris.json")))
	if err != nil {
		t.Fatal(err)
	}
	dst, err := Decode(migrate(t, mustRead(t, "../../examples/invaris.json")).Bytes)
	if err != nil || sameSemantics(src, dst) != nil {
		t.Fatalf("faithful migration rejected: %v", err)
	}
	for name, change := range map[string]func(*Specification){
		"weak evidence revoked":  func(s *Specification) { g := *s.Gate; g.AllowWeakEvidence = false; s.Gate = &g },
		"guard not required":     func(s *Specification) { g := *s.Gate; g.RequiredGuards = nil; s.Gate = &g },
		"refusal-first":          func(s *Specification) { g := *s.Gate; g.Precedence = domain.RefusalFirst; s.Gate = &g },
		"target not writable":    func(s *Specification) { s.WritablePaths = []string{} },
		"argument changed":       func(s *Specification) { s.Guards[0].Args = []string{"-c", "true"} },
		"failFast changed":       func(s *Specification) { f := false; s.FailFast = &f },
		"policy declaration set": func(s *Specification) { s.Policies = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			d := dst
			d.Guards = slices.Clone(dst.Guards)
			change(&d)
			if sameSemantics(src, d) == nil {
				t.Fatal("semantic drift accepted")
			}
		})
	}
}

// Real legacy fixtures give the same decisions and per-result outcomes before
// and after migration, including weak marker proofs.
func TestMigratedFixturesBehaveTheSame(t *testing.T) {
	for _, dir := range []string{"../../examples", "../../examples/structured"} {
		raw := mustRead(t, filepath.Join(dir, "invaris.json"))
		src, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		dst, err := Decode(migrate(t, raw).Bytes)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"run", "check", "challenge"} {
			before := RunEvidence(context.Background(), src, dir, action)
			after := RunEvidence(context.Background(), dst, dir, action)
			if before.Decision != after.Decision || before.Decision != domain.Accept {
				t.Errorf("%s %s: %s -> %s", dir, action, before.Decision, after.Decision)
			}
			outcomes := func(r []domain.Proof) []string {
				var s []string
				for _, p := range r {
					s = append(s, p.ID+"="+string(p.Status)+"/"+p.EvidenceStrength+"/"+p.Reason)
				}
				return s
			}
			if !slices.Equal(outcomes(before.Proofs), outcomes(after.Proofs)) || len(before.Checks) != len(after.Checks) {
				t.Errorf("%s %s: proofs %v -> %v", dir, action, outcomes(before.Proofs), outcomes(after.Proofs))
			}
			if before.Compatibility.Status != "legacy" || after.Compatibility.Status != "native" {
				t.Errorf("compatibility labels: %s -> %s", before.Compatibility.Status, after.Compatibility.Status)
			}
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
