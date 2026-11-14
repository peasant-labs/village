//go:build integration

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

//go:embed testdata/contribution-snapshot.yaml
var contributionSnapshotYAML []byte

// snapshotSeed is one transcript of a fixture case. Attempts are the ledger
// events in the collective under test, oldest first; OtherAttempts are events
// in a different collective.
type snapshotSeed struct {
	Key           string   `yaml:"key"`
	Project       string   `yaml:"project"`
	ProjectName   string   `yaml:"project_name"`
	Title         string   `yaml:"title"`
	Visibility    string   `yaml:"visibility"`
	Parent        string   `yaml:"parent"`
	OrphanParent  bool     `yaml:"orphan_parent"`
	Provider      string   `yaml:"provider"`
	Owner         string   `yaml:"owner"`
	Attempts      []string `yaml:"attempts"`
	OtherAttempts []string `yaml:"other_attempts"`
}

type snapshotRequestCase struct {
	Name        string   `yaml:"name"`
	Project     string   `yaml:"project"`
	Q           string   `yaml:"q"`
	QIDOf       string   `yaml:"q_id_of"`
	QIDPrefixOf string   `yaml:"q_id_prefix_of"`
	Provider    string   `yaml:"provider"`
	Want        []string `yaml:"want"`
}

type snapshotFixtureCase struct {
	Name        string                `yaml:"name"`
	Why         string                `yaml:"why"`
	Transcripts []snapshotSeed        `yaml:"transcripts"`
	Requests    []snapshotRequestCase `yaml:"requests"`
}

// loadSnapshotCases decodes the fixture strictly and refuses a corpus that has
// lost a case or a request. Names, not counts: a count guard churns on every
// addition, while a name guard fails only when something real is deleted.
func loadSnapshotCases(t *testing.T) []snapshotFixtureCase {
	t.Helper()
	cases, err := decodeFixtureRows[snapshotFixtureCase](contributionSnapshotYAML)
	if err != nil {
		t.Fatalf("decode the contribution snapshot fixture: %v", err)
	}
	required := map[string][]string{
		"families_and_statuses": {"alpha", "beta", "unknown_project"},
		"search": {
			"no_q", "root_title", "root_title_other_case", "helper_only_title", "project_name",
			"helper_project_name_brings_the_family_in", "exact_id_of_a_helper", "exact_id_of_a_root",
			"partial_id_does_not_match", "nothing_matches", "percent_is_literal", "underscore_is_literal",
			"blank_q_is_no_q", "provider_alone", "provider_nobody_holds", "provider_and_q_intersect",
		},
	}
	byName := map[string]snapshotFixtureCase{}
	for _, c := range cases {
		if _, dup := byName[c.Name]; dup {
			t.Fatalf("fixture case %q is declared twice", c.Name)
		}
		byName[c.Name] = c
	}
	for name, requests := range required {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("the snapshot corpus no longer contains the case %q. It exists because losing it hides a real "+
				"failure; restore it rather than deleting it from this manifest.", name)
		}
		have := map[string]bool{}
		for _, r := range c.Requests {
			have[r.Name] = true
		}
		for _, r := range requests {
			if !have[r] {
				t.Fatalf("case %q no longer contains the request %q; restore it rather than deleting it from this manifest.", name, r)
			}
		}
	}
	return cases
}

// ---------------------------------------------------------------------------
// The harness: a world, a router that mounts the real routes, and a token.
// ---------------------------------------------------------------------------

const snapshotTestSecret = "disposable-contribution-snapshot-secret"

type snapshotHarness struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	w      *contributeWorld
	h      *Handler
	router chi.Router
	// others caches the routers built for handlers other than s.h: a handler
	// installs its grouped predicates once, so mounting twice would panic.
	others map[*Handler]chi.Router
	// otherGroup is a second collective, created on first use, whose attempts
	// must never leak into answers about the first.
	otherGroup pgtype.UUID
	ordinal    int
	status     map[string][]string // key -> attempts in the collective under test
	projects   map[string]string   // key -> project key
	visibility map[string]string
	parents    map[string]string // key -> parent key ("" for a root)
}

func newSnapshotHarness(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, cfg *config.Config) *snapshotHarness {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.JWTSecret = snapshotTestSecret
	s := &snapshotHarness{
		t: t, ctx: ctx, pool: pool,
		w:          newContributeWorld(t, ctx, pool, name, "open", true),
		status:     map[string][]string{},
		projects:   map[string]string{},
		visibility: map[string]string{},
		parents:    map[string]string{},
	}
	s.h = New(cfg, pool, nil)
	s.router = chi.NewRouter()
	s.router.Route("/api/v1", s.h.RegisterCollectiveBrowseRoutes)
	return s
}

