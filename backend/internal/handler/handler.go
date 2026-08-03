package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/github"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type TitlePipeline interface {
	Generate(string, redact.TitleContext) (redact.TitleResult, error)
	Sanitize(string, redact.TitleContext) (redact.TitleResult, error)
}

type Handler struct {
	cfg     *config.Config
	pool    *pgxpool.Pool
	queries Querier
	blobs   storage.TranscriptBlobStore
	titles  TitlePipeline

	// gh is the GitHub App client backing the collective-repository feature.
	// It is nil when the App is not configured; handlers detect this via
	// githubClient() and respond 501. Tests inject a client pointed at httptest.
	gh *github.Client
}

// New consumes the single transcript store composed by the runtime. Callers
// that cannot serve transcript bodies pass nil; serving runtime wiring supplies
// exactly one shared instance.
func New(cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore) *Handler {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		panic("construct title safety pipeline: " + err.Error())
	}
	return NewWithTitlePipeline(cfg, pool, blobs, titles)
}

func NewWithTitlePipeline(cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles TitlePipeline) *Handler {
	h := &Handler{
		cfg:     cfg,
		pool:    pool,
		queries: sqlc.New(pool),
		blobs:   blobs,
		titles:  titles,
	}

	// The GitHub App is optional. If credentials are absent (or invalid),
	// gh stays nil and the collective-repository endpoints return 501. We log
	// the outcome once at startup rather than failing the whole server.
	gh, err := github.NewClient(github.Config{
		AppID:         cfg.GitHubAppID,
		PrivateKeyPEM: cfg.GitHubAppPrivateKey,
	})
	switch {
	case errors.Is(err, github.ErrNotConfigured):
		log.Println("GitHub App not configured; collective-repository endpoints disabled (set GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY to enable)")
	case err != nil:
		log.Printf("GitHub App config present but invalid; collective-repository endpoints disabled: %v", err)
	default:
		h.gh = gh
		log.Println("GitHub App configured; collective-repository endpoints enabled")
	}

	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func toPgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func toPgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

func toPgTextPtr(s *string) pgtype.Text {
	if s == nil || *s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func toPgTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func toPgInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func toPgInt8(v int64) pgtype.Int8 {
	return pgtype.Int8{Int64: v, Valid: true}
}

func toPgBool(b *bool) pgtype.Bool {
	if b == nil {
		return pgtype.Bool{Valid: false}
	}
	return pgtype.Bool{Bool: *b, Valid: true}
}

func unixMsToTimestamptz(ms *int64) pgtype.Timestamptz {
	if ms == nil || *ms == 0 {
		return pgtype.Timestamptz{Valid: false}
	}
	t := time.UnixMilli(*ms)
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func toPgInt4FromIntPtr(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func toPgInt8FromIntPtr(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{Valid: false}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func toPgFloat4FromFloat64Ptr(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{Valid: false}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

func toPgFloat8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{Valid: false}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

func toPgInt4FromInt(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}
