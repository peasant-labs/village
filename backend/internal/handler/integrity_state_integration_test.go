//go:build integration

package handler

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/integrity-sticky.yaml
var integrityStickyYAML []byte

type integrityStickyStep struct {
	Action     string `yaml:"action"`
	HashRef    string `yaml:"hash_ref"`
	Publisher  string `yaml:"publisher"`
	Session    string `yaml:"session"`
	Visibility string `yaml:"visibility"`
	Version    string `yaml:"version"`
	Verdict    string `yaml:"verdict"`
	Turns      []int  `yaml:"turns"`
	DaysAgo    int    `yaml:"days_ago"`
}

type integrityStickyWant struct {
	OpenFlag               *bool `yaml:"open_flag"`
	Tombstone              bool  `yaml:"tombstone"`
	TombstoneFlaggedAtStep *int  `yaml:"tombstone_flagged_at_step"`
	TombstoneFlaggedAtSeed bool  `yaml:"tombstone_flagged_at_seed"`
	LatestTurns            []int `yaml:"latest_turns"`
}

type integrityStickyCase struct {
	Name  string                `yaml:"name"`
	Why   string                `yaml:"why"`
	Steps []integrityStickyStep `yaml:"steps"`
	Want  integrityStickyWant   `yaml:"want"`
}

var requiredIntegrityStickyCases = []string{
	"public_flag_is_open",
	"shared_flag_is_open",
	"flag_written_after_share_withdrawn_mid_check",
	"flag_written_after_made_private_mid_check",
	"republish_mid_check_keeps_the_flag",
	"withdrawn_share_keeps_the_flag",
	"rejected_share_keeps_the_flag",
	"republish_keeps_the_flag",
	"private_then_public_keeps_the_flag",
	"later_version_checked_empty_on_same_content_voids",
	"version_order_is_numeric",
	"later_version_checked_empty_on_other_content_does_not_void",
	"earlier_version_checked_empty_does_not_void",
	"later_version_unchecked_does_not_void",
	"later_unchecked_keeps_turns",
	"clean_and_unchecked_answers_are_not_flags",
	"delete_of_flagged_writes_tombstone",
	"delete_of_voided_writes_no_tombstone",
	"delete_of_unflagged_writes_no_tombstone",
	"republish_after_delete_same_session_inherits",
	"other_session_does_not_inherit",
	"other_publisher_does_not_inherit",
	"inherited_flag_survives_a_checked_empty_result",
	"second_delete_keeps_earliest_flag_date",
	"record_over_an_earlier_record_keeps_its_date",
}

func loadIntegrityStickyCases(t *testing.T) []integrityStickyCase {
	t.Helper()
	cases, err := decodeFixtureRows[integrityStickyCase](integrityStickyYAML)
	if err != nil {
		t.Fatalf("load the integrity sticky corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || present[c.Name] || len(c.Steps) == 0 {
			t.Fatalf("integrity sticky case %+v must carry a unique name, a reason and steps", c)
		}
		present[c.Name] = true
	}
	for _, name := range requiredIntegrityStickyCases {
		if !present[name] {
			t.Fatalf("the integrity sticky corpus no longer contains %q; restore it", name)
		}
	}
	return cases
}

