//go:build integration

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
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/invalid-attempt-emission.yaml
var invalidAttemptEmissionYAML []byte

type invalidAttemptEmissionCase struct {
	Name     string `yaml:"name"`
	Recorded string `yaml:"recorded"`
	Decided  string `yaml:"decided"`
}

// The database permits historical values broader than the wire contract.
// Exercise the real mounted owner-history exit: a shared validator must refuse
// invalid stored evidence before the server writes a successful JSON array.
func TestInvalidStoredAttemptEmissionFailsClosed(t *testing.T) {
	cases, err := decodeFixtureRows[invalidAttemptEmissionCase](invalidAttemptEmissionYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"zero-recorded-at-refuses-history-emission": false, "pending-decision-refuses-history-emission": false}
	for _, c := range cases {
		if _, ok := required[c.Name]; !ok || required[c.Name] {
			t.Fatalf("unknown/repeated invalid attempt case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing invalid attempt case %q", name)
		}
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	const secret = "disposable-invalid-attempt-secret"
	h := New(&config.Config{JWTSecret: secret}, pool, nil)
	r := chi.NewRouter()
	r.With(h.AuthRequired).Get("/api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events", h.ListShareEventHistory)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newShareWorld(t, ctx, pool, c.Name, "curated")
			defer w.cleanup(t, ctx)
			if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts(transcript_id,group_id,event_num,status,recorded_at,decided_at) VALUES($1,$2,1,'pending',$3::text::timestamptz,NULLIF($4::text,'')::timestamptz)`, w.transcript, w.group, c.Recorded, c.Decided); err != nil {
				t.Fatal(err)
			}
			token, err := auth.CreateToken(secret, uuid.UUID(w.owner.Bytes), w.ownerName)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/collectives/"+uuid.UUID(w.group.Bytes).String()+"/transcripts/"+uuid.UUID(w.transcript.Bytes).String()+"/events", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "history contract") {
				t.Fatalf("invalid stored attempt emitted as %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
