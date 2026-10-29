//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/scry"
)

//go:embed testdata/integrity-triggers.yaml
var integrityTriggersYAML []byte

type integrityTriggerStep struct {
	Do                string `yaml:"do"`
	Ref               string `yaml:"ref"`
	T                 string `yaml:"t"`
	Shape             string `yaml:"shape"`
	Turns             []int  `yaml:"turns"`
	Collective        string `yaml:"collective"`
	As                string `yaml:"as"`
	Version           string `yaml:"version"`
	Status            int    `yaml:"status"`
	Count             *int   `yaml:"count"`
	On                bool   `yaml:"on"`
	WithdrawAfterList bool   `yaml:"withdraw_after_list"`
}

type integrityLedgerWant struct {
	Ref      string `yaml:"ref"`
	Version  string `yaml:"version"`
	Status   string `yaml:"status"`
	Error    string `yaml:"error"`
	Attempts int    `yaml:"attempts"`
	DueIn    *int   `yaml:"due_in"`
}

type integrityResultWant struct {
	Ref     string `yaml:"ref"`
	Version string `yaml:"version"`
	Turns   []int  `yaml:"turns"`
}

type integrityTriggerWant struct {
	Sent    []string              `yaml:"sent"`
	Ordered bool                  `yaml:"ordered"`
	Absent  []string              `yaml:"absent"`
	Ledger  []integrityLedgerWant `yaml:"ledger"`
	Results []integrityResultWant `yaml:"results"`
	Nudges  *int                  `yaml:"nudges"`
}

type integrityTriggerCase struct {
	Name       string                 `yaml:"name"`
	Why        string                 `yaml:"why"`
	Unlaunched bool                   `yaml:"unlaunched"`
	Steps      []integrityTriggerStep `yaml:"steps"`
	Want       integrityTriggerWant   `yaml:"want"`
}

var requiredIntegrityTriggerCases = []string{
	"private_unshared_sends_nothing",
	"shared_publish_is_checked",
	"pending_share_is_checked",
	"approval_of_checked_hash_sends_nothing",
	"rejected_share_edit_is_checked",
	"rejected_secret_burns_no_attempts",
	"store_failure_backs_off_and_caps",
	"database_failure_before_the_call_backs_off",
	"claim_refuses_past_the_cap",
	"withdrawn_share_stops_checks",
	"reshare_of_checked_hash_sends_nothing",
	"public_publish_is_checked_once",
	"second_tick_sends_nothing",
	"private_to_public_is_checked",
	"public_to_private_unshared_sends_nothing",
	"public_republish_checks_the_new_hash",
	"shared_republish_checks_the_new_hash",
	"fingerprint_replay_sends_nothing",
	"legacy_raw_jsonl_is_skipped_without_a_call",
	"legacy_rewrite_is_skipped_without_a_call",
	"rewritten_hash_is_a_candidate",
	"sent_detail_is_the_served_detail",
	"version_change_rechecks_public_and_shared",
	"version_sweep_after_new_content",
	"scry_down_ends_the_tick_without_writes",
	"failure_backs_off",
	"six_failures_is_failed",
	"too_large_is_terminal",
	"unreadable_content_is_read_failed",
	"left_set_between_list_and_read_sends_nothing",
	"project_contribution_is_checked",
	"existing_public_rows_are_checked_at_launch",
	"existing_shared_rows_are_checked_at_launch",
	"request_never_includes_unshared_private_content",
}

func loadIntegrityTriggerCases(t *testing.T) []integrityTriggerCase {
	t.Helper()
	cases, err := decodeFixtureRows[integrityTriggerCase](integrityTriggersYAML)
	if err != nil {
		t.Fatalf("load the integrity trigger corpus: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || len(c.Steps) == 0 {
			t.Fatalf("integrity trigger case %+v must carry a name, a reason and steps", c)
		}
		if _, dup := present[c.Name]; dup {
			t.Fatalf("integrity trigger case %q is repeated", c.Name)
		}
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "integrity-triggers", present, requiredIntegrityTriggerCases)
	return cases
}

