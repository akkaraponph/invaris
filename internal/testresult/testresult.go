// Package testresult converts the output of common test reporters into the
// invaris.test-result/v1alpha1 object that mutation test commands print.
//
// Conversion is deliberately strict. Skipped tests are omitted, so a selected
// test that was skipped makes the experiment inconclusive rather than passing.
// Anything that is not a completed assertion outcome (a package that did not
// build, a timeout, a suite that failed to load or whose hooks threw, a pytest
// setup error) is an error, which
// the CLI reports as an infrastructure failure (exit 2) so that it can never
// count as a killed mutant.
package testresult

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
)

// Version is the result object version every conversion produces.
const Version = "invaris.test-result/v1alpha1"

// MaxInputBytes bounds the reporter output read.
const MaxInputBytes = 16 << 20

// Formats lists the accepted format names.
var Formats = []string{"go-test-json", "jest-json", "junit-xml"}

// Result is one invaris.test-result/v1alpha1 object.
type Result struct {
	Version string                  `json:"version"`
	Tests   []domain.TestCaseResult `json:"tests"`
}

// Failed reports whether any test has ASSERTION_FAILED.
func (r Result) Failed() bool {
	for _, t := range r.Tests {
		if t.Status == domain.TestAssertionFailed {
			return true
		}
	}
	return false
}

// Convert reads one reporter output in format and returns the result. cwd is
// the directory jest-json file names are made relative to; empty keeps them.
func Convert(format string, r io.Reader, cwd string) (Result, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > MaxInputBytes {
		return Result{}, fmt.Errorf("reporter output exceeds %d bytes", MaxInputBytes)
	}
	var c collector
	switch format {
	case "go-test-json":
		err = goTest(data, &c)
	case "jest-json":
		err = jest(data, cwd, &c)
	case "junit-xml":
		err = junit(data, &c)
	default:
		return Result{}, fmt.Errorf("unknown format %q (want one of %s)", format, strings.Join(Formats, ", "))
	}
	if err != nil {
		return Result{}, err
	}
	if len(c.tests) == 0 {
		return Result{}, errors.New("no passed or failed tests in the reporter output")
	}
	return Result{Version: Version, Tests: c.tests}, nil
}

// collector keeps tests in first-seen order and refuses duplicate IDs.
type collector struct {
	tests []domain.TestCaseResult
	index map[string]int
}

func (c *collector) add(id, status string) error {
	if c.index == nil {
		c.index = map[string]int{}
	}
	if _, dup := c.index[id]; dup {
		return fmt.Errorf("duplicate test ID %q", id)
	}
	c.index[id] = len(c.tests)
	c.tests = append(c.tests, domain.TestCaseResult{ID: id, Status: status})
	return nil
}

// goTest reads `go test -json`. The test ID is Package.Test, with subtests as
// Package.TestX/sub. The last pass, fail or skip event of a test decides it.
func goTest(data []byte, c *collector) error {
	type event struct {
		Action, Package, Test, ImportPath string
	}
	final := map[string]string{}
	var order []string
	var failedPackages []string
	pkg := map[string]string{} // test ID -> package
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), MaxInputBytes)
	for line := 1; scanner.Scan(); line++ {
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(text, &e); err != nil {
			return fmt.Errorf("line %d is not a go test -json event", line)
		}
		switch {
		case e.Action == "build-fail":
			return fmt.Errorf("%s did not build", e.ImportPath)
		case e.Test == "" && e.Action == "fail":
			failedPackages = append(failedPackages, e.Package)
		case e.Test != "" && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip"):
			id := e.Package + "." + e.Test
			if _, seen := final[id]; !seen {
				order = append(order, id)
			}
			final[id] = e.Action
			pkg[id] = e.Package
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// A package that failed with no failed test failed outside an assertion:
	// a build or setup error, a timeout (the running test gets no event) or
	// TestMain exiting nonzero. Passing its tests would hide that failure.
	for _, p := range failedPackages {
		failed := false
		for id, action := range final {
			failed = failed || action == "fail" && pkg[id] == p
		}
		if !failed {
			return fmt.Errorf("package %s failed without a failed test (build, setup, timeout or TestMain failure)", p)
		}
	}
	for _, id := range order {
		var err error
		switch final[id] {
		case "pass":
			err = c.add(id, domain.TestPass)
		case "fail":
			err = c.add(id, domain.TestAssertionFailed)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// jest reads jest --json, which vitest --reporter=json also writes. The test
// ID is the file name relative to cwd, "::", and the assertion's fullName.
type assertion struct {
	FullName string `json:"fullName"`
	Status   string `json:"status"`
}

func jest(data []byte, cwd string, c *collector) error {
	var doc struct {
		TestResults *[]struct {
			Name             string      `json:"name"`
			Status           string      `json:"status"`
			Message          string      `json:"message"`
			AssertionResults []assertion `json:"assertionResults"`
		} `json:"testResults"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.TestResults == nil {
		return errors.New("input is not a jest or vitest JSON report (no testResults)")
	}
	for _, file := range *doc.TestResults {
		name := filepath.ToSlash(file.Name)
		if cwd != "" && filepath.IsAbs(file.Name) {
			if rel, err := filepath.Rel(cwd, file.Name); err == nil {
				name = filepath.ToSlash(rel)
			}
		}
		// A failed file with no failed assertion failed outside a test: it
		// did not load, or a hook such as afterAll threw.
		if file.Status == "failed" && !slices.ContainsFunc(file.AssertionResults, func(a assertion) bool { return a.Status == "failed" }) {
			first, _, _ := strings.Cut(strings.TrimSpace(file.Message), "\n")
			return fmt.Errorf("test file %s failed outside its tests: %s", name, first)
		}
		for _, a := range file.AssertionResults {
			id := name + "::" + a.FullName
			var err error
			switch a.Status {
			case "passed":
				err = c.add(id, domain.TestPass)
			case "failed":
				err = c.add(id, domain.TestAssertionFailed)
			case "pending", "skipped", "todo", "disabled":
			default:
				err = fmt.Errorf("test %s has unknown status %q", id, a.Status)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// junit reads JUnit XML as pytest --junitxml writes it. The test ID is the
// testcase classname, "::", and its name.
func junit(data []byte, c *collector) error {
	type marker struct {
		Message string `xml:"message,attr"`
	}
	type testcase struct {
		Classname string  `xml:"classname,attr"`
		Name      string  `xml:"name,attr"`
		Failure   *marker `xml:"failure"`
		Error     *marker `xml:"error"`
		Skipped   *marker `xml:"skipped"`
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	sawRoot := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("input is not JUnit XML: %v", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "testsuites", "testsuite":
			sawRoot = true
		case "testcase":
			var tc testcase
			if err := d.DecodeElement(&tc, &start); err != nil {
				return fmt.Errorf("invalid testcase: %v", err)
			}
			id := tc.Classname + "::" + tc.Name
			switch {
			case tc.Error != nil:
				return fmt.Errorf("test %s errored: %s", id, tc.Error.Message)
			case tc.Skipped != nil:
			case tc.Failure != nil:
				err = c.add(id, domain.TestAssertionFailed)
			default:
				err = c.add(id, domain.TestPass)
			}
			if err != nil {
				return err
			}
		}
	}
	if !sawRoot {
		return errors.New("input is not JUnit XML (no testsuite element)")
	}
	return nil
}
