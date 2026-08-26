//go:build integration

package handler

// Who may learn that a transcript is in a collective, and what the four
// contribution counters say - driven through the REAL share and collectives
// handlers against a REAL PostgreSQL.
//
// It has to be a real database and it has to go through the HTTP handlers. The
// current-state share row the counters read is written by a trigger, so a
// mock-backed test computing its own expected values would pass with no
// derivation installed at all; and the visibility and opt-in predicates live in
// SQL, so asserting them against a Go re-implementation would prove only that
// the re-implementation agrees with itself.
//
// The shipped SearchCollectives is used as an ORACLE on the same corpus: the
// new surfaces carry a copy of its visibility predicate, and copies drift. A
// case where the oracle and a new surface disagree about a collective is a
// finding, except where the contributor opt-in - which SearchCollectives does
// not have - is the declared reason for the difference.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/collective-visibility.yaml
var collectiveVisibilityYAML []byte

//go:embed testdata/contributor-optin.yaml
var contributorOptInYAML []byte

type cvCollective struct {
	DataAccess     string `yaml:"data_access"`
	AcceptanceMode string `yaml:"acceptance_mode"`
}

type cvTranscript struct {
	Steps []string `yaml:"steps"`
}

type cvExpect struct {
	TranscriptCollectives        string `yaml:"transcript_collectives"`
	ProjectRollup                string `yaml:"project_rollup"`
	ProjectRollupTranscriptCount int    `yaml:"project_rollup_transcript_count"`
	SearchCollectives            string `yaml:"search_collectives"`
	ContributionsListed          bool   `yaml:"contributions_listed"`
	ApprovedCount                int32  `yaml:"approved_count"`
	PendingCount                 int32  `yaml:"pending_count"`
	RejectedAttemptCount         int32  `yaml:"rejected_attempt_count"`
	WithdrawnAttemptCount        int32  `yaml:"withdrawn_attempt_count"`
}

type cvCase struct {
	Name                 string         `yaml:"name"`
	Why                  string         `yaml:"why"`
	Collective           cvCollective   `yaml:"collective"`
	OwnerIsDiscoverable  bool           `yaml:"owner_is_discoverable"`
	TranscriptVisibility string         `yaml:"transcript_visibility"`
	Transcripts          []cvTranscript `yaml:"transcripts"`
	Viewer               string         `yaml:"viewer"`
	Expect               cvExpect       `yaml:"expect"`
}

// requiredCollectiveVisibilityCases names every case that must exist. Each is
// here because losing it hides a specific failure: the viewer predicate, the
// member branch, the opt-in gate, the status-versus-join confusion, and the one
// place where counting transcripts and counting events genuinely diverge.
var requiredCollectiveVisibilityCases = []string{
	"public_collective_listed_for_anonymous_viewer",
	"public_collective_listed_for_other_signed_in_viewer",
	"public_collective_listed_for_contributing_owner",
	"open_collective_listed_for_anonymous_viewer",
	"open_collective_listed_for_other_signed_in_viewer",
	"members_only_collective_hidden_from_anonymous_viewer",
	"members_only_collective_hidden_from_non_member_viewer",
	"members_only_collective_listed_for_member_viewer",
	"members_only_collective_listed_for_contributing_owner",
	"collective_with_only_pending_shares",
	"collective_with_rejected_share",
	"collective_with_repeatedly_rejected_share",
	"collective_with_only_retracted_shares",
	"collective_with_only_revoked_shares",
	"collective_with_retracted_and_revoked_shares",
	"approved_count_counts_transcripts_not_attempts",
	"collective_with_approved_and_pending_transcripts",
	"shared_transcript_hidden_from_anonymous_viewer",
	"opted_out_owner_hidden_from_other_viewer_though_collective_is_public",
	"opted_out_owner_still_sees_their_own_memberships",
}