func (s *snapshotHarness) cleanup() {
	if s.otherGroup.Valid {
		if _, err := s.pool.Exec(s.ctx, "DELETE FROM groups WHERE id = $1", s.otherGroup); err != nil {
			s.t.Errorf("cleanup the second collective: %v", err)
		}
	}
	s.w.cleanup(s.t, s.ctx)
}

func (s *snapshotHarness) token(user pgtype.UUID, name string) string {
	s.t.Helper()
	token, err := auth.CreateToken(snapshotTestSecret, uuid.UUID(user.Bytes), name)
	if err != nil {
		s.t.Fatal(err)
	}
	return token
}

// get serves one request through the real router as the member.
func (s *snapshotHarness) get(prefix, query string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.getAs(s.h, s.w.member, s.w.memberName, s.w.groupID(), prefix, query)
}

func (s *snapshotHarness) getAs(h *Handler, user pgtype.UUID, name, group, prefix, query string) *httptest.ResponseRecorder {
	s.t.Helper()
	router := s.router
	if h != s.h {
		if s.others == nil {
			s.others = map[*Handler]chi.Router{}
		}
		router = s.others[h]
		if router == nil {
			router = chi.NewRouter()
			router.Route("/api/v1", h.RegisterCollectiveBrowseRoutes)
			s.others[h] = router
		}
	}
	target := "/api/v1" + prefix + "/" + group + "/contributable"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+s.token(user, name))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func (s *snapshotHarness) snapshotQuery(project string, extra url.Values) string {
	values := url.Values{"view": {"snapshot"}, "project_hash": {s.w.projectHash(project)}}
	for k, v := range extra {
		values[k] = v
	}
	return values.Encode()
}

// seed inserts transcripts as the system actor, then their attempts.
func (s *snapshotHarness) seed(rows []snapshotSeed) {
	s.t.Helper()
	for _, row := range rows {
		s.insert(row)
	}
}

func (s *snapshotHarness) insert(row snapshotSeed) {
	s.t.Helper()
	w := s.w
	if row.Project == "" {
		s.t.Fatalf("seed %q names no project", row.Key)
	}
	visibility := row.Visibility
	if visibility == "" {
		visibility = "shared"
	}
	provider := row.Provider
	if provider == "" {
		provider = "claude-code"
	}
	title := row.Title
	if title == "" {
		title = "t-" + row.Key
	}
	owner := w.member
	if row.Owner == "other" {
		owner = w.other
	}
	var parent pgtype.Text
	switch {
	case row.OrphanParent:
		parent = pgtype.Text{String: "never-published-" + w.suffix + "-" + row.Key, Valid: true}
	case row.Parent != "":
		local, ok := w.localIDs[row.Parent]
		if !ok {
			s.t.Fatalf("seed %q names parent %q, which is not declared before it", row.Key, row.Parent)
		}
		parent = pgtype.Text{String: local, Valid: true}
	}
	localID := "snap-" + w.suffix + "-" + row.Key
	id := toPgUUID(uuid.New())
	s.ordinal++
	tx, err := s.pool.Begin(s.ctx)
	if err != nil {
		s.t.Fatalf("begin seed of %q: %v", row.Key, err)
	}
	defer func() { _ = tx.Rollback(s.ctx) }()
	if _, err := tx.Exec(s.ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		s.t.Fatalf("declare the system actor: %v", err)
	}
	if _, err := tx.Exec(s.ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash, project_name, parent_session_id, git_branch, session_origin,
		                         published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'0.1.0',$10,$11,'aes-256-gcm-random-nonce-v1',1,$12,$13,$14,'main','user',
		        now() - ($15 * interval '1 minute'))
	`, id, owner, localID, title, visibility, provider, "m-"+row.Key, "blob/"+localID, int64(len(localID)),
		hex.EncodeToString(sha256.New().Sum([]byte(localID))[:16]), []byte("fixture-wrapped-data-key"),
		w.projectHash(row.Project), toPgText(row.ProjectName), parent, s.ordinal); err != nil {
		s.t.Fatalf("insert transcript %s: %v", row.Key, err)
	}
	if err := tx.Commit(s.ctx); err != nil {
		s.t.Fatalf("commit transcript %s: %v", row.Key, err)
	}
	w.transcripts[row.Key] = id
	w.localIDs[row.Key] = localID
	s.projects[row.Key] = row.Project
	s.visibility[row.Key] = visibility
	if row.Owner != "other" {
		s.parents[row.Key] = row.Parent
	}
	s.attempts(row.Key, w.group, row.Attempts)
	if len(row.OtherAttempts) > 0 {
		s.attempts(row.Key, s.second(), row.OtherAttempts)
	}
	if len(row.Attempts) > 0 {
		s.status[row.Key] = append([]string(nil), row.Attempts...)
	}
}

func (s *snapshotHarness) second() pgtype.UUID {
	if !s.otherGroup.Valid {
		s.otherGroup = s.w.secondCollective(s.t, s.ctx)
	}
	return s.otherGroup
}

func (s *snapshotHarness) attempts(key string, group pgtype.UUID, statuses []string) {
	s.t.Helper()
	for i, status := range statuses {
		if _, err := s.pool.Exec(s.ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
			VALUES ($1, $2, $3, $4)
		`, s.w.transcripts[key], group, i+1, status); err != nil {
			s.t.Fatalf("record attempt %d (%s) of %q: %v", i+1, status, key, err)
		}
	}
}

