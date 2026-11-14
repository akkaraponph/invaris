package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/adapter"
	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/runner"
)

const helperArg = "invaris-bridge-helper"

// The test binary doubles as the provider process so adapter.Call, the real
// client, validates every response the bridge produces.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == helperArg {
		cwd, _ := os.Getwd()
		os.Exit(run(context.Background(), os.Stdin, os.Stdout, os.Stderr, cwd))
	}
	os.Exit(m.Run())
}

func bridge() runner.Command {
	return runner.Command{Program: os.Args[0], Args: []string{helperArg}, Timeout: 20 * time.Second}
}

func opts(script string, extra ...string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"program": "sh", "args": append([]string{"-c", script}, extra...)})
	return b
}

func check(t *testing.T, root string, o json.RawMessage) (adapter.Response, error) {
	t.Helper()
	return adapter.Call(context.Background(), root, bridge(), adapter.Request{
		Operation: adapter.OpCheck, PolicyIDs: []string{"no-forbidden-marker"}, Scope: []string{"."}, Options: o})
}

const clean = `printf '{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-marker","inspected":7,"findings":[]}'`
const violated = `printf '{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-marker","inspected":7,"findings":[{"policyId":"no-forbidden-marker","path":"src/a.js"}]}'; exit 1`

func TestDiscoverAdvertisesOnlyStructuredCommands(t *testing.T) {
	resp, err := adapter.Call(context.Background(), t.TempDir(), bridge(), adapter.Request{Operation: adapter.OpDiscover, PolicyIDs: []string{"p1"}})
	c := resp.Capabilities
	if err != nil || !c.Available || len(c.Languages) != 0 || len(c.ProofTypes) != 0 || len(c.PolicyIDs) != 1 || c.PolicyIDs[0] != "p1" {
		t.Fatalf("%+v %v", resp, err)
	}
	for _, op := range c.Operations {
		if op == adapter.OpMutate {
			t.Fatal("the command provider must not advertise mutation")
		}
	}
	if resp.Provider.Name != providerName || resp.Provider.Version == "" {
		t.Fatalf("identity: %+v", resp.Provider)
	}
	if _, err = adapter.Call(context.Background(), t.TempDir(), bridge(), adapter.Request{Operation: adapter.OpMutate, PolicyIDs: []string{"p"}, Scope: []string{"."}}); !isCode(err, adapter.CodeUnsupportedCap) {
		t.Fatalf("mutate: %v", err)
	}
}

func isCode(err error, code string) bool {
	var pe *adapter.ProtocolError
	return errors.As(err, &pe) && pe.Code == code
}

