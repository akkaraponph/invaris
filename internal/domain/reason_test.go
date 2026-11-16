package domain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

func TestReasonsSortedUniqueAndCategorized(t *testing.T) {
	reasons := Reasons()
	if len(reasons) == 0 {
		t.Fatal("Reasons() is empty")
	}
	if !slices.IsSorted(reasons) {
		t.Errorf("Reasons() is not sorted: %v", reasons)
	}
	if len(slices.Compact(slices.Clone(reasons))) != len(reasons) {
		t.Errorf("Reasons() has duplicates: %v", reasons)
	}
	for _, r := range reasons {
		if ReasonCategory(r) == "" {
			t.Errorf("reason %q has no category", r)
		}
	}
}

// Every Reason* constant must be registered in reasonCategory, or Reasons()
// and everything derived from it would silently omit it.
func TestEveryReasonConstantIsRegistered(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "reason.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if len(name.Name) > 6 && name.Name[:6] == "Reason" && name.Name != "ReasonCategory" {
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				declared = append(declared, value)
			}
		}
		return true
	})
	slices.Sort(declared)
	if !slices.Equal(declared, Reasons()) {
		t.Errorf("Reason* constants %v differ from Reasons() %v", declared, Reasons())
	}
}
