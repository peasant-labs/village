//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/provider_identity.yaml
var providerIdentityYAML []byte

// requiredProviderIdentityCaseNames is the name manifest for the identity
// fixture. Every case pins an arm of the resolution rule, including the
// non-GitHub-provider arm, so losing one must name itself.
var requiredProviderIdentityCaseNames = []string{
	"github-id-resolves-the-signed-in-user",
	"login-string-is-not-a-provider-identity",
	"non-github-provider-user-does-not-resolve-a-github-lookup",
	"renamed-login-still-resolves-by-id",
	"same-numeric-id-on-two-providers-is-two-people",
	"unknown-id-resolves-nobody",
}

type identitySeedFixture struct {
	Provider       string `yaml:"provider"`
	ProviderUserID string `yaml:"provider_user_id"`
	GithubID       int64  `yaml:"github_id"`
	Username       string `yaml:"username"`
	RenameTo       string `yaml:"rename_to"`
	// SignIn seeds through the GitHub sign-in writer instead of the explicit
	// provider writer, which is what proves the stored identity's shape.
	SignIn bool `yaml:"sign_in"`
}

type identityLookupFixture struct {
	Provider       string `yaml:"provider"`
	ProviderUserID string `yaml:"provider_user_id"`
	Expect         string `yaml:"expect"`
	ExpectUsername string `yaml:"expect_username"`
}

type identityCaseFixture struct {
	Name    string                  `yaml:"name"`
	Seed    []identitySeedFixture   `yaml:"seed"`
	Lookups []identityLookupFixture `yaml:"lookups"`
}

type identityFixture struct {
	Cases []identityCaseFixture `yaml:"cases"`
}

func loadProviderIdentityFixture(t *testing.T) identityFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(providerIdentityYAML))
	decoder.KnownFields(true)
	var fixture identityFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode strict provider identity fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("provider identity fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("provider identity fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("provider identity fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	declared := map[string]bool{}
	for _, name := range requiredProviderIdentityCaseNames {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range seen {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/provider_identity.yaml no longer carries %v, which its manifest declares: each case "+
			"guards an arm of identity resolution. Restore the row under its exact name.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/provider_identity.yaml carries %v, which its manifest does not declare: an undeclared "+
			"case is unprotected, so add each new name to the manifest in the same change.", undeclared)
	}
	return fixture
}

// TestGetUserByProviderIdentity proves the matcher's identity read against real
// PostgreSQL: it resolves by the (provider, provider_user_id) pair and never by
// login. A renamed login still resolves, a login string resolves nobody, a user
// who signed in through another provider does not answer a GitHub lookup, the
// same numeric id on two providers is two people, and an unknown id is
// ErrNoRows rather than a default.
func TestGetUserByProviderIdentity(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	queries := sqlc.New(pool)

	for _, tc := range loadProviderIdentityFixture(t).Cases {
		t.Run(tc.Name, func(t *testing.T) {
			seeded := seedProviderIdentities(t, ctx, queries, tc.Seed)

			for _, lookup := range tc.Lookups {
				got, err := queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
					Provider:       lookup.Provider,
					ProviderUserID: lookup.ProviderUserID,
				})
				switch lookup.Expect {
				case "not_found":
					if !errors.Is(err, pgx.ErrNoRows) {
						t.Fatalf("%s/%s resolved to %v (err %v), want pgx.ErrNoRows",
							lookup.Provider, lookup.ProviderUserID, got.ID, err)
					}
				case "found":
					if err != nil {
						t.Fatalf("%s/%s: %v", lookup.Provider, lookup.ProviderUserID, err)
					}
					want, ok := seeded[lookup.Provider+"/"+lookup.ProviderUserID]
					if !ok {
						t.Fatalf("%s/%s resolved but the case never seeded it", lookup.Provider, lookup.ProviderUserID)
					}
					if got.ID != want {
						t.Fatalf("%s/%s resolved %v, want the seeded user %v", lookup.Provider, lookup.ProviderUserID, got.ID, want)
					}
					if got.GithubUsername != lookup.ExpectUsername {
						t.Fatalf("%s/%s username = %q, want %q", lookup.Provider, lookup.ProviderUserID, got.GithubUsername, lookup.ExpectUsername)
					}
					if got.Provider != lookup.Provider || got.ProviderUserID != lookup.ProviderUserID {
						t.Fatalf("%s/%s resolved provider pair %s/%s", lookup.Provider, lookup.ProviderUserID, got.Provider, got.ProviderUserID)
					}
				default:
					t.Fatalf("case %q has an unknown expectation %q", tc.Name, lookup.Expect)
				}
			}
		})
	}
}

// seedProviderIdentities writes each seed through the same writer sign-in uses,
// keyed by the (provider, provider_user_id) pair, and returns the id per pair so
// a lookup can be checked against the exact row it should resolve.
func seedProviderIdentities(t *testing.T, ctx context.Context, queries *sqlc.Queries, seeds []identitySeedFixture) map[string]pgtype.UUID {
	t.Helper()
	seeded := map[string]pgtype.UUID{}
	for _, seed := range seeds {
		if seed.SignIn {
			user, err := queries.UpsertUser(ctx, sqlc.UpsertUserParams{
				GithubID:         seed.GithubID,
				GithubUsername:   seed.Username,
				ProviderUsername: pgtype.Text{String: seed.Username, Valid: true},
				DisplayName:      pgtype.Text{String: seed.Username, Valid: true},
				AvatarUrl:        pgtype.Text{String: "https://example.test/" + seed.Username + ".png", Valid: true},
			})
			if err != nil {
				t.Fatalf("sign in %s/%s: %v", seed.Provider, seed.ProviderUserID, err)
			}
			// The sign-in writer is what defines the stored identity the lookup
			// must match: provider 'github' and the numeric id as text.
			if user.Provider != "github" || user.ProviderUserID != seed.ProviderUserID {
				t.Fatalf("sign-in stored %s/%s, want github/%s", user.Provider, user.ProviderUserID, seed.ProviderUserID)
			}
			seeded[seed.Provider+"/"+seed.ProviderUserID] = user.ID
			continue
		}
		user, err := queries.UpsertUserByProvider(ctx, sqlc.UpsertUserByProviderParams{
			GithubID:         seed.GithubID,
			GithubUsername:   seed.Username,
			ProviderUsername: pgtype.Text{String: seed.Username, Valid: true},
			DisplayName:      pgtype.Text{String: seed.Username, Valid: true},
			AvatarUrl:        pgtype.Text{String: "https://example.test/" + seed.Username + ".png", Valid: true},
			Provider:         seed.Provider,
			ProviderUserID:   seed.ProviderUserID,
		})
		if err != nil {
			t.Fatalf("seed %s/%s: %v", seed.Provider, seed.ProviderUserID, err)
		}
		if seed.RenameTo != "" {
			if _, err := queries.SetUsername(ctx, sqlc.SetUsernameParams{ID: user.ID, GithubUsername: seed.RenameTo}); err != nil {
				t.Fatalf("rename %s/%s: %v", seed.Provider, seed.ProviderUserID, err)
			}
		}
		seeded[seed.Provider+"/"+seed.ProviderUserID] = user.ID
	}
	return seeded
}
