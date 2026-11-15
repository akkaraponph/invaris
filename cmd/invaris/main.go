package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/engine"
	"github.com/akkaraponph/invaris/internal/evidence"
)

func main() { os.Exit(mainExit()) }

// mainExit owns the process signal context and delegates to runCLI.
func mainExit() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

// runCLI returns the process exit code: 0 ACCEPT, 1 REJECT, 2 otherwise. The report
// goes to stdout only after any requested report file has been published, so a
// publication failure never leaves a verdict on stdout.
func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var reportPath string
	if len(args) >= 2 && args[0] == "--report" {
		reportPath, args = args[1], args[2:]
		if reportPath == "" {
			fmt.Fprintln(stderr, "report path is empty")
			return 2
		}
	}
	if len(args) >= 1 && (args[0] == "init" || args[0] == "migrate") {
		if reportPath != "" {
			fmt.Fprintf(stderr, "--report is not valid for %s: authoring output is not verification evidence\n", args[0])
			return 2
		}
		if args[0] == "init" {
			return runInitCLI(args[1:], stdout, stderr)
		}
		return runMigrateCLI(args[1:], stdout, stderr)
	}
	if len(args) >= 1 && args[0] == "campaign" {
		return runCampaignCLI(ctx, reportPath, args[1:], stdout, stderr)
	}
	if len(args) < 1 {
		return usageError(stderr, "missing action")
	}
	action := args[0]
	if action != "run" && action != "check" && action != "challenge" && action != "mutate" {
		fmt.Fprintln(stderr, "unsupported action:", action)
		return 2
	}
	format, file, v5, err := actionArgs(action, args[1:])
	if err != nil {
		return usageError(stderr, err.Error())
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	spec, err := engine.Load(absolute)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !spec.SupportsAction(action) {
		fmt.Fprintln(stderr, "unsupported action for harness version:", action, spec.Version)
		return 2
	}
	if reportPath != "" {
		reportPath, err = prepareReport(reportPath)
		if err != nil {
			fmt.Fprintln(stderr, "prepare report:", err)
			return 2
		}
	}
	// The evidence version is fixed before anything runs: requested, or
	// required by a v1alpha4 harness.
	var report evidence.Report
	if v5 || engine.RequiresV5(spec) {
		report = engine.RunEvidenceV5(ctx, spec, filepath.Dir(absolute), action)
	} else {
		report = engine.RunEvidence(ctx, spec, filepath.Dir(absolute), action)
	}
	data, err := evidence.Marshal(report)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if reportPath != "" {
		if err := saveReport(reportPath, data); err != nil {
			fmt.Fprintln(stderr, "save report:", err)
			return 2
		}
	}
	if format != formatJSON {
		if data, err = formatReport(report, format); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "write stdout:", err)
		return 2
	}
	switch report.Decision {
	case domain.Accept:
		return 0
	case domain.Reject:
		return 1
	}
	return 2
}

func usageError(stderr io.Writer, problem string) int {
	fmt.Fprintln(stderr, problem)
	fmt.Fprintln(stderr, "usage: invaris [--report new-file.json] <run|mutate> [--evidence-version v1alpha5] [harness.json]")
	fmt.Fprintln(stderr, "       invaris [--report new-file.json] <check|challenge> [--format json|text|compact] [--evidence-version v1alpha5] [harness.json]")
	fmt.Fprintln(stderr, "       "+campaignUsage)
	fmt.Fprintln(stderr, "       "+initUsage)
	fmt.Fprintln(stderr, "       "+migrateUsage)
	return 2
}

// evidenceV5Flag is the only value --evidence-version accepts. Older report
// versions are the defaults for older inputs and need no flag.
const evidenceV5Flag = "v1alpha5"

// actionArgs parses the arguments after run, check, challenge or mutate:
// optional flags first, then at most one harness file. Only arguments that
// start with "--" are flags, so a harness file name that starts with a single
// "-" keeps working as before. Problems are reported before anything runs.
func actionArgs(action string, args []string) (format, file string, v5 bool, err error) {
	format, file = formatJSON, "invaris.json"
	seen := map[string]bool{}
	seenFile := false
	value := func(i int, flag string) (string, error) {
		if seen[flag] {
			return "", fmt.Errorf("%s is repeated", flag)
		}
		seen[flag] = true
		if i+1 >= len(args) || args[i+1] == "" {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case seenFile:
			if strings.HasPrefix(arg, "--") {
				return "", "", false, fmt.Errorf("flags must come before the harness file: %s", arg)
			}
			return "", "", false, fmt.Errorf("unexpected argument: %s", arg)
		case arg == "--format":
			if action == "run" || action == "mutate" {
				return "", "", false, fmt.Errorf("--format is not available for %s; its output is JSON only", action)
			}
			if format, err = value(i, arg); err != nil {
				return "", "", false, err
			}
			i++
			if !validFormat(format) {
				return "", "", false, fmt.Errorf("unsupported format: %s", format)
			}
		case arg == "--evidence-version":
			v, err := value(i, arg)
			if err != nil {
				return "", "", false, err
			}
			i++
			if v != evidenceV5Flag {
				return "", "", false, fmt.Errorf("unsupported evidence version: %s (only %s can be requested)", v, evidenceV5Flag)
			}
			v5 = true
		case strings.HasPrefix(arg, "--"):
			return "", "", false, fmt.Errorf("unknown flag: %s", arg)
		default:
			file, seenFile = arg, true
		}
	}
	return format, file, v5, nil
}
