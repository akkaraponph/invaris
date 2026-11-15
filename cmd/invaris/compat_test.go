package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The compatibility goldens record the shape of reports produced by
// invocations that existed before v0.3.0. Only values that vary between runs
// or machines are normalized (run IDs, timestamps, durations, the temporary
// root, resolved executable paths and their hashes, and a stamped engine
// version); statuses, reasons, nulls, score units and order are kept. Refresh
// them only for a deliberate contract change: go test ./cmd/invaris -run
// TestCompatibilityGoldens -update-compat
var updateCompat = flag.Bool("update-compat", false, "rewrite cmd/invaris/testdata/compat goldens")

type compatCase struct {
	name  string
	files map[string]string // relative destination -> source under examples/ or literal content with "=" prefix
	args  []string
	exit  int
}

func compatCases() []compatCase {
	legacy := map[string]string{"invaris.json": "invaris.json", "source.txt": "source.txt"}
	structured := map[string]string{"invaris.json": "structured/invaris.json", "source.txt": "structured/source.txt", "guard.sh": "structured/guard.sh"}
	v2 := map[string]string{"invaris.json": "v1alpha2/invaris.json", "source.txt": "v1alpha2/source.txt", "guard.sh": "v1alpha2/guard.sh"}
	mutation := map[string]string{}
	for _, f := range []string{"behavior.sh", "campaign.json", "invalid.json", "killed.json", "survived.json", "test.sh", "validate.sh"} {
		mutation[f] = "mutation/" + f
	}
	with := func(base map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	const head = `{"version":"invaris.dev/v1alpha2","name":"compat","trustMode":"trusted-local","networkPolicy":"unrestricted",`
	const mixed = `"guards":[{"id":"fails","program":"sh","args":["-c","exit 1"]},{"id":"refuses","program":"./missing-guard"}]`
	guardrailCampaign := `={"version":"invaris.campaign/v1alpha1","name":"compat-guardrail","trustMode":"trusted-local","networkPolicy":"unrestricted",` +
		`"experiments":[{"id":"marker","kind":"guardrail","harness":"invaris.json","challengeId":"detect-marker","repeat":2}],` +
		`"acceptance":{"requireNoSurvivors":false,"requireAllGuardrailsProven":true,"requireNoInconclusive":true,"requireNoMixed":true}}`
	noEligible := `={"version":"invaris.campaign/v1alpha1","name":"compat-no-eligible","trustMode":"trusted-local","networkPolicy":"unrestricted",` +
		`"experiments":[{"id":"invalid","kind":"mutation","harness":"invalid.json","experimentId":"mutation-invalid","repeat":1}],` +
		`"acceptance":{"requireNoSurvivors":true,"requireAllGuardrailsProven":false,"requireNoInconclusive":false,"requireNoMixed":true}}`
	return []compatCase{
		{name: "v1-legacy-check", files: legacy, args: []string{"check"}, exit: 0},
		{name: "v1-legacy-challenge-weak", files: legacy, args: []string{"challenge"}, exit: 0},
		{name: "v1-legacy-run", files: legacy, args: []string{"run", "invaris.json"}, exit: 0},
		{name: "v1-structured-check", files: structured, args: []string{"check"}, exit: 0},
		{name: "v1-structured-challenge", files: structured, args: []string{"challenge"}, exit: 0},
		{name: "v1-structured-check-failing", files: with(structured, map[string]string{"source.txt": "=FORBIDDEN"}), args: []string{"check"}, exit: 1},
		{name: "v2-check", files: v2, args: []string{"check"}, exit: 0},
		{name: "v2-challenge", files: v2, args: []string{"challenge"}, exit: 0},
		{name: "v2-challenge-unhealthy-baseline", files: with(v2, map[string]string{"source.txt": "=FORBIDDEN"}), args: []string{"challenge"}, exit: 2},
		{name: "v2-failure-first", files: map[string]string{"invaris.json": "=" + head + mixed + `,"gate":{"requiredGuards":["fails","refuses"],"precedence":"failure-first"}}`}, args: []string{"check"}, exit: 1},
		{name: "v2-refusal-first", files: map[string]string{"invaris.json": "=" + head + mixed + `,"gate":{"requiredGuards":["fails","refuses"],"precedence":"refusal-first"}}`}, args: []string{"check"}, exit: 2},
		{name: "v2-empty-work", files: map[string]string{"invaris.json": "=" + head + `"guards":[]}`}, args: []string{"check"}, exit: 2},
		{name: "v3-mutate-killed", files: mutation, args: []string{"mutate", "killed.json"}, exit: 0},
		{name: "v3-mutate-survived", files: mutation, args: []string{"mutate", "survived.json"}, exit: 1},
		{name: "v3-mutate-invalid", files: mutation, args: []string{"mutate", "invalid.json"}, exit: 2},
		{name: "campaign-mutation", files: mutation, args: []string{"campaign", "campaign.json"}, exit: 1},
		{name: "campaign-mutation-filtered", files: mutation, args: []string{"campaign", "--only", "killed", "campaign.json"}, exit: 0},
		{name: "campaign-no-eligible", files: with(mutation, map[string]string{"campaign.json": noEligible}), args: []string{"campaign"}, exit: 0},
		{name: "campaign-guardrail-repeat", files: with(v2, map[string]string{"campaign.json": guardrailCampaign}), args: []string{"campaign"}, exit: 0},
	}
}

func TestCompatibilityGoldens(t *testing.T) {
	for _, tc := range compatCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for dst, src := range tc.files {
				data := strings.TrimPrefix(src, "=")
				if !strings.HasPrefix(src, "=") {
					raw, err := os.ReadFile(filepath.Join("..", "..", "examples", filepath.FromSlash(src)))
					if err != nil {
						t.Fatal(err)
					}
					data = string(raw)
				}
				if err := os.WriteFile(filepath.Join(dir, dst), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
				// Modes are part of the source snapshot digest; fix them.
				if err := os.Chmod(filepath.Join(dir, dst), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(cliBinary, tc.args...)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			exit := 0
			if err != nil {
				var status *exec.ExitError
				if !errors.As(err, &status) {
					t.Fatal(err)
				}
				exit = status.ExitCode()
			}
			if exit != tc.exit {
				t.Fatalf("exit=%d, want %d; stderr=%s", exit, tc.exit, &stderr)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", &stderr)
			}
			got := normalizeReport(t, stdout, dir)
			golden := filepath.Join("testdata", "compat", tc.name+".json")
			if *updateCompat {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden (run with -update-compat): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("report differs from %s:\n%s", golden, firstDifference(want, got))
			}
		})
	}
}

// TestCompatibilitySemantics checks contract values directly, so a golden
// refreshed by mistake cannot bless a changed unit, null or verdict.
func TestCompatibilitySemantics(t *testing.T) {
	read := func(name string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", "compat", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	schema := map[string]string{
		"v1-legacy-check": "invaris.evidence/v1alpha2", "v2-challenge": "invaris.evidence/v1alpha2",
		"v3-mutate-killed": "invaris.evidence/v1alpha3", "campaign-mutation": "invaris.evidence/v1alpha4",
	}
	for name, want := range schema {
		if got := read(name)["schemaVersion"]; got != want {
			t.Errorf("%s: schemaVersion %v, want %s", name, got, want)
		}
	}
	// Two eligible of three (one killed, one survived, one invalid): 0.5, a fraction.
	m := read("campaign-mutation")["mutationSummary"].(map[string]any)
	if m["score"] != 0.5 || m["eligible"] != 2.0 || m["killed"] != 1.0 || m["survived"] != 1.0 || m["invalid"] != 1.0 {
		t.Errorf("mutation summary changed: %v", m)
	}
	if s := read("campaign-no-eligible")["mutationSummary"].(map[string]any); s["score"] != nil || s["eligible"] != 0.0 {
		t.Errorf("no eligible mutants must give a null score: %v", s)
	}
	g := read("campaign-guardrail-repeat")["guardrailSummary"].(map[string]any)
	if g["proofRate"] != 1.0 || g["proven"] != 1.0 {
		t.Errorf("repeats must count once: %v", g)
	}
	f := read("campaign-mutation-filtered")["selection"].(map[string]any)
	if f["mode"] != "filtered" || f["selected"] != 1.0 || f["filtered"] != 2.0 {
		t.Errorf("filtered selection changed: %v", f)
	}
	weak := read("v1-legacy-challenge-weak")
	if p := weak["proofs"].([]any)[0].(map[string]any); p["evidenceStrength"] != "weak" || p["reason"] != "marker_matched" {
		t.Errorf("legacy weak proof changed: %v", p)
	}
	if weak["gate"].(map[string]any)["allowWeakEvidence"] != true || weak["compatibility"].(map[string]any)["status"] != "legacy" {
		t.Errorf("legacy gate changed: %v", weak["gate"])
	}
	if read("v2-challenge")["gate"].(map[string]any)["allowWeakEvidence"] != false {
		t.Error("v1alpha2 must not allow weak evidence")
	}
	if p := read("v2-challenge-unhealthy-baseline")["proofs"].([]any)[0].(map[string]any); p["status"] != "INCONCLUSIVE" || p["reason"] != "unhealthy_baseline" {
		t.Errorf("unhealthy baseline changed: %v", p)
	}
	if read("v2-failure-first")["decision"] != "REJECT" || read("v2-refusal-first")["decision"] != "REFUSE" || read("v2-empty-work")["decision"] != "REFUSE" {
		t.Error("gate precedence or empty work decision changed")
	}
}

// TestCompatibilityCLIForms pins the exit and stream behavior of invocation
// forms that must keep working, and of malformed ones that must fail before
// anything runs.
func TestCompatibilityCLIForms(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	mustWrite(t, filepath.Join(dir, "invaris.json"), `{"version":"invaris.dev/v1alpha1","name":"forms","steps":[{"id":"s","program":"touch","args":["`+sentinel+`"]}],"guards":[{"id":"g","program":"true"}]}`)
	for _, tc := range []struct {
		args []string
		exit int
		runs bool
	}{
		{[]string{"run"}, 0, true},
		{[]string{"run", "invaris.json"}, 0, true},
		{[]string{"check", "invaris.json"}, 0, false},
		{[]string{}, 2, false},
		{[]string{"check", "invaris.json", "extra"}, 2, false},
		{[]string{"explode"}, 2, false},
		{[]string{"mutate", "invaris.json"}, 2, false},
		{[]string{"run", "missing.json"}, 2, false},
		{[]string{"--report"}, 2, false},
		{[]string{"--report", "", "run"}, 2, false},
		{[]string{"campaign", "--format"}, 2, false},
		{[]string{"campaign", "--format", "json", "--format", "text"}, 2, false},
		{[]string{"campaign", "--format", "yaml"}, 2, false},
		{[]string{"campaign", "--parallel", "9"}, 2, false},
		{[]string{"campaign", "--unknown"}, 2, false},
		{[]string{"campaign", "a.json", "b.json"}, 2, false},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			os.Remove(sentinel)
			code, stdout, stderr := invoke(t, dir, tc.args...)
			if code != tc.exit {
				t.Fatalf("exit=%d want %d; stderr=%q", code, tc.exit, stderr)
			}
			if code == 2 && (len(stdout) != 0 || stderr == "") {
				t.Fatalf("usage errors go to stderr only: stdout=%q stderr=%q", stdout, stderr)
			}
			if _, err := os.Stat(sentinel); (err == nil) != tc.runs {
				t.Fatalf("step executed=%v, want %v", err == nil, tc.runs)
			}
		})
	}
}

