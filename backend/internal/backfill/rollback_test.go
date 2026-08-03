package backfill

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type rollbackProbe struct {
	pgx.Tx
	err         error
	hadDeadline bool
	remaining   time.Duration
}

func (p *rollbackProbe) Rollback(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	p.hadDeadline = ok
	p.remaining = time.Until(deadline)
	return p.err
}

func TestRollbackMaintenanceTransactionBoundsAndReportsFailure(t *testing.T) {
	probe := &rollbackProbe{err: errors.New("injected rollback failure")}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(previous)

	rollbackMaintenanceTransaction(probe, "content_identity_install")

	if !probe.hadDeadline || probe.remaining <= 0 || probe.remaining > maintenanceRollbackTimeout {
		t.Fatalf("rollback deadline remaining=%s, want within (0,%s]", probe.remaining, maintenanceRollbackTimeout)
	}
	logged := output.String()
	if !strings.Contains(logged, "operation=content_identity_install") ||
		!strings.Contains(logged, "stage=rollback") ||
		!strings.Contains(logged, "consequence=") ||
		!strings.Contains(logged, "remediation=") ||
		!strings.Contains(logged, "injected rollback failure") {
		t.Fatalf("cleanup evidence is incomplete: %q", logged)
	}
}

func TestRollbackMaintenanceTransactionIgnoresClosedTransaction(t *testing.T) {
	probe := &rollbackProbe{err: pgx.ErrTxClosed}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(previous)

	rollbackMaintenanceTransaction(probe, "wrapped_data_key_install")

	if output.Len() != 0 {
		t.Fatalf("closed transaction emitted cleanup evidence: %q", output.String())
	}
}
