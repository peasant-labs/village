//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The lifecycle's integration evidence. It runs against real PostgreSQL and the
// real state machine with a fake blob store (so the transcript path is exercised
// without object storage) and a fake GitHub, which is the only way to prove the
// two things unit tests cannot: that the governance writes and the derived
// transcript_shares row come out right, and that a GitHub failure leaves the
// attachment and every transcript exactly as they were.

// attachmentPublicationContent is a minimal but valid stored publication with
// one prompt and one assistant turn, so the digest has something to render.
func attachmentPublicationContent() []byte {
	return []byte(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"attachment-session","harness":"claude-code","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:05:00Z","durationMins":5,"totalTokens":100,"tokensIn":60,"tokensOut":40,"turnCount":2,"toolCallCount":0,"turns":[{"index":0,"role":"user","content":"please attach my prompts","timestamp":"2026-01-01T00:00:01Z","depth":0},{"index":1,"role":"assistant","content":"done","timestamp":"2026-01-01T00:00:02Z","depth":0}]}}`)
}

// attachmentGitHubFake serves the GitHub surface the lifecycle touches and
// counts the writes, so a test can prove what was posted and, with failWrites,
// that a failure changes nothing.
type attachmentGitHubFake struct {
	srv         *httptest.Server
	private     bool
	pullCommits []string

	mu             sync.Mutex
	failWrites     bool
	checkCreates   int
	checkUpdates   int
	commentCreates int
	commentEdits   int
	commentDeletes int
}

func newAttachmentGitHubFake(t *testing.T) *attachmentGitHubFake {
	t.Helper()
	fake := &attachmentGitHubFake{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeFailure := func() bool {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.failWrites {
				http.Error(w, `{"message":"nope"}`, http.StatusUnprocessableEntity)
				return true
			}
			return false
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/app/installations/"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":"ghs_attachment","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		case strings.Contains(r.URL.Path, "/pulls/") && strings.HasSuffix(r.URL.Path, "/commits"):
			commits := make([]string, 0)
			fake.mu.Lock()
			commits = append(commits, fake.pullCommits...)
			fake.mu.Unlock()
			parts := make([]string, 0, len(commits))
			for _, sha := range commits {
				parts = append(parts, fmt.Sprintf(`{"sha":%q,"commit":{"message":"m","author":{"name":"A","date":"2026-01-01T00:00:00Z"}}}`, sha))
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "[%s]", strings.Join(parts, ","))
		case strings.Contains(r.URL.Path, "/check-runs"):
			if writeFailure() {
				return
			}
			fake.mu.Lock()
			if r.Method == http.MethodPatch {
				fake.checkUpdates++
			} else {
				fake.checkCreates++
			}
			fake.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"id":11,"html_url":"https://example.test/check/11","status":"completed","conclusion":"success"}`)
		case strings.Contains(r.URL.Path, "/issues/") && strings.Contains(r.URL.Path, "/comments"):
			if writeFailure() {
				return
			}
			fake.mu.Lock()
			switch r.Method {
			case http.MethodDelete:
				fake.commentDeletes++
			case http.MethodPatch:
				fake.commentEdits++
			default:
				fake.commentCreates++
			}
			fake.mu.Unlock()
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"id":22,"html_url":"https://example.test/comment/22","body":"posted"}`)
		default:
			fake.mu.Lock()
			private := fake.private
			fake.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"name":"widgets","owner":{"login":"acme"},"private":%t}`, private)
		}
	})
	fake.srv = httptest.NewServer(mux)
	t.Cleanup(fake.srv.Close)
	return fake
}

func (f *attachmentGitHubFake) setPullCommits(shas ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullCommits = shas
}

// attachmentTestHandler composes the lifecycle over a real pool, a fake blob
// store, and the fake GitHub.
func attachmentTestHandler(t *testing.T) (*Handler, *pgxpool.Pool, *recordingTranscriptBlobStore, *attachmentGitHubFake) {
	t.Helper()
	pool := publishLockPool(t, 8)
	blobs := newRecordingTranscriptBlobStore()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	fake := newAttachmentGitHubFake(t)
	client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)}, gh.WithBaseURL(fake.srv.URL))
	if err != nil {
		t.Fatalf("compose the GitHub client: %v", err)
	}
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: blobs, titles: titles, gh: client, cfg: &config.Config{FrontendURL: "https://village.example"}}
	return h, pool, blobs, fake
}

func attachmentInsertOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubID int64) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $1::bigint::text) RETURNING id
	`, githubID, fmt.Sprintf("attachment-owner-%d", githubID)).Scan(&id); err != nil {
		t.Fatalf("insert attachment owner: %v", err)
	}
	return id
}

// attachmentLinkCollective creates a collective that linked the repository, with
// the prompts check on and a chosen mode, and returns its id.
func attachmentLinkCollective(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, repoOwner, repoName string, private bool, mode string) pgtype.UUID {
	t.Helper()
	var groupID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, linked_github_org, post_prompts_check, prompts_check_mode)
		VALUES ($1, $2, 'acme', true, $3) RETURNING id
	`, "attachment-"+uuid.NewString(), owner, mode).Scan(&groupID); err != nil {
		t.Fatalf("insert attachment collective: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collective_repositories (group_id, owner, name, installation_id, is_private, linked_by)
		VALUES ($1, $2, $3, 4242, $4, $5)
	`, groupID, repoOwner, repoName, private, owner); err != nil {
		t.Fatalf("link attachment repository: %v", err)
	}
	return groupID
}

// attachmentSeedTranscript writes a decryptable transcript through the fake blob
// store and returns its id.
func attachmentSeedTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blobs *recordingTranscriptBlobStore, owner pgtype.UUID, remote, sha, visibility, branch string, sessionStart time.Time) pgtype.UUID {
	t.Helper()
	contents := attachmentPublicationContent()
	descriptor, identity, err := blobs.Write(ctx, uuid.New(), contents)
	if err != nil {
		t.Fatalf("write transcript blob: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transcript seed: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", database.SystemActorID); err != nil {
		t.Fatalf("declare actor and writer marker: %v", err)
	}

	var id pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id, local_id, model_provider, blob_key, blob_size_bytes, schema_version, project_hash,
		                         wrapped_data_key, encryption_algorithm, key_version, content_hash,
		                         git_remote, git_branch, session_start, visibility)
		VALUES ($1, $2, 'claude-code', $3, $4, '2', 'c4e19a2f0b73', $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id
	`, owner, uuid.NewString(), string(descriptor.ObjectKey()), identity.PlaintextSize(), descriptor.WrappedDEK(),
		string(descriptor.Algorithm()), int32(descriptor.KeyVersion()), string(identity.Hash()),
		remote, nullableText(branch), pgtype.Timestamptz{Time: sessionStart, Valid: true}, visibility).Scan(&id); err != nil {
		t.Fatalf("insert transcript: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_commits (transcript_id, commit_order, sha, message, authored_at)
		VALUES ($1, 0, $2, 'seed', $3)
	`, id, sha, pgtype.Timestamptz{Time: sessionStart, Valid: true}); err != nil {
		t.Fatalf("insert transcript commit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transcript seed: %v", err)
	}
	return id
}

// attachmentCreatePreview records an attachment and moves it to preview, which
// is the state confirm acts on.
func attachmentCreatePreview(t *testing.T, ctx context.Context, h *Handler, groupID, owner pgtype.UUID, repoOwner, repoName, sha string, number int) sqlc.PullRequestAttachment {
	t.Helper()
	attachment, err := h.queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		GroupID:      groupID,
		RepoOwner:    repoOwner,
		RepoName:     repoName,
		GithubRepoID: 4242,
		Number:       int32(number),
		HeadSha:      sha,
		BaseRemote:   "acme/" + repoName,
		HeadRemote:   "acme/" + repoName,
		AuthorID:     owner,
	})
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	previewed, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Preview)
	if err != nil {
		t.Fatalf("move attachment to preview: %v", err)
	}
	return previewed
}

func attachmentRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/pulls/{owner}/{name}/{number}", h.GetPullRequestAttachment)
	r.Post("/api/v1/pulls/{owner}/{name}/{number}/confirm", h.ConfirmPullRequestAttachment)
	r.Delete("/api/v1/pulls/{owner}/{name}/{number}", h.DetachPullRequestAttachment)
	r.Get("/api/v1/users/me/prompt-requests", h.ListMyPromptRequests)
	r.Get("/api/v1/users/me/settings", h.GetUserSettings)
	r.Patch("/api/v1/users/me/settings", h.UpdateUserSettings)
	return r
}

func attachmentServe(t *testing.T, router http.Handler, method, target string, viewer pgtype.UUID) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if method == http.MethodPatch {
		body = bytes.NewReader([]byte(`{"preview_before_attach":true}`))
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, body)
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
	}
	if viewer.Valid {
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(viewer.Bytes), Username: "attachment"}))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestConfirmWidensSharesAndPosts_RealPostgres is the attached path end to end:
// a preview whose recorded commit is in the pull request is confirmed, the
// transcript is shared with the linking collective through an APPROVED share
// recorded before the widening, and the check and sticky comment are posted.
func TestConfirmWidensSharesAndPosts_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992001)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "abc1234000000000000000000000000000000001"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))

	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)

	rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/7/confirm", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var response schema.VillagePullRequestAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if response.Attachment.State != schema.VillagePullRequestAttachmentState("attached") {
		t.Fatalf("state = %q, want attached", response.Attachment.State)
	}
	if response.Digest == nil {
		t.Fatal("a confirmed attachment must carry its digest")
	}
	if !response.Attachment.IsPrivateRepository {
		t.Error("is_private_repository = false, want true for a private repository")
	}
	if len(response.Transcripts) != 1 || response.Transcripts[0].PreviousVisibility != schema.VillageTranscriptVisibilityPrivate {
		t.Fatalf("transcripts = %+v, want one bound at private", response.Transcripts)
	}

	// The transcript was widened to the collective and holds an APPROVED share.
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "shared" {
		t.Fatalf("transcript visibility = %q, want shared", visibility)
	}
	var shareStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM transcript_shares WHERE transcript_id = $1 AND group_id = $2
	`, transcriptID, groupID).Scan(&shareStatus); err != nil {
		t.Fatalf("read the derived share: %v", err)
	}
	if shareStatus != "approved" {
		t.Fatalf("derived share status = %q, want approved", shareStatus)
	}

	// The snapshot is the visibility before the widening, and the posted ids and
	// digest were recorded.
	stored, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.CommentID.Valid || !stored.CheckRunID.Valid || len(stored.Digest) == 0 {
		t.Fatalf("stored attachment = %+v, want a comment, a check run, and a digest", stored)
	}
	var previous string
	if err := pool.QueryRow(ctx, `
		SELECT previous_visibility FROM pull_request_attachment_transcripts WHERE attachment_id = $1 AND transcript_id = $2
	`, attachment.ID, transcriptID).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if previous != "private" {
		t.Fatalf("recorded previous_visibility = %q, want private", previous)
	}

	fake.mu.Lock()
	creates, comments := fake.checkCreates, fake.commentCreates
	fake.mu.Unlock()
	if creates != 1 || comments != 1 {
		t.Fatalf("check creates = %d and comment creates = %d, want one each", creates, comments)
	}
}

// TestDetachRestoresEachRecordedVisibility_RealPostgres covers the visibility
// trio: a transcript that started private widens to shared, one that started
// shared stays shared, and one that started public is never narrowed by a
// private repository. Detaching restores each one's recorded value exactly.
func TestDetachRestoresEachRecordedVisibility_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992002)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "trio-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")

	shas := []string{
		"aaa1234000000000000000000000000000000001",
		"bbb1234000000000000000000000000000002",
		"ccc1234000000000000000000000000000003",
	}
	started := []string{"private", "shared", "public"}
	ids := make([]pgtype.UUID, 0, 3)
	for i, sha := range shas {
		ids = append(ids, attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, started[i], "", time.Now().Add(-time.Duration(i+1)*time.Hour)))
	}
	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, shas[0], 8)
	fake.setPullCommits(shas...)

	if rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/8/confirm", owner); rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	// Attaching widens: private becomes shared, the others are not narrowed.
	wantAfterAttach := []string{"shared", "shared", "public"}
	for i, id := range ids {
		var visibility string
		if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", id).Scan(&visibility); err != nil {
			t.Fatal(err)
		}
		if visibility != wantAfterAttach[i] {
			t.Fatalf("transcript starting %s has visibility %q after attach, want %q", started[i], visibility, wantAfterAttach[i])
		}
	}

	if rec := attachmentServe(t, attachmentRouter(h), http.MethodDelete, "/api/v1/pulls/acme/"+repoName+"/8", owner); rec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	// Detaching restores exactly what each one started with.
	for i, id := range ids {
		var visibility string
		if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", id).Scan(&visibility); err != nil {
			t.Fatal(err)
		}
		if visibility != started[i] {
			t.Fatalf("transcript starting %s was restored to %q, want %q", started[i], visibility, started[i])
		}
	}
	var bindings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pull_request_attachment_transcripts WHERE attachment_id = $1", attachment.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("bindings = %d after detach, want 0", bindings)
	}
	fake.mu.Lock()
	deletes := fake.commentDeletes
	fake.mu.Unlock()
	if deletes != 1 {
		t.Fatalf("comment deletes = %d, want 1", deletes)
	}
}

