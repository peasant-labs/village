//go:build integration

package handler

// Offering one exact selection of sessions to a collective, driven through the
// mounted production route against a REAL PostgreSQL. The cases live in
// testdata/contribution-selection-write.yaml.
//
// It has to be a real database. Whether a session is live is read from the
// append-only attempt ledger and the derived share row is written by a trigger,
// so a Go test computing its own expectation would pass with neither installed.
// "Nothing was written" is a property of one PostgreSQL transaction, and the
// governance event of a widened private session is written by a trigger. And
// every change made while the request waits is made by a second connection
// while the test holds the publish lock the request is blocked on, so the
// locked re-check is observed seeing a state its preflight never saw.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/contribution-selection-write.yaml
var contributionSelectionWriteYAML []byte

type selectionWriteTranscript struct {
	Key        string   `yaml:"key"`
	Project    string   `yaml:"project"`
	Visibility string   `yaml:"visibility"`
	Parent     string   `yaml:"parent"`
	Owner      string   `yaml:"owner"`
	History    []string `yaml:"history"`
}

type selectionWriteRequest struct {
	Prefix     string   `yaml:"prefix"`
	Collective string   `yaml:"collective"`
	NoAuth     bool     `yaml:"no_auth"`
	Project    string   `yaml:"project"`
	Select     []string `yaml:"select"`
	Approve    []string `yaml:"approve"`
	RawBody    string   `yaml:"raw_body"`
	Oversize   bool     `yaml:"oversize"`
	// PadTo and PadApprovalsTo repeat the first selected (or approved) id
	// until the list holds that many entries, to reach the contract's
	// per-request bound without naming a thousand sessions.
	PadTo          int `yaml:"pad_to"`
	PadApprovalsTo int `yaml:"pad_approvals_to"`
}

type selectionWriteRace struct {
	Hold       string `yaml:"hold"`
	Action     string `yaml:"action"`
	Project    string `yaml:"project"`
	Acceptance string `yaml:"acceptance"`
}

type selectionWriteDetail struct {
	Key            string `yaml:"key"`
	Code           string `yaml:"code"`
	CurrentProject string `yaml:"current_project"`
}

type selectionWriteExpect struct {
	Status        int                    `yaml:"status"`
	Code          string                 `yaml:"code"`
	Details       []selectionWriteDetail `yaml:"details"`
	ErrorContains []string               `yaml:"error_contains"`
	Shared        map[string]string      `yaml:"shared"`
	AlreadyShared []string               `yaml:"already_shared"`
	Attempts      map[string]int         `yaml:"attempts"`
	Derived       map[string]string      `yaml:"derived"`
	Visibility    map[string]string      `yaml:"visibility"`
	Audit         map[string]int         `yaml:"audit"`
}

type selectionWriteCase struct {
	Name        string                     `yaml:"name"`
	Why         string                     `yaml:"why"`
	Acceptance  string                     `yaml:"acceptance"`
	Membership  string                     `yaml:"membership"`
	Inject      string                     `yaml:"inject"`
	Transcripts []selectionWriteTranscript `yaml:"transcripts"`
	Request     selectionWriteRequest      `yaml:"request"`
	Race        *selectionWriteRace        `yaml:"race"`
	Expect      selectionWriteExpect       `yaml:"expect"`
}

// requiredSelectionWriteCases names the cases that must exist. Each one guards a
// behaviour the operation promises: offering exactly the list, already-shared
// separation, private approvals, every refusal code and its details, the
// request-shape refusals, the whole-selection rollback, and every change made
// while the request waits for its locks, with its controls.
var requiredSelectionWriteCases = []string{
	"offers_exactly_the_listed_sessions",
	"curated_collective_opens_pending_attempts",
	"a_contributor_may_contribute",
	"a_root_is_never_expanded_into_its_helpers",
	"a_helper_in_another_project_is_accepted",
	"a_helper_selected_alone_is_not_checked_against_the_project",
	"a_decided_session_is_offered_again",
	"a_live_attempt_in_another_collective_does_not_count",
	"duplicate_ids_are_folded",
	"live_sessions_are_reported_already_shared",
	"every_selected_session_already_live_answers_200_with_nothing_shared",
	"private_session_with_approval_is_shared_and_widened",
	"private_session_without_approval_refuses_everything",
	"an_approval_for_a_session_that_is_not_private_is_ignored",
	"moved_root_refuses_everything_naming_its_current_project",
	"missing_session_refuses_everything",
	"another_owners_session_reads_as_missing",
	"every_offender_is_listed_with_its_own_code",
	"a_moved_live_root_is_refused_not_reported_already_shared",
	"non_member_is_refused_with_membership",
	"pending_membership_is_refused_with_membership",
	"an_unknown_collective_is_refused_with_membership",
	"policy_refusal_is_request_level",
	"an_empty_selection_violates_the_contract",
	"a_non_canonical_id_violates_the_contract",
	"missing_private_approvals_violates_the_contract",
	"a_body_that_is_not_json_is_refused",
	"a_collective_id_that_is_not_an_identifier_is_refused",
	"a_body_over_one_mebibyte_is_refused",
	"unauthenticated_is_refused",
	"the_groups_alias_offers_the_same_way",
	"a_failure_in_the_middle_of_the_write_rolls_everything_back",
	"becomes_private_while_waiting_is_refused",
	"private_before_the_preflight_is_refused_by_the_preflight",
	"approved_private_while_waiting_is_shared",
	"becomes_live_while_waiting_is_reported_already_shared",
	"withdrawn_while_waiting_is_stale",
	"decided_while_waiting_is_stale",
	"moved_while_waiting_is_refused",
	"policy_change_while_waiting_refuses_everything",
	"membership_removed_while_waiting_refuses_everything",
	"a_competing_writer_is_serialized_by_the_row_lock",
	"a_selection_at_the_bound_is_accepted",
	"a_selection_past_the_bound_violates_the_contract",
	"approvals_past_the_bound_violate_the_contract",
}

