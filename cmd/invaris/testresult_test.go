package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func runTestResult(t *testing.T, input string, args ...string) (int, string, string) {
	t.Helper()
	if input != "" {
		old := stdin
		stdin = strings.NewReader(input)
		t.Cleanup(func() { stdin = old })
	}
	var out, errb bytes.Buffer
	code := runCLI(t.Context(), append([]string{"test-result"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestTestResultCLI(t *testing.T) {
	fixture := filepath.Join("..", "..", "internal", "testresult", "testdata")
	code, out, _ := runTestResult(t, "", "junit-xml", filepath.Join(fixture, "pytest-junit.xml"))
	if code != 1 || !strings.HasPrefix(out, `{"version":"invaris.test-result/v1alpha1","tests":[{"id":"test_math::test_adds","status":"PASS"}`) {
		t.Fatalf("code %d out %s", code, out)
	}
	code, out, _ = runTestResult(t, `{"Action":"pass","Package":"p","Test":"TestA"}`+"\n", "go-test-json")
	if code != 0 || out != `{"version":"invaris.test-result/v1alpha1","tests":[{"id":"p.TestA","status":"PASS"}]}`+"\n" {
		t.Fatalf("stdin: code %d out %s", code, out)
	}
	code, out, errText := runTestResult(t, "", "go-test-json", filepath.Join(fixture, "go-test-buildfail.json"))
	if code != 2 || out != "" || !strings.Contains(errText, "did not build") {
		t.Fatalf("build failure: code %d out %q err %q", code, out, errText)
	}
	if code, _, _ := runTestResult(t, "", "tap"); code != 2 {
		t.Fatalf("unknown format: code %d", code)
	}
	var o, e bytes.Buffer
	if code := runCLI(t.Context(), []string{"--report", "x.json", "test-result", "go-test-json"}, &o, &e); code != 2 || !strings.Contains(e.String(), "not valid for test-result") {
		t.Fatalf("--report: code %d %s", code, e.String())
	}
}

// A whole-suite run must be narrowed to a harness's testIds: the mutation
// engine refuses results with any other set of tests.
func TestTestResultSelectsTestIDs(t *testing.T) {
	suite := `{"Action":"pass","Package":"p","Test":"TestA"}` + "\n" +
		`{"Action":"fail","Package":"p","Test":"TestB"}` + "\n" +
		`{"Action":"pass","Package":"p","Test":"TestC"}` + "\n"
	code, out, errText := runTestResult(t, suite, "go-test-json", "--test-id", "p.TestC", "--test-id=p.TestA")
	if code != 0 || out != `{"version":"invaris.test-result/v1alpha1","tests":[{"id":"p.TestC","status":"PASS"},{"id":"p.TestA","status":"PASS"}]}`+"\n" {
		t.Fatalf("unselected failure must not count: code %d out %s err %s", code, out, errText)
	}
	if code, _, _ := runTestResult(t, suite, "go-test-json", "--test-id", "p.TestB", "-"); code != 1 {
		t.Fatalf("selected failure: code %d", code)
	}
	code, out, errText = runTestResult(t, suite, "go-test-json", "--test-id", "p.TestMissing")
	if code != 2 || out != "" || !strings.Contains(errText, "not in the reporter output") {
		t.Fatalf("missing selection: code %d out %q err %q", code, out, errText)
	}
	for _, args := range [][]string{{"go-test-json", "--test-id"}, {"go-test-json", "--test-id="}, {"go-test-json", "a", "b"}, {"go-test-json", "--bogus"}} {
		if code, _, _ := runTestResult(t, suite, args...); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
}

func TestActionHelp(t *testing.T) {
	for _, action := range []string{"run", "check", "challenge", "mutate", "campaign", "init", "migrate", "explain", "doctor", "test-result"} {
		for _, flag := range []string{"--help", "-h"} {
			var out, errb bytes.Buffer
			code := runCLI(t.Context(), []string{action, flag}, &out, &errb)
			if code != 0 || !strings.HasPrefix(out.String(), "usage: invaris") || !strings.Contains(out.String(), action) || errb.Len() != 0 {
				t.Fatalf("%s %s: code %d out %q err %q", action, flag, code, out.String(), errb.String())
			}
		}
	}
	var out, errb bytes.Buffer
	if code := runCLI(t.Context(), []string{"bogus", "--help"}, &out, &errb); code != 2 {
		t.Fatalf("unknown action help: code %d", code)
	}
}
