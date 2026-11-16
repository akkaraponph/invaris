package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// harnessV2 is a v1alpha2 guard harness whose guard and challenge are filled in.
func harnessV2(guard, challenge string) string {
	return fmt.Sprintf(`{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"writablePaths":["source.txt"],"guards":[%s],"challenges":[%s]}`, guard, challenge)
}

func guardJSON(program, env string) string {
	return fmt.Sprintf(`{"id":"g","program":%q,"args":[],"envAllowlist":%s}`, program, env)
}

func find(t *testing.T, d Diagnosis, id string) Check {
	t.Helper()
	for _, c := range d.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, d.Checks)
	return Check{}
}

func requireStatus(t *testing.T, d Diagnosis, id, status string) Check {
	t.Helper()
	c := find(t, d, id)
	if c.Status != status {
		t.Fatalf("%s: status %s (%s), want %s", id, c.Status, c.Message, status)
	}
	return c
}

func noExecution(t *testing.T, d Diagnosis) {
	t.Helper()
	for _, c := range d.Checks {
		if c.Executed {
			t.Fatalf("%s reports executed", c.ID)
		}
	}
}

func repoFile(parts ...string) string {
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

func TestDoctorExamplesHaveNoFailures(t *testing.T) {
	cases := []struct{ path, needs string }{
		{repoFile("examples", "structured", "invaris.json"), ""},
		{repoFile("examples", "go-project", "guardrail.json"), "go"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if c.needs != "" {
				if _, err := exec.LookPath(c.needs); err != nil {
					t.Skip(c.needs + " is not on PATH")
				}
			}
			d := Inspect(c.path)
			for _, ck := range d.Checks {
				if ck.Status == Fail {
					t.Errorf("%s failed: %s", ck.ID, ck.Message)
				}
			}
			noExecution(t, d)
			if d.HarnessVersion == nil || d.Version != SchemaVersion || d.Harness != c.path {
				t.Fatalf("header: %+v", d)
			}
			requireStatus(t, d, "harness.load", Pass)
			requireStatus(t, d, "workspace.copy", Pass)
		})
	}
}

func TestDoctorActionsAndOrder(t *testing.T) {
	d := Inspect(repoFile("examples", "structured", "invaris.json"))
	var ids []string
	for _, c := range d.Checks[:5] {
		ids = append(ids, c.ID)
	}
	if got := strings.Join(ids, ","); got != "harness.load,action.run,action.check,action.challenge,action.mutate" {
		t.Fatalf("order: %s", got)
	}
	requireStatus(t, d, "action.mutate", Skipped)
	requireStatus(t, d, "action.check", Pass)
	sum := d.Summary.Pass + d.Summary.Fail + d.Summary.Warn + d.Summary.Skipped
	if sum != len(d.Checks) {
		t.Fatalf("summary %+v does not count %d checks", d.Summary, len(d.Checks))
	}
}

func TestDoctorUsesEffectivePath(t *testing.T) {
	bin := t.TempDir()
	writeFile(t, bin, "only-here", "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	dir := t.TempDir()
	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	challenge := `{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`

	h := writeFile(t, dir, "no-path.json", harnessV2(guardJSON("only-here", "[]"), challenge), 0o644)
	c := requireStatus(t, Inspect(h), "program.g", Fail)
	if !strings.Contains(c.Message, "/usr/bin:/bin") || !strings.Contains(c.Message, "not allowlisted") {
		t.Fatalf("message must name the searched PATH: %s", c.Message)
	}

	h = writeFile(t, dir, "with-path.json", harnessV2(guardJSON("only-here", `["PATH"]`), challenge), 0o644)
	c = requireStatus(t, Inspect(h), "program.g", Pass)
	if c.Message != filepath.Join(bin, "only-here") {
		t.Fatalf("resolved path: %s", c.Message)
	}
}

func TestDoctorMarkerMatchesReplace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source.txt", "SAFE and SAFE\n", 0o644)
	h := writeFile(t, dir, "h.json", harnessV2(guardJSON("sh", "[]"),
		`{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`), 0o644)
	d := Inspect(h)
	requireStatus(t, d, "target.c", Pass)
	c := requireStatus(t, d, "edit.c", Fail)
	if !strings.Contains(c.Message, "exactly once") {
		t.Fatal(c.Message)
	}

	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	requireStatus(t, Inspect(h), "edit.c", Pass)
}

