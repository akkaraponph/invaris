package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/akkaraponph/invaris/internal/testresult"
)

const testResultUsage = "invaris test-result <go-test-json|jest-json|junit-xml> [FILE|-]"

// stdin is the reader test-result uses without a FILE; tests replace it.
var stdin io.Reader = os.Stdin

// runTestResultCLI converts one reporter output into an
// invaris.test-result/v1alpha1 object on stdout. Exit 0 means every test
// passed, 1 that at least one assertion failed, and 2 that the output could
// not be converted (a build or setup failure, or unusable input), which the
// mutation engine treats as an infrastructure failure.
func runTestResultCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 || len(args) > 2 {
		return usageError(stderr, "test-result needs a format and at most one file")
	}
	in := stdin
	if len(args) == 2 && args[1] != "-" {
		f, err := os.Open(args[1])
		if err != nil {
			fmt.Fprintln(stderr, "test-result:", err)
			return 2
		}
		defer f.Close()
		in = f
	}
	cwd, _ := os.Getwd()
	result, err := testresult.Convert(args[0], in, cwd)
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