// integrityTriggerContent is the stored bytes for one content ref, carrying
// text unique to the case and ref so a request can be searched for it.
func integrityTriggerContent(t *testing.T, text, shape string) []byte {
	t.Helper()
	switch shape {
	case "", "canonical":
		payload, _, err := defaultContentMigrator.Migrate(context.Background(), legacyBarePayloadJSON("claude"))
		if err != nil {
			t.Fatal(err)
		}
		payload.Turns[0].Content = text
		encoded, err := encodeCanonicalTranscript(payload)
		if err != nil {
			t.Fatalf("encode canonical content: %v", err)
		}
		return encoded
	case "bare":
		return bytes.Replace(legacyBarePayloadJSON("claude"), []byte(`"content": "hi"`), []byte(`"content": "`+text+`"`), 1)
	case "raw_jsonl":
		return []byte(`[{"role":"user","content":"` + text + `"},{"role":"assistant","content":"yo"}]`)
	default:
		t.Fatalf("unknown shape %q", shape)
		return nil
	}
}

type integrityTriggerKey struct {
	transcript pgtype.UUID
	detail     string
}

func TestIntegrityTriggers(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	objects := publishEvidenceObjects(t)
	blobs := publishEvidenceStore(t, objects)

	for _, c := range loadIntegrityTriggerCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			queries := sqlc.New(pool)

			// Nothing another test left behind may be in the checked set: a
			// pass would check it and crowd the case's own transcripts.
			if leftover, err := queries.ListIntegrityCandidates(ctx, sqlc.ListIntegrityCandidatesParams{ScryVersion: "999.0.0", Batch: 1}); err != nil || len(leftover) != 0 {
				t.Fatalf("a transcript outside this case is in the checked set (err %v); clean the test database", err)
			}

			fake := scry.NewFake(t, "1.0.0")
			h := NewWithTitlePipeline(minimalConfig(), pool, blobs, titles)
			faults := &integrityFaultQuerier{Querier: h.queries}
			h.queries = faults
			checker := &countingChecker{inner: scry.NewHTTPClient(fake.Settings())}
			service := newIntegrityServiceFor(checker)
			service.callSpacing = 0
			if !c.Unlaunched {
				h.integrity = service
			}
			nudges := 0
			h.onIntegrityNudge = func() { nudges++ }

			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			owner := shareInsertUser(t, ctx, pool, "integrity-owner-"+suffix)
			moderator := shareInsertUser(t, ctx, pool, "integrity-mod-"+suffix)
			var seen []pgtype.UUID
			groups := map[string]pgtype.UUID{}
			defer func() {
				for _, g := range groups {
					if _, err := pool.Exec(context.Background(), "DELETE FROM groups WHERE id = $1", g); err != nil {
						t.Errorf("cleanup collective: %v", err)
					}
				}
				cleanupOwners(t, context.Background(), pool, owner, moderator)
				purgeAuditRows(t, context.Background(), pool, seen)
			}()
			for _, mode := range []string{"open", "curated"} {
				var g pgtype.UUID
				if err := pool.QueryRow(ctx, `INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, $3) RETURNING id`, "integrity-"+mode+"-"+suffix, moderator, mode).Scan(&g); err != nil {
					t.Fatalf("create the %s collective: %v", mode, err)
				}
				shareAddMember(t, ctx, pool, g, moderator, "owner")
				shareAddMember(t, ctx, pool, g, owner, "member")
				groups[mode] = g
			}

			router := chi.NewRouter()
			router.Patch("/transcripts/{id}", h.UpdateTranscript)
			router.Post("/transcripts/{id}/share", h.ShareTranscript)
			router.Delete("/transcripts/{id}/share/{groupID}", h.UnshareTranscript)
			router.Patch("/groups/{id}/shares/{transcriptID}", h.ReviewShare)
			router.Post("/groups/{id}/shares", h.BatchShareProject)
			router.Get("/transcripts/{id}/content", h.GetTranscriptContent)
			call := func(actor pgtype.UUID, method, path string, body any) *httptest.ResponseRecorder {
				t.Helper()
				var reader *bytes.Reader
				if body == nil {
					reader = bytes.NewReader(nil)
				} else {
					encoded, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					reader = bytes.NewReader(encoded)
				}
				r := httptest.NewRequest(method, path, reader)
				r.Header.Set("Content-Type", "application/json")
				r = r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuidFromPg(actor)}))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s %s: status %d: %s", method, path, w.Code, w.Body.String())
				}
				return w
			}

			transcripts := map[string]pgtype.UUID{}
			localIDs := map[string]string{}
			refHash := map[string]string{}
			refDetail := map[string]string{}
			refTranscript := map[string]pgtype.UUID{}
			refText := map[string]string{}
			answers := map[string][]int{}
			version := "1.0.0"
			answer := func(detail string, turns []int) {
				if turns == nil {
					turns = []int{}
				}
				answers[detail] = turns
				fake.Answer(detail, schema.ScryCheckResponse{ScryVersion: version, Verdict: schema.ScryVerdictChecked, FlaggedTurns: turns})
			}
			name := func(step integrityTriggerStep) string {
				if step.T == "" {
					return "t1"
				}
				return step.T
			}
			current := func(step integrityTriggerStep) pgtype.UUID {
				id, ok := transcripts[name(step)]
				if !ok {
					t.Fatalf("step %s names unpublished transcript %s", step.Do, name(step))
				}
				return id
			}
			tick := func() {
				t.Helper()
				if err := h.integrityTick(ctx); err != nil {
					t.Fatalf("integrity pass: %v", err)
				}
			}

			for index, step := range c.Steps {
				switch step.Do {
				case "publish":
					tn := name(step)
					if _, ok := localIDs[tn]; !ok {
						localIDs[tn] = uuid.NewString()
					}
					text := fmt.Sprintf("integrity %s %s sentinel", c.Name, step.Ref)
					content := integrityTriggerContent(t, text, step.Shape)
					w := publishEvidencePost(t, h, &AuthUser{ID: uuidFromPg(owner), Username: "integrity"}, publishEvidenceRequest(localIDs[tn], content, ""), content)
					if w.Code >= 300 {
						t.Fatalf("step %d publish: status %d: %s", index, w.Code, w.Body.String())
					}
					id, err := queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: owner, LocalID: localIDs[tn]})
					if err != nil {
						t.Fatalf("step %d: load the published transcript: %v", index, err)
					}
					row, err := queries.GetTranscriptByID(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Contains(seen, id) {
						seen = append(seen, id)
					}
					transcripts[tn] = id
					refHash[step.Ref] = row.ContentHash.String
					refTranscript[step.Ref] = id
					refText[step.Ref] = text
					if step.Shape != "raw_jsonl" {
						payload, _, err := defaultContentMigrator.Migrate(ctx, content)
						if err != nil {
							t.Fatal(err)
						}
						refDetail[step.Ref] = scry.HashOf(*payload)
						answer(refDetail[step.Ref], step.Turns)
					}
				case "make_public", "make_private":
					visibility := "public"
					if step.Do == "make_private" {
						visibility = "private"
					}
					call(owner, http.MethodPatch, "/transcripts/"+uuidFromPg(current(step)).String(), map[string]string{"visibility": visibility})
				case "share":
					call(owner, http.MethodPost, "/transcripts/"+uuidFromPg(current(step)).String()+"/share", map[string][]string{"group_ids": {uuidFromPg(groups[step.Collective]).String()}})
				case "withdraw":
					call(owner, http.MethodDelete, "/transcripts/"+uuidFromPg(current(step)).String()+"/share/"+uuidFromPg(groups[step.Collective]).String(), nil)
				case "approve", "reject":
					status := "approved"
					if step.Do == "reject" {
						status = "rejected"
					}
					call(moderator, http.MethodPatch, "/groups/"+uuidFromPg(groups["curated"]).String()+"/shares/"+uuidFromPg(current(step)).String(), map[string]string{"status": status})
				case "contribute":
					call(owner, http.MethodPost, "/groups/"+uuidFromPg(groups["open"]).String()+"/shares", map[string]any{
						"project_hash": testProjectHash, "transcript_ids": []string{uuidFromPg(current(step)).String()}, "visibility_confirmed": true,
					})
				case "read_content":
					id := current(step)
					w := call(owner, http.MethodGet, "/transcripts/"+uuidFromPg(id).String()+"/content", nil)
					if step.As != "" {
						installed := w.Header().Get(contentHashHeader)
						row, err := queries.GetTranscriptByID(ctx, id)
						if err != nil {
							t.Fatal(err)
						}
						if installed == "" || installed != row.ContentHash.String || slices.Contains(integrityHashes(refHash), installed) {
							t.Fatalf("step %d: the read installed no new content (header %q, row %q)", index, installed, row.ContentHash.String)
						}
						refHash[step.As] = installed
						refTranscript[step.As] = id
						var envelope schema.TranscriptContent
						if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.SessionDetail == nil {
							t.Fatalf("step %d: decode the served envelope: %v", index, err)
						}
						refDetail[step.As] = scry.HashOf(*envelope.SessionDetail)
						answer(refDetail[step.As], nil)
					}
				case "set_version":
					version = step.Version
					fake.SetVersion(version)
					for detail, turns := range answers {
						answer(detail, turns)
					}
				case "fail":
					fake.Fail(step.Status)
				case "fail_check":
					fake.FailCheck(step.Status)
				case "make_due":
					if _, err := pool.Exec(ctx, `UPDATE transcript_integrity_attempts SET next_attempt_at = now() - interval '1 second' WHERE transcript_id = $1`, current(step)); err != nil {
						t.Fatal(err)
					}
				case "delete_object":
					row, err := queries.GetTranscriptByID(ctx, current(step))
					if err != nil {
						t.Fatal(err)
					}
					if err := objects.Delete(ctx, mustDescriptor(t, row).ObjectKey()); err != nil {
						t.Fatalf("delete the stored object: %v", err)
					}
				case "record_legacy_rewrite":
					row, err := queries.GetTranscriptByID(ctx, current(step))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `INSERT INTO transcript_content_rewrites (transcript_id, replaced_content_hash, installed_content_hash, replaced_was_legacy) VALUES ($1, $2, $3, true)`, row.ID, strings.Repeat("0", 64), row.ContentHash.String); err != nil {
						t.Fatalf("record the legacy rewrite: %v", err)
					}
				case "expect_sent":
					if step.Count == nil {
						t.Fatalf("step %d: expect_sent needs a count", index)
					}
					if got := len(fake.Requests()); got != *step.Count {
						t.Fatalf("step %d: the service has received %d checks, want %d (%s)", index, got, *step.Count, c.Why)
					}
				case "wrong_secret":
					checker.inner = scry.NewHTTPClient(scry.Settings{URL: fake.URL(), SharedSecret: "refused-secret-0123456789abcdef0123456789"})
				case "right_secret":
					checker.inner = scry.NewHTTPClient(fake.Settings())
				case "expect_calls":
					if step.Count == nil {
						t.Fatalf("step %d: expect_calls needs a count", index)
					}
					if checker.checks != *step.Count {
						t.Fatalf("step %d: the worker made %d check calls, want %d (%s)", index, checker.checks, *step.Count, c.Why)
					}
				case "fail_store":
					faults.failStore = step.On
				case "fail_lineage":
					faults.failLineage = step.On
				case "seed_attempts":
					row, err := queries.GetTranscriptByID(ctx, current(step))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `INSERT INTO transcript_integrity_attempts (transcript_id, content_hash, scry_version, status, attempts, next_attempt_at) VALUES ($1, $2, '1.0.0', 'pending', $3, now() - interval '1 second')`, row.ID, row.ContentHash.String, *step.Count); err != nil {
						t.Fatalf("seed the attempts: %v", err)
					}
				case "launch":
					h.integrity = service
				case "tick":
					if step.WithdrawAfterList {
						withdraw := step
						h.onIntegrityCandidates = func() {
							h.onIntegrityCandidates = nil
							call(owner, http.MethodDelete, "/transcripts/"+uuidFromPg(current(withdraw)).String()+"/share/"+uuidFromPg(groups[withdraw.Collective]).String(), nil)
						}
					}
					tick()
					h.onIntegrityCandidates = nil
				case "assert_served":
					id := current(step)
					w := call(owner, http.MethodGet, "/transcripts/"+uuidFromPg(id).String()+"/content", nil)
					var envelope schema.TranscriptContent
					if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.SessionDetail == nil {
						t.Fatalf("step %d: decode the served envelope: %v", index, err)
					}
					var last *schema.ScryCheckRequest
					for _, req := range fake.Requests() {
						if string(req.TranscriptID) == uuidFromPg(id).String() {
							req := req
							last = &req
						}
					}
					if last == nil {
						t.Fatalf("step %d: no check was sent for the transcript", index)
					}
					if scry.HashOf(*envelope.SessionDetail) != scry.HashOf(last.SessionDetail) {
						t.Fatalf("step %d: the check sent a session detail the content route does not serve", index)
					}
				default:
					t.Fatalf("unknown step %q", step.Do)
				}
			}

			// What the service received.
			var got []integrityTriggerKey
			requests := fake.Requests()
			for _, req := range requests {
				id, err := uuid.Parse(string(req.TranscriptID))
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, integrityTriggerKey{transcript: pgtype.UUID{Bytes: id, Valid: true}, detail: scry.HashOf(req.SessionDetail)})
			}
			var want []integrityTriggerKey
			for _, ref := range c.Want.Sent {
				detail, ok := refDetail[ref]
				if !ok {
					t.Fatalf("want.sent names %q, which has no sendable content", ref)
				}
				want = append(want, integrityTriggerKey{transcript: refTranscript[ref], detail: detail})
			}
			keyString := func(k integrityTriggerKey) string { return uuidFromPg(k.transcript).String() + "/" + k.detail }
			gotS, wantS := integrityKeys(got, keyString), integrityKeys(want, keyString)
			if !c.Want.Ordered {
				sort.Strings(gotS)
				sort.Strings(wantS)
			}
			if !slices.Equal(gotS, wantS) {
				t.Fatalf("the service received %d checks %v, want %v (%s)", len(gotS), gotS, wantS, c.Why)
			}
			if fake.Unanswered() != 0 {
				t.Fatalf("%d checks matched no answer: the session detail sent is not the one the case published", fake.Unanswered())
			}
			encoded, err := json.Marshal(requests)
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range c.Want.Absent {
				if bytes.Contains(encoded, []byte(refText[ref])) {
					t.Fatalf("a request carried the content of %q (%s)", ref, c.Why)
				}
			}

			refOf := func(id pgtype.UUID, hash string) string {
				for ref, h := range refHash {
					if h == hash && refTranscript[ref] == id {
						return ref
					}
				}
				return "?" + hash[:8]
			}
			orDefault := func(v string) string {
				if v == "" {
					return "1.0.0"
				}
				return v
			}

			// The ledger.
			rows, err := pool.Query(ctx, `SELECT transcript_id, content_hash, scry_version, status, COALESCE(last_error, ''), attempts, EXTRACT(EPOCH FROM next_attempt_at - now())::float8 FROM transcript_integrity_attempts WHERE transcript_id = ANY($1)`, seen)
			if err != nil {
				t.Fatal(err)
			}
			type ledgerRow struct {
				key   string
				dueIn float64
			}
			var ledger []ledgerRow
			for rows.Next() {
				var id pgtype.UUID
				var hash, v, status, lastErr string
				var attempts int
				var due float64
				if err := rows.Scan(&id, &hash, &v, &status, &lastErr, &attempts, &due); err != nil {
					t.Fatal(err)
				}
				ledger = append(ledger, ledgerRow{key: fmt.Sprintf("%s@%s %s/%s x%d", refOf(id, hash), v, status, lastErr, attempts), dueIn: due})
			}
			rows.Close()
			var gotLedger, wantLedger []string
			for _, row := range ledger {
				gotLedger = append(gotLedger, row.key)
			}
			for _, entry := range c.Want.Ledger {
				key := fmt.Sprintf("%s@%s %s/%s x%d", entry.Ref, orDefault(entry.Version), entry.Status, entry.Error, entry.Attempts)
				wantLedger = append(wantLedger, key)
				if entry.DueIn != nil {
					for _, row := range ledger {
						if row.key == key && math.Abs(row.dueIn-float64(*entry.DueIn)) > 15 {
							t.Fatalf("attempt %s is due in %.0fs, want about %ds (%s)", key, row.dueIn, *entry.DueIn, c.Why)
						}
					}
				}
			}
			sort.Strings(gotLedger)
			sort.Strings(wantLedger)
			if !slices.Equal(gotLedger, wantLedger) {
				t.Fatalf("ledger %v, want %v (%s)", gotLedger, wantLedger, c.Why)
			}

			// The stored results.
			rows, err = pool.Query(ctx, `SELECT transcript_id, content_hash, scry_version, flagged_turns FROM transcript_integrity_results WHERE transcript_id = ANY($1)`, seen)
			if err != nil {
				t.Fatal(err)
			}
			var gotResults, wantResults []string
			for rows.Next() {
				var id pgtype.UUID
				var hash, v string
				var turns []int32
				if err := rows.Scan(&id, &hash, &v, &turns); err != nil {
					t.Fatal(err)
				}
				gotResults = append(gotResults, fmt.Sprintf("%s@%s %v", refOf(id, hash), v, turns))
			}
			rows.Close()
			for _, entry := range c.Want.Results {
				turns := make([]int32, len(entry.Turns))
				for i, turn := range entry.Turns {
					turns[i] = int32(turn)
				}
				wantResults = append(wantResults, fmt.Sprintf("%s@%s %v", entry.Ref, orDefault(entry.Version), turns))
			}
			sort.Strings(gotResults)
			sort.Strings(wantResults)
			if !slices.Equal(gotResults, wantResults) {
				t.Fatalf("results %v, want %v (%s)", gotResults, wantResults, c.Why)
			}

			if c.Want.Nudges != nil && nudges != *c.Want.Nudges {
				t.Fatalf("integrity nudges = %d, want %d (%s)", nudges, *c.Want.Nudges, c.Why)
			}
		})
	}
}

