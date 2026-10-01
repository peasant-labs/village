//go:build integration

package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/attachment-deletion.yaml
var attachmentDeletionYAML []byte

type attachmentDeletionCase struct {
	Name          string `yaml:"name"`
	Visibility    string `yaml:"visibility"`
	State         string `yaml:"state"`
	RemoteFailure bool   `yaml:"remote_failure"`
}

func TestDeletedTranscriptsLeaveNoStoredAttachmentPrompts_RealPostgres(t *testing.T) {
	cases, err := decodeFixtureRows[attachmentDeletionCase](attachmentDeletionYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatal("empty deletion fixture name")
		}
		if _, ok := present[c.Name]; ok {
			t.Fatalf("duplicate deletion fixture %s", c.Name)
		}
		present[c.Name] = struct{}{}
		if !containsString([]string{"private", "public"}, c.Visibility) || !containsString([]string{"preview", "attached", "detached", "posting"}, c.State) {
			t.Fatalf("invalid deletion fixture %s", c.Name)
		}
	}
	assertExactTitleFixtureNames(t, "attachment-deletion", present, []string{"deleting_a_private_preview_prunes_its_saved_prompt", "deleting_a_public_preview_prunes_its_saved_prompt", "deleting_a_private_attachment_prunes_its_saved_prompt", "deleting_a_public_attachment_withdraws_its_posted_prompt", "deleting_a_detached_transcript_prunes_its_saved_prompt", "a_failed_remote_repost_cannot_keep_deleted_prompt_bytes", "deleting_during_an_attach_cannot_restore_deleted_prompt_bytes", "a_failed_reconcile_cannot_restore_deleted_prompt_bytes"})
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAttachAudienceWorld(t, attachAudienceCase{Repository: "public", Transcript: c.Visibility}, 998000+int64(i)*10)
			t.Cleanup(func() { purgeAuditRows(t, context.Background(), w.pool, []pgtype.UUID{w.transcriptID}) })
			attachment, err := w.h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: w.repoName, Number: int32(w.number)})
			if err != nil {
				t.Fatal(err)
			}
			// The preview must really contain the prompt before deletion can prove pruning.
			if !strings.Contains(string(attachment.Digest), "please attach my prompts") {
				t.Fatal("fixture never saved the preview prompt")
			}
			remove := func() {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+uuidFromPg(w.transcriptID).String(), nil)
				req = withChiURLParam(req, "id", uuidFromPg(w.transcriptID).String())
				req = req.WithContext(context.WithValue(req.Context(), UserContextKey, w.owner))
				rec := httptest.NewRecorder()
				w.h.DeleteTranscript(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("delete status %d: %s", rec.Code, rec.Body.String())
				}
			}
			if c.State == "posting" {
				w.fake.mu.Lock()
				w.fake.beforeCommentWrite = func() {
					remove()
					w.fake.mu.Lock()
					w.fake.failWrites = c.RemoteFailure
					w.fake.mu.Unlock()
				}
				w.fake.mu.Unlock()
				attachmentConfirm(t, w.h, w.owner.PgID(), "acme", w.repoName, w.number)
			} else {
				if c.State != "preview" {
					attachmentConfirm(t, w.h, w.owner.PgID(), "acme", w.repoName, w.number)
				}
				if c.State == "detached" {
					rec := attachmentServe(t, attachmentRouter(w.h), http.MethodDelete, "/api/v1/pulls/acme/"+w.repoName+"/7", w.owner.PgID())
					if rec.Code != http.StatusOK {
						t.Fatalf("detach status %d: %s", rec.Code, rec.Body.String())
					}
				}
				w.fake.mu.Lock()
				w.fake.failWrites = c.RemoteFailure
				w.fake.mu.Unlock()
				remove()
			}
			staleDigest := append([]byte(nil), attachment.Digest...)
			attachment, err = w.h.queries.GetPullRequestAttachment(ctx, attachment.ID)
			if err != nil {
				t.Fatal(err)
			}
			stored := string(attachment.Digest)
			if strings.Contains(stored, "please attach my prompts") || strings.Contains(stored, w.title) || strings.Contains(stored, uuidFromPg(w.transcriptID).String()) {
				t.Fatalf("deleted content survived in the stored digest: %s", stored)
			}
			if _, err := w.h.queries.GetTranscriptByID(ctx, w.transcriptID); err == nil {
				t.Fatal("the transcript was not deleted")
			}
			// Exercise a late artifact write independently of the eventual repost:
			// it must not resurrect a deleted prompt even when GitHub remains offline.
			if err := w.h.storeAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{ID: attachment.ID, HeadSha: attachment.HeadSha, CommentID: attachment.CommentID, CheckRunID: attachment.CheckRunID, Digest: staleDigest}); err != nil {
				t.Fatal(err)
			}
			late, err := w.h.queries.GetPullRequestAttachment(ctx, attachment.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(late.Digest), "please attach my prompts") || strings.Contains(string(late.Digest), uuidFromPg(w.transcriptID).String()) {
				t.Fatal("a late writer restored deleted prompt bytes")
			}
			if c.State == "preview" {
				// A pre-fix stored preview can still contain historical text. The read
				// boundary independently requires the transcript to exist and be owned.
				if err := w.h.queries.SetPullRequestAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{ID: attachment.ID, HeadSha: attachment.HeadSha, Digest: staleDigest}); err != nil {
					t.Fatal(err)
				}
			}
			page := pageAs(t, w.h, w.owner, "acme", w.repoName, w.number)
			if page.Code != http.StatusOK {
				t.Fatalf("owner page status %d: %s", page.Code, page.Body.String())
			}
			if strings.Contains(page.Body.String(), "please attach my prompts") || strings.Contains(page.Body.String(), w.title) || strings.Contains(page.Body.String(), uuidFromPg(w.transcriptID).String()) {
				t.Fatalf("owner page exposed deleted content: %s", page.Body.String())
			}
			if (c.State == "attached" || c.State == "posting") && !c.RemoteFailure {
				posted := postedNow(w.fake)
				if strings.Contains(posted.comment, "please attach my prompts") || strings.Contains(posted.check, "please attach my prompts") || posted.conclusion != "neutral" {
					t.Fatalf("posted copy remained after deletion: %+v", posted)
				}
			}
		})
	}
}
