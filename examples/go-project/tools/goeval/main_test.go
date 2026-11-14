package main

import (
	"strings"
	"testing"
)

func ev(lines ...string) []byte { return []byte(strings.Join(lines, "\n")) }

func TestEvalTests(t *testing.T) {
	pass := `{"Action":"pass","Test":"TestA"}`
	fail := `{"Action":"fail","Test":"TestA"}`
	for _, tc := range []struct {
		name   string
		out    []byte
		goExit int
		code   int
		err    bool
		want   string
	}{
		{"pass", ev(pass), 0, 0, false, `"PASS"`},
		{"assertion failure", ev(fail), 1, 1, false, `"ASSERTION_FAILED"`},
		{"build failure", ev(`{"Action":"build-fail"}`), 1, 0, true, ""},
		{"skipped", ev(`{"Action":"skip","Test":"TestA"}`), 0, 0, true, ""},
		{"never ran", ev(`{"Action":"pass","Package":"p"}`), 0, 0, true, ""},
		{"exit disagrees", ev(pass), 1, 0, true, ""},
		{"fail with exit 0", ev(fail), 0, 0, true, ""},
		{"garbage", ev(`{`), 1, 0, true, ""},
		{"subtest ignored", ev(`{"Action":"fail","Test":"TestA/sub"}`, pass), 0, 0, false, `"PASS"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code, err := evalTests(tc.out, tc.goExit, []string{"TestA"})
			if (err != nil) != tc.err || code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("got %q, %d, %v", out, code, err)
			}
		})
	}
}

func TestEvalValidity(t *testing.T) {
	bad := `{"Action":"build-fail","ImportPath":"p"}`
	for _, tc := range []struct {
		name   string
		out    []byte
		goExit int
		status string
		code   int
		err    bool
	}{
		{"valid", nil, 0, "VALID", 0, false},
		{"compile error", ev(bad), 1, "INVALID", 1, false},
		{"module error without build event", nil, 1, "", 0, true},
		{"unrunnable go", nil, -1, "", 0, true},
		{"failure event with exit 0", ev(bad), 0, "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code, err := evalValidity(tc.out, tc.goExit)
			if (err != nil) != tc.err || code != tc.code || !strings.Contains(out, tc.status) {
				t.Fatalf("got %q, %d, %v", out, code, err)
			}
		})
	}
}

func TestCheckNames(t *testing.T) {
	for _, bad := range [][]string{nil, {"Test A"}, {"TestA|X"}, {"BenchmarkA"}, {"TestA", "TestA"}, {"Test"}} {
		if checkNames(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if checkNames([]string{"TestA", "TestB_1"}) != nil {
		t.Fatal("rejected valid names")
	}
}
