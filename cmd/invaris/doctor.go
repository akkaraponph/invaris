package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/akkaraponph/invaris/internal/doctor"
)

const doctorUsage = "invaris doctor [--format text|json] [HARNESS]"

// doctorFooter is the last line of text output.
const doctorFooter = "doctor executed no harness commands; results describe setup only."

// doctorArgs parses the arguments after doctor: optional flags, then at most
// one harness path (default invaris.json).
func doctorArgs(args []string) (format, file string, err error) {
	format, file = formatText, "invaris.json"
	seenFormat, seenFile := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case seenFile:
			if strings.HasPrefix(arg, "--") {
				return "", "", fmt.Errorf("flags must come before the harness file: %s", arg)
			}
			return "", "", fmt.Errorf("unexpected argument: %s", arg)
		case arg == "--format":
			if seenFormat {
				return "", "", errors.New("--format is repeated")
			}
			seenFormat = true
			if i+1 >= len(args) || args[i+1] == "" {
				return "", "", errors.New("--format requires a value")
			}
			i++
			if format = args[i]; format != formatText && format != formatJSON {
				return "", "", fmt.Errorf("unsupported format: %s", format)
			}
		case strings.HasPrefix(arg, "--"):
			return "", "", fmt.Errorf("unknown flag: %s", arg)
		default:
			file, seenFile = arg, true
		}
	}
	return format, file, nil
}

// runDoctorCLI inspects a harness's setup. It executes nothing and writes no
// files. Exit 0 means no check failed, 1 a check failed, 2 a usage problem or
// an unreadable harness file.
func runDoctorCLI(args []string, stdout, stderr io.Writer) int {
	format, file, err := doctorArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, "usage:", doctorUsage)
		return 2
	}
	if info, err := os.Stat(file); err != nil {
		fmt.Fprintln(stderr, "read harness:", err)
		return 2
	} else if info.IsDir() {
		fmt.Fprintln(stderr, "read harness:", file, "is a directory")
		return 2
	}
	d := doctor.Inspect(file)
	var out []byte
	if format == formatJSON {
		if out, err = d.JSON(); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	} else {
		out = []byte(doctorText(d))
	}
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintln(stderr, "write stdout:", err)
		return 2
	}
	if d.Failed() {
		return 1
	}
	return 0
}

// doctorText renders a diagnosis for people. Ids and messages are escaped.
func doctorText(d doctor.Diagnosis) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	version := "not loaded"
	if d.HarnessVersion != nil {
		version = token(*d.HarnessVersion)
	}
	line("Invaris doctor: %s  (%s)", token(d.Harness), version)
	labels := map[string]string{doctor.Pass: "pass", doctor.Fail: "FAIL", doctor.Warn: "warn", doctor.Skipped: "skip"}
	for _, c := range d.Checks {
		line("  [%s] %s  %s", labels[c.Status], token(c.ID), message(c.Message))
	}
	line("Summary: %d passed, %d failed, %d warnings, %d skipped", d.Summary.Pass, d.Summary.Fail, d.Summary.Warn, d.Summary.Skipped)
	line("%s", doctorFooter)
	return b.String()
}