func loadCollectiveVisibilityCases(t *testing.T) []cvCase {
	t.Helper()
	cases, err := decodeFixtureRows[cvCase](collectiveVisibilityYAML)
	if err != nil {
		t.Fatalf("load the collective-visibility corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatalf("a collective-visibility case has no name; every case is addressed by name")
		}
		if present[c.Name] {
			t.Fatalf("the collective-visibility corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if len(c.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the surfaces nothing", c.Name)
		}
		if c.TranscriptVisibility != "public" && c.TranscriptVisibility != "shared" {
			t.Fatalf("case %q uses transcript visibility %q; the corpus drives public and shared", c.Name, c.TranscriptVisibility)
		}
	}
	for _, required := range requiredCollectiveVisibilityCases {
		if !present[required] {
			t.Fatalf("the collective-visibility corpus no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

// TestCollectiveVisibilityMatrix drives every corpus case end to end.
func TestCollectiveVisibilityMatrix(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadCollectiveVisibilityCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			world := newCollectiveWorld(t, ctx, pool, collectiveWorldSpec{
				caseName:       testCase.Name,
				dataAccess:     testCase.Collective.DataAccess,
				acceptanceMode: testCase.Collective.AcceptanceMode,
				discoverable:   testCase.OwnerIsDiscoverable,
				visibility:     testCase.TranscriptVisibility,
				transcripts:    len(testCase.Transcripts),
			})
			defer world.cleanup(t, ctx)

			for i, transcript := range testCase.Transcripts {
				for _, step := range transcript.Steps {
					world.step(t, h, i, step)
				}
			}

			world.assertTranscriptCollectives(t, h, testCase.Viewer, testCase.Expect.TranscriptCollectives)
			world.assertProjectRollup(t, ctx, h, testCase.Viewer, testCase.Expect)
			world.assertContributions(t, h, testCase.Expect, testCase.Why)
			oracle := world.searchOracle(t, ctx, h, testCase.Viewer)
			world.assertOracleAgrees(t, testCase, oracle)
		})
	}
}

// assertOracleAgrees compares the new surfaces with the shipped
// SearchCollectives on the same world. The copied predicate is only proven
// equivalent by a comparison that could disagree, so this runs on every case
// whose difference is NOT explained by the contributor opt-in, which the oracle
// does not carry.
func (w *collectiveWorld) assertOracleAgrees(t *testing.T, testCase cvCase, oracleVisible bool) {
	t.Helper()
	declared := testCase.Expect.SearchCollectives == "visible"
	if oracleVisible != declared {
		t.Fatalf("the shipped SearchCollectives reports the collective visible=%v to the %s viewer, but the corpus "+
			"declares %q. The corpus has drifted from the oracle it compares against; fix the declaration or the world.",
			oracleVisible, testCase.Viewer, testCase.Expect.SearchCollectives)
	}
	optInSatisfied := testCase.OwnerIsDiscoverable || testCase.Viewer == "owner"
	if !optInSatisfied || testCase.Expect.ApprovedCount == 0 || testCase.Expect.TranscriptCollectives == "not_found" {
		return
	}
	if (testCase.Expect.TranscriptCollectives == "listed") != oracleVisible {
		t.Errorf("the per-transcript surface says %q while SearchCollectives says visible=%v. The visibility predicate is "+
			"a copy of the oracle's and must decide identically (%s).", testCase.Expect.TranscriptCollectives, oracleVisible, testCase.Why)
	}
	if (testCase.Expect.ProjectRollup == "listed") != oracleVisible {
		t.Errorf("the project roll-up says %q while SearchCollectives says visible=%v. The visibility predicate is a copy "+
			"of the oracle's and must decide identically (%s).", testCase.Expect.ProjectRollup, oracleVisible, testCase.Why)
	}
}

// ---------------------------------------------------------------------------
// The contributor opt-in, on both viewer-facing surfaces.
// ---------------------------------------------------------------------------

type coExpect struct {
	HTTPStatus            int    `yaml:"http_status"`
	TranscriptCollectives string `yaml:"transcript_collectives"`
	ProjectRollup         string `yaml:"project_rollup"`
}

type coCase struct {
	Name                string   `yaml:"name"`
	Why                 string   `yaml:"why"`
	OwnerIsDiscoverable bool     `yaml:"owner_is_discoverable"`
	Viewer              string   `yaml:"viewer"`
	Expect              coExpect `yaml:"expect"`
}

var requiredContributorOptInCases = []string{
	"opted_in_owner_listed_to_anonymous_viewer",
	"opted_in_owner_listed_to_other_signed_in_viewer",
	"opted_in_owner_listed_to_themselves",
	"opted_out_owner_returns_empty_list_not_forbidden_to_anonymous",
	"opted_out_owner_hidden_from_other_signed_in_viewer",
	"opted_out_owner_still_sees_their_own_memberships",
}

func loadContributorOptInCases(t *testing.T) []coCase {
	t.Helper()
	cases, err := decodeFixtureRows[coCase](contributorOptInYAML)
	if err != nil {
		t.Fatalf("load the contributor opt-in corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" {
			t.Fatalf("contributor opt-in case %+v must carry a name and a reason it exists", c)
		}
		if present[c.Name] {
			t.Fatalf("the contributor opt-in corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Expect.HTTPStatus != http.StatusOK {
			t.Fatalf("case %q expects status %d. Withholding memberships is answered with an empty list and 200, never a "+
				"refusal: a refusal would itself confirm that hidden memberships exist.", c.Name, c.Expect.HTTPStatus)
		}
	}
	for _, required := range requiredContributorOptInCases {
		if !present[required] {
			t.Fatalf("the contributor opt-in corpus no longer contains %q. That case exists because losing it hides a real "+
				"failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

func TestContributorOptInMatrix(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadContributorOptInCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			world := newCollectiveWorld(t, ctx, pool, collectiveWorldSpec{
				caseName: testCase.Name,
				// Public and open, so collective visibility can never be the
				// reason a row is missing. The only variable is the opt-in.
				dataAccess:     "public",
				acceptanceMode: "open",
				discoverable:   testCase.OwnerIsDiscoverable,
				visibility:     "public",
				transcripts:    1,
			})
			defer world.cleanup(t, ctx)
			world.step(t, h, 0, "submit")

			status, ids := world.transcriptCollectives(t, h, testCase.Viewer)
			if status == http.StatusForbidden {
				t.Fatalf("the per-transcript surface refused with 403. A refusal confirms that memberships exist and are "+
					"being withheld, which is the leak the empty list exists to avoid (%s).", testCase.Why)
			}
			if status != testCase.Expect.HTTPStatus {
				t.Fatalf("the per-transcript surface answered %d, want %d (%s)", status, testCase.Expect.HTTPStatus, testCase.Why)
			}
			world.assertMembership(t, "per-transcript surface", ids, testCase.Expect.TranscriptCollectives, testCase.Why)

			rollup := world.projectRollup(t, ctx, h, testCase.Viewer)
			world.assertMembership(t, "project roll-up", rollup, testCase.Expect.ProjectRollup, testCase.Why)
		})
	}
}

// assertMembership checks whether the world's collective is present in a
// surface's answer. "listed" and "empty" are the two outcomes the opt-in corpus
// distinguishes; an empty answer must be empty, not merely missing this one row.
func (w *collectiveWorld) assertMembership(t *testing.T, surface string, ids []string, want, why string) {
	t.Helper()
	switch want {
	case "listed":
		if !containsID(ids, w.groupID()) {
			t.Errorf("the %s omits the collective, want it listed (%s)", surface, why)
		}
	case "empty":
		if len(ids) != 0 {
			t.Errorf("the %s returned %d collective(s), want an empty list (%s)", surface, len(ids), why)
		}
	default:
		t.Fatalf("the corpus asks for outcome %q on the %s; the outcomes are listed and empty", want, surface)
	}
}

// ---------------------------------------------------------------------------
// The world: one collective, one project, and the four identities that ask.
// ---------------------------------------------------------------------------

type collectiveWorldSpec struct {
	caseName       string
	dataAccess     string
	acceptanceMode string
	discoverable   bool
	visibility     string
	transcripts    int
}

type collectiveWorld struct {
	pool        *pgxpool.Pool
	owner       pgtype.UUID
	ownerName   string
	moderator   pgtype.UUID
	member      pgtype.UUID
	other       pgtype.UUID
	group       pgtype.UUID
	groupName   string
	projectHash string
	transcripts []pgtype.UUID
}

func newCollectiveWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, spec collectiveWorldSpec) *collectiveWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &collectiveWorld{
		pool:        pool,
		ownerName:   "cv-owner-" + suffix,
		groupName:   spec.caseName + "-" + suffix,
		projectHash: fixtureProjectHash(spec.caseName + suffix),
	}
	w.owner = cvInsertUser(t, ctx, pool, w.ownerName, spec.discoverable)
	w.moderator = cvInsertUser(t, ctx, pool, "cv-mod-"+suffix, true)
	w.member = cvInsertUser(t, ctx, pool, "cv-member-"+suffix, true)
	w.other = cvInsertUser(t, ctx, pool, "cv-other-"+suffix, true)

	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode, data_access) VALUES ($1, $2, $3, $4) RETURNING id
	`, w.groupName, w.moderator, spec.acceptanceMode, spec.dataAccess).Scan(&w.group); err != nil {
		t.Fatalf("create the collective: %v", err)
	}
	shareAddMember(t, ctx, pool, w.group, w.moderator, "owner")
	shareAddMember(t, ctx, pool, w.group, w.member, "member")
	// Submitting requires membership, so the contributor is always a member.
	shareAddMember(t, ctx, pool, w.group, w.owner, "member")

	for i := 0; i < spec.transcripts; i++ {
		w.transcripts = append(w.transcripts,
			cvInsertTranscript(t, ctx, pool, w.owner, spec.caseName+"-"+suffix, i, spec.visibility, w.projectHash))
	}
	return w
}

func (w *collectiveWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.group); err != nil {
		t.Errorf("cleanup the collective: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.owner, w.moderator, w.member, w.other)
}

func (w *collectiveWorld) groupID() string { return uuid.UUID(w.group.Bytes).String() }

// viewer resolves a corpus viewer name to the identity that asks. An anonymous
// viewer has no identity at all, which is what makes the NULL branch of the
// visibility predicate reachable.
func (w *collectiveWorld) viewer(t *testing.T, name string) (*AuthUser, bool) {
	t.Helper()
	switch name {
	case "owner":
		return &AuthUser{ID: uuid.UUID(w.owner.Bytes), Username: w.ownerName}, true
	case "member":
		return &AuthUser{ID: uuid.UUID(w.member.Bytes), Username: "cv-member"}, false
	case "other":
		return &AuthUser{ID: uuid.UUID(w.other.Bytes), Username: "cv-other"}, false
	case "anonymous":
		return nil, false
	default:
		t.Fatalf("the corpus names viewer %q; the viewers are owner, member, other and anonymous", name)
		return nil, false
	}
}

func (w *collectiveWorld) request(actor *AuthUser, method, target string, body []byte, params map[string]string) *http.Request {
	route := chi.NewRouteContext()
	for key, value := range params {
		route.URLParams.Add(key, value)
	}
	reqCtx := context.Background()
	if actor != nil {
		reqCtx = context.WithValue(reqCtx, UserContextKey, actor)
	}
	reqCtx = context.WithValue(reqCtx, chi.RouteCtxKey, route)
	return httptest.NewRequest(method, target, bytes.NewReader(body)).WithContext(reqCtx)
}

// step performs one recorded transition on one transcript through the shipped
// handlers, so the corpus exercises the production path rather than writing the
// ledger behind it.
func (w *collectiveWorld) step(t *testing.T, h *Handler, index int, step string) {
	t.Helper()
	owner := &AuthUser{ID: uuid.UUID(w.owner.Bytes), Username: w.ownerName}
	moderator := &AuthUser{ID: uuid.UUID(w.moderator.Bytes), Username: "cv-mod"}
	transcript := uuid.UUID(w.transcripts[index].Bytes).String()
	rec := httptest.NewRecorder()

	switch step {
	case "submit":
		body, err := json.Marshal(map[string][]string{"group_ids": {w.groupID()}})
		if err != nil {
			t.Fatal(err)
		}
		h.ShareTranscript(rec, w.request(owner, http.MethodPost, "/api/v1/transcripts/share", body,
			map[string]string{"id": transcript}))
	case "approve", "reject":
		decision := "approved"
		if step == "reject" {
			decision = "rejected"
		}
		body, err := json.Marshal(map[string]string{"status": decision})
		if err != nil {
			t.Fatal(err)
		}
		h.ReviewShare(rec, w.request(moderator, http.MethodPatch, "/api/v1/groups/shares", body,
			map[string]string{"id": w.groupID(), "transcriptID": transcript}))
	case "unshare":
		h.UnshareTranscript(rec, w.request(owner, http.MethodDelete, "/api/v1/transcripts/share", nil,
			map[string]string{"id": transcript, "groupID": w.groupID()}))
	case "remove":
		h.RemoveGroupTranscript(rec, w.request(moderator, http.MethodDelete, "/api/v1/groups/transcripts", nil,
			map[string]string{"id": w.groupID(), "transcriptID": transcript}))
	default:
		t.Fatalf("the corpus asks for step %q on transcript %d; this interpreter performs submit, approve, reject, "+
			"unshare and remove", step, index)
	}

	if code := rec.Result().StatusCode; code != http.StatusOK {
		t.Fatalf("step %q on transcript %d answered %d, want 200 (body: %s)", step, index, code, rec.Body.String())
	}
}

// transcriptCollectives asks GET /transcripts/{id}/collectives as the viewer and
// returns the status together with the collective ids in the body.
func (w *collectiveWorld) transcriptCollectives(t *testing.T, h *Handler, viewerName string) (int, []string) {
	t.Helper()
	actor, _ := w.viewer(t, viewerName)
	rec := httptest.NewRecorder()
	h.ListTranscriptCollectives(rec, w.request(actor, http.MethodGet, "/api/v1/transcripts/collectives", nil,
		map[string]string{"id": uuid.UUID(w.transcripts[0].Bytes).String()}))
	if rec.Result().StatusCode != http.StatusOK {
		return rec.Result().StatusCode, nil
	}
	var body struct {
		Collectives []struct {
			ID string `json:"id"`
		} `json:"collectives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the per-transcript collectives body: %v (body: %s)", err, rec.Body.String())
	}
	ids := make([]string, 0, len(body.Collectives))
	for _, c := range body.Collectives {
		ids = append(ids, c.ID)
	}
	return http.StatusOK, ids
}

