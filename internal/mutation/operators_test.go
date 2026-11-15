package mutation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func spec(op string, original []byte) Spec {
	s := Spec{Operator: op, Version: Version, PreimageSHA256: digest(original)}
	if op == SecretLeakInjection {
		s.Fixture = SecretFixture
	}
	return s
}

func TestPlanGoldens(t *testing.T) {
	for _, tc := range []struct {
		name, op, in, out string
	}{
		{"whitespace first line", TrailingWhitespace, "a\nb\n", "a \nb\n"},
		{"whitespace skips empty and already-trailing lines", TrailingWhitespace, "\n\nx \ny\t\nz\nw\n", "\n\nx \ny\t\nz \nw\n"},
		{"whitespace ignores unterminated last line", TrailingWhitespace, "a \nlast", ""},
		{"whitespace unicode", TrailingWhitespace, "héllo wörld\n", "héllo wörld \n"},
		{"crlf first LF only", CRLFInjection, "a\nb\nc\n", "a\r\nb\nc\n"},
		{"crlf leading empty line", CRLFInjection, "\nb", "\r\nb"},
		{"secret appended", SecretLeakInjection, "KEY=value\n", "KEY=value\nINVARIS_SYNTHETIC_SECRET=INVARIS_TEST_TOKEN_DO_NOT_USE_00000000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			kept := bytes.Clone(in)
			e, err := Plan(spec(tc.op, in), in)
			if tc.out == "" {
				if !errors.Is(err, ErrIneligible) {
					t.Fatalf("err=%v, want ineligible", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(e.Before) != tc.in || string(e.After) != tc.out {
				t.Fatalf("got %q -> %q, want %q", e.Before, e.After, tc.out)
			}
			if !bytes.Equal(in, kept) {
				t.Fatal("input modified")
			}
			again, _ := Plan(spec(tc.op, in), in)
			if !bytes.Equal(again.After, e.After) {
				t.Fatal("not deterministic")
			}
			// The mutated bytes no longer match the declared preimage.
			if _, err := Plan(spec(tc.op, in), e.After); !errors.Is(err, ErrIneligible) {
				t.Fatalf("planning on mutated data with the original digest: %v", err)
			}
		})
	}
}

func TestPlanIneligibleInputs(t *testing.T) {
	big := []byte(strings.Repeat("a", MaxBytes-1) + "\n")
	nearCap := []byte(strings.Repeat("a", MaxBytes-len(SecretLine)) + "\n")
	for _, tc := range []struct {
		name string
		ops  []string
		in   []byte
	}{
		{"empty", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, nil},
		{"NUL", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte("a\x00b\n")},
		{"invalid UTF-8", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte("a\xffb\n")},
		{"CRLF", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte("a\r\nb\r\n")},
		{"mixed endings", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte("a\r\nb\n")},
		{"lone CR", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte("a\rb\n")},
		{"oversized", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte(strings.Repeat("a", MaxBytes) + "\n")},
		{"result over limit", []string{TrailingWhitespace, CRLFInjection}, big},
		{"secret result over limit", []string{SecretLeakInjection}, nearCap},
		{"no newline", []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}, []byte("abc")},
		{"every line already trailing", []string{TrailingWhitespace}, []byte("a \nb\t\n\n")},
		{"secret without final LF", []string{SecretLeakInjection}, []byte("a\nb")},
		{"secret already present", []string{SecretLeakInjection}, []byte("x " + SecretToken + "\n")},
	} {
		for _, op := range tc.ops {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				if _, err := Plan(spec(op, tc.in), tc.in); !errors.Is(err, ErrIneligible) {
					t.Fatalf("err=%v, want ErrIneligible", err)
				}
			})
		}
	}
	in := []byte("a\n")
	s := spec(TrailingWhitespace, in)
	s.PreimageSHA256 = digest([]byte("b\n"))
	if _, err := Plan(s, in); !errors.Is(err, ErrIneligible) || !strings.Contains(err.Error(), "preimage") {
		t.Fatalf("preimage mismatch: %v", err)
	}
	// Largest eligible input: the result reaches the limit exactly.
	edge := []byte(strings.Repeat("a", MaxBytes-2) + "\n")
	if e, err := Plan(spec(CRLFInjection, edge), edge); err != nil || len(e.After) != MaxBytes {
		t.Fatalf("edge: %v", err)
	}
}

