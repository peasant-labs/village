//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/attempt-emission.yaml
var attemptEmissionYAML []byte

type attemptEmissionCase struct {
	Name       string `yaml:"name"`
	Acceptance string `yaml:"acceptance"`
	Action     string `yaml:"action"`
	Mutation   string `yaml:"mutation"`
	Status     string `yaml:"status"`
	Ordinal    int32  `yaml:"ordinal"`
	Decided    bool   `yaml:"decided"`
}

func loadAttemptEmissionFixtures(t *testing.T) []attemptEmissionCase {
	t.Helper()
	cases, err := decodeFixtureRows[attemptEmissionCase](attemptEmissionYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"attempt-pending-approved": false, "attempt-pending-rejected": false, "attempt-pending-retracted": false, "attempt-pending-revoked": false,
		"attempt-late-pending-after-terminal": false, "attempt-terminal-exact-replay": false, "attempt-recorded-at-conflict": false,
		"attempt-terminal-conflict": false, "attempt-terminal-decision-conflict": false, "attempt-approved-withdrawal-new-ordinal": false,
		"attempt-initial-auto-approved-null-decision": false, "attempt-moderator-deletion-wire-unchanged": false,
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("missing/repeated attempt fixture name %q", c.Name)
		}
		seen[c.Name] = true
		if _, ok := required[c.Name]; ok {
			required[c.Name] = true
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing required attempt fixture %q", name)
		}
	}
	return cases
}

// This proves the server SQL/wire side of the shared reconciliation relation:
// pending evolves in place; terminal rewrites are refused by shipped guards;
// approved withdrawal appends; moderator anonymization changes no wire evidence.
// Client stale-observation reconciliation is owned and tested in Schema, not
// reimplemented here. These are mounted production handlers on real PostgreSQL.
func TestContributionAttemptEmission(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)
	for _, c := range loadAttemptEmissionFixtures(t) {
		t.Run(c.Name, func(t *testing.T) {
			world := newShareWorld(t, ctx, pool, c.Name, c.Acceptance)
			defer world.cleanup(t, ctx)
			router := chi.NewRouter()
			router.Post("/transcripts/{id}/share", h.ShareTranscript)
			router.Patch("/groups/{id}/shares/{transcriptID}", h.ReviewShare)
			router.Delete("/transcripts/{id}/share/{groupID}", h.UnshareTranscript)
			router.Delete("/groups/{id}/transcripts/{transcriptID}", h.RemoveGroupTranscript)
			router.Get("/users/me/collectives/{groupId}/transcripts/{transcriptId}/events", h.ListShareEventHistory)
			tid, gid := uuid.UUID(world.transcript.Bytes).String(), uuid.UUID(world.group.Bytes).String()
			call := func(actor uuid.UUID, method, path string, body []byte) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, bytes.NewReader(body))
				r = r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: actor}))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, r)
				if rec.Code != 200 {
					t.Fatalf("%s %s returned %d: %s", method, path, rec.Code, rec.Body.String())
				}
				return rec
			}
			history := func() ([]ShareEventHistoryEntry, []byte) {
				rec := call(uuid.UUID(world.owner.Bytes), http.MethodGet, "/users/me/collectives/"+gid+"/transcripts/"+tid+"/events", nil)
				var result []ShareEventHistoryEntry
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				for _, event := range result {
					if err := event.Validate(); err != nil {
						t.Fatalf("emitted attempt violates shared canonical validation: %v", err)
					}
				}
				return result, append([]byte(nil), rec.Body.Bytes()...)
			}
			body, _ := json.Marshal(map[string][]string{"group_ids": {gid}})
			call(uuid.UUID(world.owner.Bytes), http.MethodPost, "/transcripts/"+tid+"/share", body)
			before, _ := history()
			moderator := world.moderator
			if c.Action == "delete-moderator" {
				moderator = shareInsertUser(t, ctx, pool, "deciding-"+world.ownerName)
				shareAddMember(t, ctx, pool, world.group, moderator, "owner")
				defer func() {
					if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id=$1", moderator); err != nil {
						t.Error(err)
					}
				}()
			}
			switch c.Action {
			case "none":
			case "approve", "reject", "delete-moderator":
				status := "approved"
				if c.Action == "reject" {
					status = "rejected"
				}
				body, _ := json.Marshal(map[string]string{"status": status})
				call(uuid.UUID(moderator.Bytes), http.MethodPatch, "/groups/"+gid+"/shares/"+tid, body)
			case "unshare":
				call(uuid.UUID(world.owner.Bytes), http.MethodDelete, "/transcripts/"+tid+"/share/"+gid, nil)
			case "remove":
				call(uuid.UUID(moderator.Bytes), http.MethodDelete, "/groups/"+gid+"/transcripts/"+tid, nil)
			default:
				t.Fatalf("unknown action %q", c.Action)
			}
			after, wire := history()
			if len(after) != int(c.Ordinal) || after[len(after)-1].EventNum != c.Ordinal || string(after[len(after)-1].Status) != c.Status || (after[len(after)-1].DecidedAt != nil) != c.Decided {
				t.Fatalf("unexpected attempt emission: %s", wire)
			}
			if after[0].RecordedAt != before[0].RecordedAt {
				t.Fatalf("pending decision rewrote attempt creation time: %s", wire)
			}
			if c.Mutation != "" {
				// Mutation values are a closed test operation menu, never SQL input.
				statement := ""
				switch c.Mutation {
				case "pending":
					statement = "UPDATE transcript_share_attempts SET status='pending',decided_at=NULL,decided_by=NULL WHERE transcript_id=$1 AND group_id=$2"
				case "recorded_at":
					statement = "UPDATE transcript_share_attempts SET recorded_at=recorded_at+interval '1 second' WHERE transcript_id=$1 AND group_id=$2"
				case "rejected":
					statement = "UPDATE transcript_share_attempts SET status='rejected' WHERE transcript_id=$1 AND group_id=$2"
				case "decided_at":
					statement = "UPDATE transcript_share_attempts SET decided_at=decided_at+interval '1 second' WHERE transcript_id=$1 AND group_id=$2"
				default:
					t.Fatalf("unknown mutation %q", c.Mutation)
				}
				if _, err := pool.Exec(ctx, statement, world.transcript, world.group); err == nil {
					t.Fatal("terminal history rewrite was accepted")
				}
			}
			if c.Action == "delete-moderator" {
				if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id=$1", moderator); err != nil {
					t.Fatal(err)
				}
			}
			_, replayed := history()
			if !bytes.Equal(wire, replayed) {
				t.Fatalf("terminal replay/anonymization changed wire: before=%s after=%s", wire, replayed)
			}
		})
	}
}
