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
	if evidence.Inspected < guard.MinInspected {
		result.Status, result.Detail = "REFUSE", "guard inspected fewer targets than the declared minInspected"
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
