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
