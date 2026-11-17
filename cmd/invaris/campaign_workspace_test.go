package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceCampaign builds a campaign over the shipped mutation fixture, in
// the root and in app/, with the given workspace blocks ("" for none).
func workspaceCampaign(t *testing.T, rootBlock, appBlock string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "..", "examples", "mutation")
	harness, err := os.ReadFile(filepath.Join(src, "killed.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{".", "app"} {
		d := filepath.Join(dir, sub)
		if err := os.MkdirAll(filepath.Join(d, "node_modules"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"behavior.sh", "test.sh", "validate.sh"} {
			b, err := os.ReadFile(filepath.Join(src, f))
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(d, f), string(b))
		}
		h := string(harness)
		if block := map[string]string{".": rootBlock, "app": appBlock}[sub]; block != "" {
			h = strings.Replace(h, `"writablePaths"`, `"workspace": `+block+`, "writablePaths"`, 1)
		}
		mustWrite(t, filepath.Join(d, "killed.json"), h)
	}
	mustWrite(t, filepath.Join(dir, "campaign.json"), `{"version":"invaris.campaign/v1alpha1","name":"w","trustMode":"trusted-local","networkPolicy":"unrestricted",
"experiments":[{"id":"root","kind":"mutation","harness":"killed.json","experimentId":"mutation-killed","repeat":1},
{"id":"app","kind":"mutation","harness":"app/killed.json","experimentId":"mutation-killed","repeat":1}],
"acceptance":{"requireNoSurvivors":true,"requireAllGuardrailsProven":false,"requireNoInconclusive":true,"requireNoMixed":true}}`)
	return filepath.Join(dir, "campaign.json")
}

func campaignIdentity(t *testing.T, campaign string, report string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	args := []string{"campaign", campaign}
	if report != "" {
		args = append([]string{"--report", report}, args...)
	}
	if code := runCLI(t.Context(), args, &out, &errb); code != 0 {
		t.Fatalf("campaign exit %d: %s", code, errb.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc["sourceIdentity"].(map[string]any)
}

func TestCampaignRecordsWorkspaceExclusions(t *testing.T) {
	c := workspaceCampaign(t, `{"exclude":[".next"],"maxEntries":5000}`, `{"dependencies":["node_modules"]}`)
	report := filepath.Join(t.TempDir(), "r.json")
	id := campaignIdentity(t, c, report)
	got, _ := json.Marshal(id["workspaceExclude"])
	if string(got) != `[".next","app/node_modules"]` || id["workspaceMaxEntries"] != float64(5000) {
		t.Fatalf("identity %v", id)
	}
	var out, errb bytes.Buffer
	if code := runCLI(t.Context(), []string{"explain", report}, &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "left out .next, app/node_modules") {
		t.Fatalf("explain %d: %s %s", code, out.String(), errb.String())
	}
}

func TestCampaignWithoutWorkspaceUnchanged(t *testing.T) {
	id := campaignIdentity(t, workspaceCampaign(t, "", ""), "")
	if _, ok := id["workspaceExclude"]; ok {
		t.Fatalf("identity %v", id)
	}
	if _, ok := id["workspaceMaxEntries"]; ok {
		t.Fatalf("identity %v", id)
	}
}
