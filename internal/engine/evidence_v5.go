package engine

import (
	"context"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

// releaseVersion is set when release archives are linked:
//
//	-ldflags "-X github.com/akkaraponph/invaris/internal/engine.releaseVersion=v0.3.0"
//
// It records a build claim, not verified provenance. Empty means unset.
var releaseVersion string

// RequiresV5 reports whether a harness only has a ProfileV5 report: a
// v1alpha4 guard harness always produces one, even on early failure.
func RequiresV5(spec Specification) bool { return spec.Version == versionV4 }

// CampaignRequiresV5 reports whether any experiment, selected or filtered,
// references a v1alpha4 harness.
func CampaignRequiresV5(c Campaign) bool {
	for _, x := range c.Experiments {
		if RequiresV5(x.Spec) {
			return true
		}
	}
	return false
}

// RunEvidenceV5 runs the action exactly as RunEvidence does and returns the
// ProfileV5 report. Classification, the decision and secret redaction happen
// first; the portable projection is applied last and never changes them.
func RunEvidenceV5(ctx context.Context, spec Specification, root, action string) evidence.Report {
	return upgradeReport(spec, RunEvidence(ctx, spec, root, action))
}

func upgradeReport(spec Specification, r evidence.Report) evidence.Report {
	r.SchemaVersion = evidence.ProfileV5
	r.EngineVersion = engineVersion()
	byID := map[string]Challenge{}
	for _, c := range spec.Challenges {
		byID[c.ID] = c
	}
	digest := ""
	if r.ConfigDigest != nil {
		digest = *r.ConfigDigest
	}
	r.MutationMetadata = []evidence.MutationMetadata{}
	for _, p := range r.Proofs {
		if c, ok := byID[p.ID]; ok && c.Builtin != nil {
			r.MutationMetadata = append(r.MutationMetadata, evidence.NewMutationMetadata(p.ID, *c.Builtin, c.Guard, p, digest))
		}
	}
	r.CommandDigests = []evidence.CommandDigest{}
	for _, o := range r.Observations {
		r.CommandDigests = append(r.CommandDigests, evidence.ObservationDigest(o))
	}
	evidence.ProjectPortable(&r)
	if r.Action != "mutate" && r.Decision == domain.Accept {
		if err := evidence.Validate(r); err != nil {
			// Inconsistent evidence can never be accepted; the cause stays
			// typed because free text is withheld in this profile.
			d := domain.Diagnostic{Code: "EVIDENCE_INCOMPLETE", Message: evidence.Withheld}
			r.Checks = append(r.Checks, domain.Check{ID: "evidence", ProviderID: commandProvider, Status: domain.Refuse, Diagnostics: []domain.Diagnostic{d}})
			r.Diagnostics = append(r.Diagnostics, d)
			r.Decision = domain.Decide(r.Gate, r.Steps, r.Checks, r.Proofs)
		}
	}
	return r
}

// RunCampaignV5 runs the campaign exactly as RunCampaign does and returns the
// ProfileV5 report.
func RunCampaignV5(ctx context.Context, c Campaign, selected []bool, only []string, parallel int) evidence.CampaignReport {
	return upgradeCampaign(c, RunCampaign(ctx, c, selected, only, parallel))
}

func upgradeCampaign(c Campaign, r evidence.CampaignReport) evidence.CampaignReport {
	r.SchemaVersion = evidence.ProfileV5
	r.MutationMetadata = []evidence.MutationMetadata{}
	for i, x := range c.Experiments {
		if x.Kind != domain.KindGuardrail || i >= len(r.Experiments) {
			continue
		}
		var ch *Challenge
		for k := range x.Spec.Challenges {
			if x.Spec.Challenges[k].ID == x.ReferenceID {
				ch = &x.Spec.Challenges[k]
			}
		}
		if ch == nil || ch.Builtin == nil {
			continue
		}
		for _, a := range r.Experiments[i].Attempts {
			if a.Proof != nil {
				r.MutationMetadata = append(r.MutationMetadata, evidence.NewMutationMetadata(a.ID, *ch.Builtin, ch.Guard, *a.Proof, x.HarnessDigest))
			}
		}
	}
	r.Effectiveness = campaignEffectiveness(c, r)
	r.CommandDigests = evidence.CampaignCommandDigests(r)
	evidence.ProjectPortableCampaign(&r)
	return r
}

// campaignEffectiveness places every guardrail experiment, selected or
// filtered, in its harness-scoped guard row and its operator row.
func campaignEffectiveness(c Campaign, r evidence.CampaignReport) *evidence.Effectiveness {
	members := map[string]evidence.Membership{}
	for _, x := range c.Experiments {
		if x.Kind != domain.KindGuardrail {
			continue
		}
		m := evidence.Membership{Guard: evidence.GuardRowID(x.Harness, guardOf(x.Spec, x.ReferenceID)), Operator: evidence.ReplaceOperator}
		for _, ch := range x.Spec.Challenges {
			if ch.ID == x.ReferenceID && ch.Builtin != nil {
				m.Operator, m.OperatorVersion = ch.Builtin.Operator, ch.Builtin.Version
			}
		}
		members[x.ID] = m
	}
	e := evidence.BuildEffectiveness(r, members)
	return &e
}
