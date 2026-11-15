package engine

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
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
	// From here on the guard exited; a refusal is about its evidence. Exit 0
	// and 1 are completed assessments, so rejected output is malformed
	// evidence. Any other exit is the tool reporting its own failure.
	refuse := func(detail string) Result {
		result.Status, result.Detail = "REFUSE", detail
		if result.ExitCode != nil && (*result.ExitCode == 0 || *result.ExitCode == 1) {
			result.reason = domain.ReasonMalformedEvidence
		}
		return result
	}
	decoder := json.NewDecoder(strings.NewReader(result.Output))
	decoder.DisallowUnknownFields()
	var evidence Inspection
	var extra any
	if decoder.Decode(&evidence) != nil || decoder.Decode(&extra) != io.EOF ||
		evidence.Version != "invaris.inspection/v1alpha1" || evidence.PolicyID != guard.PolicyID ||
		evidence.Inspected <= 0 || evidence.Findings == nil || result.ExitCode == nil {
		return refuse("invalid or incomplete inspection evidence")
	}
	if evidence.Inspected < guard.MinInspected {
		return refuse("guard inspected fewer targets than the declared minInspected")
	}
	for _, finding := range evidence.Findings {
		if finding.PolicyID != guard.PolicyID || validatePath(finding.Path) != nil {
			return refuse("finding has an unexpected policy or path")
		}
	}
	code := *result.ExitCode
	if (code != 0 || len(evidence.Findings) != 0) && (code != 1 || len(evidence.Findings) == 0) {
		return refuse("guard exit status disagrees with inspection evidence")
	}
	result.Inspection = &evidence
	return result
}
