//go:build integration

package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/auth"
)

//go:embed testdata/absentee_reads.yaml
var absenteeReadYAML []byte

type absenteeRoute struct {
	Method string `yaml:"method"`
	Suffix string `yaml:"suffix"`
}

func testSharedRetraction(t *testing.T, w *orderingWorld, pool *pgxpool.Pool, mux *chi.Mux, name, id, missing, retractedCopy string, meta absenteeRoute,
	read func(string, string, absenteeRoute, *AuthUser) *httptest.ResponseRecorder,
	compare404 func(string, absenteeRoute, *AuthUser),
	check410 func(*httptest.ResponseRecorder, string)) {
	t.Helper()
	groupOwner := pullInsertUser(t, w.ctx, pool, int64(720000+len(id))+int64(w.owner.ID[0])*1000, "audience-"+uuid.NewString()[:8])
	memberID := pullInsertUser(t, w.ctx, pool, int64(820000+len(id))+int64(w.owner.ID[0])*1000, "audience-"+uuid.NewString()[:8])
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, groupOwner, memberID) })
	group := pullInsertGroup(t, w.ctx, pool, groupOwner, "audience-"+uuid.NewString()[:8])
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM groups WHERE id=$1", group) })
	role := "member"
	if name == "read-retracted-shared-role-denied-404" {
		role = "pending"
	}
	pullAddMember(t, w.ctx, pool, group, memberID, role)
	if name == "read-retracted-shared-owner-account-deleted" {
		pullAddMember(t, w.ctx, pool, group, w.owner.PgID(), "member")
	}
	pullShare(t, w.ctx, pool, w.id, group, "approved")
	execAsSystem(t, w.ctx, pool, "UPDATE transcripts SET visibility='shared' WHERE id=$1", w.id)
	member := &AuthUser{ID: uuidFromPg(memberID)}
	if name == "read-retracted-shared-departed-member-404" {
		if _, err := pool.Exec(w.ctx, "DELETE FROM group_members WHERE group_id=$1 AND user_id=$2", group, memberID); err != nil {
			t.Fatal(err)
		}
	}
	if name == "read-retracted-shared-owner-account-deleted" {
		token, err := auth.CreateToken(w.handler.cfg.JWTSecret, w.owner.ID, w.owner.Username)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodDelete, "/auth/me", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, w.owner))
		out := httptest.NewRecorder()
		w.handler.DeleteAccount(out, req)
		if out.Code != 200 {
			t.Fatalf("account deletion: %d %s", out.Code, out.Body.String())
		}
		check410(read("/transcripts/", id, meta, member), retractedCopy)
		compare404("/transcripts/", meta, nil)
		request := func(target string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, "/transcripts/"+target, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			w.handler.AuthOptional(mux).ServeHTTP(response, r)
			return response
		}
		got, absent := request(id), request(missing)
		if got.Code != 404 || absent.Code != 404 || got.Body.String() != absent.Body.String() || !reflect.DeepEqual(got.Header(), absent.Header()) {
			t.Fatalf("erased owner: %d %v %q, missing %d %v %q", got.Code, got.Header(), got.Body, absent.Code, absent.Header(), absent.Body)
		}
		return
	}
	if got := w.deleteTranscript(); got.Code != 200 && got.Code != 204 {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	switch name {
	case "read-retracted-shared-member-410":
		check410(read("/transcripts/", id, meta, member), retractedCopy)
	case "read-retracted-shared-nonmember-404":
		compare404("/transcripts/", meta, w.owner)
	case "read-retracted-shared-role-denied-404", "read-retracted-shared-departed-member-404":
		compare404("/transcripts/", meta, member)
	default:
		t.Fatalf("unknown case %q", name)
	}
}

var requiredAbsenteeReads = []string{
	"read-unavailable-410-after-access", "read-unavailable-denied-404",
	"read-unavailable-metadata-unchanged", "cache-not-served-for-unavailable",
	"read-retracted-public-410", "read-retracted-shared-member-410",
	"read-retracted-shared-owner-account-deleted", "read-retracted-shared-nonmember-404",
	"read-retracted-shared-role-denied-404", "read-retracted-shared-departed-member-404",
	"read-retracted-private-stranger-404", "read-retracted-content-routes-404",
	"pull-retracted-stays-404", "pull-unavailable-stays-404",
}

