package domain

type MutationVerdict string

const (
	MutationKilled       MutationVerdict = "KILLED"
	MutationSurvived     MutationVerdict = "SURVIVED"
	MutationInvalid      MutationVerdict = "INVALID"
	MutationInconclusive MutationVerdict = "INCONCLUSIVE"

	TestPass            = "PASS"
	TestAssertionFailed = "ASSERTION_FAILED"
	MutantValid         = "VALID"
	MutantInvalid       = "INVALID"
)

type TestCaseResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// TestExit verifies the selected cases and computes the expected evaluator exit.
func TestExit(tests []TestCaseResult, ids []string) (int, bool) {
	if len(tests) != len(ids) || len(ids) == 0 {
		return 0, false
	}
	exit := 0
	for i, test := range tests {
		if test.ID != ids[i] {
			return 0, false
		}
		switch test.Status {
		case TestPass:
		case TestAssertionFailed:
			exit = 1
		default:
			return 0, false
		}
	}
	return exit, true
}

func MutationDecision(verdict MutationVerdict) Decision {
	switch verdict {
	case MutationKilled:
		return Accept
	case MutationSurvived:
		return Reject
	default:
		return RefuseDecision
	}
}
