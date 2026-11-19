package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTypeScriptValidity(t *testing.T) {
	dir := t.TempDir()
	project, source := filepath.Join(dir, "tsconfig.json"), filepath.Join(dir, "entry.ts")
	for _, name := range []string{project, source} {
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, body, status string
		code               int
	}{
		{"clean", "exit 0", "VALID", 0},
		{"source type error", fmt.Sprintf("printf '%%s\n' '%s(1,1): error TS2322: incompatible type'; exit 2", source), "INVALID", 1},
		{"configuration failure", "echo 'error TS6053: File not found.'; exit 1", "", 2},
		{"ambiguous missing dependency", fmt.Sprintf("printf '%%s\n' '%s(1,1): error TS2307: missing module'; exit 1", source), "", 2},
		{"unrecognized failure", "exit 1", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := filepath.Join(dir, "tsc")
			if err := os.WriteFile(program, []byte("#!/bin/sh\n"+tc.body+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := runCLI(context.Background(), []string{"validity-result", "tsc", program, project}, &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, stderr.String())
			}
			if tc.status == "" {
				if stdout.Len() != 0 || stderr.Len() == 0 {
					t.Fatalf("setup failure must supply only diagnostics: %q %q", stdout.String(), stderr.String())
				}
				return
			}
			var result struct{ Version, Status string }
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Version != "invaris.validity-result/v1alpha1" || result.Status != tc.status {
				t.Fatalf("validity evidence: %s (%v)", stdout.String(), err)
			}
		})
	}
}
