package handler

import (
	"context"
	_ "embed"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/attachment-actor-lookup.yaml
var attachmentActorLookupYAML []byte

type attachmentActorCase struct {
	Name     string `yaml:"name"`
	Outcome  string `yaml:"outcome"`
	Resolved bool   `yaml:"resolved"`
	Error    bool   `yaml:"error"`
}

func TestAttachmentActorLookupPreservesDatabaseFailures(t *testing.T) {
	rows, err := decodeFixtureRows[attachmentActorCase](attachmentActorLookupYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, row := range rows {
		if _, ok := present[row.Name]; ok || row.Name == "" {
			t.Fatal("invalid actor lookup fixture name")
		}
		present[row.Name] = struct{}{}
		if !containsString([]string{"known", "missing", "failed"}, row.Outcome) {
			t.Fatal("invalid actor lookup outcome")
		}
	}
	assertExactTitleFixtureNames(t, "attachment-actor-lookup", present, []string{"a_known_account_resolves", "an_unknown_account_is_not_a_lookup_failure", "a_database_failure_is_reported_for_retry"})
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			queryErr := errors.New("fixture database failure")
			owner := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
			q := &mockQuerier{getUserByProviderIdentity: func(ctx context.Context, arg sqlc.GetUserByProviderIdentityParams) (sqlc.User, error) {
				if arg.Provider != "github" || arg.ProviderUserID != "123" {
					t.Fatalf("wrong provider lookup: %+v", arg)
				}
				switch row.Outcome {
				case "missing":
					return sqlc.User{}, pgx.ErrNoRows
				case "failed":
					return sqlc.User{}, queryErr
				default:
					return sqlc.User{ID: owner}, nil
				}
			}}
			h := &Handler{queries: q}
			id, resolved, err := h.resolveGitHubActor(context.Background(), 123)
			if resolved != row.Resolved || (err != nil) != row.Error || (resolved && id != owner) {
				t.Fatalf("lookup=(%v,%v,%v)", id, resolved, err)
			}
			if row.Error && !errors.Is(err, queryErr) {
				t.Fatal("lookup lost the retryable database error")
			}
			_, authorErr := h.userByGitHubID(context.Background(), 123)
			if row.Outcome == "missing" && (authorErr == nil || errors.Is(authorErr, pgx.ErrNoRows)) {
				t.Fatal("unknown author did not report the missing Village account")
			}
			if row.Error && !errors.Is(authorErr, queryErr) {
				t.Fatal("author lookup replaced a database error with unknown-account copy")
			}
		})
	}
}
