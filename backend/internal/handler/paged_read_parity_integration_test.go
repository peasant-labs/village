//go:build integration

package handler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// The synthetic parity corpus: one stored transcript per (row state, audience
// kind) pair, published through the production publish path into real
// PostgreSQL and the encrypted store over RustFS, moved into its row state,
// given its audience, and then read both ways through the production handlers
// as a viewer of that audience. A unit test cannot observe this: the row state
// decides how the encrypted read authenticates (or refuses) the object, and the
// audience decides the access rule and the repository projection, both of
// which live in SQL and in the stored object.
//
// Promotion record. Subject: the paged reads serve what the whole-document
// read serves, for every shape. Necessity: the shapes differ only in stored
// columns and grants. Production path: the publish handler, the mounted read
// handlers and the encrypted store; nothing is mocked. Cost: one publish and
// about ten reads per shape, 28 shapes, a few seconds. Lifetime: every row,
// user, collective, audit row and object is removed by the test's cleanups.
// Concurrency: rows are owned by fresh users. CI parity: the encrypted
// aggregate's environment. Evidence: HTTP bodies and statuses. Mutation: the
// comparer's own perturbation cases. Exit: when the row-state set shrinks to
// the states a writer produces, this corpus shrinks with it.

// parityShapeWorld publishes the corpus subject, applies the audience and then
// the row state, and returns the world and the viewer to read it as.
func parityShapeWorld(t *testing.T, pool *pgxpool.Pool, objects storage.ObjectStore, ordinal int, content []byte, shape transcriptReadShape) (*orderingWorld, *AuthUser) {
	t.Helper()
	w := newOrderingWorld(t, pool, objects, ordinal, content)
	ctx := w.ctx
	newUser := func(suffix string) pgtype.UUID {
		id := pullInsertUser(t, ctx, pool, int64(5_200_000+ordinal*10+len(suffix)), "parity-"+suffix+"-"+uuid.NewString()[:8])
		t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, id) })
		return id
	}
	newCollective := func(creator pgtype.UUID) pgtype.UUID {
		var group pgtype.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id`, "parity-"+uuid.NewString(), creator).Scan(&group); err != nil {
			t.Fatalf("insert the collective: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, $3, 'legacy') ON CONFLICT DO NOTHING`, group, creator, collectiveOwnerRole); err != nil {
			t.Fatalf("add the collective owner: %v", err)
		}
		return group
	}
	setVisibility := func(visibility string) {
		execAsSystem(t, ctx, pool, `UPDATE transcripts SET visibility = $2 WHERE id = $1`, w.id, visibility)
	}
	var viewer *AuthUser
	switch shape.Audience {
	case audiencePublic:
	case audienceOwner:
		setVisibility(dbVisibilityPrivate)
		viewer = w.owner
	case audienceCollectiveMember:
		member := newUser("member")
		group := newCollective(pgtype.UUID{Bytes: w.owner.ID, Valid: true})
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'member', 'legacy')`, group, member); err != nil {
			t.Fatalf("add the member: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, 1, $3)`, w.id, group, string(ShareStatusApproved)); err != nil {
			t.Fatalf("approve the share: %v", err)
		}
		setVisibility(dbVisibilityShared)
		viewer = &AuthUser{ID: uuid.UUID(member.Bytes), Username: "parity-member"}
	case audienceCollectiveOwnerPreview:
		curator := newUser("curator")
		group := newCollective(curator)
		if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, 1, $3)`, w.id, group, string(ShareStatusPending)); err != nil {
			t.Fatalf("submit the transcript: %v", err)
		}
		setVisibility(dbVisibilityPrivate)
		viewer = &AuthUser{ID: uuid.UUID(curator.Bytes), Username: "parity-curator"}
	default:
		t.Fatalf("no corpus audience setup for %s; add one", shape.Audience)
	}
	switch shape.State {
	case rowStateNew:
	case rowStateLegacyKnown:
		execFixtureIdentity(t, ctx, pool, `UPDATE transcripts SET publish_metadata_key = NULL, publish_metadata_hash = NULL, publish_metadata_size = NULL WHERE id = $1`, w.id)
	case rowStateHashOnly:
		execFixtureIdentity(t, ctx, pool, `UPDATE transcripts SET blob_size_bytes = NULL WHERE id = $1`, w.id)
	case rowStateUntracked:
		execFixtureIdentity(t, ctx, pool, `UPDATE transcripts SET content_hash = NULL WHERE id = $1`, w.id)
	case rowStateExposedLegacy:
		execFixtureIdentity(t, ctx, pool, `UPDATE transcripts SET segmented_identity_hash = content_hash, segmented_identity_size = blob_size_bytes, content_hash = NULL WHERE id = $1`, w.id)
	case rowStateMalformedHash:
		execFixtureIdentity(t, ctx, pool, `UPDATE transcripts SET content_hash = 'not-a-canonical-content-hash' WHERE id = $1`, w.id)
	case rowStateDivergent:
		execFixtureIdentity(t, ctx, pool, `UPDATE transcripts SET content_hash = NULL, content_revision = 2 WHERE id = $1`, w.id)
	default:
		t.Fatalf("no corpus state setup for %s; add one", shape.State)
	}
	if t.Failed() {
		t.FailNow()
	}
	return w, viewer
}

func TestPagedReadParityCorpus(t *testing.T) {
	fixture := loadPagedReadParityFixture(t)
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)

	before, err := runPagedReadCensus(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	type corpusCase struct {
		shape  transcriptReadShape
		world  *orderingWorld
		viewer *AuthUser
	}
	var corpus []corpusCase
	for i, shape := range allTranscriptReadShapes() {
		if fixture.StateReads[shape.State.String()] == "unreachable" {
			continue
		}
		if !strings.Contains(strings.Join(fixture.Corpus, " "), shapeCaseName(shape)) {
			t.Fatalf("shape %s has no corpus name; run the coverage case", shape)
		}
		w, viewer := parityShapeWorld(t, pool, objects, 52000+i, []byte(fixture.Content), shape)
		state, ok := classifyTranscriptRowState(rowIdentityFactsOf(w.row()))
		if !ok || state != shape.State {
			t.Fatalf("the %s world's row classifies as %s (explained %v)", shape, state, ok)
		}
		corpus = append(corpus, corpusCase{shape: shape, world: w, viewer: viewer})
	}

	// The census counts exactly the corpus rows on top of what was stored
	// before: each row once per audience kind it is reachable through.
	after, err := runPagedReadCensus(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if after.Unexplained != before.Unexplained {
		t.Fatalf("the corpus added %d unexplained rows to the census", after.Unexplained-before.Unexplained)
	}
	for _, state := range allTranscriptRowStates() {
		if fixture.StateReads[state.String()] == "unreachable" {
			continue
		}
		want := map[transcriptAudienceKind]int64{audiencePublic: 1, audienceOwner: int64(len(allTranscriptAudienceKinds())), audienceCollectiveMember: 1, audienceCollectiveOwnerPreview: 1}
		for _, audience := range allTranscriptAudienceKinds() {
			shape := transcriptReadShape{State: state, Audience: audience}
			if got := after.Shapes[shape] - before.Shapes[shape]; got != want[audience] {
				t.Errorf("census counted %d corpus rows for %s, want %d", got, shape, want[audience])
			}
		}
	}
	t.Run("unreachable-l1d", func(t *testing.T) {
		w := corpus[0].world
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		// Isolate the CHECK from the separate writer trigger. The UPDATE must
		// fail, and the transaction is always rolled back.
		if _, err := tx.Exec(context.Background(), `SET LOCAL session_replication_role = 'replica'`); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(context.Background(), `UPDATE transcripts SET content_hash = NULL, content_revision = 2 WHERE id = $1`, w.id)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != fixture.Unreachable["l1d"] {
			t.Fatalf("unreachable row returned %v, want constraint %s", err, fixture.Unreachable["l1d"])
		}
	})

	t.Run("parity-every-shape-matches-content", func(t *testing.T) {
		requireFamilyName(t, fixture, "parity-every-shape-matches-content")
		for _, c := range corpus {
			t.Run(shapeCaseName(c.shape), func(t *testing.T) {
				w := c.world
				if !w.handler.canViewTranscript(w.ctx, c.viewer, w.row()) {
					t.Fatalf("the %s viewer cannot read the row, so the shape is not exercised", c.shape.Audience)
				}
				obs, err := w.handler.observePagedReads(w.ctx, w.handler.pagedReadRoutes(), uuidFromPg(w.id), c.viewer)
				if err != nil {
					t.Fatal(err)
				}
				served := obs.ContentStatus == http.StatusOK
				if want := fixture.StateReads[c.shape.State.String()] == "served"; served != want {
					t.Fatalf("the whole-document read answered %d for row state %s, want served=%v", obs.ContentStatus, c.shape.State, want)
				}
				if served && (len(obs.Partitions) == 0 || len(obs.Partitions[0].Segments) == 0) {
					t.Fatalf("the paged reads served no segment, so nothing was compared")
				}
				if findings := comparePagedReads(obs, schema.MaxTranscriptDirectoryRefs); len(findings) != 0 {
					t.Fatalf("paged reads differ from the whole-document read: %+v", findings)
				}
			})
		}
	})

	// The operator path end to end: the sample mode reads this database
	// through the same comparer, as viewers it selects itself, and its report
	// and the process log carry no content.
	t.Run("parity-privacy-no-content-logged", func(t *testing.T) {
		requireFamilyName(t, fixture, "parity-privacy-no-content-logged")
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
		t.Cleanup(func() { slog.SetDefault(previous) })
		var out bytes.Buffer
		h := corpus[0].world.handler
		if err := h.RunPagedReadCheck(context.Background(), &out, PagedReadCheckOptions{Mode: PagedReadCheckSample, AuthorizationRef: "ops/synthetic-corpus"}); err != nil {
			t.Fatalf("the sample check failed: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "result subject=1 outcome=clean") || strings.Contains(out.String(), "broken=1") {
			t.Fatalf("the sample check read nothing or found a break:\n%s", out.String())
		}
		assertContentFreeReport(t, out.String(), logs.String(), "parity corpus", "first prompt")
	})
}
