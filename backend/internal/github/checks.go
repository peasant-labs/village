package github

import (
	"context"
	"fmt"
	"net/http"

	"github.com/peasant-labs/village/backend/internal/promptattach"
)

const (
	// PromptCheckName is the name of the one check run Village owns on a pull
	// request. It is stable so an update finds the run it created, and a reader
	// recognises which check is Village's.
	PromptCheckName = "peasant / prompts"
	// maxCheckActionLabel is GitHub's limit for a check-run action label.
	maxCheckActionLabel = 20
	// maxCheckActions is GitHub's limit for the number of actions on a run.
	maxCheckActions = 3
)

// The fixed action menu Village offers on its check run.
const (
	CheckActionAttach  = "Attach prompts"
	CheckActionDetach  = "Detach"
	CheckActionRefresh = "Refresh"
)

// Check conclusions Village uses. A check is always posted completed, so one of
// these is always set.
const (
	CheckConclusionSuccess = "success"
	CheckConclusionFailure = "failure"
	CheckConclusionNeutral = "neutral"
)

// CheckAction is one button on the check run.
type CheckAction struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Identifier  string `json:"identifier"`
}

// PromptCheckActions is the action menu, in the order GitHub renders it. The
// labels are the exact strings the check displays; the identifiers are what a
// later click reports back.
func PromptCheckActions() []CheckAction {
	return []CheckAction{
		{Label: CheckActionAttach, Identifier: "attach", Description: "Match and attach this author's prompts to the pull request"},
		{Label: CheckActionDetach, Identifier: "detach", Description: "Detach the attached prompts from this pull request"},
		{Label: CheckActionRefresh, Identifier: "refresh", Description: "Recompute the attached prompts and the digest"},
	}
}

// PromptCheckConclusion chooses the check's conclusion from the collective's
// prompts_check_mode and whether anything is attached.
//
// informational: neutral when nothing is attached, success when something is.
// required: failure when nothing is attached, success when something is.
//
// It fails closed: a mode outside the menu is treated as required, so an
// unrecognized value can never weaken the check into a neutral pass.
func PromptCheckConclusion(mode promptattach.CheckMode, attached bool) string {
	if attached {
		return CheckConclusionSuccess
	}
	if mode == promptattach.Informational {
		return CheckConclusionNeutral
	}
	return CheckConclusionFailure
}

// CheckRunRequest is the content of a check run, apart from the pull request it
// belongs to. HeadSHA is required when creating and ignored when updating;
// Conclusion and Title are required either way.
type CheckRunRequest struct {
	HeadSHA    string
	ExternalID string
	Conclusion string
	Title      string
	Summary    string
	DetailsURL string
	Actions    []CheckAction
}

// CheckRun is the created or updated check run, as far as callers need it.
type CheckRun struct {
	ID         int64
	HTMLURL    string
	Status     string
	Conclusion string
}

// checkRunResponse mirrors GitHub's check-run object.
type checkRunResponse struct {
	ID         int64  `json:"id"`
	HTMLURL    string `json:"html_url"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func (r checkRunResponse) checkRun() *CheckRun {
	return &CheckRun{ID: r.ID, HTMLURL: r.HTMLURL, Status: r.Status, Conclusion: r.Conclusion}
}

// CreateCheckRun posts the pull request's check run for one head SHA. It is
// called when the pull request opens, reopens, or is pushed to.
func (c *Client) CreateCheckRun(ctx context.Context, installationID int64, owner, name string, req CheckRunRequest) (*CheckRun, error) {
	if err := validateCheckRunRequest(req, true); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"name":       PromptCheckName,
		"head_sha":   req.HeadSHA,
		"status":     "completed",
		"conclusion": req.Conclusion,
		"output":     map[string]string{"title": req.Title, "summary": req.Summary},
	}
	if req.ExternalID != "" {
		payload["external_id"] = req.ExternalID
	}
	if req.DetailsURL != "" {
		payload["details_url"] = req.DetailsURL
	}
	if len(req.Actions) > 0 {
		payload["actions"] = req.Actions
	}

	var out checkRunResponse
	path := fmt.Sprintf("/repos/%s/%s/check-runs", owner, name)
	if err := c.doInstallationJSON(ctx, installationID, "create check run", http.MethodPost, path, payload, &out); err != nil {
		return nil, err
	}
	return out.checkRun(), nil
}

// UpdateCheckRun updates the recorded check run in place. GitHub's update
// endpoint takes no name or head SHA, which is why the caller must have stored
// the id the create returned.
func (c *Client) UpdateCheckRun(ctx context.Context, installationID int64, owner, name string, checkRunID int64, req CheckRunRequest) (*CheckRun, error) {
	if checkRunID <= 0 {
		return nil, fmt.Errorf("github: update check run: check run id must be positive")
	}
	if err := validateCheckRunRequest(req, false); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"status":     "completed",
		"conclusion": req.Conclusion,
		"output":     map[string]string{"title": req.Title, "summary": req.Summary},
	}
	if req.DetailsURL != "" {
		payload["details_url"] = req.DetailsURL
	}
	if len(req.Actions) > 0 {
		payload["actions"] = req.Actions
	}

	var out checkRunResponse
	path := fmt.Sprintf("/repos/%s/%s/check-runs/%d", owner, name, checkRunID)
	if err := c.doInstallationJSON(ctx, installationID, "update check run", http.MethodPatch, path, payload, &out); err != nil {
		return nil, err
	}
	return out.checkRun(), nil
}

// validateCheckRunRequest fails closed on a request GitHub would reject, so a
// defect surfaces as an error here rather than as a 422 the caller retries
// forever.
func validateCheckRunRequest(req CheckRunRequest, creating bool) error {
	if creating && req.HeadSHA == "" {
		return fmt.Errorf("github: check run: head sha is required when creating")
	}
	switch req.Conclusion {
	case CheckConclusionSuccess, CheckConclusionFailure, CheckConclusionNeutral:
	default:
		return fmt.Errorf("github: check run: conclusion %q is not one of %s, %s, %s",
			req.Conclusion, CheckConclusionSuccess, CheckConclusionFailure, CheckConclusionNeutral)
	}
	if req.Title == "" {
		return fmt.Errorf("github: check run: title is required")
	}
	if len(req.Actions) > maxCheckActions {
		return fmt.Errorf("github: check run: at most %d actions, got %d", maxCheckActions, len(req.Actions))
	}
	seen := map[string]bool{}
	for _, action := range req.Actions {
		if action.Label == "" || len(action.Label) > maxCheckActionLabel {
			return fmt.Errorf("github: check run: action label %q must be 1-%d characters", action.Label, maxCheckActionLabel)
		}
		if action.Identifier == "" {
			return fmt.Errorf("github: check run: action %q has no identifier", action.Label)
		}
		if seen[action.Identifier] {
			return fmt.Errorf("github: check run: action identifier %q is repeated", action.Identifier)
		}
		seen[action.Identifier] = true
	}
	return nil
}
