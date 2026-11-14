package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

// fakeProvider is a scripted invaris.adapter/v1alpha1 provider written in sh.
// It appends every operation to calls.log (so tests can count discoveries and
// checks), answers discover with the capabilities it is given, and answers a
// check from the requested policy ID: a policy containing "bad" is violated at
// <scope>/main.js by rule "rule-x", every other policy is clean.
const fakeScript = `req=$(cat)
id=$(printf '%s' "$req" | sed -n 's/.*"requestId":"\([^"]*\)".*/\1/p')
op=$(printf '%s' "$req" | sed -n 's/.*"operation":"\([^"]*\)".*/\1/p')
pol=$(printf '%s' "$req" | sed -n 's/.*"policyIds":\["\([^"]*\)".*/\1/p')
scope=$(printf '%s' "$req" | sed -n 's/.*"scope":\["\([^"]*\)".*/\1/p')
echo "$op" >> calls.log
name=fake; [ "$2" = "renamed" ] && [ "$op" = "check" ] && name=impostor
head='{"version":"invaris.adapter/v1alpha1","requestId":"'$id'","provider":{"name":"'$name'","version":"2.0","toolName":"fake-tool","toolVersion":"1.2.3"},'
case "$op" in
discover) printf '%s"capabilities":%s}\n' "$head" "$1" ;;
check)
  [ "$2" = "garbage" ] && { echo nope; exit 0; }
  case "$pol" in
  *bad*) printf '%s"check":{"id":"c","policyId":"%s","providerId":"%s","status":"FAIL","inspected":5,"findings":[{"policyId":"%s","ruleId":"rule-x","path":"%s/main.js","message":"bad import"}],"diagnostics":[],"exitCode":1}}\n' "$head" "$pol" "$name" "$pol" "$scope" ;;
  *) printf '%s"check":{"id":"c","policyId":"%s","providerId":"%s","status":"PASS","inspected":5,"findings":[],"diagnostics":[],"exitCode":0}}\n' "$head" "$pol" "$name" ;;
  esac ;;
esac
`

func caps(policies []string, ops []string, available bool) string {
	b, _ := json.Marshal(map[string]any{"languages": []string{}, "policyIds": policies, "operations": ops, "proofTypes": []string{}, "reportFormats": []string{}, "available": available, "prerequisites": []string{"fake-tool >= 1"}})
	return string(b)
}

func fakeAdapter(id, capsJSON string, mode string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "program": "sh", "args": []string{"-c", fakeScript, "fake", capsJSON, mode}})
	return string(b)
}

func shAdapter(id, script string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "program": "sh", "args": []string{"-c", script}})
	return string(b)
}

func missingAdapter(id string) string {
	return `{"id":"` + id + `","program":"invaris-missing-provider-4242","args":[]}`
}

func pguard(id, adapter, policy string) string {
	return `{"id":"` + id + `","adapterId":"` + adapter + `","policyId":"` + policy + `","scope":["src"]}`
}

type fixture struct {
	root string
}

func newFixture(t *testing.T) fixture { return fixture{root: t.TempDir()} }

func (f fixture) sentinel(name string) bool {
	_, err := os.Stat(filepath.Join(f.root, name))
	return err == nil
}

func (f fixture) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "calls.log"))
	if err != nil {
		return nil
	}
	return strings.Fields(string(b))
}

func count(list []string, v string) int {
	n := 0
	for _, s := range list {
		if s == v {
			n++
		}
	}
	return n
}

// harness builds a v1alpha2 harness. steps and extra are raw JSON fragments.
func harness(adapters []string, guards []string, steps string, extra string) string {
	s := v2Head + `,"adapters":[` + strings.Join(adapters, ",") + `],"guards":[` + strings.Join(guards, ",") + `]`
	if steps != "" {
		s += `,"steps":[` + steps + `]`
	}
	if extra != "" {
		s += "," + extra
	}
	return s
}

const sentinelStep = `{"id":"step","program":"touch","args":["sentinel"]}`
const localGuard = `{"id":"local","program":"touch","args":["local-sentinel"]}`

var allOps = []string{"discover", "check"}

func byID(r evidence.Report) map[string]domain.Check {
	m := map[string]domain.Check{}
	for _, c := range append(append([]domain.Check{}, r.Steps...), r.Checks...) {
		m[c.ID] = c
	}
	return m
}

