package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
)

type Finding struct {
	PolicyID string `json:"policyId"`
	Path     string `json:"path"`
	// RuleID, Message and Line are optional. An omitted ruleId reports the
	// policy as the rule, as before; present values are bounded.
	RuleID  string `json:"ruleId,omitempty"`
	Message string `json:"message,omitempty"`
	Line    *int   `json:"line,omitempty"`
}

// Bounds on the optional inspection finding fields.
const (
	maxFindingRuleID  = 128
	maxFindingMessage = 1024
)

// UnmarshalJSON decodes one finding strictly: unknown fields, null values,
// an empty ruleId and a line below 1 are refused.
func (f *Finding) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		switch k {
		case "policyId", "path", "ruleId", "message", "line":
		default:
			return fmt.Errorf("unknown finding field %q", k)
		}
		if string(bytes.TrimSpace(v)) == "null" {
			return fmt.Errorf("finding field %q is null", k)
		}
	}
	type plain Finding
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if _, ok := raw["ruleId"]; ok && (p.RuleID == "" || len(p.RuleID) > maxFindingRuleID) {
		return errors.New("finding ruleId must be 1 to 128 bytes")
	}
	if len(p.Message) > maxFindingMessage {
		return errors.New("finding message exceeds 1024 bytes")
	}
	if p.Line != nil && *p.Line < 1 {
		return errors.New("finding line must be at least 1")
	}
	*f = Finding(p)
	return nil
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
