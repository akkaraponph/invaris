package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/akkaraponph/invaris/internal/configfile"
	"github.com/akkaraponph/invaris/internal/engine"
)

const migrateUsage = "invaris migrate [--dry-run] [--from v1alpha1] [--to v1alpha2] [harness.json]"

// runMigrateCLI previews or performs a semantics-preserving harness upgrade.
// It runs no harness command. A real migration keeps an exact backup.
func runMigrateCLI(args []string, stdout, stderr io.Writer) int {
	usage := func(problem string) int {
		fmt.Fprintln(stderr, problem)
		fmt.Fprintln(stderr, "usage:", migrateUsage)
		return 2
	}
	f, err := parseFlags(args, []string{"--from", "--to"}, []string{"--dry-run"})
	if err != nil {
		return usage(err.Error())
	}
	if len(f.args) > 1 {
		return usage("more than one harness file")
	}
	file := configfile.DefaultPath("invaris.json")
	if len(f.args) == 1 {
		file = f.args[0]
	}
	path, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		return 2
	}
	info, err := os.Lstat(path)
	if err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		return 2
	}
	if !info.Mode().IsRegular() {
		fmt.Fprintf(stderr, "migrate: %s is not a regular file (symbolic links and special files are refused)\n", path)
		return 2
	}
	raw, err := readHarness(path)
	if err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		return 2
	}
	result, err := engine.MigrateConfig(raw, f.values["--from"], f.values["--to"])
	if err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		return 2
	}
	backup := path + ".bak"
	if f.bools["--dry-run"] {
		if result.Changed {
			fmt.Fprintf(stderr, "dry run: %s would be migrated from %s to %s; the original would be kept at %s (nothing was written)\n",
				path, result.SourceVersion, result.TargetVersion, backup)
		} else {
			fmt.Fprintf(stderr, "dry run: %s is already %s; nothing would change\n", path, result.TargetVersion)
		}
		if _, err := stdout.Write(result.Bytes); err != nil {
			fmt.Fprintln(stderr, "write stdout:", err)
			return 2
		}
		return 0
	}
	if !result.Changed {
		fmt.Fprintf(stdout, "%s is already %s; nothing changed\n", path, result.TargetVersion)
		return 0
	}
	if err := configfile.ReplaceWithBackup(path, raw, result.Bytes); err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		if errors.Is(err, configfile.ErrReplaced) {
			fmt.Fprintf(stderr, "migrate: check %s with `invaris check`, compare it with %s, and restore the backup by hand if needed\n", path, backup)
		}
		return 2
	}
	fmt.Fprintf(stdout, "migrated %s from %s to %s; original kept at %s\n", path, result.SourceVersion, result.TargetVersion, backup)
	fmt.Fprintln(stderr, "commands, policies, writable targets and gates are unchanged; reports now say compatibility \"native\" and the config digest changes")
	return 0
}

// readHarness reads at most 1 MiB, the harness size limit.
func readHarness(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1<<20 {
		return nil, errors.New("harness exceeds 1 MiB")
	}
	return raw, nil
}