// ---------------------------------------------------------------------------
// The wire, decoded independently of the production types.
// ---------------------------------------------------------------------------

type snapshotWireRecord struct {
	ID                 string  `json:"id"`
	Visibility         string  `json:"visibility"`
	LatestStatus       *string `json:"latest_status"`
	ProjectHash        string  `json:"project_hash"`
	ParentTranscriptID *string `json:"parent_transcript_id"`
}

type snapshotWire struct {
	GroupID     string               `json:"group_id"`
	ProjectHash string               `json:"project_hash"`
	Records     []snapshotWireRecord `json:"records"`
	Count       int                  `json:"count"`
}

func decodeSnapshot(t *testing.T, rec *httptest.ResponseRecorder) snapshotWire {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("snapshot Content-Type = %q, want application/json", got)
	}
	var wire snapshotWire
	decoder := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		t.Fatalf("decode the snapshot: %v; body: %s", err, rec.Body.String())
	}
	if wire.Count != len(wire.Records) {
		t.Fatalf("count = %d but the response carries %d records", wire.Count, len(wire.Records))
	}
	return wire
}

// keysOf names each record by its fixture key and fails on an id the world
// never created, so an invented or foreign row is an error rather than noise.
func (s *snapshotHarness) keysOf(wire snapshotWire) []string {
	s.t.Helper()
	byID := map[string]string{}
	for key, id := range s.w.transcripts {
		byID[uuid.UUID(id.Bytes).String()] = key
	}
	keys := make([]string, 0, len(wire.Records))
	seen := map[string]bool{}
	for _, record := range wire.Records {
		key, ok := byID[record.ID]
		if !ok {
			s.t.Fatalf("the snapshot returned %s, which no fixture transcript owns", record.ID)
		}
		if seen[key] {
			s.t.Fatalf("the snapshot returned %q twice", key)
		}
		seen[key] = true
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *snapshotHarness) assertRecords(label string, wire snapshotWire) {
	s.t.Helper()
	byID := map[string]string{}
	for key, id := range s.w.transcripts {
		byID[uuid.UUID(id.Bytes).String()] = key
	}
	for _, record := range wire.Records {
		key := byID[record.ID]
		if want := s.w.projectHash(s.projects[key]); record.ProjectHash != want {
			s.t.Errorf("%s: %q reports project %s, want its OWN project %s", label, key, record.ProjectHash, want)
		}
		if record.Visibility != s.visibility[key] {
			s.t.Errorf("%s: %q reports visibility %q, want %q", label, key, record.Visibility, s.visibility[key])
		}
		switch attempts := s.status[key]; {
		case len(attempts) == 0 && record.LatestStatus != nil:
			s.t.Errorf("%s: %q has no attempt in this collective but reports latest_status %q", label, key, *record.LatestStatus)
		case len(attempts) > 0 && (record.LatestStatus == nil || *record.LatestStatus != attempts[len(attempts)-1]):
			s.t.Errorf("%s: %q reports latest_status %v, want %q", label, key, record.LatestStatus, attempts[len(attempts)-1])
		}
		parent := s.parents[key]
		switch {
		case parent == "" && record.ParentTranscriptID != nil:
			s.t.Errorf("%s: %q is a root but reports parent %q", label, key, *record.ParentTranscriptID)
		case parent != "" && (record.ParentTranscriptID == nil || *record.ParentTranscriptID != uuid.UUID(s.w.transcripts[parent].Bytes).String()):
			s.t.Errorf("%s: %q reports parent %v, want %q", label, key, record.ParentTranscriptID, parent)
		}
	}
}

// rawKeys returns the key set of every record object, to prove a record carries
// nothing beyond the compact fields.
func rawRecordKeys(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var raw struct {
		Records []map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, record := range raw.Records {
		for k := range record {
			keys[k] = true
		}
	}
	return keys
}

// ---------------------------------------------------------------------------
// Fixture-driven exactness and search
// ---------------------------------------------------------------------------

func TestContributionSnapshotFixtures(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, testCase := range loadSnapshotCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			s := newSnapshotHarness(t, ctx, pool, "snap-"+testCase.Name, nil)
			defer s.cleanup()
			s.seed(testCase.Transcripts)

			for _, request := range testCase.Requests {
				extra := url.Values{}
				switch {
				case request.QIDOf != "":
					extra.Set("q", uuid.UUID(s.w.transcripts[request.QIDOf].Bytes).String())
				case request.QIDPrefixOf != "":
					extra.Set("q", uuid.UUID(s.w.transcripts[request.QIDPrefixOf].Bytes).String()[:8])
				case request.Q != "":
					extra.Set("q", request.Q)
				}
				if request.Provider != "" {
					extra.Set("provider", request.Provider)
				}
				want := append([]string(nil), request.Want...)
				sort.Strings(want)

				rec := s.get("/collectives", s.snapshotQuery(request.Project, extra))
				wire := decodeSnapshot(t, rec)
				got := s.keysOf(wire)
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("%s: selection = %v, want exactly %v", request.Name, got, want)
				}
				if wire.GroupID != s.w.groupID() || wire.ProjectHash != s.w.projectHash(request.Project) {
					t.Errorf("%s: envelope names group %s project %s, want %s and %s", request.Name,
						wire.GroupID, wire.ProjectHash, s.w.groupID(), s.w.projectHash(request.Project))
				}
				if wire.Records == nil || !strings.Contains(rec.Body.String(), `"records":[`) {
					t.Errorf("%s: records must be an array, never null; body: %s", request.Name, rec.Body.String())
				}
				s.assertRecords(request.Name, wire)
				for key := range rawRecordKeys(t, rec.Body.Bytes()) {
					switch key {
					case "id", "visibility", "latest_status", "project_hash", "parent_transcript_id":
					default:
						t.Errorf("%s: a record carries %q, which is not a compact field", request.Name, key)
					}
				}
			}
		})
	}
}

