//go:build integration

package handler

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

//go:embed testdata/legacy-detach.yaml
var legacyDetachYAML []byte

type legacyDetachCase struct {
	Name             string `yaml:"name"`
	Repository       string `yaml:"repository"`
	Previous         string `yaml:"previous"`
	OtherShareBefore bool   `yaml:"other_share_before"`
	WidenedTo        string `yaml:"widened_to"`
	OpenedShare      bool   `yaml:"opened_share"`
	OwnerThen        string `yaml:"owner_then"`
	Expect           struct {
		Visibility       string `yaml:"visibility"`
		LinkingShareLive bool   `yaml:"linking_share_live"`
		OtherShareLive   bool   `yaml:"other_share_live"`
		Narrowed         bool   `yaml:"narrowed"`
	} `yaml:"expect"`
}

// requiredLegacyDetachCases names every row: each recorded visibility on each
// repository kind, and an owner's narrowing or later share kept through the
// detach.
var requiredLegacyDetachCases = []string{
	"private_repository_private_is_restored",
	"private_repository_shared_stays_shared",
	"private_repository_public_stays_public",
	"public_repository_private_is_restored",
	"public_repository_shared_is_restored",
	"an_owner_narrowing_is_kept",
	"an_owner_share_made_since_is_kept",
	"an_owner_share_made_since_is_kept_on_a_public_repository",
}

func loadLegacyDetachCases(t *testing.T) []legacyDetachCase {
	t.Helper()
	cases, err := decodeFixtureRows[legacyDetachCase](legacyDetachYAML)
	if err != nil {
		t.Fatalf("load testdata/legacy-detach.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; c.Name == "" || repeated {
			t.Fatalf("legacy-detach row name %q is empty or repeated", c.Name)
		}
		present[c.Name] = struct{}{}
		if c.Repository != "public" && c.Repository != "private" {
			t.Fatalf("row %q: repository %q is neither public nor private", c.Name, c.Repository)
		}
		for _, visibility := range []string{c.Previous, c.WidenedTo, c.Expect.Visibility} {
			if !containsString(shareAttemptVisibilities, visibility) {
				t.Fatalf("row %q: visibility %q is not one of %v", c.Name, visibility, shareAttemptVisibilities)
			}
		}
		if disclosureRank(c.WidenedTo) < disclosureRank(c.Previous) {
			t.Fatalf("row %q: an older attach only ever widened, so widened_to %q cannot be narrower than previous %q", c.Name, c.WidenedTo, c.Previous)
		}
		if c.OpenedShare && c.Repository != "private" {
			t.Fatalf("row %q: an older attach opened a share only on a private repository", c.Name)
		}
		if c.OwnerThen != "" && c.OwnerThen != "narrow_to_private" && c.OwnerThen != "share_with_other" {
			t.Fatalf("row %q: owner_then %q is neither narrow_to_private nor share_with_other", c.Name, c.OwnerThen)
		}
	}
	assertExactTitleFixtureNames(t, "legacy-detach", present, requiredLegacyDetachCases)
	return cases
}

// legacyDetachWorld is the state an older attach left: a transcript it widened,
// the share it opened, and the binding it marked, on an attached pull request.
type legacyDetachWorld struct {
	h            *Handler
	pool         *pgxpool.Pool
	fake         *attachmentGitHubFake
	owner        *AuthUser
	repoName     string
	number       int
	sha          string
	groupID      pgtype.UUID
	otherGroupID pgtype.UUID
	transcriptID pgtype.UUID
	attachment   sqlc.PullRequestAttachment
}

func newLegacyDetachWorld(t *testing.T, c legacyDetachCase, githubID int64) *legacyDetachWorld {
	t.Helper()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, githubID)
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, owner) })

	w := &legacyDetachWorld{
		h: h, pool: pool, fake: fake, number: 7,
		owner:    &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "legacy-owner"},
		repoName: "legacy-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8],
		sha:      fmt.Sprintf("b%039d", githubID),
	}
	w.groupID = attachmentLinkCollective(t, ctx, pool, owner, "acme", w.repoName, c.Repository == "private", "informational")
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id
	`, "legacy-other-"+uuid.NewString(), owner).Scan(&w.otherGroupID); err != nil {
		t.Fatalf("insert the owner's other collective: %v", err)
	}
	w.transcriptID = attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+w.repoName+".git", w.sha, c.Previous, "", time.Now().Add(-time.Hour))
	if c.OtherShareBefore {
		w.approveShare(t, w.otherGroupID)
	}

	// What the older attach did: widen, open the linking collective's share,
	// and record the visibility it found on a binding it marked.
	if c.WidenedTo != c.Previous {
		execAsSystem(t, ctx, pool, `UPDATE transcripts SET visibility = $2 WHERE id = $1`, w.transcriptID, c.WidenedTo)
	}
	if c.OpenedShare {
		w.approveShare(t, w.groupID)
	}
	created, err := h.queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		GroupID: w.groupID, RepoOwner: "acme", RepoName: w.repoName, GithubRepoID: 4242, Number: int32(w.number),
		HeadSha: w.sha, BaseRemote: "acme/" + w.repoName, HeadRemote: "acme/" + w.repoName, AuthorID: owner,
	})
	if err != nil {
		t.Fatalf("create the attachment: %v", err)
	}
	if w.attachment, err = promptattach.Transition(ctx, h.queries, created.ID, promptattach.Attached); err != nil {
		t.Fatalf("attach the attachment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pull_request_attachment_transcripts (attachment_id, transcript_id, position, previous_visibility, attach_widened)
		VALUES ($1, $2, 0, $3, true)`, w.attachment.ID, w.transcriptID, c.Previous); err != nil {
		t.Fatalf("bind the transcript the way the older attach did: %v", err)
	}
	fake.setPullCommits(w.sha)
	return w
}

