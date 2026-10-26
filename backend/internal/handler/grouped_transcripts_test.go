package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

// groupedProjectionRow is one stored transcript with every field the grouped
// projection reads. DiagnosticsWarnings carries the structured entries the
// publish contract writes, which are the bytes that broke the string-typed
// projection.
func groupedProjectionRow(id uuid.UUID, warnings string) sqlc.Transcript {
	now := time.Date(2026, 10, 24, 6, 47, 34, 0, time.UTC)
	return sqlc.Transcript{
		ID:                   toPgUUID(id),
		OwnerID:              toPgUUID(uuid.MustParse("c7649a34-b842-4653-856e-5f62c1f587a6")),
		LocalID:              "ses_grouped_projection",
		Visibility:           "shared",
		ModelProvider:        "opencode",
		ModelName:            pgText("deepseek-flash"),
		SchemaVersion:        "12",
		PublishedAt:          pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:            pgtype.Timestamptz{Time: now, Valid: true},
		ProjectHash:          strings.Repeat("a", 64),
		DiagnosticsWarnings:  []byte(warnings),
		DiagnosticsPartial:   pgtype.Bool{Bool: true, Valid: true},
		SessionOrigin:        "user",
		SessionRelationships: []byte("[]"),
	}
}

func TestGroupedSessionFromTranscriptProjectsStoredDiagnosticEntries(t *testing.T) {
	const stored = `[{"errorType":"unknown_data_retained","location":"selected session entry","message":"24 control message rows are outside the interpreted vocabulary","remediation":"No action is required"}]`
	id := uuid.MustParse("17dab359-74c3-4181-a19d-6cf094cc878d")

	session, err := groupedSessionFromTranscript(groupedProjectionRow(id, stored), projectname.Resolved{
		DisplayName: "photocraft", Source: projectname.NameSourceRemote, RemoteLabel: "dayvidpham/photocraft",
	})
	if err != nil {
		t.Fatalf("a stored diagnostic entry must project into the canonical session; got %v", err)
	}
	if session.DiagnosticsWarnings == nil || len(*session.DiagnosticsWarnings) != 1 {
		t.Fatalf("diagnostics warnings were lost during grouped projection; got %v", session.DiagnosticsWarnings)
	}
	warning := (*session.DiagnosticsWarnings)[0]
	if warning.ErrorType != "unknown_data_retained" || warning.Location != "selected session entry" ||
		!strings.Contains(warning.Message, "24 control message rows") || warning.Remediation != "No action is required" {
		t.Fatalf("a stored diagnostic entry was altered during grouped projection; got %+v", warning)
	}
	owner := schema.NewVillagePublicProfile("17dab359-74c3-4181-a19d-6cf094cc8700", "photocraft-owner", nil, nil)
	if err := (schema.VillageSessionRow{Session: session, Owner: owner}).Validate(); err != nil {
		t.Fatalf("the projected session must satisfy the canonical row shape: %v", err)
	}
}

func TestGroupedSessionFromTranscriptNamesTheUnprojectableTranscript(t *testing.T) {
	id := uuid.MustParse("17dab359-74c3-4181-a19d-6cf094cc878d")
	row := groupedProjectionRow(id, "[]")
	row.SessionRelationships = []byte(`{"not":"an array"}`)

	_, err := groupedSessionFromTranscript(row, projectname.Resolved{})
	if err == nil {
		t.Fatal("a stored relationship that is not an array must fail the projection")
	}
	if !strings.Contains(err.Error(), id.String()) {
		t.Fatalf("the projection failure must name the transcript that could not be projected; got %v", err)
	}
}