var (
	selectionWriteMemberships = map[string]bool{"": true, "member": true, "contributor": true, "pending": true, "none": true}
	selectionWriteAcceptances = map[string]bool{"": true, "open": true, "curated": true, "verified_only": true}
	selectionWriteInjections  = map[string]bool{"": true, "refuse_last_ledger_insert": true}
	selectionWriteActions     = map[string]bool{
		selectionRaceMakePrivate: true, selectionRaceMakePrivateBefore: true, selectionRaceAppendApproved: true,
		selectionRaceWithdraw: true, selectionRaceReject: true, selectionRaceMove: true,
		selectionRaceSetAcceptance: true, selectionRaceRemoveMembership: true, selectionRaceCompete: true,
	}
	selectionWriteToken = regexp.MustCompile(`\{(id|upper|project):[^}]*\}`)
)

func loadSelectionWriteCases(t *testing.T) []selectionWriteCase {
	t.Helper()
	cases, err := decodeFixtureRows[selectionWriteCase](contributionSelectionWriteYAML)
	if err != nil {
		t.Fatalf("load the selection write corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if err := validateSelectionWriteCase(c); err != nil {
			t.Fatalf("selection write case %q: %v", c.Name, err)
		}
		if present[c.Name] {
			t.Fatalf("the selection write corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, required := range requiredSelectionWriteCases {
		if !present[required] {
			t.Fatalf("the selection write corpus no longer contains %q; restore it rather than deleting it from this "+
				"manifest", required)
		}
	}
	return cases
}

// validateSelectionWriteCase refuses a case that could pass without asserting
// what it claims: an undeclared key, an unknown action, a 200 without its
// receipt, or a refusal code without the offenders it names.
func validateSelectionWriteCase(c selectionWriteCase) error {
	if c.Name == "" || strings.TrimSpace(c.Why) == "" || c.Expect.Status == 0 {
		return fmt.Errorf("a case needs a name, a reason and an expected status")
	}
	if !selectionWriteMemberships[c.Membership] || !selectionWriteAcceptances[c.Acceptance] || !selectionWriteInjections[c.Inject] {
		return fmt.Errorf("membership %q, acceptance %q or inject %q is not one the test knows", c.Membership, c.Acceptance, c.Inject)
	}
	declared := map[string]bool{}
	for _, tr := range c.Transcripts {
		if tr.Key == "" || tr.Project == "" || strings.HasPrefix(tr.Key, "ghost") || declared[tr.Key] {
			return fmt.Errorf("transcript %+v needs a unique key that does not start with ghost, and a project", tr)
		}
		if tr.Parent != "" && !declared[tr.Parent] {
			return fmt.Errorf("transcript %q names parent %q, which is not declared before it", tr.Key, tr.Parent)
		}
		if tr.Owner != "" && tr.Owner != fixtureOwnerOther {
			return fmt.Errorf("transcript %q has owner %q; only other is known", tr.Key, tr.Owner)
		}
		declared[tr.Key] = true
	}
	requestKey := func(key string) error {
		if declared[key] || strings.HasPrefix(key, "ghost") {
			return nil
		}
		return fmt.Errorf("the request names %q, which is not a declared transcript or a ghost", key)
	}
	stateKey := func(key string) error {
		if declared[key] {
			return nil
		}
		return fmt.Errorf("the expected state names %q, which is not a declared transcript", key)
	}
	r := c.Request
	if r.RawBody == "" && (r.Project == "" || len(r.Select) == 0) {
		return fmt.Errorf("a request without raw_body needs a project and a selection")
	}
	if r.RawBody != "" && (r.Project != "" || len(r.Select) > 0 || len(r.Approve) > 0 || r.Oversize ||
		r.PadTo != 0 || r.PadApprovalsTo != 0) {
		return fmt.Errorf("raw_body replaces project, select, approve, oversize and padding")
	}
	if r.PadTo < 0 || r.PadApprovalsTo < 0 || (r.PadTo != 0 && r.PadTo < len(r.Select)) ||
		(r.PadApprovalsTo != 0 && (len(r.Approve) == 0 || r.PadApprovalsTo < len(r.Approve))) {
		return fmt.Errorf("pad_to and pad_approvals_to name a length at least the listed ids, and pad_approvals_to needs an approval")
	}
	if r.Prefix != "" && r.Prefix != "/collectives" && r.Prefix != "/groups" {
		return fmt.Errorf("prefix %q is not a collective prefix", r.Prefix)
	}
	if r.Collective != "" && r.Collective != fixtureCollectiveUnknown && !strings.HasPrefix(r.Collective, "literal:") {
		return fmt.Errorf("collective %q is not unknown or literal:<text>", r.Collective)
	}
	for _, key := range append(append([]string{}, r.Select...), r.Approve...) {
		if err := requestKey(key); err != nil {
			return err
		}
	}
	if c.Race != nil {
		if !selectionWriteActions[c.Race.Action] || !declared[c.Race.Hold] {
			return fmt.Errorf("race %+v needs a known action and a declared hold", *c.Race)
		}
		if (c.Race.Action == selectionRaceMove) != (c.Race.Project != "") || (c.Race.Action == selectionRaceSetAcceptance) != (c.Race.Acceptance != "") {
			return fmt.Errorf("race %+v: move needs exactly a project and set_acceptance exactly an acceptance", *c.Race)
		}
		if c.Inject != "" {
			return fmt.Errorf("a race case cannot also inject a failure")
		}
	}
	e := c.Expect
	if len(e.Attempts) == 0 {
		return fmt.Errorf("every case asserts the persisted attempts of its sessions")
	}
	for _, m := range []map[string]int{e.Attempts, e.Audit} {
		for key := range m {
			if err := stateKey(key); err != nil {
				return err
			}
		}
	}
	for _, m := range []map[string]string{e.Derived, e.Visibility} {
		for key := range m {
			if err := stateKey(key); err != nil {
				return err
			}
		}
	}
	if e.Status == http.StatusOK {
		if e.Shared == nil || e.AlreadyShared == nil || e.Code != "" || len(e.Details) > 0 || len(e.ErrorContains) > 0 {
			return fmt.Errorf("a 200 states its whole receipt (shared and already_shared) and no refusal")
		}
		for key := range e.Shared {
			if err := stateKey(key); err != nil {
				return err
			}
		}
		for _, key := range e.AlreadyShared {
			if err := stateKey(key); err != nil {
				return err
			}
		}
		return nil
	}
	if e.Shared != nil || e.AlreadyShared != nil {
		return fmt.Errorf("a refusal has no receipt")
	}
	if (e.Code == "") != (len(e.Details) == 0) {
		return fmt.Errorf("a refusal code and its details come together")
	}
	if e.Code != "" && !schema.VillageContributionErrorCode(e.Code).IsValid() {
		return fmt.Errorf("code %q is not in the contract's closed set", e.Code)
	}
	for _, d := range e.Details {
		if !schema.VillageContributionErrorCode(d.Code).IsValid() {
			return fmt.Errorf("detail code %q is not in the contract's closed set", d.Code)
		}
		if d.Key != "" {
			if err := requestKey(d.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

// selectionWriteWorld is the contribute world of one case, mounted behind the
// production route registration with a real signed token.
type selectionWriteWorld struct {
	*contributeWorld
	spec       selectionWriteCase
	h          *Handler
	router     http.Handler
	token      string
	otherGroup pgtype.UUID
	ghosts     map[string]pgtype.UUID
}

func newSelectionWriteWorld(t *testing.T, ctx context.Context, c selectionWriteCase, secret string, world *contributeWorld) *selectionWriteWorld {
	t.Helper()
	w := &selectionWriteWorld{contributeWorld: world, spec: c, ghosts: map[string]pgtype.UUID{}}
	switch c.Membership {
	case string(schema.VillageGroupRoleContributor), string(schema.VillageGroupRolePending):
		shareAddMember(t, ctx, w.pool, w.group, w.member, c.Membership)
	}
	for ordinal, tr := range c.Transcripts {
		w.insertTranscript(t, ctx, ordinal, tr.Key, tr.Project, tr.Visibility, tr.Owner, tr.Parent)
	}
	for _, tr := range c.Transcripts {
		w.appendHistory(t, ctx, tr)
	}
	for _, key := range append(append([]string{}, c.Request.Select...), c.Request.Approve...) {
		if _, declared := w.transcripts[key]; !declared {
			if _, known := w.ghosts[key]; !known {
				w.ghosts[key] = toPgUUID(uuid.New())
			}
		}
	}

	w.h = New(&config.Config{JWTSecret: secret}, w.pool, nil)
	router := chi.NewRouter()
	router.Route(APIPrefix, w.h.RegisterContributionSelectionRoutes)
	w.router = router
	token, err := auth.CreateToken(secret, uuid.UUID(w.member.Bytes), w.memberName)
	if err != nil {
		t.Fatal(err)
	}
	w.token = token
	return w
}

// appendHistory writes a transcript's attempt ledger directly, one event per
// status, numbered from one in each collective. A status prefixed "other:"
// lands in a second collective.
func (w *selectionWriteWorld) appendHistory(t *testing.T, ctx context.Context, tr selectionWriteTranscript) {
	t.Helper()
	next := map[bool]int{}
	for _, entry := range tr.History {
		elsewhere := strings.HasPrefix(entry, "other:")
		group := w.group
		if elsewhere {
			if !w.otherGroup.Valid {
				if err := w.pool.QueryRow(ctx, `
					INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, 'open') RETURNING id
				`, "selection-other-"+w.suffix, w.moderator).Scan(&w.otherGroup); err != nil {
					t.Fatalf("create the second collective: %v", err)
				}
			}
			group = w.otherGroup
		}
		next[elsewhere]++
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, $3, $4)
		`, w.transcripts[tr.Key], group, next[elsewhere], strings.TrimPrefix(entry, "other:")); err != nil {
			t.Fatalf("record event %q for %s: %v", entry, tr.Key, err)
		}
	}
}

func (w *selectionWriteWorld) idOf(t *testing.T, key string) pgtype.UUID {
	t.Helper()
	if id, ok := w.transcripts[key]; ok {
		return id
	}
	if id, ok := w.ghosts[key]; ok {
		return id
	}
	t.Fatalf("case %s: no transcript or ghost is called %q", w.caseName, key)
	return pgtype.UUID{}
}

func (w *selectionWriteWorld) idText(t *testing.T, key string) string {
	t.Helper()
	return uuid.UUID(w.idOf(t, key).Bytes).String()
}

// keyOf names an id by the key the case gave it, for readable failures.
func (w *selectionWriteWorld) keyOf(id string) string {
	for key, pg := range w.transcripts {
		if uuid.UUID(pg.Bytes).String() == id {
			return key
		}
	}
	for key, pg := range w.ghosts {
		if uuid.UUID(pg.Bytes).String() == id {
			return key
		}
	}
	return id
}

func (w *selectionWriteWorld) target() string {
	prefix := w.spec.Request.Prefix
	if prefix == "" {
		prefix = "/collectives"
	}
	collective := w.groupID()
	switch {
	case w.spec.Request.Collective == fixtureCollectiveUnknown:
		collective = uuid.NewString()
	case strings.HasPrefix(w.spec.Request.Collective, "literal:"):
		collective = strings.TrimPrefix(w.spec.Request.Collective, "literal:")
	}
	return APIPrefix + prefix + "/" + collective + "/shares/selection"
}

func (w *selectionWriteWorld) body(t *testing.T) []byte {
	t.Helper()
	r := w.spec.Request
	if r.RawBody != "" {
		out := r.RawBody
		for key := range w.transcripts {
			id := w.idText(t, key)
			out = strings.ReplaceAll(out, "{id:"+key+"}", id)
			out = strings.ReplaceAll(out, "{upper:"+key+"}", strings.ToUpper(id))
		}
		for _, tr := range w.spec.Transcripts {
			out = strings.ReplaceAll(out, "{project:"+tr.Project+"}", w.projectHash(tr.Project))
		}
		if leftover := selectionWriteToken.FindString(out); leftover != "" {
			t.Fatalf("case %s: the raw body token %s names no transcript or project", w.caseName, leftover)
		}
		return []byte(out)
	}
	ids := make([]schema.TranscriptID, 0, len(r.Select))
	for _, key := range r.Select {
		ids = append(ids, schema.TranscriptID(w.idText(t, key)))
	}
	if r.Oversize {
		for len(ids) <= (1<<20)/36+1 {
			ids = append(ids, ids[0])
		}
	}
	for len(ids) < r.PadTo {
		ids = append(ids, ids[0])
	}
	approvals := make([]schema.TranscriptID, 0, len(r.Approve))
	for _, key := range r.Approve {
		approvals = append(approvals, schema.TranscriptID(w.idText(t, key)))
	}
	for len(approvals) < r.PadApprovalsTo {
		approvals = append(approvals, approvals[0])
	}
	body, err := json.Marshal(schema.VillageBatchShareSelectionRequest{
		ProjectHash:      schema.ProjectHash(w.projectHash(r.Project)),
		TranscriptIDs:    ids,
		PrivateApprovals: approvals,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Oversize && len(body) <= 1<<20 {
		t.Fatalf("case %s: the oversize body is only %d bytes, so it would not exercise the 1 MiB cap", w.caseName, len(body))
	}
	return body
}

func (w *selectionWriteWorld) request(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, w.target(), bytes.NewReader(w.body(t)))
	req.Header.Set("Content-Type", "application/json")
	if !w.spec.Request.NoAuth {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}
	return req
}

func (w *selectionWriteWorld) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Persisted state
// ---------------------------------------------------------------------------

func (w *selectionWriteWorld) auditShared(t *testing.T, ctx context.Context, key string) int {
	t.Helper()
	var count int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM transcript_governance_events_audit
		WHERE transcript_id = $1 AND event_type = 'visibility_changed' AND changed_by = $2 AND visibility = 'shared'
	`, w.transcripts[key], w.member).Scan(&count); err != nil {
		t.Fatalf("count the governance events of %q: %v", key, err)
	}
	return count
}

func (w *selectionWriteWorld) latestStatus(t *testing.T, ctx context.Context, key string) string {
	t.Helper()
	var status string
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT status FROM transcript_share_attempts WHERE transcript_id = $1 AND group_id = $2
		                 ORDER BY event_num DESC LIMIT 1), '<none>')
	`, w.transcripts[key], w.group).Scan(&status); err != nil {
		t.Fatalf("read the latest attempt of %q: %v", key, err)
	}
	return status
}

// selectionWriteSnapshot is everything a refused request must leave untouched:
// every attempt of every session in any collective, every visibility, and
// every governance event recorded on the caller's behalf.
type selectionWriteSnapshot struct {
	attempts   map[string]int
	visibility map[string]string
	audit      int
}

func (w *selectionWriteWorld) snapshot(t *testing.T, ctx context.Context) selectionWriteSnapshot {
	t.Helper()
	s := selectionWriteSnapshot{attempts: map[string]int{}, visibility: map[string]string{}}
	for key, id := range w.transcripts {
		var count int
		if err := w.pool.QueryRow(ctx, "SELECT count(*)::int FROM transcript_share_attempts WHERE transcript_id = $1", id).Scan(&count); err != nil {
			t.Fatalf("count every attempt of %q: %v", key, err)
		}
		s.attempts[key] = count
		s.visibility[key] = w.visibility(t, ctx, key)
	}
	if err := w.pool.QueryRow(ctx, "SELECT count(*)::int FROM transcript_governance_events_audit WHERE changed_by = $1", w.member).Scan(&s.audit); err != nil {
		t.Fatalf("count the governance events recorded for the caller: %v", err)
	}
	return s
}

func (s selectionWriteSnapshot) String() string {
	keys := make([]string, 0, len(s.attempts))
	for key := range s.attempts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d/%s", key, s.attempts[key], s.visibility[key]))
	}
	parts = append(parts, fmt.Sprintf("audit=%d", s.audit))
	return strings.Join(parts, " ")
}

func (w *selectionWriteWorld) assertPersisted(t *testing.T, ctx context.Context) {
	t.Helper()
	e := w.spec.Expect
	for key, want := range e.Attempts {
		if got := w.attemptCount(t, ctx, key); got != want {
			t.Errorf("%s has %d attempts in this collective, want %d (%s)", key, got, want, w.spec.Why)
		}
	}
	for key, want := range e.Derived {
		if got := w.derivedStatus(t, ctx, key); got != want {
			t.Errorf("the derived share row of %s is %s, want %s (%s)", key, got, want, w.spec.Why)
		}
	}
	for key, want := range e.Visibility {
		if got := w.visibility(t, ctx, key); got != want {
			t.Errorf("%s is %s, want %s (%s)", key, got, want, w.spec.Why)
		}
	}
	for key, want := range e.Audit {
		if got := w.auditShared(t, ctx, key); got != want {
			t.Errorf("%s has %d governance events moving it to shared on the caller's behalf, want %d (%s)", key, got, want, w.spec.Why)
		}
	}
	assertProjectionMatchesLedger(t, ctx, w.pool, w.caseName)
}

// ---------------------------------------------------------------------------
// The answer
// ---------------------------------------------------------------------------

func (w *selectionWriteWorld) assertReceipt(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := w.spec.Expect
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("the receipt is not a JSON object: %v\nbody: %s", err, rec.Body.String())
	}
	for _, field := range []string{"shared", "already_shared"} {
		if value, ok := raw[field]; !ok || !bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
			t.Fatalf("the receipt's %s is %s; the contract declares it a list that is never null or absent", field, value)
		}
	}
	var resp schema.VillageBatchShareResponse
	decoder := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resp); err != nil {
		t.Fatalf("the receipt does not decode into the contract's VillageBatchShareResponse: %v\nbody: %s", err, rec.Body.String())
	}
	if string(resp.ProjectHash) != w.projectHash(w.spec.Request.Project) {
		t.Errorf("the receipt names project %s, want the requested %s", resp.ProjectHash, w.projectHash(w.spec.Request.Project))
	}

	type entry struct{ id, status string }
	var want []entry
	for key, status := range e.Shared {
		want = append(want, entry{w.idText(t, key), status})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].id < want[j].id })
	got := make([]entry, 0, len(resp.Shared))
	for _, s := range resp.Shared {
		got = append(got, entry{string(s.TranscriptID), string(s.Status)})
	}
	render := func(entries []entry) string {
		parts := make([]string, 0, len(entries))
		for _, en := range entries {
			parts = append(parts, w.keyOf(en.id)+":"+en.status)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	if render(got) != render(want) {
		t.Errorf("shared = %s, want %s in ascending id order (%s)", render(got), render(want), w.spec.Why)
	}

	wantAlready := make([]string, 0, len(e.AlreadyShared))
	for _, key := range e.AlreadyShared {
		wantAlready = append(wantAlready, w.idText(t, key))
	}
	sort.Strings(wantAlready)
	gotAlready := make([]string, 0, len(resp.AlreadyShared))
	for _, id := range resp.AlreadyShared {
		gotAlready = append(gotAlready, string(id))
	}
	keys := func(ids []string) string {
		named := make([]string, 0, len(ids))
		for _, id := range ids {
			named = append(named, w.keyOf(id))
		}
		return "[" + strings.Join(named, " ") + "]"
	}
	if strings.Join(gotAlready, ",") != strings.Join(wantAlready, ",") {
		t.Errorf("already_shared = %s, want %s in ascending id order (%s)", keys(gotAlready), keys(wantAlready), w.spec.Why)
	}

	// The receipts add up: every distinct selected session is accounted for
	// exactly once, either offered now or reported already shared.
	selected := map[string]bool{}
	for _, key := range w.spec.Request.Select {
		selected[w.idText(t, key)] = true
	}
	accounted := map[string]int{}
	for _, en := range got {
		accounted[en.id]++
	}
	for _, id := range gotAlready {
		accounted[id]++
	}
	for id := range selected {
		if accounted[id] != 1 {
			t.Errorf("selected session %s appears %d times across shared and already_shared, want exactly once", w.keyOf(id), accounted[id])
		}
	}
	for id := range accounted {
		if !selected[id] {
			t.Errorf("the receipt names %s, which was not selected; a selection is never expanded", w.keyOf(id))
		}
	}
}

func (w *selectionWriteWorld) assertRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := w.spec.Expect
	var resp schema.VillageContributionErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resp); err != nil {
		t.Fatalf("the refusal does not decode into the contract's VillageContributionErrorResponse: %v\nbody: %s", err, rec.Body.String())
	}
	if strings.TrimSpace(resp.Error) == "" {
		t.Fatalf("the refusal carries no message; a person cannot act on a bare status\nbody: %s", rec.Body.String())
	}
	for _, phrase := range e.ErrorContains {
		if !strings.Contains(resp.Error, phrase) {
			t.Errorf("the refusal %q does not say %q (%s)", resp.Error, phrase, w.spec.Why)
		}
	}
	if e.Code == "" {
		if resp.Code != nil || len(resp.Details) != 0 {
			t.Errorf("the refusal carries code %v and details %+v; only a contribution refusal is typed (%s)", resp.Code, resp.Details, w.spec.Why)
		}
		return
	}
	if resp.Code == nil || string(*resp.Code) != e.Code {
		t.Errorf("code = %v, want %s (%s)", resp.Code, e.Code, w.spec.Why)
	}
	if rec.Code == http.StatusConflict && !strings.Contains(resp.Error, "nothing was written") {
		t.Errorf("the conflict %q does not say that nothing was written", resp.Error)
	}

	type detail struct{ id, code, current string }
	render := func(details []detail) string {
		parts := make([]string, 0, len(details))
		for _, d := range details {
			name := "<request>"
			if d.id != "" {
				name = w.keyOf(d.id)
			}
			part := name + ":" + d.code
			if d.current != "" {
				part += "@" + d.current
			}
			parts = append(parts, part)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	var want []detail
	for _, d := range e.Details {
		wd := detail{code: d.Code}
		if d.Key != "" {
			wd.id = w.idText(t, d.Key)
		}
		if d.CurrentProject != "" {
			wd.current = w.projectHash(d.CurrentProject)
		}
		want = append(want, wd)
	}
	sort.SliceStable(want, func(i, j int) bool { return want[i].id < want[j].id })
	got := make([]detail, 0, len(resp.Details))
	for _, d := range resp.Details {
		gd := detail{code: string(d.Code)}
		if d.TranscriptID != nil {
			gd.id = string(*d.TranscriptID)
		}
		if d.CurrentProjectHash != nil {
			gd.current = string(*d.CurrentProjectHash)
		}
		got = append(got, gd)
	}
	if render(got) != render(want) {
		t.Errorf("details = %s, want exactly %s in ascending id order (%s)", render(got), render(want), w.spec.Why)
	}
}

// ---------------------------------------------------------------------------
// Changes made while the request waits
// ---------------------------------------------------------------------------

// race holds the publish lock of the race's session on its own connection,
// starts the request, and proves it waits: PostgreSQL must show it blocked on
// that connection. Only then is the change made and the lock released, so the
// request's locked re-check is the first read that can see it. The lock key
// comes from the production helper, so a change to its shape cannot leave this
// test holding a lock nothing contends for.
func (w *selectionWriteWorld) race(t *testing.T, ctx context.Context, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	race := *w.spec.Race
	lockKey := sessionPublishLockKey(w.member, w.localIDs[race.Hold])
	lockConn, err := w.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the connection that holds the publish lock: %v", err)
	}
	held := false
	release := func() {
		if !held {
			return
		}
		held = false
		if _, err := lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey); err != nil {
			t.Errorf("release the publish lock: %v", err)
		}
	}
	defer func() {
		release()
		lockConn.Release()
	}()
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		t.Fatalf("hold the publish lock of %s: %v", race.Hold, err)
	}
	held = true
	blocker := backendPID(t, ctx, lockConn)

	done := make(chan *httptest.ResponseRecorder, 1)
	start := func() { go func() { done <- w.serve(req) }() }
	await := func(stage string) *httptest.ResponseRecorder {
		select {
		case rec := <-done:
			return rec
		case <-time.After(30 * time.Second):
			t.Fatalf("the request did not answer %s", stage)
			return nil
		}
	}

	if race.Action == selectionRaceMakePrivateBefore {
		// The negative control: the change lands before the preflight, which
		// must refuse without waiting for the lock the test still holds.
		w.perform(t, ctx, race, selectionRaceMakePrivate)
		start()
		select {
		case rec := <-done:
			release()
			return rec
		case <-time.After(15 * time.Second):
			release()
			_ = await("after the publish lock was released")
			t.Fatalf("the request waited for the publish lock of %s; a session that is private before the request starts "+
				"must be refused by the preflight, before any lock is taken", race.Hold)
			return nil
		}
	}

	start()
	waitForSessionBlockedBy(t, ctx, w.pool, blocker)
	if race.Action == selectionRaceCompete {
		w.compete(t, ctx, race, release)
	} else {
		w.perform(t, ctx, race, race.Action)
		release()
	}
	return await("after the publish lock was released")
}

// perform makes one change on a connection of its own, through the
// application's own statements where one exists, and checks it took effect so
// a race can never pass because its change silently did nothing.
func (w *selectionWriteWorld) perform(t *testing.T, ctx context.Context, race selectionWriteRace, action string) {
	t.Helper()
	id := w.transcripts[race.Hold]
	switch action {
	case selectionRaceMakePrivate:
		private := dbVisibilityPrivate
		if err := w.h.inTxAs(ctx, w.member, func(q Querier) error {
			_, err := applyMetadataPatch(ctx, q, id, metadataPatch{Visibility: &private})
			return err
		}); err != nil {
			t.Fatalf("make %s private: %v", race.Hold, err)
		}
		if got := w.visibility(t, ctx, race.Hold); got != dbVisibilityPrivate {
			t.Fatalf("%s is %s after it was made private", race.Hold, got)
		}
	case selectionRaceAppendApproved:
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
			VALUES ($1, $2, (SELECT COALESCE(MAX(event_num), 0) + 1 FROM transcript_share_attempts
			                 WHERE transcript_id = $1 AND group_id = $2), 'approved')
		`, id, w.group); err != nil {
			t.Fatalf("append an accepted attempt for %s: %v", race.Hold, err)
		}
	case selectionRaceWithdraw:
		if err := w.h.inTxAs(ctx, w.member, func(q Querier) error {
			return q.UnshareTranscript(ctx, sqlc.UnshareTranscriptParams{TranscriptID: id, GroupID: w.group})
		}); err != nil {
			t.Fatalf("withdraw %s: %v", race.Hold, err)
		}
		if got := w.latestStatus(t, ctx, race.Hold); got != string(schema.VillageShareStatusRetracted) {
			t.Fatalf("the latest attempt of %s is %s after it was withdrawn", race.Hold, got)
		}
	case selectionRaceReject:
		if err := w.h.inTxAs(ctx, w.moderator, func(q Querier) error {
			return q.UpdateShareStatus(ctx, sqlc.UpdateShareStatusParams{
				TranscriptID: id, GroupID: w.group, Status: string(schema.VillageShareStatusRejected), DecidedBy: w.moderator,
			})
		}); err != nil {
			t.Fatalf("reject %s: %v", race.Hold, err)
		}
		if got := w.latestStatus(t, ctx, race.Hold); got != string(schema.VillageShareStatusRejected) {
			t.Fatalf("the latest attempt of %s is %s after it was rejected", race.Hold, got)
		}
	case selectionRaceMove:
		tag, err := w.pool.Exec(ctx, "UPDATE transcripts SET project_hash = $2 WHERE id = $1", id, w.projectHash(race.Project))
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("move %s to project %s: %v (%d rows)", race.Hold, race.Project, err, tag.RowsAffected())
		}
	case selectionRaceSetAcceptance:
		tag, err := w.pool.Exec(ctx, "UPDATE groups SET acceptance_mode = $2 WHERE id = $1", w.group, race.Acceptance)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("make the collective %s: %v (%d rows)", race.Acceptance, err, tag.RowsAffected())
		}
	case selectionRaceRemoveMembership:
		tag, err := w.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id = $1 AND user_id = $2", w.group, w.member)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("remove the caller's membership: %v (%d rows)", err, tag.RowsAffected())
		}
	default:
		t.Fatalf("race action %q has no implementation", action)
	}
}

