package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

// Output formats. JSON is the default for every action, whatever the terminal.
const (
	formatJSON    = "json"
	formatText    = "text"
	formatCompact = "compact"
)

func validFormat(f string) bool { return f == formatJSON || f == formatText || f == formatCompact }

// messageLimit bounds diagnostic excerpts in human output, in bytes.
const messageLimit = 160

// formatReport renders a validated, redacted check or challenge report. Every
// value comes from the report; nothing is recomputed or invented, and the
// caller keeps the report's decision and exit code. JSON is the caller's
// already-marshaled bytes, so it is not handled here.
func formatReport(r evidence.Report, format string) ([]byte, error) {
	if r.Action != "check" && r.Action != "challenge" {
		return nil, fmt.Errorf("%s output is not available for %s", format, r.Action)
	}
	switch format {
	case formatText:
		return []byte(reportText(r)), nil
	case formatCompact:
		return []byte(reportCompact(r)), nil
	}
	return nil, fmt.Errorf("unsupported format %q", format)
}

// formatCampaign renders a validated campaign report as text or compact lines.
func formatCampaign(r evidence.CampaignReport, format string) ([]byte, error) {
	switch format {
	case formatText:
		return []byte(campaignText(r)), nil
	case formatCompact:
		return []byte(campaignCompact(r)), nil
	}
	return nil, fmt.Errorf("unsupported format %q", format)
}

// token renders a user-controlled value as one unambiguous token: plain when
// it is a nonempty run of printable characters without spaces, quotes or "=",
// otherwise Go-quoted with escapes. It is never truncated.
func token(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !unicode.IsGraphic(r) || unicode.IsSpace(r) || r == '"' || r == '=' || r == '\\'
	}) && utf8.ValidString(s) {
		return s
	}
	return strconv.QuoteToGraphic(s)
}

// message renders free text on one line: control characters are escaped and
// the result is cut at messageLimit bytes on a rune boundary.
func message(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			b.WriteString(`�`)
		case r == '\\':
			b.WriteString(`\\`)
		case unicode.IsControl(r) || !unicode.IsGraphic(r) && !unicode.IsSpace(r):
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(q[1 : len(q)-1])
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) <= messageLimit {
		return out
	}
	cut := messageLimit
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + "... (truncated)"
}

func duration(start, finish *time.Time) string {
	if start == nil || finish == nil || start.IsZero() || finish.IsZero() {
		return "n/a"
	}
	return strconv.FormatInt(finish.Sub(*start).Milliseconds(), 10) + "ms"
}

func optInt(p *int) string {
	if p == nil {
		return "n/a"
	}
	return strconv.Itoa(*p)
}

// events indexes lifecycle entries and observations by ID.
func events(r evidence.Report) (map[string]evidence.Event, map[string]evidence.Observation) {
	ev, obs := map[string]evidence.Event{}, map[string]evidence.Observation{}
	for _, e := range r.Lifecycle {
		ev[e.ID] = e
	}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	return ev, obs
}

func reportText(r evidence.Report) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	ev, obs := events(r)
	line("Invaris %s: %s", r.Action, token(r.Harness))
	if r.Compatibility.Status == "legacy" {
		line("  input %s (legacy compatibility)", token(r.Compatibility.InputVersion))
	}
	checkLine := func(kind string, c domain.Check) {
		e := ev[c.ID]
		evidenceID, dur := "n/a", duration(e.StartedAt, e.FinishedAt)
		if o, ok := obs[c.ID]; ok {
			evidenceID = token(o.ID)
			if dur == "n/a" {
				dur = duration(o.StartedAt, o.FinishedAt)
			}
		}
		policy := "none"
		if c.PolicyID != "" {
			policy = token(c.PolicyID)
		}
		exit := optInt(c.ExitCode)
		line("  %s %s  status=%s  policy=%s  inspected=%s  findings=%d  exit=%s", kind, token(c.ID), c.Status, policy, optInt(c.Inspected), len(c.Findings), exit)
		line("    evidence=%s  duration=%s", evidenceID, dur)
		for _, f := range c.Findings {
			line("    finding policy=%s rule=%s path=%s", token(f.PolicyID), token(f.RuleID), token(f.Path))
		}
		for _, d := range c.Diagnostics {
			line("    %s: %s", token(d.Code), message(d.Message))
		}
	}
	for _, c := range r.Steps {
		checkLine("step", c)
	}
	for _, c := range r.Checks {
		checkLine("guard", c)
	}
	operators := map[string]evidence.MutationMetadata{}
	for _, m := range r.MutationMetadata {
		operators[m.EvidenceID] = m
	}
	for _, p := range r.Proofs {
		e := ev[p.ID]
		line("  %s  guard=%s  target=%s", token(p.ID), token(p.GuardID), token(p.Target))
		if m, ok := operators[p.ID]; ok {
			line("    operator=%s@%s  application=%s", token(m.Operator), token(m.OperatorVersion), m.Application)
		}
		expected := "marker (weak evidence)"
		if p.EvidenceStrength == domain.StrengthStrong {
			expected = token(p.PolicyID) + "/" + token(p.Target)
		}
		line("    expected=%s  strength=%s", expected, token(p.EvidenceStrength))
		baseline, observed := "n/a", "n/a"
		if p.Baseline != nil {
			baseline = string(p.Baseline.Status)
		}
		if p.Challenged != nil {
			observed = string(p.Challenged.Status)
			for _, f := range p.Challenged.Findings {
				line("    observed finding policy=%s rule=%s path=%s", token(f.PolicyID), token(f.RuleID), token(f.Path))
			}
		}
		reason := "n/a"
		if p.Reason != "" {
			reason = token(p.Reason)
		}
		line("    baseline=%s  observed=%s  proof=%s  reason=%s", baseline, observed, p.Status, reason)
		// The proof's own check records are its evidence: the challenged
		// check when the edit was evaluated, otherwise the baseline.
		evidenceID := "n/a"
		if p.Challenged != nil && p.Challenged.ID != "" {
			evidenceID = token(p.Challenged.ID)
		} else if p.Baseline != nil && p.Baseline.ID != "" {
			evidenceID = token(p.Baseline.ID)
		}
		line("    evidence=%s  duration=%s", evidenceID, duration(e.StartedAt, e.FinishedAt))
		if p.Status != domain.Proven && p.Detail != "" {
			line("    detail: %s", message(p.Detail))
		}
		for _, s := range p.Stages {
			if s.Status == domain.StageFailed {
				line("    stage %s FAILED: %s", token(s.Name), message(s.Detail))
			}
		}
	}
	line("Decision: %s  %s", r.Decision, summary(r))
	return b.String()
}

