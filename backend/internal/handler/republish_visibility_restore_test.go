package handler

import (
	"context"
	_ "embed"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/republish-visibility-restore.yaml
var republishVisibilityRestoreYAML []byte

type republishVisibilityRestoreCase struct {
	Name         string `yaml:"name"`
	NarrowedFrom string `yaml:"narrowed_from"`
	Locked       string `yaml:"locked"`
	WantPinned   string `yaml:"want_pinned"`
	WantRestored string `yaml:"want_restored"`
}

// requiredRepublishVisibilityRestoreCases guards the two rows only this corpus
// reaches: a later decision found under the lock must stand, in either tier.
var requiredRepublishVisibilityRestoreCases = []string{
	"nothing_narrowed_keeps_private",
	"nothing_narrowed_keeps_a_value_it_did_not_touch",
	"narrowed_shared_is_restored",
	"narrowed_public_is_restored",
	"a_later_public_decision_stands",
	"a_later_shared_decision_stands",
}

func loadRepublishVisibilityRestoreCases(t *testing.T) []republishVisibilityRestoreCase {
	t.Helper()
	cases, err := decodeFixtureRows[republishVisibilityRestoreCase](republishVisibilityRestoreYAML)
	if err != nil {
		t.Fatalf("load the republish visibility restore fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, required := range requiredRepublishVisibilityRestoreCases {
		if !present[required] {
			t.Fatalf("the republish visibility restore fixture no longer contains %q; restore it rather than removing it from this manifest", required)
		}
	}
	return cases
}

// TestRepublishVisibilityRestoreFollowsTheLockedRow runs both writers of the
// restore, the receipt's pin and the compensating transaction, against the
// visibility the row lock returns.
func TestRepublishVisibilityRestoreFollowsTheLockedRow(t *testing.T) {
	for _, c := range loadRepublishVisibilityRestoreCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			var written []string
			q := &mockQuerier{
				getTranscriptGovernanceForUpdate: func(context.Context, pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
					return sqlc.GetTranscriptGovernanceForUpdateRow{Visibility: c.Locked}, nil
				},
				updateTranscriptMetadata: func(_ context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error) {
					written = append(written, arg.Visibility)
					return sqlc.Transcript{Visibility: arg.Visibility}, nil
				},
			}

			params := sqlc.UpdateTranscriptByOwnerAndLocalIDParams{}
			if err := pinRepublishGovernance(context.Background(), q, pgtype.UUID{}, &params, c.NarrowedFrom); err != nil {
				t.Fatalf("pin the republish governance: %v", err)
			}
			if params.Visibility != c.WantPinned {
				t.Errorf("the receipt carries visibility %q, want %q", params.Visibility, c.WantPinned)
			}

			if err := restoreNarrowedVisibility(context.Background(), q, pgtype.UUID{}, c.NarrowedFrom); err != nil {
				t.Fatalf("compensate the narrowing: %v", err)
			}
			switch {
			case c.WantRestored == "" && len(written) != 0:
				t.Errorf("the compensation wrote %v, want no write: the row holds a value the narrowing did not leave", written)
			case c.WantRestored != "" && (len(written) != 1 || written[0] != c.WantRestored):
				t.Errorf("the compensation wrote %v, want exactly [%s]", written, c.WantRestored)
			}
		})
	}
}
