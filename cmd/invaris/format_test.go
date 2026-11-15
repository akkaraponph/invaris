package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

var updateFormat = flag.Bool("update-format", false, "rewrite cmd/invaris/testdata/format goldens")

var msPattern = regexp.MustCompile(`duration=\d+ms`)

// formatCases cover pass, fail, refuse, weak proof, unhealthy baseline, wrong
// finding and timeout for both check and challenge.
func formatCases() []compatCase {
	cases := []compatCase{}
	for _, c := range compatCases() {
		if strings.HasPrefix(c.name, "v1-") || strings.HasPrefix(c.name, "v2-") {
			if a := c.args[0]; a == "check" || a == "challenge" {
				cases = append(cases, c)
			}
		}
	}
	const head = `={"version":"invaris.dev/v1alpha2","name":"format","trustMode":"trusted-local","networkPolicy":"unrestricted","writablePaths":["source.txt"],`
	wrong := `#!/bin/sh
case "$(cat source.txt)" in
  SAFE) printf '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}\n'; exit 0 ;;
  *) printf '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"p","path":"other.txt"}]}\n'; exit 1 ;;
esac
`
	cases = append(cases,
		compatCase{name: "wrong-finding", files: map[string]string{"source.txt": "=SAFE", "guard.sh": "=" + wrong,
			"invaris.json": head + `"guards":[{"id":"g","policyId":"p","program":"sh","args":["guard.sh"]}],"challenges":[{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD"}]}`},
			args: []string{"challenge"}, exit: 2},
		compatCase{name: "timeout", files: map[string]string{"source.txt": "=SAFE",
			"invaris.json": head + `"guards":[{"id":"slow","program":"sleep","args":["5"],"timeoutSeconds":1}],"challenges":[{"id":"c","guard":"slow","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}]}`},
			args: []string{"challenge"}, exit: 2},
		compatCase{name: "control-characters", files: map[string]string{
			"invaris.json": `={"version":"invaris.dev/v1alpha2","name":"evil\u001b[31mname\nnext","trustMode":"trusted-local","networkPolicy":"unrestricted","guards":[{"id":"g\u001b[0m","program":"sh","args":["-c","exit 1"]}]}`},
			args: []string{"check"}, exit: 1},
	)
	return cases
}

func setupCase(t *testing.T, c compatCase) string {
	t.Helper()
	dir := t.TempDir()
	for dst, src := range c.files {
		data := strings.TrimPrefix(src, "=")
		if !strings.HasPrefix(src, "=") {
			raw, err := os.ReadFile(filepath.Join("..", "..", "examples", filepath.FromSlash(src)))
			if err != nil {
				t.Fatal(err)
			}
			data = string(raw)
		}
		mustWrite(t, filepath.Join(dir, dst), data)
	}
	return dir
}

