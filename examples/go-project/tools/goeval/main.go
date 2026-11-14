// Command goeval adapts the Go toolchain to the Invaris mutation evaluator
// contracts: `goeval validity` and `goeval tests TestName...`.
//
// Exit 0 and 1 are reserved for completed assessments. Anything the tool
// cannot classify (no go binary, module errors, skipped or missing tests,
// disagreement between events and the go exit status) exits 2 with a stderr
// diagnostic, which Invaris records as INCONCLUSIVE. The tool is trusted to
// report what the Go toolchain printed; it is not a sandbox.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type event struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
}

var testName = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)

func main() {
	if len(os.Args) < 2 {
		os.Exit(infra(errors.New("usage: goeval validity | goeval tests TestName...")))
	}
	switch os.Args[1] {
	case "validity":
		out, code := goRun("build", "-json", "./...")
		report, exit, err := evalValidity(out, code)
		finish(report, exit, err)
	case "tests":
		names := os.Args[2:]
		if err := checkNames(names); err != nil {
			os.Exit(infra(err))
		}
		out, code := goRun("test", "-count=1", "-json", "-run", "^("+strings.Join(names, "|")+")$", "./...")
		report, exit, err := evalTests(out, code, names)
		finish(report, exit, err)
	default:
		os.Exit(infra(fmt.Errorf("unknown subcommand %q", os.Args[1])))
	}
}

func finish(report string, exit int, err error) {
	if err != nil {
		os.Exit(infra(err))
	}
	fmt.Println(report)
	os.Exit(exit)
}

func infra(err error) int {
	fmt.Fprintln(os.Stderr, "goeval:", err)
	return 2
}

// goRun runs the go tool offline with the toolchain pinned to the installed
// one. The returned code is -1 when the process could not run at all.
func goRun(args ...string) ([]byte, int) {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=-mod=readonly")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.Bytes(), 0
	case errors.As(err, &exitErr):
		return out.Bytes(), exitErr.ExitCode()
	default:
		fmt.Fprintln(os.Stderr, "goeval:", err)
		return nil, -1
	}
}

func checkNames(names []string) error {
	if len(names) == 0 {
		return errors.New("no test names given")
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !testName.MatchString(n) || seen[n] {
			return fmt.Errorf("invalid or duplicate test name %q", n)
		}
		seen[n] = true
	}
	return nil
}

func decode(out []byte) ([]event, error) {
	var events []event
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("unparsable go output: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

func evalValidity(out []byte, goExit int) (string, int, error) {
	events, err := decode(out)
	if err != nil {
		return "", 0, err
	}
	failed := false
	for _, e := range events {
		failed = failed || e.Action == "build-fail"
	}
	const format = `{"version":"invaris.validity-result/v1alpha1","status":%q}`
	switch {
	case goExit == 0 && !failed:
		return fmt.Sprintf(format, "VALID"), 0, nil
	case goExit > 0 && failed:
		return fmt.Sprintf(format, "INVALID"), 1, nil
	}
	return "", 0, fmt.Errorf("go build exit %d is not a compile verdict", goExit)
}

func evalTests(out []byte, goExit int, names []string) (string, int, error) {
	events, err := decode(out)
	if err != nil {
		return "", 0, err
	}
	status := map[string]string{}
	for _, e := range events {
		if e.Action == "build-fail" {
			return "", 0, errors.New("the tests did not build")
		}
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		switch e.Action {
		case "pass", "fail", "skip":
			status[e.Test] = e.Action
		}
	}
	exit := 0
	parts := make([]string, 0, len(names))
	for _, n := range names {
		var s string
		switch status[n] {
		case "pass":
			s = "PASS"
		case "fail":
			s, exit = "ASSERTION_FAILED", 1
		default:
			return "", 0, fmt.Errorf("test %s did not complete (%q)", n, status[n])
		}
		parts = append(parts, fmt.Sprintf(`{"id":%q,"status":%q}`, n, s))
	}
	if (goExit == 0) != (exit == 0) {
		return "", 0, fmt.Errorf("go test exit %d disagrees with the selected results", goExit)
	}
	return `{"version":"invaris.test-result/v1alpha1","tests":[` + strings.Join(parts, ",") + `]}`, exit, nil
}
