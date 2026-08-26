//go:build integration

package handler

// Deterministic discovery pagination against real PostgreSQL. These pin the two
// guarantees the ordering + snapshot change exists to provide on
// GET /api/v1/transcripts:
//
//  1. Rows that tie on the user-selected primary sort column take a stable,
//     unique position across repeated reads and adjacent offset pages, because
//     every ORDER BY ends with the unique primary key (t.id). Ambiguous SQL order
//     could otherwise duplicate, omit, or reorder a tied row between pages.
//  2. The total and the returned page describe ONE database snapshot: the count
//     and the page read run in one read-only REPEATABLE READ transaction, so a
//     publish committed by another connection between the two reads cannot make
//     the count disagree with the page.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration -race ./internal/handler/...

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// discoveryRow is one controllable transcript in the shared discovery dataset.
// Optional metric pointers are nil to insert SQL NULL and exercise NULLS LAST.
type discoveryRow struct {
	Name          string `yaml:"name"`
	ID            string `yaml:"id"`
	Visibility    string `yaml:"visibility"`
	PublishedAtMs int64  `yaml:"publishedAtMs"`
	TurnCount     *int32 `yaml:"turnCount"`
	TokenCount    *int32 `yaml:"tokenCount"`
	DurationMs    *int64 `yaml:"durationMs"`
}

// discoveryCase is one anonymous read of the shared dataset with an exact
// expectation: the ordered ids for the requested page plus the wire metadata the
// response must preserve — total, the echoed page, and the normalized limit.
type discoveryCase struct {
	Name          string   `yaml:"name"`
	Sort          string   `yaml:"sort"`
	Page          int      `yaml:"page"`
	Limit         int      `yaml:"limit"`
	ExpectedTotal int64    `yaml:"expectedTotal"`
	ExpectedPage  int      `yaml:"expectedPage"`
	ExpectedLimit int      `yaml:"expectedLimit"`
	ExpectedIDs   []string `yaml:"expectedIds"`
}

// discoveryResponse is the decoded discovery wire under assertion: the ordered
// ids plus the total/page/limit metadata the endpoint must preserve.
type discoveryResponse struct {
	IDs   []string
	Total int64
	Page  int
	Limit int
}

//go:embed testdata/discovery_pagination/inventory.yaml
var discoveryInventoryYAML []byte

//go:embed testdata/discovery_pagination/cases.yaml
var discoveryCasesYAML []byte

//go:embed testdata/discovery_pagination/snapshot_seed.yaml
var discoverySnapshotSeedYAML []byte

