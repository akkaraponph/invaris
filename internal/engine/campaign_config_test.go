package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

// Shell evaluators used by the campaign tests. Each reads ./src in the workspace.
const (
	// The selected test "case" fails only when src was mutated to "new".
	shTests = `if [ "$(cat src)" = new ]; then printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"ASSERTION_FAILED"}]}'; exit 1; fi; printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"PASS"}]}'`
	// The selected test never notices the mutation.
	shTestsPass = `printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"PASS"}]}'`
	shValid     = `printf '%s\n' '{"version":"invaris.validity-result/v1alpha1","status":"VALID"}'`
	shInvalid   = `printf '%s\n' '{"version":"invaris.validity-result/v1alpha1","status":"INVALID"}'; exit 1`
	// A structured guard for policy p that reports src once it contains "new".
	shGuard = `if [ "$(cat src)" = new ]; then printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"p","path":"src"}]}'; exit 1; fi; printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}'`
)

func marshalJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// mutationHarness is a v1alpha3 harness that replaces old with new in src.
func mutationHarness(t testing.TB, id, tests, validity string, env ...string) string {
	if env == nil {
		env = []string{}
	}
	return marshalJSON(t, map[string]any{
		"version": "invaris.dev/v1alpha3", "name": id, "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"writablePaths": []string{"src"},
		"experiment": map[string]any{
			"id":         id,
			"mutation":   map[string]any{"id": id + "-change", "operator": "replace", "path": "src", "before": "old", "after": "new"},
			"validation": map[string]any{"id": "valid", "program": "sh", "args": []string{"-c", validity}, "envAllowlist": env, "timeoutSeconds": 10},
			"test":       map[string]any{"id": "test", "program": "sh", "args": []string{"-c", tests}, "envAllowlist": env, "timeoutSeconds": 10},
			"testIds":    []string{"case"},
		},
	})
}

// guardHarness is a v1alpha2 harness with structured guard g and challenge id.
func guardHarness(t testing.TB, id, guard string, policy string, env ...string) string {
	if env == nil {
		env = []string{}
	}
	g := map[string]any{"id": "g", "program": "sh", "args": []string{"-c", guard}, "envAllowlist": env, "timeoutSeconds": 10}
	if policy != "" {
		g["policyId"] = policy
	}
	c := map[string]any{"id": id, "guard": "g", "file": "src", "before": "old", "after": "new"}
	if policy == "" {
		c["expectedOutput"] = "violation"
	}
	return marshalJSON(t, map[string]any{
		"version": "invaris.dev/v1alpha2", "name": id, "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"writablePaths": []string{"src"}, "guards": []any{g}, "challenges": []any{c},
	})
}

type entry struct {
	ID, Kind, Harness, Ref string
	Repeat                 int
}

func campaignJSON(t testing.TB, entries []entry, acceptance map[string]any) string {
	var xs []any
	for _, e := range entries {
		x := map[string]any{"id": e.ID, "kind": e.Kind, "harness": e.Harness, "repeat": e.Repeat}
		if e.Kind == "mutation" {
			x["experimentId"] = e.Ref
		} else {
			x["challengeId"] = e.Ref
		}
		xs = append(xs, x)
	}
	return marshalJSON(t, map[string]any{
		"version": CampaignVersion, "name": "test-campaign", "trustMode": "trusted-local", "networkPolicy": "unrestricted",
		"experiments": xs, "acceptance": acceptance,
	})
}

func allRequirements() map[string]any {
	return map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": true, "requireNoInconclusive": true, "requireNoMixed": true}
}

func writeFile(t testing.TB, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// campaignProject writes src, a killed-mutation harness, a guard harness and
// the campaign file, and returns the directory.
func campaignProject(t *testing.T, campaign string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "src", "old")
	writeFile(t, dir, "mut.json", mutationHarness(t, "m1", shTests, shValid))
	writeFile(t, dir, "guard.json", guardHarness(t, "c1", shGuard, "p"))
	writeFile(t, dir, "campaign.json", campaign)
	return dir
}

