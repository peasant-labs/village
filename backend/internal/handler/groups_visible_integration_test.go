//go:build integration

package handler

// Which collectives GET /groups/visible lists, and what role each row carries -
// driven through the REAL handler against a REAL PostgreSQL.
//
// It has to be a real database. The visibility rule lives entirely in SQL, so a
// mock-backed test would assert only that a Go re-implementation agrees with
// itself, and the null-role branch only exists because the join misses.
//
// The shipped SearchCollectives is used as an ORACLE on the same world. The new
// query carries a COPY of its visibility predicate, and copies drift, so every
// case asks both and a disagreement fails.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration -race ./internal/handler/...

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/groups-visible.yaml
var groupsVisibleYAML []byte

type gvCollective struct {
	DataAccess     string `yaml:"data_access"`
	AcceptanceMode string `yaml:"acceptance_mode"`
}

type gvExpect struct {
	Listed bool `yaml:"listed"`
	// Role is the role the row must report. An empty Role means the row must
	// carry a NULL role, which is how a collective seen through the public or
	// open rule alone answers.
	Role          string `yaml:"role"`
	OracleVisible bool   `yaml:"oracle_visible"`
}

type gvCase struct {
	Name string `yaml:"name"`
	Why  string `yaml:"why"`
	// CallerMembership is the role the caller holds, or "none".
	CallerMembership string       `yaml:"caller_membership"`
	Collective       gvCollective `yaml:"collective"`
	Expect           gvExpect     `yaml:"expect"`
}

// requiredVisibleGroupsCases names every case that must exist. Each is here
// because losing it hides a specific failure: one of the three branches of the
// predicate, the closed collective that must stay closed, the role the row
// reports, and the null role that tells a consumer "you are not a member".
var requiredVisibleGroupsCases = []string{
	"member_of_members_only_collective_is_listed_with_their_role",
	"owner_of_members_only_collective_is_listed_with_owner_role",
	"pending_member_of_members_only_collective_is_listed_with_pending_role",
	"members_only_curated_collective_is_hidden_from_a_non_member",
	"public_collective_is_listed_for_a_non_member_with_no_role",
	"open_collective_is_listed_for_a_non_member_with_no_role",
	"member_of_a_public_collective_is_listed_with_their_role",
}