func (w *collectiveWorld) assertTranscriptCollectives(t *testing.T, h *Handler, viewerName, want string) {
	t.Helper()
	status, ids := w.transcriptCollectives(t, h, viewerName)
	if want == "not_found" {
		if status != http.StatusNotFound {
			t.Fatalf("the per-transcript surface answered %d to the %s viewer, want 404: a transcript the viewer may not "+
				"read must be indistinguishable from one that does not exist", status, viewerName)
		}
		return
	}
	if status != http.StatusOK {
		t.Fatalf("the per-transcript surface answered %d to the %s viewer, want 200", status, viewerName)
	}
	present := containsID(ids, w.groupID())
	switch want {
	case "listed":
		if !present {
			t.Errorf("the per-transcript surface omits the collective from the %s viewer's answer, want it listed", viewerName)
		}
	case "absent":
		if present {
			t.Errorf("the per-transcript surface listed the collective to the %s viewer, want it withheld", viewerName)
		}
	default:
		t.Fatalf("the corpus asks for outcome %q on the per-transcript surface; the outcomes are listed, absent and not_found", want)
	}
}

func (w *collectiveWorld) projectRollup(t *testing.T, ctx context.Context, h *Handler, viewerName string) []string {
	t.Helper()
	actor, isOwner := w.viewer(t, viewerName)
	rows, err := h.queries.ListProjectCollectiveRollup(ctx, sqlc.ListProjectCollectiveRollupParams{
		OwnerID:       w.owner,
		ProjectHash:   w.projectHash,
		UserID:        viewerID(actor),
		ViewerIsOwner: isOwner,
	})
	if err != nil {
		t.Fatalf("read the project roll-up as the %s viewer: %v", viewerName, err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, uuid.UUID(row.ID.Bytes).String())
	}
	return ids
}