func TestDoctorMissingTargetSkipsEdit(t *testing.T) {
	dir := t.TempDir()
	h := writeFile(t, dir, "h.json", harnessV2(guardJSON("sh", "[]"),
		`{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`), 0o644)
	d := Inspect(h)
	requireStatus(t, d, "target.c", Fail)
	requireStatus(t, d, "edit.c", Skipped)
	requireStatus(t, d, "writable.source.txt", Fail)
}

func TestDoctorBuiltinPreimage(t *testing.T) {
	dir := t.TempDir()
	body := "package x\n"
	sum := sha256.Sum256([]byte(body))
	digest := hex.EncodeToString(sum[:])
	writeFile(t, dir, "source.txt", body, 0o644)
	harness := func(d string) string {
		return fmt.Sprintf(`{"version":"invaris.dev/v1alpha4","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"writablePaths":["source.txt"],"policies":[{"id":"p"}],
"guards":[{"id":"g","program":"sh","args":[],"policyId":"p"}],
"challenges":[{"id":"c","guard":"g","file":"source.txt","mutation":{"operator":"trailing-whitespace","version":"1","preimageSha256":%q}}]}`, d)
	}
	h := writeFile(t, dir, "h.json", harness(digest), 0o644)
	requireStatus(t, Inspect(h), "edit.c", Pass)

	writeFile(t, dir, "source.txt", body+"changed\n", 0o644)
	c := requireStatus(t, Inspect(h), "edit.c", Fail)
	if !strings.Contains(c.Message, "preimage") {
		t.Fatal(c.Message)
	}
}

func TestDoctorLiteralPreimageChecksFirst(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "behavior.sh", "echo 'a'\n", 0o644)
	h := writeFile(t, dir, "m.json", `{"version":"invaris.dev/v1alpha3","name":"m","trustMode":"trusted-local","networkPolicy":"unrestricted",
"writablePaths":["behavior.sh"],"experiment":{"id":"x","mutation":{"id":"r","operator":"replace","path":"behavior.sh","before":"'a'","after":"'b'",
"preimageSha256":"`+strings.Repeat("0", 64)+`"},
"validation":{"id":"syntax","program":"sh","args":[]},"test":{"id":"t","program":"sh","args":[]},"testIds":["one"]}}`, 0o644)
	d := Inspect(h)
	c := requireStatus(t, d, "edit.x", Fail)
	if c.Message != "content does not match the declared preimage digest" {
		t.Fatal(c.Message)
	}
	requireStatus(t, d, "target.x", Pass)
	requireStatus(t, d, "program.syntax", Pass)
	requireStatus(t, d, "program.t", Pass)
	requireStatus(t, d, "action.mutate", Pass)
	requireStatus(t, d, "action.check", Skipped)
}

func TestDoctorSymlinkFailsWorkspaceCopy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	writeFile(t, dir, "deep/real", "x", 0o644)
	if err := os.Symlink("real", filepath.Join(dir, "deep", "link")); err != nil {
		t.Fatal(err)
	}
	h := writeFile(t, dir, "h.json", harnessV2(guardJSON("sh", "[]"),
		`{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`), 0o644)
	c := requireStatus(t, Inspect(h), "workspace.copy", Fail)
	if !strings.Contains(c.Message, "deep/link") || strings.Contains(c.Message, dir) {
		t.Fatalf("must name the relative path: %s", c.Message)
	}
}

