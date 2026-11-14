package engine

import (
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

const mutationConfig = `{"version":"invaris.dev/v1alpha3","name":"example","trustMode":"trusted-local","networkPolicy":"unrestricted","writablePaths":["src"],"experiment":{"id":"experiment","mutation":{"id":"change","operator":"replace","path":"src","before":"old","after":"new"},"validation":{"id":"valid","program":"sh"},"test":{"id":"test","program":"sh","envAllowlist":[]},"testIds":["second","first"]}}`

func TestMutationHarnessContract(t *testing.T) {
	s, err := decodeSpecification([]byte(mutationConfig))
	if err != nil || s.ConfigDigest == "" || strings.Join(s.Experiment.TestIDs, ",") != "second,first" {
		t.Fatalf("decode = %+v, %v", s, err)
	}
	for _, pair := range [][2]string{
		{`invaris.dev/v1alpha3`, `invaris.dev/v1alpha1`},
		{`invaris.dev/v1alpha3`, `invaris.dev/v1alpha2`},
		{`"name":"example",`, ``},
		{`"trustMode":"trusted-local",`, ``},
		{`"networkPolicy":"unrestricted",`, ``},
		{`"writablePaths":["src"],`, ``},
		{`["src"]`, `[]`},
		{`["src"]`, `["other"]`},
		{`["src"]`, `["src","src"]`},
		{`"id":"experiment",`, ``},
		{`"id":"change"`, `"id":" "`},
		{`"operator":"replace",`, ``},
		{`"operator":"replace"`, `"operator":"patch"`},
		{`"path":"src"`, `"path":"../src"`},
		{`"before":"old",`, ``},
		{`"after":"new"`, `"after":"old"`},
		{`"after":"new"`, `"after":""`},
		{`"after":"new"`, `"after":null`},
		{`"after":"new"`, `"after":"new","after":"dup"`},
		{`"after":"new"`, `"after":"new","extra":true`},
		{`"validation":{"id":"valid","program":"sh"},`, ``},
		{`"test":{"id":"test","program":"sh","envAllowlist":[]},`, ``},
		{`"program":"sh"`, `"program":"sh","policyId":""`},
		{`"program":"sh"`, `"program":"sh","timeoutSeconds":301`},
		{`,"testIds":["second","first"]`, ``},
		{`["second","first"]`, `[]`},
		{`["second","first"]`, `["second","second"]`},
		{`["second","first"]`, `[""]`},
		{`,"experiment":`, `,"steps":[],"experiment":`},
	} {
		bad := strings.Replace(mutationConfig, pair[0], pair[1], 1)
		if bad == mutationConfig {
			t.Fatalf("test input lacks %q", pair[0])
		}
		if _, err := decodeSpecification([]byte(bad)); err == nil {
			t.Errorf("accepted replacement %q with %q", pair[0], pair[1])
		}
	}
	s.Experiment.Mutation.After = s.Experiment.Mutation.Before
	if s.Validate() == nil {
		t.Fatal("direct spec accepted no-op")
	}
}

const cleanTests = `{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"PASS"}]}`
const cleanValidity = `{"version":"invaris.validity-result/v1alpha1","status":"VALID"}`

func TestMutationEvaluatorEvidence(t *testing.T) {
	cases := []struct {
		output string
		exit   int
		ok     bool
	}{
		{cleanTests, 0, true},
		{strings.ReplaceAll(cleanTests, "PASS", "ASSERTION_FAILED"), 1, true},
		{strings.ReplaceAll(cleanTests, "PASS", "ASSERTION_FAILED"), 0, false},
		{cleanTests, 1, false},
		{cleanTests, 2, false},
		{`{"version":"invaris.test-result/v1alpha1","tests":[]}`, 0, false},
		{strings.ReplaceAll(cleanTests, "case", "another"), 0, false},
		{strings.ReplaceAll(cleanTests, "PASS", "SKIPPED"), 0, false},
		{strings.ReplaceAll(cleanTests, "v1alpha1", "v1alpha2"), 0, false},
		{strings.ReplaceAll(cleanTests, `"PASS"`, "null"), 0, false},
		{strings.ReplaceAll(cleanTests, `"status":"PASS"`, `"status":"PASS","status":"PASS"`), 0, false},
		{strings.ReplaceAll(cleanTests, `"status":"PASS"`, `"status":"PASS","skip":false`), 0, false},
		{cleanTests + "{}", 0, false},
		{cleanTests[:len(cleanTests)-1], 0, false},
	}
	for _, tc := range cases {
		if _, err := parseTestResult(tc.output, tc.exit, []string{"case"}); (err == nil) != tc.ok {
			t.Errorf("tests %q, exit %d: %v", tc.output, tc.exit, err)
		}
	}
	for _, repeat := range []bool{false, true} {
		second := "other"
		if repeat {
			second = "case"
		}
		input := strings.Replace(cleanTests, `}]`, `},{"id":"`+second+`","status":"PASS"}]`, 1)
		got, err := parseTestResult(input, 0, []string{"other", "case"})
		if repeat && err == nil || !repeat && (err != nil || got[0].ID != "other") {
			t.Errorf("selection normalization: %+v %v", got, err)
		}
	}
	invalid := strings.ReplaceAll(cleanValidity, `"VALID"`, `"INVALID"`)
	for _, tc := range []struct {
		output string
		exit   int
		want   string
	}{
		{cleanValidity, 0, domain.MutantValid},
		{invalid, 1, domain.MutantInvalid},
		{cleanValidity, 1, ""},
		{invalid, 0, ""},
		{invalid, 2, ""},
		{cleanValidity + "{}", 0, ""},
		{`{"status":"VALID"}`, 0, ""},
		{strings.ReplaceAll(cleanValidity, `"VALID"`, "null"), 0, ""},
	} {
		got, err := parseValidityResult(tc.output, tc.exit)
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("validity %q, exit %d: %q %v", tc.output, tc.exit, got, err)
		}
	}
}

func FuzzMutationTestResult(f *testing.F) {
	f.Add(cleanTests, 0)
	f.Add(cleanTests, 1)
	f.Add(strings.Replace(cleanTests, `}]`, `},{"id":"case","status":"PASS"}]`, 1), 0)
	f.Fuzz(func(t *testing.T, stdout string, exit int) {
		results, err := parseTestResult(stdout, exit, []string{"case"})
		if err == nil && (len(results) != 1 || results[0].ID != "case" ||
			!(exit == 0 && results[0].Status == "PASS" || exit == 1 && results[0].Status == "ASSERTION_FAILED")) {
			t.Fatalf("accepted inconsistent result: %+v, exit %d", results, exit)
		}
	})
}
