//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/pull-publish-metadata.yaml
var pullPublishMetadataYAML []byte

type pullPublishMetadataCase struct {
	Name            string   `yaml:"name"`
	Why             string   `yaml:"why"`
	Caller          string   `yaml:"caller"`
	Publish         string   `yaml:"publish"`
	Visibility      string   `yaml:"visibility"`
	Share           string   `yaml:"share"`
	Fault           string   `yaml:"fault"`
	Target          string   `yaml:"target"`
	PullContract    *string  `yaml:"pull_contract"`
	WantStatus      int      `yaml:"want_status"`
	WantMetadata    bool     `yaml:"want_metadata"`
	ReadsTranscript bool     `yaml:"reads_transcript"`
	CompareNotFound []string `yaml:"compare_not_found"`
}

type pullPublishMetadataFile struct {
	Sentinel string                    `yaml:"sentinel"`
	Cases    []pullPublishMetadataCase `yaml:"cases"`
}

var requiredPullPublishMetadataCases = []string{
	"owner_reads_it",
	"collective_owner_pending_reads_it",
	"collective_owner_approved_reads_it",
	"collective_owner_rejected_404",
	"other_member_of_that_collective_404",
	"public_reader_404",
	"signed_out_401",
	"none_stored_404",
	"missing_transcript_404",
	"tampered_stored_object_500",
	"below_pull_contract_refused",
	"owner_anonymous_to_caller_404",
	"not_found_bodies_identical",
}

