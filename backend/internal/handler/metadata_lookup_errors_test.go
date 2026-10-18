package handler

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/metadata-lookup-errors.yaml
var metadataLookupErrorsYAML []byte

func TestMetadataLookupErrorsPreserveFailureAndPrivacy(t *testing.T) {
	cases, err := decodeFixtureRows[struct {
		Name    string `yaml:"name"`
		Route   string `yaml:"route"`
		Failure string `yaml:"failure"`
		Status  int    `yaml:"status"`
	}](metadataLookupErrorsYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"history-transient-is-failure":                false,
		"history-wrapped-transient-is-failure":        false,
		"history-cancellation-is-failure":             false,
		"history-absent-is-private-negative":          false,
		"history-wrapped-absence-is-private-negative": false,
		"history-nonowner-remains-private-negative":   false,
		"history-anonymous-remains-unauthorized":      false,
		"collective-transient-is-failure":             false,
		"collective-wrapped-transient-is-failure":     false,
		"collective-cancellation-is-failure":          false,
		"collective-absent-is-negative":               false,
		"collective-wrapped-absence-is-negative":      false,
	}
	for _, c := range cases {
		if seen, ok := required[c.Name]; !ok || seen {
			t.Fatalf("unknown/repeated metadata lookup fixture %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing required metadata lookup fixture %q", name)
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var lookupErr error
			switch c.Failure {
			case "transient":
				lookupErr = errors.New("private-query-identity must never be disclosed")
			case "wrapped-transient":
				lookupErr = fmt.Errorf("private-query-identity: %w", errors.New("connection unavailable"))
			case "canceled":
				lookupErr = context.Canceled
			case "absent":
				lookupErr = pgx.ErrNoRows
			case "wrapped-absent":
				lookupErr = fmt.Errorf("private-query-identity: %w", pgx.ErrNoRows)
			case "nonowner", "anonymous":
			default:
				t.Fatalf("unknown failure %q", c.Failure)
			}
			caller := uuid.New()
			lookups := 0
			q := &mockQuerier{
				getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
					lookups++
					return sqlc.Transcript{OwnerID: toPgUUID(uuid.New())}, lookupErr
				},
				getGroupByID: func(context.Context, pgtype.UUID) (sqlc.Group, error) { lookups++; return sqlc.Group{}, lookupErr },
			}
			h := newTestHandler(q, nil)
			token, err := auth.CreateToken(h.cfg.JWTSecret, caller, "lookup-reader")
			if err != nil {
				t.Fatal(err)
			}
			// The exact mounted production auth middleware and handlers are shared
			// by the canonical and deprecated resource families. Only SQL fails.
			r := chi.NewRouter()
			r.With(h.AuthRequired).Get("/api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events", h.ListShareEventHistory)
			r.With(h.AuthRequired).Get("/api/v1/users/me/groups/{groupId}/transcripts/{transcriptId}/events", h.ListShareEventHistory)
			r.With(h.AuthOptional).Get("/api/v1/collectives/{id}", h.GetGroup)
			r.With(h.AuthOptional).Get("/api/v1/groups/{id}", h.GetGroup)
			var firstBody string
			for _, family := range []string{"collectives", "groups"} {
				path := "/api/v1/" + family + "/" + caller.String()
				if c.Route == "history" {
					path = "/api/v1/users/me/" + family + "/" + caller.String() + "/transcripts/" + caller.String() + "/events"
				} else if c.Route != "collective" {
					t.Fatal("unknown route")
				}
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if c.Failure != "anonymous" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				if rec.Code != c.Status {
					t.Fatalf("%s lookup failure returned %d, want %d: %s", family, rec.Code, c.Status, rec.Body.String())
				}
				body := decodeError(t, rec.Body.Bytes())
				if strings.Contains(body, "private-query-identity") || strings.Contains(body, caller.String()) || strings.Contains(body, "context canceled") {
					t.Fatalf("lookup leaked private failure details: %s", body)
				}
				if c.Status == 500 && !strings.Contains(strings.ToLower(body), "retry") {
					t.Fatalf("failure must provide a safe corrective action: %s", body)
				}
				if firstBody != "" && firstBody != body {
					t.Fatalf("alias error differs: %q != %q", firstBody, body)
				}
				firstBody = body
			}
			if c.Failure == "anonymous" {
				if lookups != 0 {
					t.Fatal("anonymous history reached SQL")
				}
			} else if lookups != 2 {
				t.Fatalf("both actual alias handlers must reach failing query; got %d", lookups)
			}
		})
	}
}
