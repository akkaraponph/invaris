package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildProvider(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "invaris-command-provider")
	if out, err := exec.Command("go", "build", "-o", bin, "../invaris-command-provider").CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, out)
	}
	return bin
}

// providerProject copies examples/provider into a temporary project whose
// adapter program is the absolute path of the freshly built provider.
func providerProject(t *testing.T, program string, edit func(cfg map[string]any)) (dir, config string) {
	t.Helper()
	dir = t.TempDir()
	for _, name := range []string{"guard.sh", "source.txt", "invaris.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", "provider", name))
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, name), string(b))
	}
	var cfg map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "invaris.json"))
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["adapters"].([]any)[0].(map[string]any)["program"] = program
	if edit != nil {
		edit(cfg)
	}
	out, _ := json.Marshal(cfg)
	config = filepath.Join(dir, "invaris.json")
	mustWrite(t, config, string(out))
	return dir, config
}

type providerReport struct {
	Decision string `json:"decision"`
	Checks   []struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		ProviderID string `json:"providerId"`
		Inspected  *int   `json:"inspected"`
		ExitCode   *int   `json:"exitCode"`
		Findings   []struct {
			PolicyID string `json:"policyId"`
			Path     string `json:"path"`
		} `json:"findings"`
	} `json:"checks"`
	Observations []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Provider struct {
			Name    string  `json:"name"`
			Version *string `json:"version"`
		} `json:"provider"`
	} `json:"observations"`
}

func TestProviderCheckCLI(t *testing.T) {
	bin := buildProvider(t)
	dir, config := providerProject(t, bin, nil)

	code, stdout, stderr := invoke(t, dir, "check", config)
	var got providerReport
	if err := json.Unmarshal(stdout, &got); err != nil || code != 0 || got.Decision != "ACCEPT" {
		t.Fatalf("clean: code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	c := got.Checks[0]
	if c.ID != "fixture-guard" || c.Status != "PASS" || c.ProviderID != "invaris-command-provider" || *c.Inspected != 1 || *c.ExitCode != 0 {
		t.Fatalf("clean check: %+v", c)
	}
	discoveries := 0
	for _, o := range got.Observations {
		if o.Kind == "discover" {
			discoveries++
			if o.Provider.Name != "invaris-command-provider" || o.Provider.Version == nil {
				t.Fatalf("discovery provenance: %+v", o)
			}
		}
	}
	if discoveries != 1 {
		t.Fatalf("discoveries: %d", discoveries)
	}

	// An exact policy violation reported by the child exits 1 with the finding intact.
	mustWrite(t, filepath.Join(dir, "source.txt"), "FORBIDDEN")
	code, stdout, stderr = invoke(t, dir, "check", config)
	got = providerReport{}
	if err := json.Unmarshal(stdout, &got); err != nil || code != 1 || got.Decision != "REJECT" {
		t.Fatalf("violation: code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	c = got.Checks[0]
	if c.Status != "FAIL" || *c.ExitCode != 1 || len(c.Findings) != 1 || c.Findings[0].PolicyID != "no-forbidden-marker" || c.Findings[0].Path != "source.txt" {
		t.Fatalf("violation check: %+v", c)
	}

	// A child that cannot be evaluated is a refusal that keeps the native exit status.
	mustWrite(t, filepath.Join(dir, "source.txt"), "unexpected")
	code, stdout, _ = invoke(t, dir, "check", config)
	got = providerReport{}
	_ = json.Unmarshal(stdout, &got)
	if code != 2 || got.Decision != "REFUSE" || got.Checks[0].Status != "REFUSE" || *got.Checks[0].ExitCode != 2 || got.Checks[0].Inspected != nil {
		t.Fatalf("unevaluable child: code=%d %+v", code, got.Checks)
	}
}

func TestProviderPreflightRefusalCLI(t *testing.T) {
	step := func(cfg map[string]any) {
		cfg["steps"] = []any{map[string]any{"id": "step", "program": "touch", "args": []any{"sentinel"}}}
		cfg["gate"] = map[string]any{"requiredGuards": []any{"fixture-guard"}}
	}
	// Missing provider: exit 2 and the step never runs.
	dir, config := providerProject(t, filepath.Join(t.TempDir(), "no-such-provider"), step)
	code, stdout, stderr := invoke(t, dir, "run", config)
	var got providerReport
	_ = json.Unmarshal(stdout, &got)
	if code != 2 || got.Decision != "REFUSE" {
		t.Fatalf("missing provider: code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "sentinel")); !os.IsNotExist(err) {
		t.Fatal("a step ran although the required provider is missing")
	}
	// Control: the same project with the real provider runs the step and accepts.
	dir, config = providerProject(t, buildProvider(t), step)
	if code, stdout, stderr = invoke(t, dir, "run", config); code != 0 {
		t.Fatalf("control: code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
		t.Fatalf("control step did not run: %v", err)
	}
}