func (w *collectiveWorld) assertProjectRollup(t *testing.T, ctx context.Context, h *Handler, viewerName string, expect cvExpect) {
	t.Helper()
	actor, isOwner := w.viewer(t, viewerName)
	rows, err := h.queries.ListProjectCollectiveRollup(ctx, sqlc.ListProjectCollectiveRollupParams{
		OwnerID:       w.owner,
		ProjectHash:   w.projectHash,
		UserID:        viewerID(actor),
		ViewerIsOwner: isOwner,
	})
	if err != nil {
		t.Fatalf("read the project roll-up as the %s viewer: %v", viewerName, err)
	}
	var found *sqlc.ListProjectCollectiveRollupRow
	for i := range rows {
		if uuid.UUID(rows[i].ID.Bytes).String() == w.groupID() {
			found = &rows[i]
		}
	}
	switch expect.ProjectRollup {
	case "listed":
		if found == nil {
			t.Fatalf("the project roll-up omits the collective from the %s viewer's answer, want it listed", viewerName)
		}
		if int(found.TranscriptCount) != expect.ProjectRollupTranscriptCount {
			t.Errorf("the project roll-up counts %d transcript(s) in the collective, want %d; the roll-up counts the "+
				"project's ACCEPTED transcripts", found.TranscriptCount, expect.ProjectRollupTranscriptCount)
		}
	case "absent":
		if found != nil {
			t.Errorf("the project roll-up listed the collective to the %s viewer, want it withheld", viewerName)
		}
	default:
		t.Fatalf("the corpus asks for outcome %q on the project roll-up; the outcomes are listed and absent", expect.ProjectRollup)
	}
}

