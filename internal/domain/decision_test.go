package domain

import "testing"

func chk(id string, s CheckStatus) Check { return Check{ID: id, Status: s} }

func proof(id string, s ProofStatus, strength string) Proof {
	return Proof{ID: id, Status: s, EvidenceStrength: strength}
}

func TestDecisionPreservesFailureAndRefusal(t *testing.T) {
	steps := []Check{chk("s", Pass)}
	checks := []Check{chk("fails", Fail), chk("refuses", Refuse)}
	gate := Gate{RequiredGuards: []string{"fails", "refuses"}}
	for _, tc := range []struct {
		precedence string
		want       Decision
	}{{"", Reject}, {FailureFirst, Reject}, {RefusalFirst, RefuseDecision}} {
		gate.Precedence = tc.precedence
		if got := Decide(gate, steps, checks, nil); got != tc.want {
			t.Errorf("precedence %q: got %s, want %s", tc.precedence, got, tc.want)
		}
		// Order of results must not change the outcome.
		reversed := []Check{checks[1], checks[0]}
		if got := Decide(gate, steps, reversed, nil); got != tc.want {
			t.Errorf("precedence %q reversed: got %s, want %s", tc.precedence, got, tc.want)
		}
	}
	if got := Decide(Gate{Precedence: "bogus", RequiredGuards: []string{"a"}}, nil, []Check{chk("a", Pass)}, nil); got != RefuseDecision {
		t.Fatalf("unknown precedence accepted: %s", got)
	}
}

func TestMissingRequiredResultRefuses(t *testing.T) {
	g := Gate{Precedence: FailureFirst, RequiredGuards: []string{"a", "b"}, RequiredProofs: []string{"p"}}
	pass := []Check{chk("a", Pass), chk("b", Pass)}
	okProof := []Proof{proof("p", Proven, StrengthStrong)}
	if got := Decide(g, nil, pass, okProof); got != Accept {
		t.Fatalf("complete passing set: %s", got)
	}
	for name, got := range map[string]Decision{
		"missing guard":          Decide(g, nil, pass[:1], okProof),
		"missing proof":          Decide(g, nil, pass, nil),
		"required NOT_RUN guard": Decide(g, nil, []Check{chk("a", Pass), chk("b", NotRun)}, okProof),
		"required NOT_RUN proof": Decide(g, nil, pass, []Proof{proof("p", ProofNotRun, StrengthStrong)}),
		"duplicate guard ID":     Decide(g, nil, append(pass, chk("a", Pass)), okProof),
		"step not run":           Decide(g, []Check{chk("s", NotRun)}, pass, okProof),
		"unknown status":         Decide(g, nil, []Check{chk("a", Pass), chk("b", "MAYBE")}, okProof),
		"empty work":             Decide(Gate{Precedence: FailureFirst}, nil, nil, nil),
	} {
		if got != RefuseDecision {
			t.Errorf("%s: got %s, want REFUSE", name, got)
		}
	}
	// An optional NOT_RUN check does not block acceptance but stays visible.
	if got := Decide(Gate{RequiredGuards: []string{"a"}}, nil, []Check{chk("a", Pass), chk("optional", NotRun)}, nil); got != Accept {
		t.Fatalf("optional NOT_RUN: %s", got)
	}
}

func TestProofStatusesAndWeakEvidence(t *testing.T) {
	g := Gate{Precedence: FailureFirst, RequiredProofs: []string{"p"}}
	for _, tc := range []struct {
		name  string
		gate  Gate
		proof Proof
		want  Decision
	}{
		{"strong proven", g, proof("p", Proven, StrengthStrong), Accept},
		{"unproven rejects", g, proof("p", Unproven, StrengthStrong), Reject},
		{"inconclusive refuses", g, proof("p", Inconclusive, StrengthStrong), RefuseDecision},
		{"weak proven rejects by default", g, proof("p", Proven, StrengthWeak), Reject},
		{"unlabelled strength is weak", g, proof("p", Proven, ""), Reject},
		{"weak proven allowed", Gate{RequiredProofs: []string{"p"}, AllowWeakEvidence: true}, proof("p", Proven, StrengthWeak), Accept},
		{"weak allowed does not rescue unproven", Gate{RequiredProofs: []string{"p"}, AllowWeakEvidence: true}, proof("p", Unproven, StrengthWeak), Reject},
	} {
		if got := Decide(tc.gate, nil, nil, []Proof{tc.proof}); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
	// Both precedences: UNPROVEN with INCONCLUSIVE.
	ps := []Proof{proof("p", Unproven, StrengthStrong), proof("q", Inconclusive, StrengthStrong)}
	two := Gate{RequiredProofs: []string{"p", "q"}}
	if got := Decide(two, nil, nil, ps); got != Reject {
		t.Fatalf("failure-first: %s", got)
	}
	two.Precedence = RefusalFirst
	if got := Decide(two, nil, nil, ps); got != RefuseDecision {
		t.Fatalf("refusal-first: %s", got)
	}
}

func TestFailingStepRejects(t *testing.T) {
	g := Gate{RequiredGuards: []string{"a"}}
	if got := Decide(g, []Check{chk("s", Fail)}, []Check{chk("a", Pass)}, nil); got != Reject {
		t.Fatalf("failed step: %s", got)
	}
}
