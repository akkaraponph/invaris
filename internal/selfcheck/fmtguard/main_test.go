package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func decode(t *testing.T, s string) inspection {
	t.Helper()
	var res inspection
	if err := json.Unmarshal([]byte(s), &res); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", s, err)
	}
	return res
}

func TestCleanFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a\n\nvar X = 1\n")
	code, out, _ := invoke(t, "-policy", "gofmt-clean", root)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	res := decode(t, out)
	if res.Inspected != 1 || res.PolicyID != "gofmt-clean" || res.Version != "invaris.inspection/v1alpha1" {
		t.Fatalf("unexpected result %+v", res)
	}
	if !bytes.Contains([]byte(out), []byte(`"findings":[]`)) {
		t.Fatalf("findings must be [] not null: %s", out)
	}
}

func TestTrailingWhitespaceIsFinding(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "b.go")
	write(t, bad, "package b\n\nvar X = 1 \n")
	code, out, _ := invoke(t, "-policy", "gofmt-clean", root)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	res := decode(t, out)
	if len(res.Findings) != 1 || res.Findings[0].Path != filepath.ToSlash(bad) || res.Findings[0].PolicyID != "gofmt-clean" {
		t.Fatalf("unexpected findings %+v", res.Findings)
	}
}

func TestUnparseableFileExits2WithoutOutput(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "c.go"), "package c\n\nfunc (\n")
	code, out, errb := invoke(t, "-policy", "gofmt-clean", root)
	if code != 2 || out != "" || errb == "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"-policy", "p"}, {"root"}, {"-nope"}} {
		if code, out, _ := invoke(t, args...); code != 2 || out != "" {
			t.Fatalf("args %v: code=%d stdout=%q", args, code, out)
		}
	}
	if code, out, _ := invoke(t, "-policy", "p", filepath.Join(t.TempDir(), "missing")); code != 2 || out != "" {
		t.Fatalf("missing root: code=%d stdout=%q", code, out)
	}
}

func TestSkippedDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ok.go"), "package ok\n")
	for _, d := range []string{"testdata", "node_modules", "vendor", ".hidden"} {
		write(t, filepath.Join(root, d, "bad.go"), "package bad\n\nfunc (\n")
		write(t, filepath.Join(root, "sub", d, "ugly.go"), "package x \n")
	}
	code, out, errb := invoke(t, "-policy", "gofmt-clean", root)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	if res := decode(t, out); res.Inspected != 1 {
		t.Fatalf("inspected %d, want 1", res.Inspected)
	}
}