// Both route prefixes serve the same bytes: the deprecated alias must not drift.
func TestContributionSnapshotBothPrefixes(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-prefixes", nil)
	defer s.cleanup()
	s.seed([]snapshotSeed{
		{Key: "r", Project: "p", Attempts: []string{"approved"}},
		{Key: "h", Project: "q", Parent: "r", Visibility: "private"},
	})
	canonical := s.get("/collectives", s.snapshotQuery("p", nil))
	legacy := s.get("/groups", s.snapshotQuery("p", nil))
	if canonical.Code != http.StatusOK || legacy.Code != http.StatusOK {
		t.Fatalf("statuses %d and %d, want 200 and 200; %s / %s", canonical.Code, legacy.Code, canonical.Body.String(), legacy.Body.String())
	}
	if canonical.Body.String() != legacy.Body.String() {
		t.Fatalf("the two prefixes disagree:\n%s\n%s", canonical.Body.String(), legacy.Body.String())
	}
	if got := s.keysOf(decodeSnapshot(t, canonical)); strings.Join(got, ",") != "h,r" {
		t.Fatalf("selection = %v, want [h r]", got)
	}
}

// ---------------------------------------------------------------------------
// Request validation, access and the views that must not change
// ---------------------------------------------------------------------------

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func TestContributionSnapshotRefusalsAndViews(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-refusals", nil)
	defer s.cleanup()
	s.seed([]snapshotSeed{{Key: "r", Project: "p", Attempts: []string{"pending"}}})
	hash := s.w.projectHash("p")

	for _, prefix := range []string{"/collectives", "/groups"} {
		t.Run(strings.TrimPrefix(prefix, "/"), func(t *testing.T) {
			missing := s.get(prefix, "view=snapshot")
			if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "project_hash") {
				t.Errorf("a snapshot without project_hash = %d %s, want 400 naming project_hash", missing.Code, missing.Body.String())
			}
			for name, query := range map[string]string{
				"malformed hash":  "view=snapshot&project_hash=not-a-hash",
				"uppercase hash":  "view=snapshot&project_hash=" + strings.ToUpper(hash),
				"repeated hash":   "view=snapshot&project_hash=" + hash + "&project_hash=" + hash,
				"repeated q":      "view=snapshot&project_hash=" + hash + "&q=a&q=b",
				"unknown param":   "view=snapshot&project_hash=" + hash + "&cursor=x",
				"retired snapcap": "view=snapshot&project_hash=" + hash + "&snapshot_limit=5",
			} {
				rec := s.get(prefix, query)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("%s: status %d, want 400; body %s", name, rec.Code, rec.Body.String())
				}
			}

			unknown := s.get(prefix, "view=everything")
			if unknown.Code != http.StatusBadRequest {
				t.Fatalf("an unknown view = %d, want 400; body %s", unknown.Code, unknown.Body.String())
			}
			for _, accepted := range []string{"grouped", "paged", "snapshot"} {
				if !strings.Contains(unknown.Body.String(), accepted) {
					t.Errorf("the unknown-view refusal does not name %q: %s", accepted, unknown.Body.String())
				}
			}

			// The flat response keeps its envelope.
			flat := s.get(prefix, "")
			if flat.Code != http.StatusOK {
				t.Fatalf("flat = %d: %s", flat.Code, flat.Body.String())
			}
			var flatKeys map[string]json.RawMessage
			if err := json.Unmarshal(flat.Body.Bytes(), &flatKeys); err != nil {
				t.Fatal(err)
			}
			if len(flatKeys) != 2 || flatKeys["group_id"] == nil || flatKeys["transcripts"] == nil {
				t.Errorf("the flat envelope changed: %s", flat.Body.String())
			}
			if explicit := s.get(prefix, "view="); explicit.Body.String() != flat.Body.String() {
				t.Errorf("an empty view is not the flat response:\n%s\n%s", explicit.Body.String(), flat.Body.String())
			}

			// The grouped envelope keeps its keys.
			grouped := s.get(prefix, "view=grouped&page=1&limit=5")
			if grouped.Code != http.StatusOK {
				t.Fatalf("grouped = %d: %s", grouped.Code, grouped.Body.String())
			}
			var groupedKeys map[string]json.RawMessage
			if err := json.Unmarshal(grouped.Body.Bytes(), &groupedKeys); err != nil {
				t.Fatal(err)
			}
			if groupedKeys["transcriptList"] == nil || groupedKeys["records"] != nil {
				t.Errorf("the grouped envelope changed: %s", grouped.Body.String())
			}
		})
	}

	t.Run("not a member and unknown collective answer alike", func(t *testing.T) {
		stranger := s.getAs(s.h, s.w.other, "stranger", s.w.groupID(), "/collectives", s.snapshotQuery("p", nil))
		flatStranger := s.getAs(s.h, s.w.other, "stranger", s.w.groupID(), "/collectives", "")
		unknown := s.getAs(s.h, s.w.member, s.w.memberName, uuid.NewString(), "/collectives", s.snapshotQuery("p", nil))
		if stranger.Code != flatStranger.Code {
			t.Errorf("a non-member gets %d from the snapshot but %d from the flat route", stranger.Code, flatStranger.Code)
		}
		if stranger.Code == http.StatusOK {
			t.Fatalf("a non-member was served: %s", stranger.Body.String())
		}
		if stranger.Body.String() != unknown.Body.String() && stranger.Code != unknown.Code {
			t.Errorf("a stranger and an unknown collective are distinguishable: %d %s / %d %s",
				stranger.Code, stranger.Body.String(), unknown.Code, unknown.Body.String())
		}
		if strings.Contains(stranger.Body.String(), uuid.UUID(s.w.transcripts["r"].Bytes).String()) {
			t.Errorf("a refusal leaked a transcript id: %s", stranger.Body.String())
		}
		anon := httptest.NewRecorder()
		s.router.ServeHTTP(anon, httptest.NewRequest(http.MethodGet,
			"/api/v1/collectives/"+s.w.groupID()+"/contributable?"+s.snapshotQuery("p", nil), nil))
		if anon.Code != http.StatusUnauthorized {
			t.Errorf("an anonymous snapshot = %d, want 401", anon.Code)
		}
		bad := s.getAs(s.h, s.w.member, s.w.memberName, "not-a-uuid", "/collectives", s.snapshotQuery("p", nil))
		if bad.Code != http.StatusBadRequest {
			t.Errorf("a malformed collective id = %d, want 400", bad.Code)
		}
	})

	t.Run("a project the caller does not own is an empty exact set", func(t *testing.T) {
		wire := decodeSnapshot(t, s.get("/collectives", "view=snapshot&project_hash="+strings.Repeat("a", 64)))
		if wire.Count != 0 || len(wire.Records) != 0 {
			t.Errorf("a foreign project returned %d records", len(wire.Records))
		}
	})
}

