package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runDoctor(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runCLI(t.Context(), append([]string{"doctor"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

const doctorLastLine = "doctor executed no harness commands; results describe setup only."

func doctorHarness(t *testing.T, program string) string {
	t.Helper()
	dir := t.TempDir()
	body := `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"steps":[{"id":"s","program":"` + program + `","args":[]}]}`
	p := filepath.Join(dir, "h.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDoctorJSON(t *testing.T) {
	code, out, errText := runDoctor(t, "--format", "json", doctorHarness(t, "sh"))
	if code != 0 || errText != "" {
		t.Fatalf("exit %d stderr %q", code, errText)
	}
	var d struct {
		Version string
		Checks  []struct {
			ID       string
			Executed bool
		}
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.Version != "invaris.doctor/v1alpha1" || len(d.Checks) == 0 {
		t.Fatalf("%v %s", err, out)
	}
	for _, c := range d.Checks {
		if c.Executed {
			t.Fatalf("%s executed", c.ID)
		}
	}
}

func TestDoctorTextEndsWithNoExecutionLine(t *testing.T) {
	code, out, _ := runDoctor(t, doctorHarness(t, "sh"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if last := lines[len(lines)-1]; last != doctorLastLine || !strings.HasSuffix(out, "\n") {
		t.Fatalf("last line %q", last)
	}
	if !strings.HasPrefix(lines[0], "Invaris doctor: ") || !strings.Contains(lines[0], "(invaris.dev/v1alpha2)") {
		t.Fatalf("header %q", lines[0])
	}
	if !strings.Contains(out, "  [pass] program.s  ") || !strings.Contains(out, "  [skip] action.mutate  ") || !strings.Contains(out, "\nSummary: ") {
		t.Fatal(out)
	}
}

func TestDoctorExitCodes(t *testing.T) {
	code, out, _ := runDoctor(t, doctorHarness(t, "no-such-program-invaris"))
	if code != 1 || !strings.Contains(out, "[FAIL] program.s  ") || !strings.HasSuffix(out, doctorLastLine+"\n") {
		t.Fatalf("failing harness: exit %d\n%s", code, out)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runDoctor(t, bad); code != 1 || !strings.Contains(out, "[FAIL] harness.load") || !strings.Contains(out, "[skip] inspection") {
		t.Fatalf("invalid harness is a failed check: exit %d\n%s", code, out)
	}
	if code, _, errText := runDoctor(t, filepath.Join(t.TempDir(), "missing.json")); code != 2 || !strings.Contains(errText, "read harness:") {
		t.Fatalf("missing file: exit %d %q", code, errText)
	}
	if code, _, _ := runDoctor(t, t.TempDir()); code != 2 {
		t.Fatalf("directory: exit %d", code)
	}
}

func TestDoctorBadArguments(t *testing.T) {
	h := doctorHarness(t, "sh")
	for _, args := range [][]string{
		{"--bogus"}, {"--format"}, {"--format", "xml", h}, {"--format", "json", "--format", "text", h},
		{h, "other.json"}, {h, "--format", "json"},
	} {
		code, out, errText := runDoctor(t, args...)
		if code != 2 || out != "" || !strings.Contains(errText, "usage: "+doctorUsage) {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, out, errText)
		}
	}
}

func TestDoctorRefusesReport(t *testing.T) {
	var out, errb bytes.Buffer
	code := runCLI(t.Context(), []string{"--report", "x.json", "doctor", doctorHarness(t, "sh")}, &out, &errb)
	if code != 2 || out.Len() != 0 || errb.String() != "--report is not valid for doctor: it inspects setup and writes no report\n" {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat("x.json"); err == nil {
		t.Fatal("a report file was written")
	}
}

func TestDoctorDefaultsToInvarisJSON(t *testing.T) {
	dir := t.TempDir()
	body, err := os.ReadFile(doctorHarness(t, "sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invaris.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	code, out, errText := runDoctor(t)
	if code != 0 || errText != "" || !strings.HasPrefix(out, "Invaris doctor: invaris.json  (") {
		t.Fatalf("exit %d stderr %q\n%s", code, errText, out)
	}
	if err := os.Remove("invaris.json"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runDoctor(t); code != 2 {
		t.Fatalf("missing default harness: exit %d", code)
	}
}

func TestUsageErrorListsDoctor(t *testing.T) {
	var errb bytes.Buffer
	usageError(&errb, "x")
	if !strings.Contains(errb.String(), doctorUsage) {
		t.Fatal(errb.String())
	}
}

func TestDoctorUnreadableHarnessExits2(t *testing.T) {
	if os.Geteuid() != 0 {
		locked := doctorHarness(t, "sh")
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		if code, out, errText := runDoctor(t, locked); code != 2 || out != "" || !strings.Contains(errText, "read harness:") {
			t.Fatalf("unreadable: exit %d out %q err %q", code, out, errText)
		}
	}
	if code, _, _ := runDoctor(t, t.TempDir()); code != 2 {
		t.Fatalf("directory: exit %d", code)
	}
}

func TestDoctorDeleteHarness(t *testing.T) {
	code, out, _ := runDoctor(t, filepath.Join("..", "..", "examples", "mutation", "deleted.json"))
	if code != 0 || !strings.Contains(out, "edit.mutation-deleted") || !strings.Contains(out, "0 failed") {
		t.Fatalf("code %d\n%s", code, out)
	}
}
