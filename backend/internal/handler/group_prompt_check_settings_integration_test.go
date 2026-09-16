//go:build integration

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestUpdateGroupAppliesPromptCheckSettings proves the two settings the served
// contract declares are actually written: setting them persists, an omitted
// field keeps its stored value, and a mode outside the closed menu is refused
// before any write. Until this path existed, the contract said they were
// settable and the server answered 200 while dropping them.
func TestUpdateGroupAppliesPromptCheckSettings(t *testing.T) {
	ctx := context.Background()
	pool := publishLockPool(t, 4)
	owner := pullInsertUser(t, ctx, pool, 208001, "group-check-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	var groupID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by) VALUES ('check-settings', $1) RETURNING id
	`, owner).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'owner')
	`, groupID, owner); err != nil {
		t.Fatalf("insert owner membership: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", groupID); err != nil {
			t.Errorf("cleanup group: %v", err)
		}
	}()

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	target := "/api/v1/groups/" + uuid.UUID(groupID.Bytes).String()
	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := chi.NewRouter()
		r.Patch("/api/v1/groups/{id}", h.UpdateGroup)
		req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "group-check-owner"}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	settings := func() (bool, string) {
		t.Helper()
		var post bool
		var mode string
		if err := pool.QueryRow(ctx, "SELECT post_prompts_check, prompts_check_mode FROM groups WHERE id = $1", groupID).Scan(&post, &mode); err != nil {
			t.Fatalf("read settings: %v", err)
		}
		return post, mode
	}

	// Both fields set together.
	if rec := patch(`{"post_prompts_check":false,"prompts_check_mode":"required"}`); rec.Code != http.StatusOK {
		t.Fatalf("set both = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if post, mode := settings(); post || mode != "required" {
		t.Fatalf("stored settings = post=%v mode=%q, want false/required", post, mode)
	}

	// Omitting both preserves what is stored.
	if rec := patch(`{"name":"check-settings-renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename only = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if post, mode := settings(); post || mode != "required" {
		t.Fatalf("an omitted field was not preserved: post=%v mode=%q", post, mode)
	}

	// Setting only the flag preserves the mode.
	if rec := patch(`{"post_prompts_check":true}`); rec.Code != http.StatusOK {
		t.Fatalf("set flag only = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if post, mode := settings(); !post || mode != "required" {
		t.Fatalf("flag-only update = post=%v mode=%q, want true/required", post, mode)
	}

	// A mode outside the menu is refused before any write.
	if rec := patch(`{"prompts_check_mode":"sometimes"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode = %d (%s), want 400", rec.Code, rec.Body.String())
	}
	if post, mode := settings(); !post || mode != "required" {
		t.Fatalf("a refused update changed the row: post=%v mode=%q", post, mode)
	}
}