// ---------------------------------------------------------------------------
// One snapshot, one moment
// ---------------------------------------------------------------------------

func TestContributionSnapshotIsOneConsistentRead(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-consistent", nil)
	defer s.cleanup()
	s.seed([]snapshotSeed{
		{Key: "r1", Project: "p", Attempts: []string{"approved"}},
		{Key: "h1", Project: "p", Parent: "r1"},
		{Key: "r2", Project: "p", Title: "needle one"},
		{Key: "r3", Project: "q", Title: "needle two"},
		{Key: "named", Project: "p", ProjectName: "Before Rename"},
	})

	fired := 0
	s.h.contributionSnapshot.afterSnapshot = func(context.Context) {
		fired++
		// Everything a republish or a share can do, committed on other
		// connections while the snapshot is open.
		execAsSystem(t, ctx, pool, "UPDATE transcripts SET project_hash = $2 WHERE id = $1", s.w.transcripts["r2"], s.w.projectHash("q"))
		execAsSystem(t, ctx, pool, "UPDATE transcripts SET project_hash = $2 WHERE id = $1", s.w.transcripts["r3"], s.w.projectHash("p"))
		execAsSystem(t, ctx, pool, "UPDATE transcripts SET title = 'renamed away' WHERE id = $1", s.w.transcripts["r2"])
		execAsSystem(t, ctx, pool, "UPDATE transcripts SET project_name = 'After Rename' WHERE id = $1", s.w.transcripts["named"])
		s.insert(snapshotSeed{Key: "late", Project: "p", Title: "needle three"})
		if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,2,'revoked')`,
			s.w.transcripts["r1"], s.w.group); err != nil {
			t.Errorf("withdraw during the snapshot: %v", err)
		}
	}

	before := decodeSnapshot(t, s.get("/collectives", s.snapshotQuery("p", url.Values{"q": {"needle"}})))
	if fired != 1 {
		t.Fatalf("the barrier fired %d times, want exactly 1", fired)
	}
	// "needle" matched r2 only (and r3 is in project q): the set at the snapshot
	// moment, whatever committed afterwards.
	if got := s.keysOf(before); strings.Join(got, ",") != "r2" {
		t.Fatalf("the snapshot saw %v, want exactly [r2] as of its own moment", got)
	}

	s.h.contributionSnapshot.afterSnapshot = nil
	after := decodeSnapshot(t, s.get("/collectives", s.snapshotQuery("p", url.Values{"q": {"needle"}})))
	// Fresh read: r2 moved out and was retitled, r3 moved in, late arrived.
	if got := s.keysOf(after); strings.Join(got, ",") != "late,r3" {
		t.Fatalf("a fresh read saw %v, want [late r3]: the changes did not commit, so the control proves nothing", got)
	}

	// The same barrier with no q, and a status change in the records.
	s2 := newSnapshotHarness(t, ctx, pool, "snap-consistent-status", nil)
	defer s2.cleanup()
	s2.seed([]snapshotSeed{{Key: "r1", Project: "p", Attempts: []string{"approved"}}})
	s2.h.contributionSnapshot.afterSnapshot = func(context.Context) {
		if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,2,'revoked')`,
			s2.w.transcripts["r1"], s2.w.group); err != nil {
			t.Errorf("revoke during the snapshot: %v", err)
		}
	}
	wire := decodeSnapshot(t, s2.get("/collectives", s2.snapshotQuery("p", nil)))
	if len(wire.Records) != 1 || wire.Records[0].LatestStatus == nil || *wire.Records[0].LatestStatus != "approved" {
		t.Fatalf("a status written during the snapshot leaked into it: %+v", wire.Records)
	}
	s2.h.contributionSnapshot.afterSnapshot = nil
	wire = decodeSnapshot(t, s2.get("/collectives", s2.snapshotQuery("p", nil)))
	if wire.Records[0].LatestStatus == nil || *wire.Records[0].LatestStatus != "revoked" {
		t.Fatalf("a fresh read must see the revocation, got %+v", wire.Records)
	}
}