// discoveryInsertRow inserts one fully-controlled transcript (explicit id,
// published_at, and optional metrics) attributed to the SYSTEM actor, satisfying
// the migration-026 fail-closed publish trigger. It is the discovery analogue of
// pullInsertTranscript, extended with the columns discovery orders on.
func discoveryInsertRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, row discoveryRow) {
	t.Helper()
	id, err := uuid.Parse(row.ID)
	if err != nil {
		t.Fatalf("discoveryInsertRow %q: parse id %q: %v", row.Name, row.ID, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("discoveryInsertRow %q: begin: %v", row.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("discoveryInsertRow %q: declare system actor: %v", row.Name, err)
	}
	hash := schema.ComputeTranscriptHash([]byte(row.Name))
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcripts (
			id, owner_id, local_id, title, visibility, model_provider, model_name,
			blob_key, blob_size_bytes, schema_version, content_hash, wrapped_data_key,
			encryption_algorithm, key_version, published_at, turn_count, token_count, duration_ms,
			project_hash
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`,
		toPgUUID(id), owner, row.Name, "t-"+row.Name, row.Visibility, "claude-code", "m-"+row.Name,
		"blob/"+row.Name, int64(len(row.Name)), "0.1.0", hash, []byte("fixture-wrapped-data-key"),
		"aes-256-gcm-random-nonce-v1", 1, time.UnixMilli(row.PublishedAtMs), row.TurnCount, row.TokenCount, row.DurationMs,
		fixtureProjectHash(row.Name),
	); err != nil {
		t.Fatalf("discoveryInsertRow %q: insert: %v", row.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("discoveryInsertRow %q: commit: %v", row.Name, err)
	}
}

// discoveryList runs the REAL ListTranscripts handler as an anonymous caller
// scoped to one owner, returning the ordered transcript ids and the wire
// total/page/limit metadata the endpoint reports.
func discoveryList(t *testing.T, h *Handler, ownerUsername, sort string, page, limit int) discoveryResponse {
	t.Helper()
	target := fmt.Sprintf("/api/v1/transcripts?owner=%s&page=%d&limit=%d", ownerUsername, page, limit)
	if sort != "" {
		target += "&sort=" + sort
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	h.ListTranscripts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("ListTranscripts(%s) status = %d, want 200 (body: %s)", target, w.Code, w.Body.String())
	}
	var resp struct {
		Transcripts []struct {
			Transcript struct {
				ID string `json:"id"`
			} `json:"transcript"`
		} `json:"transcripts"`
		Total int64 `json:"total"`
		Page  int   `json:"page"`
		Limit int   `json:"limit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response for %s: %v\nbody: %s", target, err, w.Body.String())
	}
	ids := make([]string, 0, len(resp.Transcripts))
	for _, row := range resp.Transcripts {
		ids = append(ids, row.Transcript.ID)
	}
	return discoveryResponse{IDs: ids, Total: resp.Total, Page: resp.Page, Limit: resp.Limit}
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestListTranscripts_DeterministicPagination_RealPostgres proves stable unique
// ordering and membership for every sort mode and page against a dataset built
// entirely from primary-sort ties, and that the private row is filtered.
func TestListTranscripts_DeterministicPagination_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980801, "discovery-order-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	inventory := loadFixtureRows[discoveryRow](t, discoveryInventoryYAML, 6)
	names := make(map[string]bool, len(inventory))
	ids := make(map[string]bool, len(inventory))
	for _, row := range inventory {
		if row.Name == "" || row.ID == "" {
			t.Fatalf("inventory row missing name/id: %+v", row)
		}
		if names[row.Name] {
			t.Fatalf("duplicate inventory row name %q", row.Name)
		}
		if ids[row.ID] {
			t.Fatalf("duplicate inventory row id %q", row.ID)
		}
		names[row.Name] = true
		ids[row.ID] = true
		discoveryInsertRow(t, ctx, pool, owner, row)
	}

	// All six rows (including the private one) are really stored; the anonymous
	// listing below must still exclude the private row (total 5, never present).
	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&stored); err != nil {
		t.Fatalf("count stored transcripts: %v", err)
	}
	if stored != 6 {
		t.Fatalf("stored transcripts = %d, want 6 (fixture insert incomplete)", stored)
	}

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	cases := loadFixtureRows[discoveryCase](t, discoveryCasesYAML, 14)
	caseNames := make(map[string]bool, len(cases))
	for _, c := range cases {
		if c.Name == "" {
			t.Fatalf("case missing name: %+v", c)
		}
		if caseNames[c.Name] {
			t.Fatalf("duplicate case name %q", c.Name)
		}
		caseNames[c.Name] = true

		got := discoveryList(t, h, "discovery-order-owner", c.Sort, c.Page, c.Limit)
		if got.Total != c.ExpectedTotal {
			t.Errorf("case %q: total = %d, want %d", c.Name, got.Total, c.ExpectedTotal)
		}
		if got.Page != c.ExpectedPage {
			t.Errorf("case %q: wire page = %d, want %d (the response must echo the requested page)", c.Name, got.Page, c.ExpectedPage)
		}
		if got.Limit != c.ExpectedLimit {
			t.Errorf("case %q: wire limit = %d, want %d (the response must preserve/normalize the requested limit)", c.Name, got.Limit, c.ExpectedLimit)
		}
		if !equalIDs(got.IDs, c.ExpectedIDs) {
			t.Errorf("case %q: ordered ids = %v, want %v", c.Name, got.IDs, c.ExpectedIDs)
		}
		for _, id := range got.IDs {
			if id == "66666666-6666-6666-6666-666666666666" {
				t.Errorf("case %q: private row leaked into anonymous listing", c.Name)
			}
		}
	}

	// Adjacent pages of the default sort must partition the full public ordering
	// with no duplicate, omission, or leak. Read page 1..3 (limit 2) live and
	// assert the union is exactly the five public ids with no repeat.
	union := make(map[string]int)
	for page := 1; page <= 3; page++ {
		got := discoveryList(t, h, "discovery-order-owner", "", page, 2)
		for _, id := range got.IDs {
			union[id]++
		}
	}
	if len(union) != 5 {
		t.Errorf("union of adjacent pages had %d distinct ids, want 5 (rows duplicated or omitted across pages): %v", len(union), union)
	}
	for id, n := range union {
		if n != 1 {
			t.Errorf("id %s appeared %d times across adjacent pages, want exactly once", id, n)
		}
	}
	for _, want := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"44444444-4444-4444-4444-444444444444",
		"55555555-5555-5555-5555-555555555555",
	} {
		if union[want] != 1 {
			t.Errorf("public id %s missing from the adjacent-page union", want)
		}
	}
}

