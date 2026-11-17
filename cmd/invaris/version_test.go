package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		var out, errOut bytes.Buffer
		if code := runCLI(context.Background(), []string{arg}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "usage: invaris") || errOut.Len() != 0 {
			t.Fatalf("%s: code %d stdout %q stderr %q", arg, code, out.String(), errOut.String())
		}
	}
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		var out, errOut bytes.Buffer
		if code := runCLI(context.Background(), []string{arg}, &out, &errOut); code != 0 || out.String() != "invaris "+versionString()+"\n" || errOut.Len() != 0 {
			t.Fatalf("%s: code %d stdout %q stderr %q", arg, code, out.String(), errOut.String())
		}
	}
	if v := versionString(); v != "dev" && !strings.HasPrefix(v, "v") {
		t.Fatalf("version %q is neither dev nor a module version", v)
	}
}

func TestHelpWithExtraArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runCLI(context.Background(), []string{"--help", "x"}, &out, &errOut); code != 2 {
		t.Fatalf("code %d", code)
	}
}
