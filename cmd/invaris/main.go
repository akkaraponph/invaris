package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/akkaraponph/invaris/internal/engine"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() { os.Exit(mainExit()) }
func mainExit() int {
	args := os.Args[1:]
	var reportPath string
	if len(args) >= 2 && args[0] == "--report" {
		reportPath, args = args[1], args[2:]
		if reportPath == "" {
			fmt.Fprintln(os.Stderr, "report path is empty")
			return 2
		}
	}
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: invaris [--report new-file.json] <run|check|challenge> [harness.json]")
		return 2
	}
	action := args[0]
	if action != "run" && action != "check" && action != "challenge" {
		fmt.Fprintln(os.Stderr, "unsupported action:", action)
		return 2
	}
	file := "invaris.json"
	if len(args) == 2 {
		file = args[1]
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	spec, err := engine.Load(absolute)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if reportPath != "" {
		reportPath, err = prepareReport(reportPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "prepare report:", err)
			return 2
		}
	}
	root := filepath.Dir(absolute)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report := engine.Run(ctx, spec, root, action)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	data = append(data, '\n')
	if reportPath != "" {
		if err := saveReport(reportPath, data); err != nil {
			fmt.Fprintln(os.Stderr, "save report:", err)
			return 2
		}
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if report.Status == "PASS" {
		return 0
	}
	if report.Status == "FAIL" {
		return 1
	}
	return 2
}

// Link publishes the completed file only if the destination does not exist.
func saveReport(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".invaris-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(f.Name(), path)
}

// prepareReport checks that exclusive publication is possible before executing commands.
func prepareReport(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(abs); err == nil {
		return "", fmt.Errorf("report destination already exists")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(abs), ".invaris-preflight-*")
	if err != nil {
		return "", err
	}
	temp := f.Name()
	if err = f.Close(); err != nil {
		os.Remove(temp)
		return "", err
	}
	link := temp + ".link"
	if err = os.Link(temp, link); err != nil {
		os.Remove(temp)
		return "", err
	}
	if err = os.Remove(link); err != nil {
		os.Remove(temp)
		return "", err
	}
	if err = os.Remove(temp); err != nil {
		return "", err
	}
	return abs, nil
}