// compete is another writer that does not take the publish lock: it appends an
// accepted attempt for the held session and keeps its transaction open. Once
// the publish lock is released the request must be seen waiting on THIS
// writer, which is the row lock it takes before re-reading; only then does the
// writer commit.
func (w *selectionWriteWorld) compete(t *testing.T, ctx context.Context, race selectionWriteRace, releasePublishLock func()) {
	t.Helper()
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the competing writer's connection: %v", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the competing writer: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	var competitor int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&competitor); err != nil {
		t.Fatalf("read the competing writer's backend: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
		VALUES ($1, $2, (SELECT COALESCE(MAX(event_num), 0) + 1 FROM transcript_share_attempts
		                 WHERE transcript_id = $1 AND group_id = $2), 'approved')
	`, w.transcripts[race.Hold], w.group); err != nil {
		t.Fatalf("append the competing attempt for %s: %v", race.Hold, err)
	}
	releasePublishLock()
	waitForSessionBlockedBy(t, ctx, w.pool, competitor)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the competing attempt: %v", err)
	}
	committed = true
}

// refuseLastLedgerInsert makes the ledger refuse the attempt of the selected
// session with the highest id. Attempts are written in ascending id order, so
// every other session has been offered, and widened if it was private, before
// the failure. The trigger is named after the transcript so no other case can
// remove it.
func (w *selectionWriteWorld) refuseLastLedgerInsert(t *testing.T, ctx context.Context) {
	t.Helper()
	last := ""
	for _, key := range w.spec.Request.Select {
		if id := w.idText(t, key); id > last {
			last = id
		}
	}
	name := "selection_ledger_refusal_" + strings.ReplaceAll(last, "-", "")
	t.Cleanup(func() {
		if _, err := w.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+name+` ON transcript_share_attempts; DROP FUNCTION IF EXISTS `+name+`()`); err != nil {
			t.Errorf("remove the injected ledger refusal: %v", err)
		}
	})
	if _, err := w.pool.Exec(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected ledger refusal'; END $$`); err != nil {
		t.Fatalf("install the injected ledger refusal function: %v", err)
	}
	if _, err := w.pool.Exec(ctx, `CREATE TRIGGER `+name+` BEFORE INSERT OR UPDATE ON transcript_share_attempts FOR EACH ROW
		WHEN (NEW.transcript_id = '`+last+`'::uuid) EXECUTE FUNCTION `+name+`()`); err != nil {
		t.Fatalf("install the injected ledger refusal trigger: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The route
// ---------------------------------------------------------------------------

func TestBatchShareSelectionWrite(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	const secret = "disposable-selection-write-secret"

	for _, c := range loadSelectionWriteCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			membership := c.Membership
			if membership == "" {
				membership = "member"
			}
			world := newContributeWorld(t, ctx, pool, c.Name, c.Acceptance, membership == "member")
			defer world.cleanup(t, ctx)
			w := newSelectionWriteWorld(t, ctx, c, secret, world)
			defer func() {
				if w.otherGroup.Valid {
					if _, err := pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.otherGroup); err != nil {
						t.Errorf("cleanup the second collective: %v", err)
					}
				}
			}()
			if c.Inject == "refuse_last_ledger_insert" {
				w.refuseLastLedgerInsert(t, ctx)
			}

			before := w.snapshot(t, ctx)
			req := w.request(t)
			var rec *httptest.ResponseRecorder
			if c.Race != nil {
				rec = w.race(t, ctx, req)
			} else {
				rec = w.serve(req)
			}
			if rec.Code != c.Expect.Status {
				t.Fatalf("answered %d, want %d (%s)\nbody: %s", rec.Code, c.Expect.Status, c.Why, rec.Body.String())
			}
			if rec.Code == http.StatusOK {
				w.assertReceipt(t, rec)
			} else {
				w.assertRefusal(t, rec)
			}
			w.assertPersisted(t, ctx)

			// A refusal writes nothing at all: not an attempt in any collective,
			// not a visibility change, not a governance event. A race changes the
			// world on purpose, so its expected state is asserted above instead.
			if c.Race == nil && rec.Code != http.StatusOK {
				if after := w.snapshot(t, ctx); after.String() != before.String() {
					t.Errorf("a refused request changed the database:\nbefore %s\nafter  %s", before, after)
				}
			}
		})
	}
}
