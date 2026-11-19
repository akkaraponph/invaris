package engine

import (
	"errors"
	"fmt"

	"github.com/akkaraponph/invaris/internal/domain"
)

type TestCaseResult = domain.TestCaseResult

func parseTestResult(stdout string, exitCode int, expectedIDs []string) ([]TestCaseResult, error) {
	var response struct {
		Version string           `json:"version"`
		Tests   []TestCaseResult `json:"tests"`
	}
	if err := decodeStrict([]byte(stdout), &response); err != nil {
		return nil, err
	}
	if response.Version != "invaris.test-result/v1alpha1" {
		return nil, errors.New("invalid test evidence version")
	}
	if len(response.Tests) != len(expectedIDs) {
		return nil, fmt.Errorf("test evidence has %d tests but the harness selects %d testIds; print only the selected tests (invaris test-result --test-id ID ...)", len(response.Tests), len(expectedIDs))
	}
	byID := make(map[string]TestCaseResult, len(response.Tests))
	for _, result := range response.Tests {
		if _, exists := byID[result.ID]; exists {
			return nil, errors.New("duplicate test result ID")
		}
		byID[result.ID] = result
	}
	ordered := make([]TestCaseResult, 0, len(expectedIDs))
	for _, id := range expectedIDs {
		result, exists := byID[id]
		if !exists {
			return nil, errors.New("missing selected test result")
		}
		ordered = append(ordered, result)
	}
	if exit, ok := domain.TestExit(ordered, expectedIDs); !ok || exit != exitCode {
		return nil, errors.New("test evidence disagrees with exit status or has an unknown case status")
	}
	return ordered, nil
}

func parseValidityResult(stdout string, exitCode int) (string, error) {
	var response struct {
		Version string `json:"version"`
		Status  string `json:"status"`
	}
	if err := decodeStrict([]byte(stdout), &response); err != nil {
		return "", err
	}
	if response.Version != "invaris.validity-result/v1alpha1" ||
		!(response.Status == domain.MutantValid && exitCode == 0 || response.Status == domain.MutantInvalid && exitCode == 1) {
		return "", errors.New("invalid validity evidence or exit mismatch")
	}
	return response.Status, nil
}