func TestValidate(t *testing.T) {
	h := strings.Repeat("a", 64)
	for _, tc := range []struct {
		s  Spec
		ok bool
	}{
		{Spec{TrailingWhitespace, "1", h, ""}, true},
		{Spec{CRLFInjection, "1", h, ""}, true},
		{Spec{SecretLeakInjection, "1", h, SecretFixture}, true},
		{Spec{"replace", "1", h, ""}, false},
		{Spec{"", "1", h, ""}, false},
		{Spec{TrailingWhitespace, "2", h, ""}, false},
		{Spec{TrailingWhitespace, "", h, ""}, false},
		{Spec{TrailingWhitespace, "1", "", ""}, false},
		{Spec{TrailingWhitespace, "1", strings.ToUpper(h), ""}, false},
		{Spec{TrailingWhitespace, "1", h[:63], ""}, false},
		{Spec{TrailingWhitespace, "1", h, SecretFixture}, false},
		{Spec{SecretLeakInjection, "1", h, ""}, false},
		{Spec{SecretLeakInjection, "1", h, "real-token"}, false},
	} {
		if err := Validate(tc.s); (err == nil) != tc.ok {
			t.Errorf("%+v: err=%v", tc.s, err)
		}
		if _, err := Plan(tc.s, []byte("x\n")); !tc.ok && (err == nil || errors.Is(err, ErrIneligible)) {
			t.Errorf("%+v: invalid declaration must not be reported as ineligibility: %v", tc.s, err)
		}
	}
}

// FuzzPlan claims only invariants: no panic, and an accepted edit is the
// exact specified delta of a bounded, distinct result.
func FuzzPlan(f *testing.F) {
	for _, s := range []string{"a\nb\n", "\n", "x \ny\n", "a\r\n", "", "héllo\n", SecretToken + "\n"} {
		f.Add([]byte(s), uint8(0))
		f.Add([]byte(s), uint8(1))
		f.Add([]byte(s), uint8(2))
	}
	ops := []string{TrailingWhitespace, CRLFInjection, SecretLeakInjection}
	f.Fuzz(func(t *testing.T, in []byte, which uint8) {
		op := ops[int(which)%len(ops)]
		kept := bytes.Clone(in)
		e, err := Plan(spec(op, in), in)
		if !bytes.Equal(in, kept) {
			t.Fatal("input modified")
		}
		if err != nil {
			if !errors.Is(err, ErrIneligible) {
				t.Fatalf("valid declaration gave non-eligibility error: %v", err)
			}
			return
		}
		if len(e.After) == 0 || bytes.Equal(e.After, e.Before) || len(e.After) > MaxBytes || !bytes.Equal(e.Before, in) || !utf8.Valid(e.After) {
			t.Fatalf("bad edit %q -> %q", e.Before, e.After)
		}
		switch op {
		case TrailingWhitespace:
			i := firstDiff(e.Before, e.After)
			if len(e.After) != len(in)+1 || e.After[i] != ' ' || e.After[i+1] != '\n' || !bytes.Equal(append(bytes.Clone(e.After[:i]), e.After[i+1:]...), in) {
				t.Fatalf("not a single space before LF: %q -> %q", in, e.After)
			}
		case CRLFInjection:
			if bytes.Count(e.After, []byte("\r")) != 1 || bytes.Count(e.After, []byte("\n")) != bytes.Count(in, []byte("\n")) || !bytes.Equal(bytes.Replace(e.After, []byte("\r"), nil, 1), in) {
				t.Fatalf("not one CR before the first LF: %q", e.After)
			}
		case SecretLeakInjection:
			if !bytes.Equal(e.After, append(bytes.Clone(in), SecretLine...)) {
				t.Fatalf("not an exact append: %q", e.After)
			}
		}
	})
}

func firstDiff(a, b []byte) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}
