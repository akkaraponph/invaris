package domain

// Decide aggregates results into a gate decision.
//
// ACCEPT requires that every step passed, every required guard and proof is
// present with a satisfying status, and no signal of failure or refusal exists.
// Anything unknown, missing, duplicated or malformed refuses; it never accepts.
// With failure-first (the default) a REJECT signal wins over a REFUSE signal;
// with refusal-first the order is reversed. Callers keep the original results.
func Decide(gate Gate, steps []Check, checks []Check, proofs []Proof) Decision {
	if gate.Precedence != "" && gate.Precedence != FailureFirst && gate.Precedence != RefusalFirst {
		return RefuseDecision
	}
	if len(steps)+len(gate.RequiredGuards)+len(gate.RequiredProofs) == 0 {
		return RefuseDecision // empty requested work
	}
	var reject, refuse bool
	seen := map[string]bool{}
	unique := func(id string) bool {
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
		return true
	}
	for _, s := range steps {
		if !unique(s.ID) {
			refuse = true
		}
		switch s.Status {
		case Pass:
		case Fail:
			reject = true
		default: // REFUSE, NOT_RUN and unknown values
			refuse = true
		}
	}
	required := map[string]bool{}
	for _, id := range gate.RequiredGuards {
		required[id] = true
	}
	byID := map[string]Check{}
	for _, c := range checks {
		if !unique(c.ID) {
			refuse = true
		}
		byID[c.ID] = c
		switch c.Status {
		case Pass:
		case Fail:
			reject = true
		case NotRun:
			if required[c.ID] {
				refuse = true
			}
		default:
			refuse = true
		}
	}
	for _, id := range gate.RequiredGuards {
		if _, ok := byID[id]; !ok {
			refuse = true
		}
	}
	requiredProof := map[string]bool{}
	for _, id := range gate.RequiredProofs {
		requiredProof[id] = true
	}
	proofByID := map[string]Proof{}
	for _, p := range proofs {
		if !unique("proof:" + p.ID) {
			refuse = true
		}
		proofByID[p.ID] = p
		switch p.Status {
		case Proven:
			if p.EvidenceStrength != StrengthStrong && !gate.AllowWeakEvidence {
				reject = true
			}
		case Unproven:
			reject = true
		case ProofNotRun:
			if requiredProof[p.ID] {
				refuse = true
			}
		default: // INCONCLUSIVE and unknown values
			refuse = true
		}
	}
	for _, id := range gate.RequiredProofs {
		if _, ok := proofByID[id]; !ok {
			refuse = true
		}
	}
	first, second := reject, refuse
	firstDecision, secondDecision := Reject, RefuseDecision
	if gate.Precedence == RefusalFirst {
		first, second = refuse, reject
		firstDecision, secondDecision = RefuseDecision, Reject
	}
	switch {
	case first:
		return firstDecision
	case second:
		return secondDecision
	}
	return Accept
}
