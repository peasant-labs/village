package backfill

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const maintenanceRollbackTimeout = 5 * time.Second

func rollbackMaintenanceTransaction(tx pgx.Tx, operation string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), maintenanceRollbackTimeout)
	defer cancel()
	if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("maintenance transaction cleanup failed",
			"operation", operation,
			"stage", "rollback",
			"consequence", "the PostgreSQL connection may be discarded instead of reused, while the maintenance result remains unchanged",
			"remediation", "inspect PostgreSQL connectivity and transaction health before rerunning the bounded maintenance operation",
			"error", err)
	}
}