func integrityKeys[T any](items []T, key func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, key(item))
	}
	return out
}

func integrityHashes(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// countingChecker counts check calls, refused ones included, and lets a case
// swap the client it calls through.
type countingChecker struct {
	inner  scry.Checker
	checks int
}

func (c *countingChecker) Health(ctx context.Context) (schema.ScryHealthResponse, error) {
	return c.inner.Health(ctx)
}

func (c *countingChecker) Check(ctx context.Context, req schema.ScryCheckRequest) (schema.ScryCheckResponse, error) {
	c.checks++
	return c.inner.Check(ctx, req)
}

// integrityFaultQuerier fails one database call on demand and passes every
// other through.
type integrityFaultQuerier struct {
	Querier
	failStore   bool
	failLineage bool
}

func (q *integrityFaultQuerier) InsertIntegrityResult(ctx context.Context, arg sqlc.InsertIntegrityResultParams) error {
	if q.failStore {
		return errors.New("injected result store failure")
	}
	return q.Querier.InsertIntegrityResult(ctx, arg)
}

func (q *integrityFaultQuerier) IsLegacyLineageContent(ctx context.Context, arg sqlc.IsLegacyLineageContentParams) (bool, error) {
	if q.failLineage {
		return false, errors.New("injected lineage read failure")
	}
	return q.Querier.IsLegacyLineageContent(ctx, arg)
}
