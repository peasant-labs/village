package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/middleware"
)

//go:embed testdata/content-hash-header.yaml
var contentHashHeaderYAML []byte

type contentHashHeaderCase struct {
	Name               string   `yaml:"name"`
	Why                string   `yaml:"why"`
	Stored             string   `yaml:"stored"`
	Identity           string   `yaml:"identity"`
	CAS                string   `yaml:"cas"`
	Viewers            []string `yaml:"viewers"`
	OwnerAnonymous     bool     `yaml:"owner_anonymous"`
	WantHeader         string   `yaml:"want_header"`
	WantNudges         int      `yaml:"want_nudges"`
	WantRewriteRecords bool     `yaml:"want_rewrite_records"`
	CORS               bool     `yaml:"cors"`
}

var requiredContentHashHeaderCases = []string{
	"envelope_sends_its_hash",
	"pending_identity_sends_none",
	"rewrite_moves_the_header_and_queues_the_new_hash",
	"lost_rewrite_race_sends_the_read_hash",
	"anonymous_owner_sends_the_same_hash",
	"no_etag_on_any_path",
	"cors_exposes_the_header",
}

func loadContentHashHeaderCases(t *testing.T) []contentHashHeaderCase {
	t.Helper()
	cases, err := decodeFixtureRows[contentHashHeaderCase](contentHashHeaderYAML)
	if err != nil {
		t.Fatalf("load the content-hash header corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || present[c.Name] || len(c.Viewers) == 0 {
			t.Fatalf("content-hash header case %+v must carry a unique name, a reason and at least one viewer", c)
		}
		present[c.Name] = true
	}
	for _, name := range requiredContentHashHeaderCases {
		if !present[name] {
			t.Fatalf("the content-hash header corpus no longer contains %q; restore it", name)
		}
	}
	return cases
}

// canonicalStoredContent is a generation migrate-on-read serves as stored: the
// canonical encoding of the legacy fixture after its one migration.
func canonicalStoredContent(t *testing.T) []byte {
	t.Helper()
	payload, rewrite, err := defaultContentMigrator.Migrate(context.Background(), legacyBarePayloadJSON("claude"))
	if err != nil || !rewrite {
		t.Fatalf("migrate the legacy fixture: rewrite=%v err=%v", rewrite, err)
	}
	encoded, err := encodeCanonicalTranscript(payload)
	if err != nil {
		t.Fatalf("encode the canonical fixture: %v", err)
	}
	return encoded
}