func reportCompact(r evidence.Report) string {
	var b strings.Builder
	for _, c := range append(append([]domain.Check{}, r.Steps...), r.Checks...) {
		fmt.Fprintf(&b, "%s %s", c.Status, token(c.ID))
		if c.PolicyID != "" {
			fmt.Fprintf(&b, " policy=%s", token(c.PolicyID))
		}
		fmt.Fprintf(&b, " findings=%d\n", len(c.Findings))
	}
	for _, p := range r.Proofs {
		reason := "n/a"
		if p.Reason != "" {
			reason = token(p.Reason)
		}
		fmt.Fprintf(&b, "%s %s guard=%s target=%s reason=%s", p.Status, token(p.ID), token(p.GuardID), token(p.Target), reason)
		if p.EvidenceStrength != domain.StrengthStrong {
			b.WriteString(" strength=weak")
		}
		b.WriteByte('\n')
	}
	if r.Action == "challenge" {
		fmt.Fprintf(&b, "%s proofs=%d\n", r.Decision, len(r.Proofs))
	} else {
		fmt.Fprintf(&b, "%s checks=%d\n", r.Decision, len(r.Steps)+len(r.Checks))
	}
	return b.String()
}

// summary counts results by status as recorded in the report.
func summary(r evidence.Report) string {
	if r.Action == "challenge" {
		n := map[domain.ProofStatus]int{}
		for _, p := range r.Proofs {
			n[p.Status]++
		}
		return fmt.Sprintf("proofs=%d proven=%d unproven=%d inconclusive=%d not_run=%d",
			len(r.Proofs), n[domain.Proven], n[domain.Unproven], n[domain.Inconclusive], n[domain.ProofNotRun])
	}
	n := map[domain.CheckStatus]int{}
	all := append(append([]domain.Check{}, r.Steps...), r.Checks...)
	for _, c := range all {
		n[c.Status]++
	}
	return fmt.Sprintf("checks=%d pass=%d fail=%d refuse=%d not_run=%d",
		len(all), n[domain.Pass], n[domain.Fail], n[domain.Refuse], n[domain.NotRun])
}

// campaignCompact prints one line per experiment and a final decision line.
func campaignCompact(r evidence.CampaignReport) string {
	var b strings.Builder
	for _, x := range r.Experiments {
		result := string(x.Stability)
		if x.AggregateVerdict != nil {
			result = *x.AggregateVerdict
		}
		if x.Selection == domain.FilteredOut {
			result = "FILTERED_OUT"
		}
		var attempts []string
		for _, a := range x.Attempts {
			v := string(a.Execution)
			if a.Verdict != nil {
				v = *a.Verdict
			}
			if a.Reason != "" {
				v += "(" + a.Reason + ")"
			}
			attempts = append(attempts, v)
		}
		fmt.Fprintf(&b, "%s %s kind=%s", result, token(x.ID), x.Kind)
		if len(attempts) > 0 {
			fmt.Fprintf(&b, " attempts=%s", strings.Join(attempts, ","))
		}
		b.WriteByte('\n')
	}
	score, rate := "n/a", "n/a"
	if s := r.MutationSummary.Score; s != nil {
		score = strconv.FormatFloat(*s*100, 'f', 1, 64) + "%"
	}
	if p := r.GuardrailSummary.ProofRate; p != nil {
		rate = strconv.FormatFloat(*p*100, 'f', 1, 64) + "%"
	}
	fmt.Fprintf(&b, "%s experiments=%d mutation_score=%s proof_rate=%s\n", r.Decision, len(r.Experiments), score, rate)
	return b.String()
}
