package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/akkaraponph/invaris/internal/runner"
)

const validityResultUsage = "invaris validity-result tsc <executable> <tsconfig.json>"

var tsDiagnostic = regexp.MustCompile(`^(.+)\([0-9]+,[0-9]+\): error TS([0-9]+): `)

// runValidityResultCLI executes a bounded no-emit TypeScript check. A global,
// configuration, dependency, or unrecognized diagnostic leaves the experiment
// inconclusive because it cannot reliably be attributed to the mutant.
func runValidityResultCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[0] != "tsc" || args[1] == "" || args[2] == "" {
		return usageError(stderr, validityResultUsage)
	}
	project, err := filepath.Abs(args[2])
	if err != nil {
		fmt.Fprintln(stderr, "validity-result:", err)
		return 2
	}
	info, err := os.Stat(project)
	if err != nil || !info.Mode().IsRegular() {
		fmt.Fprintln(stderr, "validity-result: expected a TypeScript project file:", args[2])
		return 2
	}
	cache, err := os.MkdirTemp("", "invaris-tsc-")
	if err != nil {
		fmt.Fprintln(stderr, "validity-result:", err)
		return 2
	}
	defer os.RemoveAll(cache)
	result := runner.Run(ctx, ".", runner.Command{
		Program:      args[1],
		Args:         []string{"--project", project, "--noEmit", "--pretty", "false", "--incremental", "--tsBuildInfoFile", filepath.Join(cache, "build.tsbuildinfo")},
		EnvAllowlist: []string{"PATH"},
	})
	status, code := "VALID", 0
	var exitErr *exec.ExitError
	switch {
	case result.Err == nil && strings.TrimSpace(result.Stdout+result.Stderr) == "":
	case errors.As(result.Err, &exitErr) && result.ExitCode != nil && (*result.ExitCode == 1 || *result.ExitCode == 2) &&
		typeScriptSourceErrors(result.Stdout+"\n"+result.Stderr):
		status, code = "INVALID", 1
	default:
		fmt.Fprintln(stderr, "validity-result: TypeScript check did not produce a conclusive result")
		if result.Err != nil {
			fmt.Fprintln(stderr, result.Err)
		}
		fmt.Fprint(stderr, result.Stdout, result.Stderr)
		return 2
	}
	if err := json.NewEncoder(stdout).Encode(struct {
		Version string `json:"version"`
		Status  string `json:"status"`
	}{"invaris.validity-result/v1alpha1", status}); err != nil {
		return 2
	}
	return code
}

func typeScriptSourceErrors(output string) bool {
	diagnostic := false
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" || diagnostic && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		match := tsDiagnostic.FindStringSubmatch(line)
		if match == nil || !typeScriptSourceFile(match[1]) {
			return false
		}
		code, _ := strconv.Atoi(match[2])
		switch code {
		case 2307, 2688, 2792, 2821, 2882, 7016:
			return false // missing modules or declarations can be setup failures
		}
		if code < 1000 || code >= 3000 {
			return false // restrict to source parse and semantic diagnostics
		}
		diagnostic = true
	}
	return diagnostic
}

func typeScriptSourceFile(name string) bool {
	for _, segment := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		if segment == "node_modules" {
			return false
		}
	}
	switch filepath.Ext(name) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		info, err := os.Stat(name)
		return err == nil && info.Mode().IsRegular()
	default:
		return false
	}
}