func TestBridgePreservesNativeExitAndInspectedCounts(t *testing.T) {
	root := t.TempDir()
	resp, err := check(t, root, opts(clean))
	c := resp.Check
	if err != nil || c.Status != domain.Pass || *c.Inspected != 7 || *c.ExitCode != 0 || resp.Provider.ToolName == nil || *resp.Provider.ToolName != "sh" {
		t.Fatalf("clean: %+v %v", resp, err)
	}
	resp, err = check(t, root, opts(violated))
	c = resp.Check
	if err != nil || c.Status != domain.Fail || *c.Inspected != 7 || *c.ExitCode != 1 || len(c.Findings) != 1 || c.Findings[0].Path != "src/a.js" || c.Findings[0].PolicyID != "no-forbidden-marker" {
		t.Fatalf("violation: %+v %v", c, err)
	}
	// The child runs in the project root.
	if err := os.WriteFile(filepath.Join(root, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if resp, err = check(t, root, opts(`test -f marker && `+clean)); err != nil || resp.Check.Status != domain.Pass {
		t.Fatalf("cwd: %+v %v", resp, err)
	}
}

func TestBridgeNeverFabricatesEvidence(t *testing.T) {
	policy := "no-forbidden-marker"
	for name, tc := range map[string]struct {
		script string
		exit   int
		code   string
	}{
		"native failure with no findings": {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":3,"findings":[]}'; exit 2`, 2, "EXIT_EVIDENCE_MISMATCH"},
		"exit 1 with no findings":         {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":3,"findings":[]}'; exit 1`, 1, "EXIT_EVIDENCE_MISMATCH"},
		"findings but clean exit":         {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":3,"findings":[{"policyId":"` + policy + `","path":"a"}]}'`, 0, "EXIT_EVIDENCE_MISMATCH"},
		"marker only, no inspection JSON": {`echo OK; exit 0`, 0, "NO_INSPECTION_EVIDENCE"},
		"silent success":                  {`exit 0`, 0, "NO_INSPECTION_EVIDENCE"},
		"crash without output":            {`exit 127`, 127, "NO_INSPECTION_EVIDENCE"},
		"zero inspected":                  {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":0,"findings":[]}'`, 0, "INVALID_INSPECTION"},
		"null findings":                   {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":2,"findings":null}'`, 0, "INVALID_INSPECTION"},
		"wrong policy":                    {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"other","inspected":2,"findings":[]}'`, 0, "INVALID_INSPECTION"},
		"wrong version":                   {`printf '{"version":"x","policyId":"` + policy + `","inspected":2,"findings":[]}'`, 0, "INVALID_INSPECTION"},
		"finding for another policy":      {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":2,"findings":[{"policyId":"o","path":"a"}]}'; exit 1`, 1, "INVALID_INSPECTION"},
		"unknown field":                   {`printf '{"version":"invaris.inspection/v1alpha1","policyId":"` + policy + `","inspected":2,"findings":[],"extra":1}'`, 0, "NO_INSPECTION_EVIDENCE"},
		"two JSON values":                 {clean + `; ` + clean, 0, "NO_INSPECTION_EVIDENCE"},
		"output over the limit":           {`head -c 70000 /dev/zero | tr '\0' x`, 0, "OUTPUT_LIMIT"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := check(t, t.TempDir(), opts(tc.script))
			if err != nil {
				t.Fatalf("a refused check is a typed result, got error %v", err)
			}
			c := resp.Check
			if c.Status != domain.Refuse || c.Inspected != nil || len(c.Findings) != 0 || len(c.Diagnostics) == 0 || c.Diagnostics[0].Code != tc.code {
				t.Fatalf("got %+v", c)
			}
			if tc.code != "OUTPUT_LIMIT" && (c.ExitCode == nil || *c.ExitCode != tc.exit) { // a flooded pipe may end the child with SIGPIPE
				t.Fatalf("native exit status lost: %+v", c.ExitCode)
			}
		})
	}
}

func TestBridgeErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := check(t, root, json.RawMessage(`{"program":"invaris-missing-program-4242"}`)); !isCode(err, adapter.CodeToolUnavailable) {
		t.Fatalf("missing program: %v", err)
	}
	for name, o := range map[string]json.RawMessage{
		"no options":         nil,
		"unknown option":     json.RawMessage(`{"program":"sh","extra":1}`),
		"empty program":      json.RawMessage(`{"program":" "}`),
		"timeout over limit": json.RawMessage(`{"program":"sh","timeoutSeconds":301}`),
	} {
		if _, err := check(t, root, o); !isCode(err, adapter.CodeInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Exactly one policy and a scope are required; Call itself requires >= 1.
	_, err := adapter.Call(context.Background(), root, bridge(), adapter.Request{Operation: adapter.OpCheck, PolicyIDs: []string{"a", "b"}, Scope: []string{"."}, Options: opts(clean)})
	if !isCode(err, adapter.CodeInvalidRequest) {
		t.Fatalf("two policies: %v", err)
	}
	// Timeout is a typed provider error, not a check.
	start := time.Now()
	_, err = check(t, root, json.RawMessage(`{"program":"sh","args":["-c","sleep 30"],"timeoutSeconds":1}`))
	if !isCode(err, adapter.CodeExecutionTimeout) || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
}

func TestBridgeRefusesMalformedRequests(t *testing.T) {
	cwd := t.TempDir()
	for name, in := range map[string]string{"garbage": "nope", "no id": `{"version":"` + adapter.Version + `","operation":"discover"}`, "unknown field": `{"requestId":"r","extra":1}`} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), strings.NewReader(in), &out, &errOut, cwd); code == 0 || out.Len() != 0 {
			t.Errorf("%s: code=%d stdout=%q", name, code, out.String())
		}
	}
	var out bytes.Buffer
	run(context.Background(), strings.NewReader(`{"version":"x","requestId":"r","operation":"discover","policyIds":[],"scope":[]}`), &out, &bytes.Buffer{}, cwd)
	if !strings.Contains(out.String(), adapter.CodeUnsupportedProtocol) {
		t.Fatalf("version: %s", out.String())
	}
	out.Reset()
	run(context.Background(), strings.NewReader(`{"version":"`+adapter.Version+`","requestId":"r","operation":"explode","policyIds":[],"scope":[]}`), &out, &bytes.Buffer{}, cwd)
	if !strings.Contains(out.String(), adapter.CodeInvalidRequest) {
		t.Fatalf("operation: %s", out.String())
	}
}