// TestListTranscripts_CountPageSnapshot_RealPostgres proves the total and the
// page come from one snapshot. A competing public publish is committed on
// another connection BETWEEN the count and the page read (via the discovery read
// barrier). Under the read-only REPEATABLE READ transaction the page read cannot
// see that late row, so the returned total equals the returned row count and the
// late row is absent. The final stored-count assertion proves the competing
// write really committed mid-flight, so the test is not vacuous: under READ
// COMMITTED the page read would include the late row and the count/page equality
// would fail.
func TestListTranscripts_CountPageSnapshot_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 981001, "discovery-snapshot-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	seed := loadFixtureRows[discoveryRow](t, discoverySnapshotSeedYAML, 3)
	seedNames := make(map[string]bool, len(seed))
	seedIDs := make(map[string]bool, len(seed))
	for _, row := range seed {
		if row.Name == "" || row.ID == "" {
			t.Fatalf("snapshot seed row missing name/id: %+v", row)
		}
		if seedNames[row.Name] {
			t.Fatalf("duplicate snapshot seed row name %q", row.Name)
		}
		if seedIDs[row.ID] {
			t.Fatalf("duplicate snapshot seed row id %q", row.ID)
		}
		seedNames[row.Name] = true
		seedIDs[row.ID] = true
		discoveryInsertRow(t, ctx, pool, owner, row)
	}

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	lateID := "aaaaaaaa-0000-0000-0000-00000000ffff"
	var once sync.Once
	h.discoveryReadBarrier = func() {
		once.Do(func() {
			discoveryInsertRow(t, ctx, pool, owner, discoveryRow{
				Name: "late", ID: lateID, Visibility: "public", PublishedAtMs: 1700000003000,
			})
		})
	}

	got := discoveryList(t, h, "discovery-snapshot-owner", "", 1, 50)

	if int64(len(got.IDs)) != got.Total {
		t.Errorf("returned %d rows but total = %d; count and page are not one snapshot", len(got.IDs), got.Total)
	}
	if got.Total != 3 {
		t.Errorf("total = %d, want 3 (the count was taken before the competing publish committed)", got.Total)
	}
	for _, id := range got.IDs {
		if id == lateID {
			t.Errorf("late row committed mid-transaction leaked into the REPEATABLE READ page snapshot")
		}
	}

	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&stored); err != nil {
		t.Fatalf("count stored transcripts: %v", err)
	}
	if stored != 4 {
		t.Fatalf("stored transcripts = %d, want 4; the barrier's competing publish did not commit, so the snapshot test is vacuous", stored)
	}
}
