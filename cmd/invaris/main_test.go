package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCLIPreflightPreventsExecution(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "invaris.json")
	sentinel := filepath.Join(dir, "sentinel")
	report := filepath.Join(dir, "occupied.json")
	if err := os.WriteFile(report, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	data := `{"version":"invaris.dev/v1alpha1","name":"test","steps":[{"id":"s","program":"touch","args":["` + sentinel + `"]}]}`
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", ".", "--report", report, "run", config)
	out, err := cmd.Output()
	if err == nil || len(out) != 0 {
		t.Fatalf("unexpected result %q %v", out, err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("executed despite existing report: %v", err)
	}
}
