package explain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

func TestEveryReasonHasGuidance(t *testing.T) {
	for _, r := range domain.Reasons() {
		g, ok := GuidanceFor(r)
		if !ok {
			t.Errorf("no guidance for reason %q", r)
			continue
		}
		if g.Meaning == "" || g.Next == "" {
			t.Errorf("reason %q has empty guidance: %+v", r, g)
		}
		if strings.ContainsAny(g.Meaning+g.Next, "\n\r") {
			t.Errorf("reason %q guidance contains a line break", r)
		}
	}
}

func TestNoStaleGuidance(t *testing.T) {
	if len(guidance) != len(domain.Reasons()) {
		t.Errorf("guidance has %d entries for %d reasons", len(guidance), len(domain.Reasons()))
	}
	for r := range guidance {
		if domain.ReasonCategory(r) == "" {
			t.Errorf("guidance for unknown reason %q", r)
		}
	}
}

func TestUnknownReason(t *testing.T) {
	if _, ok := GuidanceFor("nope"); ok {
		t.Error("GuidanceFor(\"nope\") reported ok")
	}
}

func TestGuidanceHonesty(t *testing.T) {
	for _, r := range []string{domain.ReasonMarkerMatched, domain.ReasonMarkerMissing} {
		if g, _ := GuidanceFor(r); !strings.Contains(g.Meaning, "weak") {
			t.Errorf("%s must say the evidence is weak: %q", r, g.Meaning)
		}
	}
	// Guidance may point at `invaris doctor HARNESS`, which inspects setup
	// without running anything; it must never be described as running checks.
	for r, g := range guidance {
		if strings.Contains(g.Next, "invaris doctor") && !strings.Contains(g.Next, "invaris doctor HARNESS") {
			t.Errorf("%s names invaris doctor without its HARNESS argument: %q", r, g.Next)
		}
	}
}

func TestReferencedDocsExist(t *testing.T) {
	ref := regexp.MustCompile(`docs/[A-Za-z0-9._-]+\.md`)
	for r, g := range guidance {
		for _, path := range ref.FindAllString(g.Meaning+" "+g.Next, -1) {
			if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
				t.Errorf("%s references %s: %v", r, path, err)
			}
		}
	}
}