// normalizeReport replaces values that change between runs or machines. It
// keeps the emitted key order, so a reordered field is a visible difference.
func normalizeReport(t *testing.T, raw []byte, root string) []byte {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := decodeOrdered(d)
	if err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, raw)
	}
	if _, err := d.Token(); err == nil {
		t.Fatalf("stdout holds more than one JSON document:\n%s", raw)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	v = normalizeValue("", v, []string{canonical, root})
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ordered is a JSON object that remembers its key order.
type ordered struct {
	keys []string
	vals map[string]any
}

func (o *ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		var val bytes.Buffer
		enc := json.NewEncoder(&val)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(o.vals[k]); err != nil {
			return nil, err
		}
		b.Write(bytes.TrimRight(val.Bytes(), "\n"))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func decodeOrdered(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := &ordered{vals: map[string]any{}}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeOrdered(d)
			if err != nil {
				return nil, err
			}
			o.keys = append(o.keys, k.(string))
			o.vals[k.(string)] = v
		}
		_, err = d.Token()
		return o, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := decodeOrdered(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	}
	return tok, nil
}

func normalizeValue(key string, v any, roots []string) any {
	switch x := v.(type) {
	case *ordered:
		for _, k := range x.keys {
			x.vals[k] = normalizeValue(k, x.vals[k], roots)
		}
		switch key {
		case "executable":
			x.vals["path"] = "<executable>"
			if x.vals["sha256"] != nil {
				x.vals["sha256"] = "<executable-sha256>"
			}
		case "environment":
			x.vals["os"], x.vals["arch"] = "<os>", "<arch>"
		}
		return x
	case []any:
		for i := range x {
			x[i] = normalizeValue(key, x[i], roots)
		}
		return x
	case string:
		switch key {
		case "runId":
			return "<runId>"
		case "startedAt", "finishedAt":
			return "<time>"
		case "engineVersion":
			return "<engineVersion>"
		}
		for _, r := range roots {
			x = strings.ReplaceAll(x, r, "<root>")
		}
		return x
	case json.Number:
		if key == "durationMs" {
			return json.Number("0")
		}
		return x
	}
	return v
}

func firstDifference(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d\n- %s\n+ %s", i+1, a, b)
		}
	}
	return "(no line difference)"
}