func loadPullPublishMetadataFixture(t *testing.T) pullPublishMetadataFile {
	t.Helper()
	var file pullPublishMetadataFile
	decoder := yaml.NewDecoder(bytes.NewReader(pullPublishMetadataYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("load the publish metadata pull fixture: %v", err)
	}
	if strings.TrimSpace(file.Sentinel) == "" {
		t.Fatal("the publish metadata pull fixture must name a sentinel")
	}
	present := map[string]bool{}
	for _, c := range file.Cases {
		if c.Name == "" || c.Why == "" || present[c.Name] {
			t.Fatalf("publish metadata pull case %+v must carry a unique name and a reason", c)
		}
		if len(c.CompareNotFound) == 0 && c.WantStatus == 0 {
			t.Fatalf("publish metadata pull case %q names no status", c.Name)
		}
		present[c.Name] = true
	}
	for _, name := range requiredPullPublishMetadataCases {
		if !present[name] {
			t.Fatalf("the publish metadata pull fixture no longer contains %q; restore it", name)
		}
	}
	return file
}

// anonymousOwnerQuerier answers the public-owner rule as anonymous for one
// person, whoever the viewer is.
type anonymousOwnerQuerier struct {
	Querier
	hidden pgtype.UUID
}

func (q anonymousOwnerQuerier) ListPublicOwnersForViewer(ctx context.Context, arg sqlc.ListPublicOwnersForViewerParams) ([]sqlc.ListPublicOwnersForViewerRow, error) {
	rows, err := q.Querier.ListPublicOwnersForViewer(ctx, arg)
	for i := range rows {
		if rows[i].ID == q.hidden {
			rows[i].ProfileVisible = false
		}
	}
	return rows, err
}

func pullPublishMetadataRequest(localID, sentinel string, content []byte) schema.AuthoritativePublishRequest {
	req := publishEvidenceRequest(localID, content, "peasant 1.4.0")
	req.Source.FilePath = "/fixture/" + sentinel + "/session.jsonl"
	req.Model.Model = schema.ModelID("model-" + sentinel)
	return req
}

func TestPullPublishMetadata(t *testing.T) {
	file := loadPullPublishMetadataFixture(t)
	pool := govTestPool(t)
	defer pool.Close()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	rawObjects := publishEvidenceObjects(t)
	blobs := publishEvidenceStore(t, rawObjects)
	cfg := minimalConfig()

	notFoundBodies := map[string][]byte{}
	for ordinal, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if len(c.CompareNotFound) > 0 {
				var first []byte
				for i, name := range c.CompareNotFound {
					body, ok := notFoundBodies[name]
					if !ok {
						t.Fatalf("%q produced no 404 body to compare; it must run first and answer 404", name)
					}
					if i == 0 {
						first = body
						continue
					}
					if !bytes.Equal(body, first) {
						t.Fatalf("404 body of %q = %q, want the same bytes as %q: %q", name, body, c.CompareNotFound[0], first)
					}
				}
				return
			}

			ctx := context.Background()
			base := int64(995500 + ordinal*10)
			suffix := uuid.NewString()[:8]
			owner := pullInsertUser(t, ctx, pool, base+1, "pmeta-owner-"+suffix)
			collectiveOwner := pullInsertUser(t, ctx, pool, base+2, "pmeta-cowner-"+suffix)
			member := pullInsertUser(t, ctx, pool, base+3, "pmeta-member-"+suffix)
			stranger := pullInsertUser(t, ctx, pool, base+4, "pmeta-stranger-"+suffix)
			users := []pgtype.UUID{owner, collectiveOwner, member, stranger}
			defer func() {
				_, _ = pool.Exec(context.Background(), "DELETE FROM groups WHERE created_by = ANY($1)", users)
				cleanupOwners(t, context.Background(), pool, users...)
			}()

			queries := sqlc.New(pool)
			var q Querier = queries
			if c.Fault == "owner_anonymous_to_caller" {
				q = anonymousOwnerQuerier{Querier: queries, hidden: owner}
			}
			h := &Handler{cfg: cfg, pool: pool, queries: queries, blobs: blobs, titles: titles}

			// Publish as the owner through the production handler.
			localID := uuid.NewString()
			content := []byte(`[{"role":"user","content":"pull publish metadata ` + suffix + `"}]`)
			ownerUser := &AuthUser{ID: uuidFromPg(owner), Username: "pmeta-owner-" + suffix}
			var published []byte
			switch c.Publish {
			case "", "authoritative":
				req := pullPublishMetadataRequest(localID, file.Sentinel, content)
				if published, err = json.Marshal(req); err != nil {
					t.Fatal(err)
				}
				if w := publishEvidencePost(t, h, ownerUser, req, content); w.Code != http.StatusCreated {
					t.Fatalf("publish: %d %s", w.Code, w.Body.String())
				}
			case "legacy":
				req := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.SourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "evidence"}}
				if w := publishEvidencePost(t, h, ownerUser, req, content); w.Code != http.StatusCreated {
					t.Fatalf("legacy publish: %d %s", w.Code, w.Body.String())
				}
			default:
				t.Fatalf("unknown publish %q", c.Publish)
			}
			transcriptID, err := queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: owner, LocalID: localID})
			if err != nil {
				t.Fatalf("load the published transcript id: %v", err)
			}
			row, err := queries.GetTranscriptByID(ctx, transcriptID)
			if err != nil {
				t.Fatalf("load the published transcript: %v", err)
			}
			defer func() {
				_ = rawObjects.Delete(context.Background(), storage.ObjectKey(row.BlobKey))
				if row.PublishMetadataKey.Valid {
					_ = rawObjects.Delete(context.Background(), storage.ObjectKey(row.PublishMetadataKey.String))
				}
			}()
			if (c.Publish == "legacy") == row.PublishMetadataKey.Valid {
				t.Fatalf("publish %q stored metadata object %v", c.Publish, row.PublishMetadataKey)
			}

			switch c.Visibility {
			case "", "private":
			case "public":
				execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility = 'public' WHERE id = $1", transcriptID)
			default:
				t.Fatalf("unknown visibility %q", c.Visibility)
			}
			if c.Share != "" {
				group := pullInsertGroup(t, ctx, pool, collectiveOwner, "pmeta-grp-"+suffix)
				pullAddMember(t, ctx, pool, group, collectiveOwner, "owner")
				pullAddMember(t, ctx, pool, group, member, "member")
				if c.Share == "approved" {
					execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility = 'shared' WHERE id = $1", transcriptID)
				}
				pullShare(t, ctx, pool, transcriptID, group, c.Share)
			}

			switch c.Fault {
			case "", "owner_anonymous_to_caller":
			case "tamper_stored_object":
				key := storage.ObjectKey(row.PublishMetadataKey.String)
				ciphertext, contentType, err := rawObjects.Get(ctx, key)
				if err != nil {
					t.Fatalf("read the stored metadata ciphertext: %v", err)
				}
				ciphertext[len(ciphertext)-1] ^= 0xff
				if err := rawObjects.Put(ctx, key, ciphertext, contentType); err != nil {
					t.Fatalf("overwrite the stored metadata ciphertext: %v", err)
				}
			default:
				t.Fatalf("unknown fault %q", c.Fault)
			}
			h.queries = q

			id := uuidFromPg(transcriptID).String()
			switch c.Target {
			case "":
			case "missing":
				id = uuid.NewString()
			case "invalid":
				id = "not-a-transcript-id"
			default:
				t.Fatalf("unknown target %q", c.Target)
			}

			callers := map[string]pgtype.UUID{"owner": owner, "collective_owner": collectiveOwner, "member": member, "stranger": stranger}
			if c.ReadsTranscript {
				shaped, err := queries.GetTranscriptByID(ctx, transcriptID)
				if err != nil {
					t.Fatalf("reload the transcript: %v", err)
				}
				readable, err := h.transcriptViewAccess(ctx, &AuthUser{ID: uuidFromPg(callers[c.Caller]), Username: c.Caller}, shaped)
				if err != nil || !readable {
					t.Fatalf("precondition: %s must be able to read the transcript itself (readable=%v err=%v)", c.Caller, readable, err)
				}
			}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+id+"/publish-metadata", nil)
			if c.Caller != "signed_out" {
				caller, ok := callers[c.Caller]
				if !ok {
					t.Fatalf("unknown caller %q", c.Caller)
				}
				token, err := auth.CreateToken(cfg.JWTSecret, uuidFromPg(caller), c.Caller)
				if err != nil {
					t.Fatal(err)
				}
				r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
			}
			contract := schema.PublicOwnerPullContractVersion.String()
			if c.PullContract != nil {
				contract = *c.PullContract
			}
			r.Header.Set(schema.PullContractHeader, contract)

			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			router := chi.NewRouter()
			router.With(h.AuthRequired).Get("/api/v1/pull/transcripts/{id}/publish-metadata", h.GetPullTranscriptPublishMetadata)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			slog.SetDefault(previous)

			if w.Code != c.WantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, c.WantStatus, w.Body.String())
			}
			if etag := w.Header().Get("ETag"); etag != "" {
				t.Fatalf("the route sent ETag %q; it serves no conditional GET", etag)
			}
			if c.WantStatus != http.StatusUnauthorized {
				if vary := w.Header().Values("Vary"); len(vary) != 1 || vary[0] != schema.PullContractHeader {
					t.Fatalf("Vary = %v, want exactly %s as the pull content route sends", vary, schema.PullContractHeader)
				}
			}
			switch c.WantStatus {
			case http.StatusOK:
				if got := w.Header().Get(schema.PullContractHeader); got != contract {
					t.Fatalf("served pull contract %q, want %q", got, contract)
				}
				if ct := w.Header().Get("Content-Type"); ct != "application/json" {
					t.Fatalf("Content-Type = %q, want application/json", ct)
				}
				if !c.WantMetadata || !bytes.Equal(w.Body.Bytes(), published) {
					t.Fatalf("body:\n%s\nwant json.Marshal of the published request:\n%s", w.Body.Bytes(), published)
				}
				if _, err := schema.DecodeAuthoritativePublishMetadataRaw(w.Body.Bytes()); err != nil {
					t.Fatalf("the served body is not a valid authoritative publish request: %v", err)
				}
			case http.StatusNotFound:
				var body schema.VillageErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error != "Transcript publish metadata not found" {
					t.Fatalf("404 body = %s (%v), want the shared not-found message", w.Body.String(), err)
				}
				notFoundBodies[c.Name] = append([]byte(nil), w.Body.Bytes()...)
			case http.StatusInternalServerError:
				var body schema.VillageErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error != "Stored publish metadata failed verification; nothing was served" {
					t.Fatalf("500 body = %s (%v), want the verification failure message", w.Body.String(), err)
				}
				captured := logs.String()
				if !strings.Contains(captured, "publish metadata verification failed") || !strings.Contains(captured, "transcript_id="+uuidFromPg(transcriptID).String()) {
					t.Fatalf("the log does not name the failure and the transcript id: %s", captured)
				}
				for _, leak := range []string{file.Sentinel, row.PublishMetadataKey.String, "AES-GCM"} {
					if strings.Contains(captured, leak) || strings.Contains(w.Body.String(), leak) {
						t.Fatalf("the 500 or its log carries %q: body %s log %s", leak, w.Body.String(), captured)
					}
				}
			case http.StatusNotAcceptable, http.StatusUnauthorized:
				if strings.Contains(w.Body.String(), file.Sentinel) {
					t.Fatalf("the refusal carries stored bytes: %s", w.Body.String())
				}
			default:
				t.Fatalf("the fixture names status %d, which this test does not check", c.WantStatus)
			}
		})
	}
}
