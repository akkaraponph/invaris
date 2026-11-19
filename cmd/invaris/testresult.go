package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/akkaraponph/invaris/internal/testresult"
)

const testResultUsage = "invaris test-result <go-test-json|jest-json|junit-xml> [--test-id ID]... [FILE|-]"

// stdin is the reader test-result uses without a FILE; tests replace it.
var stdin io.Reader = os.Stdin

// runTestResultCLI converts one reporter output into an
// invaris.test-result/v1alpha1 object on stdout. Exit 0 means every test
// passed, 1 that at least one assertion failed, and 2 that the output could
// not be converted (a build or setup failure, or unusable input), which the
// mutation engine treats as an infrastructure failure. Each --test-id keeps
// only the named tests, in order, so a whole-suite run matches a harness's
// testIds; the exit status then reflects only those tests.
func runTestResultCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		return usageError(stderr, "test-result needs a format and at most one file")
	}
	format, rest := args[0], args[1:]
	var ids []string
	var file string
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "--test-id":
			if i+1 >= len(rest) || rest[i+1] == "" {
				return usageError(stderr, "--test-id needs a test ID")
			}
			i++
			ids = append(ids, rest[i])
		case strings.HasPrefix(arg, "--test-id="):
			id := strings.TrimPrefix(arg, "--test-id=")
			if id == "" {
				return usageError(stderr, "--test-id needs a test ID")
			}
			ids = append(ids, id)
		case file == "" && (arg == "-" || !strings.HasPrefix(arg, "-")):
			file = arg
		default:
			return usageError(stderr, "test-result needs a format and at most one file")
		}
	}
	in := stdin
	if file != "" && file != "-" {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintln(stderr, "test-result:", err)
			return 2
		}
		defer f.Close()
		in = f
	}
	cwd, _ := os.Getwd()
	result, err := testresult.Convert(format, in, cwd)
	if err == nil && len(ids) > 0 {
		result, err = result.Select(ids)
	}
	if err != nil {
		fmt.Fprintln(stderr, "test-result:", err)
		return 2
	}
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(stderr, "test-result:", err)
		return 2
	}
	if _, err := fmt.Fprintf(stdout, "%s\n", data); err != nil {
		return 2
	}
	if result.Failed() {
		return 1
	}
	return 0
}
