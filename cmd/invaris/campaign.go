package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/engine"
	"github.com/akkaraponph/invaris/internal/evidence"
)

const campaignUsage = "invaris [--report new-file.json] campaign [--only ID]... [--format json|text] [campaign.json]"

// runCampaignCLI loads, runs and reports a campaign. Like the other actions it
// writes nothing to stdout until the validated report has been published to
// the --report file (when one is requested), so an error never leaves a
// verdict behind. The report file is always JSON; --format changes stdout only.
func runCampaignCLI(ctx context.Context, reportPath string, args []string, stdout, stderr io.Writer) int {
	usage := func(problem string) int {
		fmt.Fprintln(stderr, problem)
		fmt.Fprintln(stderr, "usage:", campaignUsage)
		return 2
	}
	var only []string
	format, file := "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--only" || arg == "--format":
			if i+1 >= len(args) {
				return usage(arg + " requires a value")
			}
			i++
			if arg == "--only" {
				only = append(only, args[i])
			} else if format != "" {
				return usage("--format is repeated")
			} else {
				format = args[i]
			}
		case strings.HasPrefix(arg, "-"):
			return usage("unknown flag: " + arg)
		case file != "":
			return usage("more than one campaign file")
		default:
			file = arg
		}
	}
	switch format {
	case "":
		format = "json"
	case "json", "text":
	default:
		return usage("unsupported format: " + format)
	}
	if file == "" {
		file = "campaign.json"
	}
	c, err := engine.LoadCampaign(file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	selected, err := c.Select(only)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if reportPath != "" {
		if reportPath, err = prepareReport(reportPath); err != nil {
			fmt.Fprintln(stderr, "prepare report:", err)
			return 2
		}
	}
	report := engine.RunCampaign(ctx, c, selected, only)
	data, err := evidence.MarshalCampaign(report)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if reportPath != "" {
		if err := saveReport(reportPath, data); err != nil {
			fmt.Fprintln(stderr, "save report:", err)
			return 2
		}
	}
	if format == "text" {
		data = []byte(campaignText(report))
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "write stdout:", err)
		return 2
	}
	switch report.Decision {
	case domain.Accept:
		return 0
	case domain.Reject:
		return 1
	}
	return 2
}

// campaignText renders a short summary from a validated report. Every number
// and word comes from the report; nothing is recomputed here.
func campaignText(r evidence.CampaignReport) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("Invaris campaign: %s", r.Campaign)
	line("")
	line("Experiments: %d", len(r.Experiments))
	line("Selected:    %d", r.Selection.Selected)
	line("Filtered:    %d", r.Selection.Filtered)
	kinds := map[domain.ExperimentKind]bool{}
	for _, x := range r.Experiments {
		kinds[x.Kind] = true
	}
	if kinds[domain.KindMutation] {
		m := r.MutationSummary
		line("")
		line("Behavioral mutations")
		line("  Killed:        %d", m.Killed)
		line("  Survived:      %d", m.Survived)
		line("  Invalid:       %d", m.Invalid)
		line("  Inconclusive:  %d", m.Inconclusive)
		line("  Mixed:         %d", m.Mixed)
		line("  Not executed:  %d", m.NotExecuted)
		if m.Score != nil {
			line("  Score:         %.1f%% (%d of %d eligible; %d excluded)", *m.Score*100, m.Killed, m.Eligible, m.Excluded)
		} else {
			line("  Score:         n/a (no eligible mutants; %d excluded)", m.Excluded)
		}
	}
	if kinds[domain.KindGuardrail] {
		g := r.GuardrailSummary
		line("")
		line("Guardrail challenges")
		line("  Proven:        %d", g.Proven)
		line("  Unproven:      %d", g.Unproven)
		line("  Inconclusive:  %d", g.Inconclusive)
		line("  Mixed:         %d", g.Mixed)
		line("  Not executed:  %d", g.NotExecuted)
	}
	line("")
	line("Results")
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
				v += " (" + a.Reason + ")"
			}
			attempts = append(attempts, v)
		}
		line("  %-24s %-9s %-13s %s", x.ID, x.Kind, result, strings.Join(attempts, ", "))
	}
	line("")
	line("Acceptance: %s", r.Decision)
	var reasons []string
	if !r.AcceptanceEvaluation.Complete {
		reasons = append(reasons, "campaign did not run to completion")
	}
	for _, req := range r.AcceptanceEvaluation.Requirements {
		if req.Status == domain.RequirementSatisfied {
			continue
		}
		reason := req.Requirement + ": " + req.Status
		if len(req.Experiments) > 0 {
			reason += " (" + strings.Join(req.Experiments, ", ") + ")"
		}
		reasons = append(reasons, reason)
	}
	if len(reasons) > 0 {
		line("")
		line("Reasons:")
		for _, reason := range reasons {
			line("  - %s", reason)
		}
	}
	if r.Selection.Mode == "filtered" {
		line("")
		line("Scope: filtered (--only %s)", strings.Join(r.Selection.Only, ", "))
	}
	return b.String()
}
