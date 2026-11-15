// Package mutation holds the built-in guard-challenge operators. Each is a
// pure, deterministic function from the exact bytes of one text file to the
// exact bytes of its mutated form. The package performs no I/O: the engine
// reads the target, plans the edit here, and installs it through the
// workspace's checked replacement.
//
// Operators make one small, fully specified change. They do not parse source
// code, guess comment syntax or produce real credentials; the synthetic
// secret is a fixed, documented test token.
package mutation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Operator names (version 1).
const (
	TrailingWhitespace  = "trailing-whitespace"
	CRLFInjection       = "crlf-injection"
	SecretLeakInjection = "secret-leak-injection"
)

// Version is the only operator version. A change to any operator's output
// bytes requires a new version.
const Version = "1"

// SecretFixture names the only synthetic secret, and SecretLine is the exact
// line secret-leak-injection appends. The token is not a credential for any
// service.
const (
	SecretFixture = "invaris-test-token-v1"
	SecretToken   = "INVARIS_TEST_TOKEN_DO_NOT_USE_00000000"
	SecretLine    = "INVARIS_SYNTHETIC_SECRET=" + SecretToken + "\n"
)

// MaxBytes bounds the input and the result; it equals the workspace file limit.
const MaxBytes = 10 << 20

// Spec is a declared built-in mutation.
type Spec struct {
	Operator       string
	Version        string
	PreimageSHA256 string
	Fixture        string // only for secret-leak-injection
}

// Edit is a planned whole-file replacement: Before is the full original and
// After the full mutated content. Both are nonempty, distinct and bounded.
type Edit struct{ Before, After []byte }

// ErrIneligible marks a target the operator cannot mutate as specified,
// including a preimage mismatch. It is a property of the file, not of the
// declaration.
var ErrIneligible = errors.New("ineligible mutation target")

// Validate checks the declaration alone: a known operator at version "1", a
// lowercase 64-hex preimage digest, and the fixture exactly when required.
func Validate(spec Spec) error {
	switch spec.Operator {
	case TrailingWhitespace, CRLFInjection, SecretLeakInjection:
	default:
		return fmt.Errorf("unknown mutation operator %q", spec.Operator)
	}
	if spec.Version != Version {
		return fmt.Errorf("operator %s has no version %q (want %q)", spec.Operator, spec.Version, Version)
	}
	if !validDigest(spec.PreimageSHA256) {
		return errors.New("preimageSha256 must be 64 lowercase hex characters")
	}
	if spec.Operator == SecretLeakInjection {
		if spec.Fixture != SecretFixture {
			return fmt.Errorf("secret-leak-injection requires fixture %q", SecretFixture)
		}
	} else if spec.Fixture != "" {
		return fmt.Errorf("fixture is only valid for %s", SecretLeakInjection)
	}
	return nil
}

func validDigest(d string) bool {
	return len(d) == 64 && strings.Trim(d, "0123456789abcdef") == ""
}

func ineligible(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrIneligible, fmt.Sprintf(format, args...))
}

// Plan returns the edit for original. original is not modified. Errors from
// Validate are declaration errors; every other error wraps ErrIneligible.
func Plan(spec Spec, original []byte) (Edit, error) {
	if err := Validate(spec); err != nil {
		return Edit{}, err
	}
	sum := sha256.Sum256(original)
	if hex.EncodeToString(sum[:]) != spec.PreimageSHA256 {
		return Edit{}, ineligible("content does not match the declared preimage digest")
	}
	switch {
	case len(original) == 0:
		return Edit{}, ineligible("empty file")
	case len(original) > MaxBytes:
		return Edit{}, ineligible("file exceeds %d bytes", MaxBytes)
	case !utf8.Valid(original):
		return Edit{}, ineligible("file is not valid UTF-8 text")
	case bytes.IndexByte(original, 0) >= 0:
		return Edit{}, ineligible("file contains NUL bytes")
	case bytes.IndexByte(original, '\r') >= 0:
		return Edit{}, ineligible("file contains carriage returns; only LF-only text is eligible")
	}
	var after []byte
	switch spec.Operator {
	case TrailingWhitespace:
		at := -1
		for start := 0; start < len(original); {
			end := bytes.IndexByte(original[start:], '\n')
			if end < 0 {
				break // the last line has no LF and is not eligible
			}
			end += start
			if end > start {
				if last := original[end-1]; last != ' ' && last != '\t' {
					at = end
					break
				}
			}
			start = end + 1
		}
		if at < 0 {
			return Edit{}, ineligible("no LF-terminated nonempty line without trailing space or tab")
		}
		after = make([]byte, 0, len(original)+1)
		after = append(after, original[:at]...)
		after = append(after, ' ')
		after = append(after, original[at:]...)
	case CRLFInjection:
		at := bytes.IndexByte(original, '\n')
		if at < 0 {
			return Edit{}, ineligible("file has no line feed")
		}
		after = make([]byte, 0, len(original)+1)
		after = append(after, original[:at]...)
		after = append(after, '\r')
		after = append(after, original[at:]...)
	case SecretLeakInjection:
		if original[len(original)-1] != '\n' {
			return Edit{}, ineligible("file does not end with a line feed")
		}
		if bytes.Contains(original, []byte(SecretToken)) {
			return Edit{}, ineligible("file already contains the synthetic token")
		}
		after = make([]byte, 0, len(original)+len(SecretLine))
		after = append(after, original...)
		after = append(after, SecretLine...)
	}
	if len(after) > MaxBytes {
		return Edit{}, ineligible("mutated file would exceed %d bytes", MaxBytes)
	}
	if bytes.Equal(after, original) {
		return Edit{}, ineligible("operator produced no change")
	}
	return Edit{Before: bytes.Clone(original), After: after}, nil
}