func TestIntegrityStickyHistory(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	objects := publishEvidenceObjects(t)
	blobs := publishEvidenceStore(t, objects)

	for ordinal, c := range loadIntegrityStickyCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			queries := sqlc.New(pool)
			h := &Handler{cfg: minimalConfig(), pool: pool, queries: queries, blobs: blobs, titles: titles}

			owners := map[string]pgtype.UUID{}
			var ownerList []pgtype.UUID
			var seen []pgtype.UUID
			defer func() {
				cleanupOwners(t, context.Background(), pool, ownerList...)
				purgeAuditRows(t, context.Background(), pool, seen)
			}()
			owner := func(publisher string) pgtype.UUID {
				if publisher == "" {
					publisher = "a"
				}
				if id, ok := owners[publisher]; ok {
					return id
				}
				id := pullInsertUser(t, ctx, pool, int64(995500+ordinal*10+len(owners)), "sticky-"+publisher+"-"+uuid.NewString()[:8])
				owners[publisher] = id
				ownerList = append(ownerList, id)
				return id
			}
			sessions := map[string]string{}
			session := func(name string) string {
				if name == "" {
					name = "s1"
				}
				if id, ok := sessions[name]; ok {
					return id
				}
				sessions[name] = uuid.NewString()
				return sessions[name]
			}
			primaryOwner := owner("a")
			primarySession := session("s1")
			group := pullInsertGroup(t, ctx, pool, primaryOwner, "sticky-"+uuid.NewString()[:8])

			hashes := map[string]string{}
			checkedAt := map[int]time.Time{}
			var current pgtype.UUID
			var currentOwner pgtype.UUID
			var currentHash string
			deleted := false
			var seededAt time.Time
			shareEvent := 0

			publish := func(index int, step integrityStickyStep) {
				o := owner(step.Publisher)
				localID := session(step.Session)
				content := []byte(fmt.Sprintf(`[{"role":"user","content":"sticky %s content %s"}]`, c.Name, step.HashRef))
				w := publishEvidencePost(t, h, &AuthUser{ID: uuidFromPg(o), Username: "sticky"}, publishEvidenceRequest(localID, content, ""), content)
				if w.Code >= 300 {
					t.Fatalf("step %d %s: status %d: %s", index, step.Action, w.Code, w.Body.String())
				}
				id, err := queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: o, LocalID: localID})
				if err != nil {
					t.Fatalf("step %d: load the published transcript: %v", index, err)
				}
				row, err := queries.GetTranscriptByID(ctx, id)
				if err != nil {
					t.Fatalf("step %d: load the published row: %v", index, err)
				}
				switch step.Action {
				case "publish":
					if slices.Contains(seen, id) {
						t.Fatalf("step %d: publish did not create a new transcript", index)
					}
					seen = append(seen, id)
				case "republish":
					if id != current {
						t.Fatalf("step %d: republish replaced a different transcript", index)
					}
				}
				if step.Action == "publish" {
					shareEvent = 0
				}
				current, currentOwner, currentHash, deleted = id, o, row.ContentHash.String, false
				hashes[step.HashRef] = row.ContentHash.String
			}

			for index, step := range c.Steps {
				switch step.Action {
				case "publish", "republish":
					publish(index, step)
				case "patch_visibility":
					execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility = $2 WHERE id = $1", current, step.Visibility)
				case "share":
					shareEvent++
					if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, $3, 'pending')`, current, group, shareEvent); err != nil {
						t.Fatalf("step %d: share: %v", index, err)
					}
				case "withdraw_share", "reject_share":
					status := "retracted"
					if step.Action == "reject_share" {
						status = "rejected"
					}
					tag, err := pool.Exec(ctx, `UPDATE transcript_share_attempts SET status = $3, decided_at = now() WHERE transcript_id = $1 AND group_id = $2 AND status = 'pending'`, current, group, status)
					if err != nil || tag.RowsAffected() != 1 {
						t.Fatalf("step %d: %s: rows=%d err=%v", index, step.Action, tag.RowsAffected(), err)
					}
				case "seed_tombstone":
					if step.DaysAgo <= 0 {
						t.Fatalf("step %d: seed_tombstone needs a positive days_ago", index)
					}
					if err := pool.QueryRow(ctx, `INSERT INTO integrity_flag_tombstones (owner_id, local_id, flagged_at) VALUES ($1, $2, now() - make_interval(days => $3)) RETURNING flagged_at`, owner(step.Publisher), session(step.Session), step.DaysAgo).Scan(&seededAt); err != nil {
						t.Fatalf("step %d: seed the post-deletion record: %v", index, err)
					}
				case "delete":
					id := uuidFromPg(current).String()
					r := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+id, nil)
					r = withChiURLParam(r, "id", id)
					r = r.WithContext(withUserID(r.Context(), uuidFromPg(currentOwner)))
					w := httptest.NewRecorder()
					h.DeleteTranscript(w, r)
					if w.Code != http.StatusOK {
						t.Fatalf("step %d: delete status %d: %s", index, w.Code, w.Body.String())
					}
					deleted = true
				case "result":
					hash, ok := hashes[step.HashRef]
					if !ok {
						t.Fatalf("step %d: result names unpublished content %q", index, step.HashRef)
					}
					verdict := schema.ScryVerdictChecked
					if step.Verdict != "" {
						verdict = schema.ScryVerdict(step.Verdict)
					}
					turns := step.Turns
					if turns == nil {
						turns = []int{}
					}
					resp := schema.ScryCheckResponse{ScryVersion: step.Version, Verdict: verdict, FlaggedTurns: turns}
					if err := recordIntegrityResult(ctx, queries, current, hash, resp); err != nil {
						t.Fatalf("step %d: record result: %v", index, err)
					}
					var at time.Time
					if err := pool.QueryRow(ctx, `SELECT checked_at FROM transcript_integrity_results WHERE transcript_id = $1 AND content_hash = $2 AND scry_version = $3`, current, hash, step.Version).Scan(&at); err != nil {
						t.Fatalf("step %d: read the stored result: %v", index, err)
					}
					checkedAt[index] = at
				default:
					t.Fatalf("unknown step action %q", step.Action)
				}
			}

			if c.Want.OpenFlag != nil {
				if deleted {
					t.Fatal("open_flag is wanted but the last transcript was deleted")
				}
				open, err := queries.HasOpenIntegrityFlag(ctx, current)
				if err != nil {
					t.Fatalf("HasOpenIntegrityFlag: %v", err)
				}
				if open != *c.Want.OpenFlag {
					t.Fatalf("open flag = %v, want %v", open, *c.Want.OpenFlag)
				}
			}

			var tombstoneAt time.Time
			err := pool.QueryRow(ctx, `SELECT flagged_at FROM integrity_flag_tombstones WHERE owner_id = $1 AND local_id = $2`, primaryOwner, primarySession).Scan(&tombstoneAt)
			hasTombstone := err == nil
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("read the post-deletion record: %v", err)
			}
			if hasTombstone != c.Want.Tombstone {
				t.Fatalf("post-deletion record present = %v, want %v", hasTombstone, c.Want.Tombstone)
			}
			if step := c.Want.TombstoneFlaggedAtStep; step != nil {
				want, ok := checkedAt[*step]
				if !ok {
					t.Fatalf("tombstone_flagged_at_step %d is not a result step", *step)
				}
				if !tombstoneAt.Equal(want) {
					t.Fatalf("post-deletion record flagged_at = %v, want the step %d result's %v", tombstoneAt, *step, want)
				}
			}
			if c.Want.TombstoneFlaggedAtSeed {
				if seededAt.IsZero() {
					t.Fatal("tombstone_flagged_at_seed is wanted but no step seeded a record")
				}
				if !tombstoneAt.Equal(seededAt) {
					t.Fatalf("post-deletion record flagged_at = %v, want the earlier seeded %v", tombstoneAt, seededAt)
				}
			}
			var others int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM integrity_flag_tombstones WHERE owner_id = ANY($1) AND NOT (owner_id = $2 AND local_id = $3)`, ownerList, primaryOwner, primarySession).Scan(&others); err != nil {
				t.Fatalf("count the other post-deletion records: %v", err)
			}
			if others != 0 {
				t.Fatalf("%d post-deletion records name another publisher or session", others)
			}

			if !deleted {
				var inheritedAt time.Time
				err := pool.QueryRow(ctx, `SELECT flagged_at FROM transcript_integrity_inherited_flags WHERE transcript_id = $1`, current).Scan(&inheritedAt)
				switch {
				case err == nil:
					if !hasTombstone || !inheritedAt.Equal(tombstoneAt) {
						t.Fatalf("inherited flag dated %v does not carry the post-deletion record's date %v", inheritedAt, tombstoneAt)
					}
				case !errors.Is(err, pgx.ErrNoRows):
					t.Fatalf("read the inherited flag: %v", err)
				}
			}

			if c.Want.LatestTurns != nil {
				latest, err := queries.ServedIntegrityForContent(ctx, sqlc.ServedIntegrityForContentParams{TranscriptID: current, ContentHash: currentHash})
				if err != nil {
					t.Fatalf("ServedIntegrityForContent: %v", err)
				}
				got := make([]int, len(latest.FlaggedTurns))
				for i, turn := range latest.FlaggedTurns {
					got[i] = int(turn)
				}
				if !slices.Equal(got, c.Want.LatestTurns) {
					t.Fatalf("latest checked turns for the current content: %v, want %v", got, c.Want.LatestTurns)
				}
			}
		})
	}
}
