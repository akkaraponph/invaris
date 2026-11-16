package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/akkaraponph/invaris/internal/explain"
)

const explainUsage = "invaris explain [--format text|json] REPORT"

// explainArgs parses the arguments after explain: optional flags, then exactly
// one report path. Only arguments starting with "--" are flags, so "-" is an
// ordinary path.
func explainArgs(args []string) (format, file string, err error) {
	format = formatText
	seenFormat, seenFile := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case seenFile:
			if strings.HasPrefix(arg, "--") {
				return "", "", fmt.Errorf("flags must come before the report file: %s", arg)
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
	if !seenFile {
		return "", "", errors.New("missing report file")
	}
	return format, file, nil
}

// runExplainCLI interprets a saved report offline. It runs nothing and writes
// no files.
func runExplainCLI(args []string, stdout, stderr io.Writer) int {
	format, file, err := explainArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, "usage:", explainUsage)
		return 2
	}
	data, err := readBounded(file, explain.MaxReportBytes+1)
	if err != nil {
		fmt.Fprintln(stderr, "read report:", err)
		return 2
	}
	e, err := explain.Explain(data)
	switch {
	case errors.Is(err, explain.ErrUnsupported):
		fmt.Fprintln(stderr, strings.TrimPrefix(err.Error(), explain.ErrUnsupported.Error()+": "))
		return 2
	case err != nil:
		fmt.Fprintln(stderr, "report is not valid evidence:", strings.TrimPrefix(err.Error(), explain.ErrInvalidReport.Error()+": "))
		return 2
	}
	var out []byte
	if format == formatJSON {
		if out, err = e.JSON(); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	} else {
		out = []byte(explanationText(e))
	}
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintln(stderr, "write stdout:", err)
		return 2
	}
	return 0
}

// readBounded reads at most limit bytes of the named file.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// explanationText renders an explanation for people. Every value that came
// from the report is escaped with token or message.
func explanationText(e explain.Explanation) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("Invaris explain: %s %s", e.Report.Action, token(e.Report.Subject))
	line("  report %s  run %s  decision %s", token(e.Report.SchemaVersion), token(e.Report.RunID), token(e.Report.Decision))
	line("Results")
	for _, it := range e.Items {
		line("  %s  %s  %s  -> %s", token(it.ID), it.Kind, token(it.Outcome), it.Class)
		if it.Reason != nil {
			category := ""
			if it.Category != nil {
				category = " (" + token(*it.Category) + ")"
			}
			line("    reason: %s%s", token(*it.Reason), category)
		}
		line("    meaning: %s", message(it.Meaning))
		if it.Next != nil {
			line("    next: %s", message(*it.Next))
		}
	}
	line("Limits")
	for _, l := range e.Limits {
		line("  - %s: %s", l.Code, message(l.Message))
	}
	line("Explained offline from the report; nothing was run.")
	return b.String()
}
