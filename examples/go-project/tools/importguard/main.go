// Command importguard is a structured Invaris guard: it reports non-test Go
// files that import a forbidden package.
//
//	importguard -policy ID -forbid a,b ROOT...
//
// It prints one invaris.inspection/v1alpha1 object. Exit 0 means no findings
// and exit 1 means at least one. Files it cannot parse exit 2, so an
// unreadable tree is never reported as clean. `inspected` counts the files
// parsed; Invaris can only compare that count with the harness's minInspected.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type finding struct {
	PolicyID string `json:"policyId"`
	Path     string `json:"path"`
}

type inspection struct {
	Version   string    `json:"version"`
	PolicyID  string    `json:"policyId"`
	Inspected int       `json:"inspected"`
	Findings  []finding `json:"findings"`
}

func scan(policy string, forbid map[string]bool, roots []string) (inspection, error) {
	out := inspection{Version: "invaris.inspection/v1alpha1", PolicyID: policy, Findings: []finding{}}
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			out.Inspected++
			rel := filepath.ToSlash(path)
			for _, imp := range f.Imports {
				if p, err := strconv.Unquote(imp.Path.Value); err == nil && forbid[p] {
					out.Findings = append(out.Findings, finding{PolicyID: policy, Path: rel})
					break
				}
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func main() {
	policy := flag.String("policy", "", "policy ID reported in the evidence")
	forbid := flag.String("forbid", "", "comma-separated forbidden import paths")
	flag.Parse()
	set := map[string]bool{}
	for _, p := range strings.Split(*forbid, ",") {
		if p != "" {
			set[p] = true
		}
	}
	if *policy == "" || len(set) == 0 || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: importguard -policy ID -forbid a,b ROOT...")
		os.Exit(2)
	}
	res, err := scan(*policy, set, flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "importguard:", err)
		os.Exit(2)
	}
	enc, _ := json.Marshal(res)
	fmt.Println(string(enc))
	if len(res.Findings) > 0 {
		os.Exit(1)
	}
}