// approveShare appends an approved attempt for the transcript and one
// collective, the way an accepted submission or the older attach recorded one.
func (w *legacyDetachWorld) approveShare(t *testing.T, groupID pgtype.UUID) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(), `
		INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
		SELECT $1, $2, COALESCE(MAX(event_num), 0) + 1, 'approved'
		FROM transcript_share_attempts WHERE transcript_id = $1 AND group_id = $2`, w.transcriptID, groupID); err != nil {
		t.Fatalf("approve a share: %v", err)
	}
}

func (w *legacyDetachWorld) detach(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return attachmentServe(t, attachmentRouter(w.h), http.MethodDelete,
		fmt.Sprintf("/api/v1/pulls/acme/%s/%d", w.repoName, w.number), pgtype.UUID{Bytes: w.owner.ID, Valid: true})
}

// TestDetachReleasesWhatAnOlderAttachWidened_RealPostgres drives every row of
// testdata/legacy-detach.yaml through the author's mounted detach route over
// real PostgreSQL.
func TestDetachReleasesWhatAnOlderAttachWidened_RealPostgres(t *testing.T) {
	for i, c := range loadLegacyDetachCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newLegacyDetachWorld(t, c, 996300+int64(i))

			switch c.OwnerThen {
			case "narrow_to_private":
				if rec := transcriptVisibilityPatch(t, w.h, w.owner, w.transcriptID, dbVisibilityPrivate); rec.Code != http.StatusOK {
					t.Fatalf("the owner's narrowing: status = %d (%s), want 200", rec.Code, rec.Body.String())
				}
			case "share_with_other":
				w.approveShare(t, w.otherGroupID)
			}

			eventsBefore := governanceEventCount(t, ctx, w.pool, w.transcriptID)
			if rec := w.detach(t); rec.Code != http.StatusOK {
				t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
			}

			if visibility := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); visibility != c.Expect.Visibility {
				t.Errorf("visibility = %q after the detach, want %q", visibility, c.Expect.Visibility)
			}
			if live := shareAttemptIsLive(latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID)); live != c.Expect.LinkingShareLive {
				t.Errorf("the linking collective's share is live = %t after the detach, want %t", live, c.Expect.LinkingShareLive)
			}
			if live := shareAttemptIsLive(latestShareStatus(t, ctx, w.pool, w.transcriptID, w.otherGroupID)); live != c.Expect.OtherShareLive {
				t.Errorf("the owner's other share is live = %t after the detach, want %t: detaching never takes away a grant the owner made", live, c.Expect.OtherShareLive)
			}
			if c.OpenedShare {
				if latest := latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID); latest != "retracted" {
					t.Errorf("the linking collective's latest attempt = %q, want retracted: the ledger keeps the acceptance as history", latest)
				}
			}

			wantEvents := 0
			if c.Expect.Narrowed {
				wantEvents = 1
			}
			if events := governanceEventCount(t, ctx, w.pool, w.transcriptID) - eventsBefore; events != wantEvents {
				t.Errorf("the detach appended %d governance events, want %d", events, wantEvents)
			}
			if c.Expect.Narrowed {
				var actor pgtype.UUID
				var visibility string
				if err := w.pool.QueryRow(ctx, `
					SELECT changed_by, visibility FROM transcript_governance_events_audit
					WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1`, w.transcriptID).Scan(&actor, &visibility); err != nil {
					t.Fatalf("read the detach's governance event: %v", err)
				}
				if actor.Bytes != w.owner.ID || visibility != c.Expect.Visibility {
					t.Errorf("the detach's event = %s moving to %q, want the owner moving to %q", uuid.UUID(actor.Bytes), visibility, c.Expect.Visibility)
				}
			}

			binding, err := w.h.queries.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{
				AttachmentID: w.attachment.ID, TranscriptID: w.transcriptID,
			})
			if err != nil {
				t.Fatalf("the detached pull request must keep its binding: %v", err)
			}
			if binding.AttachWidened {
				t.Error("the released binding still reads as widened, so a later detach would restore it a second time")
			}
			if binding.PreviousVisibility != c.Previous {
				t.Errorf("previous_visibility = %q after the detach, want the recorded %q", binding.PreviousVisibility, c.Previous)
			}
		})
	}
}