func TestProviderPreflightBeforeSteps(t *testing.T) {
	good := caps([]string{"p-clean"}, allOps, true)
	for name, tc := range map[string]struct {
		adapter string
		extra   string // appended harness fields
	}{
		"missing executable":    {missingAdapter("a"), ""},
		"unavailable":           {fakeAdapter("a", caps([]string{"p-clean"}, allOps, false), ""), ""},
		"unsupported policy":    {fakeAdapter("a", caps([]string{"other"}, allOps, true), ""), ""},
		"no check operation":    {fakeAdapter("a", caps([]string{"p-clean"}, []string{"discover"}, true), ""), ""},
		"garbage discovery":     {shAdapter("a", "cat >/dev/null; echo nope"), ""},
		"discovery transport":   {shAdapter("a", "cat >/dev/null; exit 3"), ""},
		"wrong protocol":        {shAdapter("a", `cat >/dev/null; echo '{"version":"v0","requestId":"x"}'`), ""},
		"required adapter only": {missingAdapter("a"), `"requiredAdapters":["a"],"gate":{"requiredGuards":["local"]}`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			extra := tc.extra
			if extra == "" {
				extra = `"requiredAdapters":["a"]`
			}
			guards := []string{localGuard, pguard("pg", "a", "p-clean")}
			spec := mustSpec(t, harness([]string{tc.adapter}, guards, sentinelStep, extra))
			r := RunEvidence(context.Background(), spec, f.root, "run")
			if r.Decision != domain.RefuseDecision {
				t.Fatalf("decision %s: %+v", r.Decision, r.Diagnostics)
			}
			if f.sentinel("sentinel") || f.sentinel("local-sentinel") {
				t.Fatal("work ran although provider preflight failed")
			}
			if count(f.calls(t), "check") != 0 {
				t.Fatalf("provider received a check after failed preflight: %v", f.calls(t))
			}
			checks := byID(r)
			if checks["step"].Status != domain.NotRun || checks["local"].Status != domain.NotRun || checks["pg"].Status != domain.NotRun {
				t.Fatalf("work not recorded as NOT_RUN: %+v", checks)
			}
			refused := false
			for id, c := range checks {
				refused = refused || (strings.HasPrefix(id, "provider") && c.Status == domain.Refuse)
			}
			if !refused {
				t.Fatalf("no provider refusal recorded: %+v", checks)
			}
			if err := evidence.Validate(r); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Control: with a capable provider the same harness runs everything.
	f := newFixture(t)
	spec := mustSpec(t, harness([]string{fakeAdapter("a", good, "")}, []string{localGuard, pguard("pg", "a", "p-clean")}, sentinelStep, `"requiredAdapters":["a"]`))
	if r := RunEvidence(context.Background(), spec, f.root, "run"); r.Decision != domain.Accept || !f.sentinel("sentinel") || !f.sentinel("local-sentinel") {
		t.Fatalf("control: %s %+v", r.Decision, r.Diagnostics)
	}
}

func TestProviderCheckEvidence(t *testing.T) {
	f := newFixture(t)
	capabilities := caps([]string{"p-clean", "p-bad"}, allOps, true)
	spec := mustSpec(t, harness([]string{fakeAdapter("a", capabilities, "")}, []string{localGuard, pguard("clean", "a", "p-clean"), pguard("bad", "a", "p-bad")}, "", `"gate":{"requiredGuards":["local","clean","bad"],"precedence":"failure-first"}`))
	r := RunEvidence(context.Background(), spec, f.root, "check")
	if r.Decision != domain.Reject {
		t.Fatalf("a policy violation must reject: %s %+v", r.Decision, r.Diagnostics)
	}
	checks := byID(r)
	if c := checks["clean"]; c.Status != domain.Pass || *c.Inspected != 5 || *c.ExitCode != 0 || c.PolicyID != "p-clean" || c.ProviderID != "fake" {
		t.Fatalf("clean: %+v", c)
	}
	c := checks["bad"]
	if c.Status != domain.Fail || len(c.Findings) != 1 || c.Findings[0].RuleID != "rule-x" || c.Findings[0].Path != "src/main.js" || c.Findings[0].PolicyID != "p-bad" || *c.ExitCode != 1 {
		t.Fatalf("exact policy violation lost: %+v", c)
	}
	if l := checks["local"]; l.Status != domain.Pass || l.ProviderID != commandProvider {
		t.Fatalf("legacy command guard changed: %+v", l)
	}
	// Provider identity and discovery provenance are in the evidence.
	var discoveries, guards int
	for _, o := range r.Observations {
		switch {
		case o.Kind == "discover":
			discoveries++
			if o.Provider.Name != "fake" || o.Provider.Version == nil || *o.Provider.Version != "2.0" || o.Provider.ToolName == nil || *o.Provider.ToolName != "fake-tool" || *o.Provider.ToolVersion != "1.2.3" || o.ID != "discover:a" {
				t.Fatalf("discovery observation: %+v", o)
			}
		case o.ID == "bad" || o.ID == "clean":
			guards++
			if o.Provider.Name != "fake" || len(o.Scope) != 1 || o.Scope[0] != "src" || o.Program != "sh" {
				t.Fatalf("guard observation: %+v", o)
			}
		}
	}
	if discoveries != 1 || guards != 2 {
		t.Fatalf("observations: discover=%d guards=%d", discoveries, guards)
	}
	// Discovery is made once per provider per run, not once per guard.
	if calls := f.calls(t); count(calls, "discover") != 1 || count(calls, "check") != 2 {
		t.Fatalf("calls: %v", calls)
	}
	for _, e := range r.Lifecycle {
		if e.Kind != "guard" {
			t.Fatalf("lifecycle kind %q must be schema-valid: %+v", e.Kind, e)
		}
	}
	if err := evidence.Validate(r); err != nil {
		t.Fatal(err)
	}
	if r.TargetCount == nil || *r.TargetCount != 11 { // 1 local + 5 + 5
		t.Fatalf("targetCount %v", r.TargetCount)
	}

	// Clean provider guards accept.
	f = newFixture(t)
	spec = mustSpec(t, harness([]string{fakeAdapter("a", capabilities, "")}, []string{pguard("clean", "a", "p-clean")}, "", ""))
	if r = RunEvidence(context.Background(), spec, f.root, "check"); r.Decision != domain.Accept {
		t.Fatalf("clean provider guard: %s %+v", r.Decision, r.Diagnostics)
	}
}

func TestOptionalUnsupportedCapabilityIsNotRun(t *testing.T) {
	// The provider supports only p-clean. The p-other guard is outside the gate.
	narrow := caps([]string{"p-clean"}, allOps, true)
	guards := []string{pguard("clean", "a", "p-clean"), pguard("optional", "a", "p-other")}
	f := newFixture(t)
	spec := mustSpec(t, harness([]string{fakeAdapter("a", narrow, "")}, guards, "", `"gate":{"requiredGuards":["clean"]}`))
	r := RunEvidence(context.Background(), spec, f.root, "check")
	c := byID(r)["optional"]
	if r.Decision != domain.Accept || c.Status != domain.NotRun || len(c.Diagnostics) == 0 || !strings.Contains(c.Diagnostics[0].Message, "does not support policy") {
		t.Fatalf("optional unsupported: %s %+v", r.Decision, c)
	}
	if count(f.calls(t), "check") != 1 {
		t.Fatalf("the unsupported guard was dispatched: %v", f.calls(t))
	}
	// The same harness without a gate requires every guard, so it refuses.
	f = newFixture(t)
	spec = mustSpec(t, harness([]string{fakeAdapter("a", narrow, "")}, guards, sentinelStep, ""))
	if r = RunEvidence(context.Background(), spec, f.root, "run"); r.Decision != domain.RefuseDecision || f.sentinel("sentinel") || count(f.calls(t), "check") != 0 {
		t.Fatalf("required unsupported capability: %s sentinel=%v calls=%v", r.Decision, f.sentinel("sentinel"), f.calls(t))
	}
	// An optional provider that cannot even be discovered is NOT_RUN, while required work still runs.
	f = newFixture(t)
	spec = mustSpec(t, harness([]string{missingAdapter("gone")}, []string{localGuard, pguard("optional", "gone", "p")}, "", `"gate":{"requiredGuards":["local"]}`))
	r = RunEvidence(context.Background(), spec, f.root, "check")
	if r.Decision != domain.Accept || byID(r)["optional"].Status != domain.NotRun || !f.sentinel("local-sentinel") {
		t.Fatalf("optional missing provider: %s %+v", r.Decision, byID(r))
	}
	found := false
	for _, o := range r.Observations {
		found = found || (o.Kind == "discover" && len(o.Diagnostics) == 1 && o.Diagnostics[0].Code == "DISCOVERY_FAILED")
	}
	if !found {
		t.Fatal("failed discovery not recorded in the evidence")
	}
}

func TestProviderCheckTimeFailuresRefuse(t *testing.T) {
	capabilities := caps([]string{"p-clean"}, allOps, true)
	for name, mode := range map[string]string{"invalid check response": "garbage", "identity changes after discovery": "renamed"} {
		f := newFixture(t)
		spec := mustSpec(t, harness([]string{fakeAdapter("a", capabilities, mode)}, []string{pguard("pg", "a", "p-clean")}, "", ""))
		r := RunEvidence(context.Background(), spec, f.root, "check")
		c := byID(r)["pg"]
		if r.Decision != domain.RefuseDecision || c.Status != domain.Refuse || len(c.Diagnostics) == 0 {
			t.Errorf("%s: %s %+v", name, r.Decision, c)
		}
	}
	// A failed step with alwaysRunGuards=false leaves the provider guard unrun and undispatched.
	f := newFixture(t)
	spec := mustSpec(t, harness([]string{fakeAdapter("a", capabilities, "")}, []string{pguard("pg", "a", "p-clean")}, `{"id":"step","program":"sh","args":["-c","exit 1"]}`, `"alwaysRunGuards":false`))
	r := RunEvidence(context.Background(), spec, f.root, "run")
	if byID(r)["pg"].Status != domain.NotRun || count(f.calls(t), "check") != 0 || r.Decision == domain.Accept {
		t.Fatalf("scheduling: %s %+v calls=%v", r.Decision, byID(r)["pg"], f.calls(t))
	}
}

func TestResolveProvidersDiscoversOnce(t *testing.T) {
	f := newFixture(t)
	capabilities := caps([]string{"p-clean", "p-bad"}, allOps, true)
	spec := mustSpec(t, harness([]string{fakeAdapter("a", capabilities, ""), fakeAdapter("unused", capabilities, ""), missingAdapter("m")},
		[]string{pguard("g1", "a", "p-clean"), pguard("g2", "a", "p-bad"), pguard("g3", "m", "p")}, "", ""))
	got, err := resolveProviders(context.Background(), spec, f.root)
	if err == nil || !strings.Contains(err.Error(), `provider "m"`) || strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("error should name only the failed provider: %v", err)
	}
	if len(got) != 1 || got["a"].Provider.Name != "fake" || got["a"].Capabilities == nil {
		t.Fatalf("responses: %+v", got)
	}
	// One discovery for "a" despite two guards; "unused" is never started.
	if calls := f.calls(t); len(calls) != 1 || calls[0] != "discover" {
		t.Fatalf("calls: %v", calls)
	}
	// evaluateProviderGuard maps a violation to a typed FAIL check.
	decl, _ := adapterByID(spec, "a")
	if c := evaluateProviderGuard(context.Background(), f.root, spec.ProviderGuards[1], decl); c.Status != domain.Fail || c.ID != "g2" {
		t.Fatalf("evaluate: %+v", c)
	}
	missing, _ := adapterByID(spec, "m")
	if c := evaluateProviderGuard(context.Background(), f.root, spec.ProviderGuards[2], missing); c.Status != domain.Refuse || c.Diagnostics[0].Code != "PROVIDER_TRANSPORT" {
		t.Fatalf("transport failure: %+v", c)
	}
}

// An unavailable provider is judged per guard when it is not in requiredAdapters.
func TestUnavailableProviderPerGuard(t *testing.T) {
	down := fakeAdapter("a", caps([]string{"p-clean"}, allOps, false), "")
	f := newFixture(t)
	spec := mustSpec(t, harness([]string{down}, []string{localGuard, pguard("pg", "a", "p-clean")}, sentinelStep, `"gate":{"requiredGuards":["local","pg"]}`))
	if r := RunEvidence(context.Background(), spec, f.root, "run"); r.Decision != domain.RefuseDecision || f.sentinel("sentinel") || f.sentinel("local-sentinel") {
		t.Fatalf("required guard on an unavailable provider: %s", r.Decision)
	}
	f = newFixture(t)
	spec = mustSpec(t, harness([]string{down}, []string{localGuard, pguard("pg", "a", "p-clean")}, "", `"gate":{"requiredGuards":["local"]}`))
	r := RunEvidence(context.Background(), spec, f.root, "check")
	c := byID(r)["pg"]
	if r.Decision != domain.Accept || c.Status != domain.NotRun || !strings.Contains(c.Diagnostics[0].Message, "unavailable") || !strings.Contains(c.Diagnostics[0].Message, "fake-tool >= 1") {
		t.Fatalf("optional guard on an unavailable provider: %s %+v", r.Decision, c)
	}
	if count(f.calls(t), "check") != 0 {
		t.Fatal("an unavailable provider received a check")
	}
}
