package testresult

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

func convertFile(t *testing.T, format, name string) (Result, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return Convert(format, f, "/work")
}

func TestConvertFormats(t *testing.T) {
	const pass, fail = domain.TestPass, domain.TestAssertionFailed
	for _, tc := range []struct {
		format, file string
		want         []domain.TestCaseResult
	}{
		{"go-test-json", "go-test.json", []domain.TestCaseResult{
			{ID: "example.com/m.TestAdds", Status: pass},
			{ID: "example.com/m.TestSubtracts", Status: fail},
			{ID: "example.com/m.TestNested/multiplies", Status: pass},
			{ID: "example.com/m.TestNested", Status: pass},
		}},
		{"jest-json", "jest.json", []domain.TestCaseResult{
			{ID: "tests/math.jest.cjs::math adds", Status: pass},
			{ID: "tests/math.jest.cjs::math subtracts", Status: fail},
			{ID: "tests/math.jest.cjs::math nested multiplies", Status: pass},
		}},
		{"jest-json", "vitest.json", []domain.TestCaseResult{
			{ID: "tests/math.test.js::math adds", Status: pass},
			{ID: "tests/math.test.js::math subtracts", Status: fail},
			{ID: "tests/math.test.js::math nested multiplies", Status: pass},
		}},
		{"junit-xml", "pytest-junit.xml", []domain.TestCaseResult{
			{ID: "test_math::test_adds", Status: pass},
			{ID: "test_math::test_subtracts", Status: fail},
			{ID: "test_math.TestNested::test_multiplies", Status: pass},
		}},
	} {
		got, err := convertFile(t, tc.format, tc.file)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if got.Version != Version || len(got.Tests) != len(tc.want) {
			t.Fatalf("%s: got %+v", tc.file, got)
		}
		for i := range tc.want {
			if got.Tests[i] != tc.want[i] {
				t.Errorf("%s: test %d = %+v, want %+v", tc.file, i, got.Tests[i], tc.want[i])
			}
		}
		if !got.Failed() {
			t.Errorf("%s: a failed test was not reported", tc.file)
		}
	}
}

func TestGoBuildFailureIsInfrastructure(t *testing.T) {
	if _, err := convertFile(t, "go-test-json", "go-test-buildfail.json"); err == nil || !strings.Contains(err.Error(), "did not build") {
		t.Fatalf("err = %v", err)
	}
	// A failed package with no test events is refused even without build events.
	_, err := Convert("go-test-json", strings.NewReader(`{"Action":"start","Package":"p"}
{"Action":"fail","Package":"p"}`), "")
	if err == nil || !strings.Contains(err.Error(), "without a failed test") {
		t.Fatalf("err = %v", err)
	}
}

// A package that fails while all its reported tests pass failed outside an
// assertion; converting it to all-PASS would hide the failure.
func TestGoFailureOutsideTestsIsInfrastructure(t *testing.T) {
	for _, f := range []string{"go-test-timeout.json", "go-test-mainexit.json"} {
		if _, err := convertFile(t, "go-test-json", f); err == nil || !strings.Contains(err.Error(), "without a failed test") {
			t.Errorf("%s: err = %v", f, err)
		}
	}
	// Package paths may contain dots: a failed test in x/a.b does not
	// account for a failure of package x/a.
	_, err := Convert("go-test-json", strings.NewReader(`{"Action":"pass","Package":"x/a","Test":"TestA"}
{"Action":"fail","Package":"x/a.b","Test":"TestB"}
{"Action":"fail","Package":"x/a.b"}
{"Action":"fail","Package":"x/a"}`), "")
	if err == nil || !strings.Contains(err.Error(), "package x/a failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestSuiteLoadFailureIsInfrastructure(t *testing.T) {
	for _, f := range []string{"jest-loadfail.json", "vitest-loadfail.json", "jest-hookfail.json", "vitest-hookfail.json"} {
		if _, err := convertFile(t, "jest-json", f); err == nil || !strings.Contains(err.Error(), "failed outside its tests") {
			t.Errorf("%s: err = %v", f, err)
		}
	}
	if _, err := convertFile(t, "junit-xml", "pytest-junit-error.xml"); err == nil || !strings.Contains(err.Error(), "test_math::test_errors errored") {
		t.Errorf("pytest error: err = %v", err)
	}
}

func TestDuplicateIDs(t *testing.T) {
	_, err := Convert("junit-xml", strings.NewReader(`<testsuite><testcase classname="a" name="b"/><testcase classname="a" name="b"/></testsuite>`), "")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
}

func TestConvertRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ format, input string }{
		"unknown format":   {"tap", "ok 1"},
		"not go json":      {"go-test-json", "ok example.com/m 0.1s"},
		"not jest":         {"jest-json", `{"tests":[]}`},
		"not junit":        {"junit-xml", `<html/>`},
		"only skipped":     {"junit-xml", `<testsuite><testcase classname="a" name="b"><skipped/></testcase></testsuite>`},
		"unknown status":   {"jest-json", `{"testResults":[{"name":"a","status":"passed","assertionResults":[{"fullName":"x","status":"flaky"}]}]}`},
		"oversized output": {"junit-xml", strings.Repeat(" ", MaxInputBytes+1)},
	} {
		if _, err := Convert(tc.format, strings.NewReader(tc.input), ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSelectKeepsNamedTestsInOrder(t *testing.T) {
	r := Result{Version: Version, Tests: []domain.TestCaseResult{
		{ID: "a", Status: domain.TestPass},
		{ID: "b", Status: domain.TestAssertionFailed},
		{ID: "c", Status: domain.TestPass},
	}}
	got, err := r.Select([]string{"c", "a"})
	if err != nil || len(got.Tests) != 2 || got.Tests[0].ID != "c" || got.Tests[1].ID != "a" || got.Failed() {
		t.Fatalf("select = %+v, %v", got, err)
	}
	if _, err := r.Select([]string{"a", "missing"}); err == nil || !strings.Contains(err.Error(), "not in the reporter output") {
		t.Fatalf("missing ID: %v", err)
	}
	if _, err := r.Select([]string{"a", "a"}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate ID: %v", err)
	}
}
