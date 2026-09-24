//go:build integration

package handler

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
)

// TestGitHubInstallCallbackBindsTheCollective proves the handshake binds an
// unlinked collective to the installation's account against a real database,
// and that binding it leaves every other setting alone. The linked org is
// written on its own, so the binding cannot silently change what the collective
// accepts, who may read it, or whether its pull requests get a prompts check.
func TestGitHubInstallCallbackBindsTheCollective(t *testing.T) {
	ctx := context.Background()
	pool := publishLockPool(t, 4)
	owner := pullInsertUser(t, ctx, pool, 208101, "install-bind-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	// Every setting the binding must not touch carries a value that differs from
	// its column default, so a write that clobbered them would be visible.
	var groupID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode, data_access, display_members,
		                    transcript_deletion_policy, post_prompts_check, prompts_check_mode)
		VALUES ('install-bind', $1, 'curated', 'public', false, 'mandatory', false, 'required')
		RETURNING id
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

	f := &fakeGitHub{installationID: 777, installationAccount: "acme"}
	newFakeGitHub(t, f)
	client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)}, gh.WithBaseURL(f.srv.URL))
	if err != nil {
		t.Fatalf("gh.NewClient: %v", err)
	}
	h := newTestHandler(sqlc.New(pool), nil)
	h.pool = pool
	h.gh = client
	h.cfg.JWTSecret = installTestSecret
	h.cfg.FrontendURL = "https://app.example.com"

	userID := uuid.UUID(owner.Bytes)
	group := uuid.UUID(groupID.Bytes).String()
	state, err := auth.CreateInstallState(installTestSecret, userID.String(), group)
	if err != nil {
		t.Fatal(err)
	}

	w := installRequest(h, "/integrations/github/callback?installation_id=777&state="+url.QueryEscape(state), userID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body: %s)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups/"+group+"/settings") {
		t.Fatalf("redirect = %q, want the bound collective's settings page", loc)
	}

	var linked pgtype.Text
	var acceptance, dataAccess, deletion, mode string
	var displayMembers, postCheck bool
	if err := pool.QueryRow(ctx, `
		SELECT linked_github_org, acceptance_mode, data_access, display_members,
		       transcript_deletion_policy, post_prompts_check, prompts_check_mode
		FROM groups WHERE id = $1
	`, groupID).Scan(&linked, &acceptance, &dataAccess, &displayMembers, &deletion, &postCheck, &mode); err != nil {
		t.Fatalf("read group: %v", err)
	}
	if !linked.Valid || linked.String != "acme" {
		t.Fatalf("linked org = %+v, want acme", linked)
	}
	if acceptance != "curated" || dataAccess != "public" || displayMembers || deletion != "mandatory" || postCheck || mode != "required" {
		t.Fatalf("binding changed the collective's other settings: acceptance=%q access=%q members=%v deletion=%q check=%v mode=%q",
			acceptance, dataAccess, displayMembers, deletion, postCheck, mode)
	}
}