// TestLegacyDetachWithdrawsUnderThePublishLock_RealPostgres proves the release
// is one transaction under the transcript's publish lock. An owner's unshare
// takes that lock too, so the two can no longer race each other to the next
// ledger ordinal: while the lock is held the detach waits, and it withdraws
// nothing until it gets the lock.
func TestLegacyDetachWithdrawsUnderThePublishLock_RealPostgres(t *testing.T) {
	c := legacyDetachCase{Name: "lock", Repository: "private", Previous: dbVisibilityPrivate, WidenedTo: dbVisibilityShared, OpenedShare: true}
	w := newLegacyDetachWorld(t, c, 996351)
	ctx := context.Background()

	var localID string
	if err := w.pool.QueryRow(ctx, `SELECT local_id FROM transcripts WHERE id = $1`, w.transcriptID).Scan(&localID); err != nil {
		t.Fatalf("read the transcript's session: %v", err)
	}
	lockConn, err := w.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lock-holding connection: %v", err)
	}
	lockKey := sessionPublishLockKey(pgtype.UUID{Bytes: w.owner.ID, Valid: true}, localID)
	released := false
	defer func() {
		if !released {
			_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey)
		}
		lockConn.Release()
	}()
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		t.Fatalf("hold the transcript's publish lock: %v", err)
	}

	var wg sync.WaitGroup
	var rec *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec = w.detach(t)
	}()
	waitForSessionBlockedBy(t, ctx, w.pool, backendPID(t, ctx, lockConn))

	if latest := latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID); latest != "approved" {
		t.Fatalf("the linking collective's share is %q while the publish lock is held, want approved: the detach withdrew outside the lock", latest)
	}
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey); err != nil {
		t.Fatalf("release the publish lock: %v", err)
	}
	released = true
	wg.Wait()

	if rec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if latest := latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID); latest != "retracted" {
		t.Errorf("the linking collective's latest attempt = %q after the detach, want retracted", latest)
	}
	if visibility := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); visibility != dbVisibilityPrivate {
		t.Errorf("visibility = %q after the detach, want the recorded private", visibility)
	}
}

// TestLegacyDetachRepostsAnotherPullRequestThatListedIt_RealPostgres covers the
// one detach that narrows a transcript: another attached pull request that
// listed it while it was public must stop listing it, without waiting for an
// unrelated refresh.
func TestLegacyDetachRepostsAnotherPullRequestThatListedIt_RealPostgres(t *testing.T) {
	c := legacyDetachCase{Name: "repost", Repository: "public", Previous: dbVisibilityPrivate, WidenedTo: dbVisibilityPublic}
	w := newLegacyDetachWorld(t, c, 996352)
	ctx := context.Background()
	owner := pgtype.UUID{Bytes: w.owner.ID, Valid: true}

	// A second pull request in the same repository binds the transcript the way
	// attaching does now, while it is public, so its comment lists the prompt.
	attachmentCreatePreview(t, ctx, w.h, w.groupID, owner, "acme", w.repoName, w.sha, 8)
	attachmentConfirm(t, w.h, owner, "acme", w.repoName, 8)
	w.fake.mu.Lock()
	listed := strings.Contains(w.fake.lastCommentBody, "please attach my prompts")
	editsBefore := w.fake.commentEdits
	w.fake.mu.Unlock()
	if !listed {
		t.Fatal("the second pull request must list the public transcript before the detach, or the repost proves nothing")
	}

	if rec := w.detach(t); rec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if visibility := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); visibility != dbVisibilityPrivate {
		t.Fatalf("visibility = %q after the detach, want the recorded private", visibility)
	}
	w.fake.mu.Lock()
	defer w.fake.mu.Unlock()
	if w.fake.commentEdits <= editsBefore {
		t.Fatal("the other pull request was not reposted after the detach narrowed its transcript")
	}
	if strings.Contains(w.fake.lastCommentBody, "please attach my prompts") {
		t.Errorf("the other pull request still lists a transcript the detach made private: %s", w.fake.lastCommentBody)
	}
	if !strings.Contains(w.fake.lastCommentBody, "1 attached transcript is not listed here.") {
		t.Errorf("the other pull request must say a transcript is not listed: %s", w.fake.lastCommentBody)
	}
}