func TestAbsenteeReads(t *testing.T) {
	fixture, err := decodeFixtureDocument[struct {
		RetractedCopy   string `yaml:"retracted_copy"`
		UnavailableCopy string `yaml:"unavailable_copy"`
		Cases           []struct {
			Name string `yaml:"name"`
		} `yaml:"cases"`
		ContentRoutes []absenteeRoute `yaml:"content_routes"`
		PullRoutes    []absenteeRoute `yaml:"pull_routes"`
	}](absenteeReadYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := make(map[string]bool)
	for _, name := range requiredAbsenteeReads {
		required[name] = true
	}
	for _, c := range fixture.Cases {
		if !required[c.Name] {
			t.Fatalf("unexpected or duplicate case %q", c.Name)
		}
		delete(required, c.Name)
	}
	if len(required) != 0 {
		t.Fatalf("missing cases: %v", required)
	}
	requireRoutes := func(routes []absenteeRoute, expected map[absenteeRoute]bool) {
		t.Helper()
		for _, route := range routes {
			if !expected[route] {
				t.Fatalf("unexpected or duplicate route: %+v", route)
			}
			delete(expected, route)
		}
		if len(expected) != 0 {
			t.Fatalf("missing routes: %v", expected)
		}
	}
	requireRoutes(fixture.ContentRoutes, map[absenteeRoute]bool{
		{"GET", "/content"}: true, {"GET", "/segments?v=2"}: true,
		{"GET", "/segments/0?v=2"}: true, {"GET", "/window?v=2"}: true,
		{"GET", "/header"}: true, {"POST", "/segments/locate"}: true,
	})
	requireRoutes(fixture.PullRoutes, map[absenteeRoute]bool{
		{"GET", ""}: true, {"GET", "/content"}: true,
		{"GET", "/annotations"}: true, {"GET", "/publish-metadata"}: true,
	})
	content, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for n, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 61000+n, []byte(content.Content))
			w.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
			counter := &decryptionCountingStore{TranscriptBlobStore: w.handler.blobs}
			w.handler.blobs = counter
			mux := chi.NewRouter()
			mux.Get("/transcripts/{id}", w.handler.GetTranscript)
			mux.Get("/transcripts/{id}/content", w.handler.GetTranscriptContent)
			mux.Get("/transcripts/{id}/segments", w.handler.GetTranscriptSegments)
			mux.Get("/transcripts/{id}/segments/{index}", w.handler.GetTranscriptSegment)
			mux.Get("/transcripts/{id}/window", w.handler.GetTranscriptWindow)
			mux.Get("/transcripts/{id}/header", w.handler.GetTranscriptHeader)
			mux.Post("/transcripts/{id}/segments/locate", w.handler.LocateTranscriptEntry)
			mux.Get("/pull/transcripts/{id}", w.handler.GetPullTranscript)
			mux.Get("/pull/transcripts/{id}/content", w.handler.GetPullTranscriptContent)
			mux.Get("/pull/transcripts/{id}/annotations", w.handler.GetPullTranscriptAnnotations)
			mux.Get("/pull/transcripts/{id}/publish-metadata", w.handler.GetPullTranscriptPublishMetadata)
			id := uuidFromPg(w.id).String()
			missing := uuid.NewString()
			read := func(prefix, target string, route absenteeRoute, viewer *AuthUser) *httptest.ResponseRecorder {
				req := httptest.NewRequest(route.Method, prefix+target+route.Suffix, strings.NewReader("{}"))
				if viewer != nil {
					req = req.WithContext(context.WithValue(req.Context(), UserContextKey, viewer))
				}
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, req)
				return out
			}
			compare404 := func(prefix string, route absenteeRoute, viewer *AuthUser) {
				t.Helper()
				got, absent := read(prefix, id, route, viewer), read(prefix, missing, route, viewer)
				if got.Code != 404 || absent.Code != 404 || got.Body.String() != absent.Body.String() || !reflect.DeepEqual(got.Header(), absent.Header()) {
					t.Fatalf("404 differs at %s: got %d %v %q, missing %d %v %q", route.Suffix, got.Code, got.Header(), got.Body.String(), absent.Code, absent.Header(), absent.Body.String())
				}
			}
			check410 := func(out *httptest.ResponseRecorder, copy string) {
				t.Helper()
				if out.Code != 410 || out.Header().Get("Cache-Control") != "private, no-store" || decodeError(t, out.Body.Bytes()) != copy {
					t.Fatalf("unexpected refusal: %d %v %s", out.Code, out.Header(), out.Body.String())
				}
			}
			meta := absenteeRoute{Method: http.MethodGet}
			unavailable := func() {
				t.Helper()
				conn, err := pool.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Release()
				if _, err = conn.Exec(context.Background(), "UPDATE transcripts SET content_state='unavailable' WHERE id=$1", w.id); err != nil {
					t.Fatal(err)
				}
			}
			retract := func() {
				t.Helper()
				if got := w.deleteTranscript(); got.Code != 200 && got.Code != 204 {
					t.Fatalf("delete: %d %s", got.Code, got.Body.String())
				}
			}
			switch c.Name {
			case "read-unavailable-410-after-access":
				unavailable()
				for _, route := range fixture.ContentRoutes {
					before := counter.count()
					check410(read("/transcripts/", id, route, w.owner), fixture.UnavailableCopy)
					if counter.count() != before {
						t.Fatal("unavailable read accessed object")
					}
				}
			case "read-unavailable-denied-404":
				if r := transcriptVisibilityPatch(t, w.handler, w.owner, w.id, dbVisibilityPrivate); r.Code != 200 {
					t.Fatal(r.Body.String())
				}
				unavailable()
				for _, route := range fixture.ContentRoutes {
					compare404("/transcripts/", route, nil)
				}
			case "read-unavailable-metadata-unchanged":
				before := read("/transcripts/", id, meta, w.owner)
				unavailable()
				after := read("/transcripts/", id, meta, w.owner)
				if before.Code != 200 || after.Code != 200 || before.Body.String() != after.Body.String() || !reflect.DeepEqual(before.Header(), after.Header()) {
					t.Fatalf("metadata changed: %s %s", before.Body, after.Body)
				}
			case "cache-not-served-for-unavailable":
				route := absenteeRoute{Method: "GET", Suffix: "/segments?v=2"}
				warm := read("/transcripts/", id, route, w.owner)
				if warm.Code != 200 || counter.count() == 0 {
					t.Fatalf("cache warm failed: %d %s", warm.Code, warm.Body)
				}
				unavailable()
				before := counter.count()
				got := read("/transcripts/", id, route, w.owner)
				check410(got, fixture.UnavailableCopy)
				if counter.count() != before || got.Body.String() == warm.Body.String() {
					t.Fatal("cached content or object was served")
				}
			case "read-retracted-public-410":
				retract()
				check410(read("/transcripts/", id, meta, nil), fixture.RetractedCopy)
			case "read-retracted-private-stranger-404":
				if r := transcriptVisibilityPatch(t, w.handler, w.owner, w.id, dbVisibilityPrivate); r.Code != 200 {
					t.Fatal(r.Body.String())
				}
				retract()
				compare404("/transcripts/", meta, nil)
			case "read-retracted-content-routes-404":
				retract()
				for _, route := range fixture.ContentRoutes {
					compare404("/transcripts/", route, w.owner)
				}
			case "pull-retracted-stays-404":
				retract()
				for _, route := range fixture.PullRoutes {
					compare404("/pull/transcripts/", route, w.owner)
				}
			case "pull-unavailable-stays-404":
				unavailable()
				for _, route := range fixture.PullRoutes {
					compare404("/pull/transcripts/", route, w.owner)
				}
			default:
				testSharedRetraction(t, w, pool, mux, c.Name, id, missing, fixture.RetractedCopy, meta, read, compare404, check410)
			}
		})
	}
}
