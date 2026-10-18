//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/org-generation-writers.yaml
var orgGenerationWritersYAML []byte

type orgGenerationWriter struct {
	Name      string `yaml:"name"`
	Operation string `yaml:"operation"`
}

func TestMigration045GitHubOrgGenerationUpgradeAndWriters(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	for _, m := range migrations {
		if m.version >= 45 {
			continue
		}
		if err := runMigration(pool, m); err != nil {
			t.Fatal(err)
		}
	}
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO users(github_id,github_username,provider_user_id) VALUES(945001,'org-generation-user','945001') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO user_github_orgs(user_id,org_login,org_id,visible) VALUES($1,'retained',1,true)", id); err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 45)); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := pool.QueryRow(ctx, "SELECT github_org_sync_generation FROM users WHERE id=$1", id).Scan(&generation); err != nil || generation != 0 {
		t.Fatalf("upgrade default %d %v", generation, err)
	}
	var newcomer pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO users(github_id,github_username,provider_user_id) VALUES(945002,'org-generation-new','945002') RETURNING id,github_org_sync_generation").Scan(&newcomer, &generation); err != nil || generation != 0 {
		t.Fatalf("new default %d %v", generation, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET github_org_sync_generation=-1 WHERE id=$1", id); err == nil {
		t.Fatal("negative generation accepted")
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET github_org_sync_generation=9223372036854775807 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(pool)
	if _, err := q.IssueGitHubOrgSyncGeneration(ctx, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("exhausted generation not refused: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET github_org_sync_generation=73 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	var cases []orgGenerationWriter
	decoder := yaml.NewDecoder(bytes.NewReader(orgGenerationWritersYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("writer fixture has extra YAML document: %v", err)
	}
	required := map[string]bool{"github-profile-upsert-keeps-generation": false, "provider-profile-upsert-keeps-generation": false, "username-change-keeps-generation": false, "handle-confirmation-keeps-generation": false, "discoverability-change-keeps-generation": false, "attachment-settings-change-keeps-generation": false}
	for _, c := range cases {
		if _, ok := required[c.Name]; !ok || required[c.Name] {
			t.Fatalf("unknown/repeated writer %q", c.Name)
		}
		required[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			var err error
			switch c.Operation {
			case "github":
				_, err = q.UpsertUser(ctx, sqlc.UpsertUserParams{GithubID: 945001, GithubUsername: "unused"})
			case "provider":
				_, err = q.UpsertUserByProvider(ctx, sqlc.UpsertUserByProviderParams{GithubID: 945001, GithubUsername: "unused", Provider: "github", ProviderUserID: "945001"})
			case "username":
				_, err = q.SetUsername(ctx, sqlc.SetUsernameParams{ID: id, GithubUsername: "org-generation-renamed"})
			case "confirm":
				if _, err = pool.Exec(ctx, "UPDATE users SET username_chosen=false WHERE id=$1", id); err == nil {
					_, err = q.ConfirmOwnHandle(ctx, sqlc.ConfirmOwnHandleParams{ID: id, GithubUsername: "org-generation-renamed"})
				}
			case "discoverability":
				_, err = q.UpdateUserDiscoverable(ctx, sqlc.UpdateUserDiscoverableParams{ID: id, IsDiscoverable: true})
			case "settings":
				_, err = q.UpdateUserAttachSettings(ctx, sqlc.UpdateUserAttachSettingsParams{ID: id, AutoAttachPullRequests: pgtype.Bool{Bool: true, Valid: true}})
			default:
				t.Fatalf("unknown writer %q", c.Operation)
			}
			if err != nil {
				t.Fatal(err)
			}
			row, err := q.GetUserByID(ctx, id)
			if err != nil || row.GithubOrgSyncGeneration != 73 {
				t.Fatalf("writer reset generation: %+v %v", row, err)
			}
		})
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing writer %q", name)
		}
	}
	if err := q.DeleteUser(ctx, newcomer); err != nil {
		t.Fatal(err)
	}
	if _, err := q.IssueGitHubOrgSyncGeneration(ctx, newcomer); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("deleted generation not refused: %v", err)
	}
	var missing pgtype.UUID
	missing.Valid = true
	missing.Bytes[0] = 99
	if _, err := q.IssueGitHubOrgSyncGeneration(ctx, missing); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing generation not refused: %v", err)
	}
	down, err := migrationsFS.ReadFile("migrations/045_github_org_sync_generation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='users' AND column_name='github_org_sync_generation')").Scan(&exists); err != nil || exists {
		t.Fatalf("down retained column: %v %v", exists, err)
	}
	var org string
	var visible bool
	if err := pool.QueryRow(ctx, "SELECT org_login,visible FROM user_github_orgs WHERE user_id=$1", id).Scan(&org, &visible); err != nil || org != "retained" || !visible {
		t.Fatalf("upgrade/down altered org membership: %s %v %v", org, visible, err)
	}
}