func TestContentHashHeader(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	canonical := canonicalStoredContent(t)
	for _, c := range loadContentHashHeaderCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			var stored []byte
			switch c.Stored {
			case "canonical":
				stored = canonical
			case "legacy":
				stored = legacyBarePayloadJSON("claude")
			default:
				t.Fatalf("unknown stored kind %q", c.Stored)
			}
			readHash := schema.ComputeTranscriptHash(stored)
			owner, other, id := uuid.New(), uuid.New(), uuid.New()

			store := newFakeBlobStore()
			store.put(key, stored)
			q := publicTranscriptQuerier(key)
			base := q.getTranscriptByID
			q.getTranscriptByID = func(ctx context.Context, tid pgtype.UUID) (sqlc.Transcript, error) {
				row, err := base(ctx, tid)
				row.OwnerID = pgUUIDFrom(owner)
				switch c.Identity {
				case "known":
					row.ContentHash = pgtype.Text{String: readHash, Valid: true}
					row.BlobSizeBytes = pgtype.Int8{Int64: int64(len(stored)), Valid: true}
				case "pending":
				default:
					t.Fatalf("unknown identity %q", c.Identity)
				}
				return row, err
			}
			q.getUserByID = func(_ context.Context, uid pgtype.UUID) (sqlc.User, error) {
				return sqlc.User{ID: uid, GithubUsername: "header-owner", IsDiscoverable: !c.OwnerAnonymous}, nil
			}
			var casCalls, replaceCalls, recordCalls int
			q.compareAndSwapTranscriptBlob = func(_ context.Context, arg sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error) {
				casCalls++
				if c.CAS == "lost" {
					return sqlc.Transcript{}, pgx.ErrNoRows
				}
				return sqlc.Transcript{ID: arg.ID}, nil
			}
			q.replaceTranscriptPublishMetadataObject = func(context.Context, sqlc.ReplaceTranscriptPublishMetadataObjectParams) (int64, error) {
				replaceCalls++
				return 1, nil
			}
			q.recordTranscriptContentRewrite = func(_ context.Context, arg sqlc.RecordTranscriptContentRewriteParams) error {
				recordCalls++
				if arg.ReplacedContentHash != readHash {
					t.Errorf("rewrite record replaced hash %q, want the read hash %q", arg.ReplacedContentHash, readHash)
				}
				return nil
			}
			h := newTestHandler(q, store)
			nudges := 0
			h.onIntegrityNudge = func() { nudges++ }

			var routes http.Handler = http.HandlerFunc(h.GetTranscriptContent)
			if c.CORS {
				r := chi.NewRouter()
				r.Use(middleware.CORS(minimalConfig().FrontendURL))
				r.Get("/api/v1/transcripts/{id}/content", h.GetTranscriptContent)
				routes = r
				preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/transcripts/"+id.String()+"/content", nil)
				preflight.Header.Set("Origin", minimalConfig().FrontendURL)
				preflightResponse := httptest.NewRecorder()
				routes.ServeHTTP(preflightResponse, preflight)
				assertExposesContentHash(t, "OPTIONS", preflightResponse)
			}

			for _, viewer := range c.Viewers {
				nudges, casCalls, replaceCalls, recordCalls = 0, 0, 0, 0
				store.put(key, stored)
				r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil)
				if !c.CORS {
					r = withChiURLParam(r, "id", id.String())
				}
				switch viewer {
				case "owner":
					r = r.WithContext(withUserID(r.Context(), owner))
				case "other":
					r = r.WithContext(withUserID(r.Context(), other))
				case "signed_out":
				default:
					t.Fatalf("unknown viewer %q", viewer)
				}
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d, want 200 (%s): %s", viewer, w.Code, c.Why, w.Body.String())
				}
				got, sent := w.Header()[http.CanonicalHeaderKey(contentHashHeader)], false
				if len(got) > 0 {
					sent = true
				}
				switch c.WantHeader {
				case "none":
					if sent {
						t.Fatalf("%s: %s = %v, want no header (%s)", viewer, contentHashHeader, got, c.Why)
					}
				case "read":
					if !sent || len(got) != 1 || got[0] != readHash {
						t.Fatalf("%s: %s = %v, want the read hash %q (%s)", viewer, contentHashHeader, got, readHash, c.Why)
					}
				case "installed":
					installed := schema.ComputeTranscriptHash(store.lastBody)
					if store.uploadCount() == 0 || installed == readHash {
						t.Fatalf("%s: no distinct rewrite was installed", viewer)
					}
					if !sent || len(got) != 1 || got[0] != installed {
						t.Fatalf("%s: %s = %v, want the installed hash %q (%s)", viewer, contentHashHeader, got, installed, c.Why)
					}
				default:
					t.Fatalf("unknown want_header %q", c.WantHeader)
				}
				if etag := w.Header().Get("ETag"); etag != "" {
					t.Fatalf("%s: ETag %q on the browser content route, want none", viewer, etag)
				}
				if nudges != c.WantNudges {
					t.Fatalf("%s: integrity nudges = %d, want %d (%s)", viewer, nudges, c.WantNudges, c.Why)
				}
				if c.Stored == "legacy" {
					if casCalls != 1 {
						t.Fatalf("%s: compare-and-swap calls = %d, want 1 inside the transaction", viewer, casCalls)
					}
					wantRecords := 0
					if c.WantRewriteRecords {
						wantRecords = 1
					}
					if recordCalls != wantRecords {
						t.Fatalf("%s: rewrite records = %d, want %d (%s)", viewer, recordCalls, wantRecords, c.Why)
					}
					// The row has no metadata object, so nothing replaces one.
					if replaceCalls != 0 {
						t.Fatalf("%s: metadata replacements = %d for a row without metadata", viewer, replaceCalls)
					}
				} else if casCalls != 0 || recordCalls != 0 {
					t.Fatalf("%s: a canonical generation was rewritten (cas=%d records=%d)", viewer, casCalls, recordCalls)
				}
				if c.CORS {
					assertExposesContentHash(t, "GET", w)
				}
			}
		})
	}
}

func assertExposesContentHash(t *testing.T, method string, w *httptest.ResponseRecorder) {
	t.Helper()
	exposed := w.Header().Get("Access-Control-Expose-Headers")
	for _, name := range strings.Split(exposed, ",") {
		if strings.EqualFold(strings.TrimSpace(name), contentHashHeader) {
			return
		}
	}
	t.Fatalf("%s: Access-Control-Expose-Headers = %q, want it to contain %s", method, exposed, contentHashHeader)
}
