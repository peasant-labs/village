//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestGetUserByProviderIdentityResolvesByIdentityNotLogin proves the matcher's
// identity read resolves a webhook's numeric sender id to the user who signed
// in with it, and that nothing about the resolution depends on a login.
//
// A login can be renamed and re-used, so it is not identity. Sign-in stores the
// GitHub id as provider_user_id, and this query matches on the (provider, id)
// pair: a renamed login still resolves, a login string does not, and the same
// numeric id on a different provider is a different person.
func TestGetUserByProviderIdentityResolvesByIdentityNotLogin(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	queries := sqlc.New(pool)

	github, err := queries.UpsertUser(ctx, sqlc.UpsertUserParams{
		GithubID:         1001,
		GithubUsername:   "author",
		ProviderUsername: pgtype.Text{String: "author", Valid: true},
		DisplayName:      pgtype.Text{String: "Author", Valid: true},
		AvatarUrl:        pgtype.Text{String: "https://example.test/a.png", Valid: true},
	})
	if err != nil {
		t.Fatalf("seed github user: %v", err)
	}
	if github.Provider != "github" || github.ProviderUserID != "1001" {
		t.Fatalf("seeded user carries provider=%q id=%q, want github/1001", github.Provider, github.ProviderUserID)
	}

	// Another provider claims the SAME numeric id string: identity has to come
	// from the pair, never from the number alone.
	gitlab, err := queries.UpsertUserByProvider(ctx, sqlc.UpsertUserByProviderParams{
		GithubID:         5001,
		GithubUsername:   "gitlab-author",
		ProviderUsername: pgtype.Text{String: "gitlab-author", Valid: true},
		DisplayName:      pgtype.Text{String: "GitLab Author", Valid: true},
		AvatarUrl:        pgtype.Text{String: "https://example.test/g.png", Valid: true},
		Provider:         "gitlab",
		ProviderUserID:   "1001",
	})
	if err != nil {
		t.Fatalf("seed gitlab user: %v", err)
	}

	got, err := queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "github",
		ProviderUserID: "1001",
	})
	if err != nil {
		t.Fatalf("resolve github id 1001: %v", err)
	}
	if got.ID != github.ID {
		t.Fatalf("resolved user %v, want the github user %v", got.ID, github.ID)
	}

	otherProvider, err := queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "gitlab",
		ProviderUserID: "1001",
	})
	if err != nil {
		t.Fatalf("resolve gitlab id 1001: %v", err)
	}
	if otherProvider.ID != gitlab.ID || otherProvider.ID == github.ID {
		t.Fatalf("resolved %v for gitlab/1001, want the gitlab user %v and not the github one %v",
			otherProvider.ID, gitlab.ID, github.ID)
	}

	// Renaming the login changes nothing: the id is the identity.
	if _, err := pool.Exec(ctx, `UPDATE users SET github_username = $1 WHERE id = $2`, "renamed-author", github.ID); err != nil {
		t.Fatalf("rename login: %v", err)
	}
	got, err = queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "github",
		ProviderUserID: "1001",
	})
	if err != nil {
		t.Fatalf("resolve after rename: %v", err)
	}
	if got.ID != github.ID || got.GithubUsername != "renamed-author" {
		t.Fatalf("after rename resolved %v (%q), want the same user with the new login", got.ID, got.GithubUsername)
	}

	// A login string is not a provider identity: it resolves nobody.
	if _, err := queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "github",
		ProviderUserID: "renamed-author",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("resolving by login returned err=%v, want pgx.ErrNoRows", err)
	}

	// Someone who never signed in resolves nobody, rather than a default.
	if _, err := queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "github",
		ProviderUserID: "9999",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("resolving an unknown id returned err=%v, want pgx.ErrNoRows", err)
	}
}