var validEntries = []entry{{"killed", "mutation", "mut.json", "m1", 1}, {"proven", "guardrail", "guard.json", "c1", 2}}

func TestLoadCampaign(t *testing.T) {
	dir := campaignProject(t, campaignJSON(t, validEntries, allRequirements()))
	c, err := LoadCampaign(filepath.Join(dir, "campaign.json"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(dir)
	if c.Name != "test-campaign" || c.Root != canonical || c.File != "campaign.json" || !strings.HasPrefix(c.ConfigDigest, "sha256:") || len(c.ConfigDigest) != 71 {
		t.Fatalf("campaign metadata: %+v", c)
	}
	if len(c.Experiments) != 2 {
		t.Fatalf("experiments: %+v", c.Experiments)
	}
	m, g := c.Experiments[0], c.Experiments[1]
	if m.ID != "killed" || m.Kind != domain.KindMutation || m.ReferenceID != "m1" || m.Repeat != 1 || m.Spec.Experiment == nil || m.HarnessDigest != m.Spec.ConfigDigest {
		t.Fatalf("mutation entry: %+v", m)
	}
	if g.Kind != domain.KindGuardrail || g.ReferenceID != "c1" || g.Repeat != 2 || len(g.Spec.Challenges) != 1 {
		t.Fatalf("guardrail entry: %+v", g)
	}
	if !c.Acceptance.RequireNoSurvivors || c.Acceptance.RequireNoInvalid || c.Acceptance.MinMutationScore != nil {
		t.Fatalf("acceptance: %+v", c.Acceptance)
	}
}

func TestLoadCampaignRejects(t *testing.T) {
	valid := campaignJSON(t, validEntries, allRequirements())
	edit := func(f func(m map[string]any)) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(valid), &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		return marshalJSON(t, m)
	}
	exp := func(m map[string]any, i int) map[string]any { return m["experiments"].([]any)[i].(map[string]any) }
	acc := func(m map[string]any) map[string]any { return m["acceptance"].(map[string]any) }
	only := func(entries ...entry) string { return campaignJSON(t, entries, allRequirements()) }
	for _, tc := range []struct {
		name, body, want string
		setup            func(t *testing.T, dir string)
	}{
		{name: "unsupported version", body: edit(func(m map[string]any) { m["version"] = "invaris.campaign/v1alpha2" }), want: "unsupported version"},
		{name: "unknown field", body: edit(func(m map[string]any) { m["parallel"] = true }), want: "unknown or duplicate field"},
		{name: "unknown nested field", body: edit(func(m map[string]any) { exp(m, 0)["timeout"] = 1 }), want: "unknown or duplicate field"},
		{name: "duplicate key", body: strings.Replace(valid, `"name":"test-campaign"`, `"name":"a","name":"b"`, 1), want: "unknown or duplicate field"},
		{name: "null field", body: edit(func(m map[string]any) { m["name"] = nil }), want: "null"},
		{name: "trailing value", body: valid + "{}", want: "one object"},
		{name: "not an object", body: `[]`, want: "unexpected array"},
		{name: "blank name", body: edit(func(m map[string]any) { m["name"] = " " }), want: "name is required"},
		{name: "trust mode", body: edit(func(m map[string]any) { m["trustMode"] = "sandboxed" }), want: "trustMode"},
		{name: "network policy", body: edit(func(m map[string]any) { m["networkPolicy"] = "none" }), want: "networkPolicy"},
		{name: "empty campaign", body: edit(func(m map[string]any) { m["experiments"] = []any{} }), want: "1 to 64"},
		{name: "too many experiments", body: edit(func(m map[string]any) {
			var xs []any
			for i := range 65 {
				xs = append(xs, map[string]any{"id": "e" + strings.Repeat("x", i), "kind": "mutation", "harness": "mut.json", "experimentId": "m1", "repeat": 1})
			}
			m["experiments"] = xs
		}), want: "1 to 64"},
		{name: "missing id", body: edit(func(m map[string]any) { delete(exp(m, 0), "id") }), want: "experiment ID"},
		{name: "padded id", body: edit(func(m map[string]any) { exp(m, 0)["id"] = " killed" }), want: "experiment ID"},
		{name: "duplicate id", body: edit(func(m map[string]any) { exp(m, 1)["id"] = "killed" }), want: "duplicate experiment ID"},
		{name: "unknown kind", body: edit(func(m map[string]any) { exp(m, 0)["kind"] = "fuzz" }), want: "unknown kind"},
		{name: "repeat zero", body: edit(func(m map[string]any) { exp(m, 0)["repeat"] = 0 }), want: "repeat must be 1 to 5"},
		{name: "repeat missing", body: edit(func(m map[string]any) { delete(exp(m, 0), "repeat") }), want: "repeat must be 1 to 5"},
		{name: "repeat six", body: edit(func(m map[string]any) { exp(m, 0)["repeat"] = 6 }), want: "repeat must be 1 to 5"},
		{name: "repeat fraction", body: edit(func(m map[string]any) { exp(m, 0)["repeat"] = 1.5 }), want: "parse"},
		{name: "harness parent", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "../mut.json" }), want: "relative path inside the campaign directory"},
		{name: "harness dot segment", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "./mut.json" }), want: "relative path inside the campaign directory"},
		{name: "harness absolute", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "/tmp/mut.json" }), want: "relative path inside the campaign directory"},
		{name: "harness backslash", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = `a\mut.json` }), want: "relative path inside the campaign directory"},
		{name: "harness missing directory", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "a/mut.json" }), want: "no such file"},
		{name: "harness in excluded directory", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = ".git/mut.json" }), want: "excludes"},
		{name: "harness through symlinked directory", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "linked/mut.json" }), want: "not a directory",
			setup: func(t *testing.T, dir string) {
				os.Mkdir(filepath.Join(dir, "real"), 0o700)
				writeFile(t, filepath.Join(dir, "real"), "mut.json", mutationHarness(t, "m1", shTests, shValid))
				if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "linked")); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "harness is the campaign", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "campaign.json" }), want: "campaign file itself"},
		{name: "harness missing", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "absent.json" }), want: "no such file"},
		{name: "harness symlink", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "link.json" }), want: "not a regular file",
			setup: func(t *testing.T, dir string) {
				if err := os.Symlink(filepath.Join(dir, "mut.json"), filepath.Join(dir, "link.json")); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "harness directory", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "sub" }), want: "not a regular file",
			setup: func(t *testing.T, dir string) { os.Mkdir(filepath.Join(dir, "sub"), 0o700) }},
		{name: "harness invalid", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "bad.json" }), want: "harness bad.json",
			setup: func(t *testing.T, dir string) { writeFile(t, dir, "bad.json", `{"version":"invaris.dev/v1alpha3"}`) }},
		{name: "mutation without experimentId", body: edit(func(m map[string]any) { delete(exp(m, 0), "experimentId") }), want: "needs experimentId"},
		{name: "mutation with challengeId", body: edit(func(m map[string]any) { exp(m, 0)["challengeId"] = "c1" }), want: "needs experimentId and no challengeId"},
		{name: "mutation unknown reference", body: edit(func(m map[string]any) { exp(m, 0)["experimentId"] = "m2" }), want: `no mutation experiment "m2"`},
		{name: "mutation to challenge harness", body: edit(func(m map[string]any) { exp(m, 0)["harness"] = "guard.json" }), want: "not an invaris.dev/v1alpha3 mutation harness"},
		{name: "guardrail with experimentId", body: edit(func(m map[string]any) { exp(m, 1)["experimentId"] = "m1" }), want: "needs challengeId and no experimentId"},
		{name: "guardrail unknown reference", body: edit(func(m map[string]any) { exp(m, 1)["challengeId"] = "c2" }), want: `no challenge "c2"`},
		{name: "guardrail to mutation harness", body: edit(func(m map[string]any) { exp(m, 1)["harness"] = "mut.json" }), want: "not an invaris.dev/v1alpha2 challenge harness"},
		{name: "guardrail with marker guard", body: edit(func(m map[string]any) { exp(m, 1)["harness"] = "marker.json" }), want: "without a policyId",
			setup: func(t *testing.T, dir string) { writeFile(t, dir, "marker.json", guardHarness(t, "c1", "exit 0", "")) }},
		{name: "guardrail to v1alpha1 harness", body: edit(func(m map[string]any) { exp(m, 1)["harness"] = "v1.json" }), want: "not an invaris.dev/v1alpha2",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "v1.json", `{"version":"invaris.dev/v1alpha1","name":"v1","guards":[{"id":"g","program":"true","policyId":"p"}],"challenges":[{"id":"c1","guard":"g","file":"src","before":"old","after":"new"}]}`)
			}},
		{name: "same reference twice", body: only(validEntries[0], entry{"again", "mutation", "mut.json", "m1", 1}, validEntries[1]), want: "already referenced"},
		{name: "missing acceptance", body: edit(func(m map[string]any) { delete(m, "acceptance") }), want: "acceptance must set"},
		{name: "missing required boolean", body: edit(func(m map[string]any) { delete(acc(m), "requireNoMixed") }), want: "acceptance must set"},
		{name: "nothing enabled", body: edit(func(m map[string]any) {
			for k := range acc(m) {
				acc(m)[k] = false
			}
		}), want: "at least one requirement"},
		{name: "score zero", body: edit(func(m map[string]any) { acc(m)["minMutationScore"] = 0 }), want: "minMutationScore"},
		{name: "score above one", body: edit(func(m map[string]any) { acc(m)["minMutationScore"] = 1.5 }), want: "minMutationScore"},
		{name: "score as string", body: edit(func(m map[string]any) { acc(m)["minMutationScore"] = "0.5" }), want: "parse"},
		{name: "survivor rule without mutations", body: only(validEntries[1]), want: "no mutation experiment"},
		{name: "guardrail rule without guardrails", body: only(validEntries[0]), want: "no guardrail experiment"},
		{name: "tool without id", body: edit(func(m map[string]any) { m["tools"] = []any{map[string]any{"program": "go"}} }), want: "tool ID"},
		{name: "tool repeated", body: edit(func(m map[string]any) {
			m["tools"] = []any{map[string]any{"id": "go", "program": "go"}, map[string]any{"id": "go", "program": "go"}}
		}), want: "tool ID"},
		{name: "tool without program", body: edit(func(m map[string]any) { m["tools"] = []any{map[string]any{"id": "go", "program": " "}} }), want: "tool go"},
		{name: "tool timeout", body: edit(func(m map[string]any) {
			m["tools"] = []any{map[string]any{"id": "go", "program": "go", "timeoutSeconds": 301}}
		}), want: "tool go"},
		{name: "tool env key", body: edit(func(m map[string]any) {
			m["tools"] = []any{map[string]any{"id": "go", "program": "go", "envAllowlist": []string{"A=B"}}}
		}), want: "tool go"},
		{name: "tool unknown field", body: edit(func(m map[string]any) {
			m["tools"] = []any{map[string]any{"id": "go", "program": "go", "policyId": "p"}}
		}), want: "unknown or duplicate field"},
		{name: "too many tools", body: edit(func(m map[string]any) {
			var ts []any
			for i := range 17 {
				ts = append(ts, map[string]any{"id": "t" + strings.Repeat("x", i), "program": "true"})
			}
			m["tools"] = ts
		}), want: "at most 16"},
		{name: "invalid rule without mutations", body: campaignJSON(t, validEntries[1:], map[string]any{"requireNoSurvivors": false, "requireAllGuardrailsProven": true, "requireNoInconclusive": true, "requireNoMixed": true, "requireNoInvalid": true}), want: "no mutation experiment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := campaignProject(t, tc.body)
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			_, err := LoadCampaign(filepath.Join(dir, "campaign.json"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("oversized", func(t *testing.T) {
		dir := campaignProject(t, valid+strings.Repeat(" ", 1<<20))
		if _, err := LoadCampaign(filepath.Join(dir, "campaign.json")); err == nil || !strings.Contains(err.Error(), "1 MiB") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("campaign file is a symlink", func(t *testing.T) {
		dir := campaignProject(t, valid)
		if err := os.Symlink(filepath.Join(dir, "campaign.json"), filepath.Join(dir, "alias.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCampaign(filepath.Join(dir, "alias.json")); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("error %v", err)
		}
	})
}

func TestLoadCampaignSubdirectoryHarness(t *testing.T) {
	entries := []entry{{"killed", "mutation", "svc/mut.json", "m1", 1}, {"proven", "guardrail", "guard.json", "c1", 1}}
	dir := campaignProject(t, campaignJSON(t, entries, allRequirements()))
	os.Mkdir(filepath.Join(dir, "svc"), 0o700)
	writeFile(t, filepath.Join(dir, "svc"), "mut.json", mutationHarness(t, "m1", shTests, shValid))
	c, err := LoadCampaign(filepath.Join(dir, "campaign.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Experiments[0].Dir != "svc" || c.Experiments[1].Dir != "." {
		t.Fatalf("dirs %q %q", c.Experiments[0].Dir, c.Experiments[1].Dir)
	}
}

func TestLoadCampaignOptionalRequirements(t *testing.T) {
	a := allRequirements()
	a["requireNoInvalid"], a["minMutationScore"] = true, 0.75
	dir := campaignProject(t, campaignJSON(t, validEntries, a))
	c, err := LoadCampaign(filepath.Join(dir, "campaign.json"))
	if err != nil || !c.Acceptance.RequireNoInvalid || c.Acceptance.MinMutationScore == nil || *c.Acceptance.MinMutationScore != 0.75 {
		t.Fatalf("%+v %v", c.Acceptance, err)
	}
}

func TestSelect(t *testing.T) {
	c := Campaign{Experiments: []CampaignExperiment{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	if got, err := c.Select(nil); err != nil || !got[0] || !got[1] || !got[2] {
		t.Fatalf("all: %v %v", got, err)
	}
	if got, err := c.Select([]string{"c", "a"}); err != nil || !got[0] || got[1] || !got[2] {
		t.Fatalf("filtered: %v %v", got, err)
	}
	for _, only := range [][]string{{"missing"}, {"a", "a"}, {""}} {
		if _, err := c.Select(only); err == nil {
			t.Fatalf("%v accepted", only)
		}
	}
}

// FuzzCampaignHarnessPath: an accepted harness path is relative, has no "."
// or ".." element and no excluded directory, so it resolves inside the root.
func FuzzCampaignHarnessPath(f *testing.F) {
	for _, s := range []string{"mut.json", "../x.json", "a/b.json", "/abs.json", ".", "..", "", "a\\b", "a\x00b", "c:d", "a..b.json", "campaign.json"} {
		f.Add(s)
	}
	root := filepath.FromSlash("/campaign/root")
	f.Fuzz(func(t *testing.T, name string) {
		if campaignHarnessName(name, "campaign.json") != nil {
			return
		}
		if strings.ContainsAny(name, "\\\x00:") || name == "" || name == "campaign.json" || strings.HasPrefix(name, "/") {
			t.Fatalf("accepted %q", name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." || part == ".git" || part == ".invaris" {
				t.Fatalf("accepted %q", name)
			}
		}
		if p := filepath.Join(root, name); !strings.HasPrefix(p, root+string(filepath.Separator)) || filepath.ToSlash(p[len(root)+1:]) != name {
			t.Fatalf("%q resolves to %q", name, p)
		}
	})
}
