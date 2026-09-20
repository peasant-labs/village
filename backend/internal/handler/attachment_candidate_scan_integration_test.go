//go:build integration

package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
)

// commitReadTracer counts the reads of recorded commits, so a test can assert that
// matching a candidate pool reads them once for the pool rather than once per
// candidate.
type commitReadTracer struct {
	mu    sync.Mutex
	reads int
}

func (t *commitReadTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM transcript_commits") {
		t.mu.Lock()
		t.reads++
		t.mu.Unlock()
	}
	return ctx
}

func (t *commitReadTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *commitReadTracer) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reads
}

func (t *commitReadTracer) reset() {
	t.mu.Lock()
	t.reads = 0
	t.mu.Unlock()
}

// candidateCorpusHandler is the production handler over a pool that counts the
// commit reads. It mirrors the shared attachment harness; only the pool differs.
func candidateCorpusHandler(t *testing.T, tracer *commitReadTracer) (*Handler, *pgxpool.Pool, *recordingTranscriptBlobStore, *attachmentGitHubFake) {
	t.Helper()
	cfg, err := database.PoolConfig(pullTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("build the corpus pool config: %v", err)
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create the corpus pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("migrate the corpus database: %v", err)
	}

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

// TestCandidateCommitReadsDoNotScaleWithTheCorpus is the measurement #206 asks
// for. The candidate pool is every transcript the author owns that carries a
// stored remote, so it grows with what they have published, and matching it runs
// on a confirm, a refresh, and inside every publish by them. Here the author has
// seventeen such transcripts, sixteeen of them unrelated to the pull request plus
// one that matches: reading the recorded commits costs one round trip, not one
// per candidate.
func TestCandidateCommitReadsDoNotScaleWithTheCorpus(t *testing.T) {
	tracer := &commitReadTracer{}
	h, pool, blobs, fake := candidateCorpusHandler(t, tracer)
	ctx := context.Background()

	owner := attachmentInsertOwner(t, ctx, pool, 996001)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "corpus-" + strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	remote := "git@github.com:acme/" + repoName + ".git"
	matchedSHA := "abc888800000000000000000000000000000001"

	const corpus = 17
	for i := 0; i < corpus-1; i++ {
		attachmentSeedTranscript(t, ctx, pool, blobs, owner, remote,
			fmt.Sprintf("abc88880000000000000000000000000000%03d", i), "private", fmt.Sprintf("corpus/%d", i), time.Now().Add(-time.Duration(i+1)*time.Hour))
	}
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, remote, matchedSHA, "private", "corpus/head", time.Now().Add(-time.Minute))

	attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, matchedSHA, 7)
	fake.setPullCommits(matchedSHA)

	tracer.reset()
	rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/7/confirm", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if reads := tracer.count(); reads != 1 {
		t.Fatalf("recorded-commit reads while matching a %d-transcript corpus = %d, want 1: the pool grows with what the author has published, so the read must not", corpus, reads)
	}
}