func TestDoctorInvalidHarness(t *testing.T) {
	dir := t.TempDir()
	h := writeFile(t, dir, "h.json", "{not json", 0o644)
	d := Inspect(h)
	if len(d.Checks) != 2 || d.HarnessVersion != nil {
		t.Fatalf("%+v", d)
	}
	requireStatus(t, d, "harness.load", Fail)
	c := requireStatus(t, d, "inspection", Skipped)
	if c.Message != "harness did not load" {
		t.Fatal(c.Message)
	}
	if d.Summary != (Summary{Fail: 1, Skipped: 1}) || !d.Failed() {
		t.Fatalf("%+v", d.Summary)
	}
	out, err := d.JSON()
	if err != nil || !strings.Contains(string(out), `"harnessVersion": null`) || !strings.HasSuffix(string(out), "}\n") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestDoctorWarnsForUnsetVariable(t *testing.T) {
	t.Setenv("INVARIS_DOCTOR_SURELY_UNSET", "x")
	os.Unsetenv("INVARIS_DOCTOR_SURELY_UNSET")
	t.Setenv("INVARIS_DOCTOR_SET", "1")
	dir := t.TempDir()
	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	h := writeFile(t, dir, "h.json", harnessV2(guardJSON("sh", `["PATH","INVARIS_DOCTOR_SURELY_UNSET","INVARIS_DOCTOR_SET"]`),
		`{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`), 0o644)
	d := Inspect(h)
	c := requireStatus(t, d, "env.g.INVARIS_DOCTOR_SURELY_UNSET", Warn)
	if c.Message != "allowlisted variable is not set in this environment" {
		t.Fatal(c.Message)
	}
	for _, ck := range d.Checks {
		if ck.ID == "env.g.INVARIS_DOCTOR_SET" || ck.ID == "env.g.PATH" {
			t.Fatalf("unexpected %s", ck.ID)
		}
	}
	if d.Failed() {
		t.Fatal("a warning must not fail the diagnosis")
	}
}

func TestDoctorExecutesNothing(t *testing.T) {
	bin := t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "ran")
	writeFile(t, bin, "trap", "#!/bin/sh\necho ran > "+sentinel+"\n", 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	dir := t.TempDir()
	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	writeFile(t, dir, "local-trap", "#!/bin/sh\necho ran > "+sentinel+"\n", 0o755)

	guard := guardJSON("trap", `["PATH"]`)
	harnesses := map[string]string{
		"guard": harnessV2(guard, `{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`),
		"step": `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"steps":[{"id":"s","program":"trap","args":[],"envAllowlist":["PATH"]},{"id":"s2","program":"./local-trap","args":[]}]}`,
		"adapter": `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"policies":[{"id":"p"}],"adapters":[{"id":"prov","program":"trap","args":[],"envAllowlist":["PATH"]}],"requiredAdapters":["prov"],
"guards":[{"id":"pg","adapterId":"prov","policyId":"p","scope":["source.txt"]}]}`,
	}
	for name, body := range harnesses {
		t.Run(name, func(t *testing.T) {
			h := writeFile(t, dir, name+".json", body, 0o644)
			d := Inspect(h)
			noExecution(t, d)
			if name == "adapter" {
				requireStatus(t, d, "provider.prov", Skipped)
				requireStatus(t, d, "program.adapter.prov", Pass)
			}
			if d.Failed() {
				t.Fatalf("%+v", d.Checks)
			}
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("doctor ran a harness program")
			}
		})
	}
}

const challengeC = `{"id":"c","guard":"g","file":"source.txt","before":"SAFE","after":"BAD","expectedOutput":"BAD"}`

func stepOnlyHarness(extra string) string {
	return `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",` + extra + `
"steps":[{"id":"s","program":"sh","args":[]}]}`
}

func TestDoctorWorkspaceCopyOnlyForEdits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real", "x", 0o644)
	if err := os.Symlink("real", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	h := writeFile(t, dir, "steps.json", stepOnlyHarness(""), 0o644)
	d := Inspect(h)
	c := requireStatus(t, d, "workspace.copy", Skipped)
	if c.Message != "no challenge or experiment prepares a workspace" || d.Failed() {
		t.Fatalf("%s %+v", c.Message, d.Checks)
	}
	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	h = writeFile(t, dir, "chal.json", harnessV2(guardJSON("sh", "[]"), challengeC), 0o644)
	c = requireStatus(t, Inspect(h), "workspace.copy", Fail)
	if !strings.Contains(c.Message, "link") {
		t.Fatal(c.Message)
	}
}

func TestDoctorCheckIDsAreUnique(t *testing.T) {
	t.Setenv("INVARIS_DOCTOR_SURELY_UNSET", "x")
	os.Unsetenv("INVARIS_DOCTOR_SURELY_UNSET")
	dir := t.TempDir()
	h := writeFile(t, dir, "h.json", `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"policies":[{"id":"p"}],"adapters":[{"id":"x","program":"sh","args":[],"envAllowlist":["INVARIS_DOCTOR_SURELY_UNSET"]}],"requiredAdapters":["x"],
"steps":[{"id":"x","program":"sh","args":[],"envAllowlist":["INVARIS_DOCTOR_SURELY_UNSET"]}],
"guards":[{"id":"pg","adapterId":"x","policyId":"p","scope":["a.txt"]}]}`, 0o644)
	d := Inspect(h)
	seen := map[string]bool{}
	for _, c := range d.Checks {
		if seen[c.ID] {
			t.Fatalf("duplicate id %s", c.ID)
		}
		seen[c.ID] = true
	}
	for _, id := range []string{"program.x", "program.adapter.x", "env.x.INVARIS_DOCTOR_SURELY_UNSET", "env.adapter.x.INVARIS_DOCTOR_SURELY_UNSET", "provider.x"} {
		find(t, d, id)
	}
}