// TestConfirmIsConflictOutsidePreview proves the route answers 409 for a state
// that does not allow the action, and that nothing was widened.
func TestConfirmIsConflictOutsidePreview(t *testing.T) {
	h, pool, blobs, _ := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992003)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "conflict-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "ddd1234000000000000000000000000000004"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	// Left in 'requested': confirming is not allowed from there.
	if _, err := h.queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		GroupID: groupID, RepoOwner: "acme", RepoName: repoName, GithubRepoID: 4242, Number: 9,
		HeadSha: sha, BaseRemote: "acme/" + repoName, HeadRemote: "acme/" + repoName, AuthorID: owner,
	}); err != nil {
		t.Fatal(err)
	}

	rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/9/confirm", owner)
	if rec.Code != http.StatusConflict {
		t.Fatalf("confirm from requested = %d, want 409", rec.Code)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" {
		t.Fatalf("a refused confirm changed visibility to %q", visibility)
	}
}

// TestGitHubFailureLeavesTheAttachmentUnchanged proves the 502 contract: a
// GitHub failure answers 502, the attachment stays in preview, and every
// transcript keeps its visibility.
func TestGitHubFailureLeavesTheAttachmentUnchanged(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992004)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "fail-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "eee1234000000000000000000000000000005"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 10)
	fake.setPullCommits(sha)
	fake.mu.Lock()
	fake.failWrites = true
	fake.mu.Unlock()

	rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/10/confirm", owner)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("confirm with GitHub failing = %d (%s), want 502", rec.Code, rec.Body.String())
	}

	stored, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "preview" {
		t.Fatalf("state = %q after a 502, want preview", stored.State)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" {
		t.Fatalf("visibility = %q after a 502, want private", visibility)
	}
	var bindings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pull_request_attachment_transcripts WHERE attachment_id = $1", attachment.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("bindings = %d after a 502, want none", bindings)
	}
}

