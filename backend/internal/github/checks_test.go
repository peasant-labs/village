package github

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/promptattach"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/check_conclusions.yaml
var checkConclusionsYAML []byte

//go:embed testdata/check_actions.yaml
var checkActionsYAML []byte

//go:embed testdata/check_run_validation.yaml
var checkRunValidationYAML []byte

var requiredConclusionCaseNames = []string{
	"informational-nothing-attached-is-neutral",
	"informational-something-attached-is-success",
	"required-nothing-attached-is-failure",
	"required-something-attached-is-success",
	"unknown-mode-nothing-attached-fails-closed",
	"unknown-mode-something-attached-is-still-success",
}

var requiredActionCaseNames = []string{"attach-prompts", "detach", "refresh"}

var requiredValidationCaseNames = []string{
	"a-complete-create-request-is-accepted",
	"an-action-needs-an-identifier",
	"an-update-does-not-need-a-head-sha",
	"action-identifiers-must-be-unique",
	"action-label-must-fit-the-limit",
	"at-most-three-actions",
	"conclusion-must-be-in-the-menu",
	"create-requires-a-head-sha",
	"title-is-required",
}

type conclusionCase struct {
	Name       string `yaml:"name"`
	Mode       string `yaml:"mode"`
	Attached   bool   `yaml:"attached"`
	Conclusion string `yaml:"conclusion"`
}

type conclusionFixture struct {
	Cases []conclusionCase `yaml:"cases"`
}

type actionCase struct {
	Name       string `yaml:"name"`
	Label      string `yaml:"label"`
	Identifier string `yaml:"identifier"`
}

type actionFixture struct {
	MaxActionLabel int          `yaml:"max_action_label"`
	Actions        []actionCase `yaml:"actions"`
}

type actionFixtureEntry struct {
	Label      string `yaml:"label"`
	Identifier string `yaml:"identifier"`
}

type checkRunRequestFixture struct {
	HeadSHA    string               `yaml:"head_sha"`
	ExternalID string               `yaml:"external_id"`
	Conclusion string               `yaml:"conclusion"`
	Title      string               `yaml:"title"`
	Summary    string               `yaml:"summary"`
	DetailsURL string               `yaml:"details_url"`
	Actions    []actionFixtureEntry `yaml:"actions"`
}

type validationCase struct {
	Name        string                 `yaml:"name"`
	Creating    bool                   `yaml:"creating"`
	Request     checkRunRequestFixture `yaml:"request"`
	ExpectError bool                   `yaml:"expect_error"`
}

type validationFixture struct {
	Cases []validationCase `yaml:"cases"`
}

func decodeFixture(t *testing.T, name string, data []byte, out any) {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		t.Fatalf("decode strict %s fixture: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("%s fixture must contain exactly one YAML document: %v", name, err)
	}
}

func assertExactTemplateNames(t *testing.T, fixture string, present map[string]bool, required []string) {
	t.Helper()
	declared := map[string]bool{}
	for _, name := range required {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/%s.yaml no longer carries %v, which its manifest declares: each case pins one arm "+
			"of the check-run contract. Restore the row rather than deleting the name.", fixture, missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/%s.yaml carries %v, which its manifest does not declare: add each new name to the "+
			"manifest in the same change.", fixture, undeclared)
	}
}

func loadConclusionCases(t *testing.T) []conclusionCase {
	t.Helper()
	var fixture conclusionFixture
	decodeFixture(t, "check_conclusions", checkConclusionsYAML, &fixture)
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		seen[c.Name] = true
	}
	assertExactTemplateNames(t, "check_conclusions", seen, requiredConclusionCaseNames)
	return fixture.Cases
}

func loadActionFixture(t *testing.T) actionFixture {
	t.Helper()
	var fixture actionFixture
	decodeFixture(t, "check_actions", checkActionsYAML, &fixture)
	seen := map[string]bool{}
	for _, a := range fixture.Actions {
		seen[a.Name] = true
	}
	assertExactTemplateNames(t, "check_actions", seen, requiredActionCaseNames)
	return fixture
}

func loadValidationCases(t *testing.T) []validationCase {
	t.Helper()
	var fixture validationFixture
	decodeFixture(t, "check_run_validation", checkRunValidationYAML, &fixture)
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		seen[c.Name] = true
	}
	assertExactTemplateNames(t, "check_run_validation", seen, requiredValidationCaseNames)
	return fixture.Cases
}

func toCheckRunRequest(f checkRunRequestFixture) CheckRunRequest {
	req := CheckRunRequest{
		HeadSHA:    f.HeadSHA,
		ExternalID: f.ExternalID,
		Conclusion: f.Conclusion,
		Title:      f.Title,
		Summary:    f.Summary,
		DetailsURL: f.DetailsURL,
	}
	for _, a := range f.Actions {
		req.Actions = append(req.Actions, CheckAction{Label: a.Label, Identifier: a.Identifier})
	}
	return req
}

func TestPromptCheckConclusion(t *testing.T) {
	for _, tc := range loadConclusionCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			got := PromptCheckConclusion(promptattach.CheckMode(tc.Mode), tc.Attached)
			if got != tc.Conclusion {
				t.Errorf("PromptCheckConclusion(%q, %v) = %q, want %q", tc.Mode, tc.Attached, got, tc.Conclusion)
			}
		})
	}
}