func runBinary(t *testing.T, dir string, env []string, args ...string) (int, []byte, string) {
	t.Helper()
	cmd := exec.Command(cliBinary, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	code := 0
	if err != nil {
		var status *exec.ExitError
		if !errors.As(err, &status) {
			t.Fatal(err)
		}
		code = status.ExitCode()
	}
	return code, out, stderr.String()
}

func TestFormatMatrixKeepsDecisionAndExit(t *testing.T) {
	for _, c := range formatCases() {
		t.Run(c.name, func(t *testing.T) {
			dir := setupCase(t, c)
			action := c.args[0]
			code, jsonOut, _ := runBinary(t, dir, nil, action)
			var report struct{ Decision string }
			if code != c.exit || json.Unmarshal(jsonOut, &report) != nil {
				t.Fatalf("json: exit=%d want %d: %s", code, c.exit, jsonOut)
			}
			for _, format := range []string{"json", "text", "compact"} {
				dest := filepath.Join(dir, format+".report.json")
				code, out, stderr := runBinary(t, dir, nil, "--report", dest, action, "--format", format, "invaris.json")
				if code != c.exit || stderr != "" {
					t.Fatalf("%s: exit=%d stderr=%q", format, code, stderr)
				}
				if bytes.IndexByte(out, 0x1b) >= 0 || bytes.IndexByte(out, '\r') >= 0 {
					t.Fatalf("%s output contains terminal control bytes: %q", format, out)
				}
				saved, err := os.ReadFile(dest)
				var savedReport struct{ SchemaVersion, Decision string }
				if err != nil || json.Unmarshal(saved, &savedReport) != nil || savedReport.Decision != report.Decision || savedReport.SchemaVersion != "invaris.evidence/v1alpha2" {
					t.Fatalf("%s: report file is not the JSON report: %s", format, saved)
				}
				lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
				last := lines[len(lines)-1]
				switch format {
				case "json":
					d := json.NewDecoder(bytes.NewReader(out))
					var v any
					if d.Decode(&v) != nil || d.More() || !bytes.Equal(out, saved) {
						t.Fatal("json stdout must be exactly one report, identical to the report file")
					}
				case "text":
					if !strings.HasPrefix(lines[0], "Invaris "+action+": ") || !strings.HasPrefix(last, "Decision: "+report.Decision+"  ") {
						t.Fatalf("text:\n%s", out)
					}
				case "compact":
					if !strings.HasPrefix(last, report.Decision+" ") || bytes.ContainsAny(out, "{}") {
						t.Fatalf("compact:\n%s", out)
					}
				}
			}
		})
	}
}

func TestFormatTextGoldens(t *testing.T) {
	for _, c := range formatCases() {
		for _, format := range []string{"text", "compact"} {
			t.Run(c.name+"/"+format, func(t *testing.T) {
				dir := setupCase(t, c)
				_, out, _ := runBinary(t, dir, nil, c.args[0], "--format", format)
				got := msPattern.ReplaceAll(out, []byte("duration=<ms>"))
				golden := filepath.Join("testdata", "format", c.name+"."+format+".txt")
				if *updateFormat {
					os.MkdirAll(filepath.Dir(golden), 0o755)
					os.WriteFile(golden, got, 0o644)
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("missing golden (run with -update-format): %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s differs:\n%s", golden, firstDifference(want, got))
				}
			})
		}
	}
}

// Output does not depend on color settings, terminal type or width.
func TestFormatIgnoresTerminalEnvironment(t *testing.T) {
	c := formatCases()[0]
	dir := setupCase(t, c)
	var first []byte
	for i, env := range [][]string{nil, {"NO_COLOR=1", "TERM=dumb"}, {"TERM=xterm-256color", "COLUMNS=20", "CLICOLOR_FORCE=1", "CI=true"}} {
		_, out, _ := runBinary(t, dir, env, c.args[0], "--format", "text")
		out = msPattern.ReplaceAll(out, nil)
		if i == 0 {
			first = out
		} else if !bytes.Equal(out, first) {
			t.Fatalf("environment %v changed output:\n%s\n%s", env, first, out)
		}
	}
}

func TestFormatFlagErrorsRunNothing(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	mustWrite(t, filepath.Join(dir, "invaris.json"), `{"version":"invaris.dev/v1alpha1","name":"f","steps":[{"id":"s","program":"touch","args":["`+sentinel+`"]}],"guards":[{"id":"g","program":"touch","args":["`+sentinel+`"]}]}`)
	for _, args := range [][]string{
		{"check", "--format", "yaml"},
		{"check", "--format", ""},
		{"check", "--format"},
		{"check", "--format", "text", "--format", "json"},
		{"check", "invaris.json", "--format", "text"},
		{"check", "--colour"},
		{"challenge", "--format", "text", "a.json", "b.json"},
		{"run", "--format", "text"},
		{"mutate", "--format", "json"},
		{"--report", "r.json", "check", "--format", "html"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			code, stdout, stderr := invoke(t, dir, args...)
			if code != 2 || len(stdout) != 0 || stderr == "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("a command ran despite a usage error")
			}
			if _, err := os.Stat(filepath.Join(dir, "r.json")); err == nil {
				t.Fatal("report written despite a usage error")
			}
		})
	}
	// A harness file whose name starts with a single dash is still a file.
	mustWrite(t, filepath.Join(dir, "-dash.json"), `{"version":"invaris.dev/v1alpha1","name":"d","guards":[{"id":"g","program":"true"}]}`)
	if code, _, stderr := invoke(t, dir, "check", "-dash.json"); code != 0 {
		t.Fatalf("dash-named harness: %d %s", code, stderr)
	}
}

