package engine

import (
	"context"
	"encoding/json"
	"io"
	"strings"
)

type Finding struct {
	PolicyID string `json:"policyId"`
	Path     string `json:"path"`
}

type Inspection struct {
	Version   string    `json:"version"`
	PolicyID  string    `json:"policyId"`
	Inspected int       `json:"inspected"`
	Findings  []Finding `json:"findings"`
}

func checkGuard(ctx context.Context, root string, guard Command) Result {
	result := execute(ctx, root, guard)
	if guard.PolicyID == "" || result.Status == "REFUSE" {
		return result
	}
	decoder := json.NewDecoder(strings.NewReader(result.Output))
	decoder.DisallowUnknownFields()
	var evidence Inspection
	var extra any
	if decoder.Decode(&evidence) != nil || decoder.Decode(&extra) != io.EOF ||
		evidence.Version != "invaris.inspection/v1alpha1" || evidence.PolicyID != guard.PolicyID ||
		evidence.Inspected <= 0 || evidence.Findings == nil || result.ExitCode == nil {
		result.Status, result.Detail = "REFUSE", "invalid or incomplete inspection evidence"
		return result
	}
	for _, finding := range evidence.Findings {
		if finding.PolicyID != guard.PolicyID || validatePath(finding.Path) != nil {
			result.Status, result.Detail = "REFUSE", "finding has an unexpected policy or path"
			return result
		}
	}
	code := *result.ExitCode
	if (code != 0 || len(evidence.Findings) != 0) && (code != 1 || len(evidence.Findings) == 0) {
		result.Status, result.Detail = "REFUSE", "guard exit status disagrees with inspection evidence"
		return result
	}
	result.Inspection = &evidence
	return result
}

func classifyStructuredProof(result Result, policy, target string) Result {
	result.Status, result.Detail = "INCONCLUSIVE", "challenged guard did not produce usable evidence"
	challenged := result.Challenged
	if challenged.Status == "PASS" {
		result.Status, result.Detail = "UNPROVEN", "guard passed after mutation"
	}
	if challenged.Status == "FAIL" && challenged.Inspection != nil {
		findings := challenged.Inspection.Findings
		if len(findings) == 1 && findings[0].PolicyID == policy && findings[0].Path == target {
			result.Status, result.Detail = "PROVEN", "guard reported the expected policy at the mutated path"
		} else {
			result.Detail = "findings do not identify exactly one violation at the mutated path"
		}
	}
	return result
}