func TestPromptCheckActions(t *testing.T) {
	fixture := loadActionFixture(t)
	got := PromptCheckActions()

	if len(got) != len(fixture.Actions) {
		t.Fatalf("action menu has %d entries, want %d", len(got), len(fixture.Actions))
	}
	for i, want := range fixture.Actions {
		if got[i].Label != want.Label {
			t.Errorf("action %d label = %q, want %q", i, got[i].Label, want.Label)
		}
		if got[i].Identifier != want.Identifier {
			t.Errorf("action %d identifier = %q, want %q", i, got[i].Identifier, want.Identifier)
		}
	}
	// GitHub rejects a run whose action label is longer than the limit, so the
	// menu must stay inside it rather than surfacing as a 422 at click time.
	for _, action := range got {
		if len(action.Label) > fixture.MaxActionLabel {
			t.Errorf("action label %q is %d characters, over GitHub's limit of %d",
				action.Label, len(action.Label), fixture.MaxActionLabel)
		}
	}
}

func TestValidateCheckRunRequest(t *testing.T) {
	for _, tc := range loadValidationCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			err := validateCheckRunRequest(toCheckRunRequest(tc.Request), tc.Creating)
			if tc.ExpectError && err == nil {
				t.Fatal("expected the request to be rejected")
			}
			if !tc.ExpectError && err != nil {
				t.Fatalf("expected the request to be accepted, got %v", err)
			}
		})
	}
}

func TestCreateCheckRun_PostsTheCompletedRun(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	run, err := c.CreateCheckRun(context.Background(), 42, "acme", "repo", CheckRunRequest{
		HeadSHA:    "abc1234",
		ExternalID: "peasant-village",
		Conclusion: CheckConclusionSuccess,
		Title:      "3 prompts attached",
		Summary:    "3 sessions, 5 commits.",
		Actions:    PromptCheckActions(),
	})
	if err != nil {
		t.Fatalf("CreateCheckRun: %v", err)
	}
	if run.ID != 77 {
		t.Errorf("check run id = %d, want the id GitHub returned", run.ID)
	}

	req := a.lastRequest()
	if req.Method != "POST" || req.Path != "/repos/acme/repo/check-runs" {
		t.Fatalf("request = %s %s, want POST /repos/acme/repo/check-runs", req.Method, req.Path)
	}
	if req.AuthHeader != "token ghs_installtoken" {
		t.Errorf("Authorization = %q, want the installation token", req.AuthHeader)
	}
	payload := decodeObject(t, req.Body)
	if payload["name"] != PromptCheckName {
		t.Errorf("name = %v, want %q", payload["name"], PromptCheckName)
	}
	if payload["head_sha"] != "abc1234" || payload["status"] != "completed" || payload["conclusion"] != CheckConclusionSuccess {
		t.Errorf("run payload = %v, want the head sha, completed status, and success conclusion", payload)
	}
	actions, ok := payload["actions"].([]any)
	if !ok || len(actions) != 3 {
		t.Fatalf("actions = %v, want the three-action menu", payload["actions"])
	}
}

func TestUpdateCheckRun_PatchesTheRecordedID(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.UpdateCheckRun(context.Background(), 42, "acme", "repo", 77, CheckRunRequest{
		Conclusion: CheckConclusionFailure,
		Title:      "No prompts attached yet",
		Actions:    PromptCheckActions(),
	}); err != nil {
		t.Fatalf("UpdateCheckRun: %v", err)
	}

	req := a.lastRequest()
	if req.Method != "PATCH" || req.Path != "/repos/acme/repo/check-runs/77" {
		t.Fatalf("request = %s %s, want PATCH /repos/acme/repo/check-runs/77", req.Method, req.Path)
	}
	payload := decodeObject(t, req.Body)
	if _, present := payload["head_sha"]; present {
		t.Error("an update must not carry a head sha: GitHub's update endpoint takes none")
	}
	if _, present := payload["name"]; present {
		t.Error("an update must not carry a name: GitHub's update endpoint takes none")
	}
	if payload["conclusion"] != CheckConclusionFailure {
		t.Errorf("conclusion = %v, want failure", payload["conclusion"])
	}
}

func TestUpdateCheckRun_RejectsNonPositiveID(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.UpdateCheckRun(context.Background(), 42, "acme", "repo", 0, CheckRunRequest{
		Conclusion: CheckConclusionSuccess,
		Title:      "t",
	}); err == nil {
		t.Fatal("expected an error for a non-positive check run id")
	}
	if len(a.requests) != 0 {
		t.Errorf("a refused update still called GitHub: %+v", a.requests)
	}
}

func TestCreateCheckRun_Non2xxSurfacesError(t *testing.T) {
	a := &appServer{failWrite: true}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	_, err := c.CreateCheckRun(context.Background(), 42, "acme", "repo", CheckRunRequest{
		HeadSHA:    "abc1234",
		Conclusion: CheckConclusionSuccess,
		Title:      "3 prompts attached",
	})
	if err == nil {
		t.Fatal("expected a non-2xx response to surface as an error")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error = %v, want it to name the status so the caller can decide whether to retry", err)
	}
	if len(a.requests) != 1 {
		t.Errorf("made %d requests, want exactly 1: the client never retries", len(a.requests))
	}
}

func decodeObject(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	return payload
}
