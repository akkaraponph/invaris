package main

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ok.go", "package p\nimport \"strconv\"\nvar _ = strconv.Itoa\n")
	write(t, dir, "x_test.go", "package p\nimport _ \"net/http\"\n")
	forbid := map[string]bool{"net/http": true}
	got, err := scan("pol", forbid, []string{dir})
	if err != nil || got.Inspected != 1 || len(got.Findings) != 0 {
		t.Fatalf("clean scan = %+v, %v", got, err)
	}
	write(t, dir, "bad.go", "package p\nimport (\n\t\"strconv\"\n\t_ \"net/http\"\n)\n")
	got, err = scan("pol", forbid, []string{dir})
	want := filepath.ToSlash(filepath.Join(dir, "bad.go"))
	if err != nil || got.Inspected != 2 || len(got.Findings) != 1 || got.Findings[0].Path != want || got.Findings[0].PolicyID != "pol" {
		t.Fatalf("violation scan = %+v, %v", got, err)
	}
	write(t, dir, "broken.go", "package p\nimport (")
	if _, err := scan("pol", forbid, []string{dir}); err == nil {
		t.Fatal("unparsable file reported as a result")
	}
	if _, err := scan("pol", forbid, []string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing root reported as a result")
	}
}