func TestDoctorOmittedTargets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".git/x", "SAFE\n", 0o644)
	writeFile(t, dir, ".invaris/y", "SAFE\n", 0o644)
	body := `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"writablePaths":[".git/x",".invaris/y"],"guards":[{"id":"g","program":"sh","args":[]}],"challenges":[
{"id":"c","guard":"g","file":".git/x","before":"SAFE","after":"BAD","expectedOutput":"BAD"},
{"id":"d","guard":"g","file":".invaris/y","before":"SAFE","after":"BAD","expectedOutput":"BAD"}]}`
	d := Inspect(writeFile(t, dir, "h.json", body, 0o644))
	for _, id := range []string{"target.c", "target.d", "writable..git/x", "writable..invaris/y"} {
		c := requireStatus(t, d, id, Fail)
		if !strings.Contains(c.Message, "is inside .git or .invaris, which the workspace copy omits") {
			t.Fatalf("%s: %s", id, c.Message)
		}
	}
}

func TestDoctorRelativeProgramWarnings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok", "#!/bin/sh\n", 0o755)
	writeFile(t, dir, ".git/hook", "#!/bin/sh\n", 0o755)
	writeFile(t, dir, "sub/x", "#!/bin/sh\n", 0o755)
	for program, want := range map[string]string{
		"./ok": Pass, ".git/hook": Warn, "./.git/hook": Warn, "sub/../ok": Warn, "./missing": Fail,
	} {
		h := writeFile(t, dir, "h.json", `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"steps":[{"id":"s","program":`+fmt.Sprintf("%q", program)+`,"args":[]}]}`, 0o644)
		c := requireStatus(t, Inspect(h), "program.s", want)
		if want == Warn && !strings.Contains(c.Message, "inside the workspace copy") {
			t.Fatalf("%s: %s", program, c.Message)
		}
	}
}

func TestDoctorErrorsDoNotLeakDirectory(t *testing.T) {
	dir := t.TempDir()
	check := func(what, msg, reason string) {
		t.Helper()
		head := msg
		if len(head) > 100 {
			head = head[:100]
		}
		if !strings.Contains(head, reason) || strings.Contains(msg, dir) {
			t.Fatalf("%s: %q", what, msg)
		}
	}
	if os.Geteuid() != 0 {
		h := writeFile(t, dir, "locked.json", stepOnlyHarness(""), 0o000)
		check("unreadable", requireStatus(t, Inspect(h), "harness.load", Fail).Message, "permission denied")
	}
	h := writeFile(t, dir, "bad.json", "{not json", 0o644)
	check("invalid", requireStatus(t, Inspect(h), "harness.load", Fail).Message, "invalid")
	h = writeFile(t, dir, "prog.json", `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"steps":[{"id":"s","program":"./nope/x","args":[]}]}`, 0o644)
	check("program", requireStatus(t, Inspect(h), "program.s", Fail).Message, "no such file")
}

func TestDoctorWritableWarnsWhenUnused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source.txt", "SAFE\n", 0o644)
	h := writeFile(t, dir, "h.json", `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"writablePaths":["source.txt","extra.txt"],"guards":[{"id":"g","program":"sh","args":[]}],"challenges":[`+challengeC+`]}`, 0o644)
	d := Inspect(h)
	c := requireStatus(t, d, "writable.extra.txt", Warn)
	if c.Message != "extra.txt is listed in writablePaths but does not exist; no edit uses it" || d.Failed() {
		t.Fatalf("%s %+v", c.Message, d.Checks)
	}
	// an edit that targets a missing writable path still fails
	h = writeFile(t, dir, "h3.json", `{"version":"invaris.dev/v1alpha2","name":"t","trustMode":"trusted-local","networkPolicy":"unrestricted",
"writablePaths":["gone.txt"],"guards":[{"id":"g","program":"sh","args":[]}],"challenges":[`+strings.Replace(challengeC, "source.txt", "gone.txt", 1)+`]}`, 0o644)
	requireStatus(t, Inspect(h), "writable.gone.txt", Fail)
}

func TestDoctorChallengeActionSkippedWithoutChallenges(t *testing.T) {
	dir := t.TempDir()
	d := Inspect(writeFile(t, dir, "h.json", stepOnlyHarness(""), 0o644))
	c := requireStatus(t, d, "action.challenge", Skipped)
	if c.Message != "no challenges declared" {
		t.Fatal(c.Message)
	}
	requireStatus(t, d, "action.run", Pass)
	requireStatus(t, d, "action.check", Pass)
}
