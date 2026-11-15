package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
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
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(stderr, "usage: invaris [--report new-file.json] <run|check|challenge|mutate> [harness.json]")
		fmt.Fprintln(stderr, "       "+campaignUsage)
		return 2
	}
	action := args[0]
	if action != "run" && action != "check" && action != "challenge" && action != "mutate" {
		fmt.Fprintln(stderr, "unsupported action:", action)
		return 2
	}
	file := "invaris.json"
	if len(args) == 2 {
		file = args[1]
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
	report := engine.RunEvidence(ctx, spec, filepath.Dir(absolute), action)
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
