package backfill

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maximumReportedShareStateDrift bounds how many disagreeing rows one run
// names individually. The total is always reported; the sample exists so an
// operator has something concrete to look at without a corrupted projection
// producing a log line per row.
const maximumReportedShareStateDrift = 50

// ShareStateDriftRow is one way the stored projection disagrees with the
// ledger it is derived from.
type ShareStateDriftRow struct {
	TranscriptID   pgtype.UUID
	GroupID        pgtype.UUID
	Problem        string
	StoredStatus   pgtype.Text
	ExpectedStatus pgtype.Text
}

// ShareStateReport is the outcome of one consistency run. It is REPORT-ONLY:
// nothing in this file writes, so it is safe to run against production at any
// time. Repair is a separate, deliberate act (rebuild_transcript_shares()).
type ShareStateReport struct {
	ProjectionRows int64
	LedgerPairs    int64
	DriftRows      int64
	Sample         []ShareStateDriftRow
}

// Consistent reports whether the projection is exactly a latest-event fold
// over the whole ledger.
func (r ShareStateReport) Consistent() bool { return r.DriftRows == 0 }

// ShareStateConsistency compares transcript_shares against a latest-event fold
// over the WHOLE of transcript_share_attempts and reports every disagreement.
//
// The comparison itself lives in the database (the transcript_share_drift
// view), deliberately: a Go reimplementation of "latest event wins" would be a
// second definition that could drift from the trigger's, and then a green check
// would prove only that two Go functions agree with each other.
func ShareStateConsistency(ctx context.Context, pool *pgxpool.Pool) (ShareStateReport, error) {
	var report ShareStateReport

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_shares`).Scan(&report.ProjectionRows); err != nil {
		return report, fmt.Errorf("share-state consistency could not count the stored projection before comparing it with the ledger; nothing was read or changed; restore database access and rerun: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_share_expected_state`).Scan(&report.LedgerPairs); err != nil {
		return report, fmt.Errorf("share-state consistency could not fold the ledger into its expected projection; the stored projection was left untouched; confirm migration 036 applied (transcript_share_expected_state must exist) and rerun: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT check_transcript_shares_drift()`).Scan(&report.DriftRows); err != nil {
		return report, fmt.Errorf("share-state consistency could not evaluate the drift check; no repair was attempted; confirm migration 036 applied (check_transcript_shares_drift() must exist) and rerun: %w", err)
	}
	if report.DriftRows == 0 {
		return report, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT transcript_id, group_id, problem, stored_status, expected_status
		FROM transcript_share_drift
		ORDER BY problem, transcript_id, group_id
		LIMIT $1`, maximumReportedShareStateDrift)
	if err != nil {
		return report, fmt.Errorf("share-state consistency found %d disagreeing rows but could not read the sample naming them; the count stands and no repair was attempted; rerun to obtain the sample: %w", report.DriftRows, err)
	}
	defer rows.Close()
	for rows.Next() {
		var row ShareStateDriftRow
		if err := rows.Scan(&row.TranscriptID, &row.GroupID, &row.Problem, &row.StoredStatus, &row.ExpectedStatus); err != nil {
			return report, fmt.Errorf("share-state consistency could not decode a drift row while building its sample; the count of %d stands and no repair was attempted; rerun to obtain the sample: %w", report.DriftRows, err)
		}
		report.Sample = append(report.Sample, row)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("share-state consistency stopped reading its drift sample early; the count of %d stands and no repair was attempted; rerun to obtain the sample: %w", report.DriftRows, err)
	}
	return report, nil
}