// assertContributions drives GET /users/me/collectives/contributions as the
// owner - the only viewer that route has - and checks the four counters.
func (w *collectiveWorld) assertContributions(t *testing.T, h *Handler, expect cvExpect, why string) {
	t.Helper()
	owner := &AuthUser{ID: uuid.UUID(w.owner.Bytes), Username: w.ownerName}
	rec := httptest.NewRecorder()
	h.ListMyCollectiveContributions(rec, w.request(owner, http.MethodGet, "/api/v1/users/me/collectives/contributions", nil, nil))
	if rec.Result().StatusCode != http.StatusOK {
		t.Fatalf("the contributions surface answered %d, want 200 (body: %s)", rec.Result().StatusCode, rec.Body.String())
	}
	var body struct {
		Collectives []struct {
			ID                    string `json:"id"`
			ApprovedCount         int32  `json:"approved_count"`
			PendingCount          int32  `json:"pending_count"`
			RejectedAttemptCount  int32  `json:"rejected_attempt_count"`
			WithdrawnAttemptCount int32  `json:"withdrawn_attempt_count"`
		} `json:"collectives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the contributions body: %v (body: %s)", err, rec.Body.String())
	}
	for _, row := range body.Collectives {
		if row.ID != w.groupID() {
			continue
		}
		if !expect.ContributionsListed {
			t.Fatalf("the contributions surface listed the collective, want it absent (%s)", why)
		}
		if row.ApprovedCount != expect.ApprovedCount {
			t.Errorf("approved_count = %d, want %d; it counts DISTINCT TRANSCRIPTS currently accepted (%s)",
				row.ApprovedCount, expect.ApprovedCount, why)
		}
		if row.PendingCount != expect.PendingCount {
			t.Errorf("pending_count = %d, want %d; it counts DISTINCT TRANSCRIPTS awaiting review (%s)",
				row.PendingCount, expect.PendingCount, why)
		}
		if row.RejectedAttemptCount != expect.RejectedAttemptCount {
			t.Errorf("rejected_attempt_count = %d, want %d; it counts REFUSAL EVENTS, not transcripts (%s)",
				row.RejectedAttemptCount, expect.RejectedAttemptCount, why)
		}
		if row.WithdrawnAttemptCount != expect.WithdrawnAttemptCount {
			t.Errorf("withdrawn_attempt_count = %d, want %d; it counts WITHDRAWAL EVENTS - retractions by the owner and "+
				"removals by the collective, added together - not transcripts. Before this counter existed those events "+
				"were counted nowhere and a withdrawn contribution vanished from every total (%s)",
				row.WithdrawnAttemptCount, expect.WithdrawnAttemptCount, why)
		}
		return
	}
	if expect.ContributionsListed {
		t.Fatalf("the contributions surface omits the collective entirely, want it listed with approved=%d pending=%d "+
			"rejected_attempt=%d withdrawn_attempt=%d (%s)", expect.ApprovedCount, expect.PendingCount,
			expect.RejectedAttemptCount, expect.WithdrawnAttemptCount, why)
	}
}

// searchOracle asks the shipped SearchCollectives whether this viewer can see
// this collective at all. It is the reference the copied predicate is compared
// against.
func (w *collectiveWorld) searchOracle(t *testing.T, ctx context.Context, h *Handler, viewerName string) bool {
	t.Helper()
	actor, _ := w.viewer(t, viewerName)
	rows, err := h.queries.SearchCollectives(ctx, sqlc.SearchCollectivesParams{
		Column1: pgtype.Text{String: w.groupName, Valid: true},
		UserID:  viewerID(actor),
		Limit:   50,
	})
	if err != nil {
		t.Fatalf("ask the SearchCollectives oracle as the %s viewer: %v", viewerName, err)
	}
	for _, row := range rows {
		if uuid.UUID(row.ID.Bytes).String() == w.groupID() {
			return true
		}
	}
	return false
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func cvInsertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username string, discoverable bool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id, is_discoverable)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, int64(uuid.New().ID()), username, username, discoverable).Scan(&id); err != nil {
		t.Fatalf("insert %s: %v", username, err)
	}
	return id
}

// cvInsertTranscript stores one transcript of the world's project. Every
// transcript carries the project hash, which is required, and the visibility the
// case declares, which gates the request before collective visibility does.
func cvInsertTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID,
	seed string, index int, visibility, projectHash string) pgtype.UUID {
	t.Helper()
	localID := seed + "-" + string(rune('a'+index))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)",
		database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	id := toPgUUID(uuid.New())
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash)
		VALUES ($1,$2,$3,$4,$5,'claude-code',$6,$7,$8,'0.1.0',$9,$10,'aes-256-gcm-random-nonce-v1',1,$11)
		RETURNING id
	`, id, owner, localID, "t-"+localID, visibility, "m-"+localID, "blob/"+localID, int64(len(localID)),
		schema.ComputeTranscriptHash([]byte(localID)), []byte("fixture-wrapped-data-key"), projectHash).Scan(&id); err != nil {
		t.Fatalf("insert transcript %s: %v", localID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}