func TestFormatPublicationAndWriterFailures(t *testing.T) {
	dir := setupCase(t, formatCases()[0])
	mustWrite(t, filepath.Join(dir, "taken.json"), "existing")
	for _, format := range []string{"text", "compact"} {
		code, stdout, stderr := invoke(t, dir, "--report", "taken.json", "check", "--format", format)
		if code != 2 || len(stdout) != 0 || !strings.Contains(stderr, "prepare report") {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", format, code, stdout, stderr)
		}
		w := &failingWriter{}
		code, _, stderr = invokeWith(t, dir, w, "check", "--format", format)
		if code != 2 || w.calls != 1 || !strings.Contains(stderr, "write stdout") {
			t.Fatalf("%s writer failure: code=%d calls=%d stderr=%q", format, code, w.calls, stderr)
		}
	}
}

func TestFormatEscapesAndTruncates(t *testing.T) {
	start := time.Unix(0, 0).UTC()
	finish := start.Add(1500 * time.Millisecond)
	r := evidence.Report{Action: "check", Harness: "a\x1b[2Jb", Decision: domain.Reject,
		Lifecycle: []evidence.Event{{ID: "g id", StartedAt: &start, FinishedAt: &finish}},
		Checks: []domain.Check{{ID: "g id", PolicyID: "p=q", Status: domain.Fail,
			Findings:    []domain.Finding{{PolicyID: "p=q", RuleID: "r", Path: "dir/‮file"}},
			Diagnostics: []domain.Diagnostic{{Code: "X", Message: "line1\nline2\r\x07" + strings.Repeat("é", 200)}}}}}
	out, err := formatReport(r, formatText)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"\x1b", "\r", "\x07", "‮"} {
		if strings.Contains(s, bad) {
			t.Fatalf("unescaped %q in:\n%s", bad, s)
		}
	}
	for _, want := range []string{`"a\x1b[2Jb"`, `guard "g id"`, `policy="p=q"`, `duration=1500ms`, `line1\nline2\r\a`, `... (truncated)`, "path=\"dir/\\u202efile\""} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Count(s, "\n") != 6 {
		t.Fatalf("free text must stay on one line:\n%s", s)
	}
	if _, err := formatReport(evidence.Report{Action: "run"}, formatText); err == nil {
		t.Fatal("run must not be formatted as text")
	}
	if c, _ := formatReport(evidence.Report{Action: "check"}, formatCompact); !strings.Contains(string(c), "checks=0") {
		t.Fatalf("compact empty check: %q", c)
	}
}

func TestCampaignCompactFormat(t *testing.T) {
	dir := setupCase(t, compatCase{files: func() map[string]string {
		m := map[string]string{}
		for _, f := range []string{"behavior.sh", "campaign.json", "invalid.json", "killed.json", "survived.json", "test.sh", "validate.sh"} {
			m[f] = "mutation/" + f
		}
		return m
	}()})
	code, out, stderr := runBinary(t, dir, nil, "campaign", "campaign.json", "--format", "compact")
	want := "KILLED killed kind=mutation attempts=KILLED(assertion_failed)\n" +
		"SURVIVED survived kind=mutation attempts=SURVIVED(all_tests_passed)\n" +
		"INVALID invalid kind=mutation attempts=INVALID(mutant_invalid)\n" +
		"REJECT experiments=3 mutation_score=50.0% proof_rate=n/a\n"
	if code != 1 || string(out) != want {
		t.Fatalf("code=%d stderr=%q\n%s", code, stderr, out)
	}
}