func loadVisibleGroupsCases(t *testing.T) []gvCase {
	t.Helper()
	cases, err := decodeFixtureRows[gvCase](groupsVisibleYAML)
	if err != nil {
		t.Fatalf("load the visible-collectives corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatalf("a visible-collectives case has no name; every case is addressed by name")
		}
		if present[c.Name] {
			t.Fatalf("the visible-collectives corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if c.Collective.DataAccess == "" || c.Collective.AcceptanceMode == "" {
			t.Fatalf("case %q leaves data access or acceptance mode unstated; both halves of the visibility rule are "+
				"declared on every case so it is clear which half decided", c.Name)
		}
		if c.CallerMembership == "none" && c.Expect.Role != "" {
			t.Fatalf("case %q expects role %q from a caller who is not a member; a non-member row carries no role",
				c.Name, c.Expect.Role)
		}
		if !c.Expect.Listed && c.Expect.Role != "" {
			t.Fatalf("case %q expects a role on a collective it also expects to be absent", c.Name)
		}
	}
	for _, required := range requiredVisibleGroupsCases {
		if !present[required] {
			t.Fatalf("the visible-collectives corpus no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

// gvRow mirrors the JSON body GET /groups/visible writes straight from
// []sqlc.ListVisibleGroupsRow. Decoding into it pins that the wire actually
// carries these keys, and that role is null rather than absent or empty.
type gvRow struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Role *string `json:"role"`
}

func TestVisibleGroups(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadVisibleGroupsCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			world := newVisibleGroupsWorld(t, ctx, pool, testCase)
			defer world.cleanup(t, ctx)

			rows := world.visibleGroups(t, h)
			row, found := world.find(rows)

			if found != testCase.Expect.Listed {
				t.Fatalf("GET /groups/visible listed=%v, want %v (%s)", found, testCase.Expect.Listed, testCase.Why)
			}
			if !testCase.Expect.Listed {
				world.assertOracleAgrees(t, ctx, h, testCase, false)
				return
			}
			switch {
			case testCase.Expect.Role == "" && row.Role != nil:
				t.Errorf("the row reports role %q, want a null role: the caller is not a member of this collective and a "+
					"consumer reading a role would show a membership badge that does not exist (%s)",
					*row.Role, testCase.Why)
			case testCase.Expect.Role != "" && row.Role == nil:
				t.Errorf("the row reports a null role, want %q (%s)", testCase.Expect.Role, testCase.Why)
			case testCase.Expect.Role != "" && *row.Role != testCase.Expect.Role:
				t.Errorf("the row reports role %q, want %q (%s)", *row.Role, testCase.Expect.Role, testCase.Why)
			}
			world.assertOracleAgrees(t, ctx, h, testCase, true)
		})
	}
}

// TestVisibleGroupsRefusesAnAnonymousCaller pins the route's own gate. The
// visible set is defined partly by which collectives the caller belongs to, so
// there is no caller-free answer to give.
func TestVisibleGroupsRefusesAnAnonymousCaller(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	rec := httptest.NewRecorder()
	h.ListVisibleGroups(rec, httptest.NewRequest(http.MethodGet, "/api/v1/groups/visible", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous caller got %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
}

type visibleGroupsWorld struct {
	pool      *pgxpool.Pool
	caller    pgtype.UUID
	other     pgtype.UUID
	group     pgtype.UUID
	groupName string
}

// newVisibleGroupsWorld builds one collective owned by somebody else, so the
// caller's own membership is the only thing the case varies. A caller who owned
// every collective would satisfy the member branch by accident.
func newVisibleGroupsWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, testCase gvCase) *visibleGroupsWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &visibleGroupsWorld{
		pool:      pool,
		groupName: testCase.Name + "-" + suffix,
	}
	w.caller = cvInsertUser(t, ctx, pool, "gv-caller-"+suffix, true)
	w.other = cvInsertUser(t, ctx, pool, "gv-owner-"+suffix, true)

	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode, data_access) VALUES ($1, $2, $3, $4) RETURNING id
	`, w.groupName, w.other, testCase.Collective.AcceptanceMode, testCase.Collective.DataAccess).Scan(&w.group); err != nil {
		t.Fatalf("create the collective: %v", err)
	}
	shareAddMember(t, ctx, pool, w.group, w.other, "owner")
	if testCase.CallerMembership != "none" {
		shareAddMember(t, ctx, pool, w.group, w.caller, testCase.CallerMembership)
	}
	return w
}

func (w *visibleGroupsWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.group); err != nil {
		t.Errorf("cleanup the collective: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.caller, w.other)
}

func (w *visibleGroupsWorld) groupID() string { return uuid.UUID(w.group.Bytes).String() }

// visibleGroups drives the REAL handler as the caller and decodes the parsed
// JSON body.
func (w *visibleGroupsWorld) visibleGroups(t *testing.T, h *Handler) []gvRow {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/visible", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey,
		&AuthUser{ID: uuid.UUID(w.caller.Bytes), Username: "gv-caller"}))
	rec := httptest.NewRecorder()
	h.ListVisibleGroups(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /groups/visible answered %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var rows []gvRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode the GET /groups/visible body: %v", err)
	}
	return rows
}

func (w *visibleGroupsWorld) find(rows []gvRow) (gvRow, bool) {
	for _, row := range rows {
		if row.ID == w.groupID() {
			return row, true
		}
	}
	return gvRow{}, false
}

// assertOracleAgrees compares the new query with the shipped SearchCollectives
// on the same world. The corpus declares what the oracle says, so a case can be
// wrong about the oracle as well as about the query, and both are failures.
func (w *visibleGroupsWorld) assertOracleAgrees(t *testing.T, ctx context.Context, h *Handler, testCase gvCase, listed bool) {
	t.Helper()
	rows, err := h.queries.SearchCollectives(ctx, sqlc.SearchCollectivesParams{
		Column1: pgtype.Text{String: w.groupName, Valid: true},
		UserID:  w.caller,
		Limit:   50,
	})
	if err != nil {
		t.Fatalf("ask the SearchCollectives oracle: %v", err)
	}
	oracleVisible := false
	for _, row := range rows {
		if uuid.UUID(row.ID.Bytes).String() == w.groupID() {
			oracleVisible = true
		}
	}
	if oracleVisible != testCase.Expect.OracleVisible {
		t.Fatalf("the shipped SearchCollectives reports visible=%v, but the corpus declares %v. The corpus has drifted "+
			"from the oracle it compares against; fix the declaration or the world.", oracleVisible, testCase.Expect.OracleVisible)
	}
	if listed != oracleVisible {
		t.Errorf("GET /groups/visible says listed=%v while SearchCollectives says visible=%v. The visibility rule is a "+
			"copy of the oracle's and must decide identically (%s).", listed, oracleVisible, testCase.Why)
	}
}