// TestReadRouteIsAuthorOrCollectiveForPrivateRepositories proves the read policy:
// a private repository's attachment is 404 to an anonymous reader and served to
// its author.
func TestReadRouteIsAuthorOrCollectiveForPrivateRepositories(t *testing.T) {
	h, pool, _, _ := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992005)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "read-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "fff1234000000000000000000000000000006"
	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 11)

	anonymous := attachmentServe(t, attachmentRouter(h), http.MethodGet, "/api/v1/pulls/acme/"+repoName+"/11", pgtype.UUID{})
	if anonymous.Code != http.StatusNotFound {
		t.Fatalf("anonymous read of a private attachment = %d, want 404", anonymous.Code)
	}
	author := attachmentServe(t, attachmentRouter(h), http.MethodGet, "/api/v1/pulls/acme/"+repoName+"/11", owner)
	if author.Code != http.StatusOK {
		t.Fatalf("author read = %d (%s), want 200", author.Code, author.Body.String())
	}
	var response schema.VillagePullRequestAttachmentResponse
	if err := json.Unmarshal(author.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Attachment.ID != schema.VillageUUID(uuid.UUID(attachment.ID.Bytes).String()) {
		t.Fatalf("read attachment id = %s, want the stored one", response.Attachment.ID)
	}
	if !response.ViewerIsAuthor {
		t.Error("viewer_is_author = false, want true for the author")
	}
	if response.Transcripts == nil {
		t.Error("transcripts must serialise as an empty array, not null")
	}
}
