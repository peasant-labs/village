//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

//go:embed testdata/attachment-grants.yaml
var attachmentGrantsYAML []byte

type attachmentGrantCase struct {
	Name       string `yaml:"name"`
	Visibility string `yaml:"visibility"`
	Share      string `yaml:"share"`
	Member     string `yaml:"member"`
	Action     string `yaml:"action"`
	Before     string `yaml:"before"`
	After      string `yaml:"after"`
	DuringPost string `yaml:"during_post"`
}

func TestPromptChecksFollowActualTranscriptGrants_RealPostgres(t *testing.T) {
	cases, err := decodeFixtureRows[attachmentGrantCase](attachmentGrantsYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, duplicate := present[c.Name]; duplicate || c.Name == "" {
			t.Fatalf("empty or duplicate grant case %q", c.Name)
		}
		present[c.Name] = struct{}{}
		if !containsString([]string{"private", "shared"}, c.Visibility) || !containsString([]string{"", "pending", "approved"}, c.Share) || !containsString([]string{"owner", "member", "pending", "absent"}, c.Member) || !containsString([]string{"none", "approve", "batch-approve", "reject", "remove-transcript", "remove-member", "add-member", "promote-member", "join"}, c.Action) || !containsString([]string{"success", "neutral"}, c.Before) || !containsString([]string{"success", "neutral"}, c.After) {
			t.Fatalf("invalid grant fixture %q", c.Name)
		}
	}
	assertExactTitleFixtureNames(t, "attachment-grants", present, []string{
		"shared_without_an_accepted_grant_is_neutral", "a_pending_member_cannot_satisfy_the_check", "an_accepted_member_satisfies_the_check", "a_review_owner_can_read_a_private_submission", "approval_refreshes_the_check_without_a_visibility_change", "batch_approval_refreshes_the_check_without_a_visibility_change", "rejection_refreshes_the_unaccepted_check", "removing_the_contribution_revokes_the_check", "the_last_member_leaving_revokes_the_check", "shared_to_private_during_post_revokes_check_success", "adding_the_first_member_refreshes_the_check", "accepting_a_pending_member_refreshes_the_check", "an_open_join_refreshes_the_check",
	})
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAttachAudienceWorld(t, attachAudienceCase{Repository: "public", Transcript: "private"}, 997500+int64(i)*10)
			if _, err := w.pool.Exec(ctx, `INSERT INTO group_members (group_id,user_id,role) VALUES ($1,$2,'owner')`, w.groupID, pgtype.UUID{Bytes: w.owner.ID, Valid: true}); err != nil {
				t.Fatal(err)
			}
			execAsSystem(t, ctx, w.pool, `UPDATE transcripts SET visibility = $2 WHERE id = $1`, w.transcriptID, c.Visibility)
			if c.Member != "absent" {
				if _, err := w.pool.Exec(ctx, `UPDATE group_members SET role = $3 WHERE group_id = $1 AND user_id = $2`, w.groupID, pgtype.UUID{Bytes: w.member.ID, Valid: true}, c.Member); err != nil {
					t.Fatal(err)
				}
			}
			if c.Member == "absent" {
				if _, err := w.pool.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1 AND user_id=$2`, w.groupID, w.member.PgID()); err != nil {
					t.Fatal(err)
				}
			}
			if c.Share != "" {
				if _, err := w.pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id,group_id,event_num,status) VALUES ($1,$2,1,$3)`, w.transcriptID, w.groupID, c.Share); err != nil {
					t.Fatal(err)
				}
			}
			if c.DuringPost != "" {
				if c.DuringPost != "private" {
					t.Fatal("unsupported visibility drift fixture")
				}
				w.fake.mu.Lock()
				w.fake.beforeCommentWrite = func() {
					if postedNow(w.fake).conclusion != "success" {
						t.Error("the accepted shared grant never produced success before its revocation")
					}
					if rec := transcriptVisibilityPatch(t, w.h, w.owner, w.transcriptID, c.DuringPost); rec.Code != http.StatusOK {
						t.Errorf("narrow status %d: %s", rec.Code, rec.Body.String())
					}
				}
				w.fake.mu.Unlock()
			}
			attachmentConfirm(t, w.h, pgtype.UUID{Bytes: w.owner.ID, Valid: true}, "acme", w.repoName, w.number)
			before := postedNow(w.fake)
			if before.conclusion != c.Before {
				t.Fatalf("before conclusion %q, want %q", before.conclusion, c.Before)
			}
			if c.Action == "none" {
				return
			}
			router := chi.NewRouter()
			router.Patch("/groups/{id}/shares/{transcriptID}", w.h.ReviewShare)
			router.Patch("/groups/{id}/shares", w.h.BatchReviewShares)
			router.Delete("/groups/{id}/transcripts/{transcriptID}", w.h.RemoveGroupTranscript)
			router.Delete("/groups/{id}/members/{userID}", w.h.RemoveGroupMember)
			router.Post("/groups/{id}/members", w.h.AddGroupMember)
			router.Post("/groups/{id}/join", w.h.JoinGroup)
			router.Patch("/groups/{id}/members/{userID}/role", w.h.PromoteMember)
			actor := w.owner
			method, path, body := http.MethodPatch, fmt.Sprintf("/groups/%s/shares/%s", uuidFromPg(w.groupID), uuidFromPg(w.transcriptID)), `{"status":"approved"}`
			switch c.Action {
			case "batch-approve":
				path = fmt.Sprintf("/groups/%s/shares", uuidFromPg(w.groupID))
				body = fmt.Sprintf(`{"status":"approved","transcript_ids":["%s"]}`, uuidFromPg(w.transcriptID))
			case "reject":
				body = `{"status":"rejected"}`
			case "remove-transcript":
				method = http.MethodDelete
				path = fmt.Sprintf("/groups/%s/transcripts/%s", uuidFromPg(w.groupID), uuidFromPg(w.transcriptID))
				body = ""
			case "add-member":
				target, err := w.h.queries.GetUserByID(ctx, w.member.PgID())
				if err != nil {
					t.Fatal(err)
				}
				method = http.MethodPost
				path = fmt.Sprintf("/groups/%s/members", uuidFromPg(w.groupID))
				body = fmt.Sprintf(`{"username":%q}`, target.GithubUsername)
			case "promote-member":
				path = fmt.Sprintf("/groups/%s/members/%s/role", uuidFromPg(w.groupID), w.member.ID)
				body = `{"role":"contributor"}`
			case "join":
				if _, err := w.pool.Exec(ctx, `UPDATE groups SET acceptance_mode='open' WHERE id=$1`, w.groupID); err != nil {
					t.Fatal(err)
				}
				method = http.MethodPost
				path = fmt.Sprintf("/groups/%s/join", uuidFromPg(w.groupID))
				body = ""
				actor = w.member
			case "remove-member":
				method = http.MethodDelete
				path = fmt.Sprintf("/groups/%s/members/%s", uuidFromPg(w.groupID), w.member.ID)
				body = ""
			}
			req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, actor))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("grant action status %d: %s", rec.Code, rec.Body.String())
			}
			after := postedNow(w.fake)
			if after.edits <= before.edits {
				t.Fatal("the committed grant change never refreshed the posted check")
			}
			if after.conclusion != c.After {
				t.Fatalf("after conclusion %q, want %q", after.conclusion, c.After)
			}
			if got := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); got != c.Visibility {
				t.Fatalf("grant action moved visibility to %q", got)
			}
		})
	}
}