// ---------------------------------------------------------------------------
// No cap
// ---------------------------------------------------------------------------

func TestContributionSnapshotHasNoRecordOrByteCap(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds sixty thousand sessions")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-nocap", nil)
	defer s.cleanup()

	// Past both of the retired bounds: 50,000 records and 8 MiB of body.
	const sessions = 60000
	execAsSystem(t, ctx, pool, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash, git_branch, session_origin, published_at)
		SELECT gen_random_uuid(), $1, 'bulk-' || $3 || '-' || n, 'bulk ' || n, 'shared', 'claude-code', 'm', 'blob/' || n, 1,
		       '0.1.0', encode(sha256(convert_to($3 || n::text, 'UTF8')), 'hex'), '\x00'::bytea, 'aes-256-gcm-random-nonce-v1', 1,
		       $2, 'main', 'user', now() - (n * interval '1 second')
		FROM generate_series(1, $4::int) AS n`,
		s.w.member, s.w.projectHash("big"), s.w.suffix, sessions)
	// A bulk load is followed by a statistics refresh in production too
	// (autovacuum); without it the planner sees an empty table.
	execAsSystem(t, ctx, pool, `ANALYZE transcripts`)

	start := time.Now()
	rec := s.get("/collectives", s.snapshotQuery("big", nil))
	t.Logf("served %d bytes in %s", rec.Body.Len(), time.Since(start))
	wire := decodeSnapshot(t, rec)
	if wire.Count != sessions || len(wire.Records) != sessions {
		t.Fatalf("count %d, records %d, want %d each", wire.Count, len(wire.Records), sessions)
	}
	if rec.Body.Len() <= 8<<20 {
		t.Fatalf("the body is %d bytes; the fixture must exceed 8 MiB to prove there is no byte cap", rec.Body.Len())
	}
	seen := make(map[string]bool, sessions)
	for _, record := range wire.Records {
		if seen[record.ID] {
			t.Fatalf("duplicate record %s", record.ID)
		}
		seen[record.ID] = true
	}
}

// ---------------------------------------------------------------------------
// Refusals: timeout and busy
// ---------------------------------------------------------------------------

func decodeRefusal(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) errorBody {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the refusal: %v; body %s", err, rec.Body.String())
	}
	if body.Code != wantCode || body.Error == "" {
		t.Fatalf("refusal = %+v, want code %q with a message", body, wantCode)
	}
	if strings.Contains(rec.Body.String(), `"records"`) {
		t.Fatalf("a refusal carries records: %s", rec.Body.String())
	}
	return body
}

func TestContributionSnapshotTimeout(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-timeout", &config.Config{ContributionSnapshotTimeout: 300 * time.Millisecond})
	defer s.cleanup()
	s.seed([]snapshotSeed{{Key: "r", Project: "p"}})

	s.h.contributionSnapshot.afterSnapshot = func(ctx context.Context) { <-ctx.Done() }
	body := decodeRefusal(t, s.get("/collectives", s.snapshotQuery("p", nil)), "snapshot_timeout")
	if !strings.Contains(body.Error, "retry") && !strings.Contains(body.Error, "Retry") {
		t.Errorf("the timeout refusal gives no way forward: %q", body.Error)
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("a timed-out snapshot left %d connections acquired", got)
	}

	// The refusal is not sticky: the next request is served.
	s.h.contributionSnapshot.afterSnapshot = nil
	if wire := decodeSnapshot(t, s.get("/collectives", s.snapshotQuery("p", nil))); wire.Count != 1 {
		t.Fatalf("recovery count = %d, want 1", wire.Count)
	}
}

type heldSnapshot struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHeldSnapshot() *heldSnapshot {
	return &heldSnapshot{entered: make(chan struct{}), release: make(chan struct{})}
}

func (h *heldSnapshot) hook(context.Context) {
	h.once.Do(func() { close(h.entered) })
	<-h.release
}

func TestContributionSnapshotBusy(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	t.Run("the same viewer", func(t *testing.T) {
		s := newSnapshotHarness(t, ctx, pool, "snap-busy-viewer", nil)
		defer s.cleanup()
		s.seed([]snapshotSeed{{Key: "r", Project: "p"}})
		s.h.contributionSnapshot.queueWait = 50 * time.Millisecond
		held := newHeldSnapshot()
		s.h.contributionSnapshot.afterSnapshot = held.hook

		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- s.get("/collectives", s.snapshotQuery("p", nil)) }()
		<-held.entered

		decodeRefusal(t, s.get("/collectives", s.snapshotQuery("p", nil)), "snapshot_busy")
		close(held.release)
		if wire := decodeSnapshot(t, <-done); wire.Count != 1 {
			t.Fatalf("the held snapshot finished with %d records, want 1", wire.Count)
		}
		// The slot is free again.
		if wire := decodeSnapshot(t, s.get("/collectives", s.snapshotQuery("p", nil))); wire.Count != 1 {
			t.Fatalf("after the slot freed: %d records, want 1", wire.Count)
		}
	})

	t.Run("the instance bound across viewers", func(t *testing.T) {
		s := newSnapshotHarness(t, ctx, pool, "snap-busy-instance", &config.Config{ContributionSnapshotMaxConcurrent: 1})
		defer s.cleanup()
		s.seed([]snapshotSeed{{Key: "r", Project: "p"}})
		shareAddMember(t, ctx, pool, s.w.group, s.w.other, "member")
		s.h.contributionSnapshot.queueWait = 50 * time.Millisecond
		held := newHeldSnapshot()
		s.h.contributionSnapshot.afterSnapshot = held.hook

		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- s.get("/collectives", s.snapshotQuery("p", nil)) }()
		<-held.entered

		// Another viewer's own slot is free; the instance bound is what refuses.
		second := s.getAs(s.h, s.w.other, "other", s.w.groupID(), "/collectives", s.snapshotQuery("p", nil))
		decodeRefusal(t, second, "snapshot_busy")
		close(held.release)
		decodeSnapshot(t, <-done)
		s.h.contributionSnapshot.afterSnapshot = nil
		third := s.getAs(s.h, s.w.other, "other", s.w.groupID(), "/collectives", s.snapshotQuery("p", nil))
		if wire := decodeSnapshot(t, third); wire.Count != 0 {
			// The stranger owns no transcripts, so the exact set is empty.
			t.Fatalf("another viewer's snapshot returned %d records, want 0", wire.Count)
		}
	})
}

// A supported one-connection pool is served, one snapshot at a time, and never
// deadlocks on a second connection it cannot have.
func TestContributionSnapshotOneConnectionPool(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-one-conn", nil)
	defer s.cleanup()
	s.seed([]snapshotSeed{
		{Key: "r", Project: "p", ProjectName: "Single Connection Project"},
		{Key: "h", Project: "q", Parent: "r"},
	})

	cfg, err := pgxpool.ParseConfig(pullTestDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	h := New(&config.Config{JWTSecret: snapshotTestSecret}, one, nil)
	h.contributionSnapshot.queueWait = 100 * time.Millisecond

	// A q that needs project display names exercises every read the snapshot
	// can make; all of them must ride the one transaction.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- s.getAs(h, s.w.member, s.w.memberName, s.w.groupID(), "/collectives",
			s.snapshotQuery("p", url.Values{"q": {"single connection"}}))
	}()
	select {
	case rec := <-done:
		if wire := decodeSnapshot(t, rec); wire.Count != 2 {
			t.Fatalf("count = %d, want 2", wire.Count)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the snapshot deadlocked on a one-connection pool")
	}

	// While one snapshot holds the only connection, a second request waits for
	// it behind the pool (even its membership check needs a connection) and is
	// served once the first finishes; neither deadlocks and neither is refused.
	held := newHeldSnapshot()
	h.contributionSnapshot.afterSnapshot = held.hook
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- s.getAs(h, s.w.member, s.w.memberName, s.w.groupID(), "/collectives", s.snapshotQuery("p", nil))
	}()
	<-held.entered
	go func() {
		second <- s.getAs(h, s.w.member, s.w.memberName, s.w.groupID(), "/collectives", s.snapshotQuery("p", nil))
	}()
	close(held.release)
	decodeSnapshot(t, <-done)
	decodeSnapshot(t, <-second)
	h.contributionSnapshot.afterSnapshot = nil
	if wire := decodeSnapshot(t, s.getAs(h, s.w.member, s.w.memberName, s.w.groupID(), "/collectives", s.snapshotQuery("p", nil))); wire.Count != 2 {
		t.Fatalf("recovery count = %d, want 2", wire.Count)
	}
}

// ---------------------------------------------------------------------------
// The served contract
// ---------------------------------------------------------------------------

// snapshotContractSchema compiles a component of the served contract that
// declares the snapshot, or skips when the pinned module does not carry it yet.
func snapshotContractSchema(t *testing.T, component string) *jsonschema.Schema {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/peasant-labs/schema").Output()
	if err != nil {
		t.Skipf("cannot locate the schema module: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(strings.TrimSpace(string(out)), "generated", "village-api-*.json"))
	sort.Strings(matches)
	for i := len(matches) - 1; i >= 0; i-- {
		raw, err := os.ReadFile(matches[i])
		if err != nil || !bytes.Contains(raw, []byte(component)) {
			continue
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("village-api.json", bytes.NewReader(raw)); err != nil {
			t.Fatalf("load %s: %v", matches[i], err)
		}
		schema, err := compiler.Compile("village-api.json#/components/schemas/" + component)
		if err != nil {
			t.Fatalf("compile %s of %s: %v", component, matches[i], err)
		}
		return schema
	}
	t.Skipf("no served contract declares %s; re-pin the schema module to a release that does", component)
	return nil
}

func TestContributionSnapshotMatchesServedContract(t *testing.T) {
	response := snapshotContractSchema(t, "SchemaVillageContributionSnapshotResponse")
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	s := newSnapshotHarness(t, ctx, pool, "snap-contract", &config.Config{ContributionSnapshotMaxConcurrent: 1})
	defer s.cleanup()
	s.seed([]snapshotSeed{
		{Key: "root", Project: "p", Attempts: []string{"approved"}},
		{Key: "helper", Project: "q", Parent: "root", Visibility: "public", Attempts: []string{"retracted"}},
		{Key: "bare", Project: "p", Visibility: "private"},
	})

	for _, query := range []string{s.snapshotQuery("p", nil), s.snapshotQuery("nowhere", nil)} {
		rec := s.get("/collectives", query)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var document any
		if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if err := response.Validate(document); err != nil {
			t.Errorf("the snapshot does not satisfy the served contract: %v\n%s", err, rec.Body.String())
		}
	}

	// A refusal satisfies the contract's error envelope, code included.
	refusal := snapshotContractSchema(t, "SchemaVillageErrorResponse")
	s.h.contributionSnapshot.queueWait = 20 * time.Millisecond
	held := newHeldSnapshot()
	s.h.contributionSnapshot.afterSnapshot = held.hook
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- s.get("/collectives", s.snapshotQuery("p", nil)) }()
	<-held.entered
	busy := s.get("/collectives", s.snapshotQuery("p", nil))
	close(held.release)
	<-done
	decodeRefusal(t, busy, "snapshot_busy")
	var document any
	if err := json.Unmarshal(busy.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := refusal.Validate(document); err != nil {
		t.Errorf("the busy refusal does not satisfy the served error envelope: %v\n%s", err, busy.Body.String())
	}
}
